package k8s

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	clientgotesting "k8s.io/client-go/testing"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// runningPod is a pod with one ordinary container, enough for the debug patch
// to have something to append to.
func runningPod(namespace, name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app", Image: "nginx:1.27"}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

// TestAddEphemeralContainerPreservesExistingAndSetsTarget pins the three
// properties the strategic merge patch must hold: an ephemeral container
// already on the pod survives, the new one's targetContainerName is set for
// process-namespace sharing, and the generated name has the debugger prefix.
func TestAddEphemeralContainerPreservesExistingAndSetsTarget(t *testing.T) {
	pod := runningPod("default", "web-0")
	// A debug container from an earlier investigation. It must not be lost.
	pod.Spec.EphemeralContainers = []corev1.EphemeralContainer{{
		EphemeralContainerCommon: corev1.EphemeralContainerCommon{Name: "debugger-aaaaa", Image: "busybox:1.36"},
	}}

	client := fake.NewSimpleClientset(pod)
	adapter := newTestAdapter("dev", client)

	name, err := adapter.AddEphemeralContainer(context.Background(), "dev", "default", "web-0", domain.DebugContainerSpec{
		Image:           "busybox:1.37",
		TargetContainer: "app",
		Command:         []string{"sh"},
		TTY:             true,
		Stdin:           true,
	})
	if err != nil {
		t.Fatalf("AddEphemeralContainer() error = %v", err)
	}
	if !strings.HasPrefix(name, "debugger-") {
		t.Fatalf("generated name = %q, want the %q prefix", name, "debugger-")
	}

	got, err := client.CoreV1().Pods("default").Get(context.Background(), "web-0", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("re-reading pod: %v", err)
	}

	names := make(map[string]corev1.EphemeralContainer)
	for _, ec := range got.Spec.EphemeralContainers {
		names[ec.Name] = ec
	}
	if _, ok := names["debugger-aaaaa"]; !ok {
		t.Errorf("existing ephemeral container was lost; have %v", ephemeralNames(got))
	}
	added, ok := names[name]
	if !ok {
		t.Fatalf("added ephemeral container %q is not on the pod; have %v", name, ephemeralNames(got))
	}
	if added.TargetContainerName != "app" {
		t.Errorf("targetContainerName = %q, want %q", added.TargetContainerName, "app")
	}
	if added.Image != "busybox:1.37" {
		t.Errorf("image = %q, want %q", added.Image, "busybox:1.37")
	}
	if !added.TTY || !added.Stdin {
		t.Errorf("tty=%v stdin=%v, want both true", added.TTY, added.Stdin)
	}
}

// TestAddEphemeralContainerGeneratesAUniqueNameEachTime asserts two debug
// containers added in a row do not collide.
func TestAddEphemeralContainerGeneratesAUniqueNameEachTime(t *testing.T) {
	client := fake.NewSimpleClientset(runningPod("default", "web-0"))
	adapter := newTestAdapter("dev", client)

	spec := domain.DebugContainerSpec{Image: "busybox:1.37", Command: []string{"sh"}, TTY: true, Stdin: true}

	first, err := adapter.AddEphemeralContainer(context.Background(), "dev", "default", "web-0", spec)
	if err != nil {
		t.Fatalf("first AddEphemeralContainer() error = %v", err)
	}
	second, err := adapter.AddEphemeralContainer(context.Background(), "dev", "default", "web-0", spec)
	if err != nil {
		t.Fatalf("second AddEphemeralContainer() error = %v", err)
	}
	if first == second {
		t.Fatalf("both debug containers got the same name %q, want unique names", first)
	}
}

// TestAddEphemeralContainerClassifiesTheUnsupportedSubresource pins the error
// path a cluster too old (or with the feature gate off) produces: a 404 on the
// ephemeralcontainers subresource while the pod itself is present must be
// reported as ErrEphemeralContainersUnsupported, never a bare not-found.
func TestAddEphemeralContainerClassifiesTheUnsupportedSubresource(t *testing.T) {
	client := fake.NewSimpleClientset(runningPod("default", "web-0"))

	// The subresource endpoint answers 404, exactly as an API server that does
	// not serve pods/ephemeralcontainers does — while the pod GET still works.
	client.PrependReactor("patch", "pods", func(clientgotesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, "web-0")
	})

	adapter := newTestAdapter("dev", client)

	_, err := adapter.AddEphemeralContainer(context.Background(), "dev", "default", "web-0", domain.DebugContainerSpec{
		Image: "busybox:1.37", Command: []string{"sh"}, TTY: true, Stdin: true,
	})
	if !errors.Is(err, ports.ErrEphemeralContainersUnsupported) {
		t.Fatalf("AddEphemeralContainer() error = %v, want wrapping ErrEphemeralContainersUnsupported", err)
	}
	if errors.Is(err, ports.ErrNotFound) {
		t.Fatal("the unsupported subresource must not classify as a missing pod")
	}
}

// TestWaitForEphemeralContainerRunningSucceedsOnceRunning drives the success
// path: the pod's ephemeralContainerStatuses gains a running entry, and the
// wait returns.
func TestWaitForEphemeralContainerRunningSucceedsOnceRunning(t *testing.T) {
	pod := runningPod("default", "web-0")
	pod.Status.EphemeralContainerStatuses = []corev1.ContainerStatus{{
		Name:  "debugger-bbbbb",
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.Now()}},
	}}
	client := fake.NewSimpleClientset(pod)

	err := waitEphemeralContainerRunning(context.Background(), client, "default", "web-0", "debugger-bbbbb", time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("waitEphemeralContainerRunning() error = %v, want nil once the container reports Running", err)
	}
}

// TestWaitForEphemeralContainerRunningTimesOut drives the timeout path: the
// container never appears, so the wait gives up with a clear message rather
// than hanging.
func TestWaitForEphemeralContainerRunningTimesOut(t *testing.T) {
	client := fake.NewSimpleClientset(runningPod("default", "web-0"))

	err := waitEphemeralContainerRunning(context.Background(), client, "default", "web-0", "debugger-ccccc", time.Millisecond, 20*time.Millisecond)
	if err == nil {
		t.Fatal("waitEphemeralContainerRunning() error = nil, want a timeout")
	}
	if !strings.Contains(err.Error(), "did not start") {
		t.Fatalf("waitEphemeralContainerRunning() error = %v, want it to say the container did not start", err)
	}
}

// TestWaitForEphemeralContainerRunningReportsATerminatedContainer covers the
// container coming up and dying (a bad image, a command that exits): the wait
// must not spin until the timeout when the answer is already known.
func TestWaitForEphemeralContainerRunningReportsATerminatedContainer(t *testing.T) {
	pod := runningPod("default", "web-0")
	pod.Status.EphemeralContainerStatuses = []corev1.ContainerStatus{{
		Name: "debugger-ddddd",
		State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			Reason: "Error", ExitCode: 1,
		}},
	}}
	client := fake.NewSimpleClientset(pod)

	err := waitEphemeralContainerRunning(context.Background(), client, "default", "web-0", "debugger-ddddd", time.Millisecond, time.Second)
	if err == nil {
		t.Fatal("waitEphemeralContainerRunning() error = nil, want the terminated container reported")
	}
	if !strings.Contains(err.Error(), "terminated") {
		t.Fatalf("waitEphemeralContainerRunning() error = %v, want it to name the termination", err)
	}
}

func ephemeralNames(pod *corev1.Pod) []string {
	out := make([]string, 0, len(pod.Spec.EphemeralContainers))
	for _, ec := range pod.Spec.EphemeralContainers {
		out = append(out, ec.Name)
	}
	return out
}
