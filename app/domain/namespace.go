package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// dns1123Label matches the subset of DNS-1123 labels Kubernetes accepts for
// namespace names: lowercase alphanumerics and '-', starting and ending with
// an alphanumeric.
var dns1123Label = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// maxNamespaceNameLength is the DNS-1123 label limit enforced by the API server.
const maxNamespaceNameLength = 63

// NamespaceName is a validated Kubernetes namespace name.
//
// The zero value is NamespaceAll, which the Kubernetes API interprets as "every
// namespace the caller may read". Modelling that as a first-class value rather
// than a magic empty string keeps the intent visible at every call site.
type NamespaceName string

const (
	// NamespaceAll selects every namespace. It is the zero value.
	NamespaceAll NamespaceName = ""

	// NamespaceDefault is the namespace used when a kubeconfig context does
	// not pin one.
	NamespaceDefault NamespaceName = "default"
)

// NewNamespaceName validates raw and returns it as a NamespaceName.
//
// A blank input is not an error: it yields NamespaceAll. Anything else must be
// a valid DNS-1123 label, which is what the API server itself enforces —
// rejecting it here turns a remote 422 into a local, cheap failure.
func NewNamespaceName(raw string) (NamespaceName, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return NamespaceAll, nil
	}
	if len(trimmed) > maxNamespaceNameLength {
		return "", fmt.Errorf("%w: %q exceeds %d characters",
			ErrInvalidNamespaceName, trimmed, maxNamespaceNameLength)
	}
	if !dns1123Label.MatchString(trimmed) {
		return "", fmt.Errorf("%w: %q is not a DNS-1123 label",
			ErrInvalidNamespaceName, trimmed)
	}
	return NamespaceName(trimmed), nil
}

// String renders the name. NamespaceAll renders as the empty string, which is
// exactly what the Kubernetes list APIs expect for a cross-namespace query.
func (n NamespaceName) String() string { return string(n) }

// IsAll reports whether the name selects every namespace.
func (n NamespaceName) IsAll() bool { return n == NamespaceAll }

// OrDefault returns n, or NamespaceDefault when n selects every namespace.
func (n NamespaceName) OrDefault() NamespaceName {
	if n.IsAll() {
		return NamespaceDefault
	}
	return n
}

// NamespacePhase is the lifecycle phase reported for a namespace.
type NamespacePhase string

const (
	// NamespacePhaseActive means the namespace accepts new objects.
	NamespacePhaseActive NamespacePhase = "Active"

	// NamespacePhaseTerminating means the namespace is being drained and
	// rejects new objects.
	NamespacePhaseTerminating NamespacePhase = "Terminating"

	// NamespacePhaseUnknown covers any phase this version of PodSteer does not
	// recognise, so that an unfamiliar cluster degrades instead of failing.
	NamespacePhaseUnknown NamespacePhase = "Unknown"
)

// NewNamespacePhase maps a raw API phase onto the known set, falling back to
// NamespacePhaseUnknown rather than rejecting the object.
func NewNamespacePhase(raw string) NamespacePhase {
	switch NamespacePhase(strings.TrimSpace(raw)) {
	case NamespacePhaseActive:
		return NamespacePhaseActive
	case NamespacePhaseTerminating:
		return NamespacePhaseTerminating
	default:
		return NamespacePhaseUnknown
	}
}

// Namespace is a namespace as observed in a cluster at a point in time.
type Namespace struct {
	name      NamespaceName
	phase     NamespacePhase
	createdAt time.Time
}

// NewNamespace builds a Namespace, validating its name.
//
// A namespace observed on a cluster always has a concrete name, so unlike
// NewNamespaceName this rejects a blank one: NamespaceAll is a query, not an
// object.
func NewNamespace(name string, phase NamespacePhase, createdAt time.Time) (Namespace, error) {
	validated, err := NewNamespaceName(name)
	if err != nil {
		return Namespace{}, err
	}
	if validated.IsAll() {
		return Namespace{}, fmt.Errorf("%w: name must not be empty", ErrInvalidNamespaceName)
	}
	return Namespace{
		name:      validated,
		phase:     phase,
		createdAt: createdAt.UTC(),
	}, nil
}

// Name returns the namespace name.
func (n Namespace) Name() NamespaceName { return n.name }

// Phase returns the lifecycle phase.
func (n Namespace) Phase() NamespacePhase { return n.phase }

// CreatedAt returns the creation timestamp in UTC.
func (n Namespace) CreatedAt() time.Time { return n.createdAt }

// IsActive reports whether the namespace accepts new objects.
func (n Namespace) IsActive() bool { return n.phase == NamespacePhaseActive }

// Age returns how long the namespace has existed relative to now.
//
// The reference time is a parameter rather than a call to time.Now so that the
// domain stays deterministic and trivially testable.
func (n Namespace) Age(now time.Time) time.Duration {
	if n.createdAt.IsZero() {
		return 0
	}
	return now.Sub(n.createdAt)
}
