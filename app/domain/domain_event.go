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

// Compile-time proof that both events satisfy DomainEvent. Cheap insurance: the
// interface is only ever consumed through an outbound port, so a missing
// method would otherwise surface as a confusing failure at the wiring site.
var (
	_ DomainEvent = ClusterConnected{}
	_ DomainEvent = ClusterUnreachable{}
)
