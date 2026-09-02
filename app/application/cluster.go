package application

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// ClusterServiceDeps are the collaborators ClusterService needs.
type ClusterServiceDeps struct {
	// Kubeconfig discovers the clusters this machine can talk to. Required.
	Kubeconfig ports.KubeconfigPort
	// Cluster reads cluster-scoped facts from an API server. Required.
	Cluster ports.ClusterPort
	// Workloads reads the pods a namespace summary counts. Required.
	//
	// A namespace is a name and a phase and nothing else as far as Kubernetes
	// is concerned; everything worth knowing about one is what is inside it.
	Workloads ports.WorkloadPort
	// Metrics supplies node usage. Required, but allowed to fail.
	Metrics ports.MetricsPort
	// Events receives connection lifecycle events. Required.
	Events ports.EventPublisher
	// Registry tracks open connections. Required.
	Registry *Registry
	// Catalog holds the browsable kinds per cluster. Required.
	Catalog *domain.Catalog
	// Invalidator releases an adapter's per-cluster caches on disconnect.
	//
	// Optional, and deliberately not a port: it is a property of the
	// Kubernetes adapter's caching, not something the domain asks for. A
	// service wired without one simply keeps whatever the adapter cached,
	// which is what every test wants.
	Invalidator ClusterInvalidator
	// Logger receives diagnostics. Optional; defaults to slog.Default.
	Logger *slog.Logger
	// Now supplies the current time. Optional; defaults to time.Now.
	Now func() time.Time
}

// ClusterService implements the cluster-selection use cases.
// ClusterInvalidator drops everything an adapter has cached for one cluster.
//
// Defined here, at the consumer, because this is the only thing that needs
// it. The Kubernetes adapter satisfies it without being told to.
type ClusterInvalidator interface {
	Invalidate(id domain.ClusterID)
}

type ClusterService struct {
	kubeconfig  ports.KubeconfigPort
	cluster     ports.ClusterPort
	workloads   ports.WorkloadPort
	metrics     ports.MetricsPort
	invalidator ClusterInvalidator
	events      ports.EventPublisher
	registry    *Registry
	catalog     *domain.Catalog
	logger      *slog.Logger
	now         func() time.Time
}

// Compile-time proof that the service satisfies its inbound port.
var _ ports.ClusterService = (*ClusterService)(nil)

// NewClusterService validates deps and returns the service.
func NewClusterService(deps ClusterServiceDeps) (*ClusterService, error) {
	switch {
	case deps.Kubeconfig == nil:
		return nil, errors.New("application: ClusterService requires a KubeconfigPort")
	case deps.Cluster == nil:
		return nil, errors.New("application: ClusterService requires a ClusterPort")
	case deps.Workloads == nil:
		return nil, errors.New("application: ClusterService requires a WorkloadPort")
	case deps.Metrics == nil:
		return nil, errors.New("application: ClusterService requires a MetricsPort")
	case deps.Events == nil:
		return nil, errors.New("application: ClusterService requires an EventPublisher")
	case deps.Registry == nil:
		return nil, errors.New("application: ClusterService requires a Registry")
	case deps.Catalog == nil:
		return nil, errors.New("application: ClusterService requires a Catalog")
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
		kubeconfig:  deps.Kubeconfig,
		cluster:     deps.Cluster,
		workloads:   deps.Workloads,
		metrics:     deps.Metrics,
		invalidator: deps.Invalidator,
		events:      deps.Events,
		registry:    deps.Registry,
		catalog:     deps.Catalog,
		logger:      logger.With(slog.String("service", "cluster")),
		now:         now,
	}, nil
}

// ListClusters returns every cluster described by the local kubeconfig,
// current context first and then alphabetically.
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

	// Report clusters already open as reachable, carrying the version they
	// reported, so the picker distinguishes open from merely configured.
	for i, cluster := range clusters {
		if open, err := s.registry.Get(cluster.ID()); err == nil {
			clusters[i] = open
		}
	}

	s.logger.DebugContext(ctx, "listed clusters",
		slog.Int("count", len(clusters)),
		slog.Int("open", s.registry.Len()))

	return clusters, nil
}

