package ports

import (
	"context"

	"k8sense/app/domain"
)

// ClusterService is the use-case surface for choosing and connecting to a
// cluster, plus the cluster-scoped navigation the UI needs to build.
type ClusterService interface {
	// ListClusters returns every cluster K8Sense knows about, in a stable
	// order suitable for direct display.
	ListClusters(ctx context.Context) ([]domain.Cluster, error)

	// Connect verifies that the given cluster answers, then makes it the
	// active cluster for subsequent resource queries. It returns the cluster
	// enriched with the version its API server reported.
	Connect(ctx context.Context, id domain.ClusterID) (domain.Cluster, error)

	// ActiveCluster returns the currently connected cluster, or an error
	// wrapping domain.ErrNoActiveCluster when Connect has not succeeded yet.
	ActiveCluster(ctx context.Context) (domain.Cluster, error)

	// ListNamespaces returns the namespaces of the active cluster.
	ListNamespaces(ctx context.Context) ([]domain.Namespace, error)
}

// WorkloadService is the use-case surface for reading workloads from the
// active cluster.
type WorkloadService interface {
	// ListPods returns the pods in the given namespace of the active cluster,
	// or across every namespace when it is domain.NamespaceAll.
	ListPods(ctx context.Context, namespace domain.NamespaceName) ([]domain.Pod, error)
}
