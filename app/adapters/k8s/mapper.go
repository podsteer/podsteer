package k8s

import (
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiversion "k8s.io/apimachinery/pkg/version"

	"github.com/podsteer/podsteer/app/domain"
)

// This file is the anti-corruption layer between Kubernetes API types and the
// PodSteer domain. Nothing outside this package should ever see a corev1 value,
// and nothing in here should encode business rules — it translates, the domain
// decides.

// mapPod translates a Kubernetes pod into the domain model.
func mapPod(clusterID domain.ClusterID, pod *corev1.Pod) (domain.Pod, error) {
	namespace, err := domain.NewNamespaceName(pod.Namespace)
	if err != nil {
		return domain.Pod{}, err
	}

	return domain.NewPod(domain.PodSpec{
		UID:        string(pod.UID),
		Name:       pod.Name,
		Namespace:  namespace,
		ClusterID:  clusterID,
		Phase:      mapPodPhase(pod),
		NodeName:   pod.Spec.NodeName,
		PodIP:      pod.Status.PodIP,
		Containers: mapContainers(pod),
		Labels:     pod.Labels,
		Owners:     mapOwnerReferences(pod.OwnerReferences),
		QoSClass:   domain.NewQoSClass(string(pod.Status.QOSClass)),
		Reason:     podReason(pod),
		Message:    podMessage(pod),
		CreatedAt:  pod.CreationTimestamp.Time,
	})
}

// podReason returns the pod-level reason.
//
// status.Reason carries "Evicted" and "NodeAffinity", but it is empty for the
// commonest pending case: an unschedulable pod records its reason on the
// PodScheduled condition instead, and reading only status.Reason is why a
// dashboard shows a pod as merely "Pending" when the scheduler has already
// said it will never fit.
func podReason(pod *corev1.Pod) string {
	if pod.Status.Reason != "" {
		return pod.Status.Reason
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse {
			return condition.Reason
		}
	}
	return ""
}

// podMessage returns the most useful explanation the pod carries.
//
// The PodScheduled condition is preferred over status.message because for the
// one case where a pod cannot explain itself any other way — it will not
// schedule — that condition holds the scheduler's own account ("0/6 nodes are
// available: 6 Insufficient cpu"). Nothing else in the API says why.
func podMessage(pod *corev1.Pod) string {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse {
			if condition.Message != "" {
				return condition.Message
			}
		}
	}
	return pod.Status.Message
}

// mapPodPhase derives the phase PodSteer shows from the pod's reported phase.
//
// The one substitution is deletion: a pod with a deletion timestamp keeps
// reporting Running right up until it vanishes, so PodSteer reports Terminating
// instead — the same correction kubectl applies in its STATUS column.
func mapPodPhase(pod *corev1.Pod) domain.PodPhase {
	if pod.DeletionTimestamp != nil {
		return domain.PodPhaseTerminating
	}
	return domain.NewPodPhase(string(pod.Status.Phase))
}

