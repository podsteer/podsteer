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

// PodUsage is one pod's measured consumption, and its containers'.
//
// The total was all that ever left the adapter, which threw the breakdown
// away immediately after computing it — so a pod at 90% of its memory limit
// gave no clue WHICH of its three containers was responsible, and the answer
// had already been in memory and been discarded.
//
// Containers is keyed by container name and may be absent for a container the
// metrics API did not report, which is normal for one that has not started.
type PodUsage struct {
	Total      Metrics
	Containers map[string]Metrics
}

// Filesystem is how full one of a node's disks is.
//
// This is the number no other part of the Kubernetes API carries. Capacity and
// allocatable describe what the SCHEDULER may hand out; neither says how much
// of the disk is actually occupied, and the DiskPressure condition is a
// latch that only closes once the kubelet has already started evicting. A
// filesystem at 82% is invisible everywhere else and is the moment somebody
// can still act on it cheaply.
type Filesystem struct {
	// UsedBytes is what is occupied right now.
	UsedBytes int64
	// CapacityBytes is the size of the filesystem.
	CapacityBytes int64
}

// Percent returns how full the filesystem is, or 0 when its size is unknown.
func (f Filesystem) Percent() float64 {
	if f.CapacityBytes <= 0 {
		return 0
	}
	return float64(f.UsedBytes) / float64(f.CapacityBytes) * 100
}

// IsZero reports whether nothing was reported.
func (f Filesystem) IsZero() bool { return f == Filesystem{} }

// NodeFilesystems is what a kubelet reports about its own disks.
//
// Two of them, because they fill for different reasons and are cleared in
// different ways. Nodefs is where volumes, container writable layers and logs
// live; imagefs holds pulled images and is reclaimed by garbage collection.
// On most clusters they are the same underlying disk, and on some they are
// not — which is why the kubelet reports them separately and why guessing
// would be wrong.
type NodeFilesystems struct {
	// Pressure rides along because it comes from the same kubelet response.
	// Structurally it has nothing to do with filesystems; practically,
	// splitting them would mean asking every kubelet twice for one payload.
	Pressure Pressure
	// Nodefs is the kubelet's working filesystem.
	Nodefs Filesystem
	// Imagefs holds container images. Equal to Nodefs when they share a disk.
	Imagefs Filesystem
	// Measured distinguishes a node that reported zero from one that was
	// never asked, or that refused to answer.
	Measured bool
}

// Fullest returns whichever filesystem is closest to full, which is the one
// that decides whether the kubelet starts evicting.
func (n NodeFilesystems) Fullest() Filesystem {
	if n.Imagefs.Percent() > n.Nodefs.Percent() {
		return n.Imagefs
	}
	return n.Nodefs
}

// Pressure is how much time a node spent with work STALLED, waiting for a
// resource rather than using it.
//
// The signal utilisation cannot give you. A node at 100% CPU may be running
// exactly as intended; a node where tasks spend a fifth of their time waiting
// for CPU is a node where everything is slow, and those two look identical on
// a utilisation gauge. Brendan Gregg's USE method makes the same point: any
// degree of saturation is a problem, while utilisation has to be interpreted.
//
// From the kernel's pressure stall information, which Kubernetes exposes
// through the kubelet summary from 1.36. Values are the proportion of the
// last ten seconds during which at least one task was stalled, 0 to 100.
//
// SOME, not FULL. "Some" means at least one task was waiting, which is the
// early signal; "full" means every task was, which is a node already in
// trouble by every other measure too.
type Pressure struct {
	CPU    float64
	Memory float64
	IO     float64
	// Measured distinguishes a node reporting no stall from a kernel or
	// Kubernetes version that does not report stall at all. Most clusters
	// will be the second for a while yet.
	Measured bool
}

// Worst returns the highest of the three, which is the one worth a glance.
func (p Pressure) Worst() float64 {
	worst := p.CPU
	if p.Memory > worst {
		worst = p.Memory
	}
	if p.IO > worst {
		worst = p.IO
	}
	return worst
}

// Capacity is a node's resource capacity or allocatable amount.
type Capacity struct {
	// CPUMilli is total CPU in millicores.
	CPUMilli int64
	// MemoryBytes is total memory in bytes.
	MemoryBytes int64
	// Pods is the maximum number of pods the node accepts.
	Pods int64
	// EphemeralBytes is the node's scratch disk, as the kubelet reports it.
	//
	// Not the root filesystem's size: the allocatable figure has the
	// kubelet's own reservation and the eviction threshold already taken off
	// it, which is the number the scheduler actually works with.
	EphemeralBytes int64
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
