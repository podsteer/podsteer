package k8s

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// The polling reads a UI makes, coalesced.
//
// THE PROBLEM THIS SOLVES IS A BURST, NOT A RATE. Every refresh fires the
// assessment and the open list at the same instant, and they ask for
// overlapping things: on the namespace list, both want every pod in the
// cluster; on a controller list, both want that kind and the namespace's
// metrics. Two identical requests leaving together is not a caching problem —
// it is the same request twice — and on a five-thousand-pod cluster it is
// several megabytes twice.
//
// So this is a singleflight first and a cache second. Identical reads in
// flight share one answer; a read repeated within a couple of seconds reuses
// the last. The window is deliberately SHORTER THAN THE FASTEST REFRESH the
// application offers, so it can never serve one tick's data to the next — it
// only collapses the pile-up inside a single tick.
//
// Not for everything. It wraps the whole-collection lists a poll repeats;
// anything narrowed to one object, and anything read on demand, goes straight
// through. The other caches here are the opposite trade — see filesystemCache
// and backendCache, which hold answers for a minute and a day because the
// questions move that slowly.
const readTTL = 2 * time.Second

type readCache struct {
	mu      sync.Mutex
	entries map[string]*readEntry
}

type readEntry struct {
	// done is closed when the fetch finishes. Waiters block on it rather than
	// on the mutex, so one slow list does not hold up an unrelated one.
	done chan struct{}
	// at is when it finished, and is zero while it is still running — which
	// is how a waiter tells "someone is fetching this" from "someone fetched
	// it a while ago".
	at    time.Time
	value any
	err   error
}

// cachedRead returns the shared result for key, fetching it if nobody else is.
//
// A package function rather than a method because Go has no generic methods,
// and the alternative — storing `any` and asserting at every call site — is
// exactly the kind of type hole that shows up as a nil list on someone's
// screen rather than as a compile error.
func cachedRead[T any](
	cache *readCache,
	ctx context.Context,
	key string,
	fetch func(context.Context) (T, error),
) (T, error) {
	var zero T

	cache.mu.Lock()
	if cache.entries == nil {
		cache.entries = make(map[string]*readEntry)
	}

	if entry, found := cache.entries[key]; found {
		// In flight, or finished recently enough to reuse.
		if entry.at.IsZero() || time.Since(entry.at) < readTTL {
			cache.mu.Unlock()
			if err := wait(ctx, entry.done); err != nil {
				return zero, err
			}
			return result[T](entry)
		}
		delete(cache.entries, key)
	}

	entry := &readEntry{done: make(chan struct{})}
	cache.entries[key] = entry
	cache.mu.Unlock()

	fetchCtx, release := detach(ctx)
	entry.value, entry.err = fetch(fetchCtx)
	release()

	cache.mu.Lock()
	entry.at = time.Now()
	if entry.err != nil {
		// A FAILURE IS NOT WORTH REUSING. Handing the same error to every
		// caller for two seconds turns one refused read into a pane that
		// stays broken after the permission is granted, and the retry costs
		// nothing when the answer was never received.
		if cache.entries[key] == entry {
			delete(cache.entries, key)
		}
	}
	cache.mu.Unlock()
	close(entry.done)

	if entry.err != nil {
		return zero, entry.err
	}
	return result[T](entry)
}

// cachedSlice is cachedRead for a list, handing every caller its own slice.
//
// THE COPY IS NOT OPTIONAL. Four use cases sort what they are given, in
// place, and the whole point of this file is that two of them are now holding
// the same read. Sharing the backing array turns "the assessment and the open
// list both wanted the pods" into two goroutines permuting one array — a data
// race, and the kind that shows up as rows in the wrong order rather than as
// a crash.
//
// Shallow, deliberately: sorting permutes the outer slice and nothing reaches
// into the elements, so cloning what they point at would be work for nobody.
func cachedSlice[T any](
	cache *readCache,
	ctx context.Context,
	key string,
	fetch func(context.Context) ([]T, error),
) ([]T, error) {
	shared, err := cachedRead(cache, ctx, key, fetch)
	if err != nil {
		return nil, err
	}
	return slices.Clone(shared), nil
}

