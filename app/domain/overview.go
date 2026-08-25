package domain

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// This file turns a snapshot of a cluster into an assessment of it.
//
// Every other list in PodSteer answers "what is there". The overview answers
// "what is wrong, and what should I look at first" — which is the question an
// operator actually opens a cluster with. That makes it analysis, not
// presentation, so it lives in the domain: it is testable without a cluster,
// and the same rules could drive a CLI or an alert without being rewritten.
//
// Two principles run through the rules below:
//
//   - Group before reporting. Twelve crash-looping pods of one deployment are
//     one problem, not twelve. A dashboard that lists them separately is a
//     dashboard nobody can read during an incident.
//   - Say why, and say what to do. A finding that repeats the reason string
//     the API server already printed adds nothing.

// Severity ranks how much a finding deserves attention.
type Severity string

const (
	// SeverityCritical means something is broken now: workloads are not
	// serving, or the cluster cannot place them.
	SeverityCritical Severity = "critical"
	// SeverityWarning means something is degraded or heading for trouble.
	SeverityWarning Severity = "warning"
	// SeverityInfo means something is worth knowing but is not a fault.
	SeverityInfo Severity = "info"
)

// rank orders severities for sorting, highest first.
func (s Severity) rank() int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 0
	}
}

// HealthGrade is the cluster's one-word verdict.
type HealthGrade string

const (
	// HealthHealthy means no critical or warning finding was raised.
	HealthHealthy HealthGrade = "healthy"
	// HealthDegraded means warnings exist but nothing is outright broken.
	HealthDegraded HealthGrade = "degraded"
	// HealthCritical means at least one critical finding was raised.
	HealthCritical HealthGrade = "critical"
)

// FindingCategory groups findings by what an operator would do about them.
type FindingCategory string

const (
	// CategoryFindingWorkload covers pods and the controllers that own them.
	CategoryFindingWorkload FindingCategory = "Workloads"
	// CategoryFindingScheduling covers pods the cluster cannot place.
	CategoryFindingScheduling FindingCategory = "Scheduling"
	// CategoryFindingNode covers node health.
	CategoryFindingNode FindingCategory = "Nodes"
	// CategoryFindingCapacity covers headroom and commitment.
	CategoryFindingCapacity FindingCategory = "Capacity"
	// CategoryFindingConfiguration covers declarations that will hurt later.
	CategoryFindingConfiguration FindingCategory = "Configuration"
	// CategoryFindingStorage covers persistent volumes and the claims on them.
	CategoryFindingStorage FindingCategory = "Storage"
)

// Thresholds for the rules below. They are named constants rather than magic
// numbers because each one encodes a judgement about what is normal churn and
// what is a problem, and those judgements are the part worth arguing over.
const (
	// pendingGrace is how long a pod may sit unscheduled before it is worth
	// reporting. Anything shorter reports every rollout.
	pendingGrace = 2 * time.Minute
	// startingGrace is how long a container may spend pulling an image or
	// mounting volumes before that counts as stuck.
	startingGrace = 5 * time.Minute
	// terminatingGrace is how long a deleting pod may take before its
	// finalizers are suspect. The default termination grace period is 30s.
	terminatingGrace = 5 * time.Minute
	// notReadyGrace is how long a running pod may fail readiness before it
	// stops being a slow start and becomes a failing probe.
	notReadyGrace = 5 * time.Minute
	// restartsOfNote is the restart count at which a pod is worth naming even
	// though it is currently up.
	restartsOfNote = 5
	// commitmentWarning is the fraction of allocatable requested at which
	// scheduling headroom is worth flagging.
	commitmentWarning = 0.90
	// podSlotWarning is the fraction of pod slots used at which the cluster is
	// close to refusing pods regardless of CPU and memory.
	podSlotWarning = 0.85
	// memoryOvercommit is the ratio of memory limits to allocatable above
	// which simultaneous peaks would exceed the cluster.
	memoryOvercommit = 1.5
	// wasteRatio is the usage-to-requests ratio below which requests are
	// considered badly overstated.
	wasteRatio = 0.25
	// wasteFloor is how much of the cluster must be requested before the
	// ratio above is worth mentioning. A small idle cluster is not news.
	wasteFloor = 0.50
	// eventWindow is how recent a warning event must be to count. Kubernetes
	// keeps events about an hour; anything older is already history.
	eventWindow = 30 * time.Minute
	// maxSubjects caps how many objects a single finding names, so one broken
	// daemonset cannot produce a payload with a thousand entries in it.
	maxSubjects = 25
	// maxNamespaces caps the namespace breakdown.
	maxNamespaces = 10
	// maxHotspots caps the restart list.
	maxHotspots = 8
	// maxConsumers caps the top-consumer lists. Five is what somebody reads
	// under pressure; a tenth-place pod is a query, not a dashboard entry.
	maxConsumers = 5
	// diskWarnPercent and diskCriticalPercent are where a node's filesystem
	// becomes worth saying out loud.
	//
	// Both sit BELOW the kubelet's own eviction threshold, which defaults to
	// 10% available. That is the whole point of reading occupancy directly:
	// DiskPressure is the alarm that sounds once pods are already being
	// evicted, and these are the two before it, while somebody can still
	// clear a log directory instead of losing a workload.
	diskWarnPercent     = 80.0
	diskCriticalPercent = 90.0
)

// Subject is one object a finding is about.
type Subject struct {
	// Kind is the object's kind, e.g. "Pod".
	Kind string
	// Namespace is empty for cluster-scoped objects.
	Namespace NamespaceName
	// Name is the object name.
	Name string
	// Detail is the specific fact about this object, e.g. an exit reason or
	// the scheduler's explanation.
	Detail string
}

// Finding is one problem, already aggregated across the objects it affects.
type Finding struct {
	// ID is stable for the same problem across refreshes, so the UI can keep
	// a finding expanded while the data underneath it updates.
	ID string
	// Severity ranks the finding.
	Severity Severity
	// Category groups it.
	Category FindingCategory
	// Title is the short name of the problem, e.g. "CrashLoopBackOff".
	Title string
	// Summary states the extent, with numbers.
	Summary string
	// Advice says what to do about it. This is the half a raw reason string
	// never carries, and the half that saves an operator a search.
	Advice string
	// Subjects are the objects affected, truncated at maxSubjects.
	Subjects []Subject
	// Count is how many objects are affected, including any truncated.
	Count int
	// KindID is the navigator target for the affected objects, so a finding
	// can be clicked through to the list it came from.
	KindID string
	// OldestSeconds is the age of the longest-standing affected object, which
	// separates "started during this rollout" from "broken since Tuesday".
	OldestSeconds int64
}

// Truncated reports whether the cap dropped objects that Count includes.
//
// Count exceeding the listed subjects is not enough on its own to mean that:
// grouped events collapse repeats of one message into a single row, so a
// finding can honestly say "2 warning events" while listing one line. Only the
// cap actually withholds anything, so only the cap is reported as doing so —
// an "and 1 more" that expands to nothing is worse than no note at all.
func (f Finding) Truncated() bool {
	return len(f.Subjects) >= maxSubjects && f.Count > len(f.Subjects)
}

// ResourceUsage is one dimension of cluster capacity, in a single unit.
//
// It carries four numbers that are routinely conflated. Requests decide what
// can be scheduled; limits decide what can burst; usage is what is actually
// happening; allocatable is what exists after the kubelet's reservations.
// Showing usage alone is how a cluster ends up unable to schedule anything
// while every graph looks calm.
type ResourceUsage struct {
	// Capacity is the raw total across nodes.
	Capacity int64
	// Allocatable is what remains for pods after system reservations.
	Allocatable int64
	// Requests is the sum of requests of pods occupying nodes.
	Requests int64
	// Limits is the sum of their limits.
	Limits int64
	// Usage is measured consumption across the NODES, valid only when
	// Measured is true. It includes everything running on the machines — the
	// kubelet, the container runtime, the OS — which is what makes it the
	// honest number for "how loaded is this cluster".
	Usage int64
	// PodUsage is measured consumption summed across PODS only.
	//
	// Kept apart from Usage because the two answer different questions and
	// mixing them produces nonsense: dividing node usage by pod requests
	// reports a cluster as over 100% "efficient", since the numerator counts
	// system overhead the denominator never claimed.
	PodUsage int64
	// Measured reports whether usage across the nodes is known.
	Measured bool
	// PodMeasured reports whether per-POD usage is known, which is a
	// different question and not always the same answer.
	//
	// Ephemeral storage is the case that forced the distinction: the kubelets
	// report how full the disks are, so Usage is real, but nothing reports
	// what each pod is using of them. Without this, Efficiency divided a
	// PodUsage that was never populated by a genuine Requests and announced
	// that the cluster was wasting 100% of its disk reservation.
	PodMeasured bool
}

// RequestPercent returns requests as a percentage of allocatable.
func (r ResourceUsage) RequestPercent() float64 { return percent(r.Requests, r.Allocatable) }

// LimitPercent returns limits as a percentage of allocatable, which may exceed
// 100: limits are deliberately overcommitted on most clusters.
func (r ResourceUsage) LimitPercent() float64 { return percent(r.Limits, r.Allocatable) }

// UsagePercent returns measured usage as a percentage of allocatable.
func (r ResourceUsage) UsagePercent() float64 {
	if !r.Measured {
		return 0
	}
	return percent(r.Usage, r.Allocatable)
}

// Schedulable returns the allocatable amount not already requested — the
// headroom a new pod can actually claim.
func (r ResourceUsage) Schedulable() int64 {
	if r.Allocatable <= r.Requests {
		return 0
	}
	return r.Allocatable - r.Requests
}

// SchedulablePercent returns that headroom as a percentage of allocatable.
//
// Computed here rather than as 100 minus the requested share, which is only
// the same number while requests stay inside allocatable. An overcommitted
// cluster requests more than it has, and "-7% free" is a worse answer than
// none.
func (r ResourceUsage) SchedulablePercent() float64 {
	return percent(r.Schedulable(), r.Allocatable)
}

