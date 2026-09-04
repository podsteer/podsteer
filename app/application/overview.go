package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// APIInspector answers the two questions the upgrade-impact findings need.
//
// Defined here, at the consumer, the same way ResourceCounter used to be:
// what the overview needs from Kubernetes discovery and object metadata is
// narrower than any one outbound port, so the interface is shaped by this
// use case rather than borrowed from one. The k8s adapter satisfies it
// without being told to.
type APIInspector interface {
	// ServedAPIs returns every group/version discovery reports the cluster
	// currently serves.
	ServedAPIs(ctx context.Context, id domain.ClusterID) ([]domain.APIGroupVersion, error)
	// APIWriters scans up to limit objects of kind and reports who last
	// wrote each one through kind's own API version, per
	// metadata.managedFields — never a count of objects, which a deprecated
	// version and its replacement would report identically since Kubernetes
	// stores one copy of an object and serves it through every version it
	// offers.
	APIWriters(ctx context.Context, id domain.ClusterID, kind domain.ResourceKind, limit int) (domain.APIUsage, error)
}

// apiWriterScanLimit bounds how many objects a single deprecated resource is
// scanned for writers, so a v1beta1 Events list on an old, busy cluster
// cannot turn into a full scan of everything the cluster has ever logged.
const apiWriterScanLimit = 2000

// apiWriterConcurrency caps how many deprecated group/versions are scanned
// for writers at once, the same way NamespaceInventory bounds its per-kind
// counts — a cluster can have several upgrade-candidate entries served at
// once, and firing every scan unbounded would multiply the request burst by
// however many the table happens to name.
const apiWriterConcurrency = 4

// OverviewServiceDeps are the collaborators the overview needs.
//
// It reads through the same ports as every other use case rather than being
// given a privileged view of the cluster: the overview is an assessment of the
// data PodSteer already has, not a second way of getting it.
type OverviewServiceDeps struct {
	// Cluster reads nodes, namespaces and the server version. Required.
	Cluster ports.ClusterPort
	// Workloads reads pods and controllers. Required.
	Workloads ports.WorkloadPort
	// Events reads Kubernetes Events. Required.
	Events ports.EventPort
	// Metrics reads usage. Required, but every call may report
	// ErrMetricsUnavailable and the overview degrades when it does.
	Metrics ports.MetricsPort
	// APIs answers what the cluster serves and who is still writing through
	// a deprecated version of it — required for the upgrade-impact findings.
	APIs APIInspector
	// Registry tracks open connections. Required.
	Registry *Registry
	// Logger receives diagnostics. Optional; defaults to slog.Default.
	Logger *slog.Logger
}

// overviewFreshness is how stale an assessment the dashboard will accept.
//
// Two seconds, which is under half the shortest refresh interval offered, so
// the dashboard can never appear to skip a tick. It exists to collapse the
// assessments that arrive almost together — a tab switch landing on top of a
// timer, the sampler landing on top of the UI — not to slow the UI down.
const overviewFreshness = 2 * time.Second

// overviewEntry is one cluster's most recent assessment.
type overviewEntry struct {
	at       time.Time
	overview domain.Overview
}

// overviewCall is an assessment in flight, so that callers arriving while one
// is running wait for it instead of starting a second.
type overviewCall struct {
	done     chan struct{}
	overview domain.Overview
	err      error
}

// OverviewService assembles the cluster dashboard.
type OverviewService struct {
	cluster   ports.ClusterPort
	workloads ports.WorkloadPort
	events    ports.EventPort
	metrics   ports.MetricsPort
	apis      APIInspector
	registry  *Registry
	logger    *slog.Logger

	// mu guards both maps below. An assessment is ten or so API reads across
	// the whole cluster, and two callers want one on the same timer: the
	// dashboard and the history sampler.
	mu       sync.Mutex
	cache    map[domain.ClusterID]overviewEntry
	inflight map[domain.ClusterID]*overviewCall
}

var _ ports.OverviewService = (*OverviewService)(nil)

