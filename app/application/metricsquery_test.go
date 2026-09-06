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

// countingQuery records every request that actually leaves.
//
// THE UPDATE CHECK'S OWN SHAPE. Asserting the returned status is not enough
// for a feature whose whole promise is that nothing is sent — the question is
// whether the request was made, so this counts calls the way
// countingSource does in updates_test.go.
type countingQuery struct {
	nodeCalls  atomic.Int32
	rangeCalls atomic.Int32

	// GUARDED, because two of the tests below drive this fake from several
	// goroutines at once — which is the whole point of them, and which a
	// plainly-written fake turns into a race in the test rather than a
	// finding about the code.
	mu          sync.Mutex
	nodes       []string
	nodesErr    error
	beforeNodes func()
	series      []domain.PromSeries
	rangeErr    error
}

// answerNodes reads what the fake should say, under the lock.
func (q *countingQuery) answerNodes() ([]string, func(), error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	return append([]string(nil), q.nodes...), q.beforeNodes, q.nodesErr
}

// set changes what the fake says, under the lock.
func (q *countingQuery) set(change func(*countingQuery)) {
	q.mu.Lock()
	defer q.mu.Unlock()

	change(q)
}

func (q *countingQuery) QueryNodes(context.Context, domain.ClusterID, domain.MetricsBackend) ([]string, error) {
	q.nodeCalls.Add(1)

	nodes, before, err := q.answerNodes()
	if before != nil {
		// A hook rather than a sleep, so the concurrency tests below drive
		// the window they are about rather than hoping for it.
		before()
	}
	return nodes, err
}

func (q *countingQuery) QueryRange(
	_ context.Context,
	_ domain.ClusterID,
	_ domain.MetricsBackend,
	_ string,
	_, _ time.Time,
	_ time.Duration,
) ([]domain.PromSeries, error) {
	q.rangeCalls.Add(1)

	q.mu.Lock()
	defer q.mu.Unlock()

	return q.series, q.rangeErr
}

type stubSettings struct {
	settings domain.ClusterSettings
	err      error
}

func (s stubSettings) Cluster(context.Context, domain.ClusterID) (domain.ClusterSettings, error) {
	return s.settings, s.err
}

type stubDiscovery struct {
	backends []domain.MetricsBackend
	calls    atomic.Int32
	err      error
}

func (d *stubDiscovery) ListMetricsBackends(context.Context, domain.ClusterID) ([]domain.MetricsBackend, error) {
	d.calls.Add(1)
	return d.backends, d.err
}

type stubNodes struct {
	names []string
	calls atomic.Int32
	err   error
}