// mapContainers joins each container's declaration with its observed status.
//
// The two live in different halves of the object — spec.containers carries the
// image, status.containerStatuses carries readiness, restarts and state — and
// the status half is absent until the kubelet reports, which is exactly the
// window in which an operator is staring at the screen wondering why nothing
// has started. Containers with no status yet are still returned, in Waiting.
//
// Restartable init containers — native sidecars, the ones with a restart
// policy of Always — are included with the regular containers, which is what
// kubectl counts too. They run for the pod's whole life and hold their
// requests for all of it, so omitting them would understate what every pod
// with an injected proxy reserves. Ordinary init containers are left out: they
// have exited by the time anyone is looking at a running pod.
func mapContainers(pod *corev1.Pod) []domain.Container {
	statuses := make(map[string]corev1.ContainerStatus, len(pod.Status.ContainerStatuses))
	for _, status := range pod.Status.ContainerStatuses {
		statuses[status.Name] = status
	}
	for _, status := range pod.Status.InitContainerStatuses {
		statuses[status.Name] = status
	}

	specs := make([]corev1.Container, 0, len(pod.Spec.Containers)+1)
	for _, spec := range pod.Spec.InitContainers {
		if spec.RestartPolicy != nil && *spec.RestartPolicy == corev1.ContainerRestartPolicyAlways {
			specs = append(specs, spec)
		}
	}
	specs = append(specs, pod.Spec.Containers...)

	containers := make([]domain.Container, 0, len(specs))
	for _, spec := range specs {
		container := domain.Container{
			Name:     spec.Name,
			Image:    spec.Image,
			State:    domain.ContainerStateWaiting,
			Requests: mapResources(spec.Resources.Requests),
			Limits:   mapResources(spec.Resources.Limits),
		}

		if status, ok := statuses[spec.Name]; ok {
			container.Ready = status.Ready
			container.RestartCount = status.RestartCount
			container.State, container.Reason = mapContainerState(status.State)

			// status.Image is the digest-resolved reference actually running,
			// which differs from the spec whenever a mutable tag has been
			// re-pushed. Prefer it: it answers "what is running right now".
			if status.Image != "" {
				container.Image = status.Image
			}

			if status.Started != nil {
				container.Started = *status.Started
			}

			// The previous life, when the API server recorded one. Only the
			// TERMINATED variant carries anything: a container whose last
			// state was Waiting has nothing to explain, which is why kubectl
			// omits the whole block in that case too.
			if last := status.LastTerminationState.Terminated; last != nil {
				container.LastTermination = domain.Termination{
					ExitCode:   last.ExitCode,
					Signal:     last.Signal,
					Reason:     last.Reason,
					Message:    last.Message,
					StartedAt:  last.StartedAt.UTC(),
					FinishedAt: last.FinishedAt.UTC(),
				}
			}
		}

		containers = append(containers, container)
	}

	return containers
}

// mapResources converts a container's resource list into the domain units.
//
// MilliValue() and Value() are used rather than parsing the quantity strings:
// Kubernetes accepts "0.5", "500m", "1Gi" and "1073741824" for the same
// amounts, and the quantity type is the only thing that reconciles them.
func mapResources(list corev1.ResourceList) domain.Resources {
	var resources domain.Resources
	if cpu, ok := list[corev1.ResourceCPU]; ok {
		resources.CPUMilli = cpu.MilliValue()
	}
	if memory, ok := list[corev1.ResourceMemory]; ok {
		resources.MemoryBytes = memory.Value()
	}
	if ephemeral, ok := list[corev1.ResourceEphemeralStorage]; ok {
		resources.EphemeralBytes = ephemeral.Value()
	}
	return resources
}

// mapContainerState translates a container state union into a state and its
// reason.
func mapContainerState(state corev1.ContainerState) (domain.ContainerState, string) {
	switch {
	case state.Running != nil:
		return domain.ContainerStateRunning, ""
	case state.Terminated != nil:
		return domain.ContainerStateTerminated, state.Terminated.Reason
	case state.Waiting != nil:
		return domain.ContainerStateWaiting, state.Waiting.Reason
	default:
		return domain.ContainerStateUnknown, ""
	}
}

// mapNamespace translates a Kubernetes namespace into the domain model.
func mapNamespace(namespace *corev1.Namespace) (domain.Namespace, error) {
	return domain.NewNamespace(
		namespace.Name,
		domain.NewNamespacePhase(string(namespace.Status.Phase)),
		namespace.CreationTimestamp.Time,
	)
}

// mapServerVersion translates the API server's version report.
func mapServerVersion(info *apiversion.Info) domain.ServerVersion {
	if info == nil {
		return domain.ServerVersion{}
	}
	return domain.ServerVersion{
		GitVersion: info.GitVersion,
		Major:      info.Major,
		Minor:      info.Minor,
		Platform:   info.Platform,
	}
}

// --- Nodes ------------------------------------------------------------------