// NewOverviewService validates deps and returns the service.
func NewOverviewService(deps OverviewServiceDeps) (*OverviewService, error) {
	switch {
	case deps.Cluster == nil:
		return nil, errors.New("application: OverviewService requires a ClusterPort")
	case deps.Workloads == nil:
		return nil, errors.New("application: OverviewService requires a WorkloadPort")
	case deps.Events == nil:
		return nil, errors.New("application: OverviewService requires an EventPort")
	case deps.Metrics == nil:
		return nil, errors.New("application: OverviewService requires a MetricsPort")
	case deps.APIs == nil:
		return nil, errors.New("application: OverviewService requires an APIInspector")
	case deps.Registry == nil:
		return nil, errors.New("application: OverviewService requires a Registry")
	}

	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &OverviewService{
		cluster:   deps.Cluster,
		workloads: deps.Workloads,
		events:    deps.Events,
		metrics:   deps.Metrics,
		apis:      deps.APIs,
		registry:  deps.Registry,
		logger:    logger.With(slog.String("service", "overview")),
	}, nil
}

// controllerKinds are the workload kinds the overview reads.
//
// ReplicaSets are included even though the overview never reports them: they
// are what a Deployment's pods are actually owned by, and without them a
// finding about crash-looping pods cannot be matched to the Deployment it
// belongs to. Jobs serve the same purpose for CronJobs.
var controllerKinds = []domain.WorkloadKind{
	domain.WorkloadDeployment,
	domain.WorkloadStatefulSet,
	domain.WorkloadDaemonSet,
	domain.WorkloadReplicaSet,
	domain.WorkloadJob,
	domain.WorkloadCronJob,
}

// Overview assesses a connected cluster.
//
// Every read runs concurrently, and every read is allowed to fail on its own.
// A cluster whose RBAC forbids listing events, or that runs no metrics-server,
// still produces an overview — with the missing sources named — because the
// alternative is an error page in front of an operator who is looking at this
// screen precisely because something is wrong.
func (s *OverviewService) Overview(ctx context.Context, id domain.ClusterID) (domain.Overview, error) {
	return s.OverviewWithin(ctx, id, overviewFreshness)
}

// OverviewForTarget assesses a connected cluster against a specific upgrade
// target — the minor a "check against" selector in the UI chose — rather
// than the default of the next minor after the cluster's current version.
//
// Deliberately NOT cached or coalesced like Overview/OverviewWithin below:
// this is an occasional, operator-initiated comparison, not the polling path
// the dashboard and the history sampler share, and folding it into the same
// cache would risk a later DEFAULT-target poll being served the assessment
// made for whatever version the operator was comparing against a moment
// earlier. The cost is a full re-assessment on every call, which is the
// right trade for a control nobody clicks on every refresh tick.
func (s *OverviewService) OverviewForTarget(
	ctx context.Context,
	id domain.ClusterID,
	targetMinor string,
) (domain.Overview, error) {
	if _, err := s.registry.Get(id); err != nil {
		return domain.Overview{}, err
	}
	return s.assessWithRetry(ctx, id, targetMinor)
}

// OverviewWithin returns an assessment no older than maxAge, running one only
// if nothing recent enough is held.
//
// It exists for the history sampler, which is the second caller wanting the
// same ten-or-so cluster-wide reads on its own timer. A sample taken every
// thirty seconds is no worse for being derived from an assessment the
// dashboard made a moment ago, so the sampler passes its own interval and
// almost always reuses one — while the dashboard passes a fraction of its
// refresh interval and effectively never does.
//
// Concurrent callers share one assessment rather than racing. The cost is
// that they also share the leader's context: if the caller that started the
// work gives up, everyone waiting on it gets that error and retries on their
// own schedule. That is the right trade when the alternative is running the
// whole cluster read twice.
func (s *OverviewService) OverviewWithin(
	ctx context.Context,
	id domain.ClusterID,
	maxAge time.Duration,
) (domain.Overview, error) {
	// Before the cache, always: an assessment held for a cluster that has
	// since been disconnected must not be served as though it were live.
	if _, err := s.registry.Get(id); err != nil {
		s.forget(id)
		return domain.Overview{}, err
	}

	s.mu.Lock()
	if entry, ok := s.cache[id]; ok && maxAge > 0 && time.Since(entry.at) <= maxAge {
		s.mu.Unlock()
		return entry.overview, nil
	}

	if call, ok := s.inflight[id]; ok {
		s.mu.Unlock()
		select {
		case <-call.done:
			return call.overview, call.err
		case <-ctx.Done():
			return domain.Overview{}, ctx.Err()
		}
	}

	call := &overviewCall{done: make(chan struct{})}
	if s.inflight == nil {
		s.inflight = make(map[domain.ClusterID]*overviewCall, 2)
	}
	s.inflight[id] = call
	s.mu.Unlock()

	// "" asks for the default target (the next minor after the cluster's
	// current version) — see assess.
	call.overview, call.err = s.assessWithRetry(ctx, id, "")

	s.mu.Lock()
	delete(s.inflight, id)
	if call.err == nil {
		if s.cache == nil {
			s.cache = make(map[domain.ClusterID]overviewEntry, 2)
		}
		s.cache[id] = overviewEntry{at: time.Now(), overview: call.overview}
	}
	s.mu.Unlock()

	close(call.done)
	return call.overview, call.err
}

