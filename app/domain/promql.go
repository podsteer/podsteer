package domain

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// The PromQL PodSteer sends, and the bounds on it.
//
// THERE IS NO QUERY BOX AND NO URL TO TYPE. Every expression that can leave
// this process is in the table below, keyed by the metric and the scope the
// chart is drawing — a free-text console is a different product and an easy
// way to melt somebody's Prometheus, and a typed URL would be a new outbound
// destination this application does not have. See ADR 7.
//
// Three bounds live here rather than in the adapter, because each is a rule
// worth arguing with in a test rather than an implementation detail:
//
//   - every expression is PRE-AGGREGATED at cluster or node level, so the
//     result set never grows with the pod count;
//   - the STEP scales with the range, so an hour and thirty days both resolve
//     to a few hundred points;
//   - the composed expression is refused past a URL budget, because the query
//     travels in a GET URL and one the API server would reject is one worth
//     not sending.

// ErrQueryTooLong is returned when a composed expression would not fit the
// URL budget.
//
// ITS OWN SENTINEL because the refusal has to reach the operator as a
// sentence: a fleet backend on a very large cluster is narrowed by a node
// filter PodSteer composes, and past a few hundred nodes that filter is
// simply longer than a URL. Silence there would read as an idle cluster.
var ErrQueryTooLong = errors.New("the composed query is too long to send as a URL")

// ErrUnknownExpression is returned when nothing in the table answers for a
// metric and scope pair.
var ErrUnknownExpression = errors.New("no expression for that metric and scope")

// MaxQueryURLBytes is the budget one query's URL is held to.
//
// ROUGHLY FOUR KILOBYTES, and the number is a property of the path rather
// than of PromQL: the query travels as a GET query parameter through the API
// server's service proxy, and every hop in that path — the API server itself,
// an intervening proxy, the backend's own HTTP server — enforces a request
// line limit that is conventionally eight kilobytes and is not guaranteed to
// be. Half of that leaves room for the path, the other parameters and the
// percent-encoding an expression full of quotes and braces expands into.
//
// A POST WOULD SIDESTEP IT AND IS REFUSED. Posting to the service proxy is
// the RBAC verb `create` on services/proxy, a different permission from the
// `get` every proxying account already holds — so a form-body query would
// fail for exactly the tightly-permissioned accounts this application is
// careful about, and would do it by asking for a privilege it does not need.
const MaxQueryURLBytes = 4096

// queryTargetPoints is how many points a range query aims to return.
//
// A few hundred is as much resolution as any chart can draw and as much as
// the IPC boundary should carry. It is a TARGET rather than a cap: the step
// is rounded up to a readable interval, so the actual count lands under this
// rather than on it.
const queryTargetPoints = 300

// minQueryStep is the finest step ever asked for. Below this the backend is
// being asked to evaluate an expression more often than most clusters scrape.
const minQueryStep = 15 * time.Second

// minRateWindow is the floor on the window a rate is taken over.
//
// FIVE MINUTES, and the rule is max(5m, 2*step): a rate window shorter than
// twice the scrape interval returns nothing at all for a backend scraping
// every minute, and a chart of nothing is indistinguishable from an idle
// cluster.
const minRateWindow = 5 * time.Minute

// queryStepLadder is the set of steps offered, coarsest last.
//
// A LADDER RATHER THAN AN EXACT DIVISION, because a step of 137 seconds puts
// every point at an unreadable instant and defeats any alignment the backend
// has with its own scrape interval.
var queryStepLadder = []time.Duration{
	15 * time.Second,
	30 * time.Second,
	time.Minute,
	2 * time.Minute,
	5 * time.Minute,
	10 * time.Minute,
	15 * time.Minute,
	30 * time.Minute,
	time.Hour,
	2 * time.Hour,
	6 * time.Hour,
	12 * time.Hour,
	24 * time.Hour,
}

