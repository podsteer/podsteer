package wails

import (
	"errors"
	"log/slog"
	"time"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// ClusterAPI exposes the cluster use cases to the frontend.
//
// Wails binds this struct's exported methods as `ClusterAPI.ListClusters()`
// and so on, generating matching TypeScript. Method names and signatures are
// therefore public API.
//
// Every method that touches a cluster takes its id: the UI holds one tab per
// connected cluster and the backend keeps no notion of which one is in front.
type ClusterAPI struct {
	clusters ports.ClusterService
	app      *App
	logger   *slog.Logger
}

// NewClusterAPI returns the bound cluster API.
func NewClusterAPI(clusters ports.ClusterService, app *App, logger *slog.Logger) (*ClusterAPI, error) {
	switch {
	case clusters == nil:
		return nil, errors.New("wails: ClusterAPI requires a ClusterService")
	case app == nil:
		return nil, errors.New("wails: ClusterAPI requires an App")
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &ClusterAPI{
		clusters: clusters,
		app:      app,
		logger:   logger.With(slog.String("api", "cluster")),
	}, nil
}

// ListClusters returns every cluster in the local kubeconfig.
//
// This is the one call that works before anything is connected, so it is what
// the frontend uses to populate the cluster picker at launch. Clusters already
// open are returned carrying their version, so the picker can mark them.
func (c *ClusterAPI) ListClusters() ([]Cluster, error) {
	ctx, cancel := c.app.requestContext()
	defer cancel()

	clusters, err := c.clusters.ListClusters(ctx)
	if err != nil {
		return nil, apiError(c.logger, "ListClusters", err)
	}

	return toClusters(clusters), nil
}

// Connect opens a cluster and returns it enriched with its server version.
//
// Connecting an already open cluster refreshes it rather than failing, so the
// frontend can call this to reconnect a tab whose credentials expired.
func (c *ClusterAPI) Connect(clusterID string) (Cluster, error) {
	ctx, cancel := c.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return Cluster{}, apiError(c.logger, "Connect", err)
	}

	cluster, err := c.clusters.Connect(ctx, id)
	if err != nil {
		return Cluster{}, apiError(c.logger, "Connect", err)
	}

	return toCluster(cluster), nil
}

// Disconnect closes a cluster, for when the operator closes its tab.
func (c *ClusterAPI) Disconnect(clusterID string) error {
	ctx, cancel := c.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return apiError(c.logger, "Disconnect", err)
	}

	if err := c.clusters.Disconnect(ctx, id); err != nil {
		// Closing a tab for a cluster that is already gone is a race the UI
		// should not have to handle, not an error worth showing.
		if errors.Is(err, domain.ErrClusterNotConnected) {
			return nil
		}
		return apiError(c.logger, "Disconnect", err)
	}

	return nil
}

// Connections returns the open clusters, in the order they were opened.
//
// The frontend rebuilds its tab bar from this, which is why the order must be
// stable: tabs that reshuffle are how somebody acts on the wrong cluster.
func (c *ClusterAPI) Connections() ([]Cluster, error) {
	ctx, cancel := c.app.requestContext()
	defer cancel()

	clusters, err := c.clusters.Connections(ctx)
	if err != nil {
		return nil, apiError(c.logger, "Connections", err)
	}

	return toClusters(clusters), nil
}

// ListNamespaces returns the namespaces of a connected cluster.
func (c *ClusterAPI) ListNamespaces(clusterID string) ([]Namespace, error) {
	ctx, cancel := c.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return nil, apiError(c.logger, "ListNamespaces", err)
	}

	namespaces, err := c.clusters.ListNamespaces(ctx, id)
	if err != nil {
		return nil, apiError(c.logger, "ListNamespaces", err)
	}

	return toNamespaces(namespaces, time.Now()), nil
}

// ListNodes returns the nodes of a connected cluster, with usage where the
// cluster provides metrics.
func (c *ClusterAPI) ListNodes(clusterID string) ([]Node, error) {
	ctx, cancel := c.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return nil, apiError(c.logger, "ListNodes", err)
	}

	nodes, err := c.clusters.ListNodes(ctx, id)
	if err != nil {
		return nil, apiError(c.logger, "ListNodes", err)
	}

	return toNodes(nodes, time.Now()), nil
}
