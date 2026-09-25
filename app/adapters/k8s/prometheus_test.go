package k8s

// Tests for what discovery will and will not point a query at.
//
// The stakes moved when this became a ranked LIST rather than one guess: the
// head of the list is what a chart reads from unless somebody says otherwise,
// and the tail is what an operator is offered instead. So the properties
// worth pinning are that the head has not changed for anybody who had one
// before, that the ingest half of a monitoring stack can never appear at all,
// and that a service is judged on the part of it that holds still.

import (
	"strconv"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/podsteer/podsteer/app/domain"
)

func service(namespace, name string, ports ...corev1.ServicePort) corev1.Service {
	return corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec:       corev1.ServiceSpec{Ports: ports},
	}
}

// labelled returns a service carrying the standard app labels, which is how
// every VictoriaMetrics install is recognised.
func labelled(namespace, name, appName, component string, ports ...corev1.ServicePort) corev1.Service {
	svc := service(namespace, name, ports...)
	svc.Labels = map[string]string{"app.kubernetes.io/name": appName}
	if component != "" {
		svc.Labels["app.kubernetes.io/component"] = component
	}
	return svc
}

func webPort() corev1.ServicePort { return corev1.ServicePort{Name: "web", Port: 9090} }
func httpPort(number int32) corev1.ServicePort {
	return corev1.ServicePort{Name: "http", Port: number}
}

// pick returns the top candidate, which is what DiscoverMetricsBackend hands
// back.
func pick(t *testing.T, services []corev1.Service) domain.MetricsBackend {
	t.Helper()

	backends := discovered(services)
	if len(backends) == 0 {
		t.Fatal("expected a match")
	}
	return backends[0]
}

// discovered runs the classification and ranking over a service set, which is
// everything ListMetricsBackends does once the API server has answered.
func discovered(services []corev1.Service) []domain.MetricsBackend {
	var found []candidate
	for _, svc := range services {
		if matched, ok := classifyBackend(svc); ok {
			found = append(found, matched)
		}
	}
	return rankBackends(found)
}

// --- Prometheus, unchanged --------------------------------------------------

func TestPickPrefersThePrometheusOperatorService(t *testing.T) {
	// A kube-prometheus-stack installation returns SEVERAL services carrying
	// the same app label, and only one of them answers PromQL. Taking the
	// first match would point queries at the alertmanager.
	backend := pick(t, []corev1.Service{
		service("monitoring", "kube-prometheus-stack-alertmanager", webPort()),
		service("monitoring", "prometheus-operated", webPort()),
	})

	if backend.Service != "prometheus-operated" {
		t.Fatalf("picked %q, want prometheus-operated", backend.Service)
	}
	if backend.Namespace != "monitoring" {
		t.Fatalf("namespace %q, want monitoring", backend.Namespace)
	}
	if backend.Kind != domain.MetricsBackendPrometheus {
		t.Fatalf("kind %q, want prometheus", backend.Kind)
	}
	if got := backend.ProxyTarget(); got != "prometheus-operated:web" {
		t.Fatalf("proxy target %q", got)
	}
	// Prometheus serves the query API at the root, so there is nothing to
	// prepend — and a prefix invented here would be appended to every path.
	if backend.Prefix != "" {
		t.Fatalf("prefix %q, want none for Prometheus", backend.Prefix)
	}
}

func TestPickMatchesAChartPrefix(t *testing.T) {
	// Helm prefixes the release name, so the service is rarely called exactly
	// "prometheus-server".
	backend := pick(t, []corev1.Service{service("obs", "monitoring-prometheus-server", webPort())})

	if backend.Service != "monitoring-prometheus-server" {
		t.Fatalf("picked %q", backend.Service)
	}
}

func TestPickIgnoresUnrelatedServices(t *testing.T) {
	// The label selector is not proof on its own: an exporter and a pushgateway
	// both carry it. Guessing at one would have PodSteer proxying queries at
	// something that does not speak PromQL.
	found := discovered([]corev1.Service{
		service("monitoring", "prometheus-node-exporter", corev1.ServicePort{Name: "metrics", Port: 9100}),
		service("monitoring", "prometheus-pushgateway", corev1.ServicePort{Name: "http", Port: 9091}),
	})

	if len(found) != 0 {
		t.Fatalf("matched %+v, want no match", found)
	}
}

