package wails

import (
	"errors"
	"log/slog"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// OverviewAPI exposes the cluster dashboard.
//
// One call rather than a dozen: the assessment needs nodes, pods, controllers,
// events and metrics together, and letting the frontend fetch them separately
// would mean the dashboard reasoned about a cluster it saw at six different
// moments — and would put the analysis in the browser, where it could not be
// tested.
type OverviewAPI struct {
	overview ports.OverviewService
	app      *App
	logger   *slog.Logger
}

// NewOverviewAPI returns the bound overview API.
func NewOverviewAPI(overview ports.OverviewService, app *App, logger *slog.Logger) (*OverviewAPI, error) {
	switch {
	case overview == nil:
		return nil, errors.New("wails: OverviewAPI requires an OverviewService")
	case app == nil:
		return nil, errors.New("wails: OverviewAPI requires an App")
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &OverviewAPI{
		overview: overview,
		app:      app,
		logger:   logger.With(slog.String("api", "overview")),
	}, nil
}

// GetOverview assesses a connected cluster.
//
// An error here means the assessment could not be produced at all — in
// practice, that the cluster is not connected. A cluster missing individual
// sources still returns an overview, with those sources named in
// Overview.Unavailable.
func (o *OverviewAPI) GetOverview(clusterID string) (Overview, error) {
	ctx, cancel := o.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return Overview{}, apiError(o.logger, "GetOverview", err)
	}

	overview, err := o.overview.Overview(ctx, id)
	if err != nil {
		return Overview{}, apiError(o.logger, "GetOverview", err)
	}

	return toOverview(overview), nil
}

// GetOverviewForTarget assesses a connected cluster the way GetOverview does,
// but scores the upgrade-impact findings against a chosen Kubernetes minor
// rather than the default of the next one — what the overview header's
// "check against" selector calls when an operator picks a different target.
//
// targetMinor is e.g. "1.33". An empty string asks for the default, the same
// as GetOverview.
func (o *OverviewAPI) GetOverviewForTarget(clusterID string, targetMinor string) (Overview, error) {
	ctx, cancel := o.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return Overview{}, apiError(o.logger, "GetOverviewForTarget", err)
	}

	overview, err := o.overview.OverviewForTarget(ctx, id, targetMinor)
	if err != nil {
		return Overview{}, apiError(o.logger, "GetOverviewForTarget", err)
	}

	return toOverview(overview), nil
}
