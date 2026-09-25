package domain

import (
	"fmt"
	"slices"
	"time"
)

// What comes back when a discovered monitoring backend is asked something,
// and the two things a number on screen has to carry with it: what happened,
// and whose measurement it is.
//
// See ADR 7. The governing sentence is that PodSteer queries nothing it was
// not asked to query and draws no series it cannot attribute.

// BackendStatus is what happened when the backend was asked.
//
// MODELLED ON MetricsStatus AND ReviewStatus, for the reason those exist: a
// blank chart under a green label is a claim nothing checked, and the states
// below call for opposite actions. An operator who cannot proxy needs a
// permission, one whose Prometheus rejected the expression needs to see the
// expression, and one whose backend answered with nothing needs to know that
// their monitoring stack does not scrape kubelets.
type BackendStatus string

const (
	// BackendNotEnabled is the default state of every cluster: the operator
	// has not switched this on, so nothing was sent.
	BackendNotEnabled BackendStatus = "not-enabled"

	// BackendNothingDiscovered means the switch is on and there is no
	// monitoring service to ask — including the case where the backend the
	// operator PICKED is no longer discovered, which is reported as this
	// rather than silently answered by a different one.
	BackendNothingDiscovered BackendStatus = "nothing-discovered"

	// BackendForbidden means the account may not use the services/proxy
	// subresource. Routine on a restricted account, and not a fault: it reads
	// as "your account cannot reach the monitoring stack from here".
	BackendForbidden BackendStatus = "forbidden"

	// BackendUnreachable means the request could not be made or the API
	// server could not reach the service.
	BackendUnreachable BackendStatus = "unreachable"

	// BackendRejected means the backend answered and refused the expression.
	// Its own message is carried VERBATIM, the way ErrManifestRejected
	// carries the API server's.
	BackendRejected BackendStatus = "rejected"

	// BackendNeedsCredential means the backend has authentication of its own
	// in front of it — a kube-rbac-proxy, an oauth proxy — and refused on
	// that ground.
	//
	// SEPARATE FROM forbidden AND FROM rejected, because it is neither and
	// the advice for each of those is wrong here. The account's Kubernetes
	// permissions are fine and the expression is fine: the API server's
	// service proxy does not carry a credential to the backend, so no
	// permission and no change to the query can make this route work. Folded
	// into rejected it sends somebody to debug PromQL against a message that
	// says "Unauthorized"; folded into forbidden it sends them to ask for a
	// permission they already hold.
	BackendNeedsCredential BackendStatus = "needs-credential"

	// BackendTooLarge means the response exceeded what PodSteer will read.
	// The body arrives from a system PodSteer does not control and did not
	// size, so it is refused UNDECODED rather than parsed and then judged.
	BackendTooLarge BackendStatus = "too-large"

	// BackendUnverified means the backend answered the node probe and what it
	// answered does not license an aggregate for THIS cluster. Verification
	// says which of the three cases it was and Message says it in words.
	BackendUnverified BackendStatus = "unverified"

	// BackendAnswered means there are points to draw.
	BackendAnswered BackendStatus = "answered"

	// BackendAnsweredEmpty means the backend answered successfully with no
	// matching series.
	//
	// SEPARATE FROM ANSWERED, and it is the distinction this status model
	// exists for. A Prometheus that scrapes application endpoints and not
	// kubelets returns HTTP 200 and an empty result for every expression
	// here. Collapsed into "answered" that is a blank chart under a green
	// label, which invites the operator to conclude their cluster was idle.
	BackendAnsweredEmpty BackendStatus = "answered-empty"
)

// Drawable reports whether the result carries points worth plotting.
func (s BackendStatus) Drawable() bool { return s == BackendAnswered }

// BackendVerification is what the node-set check concluded.
//
// SUBSET, NOT OVERLAP. Thanos, Mimir, Cortex and a VictoriaMetrics cluster
// backend all sit in front of many clusters; queried from one of them they
// overlap with this cluster AND hold others besides. An overlap test passes
// them, and a cluster-level sum then returns a fleet — a chart headed with
// this cluster's name showing five other clusters' load, which is worse than
// no chart because nothing about it looks wrong.
type BackendVerification string