// Connect probes a cluster, opens it and discovers its custom resource kinds.
func (s *ClusterService) Connect(ctx context.Context, id domain.ClusterID) (domain.Cluster, error) {
	if id.IsZero() {
		return domain.Cluster{}, fmt.Errorf("connecting: %w", domain.ErrEmptyClusterID)
	}

	cluster, err := s.kubeconfig.Cluster(ctx, id)
	if err != nil {
		return domain.Cluster{}, fmt.Errorf("connecting to %q: %w", id, err)
	}

	version, err := s.cluster.ServerVersion(ctx, id)
	if err != nil {
		// The operator needs to know the attempt failed even though the caller
		// also gets the error, because a failed connect leaves the UI showing
		// whatever it was showing before.
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
	s.registry.Open(connected)

	// Discovery is best-effort. A cluster whose CRDs cannot be listed — RBAC
	// commonly forbids it — is still perfectly usable for everything built in,
	// so this must never fail the connection.
	if kinds, err := s.cluster.DiscoverCustomKinds(ctx, id); err != nil {
		s.logger.WarnContext(ctx, "custom resource discovery failed; built-in kinds only",
			slog.String("cluster", id.String()),
			slog.String("error", err.Error()))
	} else {
		s.catalog.SetCustom(id, kinds)
		s.logger.DebugContext(ctx, "discovered custom resource kinds",
			slog.String("cluster", id.String()),
			slog.Int("count", len(kinds)))
	}

	s.events.Publish(ctx, domain.ClusterConnected{Cluster: connected, At: s.now()})
	s.logger.InfoContext(ctx, "connected to cluster",
		slog.String("cluster", id.String()),
		slog.String("version", version.GitVersion))

	return connected, nil
}

// Disconnect closes a connection and forgets everything cached for it.
func (s *ClusterService) Disconnect(ctx context.Context, id domain.ClusterID) error {
	if !s.registry.Close(id) {
		return fmt.Errorf("disconnecting %q: %w", id, domain.ErrClusterNotConnected)
	}

	// The catalog must be cleared too, or a cluster's CRDs would linger and
	// reappear in the navigator when a different cluster is opened.
	s.catalog.Forget(id)

	// And the adapter's caches, or disconnecting releases nothing: the pooled
	// TLS connections stay open, the disk sweep is served to the next
	// connection from before this one closed, and — worst — the cached client
	// is keyed by kubeconfig CONTEXT NAME, so a context rewritten to a new
	// server or a rotated token keeps talking to the old one for the life of
	// the process.
	if s.invalidator != nil {
		s.invalidator.Invalidate(id)
	}

	s.logger.InfoContext(ctx, "disconnected from cluster", slog.String("cluster", id.String()))
	return nil
}

// Connections returns the open clusters in connection order.
func (s *ClusterService) Connections(_ context.Context) ([]domain.Cluster, error) {
	return s.registry.All(), nil
}

// ListNamespaces returns the namespaces of a connected cluster, sorted by name.
func (s *ClusterService) ListNamespaces(ctx context.Context, id domain.ClusterID) ([]domain.Namespace, error) {
	if _, err := s.registry.Get(id); err != nil {
		return nil, fmt.Errorf("listing namespaces: %w", err)
	}

	namespaces, err := s.cluster.ListNamespaces(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("listing namespaces of %q: %w", id, err)
	}

	slices.SortStableFunc(namespaces, func(a, b domain.Namespace) int {
		return cmp.Compare(a.Name(), b.Name())
	})

	return namespaces, nil
}

// ListNamespaceSummaries returns every namespace with what is running in it.
//
// TWO READS, CONCURRENTLY: the namespaces, and every pod in the cluster. The
// second is the expensive one and there is no cheaper way to it — Kubernetes
// has no per-namespace pod count, and asking each namespace separately turns
// one request into one per namespace.
//
// A namespace list that is only names and phases is what every other client
// shows, and it answers nothing: the questions actually asked of one are
// whether a namespace is still in use, whether anything in it is broken, and
// which of them is holding the cluster.
func (s *ClusterService) ListNamespaceSummaries(ctx context.Context, id domain.ClusterID) ([]domain.NamespaceSummary, error) {
	if _, err := s.registry.Get(id); err != nil {
		return nil, fmt.Errorf("summarising namespaces: %w", err)
	}

	var (
		namespaces []domain.Namespace
		pods       []domain.Pod
	)

	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		result, err := s.cluster.ListNamespaces(groupCtx, id)
		if err != nil {
			return fmt.Errorf("listing namespaces of %q: %w", id, err)
		}
		namespaces = result
		return nil
	})
	group.Go(func() error {
		result, err := s.workloads.ListPods(groupCtx, id, domain.NamespaceAll)
		if err != nil {
			return fmt.Errorf("listing pods of %q: %w", id, err)
		}
		pods = result
		return nil
	})

	if err := group.Wait(); err != nil {
		return nil, fmt.Errorf("summarising namespaces: %w", err)
	}

	pods, measured := podsWithUsage(ctx, s.metrics, s.logger, id, domain.NamespaceAll, pods)

	return domain.NewNamespaceSummaries(namespaces, pods, measured), nil
}