// Efficiency returns what the pods actually use as a percentage of what they
// reserved, or -1 when there is nothing to compare.
//
// This is the number that starts the conversation nobody has: a cluster
// running at 15% of its own reservations is paying for five times the compute
// it uses, and no usage gauge on its own will ever say so.
//
// Deliberately built from PodUsage rather than Usage, so both sides of the
// ratio describe the same things.
func (r ResourceUsage) Efficiency() float64 {
	if !r.PodMeasured || r.Requests <= 0 {
		return -1
	}
	return percent(r.PodUsage, r.Requests)
}

// PodCapacity is how many pods the cluster is running against how many it can.
type PodCapacity struct {
	// Scheduled is how many non-terminal pods occupy nodes.
	Scheduled int
	// Healthy is how many of those are actually doing their job, by
	// Pod.IsHealthy. Scheduled says a slot is taken; this says the workload
	// in it is working, which is a different question with the same
	// denominator.
	Healthy int
	// Capacity is the sum of the nodes' pod limits on nodes a pod could
	// actually land on: ready, uncordoned and carrying no blocking taint.
	//
	// Taints are the part every other client gets wrong. A control-plane node
	// advertises its hundred-odd slots like any other, and nothing without a
	// toleration will ever occupy one — counting them made a cluster look
	// like it had headroom that no ordinary workload could reach.
	Capacity int64
	// Reserved is the slots excluded above: real, on healthy nodes, and
	// available only to pods that tolerate the taint. Reported rather than
	// silently dropped, because on a cluster of dedicated node pools it is
	// most of the machine.
	Reserved int64
	// ReservedNodes is how many nodes hold the reserved slots, for the
	// sentence that explains why two capacity figures differ.
	ReservedNodes int
	// Unschedulable is how many pods are waiting for a node.
	Unschedulable int
}

// UsedPercent returns occupancy as a percentage of capacity.
func (p PodCapacity) UsedPercent() float64 { return percent(int64(p.Scheduled), p.Capacity) }

// Free returns the slots nothing occupies, floored: a cluster past its own
// cap has none rather than a negative number.
func (p PodCapacity) Free() int64 {
	if p.Capacity <= int64(p.Scheduled) {
		return 0
	}
	return p.Capacity - int64(p.Scheduled)
}

// FreePercent returns those slots as a percentage of capacity.
func (p PodCapacity) FreePercent() float64 { return percent(p.Free(), p.Capacity) }

// HealthyPercent returns working pods as a percentage of scheduled ones.
//
// Measured against what is scheduled rather than against capacity: the
// question is whether the pods that got a slot are working, and dividing by
// slots nobody has claimed would answer a different one badly.
func (p PodCapacity) HealthyPercent() float64 {
	return percent(int64(p.Healthy), int64(p.Scheduled))
}

// WaitingPercent returns unplaced pods as a percentage of everything that
// wants to run — those placed plus those still waiting.
func (p PodCapacity) WaitingPercent() float64 {
	return percent(int64(p.Unschedulable), int64(p.Scheduled+p.Unschedulable))
}

// CapacitySummary is the cluster's capacity across every dimension.
type CapacitySummary struct {
	// CPU is in millicores.
	CPU ResourceUsage
	// Memory is in bytes.
	Memory ResourceUsage
	// Ephemeral is node scratch disk, in bytes.
	//
	// Its Usage is only ever set when the kubelet's own statistics could be
	// read, because nothing in the core API knows how full a node's disk is.
	// Requests against it are usually near zero — hardly anybody declares
	// ephemeral-storage — which is the point: the reservation that would have
	// protected the disk does not exist.
	Ephemeral ResourceUsage
	// Pods counts scheduling slots.
	Pods PodCapacity
}

// NodeSummary counts nodes by condition.
type NodeSummary struct {
	Total         int
	Ready         int
	NotReady      int
	Cordoned      int
	UnderPressure int
	ControlPlane  int
	// Schedulable counts nodes an ordinary pod could actually be placed on:
	// ready, uncordoned and carrying no blocking taint. On a cluster of
	// dedicated pools this is a small fraction of Total, and the gap is the
	// most useful thing this summary can say.
	Schedulable int
	// Tainted counts nodes that refuse pods which do not tolerate them.
	Tainted int
	// Disks is what the kubelets reported about their own filesystems.
	Disks DiskSummary
	// Pressure counts nodes per condition currently raised.
	//
	// Split rather than summed because the three mean different things and
	// are fixed in different ways: DiskPressure is somebody's log or image
	// cache, MemoryPressure is a workload sized wrong, PIDPressure is
	// something forking. One number saying "3 nodes under pressure" tells an
	// operator to go and read the node list to find out which of those it is.
	Pressure map[NodeCondition]int
	// KubeletVersions counts nodes per kubelet version, so a skewed cluster
	// is visible without opening the node list.
	KubeletVersions map[string]int
	// OldestSeconds is the age of the longest-lived node, a decent proxy for
	// the age of the cluster itself.
	OldestSeconds int64
}

// DiskSummary reduces per-node filesystem occupancy to what a card shows.
//
// Separate from the pressure counts above because it answers a different
// question: pressure says the kubelet has already reacted, this says how close
// it is to having to.
type DiskSummary struct {
	// Measured is how many nodes answered. Zero means no kubelet could be
	// read, most often because the cluster does not grant nodes/proxy.
	Measured int
	// FullestPercent is the highest occupancy across the nodes that answered,
	// and FullestNode names it. One number is enough for a card: the node
	// closest to full is the one that decides when eviction starts.
	FullestPercent float64
	FullestNode    string
	// Filling counts nodes past the warning threshold.
	Filling int
}

// StorageSummary is the cluster's persistent storage at a glance.
//
// Provisioned rather than used, and deliberately so: how full a volume is
// belongs to the workload that mounted it and is not in any API PodSteer can
// reach without a per-CSI exporter. What IS knowable — how much has been
// provisioned, what is waiting, what nobody is using any more — is the part
// nothing else surfaces.
type StorageSummary struct {
	// ProvisionedBytes is the size of every bound volume.
	ProvisionedBytes int64
	// UnboundBytes is what claims have asked for and not received.
	UnboundBytes int64
	// OrphanedBytes is storage nothing uses that will not clean itself up.
	OrphanedBytes int64
	// Claims and Volumes count each by phase.
	Claims  map[ClaimPhase]int
	Volumes map[VolumePhase]int
	// Classes breaks the provisioned total down by storage class, largest
	// first, so a cluster paying for premium disks it did not mean to buy can
	// see it.
	Classes []StorageClassUsage
	// Total counts, so a card need not sum a map to say "38 claims".
	TotalClaims  int
	TotalVolumes int
	// Largest is the biggest bound volume, which is worth naming: storage
	// grows quietly and one volume is usually most of the bill.
	LargestBytes int64
	LargestName  string
}

// StorageClassUsage is one class's share of the provisioned total.
type StorageClassUsage struct {
	Name    string
	Volumes int
	Bytes   int64
}

// maxStorageClasses caps the breakdown, which is a card and not a report.
const maxStorageClasses = 5

// summariseStorage reduces volumes and claims to what an overview shows.
func summariseStorage(volumes []PersistentVolume, claims []PersistentVolumeClaim) StorageSummary {
	summary := StorageSummary{
		Claims:       make(map[ClaimPhase]int, 3),
		Volumes:      make(map[VolumePhase]int, 4),
		TotalClaims:  len(claims),
		TotalVolumes: len(volumes),
	}

	byClass := make(map[string]*StorageClassUsage, 4)
	for _, volume := range volumes {
		summary.Volumes[volume.Phase()]++

		if volume.Orphaned() {
			summary.OrphanedBytes += volume.CapacityBytes()
		}
		if volume.Phase() != VolumeBound {
			continue
		}
		summary.ProvisionedBytes += volume.CapacityBytes()
		if volume.CapacityBytes() > summary.LargestBytes {
			summary.LargestBytes = volume.CapacityBytes()
			summary.LargestName = volume.Name()
		}

		// An empty class is real: statically provisioned volumes have none,
		// and calling that "unknown" would be inventing a name for it.
		name := volume.StorageClass()
		if name == "" {
			name = "(none)"
		}
		entry, seen := byClass[name]
		if !seen {
			entry = &StorageClassUsage{Name: name}
			byClass[name] = entry
		}
		entry.Volumes++
		entry.Bytes += volume.CapacityBytes()
	}

	for _, claim := range claims {
		summary.Claims[claim.Phase()]++
		if claim.Phase() != ClaimBound {
			summary.UnboundBytes += claim.RequestedBytes()
		}
	}

	summary.Classes = make([]StorageClassUsage, 0, len(byClass))
	for _, entry := range byClass {
		summary.Classes = append(summary.Classes, *entry)
	}
	// Largest first, then by name so the order does not shuffle between
	// refreshes when two classes hold the same amount.
	sort.SliceStable(summary.Classes, func(i, j int) bool {
		left, right := summary.Classes[i], summary.Classes[j]
		if left.Bytes != right.Bytes {
			return left.Bytes > right.Bytes
		}
		return left.Name < right.Name
	})
	if len(summary.Classes) > maxStorageClasses {
		summary.Classes = summary.Classes[:maxStorageClasses]
	}

	return summary
}

// Consumer is one pod's measured usage, with the reservation it was given.
//
// Both halves are carried because either alone misleads. Usage without the
// request cannot distinguish a pod doing its job from one that has escaped its
// sizing, and the request without usage is what every other dashboard already
// shows.
type Consumer struct {
	Namespace NamespaceName
	Name      string
	Node      string
	// CPUMilli and MemoryBytes are measured usage.
	CPUMilli    int64
	MemoryBytes int64
	// CPURequestMilli and MemoryRequestBytes are what it reserved, zero when
	// it declared nothing.
	CPURequestMilli    int64
	MemoryRequestBytes int64
}

// CPUOfRequest returns usage as a percentage of the CPU reservation, or -1
// when nothing was reserved to compare against.
func (c Consumer) CPUOfRequest() float64 {
	if c.CPURequestMilli <= 0 {
		return -1
	}
	return float64(c.CPUMilli) / float64(c.CPURequestMilli) * 100
}

// MemoryOfRequest returns usage as a percentage of the memory reservation.
func (c Consumer) MemoryOfRequest() float64 {
	if c.MemoryRequestBytes <= 0 {
		return -1
	}
	return float64(c.MemoryBytes) / float64(c.MemoryRequestBytes) * 100
}