// MetricID names what a chart is plotting.
//
// The same three the sampled trend chart offers, deliberately: this feature
// draws a longer version of a line PodSteer already draws, so a metric with
// no sampled counterpart would be a series with nothing to sit beside.
type MetricID string

const (
	// MetricCPU is CPU used, in cores.
	MetricCPU MetricID = "cpu"
	// MetricMemory is the working set, in bytes.
	MetricMemory MetricID = "memory"
	// MetricPods is how many pods were reporting.
	MetricPods MetricID = "pods"
)

// MetricScope is the level an expression is aggregated at.
//
// CLUSTER OR NODE, AND NOTHING FINER. A per-pod scope is deliberately absent:
// its result set grows with the pod count, which is the one shape ADR 7
// refuses outright. Adding one is a new argument, not a new table row.
type MetricScope string

const (
	// ScopeCluster is one series for the whole cluster.
	ScopeCluster MetricScope = "cluster"
	// ScopeNode is one series per node.
	ScopeNode MetricScope = "node"
)

// PromExpression is one entry of the table.
type PromExpression struct {
	// Template is the expression with two placeholders: {{filter}} for an
	// extra label matcher (empty, or a leading comma and a node regex) and
	// {{range}} for a rate window.
	//
	// A REPLACER RATHER THAN fmt, because an expression is full of braces and
	// percent signs are not, so a format string here would read as noise and
	// an unused verb would append diagnostics into somebody's PromQL.
	Template string
	// Unit says what the values are in, for the chart's own formatter — so a
	// backend's numbers are formatted the same way PodSteer's own are, which
	// is the least a chart drawing both has to do.
	Unit string
}

// Expressions is the whole of what PodSteer may send, keyed by metric and
// scope.
//
// EVERY ONE HAS AN OUTER AGGREGATION, and a test asserts it rather than a
// comment claiming it: the outermost operator of each is `sum` or `count`,
// with or without a `by` clause, closing at the end of the expression. That
// is what makes the result set a function of the node count at worst, never
// of the pod count.
//
// The selectors mirror what cAdvisor writes through a kubelet scrape, which
// is what every backend in front of a Kubernetes cluster is fed: the empty
// `container` label is the pod-level cgroup roll-up and would double every
// total if it were left in, and the empty `pod` label is the node's own
// machine-level series.
var Expressions = map[MetricID]map[MetricScope]PromExpression{
	MetricCPU: {
		ScopeCluster: {
			Template: `sum(rate(container_cpu_usage_seconds_total{container!="",pod!=""{{filter}}}[{{range}}]))`,
			Unit:     "cores",
		},
		ScopeNode: {
			Template: `sum by (node) (rate(container_cpu_usage_seconds_total{container!="",pod!=""{{filter}}}[{{range}}]))`,
			Unit:     "cores",
		},
	},
	MetricMemory: {
		ScopeCluster: {
			Template: `sum(container_memory_working_set_bytes{container!="",pod!=""{{filter}}})`,
			Unit:     "bytes",
		},
		ScopeNode: {
			Template: `sum by (node) (container_memory_working_set_bytes{container!="",pod!=""{{filter}}})`,
			Unit:     "bytes",
		},
	},
	MetricPods: {
		// Counted from the same cAdvisor series the other two read rather
		// than from kube-state-metrics, which is a SEPARATE discovery and a
		// separate question — a cluster commonly has one and not the other,
		// and an expression that silently needed both would report an empty
		// chart on half of them.
		ScopeCluster: {
			Template: `count(count by (namespace, pod) (container_memory_working_set_bytes{container!="",pod!=""{{filter}}}))`,
			Unit:     "pods",
		},
		ScopeNode: {
			Template: `count by (node) (count by (node, namespace, pod) (container_memory_working_set_bytes{container!="",pod!=""{{filter}}}))`,
			Unit:     "pods",
		},
	},
}

