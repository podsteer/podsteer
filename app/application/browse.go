package application

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"golang.org/x/sync/errgroup"

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
		// Then by the project that publishes it, so a category's custom
		// resources arrive already gathered under their owner and the
		// navigator has only to draw the headings. Empty sorts first, which
		// is every built-in kind: they have no owner but Kubernetes.
		if bySubcategory := cmp.Compare(a.Subcategory, b.Subcategory); bySubcategory != 0 {
			return bySubcategory
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
func (s *BrowseService) ListEvents(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, projection domain.Projection) ([]domain.Event, error) {
	if _, err := s.registry.Get(id); err != nil {
		return nil, fmt.Errorf("listing events: %w", err)
	}

	events, err := s.events.ListEvents(ctx, id, namespace, projection)
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
func (s *BrowseService) ListTable(ctx context.Context, id domain.ClusterID, kindID string, namespace domain.NamespaceName, projection domain.Projection) (domain.ResourceTable, error) {
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

	table, err := s.resources.ListTable(ctx, id, kind, namespace, projection)
	if err != nil {
		return domain.ResourceTable{}, fmt.Errorf("listing %s in %q of %q: %w", kind.Title, namespace, id, err)
	}

	s.logger.DebugContext(ctx, "listed resources",
		slog.String("cluster", id.String()),
		slog.String("kind", kind.ID()),
		slog.Int("count", table.Len()))

	return table, nil
}

// inventoryConcurrency bounds the fan-out of a namespace inventory.
//
// One request per kind, and there are around twenty of them. Issued all at
// once they would spend the client's whole burst allowance on counting, and
// arrive at the API server as a spike from a client that is meant to be
// reading a panel — so they are paced. Eight at a time keeps the panel filling
// in well under a second on a normal round trip without being a thundering
// herd on a cluster where every call is slow.
const inventoryConcurrency = 8

// NamespaceInventory reports what a namespace holds, kind by kind.
//
// FANNED OUT HERE RATHER THAN IN THE ADAPTER, because which kinds are worth
// counting is a decision (see domain.CountableKinds) and the adapter's job is
// to count one of them. A kind that cannot be read is recorded and the rest
// continue: an account with `list pods` and not `list secrets` gets an
// inventory that says so, which is far more use than an error.
func (s *BrowseService) NamespaceInventory(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) (domain.NamespaceInventory, error) {
	if _, err := s.registry.Get(id); err != nil {
		return domain.NamespaceInventory{}, fmt.Errorf("counting namespace contents: %w", err)
	}
	if namespace.IsAll() {
		return domain.NamespaceInventory{}, fmt.Errorf(
			"counting namespace contents: %w", domain.ErrInvalidNamespaceName)
	}

	kinds := domain.CountableKinds(s.catalog.Kinds(id))
	counts := make([]domain.ResourceCount, len(kinds))

	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(inventoryConcurrency)

	for index, kind := range kinds {
		group.Go(func() error {
			count, err := s.resources.CountResources(groupCtx, id, kind, namespace)
			if err != nil {
				// Cancellation is the one failure that is not this kind's:
				// the whole call is being abandoned, and recording it against
				// every kind would report a cancelled panel as a cluster that
				// refuses everything.
				if groupCtx.Err() != nil {
					return err
				}
				counts[index] = domain.ResourceCount{Kind: kind, Unreadable: countRefusal(err)}
				return nil
			}
			counts[index] = domain.ResourceCount{Kind: kind, Count: count}
			return nil
		})
	}

	if err := group.Wait(); err != nil {
		return domain.NamespaceInventory{}, fmt.Errorf(
			"counting contents of %q in %q: %w", namespace, id, err)
	}

	inventory := domain.NewNamespaceInventory(namespace, counts)

	s.logger.DebugContext(ctx, "counted namespace contents",
		slog.String("cluster", id.String()),
		slog.String("namespace", namespace.String()),
		slog.Int("kinds", len(kinds)),
		slog.Int("objects", inventory.Total),
		slog.Int("unreadable", inventory.Unreadable))

	return inventory, nil
}

// countRefusal turns a failed count into the short reason a panel shows beside
// the kind, in place of a number.
//
// Short and factual, because it is rendered in a table cell rather than in a
// banner: the operator is reading twenty rows and needs to know which of them
// are missing and roughly why, not to be told a sentence twenty times.
func countRefusal(err error) string {
	switch {
	case errors.Is(err, ports.ErrForbidden):
		return "not permitted"
	case errors.Is(err, ports.ErrNotFound):
		return "not served here"
	case errors.Is(err, ports.ErrCountUnavailable):
		return "not reported by this API server"
	default:
		return "could not be read"
	}
}

// GetManifest returns one object serialised as YAML.
func (s *BrowseService) GetManifest(ctx context.Context, id domain.ClusterID, kindID string, namespace domain.NamespaceName, name string, revealSecrets bool) (string, error) {
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
	}, revealSecrets)
	if err != nil {
		return "", fmt.Errorf("reading manifest of %s/%s: %w", kind.Kind, name, err)
	}

	return manifest, nil
}

