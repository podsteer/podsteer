package k8s

import (
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/podsteer/podsteer/app/domain"
)

// newForwardTestAdapter returns an adapter with a live watch manager and a
// forward registry, so the resurrection path a supervisor takes is actually
// reachable rather than stubbed out.
func newForwardTestAdapter(t *testing.T, id domain.ClusterID, client kubernetes.Interface) *Adapter {
	t.Helper()

	logger := slog.New(slog.DiscardHandler)
	factory := newClientFactory(Config{})
	factory.logger = logger
	factory.clients[id] = &clients{typed: client}

	adapter := &Adapter{
		factory:    factory,
		logger:     logger,
		watches:    newWatchManager(true, logger, idleAfter, sweepEvery, recheckEvery),
		forwards:   portForwards{byID: make(map[string]*forwarder)},
		nodeShells: nodeShells{byID: make(map[string]domain.NodeShell)},
	}
	t.Cleanup(adapter.watches.stopAll)
	return adapter
}

// watchedClusters reports how many clusters the manager currently holds a
// watch set for.
func watchedClusters(manager *watchManager) int {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return len(manager.sets)
}

// TestInvalidateStopsAForwardBeforeItCanResurrectTheCluster is the
// disconnect-resurrection race.
//
// Closing a tab disconnects the cluster but stopped no forward: the forward's
// stream survives invalidation because it dialled its own transport. So when
// its pod later dies, superviseForward calls findReplacementPod every three
// seconds for two minutes — and that goes through the EXPORTED ListPods,
// which on an unregistered cluster REBUILDS the client (re-executing the
// operator's credential plugin), ENSURES a watch set of three reflectors, and
// repopulates the read cache. Precisely the resurrection Invalidate's
// ordering comment exists to prevent, arriving through a different door.
//
// The supervisor is stood in for by a loop making the same call it makes, so
// the test needs no real API server to port-forward against; what is asserted
// is the door itself, which is findReplacementPod reaching ListPods.
func TestInvalidateStopsAForwardBeforeItCanResurrectTheCluster(t *testing.T) {
	pollingLists(t)

	client, lists := watchedClient(t, richPod("api-1"))
	adapter := newForwardTestAdapter(t, "dev", client)

	forward := domain.Forward{
		ID:         "1",
		ClusterID:  "dev",
		Namespace:  domain.NamespaceName("web"),
		Pod:        "api-0",
		LocalPort:  18080,
		RemotePort: 8080,
		Selector:   map[string]string{"app": "web"},
	}
	entry := &forwarder{
		forward: forward,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}

	adapter.forwards.mu.Lock()
	adapter.forwards.byID[forward.ID] = entry
	adapter.forwards.mu.Unlock()

	// The reconnect loop, reduced to the one call that reaches the cluster.
	// It ends only when entry.stop closes, which is the whole point: nothing
	// else in the process tells it the cluster has gone.
	var searches atomic.Int64
	go func() {
		defer close(entry.done)
		for {
			select {
			case <-entry.stop:
				return
			default:
			}
			_, _ = adapter.findReplacementPod(entry, entry.snapshot())
			searches.Add(1)
		}
	}()

	// It genuinely reaches the cluster: this is a forward keeping a
	// disconnected cluster's API server in conversation on its own.
	waitFor(t, func() bool { return searches.Load() > 0 && lists.Load() > 0 })

	adapter.Invalidate("dev")

	// Invalidate waits, so by the time it returns the goroutine is finished
	// and its record is gone. Both halves matter: a record without its
	// goroutine is a forward nothing can stop, and a goroutine without its
	// record is one nothing knows about.
	select {
	case <-entry.done:
	case <-time.After(10 * time.Second):
		t.Fatal("Invalidate() returned while the forward's goroutine was still running")
	}

	adapter.forwards.mu.Lock()
	remaining := len(adapter.forwards.byID)
	adapter.forwards.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("forwards left registered after Invalidate = %d, want 0", remaining)
	}

	// AND NOTHING REACHES THE CLUSTER AFTERWARDS. Not one more list, and no
	// watch — the two halves of the resurrection.
	settled, listed := searches.Load(), lists.Load()
	// Longer than readTTL, so a detached read-cache fetch would have shown up
	// by now if this path could still start one.
	time.Sleep(3 * readTTL)

	if got := searches.Load(); got != settled {
		t.Fatalf("the supervisor made %d more searches after Invalidate, want 0", got-settled)
	}
	if got := lists.Load(); got != listed {
		t.Fatalf("the cluster was listed %d more times after Invalidate, want 0", got-listed)
	}
	if held := watchedClusters(adapter.watches); held != 0 {
		t.Fatalf("watch sets after Invalidate = %d, want 0 — the cluster was resurrected", held)
	}
}

