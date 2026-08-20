package domain

import (
	"sort"
	"time"
)

// This file models what K8Sense remembers about a cluster over time.
//
// The metrics API reports only the present: ask it twice and you get two
// unrelated instants, never a trend. Everything an operator actually wants to
// know — is memory climbing, did requests jump when that deployment rolled,
// was the cluster this full an hour ago — needs a series, and nothing in
// Kubernetes keeps one unless a monitoring stack was installed.
//
// So K8Sense keeps its own, sampled while the application is running. The
// honesty of that is the point: it covers the window the app was open, and the
// UI must say so rather than implying a complete history it does not have.

// Sample is one measurement of a whole cluster.
//
// Deliberately flat and small: this is written every sampling interval for
// every open cluster and kept for days, so each field earns its bytes. Only
// figures a chart would plot are here — findings and per-object detail are
// re-derived live, never stored.
type Sample struct {
	// At is when the sample was taken, in UTC.
	At time.Time
	// CPU is in millicores, memory in bytes, matching the rest of the domain.
	CPUUsageMilli       int64
	CPURequestsMilli    int64
	CPUAllocatableMilli int64
	MemoryUsageBytes    int64
	MemoryRequestsBytes int64
	MemoryAllocBytes    int64
	// PodsScheduled is how many pods occupy nodes; PodsNotReady how many of
	// those are not doing their job.
	PodsScheduled int
	PodsNotReady  int
	NodesReady    int
	NodesTotal    int
	// Measured reports whether the usage figures came from a metrics API.
	// Without it the requests and capacity lines are still real; the usage
	// line simply does not exist, and a chart must leave a gap rather than
	// drawing zero.
	Measured bool
}

// IsZero reports whether the sample holds no measurement at all.
func (s Sample) IsZero() bool { return s.At.IsZero() }

// NewSampleFromOverview reduces a full assessment to the handful of numbers
// worth keeping.
//
// Taking it from the overview rather than re-reading the cluster is what makes
// the chart and the dashboard agree: both are the same computation, so a
// number on the chart is never a differently-derived version of the number
// printed above it.
func NewSampleFromOverview(overview Overview) Sample {
	capacity := overview.Capacity
	return Sample{
		At:                  overview.GeneratedAt.UTC(),
		CPUUsageMilli:       capacity.CPU.PodUsage,
		CPURequestsMilli:    capacity.CPU.Requests,
		CPUAllocatableMilli: capacity.CPU.Allocatable,
		MemoryUsageBytes:    capacity.Memory.PodUsage,
		MemoryRequestsBytes: capacity.Memory.Requests,
		MemoryAllocBytes:    capacity.Memory.Allocatable,
		PodsScheduled:       capacity.Pods.Scheduled,
		PodsNotReady:        overview.Pods.NotReady,
		NodesReady:          overview.Nodes.Ready,
		NodesTotal:          overview.Nodes.Total,
		Measured:            capacity.CPU.Measured,
	}
}

// Sampling cadence bounds.
//
// The floor is not arbitrary: every sample costs a full cluster assessment —
// nodes, pods, controllers, events and metrics — so a cadence faster than this
// puts real load on the API server for a chart nobody can read at that
// resolution. The ceiling exists because a sample every twenty minutes is not
// a trend, it is four points a day.
const (
	MinSamplingInterval     = 10 * time.Second
	MaxSamplingInterval     = 15 * time.Minute
	DefaultSamplingInterval = 30 * time.Second
)

// NewSamplingInterval clamps a cadence into the supported range, falling back
// to the default when nothing sensible was asked for.
func NewSamplingInterval(interval time.Duration) time.Duration {
	switch {
	case interval <= 0:
		return DefaultSamplingInterval
	case interval < MinSamplingInterval:
		return MinSamplingInterval
	case interval > MaxSamplingInterval:
		return MaxSamplingInterval
	default:
		return interval
	}
}

// Retention is how long K8Sense keeps samples on disk.
//
// Zero means "record nothing", which is a real choice rather than a disabled
// state: a cluster's capacity profile is commercially sensitive on some sites,
// and an operator must be able to say that K8Sense writes none of it down.
type Retention struct {
	Days int
}

// RetentionOptions are the retentions offered, in the order shown.
var RetentionOptions = []Retention{{Days: 0}, {Days: 1}, {Days: 7}, {Days: 30}}

