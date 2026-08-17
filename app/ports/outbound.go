package ports

import (
	"context"

	"k8sense/app/domain"
)

// KubeconfigPort discovers the clusters this machine is configured to talk to.
//
// It is deliberately separate from KubernetesPort: enumerating contexts is a
// local filesystem concern that must keep working when every cluster in the
// file is unreachable. Folding it into the API client would make the cluster
// picker fail whenever the VPN is down — precisely when the operator needs it.
type KubeconfigPort interface {
	// Clusters returns every cluster described by the local kubeconfig.
	//
	// Contexts that cannot be turned into a valid domain.Cluster are skipped
	// rather than failing the call: one malformed entry must not hide the rest
	// of the file. An error is returned only when no cluster could be read at
	// all, wrapping ErrKubeconfigUnavailable.
	Clusters(ctx context.Context) ([]domain.Cluster, error)

	// Cluster returns the single cluster with the given id, wrapping
	// domain.ErrClusterNotFound when the kubeconfig describes no such context.
	Cluster(ctx context.Context, id domain.ClusterID) (domain.Cluster, error)
}

// KubernetesPort reads resources from a cluster's API server.
//
// Every method takes the cluster id explicitly rather than the port holding a
// bound connection, because a desktop client routinely fans out across several
// clusters at once and a stateful "current connection" would serialise them.
// Implementations are expected to cache their transport per cluster.
//
// All methods must honour ctx cancellation: they are called from UI handlers
// where the operator can navigate away mid-request.
type KubernetesPort interface {
	// ServerVersion reaches the cluster's API server and reports its version.
	// It doubles as the reachability probe used when connecting.
	ServerVersion(ctx context.Context, id domain.ClusterID) (domain.ServerVersion, error)

	// ListNamespaces returns every namespace visible to the configured
	// credentials.
	ListNamespaces(ctx context.Context, id domain.ClusterID) ([]domain.Namespace, error)

	// ListPods returns the pods in the given namespace, or across every
	// namespace when it is domain.NamespaceAll.
	ListPods(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) ([]domain.Pod, error)
}

// EventPublisher delivers domain events to whatever is observing the
// application — in the desktop build, the Svelte frontend via the Wails event
// bus.
//
// Publish returns nothing and must not block: an event is a notification, not
// a transaction, and a use case must never fail or stall because the UI is
// slow to listen. Implementations that can fail are expected to log and drop.
type EventPublisher interface {
	// Publish delivers event to all observers.
	Publish(ctx context.Context, event domain.Event)
}
