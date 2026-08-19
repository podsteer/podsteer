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
	// Available is how many have been ready long enough to count.
	Available int32
	// Images are the container images of the pod template.
	Images []string
	// Selector is the label selector, for finding the pods it manages.
	Selector map[string]string
	// Labels are the controller's own labels.
	Labels map[string]string
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
	images        []string
	selector      map[string]string
	labels        map[string]string
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
		images:        slices.Clone(spec.Images),
		selector:      maps.Clone(spec.Selector),
		labels:        maps.Clone(spec.Labels),
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

// Images returns a copy of the pod template's container images.
func (w Workload) Images() []string { return slices.Clone(w.images) }

// Selector returns a copy of the label selector.
func (w Workload) Selector() map[string]string { return maps.Clone(w.selector) }

// Labels returns a copy of the controller's labels.
func (w Workload) Labels() map[string]string { return maps.Clone(w.labels) }

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
func (w Workload) IsHealthy() bool {
	if w.suspended {
		return true
	}
	if w.kind == WorkloadCronJob {
		// A CronJob has no replicas of its own; it is healthy unless suspended.
		return true
	}
	return w.ready >= w.desired
}

// Status is the single word a workload list should show.
func (w Workload) Status() string {
	switch {
	case w.suspended:
		return "Suspended"
	case w.kind == WorkloadCronJob:
		return "Active"
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