// TopConsumers is what is actually using the cluster.
//
// Two separate rankings rather than one combined score: the pod holding the
// most CPU and the pod holding the most memory are usually different pods, and
// a single "biggest" would hide whichever dimension is the one under pressure.
type TopConsumers struct {
	ByCPU    []Consumer
	ByMemory []Consumer
	// Measured says whether metrics answered. Without it these lists are
	// empty, and an empty list must not read as "nothing is using anything".
	Measured bool
}

// topConsumers ranks the pods actually using the cluster.
func topConsumers(pods []Pod, measured bool) TopConsumers {
	if !measured {
		return TopConsumers{}
	}

	consumers := make([]Consumer, 0, len(pods))
	for _, pod := range pods {
		usage := pod.Usage()
		if !usage.Measured || !pod.OccupiesNode() {
			continue
		}
		requests := pod.Requests()
		consumers = append(consumers, Consumer{
			Namespace:          pod.Namespace(),
			Name:               pod.Name(),
			Node:               pod.NodeName(),
			CPUMilli:           usage.CPUMilli,
			MemoryBytes:        usage.MemoryBytes,
			CPURequestMilli:    requests.CPUMilli,
			MemoryRequestBytes: requests.MemoryBytes,
		})
	}
	if len(consumers) == 0 {
		return TopConsumers{}
	}

	byCPU := slices.Clone(consumers)
	// Name breaks ties so the order is stable between refreshes: two idle pods
	// both measuring zero would otherwise swap places on every assessment.
	sort.SliceStable(byCPU, func(i, j int) bool {
		if byCPU[i].CPUMilli != byCPU[j].CPUMilli {
			return byCPU[i].CPUMilli > byCPU[j].CPUMilli
		}
		return byCPU[i].Name < byCPU[j].Name
	})

	byMemory := slices.Clone(consumers)
	sort.SliceStable(byMemory, func(i, j int) bool {
		if byMemory[i].MemoryBytes != byMemory[j].MemoryBytes {
			return byMemory[i].MemoryBytes > byMemory[j].MemoryBytes
		}
		return byMemory[i].Name < byMemory[j].Name
	})

	return TopConsumers{
		ByCPU:    byCPU[:min(maxConsumers, len(byCPU))],
		ByMemory: byMemory[:min(maxConsumers, len(byMemory))],
		Measured: true,
	}
}

// NodeLoad is one node's share of the work, in the dimensions that decide
// whether more will fit on it.
//
// The per-node breakdown exists because a cluster total hides the shape of the
// problem. "46% requested" across eighteen nodes is compatible with every node
// at 46% and with half of them at 90% while the rest idle — and only the
// second explains why a pod will not schedule on a cluster that looks
// half empty.
type NodeLoad struct {
	Name         string
	Ready        bool
	Schedulable  bool
	ControlPlane bool
	// CPUPercent and MemoryPercent are requests against allocatable, which is
	// what the scheduler decides on.
	CPUPercent    float64
	MemoryPercent float64
	// PodPercent is scheduled pods against the node's own cap, the limit that
	// catches people out on small instance types.
	PodPercent float64
	// DiskPercent is the fullest filesystem, or -1 when no kubelet answered.
	DiskPercent float64
	// Pods is how many are on it.
	Pods int
	// The amounts behind those shares. A percentage alone cannot distinguish
	// a small node that is full from a large one that is busy, and "92%" of
	// two different machines is two different quantities of memory.
	CPUMilli      int64
	MemoryBytes   int64
	DiskUsedBytes int64
	// PodCapacity is the node's own cap, the denominator of PodPercent.
	PodCapacity int64
}

// nodeLoads computes each node's share of what has been requested.
func nodeLoads(nodes []Node, pods []Pod) []NodeLoad {
	if len(nodes) == 0 {
		return nil
	}

	type load struct {
		cpu, memory int64
		pods        int
	}
	byNode := make(map[string]*load, len(nodes))
	for _, pod := range pods {
		if !pod.OccupiesNode() || pod.NodeName() == "" {
			continue
		}
		entry, seen := byNode[pod.NodeName()]
		if !seen {
			entry = &load{}
			byNode[pod.NodeName()] = entry
		}
		requests := pod.Requests()
		entry.cpu += requests.CPUMilli
		entry.memory += requests.MemoryBytes
		entry.pods++
	}

	loads := make([]NodeLoad, 0, len(nodes))
	for _, node := range nodes {
		entry := byNode[node.Name()]
		if entry == nil {
			entry = &load{}
		}

		allocatable := node.Allocatable()
		out := NodeLoad{
			Name:         node.Name(),
			Ready:        node.Ready(),
			Schedulable:  !node.Unschedulable(),
			ControlPlane: node.IsControlPlane(),
			Pods:         entry.pods,
			PodCapacity:  allocatable.Pods,
			CPUMilli:     entry.cpu,
			MemoryBytes:  entry.memory,
			DiskPercent:  -1,
		}
		if allocatable.CPUMilli > 0 {
			out.CPUPercent = float64(entry.cpu) / float64(allocatable.CPUMilli) * 100
		}
		if allocatable.MemoryBytes > 0 {
			out.MemoryPercent = float64(entry.memory) / float64(allocatable.MemoryBytes) * 100
		}
		if allocatable.Pods > 0 {
			out.PodPercent = float64(entry.pods) / float64(allocatable.Pods) * 100
		}
		if disks := node.Filesystems(); disks.Measured {
			out.DiskPercent = disks.Fullest().Percent()
			out.DiskUsedBytes = disks.Fullest().UsedBytes
		}
		loads = append(loads, out)
	}

	// Busiest first, by whichever dimension is fullest: the node about to
	// refuse work is the one worth reading, and sorting by name would bury it
	// wherever the alphabet put it.
	sort.SliceStable(loads, func(i, j int) bool {
		left, right := loads[i].fullest(), loads[j].fullest()
		if left != right {
			return left > right
		}
		return loads[i].Name < loads[j].Name
	})
	return loads
}

// fullest returns the node's highest pressure across the scheduling
// dimensions, which is the one that decides whether anything more fits.
func (l NodeLoad) fullest() float64 {
	return max(l.CPUPercent, l.MemoryPercent, l.PodPercent)
}

// PodSummary counts pods by the state an operator cares about.
type PodSummary struct {
	Total       int
	Running     int
	Pending     int
	Succeeded   int
	Failed      int
	Terminating int
	Unknown     int
	// NotReady counts pods that are not doing their job, by Pod.IsHealthy.
	NotReady int
	// Restarts is the cluster-wide restart total.
	Restarts int32
	// BestEffort counts pods that declare no requests at all — the ones the
	// kubelet evicts first when a node comes under pressure.
	BestEffort int
}

// WorkloadKindSummary counts one controller kind.
type WorkloadKindSummary struct {
	// Kind is the controller kind.
	Kind WorkloadKind
	// KindID is the navigator target for the kind's list.
	KindID string
	Total  int
	// Healthy is by Workload.IsHealthy, so a scaled-to-zero deployment counts
	// as healthy rather than as a fault.
	Healthy int
	// Rolling counts controllers mid-rollout, which is expected rather than
	// wrong and so is kept apart from Degraded.
	Rolling int
	// Degraded counts controllers with fewer ready replicas than desired and
	// no rollout in progress.
	Degraded int
}

// NamespaceLoad is one namespace's share of the cluster.
type NamespaceLoad struct {
	Name NamespaceName
	Pods int
	// NotReady is how many of those pods are not doing their job.
	NotReady int
	// CPURequests is in millicores, MemoryRequests in bytes.
	CPURequests    int64
	MemoryRequests int64
	// CPUUsage and MemoryUsage are measured, valid only when Measured is true.
	CPUUsage    int64
	MemoryUsage int64
	Measured    bool
}

// RestartHotspot is a pod worth looking at because it keeps restarting.
type RestartHotspot struct {
	Namespace NamespaceName
	Name      string
	Restarts  int32
	// Reason is the container's last reason, when one is reported.
	Reason string
	// AgeSeconds is the pod's age, which is what turns a restart count into a
	// rate: 40 restarts in an hour is an incident, 40 over three months is a
	// nightly OOM nobody has noticed.
	AgeSeconds int64
	Healthy    bool
}

// OverviewInput is a cluster snapshot to assess.
//
// Every slice is optional. A source that could not be read — no metrics
// server, RBAC that forbids listing events — is left empty and named in
// Unavailable, and the assessment degrades to what the rest supports rather
// than failing. Half an overview beats an error page.
type OverviewInput struct {
	ClusterID   ClusterID
	Version     ServerVersion
	Nodes       []Node
	Pods        []Pod
	Workloads   []Workload
	Events      []Event
	Namespaces  []Namespace
	Volumes     []PersistentVolume
	Claims      []PersistentVolumeClaim
	Unavailable []string
	// MetricsMeasured reports whether the metrics API answered. Pod usage
	// being zero is not evidence either way: a genuinely idle pod measures
	// zero too.
	MetricsMeasured bool
	// Now is the reference time. Passed rather than read so the rules are
	// testable, the same reason every Age method takes it.
	Now time.Time
}

// Overview is the assessed state of one cluster at one moment.
type Overview struct {
	ClusterID   ClusterID
	Version     ServerVersion
	GeneratedAt time.Time
	Health      HealthGrade
	Findings    []Finding
	Capacity    CapacitySummary
	Storage     StorageSummary
	Consumers   TopConsumers
	Support     ReleaseSupport
	NodeLoads   []NodeLoad
	Nodes       NodeSummary
	Pods        PodSummary
	Workloads   []WorkloadKindSummary
	Namespaces  []NamespaceLoad
	Restarts    []RestartHotspot
	// Unavailable names the data sources that could not be read, so the UI can
	// say "no metrics" instead of quietly showing zeroes.
	Unavailable []string
}

