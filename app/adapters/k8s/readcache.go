package k8s

import (
	"fmt"
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
func cachedRead[T any](cache *readCache, key string, fetch func() (T, error)) (T, error) {
	var zero T

	cache.mu.Lock()
	if cache.entries == nil {
		cache.entries = make(map[string]*readEntry)
	}

	if entry, found := cache.entries[key]; found {
		// In flight, or finished recently enough to reuse.
		if entry.at.IsZero() || time.Since(entry.at) < readTTL {
			cache.mu.Unlock()
			<-entry.done
			return result[T](entry)
		}
		delete(cache.entries, key)
	}

	entry := &readEntry{done: make(chan struct{})}
	cache.entries[key] = entry
	cache.mu.Unlock()

	entry.value, entry.err = fetch()

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