const (
	// VerificationVerified means every node the backend answered with is one
	// of this cluster's. Aggregates may be drawn.
	VerificationVerified BackendVerification = "verified"

	// VerificationFleet means the backend holds nodes this cluster does not.
	// Not a fault — it is how those systems are normally run — and what
	// happens next is the operator's FleetPolicy.
	VerificationFleet BackendVerification = "fleet"

	// VerificationMismatch means the two node sets are disjoint: the backend
	// appears to hold a different cluster's data entirely.
	VerificationMismatch BackendVerification = "mismatch"

	// VerificationUnverifiable means the backend returned nothing to compare
	// against, so no aggregate is drawn. It is NOT a mismatch: nothing was
	// found to disagree with.
	VerificationUnverifiable BackendVerification = "unverifiable"
)

// VerifyBackendNodes compares the node names a backend answered with against
// this cluster's own.
//
// A pure function of two sets, so the four outcomes can be argued with in a
// test rather than reproduced against a Thanos.
func VerifyBackendNodes(backend, cluster []string) BackendVerification {
	known := make(map[string]struct{}, len(cluster))
	for _, name := range cluster {
		if name != "" {
			known[name] = struct{}{}
		}
	}

	// Nothing to compare in either direction is unverifiable rather than a
	// mismatch. A backend that answered with no nodes has not disagreed with
	// anything, and neither has a cluster PodSteer could not list.
	if len(known) == 0 {
		return VerificationUnverifiable
	}

	strangers, matches := false, 0
	for _, name := range backend {
		if name == "" {
			continue
		}
		if _, ours := known[name]; ours {
			matches++
			continue
		}
		strangers = true
	}

	switch {
	case matches == 0 && !strangers:
		return VerificationUnverifiable
	case strangers && matches == 0:
		return VerificationMismatch
	case strangers:
		return VerificationFleet
	default:
		return VerificationVerified
	}
}

// SeriesOrigin says whose measurement a series is.
//
// THE TWO MUST NEVER MERGE INTO ONE LINE, which is the rule ADR 7 exists to
// protect: one is somebody else's measurement, taken at somebody else's
// interval and through somebody else's recording rules, and the other is
// ours. Splicing them, or using one to fill a gap in the other, is the
// recorded mistake ADR 1 refused for kubelet readings.
type SeriesOrigin string

const (
	// OriginSampled is PodSteer's own record, derived from the overview.
	OriginSampled SeriesOrigin = "sampled"
	// OriginBackend is a monitoring backend's answer.
	OriginBackend SeriesOrigin = "backend"
)

// SeriesProvenance travels with every series a chart draws.
type SeriesProvenance struct {
	// Origin says whose measurement this is.
	Origin SeriesOrigin
	// Source names the service that answered, as MetricsBackend.Describe
	// renders it — "Prometheus in monitoring". Empty for a sampled series.
	Source string
	// Verification is what the node-set check concluded about Source.
	Verification BackendVerification
	// Filtered reports that the expression was narrowed to this cluster's
	// node names, which is what a fleet backend under FleetFilter produces.
	// It is stated rather than implied: a narrowed sum is a different claim
	// from an unnarrowed one.
	Filtered bool
}

// SampledProvenance is what PodSteer's own series carries.
func SampledProvenance() SeriesProvenance {
	return SeriesProvenance{Origin: OriginSampled}
}

// SeriesPoint is one instant of a backend series.
type SeriesPoint struct {
	At    time.Time
	Value float64
}

// PromSeries is one labelled series, as a range query returns it.
type PromSeries struct {
	Labels map[string]string
	Points []SeriesPoint
}

