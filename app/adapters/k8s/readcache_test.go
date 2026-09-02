package k8s

import (
	"errors"
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
			value, err := cachedRead(&cache, "dev|pods|", func() ([]string, error) {
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

	pods, err := cachedRead(&cache, "dev|pods|web", func() ([]string, error) {
		return []string{"pod"}, nil
	})
	if err != nil || len(pods) != 1 {
		t.Fatalf("pods = %v, %v", pods, err)
	}

	other, err := cachedRead(&cache, "dev|pods|staging", func() ([]string, error) {
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
		_, err := cachedRead(&cache, "dev|pods|", func() ([]string, error) {
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
		return cachedRead(&cache, readKey("dev", "pods", ""), func() (string, error) {
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
