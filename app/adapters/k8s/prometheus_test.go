package k8s

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func service(namespace, name string, ports ...corev1.ServicePort) corev1.Service {
	return corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec:       corev1.ServiceSpec{Ports: ports},
	}
}

func webPort() corev1.ServicePort { return corev1.ServicePort{Name: "web", Port: 9090} }

func TestPickPrometheusPrefersTheOperatorService(t *testing.T) {
	// A kube-prometheus-stack installation returns SEVERAL services carrying
	// the same app label, and only one of them answers PromQL. Taking the
	// first match would point queries at the alertmanager.
	services := []corev1.Service{
		service("monitoring", "kube-prometheus-stack-alertmanager", webPort()),
		service("monitoring", "prometheus-operated", webPort()),
	}

	backend, ok := pickPrometheus(services)
	if !ok {
		t.Fatal("expected a match")
	}
	if backend.Service != "prometheus-operated" {
		t.Fatalf("picked %q, want prometheus-operated", backend.Service)
	}
	if backend.Namespace != "monitoring" {
		t.Fatalf("namespace %q, want monitoring", backend.Namespace)
	}
	if got := backend.ProxyTarget(); got != "prometheus-operated:web" {
		t.Fatalf("proxy target %q", got)
	}
}

func TestPickPrometheusMatchesAChartPrefix(t *testing.T) {
	// Helm prefixes the release name, so the service is rarely called exactly
	// "prometheus-server".
	services := []corev1.Service{service("obs", "monitoring-prometheus-server", webPort())}

	backend, ok := pickPrometheus(services)
	if !ok || backend.Service != "monitoring-prometheus-server" {
		t.Fatalf("got %+v, %v", backend, ok)
	}
}

func TestPickPrometheusIgnoresUnrelatedServices(t *testing.T) {
	// The label selector is not proof on its own: an exporter and a pushgateway
	// both carry it. Guessing at one would have PodSteer proxying queries at
	// something that does not speak PromQL.
	services := []corev1.Service{
		service("monitoring", "prometheus-node-exporter", corev1.ServicePort{Name: "metrics", Port: 9100}),
		service("monitoring", "prometheus-pushgateway", corev1.ServicePort{Name: "http", Port: 9091}),
	}

	if backend, ok := pickPrometheus(services); ok {
		t.Fatalf("matched %+v, want no match", backend)
	}
}

func TestPickPrometheusSkipsExternalName(t *testing.T) {
	// An ExternalName service has no endpoints for the API server to proxy to,
	// so it would resolve and then fail at query time.
	external := service("monitoring", "prometheus", webPort())
	external.Spec.Type = corev1.ServiceTypeExternalName

	if _, ok := pickPrometheus([]corev1.Service{external}); ok {
		t.Fatal("matched an ExternalName service")
	}
}

func TestQueryPortPrefersTheNamedPort(t *testing.T) {
	// The operator's service lists a gRPC port first. Taking the first port
	// would proxy HTTP at Thanos sidecar gRPC.
	svc := service("monitoring", "prometheus-operated",
		corev1.ServicePort{Name: "grpc", Port: 10901},
		corev1.ServicePort{Name: "web", Port: 9090},
	)

	port, ok := queryPort(svc)
	if !ok || port != "web" {
		t.Fatalf("got %q, %v; want web", port, ok)
	}
}

func TestQueryPortFallsBackToNineOhNineOh(t *testing.T) {
	// A bare deployment that named nothing is still findable by number.
	svc := service("default", "prometheus", corev1.ServicePort{Port: 9090})

	port, ok := queryPort(svc)
	if !ok || port != "9090" {
		t.Fatalf("got %q, %v; want 9090", port, ok)
	}
}

func TestQueryPortRejectsAServiceWithNoQueryPort(t *testing.T) {
	svc := service("monitoring", "prometheus-operated", corev1.ServicePort{Name: "grpc", Port: 10901})

	if port, ok := queryPort(svc); ok {
		t.Fatalf("matched port %q, want none", port)
	}
}