func (n *stubNodes) ListNodes(_ context.Context, id domain.ClusterID, _ domain.Projection) ([]domain.Node, error) {
	n.calls.Add(1)
	if n.err != nil {
		return nil, n.err
	}

	nodes := make([]domain.Node, 0, len(n.names))
	for _, name := range n.names {
		node, err := domain.NewNode(domain.NodeSpec{Name: name, ClusterID: id, Ready: true})
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func testBackend() domain.MetricsBackend {
	return domain.MetricsBackend{
		Kind:      domain.MetricsBackendPrometheus,
		Namespace: "monitoring",
		Service:   "prometheus-operated",
		Port:      "web",
	}
}

func points(n int) []domain.PromSeries {
	now := time.Now().UTC()
	series := make([]domain.SeriesPoint, 0, n)
	for i := range n {
		series = append(series, domain.SeriesPoint{At: now.Add(time.Duration(-i) * time.Minute), Value: float64(i)})
	}
	return []domain.PromSeries{{Points: series}}
}

type queryFixture struct {
	service   *application.MetricsQueryService
	query     *countingQuery
	discovery *stubDiscovery
	nodes     *stubNodes
}

func newQueryFixture(t *testing.T, settings domain.MetricsQuerySettings, opts ...func(*queryFixture)) queryFixture {
	t.Helper()

	fixture := queryFixture{
		query:     &countingQuery{nodes: []string{"node-a", "node-b"}, series: points(3)},
		discovery: &stubDiscovery{backends: []domain.MetricsBackend{testBackend()}},
		nodes:     &stubNodes{names: []string{"node-a", "node-b"}},
	}
	for _, opt := range opts {
		opt(&fixture)
	}

	service, err := application.NewMetricsQueryService(application.MetricsQueryServiceDeps{
		Settings:  stubSettings{settings: domain.ClusterSettings{MetricsQuery: settings}},
		Discovery: fixture.discovery,
		Query:     fixture.query,
		Nodes:     fixture.nodes,
	})
	if err != nil {
		t.Fatalf("wiring: %v", err)
	}
	fixture.service = service
	return fixture
}

func enabled() domain.MetricsQuerySettings {
	return domain.MetricsQuerySettings{Mode: domain.MetricsQueryAuto, Fleet: domain.FleetFilter}
}

// A CLUSTER NOBODY SWITCHED THIS ON FOR BEHAVES EXACTLY AS IT DID BEFORE THE
// FEATURE EXISTED, and it is asserted by counting rather than by reading the
// status: an opt-out that ships without that assertion is precisely what has
// silently broken elsewhere.
func TestOffSendsNothingAtAll(t *testing.T) {
	for _, mode := range []domain.MetricsQueryMode{domain.MetricsQueryOff, ""} {
		fixture := newQueryFixture(t, domain.MetricsQuerySettings{Mode: mode, Fleet: domain.FleetFilter})

		result, err := fixture.service.Series(context.Background(), "dev",
			domain.MetricCPU, domain.ScopeCluster, time.Hour)
		if err != nil {
			t.Fatalf("%q: %v", mode, err)
		}

		if result.Status != domain.BackendNotEnabled {
			t.Fatalf("%q: status %q, want not-enabled", mode, result.Status)
		}
		if calls := fixture.query.nodeCalls.Load() + fixture.query.rangeCalls.Load(); calls != 0 {
			t.Fatalf("%q: %d requests left for a cluster with this off", mode, calls)
		}
		if calls := fixture.discovery.calls.Load(); calls != 0 {
			t.Fatalf("%q: discovery ran %d times for a cluster with this off", mode, calls)
		}
		if calls := fixture.nodes.calls.Load(); calls != 0 {
			t.Fatalf("%q: the node list ran %d times for a cluster with this off", mode, calls)
		}
	}
}

func TestAVerifiedBackendIsDrawnUnnarrowed(t *testing.T) {
	fixture := newQueryFixture(t, enabled())

	result, err := fixture.service.Series(context.Background(), "dev",
		domain.MetricCPU, domain.ScopeCluster, time.Hour)
	if err != nil {
		t.Fatalf("querying: %v", err)
	}

	if result.Status != domain.BackendAnswered {
		t.Fatalf("status %q (%s), want answered", result.Status, result.Message)
	}
	if result.Provenance.Verification != domain.VerificationVerified {
		t.Fatalf("verification %q, want verified", result.Provenance.Verification)
	}
	if result.Provenance.Filtered {
		t.Fatal("a verified backend's query was narrowed")
	}
	if strings.Contains(result.Expression, "node=~") {
		t.Fatalf("a verified backend's expression carries a node filter: %s", result.Expression)
	}
	if result.Provenance.Origin != domain.OriginBackend {
		t.Fatalf("origin %q, want backend", result.Provenance.Origin)
	}
}

// A backend holding other clusters besides this one is the Thanos case, and
// under the filter policy every expression is narrowed to this cluster's own
// nodes so the sum is about this cluster by construction.
func TestAFleetBackendIsNarrowedUnderTheFilterPolicy(t *testing.T) {
	fixture := newQueryFixture(t, enabled(), func(f *queryFixture) {
		f.query.nodes = []string{"node-a", "node-b", "prod-1", "prod-2"}
	})

	result, err := fixture.service.Series(context.Background(), "dev",
		domain.MetricMemory, domain.ScopeCluster, time.Hour)
	if err != nil {
		t.Fatalf("querying: %v", err)
	}

	if result.Status != domain.BackendAnswered {
		t.Fatalf("status %q (%s), want answered", result.Status, result.Message)
	}
	if result.Provenance.Verification != domain.VerificationFleet {
		t.Fatalf("verification %q, want fleet", result.Provenance.Verification)
	}
	if !result.Provenance.Filtered {
		t.Fatal("a fleet backend's series does not say it was narrowed")
	}
	if !strings.Contains(result.Expression, `node=~"node-a|node-b"`) {
		t.Fatalf("the expression was not narrowed to this cluster's nodes: %s", result.Expression)
	}
	if strings.Contains(result.Expression, "prod-1") {
		t.Fatalf("the filter carried another cluster's nodes: %s", result.Expression)
	}
}

// The other half of the policy: an operator who would rather have no number
// than one narrowed by a filter PodSteer composed.
func TestAFleetBackendIsRefusedUnderTheRefusePolicy(t *testing.T) {
	fixture := newQueryFixture(t,
		domain.MetricsQuerySettings{Mode: domain.MetricsQueryAuto, Fleet: domain.FleetRefuse},
		func(f *queryFixture) { f.query.nodes = []string{"node-a", "prod-1"} })

	result, err := fixture.service.Series(context.Background(), "dev",
		domain.MetricCPU, domain.ScopeCluster, time.Hour)
	if err != nil {
		t.Fatalf("querying: %v", err)
	}

	if result.Status != domain.BackendUnverified {
		t.Fatalf("status %q, want unverified", result.Status)
	}
	if result.Message == "" {
		t.Fatal("the refusal says nothing")
	}
	if calls := fixture.query.rangeCalls.Load(); calls != 0 {
		t.Fatalf("%d range queries were sent for a refused aggregate", calls)
	}
}

// A mismatched or unverifiable backend is never sent a range query at all:
// there is nothing that could be done with the answer.
func TestNoAggregateIsDrawnOrAskedForWhenTheCheckFails(t *testing.T) {
	cases := map[string][]string{
		"mismatch":     {"prod-1", "prod-2"},
		"unverifiable": nil,
	}

	for name, backendNodes := range cases {
		t.Run(name, func(t *testing.T) {
			fixture := newQueryFixture(t, enabled(), func(f *queryFixture) { f.query.nodes = backendNodes })

			result, err := fixture.service.Series(context.Background(), "dev",
				domain.MetricCPU, domain.ScopeCluster, time.Hour)
			if err != nil {
				t.Fatalf("querying: %v", err)
			}

			if result.Status != domain.BackendUnverified {
				t.Fatalf("status %q, want unverified", result.Status)
			}
			if calls := fixture.query.rangeCalls.Load(); calls != 0 {
				t.Fatalf("%d range queries were sent for an %s backend", calls, name)
			}
			if !strings.Contains(result.Message, "monitoring") {
				t.Fatalf("the refusal does not name the backend: %s", result.Message)
			}
		})
	}
}

// ON A FLEET BACKEND THE PROBE TOUCHES EVERY CONTAINER SERIES THE QUERIER
// FRONTS, so it is cached per cluster and backend — and the range query still
// goes out each time, because that is what was asked for.
func TestTheVerificationIsCachedPerClusterAndBackend(t *testing.T) {
	fixture := newQueryFixture(t, enabled())

	for range 4 {
		if _, err := fixture.service.Series(context.Background(), "dev",
			domain.MetricCPU, domain.ScopeCluster, time.Hour); err != nil {
			t.Fatalf("querying: %v", err)
		}
	}

	if calls := fixture.query.nodeCalls.Load(); calls != 1 {
		t.Fatalf("the node probe ran %d times, want 1", calls)
	}
	if calls := fixture.nodes.calls.Load(); calls != 1 {
		t.Fatalf("the cluster node list ran %d times, want 1", calls)
	}
	if calls := fixture.query.rangeCalls.Load(); calls != 4 {
		t.Fatalf("%d range queries, want 4 — the answer, not the check, is what was asked for", calls)
	}
}

// A tab is routinely reconnected because its kubeconfig context now names a
// different cluster, and a verification carried across that would license an
// aggregate checked against nodes this connection has never seen.
func TestInvalidatingAClusterDropsItsVerification(t *testing.T) {
	fixture := newQueryFixture(t, enabled())

	if _, err := fixture.service.Series(context.Background(), "dev",
		domain.MetricCPU, domain.ScopeCluster, time.Hour); err != nil {
		t.Fatalf("querying: %v", err)
	}
	fixture.service.Invalidate("dev")
	if _, err := fixture.service.Series(context.Background(), "dev",
		domain.MetricCPU, domain.ScopeCluster, time.Hour); err != nil {
		t.Fatalf("querying: %v", err)
	}

	if calls := fixture.query.nodeCalls.Load(); calls != 2 {
		t.Fatalf("the node probe ran %d times across an invalidation, want 2", calls)
	}
}

// A different backend is a different system, so its verification is not the
// one just made about the other.
func TestChangingTheChosenBackendReVerifies(t *testing.T) {
	other := domain.MetricsBackend{
		Kind: domain.MetricsBackendVictoriaMetrics, Namespace: "vm",
		Service: "vmselect", Port: "http", Prefix: "/select/0/prometheus",
	}

	query := &countingQuery{nodes: []string{"node-a"}, series: points(2)}
	discovery := &stubDiscovery{backends: []domain.MetricsBackend{testBackend(), other}}
	nodes := &stubNodes{names: []string{"node-a"}}

	first, err := application.NewMetricsQueryService(application.MetricsQueryServiceDeps{
		Settings:  stubSettings{settings: domain.ClusterSettings{MetricsQuery: enabled()}},
		Discovery: discovery, Query: query, Nodes: nodes,
	})
	if err != nil {
		t.Fatalf("wiring: %v", err)
	}
	if _, err := first.Series(context.Background(), "dev", domain.MetricCPU, domain.ScopeCluster, time.Hour); err != nil {
		t.Fatalf("querying: %v", err)
	}

	// The same service, now pointed at the other candidate.
	picked := enabled()
	picked.Preferred = domain.PreferredBackend{Namespace: "vm", Service: "vmselect"}
	second, err := application.NewMetricsQueryService(application.MetricsQueryServiceDeps{
		Settings:  stubSettings{settings: domain.ClusterSettings{MetricsQuery: picked}},
		Discovery: discovery, Query: query, Nodes: nodes,
	})
	if err != nil {
		t.Fatalf("wiring: %v", err)
	}
	if _, err := second.Series(context.Background(), "dev", domain.MetricCPU, domain.ScopeCluster, time.Hour); err != nil {
		t.Fatalf("querying: %v", err)
	}

	if calls := query.nodeCalls.Load(); calls != 2 {
		t.Fatalf("the node probe ran %d times across two backends, want 2", calls)
	}
}

// SILENTLY ANSWERING FROM A DIFFERENT PROMETHEUS THAN THE ONE THAT WAS CHOSEN
// is the one behaviour ADR 7 rules out by name.
func TestAChosenBackendThatIsGoneIsNotReplaced(t *testing.T) {
	picked := enabled()
	picked.Preferred = domain.PreferredBackend{Namespace: "observability", Service: "thanos-query"}

	fixture := newQueryFixture(t, picked)

	result, err := fixture.service.Series(context.Background(), "dev",
		domain.MetricCPU, domain.ScopeCluster, time.Hour)
	if err != nil {
		t.Fatalf("querying: %v", err)
	}

	if result.Status != domain.BackendNothingDiscovered {
		t.Fatalf("status %q, want nothing-discovered", result.Status)
	}
	if !strings.Contains(result.Message, "thanos-query") {
		t.Fatalf("the message does not name the backend that is gone: %s", result.Message)
	}
	if calls := fixture.query.rangeCalls.Load(); calls != 0 {
		t.Fatalf("%d queries went to a backend that was not chosen", calls)
	}
}

func TestNothingDiscoveredIsNotAnError(t *testing.T) {
	fixture := newQueryFixture(t, enabled(), func(f *queryFixture) { f.discovery.backends = nil })

	result, err := fixture.service.Series(context.Background(), "dev",
		domain.MetricCPU, domain.ScopeCluster, time.Hour)
	if err != nil {
		t.Fatalf("a cluster with no monitoring stack failed: %v", err)
	}
	if result.Status != domain.BackendNothingDiscovered {
		t.Fatalf("status %q, want nothing-discovered", result.Status)
	}
}

// An account that may not proxy is common and is not a fault. It reads as
// "your account cannot reach the monitoring stack from here".
func TestARefusedProxyIsAStatusRatherThanAnError(t *testing.T) {
	fixture := newQueryFixture(t, enabled(), func(f *queryFixture) {
		f.query.nodesErr = ports.ErrForbidden
	})

	result, err := fixture.service.Series(context.Background(), "dev",
		domain.MetricCPU, domain.ScopeCluster, time.Hour)
	if err != nil {
		t.Fatalf("a refusal was returned as an error: %v", err)
	}
	if result.Status != domain.BackendForbidden {
		t.Fatalf("status %q, want forbidden", result.Status)
	}
	if !strings.Contains(result.Message, "services/proxy") {
		t.Fatalf("the message does not name the permission: %s", result.Message)
	}
}

// A Prometheus error comes back with its own message verbatim, the way a
// rejected manifest carries the API server's.
func TestARejectedExpressionCarriesTheBackendsMessage(t *testing.T) {
	fixture := newQueryFixture(t, enabled(), func(f *queryFixture) {
		// Wrapped exactly as the adapter wraps it, so what is asserted is the
		// unwrapping this layer does rather than a fixture shaped to suit it.
		f.query.rangeErr = fmt.Errorf(`querying Prometheus in monitoring in "dev": %w: %s`,
			ports.ErrMetricsQueryRejected,
			`invalid parameter "query": 1:8: parse error: unexpected "{"`)
	})

	result, err := fixture.service.Series(context.Background(), "dev",
		domain.MetricCPU, domain.ScopeCluster, time.Hour)
	if err != nil {
		t.Fatalf("a rejection was returned as an error: %v", err)
	}
	if result.Status != domain.BackendRejected {
		t.Fatalf("status %q, want rejected", result.Status)
	}
	if result.Message != `invalid parameter "query": 1:8: parse error: unexpected "{"` {
		t.Fatalf("the message was not carried verbatim: %q", result.Message)
	}
}

// AN ACCOUNT THAT MAY PROXY BUT MAY NOT LIST NODES IS THE NAMESPACE-SCOPED
// OPERATOR THIS PROJECT IS BUILT FOR. Reporting that as a refusal to proxy
// sends them to ask for a permission they already hold, about a Service
// nobody asked about — so a cluster whose nodes could not be listed is
// UNVERIFIABLE, which is what VerifyBackendNodes already answers for an empty
// set.
func TestANodeListRefusalIsUnverifiableRatherThanForbidden(t *testing.T) {
	fixture := newQueryFixture(t, enabled(), func(f *queryFixture) {
		f.nodes.err = ports.ErrForbidden
	})

	result, err := fixture.service.Series(context.Background(), "dev",
		domain.MetricCPU, domain.ScopeCluster, time.Hour)
	if err != nil {
		t.Fatalf("querying: %v", err)
	}

	if result.Status == domain.BackendForbidden {
		t.Fatalf("a node-list refusal was reported as a refusal to proxy: %s", result.Message)
	}
	if result.Status != domain.BackendUnverified {
		t.Fatalf("status %q, want unverified", result.Status)
	}
	if result.Provenance.Verification != domain.VerificationUnverifiable {
		t.Fatalf("verification %q, want unverifiable", result.Provenance.Verification)
	}
	if strings.Contains(result.Message, "services/proxy") {
		t.Fatalf("the message names a permission that is not the problem: %s", result.Message)
	}

	// AND NOTHING IS ASKED OF THE BACKEND. With no node set of ours to
	// compare against the answer is unverifiable whatever it says, so the
	// probe is a request that cannot change the outcome.
	if calls := fixture.query.nodeCalls.Load(); calls != 0 {
		t.Fatalf("the backend was probed %d times with nothing to compare against", calls)
	}
	if calls := fixture.query.rangeCalls.Load(); calls != 0 {
		t.Fatalf("%d range queries went out for a chart that will not be drawn", calls)
	}
}

// A node list can fail transiently, and half an hour of no chart is the wrong
// price for a timeout — so that outcome is not cached.
func TestANodeListFailureIsNotCached(t *testing.T) {
	fixture := newQueryFixture(t, enabled(), func(f *queryFixture) {
		f.nodes.err = ports.ErrUnreachable
	})

	for range 3 {
		if _, err := fixture.service.Series(context.Background(), "dev",
			domain.MetricCPU, domain.ScopeCluster, time.Hour); err != nil {
			t.Fatalf("querying: %v", err)
		}
	}

	if calls := fixture.nodes.calls.Load(); calls != 3 {
		t.Fatalf("the node list was tried %d times, want 3 — a transient failure was cached", calls)
	}
}

// A BACKEND BEHIND ITS OWN AUTHENTICATION gets its own sentence, because
// neither of the other two is true: the account's permissions are fine and
// the expression is fine.
func TestABackendNeedingItsOwnCredentialSaysSo(t *testing.T) {
	fixture := newQueryFixture(t, enabled(), func(f *queryFixture) {
		f.query.nodesErr = fmt.Errorf("querying: %w: it answered HTTP 401", ports.ErrMetricsBackendAuth)
	})

	result, err := fixture.service.Series(context.Background(), "dev",
		domain.MetricCPU, domain.ScopeCluster, time.Hour)
	if err != nil {
		t.Fatalf("querying: %v", err)
	}

	if result.Status != domain.BackendNeedsCredential {
		t.Fatalf("status %q, want needs-credential", result.Status)
	}
	if strings.Contains(result.Message, "services/proxy") {
		t.Fatalf("the message names a Kubernetes permission that is not the problem: %s", result.Message)
	}
	if !strings.Contains(result.Message, "credential") {
		t.Fatalf("the message does not say what is wanted: %s", result.Message)
	}
}

// THE PROBE IS THE MOST EXPENSIVE QUERY THIS FEATURE MAKES, so a backend slow
// enough to fail once is not re-asked on the next range change — which under
// `auto` would otherwise re-run it and wait the full timeout again.
func TestAFailedProbeIsNotRepeatedImmediately(t *testing.T) {
	fixture := newQueryFixture(t, enabled(), func(f *queryFixture) {
		f.query.nodesErr = ports.ErrUnreachable
	})

	for range 4 {
		result, err := fixture.service.Series(context.Background(), "dev",
			domain.MetricCPU, domain.ScopeCluster, time.Hour)
		if err != nil {
			t.Fatalf("querying: %v", err)
		}
		if result.Status != domain.BackendUnreachable {
			t.Fatalf("status %q, want unreachable", result.Status)
		}
	}

	if calls := fixture.query.nodeCalls.Load(); calls != 1 {
		t.Fatalf("the probe ran %d times, want 1 — a failure is remembered briefly", calls)
	}
}

// TWO CHARTS OPENING TOGETHER MUST NOT SEND TWO PROBES. Against a cold cache
// that is two of the most costly request this feature has, to somebody's
// production Prometheus, for one answer.
func TestConcurrentChartsShareOneProbe(t *testing.T) {
	release := make(chan struct{})
	fixture := newQueryFixture(t, enabled(), func(f *queryFixture) {
		f.query.beforeNodes = func() { <-release }
	})

	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, _ = fixture.service.Series(context.Background(), "dev",
				domain.MetricCPU, domain.ScopeCluster, time.Hour)
		}()
	}

	// Let them all pile up on the probe before any of them may finish.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wait.Wait()

	if calls := fixture.query.nodeCalls.Load(); calls != 1 {
		t.Fatalf("the probe ran %d times for eight concurrent charts, want 1", calls)
	}
}

