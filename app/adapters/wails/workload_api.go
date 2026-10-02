package wails

import (
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// WorkloadAPI exposes the workload use cases to the frontend.
type WorkloadAPI struct {
	workloads ports.WorkloadService
	app       *App
	logger    *slog.Logger

	// chooseSavePath is the save dialog behind ExportPodsCSV — a seam for
	// the reason SystemAPI.chooseSavePath is one.
	chooseSavePath func(suggestedName string) (string, error)
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

	w := &WorkloadAPI{
		workloads: workloads,
		app:       app,
		logger:    logger.With(slog.String("api", "workload")),
	}
	w.chooseSavePath = func(suggestedName string) (string, error) { return showSaveDialog(app, suggestedName) }
	return w, nil
}

// ListPods returns pods in the given namespace of a connected cluster.
//
// An empty namespace lists across all of them, mirroring
// `kubectl get pods --all-namespaces`.
//
// annotationKeys names the annotations each row should carry — the ones the
// operator has put on a custom column of this kind. Nothing else of the
// annotation map crosses the bridge; see domain.Projection for why.
func (w *WorkloadAPI) ListPods(clusterID, namespace string, annotationKeys []string, expressions []CustomExpression) ([]Pod, error) {
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

	pods, err := w.workloads.ListPods(ctx, id, name, projectionFor(annotationKeys, expressions))
	if err != nil {
		return nil, apiError(w.logger, "ListPods", err)
	}

	// A single reference time for the whole list, so ages stay consistent
	// across rows instead of drifting by the microseconds the loop takes.
	return toPods(pods, time.Now()), nil
}

// QueryPods returns one page of the pod table — what its search box, status
// chips, sort and pager say — and the counts around it, rather than the whole
// list. See domain.QueryPods, and CLAUDE.md, "The pod table is paged in Go".
//
// annotationKeys and expressions are the list's projection, exactly as
// ListPods takes them; query.Columns are the same custom columns as specs,
// for what they add to the searchable text.
func (w *WorkloadAPI) QueryPods(clusterID, namespace string, annotationKeys []string, expressions []CustomExpression, query PodQuery) (PodPage, error) {
	ctx, cancel := w.app.requestContext()
	defer cancel()

	id, name, err := podListArgs(clusterID, namespace)
	if err != nil {
		return PodPage{}, apiError(w.logger, "QueryPods", err)
	}

	page, err := w.workloads.QueryPods(ctx, id, name, projectionFor(annotationKeys, expressions), query.toDomain())
	if err != nil {
		return PodPage{}, apiError(w.logger, "QueryPods", err)
	}

	return toPodPage(page, time.Now()), nil
}

// ListPodKeys names every pod the query matches, across every page — what
// "select all matching" ticks. The page in the query is ignored.
func (w *WorkloadAPI) ListPodKeys(clusterID, namespace string, annotationKeys []string, expressions []CustomExpression, query PodQuery) ([]PodKey, error) {
	ctx, cancel := w.app.requestContext()
	defer cancel()

	id, name, err := podListArgs(clusterID, namespace)
	if err != nil {
		return nil, apiError(w.logger, "ListPodKeys", err)
	}

	keys, err := w.workloads.ListPodKeys(ctx, id, name, projectionFor(annotationKeys, expressions), query.toDomain())
	if err != nil {
		return nil, apiError(w.logger, "ListPodKeys", err)
	}

	return toPodKeys(keys), nil
}

// ExportPodsCSV writes every pod the query matches — every page, in the
// table's order — as CSV to wherever the operator chooses, and returns the
// path, or "" when they cancelled the dialog.
//
// RENDERED AND WRITTEN HERE because the rows are: the webview holds one page
// now, and shipping the whole list across the bridge to turn it into a file
// would be the payload paging exists to avoid. The cells are the table's
// own text (see podCSVCell) and the file is web/src/lib/csv.ts's format,
// formula guard included. columns are the visible columns, in order.
func (w *WorkloadAPI) ExportPodsCSV(clusterID, namespace string, annotationKeys []string, expressions []CustomExpression, query PodQuery, columns []CSVColumn, suggestedName string) (string, error) {
	id, name, err := podListArgs(clusterID, namespace)
	if err != nil {
		return "", apiError(w.logger, "ExportPodsCSV", err)
	}

	// Read first, then ask where to put it: a refusal should arrive before
	// the dialog rather than after the operator has chosen a place.
	ctx, cancel := w.app.requestContext()
	pods, err := w.workloads.MatchingPods(ctx, id, name, projectionFor(annotationKeys, expressions), query.toDomain())
	cancel()
	if err != nil {
		return "", apiError(w.logger, "ExportPodsCSV", err)
	}

	path, err := writeCSVExport(w.chooseSavePath, suggestedName, renderPodCSV(columns, toPods(pods, time.Now())))
	if err != nil {
		return "", apiError(w.logger, "ExportPodsCSV", err)
	}
	return path, nil
}

// writeCSVExport asks where to save an export and writes it there — the
// same dialog, permissions and cancel convention as SaveTextFile.
func writeCSVExport(choose func(string) (string, error), suggestedName, content string) (string, error) {
	if strings.TrimSpace(suggestedName) == "" {
		return "", errEmptySuggestedName
	}
	path, err := choose(suggestedName)
	if err != nil || path == "" {
		return "", err
	}
	// 0o600, as SaveTextFile writes: the export holds whatever the cluster
	// returned.
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// podListArgs validates the cluster and namespace every pod list call takes.
func podListArgs(clusterID, namespace string) (domain.ClusterID, domain.NamespaceName, error) {
	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return "", "", err
	}
	name, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return "", "", err
	}
	return id, name, nil
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
//
// annotationKeys is the same projection ListPods takes.
func (w *WorkloadAPI) ListWorkloads(clusterID, kind, namespace string, annotationKeys []string, expressions []CustomExpression) ([]Workload, error) {
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

	workloads, err := w.workloads.ListWorkloads(ctx, id, domain.WorkloadKind(kind), name, projectionFor(annotationKeys, expressions))
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

// ApplicationGraph returns the map of one application: the objects labelled
// app.kubernetes.io/instance=<instance> in a namespace, and what they own.
func (w *WorkloadAPI) ApplicationGraph(clusterID, namespace, instance string) (PodGraph, error) {
	ctx, cancel := w.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return PodGraph{}, apiError(w.logger, "ApplicationGraph", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return PodGraph{}, apiError(w.logger, "ApplicationGraph", err)
	}

	graph, err := w.workloads.ApplicationGraph(ctx, id, ns, instance)
	if err != nil {
		return PodGraph{}, apiError(w.logger, "ApplicationGraph", err)
	}

	return toPodGraph(graph), nil
}

// ListApplicationPods returns the pods of one application, by the same rule
// its map draws them with.
func (w *WorkloadAPI) ListApplicationPods(clusterID, namespace, instance string) ([]Pod, error) {
	ctx, cancel := w.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return nil, apiError(w.logger, "ListApplicationPods", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return nil, apiError(w.logger, "ListApplicationPods", err)
	}

	pods, err := w.workloads.ListApplicationPods(ctx, id, ns, instance)
	if err != nil {
		return nil, apiError(w.logger, "ListApplicationPods", err)
	}

	return toPods(pods, time.Now()), nil
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

// RolloutHistory returns the recorded revisions of a Deployment,
// StatefulSet or DaemonSet's pod template, newest first — the History tab
// in the drawer, and what a rollback picks a target revision from.
func (w *WorkloadAPI) RolloutHistory(clusterID, kind, namespace, name string) ([]RevisionDTO, error) {
	ctx, cancel := w.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return nil, apiError(w.logger, "RolloutHistory", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return nil, apiError(w.logger, "RolloutHistory", err)
	}

	revisions, err := w.workloads.RolloutHistory(ctx, id, domain.WorkloadKind(kind), ns, name)
	if err != nil {
		return nil, apiError(w.logger, "RolloutHistory", err)
	}

	return toRevisions(revisions, time.Now()), nil
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
