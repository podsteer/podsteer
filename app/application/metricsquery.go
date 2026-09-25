package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// Reading a longer history out of a monitoring stack the cluster already
// runs, when the operator asked for one. See ADR 7.
//
// THE GOVERNING SENTENCE: PodSteer queries nothing it was not asked to query,
// and draws no series it cannot attribute. Everything in this file is one of
// those two halves — the settings gate on the way in, and the node-set check
// and the provenance on the way out.

// backendVerificationTTL is how long a node-set check stands.
//
// HALF AN HOUR, matched to the discovery cache beside it, and the reason is
// cost rather than staleness: on a fleet backend the probe expression touches
// every container series the querier fronts, which is the most expensive
// thing this feature ever asks for. A node joining a cluster inside the
// window changes the answer only where it would flip verified to fleet, and
// half an hour of a narrowed sum is a far better failure than probing a
// Thanos on every chart open.
const backendVerificationTTL = 30 * time.Minute

// probeFailureTTL is how long a FAILED node probe stands.
//
// SHORT, AND IT EXISTS FOR THE TIMEOUT RATHER THAN THE REFUSAL. The probe is
// the most expensive query this feature makes — on a fleet backend it touches
// every container series the querier fronts — so a backend slow enough to
// time out once is one that will time out again, and under `auto` every range
// change would run it afresh and wait the full timeout each time. Two minutes
// is long enough to stop that and short enough that a backend which has come
// back is noticed without anybody reconnecting.
const probeFailureTTL = 2 * time.Minute

// ClusterSettingsReader is the narrow view of the settings this service
// needs.
//
// AT THE CONSUMER, and narrow on purpose: the thing that decides what reaches
// a third system has no business being able to name the kubeconfig sources or
// the proxy, and a service that could would be one nobody could reason about
// from its type.
type ClusterSettingsReader interface {
	Cluster(ctx context.Context, id domain.ClusterID) (domain.ClusterSettings, error)
}

// ClusterNodeReader is the narrow view of the cluster this service needs: the
// node names the backend's answer is checked against.
type ClusterNodeReader interface {
	ListNodes(ctx context.Context, id domain.ClusterID, projection domain.Projection) ([]domain.Node, error)
}

// BackendDiscovery is the narrow view of discovery this service needs.
type BackendDiscovery interface {
	ListMetricsBackends(ctx context.Context, id domain.ClusterID) ([]domain.MetricsBackend, error)
}

// MetricsQueryServiceDeps are what the service is built from.
type MetricsQueryServiceDeps struct {
	Settings  ClusterSettingsReader
	Discovery BackendDiscovery
	Query     ports.MetricsQueryPort
	Nodes     ClusterNodeReader
	Logger    *slog.Logger
}

// MetricsQueryService answers a chart's request for a longer series.
type MetricsQueryService struct {
	settings  ClusterSettingsReader
	discovery BackendDiscovery
	query     ports.MetricsQueryPort
	nodes     ClusterNodeReader
	logger    *slog.Logger

	verifications verificationCache
	// verifying collapses concurrent checks of the same cluster into one.
	//
	// A SINGLEFLIGHT BECAUSE THE PROBE IS THE EXPENSIVE ONE. Two charts opening
	// together — or a range change racing the effect that draws the chart —
	// otherwise send two of the most costly query this feature has, against a
	// cold cache, to somebody's production Prometheus.
	verifying singleflight.Group
}

// NewMetricsQueryService wires the service.
func NewMetricsQueryService(deps MetricsQueryServiceDeps) (*MetricsQueryService, error) {
	switch {
	case deps.Settings == nil:
		return nil, errors.New("application: MetricsQueryService requires a settings reader")
	case deps.Discovery == nil:
		return nil, errors.New("application: MetricsQueryService requires backend discovery")
	case deps.Query == nil:
		return nil, errors.New("application: MetricsQueryService requires a MetricsQueryPort")
	case deps.Nodes == nil:
		return nil, errors.New("application: MetricsQueryService requires a node reader")
	}

	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &MetricsQueryService{
		settings:  deps.Settings,
		discovery: deps.Discovery,
		query:     deps.Query,
		nodes:     deps.Nodes,
		logger:    logger.With(slog.String("service", "metricsquery")),
	}, nil
}

