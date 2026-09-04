package k8s

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	clientgotesting "k8s.io/client-go/testing"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// labelledService builds a Service the way a kube-state-metrics chart does:
// the standard app label, and the metrics port beside the telemetry one.
func labelledService(namespace, name string, ports ...corev1.ServicePort) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels:    map[string]string{"app.kubernetes.io/name": kubeStateName},
		},
		Spec: corev1.ServiceSpec{Ports: ports},
	}
}

func metricsPort() corev1.ServicePort {
	return corev1.ServicePort{Name: "http-metrics", Port: 8080}
}

func telemetryPort() corev1.ServicePort {
	return corev1.ServicePort{Name: "telemetry", Port: 8081}
}

var serviceResource = schema.GroupResource{Resource: "services"}

func TestDiscoverKubeStateMetricsFindsAChartInstallation(t *testing.T) {
	// The ordinary find: the chart's own service, named exactly, with the
	// metrics port beside the telemetry one it also publishes.
	adapter := newTestAdapter("dev", fake.NewSimpleClientset(
		labelledService("monitoring", kubeStateName, telemetryPort(), metricsPort()),
	))

	found, err := adapter.DiscoverKubeStateMetrics(context.Background(), "dev")
	if err != nil {
		t.Fatalf("DiscoverKubeStateMetrics() error = %v", err)
	}
	if !found.Found() {
		t.Fatal("expected kube-state-metrics to be found")
	}
	if found.Namespace != "monitoring" || found.Service != kubeStateName {
		t.Fatalf("found %+v, want the monitoring installation", found)
	}
	// The metrics port, not the telemetry one: telemetry serves
	// kube-state-metrics' own process metrics rather than the cluster's
	// objects, so pointing at it would name the wrong half.
	if found.Port != "http-metrics" {
		t.Fatalf("port = %q, want http-metrics", found.Port)
	}
	if got := found.Describe(); got != "kube-state-metrics in monitoring" {
		t.Fatalf("describe = %q", got)
	}
}

func TestPickKubeStateMetricsRanksSeveralInstallationsDeterministically(t *testing.T) {
	// A cluster genuinely holds two of these often enough to matter: a
	// distribution's own addon in kube-system, and a kube-prometheus-stack
	// release in monitoring. Either is a true answer, so the requirement is
	// that it is the SAME answer every read — a note naming a different
	// namespace on each refresh reads as the cluster changing.
	services := []corev1.Service{
		*labelledService("monitoring", "prom-stack-kube-state-metrics", metricsPort()),
		*labelledService("kube-system", kubeStateName, metricsPort()),
	}

	found, ok := pickKubeStateMetrics(services)
	if !ok {
		t.Fatal("expected a match")
	}
	if found.Namespace != "kube-system" || found.Service != kubeStateName {
		t.Fatalf("picked %+v, want the exactly named installation", found)
	}

	// Reversed input, same answer. The API server gives no ordering
	// guarantee on a LIST, so the ranking has to be total on its own.
	reversed := []corev1.Service{services[1], services[0]}
	again, ok := pickKubeStateMetrics(reversed)
	if !ok || again != found {
		t.Fatalf("picked %+v from reversed input, want %+v", again, found)
	}
}

func TestPickKubeStateMetricsBreaksAnEqualRankByNamespace(t *testing.T) {
	// Two Helm releases, neither named exactly. Equal rank must still be a
	// total order or the answer moves between refreshes.
	services := []corev1.Service{
		*labelledService("obs", "b-kube-state-metrics", metricsPort()),
		*labelledService("obs", "a-kube-state-metrics", metricsPort()),
	}

	found, ok := pickKubeStateMetrics(services)
	if !ok || found.Service != "a-kube-state-metrics" {
		t.Fatalf("picked %+v, want the first by name", found)
	}
}

