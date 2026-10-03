package application

import (
	"sync"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// podUsageKey identifies one pod's series: the cluster is part of it, for the
// reason $stores/usageHistory gives — two clusters routinely hold a pod of
// the same name in a namespace of the same name.
type podUsageKey struct {
	cluster   domain.ClusterID
	namespace string
	name      string
}

// podUsageRing keeps each pod's recent usage from the pod lists Go already
// reads, IN MEMORY ONLY.
//
// WHY IN GO. The webview kept per-pod usage from the list rows (ADR 0004),
// and the pod table now sends it one page, so a pod off the page — the one
// in the drawer, say — had no history to draw. Every pod-list read here
// already carries every pod's usage in the namespace, so keeping it costs
// no request.
//
// NEVER ON DISK, NEVER EXPORTED. A series is keyed by an object name, and
// object names are not among what SECURITY.md says PodSteer writes for an
// operator; the sampled cluster history on disk carries none by design. This
// dies with the process, and a disconnect drops the cluster's share.
type podUsageRing struct {
	mu     sync.Mutex
	series map[podUsageKey][]domain.UsagePoint
	// generation counts each cluster's forgets. A list read in flight across
	// a disconnect or reconnect would otherwise land after forget and file
	// the OLD cluster's pods under the context name it now shares with a
	// new one; record refuses a read that began in an earlier generation.
	generation map[domain.ClusterID]uint64
	// sweptAt is when pods nothing has measured were last dropped.
	sweptAt time.Time
}

// minUsageSpacing collapses reads of one tick into one point: the pod table,
// the merged table and the read cache can hand the same measurement to
// several callers within moments of each other.
const minUsageSpacing = 2 * time.Second

// sweepUsageEvery is how often pods nobody has measured within
// UsageRingMaxAge are dropped. By time rather than by count, so a churning
// namespace's departed pods leave on schedule however quiet the cluster.
const sweepUsageEvery = 5 * time.Minute

// generationOf is the generation a read of id begins in — taken BEFORE the
// read, handed to record after it.
func (r *podUsageRing) generationOf(id domain.ClusterID) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.generation[id]
}

// record files the measured pods of one read of cluster id, begun in
// generation gen.
func (r *podUsageRing) record(id domain.ClusterID, gen uint64, pods []domain.Pod, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.generation[id] != gen {
		return // the cluster was forgotten while this read was in flight
	}
	if r.series == nil {
		r.series = make(map[podUsageKey][]domain.UsagePoint, len(pods))
	}

	for _, pod := range pods {
		usage := pod.Usage()
		if !usage.Measured {
			continue
		}
		key := podUsageKey{cluster: pod.ClusterID(), namespace: pod.Namespace().String(), name: pod.Name()}
		point := domain.UsagePoint{At: now, CPUMilli: usage.CPUMilli, MemoryBytes: usage.MemoryBytes}

		series := r.series[key]
		if n := len(series); n > 0 && now.Sub(series[n-1].At) < minUsageSpacing {
			series[n-1] = point
			continue
		}
		if len(series) >= domain.UsageRingCapacity {
			series = append(series[:0], series[len(series)-domain.UsageRingCapacity+1:]...)
		}
		r.series[key] = append(series, point)
	}

	if now.Sub(r.sweptAt) >= sweepUsageEvery {
		r.sweptAt = now
		for key, series := range r.series {
			if len(series) == 0 || now.Sub(series[len(series)-1].At) > domain.UsageRingMaxAge {
				delete(r.series, key)
			}
		}
	}
}

// since returns a copy of one pod's points no older than UsageRingMaxAge.
func (r *podUsageRing) since(key podUsageKey, now time.Time) []domain.UsagePoint {
	r.mu.Lock()
	defer r.mu.Unlock()

	series := r.series[key]
	cutoff := now.Add(-domain.UsageRingMaxAge)
	out := make([]domain.UsagePoint, 0, len(series))
	for _, point := range series {
		if point.At.After(cutoff) {
			out = append(out, point)
		}
	}
	return out
}

// forget drops one cluster's series.
func (r *podUsageRing) forget(id domain.ClusterID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.generation == nil {
		r.generation = make(map[domain.ClusterID]uint64, 4)
	}
	r.generation[id]++
	for key := range r.series {
		if key.cluster == id {
			delete(r.series, key)
		}
	}
}

// size is how many pods have a series, for tests.
func (r *podUsageRing) size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.series)
}
