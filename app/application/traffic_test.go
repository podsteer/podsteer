package application_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// countingTraffic answers instant queries by the first matching fragment and
// counts every request that leaves.
type countingTraffic struct {
	probeCalls atomic.Int32
	queryCalls atomic.Int32

	mu      sync.Mutex
	answers map[string][]domain.PromSeries
	err     error
	sent    []string
}

func (q *countingTraffic) QueryInstant(
	_ context.Context, _ domain.ClusterID, _ domain.MetricsBackend, expression string, _ time.Time,
) ([]domain.PromSeries, error) {
	if strings.HasPrefix(expression, "count(") {
		q.probeCalls.Add(1)
	} else {
		q.queryCalls.Add(1)
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	q.sent = append(q.sent, expression)
	if q.err != nil {
		return nil, q.err
	}
	// The longest matching fragment wins, so the answer does not depend on
	// map order.
	best, answer := -1, []domain.PromSeries(nil)
	for fragment, series := range q.answers {
		if strings.Contains(expression, fragment) && len(fragment) > best {
			best, answer = len(fragment), series
		}
	}
	return answer, nil
}

func one(value float64, labels map[string]string) domain.PromSeries {
	return domain.PromSeries{Labels: labels, Points: []domain.SeriesPoint{{At: time.Now(), Value: value}}}
}

type stubTrafficNodes struct{ nodes []domain.TrafficNodeRef }

func (s stubTrafficNodes) TrafficNodes(context.Context, domain.ClusterID, []domain.NamespaceName, bool) ([]domain.TrafficNodeRef, error) {
	return s.nodes, nil
}

type trafficFixture struct {
	service   *application.TrafficService
	traffic   *countingTraffic
	discovery *stubDiscovery
	nodes     *stubNodes
	query     *countingQuery
}

func newTrafficFixture(t *testing.T, mode domain.MetricsQueryMode, backendNodes []string) trafficFixture {
	t.Helper()

	query := &countingQuery{nodes: backendNodes}
	discovery := &stubDiscovery{backends: []domain.MetricsBackend{testBackend()}}
	nodes := &stubNodes{names: []string{"node-a", "node-b"}}
	metrics, err := application.NewMetricsQueryService(application.MetricsQueryServiceDeps{
		Settings: stubSettings{settings: domain.ClusterSettings{
			MetricsQuery: domain.MetricsQuerySettings{Mode: mode, Fleet: domain.FleetFilter},
		}},
		Discovery: discovery,
		Query:     query,
		Nodes:     nodes,
	})
	if err != nil {
		t.Fatal(err)
	}

	traffic := &countingTraffic{answers: map[string][]domain.PromSeries{
		"count(":                          nil,
		`count({__name__=~"istio_request`: {one(42, nil)},
	}}
	service, err := application.NewTrafficService(application.TrafficServiceDeps{
		Metrics: metrics,
		Query:   traffic,
		Nodes: stubTrafficNodes{nodes: []domain.TrafficNodeRef{
			{ID: "deploy/web", APIKind: "Deployment", Name: "web", Namespace: "shop"},
			{ID: "deploy/api", APIKind: "Deployment", Name: "api", Namespace: "shop"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return trafficFixture{service: service, traffic: traffic, discovery: discovery, nodes: nodes, query: query}
}

// OFF SENDS NOTHING, counted rather than inferred from the status.
func TestTrafficSendsNothingForAClusterThatIsOff(t *testing.T) {
	f := newTrafficFixture(t, domain.MetricsQueryOff, []string{"node-a", "node-b"})

	sources, err := f.service.Sources(context.Background(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	layer, err := f.service.Traffic(context.Background(), "dev", nil, true, domain.TrafficIstio, domain.TrafficWindow5m)
	if err != nil {
		t.Fatal(err)
	}

	if sources.Status != domain.BackendNotEnabled || layer.Status != domain.BackendNotEnabled {
		t.Errorf("statuses %s / %s, want not-enabled", sources.Status, layer.Status)
	}
	if len(sources.Sources) != 5 || sources.Sources[0].Detail == "" {
		t.Errorf("an off cluster still lists what each source needs: %+v", sources.Sources)
	}
	if calls := f.discovery.calls.Load() + f.query.nodeCalls.Load() + f.traffic.probeCalls.Load() + f.traffic.queryCalls.Load(); calls != 0 {
		t.Fatalf("%d calls for a cluster nobody switched on", calls)
	}
}

func TestTrafficProbesAreCachedAndDroppedOnInvalidate(t *testing.T) {
	f := newTrafficFixture(t, domain.MetricsQueryManual, []string{"node-a", "node-b"})

	for range 3 {
		sources, err := f.service.Sources(context.Background(), "dev")
		if err != nil {
			t.Fatal(err)
		}
		if sources.Status != domain.BackendAnswered || !sources.Sources[0].Available || sources.Sources[1].Available {
			t.Fatalf("sources: %+v", sources)
		}
	}
	if got := f.traffic.probeCalls.Load(); got != 5 {
		t.Fatalf("%d probes over three asks, want 5 (one per source, once)", got)
	}

	f.service.Invalidate("dev")
	if _, err := f.service.Sources(context.Background(), "dev"); err != nil {
		t.Fatal(err)
	}
	if got := f.traffic.probeCalls.Load(); got != 10 {
		t.Fatalf("%d probes after Invalidate, want 10", got)
	}
}

func TestTrafficFromAFleetBackendIsRefused(t *testing.T) {
	f := newTrafficFixture(t, domain.MetricsQueryAuto, []string{"node-a", "node-b", "someone-elses"})

	layer, err := f.service.Traffic(context.Background(), "dev", []domain.NamespaceName{"shop"}, false, domain.TrafficIstio, domain.TrafficWindow5m)
	if err != nil {
		t.Fatal(err)
	}
	if layer.Status != domain.BackendUnverified || !strings.Contains(layer.Message, "grouped by workload") {
		t.Errorf("%s: %s", layer.Status, layer.Message)
	}
	if got := f.traffic.probeCalls.Load() + f.traffic.queryCalls.Load(); got != 0 {
		t.Fatalf("%d traffic requests to a fleet backend", got)
	}
}

func TestTrafficAnAbsentSourceIsNotQueried(t *testing.T) {
	f := newTrafficFixture(t, domain.MetricsQueryManual, []string{"node-a", "node-b"})

	layer, err := f.service.Traffic(context.Background(), "dev", nil, true, domain.TrafficHubble, domain.TrafficWindow5m)
	if err != nil {
		t.Fatal(err)
	}
	if layer.Status != domain.BackendAnsweredEmpty || !strings.Contains(layer.Message, "labelsContext") {
		t.Errorf("%s: %s", layer.Status, layer.Message)
	}
	if got := f.traffic.queryCalls.Load(); got != 0 {
		t.Fatalf("%d queries for a source the probe said is absent", got)
	}
}

func TestTrafficAnswersMappedEdgesWithTheirExpressions(t *testing.T) {
	f := newTrafficFixture(t, domain.MetricsQueryManual, []string{"node-a", "node-b"})
	f.traffic.answers["istio_requests_total{reporter="] = []domain.PromSeries{one(7, map[string]string{
		"source_workload": "web", "source_workload_namespace": "shop",
		"destination_workload": "api", "destination_workload_namespace": "shop",
		"destination_service_name": "api", "request_protocol": "http",
	})}

	layer, err := f.service.Traffic(context.Background(), "dev", []domain.NamespaceName{"shop"}, false, domain.TrafficIstio, domain.TrafficWindow15m)
	if err != nil {
		t.Fatal(err)
	}
	if layer.Status != domain.BackendAnswered || len(layer.Edges) != 1 {
		t.Fatalf("%s %q: %+v", layer.Status, layer.Message, layer.Edges)
	}
	edge := layer.Edges[0]
	if edge.Source.NodeID != "deploy/web" || edge.Dest.NodeID != "deploy/api" || edge.RequestsPerSec != 7 {
		t.Errorf("edge %+v", edge)
	}
	if len(layer.Expressions) != 7 || layer.Provenance.Source == "" || layer.Provenance.Verification != domain.VerificationVerified {
		t.Errorf("expressions %d, provenance %+v", len(layer.Expressions), layer.Provenance)
	}
	if got := f.traffic.queryCalls.Load(); got != 7 {
		t.Errorf("%d queries, want one per expression", got)
	}
}

func TestTrafficPastTheEdgeCapIsRefused(t *testing.T) {
	f := newTrafficFixture(t, domain.MetricsQueryManual, []string{"node-a", "node-b"})

	many := make([]domain.PromSeries, 0, domain.MaxTrafficEdges+1)
	for i := range domain.MaxTrafficEdges + 1 {
		many = append(many, one(1, map[string]string{
			"source_workload": fmt.Sprintf("w%d", i), "source_workload_namespace": "shop",
			"destination_workload": "api", "destination_workload_namespace": "shop",
		}))
	}
	f.traffic.answers["istio_requests_total{reporter="] = many

	layer, err := f.service.Traffic(context.Background(), "dev", nil, true, domain.TrafficIstio, domain.TrafficWindow5m)
	if err != nil {
		t.Fatal(err)
	}
	if layer.Status != domain.BackendTooLarge || len(layer.Edges) != 0 {
		t.Fatalf("%s with %d edges", layer.Status, len(layer.Edges))
	}
}

func TestTrafficCarriesAProxyRefusalAsAStatus(t *testing.T) {
	f := newTrafficFixture(t, domain.MetricsQueryManual, []string{"node-a", "node-b"})
	f.traffic.err = fmt.Errorf("querying: %w", ports.ErrForbidden)

	layer, err := f.service.Traffic(context.Background(), "dev", nil, true, domain.TrafficIstio, domain.TrafficWindow5m)
	if err != nil {
		t.Fatal(err)
	}
	if layer.Status != domain.BackendForbidden {
		t.Errorf("%s: %s", layer.Status, layer.Message)
	}
}

func TestTrafficRefusesAnUnknownSourceBeforeSendingAnything(t *testing.T) {
	f := newTrafficFixture(t, domain.MetricsQueryManual, []string{"node-a", "node-b"})

	if _, err := f.service.Traffic(context.Background(), "dev", nil, true, "zipkin", domain.TrafficWindow5m); err == nil {
		t.Fatal("an unknown source was accepted")
	}
	if calls := f.discovery.calls.Load() + f.traffic.probeCalls.Load(); calls != 0 {
		t.Fatalf("%d calls for a request that could not be shaped", calls)
	}
}
