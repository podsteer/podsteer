package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// The topology's traffic layer: observed traffic between workloads, read
// from the monitoring backend the metrics-query feature already chose. ADR 7
// governs all of it — off until the cluster's metrics query is switched on,
// sent only when somebody asks, every expression from a fixed table.
//
// IT RIDES ON MetricsQueryService RATHER THAN BESIDE IT. The setting, the
// chosen backend, the node-set verification and the sentences for a refusal
// are that service's, and a second copy of any of them is a second place for
// "which Prometheus answers" to drift. So this service holds it and calls
// the same unexported steps its Series does.

// trafficProbeTTL is how long a source-presence answer stands. Half an hour,
// the discovery and verification cache beside it: a mesh is not installed
// between two clicks, and the probes count every series of each signature
// metric, which is not free on a large backend.
const trafficProbeTTL = 30 * time.Minute

// trafficProbeTimeout bounds the shared probe, which runs detached from any
// one caller: five instant queries, each already bounded by the adapter.
const trafficProbeTimeout = 2 * time.Minute

// TrafficNodeReader supplies the topology nodes endpoints are attached to.
//
// OPTIONAL. Without one, every edge is still drawn with its namespace and
// workload names and the interface attaches them itself; with one, the
// backend resolves NodeID and lists the endpoints it could not attach.
type TrafficNodeReader interface {
	TrafficNodes(ctx context.Context, id domain.ClusterID, namespaces []domain.NamespaceName, all bool) ([]domain.TrafficNodeRef, error)
}

// TrafficServiceDeps are what the service is built from.
type TrafficServiceDeps struct {
	Metrics *MetricsQueryService
	Query   ports.TrafficQueryPort
	Nodes   TrafficNodeReader
	Logger  *slog.Logger
}

// TrafficService answers ports.TrafficUseCase.
type TrafficService struct {
	metrics *MetricsQueryService
	query   ports.TrafficQueryPort
	nodes   TrafficNodeReader
	logger  *slog.Logger

	probes  probeCache
	probing singleflight.Group
}

var _ ports.TrafficUseCase = (*TrafficService)(nil)

// NewTrafficService wires the service.
func NewTrafficService(deps TrafficServiceDeps) (*TrafficService, error) {
	switch {
	case deps.Metrics == nil:
		return nil, errors.New("application: TrafficService requires the MetricsQueryService")
	case deps.Query == nil:
		return nil, errors.New("application: TrafficService requires a TrafficQueryPort")
	}

	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &TrafficService{
		metrics: deps.Metrics,
		query:   deps.Query,
		nodes:   deps.Nodes,
		logger:  logger.With(slog.String("service", "traffic")),
	}, nil
}

// trafficGate is the outcome of the checks every request makes first: either
// a backend that may be asked, or a status saying why not.
type trafficGate struct {
	backend      domain.MetricsBackend
	verification domain.BackendVerification
	refused      bool
	status       domain.BackendStatus
	message      string
	provenance   domain.SeriesProvenance
}