// A VERIFICATION IN FLIGHT CAN FINISH AFTER Invalidate, and what it learned is
// a node set belonging to the cluster this tab has left — which, cached, would
// license a narrowed sum over nodes the new connection has never seen.
func TestAVerificationWrittenAgainstAReplacedConnectionIsInert(t *testing.T) {
	invalidated := make(chan struct{})
	proceed := make(chan struct{})

	fixture := newQueryFixture(t, enabled(), func(f *queryFixture) {
		f.query.nodes = []string{"old-node-a", "old-node-b"}
		f.query.beforeNodes = func() {
			close(invalidated)
			<-proceed
		}
	})

	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		_, _ = fixture.service.Series(context.Background(), "dev",
			domain.MetricCPU, domain.ScopeCluster, time.Hour)
	}()

	<-invalidated
	fixture.service.Invalidate("dev")
	close(proceed)
	wait.Wait()

	// The next read must re-verify rather than inherit what the old
	// connection learned.
	fixture.query.set(func(q *countingQuery) {
		q.beforeNodes = nil
		q.nodes = []string{"node-a", "node-b"}
	})
	result, err := fixture.service.Series(context.Background(), "dev",
		domain.MetricCPU, domain.ScopeCluster, time.Hour)
	if err != nil {
		t.Fatalf("querying: %v", err)
	}

	if calls := fixture.query.nodeCalls.Load(); calls != 2 {
		t.Fatalf("the probe ran %d times, want 2 — the stale write was inherited", calls)
	}
	if strings.Contains(result.Expression, "old-node") {
		t.Fatalf("the old connection's nodes reached the new one's query: %s", result.Expression)
	}
}