func TestPickKubeStateMetricsAcceptsAServiceWithNoRecognisedPort(t *testing.T) {
	// THE DELIBERATE DIVERGENCE FROM pickPrometheus. Prometheus is refused
	// without a query port because the wrong port means proxying PromQL at
	// something that does not speak it; nothing ever connects to this, so a
	// service whose port somebody renamed is still kube-state-metrics and is
	// still worth naming. The port is simply left empty.
	services := []corev1.Service{
		*labelledService("monitoring", kubeStateName, corev1.ServicePort{Name: "ksm", Port: 9000}),
	}

	found, ok := pickKubeStateMetrics(services)
	if !ok {
		t.Fatal("expected a match despite the unrecognised port")
	}
	if found.Port != "" {
		t.Fatalf("port = %q, want it left empty rather than guessed", found.Port)
	}
}

func TestPickKubeStateMetricsFallsBackToPortEightyEighty(t *testing.T) {
	services := []corev1.Service{
		*labelledService("monitoring", kubeStateName, corev1.ServicePort{Port: 8080}),
	}

	found, ok := pickKubeStateMetrics(services)
	if !ok || found.Port != "8080" {
		t.Fatalf("found %+v, want the numeric fallback", found)
	}
}

func TestPickKubeStateMetricsSkipsExternalName(t *testing.T) {
	// An ExternalName service points OUTSIDE this cluster. "kube-state-metrics
	// is running in monitoring" is the only claim the note makes, and about
	// one of these it would be false.
	external := labelledService("monitoring", kubeStateName, metricsPort())
	external.Spec.Type = corev1.ServiceTypeExternalName

	if found, ok := pickKubeStateMetrics([]corev1.Service{*external}); ok {
		t.Fatalf("matched %+v, want an ExternalName service skipped", found)
	}
}

func TestPickKubeStateMetricsIgnoresUnrelatedServices(t *testing.T) {
	// The label selector is not proof on its own — a chart can carry the
	// label on a headless or a self-metrics service. Only the name decides.
	services := []corev1.Service{
		*labelledService("monitoring", "prometheus-node-exporter", metricsPort()),
		*labelledService("monitoring", "metrics-server", metricsPort()),
	}

	if found, ok := pickKubeStateMetrics(services); ok {
		t.Fatalf("matched %+v, want no match", found)
	}
}

func TestDiscoverKubeStateMetricsFindingNothingIsNotAnError(t *testing.T) {
	// The ordinary answer on most clusters. A cluster with no
	// kube-state-metrics is not a degraded cluster, and nothing about this
	// screen should read as broken because of it.
	adapter := newTestAdapter("dev", fake.NewSimpleClientset())

	found, err := adapter.DiscoverKubeStateMetrics(context.Background(), "dev")
	if err != nil {
		t.Fatalf("DiscoverKubeStateMetrics() error = %v, want the absence to be ordinary", err)
	}
	if found.Found() {
		t.Fatalf("found %+v, want nothing", found)
	}
	if found.Describe() != "" {
		t.Fatalf("describe = %q, want nothing said about a cluster that has none", found.Describe())
	}
}

func TestDiscoverKubeStateMetricsCachesARefusalRatherThanRetryingIt(t *testing.T) {
	// An account that may not list services cluster-wide never will be able
	// to, and retrying every poll writes denied requests into somebody's
	// audit log forever for a note that is an offer. The same discipline
	// DiscoverMetricsBackend follows.
	client := fake.NewSimpleClientset()
	var lists int
	client.PrependReactor("list", "services", func(clientgotesting.Action) (bool, runtime.Object, error) {
		lists++
		return true, nil, apierrors.NewForbidden(serviceResource, "", errors.New("nope"))
	})
	adapter := newTestAdapter("dev", client)

	for range 3 {
		found, err := adapter.DiscoverKubeStateMetrics(context.Background(), "dev")
		if err != nil {
			t.Fatalf("DiscoverKubeStateMetrics() error = %v, want a refusal to read as nothing", err)
		}
		if found.Found() {
			t.Fatalf("found %+v after a refusal, want nothing", found)
		}
	}

	// ONE list, not two: the refusal is cached on the first selector rather
	// than falling through to try the second, because an account refused
	// `list services` cluster-wide is refused it whatever the selector says.
	if lists != 1 {
		t.Errorf("listed %d times, want 1 — a refusal must be cached", lists)
	}
}

