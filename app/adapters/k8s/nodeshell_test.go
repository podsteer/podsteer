package k8s

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clientgotesting "k8s.io/client-go/testing"

	"github.com/podsteer/podsteer/app/domain"
)

// TestBuildNodeShellPodSpec asserts every field the node shell relies on. Each
// one is load-bearing: drop hostPID and nsenter has no PID 1 to enter; drop
// the toleration and the pod cannot land on a control-plane node; drop
// privileged and nsenter is refused; drop the deadline and a crashed PodSteer
// leaves a root shell running forever.
func TestBuildNodeShellPodSpec(t *testing.T) {
	pod := buildNodeShellPod("podsteer-node-shell-abcde", "kube-system", "node-1", "docker.io/library/alpine:3.20")

	if pod.Name != "podsteer-node-shell-abcde" {
		t.Errorf("name = %q, want the caller's name", pod.Name)
	}
	if pod.Namespace != "kube-system" {
		t.Errorf("namespace = %q, want kube-system", pod.Namespace)
	}
	if got := pod.Labels[nodeShellManagedByLabel]; got != nodeShellManagedByValue {
		t.Errorf("%s = %q, want %q", nodeShellManagedByLabel, got, nodeShellManagedByValue)
	}
	if got := pod.Labels[nodeShellPurposeLabel]; got != nodeShellPurposeValue {
		t.Errorf("%s = %q, want %q", nodeShellPurposeLabel, got, nodeShellPurposeValue)
	}

	spec := pod.Spec
	if spec.NodeName != "node-1" {
		t.Errorf("nodeName = %q, want node-1 (the pod must be pinned to the chosen node)", spec.NodeName)
	}
	if !spec.HostPID {
		t.Error("hostPID = false, want true — nsenter --target 1 needs the node's PID namespace")
	}
	if !spec.HostNetwork {
		t.Error("hostNetwork = false, want true")
	}
	if spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("restartPolicy = %q, want Never", spec.RestartPolicy)
	}
	if spec.ActiveDeadlineSeconds == nil || *spec.ActiveDeadlineSeconds != nodeShellDeadlineSeconds {
		t.Errorf("activeDeadlineSeconds = %v, want %d (the crash backstop)", spec.ActiveDeadlineSeconds, nodeShellDeadlineSeconds)
	}

	// A single Exists toleration with no key/effect tolerates every taint.
	if len(spec.Tolerations) != 1 {
		t.Fatalf("tolerations = %d, want exactly one universal toleration", len(spec.Tolerations))
	}
	tol := spec.Tolerations[0]
	if tol.Operator != corev1.TolerationOpExists || tol.Key != "" || tol.Effect != "" {
		t.Errorf("toleration = %+v, want a blanket {Operator: Exists} that matches every taint", tol)
	}

	if len(spec.Containers) != 1 {
		t.Fatalf("containers = %d, want exactly one", len(spec.Containers))
	}
	c := spec.Containers[0]
	if c.Name != nodeShellContainerName {
		t.Errorf("container name = %q, want %q", c.Name, nodeShellContainerName)
	}
	if c.Image != "docker.io/library/alpine:3.20" {
		t.Errorf("image = %q, want the caller's image", c.Image)
	}
	if !c.TTY || !c.Stdin {
		t.Errorf("tty=%v stdin=%v, want both true so the shell can be attached to", c.TTY, c.Stdin)
	}
	if c.SecurityContext == nil || c.SecurityContext.Privileged == nil || !*c.SecurityContext.Privileged {
		t.Error("securityContext.privileged is not true — nsenter cannot enter host namespaces without it")
	}
	if len(c.Command) == 0 || c.Command[0] != "nsenter" {
		t.Errorf("command = %v, want it to start with nsenter", c.Command)
	}
	if !slices.Contains(c.Command, "--pid") || !slices.Contains(c.Command, "--mount") {
		t.Errorf("command = %v, want it to enter the mount and pid namespaces", c.Command)
	}
	// bash -l with a fall back to sh — a host without bash still gets a shell.
	joined := strings.Join(c.Command, " ")
	if !strings.Contains(joined, "bash -l") || !strings.Contains(joined, "exec sh") {
		t.Errorf("command = %q, want a bash -l login shell falling back to sh", joined)
	}
}

// runningReactor marks every pod it creates Running, so waitPodRunning returns
// at once. Returning handled=false lets the default tracker store the mutated
// pod.
func runningReactor(action clientgotesting.Action) (bool, runtime.Object, error) {
	if create, ok := action.(clientgotesting.CreateAction); ok {
		if pod, ok := create.GetObject().(*corev1.Pod); ok {
			pod.Status.Phase = corev1.PodRunning
		}
	}
	return false, nil, nil
}