func TestAnOverSizedAnswerHasItsOwnStatus(t *testing.T) {
	fixture := newQueryFixture(t, enabled(), func(f *queryFixture) {
		f.query.rangeErr = ports.ErrMetricsQueryTooLarge
	})

	result, err := fixture.service.Series(context.Background(), "dev",
		domain.MetricCPU, domain.ScopeCluster, time.Hour)
	if err != nil {
		t.Fatalf("an over-sized answer was returned as an error: %v", err)
	}
	if result.Status != domain.BackendTooLarge {
		t.Fatalf("status %q, want too-large", result.Status)
	}
}

// A backend that answers 200 with nothing is a Prometheus that does not
// scrape kubelets, and it must not sit under a green label.
func TestAnEmptyAnswerReachesTheCallerAsAnsweredEmpty(t *testing.T) {
	fixture := newQueryFixture(t, enabled(), func(f *queryFixture) { f.query.series = nil })

	result, err := fixture.service.Series(context.Background(), "dev",
		domain.MetricCPU, domain.ScopeCluster, time.Hour)
	if err != nil {
		t.Fatalf("querying: %v", err)
	}
	if result.Status != domain.BackendAnsweredEmpty {
		t.Fatalf("status %q, want answered-empty", result.Status)
	}
	if result.Message == "" {
		t.Fatal("an empty answer says nothing about why")
	}
}

