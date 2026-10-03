package k8s

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/podsteer/podsteer/app/domain"
)

// lostForwardRig is a supervisor over a fake cluster and a fake dial, with
// every timing in milliseconds.
type lostForwardRig struct {
	adapter *Adapter
	client  *fake.Clientset
	entry   *forwarder
	dials   atomic.Int64
	// dead ends the current attempt, as a pod dying does.
	dead chan struct{}
}

func newLostForwardRig(t *testing.T) (*lostForwardRig, func()) {
	t.Helper()
	pollingLists(t)

	client, _ := watchedClient(t) // no pods: nothing to reconnect to yet
	adapter := newForwardTestAdapter(t, "dev", client)
	adapter.forwards.window = 40 * time.Millisecond
	adapter.forwards.backoff = 5 * time.Millisecond
	adapter.forwards.lostEvery = 15 * time.Millisecond

	rig := &lostForwardRig{adapter: adapter, client: client, dead: make(chan struct{})}
	adapter.forwards.dial = func(domain.ClusterID, domain.NamespaceName, string, int, int) (bool, int, attempt, error) {
		rig.dials.Add(1)
		next := attempt{stop: make(chan struct{}), done: make(chan struct{}), failed: make(chan error, 1)}
		go func() {
			<-next.stop
			close(next.done)
		}()
		return true, 18080, next, nil
	}

	first := attempt{stop: make(chan struct{}), done: make(chan struct{}), failed: make(chan error, 1)}
	go func() {
		select {
		case <-rig.dead:
			close(first.done)
		case <-first.stop:
			close(first.done)
		}
	}()

	rig.entry = &forwarder{
		forward: domain.Forward{
			ID: "1", ClusterID: "dev", Namespace: "web", Pod: "api-0",
			LocalPort: 18080, RemotePort: 8080,
			Selector: map[string]string{"app": "web"},
		},
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
		retry: make(chan struct{}, 1),
	}
	adapter.forwards.mu.Lock()
	adapter.forwards.byID["1"] = rig.entry
	adapter.forwards.mu.Unlock()

	go adapter.superviseForward(rig.entry, first, "http")

	return rig, func() { _ = adapter.StopPortForward("1") }
}

func TestAForwardWhoseWindowRanOutIsLostNotDeleted(t *testing.T) {
	rig, stop := newLostForwardRig(t)
	defer stop()

	close(rig.dead) // the pod dies and nothing replaces it

	waitFor(t, func() bool { return rig.entry.snapshot().Lost })

	listed := rig.adapter.ListPortForwards()
	if len(listed) != 1 || !listed[0].Lost || listed[0].Reconnecting {
		t.Fatalf("ListPortForwards() = %+v, want the forward kept and marked lost", listed)
	}
}

func TestALostForwardRecoversWhenTheClusterReturns(t *testing.T) {
	tests := []struct {
		name  string
		nudge bool
	}{
		{"on its own slow cadence", false},
		{"when the operator presses reconnect", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rig, stop := newLostForwardRig(t)
			defer stop()
			if tt.nudge {
				// So slow that only the nudge can explain recovery.
				rig.adapter.forwards.lostEvery = time.Hour
			}

			close(rig.dead)
			waitFor(t, func() bool { return rig.entry.snapshot().Lost })

			// The cluster comes back: a ready pod matching the selector exists.
			if _, err := rig.client.CoreV1().Pods("web").Create(
				context.Background(), richPod("api-1"), metav1.CreateOptions{}); err != nil {
				t.Fatalf("creating the replacement pod: %v", err)
			}
			if tt.nudge {
				if err := rig.adapter.ReconnectPortForward("1"); err != nil {
					t.Fatalf("ReconnectPortForward() error = %v", err)
				}
			}

			waitFor(t, func() bool {
				f := rig.entry.snapshot()
				return !f.Lost && !f.Reconnecting && f.Pod == "api-1"
			})
			if rig.dials.Load() == 0 {
				t.Fatal("recovered without dialling")
			}
		})
	}
}

func TestStoppingALostForwardEndsItsSupervisor(t *testing.T) {
	rig, _ := newLostForwardRig(t)

	close(rig.dead)
	waitFor(t, func() bool { return rig.entry.snapshot().Lost })

	if err := rig.adapter.StopPortForward("1"); err != nil {
		t.Fatalf("StopPortForward() error = %v", err)
	}
	select {
	case <-rig.entry.done:
	case <-time.After(5 * time.Second):
		t.Fatal("StopPortForward returned with the supervisor still running")
	}
	if n := len(rig.adapter.ListPortForwards()); n != 0 {
		t.Fatalf("%d forwards listed after stop, want 0", n)
	}
}

func TestASupervisorPanicRemovesTheForward(t *testing.T) {
	rig, _ := newLostForwardRig(t)
	rig.adapter.forwards.dial = func(domain.ClusterID, domain.NamespaceName, string, int, int) (bool, int, attempt, error) {
		panic("boom")
	}
	if _, err := rig.client.CoreV1().Pods("web").Create(
		context.Background(), richPod("api-1"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	close(rig.dead)

	select {
	case <-rig.entry.done:
	case <-time.After(5 * time.Second):
		t.Fatal("a panicking supervisor never finished")
	}
	if n := len(rig.adapter.ListPortForwards()); n != 0 {
		t.Fatalf("%d forwards left after the supervisor panicked, want 0", n)
	}
}