// ObjectGraph returns the neighbourhood map of one object of any kind.
//
// Thin, and correctly so — the same shape as WorkloadService.PodGraph: the
// reading is the adapter's, the rules are the domain's, and what is left here
// is the registry check every use case does, the catalogue lookup that turns a
// navigator id into a kind, and the join between the two.
func (s *BrowseService) ObjectGraph(ctx context.Context, id domain.ClusterID, kindID string, namespace domain.NamespaceName, name string) (domain.PodGraph, error) {
	if _, err := s.registry.Get(id); err != nil {
		return domain.PodGraph{}, fmt.Errorf("mapping object dependencies: %w", err)
	}
	if name == "" {
		return domain.PodGraph{}, fmt.Errorf("mapping object dependencies: %w", domain.ErrEmptyResourceName)
	}

	kind, err := s.catalog.Lookup(id, kindID)
	if err != nil {
		return domain.PodGraph{}, fmt.Errorf("mapping object dependencies: %w", err)
	}
	if !kind.Namespaced {
		namespace = domain.NamespaceAll
	}

	input, err := s.resources.ObjectGraphSources(ctx, domain.ResourceRef{
		ClusterID: id,
		Kind:      kind,
		Namespace: namespace,
		Name:      name,
	})
	if err != nil {
		return domain.PodGraph{}, fmt.Errorf("reading dependencies of %s/%s in %q: %w",
			kind.Kind, name, id, err)
	}

	return domain.NewObjectGraph(input), nil
}

// RevealSecretKey returns one decoded Secret value, on explicit request.
//
// Deliberately not part of GetManifest, ListPods or anything else that runs
// on a timer or when a pane opens. Every call to this is a person clicking
// "reveal" on one key, which is what makes the resulting audit entry
// interpretable by whoever reads the cluster's audit log.
func (s *BrowseService) RevealSecretKey(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name, key string) (string, error) {
	if _, err := s.registry.Get(id); err != nil {
		return "", fmt.Errorf("revealing secret key: %w", err)
	}
	if name == "" || key == "" {
		return "", fmt.Errorf("revealing secret key: %w", domain.ErrEmptyResourceName)
	}

	value, err := s.resources.RevealSecretKey(ctx, id, namespace, name, key)
	if err != nil {
		// The value is never in the error, and the key name is the most that
		// is logged. An RBAC denial here is ordinary — plenty of engineers
		// deliberately hold no `get secrets` — so this is not warned about.
		return "", fmt.Errorf("revealing key %q of secret %q: %w", key, name, err)
	}

	return value, nil
}

// InspectTLSSecret parses one Secret's certificate material, on explicit
// request.
//
// Deliberately not part of GetManifest or anything else that runs when a
// pane opens — see RevealSecretKey just above. The certificate itself is
// public material — anything terminating TLS with it hands it to every
// client that connects — but it lives inside the same Secret as the private
// key, and a read of that object is a read of that object regardless of
// which half somebody wanted. So this is its own deliberate act too, gated
// the same way and logged the same way: the Secret is named, its contents
// never are.
func (s *BrowseService) InspectTLSSecret(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name string) (domain.CertificateChain, error) {
	if _, err := s.registry.Get(id); err != nil {
		return domain.CertificateChain{}, fmt.Errorf("inspecting certificate: %w", err)
	}
	if name == "" {
		return domain.CertificateChain{}, fmt.Errorf("inspecting certificate: %w", domain.ErrEmptyResourceName)
	}

	chain, err := s.resources.InspectTLSSecret(ctx, id, namespace, name)
	if err != nil {
		return domain.CertificateChain{}, fmt.Errorf("inspecting certificate of secret %q: %w", name, err)
	}

	s.logger.DebugContext(ctx, "inspected certificate",
		slog.String("cluster", id.String()),
		slog.String("namespace", namespace.String()),
		slog.String("secret", name))

	return chain, nil
}

// VulnerabilitySummaries returns what a scanner already running in the
// cluster has recorded about one namespace's workloads.
//
// NOTHING ABOUT THE POD LIST DEPENDS ON THIS. It is called once when the pods
// view opens, on its own, and whatever it returns is merged onto rows that
// were already drawn — so a slow answer costs a late chip rather than a late
// list, and no answer costs nothing at all. That is also why an empty result
// is not distinguished from "no scanner": both mean there is nothing to show,
// and the adapter caches them identically.
func (s *BrowseService) VulnerabilitySummaries(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) ([]domain.VulnerabilitySummary, error) {
	if _, err := s.registry.Get(id); err != nil {
		return nil, fmt.Errorf("reading vulnerability reports: %w", err)
	}

	summaries, err := s.resources.ListVulnerabilitySummaries(ctx, id, namespace)
	if err != nil {
		return nil, fmt.Errorf("reading vulnerability reports in %q: %w", namespace, err)
	}

	return summaries, nil
}
