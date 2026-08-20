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

// withPodMetrics attaches usage to pods, degrading silently when the cluster
// serves no metrics API. See ClusterService.withNodeMetrics for why silently.
func (s *WorkloadService) withPodMetrics(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, pods []domain.Pod) []domain.Pod {
	if len(pods) == 0 {
		return pods
	}

	usage, err := s.metrics.PodMetrics(ctx, id, namespace)
	if err != nil {
		if !errors.Is(err, ports.ErrMetricsUnavailable) {
			s.logger.WarnContext(ctx, "pod metrics unavailable",
				slog.String("cluster", id.String()),
				slog.String("error", err.Error()))
		}
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
