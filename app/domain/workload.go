package domain

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// WorkloadKind is the kind of a controller that manages pods.
type WorkloadKind string

const (
	// WorkloadDeployment manages ReplicaSets, which manage pods.
	WorkloadDeployment WorkloadKind = "Deployment"
	// WorkloadStatefulSet manages pods with stable identities.
	WorkloadStatefulSet WorkloadKind = "StatefulSet"
	// WorkloadDaemonSet runs one pod per matching node.
	WorkloadDaemonSet WorkloadKind = "DaemonSet"
	// WorkloadReplicaSet maintains a pod count.
	WorkloadReplicaSet WorkloadKind = "ReplicaSet"
	// WorkloadJob runs pods to completion.
	WorkloadJob WorkloadKind = "Job"
	// WorkloadCronJob creates Jobs on a schedule.
	WorkloadCronJob WorkloadKind = "CronJob"
)

// WorkloadKinds returns every controller kind, in the order a navigator shows
// them: what most clusters run most of, first.
func WorkloadKinds() []WorkloadKind {
	return []WorkloadKind{
		WorkloadDeployment,
		WorkloadStatefulSet,
		WorkloadDaemonSet,
		WorkloadReplicaSet,
		WorkloadJob,
		WorkloadCronJob,
	}
}

// WorkloadSpec carries the data needed to build a Workload.
//
// A single type covers all six controller kinds because their *list* view is
// the same question in every case — how many should be running, how many are —
// and six near-identical entities would mean six near-identical mappers and
// six near-identical tables. The kind-specific fields below are simply unset
// for kinds that do not have them.
type WorkloadSpec struct {
	// Kind is the controller kind. Required.
	Kind WorkloadKind
	// Name is the controller name. Required.
	Name string
	// Namespace is the controller's namespace. Required.
	Namespace NamespaceName
	// ClusterID is the cluster it was read from. Required.
	ClusterID ClusterID
	// Desired is the requested replica count. For a DaemonSet this is the
	// number of nodes it should be scheduled on.
	Desired int32
	// Ready is how many replicas pass readiness.
	Ready int32
	// Current is how many replicas exist.
	Current int32
	// Updated is how many run the current template.
	Updated int32
	// Available is how many have been ready long enough to count. For a Job
	// this is how many pods are still running.
	Available int32
	// Failed is how many pods have terminated unsuccessfully. Only a Job
	// reports it; it is zero for every other kind.
	Failed int32
	// Images are the container images of the pod template.
	Images []string
	// Selector is the label selector, for finding the pods it manages.
	Selector map[string]string
	// Labels are the controller's own labels.
	Labels map[string]string
	// Annotations are the controller's annotations.
	//
	// Adapters are expected to pass only the ones a consumer needs rather
	// than everything: a real deployment's annotations are dominated by
	// kubectl's last-applied-configuration, which on this project's own test
	// cluster is 239 KiB across sixty-one deployments and would be re-sent on
	// every refresh to serve one column.
	Annotations map[string]string
	// Owner is the controlling owner, e.g. the Deployment behind a ReplicaSet.
	Owner OwnerReference
	// Suspended reports whether a Job or CronJob is suspended.
	Suspended bool
	// Schedule is a CronJob's cron expression.
	Schedule string
	// LastScheduled is when a CronJob last created a Job.
	LastScheduled time.Time
	// CreatedAt is the object creation timestamp.
	CreatedAt time.Time
}

// Workload is a pod-managing controller as observed at a point in time.
type Workload struct {
	kind          WorkloadKind
	name          string
	namespace     NamespaceName
	clusterID     ClusterID
	desired       int32
	ready         int32
	current       int32
	updated       int32
	available     int32
	failed        int32
	images        []string
	selector      map[string]string
	labels        map[string]string
	annotations   map[string]string
	owner         OwnerReference
	suspended     bool
	schedule      string
	lastScheduled time.Time
	createdAt     time.Time
}

// NewWorkload validates spec and returns the corresponding Workload.
func NewWorkload(spec WorkloadSpec) (Workload, error) {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return Workload{}, ErrEmptyResourceName
	}
	if spec.Namespace.IsAll() {
		return Workload{}, fmt.Errorf("%s %q: %w: namespace must not be empty",
			spec.Kind, name, ErrInvalidNamespaceName)
	}
	if spec.ClusterID.IsZero() {
		return Workload{}, fmt.Errorf("%s %q: %w", spec.Kind, name, ErrEmptyClusterID)
	}
	if spec.Kind == "" {
		return Workload{}, fmt.Errorf("%w: workload %q has no kind", ErrInvalidResourceKind, name)
	}

	return Workload{
		kind:          spec.Kind,
		name:          name,
		namespace:     spec.Namespace,
		clusterID:     spec.ClusterID,
		desired:       spec.Desired,
		ready:         spec.Ready,
		current:       spec.Current,
		updated:       spec.Updated,
		available:     spec.Available,
		failed:        spec.Failed,
		images:        slices.Clone(spec.Images),
		selector:      maps.Clone(spec.Selector),
		labels:        maps.Clone(spec.Labels),
		annotations:   maps.Clone(spec.Annotations),
		owner:         spec.Owner,
		suspended:     spec.Suspended,
		schedule:      spec.Schedule,
		lastScheduled: spec.LastScheduled.UTC(),
		createdAt:     spec.CreatedAt.UTC(),
	}, nil
}

