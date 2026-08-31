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
	// pod list shows. PodSteer promotes it to a phase of its own because that
	// is how an operator actually thinks about the pod.
	PodPhaseTerminating PodPhase = "Terminating"
)

// NewPodPhase maps a raw API phase onto the known set.
//
// An unrecognised phase degrades to PodPhaseUnknown instead of failing: a pod
// PodSteer cannot fully classify is still worth showing to the operator, and a
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
	// Requests is what the container reserves on its node.
	Requests Resources
	// Limits is the ceiling the kubelet enforces.
	Limits Resources
	// Started distinguishes a container the startup probe has passed from one
	// still starting. Separate from Ready deliberately: started=true with
	// ready=false is a readiness problem, started=false is a startup problem,
	// and they are investigated in different places.
	Started bool
	// LastTermination is how this container's previous life ended, when there
	// was one. See Termination — it is the only record of it that exists.
	LastTermination Termination
	// ImageID is the reference the runtime actually resolved, digest and all
	// — "…/app@sha256:abc…" — as distinct from Image, which is the reference
	// somebody wrote. Two pods can name the same tag and hold different
	// digests, and this is the only field that says so.
	ImageID string
	// Usage is what this container is measuring right now, when anything
	// measured it. The pod's total is the sum of these, and this is the half
	// that says which container the total came from.
	Usage Metrics
	// StartedAt is when the CURRENT life began, empty when it is not running.
	// The difference between it and now is how long this container has been
	// up, which at the start of a life is how long it took to come up.
	StartedAt time.Time
	// Liveness and Startup are the probes whose timings decide whether a slow
	// container gets killed for being slow. Readiness is deliberately absent:
	// failing it takes a pod out of a Service, which is a different and
	// non-fatal problem, and nothing here reasons about it yet.
	Liveness Probe
	Startup  Probe
}

// Probe is a container health check, reduced to the timings that matter.
//
// Only the numbers, not the handler: whether it is an HTTP GET or an exec is
// a presentation detail the frontend renders from the manifest, while the
// arithmetic below is a judgement about whether the configuration will kill a
// container that was merely slow.
type Probe struct {
	InitialDelaySeconds int32
	PeriodSeconds       int32
	TimeoutSeconds      int32
	FailureThreshold    int32
}

// IsZero reports whether no probe is configured.
func (p Probe) IsZero() bool { return p == Probe{} }

// KillsAfter is how long a container has to become healthy before this probe
// starts restarting it.
//
// initialDelay plus one full run of the failure threshold — the probe waits,
// then has to fail that many times in a row, each a period apart. This is the
// number nobody computes and everybody is surprised by: a 30s delay with the
// default threshold of 3 and period of 10 is not a 30-second grace, it is a
// minute.
func (p Probe) KillsAfter() time.Duration {
	if p.IsZero() {
		return 0
	}

	period := p.PeriodSeconds
	if period <= 0 {
		period = 10 // The API server's default.
	}
	threshold := p.FailureThreshold
	if threshold <= 0 {
		threshold = 3 // Likewise.
	}

	return time.Duration(p.InitialDelaySeconds)*time.Second +
		time.Duration(period*threshold)*time.Second
}

// Resources is a container's declared CPU and memory, in the same units as
// Metrics so the two can be compared directly.
//
// Requests and limits are what the *scheduler* works with, and they are a
// different question from what a pod actually uses: a cluster can be full —
// nothing more will schedule — while sitting at 8% real utilisation, because
// scheduling is decided by requests alone. Carrying both is what lets PodSteer
// say that out loud instead of showing a reassuring usage bar.
type Resources struct {
	// CPUMilli is CPU in millicores. 1000 = one core.
	CPUMilli int64
	// MemoryBytes is memory in bytes.
	MemoryBytes int64
	// EphemeralBytes is scratch disk on the node itself — the container
	// writable layer, emptyDir volumes and logs. Declaring it is rare, which
	// is exactly why it is worth reporting: a node whose disk fills has no
	// reservation to fall back on and the kubelet starts evicting.
	EphemeralBytes int64
}

// IsZero reports whether nothing was declared.
func (r Resources) IsZero() bool { return r == Resources{} }

// CPUPercent returns usage as a percentage of this declaration, or 0 when
// nothing was declared or nothing was measured.
//
// THE RESULT IS NOT CAPPED AT 100, and that is the point. A percentage of a
// *capacity* cannot exceed it; a percentage of a *request* routinely does,
// because a request is a reservation rather than a ceiling and a Burstable
// pod is entitled to climb above its own. Clamping here would quietly report
// a pod running at three times its reservation as sitting neatly at 100%,
// which is the one reading that would make anybody stop looking.
func (r Resources) CPUPercent(usage Metrics) float64 {
	if r.CPUMilli <= 0 || !usage.Measured {
		return 0
	}
	return float64(usage.CPUMilli) / float64(r.CPUMilli) * 100
}