// ListNodes returns the nodes of a connected cluster, enriched with usage.
//
// Control-plane nodes sort first, then alphabetically: they are the ones whose
// health explains everything else, so they belong at the top of the list.
// PreviewKubeconfig reports what adding raw would change, touching nothing.
func (s *ClusterService) PreviewKubeconfig(
	ctx context.Context, raw string,
) (domain.KubeconfigMerge, error) {
	return s.kubeconfig.PreviewMerge(ctx, raw)
}

// AddKubeconfig adds raw to the kubeconfig and reports what changed.
//
// Nothing is connected as a result. Adding a cluster and opening it are
// separate acts: a paste that turns out to name six contexts should not open
// six connections, and the picker showing them is the confirmation that the
// write worked.
func (s *ClusterService) AddKubeconfig(
	ctx context.Context, raw string,
) (domain.KubeconfigMerge, error) {
	merge, err := s.kubeconfig.Merge(ctx, raw)
	if err != nil {
		return merge, err
	}

	s.logger.InfoContext(ctx, "kubeconfig updated",
		slog.Int("added", len(merge.Added)))
	return merge, nil
}

func (s *ClusterService) ListNodes(ctx context.Context, id domain.ClusterID) ([]domain.Node, error) {
	if _, err := s.registry.Get(id); err != nil {
		return nil, fmt.Errorf("listing nodes: %w", err)
	}

	nodes, err := s.cluster.ListNodes(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("listing nodes of %q: %w", id, err)
	}

	nodes = s.withNodeMetrics(ctx, id, nodes)
	nodes = s.withNodeFilesystems(ctx, id, nodes)

	slices.SortStableFunc(nodes, func(a, b domain.Node) int {
		if a.IsControlPlane() != b.IsControlPlane() {
			if a.IsControlPlane() {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.Name(), b.Name())
	})

	return nodes, nil
}

// withNodeFilesystems attaches disk occupancy, when the kubelets will say.
//
// SILENT ON FAILURE, and more readily than the others. This is the only read
// in the node list that needs `nodes/proxy` — no aggregated API carries disk
// occupancy, metrics-server included — and plenty of clusters deliberately do
// not grant it. A node list that failed, or warned, because a permission
// nobody expected to have is missing would be worse than a node list with an
// empty column.
//
// The adapter caches this: it is one request per node and answers a question
// whose value moves in hours, so a refresh every ten seconds does not become
// a sweep of every kubelet every ten seconds.
func (s *ClusterService) withNodeFilesystems(ctx context.Context, id domain.ClusterID, nodes []domain.Node) []domain.Node {
	if len(nodes) == 0 {
		return nodes
	}

	disks, err := s.metrics.NodeFilesystems(ctx, id)
	if err != nil {
		if !errors.Is(err, ports.ErrMetricsUnavailable) {
			s.logger.DebugContext(ctx, "node filesystems unavailable",
				slog.String("cluster", id.String()),
				slog.String("error", err.Error()))
		}
		return nodes
	}

	enriched := make([]domain.Node, 0, len(nodes))
	for _, node := range nodes {
		if measured, ok := disks[node.Name()]; ok {
			node = node.WithFilesystems(measured)
		}
		enriched = append(enriched, node)
	}
	return enriched
}

// withNodeMetrics attaches usage to nodes, degrading silently when the cluster
// serves no metrics API.
//
// Silently is the right word: a cluster without metrics-server is an ordinary
// configuration, not a fault, and logging a warning per refresh would fill the
// log with noise about something nobody intends to change.
func (s *ClusterService) withNodeMetrics(ctx context.Context, id domain.ClusterID, nodes []domain.Node) []domain.Node {
	usage, err := s.metrics.NodeMetrics(ctx, id)
	if err != nil {
		if !errors.Is(err, ports.ErrMetricsUnavailable) {
			s.logger.WarnContext(ctx, "node metrics unavailable",
				slog.String("cluster", id.String()),
				slog.String("error", err.Error()))
		}
		return nodes
	}

	enriched := make([]domain.Node, 0, len(nodes))
	for _, node := range nodes {
		// Copy rather than mutate: domain entities are immutable values.
		if measured, ok := usage[node.Name()]; ok {
			node = node.WithUsage(measured)
		}
		enriched = append(enriched, node)
	}

	return enriched
}
