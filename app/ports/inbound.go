package ports

import (
	"context"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// ClusterService is the use-case surface for choosing, connecting to and
// describing clusters.
//
// Connections are plural on purpose: the UI shows one tab per connected
// cluster, and closing a tab must not disturb the others.
type ClusterService interface {
	// ListClusters returns every cluster PodSteer knows about, in a stable
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

	// ListNamespaceSummaries returns the same namespaces with what is running
	// in each — the list view, where ListNamespaces serves the filter.
	ListNamespaceSummaries(ctx context.Context, id domain.ClusterID) ([]domain.NamespaceSummary, error)

	// ListNodes returns the nodes of a connected cluster.
	ListNodes(ctx context.Context, id domain.ClusterID) ([]domain.Node, error)

	// PreviewKubeconfig reports what adding raw to the kubeconfig would
	// change, without touching the file.
	PreviewKubeconfig(ctx context.Context, raw string) (domain.KubeconfigMerge, error)

	// AddKubeconfig adds raw to the kubeconfig and reports what changed.
	AddKubeconfig(ctx context.Context, raw string) (domain.KubeconfigMerge, error)
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

	// PodGraph returns the dependency chain around one pod, from whatever
	// routes to it down to its containers and what it consumes.
	PodGraph(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName string) (domain.PodGraph, error)

	// WorkloadGraph returns the dependency chain around one workload: what
	// routes to it, the pods it currently has, and what they consume.
	WorkloadGraph(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, kind domain.WorkloadKind, name string) (domain.PodGraph, error)

	// ListPodsOnNode returns the pods the scheduler has placed on one node,
	// across every namespace — "what is running on this machine" is a question
	// about the machine, not about a namespace.
	ListPodsOnNode(ctx context.Context, id domain.ClusterID, nodeName string) ([]domain.Pod, error)

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

	// SamplingInterval reports how often each open cluster is sampled.
	SamplingInterval() time.Duration

	// SetSamplingInterval changes the cadence, taking effect at once rather
	// than after the current interval elapses.
	SetSamplingInterval(interval time.Duration) error
}

// ResourceService is the use-case surface for the generic browsing path.
type ResourceService interface {
	// ListTable returns objects of the given kind as a table. The kind is
	// named by its ResourceKind.ID, which is what the navigator hands back.
	ListTable(ctx context.Context, id domain.ClusterID, kindID string, namespace domain.NamespaceName) (domain.ResourceTable, error)

	// NamespaceInventory reports what one namespace holds, kind by kind.
	//
	// A use case rather than a port method because the answer is assembled:
	// Kubernetes has no endpoint that counts a namespace's contents, so this
	// is one count per kind, and which kinds are worth counting is a decision
	// the domain makes.
	NamespaceInventory(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) (domain.NamespaceInventory, error)

	// GetManifest returns one object as YAML.
	GetManifest(ctx context.Context, id domain.ClusterID, kindID string, namespace domain.NamespaceName, name string, revealSecrets bool) (string, error)

	// RevealSecretKey returns one decoded Secret value, on explicit request.
	RevealSecretKey(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name, key string) (string, error)
}