// NewOverview assesses a cluster snapshot.
//
// It is a pure function of its input: no I/O, no clock, no ordering
// dependence. Everything the UI shows is decided here, which is what makes
// the rules arguable in a test rather than only observable in production.
func NewOverview(input OverviewInput) Overview {
	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()

	nodes := summariseNodes(input.Nodes, now)
	pods := summarisePods(input.Pods)
	capacity := summariseCapacity(input.Nodes, input.Pods, input.MetricsMeasured)
	workloads := summariseWorkloads(input.Workloads)

	owners := newOwnerIndex(input.Workloads)

	support := SupportFor(input.Version, now)

	findings := make([]Finding, 0, 16)
	findings = append(findings, podFindings(input.Pods, owners, now)...)
	findings = append(findings, workloadFindings(input.Workloads, findings, now)...)
	findings = append(findings, nodeFindings(input.Nodes, nodes)...)
	findings = append(findings, filesystemFindings(input.Nodes)...)
	findings = append(findings, storageFindings(input.Volumes, input.Claims, now)...)
	findings = append(findings, releaseFindings(input.Version, support)...)
	findings = append(findings, capacityFindings(capacity)...)
	findings = append(findings, restartFindings(input.Pods, now)...)
	findings = append(findings, configurationFindings(input.Pods, pods)...)
	findings = append(findings, eventFindings(input.Events, findings, now)...)
	rankFindings(findings)

	return Overview{
		ClusterID:   input.ClusterID,
		Version:     input.Version,
		GeneratedAt: now,
		Health:      grade(findings),
		Storage:     summariseStorage(input.Volumes, input.Claims),
		Findings:    findings,
		Capacity:    capacity,
		Nodes:       nodes,
		Pods:        pods,
		Workloads:   workloads,
		Namespaces:  summariseNamespaces(input.Pods, input.MetricsMeasured),
		Restarts:    restartHotspots(input.Pods, now),
		Consumers:   topConsumers(input.Pods, input.MetricsMeasured),
		Support:     support,
		NodeLoads:   nodeLoads(input.Nodes, input.Pods),
		Unavailable: slices.Clone(input.Unavailable),
	}
}

// grade reduces the findings to one verdict.
func grade(findings []Finding) HealthGrade {
	worst := HealthHealthy
	for _, finding := range findings {
		switch finding.Severity {
		case SeverityCritical:
			return HealthCritical
		case SeverityWarning:
			worst = HealthDegraded
		}
	}
	return worst
}

// rankFindings orders findings by how much they deserve the operator's
// attention: severity first, then how much of the cluster is affected, then
// title so the order is stable between refreshes.
func rankFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		left, right := findings[i], findings[j]
		if left.Severity != right.Severity {
			return left.Severity.rank() > right.Severity.rank()
		}
		if left.Count != right.Count {
			return left.Count > right.Count
		}
		return left.Title < right.Title
	})
}

// --- summaries ------------------------------------------------------------

func summariseNodes(nodes []Node, now time.Time) NodeSummary {
	summary := NodeSummary{
		KubeletVersions: make(map[string]int, 2),
		Pressure:        make(map[NodeCondition]int, len(KnownPressureConditions())),
	}

	for _, node := range nodes {
		summary.Total++
		if node.Ready() {
			summary.Ready++
		} else {
			summary.NotReady++
		}
		if node.Unschedulable() {
			summary.Cordoned++
		}
		if node.Reserved() {
			summary.Tainted++
		}
		if node.Ready() && !node.Unschedulable() && !node.Reserved() {
			summary.Schedulable++
		}
		if disks := node.Filesystems(); disks.Measured {
			summary.Disks.Measured++
			percent := disks.Fullest().Percent()
			if percent > summary.Disks.FullestPercent {
				summary.Disks.FullestPercent = percent
				summary.Disks.FullestNode = node.Name()
			}
			if percent >= diskWarnPercent {
				summary.Disks.Filling++
			}
		}
		if conditions := node.ActiveConditions(); len(conditions) > 0 {
			summary.UnderPressure++
			for _, condition := range conditions {
				summary.Pressure[condition]++
			}
		}
		if node.IsControlPlane() {
			summary.ControlPlane++
		}
		if version := node.KubeletVersion(); version != "" {
			summary.KubeletVersions[version]++
		}
		if age := int64(node.Age(now).Seconds()); age > summary.OldestSeconds {
			summary.OldestSeconds = age
		}
	}

	return summary
}

func summarisePods(pods []Pod) PodSummary {
	var summary PodSummary

	for _, pod := range pods {
		summary.Total++
		switch pod.Phase() {
		case PodPhaseRunning:
			summary.Running++
		case PodPhasePending:
			summary.Pending++
		case PodPhaseSucceeded:
			summary.Succeeded++
		case PodPhaseFailed:
			summary.Failed++
		case PodPhaseTerminating:
			summary.Terminating++
		default:
			summary.Unknown++
		}
		if !pod.IsHealthy() {
			summary.NotReady++
		}
		if pod.QoSClass() == QoSBestEffort {
			summary.BestEffort++
		}
		summary.Restarts += pod.RestartCount()
	}

	return summary
}

// summariseCapacity totals the cluster's resources against what is claimed.
//
// Only pods that occupy a node contribute requests: a Succeeded pod is still
// an object but reserves nothing, and counting it is the standard way to
// produce a utilisation figure that is wrong on every cluster running Jobs.
// Only schedulable nodes contribute pod slots, for the same reason — a
// cordoned node's capacity is not available to anything.
func summariseCapacity(nodes []Node, pods []Pod, measured bool) CapacitySummary {
	var summary CapacitySummary
	summary.CPU.Measured = measured
	summary.Memory.Measured = measured
	// Only these two have per-pod figures; ephemeral storage is measured at
	// the node and nowhere else.
	summary.CPU.PodMeasured = measured
	summary.Memory.PodMeasured = measured

	for _, node := range nodes {
		summary.CPU.Capacity += node.Capacity().CPUMilli
		summary.CPU.Allocatable += node.Allocatable().CPUMilli
		summary.Memory.Capacity += node.Capacity().MemoryBytes
		summary.Memory.Allocatable += node.Allocatable().MemoryBytes
		summary.Ephemeral.Capacity += node.Capacity().EphemeralBytes
		summary.Ephemeral.Allocatable += node.Allocatable().EphemeralBytes

		// Occupancy comes from the kubelets, not from the API server, and is
		// summed only across the nodes that answered. Measured is set as soon
		// as ONE did: a cluster where half the kubelets are unreachable
		// should still say what it knows about the other half rather than
		// report the whole dimension as unmeasured.
		if disks := node.Filesystems(); disks.Measured {
			summary.Ephemeral.Usage += disks.Nodefs.UsedBytes
			summary.Ephemeral.Measured = true
		}

		// A pod can only land where the scheduler will place it: not on a
		// cordoned node, not on one that is not ready, and not on one whose
		// taint it does not tolerate.
		if !node.Unschedulable() && node.Ready() {
			if node.Reserved() {
				summary.Pods.Reserved += node.Allocatable().Pods
				summary.Pods.ReservedNodes++
			} else {
				summary.Pods.Capacity += node.Allocatable().Pods
			}
		}

		// Node metrics are the truthful total: pod metrics omit whatever the
		// kubelet and container runtime themselves consume, which on a busy
		// node is not a rounding error.
		if usage := node.Usage(); usage.Measured {
			summary.CPU.Usage += usage.CPUMilli
			summary.Memory.Usage += usage.MemoryBytes
		}
	}

	for _, pod := range pods {
		if !pod.OccupiesNode() {
			if pod.Phase() == PodPhasePending && !pod.IsScheduled() {
				summary.Pods.Unschedulable++
			}
			continue
		}

		summary.Pods.Scheduled++
		if pod.IsHealthy() {
			summary.Pods.Healthy++
		}
		requests, limits := pod.Requests(), pod.Limits()
		summary.CPU.Requests += requests.CPUMilli
		summary.CPU.Limits += limits.CPUMilli
		summary.Memory.Requests += requests.MemoryBytes
		summary.Memory.Limits += limits.MemoryBytes
		summary.Ephemeral.Requests += requests.EphemeralBytes
		summary.Ephemeral.Limits += limits.EphemeralBytes

		// Summed separately from the node usage above so the efficiency
		// ratio compares pods with pods.
		if usage := pod.Usage(); usage.Measured {
			summary.CPU.PodUsage += usage.CPUMilli
			summary.Memory.PodUsage += usage.MemoryBytes
		}
	}

	return summary
}

func summariseWorkloads(workloads []Workload) []WorkloadKindSummary {
	order := []WorkloadKind{
		WorkloadDeployment,
		WorkloadStatefulSet,
		WorkloadDaemonSet,
		WorkloadJob,
		WorkloadCronJob,
		WorkloadReplicaSet,
	}

	byKind := make(map[WorkloadKind]*WorkloadKindSummary, len(order))
	for _, kind := range order {
		byKind[kind] = &WorkloadKindSummary{Kind: kind, KindID: workloadKindID(kind)}
	}

	for _, workload := range workloads {
		summary, ok := byKind[workload.Kind()]
		if !ok {
			continue
		}
		summary.Total++
		switch {
		// Order matters for Jobs: one still running is in progress even
		// though earlier attempts failed, and one that failed with nothing
		// left running is not merely "not finished yet".
		case workload.HasFailed():
			summary.Degraded++
		case workload.IsRunning():
			summary.Rolling++
		case workload.IsHealthy():
			summary.Healthy++
		case workload.IsRolling():
			summary.Rolling++
		default:
			summary.Degraded++
		}
	}

	// Kinds nobody uses are dropped rather than shown as zero: a cluster with
	// no StatefulSets does not need a tile telling it so.
	summaries := make([]WorkloadKindSummary, 0, len(order))
	for _, kind := range order {
		if summary := byKind[kind]; summary.Total > 0 {
			summaries = append(summaries, *summary)
		}
	}
	return summaries
}

