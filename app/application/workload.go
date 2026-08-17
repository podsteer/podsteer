package application

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"k8sense/app/domain"
	"k8sense/app/ports"
)

// WorkloadServiceDeps are the collaborators WorkloadService needs.
type WorkloadServiceDeps struct {
	// Kubernetes reads from a cluster's API server. Required.
	Kubernetes ports.KubernetesPort
	// Session supplies the active cluster. Required.
	Session *Session
	// Logger receives diagnostics. Optional; defaults to slog.Default.
	Logger *slog.Logger
}

// WorkloadService implements the workload-reading use cases.
type WorkloadService struct {
	kubernetes ports.KubernetesPort
	session    *Session
	logger     *slog.Logger
}

// Compile-time proof that the service satisfies its inbound port.
var _ ports.WorkloadService = (*WorkloadService)(nil)

// NewWorkloadService validates deps and returns the service.
func NewWorkloadService(deps WorkloadServiceDeps) (*WorkloadService, error) {
	switch {
	case deps.Kubernetes == nil:
		return nil, errors.New("application: WorkloadService requires a KubernetesPort")
	case deps.Session == nil:
		return nil, errors.New("application: WorkloadService requires a Session")
	}

	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &WorkloadService{
		kubernetes: deps.Kubernetes,
		session:    deps.Session,
		logger:     logger.With(slog.String("service", "workload")),
	}, nil
}

// ListPods returns the pods of the active cluster in the given namespace, or
// across every namespace when it is domain.NamespaceAll.
//
// Results are sorted by namespace then name so the table is stable between
// refreshes: the API server returns pods in etcd key order, which shifts as
// objects are created and deleted and would make rows jump under the cursor.
func (s *WorkloadService) ListPods(ctx context.Context, namespace domain.NamespaceName) ([]domain.Pod, error) {
	active, err := s.session.Active()
	if err != nil {
		return nil, fmt.Errorf("listing pods: %w", err)
	}

	pods, err := s.kubernetes.ListPods(ctx, active.ID(), namespace)
	if err != nil {
		return nil, fmt.Errorf("listing pods in %q of %q: %w", namespace.String(), active.ID(), err)
	}

	slices.SortStableFunc(pods, func(a, b domain.Pod) int {
		if byNamespace := cmp.Compare(a.Namespace(), b.Namespace()); byNamespace != 0 {
			return byNamespace
		}
		return cmp.Compare(a.Name(), b.Name())
	})

	s.logger.DebugContext(ctx, "listed pods",
		slog.String("cluster", active.ID().String()),
		slog.String("namespace", namespace.String()),
		slog.Int("count", len(pods)))

	return pods, nil
}