// MemoryPercent returns usage as a percentage of this declaration. See
// CPUPercent for why it is not capped.
func (r Resources) MemoryPercent(usage Metrics) float64 {
	if r.MemoryBytes <= 0 || !usage.Measured {
		return 0
	}
	return float64(usage.MemoryBytes) / float64(r.MemoryBytes) * 100
}

// Add returns the sum of two declarations.
func (r Resources) Add(other Resources) Resources {
	return Resources{
		CPUMilli:       r.CPUMilli + other.CPUMilli,
		MemoryBytes:    r.MemoryBytes + other.MemoryBytes,
		EphemeralBytes: r.EphemeralBytes + other.EphemeralBytes,
	}
}

// PodSpec carries the data needed to build a Pod. See ClusterSpec for why the
// constructor takes a struct rather than positional parameters.
type PodSpec struct {
	// UID is the Kubernetes object UID. Optional: it is absent on objects
	// synthesised in tests, and Pod identity within PodSteer is namespace+name.
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
	// Reason is the pod-level reason the API server reports, e.g. "Evicted"
	// or "NodeAffinity". It sits beside the container reasons rather than
	// replacing them: an evicted pod has no container state left to explain
	// itself with.
	Reason string
	// Message is the pod-level explanation, most usefully the scheduler's
	// account of why a pod will not fit — "0/6 nodes are available: 6
	// Insufficient cpu". Nothing else in the API says why a pod is Pending.
	Message string
	// CreatedAt is the object creation timestamp.
	CreatedAt time.Time
	// DeletedAt is when deletion was REQUESTED, not when it happened. A pod
	// with this set is Terminating; how long ago it was set is how long
	// something has been refusing to let go.
	DeletedAt time.Time
	// Finalizers are the names still registered against the deletion. Empty
	// on a healthy pod, and the whole explanation on a stuck one.
	Finalizers []string
	// Conditions are the pod's own status conditions, with their reasons.
	Conditions []PodCondition
}

// PodCondition is one of a pod's status conditions.
//
// Carried with its Reason and Message, which `kubectl describe` discards — it
// prints Conditions as Type and Status only. That loses the half that
// explains anything: PodScheduled=False is a fact, and its Reason and Message
// are the scheduler's account of WHY, which is the longest and least
// truncated explanation the API offers.
type PodCondition struct {
	Type    string
	Status  string
	Reason  string
	Message string
}

// True reports whether the condition is satisfied.
func (c PodCondition) True() bool { return c.Status == "True" }

// Pod is a Kubernetes pod as observed in a cluster at a point in time.
//
// Within PodSteer a pod is identified by the triple (cluster, namespace, name).
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
	reason     string
	message    string
	createdAt  time.Time
	deletedAt  time.Time
	finalizers []string
	conditions []PodCondition
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
		reason:     strings.TrimSpace(spec.Reason),
		message:    strings.TrimSpace(spec.Message),
		createdAt:  spec.CreatedAt.UTC(),
		deletedAt:  spec.DeletedAt.UTC(),
		finalizers: slices.Clone(spec.Finalizers),
		conditions: slices.Clone(spec.Conditions),
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

// WithPodUsage returns a copy carrying the pod's total AND each container's.
//
// Both halves together, because they are read together: a pod near its memory
// limit is a question, and which container is holding the memory is the
// answer. Splitting this into two calls would let the two drift apart on a
// pod whose containers changed between them.
//
// A container the metrics API did not report keeps whatever it had, which is
// nothing — normal for one that has not started, and not something to
// represent as a measured zero.
func (p Pod) WithPodUsage(usage PodUsage) Pod {
	p.usage = usage.Total

	if len(usage.Containers) > 0 {
		containers := make([]Container, len(p.containers))
		copy(containers, p.containers)
		for i := range containers {
			if measured, ok := usage.Containers[containers[i].Name]; ok {
				containers[i].Usage = measured
			}
		}
		p.containers = containers
	}

	return p
}

// WithUsage returns a copy of the pod carrying the given measurement.
//
// Usage arrives from a different API than the pod does, so it cannot be part
// of the object the adapter builds. This exists so the join does not mean
// re-listing every field at the call site: doing that by hand silently drops
// whatever field was added since it was written, which is a bug that compiles.
func (p Pod) WithUsage(usage Metrics) Pod {
	p.usage = usage
	return p
}

// Finalizers returns the names still registered against this pod's deletion.
//
// THE FIELD THAT EXPLAINS A STUCK TERMINATING POD, and `kubectl describe`
// never prints it. It shows "Terminating (lasts 30s)" and then says nothing
// about why thirty seconds became three hours — while the answer, a
// controller that registered a finalizer and has not removed it, is right
// there in the object everybody is already looking at.
func (p Pod) Finalizers() []string { return slices.Clone(p.finalizers) }