func TestPickSkipsExternalName(t *testing.T) {
	// An ExternalName service has no endpoints for the API server to proxy to,
	// so it would resolve and then fail at query time.
	external := service("monitoring", "prometheus", webPort())
	external.Spec.Type = corev1.ServiceTypeExternalName

	if found := discovered([]corev1.Service{external}); len(found) != 0 {
		t.Fatalf("matched an ExternalName service: %+v", found)
	}
}

func TestServicePortPrefersTheNamedPort(t *testing.T) {
	// The operator's service lists a gRPC port first. Taking the first port
	// would proxy HTTP at Thanos sidecar gRPC.
	svc := service("monitoring", "prometheus-operated",
		corev1.ServicePort{Name: "grpc", Port: 10901},
		corev1.ServicePort{Name: "web", Port: 9090},
	)

	port, ok := servicePort(svc, prometheusPortNames, prometheusPortNumbers)
	if !ok || port != "web" {
		t.Fatalf("got %q, %v; want web", port, ok)
	}
}

func TestServicePortFallsBackToNineOhNineOh(t *testing.T) {
	// A bare deployment that named nothing is still findable by number.
	svc := service("default", "prometheus", corev1.ServicePort{Port: 9090})

	port, ok := servicePort(svc, prometheusPortNames, prometheusPortNumbers)
	if !ok || port != "9090" {
		t.Fatalf("got %q, %v; want 9090", port, ok)
	}
}

func TestServicePortRejectsAServiceWithNoQueryPort(t *testing.T) {
	svc := service("monitoring", "prometheus-operated", corev1.ServicePort{Name: "grpc", Port: 10901})

	if port, ok := servicePort(svc, prometheusPortNames, prometheusPortNumbers); ok {
		t.Fatalf("matched port %q, want none", port)
	}
}

// --- VictoriaMetrics --------------------------------------------------------

func TestVictoriaMetricsIsDiscovered(t *testing.T) {
	// Every shape a fair number of clusters actually run. Each row's labels
	// and ports are transcribed from what the operator and the charts
	// actually produce — see the file comment in prometheus.go.
	tests := map[string]struct {
		service    corev1.Service
		wantPrefix string
	}{
		"the operator's single-node component": {
			// `vmsingle-foo`, http/8429 plus the 8428 alias the operator adds.
			service: labelled("vm", "vmsingle-foo", "vmsingle", "monitoring",
				httpPort(8429), corev1.ServicePort{Name: "http-alias", Port: 8428}),
			wantPrefix: "",
		},
		"the operator's single-node component under legacy naming": {
			// `useLegacyNaming` leaves the Service called bare `foo`, with no
			// prefix at all — which is precisely why the LABEL decides and
			// the name does not.
			service:    labelled("vm", "foo", "vmsingle", "monitoring", httpPort(8429)),
			wantPrefix: "",
		},
		"the operator's cluster read component": {
			service:    labelled("vm", "vmselect-bar", "vmselect", "monitoring", httpPort(8481)),
			wantPrefix: victoriaSelectPrefix,
		},
		"the single chart": {
			service: labelled("vm", "rel-victoria-metrics-single-server",
				"victoria-metrics-single", "server", httpPort(8428)),
			wantPrefix: "",
		},
		"the cluster chart's select component": {
			service: labelled("vm", "rel-victoria-metrics-cluster-vmselect",
				"victoria-metrics-cluster", "vmselect", httpPort(8481)),
			wantPrefix: victoriaSelectPrefix,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			backend := pick(t, []corev1.Service{test.service})

			if backend.Kind != domain.MetricsBackendVictoriaMetrics {
				t.Errorf("kind = %q, want victoriametrics", backend.Kind)
			}
			if backend.Service != test.service.Name {
				t.Errorf("service = %q, want %q", backend.Service, test.service.Name)
			}
			if backend.Port != "http" {
				t.Errorf("port = %q, want http", backend.Port)
			}
			if backend.Prefix != test.wantPrefix {
				t.Errorf("prefix = %q, want %q", backend.Prefix, test.wantPrefix)
			}
		})
	}
}