// MaxRetentionDays caps what may be configured, so a mistyped value cannot
// fill a disk with samples nobody will read.
const MaxRetentionDays = 90

// NewRetention clamps days into the supported range.
func NewRetention(days int) Retention {
	switch {
	case days <= 0:
		return Retention{Days: 0}
	case days > MaxRetentionDays:
		return Retention{Days: MaxRetentionDays}
	default:
		return Retention{Days: days}
	}
}

// Enabled reports whether anything is recorded at all.
func (r Retention) Enabled() bool { return r.Days > 0 }

// Window returns the duration samples are kept for.
func (r Retention) Window() time.Duration {
	return time.Duration(r.Days) * 24 * time.Hour
}

// Cutoff returns the instant before which samples should be discarded.
func (r Retention) Cutoff(now time.Time) time.Time {
	return now.UTC().Add(-r.Window())
}

// Series is a cluster's samples, oldest first.
type Series []Sample

// Sort orders the series oldest first, which every consumer assumes.
func (s Series) Sort() {
	sort.SliceStable(s, func(i, j int) bool { return s[i].At.Before(s[j].At) })
}

// Since returns the samples taken at or after the given instant.
//
// Assumes the series is sorted, which Sort guarantees and the store maintains
// by appending in order.
func (s Series) Since(cutoff time.Time) Series {
	for i, sample := range s {
		if !sample.At.Before(cutoff) {
			return s[i:]
		}
	}
	return nil
}

// Span returns the period the series covers, which is what lets the UI say
// "the last 40 minutes" instead of implying it has everything.
func (s Series) Span() time.Duration {
	if len(s) < 2 {
		return 0
	}
	return s[len(s)-1].At.Sub(s[0].At)
}

// Downsample reduces a series to at most limit points by averaging adjacent
// buckets.
//
// A week at thirty-second intervals is twenty thousand points; no chart can
// draw that usefully and no IPC call should carry it. Averaging rather than
// dropping keeps a spike visible as a bump instead of deleting it entirely
// whenever it falls between the samples that survived.
func (s Series) Downsample(limit int) Series {
	if limit <= 0 || len(s) <= limit {
		return s
	}

	buckets := make(Series, 0, limit)
	size := float64(len(s)) / float64(limit)

	for i := range limit {
		start := int(float64(i) * size)
		end := int(float64(i+1) * size)
		if end > len(s) {
			end = len(s)
		}
		if start >= end {
			continue
		}
		buckets = append(buckets, average(s[start:end]))
	}
	return buckets
}

// average reduces a run of samples to their mean, keeping the last sample's
// timestamp so the series still ends at the moment it really ends.
func average(run Series) Sample {
	var total Sample
	measured := 0

	for _, sample := range run {
		total.CPURequestsMilli += sample.CPURequestsMilli
		total.CPUAllocatableMilli += sample.CPUAllocatableMilli
		total.MemoryRequestsBytes += sample.MemoryRequestsBytes
		total.MemoryAllocBytes += sample.MemoryAllocBytes
		total.PodsScheduled += sample.PodsScheduled
		total.PodsNotReady += sample.PodsNotReady
		total.NodesReady += sample.NodesReady
		total.NodesTotal += sample.NodesTotal

		// Usage is averaged only over the samples that carried a measurement,
		// so a gap in metrics-server does not drag the line towards zero.
		if sample.Measured {
			total.CPUUsageMilli += sample.CPUUsageMilli
			total.MemoryUsageBytes += sample.MemoryUsageBytes
			measured++
		}
	}

	count := int64(len(run))
	result := Sample{
		At:                  run[len(run)-1].At,
		CPURequestsMilli:    total.CPURequestsMilli / count,
		CPUAllocatableMilli: total.CPUAllocatableMilli / count,
		MemoryRequestsBytes: total.MemoryRequestsBytes / count,
		MemoryAllocBytes:    total.MemoryAllocBytes / count,
		PodsScheduled:       total.PodsScheduled / len(run),
		PodsNotReady:        total.PodsNotReady / len(run),
		NodesReady:          total.NodesReady / len(run),
		NodesTotal:          total.NodesTotal / len(run),
		Measured:            measured > 0,
	}
	if measured > 0 {
		result.CPUUsageMilli = total.CPUUsageMilli / int64(measured)
		result.MemoryUsageBytes = total.MemoryUsageBytes / int64(measured)
	}
	return result
}
