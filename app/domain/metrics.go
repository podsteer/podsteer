package domain

// Metrics is a point-in-time resource measurement.
//
// Units are chosen to match how Kubernetes itself quantifies things — CPU in
// millicores, memory in bytes — so no precision is lost between the API server
// and the display. Formatting into "0.090" and "49.7MiB" is a presentation
// concern and happens at the adapter boundary.
//
// The zero value means "not measured", which is different from "measured as
// zero": a cluster without metrics-server has no measurements at all, and the
// UI must show a dash rather than a misleading 0.
type Metrics struct {
	// CPUMilli is CPU usage in millicores. 1000 = one core.
	CPUMilli int64
	// MemoryBytes is working-set memory in bytes.
	MemoryBytes int64
	// Measured distinguishes a real zero from an absent measurement.
	Measured bool
}

// NewMetrics returns a measured Metrics value.
func NewMetrics(cpuMilli, memoryBytes int64) Metrics {
	return Metrics{CPUMilli: cpuMilli, MemoryBytes: memoryBytes, Measured: true}
}

// IsZero reports whether nothing was measured.
func (m Metrics) IsZero() bool { return !m.Measured }

// Add returns the sum of two measurements.
//
// The result is measured if either operand was, so summing a pod's containers
// where only some reported still yields a usable total rather than collapsing
// to "unmeasured".
func (m Metrics) Add(other Metrics) Metrics {
	if !m.Measured && !other.Measured {
		return Metrics{}
	}
	return Metrics{
		CPUMilli:    m.CPUMilli + other.CPUMilli,
		MemoryBytes: m.MemoryBytes + other.MemoryBytes,
		Measured:    true,
	}
}

// Capacity is a node's resource capacity or allocatable amount.
type Capacity struct {
	// CPUMilli is total CPU in millicores.
	CPUMilli int64
	// MemoryBytes is total memory in bytes.
	MemoryBytes int64
	// Pods is the maximum number of pods the node accepts.
	Pods int64
}

// IsZero reports whether the capacity is unset.
func (c Capacity) IsZero() bool { return c == Capacity{} }

// CPUPercent returns usage as a percentage of this capacity, or 0 when the
// capacity is unknown. Guarding the division matters: a node that has not yet
// reported capacity would otherwise panic the whole list.
func (c Capacity) CPUPercent(usage Metrics) float64 {
	if c.CPUMilli <= 0 || !usage.Measured {
		return 0
	}
	return float64(usage.CPUMilli) / float64(c.CPUMilli) * 100
}

// MemoryPercent returns usage as a percentage of this capacity.
func (c Capacity) MemoryPercent(usage Metrics) float64 {
	if c.MemoryBytes <= 0 || !usage.Measured {
		return 0
	}
	return float64(usage.MemoryBytes) / float64(c.MemoryBytes) * 100
}