// Series answers one chart's request for a metric over a window.
//
// NOTHING HERE IS EVER CALLED BY A REFRESH TICK, and that is a rule about the
// CALLER rather than about this method — the frontend's own store is what
// holds to it, and web/src/stores/backendTrend.test.ts counts calls across
// driven refreshes to prove it. What this method enforces is the half it can:
// a cluster whose mode is off makes no request at all.
//
// It returns an error only when the request could not be shaped — an unknown
// metric, a cluster that is not open. Every outcome that is a fact about
// somebody's cluster comes back as a status with a sentence, because "your
// account cannot proxy", "your Prometheus rejected this" and "your Prometheus
// holds no kubelet series" call for opposite actions and none of them is a
// fault in this application.
func (s *MetricsQueryService) Series(
	ctx context.Context,
	id domain.ClusterID,
	metric domain.MetricID,
	scope domain.MetricScope,
	window time.Duration,
) (domain.BackendSeriesResult, error) {
	settings, err := s.settings.Cluster(ctx, id)
	if err != nil {
		return domain.BackendSeriesResult{}, fmt.Errorf("reading the metrics-query setting for %q: %w", id, err)
	}

	// THE GATE, AND IT IS FIRST. A cluster nobody switched this on for
	// behaves exactly as it did before the feature existed: no discovery
	// call, no node list, no query. Enforced here as well as in the frontend
	// because the frontend disabling a control is one code path, and this is
	// the one that decides what reaches somebody else's system.
	query := settings.MetricsQuery
	if query.Mode == "" || query.Mode == domain.MetricsQueryOff {
		return domain.NotEnabled(), nil
	}

	backend, found, err := s.chosenBackend(ctx, id, query.Preferred)
	if err != nil {
		return domain.BackendSeriesResult{}, err
	}
	if !found {
		return s.nothingDiscovered(query.Preferred), nil
	}

	// CAPTURED BEFORE ANY READ, and compared before anything is cached: see
	// verificationCache.write.
	generation := s.verifications.generation(id)

	verification, clusterNodes, err := s.verify(ctx, id, generation, backend)
	if err != nil {
		return s.failed(backend, err), nil
	}

	// WHAT MAY BE DRAWN IS DECIDED BEFORE ANYTHING IS ASKED FOR. A mismatched
	// or unverifiable backend is never sent a range query at all: there is
	// nothing that could be done with the answer, and asking anyway is a
	// request on somebody's production Prometheus for a chart that will not
	// be drawn.
	var narrowTo []string
	switch verification {
	case domain.VerificationVerified:
		// Nothing to narrow to: the backend answers for this cluster and
		// nothing else.
	case domain.VerificationFleet:
		if query.Fleet == domain.FleetRefuse {
			return domain.UnverifiedResult(backend, verification), nil
		}
		narrowTo = clusterNodes
	default:
		return domain.UnverifiedResult(backend, verification), nil
	}

	expression, step, err := domain.ComposeExpression(metric, scope, window, narrowTo)
	if err != nil {
		if errors.Is(err, domain.ErrQueryTooLong) {
			// REFUSED WITH A SENTENCE RATHER THAN SENT. The node filter is
			// what makes a fleet backend's answer about this cluster, and
			// past the URL budget there is no honest way to send it — a
			// truncated filter would silently widen the sum.
			return domain.BackendSeriesResult{
				Status: domain.BackendUnverified,
				Message: fmt.Sprintf(
					"%s holds more than this cluster, and narrowing the query to this cluster's %d nodes makes it longer than a request URL allows. Set this cluster to draw nothing rather than a narrowed aggregate, or query a backend that holds only this cluster.",
					backend.Describe(), len(narrowTo)),
				Provenance: domain.SeriesProvenance{
					Origin:       domain.OriginBackend,
					Source:       backend.Describe(),
					Verification: verification,
				},
			}, nil
		}
		return domain.BackendSeriesResult{}, err
	}

	end := time.Now()
	series, err := s.query.QueryRange(ctx, id, backend, expression, end.Add(-window), end, step)
	if err != nil {
		result := s.failed(backend, err)
		result.Expression = expression
		result.Provenance.Verification = verification
		result.Provenance.Filtered = len(narrowTo) > 0
		return result, nil
	}

	return domain.AnsweredResult(backend, verification, len(narrowTo) > 0, expression, step, series), nil
}