// gate runs the metrics-query feature's own checks in its own order: the
// setting first (a cluster nobody switched on sends nothing at all), then the
// chosen backend, then the node-set verification.
func (s *TrafficService) gate(ctx context.Context, id domain.ClusterID) (trafficGate, error) {
	settings, err := s.metrics.settings.Cluster(ctx, id)
	if err != nil {
		return trafficGate{}, fmt.Errorf("reading the metrics-query setting for %q: %w", id, err)
	}

	query := settings.MetricsQuery
	if query.Mode == "" || query.Mode == domain.MetricsQueryOff {
		off := domain.NotEnabled()
		return trafficGate{refused: true, status: off.Status, message: off.Message}, nil
	}

	backend, found, err := s.metrics.chosenBackend(ctx, id, query.Preferred)
	if err != nil {
		return trafficGate{}, err
	}
	if !found {
		none := s.metrics.nothingDiscovered(query.Preferred)
		return trafficGate{refused: true, status: none.Status, message: none.Message}, nil
	}

	generation := s.metrics.verifications.generation(id)
	verification, _, err := s.metrics.verify(ctx, id, generation, backend)
	if err != nil {
		failed := s.metrics.failed(backend, err)
		return trafficGate{refused: true, status: failed.Status, message: failed.Message, provenance: failed.Provenance}, nil
	}

	// LINKERD-VIZ'S PROMETHEUS CANNOT BE VERIFIED AND NEEDS NOT BE. The node
	// check reads cAdvisor series, which it never scrapes, so it always
	// answers "unverifiable" — and it is in-cluster by construction: it is
	// installed by the extension into this cluster and discovers only this
	// cluster's proxies. A fleet or mismatch answer is still refused.
	vizAccepted := backend.LinkerdViz && verification == domain.VerificationUnverifiable
	if verification != domain.VerificationVerified && !vizAccepted {
		// A FLEET BACKEND IS REFUSED WHATEVER THE FLEET SETTING SAYS. The
		// chart narrows a fleet backend to this cluster's node names; traffic
		// is grouped by workload, not node, so there is no matcher that makes
		// the answer about this cluster — a workload of the same name in
		// another cluster would be summed in here.
		refusal := domain.UnverifiedResult(backend, verification)
		message := refusal.Message
		if verification == domain.VerificationFleet {
			message = fmt.Sprintf(
				"%s holds more than this cluster. Traffic is grouped by workload rather than by node, so it cannot be narrowed to this cluster, and a workload of the same name elsewhere would be counted here. Nothing is drawn; a backend that holds only this cluster can be chosen under Settings → Clusters.",
				backend.Describe())
		}
		return trafficGate{refused: true, status: refusal.Status, message: message, provenance: refusal.Provenance}, nil
	}

	return trafficGate{
		backend:      backend,
		verification: verification,
		provenance: domain.SeriesProvenance{
			Origin:       domain.OriginBackend,
			Source:       backend.Describe(),
			Verification: verification,
		},
	}, nil
}

// Sources says which traffic sources the chosen backend holds.
func (s *TrafficService) Sources(ctx context.Context, id domain.ClusterID) (domain.TrafficSources, error) {
	gate, err := s.gate(ctx, id)
	if err != nil {
		return domain.TrafficSources{}, err
	}
	if gate.refused {
		return domain.TrafficSources{
			Backend: gate.provenance.Source,
			Sources: unprobed(),
			Status:  gate.status,
			Message: gate.message,
		}, nil
	}

	found, err := s.probe(ctx, id, gate.backend)
	if err != nil {
		failed := s.metrics.failed(gate.backend, err)
		return domain.TrafficSources{
			Backend: gate.backend.Describe(),
			Sources: unprobed(),
			Status:  failed.Status,
			Message: failed.Message,
		}, nil
	}

	answer := domain.TrafficSources{Backend: gate.backend.Describe(), Status: domain.BackendAnswered}
	anyFound := false
	viz := s.linkerdVizElsewhere(ctx, id, gate.backend)
	for _, source := range domain.TrafficSourceNames() {
		count := found[source]
		status := domain.TrafficSourceStatus{Source: source, Available: count > 0}
		if count <= 0 && source == domain.TrafficLinkerd && viz != "" {
			status.Detail = viz
			answer.Sources = append(answer.Sources, status)
			continue
		}
		if count > 0 {
			anyFound = true
			status.Detail = fmt.Sprintf("%s series found in %s.", strconv.FormatFloat(count, 'f', -1, 64), gate.backend.Describe())
		} else {
			status.Detail = domain.TrafficRequirement(source)
		}
		answer.Sources = append(answer.Sources, status)
	}
	if !anyFound {
		answer.Status = domain.BackendAnsweredEmpty
		answer.Message = fmt.Sprintf(
			"%s holds none of the metrics PodSteer reads traffic from. Each source below says what it would need; PodSteer installs nothing.",
			gate.backend.Describe())
	}
	return answer, nil
}

// monitors names every discovered monitoring backend as a source workload,
// so what it sent — a scrape of a meshed pod — is not drawn as traffic.
//
// EVERY DISCOVERED ONE, not only the chosen: a general Prometheus can be the
// one answering while linkerd-viz's meshed Prometheus is the one scraping.
// Named by namespace and Service, which for linkerd-viz is also the
// Deployment's name. Discovery is cached, so this costs no request.
func (s *TrafficService) monitors(ctx context.Context, id domain.ClusterID, chosen domain.MetricsBackend) []domain.TrafficEndpoint {
	backends, err := s.metrics.discovery.ListMetricsBackends(ctx, id)
	if err != nil {
		backends = nil
	}
	monitors := make([]domain.TrafficEndpoint, 0, len(backends)+1)
	for _, backend := range append(backends, chosen) {
		monitors = append(monitors, domain.TrafficEndpoint{Namespace: string(backend.Namespace), Workload: backend.Service})
	}
	return monitors
}

