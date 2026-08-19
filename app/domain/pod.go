package domain

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// PodPhase is the high-level lifecycle phase of a pod.
type PodPhase string

const (
	// PodPhasePending means the pod is accepted but not all containers run yet.
	PodPhasePending PodPhase = "Pending"
	// PodPhaseRunning means the pod is bound to a node and at least one
	// container is running or restarting.
	PodPhaseRunning PodPhase = "Running"
	// PodPhaseSucceeded means every container terminated successfully.
	PodPhaseSucceeded PodPhase = "Succeeded"
	// PodPhaseFailed means every container terminated and at least one failed.
	PodPhaseFailed PodPhase = "Failed"
	// PodPhaseUnknown means the phase could not be determined, typically
	// because the node hosting the pod is unreachable.
	PodPhaseUnknown PodPhase = "Unknown"

	// PodPhaseTerminating means the pod has been marked for deletion and is
	// draining.
	//
	// Kubernetes has no such phase: a terminating pod keeps reporting Running
	// until it disappears, which is the single most confusing thing a naive
	// pod list shows. K8Sense promotes it to a phase of its own because that
	// is how an operator actually thinks about the pod.
	PodPhaseTerminating PodPhase = "Terminating"
)

// NewPodPhase maps a raw API phase onto the known set.
//
// An unrecognised phase degrades to PodPhaseUnknown instead of failing: a pod
// K8Sense cannot fully classify is still worth showing to the operator, and a
// future Kubernetes release must not make the pod list unreadable.
func NewPodPhase(raw string) PodPhase {
	switch PodPhase(strings.TrimSpace(raw)) {
	case PodPhasePending:
		return PodPhasePending
	case PodPhaseRunning:
		return PodPhaseRunning
	case PodPhaseSucceeded:
		return PodPhaseSucceeded
	case PodPhaseFailed:
		return PodPhaseFailed
	case PodPhaseTerminating:
		return PodPhaseTerminating
	default:
		return PodPhaseUnknown
	}
}

// IsTerminal reports whether the pod has reached a phase it cannot leave.
func (p PodPhase) IsTerminal() bool {
	return p == PodPhaseSucceeded || p == PodPhaseFailed
}

// ContainerState is the current state of a single container.
type ContainerState string

const (
	// ContainerStateWaiting means the container is not running yet, e.g. it is
	// pulling its image or is in CrashLoopBackOff.
	ContainerStateWaiting ContainerState = "Waiting"
	// ContainerStateRunning means the container process is executing.
	ContainerStateRunning ContainerState = "Running"
	// ContainerStateTerminated means the container process has exited.
	ContainerStateTerminated ContainerState = "Terminated"
	// ContainerStateUnknown means no state was reported.
	ContainerStateUnknown ContainerState = "Unknown"
)

// Container is one container within a pod, as observed at a point in time.
//
// Unlike Pod this is a plain value object: it has no identity of its own and
// no invariant beyond a non-empty name, which NewPod checks when assembling
// the pod.
type Container struct {
	// Name is the container name, unique within its pod.
	Name string
	// Image is the fully qualified image reference.
	Image string
	// Ready reports whether the container passes its readiness probe.
	Ready bool
	// RestartCount is how many times the kubelet has restarted the container.
	RestartCount int32
	// State is the container's current state.
	State ContainerState
	// Reason explains the state when the API server supplies one, e.g.
	// "CrashLoopBackOff", "ImagePullBackOff" or "Completed". It is the single
	// most useful field when diagnosing a pod that will not start, so it is
	// carried all the way to the UI.
	Reason string
}

