package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"

	"github.com/podsteer/podsteer/app/domain"
)

// ListPods returns pods in a namespace, or across every namespace.
//
// Shared through the read cache: the assessment lists every pod on every
// refresh whatever is on screen, and so does the namespace list, and on a
// controller list the consumption sums do it for one namespace. See
// readcache.go — identical reads in one tick become one request.
//
// THE PROJECTION IS PART OF THE KEY. A pod read with one set of annotation
// keys cannot answer a read that wants another: the domain value carries
// only what was asked for, and there is no way to add a key to a mapped pod
// afterwards. So a list view with an annotation column reads beside the
// assessment's own list rather than sharing it — one extra list per refresh,
// paid only by whoever configured such a column, and only CPU when the watch
// is serving. The empty projection, which every non-list caller passes,
// keys exactly as before.
func (a *Adapter) ListPods(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, projection domain.Projection) ([]domain.Pod, error) {
	// ONE NAMESPACE OUT OF A READ THAT ALREADY COVERS IT. The assessment
	// lists every pod in the cluster on every refresh whatever is on screen,
	// so on a controller page the cluster-wide read is in flight beside this
	// one — and a namespace's pods are a subset of it. Waiting for the read
	// already under way and filtering costs nothing on the wire.
	//
	// Only ever a reuse, never a promotion: an account that may list one
	// namespace and not the cluster has no such read to borrow, so this
	// misses and the narrow request goes out as before.
	if !namespace.IsAll() {
		if all, borrowed := borrow[[]domain.Pod](ctx, &a.reads, readKey(id.String(), "pods", "", projection.String())); borrowed {
			return podsIn(all, namespace), nil
		}
	}

	// Starts watching this cluster if nothing is yet, and does not wait: this
	// call is about to answer from the network either way, and whether the
	// store ever becomes useful is decided in the background.
	a.watches.ensure(id, func() (kubernetes.Interface, error) { return a.factory.clientFor(id) })

	// THE CACHE STAYS IN FRONT OF THE STORE. Reading the store is free on the
	// wire but not free in CPU — it is five thousand pods mapped into domain
	// values — and the assessment and the open list both want them in the
	// same instant. Coalescing that is the same job it was doing before, so
	// the mapping happens once per tick rather than once per caller.
	return cachedSlice(&a.reads, ctx, readKey(id.String(), "pods", namespace.String(), projection.String()), func(ctx context.Context) ([]domain.Pod, error) {
		if stored, serving := watched[*corev1.Pod](a.watches, id, watchPods); serving {
			return mapWatchedPods(id, stored, namespace, projection)
		}
		return a.listPods(ctx, id, namespace, projection)
	})
}

// mapWatchedPods turns the store's pods into domain values, narrowed to one
// namespace.
//
// A pod that will not map is SKIPPED rather than failing the read: the store
// holds whatever the cluster sent, and one unmappable object must not empty a
// list of five thousand. That matches how the list path already treats them.
func mapWatchedPods(id domain.ClusterID, watched []*corev1.Pod, namespace domain.NamespaceName, projection domain.Projection) ([]domain.Pod, error) {
	pods := make([]domain.Pod, 0, len(watched))
	for _, pod := range watched {
		if !namespace.IsAll() && pod.Namespace != namespace.String() {
			continue
		}
		mapped, err := mapPod(id, pod, projection)
		if err != nil {
			continue
		}
		pods = append(pods, mapped)
	}
	return pods, nil
}

// podsIn narrows a cluster-wide read to one namespace, into a slice of its
// own — callers sort what they are given.
func podsIn(pods []domain.Pod, namespace domain.NamespaceName) []domain.Pod {
	narrowed := make([]domain.Pod, 0, len(pods))
	for _, pod := range pods {
		if pod.Namespace() == namespace {
			narrowed = append(narrowed, pod)
		}
	}
	return narrowed
}

