package k8s

import (
	"context"
	"fmt"
	"log/slog"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/podsteer/podsteer/app/domain"
)

// ListPods returns the pods in namespace, or across every namespace when it is
// domain.NamespaceAll.
func (a *Adapter) ListPods(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) ([]domain.Pod, error) {
	op := fmt.Sprintf("listing pods in %q of %q", namespace, id)

	client, err := a.factory.clientFor(id)
	if err != nil {
		return nil, err
	}

	// NamespaceAll renders as the empty string, which is precisely what the
	// typed client expects for a cross-namespace list.
	list, err := client.CoreV1().Pods(namespace.String()).List(ctx, metav1.ListOptions{ResourceVersion: cachedResourceVersion})
	if err != nil {
		return nil, classify(op, err)
	}

	pods := make([]domain.Pod, 0, len(list.Items))
	for i := range list.Items {
		pod, err := mapPod(id, &list.Items[i])
		if err != nil {
			// A single object the domain rejects is a mapping bug or an
			// unfamiliar API shape, not a reason to show the operator an empty
			// cluster. Degrade to a partial list and record why.
			a.logger.WarnContext(ctx, "skipping unmappable pod",
				slog.String("cluster", id.String()),
				slog.String("namespace", list.Items[i].Namespace),
				slog.String("name", list.Items[i].Name),
				slog.String("error", err.Error()))
			continue
		}
		pods = append(pods, pod)
	}

	return pods, nil
}

// ListWorkloads returns controllers of the given kind.
//
// One method rather than six because the caller's question is the same in
// every case, and the typed clients differ only in which List to call. The
// per-kind translation lives in the mapper.
func (a *Adapter) ListWorkloads(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName) ([]domain.Workload, error) {
	op := fmt.Sprintf("listing %ss in %q of %q", kind, namespace, id)

	client, err := a.factory.clientFor(id)
	if err != nil {
		return nil, err
	}

	ns := namespace.String()
	options := metav1.ListOptions{ResourceVersion: cachedResourceVersion}

	var (
		workloads []domain.Workload
		listErr   error
	)

	switch kind {
	case domain.WorkloadDeployment:
		var list *appsv1.DeploymentList
		if list, listErr = client.AppsV1().Deployments(ns).List(ctx, options); listErr == nil {
			workloads = a.collect(ctx, id, len(list.Items), func(i int) (domain.Workload, error) {
				return mapDeployment(id, &list.Items[i])
			})
		}

	case domain.WorkloadStatefulSet:
		var list *appsv1.StatefulSetList
		if list, listErr = client.AppsV1().StatefulSets(ns).List(ctx, options); listErr == nil {
			workloads = a.collect(ctx, id, len(list.Items), func(i int) (domain.Workload, error) {
				return mapStatefulSet(id, &list.Items[i])
			})
		}

	case domain.WorkloadDaemonSet:
		var list *appsv1.DaemonSetList
		if list, listErr = client.AppsV1().DaemonSets(ns).List(ctx, options); listErr == nil {
			workloads = a.collect(ctx, id, len(list.Items), func(i int) (domain.Workload, error) {
				return mapDaemonSet(id, &list.Items[i])
			})
		}

	case domain.WorkloadReplicaSet:
		var list *appsv1.ReplicaSetList
		if list, listErr = client.AppsV1().ReplicaSets(ns).List(ctx, options); listErr == nil {
			workloads = a.collect(ctx, id, len(list.Items), func(i int) (domain.Workload, error) {
				return mapReplicaSet(id, &list.Items[i])
			})
		}

	case domain.WorkloadJob:
		var list *batchv1.JobList
		if list, listErr = client.BatchV1().Jobs(ns).List(ctx, options); listErr == nil {
			workloads = a.collect(ctx, id, len(list.Items), func(i int) (domain.Workload, error) {
				return mapJob(id, &list.Items[i])
			})
		}

	case domain.WorkloadCronJob:
		var list *batchv1.CronJobList
		if list, listErr = client.BatchV1().CronJobs(ns).List(ctx, options); listErr == nil {
			workloads = a.collect(ctx, id, len(list.Items), func(i int) (domain.Workload, error) {
				return mapCronJob(id, &list.Items[i])
			})
		}

	default:
		return nil, fmt.Errorf("%s: %w: %q is not a workload kind",
			op, domain.ErrInvalidResourceKind, kind)
	}

	if listErr != nil {
		return nil, classify(op, listErr)
	}

	return workloads, nil
}

// collect runs a per-item mapper, skipping and logging the ones that fail.
//
// Factored out because all six branches above need the identical
// skip-and-carry-on behaviour, and repeating it six times is how one of them
// ends up subtly different.
func (a *Adapter) collect(ctx context.Context, id domain.ClusterID, count int, mapItem func(int) (domain.Workload, error)) []domain.Workload {
	workloads := make([]domain.Workload, 0, count)
	for i := range count {
		workload, err := mapItem(i)
		if err != nil {
			a.logger.WarnContext(ctx, "skipping unmappable workload",
				slog.String("cluster", id.String()),
				slog.String("error", err.Error()))
			continue
		}
		workloads = append(workloads, workload)
	}
	return workloads
}

