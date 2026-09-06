package domain

import (
	"errors"
	"fmt"
	"strings"
)

// This file holds the per-cluster settings that decide whether a discovered
// monitoring backend is ever asked anything, which one answers, and what
// happens when the one that answers turns out to serve more clusters than
// this one. See ADR 7 in podsteer/business-docs.
//
// NOTHING HERE SENDS A QUERY. This is the value an operator sets; the reader
// that acts on it is a separate change, which is what keeps that change's
// security review about the request rather than about the switch.

// Sentinel errors raised when a metrics-query setting is not usable.
//
// Beside the type rather than in settings.go's block because they are
// refusals about THIS value, and Validate delegates here to raise them. They
// are sentinels for the same reason the proxy's are: the layer surfacing a
// refusal should match on the error, never on its text.
var (
	// ErrSettingsMetricsQueryMode is returned when the mode is not one of the
	// three this build understands.
	ErrSettingsMetricsQueryMode = errors.New("unknown metrics query mode")

	// ErrSettingsFleetPolicy is returned when the fleet policy is neither
	// filter nor refuse.
	ErrSettingsFleetPolicy = errors.New("unknown fleet policy")

	// ErrSettingsPreferredBackend is returned when a remembered backend names
	// something that cannot be a namespace or a Service.
	//
	// REFUSED RATHER THAN STORED, and it is the same argument the proxy
	// credential refusal makes one file over: this value is written to disk,
	// so a nonsense value arriving from the interface is a bug worth not
	// persisting. It carries the extra weight of being the one object name
	// this file holds — see PreferredBackend — so writing a malformed one
	// would be spending that exception on a value that could never resolve.
	ErrSettingsPreferredBackend = errors.New("a preferred metrics backend must name a namespace and a service")
)

// dnsLabelLimit is the DNS-1123 label ceiling the API server enforces on a
// namespace and on a Service name alike.
const dnsLabelLimit = 63

// isDNSLabel reports whether value is a DNS-1123 label Kubernetes would
// accept. Empty is not one: "unset" is expressed by PreferredBackend being
// entirely zero, never by half of it.
func isDNSLabel(value string) bool {
	return value != "" && len(value) <= dnsLabelLimit && dns1123Label.MatchString(value)
}

// MetricsQueryMode says whether a discovered backend is queried, and on whose
// initiative.
//
// THREE VALUES RATHER THAN A BOOLEAN, because "may PodSteer query this" and
// "should it do so without being asked each time" are different questions and
// an operator answers them differently on a lab cluster and a production one.
type MetricsQueryMode string

const (
	// MetricsQueryOff is the default: nothing is ever sent. A cluster nobody
	// has turned this on for behaves exactly as it did before the feature
	// existed, which is what makes shipping it safe.
	MetricsQueryOff MetricsQueryMode = "off"

	// MetricsQueryManual puts a control on each chart and sends nothing until
	// it is pressed. The query is then an act with a button behind it, the
	// same shape a reachability probe and a Secret reveal already have.
	MetricsQueryManual MetricsQueryMode = "manual"

	// MetricsQueryAuto sends a query when a chart opens or its range changes,
	// and NEVER ON THE REFRESH TICK.
	//
	// That exclusion is the whole of the difference between this and querying
	// on a poll, which ADR 7 refused: a chart opening is somebody looking at
	// something, while a tick is this application deciding on its own to put
	// PromQL onto somebody's production Prometheus every few seconds.
	MetricsQueryAuto MetricsQueryMode = "auto"
)

// IsValid reports whether the mode is one this build understands.
func (m MetricsQueryMode) IsValid() bool {
	return m == MetricsQueryOff || m == MetricsQueryManual || m == MetricsQueryAuto
}

// FleetPolicy says what to do when the backend that answers turns out to hold
// more than this cluster.
//
// Thanos, Mimir, Cortex and a VictoriaMetrics cluster backend are normally
// run in front of several clusters, so this is the ordinary case rather than
// the exotic one — and an unfiltered aggregate against one of them draws
// several clusters' load under this cluster's name, which is worse than no
// chart because nothing about it looks wrong.
type FleetPolicy string

const (
	// FleetFilter is the default: every expression is narrowed to this
	// cluster's own node names, so the aggregate is over this cluster by
	// construction.
	FleetFilter FleetPolicy = "filter"

	// FleetRefuse draws nothing and says why. It is for an operator who would
	// rather have no number than one narrowed by a filter PodSteer composed.
	FleetRefuse FleetPolicy = "refuse"
)

// IsValid reports whether the policy is one this build understands.
func (p FleetPolicy) IsValid() bool {
	return p == FleetFilter || p == FleetRefuse
}