// NodeProbeExpression asks a backend which nodes it holds series for.
//
// THE METRIC AND THE LABEL THE CHARTS THEMSELVES DEPEND ON, which is the
// whole reason it is this expression and not `/api/v1/label/node/values`.
// That endpoint reports every value of the `node` label the backend holds for
// ANY metric, which can only widen the answer — and a widened answer reads a
// perfectly ordinary single-cluster backend as a fleet. Asking with the
// series the feature is built on means the check cannot pass while the
// feature would fail.
const NodeProbeExpression = `count by (node) (container_memory_working_set_bytes)`

// NodeProbeLabel is the label NodeProbeExpression groups by.
const NodeProbeLabel = "node"

// QueryStep chooses the step for a range, aiming at queryTargetPoints.
//
// SCALED RATHER THAN FIXED. One `query_range` is one HTTP request whatever
// the range, which is the easy half; what it costs the backend is decided by
// the expression and the step, and a fixed resolution turns a thirty-day
// request into eighty thousand evaluations of it.
func QueryStep(window time.Duration) time.Duration {
	if window <= 0 {
		return minQueryStep
	}

	ideal := window / queryTargetPoints
	for _, step := range queryStepLadder {
		if step >= ideal {
			return step
		}
	}
	return queryStepLadder[len(queryStepLadder)-1]
}

// RateWindow is the window a rate is taken over for a given step.
//
// max(5m, 2*step): twice the step so consecutive points do not read the same
// samples, and never below five minutes so a backend scraping every minute
// still has two points inside the window to take a rate from.
func RateWindow(step time.Duration) time.Duration {
	if doubled := 2 * step; doubled > minRateWindow {
		return doubled
	}
	return minRateWindow
}

// NodeMatcher renders the label matcher that narrows an expression to a set
// of node names, or the empty string for no narrowing.
//
// TWO ESCAPES, NOT ONE, AND THAT IS THE WHOLE OF THIS FUNCTION.
//
// The name is escaped twice because it passes through two grammars on its way
// to the regex engine, and getting only the first one right produces an
// expression every backend rejects:
//
//  1. REGEX. A cloud provider's node names carry dots —
//     `ip-10-0-1-23.eu-west-1.compute.internal` on EKS,
//     `gke-cluster-pool-abc.c.project.internal` on GKE, an FQDN on anything
//     on-premises — and an unescaped dot matches any character, so a filter
//     meant to narrow to this cluster would match another cluster's nodes.
//     `regexp.QuoteMeta` turns `.` into `\.`.
//  2. THE PROMQL STRING LITERAL. That `\.` is then placed inside a
//     double-quoted PromQL string, whose escape rules are Go's — and `\.` is
//     not one of them. Prometheus' lexer answers
//     `unknown escape sequence U+002E '.'` and the query is a 400. So every
//     backslash the regex escaping produced is doubled for the literal.
//
// Left at step one this feature is broken on precisely the clusters the
// narrowing exists for: under FleetFilter every range query would return 400,
// and the only reason a fleet sum was never wrong is that no query ever
// succeeded. The doubled form is what Prometheus, Thanos, Mimir, Cortex and
// VictoriaMetrics' MetricsQL all accept.
func NodeMatcher(nodes []string) string {
	if len(nodes) == 0 {
		return ""
	}

	quoted := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if node == "" {
			continue
		}
		quoted = append(quoted, quoteForPromQL(node))
	}
	if len(quoted) == 0 {
		return ""
	}

	// Anchored by PromQL itself: a `=~` matcher is fully anchored, so
	// `node=~"a|b"` matches exactly a or b and nothing containing them.
	return fmt.Sprintf(`,%s=~"%s"`, NodeProbeLabel, strings.Join(quoted, "|"))
}