// PodSpec carries the data needed to build a Pod. See ClusterSpec for why the
// constructor takes a struct rather than positional parameters.
type PodSpec struct {
	// UID is the Kubernetes object UID. Optional: it is absent on objects
	// synthesised in tests, and Pod identity within K8Sense is namespace+name.
	UID string
	// Name is the pod name. Required.
	Name string
	// Namespace is the pod's namespace. Required.
	Namespace NamespaceName
	// ClusterID is the cluster the pod was read from. Required, so that a pod
	// remains attributable once several clusters' pods coexist in the UI.
	ClusterID ClusterID
	// Phase is the pod lifecycle phase.
	Phase PodPhase
	// NodeName is the node the pod is scheduled on, empty while unscheduled.
	NodeName string
	// PodIP is the pod's cluster IP, empty before it is assigned.
	PodIP string
	// Containers are the pod's containers.
	Containers []Container
	// Labels are the pod's labels.
	Labels map[string]string
	// Owners are the pod's owner references. The controlling one is what the
	// "Controlled By" column shows.
	Owners []OwnerReference
	// QoSClass is the scheduling quality-of-service class Kubernetes assigned:
	// Guaranteed, Burstable or BestEffort. It is derived from the pod's
	// requests and limits, and decides what gets evicted first under pressure.
	QoSClass QoSClass
	// Usage is the pod's measured resource consumption, unmeasured on a
	// cluster without metrics-server.
	Usage Metrics
	// CreatedAt is the object creation timestamp.
	CreatedAt time.Time
}

// Pod is a Kubernetes pod as observed in a cluster at a point in time.
//
// Within K8Sense a pod is identified by the triple (cluster, namespace, name).
// The value is a read-only snapshot: it is never written back to the cluster,
// so it carries observed status rather than desired spec.
type Pod struct {
	uid        string
	name       string
	namespace  NamespaceName
	clusterID  ClusterID
	phase      PodPhase
	nodeName   string
	podIP      string
	containers []Container
	labels     map[string]string
	owners     []OwnerReference
	qosClass   QoSClass
	usage      Metrics
	createdAt  time.Time
}

// NewPod validates spec and returns the corresponding Pod.
//
// Slice and map inputs are defensively copied so that the caller — in practice
// an adapter reusing a buffer while translating a list response — cannot
// mutate the pod after construction.
func NewPod(spec PodSpec) (Pod, error) {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return Pod{}, ErrEmptyPodName
	}
	if spec.Namespace.IsAll() {
		return Pod{}, fmt.Errorf("pod %q: %w: namespace must not be empty",
			name, ErrInvalidNamespaceName)
	}
	if spec.ClusterID.IsZero() {
		return Pod{}, fmt.Errorf("pod %q: %w", name, ErrEmptyClusterID)
	}

	containers := make([]Container, 0, len(spec.Containers))
	for _, container := range spec.Containers {
		if strings.TrimSpace(container.Name) == "" {
			return Pod{}, fmt.Errorf("pod %q: %w", name, ErrEmptyContainerName)
		}
		containers = append(containers, container)
	}

	phase := spec.Phase
	if phase == "" {
		phase = PodPhaseUnknown
	}

	return Pod{
		uid:        spec.UID,
		name:       name,
		namespace:  spec.Namespace,
		clusterID:  spec.ClusterID,
		phase:      phase,
		nodeName:   spec.NodeName,
		podIP:      spec.PodIP,
		containers: containers,
		labels:     maps.Clone(spec.Labels),
		owners:     slices.Clone(spec.Owners),
		qosClass:   spec.QoSClass,
		usage:      spec.Usage,
		createdAt:  spec.CreatedAt.UTC(),
	}, nil
}

// UID returns the Kubernetes object UID, which may be empty.
func (p Pod) UID() string { return p.uid }

// Name returns the pod name.
func (p Pod) Name() string { return p.name }

// Namespace returns the pod's namespace.
func (p Pod) Namespace() NamespaceName { return p.namespace }

// ClusterID returns the cluster the pod was read from.
func (p Pod) ClusterID() ClusterID { return p.clusterID }

// Phase returns the pod lifecycle phase.
func (p Pod) Phase() PodPhase { return p.phase }

// NodeName returns the node the pod runs on, empty while unscheduled.
func (p Pod) NodeName() string { return p.nodeName }

// PodIP returns the pod's cluster IP, empty before it is assigned.
func (p Pod) PodIP() string { return p.podIP }

// Containers returns a copy of the pod's containers, preserving immutability.
func (p Pod) Containers() []Container {
	return append([]Container(nil), p.containers...)
}

// Labels returns a copy of the pod's labels, preserving immutability.
func (p Pod) Labels() map[string]string { return maps.Clone(p.labels) }

// Owners returns a copy of the pod's owner references.
func (p Pod) Owners() []OwnerReference { return slices.Clone(p.owners) }