// forget drops a cluster's held assessment.
func (s *OverviewService) forget(id domain.ClusterID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cache, id)
}

// assessAttempts is how many times an unreachable cluster is re-read before
// the assessment gives up and says so.
//
// Three, because the failure this guards against is a blip — a wifi handover, a
// VPN renegotiating, a laptop waking — and one missed packet should not empty
// the dashboard and alarm somebody. Three is also cheap precisely when it is
// used: an unreachable host fails on connection refused or DNS in milliseconds,
// so the retries cost nothing in the case that triggers them. A reachable
// cluster never retries at all.
const assessAttempts = 3

// assessBackoff is the pause between those attempts.
//
// Short deliberately. This runs inside a refresh the operator is waiting on, so
// the whole retry budget has to stay well under one refresh interval; the point
// is to ride out a blip, not to wait out an outage.
const assessBackoff = 400 * time.Millisecond

// assessWithRetry assesses, re-attempting only when the cluster was unreachable.
//
// ONLY that error is retried. A permission failure, a bad kubeconfig or a
// cancelled request are all answers — repeating them wastes the operator's time
// and, for ErrForbidden, hammers an API server that has already said no. The
// transport failure is the one that is plausibly transient.
func (s *OverviewService) assessWithRetry(
	ctx context.Context,
	id domain.ClusterID,
	targetMinor string,
) (domain.Overview, error) {
	var err error

	for attempt := 1; attempt <= assessAttempts; attempt++ {
		var overview domain.Overview
		overview, err = s.assess(ctx, id, targetMinor)
		if err == nil {
			if attempt > 1 {
				s.logger.InfoContext(ctx, "cluster answered on retry",
					slog.String("cluster", string(id)),
					slog.Int("attempt", attempt))
			}
			return overview, nil
		}
		if !errors.Is(err, ports.ErrUnreachable) {
			return domain.Overview{}, err
		}

		if attempt < assessAttempts {
			s.logger.DebugContext(ctx, "cluster unreachable; retrying",
				slog.String("cluster", string(id)),
				slog.Int("attempt", attempt),
				slog.Int("of", assessAttempts))

			select {
			case <-ctx.Done():
				return domain.Overview{}, ctx.Err()
			case <-time.After(assessBackoff):
			}
		}
	}

	// Give up, and say so at a level somebody will see. The caller surfaces
	// this as a disconnected cluster; the alternative — which this replaced —
	// was a confident green tick over an empty dashboard.
	s.logger.WarnContext(ctx, "cluster unreachable; giving up",
		slog.String("cluster", string(id)),
		slog.Int("attempts", assessAttempts),
		slog.String("error", err.Error()))

	return domain.Overview{}, err
}

