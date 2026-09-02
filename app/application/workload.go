package application

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// WorkloadServiceDeps are the collaborators WorkloadService needs.
type WorkloadServiceDeps struct {
	// Workloads reads pods and controllers. Required.
	Workloads ports.WorkloadPort
	// Metrics supplies pod usage. Required, but allowed to fail.
	Metrics ports.MetricsPort
	// Registry tracks open connections. Required.
	Registry *Registry
	// Logger receives diagnostics. Optional; defaults to slog.Default.
	Logger *slog.Logger
}

// WorkloadService implements the workload-reading use cases.
type WorkloadService struct {
	workloads ports.WorkloadPort
	metrics   ports.MetricsPort
	registry  *Registry
	logger    *slog.Logger
}

// Compile-time proof that the service satisfies its inbound port.
var _ ports.WorkloadService = (*WorkloadService)(nil)

// NewWorkloadService validates deps and returns the service.
func NewWorkloadService(deps WorkloadServiceDeps) (*WorkloadService, error) {
	switch {
	case deps.Workloads == nil:
		return nil, errors.New("application: WorkloadService requires a WorkloadPort")
	case deps.Metrics == nil:
		return nil, errors.New("application: WorkloadService requires a MetricsPort")
	case deps.Registry == nil:
		return nil, errors.New("application: WorkloadService requires a Registry")
	}

	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &WorkloadService{
		workloads: deps.Workloads,
		metrics:   deps.Metrics,
		registry:  deps.Registry,
		logger:    logger.With(slog.String("service", "workload")),
	}, nil
}

// ListPods returns the pods of a connected cluster, enriched with usage.
//
// Sorted by namespace then name so the table is stable between refreshes: the
// API server returns pods in etcd key order, which shifts as objects come and
// go and would make rows jump under the cursor.
func (s *WorkloadService) ListPods(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) ([]domain.Pod, error) {
	if _, err := s.registry.Get(id); err != nil {
		return nil, fmt.Errorf("listing pods: %w", err)
	}

	pods, err := s.workloads.ListPods(ctx, id, namespace)
	if err != nil {
		return nil, fmt.Errorf("listing pods in %q of %q: %w", namespace, id, err)
	}

	pods = s.withPodMetrics(ctx, id, namespace, pods)

	slices.SortStableFunc(pods, func(a, b domain.Pod) int {
		if byNamespace := cmp.Compare(a.Namespace(), b.Namespace()); byNamespace != 0 {
			return byNamespace
		}
		return cmp.Compare(a.Name(), b.Name())
	})

	return pods, nil
}

// ListWorkloads returns controllers of the given kind, sorted by namespace
// then name.
func (s *WorkloadService) ListWorkloads(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName) ([]domain.Workload, error) {
	if _, err := s.registry.Get(id); err != nil {
		return nil, fmt.Errorf("listing %ss: %w", kind, err)
	}

	workloads, err := s.workloads.ListWorkloads(ctx, id, kind, namespace)
	if err != nil {
		return nil, fmt.Errorf("listing %ss in %q of %q: %w", kind, namespace, id, err)
	}

	slices.SortStableFunc(workloads, func(a, b domain.Workload) int {
		if byNamespace := cmp.Compare(a.Namespace(), b.Namespace()); byNamespace != 0 {
			return byNamespace
		}
		return cmp.Compare(a.Name(), b.Name())
	})

	return workloads, nil
}

// WorkloadConsumption sums what each controller in a list is using.
//
// SEPARATE FROM ListWorkloads, and called alongside it rather than inside it.
// The controllers themselves are one cheap read; this is the namespace's pods
// and their metrics, and on a cluster where the metrics API is missing or the
// account cannot list pods the list must still render. Folding it in would
// make a column cost the page.
//
// A pod is attributed by the controller's own label selector — see
// domain.WorkloadConsumption for why that rather than the ownerReference
// chain. CronJobs have no selector, so their Jobs are read as the missing
// hop; nothing else needs a second list.
func (s *WorkloadService) WorkloadConsumption(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName) (map[string]domain.AggregateUsage, error) {
	if _, err := s.registry.Get(id); err != nil {
		return nil, fmt.Errorf("reading %s usage: %w", kind, err)
	}

	workloads, err := s.workloads.ListWorkloads(ctx, id, kind, namespace)
	if err != nil {
		return nil, fmt.Errorf("listing %ss in %q of %q: %w", kind, namespace, id, err)
	}

	pods, err := s.workloads.ListPods(ctx, id, namespace)
	if err != nil {
		return nil, fmt.Errorf("listing pods in %q of %q: %w", namespace, id, err)
	}
	pods, _ = podsWithUsage(ctx, s.metrics, s.logger, id, namespace, pods)

	var intermediates []domain.Workload
	if kind == domain.WorkloadCronJob {
		jobs, err := s.workloads.ListWorkloads(ctx, id, domain.WorkloadJob, namespace)
		if err != nil {
			// The Jobs are the only way a CronJob reaches its pods. Without
			// them every CronJob reads as running nothing, which is also its
			// ordinary state between runs — so the two would be
			// indistinguishable, and the honest answer is to fail rather than
			// to report "idle" for a cluster that would not say.
			return nil, fmt.Errorf("listing jobs in %q of %q: %w", namespace, id, err)
		}
		intermediates = jobs
	}

	return domain.WorkloadConsumption(workloads, pods, intermediates), nil
}

// withPodMetrics attaches usage to pods, degrading silently when the cluster
// serves no metrics API. See ClusterService.withNodeMetrics for why silently.
func (s *WorkloadService) withPodMetrics(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, pods []domain.Pod) []domain.Pod {
	enriched, _ := podsWithUsage(ctx, s.metrics, s.logger, id, namespace, pods)
	return enriched
}

