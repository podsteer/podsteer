package domain

import (
	"cmp"
	"fmt"
	"maps"
	"regexp"
	"slices"
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
	name        NamespaceName
	phase       NamespacePhase
	labels      map[string]string
	annotations map[string]string
	createdAt   time.Time
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

// WithMetadata returns a copy of the namespace carrying its labels and the
// projected subset of its annotations.
//
// A copy method rather than a fourth constructor argument, for the reason
// Workload.WithSuspension gives: metadata is incidental to what a namespace
// IS, and every existing caller of NewNamespace — including the tests that
// argue over phases — has no business naming it.
func (n Namespace) WithMetadata(labels, annotations map[string]string) Namespace {
	n.labels = maps.Clone(labels)
	n.annotations = maps.Clone(annotations)
	return n
}

// Name returns the namespace name.
func (n Namespace) Name() NamespaceName { return n.name }

// Phase returns the lifecycle phase.
func (n Namespace) Phase() NamespacePhase { return n.phase }

// Labels returns a copy of the namespace's labels.
func (n Namespace) Labels() map[string]string { return maps.Clone(n.labels) }

// Annotations returns a copy of the projected annotations — only the keys
// that were asked for when the namespace was read. See Projection.
func (n Namespace) Annotations() map[string]string { return maps.Clone(n.annotations) }

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

// NamespaceSummary is a namespace as the namespace list shows it: what it is,
// and what is running in it.
//
// Kubernetes reports a namespace as a name, a phase and nothing else, which is
// why every client's namespace list is three columns of almost no information.
// The interesting half — is anything in here, is any of it broken, how much of
// the cluster is it holding — is only knowable by looking at what is inside.
type NamespaceSummary struct {
	Namespace Namespace
	// NotReady is how many of its pods are not doing their job.
	NotReady int
	// Usage is what its pods are consuming, against what they reserved and
	// what they will be stopped at. The same type a controller's row and a
	// controller's panel carry, so all three draw the same meter.
	//
	// Its Pods count is EVERY pod in the namespace, completed ones included —
	// the same count the pod list shows when filtered to it, deliberately: a
	// row that says 29 and links to a list of 34 is a bug somebody has to go
	// and disprove.
	Usage AggregateUsage
}

// Pods is how many pods are in the namespace.
func (s NamespaceSummary) Pods() int { return s.Usage.Pods }

// IsEmpty reports whether nothing is running in the namespace.
func (s NamespaceSummary) IsEmpty() bool { return s.Usage.Pods == 0 }

// NewNamespaceSummaries pairs each namespace with what is running in it.
//
// EVERY NAMESPACE GETS A ROW, including the ones holding nothing: "this
// namespace is empty" is one of the more useful things the list can say, and
// building the rows from the pods instead would silently drop it.
//
// Ordered by name. A namespace list is read by looking one up, not by ranking
// them — the ranking that matters, by what they reserve, is the overview's
// job and is already there.
func NewNamespaceSummaries(namespaces []Namespace, pods []Pod, metricsAvailable bool) []NamespaceSummary {
	byName := make(map[NamespaceName][]Pod, len(namespaces))
	for _, namespace := range namespaces {
		byName[namespace.Name()] = nil
	}

	for _, pod := range pods {
		// A pod in a namespace the caller cannot see. It happens: an account
		// may be allowed to list pods across the cluster and not to list
		// namespaces. Inventing a row for it would put a namespace on screen
		// that nothing else in the application knows about.
		if _, known := byName[pod.Namespace()]; !known {
			continue
		}
		byName[pod.Namespace()] = append(byName[pod.Namespace()], pod)
	}

	summaries := make([]NamespaceSummary, 0, len(namespaces))
	for _, namespace := range namespaces {
		held := byName[namespace.Name()]

		notReady := 0
		for _, pod := range held {
			if !pod.IsHealthy() {
				notReady++
			}
		}

		summaries = append(summaries, NamespaceSummary{
			Namespace: namespace,
			NotReady:  notReady,
			Usage:     NewAggregateUsage(held, metricsAvailable),
		})
	}

	slices.SortStableFunc(summaries, func(a, b NamespaceSummary) int {
		return cmp.Compare(a.Namespace.Name(), b.Namespace.Name())
	})

	return summaries
}
