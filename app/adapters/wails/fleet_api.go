package wails

import (
	"errors"
	"log/slog"
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
		fleet:  fleet,
		app:    app,
		logger: logger.With(slog.String("api", "fleet")),
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