// TestStartNodeShellCreatesRunsAndRecords covers the success path: a pod is
// created on the node, waited for, and recorded in the live registry.
func TestStartNodeShellCreatesRunsAndRecords(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("create", "pods", runningReactor)
	adapter := newTestAdapter("dev", client)

	shell, err := adapter.StartNodeShell(context.Background(), "dev", "kube-system", "node-1", "docker.io/library/alpine:3.20")
	if err != nil {
		t.Fatalf("StartNodeShell() error = %v", err)
	}
	if !strings.HasPrefix(shell.PodName, nodeShellNamePrefix) {
		t.Errorf("pod name = %q, want the %q prefix", shell.PodName, nodeShellNamePrefix)
	}
	if shell.NodeName != "node-1" {
		t.Errorf("node = %q, want node-1", shell.NodeName)
	}
	if shell.ContainerName != nodeShellContainerName {
		t.Errorf("container = %q, want %q", shell.ContainerName, nodeShellContainerName)
	}

	if _, err := client.CoreV1().Pods("kube-system").Get(context.Background(), shell.PodName, metav1.GetOptions{}); err != nil {
		t.Fatalf("the node-shell pod was not created: %v", err)
	}
	if live := adapter.ListNodeShells(); len(live) != 1 {
		t.Fatalf("ListNodeShells() = %d, want 1", len(live))
	}
}

// TestStopNodeShellDeletesThePodAndForgetsIt is the delete-on-end lifecycle:
// stopping a node shell removes its pod from the cluster and from the list.
func TestStopNodeShellDeletesThePodAndForgetsIt(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("create", "pods", runningReactor)
	adapter := newTestAdapter("dev", client)

	shell, err := adapter.StartNodeShell(context.Background(), "dev", "kube-system", "node-1", "docker.io/library/alpine:3.20")
	if err != nil {
		t.Fatalf("StartNodeShell() error = %v", err)
	}

	if err := adapter.StopNodeShell(shell.ID); err != nil {
		t.Fatalf("StopNodeShell() error = %v", err)
	}

	_, err = client.CoreV1().Pods("kube-system").Get(context.Background(), shell.PodName, metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("pod get after stop = %v, want NotFound — the pod must be deleted", err)
	}
	if live := adapter.ListNodeShells(); len(live) != 0 {
		t.Fatalf("ListNodeShells() = %d, want 0 after stop", len(live))
	}
}

// TestStopNodeShellIsIdempotent covers the session ending and an explicit stop
// both reaching the same shell.
func TestStopNodeShellIsIdempotent(t *testing.T) {
	adapter := newTestAdapter("dev", fake.NewSimpleClientset())
	if err := adapter.StopNodeShell("nope"); err != nil {
		t.Fatalf("StopNodeShell() on an unknown id = %v, want nil", err)
	}
}

// TestStopAllNodeShellsDeletesEveryPod is the shutdown path: no privileged pod
// is left running.
func TestStopAllNodeShellsDeletesEveryPod(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("create", "pods", runningReactor)
	adapter := newTestAdapter("dev", client)

	first, err := adapter.StartNodeShell(context.Background(), "dev", "kube-system", "node-1", "docker.io/library/alpine:3.20")
	if err != nil {
		t.Fatalf("first StartNodeShell() error = %v", err)
	}
	second, err := adapter.StartNodeShell(context.Background(), "dev", "kube-system", "node-2", "docker.io/library/alpine:3.20")
	if err != nil {
		t.Fatalf("second StartNodeShell() error = %v", err)
	}

	adapter.StopAllNodeShells()

	if live := adapter.ListNodeShells(); len(live) != 0 {
		t.Fatalf("ListNodeShells() = %d, want 0 after StopAll", len(live))
	}
	for _, shell := range []domain.NodeShell{first, second} {
		_, err := client.CoreV1().Pods("kube-system").Get(context.Background(), shell.PodName, metav1.GetOptions{})
		if !apierrors.IsNotFound(err) {
			t.Errorf("pod %q get after StopAll = %v, want NotFound", shell.PodName, err)
		}
	}
}

// TestStartNodeShellDeletesThePodWhenItNeverRuns proves the wait's failure
// path does not leak: a pod that never reaches Running is deleted rather than
// left as a privileged pod nobody attached to.
func TestStartNodeShellDeletesThePodWhenItNeverRuns(t *testing.T) {
	// No running reactor here, so the created pod stays phase "" forever.
	client := fake.NewSimpleClientset()
	adapter := newTestAdapter("dev", client)

	// A context that expires quickly stands in for the production timeout, so
	// the wait gives up almost at once.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err := adapter.StartNodeShell(ctx, "dev", "kube-system", "node-1", "docker.io/library/alpine:3.20")
	if err == nil {
		t.Fatal("StartNodeShell() error = nil, want a wait failure for a pod that never runs")
	}

	pods, listErr := client.CoreV1().Pods("kube-system").List(context.Background(), metav1.ListOptions{})
	if listErr != nil {
		t.Fatalf("listing pods: %v", listErr)
	}
	if len(pods.Items) != 0 {
		t.Fatalf("pods left after a failed start = %d, want 0 — the pod must be cleaned up", len(pods.Items))
	}
	if live := adapter.ListNodeShells(); len(live) != 0 {
		t.Fatalf("ListNodeShells() = %d, want 0 after a failed start", len(live))
	}
}

// TestWaitPodRunningTimesOut drives the timeout path directly.
func TestWaitPodRunningTimesOut(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "stuck", Namespace: "kube-system"},
		Status:     corev1.PodStatus{Phase: corev1.PodPending},
	})

	err := waitPodRunning(context.Background(), client, "kube-system", "stuck", time.Millisecond, 20*time.Millisecond)
	if err == nil {
		t.Fatal("waitPodRunning() error = nil, want a timeout for a pod stuck Pending")
	}
	if !strings.Contains(err.Error(), "did not start") {
		t.Fatalf("waitPodRunning() error = %v, want it to say the pod did not start", err)
	}
}