// Invalidate drops what is remembered about one cluster.
//
// Called from the same place the adapter's own Invalidate is, and for the
// reason its kube-state cache is dropped there: a tab is routinely
// reconnected because its kubeconfig context now names a different cluster,
// and a half-hour verification carried across that would license an aggregate
// checked against nodes this connection has never seen.
//
// FORGETTING ALSO BUMPS THE CLUSTER'S GENERATION, and that second half is
// what makes this correct rather than merely tidy: a verification already in
// flight finishes after this returns and tries to cache a node set belonging
// to the cluster this tab has left. From then on its captured generation no
// longer matches, so the write lands nowhere. Both happen under the cache's
// own mutex, so there is no instant at which a late writer can pass the check
// and then be forgotten.
func (s *MetricsQueryService) Invalidate(id domain.ClusterID) {
	s.verifications.forget(id)
}

// chosenBackend resolves which discovered candidate answers.
//
// A PICK THAT IS NO LONGER DISCOVERED IS NOT SILENTLY REPLACED. ADR 7 rules
// that behaviour out by name: answering from a different Prometheus than the
// one that was chosen is the one outcome that must not happen, so this
// reports "nothing discovered" and lets the panel say the chosen backend is
// gone.
func (s *MetricsQueryService) chosenBackend(
	ctx context.Context,
	id domain.ClusterID,
	preferred domain.PreferredBackend,
) (domain.MetricsBackend, bool, error) {
	backends, err := s.discovery.ListMetricsBackends(ctx, id)
	if err != nil {
		return domain.MetricsBackend{}, false, fmt.Errorf("discovering a metrics backend in %q: %w", id, err)
	}
	if len(backends) == 0 {
		return domain.MetricsBackend{}, false, nil
	}

	if preferred.IsZero() {
		// The ranked pick, which is what an operator who made no choice
		// meant.
		return backends[0], true, nil
	}

	for _, backend := range backends {
		if backend.Namespace == preferred.Namespace && backend.Service == preferred.Service {
			return backend, true, nil
		}
	}
	return domain.MetricsBackend{}, false, nil
}

// nothingDiscovered composes the two shapes of "there is nothing to ask".
func (s *MetricsQueryService) nothingDiscovered(preferred domain.PreferredBackend) domain.BackendSeriesResult {
	message := "No monitoring backend was found in this cluster."
	if !preferred.IsZero() {
		message = fmt.Sprintf(
			"The backend chosen for this cluster — %s in %s — is no longer there. PodSteer will not answer from a different one; choose another under Settings → Clusters.",
			preferred.Service, preferred.Namespace)
	}
	return domain.BackendSeriesResult{Status: domain.BackendNothingDiscovered, Message: message}
}

// verify answers whether this backend's series are about this cluster,
// reusing a recent answer where there is one.
//
// SINGLEFLIGHTED, because the probe is the most expensive request this
// feature makes and two charts opening together would otherwise send two of
// it. The shared answer is detached from whoever started it in the same sense
// readcache.go's is: the first caller runs it and the rest wait.
func (s *MetricsQueryService) verify(
	ctx context.Context,
	id domain.ClusterID,
	generation uint64,
	backend domain.MetricsBackend,
) (domain.BackendVerification, []string, error) {
	if cached, ok := s.verifications.get(id, backend); ok {
		return cached.verification, cached.clusterNodes, cached.failure
	}

	type answer struct {
		verification domain.BackendVerification
		nodes        []string
		err          error
	}

	shared, _, _ := s.verifying.Do(string(id)+"\x00"+backendKey(backend), func() (any, error) {
		verification, nodes, err := s.checkNodes(ctx, id, generation, backend)
		return answer{verification: verification, nodes: nodes, err: err}, nil
	})

	result, _ := shared.(answer)
	return result.verification, result.nodes, result.err
}

