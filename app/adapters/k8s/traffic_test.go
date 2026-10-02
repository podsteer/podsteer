package k8s

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// trafficTestAdapter wires an Adapter whose client set for id points at a
// test server standing in for the API server's service proxy.
func trafficTestAdapter(t *testing.T, id domain.ClusterID, handler http.HandlerFunc) *Adapter {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	typed, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatalf("building a client: %v", err)
	}
	factory := newClientFactory(Config{})
	factory.clients[id] = &clients{typed: typed, queryHTTP: server.Client()}
	return &Adapter{factory: factory, logger: slog.New(slog.DiscardHandler)}
}

func trafficBackend() domain.MetricsBackend {
	return domain.MetricsBackend{
		Kind: domain.MetricsBackendPrometheus, Namespace: "monitoring",
		Service: "prometheus-operated", Port: "web",
	}
}

func TestQueryInstantGoesThroughTheServiceProxyAsOneGet(t *testing.T) {
	var (
		mu       sync.Mutex
		requests []*http.Request
	)
	adapter := trafficTestAdapter(t, "dev", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[` +
			`{"metric":{"source_workload":"web","destination_workload":"api"},"value":[1759400000.1,"12.5"]},` +
			`{"metric":{"source_workload":"web","destination_workload":"idle"},"value":[1759400000.1,"NaN"]}]}}`))
	})

	expression := `sum by (source_workload, destination_workload) (rate(istio_requests_total{reporter="source"}[5m]))`
	at := time.Unix(1759400000, 0)
	series, err := adapter.QueryInstant(context.Background(), "dev", trafficBackend(), expression, at)
	if err != nil {
		t.Fatalf("QueryInstant: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 || requests[0].Method != http.MethodGet {
		t.Fatalf("requests %d, want exactly one GET", len(requests))
	}
	request := requests[0]
	if want := "/api/v1/namespaces/monitoring/services/http:prometheus-operated:web/proxy/api/v1/query"; request.URL.Path != want {
		t.Errorf("path %s, want %s", request.URL.Path, want)
	}
	if got := request.URL.Query().Get("query"); got != expression {
		t.Errorf("query %q", got)
	}
	if got := request.URL.Query().Get("time"); got != "1759400000.000" {
		t.Errorf("time %q", got)
	}

	if len(series) != 2 || len(series[0].Points) != 1 || series[0].Points[0].Value != 12.5 {
		t.Fatalf("series %+v", series)
	}
	if series[0].Labels["destination_workload"] != "api" {
		t.Errorf("labels %v", series[0].Labels)
	}
	if len(series[1].Points) != 0 {
		t.Errorf("a NaN became a point: %+v", series[1])
	}
}

func TestQueryInstantCarriesTheBackendsRejection(t *testing.T) {
	adapter := trafficTestAdapter(t, "dev", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"parse error: unexpected identifier"}`))
	})

	_, err := adapter.QueryInstant(context.Background(), "dev", trafficBackend(), "count(caretta_links_observed)", time.Now())
	if !errors.Is(err, ports.ErrMetricsQueryRejected) || !strings.Contains(err.Error(), "unexpected identifier") {
		t.Fatalf("error %v, want the backend's own words", err)
	}
}

func TestQueryInstantRefusesAnOverLongExpressionBeforeAnyRequest(t *testing.T) {
	var called atomic.Bool
	adapter := trafficTestAdapter(t, "dev", func(http.ResponseWriter, *http.Request) { called.Store(true) })

	_, err := adapter.QueryInstant(context.Background(), "dev", trafficBackend(), strings.Repeat("sum(x)+", 2000), time.Now())
	if !errors.Is(err, domain.ErrQueryTooLong) {
		t.Fatalf("error %v, want ErrQueryTooLong", err)
	}
	if called.Load() {
		t.Fatal("an over-long expression reached the wire")
	}
}

var _ ports.TrafficQueryPort = (*Adapter)(nil)
