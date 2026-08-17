package k8s

import (
	corev1 "k8s.io/api/core/v1"
	apiversion "k8s.io/apimachinery/pkg/version"

	"k8sense/app/domain"
)

// This file is the anti-corruption layer between Kubernetes API types and the
// K8Sense domain. Nothing outside this package should ever see a corev1 value,
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
		CreatedAt:  pod.CreationTimestamp.Time,
	})
}

// mapPodPhase derives the phase K8Sense shows from the pod's reported phase.
//
// The one substitution is deletion: a pod with a deletion timestamp keeps
// reporting Running right up until it vanishes, so K8Sense reports Terminating
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
func mapContainers(pod *corev1.Pod) []domain.Container {
	statuses := make(map[string]corev1.ContainerStatus, len(pod.Status.ContainerStatuses))
	for _, status := range pod.Status.ContainerStatuses {
		statuses[status.Name] = status
	}

	containers := make([]domain.Container, 0, len(pod.Spec.Containers))
	for _, spec := range pod.Spec.Containers {
		container := domain.Container{
			Name:  spec.Name,
			Image: spec.Image,
			State: domain.ContainerStateWaiting,
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
		}

		containers = append(containers, container)
	}

	return containers
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
