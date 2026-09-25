package k8s

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clientgotesting "k8s.io/client-go/testing"
)

// TestStartNodeShellDeletesThePodWhenTheRegistryClosedDuringTheWait is the
// shutdown race, and it is the sharpest leak this package can produce.
//
// StartNodeShell creates the pod and only registers it AFTER waiting up to a
// minute for it to run, so a StopAllNodeShells landing in that window copies
// an empty registry, finds nothing to delete, and the process exits — leaving
// a PRIVILEGED pod in the node's process and network namespaces until its
// one-hour deadline reaps it. The wait does not abort, because nothing
// cancels the framework's runtime context at shutdown.
//
// The pod here reaches Running only after the sweep has run, which is what
// makes the ordering the real one rather than a coincidence of scheduling.
func TestStartNodeShellDeletesThePodWhenTheRegistryClosedDuringTheWait(t *testing.T) {
	client := fake.NewSimpleClientset()
	adapter := newTestAdapter("dev", client)

	swept := make(chan struct{})
	var promoted sync.Once
	client.PrependReactor("get", "pods", func(action clientgotesting.Action) (bool, runtime.Object, error) {
		select {
		case <-swept:
			// The sweep has happened; let the pod come up now, so the start
			// leaves its wait on the far side of the shutdown.
			promoted.Do(func() { promoteToRunning(client, action) })
		default:
		}
		return false, nil, nil
	})

	started := make(chan error, 1)
	go func() {
		_, err := adapter.StartNodeShell(context.Background(), "dev", "kube-system", "node-1", "docker.io/library/alpine:3.20")
		started <- err
	}()

	// Wait for the pod to exist first, so the sweep genuinely races a start
	// already past Create rather than one that has not begun.
	waitForAnyPod(t, client, "kube-system")

	adapter.StopAllNodeShells()
	close(swept)

	select {
	case err := <-started:
		if !errors.Is(err, errNodeShellsClosed) {
			t.Fatalf("StartNodeShell() error = %v, want errNodeShellsClosed — a start racing shutdown must be refused, not registered", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("StartNodeShell() never returned after the registry closed")
	}

	pods, err := client.CoreV1().Pods("kube-system").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("listing pods: %v", err)
	}
	if len(pods.Items) != 0 {
		t.Fatalf("pods left after a start racing shutdown = %d, want 0 — a privileged pod must never outlive the process that created it", len(pods.Items))
	}
	if live := adapter.ListNodeShells(); len(live) != 0 {
		t.Fatalf("ListNodeShells() = %d, want 0 — nothing may be registered into a closed registry", len(live))
	}
}

// TestStopAllNodeShellsClosesTheRegistry states the same rule directly: a
// start beginning entirely after the sweep is refused and its pod deleted,
// rather than landing in a map nothing reads again.
func TestStopAllNodeShellsClosesTheRegistry(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("create", "pods", runningReactor)
	adapter := newTestAdapter("dev", client)

	adapter.StopAllNodeShells()

	_, err := adapter.StartNodeShell(context.Background(), "dev", "kube-system", "node-1", "docker.io/library/alpine:3.20")
	if !errors.Is(err, errNodeShellsClosed) {
		t.Fatalf("StartNodeShell() after StopAllNodeShells error = %v, want errNodeShellsClosed", err)
	}

	pods, listErr := client.CoreV1().Pods("kube-system").List(context.Background(), metav1.ListOptions{})
	if listErr != nil {
		t.Fatalf("listing pods: %v", listErr)
	}
	if len(pods.Items) != 0 {
		t.Fatalf("pods left after a refused start = %d, want 0", len(pods.Items))
	}
	if live := adapter.ListNodeShells(); len(live) != 0 {
		t.Fatalf("ListNodeShells() = %d, want 0", len(live))
	}
}

// promoteToRunning marks the pod a GET is about to answer with as Running.
func promoteToRunning(client *fake.Clientset, action clientgotesting.Action) {
	get, ok := action.(clientgotesting.GetAction)
	if !ok {
		return
	}
	resource := corev1.SchemeGroupVersion.WithResource("pods")
	object, err := client.Tracker().Get(resource, get.GetNamespace(), get.GetName())
	if err != nil {
		return
	}
	pod, ok := object.(*corev1.Pod)
	if !ok {
		return
	}
	running := pod.DeepCopy()
	running.Status.Phase = corev1.PodRunning
	_ = client.Tracker().Update(resource, running, get.GetNamespace())
}

// waitForAnyPod blocks until at least one pod exists in ns.
func waitForAnyPod(t *testing.T, client *fake.Clientset, ns string) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		pods, err := client.CoreV1().Pods(ns).List(context.Background(), metav1.ListOptions{})
		if err == nil && len(pods.Items) > 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("the node-shell pod was never created")
}