// summariseNamespaces ranks namespaces by what they reserve, not by what they
// use: reservations are what fill a cluster, and the biggest reserver is who
// to talk to when nothing else will schedule.
func summariseNamespaces(pods []Pod, measured bool) []NamespaceLoad {
	byName := make(map[NamespaceName]*NamespaceLoad)

	for _, pod := range pods {
		if !pod.OccupiesNode() {
			continue
		}

		load, ok := byName[pod.Namespace()]
		if !ok {
			load = &NamespaceLoad{Name: pod.Namespace(), Measured: measured}
			byName[pod.Namespace()] = load
		}

		load.Pods++
		if !pod.IsHealthy() {
			load.NotReady++
		}
		requests := pod.Requests()
		load.CPURequests += requests.CPUMilli
		load.MemoryRequests += requests.MemoryBytes
		if usage := pod.Usage(); usage.Measured {
			load.CPUUsage += usage.CPUMilli
			load.MemoryUsage += usage.MemoryBytes
		}
	}

	loads := make([]NamespaceLoad, 0, len(byName))
	for _, load := range byName {
		loads = append(loads, *load)
	}

	sort.SliceStable(loads, func(i, j int) bool {
		if loads[i].CPURequests != loads[j].CPURequests {
			return loads[i].CPURequests > loads[j].CPURequests
		}
		if loads[i].MemoryRequests != loads[j].MemoryRequests {
			return loads[i].MemoryRequests > loads[j].MemoryRequests
		}
		return loads[i].Name < loads[j].Name
	})

	if len(loads) > maxNamespaces {
		loads = loads[:maxNamespaces]
	}
	return loads
}

func restartHotspots(pods []Pod, now time.Time) []RestartHotspot {
	hotspots := make([]RestartHotspot, 0, maxHotspots)

	for _, pod := range pods {
		restarts := pod.RestartCount()
		if restarts == 0 {
			continue
		}
		hotspots = append(hotspots, RestartHotspot{
			Namespace:  pod.Namespace(),
			Name:       pod.Name(),
			Restarts:   restarts,
			Reason:     pod.StatusReason(),
			AgeSeconds: int64(pod.Age(now).Seconds()),
			Healthy:    pod.IsHealthy(),
		})
	}

	sort.SliceStable(hotspots, func(i, j int) bool {
		if hotspots[i].Restarts != hotspots[j].Restarts {
			return hotspots[i].Restarts > hotspots[j].Restarts
		}
		return hotspots[i].Name < hotspots[j].Name
	})

	if len(hotspots) > maxHotspots {
		hotspots = hotspots[:maxHotspots]
	}
	return hotspots
}

// --- pod findings ---------------------------------------------------------

// podProblem is one pod's diagnosis, before grouping.
type podProblem struct {
	title    string
	severity Severity
	category FindingCategory
	advice   string
	detail   string
}

// diagnosePod explains why a pod is not simply doing its job, or reports that
// it is.
//
// Order matters: the first rule that matches wins, and they are arranged from
// the most specific cause to the most general symptom. A crash-looping pod is
// also "not ready", but reporting it as not ready would bury the reason.
func diagnosePod(pod Pod, now time.Time) (podProblem, bool) {
	age := pod.Age(now)

	switch pod.Phase() {
	case PodPhaseSucceeded:
		return podProblem{}, false

	case PodPhaseTerminating:
		if age > 0 && pod.Age(now) > terminatingGrace {
			return podProblem{
				title:    "Stuck terminating",
				severity: SeverityWarning,
				category: CategoryFindingWorkload,
				advice: "The pod was deleted but has not gone. Almost always a finalizer waiting " +
					"on a controller that is no longer running, or a container ignoring SIGTERM.",
			}, true
		}
		return podProblem{}, false

	case PodPhaseFailed:
		if strings.EqualFold(pod.Reason(), "Evicted") {
			return podProblem{
				title:    "Evicted",
				severity: SeverityCritical,
				category: CategoryFindingNode,
				advice: "The kubelet reclaimed the node's resources and this pod was chosen. " +
					"Set requests so it is not BestEffort, or give the node room.",
				detail: pod.Message(),
			}, true
		}
		return podProblem{
			title:    "Failed",
			severity: SeverityWarning,
			category: CategoryFindingWorkload,
			advice:   "Every container terminated and at least one exited non-zero.",
			detail:   firstNonEmpty(pod.StatusReason(), pod.Reason(), pod.Message()),
		}, true

	case PodPhaseUnknown:
		return podProblem{
			title:    "Unknown state",
			severity: SeverityWarning,
			category: CategoryFindingNode,
			advice: "The API server has lost contact with the node hosting this pod. " +
				"Check the node before the pod.",
		}, true
	}

	// Unscheduled: the scheduler's message is the only thing that explains it.
	if !pod.IsScheduled() {
		if reason := pod.Reason(); strings.EqualFold(reason, "Unschedulable") || pod.Message() != "" {
			return podProblem{
				title:    "Unschedulable",
				severity: SeverityCritical,
				category: CategoryFindingScheduling,
				advice: "No node satisfies the pod's requests, affinity or tolerations. " +
					"The scheduler's message says which constraint failed.",
				detail: pod.Message(),
			}, true
		}
		if age > pendingGrace {
			return podProblem{
				title:    "Pending",
				severity: SeverityWarning,
				category: CategoryFindingScheduling,
				advice: "The pod has waited longer than a rollout should take without being " +
					"placed on a node.",
			}, true
		}
		return podProblem{}, false
	}

	// Scheduled: the container reasons are the diagnosis.
	if reason := pod.StatusReason(); reason != "" {
		if problem, ok := diagnoseContainerReason(reason, age); ok {
			return problem, true
		}
	}

	if !pod.IsReady() && age > notReadyGrace {
		return podProblem{
			title:    "Not ready",
			severity: SeverityWarning,
			category: CategoryFindingWorkload,
			advice: "The containers are running but readiness is failing, so no Service is " +
				"sending traffic here. Check the readiness probe and what it calls.",
			detail: fmt.Sprintf("%d/%d containers ready", pod.ReadyContainers(), pod.TotalContainers()),
		}, true
	}

	return podProblem{}, false
}

// diagnoseContainerReason maps a container's reason onto a problem.
//
// The reasons are the API server's own strings. Anything unrecognised falls
// through as a warning rather than being dropped: a reason PodSteer has not
// seen is still a reason the pod is not running.
func diagnoseContainerReason(reason string, age time.Duration) (podProblem, bool) {
	switch reason {
	case "CrashLoopBackOff":
		return podProblem{
			title:    "CrashLoopBackOff",
			severity: SeverityCritical,
			category: CategoryFindingWorkload,
			advice: "The container starts, exits, and is restarted with growing delay. " +
				"The logs of the PREVIOUS attempt hold the reason; the current one is usually empty.",
		}, true

	case "ImagePullBackOff", "ErrImagePull", "InvalidImageName", "ErrImageNeverPull":
		return podProblem{
			title:    "Image cannot be pulled",
			severity: SeverityCritical,
			category: CategoryFindingWorkload,
			advice: "The tag does not exist, or the node has no credentials for the registry. " +
				"A missing imagePullSecret looks identical to a typo in the tag.",
		}, true

	case "CreateContainerConfigError", "CreateContainerError":
		return podProblem{
			title:    "Container config error",
			severity: SeverityCritical,
			category: CategoryFindingWorkload,
			advice: "The container references something that is not there — usually a ConfigMap " +
				"key or Secret that was renamed or never created.",
		}, true

	case "OOMKilled":
		return podProblem{
			title:    "OOMKilled",
			severity: SeverityCritical,
			category: CategoryFindingWorkload,
			advice: "The kernel killed the container for exceeding its memory limit. Raise the " +
				"limit or find the leak; restarting changes nothing on its own.",
		}, true

	case "Error":
		return podProblem{
			title:    "Container error",
			severity: SeverityWarning,
			category: CategoryFindingWorkload,
			advice:   "The container exited non-zero. Its logs hold the reason.",
		}, true

	case "ContainerCreating", "PodInitializing":
		if age <= startingGrace {
			return podProblem{}, false
		}
		return podProblem{
			title:    "Stuck starting",
			severity: SeverityWarning,
			category: CategoryFindingWorkload,
			advice: "Creation has taken far longer than an image pull should. Usually a volume " +
				"that will not mount, or an init container that never finishes.",
		}, true

	case "CreateContainerConfigWaiting", "PodScheduled":
		return podProblem{}, false

	default:
		return podProblem{
			title:    reason,
			severity: SeverityWarning,
			category: CategoryFindingWorkload,
			advice:   "The kubelet reports this reason for a container that is not running.",
		}, true
	}
}

// ownerRef names the workload a pod ultimately belongs to.
type ownerRef struct {
	Kind string
	Name string
}

// ownerIndex resolves a pod's immediate controller to the workload an
// operator would name.
//
// A pod's controlling owner is almost never the object anybody talks about: a
// Deployment's pods are owned by a ReplicaSet called "api-7d9f8b4c9", and a
// CronJob's by a Job called "backup-29399040". Grouping by the immediate owner
// would produce findings labelled with generated hashes, and — worse — a
// Deployment could never be matched against the findings its own pods raised.
//
// One hop is enough: Kubernetes nests controllers exactly this deep.
type ownerIndex map[string]ownerRef

// newOwnerIndex maps intermediate controllers onto their parents, using the
// ownership the workloads themselves report rather than guessing from name
// prefixes.
func newOwnerIndex(workloads []Workload) ownerIndex {
	index := make(ownerIndex, len(workloads))
	for _, workload := range workloads {
		owner := workload.Owner()
		if owner.Name == "" {
			continue
		}
		key := ownerKey(workload.Namespace(), string(workload.Kind()), workload.Name())
		index[key] = ownerRef{Kind: owner.Kind, Name: owner.Name}
	}
	return index
}

// resolve returns the workload a pod belongs to, following one level of
// ownership when the index knows about it.
func (i ownerIndex) resolve(pod Pod) ownerRef {
	controller := pod.Controller()
	if controller.Name == "" {
		return ownerRef{}
	}

	direct := ownerRef{Kind: controller.Kind, Name: controller.Name}
	if parent, ok := i[ownerKey(pod.Namespace(), controller.Kind, controller.Name)]; ok {
		return parent
	}
	return direct
}

// ownerKey builds the index key for a workload.
func ownerKey(namespace NamespaceName, kind, name string) string {
	return string(namespace) + "/" + kind + "/" + name
}