// BackendSeriesResult is everything a chart needs to draw a backend's answer,
// or to say why it is not drawing one.
//
// A STATUS AND A SENTENCE RATHER THAN AN ERROR for every ordinary outcome. An
// account that may not proxy, a Prometheus that rejected the expression and
// one that holds no kubelet series are all facts about somebody's cluster,
// not faults in this application, and each needs its own words.
type BackendSeriesResult struct {
	// Status is what happened.
	Status BackendStatus
	// Message is the one line the panel shows. For BackendRejected it is the
	// backend's own words.
	Message string
	// Provenance says whose measurement this is and how far it was checked.
	Provenance SeriesProvenance
	// Series are the answers, one per label set. A cluster-scoped expression
	// returns one; a node-scoped one returns as many as there are nodes.
	Series []PromSeries
	// Expression is the PromQL that was sent, shown so an operator reading
	// their backend's own query log can match it to what they pressed.
	Expression string
	// Step is the resolution the range was evaluated at.
	Step time.Duration
}

// Span reports the period the returned points actually cover.
//
// THE EXTENT DRAWN IS THE EXTENT THAT ANSWERED. A query_range over seven days
// against a Prometheus retaining one returns one day of points and no error,
// so the chart states what it received rather than what it asked for — the
// job SeriesResult.spanSeconds already does for the sampled series.
func (r BackendSeriesResult) Span() time.Duration {
	var first, last time.Time
	for _, series := range r.Series {
		for _, point := range series.Points {
			if first.IsZero() || point.At.Before(first) {
				first = point.At
			}
			if point.At.After(last) {
				last = point.At
			}
		}
	}
	if first.IsZero() || last.IsZero() {
		return 0
	}
	return last.Sub(first)
}

// Empty reports whether anything came back with points on it.
func (r BackendSeriesResult) Empty() bool {
	return !slices.ContainsFunc(r.Series, func(s PromSeries) bool { return len(s.Points) > 0 })
}

// NotEnabled is the answer for a cluster nobody has switched this on for.
func NotEnabled() BackendSeriesResult {
	return BackendSeriesResult{
		Status:  BackendNotEnabled,
		Message: "Reading from a monitoring backend is off for this cluster. Turn it on under Settings → Clusters.",
	}
}

// UnverifiedResult composes the refusal for a backend whose node set does not
// license an aggregate, in the words the outcome deserves.
//
// SILENCE IS NOT AN OPTION HERE — a refusal that explains itself is. An
// operator looking at an empty panel cannot tell "your Thanos fronts six
// clusters" from "the feature is broken", and only one of those is worth
// their time.
func UnverifiedResult(backend MetricsBackend, verification BackendVerification) BackendSeriesResult {
	var message string
	switch verification {
	case VerificationFleet:
		message = fmt.Sprintf(
			"%s answers for nodes outside this cluster, so an aggregate from it would sum more than this cluster. Set this cluster to narrow queries to its own nodes to draw one anyway.",
			backend.Describe())
	case VerificationMismatch:
		message = fmt.Sprintf(
			"%s holds no series for any of this cluster's nodes — it appears to hold a different cluster's data.",
			backend.Describe())
	default:
		message = fmt.Sprintf(
			"%s answered the node check with nothing, so PodSteer cannot tell whether it holds this cluster's data. No aggregate is drawn.",
			backend.Describe())
	}

	return BackendSeriesResult{
		Status:  BackendUnverified,
		Message: message,
		Provenance: SeriesProvenance{
			Origin:       OriginBackend,
			Source:       backend.Describe(),
			Verification: verification,
		},
	}
}

// AnsweredResult labels a backend's answer, distinguishing an empty one.
func AnsweredResult(
	backend MetricsBackend,
	verification BackendVerification,
	filtered bool,
	expression string,
	step time.Duration,
	series []PromSeries,
) BackendSeriesResult {
	result := BackendSeriesResult{
		Status:     BackendAnswered,
		Series:     series,
		Expression: expression,
		Step:       step,
		Provenance: SeriesProvenance{
			Origin:       OriginBackend,
			Source:       backend.Describe(),
			Verification: verification,
			Filtered:     filtered,
		},
	}

	if result.Empty() {
		result.Status = BackendAnsweredEmpty
		result.Series = nil
		result.Message = fmt.Sprintf(
			"%s answered, and holds nothing matching this cluster's nodes for that period. A monitoring stack that does not scrape kubelets answers exactly this way.",
			backend.Describe())
	}
	return result
}
