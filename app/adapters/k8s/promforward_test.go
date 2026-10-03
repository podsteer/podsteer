package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// meshRig is an API server whose service proxy is refused by the backend the
// way linkerd-viz's Prometheus refused it live (403, empty body, no Status),
// plus a "pod loopback" a port-forward lands on.
type meshRig struct {
	adapter  *Adapter
	proxied  atomic.Int32
	forwards atomic.Int32
	loopback atomic.Int32
}

func vizBackendForTest() domain.MetricsBackend {
	return domain.MetricsBackend{
		Kind: domain.MetricsBackendPrometheus, Namespace: "linkerd-viz",
		Service: "prometheus", Port: "admin", LinkerdViz: true,
	}
}

func newMeshRig(t *testing.T, dialErr error) *meshRig {
	t.Helper()
	rig := &meshRig{}

	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "prometheus-7d9", Namespace: "linkerd-viz", UID: "uid-1",
			Labels: map[string]string{"component": "prometheus"}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "prometheus",
			Ports: []corev1.ContainerPort{{Name: "admin", ContainerPort: 9090}}}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
	}
	service := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "prometheus", Namespace: "linkerd-viz"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"component": "prometheus"},
			Ports:    []corev1.ServicePort{{Name: "admin", Port: 9090, TargetPort: intstr.FromInt32(9090), Protocol: corev1.ProtocolTCP}},
		},
	}

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/proxy/"):
			rig.proxied.Add(1)
			w.WriteHeader(http.StatusForbidden)
		case strings.HasSuffix(r.URL.Path, "/services/prometheus"):
			_ = json.NewEncoder(w).Encode(service)
		case strings.HasSuffix(r.URL.Path, "/pods"):
			_ = json.NewEncoder(w).Encode(corev1.PodList{Items: []corev1.Pod{pod}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(api.Close)

	loopback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rig.loopback.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"%s"]}]}}`, r.URL.Query().Get("query")[:1])
	}))
	t.Cleanup(loopback.Close)
	_, portText, _ := net.SplitHostPort(loopback.Listener.Addr().String())
	loopbackPort, _ := strconv.Atoi(portText)

	typed, err := kubernetes.NewForConfig(&rest.Config{Host: api.URL})
	if err != nil {
		t.Fatal(err)
	}
	factory := newClientFactory(Config{})
	factory.clients["dev"] = &clients{typed: typed, queryHTTP: api.Client()}
	rig.adapter = &Adapter{
		factory:  factory,
		logger:   slog.New(slog.DiscardHandler),
		forwards: portForwards{byID: make(map[string]*forwarder)},
	}
	rig.adapter.forwards.dial = func(domain.ClusterID, domain.NamespaceName, string, int, int) (bool, int, attempt, error) {
		rig.forwards.Add(1)
		if dialErr != nil {
			return false, 0, attempt{}, dialErr
		}
		next := attempt{stop: make(chan struct{}), done: make(chan struct{}), failed: make(chan error, 1)}
		go func() {
			<-next.stop
			close(next.done)
		}()
		return true, loopbackPort, next, nil
	}
	return rig
}

// The live failure: the backend's own 403 to the proxy. The same GET goes
// out once more over an ephemeral port-forward, is answered, and the forward
// is gone when the call returns.
func TestABackendRefusingTheProxyIsQueriedThroughAnEphemeralForward(t *testing.T) {
	rig := newMeshRig(t, nil)

	series, err := rig.adapter.QueryInstant(context.Background(), "dev", vizBackendForTest(), "count(request_total)", time.Now())
	if err != nil {
		t.Fatalf("QueryInstant: %v", err)
	}
	if len(series) != 1 || rig.proxied.Load() != 1 || rig.forwards.Load() != 1 || rig.loopback.Load() != 1 {
		t.Fatalf("series %d, proxied %d, forwards %d, loopback %d", len(series), rig.proxied.Load(), rig.forwards.Load(), rig.loopback.Load())
	}

	rig.adapter.forwards.mu.Lock()
	left := len(rig.adapter.forwards.byID)
	rig.adapter.forwards.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d forwards left open after the query", left)
	}
}

// pods/portforward refused: said as exactly that, naming Linkerd's policy as
// the reason the proxy failed in the first place.
func TestARefusedPortForwardIsSaidAsThePermission(t *testing.T) {
	rig := newMeshRig(t, fmt.Errorf("error upgrading connection: %w: pods \"prometheus-7d9\" is forbidden: cannot create resource \"pods/portforward\"", ports.ErrForbidden))

	_, err := rig.adapter.QueryInstant(context.Background(), "dev", vizBackendForTest(), "count(request_total)", time.Now())
	if !errors.Is(err, ports.ErrMetricsForwardRefused) {
		t.Fatalf("error %v, want ErrMetricsForwardRefused", err)
	}
	if !strings.Contains(err.Error(), "Linkerd's authorization policy") {
		t.Errorf("the cause is not named: %v", err)
	}
	if strings.Contains(err.Error(), "kube-rbac-proxy") {
		t.Errorf("blamed a kube-rbac-proxy: %v", err)
	}
}

// The API server refusing the ACCOUNT (a Status body) is not routed around.
func TestAnAccountRefusalIsNotRoutedAround(t *testing.T) {
	var forwards atomic.Int32
	adapter := trafficTestAdapter(t, "dev", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","message":"services \"prometheus\" is forbidden: User \"dev\" cannot get resource \"services/proxy\"","reason":"Forbidden","code":403}`))
	})
	adapter.forwards = portForwards{byID: make(map[string]*forwarder)}
	adapter.forwards.dial = func(domain.ClusterID, domain.NamespaceName, string, int, int) (bool, int, attempt, error) {
		forwards.Add(1)
		return false, 0, attempt{}, errors.New("unexpected")
	}

	_, err := adapter.QueryInstant(context.Background(), "dev", trafficBackend(), "count(x)", time.Now())
	if !errors.Is(err, ports.ErrForbidden) || forwards.Load() != 0 {
		t.Fatalf("error %v, forwards %d", err, forwards.Load())
	}
}