// mapNode translates a Kubernetes node into the domain model.
func mapNode(clusterID domain.ClusterID, node *corev1.Node) (domain.Node, error) {
	ready := false
	active := make([]domain.NodeCondition, 0, 2)

	for _, condition := range node.Status.Conditions {
		switch condition.Type {
		case corev1.NodeReady:
			ready = condition.Status == corev1.ConditionTrue
		default:
			// Pressure conditions are problems when TRUE, which is the inverse
			// of Ready. Only the true ones are carried forward.
			if condition.Status == corev1.ConditionTrue {
				for _, known := range domain.KnownPressureConditions() {
					if string(condition.Type) == string(known) {
						active = append(active, known)
					}
				}
			}
		}
	}

	return domain.NewNode(domain.NodeSpec{
		Name:             node.Name,
		ClusterID:        clusterID,
		Roles:            nodeRoles(node),
		Ready:            ready,
		ActiveConditions: active,
		Unschedulable:    node.Spec.Unschedulable,
		Taints:           len(node.Spec.Taints),
		BlockingTaints:   blockingTaints(node.Spec.Taints),
		KubeletVersion:   node.Status.NodeInfo.KubeletVersion,
		OSImage:          node.Status.NodeInfo.OSImage,
		Architecture:     node.Status.NodeInfo.Architecture,
		InternalIP:       nodeInternalIP(node),
		Capacity:         mapCapacity(node.Status.Capacity),
		Allocatable:      mapCapacity(node.Status.Allocatable),
		CreatedAt:        node.CreationTimestamp.Time,
	})
}

// nodeRoleLabelPrefix is how Kubernetes records a node's role: as the presence
// of a label, with an empty value. There is no roles field.
const nodeRoleLabelPrefix = "node-role.kubernetes.io/"

// nodeRoles derives a node's roles from its labels.
func nodeRoles(node *corev1.Node) []string {
	roles := make([]string, 0, 1)
	for label := range node.Labels {
		if role, found := strings.CutPrefix(label, nodeRoleLabelPrefix); found && role != "" {
			roles = append(roles, role)
		}
	}
	sort.Strings(roles)
	return roles
}

// nodeInternalIP returns the node's cluster-internal address.
func nodeInternalIP(node *corev1.Node) string {
	for _, address := range node.Status.Addresses {
		if address.Type == corev1.NodeInternalIP {
			return address.Address
		}
	}
	return ""
}

// mapCapacity converts a resource list into the domain's capacity value.
func mapCapacity(list corev1.ResourceList) domain.Capacity {
	capacity := domain.Capacity{}
	if cpu, ok := list[corev1.ResourceCPU]; ok {
		capacity.CPUMilli = cpu.MilliValue()
	}
	if memory, ok := list[corev1.ResourceMemory]; ok {
		capacity.MemoryBytes = memory.Value()
	}
	if pods, ok := list[corev1.ResourcePods]; ok {
		capacity.Pods = pods.Value()
	}
	if ephemeral, ok := list[corev1.ResourceEphemeralStorage]; ok {
		capacity.EphemeralBytes = ephemeral.Value()
	}
	return capacity
}

// --- Workloads --------------------------------------------------------------

// mapDeployment translates a Deployment.
func mapDeployment(clusterID domain.ClusterID, item *appsv1.Deployment) (domain.Workload, error) {
	return newWorkload(clusterID, domain.WorkloadDeployment, item.ObjectMeta, workloadCounts{
		Desired:   derefInt32(item.Spec.Replicas, 1),
		Ready:     item.Status.ReadyReplicas,
		Current:   item.Status.Replicas,
		Updated:   item.Status.UpdatedReplicas,
		Available: item.Status.AvailableReplicas,
	}, podTemplateImages(item.Spec.Template), matchLabels(item.Spec.Selector))
}

// mapStatefulSet translates a StatefulSet.
func mapStatefulSet(clusterID domain.ClusterID, item *appsv1.StatefulSet) (domain.Workload, error) {
	return newWorkload(clusterID, domain.WorkloadStatefulSet, item.ObjectMeta, workloadCounts{
		Desired:   derefInt32(item.Spec.Replicas, 1),
		Ready:     item.Status.ReadyReplicas,
		Current:   item.Status.Replicas,
		Updated:   item.Status.UpdatedReplicas,
		Available: item.Status.AvailableReplicas,
	}, podTemplateImages(item.Spec.Template), matchLabels(item.Spec.Selector))
}

// mapDaemonSet translates a DaemonSet.
//
// A DaemonSet has no replica count: its "desired" is however many nodes match
// its selector, which the controller reports in status.
func mapDaemonSet(clusterID domain.ClusterID, item *appsv1.DaemonSet) (domain.Workload, error) {
	return newWorkload(clusterID, domain.WorkloadDaemonSet, item.ObjectMeta, workloadCounts{
		Desired:   item.Status.DesiredNumberScheduled,
		Ready:     item.Status.NumberReady,
		Current:   item.Status.CurrentNumberScheduled,
		Updated:   item.Status.UpdatedNumberScheduled,
		Available: item.Status.NumberAvailable,
	}, podTemplateImages(item.Spec.Template), matchLabels(item.Spec.Selector))
}

