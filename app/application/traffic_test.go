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
	batches    atomic.Int32
	ended      atomic.Int32

	// hold, when set, parks every probe until it is closed; started is
	// closed when the first probe arrives.
	hold    chan struct{}
	started chan struct{}
	once    sync.Once

	mu      sync.Mutex
	answers map[string][]domain.PromSeries
	err     error
	sent    []string
	ctxErrs []error
}

func (q *countingTraffic) BeginQueryBatch(ctx context.Context) (context.Context, func()) {
	q.batches.Add(1)
	return ctx, func() { q.ended.Add(1) }
}

func (q *countingTraffic) QueryInstant(
	ctx context.Context, _ domain.ClusterID, _ domain.MetricsBackend, expression string, _ time.Time,
) ([]domain.PromSeries, error) {
	if strings.HasPrefix(expression, "count(") {
		q.probeCalls.Add(1)
		if q.hold != nil {
			q.once.Do(func() { close(q.started) })
			<-q.hold
		}
	} else {
		q.queryCalls.Add(1)
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	q.sent = append(q.sent, expression)
	q.ctxErrs = append(q.ctxErrs, ctx.Err())
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
	metrics   *application.MetricsQueryService
}

func newTrafficFixture(t *testing.T, mode domain.MetricsQueryMode, backendNodes []string) trafficFixture {
	t.Helper()
	return newTrafficFixtureWith(t, mode, backendNodes, stubTrafficNodes{nodes: []domain.TrafficNodeRef{
		{ID: "deploy/web", APIKind: "Deployment", Name: "web", Namespace: "shop"},
		{ID: "deploy/api", APIKind: "Deployment", Name: "api", Namespace: "shop"},
	}})
}

func newTrafficFixtureWith(t *testing.T, mode domain.MetricsQueryMode, backendNodes []string, reader application.TrafficNodeReader) trafficFixture {
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
		Metrics:    metrics,
		Query:      traffic,
		Nodes:      reader,
		Namespaces: stubNamespaces{names: []string{"shop", "warehouse", "linkerd-viz"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return trafficFixture{service: service, traffic: traffic, discovery: discovery, nodes: nodes, query: query, metrics: metrics}
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
	if len(layer.Expressions) != 8 || layer.Provenance.Source == "" || layer.Provenance.Verification != domain.VerificationVerified {
		t.Errorf("expressions %d, provenance %+v", len(layer.Expressions), layer.Provenance)
	}
	if got := f.traffic.queryCalls.Load(); got != 8 {
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

// A CALLER WHO GIVES UP DOES NOT FAIL THE OTHERS. The probe is shared between
// concurrent callers, and the first one's cancellation must neither cancel
// the probe nor be inherited by whoever is waiting on it.
func TestACancelledProbeLeaderDoesNotPoisonItsFollowers(t *testing.T) {
	f := newTrafficFixture(t, domain.MetricsQueryManual, []string{"node-a", "node-b"})
	f.traffic.hold, f.traffic.started = make(chan struct{}), make(chan struct{})

	leaderCtx, cancel := context.WithCancel(context.Background())
	leaderDone := make(chan domain.TrafficSources, 1)
	go func() {
		sources, _ := f.service.Sources(leaderCtx, "dev")
		leaderDone <- sources
	}()
	<-f.traffic.started

	followerDone := make(chan domain.TrafficSources, 1)
	go func() {
		sources, _ := f.service.Sources(context.Background(), "dev")
		followerDone <- sources
	}()

	cancel()
	// The leader stops waiting at once, while the probe is still parked.
	select {
	case leader := <-leaderDone:
		if leader.Status == domain.BackendAnswered {
			t.Errorf("a cancelled caller was answered: %+v", leader)
		}
	case <-time.After(5 * time.Second):
		close(f.traffic.hold)
		t.Fatal("a cancelled caller kept waiting on the shared probe")
	}

	close(f.traffic.hold)
	if follower := <-followerDone; follower.Status != domain.BackendAnswered {
		t.Errorf("the follower got %s: %s", follower.Status, follower.Message)
	}

	f.traffic.mu.Lock()
	defer f.traffic.mu.Unlock()
	for _, err := range f.traffic.ctxErrs {
		if err != nil {
			t.Fatalf("a probe ran on a cancelled context: %v", err)
		}
	}
}

// THE TOPOLOGY SERVICE IS THE NODE READER in the running application, so an
// observed endpoint carries the id of the very box the page drew for it.
func TestTrafficEndpointsCarryTheTopologysNodeIDs(t *testing.T) {
	port := &fakeTopologyPort{input: domain.TopologyInput{
		Controllers: []domain.TopologyController{
			{Kind: "Deployment", Name: "web", Namespace: "shop", Desired: 1, Ready: 1},
			{Kind: "Deployment", Name: "api", Namespace: "shop", Desired: 1, Ready: 1},
		},
	}}
	topology, _ := topologyService(t, port)
	graph, err := topology.Topology(context.Background(), "dev", mustScope(t, "shop"))
	if err != nil {
		t.Fatal(err)
	}
	drawn := map[string]bool{}
	for _, node := range graph.Nodes {
		drawn[node.ID] = true
	}

	f := newTrafficFixtureWith(t, domain.MetricsQueryManual, []string{"node-a", "node-b"}, topology)
	f.traffic.answers["istio_requests_total{reporter="] = []domain.PromSeries{one(3, map[string]string{
		"source_workload": "web", "source_workload_namespace": "shop",
		"destination_workload": "api", "destination_workload_namespace": "shop",
		"request_protocol": "http",
	})}

	layer, err := f.service.Traffic(context.Background(), "dev", []domain.NamespaceName{"shop"}, false, domain.TrafficIstio, domain.TrafficWindow5m)
	if err != nil {
		t.Fatal(err)
	}
	if len(layer.Edges) != 1 {
		t.Fatalf("%s %q: %+v", layer.Status, layer.Message, layer.Edges)
	}
	edge := layer.Edges[0]
	if edge.Source.NodeID == "" || edge.Dest.NodeID == "" || !drawn[edge.Source.NodeID] || !drawn[edge.Dest.NodeID] {
		t.Errorf("endpoints %q -> %q, want ids of drawn nodes %v", edge.Source.NodeID, edge.Dest.NodeID, drawn)
	}
	if len(layer.Unmapped) != 0 {
		t.Errorf("unmapped %+v", layer.Unmapped)
	}
}

// The live shape of an expression Prometheus refused: a status carrying the
// backend's words, never an error or an empty layer.
func TestTrafficCarriesARejectedExpressionVerbatim(t *testing.T) {
	f := newTrafficFixture(t, domain.MetricsQueryManual, []string{"node-a", "node-b"})
	f.traffic.err = fmt.Errorf("querying: %w: vector cannot contain metrics with the same labelset", ports.ErrMetricsQueryRejected)

	layer, err := f.service.Traffic(context.Background(), "dev", nil, true, domain.TrafficIstio, domain.TrafficWindow5m)
	if err != nil {
		t.Fatal(err)
	}
	if layer.Status != domain.BackendRejected || !strings.Contains(layer.Message, "same labelset") {
		t.Errorf("%s: %s", layer.Status, layer.Message)
	}
}

func vizBackend() domain.MetricsBackend {
	return domain.MetricsBackend{
		Kind: domain.MetricsBackendPrometheus, Namespace: "linkerd-viz",
		Service: "prometheus", Port: "admin", LinkerdViz: true,
	}
}

// linkerd-viz's Prometheus holds no cAdvisor series, so the node check can
// only answer "unverifiable" — and it is in-cluster by construction. Chosen,
// it answers Linkerd traffic rather than being refused.
func warehouseNodes() application.TrafficNodeReader {
	return stubTrafficNodes{nodes: []domain.TrafficNodeRef{
		{ID: "deploy/picker", APIKind: "Deployment", Name: "picker", Namespace: "warehouse"},
		{ID: "deploy/inventory-api", APIKind: "Deployment", Name: "inventory-api", Namespace: "warehouse"},
		{ID: "sts/orders-db", APIKind: "StatefulSet", Name: "orders-db", Namespace: "warehouse"},
	}}
}

func TestTrafficReadsLinkerdFromAChosenLinkerdVizPrometheus(t *testing.T) {
	f := newTrafficFixtureWith(t, domain.MetricsQueryManual, nil, warehouseNodes())
	f.discovery.backends = []domain.MetricsBackend{vizBackend()}
	f.traffic.answers["count(request_total"] = []domain.PromSeries{one(43, nil)}
	f.traffic.answers[`request_total{direction="outbound",dst_namespace!=""`] = []domain.PromSeries{one(3.9, map[string]string{
		"namespace": "warehouse", "deployment": "picker",
		"dst_namespace": "warehouse", "dst_deployment": "inventory-api", "dst_service": "inventory-api",
	})}

	layer, err := f.service.Traffic(context.Background(), "dev", nil, true, domain.TrafficLinkerd, domain.TrafficWindow5m)
	if err != nil {
		t.Fatal(err)
	}
	if layer.Status != domain.BackendAnswered || len(layer.Edges) != 1 {
		t.Fatalf("%s %q: %+v", layer.Status, layer.Message, layer.Edges)
	}
	if layer.Provenance.Verification != domain.VerificationUnverifiable {
		t.Errorf("provenance %+v should say the backend was not node-verified", layer.Provenance)
	}

	if !strings.Contains(layer.Message, "its answer was checked instead") {
		t.Errorf("the answer does not say how it was checked: %q", layer.Message)
	}
}

type stubNamespaces struct {
	names []string
	err   error
}

func (s stubNamespaces) ListNamespaces(_ context.Context, _ domain.ClusterID, _ domain.Projection) ([]domain.Namespace, error) {
	if s.err != nil {
		return nil, s.err
	}
	var out []domain.Namespace
	for _, name := range s.names {
		namespace, err := domain.NewNamespace(name, domain.NamespacePhaseActive, time.Now())
		if err != nil {
			return nil, err
		}
		out = append(out, namespace)
	}
	return out, nil
}

// A backend the node check could not verify (Istio's sample Prometheus with
// no node label, before the probe learned the others) is checked by what it
// answers: a namespace this cluster does not have, or workloads none of
// which are on this map, and nothing is drawn.
func TestAnUnverifiableBackendNamingAnotherClusterIsRefused(t *testing.T) {
	istioEdge := func(namespace string) []domain.PromSeries {
		return []domain.PromSeries{one(4, map[string]string{
			"source_workload": "web", "source_workload_namespace": namespace,
			"destination_workload": "api", "destination_workload_namespace": namespace,
			"destination_service_name": "api", "request_protocol": "http",
		})}
	}
	newService := func(t *testing.T, namespaces []string, reader application.TrafficNodeReader) (*application.TrafficService, *countingTraffic) {
		f := newTrafficFixtureWith(t, domain.MetricsQueryManual, nil, reader)
		service, err := application.NewTrafficService(application.TrafficServiceDeps{
			Metrics: f.metrics, Query: f.traffic, Nodes: reader, Namespaces: stubNamespaces{names: namespaces},
		})
		if err != nil {
			t.Fatal(err)
		}
		return service, f.traffic
	}

	// Foreign namespace: refused, and named.
	service, traffic := newService(t, []string{"shop"}, nil)
	traffic.answers["istio_requests_total{reporter="] = istioEdge("elsewhere")
	layer, err := service.Traffic(context.Background(), "dev", nil, true, domain.TrafficIstio, domain.TrafficWindow5m)
	if err != nil {
		t.Fatal(err)
	}
	if layer.Status != domain.BackendUnverified || !strings.Contains(layer.Message, "elsewhere") || len(layer.Edges) != 0 {
		t.Errorf("foreign namespace: %s %q", layer.Status, layer.Message)
	}

	// Our namespace, our workloads: drawn.
	service, traffic = newService(t, []string{"shop"}, stubTrafficNodes{nodes: []domain.TrafficNodeRef{
		{ID: "deploy/web", APIKind: "Deployment", Name: "web", Namespace: "shop"},
	}})
	traffic.answers["istio_requests_total{reporter="] = istioEdge("shop")
	layer, err = service.Traffic(context.Background(), "dev", nil, true, domain.TrafficIstio, domain.TrafficWindow5m)
	if err != nil {
		t.Fatal(err)
	}
	if layer.Status != domain.BackendAnswered || len(layer.Edges) != 1 {
		t.Errorf("own workloads: %s %q", layer.Status, layer.Message)
	}

	// A mismatch from the node check is still refused before anything is asked.
	f := newTrafficFixture(t, domain.MetricsQueryManual, []string{"someone-elses"})
	layer, err = f.service.Traffic(context.Background(), "dev", nil, true, domain.TrafficIstio, domain.TrafficWindow5m)
	if err != nil {
		t.Fatal(err)
	}
	if layer.Status != domain.BackendUnverified || f.traffic.queryCalls.Load() != 0 {
		t.Errorf("mismatch: %s, %d queries", layer.Status, f.traffic.queryCalls.Load())
	}
}

// When Linkerd's metrics are absent from the chosen backend but linkerd-viz's
// Prometheus was discovered, the empty state says where they are — it does
// not quietly answer from a backend nobody chose.
func TestTrafficNamesLinkerdVizWhenItIsNotChosen(t *testing.T) {
	f := newTrafficFixture(t, domain.MetricsQueryManual, []string{"node-a", "node-b"})
	f.discovery.backends = []domain.MetricsBackend{testBackend(), vizBackend()}

	sources, err := f.service.Sources(context.Background(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range sources.Sources {
		if source.Source == domain.TrafficLinkerd && (source.Available || !strings.Contains(source.Detail, "linkerd-viz")) {
			t.Errorf("linkerd: %+v", source)
		}
	}

	layer, err := f.service.Traffic(context.Background(), "dev", nil, true, domain.TrafficLinkerd, domain.TrafficWindow5m)
	if err != nil {
		t.Fatal(err)
	}
	if layer.Status != domain.BackendAnsweredEmpty || !strings.Contains(layer.Message, "Settings → Clusters") {
		t.Errorf("%s: %s", layer.Status, layer.Message)
	}
	if got := f.traffic.queryCalls.Load(); got != 0 {
		t.Errorf("%d queries sent to a backend without Linkerd's metrics", got)
	}
}

// What linkerd-viz's own Prometheus sent is a scrape, and the source names it:
// dropped by source, while a headless-Service request is kept.
func TestTrafficDropsWhatTheMonitoringBackendSent(t *testing.T) {
	f := newTrafficFixtureWith(t, domain.MetricsQueryManual, nil, warehouseNodes())
	f.discovery.backends = []domain.MetricsBackend{vizBackend()}
	f.traffic.answers["count(request_total"] = []domain.PromSeries{one(43, nil)}
	f.traffic.answers[`request_total{direction="outbound",dst_namespace!=""`] = []domain.PromSeries{
		one(0.2, map[string]string{"namespace": "linkerd-viz", "deployment": "prometheus", "dst_namespace": "warehouse", "dst_deployment": "picker"}),
		one(2, map[string]string{"namespace": "warehouse", "deployment": "picker", "dst_namespace": "warehouse", "dst_statefulset": "orders-db"}),
	}

	layer, err := f.service.Traffic(context.Background(), "dev", nil, true, domain.TrafficLinkerd, domain.TrafficWindow5m)
	if err != nil {
		t.Fatal(err)
	}
	if len(layer.Edges) != 1 || layer.Edges[0].Dest.Workload != "orders-db" {
		t.Fatalf("edges %+v", layer.Edges)
	}
}

// The picker's list is discovery and nothing else: no node probe, no query.
func TestBackendsListsDiscoveryWithoutQuerying(t *testing.T) {
	f := newTrafficFixture(t, domain.MetricsQueryManual, []string{"node-a", "node-b"})
	f.discovery.backends = []domain.MetricsBackend{testBackend(), vizBackend()}

	candidates, err := f.metrics.Backends(context.Background(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].Rank != 0 || candidates[1].Rank != 1 ||
		!strings.Contains(candidates[1].Detail, "linkerd-viz") || candidates[0].Verification != "" {
		t.Fatalf("%+v", candidates)
	}
	if calls := f.query.nodeCalls.Load() + f.traffic.probeCalls.Load() + f.traffic.queryCalls.Load(); calls != 0 {
		t.Fatalf("%d requests to list candidates", calls)
	}
}

type failingTrafficNodes struct{}

func (failingTrafficNodes) TrafficNodes(context.Context, domain.ClusterID, []domain.NamespaceName, bool) ([]domain.TrafficNodeRef, error) {
	return nil, fmt.Errorf("listing: %w", ports.ErrForbidden)
}

// BOTH evidence checks must RUN for an unverifiable backend: one that cannot
// is a refusal naming it, never a pass by default.
func TestEvidenceThatCannotBeGatheredRefuses(t *testing.T) {
	edge := []domain.PromSeries{one(4, map[string]string{
		"namespace": "warehouse", "deployment": "picker",
		"dst_namespace": "warehouse", "dst_deployment": "inventory-api", "dst_service": "inventory-api",
	})}
	cases := map[string]struct {
		namespaces application.TrafficNamespaceReader
		nodes      application.TrafficNodeReader
		want       string
	}{
		"namespaces refused":  {stubNamespaces{err: fmt.Errorf("x: %w", ports.ErrForbidden)}, warehouseNodes(), "list this cluster's namespaces"},
		"no namespace reader": {nil, warehouseNodes(), "list this cluster's namespaces"},
		"topology failed":     {stubNamespaces{names: []string{"warehouse"}}, failingTrafficNodes{}, "read this cluster's workloads"},
		"no topology reader":  {stubNamespaces{names: []string{"warehouse"}}, nil, "read this cluster's workloads"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newTrafficFixtureWith(t, domain.MetricsQueryManual, nil, nil)
			f.discovery.backends = []domain.MetricsBackend{vizBackend()}
			f.traffic.answers["count(request_total"] = []domain.PromSeries{one(43, nil)}
			f.traffic.answers[`request_total{direction="outbound",dst_namespace!=""`] = edge
			service, err := application.NewTrafficService(application.TrafficServiceDeps{
				Metrics: f.metrics, Query: f.traffic, Nodes: c.nodes, Namespaces: c.namespaces,
			})
			if err != nil {
				t.Fatal(err)
			}
			layer, err := service.Traffic(context.Background(), "dev", nil, true, domain.TrafficLinkerd, domain.TrafficWindow5m)
			if err != nil {
				t.Fatal(err)
			}
			if layer.Status != domain.BackendUnverified || !strings.Contains(layer.Message, c.want) || len(layer.Edges) != 0 {
				t.Errorf("%s %q", layer.Status, layer.Message)
			}
		})
	}
}

// Every gesture is one batch, ended on return.
func TestEveryTrafficCallIsOneBatch(t *testing.T) {
	f := newTrafficFixture(t, domain.MetricsQueryManual, []string{"node-a", "node-b"})
	_, _ = f.service.Sources(context.Background(), "dev")
	_, _ = f.service.Traffic(context.Background(), "dev", nil, true, domain.TrafficIstio, domain.TrafficWindow5m)
	if f.traffic.batches.Load() != 2 || f.traffic.ended.Load() != 2 {
		t.Fatalf("batches %d, ended %d", f.traffic.batches.Load(), f.traffic.ended.Load())
	}
}