// linkerdVizElsewhere names linkerd-viz's Prometheus when discovery found one
// that is not the chosen backend, or returns "".
//
// SAID, NOT SWITCHED TO. Linkerd's metrics live in linkerd-viz's own
// Prometheus, and the chosen backend usually does not scrape the proxies; but
// answering from a backend the operator did not choose is the one thing ADR 7
// rules out, so the empty state names where the metrics are and how to choose
// it. Discovery is cached, so this costs no request.
func (s *TrafficService) linkerdVizElsewhere(ctx context.Context, id domain.ClusterID, chosen domain.MetricsBackend) string {
	backends, err := s.metrics.discovery.ListMetricsBackends(ctx, id)
	if err != nil {
		return ""
	}
	for _, backend := range backends {
		if backend.LinkerdViz && backendKey(backend) != backendKey(chosen) {
			return fmt.Sprintf(
				"Linkerd's metrics are in linkerd-viz's own Prometheus (%s in %s), which PodSteer found but is not the backend chosen for this cluster. Choose it under Settings → Clusters to read Linkerd traffic.",
				backend.Service, backend.Namespace)
		}
	}
	return ""
}

// unprobed lists every source as unavailable with what it would need.
func unprobed() []domain.TrafficSourceStatus {
	statuses := make([]domain.TrafficSourceStatus, 0, len(domain.TrafficSourceNames()))
	for _, source := range domain.TrafficSourceNames() {
		statuses = append(statuses, domain.TrafficSourceStatus{Source: source, Detail: domain.TrafficRequirement(source)})
	}
	return statuses
}

// Traffic reads one source over one window.
//
// It returns an error only when the request could not be shaped — an unknown
// source or window. Everything that is a fact about the cluster comes back as
// a status with a sentence, as Series does.
func (s *TrafficService) Traffic(
	ctx context.Context,
	id domain.ClusterID,
	namespaces []domain.NamespaceName,
	all bool,
	source domain.TrafficSource,
	window domain.TrafficWindow,
) (domain.TrafficLayer, error) {
	if all {
		namespaces = nil
	}

	// Shaped BEFORE anything is sent, so a request that cannot be answered
	// costs nobody's Prometheus anything.
	queries, err := domain.TrafficExpressions(source, namespaces, window)
	if err != nil && !errors.Is(err, domain.ErrQueryTooLong) {
		return domain.TrafficLayer{}, err
	}

	layer := domain.TrafficLayer{
		Source:      source,
		Window:      window,
		Edges:       []domain.TrafficEdge{},
		Unmapped:    []domain.TrafficEndpoint{},
		Expressions: []string{},
	}

	gate, gateErr := s.gate(ctx, id)
	if gateErr != nil {
		return domain.TrafficLayer{}, gateErr
	}
	if gate.refused {
		layer.Status, layer.Message, layer.Provenance = gate.status, gate.message, gate.provenance
		return layer, nil
	}
	layer.Provenance = gate.provenance

	if err != nil {
		failed := s.metrics.failed(gate.backend, err)
		layer.Status, layer.Message = failed.Status, failed.Message
		return layer, nil
	}

	found, err := s.probe(ctx, id, gate.backend)
	if err != nil {
		failed := s.metrics.failed(gate.backend, err)
		layer.Status, layer.Message = failed.Status, failed.Message
		return layer, nil
	}
	if found[source] <= 0 {
		// NOT ASKED. The probe already said the source is absent, and every
		// expression would answer nothing.
		layer.Status = domain.BackendAnsweredEmpty
		layer.Message = fmt.Sprintf("%s holds no %s metrics. %s", gate.backend.Describe(), source, domain.TrafficRequirement(source))
		if viz := s.linkerdVizElsewhere(ctx, id, gate.backend); source == domain.TrafficLinkerd && viz != "" {
			layer.Message = viz
		}
		return layer, nil
	}

	at := time.Now()
	results := make(map[domain.TrafficRole][]domain.PromSeries, len(queries))
	for _, query := range queries {
		layer.Expressions = append(layer.Expressions, query.Expression)

		series, err := s.query.QueryInstant(ctx, id, gate.backend, query.Expression, at)
		if err != nil {
			failed := s.metrics.failed(gate.backend, err)
			layer.Status, layer.Message = failed.Status, failed.Message
			return layer, nil
		}
		results[query.Role] = append(results[query.Role], series...)
	}

	var nodes []domain.TrafficNodeRef
	if s.nodes != nil {
		read, err := s.nodes.TrafficNodes(ctx, id, namespaces, all)
		if err != nil {
			// Drawn unresolved rather than not drawn: the edges are still
			// true, only their attachment is missing.
			s.logger.Debug("could not read topology nodes for traffic",
				slog.String("cluster", id.String()), slog.String("error", err.Error()))
		} else {
			nodes = read
		}
	}

	mapped := domain.MapTraffic(source, window, results, nodes, namespaces, s.monitors(ctx, id, gate.backend)...)
	mapped.Provenance = layer.Provenance
	mapped.Expressions = layer.Expressions

	if len(mapped.Edges) > domain.MaxTrafficEdges {
		mapped.Status = domain.BackendTooLarge
		mapped.Message = fmt.Sprintf(
			"%s reported %d workload pairs talking, more than the %d PodSteer draws. Narrow the topology to fewer namespaces.",
			gate.backend.Describe(), len(mapped.Edges), domain.MaxTrafficEdges)
		mapped.Edges = []domain.TrafficEdge{}
		mapped.Unmapped = []domain.TrafficEndpoint{}
	}
	return mapped, nil
}