func TestTheVictoriaMetricsWritePathIsNeverACandidate(t *testing.T) {
	// vmagent scrapes and forwards, vminsert accepts writes and shards them,
	// vmstorage holds them. None serves a query API, so proxying PromQL at
	// one can only fail — and doing it against somebody's ingest path is a
	// request worth being sure never leaves.
	//
	// The vmstorage row is the sharp one: the chart puts the SAME
	// app.kubernetes.io/name on it as on vmselect, and it publishes a port
	// literally NAMED "vmselect" (the internal storage/select port). Anything
	// matching on the label alone, or on a port name, would fall for it.
	tests := map[string]corev1.Service{
		"the operator's vmagent": labelled("vm", "vmagent-foo", "vmagent", "monitoring",
			httpPort(8429)),
		"the operator's vminsert": labelled("vm", "vminsert-bar", "vminsert", "monitoring",
			httpPort(8480)),
		"the operator's vmstorage": labelled("vm", "vmstorage-bar", "vmstorage", "monitoring",
			httpPort(8482)),
		"the cluster chart's vminsert": labelled("vm", "rel-victoria-metrics-cluster-vminsert",
			"victoria-metrics-cluster", "vminsert", httpPort(8480)),
		"the cluster chart's vmstorage": labelled("vm", "rel-victoria-metrics-cluster-vmstorage",
			"victoria-metrics-cluster", "vmstorage",
			httpPort(8482),
			corev1.ServicePort{Name: "vmselect", Port: 8401},
			corev1.ServicePort{Name: "vminsert", Port: 8400},
		),
	}

	for name, svc := range tests {
		t.Run(name, func(t *testing.T) {
			if found := discovered([]corev1.Service{svc}); len(found) != 0 {
				t.Fatalf("the write path was offered as a query endpoint: %+v", found)
			}
		})
	}
}

func TestPrometheusOutranksVictoriaMetrics(t *testing.T) {
	// A cluster running both must keep answering the way it answered before
	// VictoriaMetrics was recognised: whoever had a Prometheus at the head of
	// this list still has it there. Between the two VictoriaMetrics shapes,
	// the single-node one goes first because it needs no prefix and no tenant
	// — nothing PodSteer had to assume.
	backends := discovered([]corev1.Service{
		labelled("vm", "vmselect-bar", "vmselect", "monitoring", httpPort(8481)),
		labelled("vm", "vmsingle-foo", "vmsingle", "monitoring", httpPort(8429)),
		service("monitoring", "prometheus-operated", webPort()),
	})

	if len(backends) != 3 {
		t.Fatalf("got %d candidates, want 3: %+v", len(backends), backends)
	}
	want := []string{"prometheus-operated", "vmsingle-foo", "vmselect-bar"}
	for index, name := range want {
		if backends[index].Service != name {
			t.Errorf("candidate %d = %q, want %q", index, backends[index].Service, name)
		}
	}
}

func TestEveryCandidateIsOfferedRatherThanOnlyTheBest(t *testing.T) {
	// The point of the list: a cluster with two Prometheus installations has
	// a right answer PodSteer cannot know, so the operator is offered both
	// rather than told which one theirs is.
	backends := discovered([]corev1.Service{
		service("obs", "team-prometheus-server", webPort()),
		service("monitoring", "prometheus-operated", webPort()),
	})

	if len(backends) != 2 {
		t.Fatalf("got %d candidates, want both: %+v", len(backends), backends)
	}
	if backends[0].Service != "prometheus-operated" {
		t.Errorf("head = %q, want the operator's service", backends[0].Service)
	}
}

func TestOneServiceMatchedTwiceIsOfferedOnce(t *testing.T) {
	// Both selectors can match the same service — a chart setting the
	// standard app label and the older `app` one alike — and the picker must
	// not show it twice.
	duplicated := service("monitoring", "prometheus-operated", webPort())
	duplicated.Labels = map[string]string{
		"app.kubernetes.io/name": "prometheus",
		"app":                    "prometheus",
	}

	backends := discovered([]corev1.Service{duplicated, duplicated})

	if len(backends) != 1 {
		t.Fatalf("got %d candidates, want 1: %+v", len(backends), backends)
	}
}

