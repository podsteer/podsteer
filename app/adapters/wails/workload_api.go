package wails

import (
	"errors"
	"log/slog"
	"time"

	"k8sense/app/domain"
	"k8sense/app/ports"
)

// WorkloadAPI exposes the workload use cases to the frontend.
type WorkloadAPI struct {
	workloads ports.WorkloadService
	app       *App
	logger    *slog.Logger
}

// NewWorkloadAPI returns the bound workload API.
func NewWorkloadAPI(workloads ports.WorkloadService, app *App, logger *slog.Logger) (*WorkloadAPI, error) {
	switch {
	case workloads == nil:
		return nil, errors.New("wails: WorkloadAPI requires a WorkloadService")
	case app == nil:
		return nil, errors.New("wails: WorkloadAPI requires an App")
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &WorkloadAPI{
		workloads: workloads,
		app:       app,
		logger:    logger.With(slog.String("api", "workload")),
	}, nil
}

// ListPods returns the pods of the active cluster in the given namespace.
//
// An empty namespace lists across every namespace the credentials can see,
// mirroring `kubectl get pods --all-namespaces`.
func (w *WorkloadAPI) ListPods(namespace string) ([]Pod, error) {
	ctx, cancel := w.app.requestContext()
	defer cancel()

	name, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return nil, apiError(w.logger, "ListPods", err)
	}

	pods, err := w.workloads.ListPods(ctx, name)
	if err != nil {
		return nil, apiError(w.logger, "ListPods", err)
	}

	// A single reference time for the whole list, so ages stay consistent
	// across rows instead of drifting by the microseconds the loop takes.
	return toPods(pods, time.Now()), nil
}