// checkNodes performs the comparison itself. See verify.
func (s *MetricsQueryService) checkNodes(
	ctx context.Context,
	id domain.ClusterID,
	generation uint64,
	backend domain.MetricsBackend,
) (domain.BackendVerification, []string, error) {
	// The cluster's own nodes first. It is a read the tab's poll has almost
	// certainly just made, so readcache.go coalesces it rather than costing a
	// second LIST.
	nodes, err := s.nodes.ListNodes(ctx, id, domain.Projection{})
	if err != nil {
		// A CLUSTER WHOSE NODES WE COULD NOT LIST IS UNVERIFIABLE, NOT
		// FORBIDDEN. This is the namespace-scoped account this project is
		// built for: it may proxy perfectly well and simply may not list
		// nodes cluster-wide. Reported as an error it reaches failed() and
		// becomes the services/proxy sentence — sending somebody to ask for a
		// permission they already hold, about a Service nobody asked about.
		//
		// The backend is NOT probed afterwards: with no node set of ours to
		// compare against, VerifyBackendNodes answers unverifiable whatever
		// the backend says, so asking is a request that cannot change the
		// answer. And it is NOT cached, because a node list can fail
		// transiently and half an hour of no chart is the wrong price for a
		// timeout.
		s.logger.Debug("could not list this cluster's nodes; the backend is unverifiable",
			slog.String("cluster", id.String()),
			slog.String("backend", backend.Describe()),
			slog.String("error", err.Error()))
		return domain.VerificationUnverifiable, nil, nil
	}

	ours := make([]string, 0, len(nodes))
	for _, node := range nodes {
		ours = append(ours, node.Name())
	}

	theirs, err := s.query.QueryNodes(ctx, id, backend)
	if err != nil {
		// CACHED BRIEFLY, AND AS THE FAILURE IT WAS. See probeFailureTTL: a
		// backend slow enough to time out will time out again, and under
		// `auto` every range change would otherwise re-run the most expensive
		// query this feature has and wait the full timeout for it.
		s.verifications.putFailure(id, generation, backend, err)
		return "", nil, err
	}

	verification := domain.VerifyBackendNodes(theirs, ours)
	s.verifications.put(id, generation, backend, verification, ours)
	s.logger.Debug("verified a metrics backend",
		slog.String("cluster", id.String()),
		slog.String("backend", backend.Describe()),
		slog.String("verification", string(verification)),
		slog.Int("backendNodes", len(theirs)),
		slog.Int("clusterNodes", len(ours)))

	return verification, ours, nil
}

// failed turns a port error into the status the panel reads.
//
// EVERY ONE OF THESE IS A FACT ABOUT SOMEBODY'S CLUSTER rather than a fault
// here, which is why they are statuses and not errors — and why each has its
// own sentence. A refusal to proxy is common and needs a permission; a
// rejected expression needs the backend's own words; an over-sized answer
// needs neither and is a bound this application chose.
func (s *MetricsQueryService) failed(backend domain.MetricsBackend, err error) domain.BackendSeriesResult {
	provenance := domain.SeriesProvenance{Origin: domain.OriginBackend, Source: backend.Describe()}

	switch {
	case errors.Is(err, ports.ErrForbidden), errors.Is(err, ports.ErrUnauthenticated):
		return domain.BackendSeriesResult{
			Status: domain.BackendForbidden,
			Message: fmt.Sprintf(
				"Your account may not reach %s through the API server's proxy. That is a permission on your cluster (get on services/proxy), not a setting here. PodSteer stops asking for a few minutes after a refusal, so a permission granted now takes a moment to take effect.",
				backend.Describe()),
			Provenance: provenance,
		}
	case errors.Is(err, ports.ErrMetricsBackendAuth):
		// ITS OWN SENTENCE BECAUSE NEITHER OF THE OTHER TWO IS TRUE. Nothing
		// is wrong with the account's Kubernetes permissions and nothing is
		// wrong with the expression: the backend sits behind authentication
		// of its own, and the API server's proxy strips the header on the way
		// through, so this route cannot be made to work.
		return domain.BackendSeriesResult{
			Status: domain.BackendNeedsCredential,
			Message: fmt.Sprintf(
				"%s has authentication of its own in front of it — a kube-rbac-proxy or an oauth proxy — and refused the request. The API server's proxy does not carry a credential to it, and PodSteer will not hold one, so this backend cannot be read this way. A backend reachable without its own credential can be chosen under Settings → Clusters.",
				backend.Describe()),
			Provenance: provenance,
		}
	case errors.Is(err, ports.ErrMetricsQueryRejected):
		// VERBATIM, the way a rejected manifest carries the API server's own
		// verdict. A backend that declined an expression says which function
		// or label it objected to, and that text is the only thing anybody
		// can act on — including, in the case that matters most, evidence
		// that the expression table itself needs changing.
		return domain.BackendSeriesResult{
			Status:     domain.BackendRejected,
			Message:    messageAfterSentinel(err, ports.ErrMetricsQueryRejected),
			Provenance: provenance,
		}
	case errors.Is(err, ports.ErrMetricsQueryTooLarge):
		return domain.BackendSeriesResult{
			Status: domain.BackendTooLarge,
			Message: fmt.Sprintf(
				"%s answered with more than PodSteer will read, so the answer was refused rather than decoded.",
				backend.Describe()),
			Provenance: provenance,
		}
	case errors.Is(err, domain.ErrQueryTooLong):
		return domain.BackendSeriesResult{
			Status:     domain.BackendUnverified,
			Message:    "The composed query is longer than a request URL allows, so nothing was sent.",
			Provenance: provenance,
		}
	default:
		return domain.BackendSeriesResult{
			Status: domain.BackendUnreachable,
			Message: fmt.Sprintf(
				"PodSteer could not reach %s: %s", backend.Describe(), err.Error()),
			Provenance: provenance,
		}
	}
}

