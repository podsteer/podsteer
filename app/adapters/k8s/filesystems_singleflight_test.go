package k8s

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// The sweep is a kubelet request per node, so the property that matters is
// how many sweeps run, not what one returns. Each test drives the cache with
// a sweep that counts itself and blocks until released, so "concurrent" is
// guaranteed rather than hoped for.

func TestFilesystemSweepIsSharedByConcurrentCallers(t *testing.T) {
	t.Parallel()

	var (
		cache   filesystemCache
		sweeps  atomic.Int32
		release = make(chan struct{})
		want    = map[string]domain.NodeFilesystems{"worker-1": {Measured: true}}
	)
	sweep := func(context.Context, filesystemEntry) sweepOutcome {
		sweeps.Add(1)
		<-release
		return stored(want)
	}

	const callers = 50
	var (
		wg      sync.WaitGroup
		started sync.WaitGroup
		errs    = make([]error, callers)
		got     = make([]map[string]domain.NodeFilesystems, callers)
	)
	started.Add(callers)
	for i := range callers {
		wg.Go(func() {
			started.Done()
			got[i], errs[i] = cache.do(t.Context(), "prod", sweep)
		})
	}
	started.Wait()
	// Every caller is running; give them a moment to reach the claim before
	// the sweep is let go, so the late ones cannot find the cache warm.
	waitFor(t, func() bool { return sweeps.Load() == 1 && inflight(&cache, "prod") })
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()

	if n := sweeps.Load(); n != 1 {
		t.Fatalf("%d concurrent callers ran %d sweeps, want 1", callers, n)
	}
	for i := range callers {
		if errs[i] != nil || len(got[i]) != 1 {
			t.Fatalf("caller %d got (%v, %v), want the shared answer", i, got[i], errs[i])
		}
	}

	// And the answer is held: a caller arriving afterwards sweeps nothing.
	if _, err := cache.do(t.Context(), "prod", sweep); err != nil {
		t.Fatalf("cached read error = %v", err)
	}
	if n := sweeps.Load(); n != 1 {
		t.Fatalf("a read inside the TTL swept again: %d sweeps", n)
	}
}

func TestFilesystemSweepSurvivesItsLeaderGivingUp(t *testing.T) {
	t.Parallel()

	var (
		cache   filesystemCache
		release = make(chan struct{})
		sweepOK atomic.Bool
	)
	sweep := func(ctx context.Context, _ filesystemEntry) sweepOutcome {
		<-release
		// The leader's cancellation must not have reached the sweep.
		sweepOK.Store(ctx.Err() == nil)
		return stored(map[string]domain.NodeFilesystems{})
	}

	leaderCtx, cancelLeader := context.WithCancel(t.Context())
	leaderDone := make(chan error, 1)
	go func() {
		_, err := cache.do(leaderCtx, "prod", sweep)
		leaderDone <- err
	}()
	waitFor(t, func() bool { return inflight(&cache, "prod") })

	followerDone := make(chan error, 1)
	go func() {
		_, err := cache.do(t.Context(), "prod", sweep)
		followerDone <- err
	}()

	cancelLeader()
	if err := <-leaderDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("leader error = %v, want it to leave on its own cancellation", err)
	}

	close(release)
	if err := <-followerDone; err != nil {
		t.Fatalf("follower error = %v, want the sweep's answer", err)
	}
	if !sweepOK.Load() {
		t.Fatal("the sweep ran on a cancelled context: the leader's cancellation became everyone's")
	}
}

func TestFilesystemSweepTransientFailureIsNotHeld(t *testing.T) {
	t.Parallel()

	var (
		cache  filesystemCache
		sweeps atomic.Int32
	)
	sweep := func(context.Context, filesystemEntry) sweepOutcome {
		sweeps.Add(1)
		return sweepOutcome{err: errors.New("node list failed")}
	}

	for range 2 {
		if _, err := cache.do(t.Context(), "prod", sweep); err == nil {
			t.Fatal("do() error = nil, want the sweep's failure")
		}
	}
	if n := sweeps.Load(); n != 2 {
		t.Fatalf("a transient failure was reused: %d sweeps for 2 reads, want 2", n)
	}
}

func TestFilesystemSweepOvertakenByDisconnectIsNotStored(t *testing.T) {
	t.Parallel()

	var (
		cache   filesystemCache
		release = make(chan struct{})
	)
	stale := func(context.Context, filesystemEntry) sweepOutcome {
		<-release
		return stored(map[string]domain.NodeFilesystems{"old": {Measured: true}})
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = cache.do(t.Context(), "prod", stale)
	}()
	waitFor(t, func() bool { return inflight(&cache, "prod") })

	cache.forget("prod")
	close(release)
	<-done

	if _, ok := cache.entries["prod"]; ok {
		t.Fatal("a sweep that finished after forget() was cached for the reconnected cluster")
	}
}

func inflight(cache *filesystemCache, id domain.ClusterID) bool {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	_, ok := cache.inflight[id]
	return ok
}

// TestFilesystemClaimSeesASweepThatJustFinished pins the one-lock decision:
// a caller that missed the cache a moment before a sweep stored its answer
// must be handed that answer by claim, not made the leader of a second
// sweep. With the cache read and the claim under separate locks, this is
// exactly the window that let a redundant sweep through.
func TestFilesystemClaimSeesASweepThatJustFinished(t *testing.T) {
	t.Parallel()

	var cache filesystemCache
	_, cached, first, leader := cache.claim("prod")
	if cached || !leader {
		t.Fatal("the first caller must lead")
	}
	cache.finish("prod", first, stored(map[string]domain.NodeFilesystems{"worker-1": {Measured: true}}))

	entry, cached, call, leader := cache.claim("prod")
	if !cached || leader || call != nil {
		t.Fatalf("claim after a stored sweep = (cached %v, leader %v), want the stored answer", cached, leader)
	}
	if len(entry.result) != 1 {
		t.Fatalf("entry = %+v, want the sweep's result", entry)
	}
}

// stored is a sweep's answer as the cache keeps it.
func stored(result map[string]domain.NodeFilesystems) sweepOutcome {
	return sweepOutcome{result: result, remember: true, entry: filesystemEntry{at: time.Now(), result: result}}
}
