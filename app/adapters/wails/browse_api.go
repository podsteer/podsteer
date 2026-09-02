package wails

import (
	"errors"
	"log/slog"
	"time"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// BrowseAPI exposes navigation, events and the generic resource browser.
//
// It is the API that makes the navigator work for kinds PodSteer has no
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

// NamespaceInventory reports what one namespace holds, kind by kind.
//
// One request per built-in namespaced kind, so it is called when a panel's
// section is opened rather than on every refresh — the counts are cheap
// individually and there are twenty of them.
func (b *BrowseAPI) NamespaceInventory(clusterID, namespace string) (NamespaceInventory, error) {
	ctx, cancel := b.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return NamespaceInventory{}, apiError(b.logger, "NamespaceInventory", err)
	}

	name, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return NamespaceInventory{}, apiError(b.logger, "NamespaceInventory", err)
	}

	inventory, err := b.resources.NamespaceInventory(ctx, id, name)
	if err != nil {
		return NamespaceInventory{}, apiError(b.logger, "NamespaceInventory", err)
	}

	return toNamespaceInventory(inventory), nil
}

// ClassifyConditions says which of an object's status conditions report a
// problem.
//
// A PURE CALL — it reaches no cluster and cannot fail. It exists because the
// polarity of a condition is a verdict and verdicts live in the domain (see
// CLAUDE.md), and because getting one backwards is invisible until somebody
// is reading the wrong colour during an incident: the rule it replaced
// coloured every healthy node as a warning.
//
// Takes the whole list rather than one condition, so a panel showing eight of
// them crosses the boundary once.
func (b *BrowseAPI) ClassifyConditions(conditions []ConditionRef) []string {
	tones := make([]string, 0, len(conditions))
	for _, condition := range conditions {
		tones = append(tones, string(domain.ClassifyCondition(condition.Type, condition.Status)))
	}
	return tones
}

// GetManifest returns one object as YAML, for the detail view.
//
// revealSecrets applies to core/v1 Secrets and nothing else: false replaces
// their values with the decoded SIZE, the way `kubectl describe secret` does.
// The default is false at every call site, and the true path exists only
// behind a deliberate click — reading a Secret is an audited action, and the
// YAML tab would otherwise perform one every time somebody browsed past.
func (b *BrowseAPI) GetManifest(clusterID, kindID, namespace, name string, revealSecrets bool) (string, error) {
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

	manifest, err := b.resources.GetManifest(ctx, id, kindID, ns, name, revealSecrets)
	if err != nil {
		return "", apiError(b.logger, "GetManifest", err)
	}

	return manifest, nil
}

// RevealSecretKey returns one decoded Secret value, for a deliberate reveal.
//
// Bound as its own narrow method rather than folded into anything the UI
// calls on its own. Nothing in PodSteer reaches this except a person clicking
// to reveal one key, which is what keeps each entry in a cluster's audit log
// interpretable — a client that resolved every referenced Secret when a pane
// opened would produce the exact burst pattern Kubernetes' Secret
// good-practices page tells operators to alert on.
//
// The returned value is never logged, never cached and never written to a
// crash report: apiError below records the operation and the error, and the
// error from the layers underneath carries the key name at most.
func (b *BrowseAPI) RevealSecretKey(clusterID, namespace, name, key string) (string, error) {
	ctx, cancel := b.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return "", apiError(b.logger, "RevealSecretKey", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return "", apiError(b.logger, "RevealSecretKey", err)
	}

	value, err := b.resources.RevealSecretKey(ctx, id, ns, name, key)
	if err != nil {
		return "", apiError(b.logger, "RevealSecretKey", err)
	}

	return value, nil
}
