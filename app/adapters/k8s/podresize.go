package k8s

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// ResizePod changes a running container's CPU and memory in place.
//
// THROUGH THE `resize` SUBRESOURCE, which is the whole point: patching the pod
// itself with new resources is refused by the API server on an existing pod,
// because a pod's containers are otherwise immutable. The subresource is the
// one door, it arrived in Kubernetes 1.33, and a cluster without it answers
// 404 — which this tells apart from a pod that is genuinely gone, the same way
// the ephemeral-container write does.
//
// A STRATEGIC MERGE PATCH, for the reason SetImage gives at length: containers
// are a LIST, a JSON merge patch replaces a list wholesale, and sending one
// container would delete every other container in the pod. The merge key is
// `name`, so this names one container and leaves everything else exactly as it
// was — including the figures in the plan that were left empty, which is what
// "leave this one alone" means.
func (a *Adapter) ResizePod(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName string, plan domain.ResizePlan) error {
	defer a.forgetReads(id)

	client, err := a.factory.clientFor(id)
	if err != nil {
		return err
	}

	ns := namespace.String()
	op := fmt.Sprintf("resizing container %q of pod %q", plan.Container, podName)

	requests := map[string]string{}
	limits := map[string]string{}
	if plan.CPURequest != "" {
		requests["cpu"] = plan.CPURequest
	}
	if plan.MemoryRequest != "" {
		requests["memory"] = plan.MemoryRequest
	}
	if plan.CPULimit != "" {
		limits["cpu"] = plan.CPULimit
	}
	if plan.MemoryLimit != "" {
		limits["memory"] = plan.MemoryLimit
	}

	resources := map[string]any{}
	if len(requests) > 0 {
		resources["requests"] = requests
	}
	if len(limits) > 0 {
		resources["limits"] = limits
	}
	if len(resources) == 0 {
		// PlanResize refuses this, so reaching here means a caller built a
		// plan by hand. Refused rather than sent: an empty patch is a write
		// that records a change in managedFields and changes nothing.
		return fmt.Errorf("%s: %w", op, domain.ErrResizeNoChange)
	}

	patch := map[string]any{
		"spec": map[string]any{
			"containers": []map[string]any{
				{"name": plan.Container, "resources": resources},
			},
		},
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshaling resize patch: %w", err)
	}

	_, err = client.CoreV1().Pods(ns).Patch(
		ctx, podName, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{}, "resize")
	if err != nil {
		// The same two-case split the ephemeral-container write makes: a 404
		// here usually means the SUBRESOURCE is absent — a cluster older than
		// 1.33, or the feature gate off — rather than that the pod is gone,
		// and telling somebody their pod does not exist while it is on screen
		// in front of them is the worst available answer.
		if apierrors.IsNotFound(err) {
			if _, getErr := client.CoreV1().Pods(ns).Get(ctx, podName, metav1.GetOptions{}); getErr == nil {
				return fmt.Errorf("%s: %w", op, ports.ErrResizeUnsupported)
			}
		}
		return classify(op, err)
	}

	return nil
}

// ContainerResizeSpec reads one container's current figures and its resize
// policy, for planning a change.
//
// READ FROM THE POD RATHER THAN FROM THE ROW ON SCREEN. The list's projection
// carries formatted usage, not the container's declared requests, and the
// resizePolicy is nowhere in it at all — and the policy is the field that
// decides whether applying this restarts somebody's database.
func (a *Adapter) ContainerResizeSpec(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName string) (domain.ContainerResize, error) {
	client, err := a.factory.clientFor(id)
	if err != nil {
		return domain.ContainerResize{}, err
	}

	op := fmt.Sprintf("reading container %q of pod %q", containerName, podName)

	pod, err := client.CoreV1().Pods(namespace.String()).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return domain.ContainerResize{}, classify(op, err)
	}

	for index := range pod.Spec.Containers {
		container := &pod.Spec.Containers[index]
		if container.Name != containerName {
			continue
		}
		return containerResize(container), nil
	}

	return domain.ContainerResize{}, fmt.Errorf("%s: %w", op, domain.ErrResizeContainerNotFound)
}

// containerResize projects one container's figures and policy.
//
// A quantity is rendered with its own String(), which is how Kubernetes wrote
// it — "500m", "1Gi" — rather than reformatted here: the field is going back
// into a text box the operator edits, and a figure that changed shape on the
// way to the screen reads as an edit nobody made.
func containerResize(container *corev1.Container) domain.ContainerResize {
	out := domain.ContainerResize{Name: container.Name}

	if value, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
		out.CPURequest = value.String()
	}
	if value, ok := container.Resources.Limits[corev1.ResourceCPU]; ok {
		out.CPULimit = value.String()
	}
	if value, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
		out.MemoryRequest = value.String()
	}
	if value, ok := container.Resources.Limits[corev1.ResourceMemory]; ok {
		out.MemoryLimit = value.String()
	}

	// ABSENT MEANS NotRequired, which is the API's own default and the reason
	// in-place resize is worth having: the kubelet changes the cgroup under
	// the running process. Only an explicit RestartContainer restarts it.
	for _, policy := range container.ResizePolicy {
		if policy.RestartPolicy != corev1.RestartContainer {
			continue
		}
		switch policy.ResourceName {
		case corev1.ResourceCPU:
			out.RestartsForCPU = true
		case corev1.ResourceMemory:
			out.RestartsForMemory = true
		}
	}

	return out
}