// podFindings diagnoses every pod and groups the results.
//
// Grouping is by problem and by owning workload, which is what collapses the
// twelve identical pods of one broken deployment into one line that says
// twelve. Bare pods group by problem and namespace alone.
func podFindings(pods []Pod, owners ownerIndex, now time.Time) []Finding {
	type group struct {
		problem  podProblem
		subjects []Subject
		count    int
		oldest   int64
		owner    ownerRef
	}

	groups := make(map[string]*group)
	order := make([]string, 0, 8)

	for _, pod := range pods {
		problem, ok := diagnosePod(pod, now)
		if !ok {
			continue
		}

		owner := owners.resolve(pod)
		key := problem.title + "|" + ownerKey(pod.Namespace(), owner.Kind, owner.Name)

		current, seen := groups[key]
		if !seen {
			current = &group{problem: problem, owner: owner}
			groups[key] = current
			order = append(order, key)
		}

		current.count++
		if age := int64(pod.Age(now).Seconds()); age > current.oldest {
			current.oldest = age
		}
		if len(current.subjects) < maxSubjects {
			current.subjects = append(current.subjects, Subject{
				Kind:      "Pod",
				Namespace: pod.Namespace(),
				Name:      pod.Name(),
				Detail:    firstNonEmpty(problem.detail, pod.StatusReason()),
			})
		}
	}

	findings := make([]Finding, 0, len(order))
	for _, key := range order {
		current := groups[key]
		findings = append(findings, Finding{
			ID:            "pod:" + key,
			Severity:      current.problem.severity,
			Category:      current.problem.category,
			Title:         current.problem.title,
			Summary:       podGroupSummary(current.count, current.subjects, current.owner),
			Advice:        current.problem.advice,
			Subjects:      current.subjects,
			Count:         current.count,
			KindID:        podKindID,
			OldestSeconds: current.oldest,
		})
	}
	return findings
}

// podGroupSummary states the extent of a grouped pod problem.
func podGroupSummary(count int, subjects []Subject, owner ownerRef) string {
	namespace := ""
	if len(subjects) > 0 {
		namespace = string(subjects[0].Namespace)
	}

	switch {
	// A bare pod is named directly; a controlled one is named by its
	// controller, because that is the object anybody would act on — and
	// because a pod name is mostly generated hash anyway.
	case owner.Name == "" && count == 1 && len(subjects) == 1:
		return fmt.Sprintf("%s/%s", namespace, subjects[0].Name)
	case owner.Name != "":
		return fmt.Sprintf("%s of %s %s/%s", plural(count, "pod", "pods"),
			strings.ToLower(owner.Kind), namespace, owner.Name)
	default:
		return fmt.Sprintf("%s in %s", plural(count, "pod", "pods"), namespace)
	}
}

// --- workload, node, capacity and event findings --------------------------

// workloadFindings reports controllers that are not at their desired scale.
//
// Controllers whose pods already explain themselves are skipped: a deployment
// is degraded *because* its pods are crash-looping, and reporting both says
// the same thing twice at two levels of detail. The specific finding wins.
func workloadFindings(workloads []Workload, existing []Finding, now time.Time) []Finding {
	explained := make(map[string]bool, len(existing))
	for _, finding := range existing {
		if key := findingOwnerKey(finding); key != "" {
			explained[key] = true
		}
	}

	byKind := make(map[WorkloadKind][]Workload)
	kinds := make([]WorkloadKind, 0, 4)

	failedJobs := make([]Workload, 0, 4)

	for _, workload := range workloads {
		// A failed Job is a different problem from a controller short of
		// replicas, and is reported as one below.
		if workload.HasFailed() {
			failedJobs = append(failedJobs, workload)
			continue
		}
		if workload.IsRunning() || workload.IsHealthy() || workload.IsRolling() ||
			workload.Kind() == WorkloadReplicaSet || workload.Kind() == WorkloadJob {
			continue
		}
		// A ReplicaSet is skipped above because it is an implementation detail
		// of its Deployment: reporting both names the same outage twice.
		if explained[ownerKey(workload.Namespace(), string(workload.Kind()), workload.Name())] {
			continue
		}
		if _, seen := byKind[workload.Kind()]; !seen {
			kinds = append(kinds, workload.Kind())
		}
		byKind[workload.Kind()] = append(byKind[workload.Kind()], workload)
	}

	findings := make([]Finding, 0, len(kinds))
	for _, kind := range kinds {
		degraded := byKind[kind]
		subjects := make([]Subject, 0, min(len(degraded), maxSubjects))
		var oldest int64

		for _, workload := range degraded {
			if age := int64(workload.Age(now).Seconds()); age > oldest {
				oldest = age
			}
			if len(subjects) >= maxSubjects {
				continue
			}
			subjects = append(subjects, Subject{
				Kind:      string(kind),
				Namespace: workload.Namespace(),
				Name:      workload.Name(),
				Detail:    fmt.Sprintf("%d/%d ready", workload.Ready(), workload.Desired()),
			})
		}

		severity := SeverityWarning
		if allUnavailable(degraded) {
			severity = SeverityCritical
		}

		findings = append(findings, Finding{
			ID:       "workload:" + string(kind), //nolint:exhaustruct // remaining fields are zero by design
			Severity: severity,
			Category: CategoryFindingWorkload,
			Title:    string(kind) + " not at desired scale",
			Summary: fmt.Sprintf("%s below the replica count they were asked for",
				plural(len(degraded), string(kind), string(kind)+"s")),
			Advice: "Fewer replicas are ready than desired and no rollout is in progress, so " +
				"this is not a deploy settling — the missing replicas cannot start.",
			Subjects:      subjects,
			Count:         len(degraded),
			KindID:        workloadKindID(kind),
			OldestSeconds: oldest,
		})
	}

	if len(failedJobs) > 0 {
		subjects := make([]Subject, 0, min(len(failedJobs), maxSubjects))
		var oldest int64
		for _, job := range failedJobs {
			if age := int64(job.Age(now).Seconds()); age > oldest {
				oldest = age
			}
			if len(subjects) >= maxSubjects {
				continue
			}
			subjects = append(subjects, Subject{
				Kind:      string(WorkloadJob),
				Namespace: job.Namespace(),
				Name:      job.Name(),
				Detail:    plural(int(job.Failed()), "failed pod", "failed pods"),
			})
		}

		findings = append(findings, Finding{
			ID:       "workload:jobs-failed",
			Severity: SeverityWarning,
			Category: CategoryFindingWorkload,
			Title:    "Jobs failed",
			Summary:  fmt.Sprintf("%s gave up with no pod still running", plural(len(failedJobs), "job", "jobs")),
			Advice: "A failed Job stays in the cluster with its pods, so the logs of the last " +
				"attempt are still there. Nothing will retry it once the backoff limit is reached.",
			Subjects:      subjects,
			Count:         len(failedJobs),
			KindID:        workloadKindID(WorkloadJob),
			OldestSeconds: oldest,
		})
	}

	return findings
}

// allUnavailable reports whether every workload in the group has no ready
// replica at all, which is an outage rather than a degradation.
func allUnavailable(workloads []Workload) bool {
	for _, workload := range workloads {
		if workload.Ready() > 0 {
			return false
		}
	}
	return len(workloads) > 0
}

// findingOwnerKey extracts the owner a pod finding was grouped by, from the
// finding's own id. The id is built as "pod:<title>|<ownerKey>", so the two
// stay in step by construction rather than by a second lookup that could
// disagree with the grouping.
func findingOwnerKey(finding Finding) string {
	if !strings.HasPrefix(finding.ID, "pod:") {
		return ""
	}
	_, key, ok := strings.Cut(finding.ID, "|")
	if !ok || strings.HasSuffix(key, "//") {
		// No controller: a bare pod explains nothing about a workload.
		return ""
	}
	return key
}

// storageFindings reports claims that never bound and volumes nobody uses.
//
// Both are invisible from the workload side. A pod waiting on a claim that
// will never bind sits at ContainerCreating with an event nobody reads, and a
// released volume has no pod, no claim and no list to appear in — while the
// cloud provider bills for it every month.
func storageFindings(volumes []PersistentVolume, claims []PersistentVolumeClaim, now time.Time) []Finding {
	var pending, lost []Subject
	for _, claim := range claims {
		switch {
		case claim.Phase() == ClaimLost:
			lost = append(lost, Subject{
				Kind: "PersistentVolumeClaim", Namespace: claim.Namespace(), Name: claim.Name(),
				Detail: "the volume behind it is gone",
			})
		case claim.StuckPending(now):
			pending = append(pending, Subject{
				Kind: "PersistentVolumeClaim", Namespace: claim.Namespace(), Name: claim.Name(),
				Detail: fmt.Sprintf("waiting %s for %s",
					formatDuration(claim.Age(now)), storageClassOrNone(claim.StorageClass())),
			})
		}
	}

	var orphaned []Subject
	var orphanedBytes int64
	for _, volume := range volumes {
		if !volume.Orphaned() {
			continue
		}
		orphanedBytes += volume.CapacityBytes()
		if len(orphaned) < maxSubjects {
			orphaned = append(orphaned, Subject{
				Kind: "PersistentVolume", Name: volume.Name(),
				Detail: fmt.Sprintf("%s, released %s ago, %s policy",
					formatBytes(volume.CapacityBytes()), formatDuration(volume.Age(now)),
					volume.ReclaimPolicy()),
			})
		}
	}

	findings := make([]Finding, 0, 3)

	if len(lost) > 0 {
		findings = append(findings, Finding{
			ID:       "storage:lost",
			Severity: SeverityCritical,
			Category: CategoryFindingStorage,
			Title:    "Claims whose volume is gone",
			Summary:  fmt.Sprintf("%s Lost", plural(len(lost), "claim is", "claims are")),
			Advice: "The workload's data is not coming back by itself. A Lost claim means the " +
				"PersistentVolume it was bound to was deleted underneath it, so anything mounting " +
				"it will fail to start until the claim is recreated against new storage.",
			Subjects: lost,
			Count:    len(lost),
			KindID:   claimKindID,
		})
	}

	if len(pending) > 0 {
		findings = append(findings, Finding{
			ID:       "storage:pending",
			Severity: SeverityWarning,
			Category: CategoryFindingStorage,
			Title:    "Claims not bound",
			Summary: fmt.Sprintf("%s waiting longer than %s for storage",
				plural(len(pending), "claim is", "claims are"), formatDuration(bindingGrace)),
			Advice: "Pods mounting these sit at ContainerCreating with no failure of their own to " +
				"read. The usual causes are a storage class that does not exist, a provisioner " +
				"with no capacity in the pod's zone, or a quota already spent.",
			Subjects: pending,
			Count:    len(pending),
			KindID:   claimKindID,
		})
	}

	if len(orphaned) > 0 {
		findings = append(findings, Finding{
			ID:       "storage:orphaned",
			Severity: SeverityInfo,
			Category: CategoryFindingStorage,
			Title:    "Storage nothing is using",
			Summary: fmt.Sprintf("%s Released and kept — %s",
				plural(len(orphaned), "volume is", "volumes are"), formatBytes(orphanedBytes)),
			Advice: "Their claims are gone but the reclaim policy keeps them, so nothing will ever " +
				"remove them. That is deliberate when the data still matters and pure cost when it " +
				"does not — on a cloud provider these are disks still being billed.",
			Subjects: orphaned,
			Count:    len(orphaned),
			KindID:   volumeKindID,
		})
	}

	return findings
}