// mapReplicaSet translates a ReplicaSet.
func mapReplicaSet(clusterID domain.ClusterID, item *appsv1.ReplicaSet) (domain.Workload, error) {
	return newWorkload(clusterID, domain.WorkloadReplicaSet, item.ObjectMeta, workloadCounts{
		Desired:   derefInt32(item.Spec.Replicas, 1),
		Ready:     item.Status.ReadyReplicas,
		Current:   item.Status.Replicas,
		Updated:   item.Status.FullyLabeledReplicas,
		Available: item.Status.AvailableReplicas,
	}, podTemplateImages(item.Spec.Template), matchLabels(item.Spec.Selector))
}

// mapJob translates a Job.
//
// "Desired" is the completion count: a Job is done when that many pods have
// succeeded, so succeeded-versus-completions is the progress an operator reads.
func mapJob(clusterID domain.ClusterID, item *batchv1.Job) (domain.Workload, error) {
	workload, err := newWorkload(clusterID, domain.WorkloadJob, item.ObjectMeta, workloadCounts{
		Desired:   derefInt32(item.Spec.Completions, 1),
		Ready:     item.Status.Succeeded,
		Current:   item.Status.Active + item.Status.Succeeded + item.Status.Failed,
		Updated:   item.Status.Succeeded,
		Available: item.Status.Active,
		Failed:    item.Status.Failed,
	}, podTemplateImages(item.Spec.Template), matchLabels(item.Spec.Selector))
	if err != nil {
		return domain.Workload{}, err
	}
	return rebuildWithSuspension(workload, derefBool(item.Spec.Suspend)), nil
}

// mapCronJob translates a CronJob.
func mapCronJob(clusterID domain.ClusterID, item *batchv1.CronJob) (domain.Workload, error) {
	var lastScheduled time.Time
	if item.Status.LastScheduleTime != nil {
		lastScheduled = item.Status.LastScheduleTime.Time
	}

	namespace, err := domain.NewNamespaceName(item.Namespace)
	if err != nil {
		return domain.Workload{}, err
	}

	return domain.NewWorkload(domain.WorkloadSpec{
		Kind:          domain.WorkloadCronJob,
		Name:          item.Name,
		Namespace:     namespace,
		ClusterID:     clusterID,
		Current:       int32(len(item.Status.Active)),
		Images:        podTemplateImages(item.Spec.JobTemplate.Spec.Template),
		Labels:        item.Labels,
		Annotations:   gitOpsAnnotations(item.Annotations),
		Owner:         domain.Controller(mapOwnerReferences(item.OwnerReferences)),
		Suspended:     derefBool(item.Spec.Suspend),
		Schedule:      item.Spec.Schedule,
		LastScheduled: lastScheduled,
		CreatedAt:     item.CreationTimestamp.Time,
	})
}

// workloadCounts groups the replica numbers, which every controller reports
// under a different set of field names.
type workloadCounts struct {
	Desired, Ready, Current, Updated, Available int32
	// Failed is set only for Jobs; every other kind leaves it zero.
	Failed int32
}

// newWorkload assembles the shared parts of a controller translation.
func newWorkload(
	clusterID domain.ClusterID,
	kind domain.WorkloadKind,
	meta metav1.ObjectMeta,
	counts workloadCounts,
	images []string,
	selector map[string]string,
) (domain.Workload, error) {
	namespace, err := domain.NewNamespaceName(meta.Namespace)
	if err != nil {
		return domain.Workload{}, err
	}

	return domain.NewWorkload(domain.WorkloadSpec{
		Kind:        kind,
		Name:        meta.Name,
		Namespace:   namespace,
		ClusterID:   clusterID,
		Desired:     counts.Desired,
		Ready:       counts.Ready,
		Current:     counts.Current,
		Updated:     counts.Updated,
		Available:   counts.Available,
		Failed:      counts.Failed,
		Images:      images,
		Selector:    selector,
		Labels:      meta.Labels,
		Annotations: gitOpsAnnotations(meta.Annotations),
		Owner:       domain.Controller(mapOwnerReferences(meta.OwnerReferences)),
		CreatedAt:   meta.CreationTimestamp.Time,
	})
}

