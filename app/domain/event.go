package domain

import (
	"strings"
	"time"
)

// EventType classifies a Kubernetes Event.
type EventType string

const (
	// EventNormal reports something expected.
	EventNormal EventType = "Normal"
	// EventWarning reports something that went wrong. These are the ones worth
	// surfacing: a cluster emits thousands of Normal events an hour and none of
	// them need anybody's attention.
	EventWarning EventType = "Warning"
)

// NewEventType maps a raw API type onto the known set, defaulting to Normal.
func NewEventType(raw string) EventType {
	if strings.EqualFold(strings.TrimSpace(raw), string(EventWarning)) {
		return EventWarning
	}
	return EventNormal
}

// EventSpec carries the data needed to build an Event.
type EventSpec struct {
	// Name is the event object's own name. Required.
	Name string
	// Namespace is the event's namespace. Required.
	Namespace NamespaceName
	// ClusterID is the cluster it was read from. Required.
	ClusterID ClusterID
	// Type classifies the event.
	Type EventType
	// Reason is the short machine-readable cause, e.g. "BackOff".
	Reason string
	// Message is the human-readable description.
	Message string
	// InvolvedKind is the kind of object the event is about.
	InvolvedKind string
	// InvolvedName is the name of the object the event is about.
	InvolvedName string
	// Source is the component that emitted it, e.g. "kubelet".
	Source string
	// Count is how many times the event has repeated.
	Count int32
	// FirstSeen is when the event first occurred.
	FirstSeen time.Time
	// LastSeen is when it most recently occurred. This, not FirstSeen, is what
	// an event list sorts by — a warning that fired once an hour ago matters
	// less than one still firing now.
	LastSeen time.Time
}

// Event is a Kubernetes Event as observed at a point in time.
//
// Note the name: within PodSteer, domain.Event means a Kubernetes Event.
// The application's own internal notifications are DomainEvent.
type Event struct {
	name         string
	namespace    NamespaceName
	clusterID    ClusterID
	eventType    EventType
	reason       string
	message      string
	involvedKind string
	involvedName string
	source       string
	count        int32
	firstSeen    time.Time
	lastSeen     time.Time
}

// NewEvent validates spec and returns the corresponding Event.
func NewEvent(spec EventSpec) (Event, error) {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return Event{}, ErrEmptyResourceName
	}
	if spec.ClusterID.IsZero() {
		return Event{}, ErrEmptyClusterID
	}

	eventType := spec.Type
	if eventType == "" {
		eventType = EventNormal
	}

	return Event{
		name:         name,
		namespace:    spec.Namespace,
		clusterID:    spec.ClusterID,
		eventType:    eventType,
		reason:       spec.Reason,
		message:      strings.TrimSpace(spec.Message),
		involvedKind: spec.InvolvedKind,
		involvedName: spec.InvolvedName,
		source:       spec.Source,
		count:        spec.Count,
		firstSeen:    spec.FirstSeen.UTC(),
		lastSeen:     spec.LastSeen.UTC(),
	}, nil
}

// Name returns the event object's name.
func (e Event) Name() string { return e.name }

// Namespace returns the event's namespace.
func (e Event) Namespace() NamespaceName { return e.namespace }

// ClusterID returns the cluster it was read from.
func (e Event) ClusterID() ClusterID { return e.clusterID }

// Type classifies the event.
func (e Event) Type() EventType { return e.eventType }

// Reason returns the short machine-readable cause.
func (e Event) Reason() string { return e.reason }

// Message returns the human-readable description.
func (e Event) Message() string { return e.message }

// InvolvedKind returns the kind of object the event is about.
func (e Event) InvolvedKind() string { return e.involvedKind }

// InvolvedName returns the name of the object the event is about.
func (e Event) InvolvedName() string { return e.involvedName }

// Source returns the component that emitted the event.
func (e Event) Source() string { return e.source }

// Count returns how many times the event has repeated.
func (e Event) Count() int32 { return e.count }

// FirstSeen returns when the event first occurred, in UTC.
func (e Event) FirstSeen() time.Time { return e.firstSeen }

// LastSeen returns when the event most recently occurred, in UTC.
func (e Event) LastSeen() time.Time { return e.lastSeen }

// IsWarning reports whether the event describes something going wrong.
func (e Event) IsWarning() bool { return e.eventType == EventWarning }

// Age returns how long ago the event was last seen.
func (e Event) Age(now time.Time) time.Duration {
	if e.lastSeen.IsZero() {
		return 0
	}
	return now.Sub(e.lastSeen)
}

// InvolvedObject renders the subject as "Kind/name" for display.
func (e Event) InvolvedObject() string {
	if e.involvedKind == "" {
		return e.involvedName
	}
	return e.involvedKind + "/" + e.involvedName
}