// storageClassOrNone names a claim's class for a message.
func storageClassOrNone(class string) string {
	if class == "" {
		return "the default storage class"
	}
	return "storage class " + class
}

// filesystemFindings reports node disks filling before the kubelet reacts.
//
// Nothing else in PodSteer can say this, and nothing else in Kubernetes will:
// the API server does not know how full a node's disk is, and by the time the
// node itself says DiskPressure the eviction has started. A node at 88% is
// invisible in every list and is the last cheap moment to act.
func filesystemFindings(nodes []Node) []Finding {
	var warning, critical []Subject

	for _, node := range nodes {
		disks := node.Filesystems()
		if !disks.Measured {
			continue
		}

		// The fullest of the two decides: nodefs and imagefs are separate
		// filesystems on some runtimes, and the kubelet evicts on whichever
		// crosses its threshold first.
		fullest := disks.Fullest()
		percent := fullest.Percent()
		if percent < diskWarnPercent {
			continue
		}

		subject := Subject{
			Kind: "Node",
			Name: node.Name(),
			Detail: fmt.Sprintf("%.0f%% full — %s of %s used",
				percent, formatBytes(fullest.UsedBytes), formatBytes(fullest.CapacityBytes)),
		}
		if percent >= diskCriticalPercent {
			critical = append(critical, subject)
		} else {
			warning = append(warning, subject)
		}
	}

	findings := make([]Finding, 0, 2)
	if len(critical) > 0 {
		findings = append(findings, Finding{
			ID:       "node:disk:critical",
			Severity: SeverityCritical,
			Category: CategoryFindingNode,
			Title:    "Node disks nearly full",
			Summary: fmt.Sprintf("%s over %.0f%% full",
				plural(len(critical), "node filesystem is", "node filesystems are"), diskCriticalPercent),
			Advice: "The kubelet evicts pods and garbage-collects images once free space crosses " +
				"its threshold, which is close now. Container logs and an unbounded emptyDir are " +
				"the usual causes, and both are recoverable without touching the workload.",
			Subjects: critical,
			Count:    len(critical),
			KindID:   nodeKindID,
		})
	}
	if len(warning) > 0 {
		findings = append(findings, Finding{
			ID:       "node:disk:warning",
			Severity: SeverityWarning,
			Category: CategoryFindingNode,
			Title:    "Node disks filling",
			Summary: fmt.Sprintf("%s over %.0f%% full",
				plural(len(warning), "node filesystem is", "node filesystems are"), diskWarnPercent),
			Advice: "Nothing has failed yet. This is the window before the kubelet starts " +
				"reclaiming, and the cheapest moment to find out what is growing.",
			Subjects: warning,
			Count:    len(warning),
			KindID:   nodeKindID,
		})
	}
	return findings
}

// pressureTitle names a pressure condition the way an operator would.
func pressureTitle(condition NodeCondition) string {
	switch condition {
	case NodeDiskPressure:
		return "Nodes out of disk"
	case NodeMemoryPressure:
		return "Nodes out of memory"
	case NodePIDPressure:
		return "Nodes out of process IDs"
	default:
		return "Nodes under pressure"
	}
}

// pressureAdvice says what each condition actually means for the workload,
// which is the half a condition name never carries.
func pressureAdvice(condition NodeCondition) string {
	switch condition {
	case NodeDiskPressure:
		return "The kubelet is garbage-collecting images and will evict pods to reclaim disk. " +
			"It is usually container logs, an emptyDir nobody bounded, or an image cache that " +
			"has never been swept — and it only reports this once it is nearly full, so the " +
			"eviction has already started."
	case NodeMemoryPressure:
		return "The kubelet is reclaiming memory and evicts BestEffort pods first, then " +
			"Burstable ones over their requests. Pods with requests matching what they use " +
			"are the last to be touched."
	case NodePIDPressure:
		return "Something on the node is forking faster than processes exit. New containers " +
			"will fail to start before existing ones are affected."
	default:
		return "The kubelet has started reclaiming resources on these nodes and will evict " +
			"BestEffort pods first."
	}
}

func nodeFindings(nodes []Node, summary NodeSummary) []Finding {
	findings := make([]Finding, 0, 4)

	var notReady, cordoned []Subject
	pressured := make(map[NodeCondition][]Subject, len(KnownPressureConditions()))
	for _, node := range nodes {
		switch {
		case !node.Ready():
			notReady = append(notReady, Subject{Kind: "Node", Name: node.Name(), Detail: node.Status()})
		default:
			// One entry per condition rather than one per node: a node that is
			// both out of disk and out of memory is two different jobs.
			for _, condition := range node.ActiveConditions() {
				pressured[condition] = append(pressured[condition], Subject{
					Kind: "Node", Name: node.Name(), Detail: conditionList(node.ActiveConditions()),
				})
			}
		}
		if node.Unschedulable() {
			cordoned = append(cordoned, Subject{Kind: "Node", Name: node.Name(), Detail: "cordoned"})
		}
	}

	if len(notReady) > 0 {
		findings = append(findings, Finding{
			ID:       "node:notready",
			Severity: SeverityCritical,
			Category: CategoryFindingNode,
			Title:    "Nodes not ready",
			Summary:  fmt.Sprintf("%s not accepting work", plural(len(notReady), "node is", "nodes are")),
			Advice: "Pods on a NotReady node keep reporting Running until the eviction timeout " +
				"expires, so the workload numbers elsewhere are optimistic until this is fixed.",
			Subjects: notReady,
			Count:    len(notReady),
			KindID:   nodeKindID,
		})
	}

	// Iterated over the known conditions rather than over the map, so the
	// order of these findings is the same on every assessment.
	for _, condition := range KnownPressureConditions() {
		subjects := pressured[condition]
		if len(subjects) == 0 {
			continue
		}
		findings = append(findings, Finding{
			ID:       "node:pressure:" + string(condition),
			Severity: SeverityWarning,
			Category: CategoryFindingNode,
			Title:    pressureTitle(condition),
			Summary: fmt.Sprintf("%s reporting %s",
				plural(len(subjects), "node is", "nodes are"), condition),
			Advice:   pressureAdvice(condition),
			Subjects: subjects,
			Count:    len(subjects),
			KindID:   nodeKindID,
		})
	}

	// Cordoned nodes are deliberate, so this is information rather than a
	// fault — but a drain somebody started and forgot is worth a line.
	if len(cordoned) > 0 {
		findings = append(findings, Finding{
			ID:       "node:cordoned",
			Severity: SeverityInfo,
			Category: CategoryFindingNode,
			Title:    "Nodes cordoned",
			Summary:  fmt.Sprintf("%s excluded from scheduling", plural(len(cordoned), "node is", "nodes are")),
			Advice:   "Healthy but unavailable to the scheduler, which reduces the headroom below.",
			Subjects: cordoned,
			Count:    len(cordoned),
			KindID:   nodeKindID,
		})
	}

	// Version skew is worth surfacing because kubelets are only supported
	// within a couple of minor versions of the API server, and a skewed
	// cluster is usually a half-finished upgrade.
	if len(summary.KubeletVersions) > 1 {
		subjects := make([]Subject, 0, len(summary.KubeletVersions))
		for version, count := range summary.KubeletVersions {
			subjects = append(subjects, Subject{
				Kind: "Node", Name: version, Detail: plural(count, "node", "nodes"),
			})
		}
		sort.SliceStable(subjects, func(i, j int) bool { return subjects[i].Name < subjects[j].Name })

		findings = append(findings, Finding{
			ID:       "node:skew",
			Severity: SeverityInfo,
			Category: CategoryFindingNode,
			Title:    "Mixed kubelet versions",
			Summary:  fmt.Sprintf("%d kubelet versions across %d nodes", len(summary.KubeletVersions), summary.Total),
			Advice:   "Usually an upgrade that stopped part-way. Kubelets are supported only within a few minor versions of the API server.",
			Subjects: subjects,
			Count:    len(summary.KubeletVersions),
			KindID:   nodeKindID,
		})
	}

	return findings
}