// borrow reuses a read that is ALREADY fresh or already under way, and
// reports whether there was one.
//
// For the case where one read contains another: a namespace's pods are a
// subset of the cluster's, and the assessment reads the cluster's on every
// refresh whatever is on screen. Rather than issue a second, narrower request
// beside it, the narrower caller waits for the one already in flight and
// filters it.
//
// It never STARTS anything. If the broader read is absent, stale or failed —
// an account that may list one namespace and not the cluster is the case that
// matters — this reports false and the caller does its own read.
func borrow[T any](ctx context.Context, cache *readCache, key string) (T, bool) {
	var zero T

	cache.mu.Lock()
	entry, found := cache.entries[key]
	if !found || (!entry.at.IsZero() && time.Since(entry.at) >= readTTL) {
		cache.mu.Unlock()
		return zero, false
	}
	cache.mu.Unlock()

	if err := wait(ctx, entry.done); err != nil {
		return zero, false
	}
	if entry.err != nil {
		return zero, false
	}

	value, ok := entry.value.(T)
	return value, ok
}

// detach separates a shared fetch from the caller that happened to start it.
//
// THE FIRST CALLER IS NOT THE ONLY CALLER, and forgetting that made one
// pane's refusal break another's. The reads here are coalesced: whoever
// arrives first runs the fetch and everyone else waits on it. If that fetch
// runs on the first caller's context, the first caller's cancellation kills
// an answer several other callers are waiting for.
//
// It is not hypothetical. ListNamespaceSummaries runs the namespace list and
// the cluster-wide pod list under one errgroup. On an account without
// `list namespaces` the first 403s immediately, the group cancels, and the
// pod list — which that account IS permitted — dies with it. Every other
// consumer coalesced onto the same key that tick gets context.Canceled for a
// read nothing refused, tick after tick, and the cause is a different pane.
//
// A DEADLINE IS KEPT AND CANCELLATION IS NOT. The deadline is a statement
// about how long the answer is worth waiting for, which is as true for the
// second caller as the first; cancellation is a statement about one caller
// having lost interest, which is not.
func detach(ctx context.Context) (context.Context, context.CancelFunc) {
	free := context.WithoutCancel(ctx)
	deadline, ok := ctx.Deadline()
	if !ok {
		return context.WithCancel(free)
	}
	return context.WithDeadline(free, deadline)
}

// wait blocks for a shared fetch, or for the caller to give up on it.
//
// A waiter must be able to leave. It does not own the fetch — it joined one
// already running — so a fetch wedged behind an unresponsive API server must
// not pin every goroutine that asked for the same thing.
func wait(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func result[T any](entry *readEntry) (T, error) {
	var zero T
	if entry.err != nil {
		return zero, entry.err
	}

	value, ok := entry.value.(T)
	if !ok {
		// Two different types behind one key, which is a programming error in
		// how a key was built rather than anything a cluster did.
		return zero, fmt.Errorf("read cache: %T held under a key expecting %T", entry.value, zero)
	}
	return value, nil
}

// forget drops everything held for one cluster.
//
// Called on disconnect and AFTER EVERY WRITE. The second is the one that
// matters: deleting a pod and then being handed the list that still contains
// it, because it was read a second ago, is the application appearing to
// ignore what it was just told to do.
func (c *readCache) forget(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	prefix := id + "|"
	for key := range c.entries {
		if strings.HasPrefix(key, prefix) {
			delete(c.entries, key)
		}
	}
}

// readKey builds a cache key. The cluster comes first so one can be dropped
// by prefix.
func readKey(id string, parts ...string) string {
	return id + "|" + strings.Join(parts, "|")
}
