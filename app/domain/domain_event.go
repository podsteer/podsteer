package domain

import "time"

// EventName is the stable, transport-agnostic name of a domain event.
//
// These strings cross the process boundary: the Svelte frontend subscribes to
// them by name through the Wails event bus. Treat them as public API and
// version them rather than renaming in place.
type EventName string

const (
	// EventClusterConnected is raised when a cluster's API server has been
	// reached and identified.
	EventClusterConnected EventName = "cluster:connected"

	// EventClusterUnreachable is raised when a cluster that PodSteer tried to
	// reach did not answer.
	EventClusterUnreachable EventName = "cluster:unreachable"

	// EventKubeconfigChanged is raised when the set of kubeconfig files
	// PodSteer reads, or the content of one of them, has changed on disk.
	EventKubeconfigChanged EventName = "kubeconfig:changed"
)

// DomainEvent is something noteworthy that happened inside PodSteer.
//
// Events let the application layer tell the UI about state changes it did not
// ask for, which is what a desktop client needs: connection state moves on its
// own, whereas a pod list only ever changes because somebody requested it.
type DomainEvent interface {
	// Name identifies the kind of event.
	Name() EventName
	// OccurredAt is when the event happened, in UTC.
	OccurredAt() time.Time
}

// ClusterConnected records that a cluster was successfully reached.
type ClusterConnected struct {
	// Cluster is the cluster as it stood when contact succeeded, carrying the
	// version its API server reported.
	Cluster Cluster
	// At is when contact succeeded.
	At time.Time
}

// Name implements Event.
func (e ClusterConnected) Name() EventName { return EventClusterConnected }

// OccurredAt implements Event.
func (e ClusterConnected) OccurredAt() time.Time { return e.At.UTC() }

// ClusterUnreachable records a failed attempt to reach a cluster.
type ClusterUnreachable struct {
	// ClusterID is the cluster that did not answer.
	ClusterID ClusterID
	// Reason is a human-readable explanation, safe to show to the operator.
	Reason string
	// At is when the attempt failed.
	At time.Time
}

// Name implements Event.
func (e ClusterUnreachable) Name() EventName { return EventClusterUnreachable }

// OccurredAt implements Event.
func (e ClusterUnreachable) OccurredAt() time.Time { return e.At.UTC() }

// KubeconfigChanged records that the kubeconfig on disk is not what PodSteer
// last read.
//
// IT CARRIES NO PATH AND NO CONTEXT NAME. Which file changed is not something
// the interface acts on — the answer to any of them is the same, re-read the
// list — and a path is a thing about the operator's machine that would then
// travel through the event bus and into any log that records events. The count
// is enough to say something true in the interface ("the kubeconfig changed")
// without saying anything about where.
type KubeconfigChanged struct {
	// Files is how many kubeconfig files were being read when the change was
	// noticed. It is a magnitude, not a list.
	Files int
	// At is when the change was noticed, which is up to one poll interval
	// after it happened — see application.KubeconfigWatcher for why that is
	// the right trade rather than a watcher per file.
	At time.Time
}

// Name implements Event.
func (e KubeconfigChanged) Name() EventName { return EventKubeconfigChanged }

// OccurredAt implements Event.
func (e KubeconfigChanged) OccurredAt() time.Time { return e.At.UTC() }

// Compile-time proof that both events satisfy DomainEvent. Cheap insurance: the
// interface is only ever consumed through an outbound port, so a missing
// method would otherwise surface as a confusing failure at the wiring site.
var (
	_ DomainEvent = KubeconfigChanged{}
	_ DomainEvent = ClusterConnected{}
	_ DomainEvent = ClusterUnreachable{}
)
