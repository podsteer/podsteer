package k8s

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIdenticalReadsInFlightBecomeOneRequest(t *testing.T) {
	// THE CASE THIS EXISTS FOR. Every refresh fires the assessment and the
	// open list at the same instant, and on the namespace page both ask for
	// every pod in the cluster. Two identical requests leaving together is
	// not a caching problem, it is the same request twice — and on a large
	// cluster it is several megabytes twice.
	var cache readCache
	var calls atomic.Int64
	release := make(chan struct{})

	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			value, err := cachedRead(&cache, t.Context(), "dev|pods|", func(context.Context) ([]string, error) {
				calls.Add(1)
				<-release
				return []string{"one"}, nil
			})
			if err != nil {
				t.Errorf("cachedRead() error = %v", err)
			}
			if len(value) != 1 {
				t.Errorf("got %d values, want the shared one", len(value))
			}
		}()
	}

	// Let them all arrive before any can finish.
	waitFor(t, func() bool { return calls.Load() >= 1 })
	time.Sleep(20 * time.Millisecond)
	close(release)
	group.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("made %d requests for 8 identical concurrent reads, want 1", got)
	}
}

func TestDifferentReadsDoNotShareAnAnswer(t *testing.T) {
	var cache readCache

	pods, err := cachedRead(&cache, t.Context(), "dev|pods|web", func(context.Context) ([]string, error) {
		return []string{"pod"}, nil
	})
	if err != nil || len(pods) != 1 {
		t.Fatalf("pods = %v, %v", pods, err)
	}

	other, err := cachedRead(&cache, t.Context(), "dev|pods|staging", func(context.Context) ([]string, error) {
		return []string{"a", "b"}, nil
	})
	if err != nil || len(other) != 2 {
		t.Fatalf("a different namespace was served the first one's answer: %v", other)
	}
}

func TestAFailedReadIsNotHandedToTheNextCaller(t *testing.T) {
	// Reusing a refusal for two seconds turns one denied read into a pane
	// that stays broken after the permission is granted, and the retry costs
	// nothing when the answer was never received.
	var cache readCache
	var calls atomic.Int64
	refused := errors.New("forbidden")

	for range 3 {
		_, err := cachedRead(&cache, t.Context(), "dev|pods|", func(context.Context) ([]string, error) {
			calls.Add(1)
			return nil, refused
		})
		if !errors.Is(err, refused) {
			t.Fatalf("err = %v, want the refusal", err)
		}
	}

	if got := calls.Load(); got != 3 {
		t.Fatalf("made %d requests for 3 failing reads, want each to retry", got)
	}
}

func TestAWriteDropsWhatTheClusterHadCached(t *testing.T) {
	// Deleting a pod and then being handed the list that still contains it,
	// because it was read a second ago, is the application appearing to
	// ignore what it was just told to do.
	var cache readCache
	var calls atomic.Int64

	read := func() (string, error) {
		return cachedRead(&cache, t.Context(), readKey("dev", "pods", ""), func(context.Context) (string, error) {
			calls.Add(1)
			return "listed", nil
		})
	}

	_, _ = read()
	_, _ = read()
	if got := calls.Load(); got != 1 {
		t.Fatalf("a repeat read cost %d requests, want it reused", got)
	}

	cache.forget("other")
	_, _ = read()
	if got := calls.Load(); got != 1 {
		t.Fatalf("forgetting another cluster dropped this one's reads (%d)", got)
	}

	cache.forget("dev")
	_, _ = read()
	if got := calls.Load(); got != 2 {
		t.Fatalf("made %d requests after a write, want the list re-read", got)
	}
}