func TestTheRankingIsTotalSoThePickerDoesNotMoveBetweenOpenings(t *testing.T) {
	// Two candidates of the same product and the same name rank are ordered
	// by namespace and name, never by the order the API server listed them.
	// A picker whose rows move between openings is a picker nobody trusts.
	first := discovered([]corev1.Service{
		labelled("b", "vmsingle-x", "vmsingle", "monitoring", httpPort(8429)),
		labelled("a", "vmsingle-x", "vmsingle", "monitoring", httpPort(8429)),
	})
	second := discovered([]corev1.Service{
		labelled("a", "vmsingle-x", "vmsingle", "monitoring", httpPort(8429)),
		labelled("b", "vmsingle-x", "vmsingle", "monitoring", httpPort(8429)),
	})

	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("got %d and %d candidates, want 2 each", len(first), len(second))
	}
	for index := range first {
		if first[index] != second[index] {
			t.Fatalf("candidate %d differs by list order: %+v vs %+v",
				index, first[index], second[index])
		}
	}
	if first[0].Namespace != "a" {
		t.Errorf("head namespace = %q, want a", first[0].Namespace)
	}
}

func TestVictoriaMetricsFallsBackToItsPortNumbers(t *testing.T) {
	// A hand-written manifest that named nothing is still findable, and the
	// numbers are VictoriaMetrics' own rather than Prometheus' 9090.
	for _, number := range victoriaPortNumbers {
		svc := labelled("vm", "vmsingle-foo", "vmsingle", "monitoring",
			corev1.ServicePort{Port: number})

		backend := pick(t, []corev1.Service{svc})
		if backend.Port != strconv.Itoa(int(number)) {
			t.Errorf("port = %q, want %d", backend.Port, number)
		}
	}
}

func TestAVictoriaMetricsServiceWithNoQueryPortIsRefused(t *testing.T) {
	// The same rule Prometheus follows: a service PodSteer cannot address is
	// not a candidate, because offering it would produce a chart that can
	// only fail.
	svc := labelled("vm", "vmsingle-foo", "vmsingle", "monitoring",
		corev1.ServicePort{Name: "grpc", Port: 10901})

	if found := discovered([]corev1.Service{svc}); len(found) != 0 {
		t.Fatalf("matched %+v, want no match", found)
	}
}

func TestDescribeNamesTheProductThatWasFound(t *testing.T) {
	// Telling somebody they run Prometheus when they run VictoriaMetrics
	// sends them looking for something that is not there, which is PodSteer
	// making a false claim about their cluster rather than a cosmetic slip.
	tests := map[string]struct {
		backend domain.MetricsBackend
		want    string
	}{
		"prometheus": {
			backend: domain.MetricsBackend{
				Kind: domain.MetricsBackendPrometheus, Namespace: "monitoring",
			},
			want: "Prometheus in monitoring",
		},
		"victoriametrics": {
			backend: domain.MetricsBackend{
				Kind: domain.MetricsBackendVictoriaMetrics, Namespace: "vm",
			},
			want: "VictoriaMetrics in vm",
		},
		"nothing found": {
			backend: domain.MetricsBackend{},
			want:    "",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := test.backend.Describe(); got != test.want {
				t.Fatalf("Describe() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestTheSelectorAsksForEveryProductInOneRequest(t *testing.T) {
	// A candidate LIST means no selector can short-circuit the rest, so a
	// selector per product would be five list calls per cluster where there
	// used to be one. The set-based form keeps it at two.
	if len(backendSelectors) != 2 {
		t.Fatalf("backendSelectors = %v, want two", backendSelectors)
	}
	for _, name := range backendNameLabels {
		if !strings.Contains(backendSelectors[0], name) {
			t.Errorf("the set selector %q does not ask about %q", backendSelectors[0], name)
		}
	}
	// The write path must not be asked for at all, quite apart from being
	// refused after the fact.
	for _, name := range writeOnlyNameLabels {
		for _, selector := range backendSelectors {
			if strings.Contains(selector, name) {
				t.Errorf("selector %q asks for the write component %q", selector, name)
			}
		}
	}
}