// Condition returns the named status condition, and whether it was reported.
func (p Pod) Condition(name string) (PodCondition, bool) {
	for _, condition := range p.conditions {
		if condition.Type == name {
			return condition, true
		}
	}
	return PodCondition{}, false
}

// Conditions returns a copy of the pod's status conditions.
func (p Pod) Conditions() []PodCondition { return slices.Clone(p.conditions) }

// Terminating reports whether deletion has been requested.
func (p Pod) Terminating() bool { return !p.deletedAt.IsZero() }

// DeletingFor is how long deletion has been pending.
func (p Pod) DeletingFor(now time.Time) time.Duration {
	if p.deletedAt.IsZero() {
		return 0
	}
	if elapsed := now.Sub(p.deletedAt); elapsed > 0 {
		return elapsed
	}
	return 0
}

// Reason returns the pod-level reason, empty when the API server gives none.
func (p Pod) Reason() string { return p.reason }

// Message returns the pod-level explanation, empty when there is none.
func (p Pod) Message() string { return p.message }

// Requests returns the sum of every container's requests — what this pod
// reserves on its node, and therefore what it costs the scheduler.
func (p Pod) Requests() Resources {
	var total Resources
	for _, container := range p.containers {
		total = total.Add(container.Requests)
	}
	return total
}

// CPUPercent returns measured CPU usage as a percentage of what this pod
// RESERVED — not of what it is capped at, and not of its node.
//
// Requests are the denominator because they are the number the rest of
// PodSteer is built around: they decide whether a pod schedules, and "how
// much of what you reserved are you actually using" is the question a pod
// list can answer that `kubectl top` cannot. Limits were the alternative and
// answer a different question — how close this pod is to being throttled —
// but they are left unset on most clusters, so a limit-based column would be
// blank for the majority of rows.
//
// Zero when the pod declares no CPU request; callers must test HasRequests
// rather than reading zero as "idle". A BestEffort pod has no reservation to
// be a proportion of, which is worth showing as absent rather than as 0%.
func (p Pod) CPUPercent() float64 { return p.Requests().CPUPercent(p.usage) }

// MemoryPercent returns measured memory usage against the pod's memory
// request. See CPUPercent.
func (p Pod) MemoryPercent() float64 { return p.Requests().MemoryPercent(p.usage) }

// Limits returns the sum of every container's limits.
func (p Pod) Limits() Resources {
	var total Resources
	for _, container := range p.containers {
		total = total.Add(container.Limits)
	}
	return total
}

// CPULimitPercent returns measured CPU usage as a percentage of the pod's
// CPU limit — how close it is to being THROTTLED.
//
// Throttling is not a lesser problem than being killed, only a different one,
// and it is the one nothing else in Kubernetes will tell you about. A pod
// pinned at its CPU limit does not fail, restart or raise an event: it simply
// takes three times as long, and a job that used to finish in ten minutes
// takes half an hour with every probe still green. That is worth colouring.
//
// It is still not the SAME kind of number as MemoryLimitPercent, which is why
// only the memory one raises a finding. Exceeding a CPU limit is enforced
// pre-emptively by the kernel and the container keeps running; exceeding a
// memory limit is enforced reactively by the OOM killer and it dies. Both are
// worth seeing on a row; only one is worth interrupting somebody over.
//
// Zero when no CPU limit is declared, which is the majority case — leaving
// CPU unbounded is deliberate practice, not an oversight. Callers must test
// for the limit rather than reading zero as "idle".
func (p Pod) CPULimitPercent() float64 { return p.Limits().CPUPercent(p.usage) }

// MemoryLimitPercent returns measured memory usage as a percentage of the
// pod's memory limit — how close it is to being OOMKilled.
//
// This is the one pod-level ratio that predicts a hard failure, which is why
// it is the one the overview raises a finding on. See CPULimitPercent for why
// its CPU twin is not treated the same way.
func (p Pod) MemoryLimitPercent() float64 { return p.Limits().MemoryPercent(p.usage) }

// IsScheduled reports whether the pod has been placed on a node.
func (p Pod) IsScheduled() bool { return p.nodeName != "" }

// OccupiesNode reports whether the pod is holding node capacity.
//
// Terminal pods are excluded: a Succeeded or Failed pod still exists as an
// object, but its containers are gone and its requests no longer count
// against anything. Summing them is the classic way to compute a cluster
// utilisation figure that is quietly wrong on any cluster running Jobs.
func (p Pod) OccupiesNode() bool {
	return p.IsScheduled() && !p.phase.IsTerminal()
}

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