// assess performs the assessment itself, unconditionally.
//
// targetMinor selects what UpgradeImpact assesses against; "" asks
// domain.NewOverview for its own default (the next minor after the
// cluster's current version).
func (s *OverviewService) assess(ctx context.Context, id domain.ClusterID, targetMinor string) (domain.Overview, error) {

	var (
		mu          sync.Mutex
		wg          sync.WaitGroup
		unavailable []string
		// attempted and unreachable count sources rather than naming them: the
		// question they answer is "did EVERY read fail because we could not
		// reach the cluster", which is a ratio, not a list.
		attempted   int
		unreachable int

		// Optimistic, and corrected by the node-metrics read if it fails.
		// Node metrics decides it rather than pod metrics because `measured`
		// is already gated on the same call — two sources of truth for one
		// fact is how they come to disagree.
		metricsStatus = domain.MetricsMeasuredOK

		version    domain.ServerVersion
		nodes      []domain.Node
		pods       []domain.Pod
		workloads  []domain.Workload
		events     []domain.Event
		namespaces []domain.Namespace

		nodeUsage map[string]domain.Metrics
		podUsage  map[string]domain.PodUsage
		nodeDisks map[string]domain.NodeFilesystems
		backend   domain.MetricsBackend
		volumes   []domain.PersistentVolume
		claims    []domain.PersistentVolumeClaim
		measured  bool

		// servedAPIs, apisKnown and apiUsage feed UpgradeImpact. servedAPIs
		// is read below, outside `run`: a discovery failure must not print
		// as an "Unavailable" source the way a metrics or events failure
		// does, because APIsKnown already carries the distinction the UI
		// needs — "not assessed" rather than one more line in a list nobody
		// reading it would connect to an upgrade check.
		servedAPIs []domain.APIGroupVersion
		apisKnown  bool
		apiUsage   = make(map[string]domain.APIUsage)
	)

	// degrade records a source that could not be read. A failure here is not
	// returned to the caller: it is reported in the overview itself, so the UI
	// can say "no metrics" rather than showing a confident zero.
	degrade := func(source string, err error) {
		mu.Lock()
		defer mu.Unlock()
		// Deduplicated because the two metrics reads fail together on a
		// cluster with no metrics-server, and the UI should say "metrics"
		// once rather than listing each call that noticed.
		if !slices.Contains(unavailable, source) {
			unavailable = append(unavailable, source)
		}
		s.logger.Debug("overview source unavailable",
			slog.String("cluster", string(id)),
			slog.String("source", source),
			slog.String("error", err.Error()))
	}

	run := func(source string, read func() error) {
		mu.Lock()
		attempted++
		mu.Unlock()

		wg.Go(func() {
			err := read()
			if err == nil {
				return
			}
			degrade(source, err)

			// ErrUnreachable is the transport-level failure — VPN dropped,
			// laptop asleep, port-forward closed — as classified in
			// adapters/k8s/errors.go. Counting it separately is what lets the
			// caller tell "this cluster has no metrics-server" from "this
			// cluster is not there".
			if errors.Is(err, ports.ErrUnreachable) {
				mu.Lock()
				unreachable++
				mu.Unlock()
			}
		})
	}

	run("version", func() error {
		result, err := s.cluster.ServerVersion(ctx, id)
		version = result
		return err
	})

	// Every list here carries the empty projection: the assessment reads no
	// annotations, and the empty projection is what lets these reads
	// coalesce with the open list view's in the same tick.
	run("nodes", func() error {
		result, err := s.cluster.ListNodes(ctx, id, domain.Projection{})
		nodes = result
		return err
	})

	run("pods", func() error {
		result, err := s.workloads.ListPods(ctx, id, domain.NamespaceAll, domain.Projection{})
		pods = result
		return err
	})

	run("namespaces", func() error {
		result, err := s.cluster.ListNamespaces(ctx, id, domain.Projection{})
		namespaces = result
		return err
	})

	// Storage is two lists rather than one because they answer different
	// questions and either can be forbidden on its own.
	run("volumes", func() error {
		result, err := s.cluster.ListPersistentVolumes(ctx, id)
		volumes = result
		return err
	})

	run("claims", func() error {
		result, err := s.cluster.ListPersistentVolumeClaims(ctx, id, domain.NamespaceAll)
		claims = result
		return err
	})

	run("events", func() error {
		result, err := s.events.ListEvents(ctx, id, domain.NamespaceAll, domain.Projection{})
		events = result
		return err
	})

	run("metrics", func() error {
		result, err := s.metrics.NodeMetrics(ctx, id)
		if err != nil {
			// The sentinel already carries the reason; this only records it
			// where the UI can reach it. Without this the operator is told
			// "no metrics" whether metrics-server is absent or merely
			// unreadable, and those need opposite advice.
			mu.Lock()
			metricsStatus = metricsStatusFor(err)
			mu.Unlock()
			return err
		}
		nodeUsage = result
		measured = true
		return nil
	})

	run("metrics", func() error {
		result, err := s.metrics.PodMetrics(ctx, id, domain.NamespaceAll)
		podUsage = result
		return err
	})

	// Named separately from "metrics" so an operator can tell which half is
	// missing: a cluster commonly has metrics-server and no nodes/proxy, and
	// "assessed without metrics" would be wrong about the half that worked.
	run("node filesystems", func() error {
		result, err := s.metrics.NodeFilesystems(ctx, id)
		nodeDisks = result
		return err
	})

	// DISCOVERY RIDES THE ASSESSMENT rather than getting a call of its own,
	// because it is one cached lookup answering a question about the cluster —
	// exactly what the assessment is for. Not run through `run` deliberately:
	// finding no Prometheus, and being forbidden to look, are both ordinary
	// answers, and neither belongs in the list of sources that failed. A
	// cluster with no monitoring stack is not a degraded cluster.
	wg.Go(func() {
		result, err := s.metrics.DiscoverMetricsBackend(ctx, id)
		if err != nil {
			s.logger.Debug("metrics backend discovery skipped",
				slog.String("cluster", string(id)),
				slog.String("error", err.Error()))
			return
		}
		mu.Lock()
		backend = result
		mu.Unlock()
	})

	// UPGRADE-IMPACT DISCOVERY AND WRITER SCANS. Not run through `run`: a
	// failure here must not print as an "Unavailable" source the way a
	// metrics or events failure does — apisKnown already carries the fact
	// the UI needs (Upgrade stays zero, TargetMinor == ""), and a line
	// reading "served APIs" in a list built for pods and events would name
	// an implementation detail nobody opening this screen recognises.
	wg.Go(func() {
		served, err := s.apis.ServedAPIs(ctx, id)
		if err != nil {
			s.logger.DebugContext(ctx, "served APIs unavailable",
				slog.String("cluster", string(id)),
				slog.String("error", err.Error()))
			return
		}

		mu.Lock()
		servedAPIs = served
		apisKnown = true
		mu.Unlock()

		// Bounded to exactly the served group/versions the deprecation table
		// could ever flag — see domain.UpgradeCandidates. A cluster's served
		// APIs routinely number in the dozens once CRDs are counted, and
		// this must never become "scan everything served": one bounded
		// writer scan per candidate entry, and nothing for the rest.
		//
		// SetLimit(4) rather than one goroutine per candidate outright: a
		// cluster can carry several upgrade-candidate entries served at
		// once (a whole flowcontrol.apiserver.k8s.io chain, say), and firing
		// every scan unbounded would multiply the request burst by however
		// many the table happens to name for this cluster.
		group, groupCtx := errgroup.WithContext(ctx)
		group.SetLimit(apiWriterConcurrency)
		for _, dep := range domain.UpgradeCandidates(served) {
			kind := dep.ResourceKind()
			group.Go(func() error {
				usage, err := s.apis.APIWriters(groupCtx, id, kind, apiWriterScanLimit)
				if err != nil {
					// A count failing for one deprecated group/version (an
					// account without `list` on it, say) is not a source
					// the rest of the overview depends on. UpgradeImpact
					// already treats an absent key as "not checked" rather
					// than "no writers", so the failure is simply left out
					// of the map rather than returned — every candidate
					// gets its own chance regardless of whether another one
					// failed.
					s.logger.DebugContext(ctx, "upgrade-impact writer scan unavailable",
						slog.String("cluster", string(id)),
						slog.String("kind", kind.ID()),
						slog.String("error", err.Error()))
					return nil
				}
				mu.Lock()
				apiUsage[kind.ID()] = usage
				mu.Unlock()
				return nil
			})
		}
		_ = group.Wait()
	})

	// One goroutine per controller kind, each appending under the lock. The
	// order they finish in does not matter: the assessment sorts everything it
	// reports.
	for _, kind := range controllerKinds {
		run("workloads/"+string(kind), func() error {
			result, err := s.workloads.ListWorkloads(ctx, id, kind, domain.NamespaceAll, domain.Projection{})
			if err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			workloads = append(workloads, result...)
			return nil
		})
	}

	wg.Wait()

	// EVERY READ FAILED, AND ALL OF THEM ON TRANSPORT. That is not a degraded
	// assessment, it is the absence of one, and returning it as an overview is
	// what produced a green "No problems found" on a cluster the laptop could
	// no longer reach.
	//
	// The ratio matters rather than any single call: one source failing this
	// way is a flaky endpoint, and the assessment should still degrade around
	// it as it always has. All of them failing this way is the cluster being
	// gone.
	if attempted > 0 && unreachable == attempted {
		return domain.Overview{}, fmt.Errorf("assessing %q: %w", id, ports.ErrUnreachable)
	}

	// Metrics arrive keyed separately from the objects they describe, so they
	// are joined here rather than in the domain: the domain should not know
	// that usage came from a different API than the pod did.
	nodes = attachNodeUsage(nodes, nodeUsage)
	nodes = attachNodeFilesystems(nodes, nodeDisks)
	pods = attachPodUsage(pods, podUsage)

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID:       id,
		Version:         version,
		Nodes:           nodes,
		Pods:            pods,
		Workloads:       workloads,
		Events:          events,
		Namespaces:      namespaces,
		Volumes:         volumes,
		Claims:          claims,
		Unavailable:     unavailable,
		MetricsMeasured: measured,
		Metrics:         metricsStatus,
		Backend:         backend,
		ServedAPIs:      servedAPIs,
		APIsKnown:       apisKnown,
		APIUsage:        apiUsage,
		TargetVersion:   targetMinor,
		Now:             time.Now().UTC(),
	})

	s.logger.Info("assessed cluster",
		slog.String("cluster", string(id)),
		slog.String("health", string(overview.Health)),
		slog.Int("findings", len(overview.Findings)),
		slog.Int("nodes", len(nodes)),
		slog.Int("pods", len(pods)),
		slog.Int("unavailable", len(unavailable)))

	return overview, nil
}