// Kind returns the controller kind.
func (w Workload) Kind() WorkloadKind { return w.kind }

// Name returns the controller name.
func (w Workload) Name() string { return w.name }

// Namespace returns the controller's namespace.
func (w Workload) Namespace() NamespaceName { return w.namespace }

// ClusterID returns the cluster it was read from.
func (w Workload) ClusterID() ClusterID { return w.clusterID }

// Desired returns the requested replica count.
func (w Workload) Desired() int32 { return w.desired }

// Ready returns how many replicas pass readiness.
func (w Workload) Ready() int32 { return w.ready }

// Current returns how many replicas exist.
func (w Workload) Current() int32 { return w.current }

// Updated returns how many replicas run the current template.
func (w Workload) Updated() int32 { return w.updated }

// Available returns how many replicas have been ready long enough to count.
func (w Workload) Available() int32 { return w.available }

// WithSuspension returns a copy of the workload marked suspended.
//
// Suspension is read from the spec while everything else comes from the
// status, so it is applied after construction. A copy method rather than a
// rebuild at the call site: re-listing every field by hand silently drops
// whichever one was added since it was written.
func (w Workload) WithSuspension(suspended bool) Workload {
	w.suspended = suspended
	return w
}

// Failed returns how many of a Job's pods terminated unsuccessfully.
func (w Workload) Failed() int32 { return w.failed }

// Images returns a copy of the pod template's container images.
func (w Workload) Images() []string { return slices.Clone(w.images) }

// Selector returns a copy of the label selector.
func (w Workload) Selector() map[string]string { return maps.Clone(w.selector) }

// Labels returns a copy of the controller's labels.
func (w Workload) Labels() map[string]string { return maps.Clone(w.labels) }

// Annotations returns a copy of the controller's annotations, which adapters
// populate selectively. See WorkloadSpec.Annotations.
func (w Workload) Annotations() map[string]string { return maps.Clone(w.annotations) }

// Owner returns the controlling owner, if any.
func (w Workload) Owner() OwnerReference { return w.owner }

// Suspended reports whether a Job or CronJob is suspended.
func (w Workload) Suspended() bool { return w.suspended }

// Schedule returns a CronJob's cron expression.
func (w Workload) Schedule() string { return w.schedule }

// LastScheduled returns when a CronJob last created a Job.
func (w Workload) LastScheduled() time.Time { return w.lastScheduled }

// CreatedAt returns the creation timestamp in UTC.
func (w Workload) CreatedAt() time.Time { return w.createdAt }

// Age returns how long the controller has existed.
func (w Workload) Age(now time.Time) time.Duration {
	if w.createdAt.IsZero() {
		return 0
	}
	return now.Sub(w.createdAt)
}

// IsRolling reports whether a rollout is in progress.
//
// Some replicas still run the previous template. This is the state where an
// operator wants to watch rather than intervene, and distinguishing it from
// "degraded" is the difference between a normal deploy and an incident.
func (w Workload) IsRolling() bool {
	return w.desired > 0 && w.updated < w.desired
}

// IsHealthy reports whether the workload is doing what it was asked to.
//
// A scaled-to-zero workload is healthy: zero replicas were requested and zero
// are running. Treating it as unhealthy would flag every intentionally
// disabled CronJob and every deployment scaled down for the weekend.
//
// A suspended Job or CronJob is likewise healthy — suspension is deliberate.
// A Job is judged by whether it failed, not by whether it has finished:
// "0 of 1 completions" describes a job that started ten seconds ago exactly as
// it describes one that will never finish, and treating the first as unhealthy
// flags every batch run on the cluster.
func (w Workload) IsHealthy() bool {
	if w.suspended {
		return true
	}
	if w.kind == WorkloadCronJob {
		// A CronJob has no replicas of its own; it is healthy unless suspended.
		return true
	}
	if w.kind == WorkloadJob {
		return w.failed == 0
	}
	return w.ready >= w.desired
}

// IsRunning reports whether a Job still has pods working.
//
// It is the Job equivalent of IsRolling: a state to watch rather than to act
// on, and one that must be told apart from a Job that has stopped short.
func (w Workload) IsRunning() bool {
	return w.kind == WorkloadJob && w.available > 0
}

// HasFailed reports whether a Job gave up with no pod still trying.
func (w Workload) HasFailed() bool {
	return w.kind == WorkloadJob && w.failed > 0 && w.available == 0
}

// Status is the single word a workload list should show.
func (w Workload) Status() string {
	switch {
	case w.suspended:
		return "Suspended"
	case w.kind == WorkloadCronJob:
		return "Active"
	case w.kind == WorkloadJob:
		// A Job's lifecycle is not a replica count, and reporting it as one
		// labels a job that is running normally "Unavailable".
		switch {
		case w.failed > 0 && w.available == 0:
			return "Failed"
		case w.available > 0:
			return "Running"
		case w.ready >= w.desired:
			return "Complete"
		default:
			return "Pending"
		}
	case w.desired == 0:
		return "Scaled to zero"
	case w.ready == 0:
		return "Unavailable"
	case w.IsRolling():
		return "Rolling"
	case w.ready < w.desired:
		return "Degraded"
	default:
		return "Running"
	}
}
