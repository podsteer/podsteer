package wails

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// FleetAPI exposes the cross-cluster reads to the frontend.
//
// One bridge call per tick per table, however many clusters are open: the
// fan-out is on this side, and the frontend gets every cluster's rows and
// every cluster's verdict in one answer, grouped per cluster in tab order.
type FleetAPI struct {
	fleet  ports.FleetService
	app    *App
	logger *slog.Logger

	// chooseSavePath is the save dialog behind ExportPodsCSV — a seam for
	// the reason SystemAPI.chooseSavePath is one.
	chooseSavePath func(suggestedName string) (string, error)
}

// NewFleetAPI returns the bound fleet API.
func NewFleetAPI(fleet ports.FleetService, app *App, logger *slog.Logger) (*FleetAPI, error) {
	switch {
	case fleet == nil:
		return nil, errors.New("wails: FleetAPI requires a FleetService")
	case app == nil:
		return nil, errors.New("wails: FleetAPI requires an App")
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &FleetAPI{
		fleet:          fleet,
		app:            app,
		logger:         logger.With(slog.String("api", "fleet")),
		chooseSavePath: func(suggestedName string) (string, error) { return showSaveDialog(app, suggestedName) },
	}, nil
}

// ListPods lists pods in the given namespace of each named cluster.
//
// An empty namespace lists across all of them in every cluster; a named one
// is looked for in every cluster, and a cluster that does not have it simply
// contributes no rows.
func (f *FleetAPI) ListPods(clusterIDs []string, namespace string) ([]ClusterPods, error) {
	ctx, cancel := f.app.requestContext()
	defer cancel()

	ids, ns, err := fleetArgs(clusterIDs, namespace)
	if err != nil {
		return nil, apiError(f.logger, "ListPods", err)
	}

	reads, err := f.fleet.ListPods(ctx, ids, ns)
	if err != nil {
		return nil, apiError(f.logger, "ListPods", err)
	}

	return toClusterPods(reads, time.Now()), nil
}

// QueryPods returns one page of the merged pod list — the search, chips,
// cluster selection, sort and page the frontend's table says — and every
// cluster's verdict for the status strip. See FleetService.QueryPods.
//
// No projection: custom columns are per kind and the merged list is not a
// kind, exactly as ListPods reads it.
func (f *FleetAPI) QueryPods(clusterIDs []string, namespace string, query PodQuery) (FleetPodPage, error) {
	ctx, cancel := f.app.requestContext()
	defer cancel()

	ids, ns, err := fleetArgs(clusterIDs, namespace)
	if err != nil {
		return FleetPodPage{}, apiError(f.logger, "QueryPods", err)
	}

	page, err := f.fleet.QueryPods(ctx, ids, ns, query.toDomain())
	if err != nil {
		return FleetPodPage{}, apiError(f.logger, "QueryPods", err)
	}

	return toFleetPodPage(page, time.Now()), nil
}

// ExportPodsCSV writes every pod of the merged list the query matches, as
// CSV, to wherever the operator chooses — see WorkloadAPI.ExportPodsCSV for
// why this is written in Go. Returns "" when the dialog was cancelled.
func (f *FleetAPI) ExportPodsCSV(clusterIDs []string, namespace string, query PodQuery, columns []CSVColumn, suggestedName string) (string, error) {
	ids, ns, err := fleetArgs(clusterIDs, namespace)
	if err != nil {
		return "", apiError(f.logger, "ExportPodsCSV", err)
	}

	ctx, cancel := f.app.requestContext()
	pods, err := f.fleet.MatchingPods(ctx, ids, ns, query.toDomain())
	cancel()
	if err != nil {
		return "", apiError(f.logger, "ExportPodsCSV", err)
	}

	path, err := writeCSVExport(f.chooseSavePath, suggestedName, renderPodCSV(columns, toPods(pods, time.Now())))
	if err != nil {
		return "", apiError(f.logger, "ExportPodsCSV", err)
	}
	return path, nil
}

// ListWorkloads lists every fleet workload kind in the given namespace of
// each named cluster.
func (f *FleetAPI) ListWorkloads(clusterIDs []string, namespace string) ([]ClusterWorkloads, error) {
	ctx, cancel := f.app.requestContext()
	defer cancel()

	ids, ns, err := fleetArgs(clusterIDs, namespace)
	if err != nil {
		return nil, apiError(f.logger, "ListWorkloads", err)
	}

	reads, err := f.fleet.ListWorkloads(ctx, ids, ns)
	if err != nil {
		return nil, apiError(f.logger, "ListWorkloads", err)
	}

	return toClusterWorkloads(reads, time.Now()), nil
}

// ListEvents lists events in the given namespace of each named cluster.
func (f *FleetAPI) ListEvents(clusterIDs []string, namespace string) ([]ClusterEvents, error) {
	ctx, cancel := f.app.requestContext()
	defer cancel()

	ids, ns, err := fleetArgs(clusterIDs, namespace)
	if err != nil {
		return nil, apiError(f.logger, "ListEvents", err)
	}

	reads, err := f.fleet.ListEvents(ctx, ids, ns)
	if err != nil {
		return nil, apiError(f.logger, "ListEvents", err)
	}

	return toClusterEvents(reads, time.Now()), nil
}

// ListTable lists one arbitrary kind in the given namespace of each named
// cluster.
//
// THE KIND IS A GROUP AND A RESOURCE, not a kind id: an id carries a version
// and a version is per-cluster. `group` is empty for the core group, exactly
// as it is in a kind id. See FleetService.ListTable and
// domain.Catalog.LookupByResource.
func (f *FleetAPI) ListTable(clusterIDs []string, group, resource, namespace string) ([]ClusterTable, error) {
	ctx, cancel := f.app.requestContext()
	defer cancel()

	ids, ns, err := fleetArgs(clusterIDs, namespace)
	if err != nil {
		return nil, apiError(f.logger, "ListTable", err)
	}
	if strings.TrimSpace(resource) == "" {
		return nil, apiError(f.logger, "ListTable", fmt.Errorf("%w: no resource named", domain.ErrInvalidResourceKind))
	}

	reads, err := f.fleet.ListTable(ctx, ids, group, resource, ns)
	if err != nil {
		return nil, apiError(f.logger, "ListTable", err)
	}

	return toClusterTables(reads), nil
}

// fleetArgs validates the arguments the three reads share.
func fleetArgs(clusterIDs []string, namespace string) ([]domain.ClusterID, domain.NamespaceName, error) {
	ids := make([]domain.ClusterID, 0, len(clusterIDs))
	for _, raw := range clusterIDs {
		id, err := domain.NewClusterID(raw)
		if err != nil {
			return nil, "", err
		}
		ids = append(ids, id)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return nil, "", err
	}
	return ids, ns, nil
}

// fleetScopeArgs is fleetArgs for a set of namespaces; empty means every one.
func fleetScopeArgs(clusterIDs []string, namespaces []string) ([]domain.ClusterID, domain.NamespaceScope, error) {
	ids := make([]domain.ClusterID, 0, len(clusterIDs))
	for _, raw := range clusterIDs {
		id, err := domain.NewClusterID(raw)
		if err != nil {
			return nil, domain.NamespaceScope{}, err
		}
		ids = append(ids, id)
	}

	scope, err := domain.NewNamespaceScope(namespaces)
	if err != nil {
		return nil, domain.NamespaceScope{}, err
	}
	return ids, scope, nil
}

// ListPodsIn is ListPods over a set of namespaces.
func (f *FleetAPI) ListPodsIn(clusterIDs []string, namespaces []string) ([]ClusterPods, error) {
	ctx, cancel := f.app.requestContext()
	defer cancel()

	ids, scope, err := fleetScopeArgs(clusterIDs, namespaces)
	if err != nil {
		return nil, apiError(f.logger, "ListPodsIn", err)
	}

	reads, err := f.fleet.ListPodsIn(ctx, ids, scope)
	if err != nil {
		return nil, apiError(f.logger, "ListPodsIn", err)
	}
	return toClusterPods(reads, time.Now()), nil
}

// QueryPodsIn is QueryPods over a set of namespaces.
func (f *FleetAPI) QueryPodsIn(clusterIDs []string, namespaces []string, query PodQuery) (FleetPodPage, error) {
	ctx, cancel := f.app.requestContext()
	defer cancel()

	ids, scope, err := fleetScopeArgs(clusterIDs, namespaces)
	if err != nil {
		return FleetPodPage{}, apiError(f.logger, "QueryPodsIn", err)
	}

	page, err := f.fleet.QueryPodsIn(ctx, ids, scope, query.toDomain())
	if err != nil {
		return FleetPodPage{}, apiError(f.logger, "QueryPodsIn", err)
	}
	return toFleetPodPage(page, time.Now()), nil
}

// ExportPodsCSVIn is ExportPodsCSV over a set of namespaces.
func (f *FleetAPI) ExportPodsCSVIn(clusterIDs []string, namespaces []string, query PodQuery, columns []CSVColumn, suggestedName string) (string, error) {
	ids, scope, err := fleetScopeArgs(clusterIDs, namespaces)
	if err != nil {
		return "", apiError(f.logger, "ExportPodsCSVIn", err)
	}

	ctx, cancel := f.app.requestContext()
	pods, err := f.fleet.MatchingPodsIn(ctx, ids, scope, query.toDomain())
	cancel()
	if err != nil {
		return "", apiError(f.logger, "ExportPodsCSVIn", err)
	}

	path, err := writeCSVExport(f.chooseSavePath, suggestedName, renderPodCSV(columns, toPods(pods, time.Now())))
	if err != nil {
		return "", apiError(f.logger, "ExportPodsCSVIn", err)
	}
	return path, nil
}

// ListWorkloadsIn is ListWorkloads over a set of namespaces.
func (f *FleetAPI) ListWorkloadsIn(clusterIDs []string, namespaces []string) ([]ClusterWorkloads, error) {
	ctx, cancel := f.app.requestContext()
	defer cancel()

	ids, scope, err := fleetScopeArgs(clusterIDs, namespaces)
	if err != nil {
		return nil, apiError(f.logger, "ListWorkloadsIn", err)
	}

	reads, err := f.fleet.ListWorkloadsIn(ctx, ids, scope)
	if err != nil {
		return nil, apiError(f.logger, "ListWorkloadsIn", err)
	}
	return toClusterWorkloads(reads, time.Now()), nil
}

// ListEventsIn is ListEvents over a set of namespaces.
func (f *FleetAPI) ListEventsIn(clusterIDs []string, namespaces []string) ([]ClusterEvents, error) {
	ctx, cancel := f.app.requestContext()
	defer cancel()

	ids, scope, err := fleetScopeArgs(clusterIDs, namespaces)
	if err != nil {
		return nil, apiError(f.logger, "ListEventsIn", err)
	}

	reads, err := f.fleet.ListEventsIn(ctx, ids, scope)
	if err != nil {
		return nil, apiError(f.logger, "ListEventsIn", err)
	}
	return toClusterEvents(reads, time.Now()), nil
}

// ListTableIn is ListTable over a set of namespaces.
func (f *FleetAPI) ListTableIn(clusterIDs []string, group, resource string, namespaces []string) ([]ClusterTable, error) {
	ctx, cancel := f.app.requestContext()
	defer cancel()

	ids, scope, err := fleetScopeArgs(clusterIDs, namespaces)
	if err != nil {
		return nil, apiError(f.logger, "ListTableIn", err)
	}
	if strings.TrimSpace(resource) == "" {
		return nil, apiError(f.logger, "ListTableIn", fmt.Errorf("%w: no resource named", domain.ErrInvalidResourceKind))
	}

	reads, err := f.fleet.ListTableIn(ctx, ids, group, resource, scope)
	if err != nil {
		return nil, apiError(f.logger, "ListTableIn", err)
	}
	return toClusterTables(reads), nil
}