// TestFindReplacementPodNeverEnsuresAWatch pins the defence in depth beside
// the fix above.
//
// The exported ListPods ensures a watch set and hands its fetch to the read
// cache, which DETACHES it from the caller — so a search already in flight
// when a disconnect lands outlives its own cancellation and can rebuild the
// client Invalidate just discarded. The reconnect search therefore goes
// straight to the narrow list: no watch is ever ensured from this path, so a
// forward cannot start one however the timing falls.
func TestFindReplacementPodNeverEnsuresAWatch(t *testing.T) {
	pollingLists(t)

	client, lists := watchedClient(t, richPod("api-1"))
	adapter := newForwardTestAdapter(t, "dev", client)

	entry := &forwarder{
		forward: domain.Forward{
			ID:        "1",
			ClusterID: "dev",
			Namespace: domain.NamespaceName("web"),
			Pod:       "api-0",
			Selector:  map[string]string{"app": "web"},
		},
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}

	replacement, err := adapter.findReplacementPod(entry, entry.snapshot())
	if err != nil {
		t.Fatalf("findReplacementPod() error = %v", err)
	}
	if replacement != "api-1" {
		t.Fatalf("findReplacementPod() = %q, want api-1 — the search must still work", replacement)
	}
	if lists.Load() == 0 {
		t.Fatal("the search never reached the cluster")
	}

	if held := watchedClusters(adapter.watches); held != 0 {
		t.Fatalf("watch sets after a replacement search = %d, want 0 — a forward must not start a watch", held)
	}
}

// TestStopPortForwardsForLeavesOtherClustersAlone is the other half of the
// rule: disconnecting one tab must not stop a forward belonging to another.
func TestStopPortForwardsForLeavesOtherClustersAlone(t *testing.T) {
	adapter := &Adapter{
		logger:   slog.New(slog.DiscardHandler),
		factory:  newClientFactory(Config{}),
		watches:  newWatchManager(false, slog.New(slog.DiscardHandler), idleAfter, sweepEvery, recheckEvery),
		forwards: portForwards{byID: make(map[string]*forwarder)},
	}

	entries := map[string]*forwarder{}
	for id, cluster := range map[string]domain.ClusterID{"1": "dev", "2": "prod"} {
		entry := &forwarder{
			forward: domain.Forward{ID: id, ClusterID: cluster},
			stop:    make(chan struct{}),
			done:    make(chan struct{}),
		}
		go func() {
			<-entry.stop
			close(entry.done)
		}()
		entries[id] = entry
		adapter.forwards.byID[id] = entry
	}

	adapter.stopPortForwardsFor("dev")

	select {
	case <-entries["1"].done:
	case <-time.After(10 * time.Second):
		t.Fatal("the disconnected cluster's forward was not stopped")
	}

	select {
	case <-entries["2"].done:
		t.Fatal("another cluster's forward was stopped by a disconnect that was not its own")
	default:
	}

	live := adapter.ListPortForwards()
	if len(live) != 1 || live[0].ClusterID != "prod" {
		t.Fatalf("ListPortForwards() = %+v, want only the still-connected cluster's forward", live)
	}

	adapter.StopAllPortForwards()
}
