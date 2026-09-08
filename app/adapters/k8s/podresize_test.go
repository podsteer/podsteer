package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	clientgotesting "k8s.io/client-go/testing"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

func sizedPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-0", Namespace: "web"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "app",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("512Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
					ResizePolicy: []corev1.ContainerResizePolicy{
						{ResourceName: corev1.ResourceMemory, RestartPolicy: corev1.RestartContainer},
						{ResourceName: corev1.ResourceCPU, RestartPolicy: corev1.NotRequired},
					},
				},
				{Name: "sidecar"},
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

// TestResizePodPatchesTheSubresourceAndNothingElse pins the two things this
// write can get wrong invisibly: which door it goes through, and how much of
// the pod it rewrites on the way.
func TestResizePodPatchesTheSubresourceAndNothingElse(t *testing.T) {
	client := fake.NewClientset(sizedPod())

	var captured clientgotesting.PatchAction
	client.PrependReactor("patch", "pods", func(action clientgotesting.Action) (bool, runtime.Object, error) {
		captured = action.(clientgotesting.PatchAction)
		return true, sizedPod(), nil
	})

	adapter := newTestAdapter("dev", client)
	err := adapter.ResizePod(context.Background(), "dev", "web", "api-0", domain.ResizePlan{
		Container:  "app",
		CPURequest: "750m",
	})
	if err != nil {
		t.Fatalf("ResizePod() error = %v", err)
	}

	// THE SUBRESOURCE IS THE POINT. Patching the pod itself is refused by the
	// API server — a pod's containers are otherwise immutable — so a patch
	// that went to the object rather than to `resize` would fail on a real
	// cluster while passing against a fake that does not enforce it.
	if got := captured.GetSubresource(); got != "resize" {
		t.Fatalf("subresource = %q, want resize", got)
	}

	var patch map[string]any
	if err := json.Unmarshal(captured.GetPatch(), &patch); err != nil {
		t.Fatalf("the patch is not JSON: %v", err)
	}

	containers := patch["spec"].(map[string]any)["containers"].([]any)
	if len(containers) != 1 {
		t.Fatalf("patch names %d containers, want exactly the one being resized", len(containers))
	}

	container := containers[0].(map[string]any)
	if container["name"] != "app" {
		t.Errorf("patch names %v, want app", container["name"])
	}

	resources := container["resources"].(map[string]any)
	requests := resources["requests"].(map[string]any)
	if requests["cpu"] != "750m" {
		t.Errorf("cpu request = %v, want 750m", requests["cpu"])
	}
	// ONLY WHAT THE PLAN CARRIED. A patch that also sent memory would rewrite
	// figures nobody touched and record every one in managedFields.
	if _, sent := requests["memory"]; sent {
		t.Errorf("patch sent memory, which the plan left alone: %v", requests)
	}
	if _, sent := resources["limits"]; sent {
		t.Errorf("patch sent limits, which the plan left alone: %v", resources)
	}
}

// TestResizePodTellsAnOldClusterFromAMissingPod covers the failure that would
// otherwise send somebody to look for a pod on screen in front of them.
func TestResizePodTellsAnOldClusterFromAMissingPod(t *testing.T) {
	client := fake.NewClientset(sizedPod())
	client.PrependReactor("patch", "pods", func(clientgotesting.Action) (bool, runtime.Object, error) {
		// What a cluster older than 1.33 answers: the SUBRESOURCE is not
		// served, reported as a 404 that names the pod.
		return true, nil, apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, "api-0")
	})

	adapter := newTestAdapter("dev", client)
	err := adapter.ResizePod(context.Background(), "dev", "web", "api-0", domain.ResizePlan{
		Container: "app", CPURequest: "750m",
	})

	if !errors.Is(err, ports.ErrResizeUnsupported) {
		t.Fatalf("err = %v, want ErrResizeUnsupported", err)
	}
	if errors.Is(err, ports.ErrNotFound) {
		t.Error("reported as a missing pod, which is on screen in front of the operator")
	}
}

func TestResizePodReportsAGenuinelyMissingPodAsMissing(t *testing.T) {
	// No pod in the fake at all: the 404 means what it says.
	client := fake.NewClientset()
	client.PrependReactor("patch", "pods", func(clientgotesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, "api-0")
	})

	adapter := newTestAdapter("dev", client)
	err := adapter.ResizePod(context.Background(), "dev", "web", "api-0", domain.ResizePlan{
		Container: "app", CPURequest: "750m",
	})

	if errors.Is(err, ports.ErrResizeUnsupported) {
		t.Fatalf("err = %v, want a missing pod reported as one", err)
	}
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestContainerResizeSpecReadsThePolicyThatDecidesARestart is why this reads
// the pod rather than the row on screen: resizePolicy is nowhere in the list's
// projection, and it is the field that decides whether applying a change
// restarts somebody's database.
func TestContainerResizeSpecReadsThePolicyThatDecidesARestart(t *testing.T) {
	adapter := newTestAdapter("dev", fake.NewClientset(sizedPod()))

	spec, err := adapter.ContainerResizeSpec(context.Background(), "dev", "web", "api-0", "app")
	if err != nil {
		t.Fatalf("ContainerResizeSpec() error = %v", err)
	}

	if spec.CPURequest != "500m" || spec.MemoryRequest != "512Mi" || spec.MemoryLimit != "1Gi" {
		t.Errorf("spec = %+v, want the container's own figures", spec)
	}
	// A figure the container does not declare stays empty rather than
	// becoming "0": the two mean different things in the box it goes into.
	if spec.CPULimit != "" {
		t.Errorf("CPULimit = %q, want empty for a container that declares none", spec.CPULimit)
	}
	if !spec.RestartsForMemory {
		t.Error("the memory policy of RestartContainer was not read")
	}
	if spec.RestartsForCPU {
		t.Error("an explicit NotRequired was read as a restart")
	}
}

func TestContainerResizeSpecRefusesAContainerThatIsNotThere(t *testing.T) {
	adapter := newTestAdapter("dev", fake.NewClientset(sizedPod()))

	_, err := adapter.ContainerResizeSpec(context.Background(), "dev", "web", "api-0", "nope")
	if !errors.Is(err, domain.ErrResizeContainerNotFound) {
		t.Fatalf("err = %v, want ErrResizeContainerNotFound", err)
	}
}