// A fleet backend on a very large cluster is narrowed by a filter PodSteer
// composes, and past the URL budget the honest answer is a sentence rather
// than a request the path will reject.
func TestAnUnnarrowableFleetIsRefusedWithASentence(t *testing.T) {
	names := make([]string, 0, 400)
	for i := range 400 {
		names = append(names, "ip-10-0-"+string(rune('a'+i%26))+"-"+strings.Repeat("x", 20)+"-"+itoa(i)+".eu-west-1.compute.internal")
	}

	fixture := newQueryFixture(t, enabled(), func(f *queryFixture) {
		f.nodes.names = names
		f.query.nodes = append(append([]string{}, names...), "stranger-1")
	})

	result, err := fixture.service.Series(context.Background(), "dev",
		domain.MetricCPU, domain.ScopeCluster, time.Hour)
	if err != nil {
		t.Fatalf("querying: %v", err)
	}

	if result.Status != domain.BackendUnverified {
		t.Fatalf("status %q, want unverified", result.Status)
	}
	if !strings.Contains(result.Message, "URL") {
		t.Fatalf("the refusal does not say why: %s", result.Message)
	}
	if calls := fixture.query.rangeCalls.Load(); calls != 0 {
		t.Fatalf("%d over-long queries were sent", calls)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}
