package wails

import (
	"errors"
	"log/slog"
	"time"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
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

// ListPods returns pods in the given namespace of a connected cluster.
//
// An empty namespace lists across all of them, mirroring
// `kubectl get pods --all-namespaces`.
func (w *WorkloadAPI) ListPods(clusterID, namespace string) ([]Pod, error) {
	ctx, cancel := w.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return nil, apiError(w.logger, "ListPods", err)
	}

	name, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return nil, apiError(w.logger, "ListPods", err)
	}

	pods, err := w.workloads.ListPods(ctx, id, name)
	if err != nil {
		return nil, apiError(w.logger, "ListPods", err)
	}

	// A single reference time for the whole list, so ages stay consistent
	// across rows instead of drifting by the microseconds the loop takes.
	return toPods(pods, time.Now()), nil
}

// WorkloadUsage sums what a controller's pods are consuming.
//
// Read while a panel is open rather than alongside the controller list: it
// costs that controller's pods and the namespace's metrics, and the figure is
// only ever looked at one controller at a time.
func (w *WorkloadAPI) WorkloadUsage(clusterID, namespace, kind, name string) (WorkloadUsage, error) {
	ctx, cancel := w.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return WorkloadUsage{}, apiError(w.logger, "WorkloadUsage", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return WorkloadUsage{}, apiError(w.logger, "WorkloadUsage", err)
	}

	usage, err := w.workloads.WorkloadUsage(ctx, id, ns, domain.WorkloadKind(kind), name)
	if err != nil {
		return WorkloadUsage{}, apiError(w.logger, "WorkloadUsage", err)
	}

	return toWorkloadUsage(usage), nil
}

// ListWorkloads returns controllers of the given kind.
//
// The kind arrives as its display name — "Deployment", "StatefulSet" — which
// is what the navigator already holds, so the frontend needs no second
// vocabulary for the same six things.
func (w *WorkloadAPI) ListWorkloads(clusterID, kind, namespace string) ([]Workload, error) {
	ctx, cancel := w.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return nil, apiError(w.logger, "ListWorkloads", err)
	}

	name, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return nil, apiError(w.logger, "ListWorkloads", err)
	}

	workloads, err := w.workloads.ListWorkloads(ctx, id, domain.WorkloadKind(kind), name)
	if err != nil {
		return nil, apiError(w.logger, "ListWorkloads", err)
	}

	return toWorkloads(workloads, time.Now()), nil
}

// PodGraph returns the dependency chain around one pod.
func (w *WorkloadAPI) PodGraph(clusterID, namespace, podName string) (PodGraph, error) {
	ctx, cancel := w.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return PodGraph{}, apiError(w.logger, "PodGraph", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return PodGraph{}, apiError(w.logger, "PodGraph", err)
	}

	graph, err := w.workloads.PodGraph(ctx, id, ns, podName)
	if err != nil {
		return PodGraph{}, apiError(w.logger, "PodGraph", err)
	}

	return toPodGraph(graph), nil
}

// WorkloadGraph returns the dependency chain around one workload.
func (w *WorkloadAPI) WorkloadGraph(clusterID, namespace, kind, name string) (PodGraph, error) {
	ctx, cancel := w.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return PodGraph{}, apiError(w.logger, "WorkloadGraph", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return PodGraph{}, apiError(w.logger, "WorkloadGraph", err)
	}

	graph, err := w.workloads.WorkloadGraph(ctx, id, ns, domain.WorkloadKind(kind), name)
	if err != nil {
		return PodGraph{}, apiError(w.logger, "WorkloadGraph", err)
	}

	return toPodGraph(graph), nil
}

// ListPodsOnNode returns the pods running on one node, across every namespace.
func (w *WorkloadAPI) ListPodsOnNode(clusterID, nodeName string) ([]Pod, error) {
	ctx, cancel := w.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return nil, apiError(w.logger, "ListPodsOnNode", err)
	}

	pods, err := w.workloads.ListPodsOnNode(ctx, id, nodeName)
	if err != nil {
		return nil, apiError(w.logger, "ListPodsOnNode", err)
	}

	return toPods(pods, time.Now()), nil
}

// ListPodsForWorkload returns all pods owned by a specific workload.
func (w *WorkloadAPI) ListPodsForWorkload(clusterID, namespace, kind, name string) ([]Pod, error) {
	ctx, cancel := w.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return nil, apiError(w.logger, "ListPodsForWorkload", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return nil, apiError(w.logger, "ListPodsForWorkload", err)
	}

	pods, err := w.workloads.ListPodsForWorkload(ctx, id, ns, domain.WorkloadKind(kind), name)
	if err != nil {
		return nil, apiError(w.logger, "ListPodsForWorkload", err)
	}

	return toPods(pods, time.Now()), nil
}
