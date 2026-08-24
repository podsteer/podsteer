package domain

import (
	"slices"
	"strings"
	"time"
)

// NodeCondition is a condition reported by the kubelet.
//
// Only the ones that mean something to an operator are modelled. Kubernetes
// reports several more, but a node list that surfaces every condition is a
// node list nobody reads.
type NodeCondition string

const (
	// NodeReady means the kubelet is healthy and accepting pods.
	NodeReady NodeCondition = "Ready"
	// NodeMemoryPressure means the node is low on memory.
	NodeMemoryPressure NodeCondition = "MemoryPressure"
	// NodeDiskPressure means the node is low on disk.
	NodeDiskPressure NodeCondition = "DiskPressure"
	// NodePIDPressure means the node is low on process IDs.
	NodePIDPressure NodeCondition = "PIDPressure"
	// NodeNetworkUnavailable means the node's network is misconfigured.
	NodeNetworkUnavailable NodeCondition = "NetworkUnavailable"
)

// pressureConditions are the conditions whose presence indicates a problem.
// Ready is inverted — its absence is the problem — so it is handled separately.
var pressureConditions = []NodeCondition{
	NodeMemoryPressure,
	NodeDiskPressure,
	NodePIDPressure,
	NodeNetworkUnavailable,
}

// NodeSpec carries the data needed to build a Node.
type NodeSpec struct {
	// Name is the node name. Required.
	Name string
	// ClusterID is the cluster the node belongs to. Required.
	ClusterID ClusterID
	// Roles are the node's roles, derived from its labels, e.g. "control-plane".
	Roles []string
	// Ready reports whether the Ready condition is true.
	Ready bool
	// ActiveConditions lists conditions currently reported as true, excluding
	// Ready.
	ActiveConditions []NodeCondition
	// Unschedulable reports whether the node has been cordoned.
	Unschedulable bool
	// Taints is how many taints the node carries.
	Taints int
	// BlockingTaints is how many of those actually refuse pods — the
	// NoSchedule and NoExecute effects.
	//
	// Counted apart from Taints because PreferNoSchedule refuses nothing: it
	// is a hint the scheduler may ignore, and treating a node carrying only
	// that one as reserved would understate the cluster.
	BlockingTaints int
	// KubeletVersion is the kubelet's version, e.g. "v1.32.7".
	KubeletVersion string
	// OSImage describes the host OS.
	OSImage string
	// Architecture is the node's CPU architecture, e.g. "arm64".
	Architecture string
	// InternalIP is the node's cluster-internal address.
	InternalIP string
	// Capacity is the node's total resources.
	Capacity Capacity
	// Allocatable is what remains after system reservations — the number that
	// actually governs scheduling, and therefore the one usage is measured
	// against.
	Allocatable Capacity
	// Usage is the node's current consumption, unmeasured without metrics.
	Usage Metrics
	// CreatedAt is when the node joined.
	CreatedAt time.Time
}

// Node is a cluster node as observed at a point in time.
type Node struct {
	name             string
	clusterID        ClusterID
	roles            []string
	ready            bool
	activeConditions []NodeCondition
	unschedulable    bool
	taints           int
	blockingTaints   int
	kubeletVersion   string
	osImage          string
	architecture     string
	internalIP       string
	capacity         Capacity
	allocatable      Capacity
	usage            Metrics
	filesystems      NodeFilesystems
	createdAt        time.Time
}

// NewNode validates spec and returns the corresponding Node.
func NewNode(spec NodeSpec) (Node, error) {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return Node{}, ErrEmptyResourceName
	}
	if spec.ClusterID.IsZero() {
		return Node{}, ErrEmptyClusterID
	}

	return Node{
		name:             name,
		clusterID:        spec.ClusterID,
		roles:            slices.Clone(spec.Roles),
		ready:            spec.Ready,
		activeConditions: slices.Clone(spec.ActiveConditions),
		unschedulable:    spec.Unschedulable,
		taints:           spec.Taints,
		blockingTaints:   spec.BlockingTaints,
		kubeletVersion:   spec.KubeletVersion,
		osImage:          spec.OSImage,
		architecture:     spec.Architecture,
		internalIP:       spec.InternalIP,
		capacity:         spec.Capacity,
		allocatable:      spec.Allocatable,
		usage:            spec.Usage,
		createdAt:        spec.CreatedAt.UTC(),
	}, nil
}