// podsWithUsage attaches measured usage to pods and reports whether the
// cluster answered at all.
//
// Shared by the two use cases that need pods with figures on them. The BOOLEAN
// is the reason it is not simply a slice: zero usage on a cluster with no
// metrics API is the absence of a measurement, and a caller that cannot tell
// the two apart reports an unmeasured namespace as an idle one.
func podsWithUsage(
	ctx context.Context,
	metrics ports.MetricsPort,
	logger *slog.Logger,
	id domain.ClusterID,
	namespace domain.NamespaceName,
	pods []domain.Pod,
) ([]domain.Pod, bool) {
	if len(pods) == 0 {
		return pods, false
	}

	usage, err := metrics.PodMetrics(ctx, id, namespace)
	if err != nil {
		if !errors.Is(err, ports.ErrMetricsUnavailable) {
			logger.WarnContext(ctx, "pod metrics unavailable",
				slog.String("cluster", id.String()),
				slog.String("error", err.Error()))
		}
		return pods, false
	}

	enriched := make([]domain.Pod, 0, len(pods))
	for _, pod := range pods {
		if measured, ok := usage[pod.Namespace().String()+"/"+pod.Name()]; ok {
			pod = pod.WithPodUsage(measured)
		}
		enriched = append(enriched, pod)
	}

	return enriched, true
}

// WorkloadUsage sums what a controller's pods are consuming.
//
// ITS PODS, FETCHED NOW, rather than anything held about the controller. A
// controller has no usage of its own — it is a template and a replica count —
// so the only honest answer is the sum over whatever it currently has, which
// is also why a Deployment scaled to zero and a CronJob between runs both
// correctly read as nothing.
//
// Called while a panel is open rather than alongside the list: summing every
// controller's pods on every refresh of the controller list would list the
// namespace's pods once per controller, and the figure is only ever looked at
// one controller at a time.
func (s *WorkloadService) WorkloadUsage(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, kind domain.WorkloadKind, name string) (domain.AggregateUsage, error) {
	if _, err := s.registry.Get(id); err != nil {
		return domain.AggregateUsage{}, fmt.Errorf("reading %s usage: %w", kind, err)
	}

	pods, err := s.workloads.ListPodsForWorkload(ctx, id, namespace, kind, name)
	if err != nil {
		return domain.AggregateUsage{}, fmt.Errorf(
			"reading pods of %s/%s in %q: %w", kind, name, namespace, err)
	}

	pods, _ = podsWithUsage(ctx, s.metrics, s.logger, id, namespace, pods)

	return domain.NewAggregateUsage(pods), nil
}

// PodGraph returns the dependency chain around one pod.
//
// Thin, and correctly so: the reading is the adapter's and the rules are the
// domain's. What is left here is the registry check every use case does and
// the join between the two.
func (s *WorkloadService) PodGraph(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName string) (domain.PodGraph, error) {
	if _, err := s.registry.Get(id); err != nil {
		return domain.PodGraph{}, fmt.Errorf("mapping pod dependencies: %w", err)
	}

	input, err := s.workloads.PodGraphSources(ctx, id, namespace, podName)
	if err != nil {
		return domain.PodGraph{}, fmt.Errorf("reading dependencies for %s/%s in %q: %w",
			namespace, podName, id, err)
	}
	return domain.NewPodGraph(input), nil
}

// WorkloadGraph returns the dependency chain around one workload.
func (s *WorkloadService) WorkloadGraph(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, kind domain.WorkloadKind, name string) (domain.PodGraph, error) {
	if _, err := s.registry.Get(id); err != nil {
		return domain.PodGraph{}, fmt.Errorf("mapping workload dependencies: %w", err)
	}

	input, err := s.workloads.WorkloadGraphSources(ctx, id, namespace, kind, name)
	if err != nil {
		return domain.PodGraph{}, fmt.Errorf("reading dependencies for %s/%s in %q: %w",
			kind, name, namespace, err)
	}
	return domain.NewWorkloadGraph(input), nil
}

// ListPodsOnNode returns the pods the scheduler has placed on one node.
//
// NOT ENRICHED WITH METRICS, unlike the workload listing. Usage for these pods
// is already on the node's own list row, which is where somebody opened this
// from; fetching PodMetrics for every namespace to decorate a list of names
// would be a second cluster-wide read for a column the panel does not show.
func (s *WorkloadService) ListPodsOnNode(ctx context.Context, id domain.ClusterID, nodeName string) ([]domain.Pod, error) {
	if _, err := s.registry.Get(id); err != nil {
		return nil, fmt.Errorf("listing pods on node: %w", err)
	}

	pods, err := s.workloads.ListPodsOnNode(ctx, id, nodeName)
	if err != nil {
		return nil, fmt.Errorf("listing pods on node %q of %q: %w", nodeName, id, err)
	}
	return pods, nil
}

// ListPodsForWorkload returns all pods owned by a specific workload.
func (s *WorkloadService) ListPodsForWorkload(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, kind domain.WorkloadKind, name string) ([]domain.Pod, error) {
	if _, err := s.registry.Get(id); err != nil {
		return nil, fmt.Errorf("listing pods for workload: %w", err)
	}

	pods, err := s.workloads.ListPodsForWorkload(ctx, id, namespace, kind, name)
	if err != nil {
		return nil, fmt.Errorf("listing pods for %s/%s in %q of %q: %w", kind, name, namespace, id, err)
	}

	// Enrich with metrics
	pods = s.withPodMetrics(ctx, id, namespace, pods)

	return pods, nil
}
