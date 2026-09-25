package k8s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/podsteer/podsteer/app/domain"
)

// Everything Kubernetes itself reports about a container's image, gathered in
// two GETs and shaped by domain.NewImageReport.
//
// TWO GETS AND NOTHING ELSE. No registry, no pull Secret, no image pulled and
// nothing cached — see domain.ImageReport for why the registry half is a
// decision that has not been made rather than a feature that was forgotten.
// It is not on any refresh tick either: ports.InspectPort's own comment is
// where that rule is written down.

// ImageFacts gathers a container's image facts. See ports.InspectPort.
func (a *Adapter) ImageFacts(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName string) (domain.ImageFacts, error) {
	op := fmt.Sprintf("reading image facts for %s/%s/%s in %q", namespace, podName, containerName, id)

	client, err := a.factory.clientFor(id)
	if err != nil {
		return domain.ImageFacts{}, err
	}

	pod, err := client.CoreV1().Pods(namespace.String()).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return domain.ImageFacts{}, classify(op, err)
	}

	facts := domain.ImageFacts{
		Container: containerName,
		NodeName:  pod.Spec.NodeName,
	}

	// Init and ephemeral containers count: an init container that cannot pull
	// its image is exactly the case somebody opens this pane for, and it is
	// the one whose image nothing else on screen describes.
	if container, ok := findContainer(pod, containerName); ok {
		facts.DeclaredRef = container.Image
		facts.PullPolicy = string(container.ImagePullPolicy)
	}
	if status, ok := findContainerStatus(pod, containerName); ok {
		facts.ResolvedRef = status.Image
		facts.ImageID = status.ImageID
	}

	// NAMES ONLY. What these Secrets contain is what a registry credential
	// is, and reading one to describe an image would be exactly the
	// read-on-render pattern the Secrets doctrine exists to prevent — see
	// domain.ImageCredentialNote, which is what the pane says instead.
	for _, ref := range pod.Spec.ImagePullSecrets {
		if ref.Name != "" {
			facts.PullSecrets = append(facts.PullSecrets, ref.Name)
		}
	}

	if facts.NodeName == "" {
		return facts, nil
	}

	node, err := client.CoreV1().Nodes().Get(ctx, facts.NodeName, metav1.GetOptions{})
	if err != nil {
		// NOT A FAILED CALL. Plenty of accounts may read a pod in their
		// namespace and not a cluster-scoped node, and the digest, the
		// references and the pull policy are all still worth showing. The
		// refusal travels inside the answer, the way Overview.Unavailable and
		// MetricsStatus do, so the size reads as unreadable rather than as
		// zero.
		facts.NodeUnreadable = nodeUnreadableReason(err, facts.NodeName)
		return facts, nil
	}

	facts.NodeImages = make([]domain.NodeImage, 0, len(node.Status.Images))
	for _, image := range node.Status.Images {
		facts.NodeImages = append(facts.NodeImages, domain.NodeImage{
			Names:     image.Names,
			SizeBytes: image.SizeBytes,
		})
	}

	return facts, nil
}

// nodeUnreadableReason is the sentence the pane prints where a size would
// have been. It names the permission, because that is the whole diagnosis and
// the operator cannot see the log.
func nodeUnreadableReason(err error, nodeName string) string {
	classified := classify("", err)
	return fmt.Sprintf("node %s could not be read, so its image list is unavailable (%v)", nodeName, classified)
}

// findContainer looks through every container list a pod has, in the order
// the pane shows them.
func findContainer(pod *corev1.Pod, name string) (corev1.Container, bool) {
	for _, container := range pod.Spec.Containers {
		if container.Name == name {
			return container, true
		}
	}
	for _, container := range pod.Spec.InitContainers {
		if container.Name == name {
			return container, true
		}
	}
	for _, container := range pod.Spec.EphemeralContainers {
		if container.Name == name {
			return corev1.Container(container.EphemeralContainerCommon), true
		}
	}
	return corev1.Container{}, false
}

// findContainerStatus does the same for the status lists, which are where the
// resolved reference and the digest live.
func findContainerStatus(pod *corev1.Pod, name string) (corev1.ContainerStatus, bool) {
	lists := [][]corev1.ContainerStatus{
		pod.Status.ContainerStatuses,
		pod.Status.InitContainerStatuses,
		pod.Status.EphemeralContainerStatuses,
	}
	for _, list := range lists {
		for _, status := range list {
			if status.Name == name {
				return status, true
			}
		}
	}
	return corev1.ContainerStatus{}, false
}
