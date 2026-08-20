package application

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

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
	// Registry tracks open connections. Required.
	Registry *Registry
	// Logger receives diagnostics. Optional; defaults to slog.Default.
	Logger *slog.Logger
}

// OverviewService assembles the cluster dashboard.
type OverviewService struct {
	cluster   ports.ClusterPort
	workloads ports.WorkloadPort
	events    ports.EventPort
	metrics   ports.MetricsPort
	registry  *Registry
	logger    *slog.Logger
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
	if _, err := s.registry.Get(id); err != nil {
		return domain.Overview{}, err
	}

	var (
		mu          sync.Mutex
		wg          sync.WaitGroup
		unavailable []string

		version    domain.ServerVersion
		nodes      []domain.Node
		pods       []domain.Pod
		workloads  []domain.Workload
		events     []domain.Event
		namespaces []domain.Namespace

		nodeUsage map[string]domain.Metrics
		podUsage  map[string]domain.Metrics
		measured  bool
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
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := read(); err != nil {
				degrade(source, err)
			}
		}()
	}

	run("version", func() error {
		result, err := s.cluster.ServerVersion(ctx, id)
		version = result
		return err
	})

	run("nodes", func() error {
		result, err := s.cluster.ListNodes(ctx, id)
		nodes = result
		return err
	})

	run("pods", func() error {
		result, err := s.workloads.ListPods(ctx, id, domain.NamespaceAll)
		pods = result
		return err
	})

	run("namespaces", func() error {
		result, err := s.cluster.ListNamespaces(ctx, id)
		namespaces = result
		return err
	})

	run("events", func() error {
		result, err := s.events.ListEvents(ctx, id, domain.NamespaceAll)
		events = result
		return err
	})

	run("metrics", func() error {
		result, err := s.metrics.NodeMetrics(ctx, id)
		if err != nil {
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

	// One goroutine per controller kind, each appending under the lock. The
	// order they finish in does not matter: the assessment sorts everything it
	// reports.
	for _, kind := range controllerKinds {
		run("workloads/"+string(kind), func() error {
			result, err := s.workloads.ListWorkloads(ctx, id, kind, domain.NamespaceAll)
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

	// Metrics arrive keyed separately from the objects they describe, so they
	// are joined here rather than in the domain: the domain should not know
	// that usage came from a different API than the pod did.
	nodes = attachNodeUsage(nodes, nodeUsage)
	pods = attachPodUsage(pods, podUsage)

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID:       id,
		Version:         version,
		Nodes:           nodes,
		Pods:            pods,
		Workloads:       workloads,
		Events:          events,
		Namespaces:      namespaces,
		Unavailable:     unavailable,
		MetricsMeasured: measured,
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

// attachPodUsage returns the pods carrying their measured usage.
func attachPodUsage(pods []domain.Pod, usage map[string]domain.Metrics) []domain.Pod {
	if len(usage) == 0 {
		return pods
	}

	enriched := make([]domain.Pod, 0, len(pods))
	for _, pod := range pods {
		if measured, ok := usage[pod.Namespace().String()+"/"+pod.Name()]; ok {
			pod = pod.WithUsage(measured)
		}
		enriched = append(enriched, pod)
	}
	return enriched
}