// ListEvents returns events in the given namespace.
func (a *Adapter) ListEvents(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) ([]domain.Event, error) {
	op := fmt.Sprintf("listing events in %q of %q", namespace, id)

	client, err := a.factory.clientFor(id)
	if err != nil {
		return nil, err
	}

	// A busy cluster holds tens of thousands of events and they expire after
	// an hour anyway. Capping the request keeps a namespace-wide event view
	// from pulling megabytes the operator will never scroll through; the
	// service sorts warnings to the top so the cap does not hide them.
	list, err := client.CoreV1().Events(namespace.String()).List(ctx, metav1.ListOptions{
		Limit: eventListLimit,
	})
	if err != nil {
		return nil, classify(op, err)
	}

	events := make([]domain.Event, 0, len(list.Items))
	for i := range list.Items {
		event, err := mapEvent(id, &list.Items[i])
		if err != nil {
			a.logger.WarnContext(ctx, "skipping unmappable event",
				slog.String("cluster", id.String()),
				slog.String("name", list.Items[i].Name),
				slog.String("error", err.Error()))
			continue
		}
		events = append(events, event)
	}

	return events, nil
}

// ListPodsForWorkload returns all pods owned by a specific workload.
//
// For Deployments, this returns pods owned by the deployment's ReplicaSets.
// For StatefulSets and DaemonSets, this returns pods directly owned by the workload.
func (a *Adapter) ListPodsForWorkload(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, kind domain.WorkloadKind, name string) ([]domain.Pod, error) {
	client, err := a.factory.clientFor(id)
	if err != nil {
		return nil, err
	}

	var podList *corev1.PodList

	switch kind {
	case domain.WorkloadDeployment:
		// For deployments, we need to find all ReplicaSets owned by the deployment,
		// then find all pods owned by those ReplicaSets.
		rsList, err := client.AppsV1().ReplicaSets(namespace.String()).List(ctx, metav1.ListOptions{ResourceVersion: cachedResourceVersion})
		if err != nil {
			return nil, fmt.Errorf("listing replicasets: %w", err)
		}

		// Find ReplicaSets owned by this deployment
		var ownedRSNames []string
		for _, rs := range rsList.Items {
			for _, owner := range rs.OwnerReferences {
				if owner.Kind == "Deployment" && owner.Name == name {
					ownedRSNames = append(ownedRSNames, rs.Name)
					break
				}
			}
		}

		// Get all pods in the namespace
		podList, err = client.CoreV1().Pods(namespace.String()).List(ctx, metav1.ListOptions{ResourceVersion: cachedResourceVersion})
		if err != nil {
			return nil, fmt.Errorf("listing pods: %w", err)
		}

		// Filter pods owned by the deployment's ReplicaSets
		var filteredPods []corev1.Pod
		for _, pod := range podList.Items {
			for _, owner := range pod.OwnerReferences {
				if owner.Kind == "ReplicaSet" {
					for _, rsName := range ownedRSNames {
						if owner.Name == rsName {
							filteredPods = append(filteredPods, pod)
							break
						}
					}
				}
			}
		}
		podList.Items = filteredPods

	case domain.WorkloadStatefulSet, domain.WorkloadDaemonSet, domain.WorkloadReplicaSet:
		// For these workloads, pods are directly owned by the workload
		podList, err = client.CoreV1().Pods(namespace.String()).List(ctx, metav1.ListOptions{ResourceVersion: cachedResourceVersion})
		if err != nil {
			return nil, fmt.Errorf("listing pods: %w", err)
		}

		// Filter pods owned by this workload
		var filteredPods []corev1.Pod
		for _, pod := range podList.Items {
			for _, owner := range pod.OwnerReferences {
				if owner.Kind == string(kind) && owner.Name == name {
					filteredPods = append(filteredPods, pod)
					break
				}
			}
		}
		podList.Items = filteredPods

	case domain.WorkloadJob, domain.WorkloadCronJob:
		// Jobs and CronJobs don't have the same pod ownership model
		// For now, return empty list
		return []domain.Pod{}, nil

	default:
		return nil, fmt.Errorf("unsupported workload kind: %s", kind)
	}

	// Convert to domain pods
	pods := make([]domain.Pod, 0, len(podList.Items))
	for i := range podList.Items {
		pod, err := mapPod(id, &podList.Items[i])
		if err != nil {
			a.logger.WarnContext(ctx, "skipping unmappable pod",
				slog.String("cluster", id.String()),
				slog.String("namespace", podList.Items[i].Namespace),
				slog.String("name", podList.Items[i].Name),
				slog.String("error", err.Error()))
			continue
		}
		pods = append(pods, pod)
	}

	return pods, nil
}

// ListEventsForResource returns events for a specific resource.
func (a *Adapter) ListEventsForResource(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, kind, name string) ([]domain.Event, error) {
	op := fmt.Sprintf("listing events for %s/%s in %q of %q", kind, name, namespace, id)

	client, err := a.factory.clientFor(id)
	if err != nil {
		return nil, err
	}

	// List all events in the namespace
	list, err := client.CoreV1().Events(namespace.String()).List(ctx, metav1.ListOptions{
		Limit: eventListLimit,
	})
	if err != nil {
		return nil, classify(op, err)
	}

	// Filter events for the specific resource
	events := make([]domain.Event, 0)
	for i := range list.Items {
		event := &list.Items[i]
		// Check if this event is for our resource
		if event.InvolvedObject.Name == name && event.InvolvedObject.Kind == kind {
			mappedEvent, err := mapEvent(id, event)
			if err != nil {
				a.logger.WarnContext(ctx, "skipping unmappable event",
					slog.String("cluster", id.String()),
					slog.String("name", event.Name),
					slog.String("error", err.Error()))
				continue
			}
			events = append(events, mappedEvent)
		}
	}

	return events, nil
}

// eventListLimit caps a single event query. See ListEvents for the reasoning.
const eventListLimit = 1000