func TestTheWindowIsShorterThanTheFastestRefresh(t *testing.T) {
	// It exists to collapse the pile-up inside one tick, never to serve one
	// tick's data to the next. The fastest refresh the application offers is
	// five seconds.
	if readTTL >= 5*time.Second {
		t.Fatalf("readTTL = %s, which can outlive a refresh interval", readTTL)
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestEveryCallerGetsItsOwnSliceToSort(t *testing.T) {
	// THE RACE THIS FIXES, and it was shipped. Four use cases sort what they
	// are given, in place, and the whole point of this cache is that two of
	// them are now holding the same read. Sharing the backing array turns
	// "the assessment and the open list both wanted the pods" into two
	// goroutines permuting one array.
	var cache readCache

	fetch := func(context.Context) ([]int, error) { return []int{3, 1, 2}, nil }

	first, err := cachedSlice(&cache, t.Context(), "dev|pods|", fetch)
	if err != nil {
		t.Fatalf("cachedSlice() error = %v", err)
	}
	second, err := cachedSlice(&cache, t.Context(), "dev|pods|", fetch)
	if err != nil {
		t.Fatalf("cachedSlice() error = %v", err)
	}

	slices.Sort(first)
	if !slices.Equal(second, []int{3, 1, 2}) {
		t.Fatalf("sorting one caller's slice reordered another's: %v", second)
	}
}

func TestANarrowerReadWaitsForTheBroaderOneAlreadyRunning(t *testing.T) {
	// A namespace's pods are a subset of the cluster's, and the assessment
	// reads the cluster's on every refresh. Rather than a second, narrower
	// request beside it, the narrow caller waits for the one in flight.
	var cache readCache
	release := make(chan struct{})
	started := make(chan struct{})

	go func() {
		_, _ = cachedRead(&cache, t.Context(), readKey("dev", "pods", ""), func(context.Context) ([]string, error) {
			close(started)
			<-release
			return []string{"web/api", "kube-system/dns"}, nil
		})
	}()

	<-started
	borrowed := make(chan bool, 1)
	go func() {
		_, ok := borrow[[]string](t.Context(), &cache, readKey("dev", "pods", ""))
		borrowed <- ok
	}()

	// It must still be waiting, not have given up and gone its own way.
	select {
	case <-borrowed:
		t.Fatal("borrow returned before the read it was waiting on finished")
	case <-time.After(20 * time.Millisecond):
	}

	close(release)
	if !<-borrowed {
		t.Fatal("borrow refused a read it had waited for")
	}
}

func TestNothingIsBorrowedFromAReadThatIsAbsentOrRefused(t *testing.T) {
	// The account that may list one namespace and not the cluster. There is
	// no cluster-wide read to narrow, and inventing one would fail where the
	// narrow request succeeds.
	var cache readCache

	if _, ok := borrow[[]string](t.Context(), &cache, readKey("dev", "pods", "")); ok {
		t.Fatal("borrowed a read nobody had made")
	}

	refused := errors.New("forbidden")
	_, _ = cachedRead(&cache, t.Context(), readKey("dev", "pods", ""), func(context.Context) ([]string, error) {
		return nil, refused
	})

	if _, ok := borrow[[]string](t.Context(), &cache, readKey("dev", "pods", "")); ok {
		t.Fatal("borrowed a read that was refused")
	}
}

func TestCachedReadSurvivesTheStartingCallersCancellation(t *testing.T) {
	// ONE PANE'S REFUSAL MUST NOT BREAK ANOTHER'S. The first caller to ask
	// runs the fetch and everyone else waits on it, so running that fetch on
	// the first caller's context makes one caller's cancellation everybody's.
	//
	// The real shape: ListNamespaceSummaries runs the namespace list and the
	// cluster-wide pod list under one errgroup. On an account without
	// `list namespaces` the first 403s at once, the group cancels, and the
	// pod list that account IS permitted dies with it — so the pod list page
	// shows context.Canceled for a read nothing refused, and the cause is a
	// different pane entirely.
	var cache readCache

	owner, cancelOwner := context.WithCancel(t.Context())
	started := make(chan struct{})
	release := make(chan struct{})

	got := make(chan error, 1)
	go func() {
		_, err := cachedRead(&cache, owner, "dev|pods|", func(ctx context.Context) ([]string, error) {
			close(started)
			<-release
			return []string{"a"}, ctx.Err()
		})
		got <- err
	}()

	<-started
	cancelOwner()
	close(release)

	if err := <-got; err != nil {
		t.Fatalf("the fetch saw its starter's cancellation: %v", err)
	}
}

func TestWaiterLeavesWhenItsOwnCallerGivesUp(t *testing.T) {
	// A waiter does not own the fetch — it joined one already running — so a
	// fetch wedged behind an unresponsive API server must not pin every
	// goroutine that happened to ask for the same thing.
	var cache readCache

	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)

	go func() {
		_, _ = cachedRead(&cache, context.Background(), "dev|pods|", func(context.Context) ([]string, error) {
			close(started)
			<-release
			return []string{"a"}, nil
		})
	}()
	<-started

	waiter, giveUp := context.WithCancel(t.Context())
	go giveUp()

	if _, err := cachedRead(&cache, waiter, "dev|pods|", func(context.Context) ([]string, error) {
		t.Error("the waiter started a second fetch instead of joining the first")
		return nil, nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter did not leave on its own cancellation: %v", err)
	}
}