// messageAfterSentinel returns whatever a wrapped error added after its
// sentinel, so the backend's own text reaches the panel without the
// operation prefix this layer has no use for.
func messageAfterSentinel(err error, sentinel error) string {
	text := err.Error()
	marker := sentinel.Error() + ": "
	if index := strings.LastIndex(text, marker); index >= 0 {
		return text[index+len(marker):]
	}
	return text
}

// verificationCache holds one node-set check per cluster and backend, and
// numbers each cluster's connection.
//
// PER BACKEND AS WELL AS PER CLUSTER, because an operator changing which
// candidate answers is asking about a different system: a verification of the
// Prometheus they were using says nothing about the Thanos they just picked,
// and carrying it across is exactly how a fleet gets drawn under one
// cluster's name.
//
// THE GENERATION COUNTER LIVES HERE RATHER THAN ON THE SERVICE, under the
// same mutex as the entries. A counter kept outside leaves a window between a
// writer reading it and writing the entry, in which Invalidate can run and
// clear an entry that is then re-created a moment later — which is the exact
// bug the counter exists to prevent, moved rather than fixed.
type verificationCache struct {
	mu          sync.Mutex
	entries     map[domain.ClusterID]verificationEntry
	generations map[domain.ClusterID]uint64
}

// generation reports the current connection number for a cluster.
func (c *verificationCache) generation(id domain.ClusterID) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.generations[id]
}

type verificationEntry struct {
	at           time.Time
	generation   uint64
	backend      string
	verification domain.BackendVerification
	// clusterNodes is what the check compared against, kept so a narrowed
	// query is composed from the SAME set the verdict was reached on rather
	// than from a fresh list that may have moved underneath it.
	clusterNodes []string
	// failure is set when the probe itself failed, and the entry then stands
	// only for probeFailureTTL rather than the full window.
	failure error
}

// ttl is how long this entry stands: the short window for a failure, the
// long one for an answer.
func (e verificationEntry) ttl() time.Duration {
	if e.failure != nil {
		return probeFailureTTL
	}
	return backendVerificationTTL
}

func (c *verificationCache) get(id domain.ClusterID, backend domain.MetricsBackend) (verificationEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[id]
	if !ok || entry.backend != backendKey(backend) || time.Since(entry.at) > entry.ttl() {
		return verificationEntry{}, false
	}
	return entry, true
}

func (c *verificationCache) put(
	id domain.ClusterID,
	generation uint64,
	backend domain.MetricsBackend,
	verification domain.BackendVerification,
	clusterNodes []string,
) {
	c.write(id, verificationEntry{
		generation:   generation,
		backend:      backendKey(backend),
		verification: verification,
		clusterNodes: append([]string(nil), clusterNodes...),
	})
}

// putFailure remembers, briefly, that the probe itself did not answer.
func (c *verificationCache) putFailure(
	id domain.ClusterID,
	generation uint64,
	backend domain.MetricsBackend,
	failure error,
) {
	c.write(id, verificationEntry{
		generation: generation,
		backend:    backendKey(backend),
		failure:    failure,
	})
}

// write stores an entry unless the connection it describes has since been
// replaced.
//
// THE GENERATION CHECK IS THE POINT. A verification that read the cluster
// before Invalidate ran finishes after it, and what it learned is a node set
// belonging to the cluster this tab has left — which, cached, would license a
// narrowed sum over nodes the new connection has never seen. Ordering cannot
// close that window; comparing generations under this mutex makes the late
// write inert.
func (c *verificationCache) write(id domain.ClusterID, entry verificationEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.generations[id] != entry.generation {
		return
	}
	if c.entries == nil {
		c.entries = make(map[domain.ClusterID]verificationEntry, 2)
	}
	entry.at = time.Now()
	c.entries[id] = entry
}

// forget drops a cluster's verification AND advances its generation, so
// anything still in flight against the old connection cannot write.
func (c *verificationCache) forget(id domain.ClusterID) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, id)
	if c.generations == nil {
		c.generations = make(map[domain.ClusterID]uint64, 2)
	}
	c.generations[id]++
}

func backendKey(backend domain.MetricsBackend) string {
	return string(backend.Namespace) + "/" + backend.Service
}