// Invalidate drops what is remembered about one cluster's sources. Called
// with the metrics-query service's own Invalidate, for its reason: a
// reconnected tab may be a different cluster behind the same context name.
func (s *TrafficService) Invalidate(id domain.ClusterID) {
	s.probes.forget(id)
}

// probe answers how many series of each source's signature metric the
// backend holds, cached per cluster and backend.
func (s *TrafficService) probe(ctx context.Context, id domain.ClusterID, backend domain.MetricsBackend) (map[domain.TrafficSource]float64, error) {
	key := backendKey(backend)
	if found, ok := s.probes.get(id, key); ok {
		return found, nil
	}
	generation := s.probes.generation(id)

	type answer struct {
		found map[domain.TrafficSource]float64
		err   error
	}

	// THE SHARED PROBE RUNS ON ITS OWN CONTEXT. Whoever arrives first starts
	// it, and a context that is theirs would let their cancellation — a
	// closed panel, a window switch — fail every caller waiting on the same
	// probe. So it is detached from the caller's cancellation, bounded by
	// its own timeout, and each caller stops waiting on its own context.
	results := s.probing.DoChan(string(id)+"\x00"+key, func() (any, error) {
		probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), trafficProbeTimeout)
		defer cancel()

		found := make(map[domain.TrafficSource]float64, len(domain.TrafficSourceNames()))
		at := time.Now()
		probes := domain.TrafficProbes()
		for _, source := range domain.TrafficSourceNames() {
			series, err := s.query.QueryInstant(probeCtx, id, backend, probes[source], at)
			if err != nil {
				// NOT CACHED: a refusal is already cached by the adapter,
				// and a timeout should be retried on the next click.
				return answer{err: err}, nil
			}
			for _, one := range series {
				if len(one.Points) > 0 {
					found[source] += one.Points[len(one.Points)-1].Value
				}
			}
		}
		s.probes.put(id, generation, key, found)
		return answer{found: found}, nil
	})

	select {
	case shared := <-results:
		result, _ := shared.Val.(answer)
		return result.found, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// probeCache holds one probe answer per cluster, with the generation counter
// verificationCache uses, for its reason: a probe in flight across an
// Invalidate must not write an answer about the cluster this tab has left.
type probeCache struct {
	mu          sync.Mutex
	entries     map[domain.ClusterID]probeEntry
	generations map[domain.ClusterID]uint64
}

type probeEntry struct {
	at      time.Time
	backend string
	found   map[domain.TrafficSource]float64
}

func (c *probeCache) generation(id domain.ClusterID) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generations[id]
}

func (c *probeCache) get(id domain.ClusterID, backend string) (map[domain.TrafficSource]float64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[id]
	if !ok || entry.backend != backend || time.Since(entry.at) > trafficProbeTTL {
		return nil, false
	}
	return entry.found, true
}

func (c *probeCache) put(id domain.ClusterID, generation uint64, backend string, found map[domain.TrafficSource]float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.generations[id] != generation {
		return
	}
	if c.entries == nil {
		c.entries = make(map[domain.ClusterID]probeEntry, 2)
	}
	c.entries[id] = probeEntry{at: time.Now(), backend: backend, found: found}
}

func (c *probeCache) forget(id domain.ClusterID) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, id)
	if c.generations == nil {
		c.generations = make(map[domain.ClusterID]uint64, 2)
	}
	c.generations[id]++
}
