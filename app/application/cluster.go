package application

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"k8sense/app/domain"
	"k8sense/app/ports"
)

// ClusterServiceDeps are the collaborators ClusterService needs.
//
// Grouping them in a struct keeps the constructor readable as the service
// grows and makes each dependency named at the wiring site in app/cmd.
type ClusterServiceDeps struct {
	// Kubeconfig discovers the clusters this machine can talk to. Required.
	Kubeconfig ports.KubeconfigPort
	// Kubernetes reads from a cluster's API server. Required.
	Kubernetes ports.KubernetesPort
	// Events receives connection lifecycle events. Required.
	Events ports.EventPublisher
	// Session holds the active cluster. Required.
	Session *Session
	// Logger receives diagnostics. Optional; defaults to slog.Default.
	Logger *slog.Logger
	// Now supplies the current time. Optional; defaults to time.Now. It is
	// injectable so tests can assert on event timestamps.
	Now func() time.Time
}

// ClusterService implements the cluster-selection use cases.
type ClusterService struct {
	kubeconfig ports.KubeconfigPort
	kubernetes ports.KubernetesPort
	events     ports.EventPublisher
	session    *Session
	logger     *slog.Logger
	now        func() time.Time
}

// Compile-time proof that the service satisfies its inbound port.
var _ ports.ClusterService = (*ClusterService)(nil)

// NewClusterService validates deps and returns the service.
//
// Missing required dependencies are reported as an error rather than
// panicking at first use, so a wiring mistake fails at startup with a message
// naming the dependency.
func NewClusterService(deps ClusterServiceDeps) (*ClusterService, error) {
	switch {
	case deps.Kubeconfig == nil:
		return nil, errors.New("application: ClusterService requires a KubeconfigPort")
	case deps.Kubernetes == nil:
		return nil, errors.New("application: ClusterService requires a KubernetesPort")
	case deps.Events == nil:
		return nil, errors.New("application: ClusterService requires an EventPublisher")
	case deps.Session == nil:
		return nil, errors.New("application: ClusterService requires a Session")
	}

	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}

	return &ClusterService{
		kubeconfig: deps.Kubeconfig,
		kubernetes: deps.Kubernetes,
		events:     deps.Events,
		session:    deps.Session,
		logger:     logger.With(slog.String("service", "cluster")),
		now:        now,
	}, nil
}

// ListClusters returns every cluster described by the local kubeconfig.
//
// The result is ordered for display: the kubeconfig's current context first —
// it is what the operator almost always wants — then the rest alphabetically,
// so the list does not reshuffle between calls.
func (s *ClusterService) ListClusters(ctx context.Context) ([]domain.Cluster, error) {
	clusters, err := s.kubeconfig.Clusters(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing clusters: %w", err)
	}

	slices.SortStableFunc(clusters, func(a, b domain.Cluster) int {
		if a.IsCurrent() != b.IsCurrent() {
			if a.IsCurrent() {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.ID(), b.ID())
	})

	s.logger.DebugContext(ctx, "listed clusters", slog.Int("count", len(clusters)))
	return clusters, nil
}

// Connect probes the given cluster and, if it answers, makes it active.
//
// The version round trip is the point: it proves the endpoint is reachable and
// the credentials are accepted *before* the UI navigates into a cluster view
// and starts issuing queries that would each fail separately.
func (s *ClusterService) Connect(ctx context.Context, id domain.ClusterID) (domain.Cluster, error) {
	if id.IsZero() {
		return domain.Cluster{}, fmt.Errorf("connecting: %w", domain.ErrEmptyClusterID)
	}

	cluster, err := s.kubeconfig.Cluster(ctx, id)
	if err != nil {
		return domain.Cluster{}, fmt.Errorf("connecting to %q: %w", id, err)
	}

	version, err := s.kubernetes.ServerVersion(ctx, id)
	if err != nil {
		// The operator needs to know the attempt failed even though the caller
		// also gets the error, because a failed connect leaves the UI showing
		// whichever cluster was active before.
		s.events.Publish(ctx, domain.ClusterUnreachable{
			ClusterID: id,
			Reason:    err.Error(),
			At:        s.now(),
		})
		s.logger.WarnContext(ctx, "cluster unreachable",
			slog.String("cluster", id.String()),
			slog.String("error", err.Error()))

		return domain.Cluster{}, fmt.Errorf("connecting to %q: %w", id, err)
	}

	connected := cluster.WithVersion(version)
	s.session.Activate(connected)

	s.events.Publish(ctx, domain.ClusterConnected{Cluster: connected, At: s.now()})
	s.logger.InfoContext(ctx, "connected to cluster",
		slog.String("cluster", id.String()),
		slog.String("version", version.GitVersion))

	return connected, nil
}

// ActiveCluster returns the currently connected cluster.
func (s *ClusterService) ActiveCluster(_ context.Context) (domain.Cluster, error) {
	return s.session.Active()
}

// ListNamespaces returns the namespaces of the active cluster, sorted by name.
func (s *ClusterService) ListNamespaces(ctx context.Context) ([]domain.Namespace, error) {
	active, err := s.session.Active()
	if err != nil {
		return nil, fmt.Errorf("listing namespaces: %w", err)
	}

	namespaces, err := s.kubernetes.ListNamespaces(ctx, active.ID())
	if err != nil {
		return nil, fmt.Errorf("listing namespaces of %q: %w", active.ID(), err)
	}

	slices.SortStableFunc(namespaces, func(a, b domain.Namespace) int {
		return cmp.Compare(a.Name(), b.Name())
	})

	s.logger.DebugContext(ctx, "listed namespaces",
		slog.String("cluster", active.ID().String()),
		slog.Int("count", len(namespaces)))

	return namespaces, nil
}
