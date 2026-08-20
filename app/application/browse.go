package application

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// This file holds the three smaller use cases that make the navigator work:
// what a cluster can show, its events, and the generic table path that serves
// every kind PodSteer has no purpose-built model for.

// BrowseServiceDeps are the collaborators the browsing services need.
type BrowseServiceDeps struct {
	// Resources reads any kind generically. Required.
	Resources ports.ResourcePort
	// Events reads Kubernetes Events. Required.
	Events ports.EventPort
	// Registry tracks open connections. Required.
	Registry *Registry
	// Catalog holds the browsable kinds per cluster. Required.
	Catalog *domain.Catalog
	// Logger receives diagnostics. Optional; defaults to slog.Default.
	Logger *slog.Logger
}

// BrowseService implements navigation, event and generic-resource use cases.
//
// The three are one type because they share exactly the same collaborators and
// are always wired together; splitting them would triple the constructor
// boilerplate in the composition root and buy nothing.
type BrowseService struct {
	resources ports.ResourcePort
	events    ports.EventPort
	registry  *Registry
	catalog   *domain.Catalog
	logger    *slog.Logger
}

// Compile-time proof that the service satisfies its inbound ports.
var (
	_ ports.NavigationService = (*BrowseService)(nil)
	_ ports.EventService      = (*BrowseService)(nil)
	_ ports.ResourceService   = (*BrowseService)(nil)
)

// NewBrowseService validates deps and returns the service.
func NewBrowseService(deps BrowseServiceDeps) (*BrowseService, error) {
	switch {
	case deps.Resources == nil:
		return nil, errors.New("application: BrowseService requires a ResourcePort")
	case deps.Events == nil:
		return nil, errors.New("application: BrowseService requires an EventPort")
	case deps.Registry == nil:
		return nil, errors.New("application: BrowseService requires a Registry")
	case deps.Catalog == nil:
		return nil, errors.New("application: BrowseService requires a Catalog")
	}

	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &BrowseService{
		resources: deps.Resources,
		events:    deps.Events,
		registry:  deps.Registry,
		catalog:   deps.Catalog,
		logger:    logger.With(slog.String("service", "browse")),
	}, nil
}

// Kinds returns every browsable kind in a connected cluster.
//
// Ordered by category, then by whether PodSteer models the kind richly, then
// alphabetically. Rich kinds float to the top of their section because they
// are the ones an operator opens most — a section that led with "Endpoints"
// because E sorts early would bury Pods.
func (s *BrowseService) Kinds(_ context.Context, id domain.ClusterID) ([]domain.ResourceKind, error) {
	if _, err := s.registry.Get(id); err != nil {
		return nil, fmt.Errorf("listing kinds: %w", err)
	}

	kinds := s.catalog.Kinds(id)
	categoryRank := make(map[domain.ResourceCategory]int, len(domain.CategoryOrder))
	for rank, category := range domain.CategoryOrder {
		categoryRank[category] = rank
	}

	slices.SortStableFunc(kinds, func(a, b domain.ResourceKind) int {
		if byCategory := cmp.Compare(categoryRank[a.Category], categoryRank[b.Category]); byCategory != 0 {
			return byCategory
		}
		if a.Rich != b.Rich {
			if a.Rich {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.Title, b.Title)
	})

	return kinds, nil
}

// ListEvents returns a cluster's events, most recently seen first.
//
// Warnings are floated above Normal events within the same instant, because an
// event list exists to answer "what is going wrong" and a burst of routine
// Scheduled events would otherwise bury the one BackOff that matters.
func (s *BrowseService) ListEvents(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) ([]domain.Event, error) {
	if _, err := s.registry.Get(id); err != nil {
		return nil, fmt.Errorf("listing events: %w", err)
	}

	events, err := s.events.ListEvents(ctx, id, namespace)
	if err != nil {
		return nil, fmt.Errorf("listing events in %q of %q: %w", namespace, id, err)
	}

	slices.SortStableFunc(events, func(a, b domain.Event) int {
		if a.IsWarning() != b.IsWarning() {
			if a.IsWarning() {
				return -1
			}
			return 1
		}
		// Descending: most recent first.
		return b.LastSeen().Compare(a.LastSeen())
	})

	return events, nil
}

// ListEventsForResource returns events for a specific resource.
//
// Filters events to only those related to the specified resource, sorted by
// most recently seen first with warnings prioritized.
func (s *BrowseService) ListEventsForResource(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, kind, name string) ([]domain.Event, error) {
	if _, err := s.registry.Get(id); err != nil {
		return nil, fmt.Errorf("listing events for resource: %w", err)
	}

	events, err := s.events.ListEventsForResource(ctx, id, namespace, kind, name)
	if err != nil {
		return nil, fmt.Errorf("listing events for %s/%s in %q of %q: %w", kind, name, namespace, id, err)
	}

	slices.SortStableFunc(events, func(a, b domain.Event) int {
		if a.IsWarning() != b.IsWarning() {
			if a.IsWarning() {
				return -1
			}
			return 1
		}
		// Descending: most recent first.
		return b.LastSeen().Compare(a.LastSeen())
	})

	return events, nil
}

// ListTable returns objects of the given kind as a generic table.
func (s *BrowseService) ListTable(ctx context.Context, id domain.ClusterID, kindID string, namespace domain.NamespaceName) (domain.ResourceTable, error) {
	if _, err := s.registry.Get(id); err != nil {
		return domain.ResourceTable{}, fmt.Errorf("listing resources: %w", err)
	}

	kind, err := s.catalog.Lookup(id, kindID)
	if err != nil {
		return domain.ResourceTable{}, fmt.Errorf("listing resources: %w", err)
	}

	// Asking for a namespace on a cluster-scoped kind returns nothing at all
	// rather than erroring, which looks like an empty cluster. Normalising
	// here means the frontend can leave its namespace filter set while the
	// operator clicks through to Nodes or StorageClasses.
	if !kind.Namespaced {
		namespace = domain.NamespaceAll
	}

	table, err := s.resources.ListTable(ctx, id, kind, namespace)
	if err != nil {
		return domain.ResourceTable{}, fmt.Errorf("listing %s in %q of %q: %w", kind.Title, namespace, id, err)
	}

	s.logger.DebugContext(ctx, "listed resources",
		slog.String("cluster", id.String()),
		slog.String("kind", kind.ID()),
		slog.Int("count", table.Len()))

	return table, nil
}

// GetManifest returns one object serialised as YAML.
func (s *BrowseService) GetManifest(ctx context.Context, id domain.ClusterID, kindID string, namespace domain.NamespaceName, name string) (string, error) {
	if _, err := s.registry.Get(id); err != nil {
		return "", fmt.Errorf("reading manifest: %w", err)
	}
	if name == "" {
		return "", fmt.Errorf("reading manifest: %w", domain.ErrEmptyResourceName)
	}

	kind, err := s.catalog.Lookup(id, kindID)
	if err != nil {
		return "", fmt.Errorf("reading manifest: %w", err)
	}
	if !kind.Namespaced {
		namespace = domain.NamespaceAll
	}

	manifest, err := s.resources.GetManifest(ctx, domain.ResourceRef{
		ClusterID: id,
		Kind:      kind,
		Namespace: namespace,
		Name:      name,
	})
	if err != nil {
		return "", fmt.Errorf("reading manifest of %s/%s: %w", kind.Kind, name, err)
	}

	return manifest, nil
}
