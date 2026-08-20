package ports

import (
	"context"
	"time"

	"k8sense/app/domain"
)

// ClusterService is the use-case surface for choosing, connecting to and
// describing clusters.
//
// Connections are plural on purpose: the UI shows one tab per connected
// cluster, and closing a tab must not disturb the others.
type ClusterService interface {
	// ListClusters returns every cluster K8Sense knows about, in a stable
	// order suitable for direct display.
	ListClusters(ctx context.Context) ([]domain.Cluster, error)

	// Connect verifies that the given cluster answers, opens a connection to
	// it and discovers its custom resource kinds. Connecting an already
	// connected cluster is not an error — it refreshes it.
	Connect(ctx context.Context, id domain.ClusterID) (domain.Cluster, error)

	// Disconnect closes a connection and releases everything cached for it.
	Disconnect(ctx context.Context, id domain.ClusterID) error

	// Connections returns the currently connected clusters, in the order they
	// were connected, so the tab bar does not reorder itself on refresh.
	Connections(ctx context.Context) ([]domain.Cluster, error)

	// ListNamespaces returns the namespaces of a connected cluster.
	ListNamespaces(ctx context.Context, id domain.ClusterID) ([]domain.Namespace, error)

	// ListNodes returns the nodes of a connected cluster.
	ListNodes(ctx context.Context, id domain.ClusterID) ([]domain.Node, error)
}

// NavigationService describes what a connected cluster can show.
//
// The navigator tree is built from this rather than hard-coded in the
// frontend, so a cluster's own CRDs appear in it without a frontend change,
// and a kind the credentials cannot read can be hidden centrally.
type NavigationService interface {
	// Kinds returns every browsable kind in the cluster, built-in first and
	// then whatever discovery found.
	Kinds(ctx context.Context, id domain.ClusterID) ([]domain.ResourceKind, error)
}

// WorkloadService is the use-case surface for reading workloads.
type WorkloadService interface {
	// ListPods returns pods in the given namespace of a connected cluster,
	// enriched with metrics where the cluster provides them.
	ListPods(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) ([]domain.Pod, error)

	// ListWorkloads returns controllers of the given kind.
	ListWorkloads(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName) ([]domain.Workload, error)

	// ListPodsForWorkload returns all pods owned by a specific workload.
	ListPodsForWorkload(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, kind domain.WorkloadKind, name string) ([]domain.Pod, error)
}

// EventService is the use-case surface for reading Kubernetes Events.
type EventService interface {
	// ListEvents returns events most-recent first, since an event list is
	// almost always read from the top.
	ListEvents(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) ([]domain.Event, error)

	// ListEventsForResource returns events for a specific resource.
	ListEventsForResource(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, kind, name string) ([]domain.Event, error)
}

// OverviewService is the use-case surface for the cluster dashboard.
//
// It is a use case of its own rather than a method on ClusterService because
// it composes almost every other read — nodes, pods, controllers, events and
// metrics — into a single assessment, and because it must succeed when several
// of those reads fail.
type OverviewService interface {
	// Overview assesses a connected cluster: what is wrong, what capacity is
	// left, and what the cluster is made of.
	//
	// Sources that could not be read are named in the result rather than
	// returned as an error. An error means the whole assessment failed, which
	// in practice means the cluster is not connected.
	Overview(ctx context.Context, id domain.ClusterID) (domain.Overview, error)
}

// HistoryService is the use-case surface for a cluster's recorded history.
//
// The history covers the window the application has been open, which is a
// weaker promise than a monitoring stack makes and must be presented as one:
// Series reports the span it actually holds so the UI can say so.
type HistoryService interface {
	// Series returns a cluster's samples over the given window, downsampled
	// to at most maxPoints.
	Series(ctx context.Context, id domain.ClusterID, window time.Duration, maxPoints int) (domain.Series, error)

	// Retention reports how long samples are kept.
	Retention(ctx context.Context) (domain.Retention, error)

	// SetRetention changes how long samples are kept, and immediately
	// discards anything already outside the new window — including
	// everything, when retention is set to zero.
	SetRetention(ctx context.Context, retention domain.Retention) error
}

// ResourceService is the use-case surface for the generic browsing path.
type ResourceService interface {
	// ListTable returns objects of the given kind as a table. The kind is
	// named by its ResourceKind.ID, which is what the navigator hands back.
	ListTable(ctx context.Context, id domain.ClusterID, kindID string, namespace domain.NamespaceName) (domain.ResourceTable, error)

	// GetManifest returns one object as YAML.
	GetManifest(ctx context.Context, id domain.ClusterID, kindID string, namespace domain.NamespaceName, name string) (string, error)
}
