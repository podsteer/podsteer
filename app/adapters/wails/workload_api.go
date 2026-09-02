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
func (w *WorkloadAPI) WorkloadUsage(clusterID, namespace, kind, name string) (Consumption, error) {
	ctx, cancel := w.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return Consumption{}, apiError(w.logger, "WorkloadUsage", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return Consumption{}, apiError(w.logger, "WorkloadUsage", err)
	}

	usage, err := w.workloads.WorkloadUsage(ctx, id, ns, domain.WorkloadKind(kind), name)
	if err != nil {
		return Consumption{}, apiError(w.logger, "WorkloadUsage", err)
	}

	return toConsumption(usage), nil
}

// ListApplications groups a cluster's workloads by the application they
// belong to.
//
// From Kubernetes' own recommended labels, which is the only thing that
// standardises this — and which is a convention rather than a guarantee, so
// the answer carries a count of what did not say.
func (w *WorkloadAPI) ListApplications(clusterID, namespace string) (ApplicationInventory, error) {
	ctx, cancel := w.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return ApplicationInventory{}, apiError(w.logger, "ListApplications", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return ApplicationInventory{}, apiError(w.logger, "ListApplications", err)
	}

	inventory, err := w.workloads.ListApplications(ctx, id, ns)
	if err != nil {
		return ApplicationInventory{}, apiError(w.logger, "ListApplications", err)
	}

	return toApplicationInventory(inventory), nil
}

// WorkloadConsumption sums what each controller in a list is using, keyed by
// "namespace/name".
//
// A SECOND CALL BESIDE ListWorkloads rather than part of it. The controllers
// are one cheap read and this is the namespace's pods and their metrics, so a
// cluster with no metrics API, or an account that may list Deployments and
// not pods, still gets its list — with the meters reading "not measured"
// rather than nothing at all.
func (w *WorkloadAPI) WorkloadConsumption(clusterID, kind, namespace string) (map[string]Consumption, error) {
	ctx, cancel := w.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return nil, apiError(w.logger, "WorkloadConsumption", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return nil, apiError(w.logger, "WorkloadConsumption", err)
	}

	usage, err := w.workloads.WorkloadConsumption(ctx, id, domain.WorkloadKind(kind), ns)
	if err != nil {
		return nil, apiError(w.logger, "WorkloadConsumption", err)
	}

	out := make(map[string]Consumption, len(usage))
	for key, one := range usage {
		out[key] = toConsumption(one)
	}
	return out, nil
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
