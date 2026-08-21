package wails

import (
	"errors"
	"log/slog"
	"os"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

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

// KubeconfigMerge is what adding a kubeconfig would change, or did.
type KubeconfigMerge struct {
	// Added names the contexts that would be, or were, added.
	Added []string `json:"added"`
	// Conflicts names contexts the kubeconfig already defines. A merge with
	// any of these is refused.
	Conflicts []string `json:"conflicts"`
	// Path is the file that was, or would be, written.
	Path string `json:"path"`
}

func toKubeconfigMerge(merge domain.KubeconfigMerge) KubeconfigMerge {
	// Never nil: the frontend maps over these, and `null` from Go would
	// arrive as a value it has to guard on every use.
	added := merge.Added
	if added == nil {
		added = []string{}
	}
	conflicts := merge.Conflicts
	if conflicts == nil {
		conflicts = []string{}
	}
	return KubeconfigMerge{Added: added, Conflicts: conflicts, Path: merge.Path}
}

// PreviewKubeconfig reports what adding the given kubeconfig would change.
//
// Separate from AddKubeconfig so the dialog can show what is about to happen
// while the operator is still typing, without a keystroke ever being able to
// write to the file.
func (c *ClusterAPI) PreviewKubeconfig(raw string) (KubeconfigMerge, error) {
	ctx, cancel := c.app.requestContext()
	defer cancel()

	merge, err := c.clusters.PreviewKubeconfig(ctx, raw)
	if err != nil {
		return KubeconfigMerge{}, apiError(c.logger, "PreviewKubeconfig", err)
	}
	return toKubeconfigMerge(merge), nil
}

// AddKubeconfig adds the given kubeconfig to the local one.
func (c *ClusterAPI) AddKubeconfig(raw string) (KubeconfigMerge, error) {
	ctx, cancel := c.app.requestContext()
	defer cancel()

	merge, err := c.clusters.AddKubeconfig(ctx, raw)
	if err != nil {
		return toKubeconfigMerge(merge), apiError(c.logger, "AddKubeconfig", err)
	}
	return toKubeconfigMerge(merge), nil
}

// ReadKubeconfigFile opens a native file picker and returns what was chosen.
//
// The file is read HERE rather than handed to the frontend as a path, because
// the webview cannot open files — and should not be able to. An empty string
// means the operator cancelled, which is not an error.
func (c *ClusterAPI) ReadKubeconfigFile() (string, error) {
	runtimeCtx, ok := c.app.runtimeContext()
	if !ok {
		return "", apiError(c.logger, "ReadKubeconfigFile",
			errors.New("the window is not ready"))
	}

	path, err := wailsruntime.OpenFileDialog(runtimeCtx, wailsruntime.OpenDialogOptions{
		Title: "Choose a kubeconfig",
		Filters: []wailsruntime.FileFilter{
			{DisplayName: "Kubeconfig (*.yaml, *.yml, *.conf, config)", Pattern: "*.yaml;*.yml;*.conf;config"},
			{DisplayName: "All files", Pattern: "*"},
		},
	})
	if err != nil {
		return "", apiError(c.logger, "ReadKubeconfigFile", err)
	}
	if path == "" {
		return "", nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "", apiError(c.logger, "ReadKubeconfigFile", err)
	}
	return string(content), nil
}