// Controller returns the owner that controls this pod, or the zero value for
// a bare pod nothing will recreate.
func (p Pod) Controller() OwnerReference { return Controller(p.owners) }

// QoSClass returns the scheduling quality-of-service class.
func (p Pod) QoSClass() QoSClass { return p.qosClass }

// Usage returns the pod's measured resource consumption.
func (p Pod) Usage() Metrics { return p.usage }

// CreatedAt returns the creation timestamp in UTC.
func (p Pod) CreatedAt() time.Time { return p.createdAt }

// TotalContainers returns how many containers the pod declares.
func (p Pod) TotalContainers() int { return len(p.containers) }

// ReadyContainers returns how many containers currently pass readiness.
func (p Pod) ReadyContainers() int {
	ready := 0
	for _, container := range p.containers {
		if container.Ready {
			ready++
		}
	}
	return ready
}

// RestartCount returns the pod's total restarts across all containers, which
// is the number `kubectl get pods` shows in its RESTARTS column.
func (p Pod) RestartCount() int32 {
	var total int32
	for _, container := range p.containers {
		total += container.RestartCount
	}
	return total
}

// IsReady reports whether every container in the pod passes readiness.
//
// A pod with no containers is not ready: it cannot be serving anything.
func (p Pod) IsReady() bool {
	return len(p.containers) > 0 && p.ReadyContainers() == len(p.containers)
}

// IsHealthy reports whether the pod is doing what it is supposed to be doing.
//
// A completed one-shot pod counts as healthy — it finished successfully, and
// flagging it would drown the operator in false positives on any cluster that
// runs Jobs. A Running pod is healthy only once all its containers are ready,
// which is what distinguishes a serving pod from one stuck in CrashLoopBackOff
// while still reporting phase Running.
func (p Pod) IsHealthy() bool {
	switch p.phase {
	case PodPhaseSucceeded:
		return true
	case PodPhaseRunning:
		return p.IsReady()
	default:
		return false
	}
}

// StatusReason returns the most informative explanation of why the pod is not
// simply running, or "" when nothing is wrong.
//
// This is the field that turns a pod list into a diagnosis. A pod stuck in
// ImagePullBackOff or CrashLoopBackOff still reports phase Pending or Running,
// and the actual cause sits in one container's state — so the phase alone
// tells the operator nothing. Terminated containers are considered too, since
// a pod whose main container exited non-zero is the other common case.
//
// "Completed" is not a problem and is deliberately not reported.
func (p Pod) StatusReason() string {
	for _, container := range p.containers {
		if container.Reason == "" || container.Reason == "Completed" {
			continue
		}
		if container.State == ContainerStateWaiting || container.State == ContainerStateTerminated {
			return container.Reason
		}
	}
	return ""
}

// Age returns how long the pod has existed relative to now. See Namespace.Age
// for why the reference time is a parameter.
func (p Pod) Age(now time.Time) time.Duration {
	if p.createdAt.IsZero() {
		return 0
	}
	return now.Sub(p.createdAt)
}

// QoSClass is the quality-of-service class the scheduler assigned to a pod.
//
// It is derived by Kubernetes from the pod's resource requests and limits, and
// it decides eviction order under node pressure — which makes it one of the
// few pieces of static metadata worth a column in a list.
type QoSClass string

const (
	// QoSGuaranteed pods have equal requests and limits on every container.
	// They are evicted last.
	QoSGuaranteed QoSClass = "Guaranteed"
	// QoSBurstable pods request less than their limit.
	QoSBurstable QoSClass = "Burstable"
	// QoSBestEffort pods set neither requests nor limits. They are evicted
	// first, which is why an unexpected BestEffort in production is worth
	// noticing.
	QoSBestEffort QoSClass = "BestEffort"
	// QoSUnknown covers anything unrecognised.
	QoSUnknown QoSClass = ""
)

// NewQoSClass maps a raw API value onto the known set.
func NewQoSClass(raw string) QoSClass {
	switch QoSClass(strings.TrimSpace(raw)) {
	case QoSGuaranteed:
		return QoSGuaranteed
	case QoSBurstable:
		return QoSBurstable
	case QoSBestEffort:
		return QoSBestEffort
	default:
		return QoSUnknown
	}
}