// quoteForPromQL escapes one literal string for use as a regex inside a
// double-quoted PromQL string.
//
// Its own function so the two-grammar rule above is applied in exactly one
// place, and so a test can assert the two steps independently of the matcher
// they are composed into.
func quoteForPromQL(literal string) string {
	// The regex grammar first: `.` becomes `\.`, `-` and `+` and the rest of
	// the metacharacters likewise.
	escaped := regexp.QuoteMeta(literal)
	// Then the string grammar: every backslash the step above introduced is
	// itself a character the PromQL lexer will try to read as the start of an
	// escape sequence, so it is doubled. A backslash that was already in the
	// name is covered by the same pass, since QuoteMeta escaped that one too.
	escaped = strings.ReplaceAll(escaped, `\`, `\\`)
	// And the double quote, which QuoteMeta does NOT touch because it is not
	// a regex metacharacter — but it ends the string literal this sits
	// inside, so an unescaped one does not corrupt the matcher, it ends the
	// expression and starts something else. A Kubernetes node name is an
	// RFC 1123 name and cannot contain one today; this function is a
	// PromQL-quoting helper rather than a node-name helper, and "the caller
	// happens to validate it" is not a property to rely on in the escaping
	// itself. It runs AFTER the doubling above so the backslash it adds is
	// not doubled in turn.
	return strings.ReplaceAll(escaped, `"`, `\"`)
}

// ComposeExpression renders one table entry for a window, narrowed to nodes
// when any are given.
//
// It returns ErrUnknownExpression for a pair the table does not hold, and
// ErrQueryTooLong when the result would not fit MaxQueryURLBytes — refused
// here rather than sent and rejected, because a request the API server
// answers with a 414 tells the operator nothing about their cluster and
// still lands in their audit log.
func ComposeExpression(metric MetricID, scope MetricScope, window time.Duration, nodes []string) (string, time.Duration, error) {
	byScope, known := Expressions[metric]
	if !known {
		return "", 0, fmt.Errorf("%w: %q", ErrUnknownExpression, metric)
	}
	entry, known := byScope[scope]
	if !known {
		return "", 0, fmt.Errorf("%w: %q at %q", ErrUnknownExpression, metric, scope)
	}

	step := QueryStep(window)
	expression := strings.NewReplacer(
		"{{filter}}", NodeMatcher(nodes),
		"{{range}}", formatPromDuration(RateWindow(step)),
	).Replace(entry.Template)

	if !WithinQueryURLBudget(expression) {
		return "", 0, fmt.Errorf("%w: %d nodes make it %d encoded bytes, over the %d-byte budget",
			ErrQueryTooLong, len(nodes), len(url.QueryEscape(expression))+queryURLOverhead, MaxQueryURLBytes)
	}
	return expression, step, nil
}

// queryURLOverhead is what the rest of the URL costs beside the expression:
// the proxy path with a namespace and a service in it, a backend's own
// prefix, and the start, end and step parameters.
//
// Generous rather than measured, because the point of the budget is to refuse
// early: a request that is under it and still rejected costs a round trip and
// an audit line, and one that is over it and sent costs the same for nothing.
const queryURLOverhead = 512

// WithinQueryURLBudget reports whether an expression fits the URL budget once
// percent-encoded.
//
// ENCODED RATHER THAN ESTIMATED. A node name is almost all unreserved
// characters and costs one byte each, while the braces, quotes and commas
// around it cost three — so a flat multiplier is wrong in both directions at
// once, and this is the one measurement that decides whether a fleet-filtered
// query goes out or is refused with a sentence.
func WithinQueryURLBudget(expression string) bool {
	return len(url.QueryEscape(expression))+queryURLOverhead <= MaxQueryURLBytes
}

// formatPromDuration renders a duration the way PromQL writes one.
//
// PromQL has no notion of `1m0s`, which is what time.Duration.String
// produces, so the two units this ever needs are written out by hand.
func formatPromDuration(d time.Duration) string {
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return fmt.Sprintf("%ds", int(d/time.Second))
}