func capacityFindings(capacity CapacitySummary) []Finding {
	findings := make([]Finding, 0, 4)

	if capacity.CPU.Allocatable > 0 && capacity.CPU.RequestPercent() >= commitmentWarning*100 {
		findings = append(findings, Finding{
			ID:       "capacity:cpu",
			Severity: SeverityWarning,
			Category: CategoryFindingCapacity,
			Title:    "CPU headroom nearly gone",
			Summary: fmt.Sprintf("%.0f%% of allocatable CPU is already requested; %s schedulable",
				capacity.CPU.RequestPercent(), formatCPU(capacity.CPU.Schedulable())),
			Advice: "Scheduling is decided by requests, not usage — the cluster will refuse pods " +
				"at 100% of this number however idle the nodes look.",
			Count:  1,
			KindID: nodeKindID,
		})
	}

	if capacity.Memory.Allocatable > 0 && capacity.Memory.RequestPercent() >= commitmentWarning*100 {
		findings = append(findings, Finding{
			ID:       "capacity:memory",
			Severity: SeverityWarning,
			Category: CategoryFindingCapacity,
			Title:    "Memory headroom nearly gone",
			Summary: fmt.Sprintf("%.0f%% of allocatable memory is already requested; %s schedulable",
				capacity.Memory.RequestPercent(), formatBytes(capacity.Memory.Schedulable())),
			Advice: "Scheduling is decided by requests, not usage — the cluster will refuse pods " +
				"at 100% of this number however idle the nodes look.",
			Count:  1,
			KindID: nodeKindID,
		})
	}

	if capacity.Pods.Capacity > 0 && capacity.Pods.UsedPercent() >= podSlotWarning*100 {
		findings = append(findings, Finding{
			ID:       "capacity:pods",
			Severity: SeverityWarning,
			Category: CategoryFindingCapacity,
			Title:    "Pod slots nearly full",
			Summary: fmt.Sprintf("%d of %d pod slots used across schedulable nodes",
				capacity.Pods.Scheduled, capacity.Pods.Capacity),
			Advice: "Every node caps how many pods it will run regardless of CPU and memory. " +
				"Hitting that cap looks exactly like being out of resources.",
			Count:  1,
			KindID: nodeKindID,
		})
	}

	if capacity.Memory.Allocatable > 0 && capacity.Memory.LimitPercent() >= memoryOvercommit*100 {
		findings = append(findings, Finding{
			ID:       "capacity:overcommit",
			Severity: SeverityWarning,
			Category: CategoryFindingCapacity,
			Title:    "Memory limits exceed the cluster",
			Summary: fmt.Sprintf("Limits total %.0f%% of allocatable memory",
				capacity.Memory.LimitPercent()),
			Advice: "Memory cannot be compressed the way CPU can. If enough pods approach their " +
				"limits at once the kernel starts killing containers.",
			Count:  1,
			KindID: podKindID,
		})
	}

	// Waste is only interesting on a cluster that is actually reserved: a
	// mostly empty cluster is inefficient by definition and saying so is noise.
	efficiency := capacity.CPU.Efficiency()
	if efficiency >= 0 && efficiency < wasteRatio*100 && capacity.CPU.RequestPercent() >= wasteFloor*100 {
		findings = append(findings, Finding{
			ID:       "capacity:waste",
			Severity: SeverityInfo,
			Category: CategoryFindingCapacity,
			Title:    "CPU requests far exceed usage",
			Summary: fmt.Sprintf("%s requested by pods, %s actually used — %.0f%% of the reservation",
				formatCPU(capacity.CPU.Requests), formatCPU(capacity.CPU.PodUsage), efficiency),
			Advice: "The reservations are what fill the cluster, so this is capacity being paid " +
				"for and not used. Right-sizing requests recovers it without adding nodes.",
			Count:  1,
			KindID: podKindID,
		})
	}

	return findings
}

// configurationFindings reports declarations that are not failures yet.
func configurationFindings(pods []Pod, summary PodSummary) []Finding {
	if summary.BestEffort == 0 {
		return nil
	}

	subjects := make([]Subject, 0, maxSubjects)
	for _, pod := range pods {
		if pod.QoSClass() != QoSBestEffort || !pod.OccupiesNode() {
			continue
		}
		if len(subjects) >= maxSubjects {
			break
		}
		subjects = append(subjects, Subject{
			Kind:      "Pod",
			Namespace: pod.Namespace(),
			Name:      pod.Name(),
			Detail:    "no requests declared",
		})
	}
	if len(subjects) == 0 {
		return nil
	}

	return []Finding{{
		ID:       "config:besteffort",
		Severity: SeverityInfo,
		Category: CategoryFindingConfiguration,
		Title:    "Pods without resource requests",
		Summary:  fmt.Sprintf("%s running as BestEffort", plural(summary.BestEffort, "pod is", "pods are")),
		Advice: "BestEffort pods are the first thing the kubelet evicts under pressure, and the " +
			"scheduler cannot reserve anything for them.",
		Subjects: subjects,
		Count:    summary.BestEffort,
		KindID:   podKindID,
	}}
}

// restartFindings reports pods that are up now but keep dying.
//
// Nothing else in PodSteer surfaces these: the pod is Running, its containers
// are ready, and every list shows it as healthy. A container that has restarted
// forty times is nevertheless dropping requests every time it does, and the
// count is the only trace left once it comes back.
func restartFindings(pods []Pod, now time.Time) []Finding {
	subjects := make([]Subject, 0, maxSubjects)
	count := 0
	var oldest int64

	for _, pod := range pods {
		// Unhealthy pods are already reported with their actual reason;
		// repeating them here as "restarting" would be the same pod twice.
		if !pod.IsHealthy() || pod.RestartCount() < restartsOfNote {
			continue
		}

		count++
		if age := int64(pod.Age(now).Seconds()); age > oldest {
			oldest = age
		}
		if len(subjects) >= maxSubjects {
			continue
		}
		subjects = append(subjects, Subject{
			Kind:      "Pod",
			Namespace: pod.Namespace(),
			Name:      pod.Name(),
			Detail:    plural(int(pod.RestartCount()), "restart", "restarts"),
		})
	}

	if count == 0 {
		return nil
	}

	return []Finding{{
		ID:       "pod:restarts",
		Severity: SeverityWarning,
		Category: CategoryFindingWorkload,
		Title:    "Pods restarting repeatedly",
		Summary:  fmt.Sprintf("%s up now but restarted at least %d times", plural(count, "pod is", "pods are"), restartsOfNote),
		Advice: "Currently healthy, so no list flags them. Each restart is dropped traffic — the " +
			"previous container's logs say why, and an OOMKill is the usual answer.",
		Subjects:      subjects,
		Count:         count,
		KindID:        podKindID,
		OldestSeconds: oldest,
	}}
}

// eventFindings groups recent warning events by reason.
//
// Events are the cluster's own account of what went wrong and often name a
// cause no object status carries — a failed mount, a rejected admission
// webhook. They are grouped by reason and deduplicated against the objects
// already explained by a pod finding, so a crash-looping pod does not appear
// twice under both "CrashLoopBackOff" and "BackOff".
func eventFindings(events []Event, existing []Finding, now time.Time) []Finding {
	explained := make(map[string]bool, len(existing)*2)
	for _, finding := range existing {
		for _, subject := range finding.Subjects {
			explained[string(subject.Namespace)+"/"+subject.Name] = true
		}
	}

	type group struct {
		subjects []Subject
		// seen deduplicates the rows: a pod that logs the same message twice
		// is one line saying so, not the same line twice. The event count
		// above it is what states how often it happened.
		seen    map[string]bool
		count   int
		newest  time.Time
		message string
	}

	groups := make(map[string]*group)
	order := make([]string, 0, 4)

	for _, event := range events {
		if !event.IsWarning() || event.Age(now) > eventWindow {
			continue
		}
		if explained[string(event.Namespace())+"/"+event.InvolvedName()] {
			continue
		}

		current, seen := groups[event.Reason()]
		if !seen {
			current = &group{message: event.Message(), seen: make(map[string]bool, 4)}
			groups[event.Reason()] = current
			order = append(order, event.Reason())
		}

		current.count++
		if event.LastSeen().After(current.newest) {
			current.newest = event.LastSeen()
			current.message = event.Message()
		}
		row := string(event.Namespace()) + "/" + event.InvolvedName() + "\x00" + event.Message()
		if len(current.subjects) < maxSubjects && !current.seen[row] {
			current.seen[row] = true
			current.subjects = append(current.subjects, Subject{
				Kind:      event.InvolvedKind(),
				Namespace: event.Namespace(),
				Name:      event.InvolvedName(),
				Detail:    event.Message(),
			})
		}
	}

	findings := make([]Finding, 0, len(order))
	for _, reason := range order {
		current := groups[reason]
		findings = append(findings, Finding{
			ID:       "event:" + reason,
			Severity: SeverityWarning,
			Category: CategoryFindingWorkload,
			Title:    reason,
			Summary: fmt.Sprintf("%s in the last %d minutes",
				plural(current.count, "warning event", "warning events"), int(eventWindow.Minutes())),
			Advice:        truncate(current.message, 240),
			Subjects:      current.subjects,
			Count:         current.count,
			KindID:        eventKindID,
			OldestSeconds: int64(now.Sub(current.newest).Seconds()),
		})
	}
	return findings
}

// --- navigation targets ---------------------------------------------------

// Kind identifiers findings navigate to. They mirror the catalog entries and
// are built the same way ResourceKind.ID does, so a finding can be clicked
// through to the list it came from.
const (
	podKindID    = "core/v1/pods"
	nodeKindID   = "core/v1/nodes"
	eventKindID  = "core/v1/events"
	claimKindID  = "core/v1/persistentvolumeclaims"
	volumeKindID = "core/v1/persistentvolumes"
)

// workloadKindID returns the navigator id for a controller kind.
func workloadKindID(kind WorkloadKind) string {
	switch kind {
	case WorkloadDeployment:
		return "apps/v1/deployments"
	case WorkloadStatefulSet:
		return "apps/v1/statefulsets"
	case WorkloadDaemonSet:
		return "apps/v1/daemonsets"
	case WorkloadReplicaSet:
		return "apps/v1/replicasets"
	case WorkloadJob:
		return "batch/v1/jobs"
	case WorkloadCronJob:
		return "batch/v1/cronjobs"
	default:
		return podKindID
	}
}

// --- small helpers --------------------------------------------------------

func percent(part, whole int64) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(part) / float64(whole) * 100
}

func plural(count int, singular, plural string) string {
	if count == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", count, plural)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

func conditionList(conditions []NodeCondition) string {
	parts := make([]string, 0, len(conditions))
	for _, condition := range conditions {
		parts = append(parts, string(condition))
	}
	return strings.Join(parts, ", ")
}

// formatCPU renders millicores the way an operator reads them: cores above a
// core, millicores below.
// formatDuration renders a span the way the rest of PodSteer renders ages:
// one unit, the largest that is not zero. "waiting 4m" is read at a glance
// where "waiting 4m17.3s" has to be parsed.
func formatDuration(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
}

func formatCPU(milli int64) string {
	if milli >= 1000 {
		return fmt.Sprintf("%.1f cores", float64(milli)/1000)
	}
	return fmt.Sprintf("%dm", milli)
}

// formatBytes renders bytes in binary units, as Kubernetes quantities do.
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%dB", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(bytes)/float64(div), "KMGTP"[exp])
}