// PreferredBackend names which discovered candidate answers for a cluster.
//
// A PICK FROM WHAT DISCOVERY FOUND, never a typed URL. ADR 7 refuses a URL
// outright: one would be a new outbound destination, a second credential at
// rest and a second network path to explain, where a discovered Service is
// reached through the API server's own proxy on the kubeconfig credential
// already open for that tab.
//
// # THE ONE OBJECT NAME IN settings.json
//
// Persisting this writes a namespace and a Service name into a file that
// promises — in SECURITY.md, in its own readme header, and in the comment at
// the top of settings.go — to carry the name of nothing in any cluster. This
// is a NAMED, DISCLOSED EXCEPTION to that claim, and the three documents say
// so rather than the claim quietly becoming false.
//
// What it reveals is bounded and worth stating exactly: that a monitoring
// stack is installed, and where. It says nothing about any workload, any
// namespace an application runs in, or anything a cluster holds — a
// monitoring Service is infrastructure the operator installed, not a name the
// cluster's contents produced.
//
// It is written ONLY when the operator picks something other than the ranked
// default. Leaving the pick alone leaves this zero, and the name never
// reaches the file at all — which is what a test in the settings store
// asserts, so the narrowness of the exception is checked rather than
// intended.
type PreferredBackend struct {
	// Namespace is the namespace the chosen Service is in.
	Namespace NamespaceName
	// Service is the chosen Service's name.
	Service string
}

// IsZero reports that no explicit choice has been made, so the ranked pick
// answers.
func (p PreferredBackend) IsZero() bool { return p.Namespace == "" && p.Service == "" }

// MetricsQuerySettings is one cluster's answer to "may a discovered backend
// be queried, which one, and on what terms".
//
// A VALUE RATHER THAN A BOOLEAN, which is the addition made on ADR 7's
// acceptance: where several backends match — the ordinary case for a
// kube-prometheus-stack install — ranking picks a default but does not make
// the choice the operator's.
type MetricsQuerySettings struct {
	// Mode says whether anything is sent, and on whose initiative.
	Mode MetricsQueryMode
	// Preferred is which discovered candidate answers. Zero means the ranked
	// pick, and zero is what keeps the object name out of the file.
	Preferred PreferredBackend
	// Fleet says what to do when the backend serves more than this cluster.
	Fleet FleetPolicy
}

// DefaultMetricsQuerySettings returns the settings a cluster nobody has
// touched has: nothing is sent, no candidate is pinned, and a fleet backend
// would be filtered rather than refused.
func DefaultMetricsQuerySettings() MetricsQuerySettings {
	return MetricsQuerySettings{Mode: MetricsQueryOff, Fleet: FleetFilter}
}

// withDefaults fills the blanks a hand-edited or partially built value leaves,
// so no consumer ever meets an empty enum.
func (m MetricsQuerySettings) withDefaults() MetricsQuerySettings {
	if m.Mode == "" {
		m.Mode = MetricsQueryOff
	}
	if m.Fleet == "" {
		m.Fleet = FleetFilter
	}
	return m
}

// normalise resets whatever this build cannot use to its default, reporting
// how many fields had to be reset.
//
// THE READ PATH, and it never fails — the contract Settings.Normalise states.
// An unrecognised mode falls back to off rather than to something that sends
// a query: a value nobody here wrote must never be resolved in the direction
// of talking to a third system.
func (m *MetricsQuerySettings) normalise() int {
	reset := 0

	// An empty enum is "not written yet", not a bad value, so filling it is
	// not a reset: a file written by a build before this field existed would
	// otherwise report repairs on every launch.
	*m = m.withDefaults()

	if !m.Mode.IsValid() {
		m.Mode = MetricsQueryOff
		reset++
	}
	if !m.Fleet.IsValid() {
		m.Fleet = FleetFilter
		reset++
	}

	m.Preferred.Namespace = NamespaceName(strings.TrimSpace(string(m.Preferred.Namespace)))
	m.Preferred.Service = strings.TrimSpace(m.Preferred.Service)
	if !m.Preferred.IsZero() && m.Preferred.validate() != nil {
		// DROPPED RATHER THAN DEFAULTED, for the reason a bad kubeconfig
		// source is dropped: a pick has no default to fall back to other than
		// having no pick, and falling back to the ranked candidate is exactly
		// what an absent pick means.
		m.Preferred = PreferredBackend{}
		reset++
	}

	return reset
}

// validate reports whether these settings may be written.
//
// THE WRITE PATH, and unlike normalise it refuses. A bad value arriving from
// the interface is a bug in the interface, and writing it would persist the
// bug — the same split settings.go describes for the proxy.
func (m MetricsQuerySettings) validate() error {
	if !m.Mode.IsValid() {
		return fmt.Errorf("%w: %q", ErrSettingsMetricsQueryMode, m.Mode)
	}
	if !m.Fleet.IsValid() {
		return fmt.Errorf("%w: %q", ErrSettingsFleetPolicy, m.Fleet)
	}
	if m.Preferred.IsZero() {
		return nil
	}
	return m.Preferred.validate()
}

// validate refuses a pick that could not name a real Service.
//
// BOTH HALVES OR NEITHER: a pick with only a namespace or only a name cannot
// resolve to anything, and the DNS-1123 check refuses it by construction
// because the empty half is not a label.
func (p PreferredBackend) validate() error {
	if !isDNSLabel(string(p.Namespace)) {
		return fmt.Errorf("%w: namespace %q is not a DNS-1123 label",
			ErrSettingsPreferredBackend, p.Namespace)
	}
	if !isDNSLabel(p.Service) {
		return fmt.Errorf("%w: service %q is not a DNS-1123 label",
			ErrSettingsPreferredBackend, p.Service)
	}
	return nil
}
