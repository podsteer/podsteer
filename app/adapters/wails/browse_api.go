package wails

import (
	"errors"
	"log/slog"
	"time"

	"k8sense/app/domain"
	"k8sense/app/ports"
)

// BrowseAPI exposes navigation, events and the generic resource browser.
//
// It is the API that makes the navigator work for kinds K8Sense has no
// purpose-built view for — including every CRD in the cluster.
type BrowseAPI struct {
	navigation ports.NavigationService
	events     ports.EventService
	resources  ports.ResourceService
	app        *App
	logger     *slog.Logger
}

// NewBrowseAPI returns the bound browsing API.
func NewBrowseAPI(
	navigation ports.NavigationService,
	events ports.EventService,
	resources ports.ResourceService,
	app *App,
	logger *slog.Logger,
) (*BrowseAPI, error) {
	switch {
	case navigation == nil:
		return nil, errors.New("wails: BrowseAPI requires a NavigationService")
	case events == nil:
		return nil, errors.New("wails: BrowseAPI requires an EventService")
	case resources == nil:
		return nil, errors.New("wails: BrowseAPI requires a ResourceService")
	case app == nil:
		return nil, errors.New("wails: BrowseAPI requires an App")
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &BrowseAPI{
		navigation: navigation,
		events:     events,
		resources:  resources,
		app:        app,
		logger:     logger.With(slog.String("api", "browse")),
	}, nil
}

// ListKinds returns every browsable kind in a connected cluster.
//
// The navigator tree is built from this rather than hard-coded in the
// frontend, so a cluster's own operators appear in it without a frontend
// change.
func (b *BrowseAPI) ListKinds(clusterID string) ([]ResourceKind, error) {
	ctx, cancel := b.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return nil, apiError(b.logger, "ListKinds", err)
	}

	kinds, err := b.navigation.Kinds(ctx, id)
	if err != nil {
		return nil, apiError(b.logger, "ListKinds", err)
	}

	return toResourceKinds(kinds), nil
}

// ListEvents returns a cluster's events, warnings first and most recent first.
func (b *BrowseAPI) ListEvents(clusterID, namespace string) ([]Event, error) {
	ctx, cancel := b.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return nil, apiError(b.logger, "ListEvents", err)
	}

	name, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return nil, apiError(b.logger, "ListEvents", err)
	}

	events, err := b.events.ListEvents(ctx, id, name)
	if err != nil {
		return nil, apiError(b.logger, "ListEvents", err)
	}

	return toEvents(events, time.Now()), nil
}

// ListEventsForResource returns events for a specific resource, warnings first and most recent first.
func (b *BrowseAPI) ListEventsForResource(clusterID, namespace, kind, name string) ([]Event, error) {
	ctx, cancel := b.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return nil, apiError(b.logger, "ListEventsForResource", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return nil, apiError(b.logger, "ListEventsForResource", err)
	}

	events, err := b.events.ListEventsForResource(ctx, id, ns, kind, name)
	if err != nil {
		return nil, apiError(b.logger, "ListEventsForResource", err)
	}

	return toEvents(events, time.Now()), nil
}

// ListTable returns objects of any kind as a table, with the columns the API
// server prints. This is the generic path behind Config, Network, Storage,
// Access Control and Custom Resources.
func (b *BrowseAPI) ListTable(clusterID, kindID, namespace string) (ResourceTable, error) {
	ctx, cancel := b.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return ResourceTable{}, apiError(b.logger, "ListTable", err)
	}

	name, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return ResourceTable{}, apiError(b.logger, "ListTable", err)
	}

	table, err := b.resources.ListTable(ctx, id, kindID, name)
	if err != nil {
		return ResourceTable{}, apiError(b.logger, "ListTable", err)
	}

	return toResourceTable(table), nil
}

// GetManifest returns one object as YAML, for the detail view.
func (b *BrowseAPI) GetManifest(clusterID, kindID, namespace, name string) (string, error) {
	ctx, cancel := b.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return "", apiError(b.logger, "GetManifest", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return "", apiError(b.logger, "GetManifest", err)
	}

	manifest, err := b.resources.GetManifest(ctx, id, kindID, ns, name)
	if err != nil {
		return "", apiError(b.logger, "GetManifest", err)
	}

	return manifest, nil
}