func TestTheRefusalCauseNamesLinkerdOnlyWhenItIsLinkerd(t *testing.T) {
	header := http.Header{}
	header.Set("l5d-proxy-error", "client is not authorized")
	if got := proxyRefusalCause(trafficBackend(), header); !strings.Contains(got, "Linkerd") || !strings.Contains(got, "not authorized") {
		t.Errorf("header: %s", got)
	}
	if got := proxyRefusalCause(trafficBackend(), http.Header{}); !strings.Contains(got, "refused the API server's proxy (HTTP 403)") {
		t.Errorf("generic: %s", got)
	}
}

// ONE forward for a whole batch, the proxy refusal remembered so the batch's
// later queries do not earn another 403, and the forward gone when the batch
// ends. While open it is internal: not listed, not reached by "Stop all".
func TestABatchSharesOneInternalForwardAndEndsIt(t *testing.T) {
	rig := newMeshRig(t, nil)
	backend := vizBackendForTest()

	ctx, end := rig.adapter.BeginQueryBatch(context.Background())
	for range 4 {
		if _, err := rig.adapter.QueryInstant(ctx, "dev", backend, "count(request_total)", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if rig.forwards.Load() != 1 || rig.proxied.Load() != 1 || rig.loopback.Load() != 4 {
		t.Fatalf("forwards %d, proxied %d, loopback %d", rig.forwards.Load(), rig.proxied.Load(), rig.loopback.Load())
	}

	if listed := rig.adapter.ListPortForwards(); len(listed) != 0 {
		t.Errorf("an internal forward is listed: %+v", listed)
	}
	rig.adapter.StopAllPortForwards()
	if _, err := rig.adapter.QueryInstant(ctx, "dev", backend, "count(request_total)", time.Now()); err != nil || rig.forwards.Load() != 1 {
		t.Errorf("Stop all reached the batch's forward: %v, forwards %d", err, rig.forwards.Load())
	}

	end()
	rig.adapter.forwards.mu.Lock()
	left := len(rig.adapter.forwards.byID)
	rig.adapter.forwards.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d forwards left after the batch ended", left)
	}

	// Remembered: the next query, batch or not, goes straight to a forward.
	if _, err := rig.adapter.QueryInstant(context.Background(), "dev", backend, "count(request_total)", time.Now()); err != nil {
		t.Fatal(err)
	}
	if rig.proxied.Load() != 1 {
		t.Errorf("the refused proxy was asked again (%d)", rig.proxied.Load())
	}

	// Forgotten with the connection.
	rig.adapter.forwardRoutes.forget("dev")
	if _, err := rig.adapter.QueryInstant(context.Background(), "dev", backend, "count(request_total)", time.Now()); err != nil {
		t.Fatal(err)
	}
	if rig.proxied.Load() != 2 {
		t.Errorf("a forgotten route was still used (%d)", rig.proxied.Load())
	}
}