// ListPods returns the pods in namespace, or across every namespace when it is
// domain.NamespaceAll.
func (a *Adapter) listPods(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, projection domain.Projection) ([]domain.Pod, error) {
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
		pod, err := mapPod(id, &list.Items[i], projection)
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

// ListWorkloads returns controllers of one kind.
//
// Cached, which is what makes the controller list's meters free: the list and
// its consumption sums both ask for the same controllers in the same tick,
// and the assessment asks for all six kinds alongside.
func (a *Adapter) ListWorkloads(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName, projection domain.Projection) ([]domain.Workload, error) {
	a.watches.ensure(id, func() (kubernetes.Interface, error) { return a.factory.clientFor(id) })

	// The projection keys the read for the reason ListPods gives.
	return cachedSlice(&a.reads, ctx, readKey(id.String(), "workloads", string(kind), namespace.String(), projection.String()), func(ctx context.Context) ([]domain.Workload, error) {
		// Only the two that are watched, and only because they are the ones a
		// refresh re-reads: ReplicaSets stand between a Deployment and its
		// pods, Jobs between a CronJob and its. The other four are one small
		// list each and go to the cluster.
		switch kind {
		case domain.WorkloadReplicaSet:
			if stored, serving := watched[*appsv1.ReplicaSet](a.watches, id, watchReplicaSets); serving {
				return mapWatched(id, stored, namespace, func(id domain.ClusterID, item *appsv1.ReplicaSet) (domain.Workload, error) {
					return mapReplicaSet(id, item, projection)
				}), nil
			}
		case domain.WorkloadJob:
			if stored, serving := watched[*batchv1.Job](a.watches, id, watchJobs); serving {
				return mapWatched(id, stored, namespace, func(id domain.ClusterID, item *batchv1.Job) (domain.Workload, error) {
					return mapJob(id, item, projection)
				}), nil
			}
		}
		return a.listWorkloads(ctx, id, kind, namespace, projection)
	})
}

// mapWatched turns stored objects into domain values, narrowed to one
// namespace.
//
// An object that will not map is SKIPPED rather than failing the read: the
// store holds whatever the cluster sent, and one unmappable object must not
// empty a list of thousands. That matches how the list path already treats
// them — see Adapter.collect.
func mapWatched[T interface{ GetNamespace() string }, R any](
	id domain.ClusterID,
	stored []T,
	namespace domain.NamespaceName,
	convert func(domain.ClusterID, T) (R, error),
) []R {
	mapped := make([]R, 0, len(stored))
	for _, item := range stored {
		if !namespace.IsAll() && item.GetNamespace() != namespace.String() {
			continue
		}
		value, err := convert(id, item)
		if err != nil {
			continue
		}
		mapped = append(mapped, value)
	}
	return mapped
}

// ListWorkloads returns controllers of the given kind.
//
// One method rather than six because the caller's question is the same in
// every case, and the typed clients differ only in which List to call. The
// per-kind translation lives in the mapper.
func (a *Adapter) listWorkloads(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName, projection domain.Projection) ([]domain.Workload, error) {
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
				return mapDeployment(id, &list.Items[i], projection)
			})
		}

	case domain.WorkloadStatefulSet:
		var list *appsv1.StatefulSetList
		if list, listErr = client.AppsV1().StatefulSets(ns).List(ctx, options); listErr == nil {
			workloads = a.collect(ctx, id, len(list.Items), func(i int) (domain.Workload, error) {
				return mapStatefulSet(id, &list.Items[i], projection)
			})
		}

	case domain.WorkloadDaemonSet:
		var list *appsv1.DaemonSetList
		if list, listErr = client.AppsV1().DaemonSets(ns).List(ctx, options); listErr == nil {
			workloads = a.collect(ctx, id, len(list.Items), func(i int) (domain.Workload, error) {
				return mapDaemonSet(id, &list.Items[i], projection)
			})
		}

	case domain.WorkloadReplicaSet:
		var list *appsv1.ReplicaSetList
		if list, listErr = client.AppsV1().ReplicaSets(ns).List(ctx, options); listErr == nil {
			workloads = a.collect(ctx, id, len(list.Items), func(i int) (domain.Workload, error) {
				return mapReplicaSet(id, &list.Items[i], projection)
			})
		}

	case domain.WorkloadJob:
		var list *batchv1.JobList
		if list, listErr = client.BatchV1().Jobs(ns).List(ctx, options); listErr == nil {
			workloads = a.collect(ctx, id, len(list.Items), func(i int) (domain.Workload, error) {
				return mapJob(id, &list.Items[i], projection)
			})
		}

	case domain.WorkloadCronJob:
		var list *batchv1.CronJobList
		if list, listErr = client.BatchV1().CronJobs(ns).List(ctx, options); listErr == nil {
			workloads = a.collect(ctx, id, len(list.Items), func(i int) (domain.Workload, error) {
				return mapCronJob(id, &list.Items[i], projection)
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
func (a *Adapter) ListEvents(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, projection domain.Projection) ([]domain.Event, error) {
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
		event, err := mapEvent(id, &list.Items[i], projection)
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

// selectorForWorkload returns the workload's own pod selector, as a string.
//
// One GET in exchange for not listing every pod in the namespace. On a
// namespace of several hundred pods that is the difference between
// transferring all of them to pick out three and asking for the three.
//
// The owner-reference checks downstream are kept even so. A label selector is
// what the workload itself uses to find its pods, but labels can be shared by
// a misconfigured neighbour, whereas an ownerReference cannot be.
func selectorForWorkload(
	ctx context.Context,
	client kubernetes.Interface,
	namespace domain.NamespaceName,
	kind domain.WorkloadKind,
	name string,
) (string, error) {
	get := metav1.GetOptions{}
	ns := namespace.String()

	var selector *metav1.LabelSelector
	switch kind {
	case domain.WorkloadDeployment:
		object, err := client.AppsV1().Deployments(ns).Get(ctx, name, get)
		if err != nil {
			return "", err
		}
		selector = object.Spec.Selector
	case domain.WorkloadStatefulSet:
		object, err := client.AppsV1().StatefulSets(ns).Get(ctx, name, get)
		if err != nil {
			return "", err
		}
		selector = object.Spec.Selector
	case domain.WorkloadDaemonSet:
		object, err := client.AppsV1().DaemonSets(ns).Get(ctx, name, get)
		if err != nil {
			return "", err
		}
		selector = object.Spec.Selector
	case domain.WorkloadReplicaSet:
		object, err := client.AppsV1().ReplicaSets(ns).Get(ctx, name, get)
		if err != nil {
			return "", err
		}
		selector = object.Spec.Selector
	default:
		return "", nil
	}

	parsed, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return "", err
	}
	return parsed.String(), nil
}

// ListPodsForWorkload returns all pods owned by a specific workload.
//
// For Deployments, this returns pods owned by the deployment's ReplicaSets.
// For StatefulSets and DaemonSets, this returns pods directly owned by the workload.
// ListPodsOnNode returns the pods the scheduler has placed on one node.
//
// Across every namespace deliberately: "what is running on this machine" is a
// question about the machine, and an operator draining a node or chasing a
// noisy neighbour does not care which namespace the answer is in. RBAC decides
// what comes back — an account scoped to one namespace sees that namespace's
// pods on the node and no error, which is the correct partial answer rather
// than a refusal.
func (a *Adapter) ListPodsOnNode(ctx context.Context, id domain.ClusterID, nodeName string) ([]domain.Pod, error) {
	op := fmt.Sprintf("listing pods on node %q of %q", nodeName, id)

	if strings.TrimSpace(nodeName) == "" {
		return nil, fmt.Errorf("%s: no node named", op)
	}

	client, err := a.factory.clientFor(id)
	if err != nil {
		return nil, err
	}

	// `spec.nodeName` is one of the few fields the API server indexes for
	// pods, so this is served from that index rather than by listing the
	// cluster and discarding most of it.
	list, err := client.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
		FieldSelector:   fields.OneTermEqualSelector("spec.nodeName", nodeName).String(),
		ResourceVersion: cachedResourceVersion,
	})
	if err != nil {
		return nil, classify(op, err)
	}

	pods := make([]domain.Pod, 0, len(list.Items))
	for i := range list.Items {
		// No projection: this feeds the node drawer's pod list, which has
		// no custom columns.
		pod, err := mapPod(id, &list.Items[i], domain.Projection{})
		if err != nil {
			// One object the domain rejects is a mapping bug, not a reason to
			// tell somebody their node is empty.
			a.logger.WarnContext(ctx, "skipping unmappable pod",
				slog.String("cluster", id.String()),
				slog.String("node", nodeName),
				slog.String("name", list.Items[i].Name),
				slog.String("error", err.Error()))
			continue
		}
		pods = append(pods, pod)
	}
	return pods, nil
}

// ownedBy reports whether an owner of the given kind and name controls this
// object.
func ownedBy(owners []metav1.OwnerReference, kind, name string) bool {
	for _, owner := range owners {
		if owner.Kind == kind && owner.Name == name {
			return true
		}
	}
	return false
}

func (a *Adapter) ListPodsForWorkload(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, kind domain.WorkloadKind, name string) ([]domain.Pod, error) {
	client, err := a.factory.clientFor(id)
	if err != nil {
		return nil, err
	}

	// Narrowed by the workload's own selector, so the API server returns its
	// pods rather than the namespace's. An empty selector (an unsupported
	// kind, or a workload without one) falls back to the previous behaviour
	// rather than failing.
	selector, err := selectorForWorkload(ctx, client, namespace, kind, name)
	if err != nil {
		return nil, fmt.Errorf("reading selector for %s %q: %w", kind, name, err)
	}
	podOptions := metav1.ListOptions{
		LabelSelector:   selector,
		ResourceVersion: cachedResourceVersion,
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
		podList, err = client.CoreV1().Pods(namespace.String()).List(ctx, podOptions)
		if err != nil {
			return nil, fmt.Errorf("listing pods: %w", err)
		}

		// Filter pods owned by the deployment's ReplicaSets
		var filteredPods []corev1.Pod
		for _, pod := range podList.Items {
			for _, owner := range pod.OwnerReferences {
				if owner.Kind == "ReplicaSet" {
					if slices.Contains(ownedRSNames, owner.Name) {
						filteredPods = append(filteredPods, pod)
					}
				}
			}
		}
		podList.Items = filteredPods

	case domain.WorkloadStatefulSet, domain.WorkloadDaemonSet, domain.WorkloadReplicaSet:
		// For these workloads, pods are directly owned by the workload
		podList, err = client.CoreV1().Pods(namespace.String()).List(ctx, podOptions)
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

	case domain.WorkloadJob:
		// BY OWNER REFERENCE, not by the `job-name` label. The label is set by
		// the Job controller and is the usual shortcut, but it is also just a
		// label: anything may carry it, and a pod relabelled by hand would be
		// claimed by a Job that never created it. The ownerReference is what
		// the controller itself uses to decide what is its.
		all, err := client.CoreV1().Pods(namespace.String()).
			List(ctx, metav1.ListOptions{ResourceVersion: cachedResourceVersion})
		if err != nil {
			return nil, classify(fmt.Sprintf("listing pods for job %q", name), err)
		}

		podList = &corev1.PodList{}
		for i := range all.Items {
			if ownedBy(all.Items[i].OwnerReferences, "Job", name) {
				podList.Items = append(podList.Items, all.Items[i])
			}
		}

	case domain.WorkloadCronJob:
		// TWO HOPS. A CronJob owns Jobs and a Job owns Pods; nothing links a
		// CronJob to a pod directly, which is why this used to return nothing
		// at all and a CronJob's map drew a box over empty space.
		jobs, err := client.BatchV1().Jobs(namespace.String()).
			List(ctx, metav1.ListOptions{ResourceVersion: cachedResourceVersion})
		if err != nil {
			return nil, classify(fmt.Sprintf("listing jobs for cronjob %q", name), err)
		}

		owned := make(map[string]bool)
		for i := range jobs.Items {
			if ownedBy(jobs.Items[i].OwnerReferences, "CronJob", name) {
				owned[jobs.Items[i].Name] = true
			}
		}

		all, err := client.CoreV1().Pods(namespace.String()).
			List(ctx, metav1.ListOptions{ResourceVersion: cachedResourceVersion})
		if err != nil {
			return nil, classify(fmt.Sprintf("listing pods for cronjob %q", name), err)
		}

		podList = &corev1.PodList{}
		for i := range all.Items {
			for _, owner := range all.Items[i].OwnerReferences {
				if owner.Kind == "Job" && owned[owner.Name] {
					podList.Items = append(podList.Items, all.Items[i])
					break
				}
			}
		}

	default:
		return nil, fmt.Errorf("unsupported workload kind: %s", kind)
	}

	// Convert to domain pods
	pods := make([]domain.Pod, 0, len(podList.Items))
	for i := range podList.Items {
		pod, err := mapPod(id, &podList.Items[i], domain.Projection{})
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

	// Selected by the API server, not by us.
	//
	// This listed the namespace's events up to a cap and then filtered them
	// here, which is not merely wasteful — it is wrong. A namespace busy
	// enough to exceed the cap could return a page containing none of this
	// object's events, and the drawer would report that a pod which had just
	// crash-looped had nothing to say. Asking for the events of one object
	// removes both the cap's relevance and the transfer.
	selector := fields.SelectorFromSet(fields.Set{
		"involvedObject.name": name,
		"involvedObject.kind": kind,
	}).String()

	list, err := client.CoreV1().Events(namespace.String()).List(ctx, metav1.ListOptions{
		FieldSelector:   selector,
		Limit:           eventListLimit,
		ResourceVersion: cachedResourceVersion,
	})
	if err != nil {
		return nil, classify(op, err)
	}

	events := make([]domain.Event, 0, len(list.Items))
	for i := range list.Items {
		event := &list.Items[i]
		mappedEvent, err := mapEvent(id, event, domain.Projection{})
		if err != nil {
			a.logger.WarnContext(ctx, "skipping unmappable event",
				slog.String("cluster", id.String()),
				slog.String("name", event.Name),
				slog.String("error", err.Error()))
			continue
		}
		events = append(events, mappedEvent)
	}

	return events, nil
}

// eventListLimit caps a single event query. See ListEvents for the reasoning.
const eventListLimit = 1000