// Name returns the node name.
func (n Node) Name() string { return n.name }

// ClusterID returns the cluster the node belongs to.
func (n Node) ClusterID() ClusterID { return n.clusterID }

// Roles returns a copy of the node's roles.
func (n Node) Roles() []string { return slices.Clone(n.roles) }

// IsControlPlane reports whether the node runs the control plane.
func (n Node) IsControlPlane() bool {
	return slices.Contains(n.roles, "control-plane") || slices.Contains(n.roles, "master")
}

// Ready reports whether the kubelet is healthy.
func (n Node) Ready() bool { return n.ready }

// Unschedulable reports whether the node is cordoned.
func (n Node) Unschedulable() bool { return n.unschedulable }

// Taints returns how many taints the node carries.
func (n Node) Taints() int { return n.taints }

// Reserved reports a node that refuses pods which do not tolerate it.
//
// The distinction matters wherever capacity is summed: a control-plane node's
// hundred-odd pod slots are real, and unavailable to everything that has not
// been told to tolerate the taint. Counting them as free headroom overstates
// what the cluster can actually accept.
func (n Node) Reserved() bool { return n.blockingTaints > 0 }

// ActiveConditions returns a copy of the problem conditions currently true.
func (n Node) ActiveConditions() []NodeCondition { return slices.Clone(n.activeConditions) }

// KubeletVersion returns the kubelet version.
func (n Node) KubeletVersion() string { return n.kubeletVersion }

// OSImage describes the host operating system.
func (n Node) OSImage() string { return n.osImage }

// Architecture returns the node's CPU architecture.
func (n Node) Architecture() string { return n.architecture }

// InternalIP returns the node's cluster-internal address.
func (n Node) InternalIP() string { return n.internalIP }

// Capacity returns the node's total resources.
func (n Node) Capacity() Capacity { return n.capacity }

// Allocatable returns the resources available for scheduling.
func (n Node) Allocatable() Capacity { return n.allocatable }

// Usage returns the node's current consumption.
func (n Node) Usage() Metrics { return n.usage }

// WithUsage returns a copy of the node carrying the given measurement. See
// Pod.WithUsage for why this is a method rather than a rebuild at each call
// site.
func (n Node) WithUsage(usage Metrics) Node {
	n.usage = usage
	return n
}

// Filesystems returns how full the node's disks are, if a kubelet said.
func (n Node) Filesystems() NodeFilesystems { return n.filesystems }

// WithFilesystems returns the node carrying its measured disk occupancy.
//
// Attached rather than constructed with the node for the same reason usage is:
// it comes from a different endpoint, needs a different permission, and is
// routinely absent.
func (n Node) WithFilesystems(filesystems NodeFilesystems) Node {
	n.filesystems = filesystems
	return n
}

// CreatedAt returns when the node joined, in UTC.
func (n Node) CreatedAt() time.Time { return n.createdAt }

// Age returns how long the node has been in the cluster.
func (n Node) Age(now time.Time) time.Duration {
	if n.createdAt.IsZero() {
		return 0
	}
	return now.Sub(n.createdAt)
}

// Status is the single word a node list should show.
//
// Cordoned wins over Ready because it is the more actionable fact: a cordoned
// node is healthy but deliberately excluded, and showing "Ready" would hide a
// drain somebody started and forgot to finish.
func (n Node) Status() string {
	switch {
	case !n.ready:
		return "NotReady"
	case n.unschedulable:
		return "SchedulingDisabled"
	case len(n.activeConditions) > 0:
		return string(n.activeConditions[0])
	default:
		return "Ready"
	}
}

// IsHealthy reports whether the node is ready and under no pressure.
//
// A cordoned node is still healthy — it was taken out of service on purpose,
// which is not a fault.
func (n Node) IsHealthy() bool {
	return n.ready && len(n.activeConditions) == 0
}

// CPUPercent returns CPU usage against allocatable.
func (n Node) CPUPercent() float64 { return n.allocatable.CPUPercent(n.usage) }

// MemoryPercent returns memory usage against allocatable.
func (n Node) MemoryPercent() float64 { return n.allocatable.MemoryPercent(n.usage) }

// KnownPressureConditions returns the conditions PodSteer treats as problems.
func KnownPressureConditions() []NodeCondition { return slices.Clone(pressureConditions) }