// gitOpsAnnotationPrefixes are the annotation keys worth carrying to the UI.
//
// An allowlist rather than the whole map, because the whole map is dominated
// by kubectl's last-applied-configuration — 239 KiB across sixty-one
// deployments on this project's test cluster, which would be re-sent on every
// refresh so that one column could read one key.
var gitOpsAnnotationPrefixes = []string{
	"argocd.argoproj.io/",
	"kustomize.toolkit.fluxcd.io/",
	"helm.toolkit.fluxcd.io/",
}

// gitOpsAnnotations copies only the annotations a GitOps controller sets.
func gitOpsAnnotations(all map[string]string) map[string]string {
	if len(all) == 0 {
		return nil
	}

	var kept map[string]string
	for key, value := range all {
		for _, prefix := range gitOpsAnnotationPrefixes {
			if strings.HasPrefix(key, prefix) {
				if kept == nil {
					kept = make(map[string]string, 2)
				}
				kept[key] = value
				break
			}
		}
	}
	return kept
}

// rebuildWithSuspension returns a copy of the workload marked as suspended.
func rebuildWithSuspension(workload domain.Workload, suspended bool) domain.Workload {
	return workload.WithSuspension(suspended)
}

// podTemplateImages lists the images a pod template runs, init containers
// included — an init container stuck pulling is just as much a reason a
// workload will not start.
func podTemplateImages(template corev1.PodTemplateSpec) []string {
	images := make([]string, 0, len(template.Spec.Containers))
	for _, container := range template.Spec.InitContainers {
		images = append(images, container.Image)
	}
	for _, container := range template.Spec.Containers {
		images = append(images, container.Image)
	}
	return images
}

// matchLabels extracts the simple label selector, which is what a controller
// uses in practice. Expression-based selectors have no compact display form
// and are left to the detail view.
func matchLabels(selector *metav1.LabelSelector) map[string]string {
	if selector == nil {
		return nil
	}
	return selector.MatchLabels
}

// --- Events -----------------------------------------------------------------

// mapEvent translates a Kubernetes Event.
func mapEvent(clusterID domain.ClusterID, event *corev1.Event) (domain.Event, error) {
	namespace, err := domain.NewNamespaceName(event.Namespace)
	if err != nil {
		return domain.Event{}, err
	}

	return domain.NewEvent(domain.EventSpec{
		Name:         event.Name,
		Namespace:    namespace,
		ClusterID:    clusterID,
		Type:         domain.NewEventType(event.Type),
		Reason:       event.Reason,
		Message:      event.Message,
		InvolvedKind: event.InvolvedObject.Kind,
		InvolvedName: event.InvolvedObject.Name,
		Source:       eventSource(event),
		Count:        eventCount(event),
		FirstSeen:    event.FirstTimestamp.Time,
		LastSeen:     eventLastSeen(event),
	})
}

// eventSource names the component that emitted the event.
func eventSource(event *corev1.Event) string {
	if event.Source.Component != "" {
		return event.Source.Component
	}
	return event.ReportingController
}

// eventCount returns how many times the event has fired.
//
// The same two generations that complicate the timestamps complicate this.
// `series.count` carries the repeat count for events emitted through the
// events.k8s.io API, `count` for the older form — and an event created once
// through the new API sets NEITHER, which is not zero occurrences. It is one.
//
// Reading the legacy field alone reported "Count 0" against events that had
// demonstrably just happened, which is the sort of figure that makes somebody
// distrust the column rather than the event.
func eventCount(event *corev1.Event) int32 {
	if event.Series != nil && event.Series.Count > 0 {
		return event.Series.Count
	}
	if event.Count > 0 {
		return event.Count
	}
	return 1
}

// eventLastSeen returns when the event most recently fired.
//
// Kubernetes has two generations of event timestamps. The modern
// `series.lastObservedTime` is set for repeating events, `lastTimestamp` for
// the older single-shot form, and `eventTime` for events emitted through the
// events.k8s.io API. Checking only one leaves a large share of a real
// cluster's events sorted as though they happened in 1970.
func eventLastSeen(event *corev1.Event) time.Time {
	if event.Series != nil && !event.Series.LastObservedTime.IsZero() {
		return event.Series.LastObservedTime.Time
	}
	if !event.LastTimestamp.IsZero() {
		return event.LastTimestamp.Time
	}
	if !event.EventTime.IsZero() {
		return event.EventTime.Time
	}
	return event.CreationTimestamp.Time
}

