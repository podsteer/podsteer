package wails

import (
	"errors"
	"log/slog"
	"time"

	"k8sense/app/domain"
	"k8sense/app/ports"
)

// ClusterAPI exposes the cluster use cases to the frontend.
//
// Wails binds this struct's exported methods as `ClusterAPI.ListClusters()`
// and so on, generating matching TypeScript. Method names and signatures are
// therefore public API.
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
// the frontend uses to populate the cluster picker at launch.
func (c *ClusterAPI) ListClusters() ([]Cluster, error) {
	ctx, cancel := c.app.requestContext()
	defer cancel()

	clusters, err := c.clusters.ListClusters(ctx)
	if err != nil {
		return nil, apiError(c.logger, "ListClusters", err)
	}

	return toClusters(clusters), nil
}

// Connect verifies that the given cluster answers and makes it active.
//
// It returns the cluster enriched with the version its API server reported,
// which is what lets the UI show a version badge immediately rather than
// issuing a second call for it.
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

// ActiveCluster returns the currently connected cluster, or null when none is.
//
// "Nothing connected yet" is the normal state at launch, not a failure, so it
// is reported as a null result rather than a rejected promise — otherwise the
// frontend would have to catch an exception on its very first render.
func (c *ClusterAPI) ActiveCluster() (*Cluster, error) {
	ctx, cancel := c.app.requestContext()
	defer cancel()

	cluster, err := c.clusters.ActiveCluster(ctx)
	if err != nil {
		if errors.Is(err, domain.ErrNoActiveCluster) {
			return nil, nil
		}
		return nil, apiError(c.logger, "ActiveCluster", err)
	}

	dto := toCluster(cluster)
	return &dto, nil
}

// ListNamespaces returns the namespaces of the active cluster.
func (c *ClusterAPI) ListNamespaces() ([]Namespace, error) {
	ctx, cancel := c.app.requestContext()
	defer cancel()

	namespaces, err := c.clusters.ListNamespaces(ctx)
	if err != nil {
		return nil, apiError(c.logger, "ListNamespaces", err)
	}

	return toNamespaces(namespaces, time.Now()), nil
}