func TestDiscoverKubeStateMetricsDoesNotCacheATransportFailure(t *testing.T) {
	// A cluster that was merely unreachable comes back, and should be asked
	// again when it does rather than in half an hour.
	client := fake.NewSimpleClientset()
	var lists int
	client.PrependReactor("list", "services", func(clientgotesting.Action) (bool, runtime.Object, error) {
		lists++
		return true, nil, apierrors.NewServiceUnavailable("the api server is down")
	})
	adapter := newTestAdapter("dev", client)

	for range 2 {
		if _, err := adapter.DiscoverKubeStateMetrics(context.Background(), "dev"); err == nil {
			t.Fatal("DiscoverKubeStateMetrics() error = nil, want the failure reported")
		} else if !errors.Is(err, ports.ErrUnreachable) {
			t.Fatalf("DiscoverKubeStateMetrics() error = %v, want it classified as unreachable", err)
		}
	}

	if lists != 2 {
		t.Errorf("listed %d times, want 2 — a transport failure must not be cached", lists)
	}
}

func TestDiscoverKubeStateMetricsServesTheSecondReadFromTheCache(t *testing.T) {
	// The whole point: this must never ride the ten-second poll. An operator
	// staring at the overview for half an hour makes ONE of these calls.
	client := fake.NewSimpleClientset(labelledService("monitoring", kubeStateName, metricsPort()))
	var lists int
	client.PrependReactor("list", "services", func(clientgotesting.Action) (bool, runtime.Object, error) {
		lists++
		// Fall through to the tracker, which holds the seeded service.
		return false, nil, nil
	})
	adapter := newTestAdapter("dev", client)

	for range 4 {
		if _, err := adapter.DiscoverKubeStateMetrics(context.Background(), "dev"); err != nil {
			t.Fatalf("DiscoverKubeStateMetrics() error = %v", err)
		}
	}

	if lists != 1 {
		t.Errorf("listed %d times, want 1 — the rest must come from the cache", lists)
	}
}

func TestKubeStateCacheIsDroppedWhenAClusterIsInvalidated(t *testing.T) {
	// A tab is routinely reconnected because its kubeconfig context now
	// points somewhere else, and a half-hour answer carried across that would
	// name a namespace in the cluster the tab used to be.
	client := fake.NewSimpleClientset(labelledService("monitoring", kubeStateName, metricsPort()))
	var lists int
	client.PrependReactor("list", "services", func(clientgotesting.Action) (bool, runtime.Object, error) {
		lists++
		return false, nil, nil
	})
	adapter := newTestAdapter("dev", client)

	if _, err := adapter.DiscoverKubeStateMetrics(context.Background(), "dev"); err != nil {
		t.Fatalf("DiscoverKubeStateMetrics() error = %v", err)
	}
	adapter.kubeState.forget("dev")
	if _, err := adapter.DiscoverKubeStateMetrics(context.Background(), "dev"); err != nil {
		t.Fatalf("DiscoverKubeStateMetrics() error = %v", err)
	}

	if lists != 2 {
		t.Errorf("listed %d times, want 2 — the cache must not survive an invalidation", lists)
	}
}

func TestKubeStateCacheForgetsOnlyTheClusterNamed(t *testing.T) {
	// Closing one tab must not cost every other tab its answer.
	cache := &kubeStateCache{}
	cache.put("dev", domain.KubeStateMetrics{Namespace: "monitoring", Service: kubeStateName})
	cache.put("prod", domain.KubeStateMetrics{Namespace: "monitoring", Service: kubeStateName})

	cache.forget("dev")

	if _, ok := cache.get("dev"); ok {
		t.Error("dev is still cached after being forgotten")
	}
	if _, ok := cache.get("prod"); !ok {
		t.Error("prod lost its cached answer when dev was forgotten")
	}
}