// --- Shared helpers ---------------------------------------------------------

// mapOwnerReferences translates owner references.
func mapOwnerReferences(owners []metav1.OwnerReference) []domain.OwnerReference {
	if len(owners) == 0 {
		return nil
	}
	mapped := make([]domain.OwnerReference, 0, len(owners))
	for _, owner := range owners {
		mapped = append(mapped, domain.OwnerReference{
			Kind:       owner.Kind,
			Name:       owner.Name,
			Controller: derefBool(owner.Controller),
		})
	}
	return mapped
}

// derefInt32 reads an optional int32, applying a default when unset.
//
// The default matters: Kubernetes treats an absent `replicas` as 1, not 0, so
// reading nil as zero would show every defaulted Deployment as scaled down.
func derefInt32(value *int32, fallback int32) int32 {
	if value == nil {
		return fallback
	}
	return *value
}

// derefBool reads an optional bool, treating unset as false.
func derefBool(value *bool) bool {
	return value != nil && *value
}

// blockingTaints counts the taints that actually refuse pods.
//
// PreferNoSchedule is excluded: it asks the scheduler to avoid the node and
// is ignored when nowhere else will do, so a node carrying only that one is
// not reserved in any sense a capacity figure should reflect.
func blockingTaints(taints []corev1.Taint) int {
	blocking := 0
	for _, taint := range taints {
		if taint.Effect == corev1.TaintEffectNoSchedule ||
			taint.Effect == corev1.TaintEffectNoExecute {
			blocking++
		}
	}
	return blocking
}

// --- Storage ----------------------------------------------------------------

// mapPersistentVolume translates a PersistentVolume.
func mapPersistentVolume(clusterID domain.ClusterID, item *corev1.PersistentVolume) (domain.PersistentVolume, error) {
	// The claim is a reference to an object that may already be gone, which is
	// exactly the case worth reporting — so it is rendered as text rather than
	// resolved.
	claimRef := ""
	if ref := item.Spec.ClaimRef; ref != nil {
		claimRef = ref.Namespace + "/" + ref.Name
	}

	var capacity int64
	if size, ok := item.Spec.Capacity[corev1.ResourceStorage]; ok {
		capacity = size.Value()
	}

	return domain.NewPersistentVolume(domain.PersistentVolumeSpec{
		Name:          item.Name,
		ClusterID:     clusterID,
		Phase:         domain.VolumePhase(item.Status.Phase),
		StorageClass:  item.Spec.StorageClassName,
		CapacityBytes: capacity,
		ReclaimPolicy: string(item.Spec.PersistentVolumeReclaimPolicy),
		ClaimRef:      claimRef,
		CreatedAt:     item.CreationTimestamp.Time,
	})
}

// mapPersistentVolumeClaim translates a PersistentVolumeClaim.
//
// Requested and actual capacity are kept apart because they routinely differ:
// providers round up to their own increments, so a claim asking for 3Gi is
// commonly bound to 4Gi, and reporting the request as the size would
// understate what the cluster is paying for.
func mapPersistentVolumeClaim(clusterID domain.ClusterID, item *corev1.PersistentVolumeClaim) (domain.PersistentVolumeClaim, error) {
	var requested, actual int64
	if size, ok := item.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
		requested = size.Value()
	}
	if size, ok := item.Status.Capacity[corev1.ResourceStorage]; ok {
		actual = size.Value()
	}

	storageClass := ""
	if item.Spec.StorageClassName != nil {
		storageClass = *item.Spec.StorageClassName
	}

	return domain.NewPersistentVolumeClaim(domain.PersistentVolumeClaimSpec{
		Name:           item.Name,
		Namespace:      domain.NamespaceName(item.Namespace),
		ClusterID:      clusterID,
		Phase:          domain.ClaimPhase(item.Status.Phase),
		StorageClass:   storageClass,
		RequestedBytes: requested,
		CapacityBytes:  actual,
		VolumeName:     item.Spec.VolumeName,
		CreatedAt:      item.CreationTimestamp.Time,
	})
}