// attachNodeUsage returns the nodes carrying their measured usage.
//
// The join happens here rather than in the domain because the domain should
// not know that usage came from a different API than the node did.
func attachNodeUsage(nodes []domain.Node, usage map[string]domain.Metrics) []domain.Node {
	if len(usage) == 0 {
		return nodes
	}

	enriched := make([]domain.Node, 0, len(nodes))
	for _, node := range nodes {
		if measured, ok := usage[node.Name()]; ok {
			node = node.WithUsage(measured)
		}
		enriched = append(enriched, node)
	}
	return enriched
}

// attachNodeFilesystems returns the nodes carrying their disk occupancy.
func attachNodeFilesystems(
	nodes []domain.Node,
	filesystems map[string]domain.NodeFilesystems,
) []domain.Node {
	if len(filesystems) == 0 {
		return nodes
	}

	enriched := make([]domain.Node, 0, len(nodes))
	for _, node := range nodes {
		if measured, ok := filesystems[node.Name()]; ok {
			node = node.WithFilesystems(measured)
		}
		enriched = append(enriched, node)
	}
	return enriched
}

// attachPodUsage returns the pods carrying their measured usage.
func attachPodUsage(pods []domain.Pod, usage map[string]domain.PodUsage) []domain.Pod {
	if len(usage) == 0 {
		return pods
	}

	enriched := make([]domain.Pod, 0, len(pods))
	for _, pod := range pods {
		if measured, ok := usage[pod.Namespace().String()+"/"+pod.Name()]; ok {
			pod = pod.WithPodUsage(measured)
		}
		enriched = append(enriched, pod)
	}
	return enriched
}

// metricsStatusFor maps a failed metrics read onto what to tell the operator.
//
// The adapter has already done the hard part: classifyMetrics turns 404 and
// 503 into ErrMetricsUnavailable, and everything else through the general
// classifier — so a 403 arrives as ErrForbidden and a dead cluster as
// ErrUnreachable. This only chooses which sentence those deserve.
func metricsStatusFor(err error) domain.MetricsStatus {
	switch {
	case errors.Is(err, ports.ErrMetricsUnavailable):
		return domain.MetricsNotInstalled
	case errors.Is(err, ports.ErrForbidden):
		return domain.MetricsForbidden
	default:
		return domain.MetricsFailed
	}
}
