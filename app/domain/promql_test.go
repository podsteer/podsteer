package domain_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// aggregators are the outermost operators an expression in the table may
// have. Every one of them collapses its input, which is the property being
// asserted: `sum` and `count` return a result whose size is decided by the
// `by` clause rather than by how many series went in.
var aggregators = []string{"sum", "count", "avg", "min", "max"}

// outerAggregation reports the aggregator wrapping the whole expression, or
// the empty string when the expression is not wrapped in one.
//
// It is a small parser rather than a prefix test on purpose. A prefix test
// passes `sum(a) + rate(b[5m])`, which begins with `sum(` and is nothing of
// the kind: the outermost operator there is the addition, and its result set
// is whatever `rate` produced. So the aggregator's own parenthesis is matched
// and has to close on the last character of the expression.
func outerAggregation(expression string) string {
	rest := strings.TrimSpace(expression)

	name := ""
	for _, candidate := range aggregators {
		if strings.HasPrefix(rest, candidate) {
			name = candidate
			break
		}
	}
	if name == "" {
		return ""
	}
	rest = strings.TrimSpace(rest[len(name):])

	// An optional grouping clause: `by (node)` or `without (pod)`.
	for _, clause := range []string{"by", "without"} {
		if !strings.HasPrefix(rest, clause) {
			continue
		}
		after := strings.TrimSpace(rest[len(clause):])
		if !strings.HasPrefix(after, "(") {
			return ""
		}
		end := matchingParen(after)
		if end < 0 {
			return ""
		}
		rest = strings.TrimSpace(after[end+1:])
		break
	}

	if !strings.HasPrefix(rest, "(") {
		return ""
	}
	if matchingParen(rest) != len(rest)-1 {
		return ""
	}
	return name
}

// promEscapes are the characters that may follow a backslash inside a
// double-quoted PromQL string.
//
// VALIDATED AGAINST THE REAL PARSER ON 2026-09-06, and transcribed rather
// than guessed: Prometheus' lexer takes Go's rules, so a backslash may be
// followed by one of these, by `x` and two hex digits, by `u`/`U` and four or
// eight, or by three octal digits — and by NOTHING ELSE. Anything else is
// `unknown escape sequence`, which is a 400 rather than an empty chart.
//
// THIS SET IS THE WHOLE REASON THIS PARSER IS STRICT. The first version of it
// skipped whatever followed a backslash, which made it accept an expression
// every backend refuses — and `TestNodeNamesAreEscapedForTheRegex` then
// pinned that broken output as correct, so the suite agreed with itself while
// the feature was unusable on every EKS and every on-premises cluster. The
// Prometheus parser is deliberately NOT taken as a test dependency: its tree
// is enormous and every module in it is a licence-gate decision
// (docs/LICENCE-POLICY.md), which is a real cost for one assertion. This
// table is the cheap half of that trade, and it carries the date it was
// checked so the next person knows what to re-check against.
const promEscapes = `abfnrtv\'"`

// scanPromString returns the index just past the closing quote of the string
// starting at index 0, or -1 when the string is one Prometheus would refuse.
func scanPromString(s string) int {
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if i+1 >= len(s) {
				return -1
			}
			next := s[i+1]
			switch {
			case strings.IndexByte(promEscapes, next) >= 0:
				i++
			case next == 'x' || next == 'u' || next == 'U':
				i++
			case next >= '0' && next <= '7':
				i++
			default:
				// The failure this parser exists to catch: a regex escape
				// such as `\.` left un-doubled for the string literal.
				return -1
			}
		case '"':
			return i + 1
		}
	}
	return -1
}

// badEscape returns the first escape sequence in expression that Prometheus
// would refuse, or the empty string.
func badEscape(expression string) string {
	for i := 0; i < len(expression); i++ {
		if expression[i] != '"' {
			continue
		}
		rest := expression[i:]
		end := scanPromString(rest)
		if end >= 0 {
			i += end - 1
			continue
		}
		// Report the offending sequence rather than the whole expression, so
		// a failure names the two characters that matter.
		for j := 1; j < len(rest); j++ {
			if rest[j] != '\\' {
				continue
			}
			if j+1 < len(rest) {
				return rest[j : j+2]
			}
			return rest[j:]
		}
		return rest
	}
	return ""
}

// matchingParen returns the index of the parenthesis closing the one at
// index 0, or -1. Quoted strings are skipped so a `|` alternation full of
// punctuation cannot be read as syntax — and a string Prometheus would refuse
// makes the whole scan fail rather than being stepped over.
func matchingParen(s string) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			end := scanPromString(s[i:])
			if end < 0 {
				return -1
			}
			i += end - 1
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// THE RULE THE TABLE EXISTS FOR. An expression without an outer aggregation
// returns one series per pod, which is the fan-out ADR 7 refuses outright —
// and it is invisible on a small cluster, so it has to be a test rather than
// a review habit.
func TestNoExpressionLacksAnOuterAggregation(t *testing.T) {
	for metric, byScope := range domain.Expressions {
		for scope := range byScope {
			expression, _, err := domain.ComposeExpression(metric, scope, time.Hour, nil)
			if err != nil {
				t.Fatalf("%s/%s: composing: %v", metric, scope, err)
			}
			if outerAggregation(expression) == "" {
				t.Errorf("%s/%s has no outer aggregation: %s", metric, scope, expression)
			}
		}
	}
}

// The same rule with a node filter on, since the filter is spliced into the
// selector and a template that closed a brace wrongly would only show it
// there.
func TestEveryExpressionStillAggregatesWhenNarrowed(t *testing.T) {
	nodes := []string{"ip-10-0-1-23.eu-west-1.compute.internal", "node-b"}

	for metric, byScope := range domain.Expressions {
		for scope := range byScope {
			expression, _, err := domain.ComposeExpression(metric, scope, time.Hour, nodes)
			if err != nil {
				t.Fatalf("%s/%s: composing: %v", metric, scope, err)
			}
			if outerAggregation(expression) == "" {
				t.Errorf("%s/%s has no outer aggregation when narrowed: %s", metric, scope, expression)
			}
			if !strings.Contains(expression, `node=~"`) {
				t.Errorf("%s/%s was not narrowed: %s", metric, scope, expression)
			}
			if bad := badEscape(expression); bad != "" {
				t.Errorf("%s/%s carries %q, which Prometheus refuses as an unknown escape sequence: %s",
					metric, scope, bad, expression)
			}
		}
	}
}

// The parser above has to be able to fail, or the two tests using it assert
// nothing at all.
func TestTheOuterAggregationCheckRejectsAFanOut(t *testing.T) {
	cases := map[string]string{
		"bare selector":       `container_memory_working_set_bytes`,
		"rate without a sum":  `rate(container_cpu_usage_seconds_total[5m])`,
		"sum plus something":  `sum(a) + rate(b[5m])`,
		"aggregation inside":  `topk(5, sum by (pod) (a))`,
		"grouping without ()": `sum by node (a)`,
	}
	for name, expression := range cases {
		if got := outerAggregation(expression); got != "" {
			t.Errorf("%s: read %q as an outer aggregation of %q", name, got, expression)
		}
	}

	if got := outerAggregation(`sum by (node) (rate(a{b!=""}[5m]))`); got != "sum" {
		t.Errorf("a genuine aggregation read as %q", got)
	}
}

// The node probe is the metric and the label the charts depend on, which is
// what makes the check unable to pass while the feature fails.
func TestTheNodeProbeUsesTheMetricTheChartsUse(t *testing.T) {
	if !strings.Contains(domain.NodeProbeExpression, "container_memory_working_set_bytes") {
		t.Fatalf("the node probe does not use the charts' own metric: %s", domain.NodeProbeExpression)
	}
	if !strings.Contains(domain.NodeProbeExpression, "by ("+domain.NodeProbeLabel+")") {
		t.Fatalf("the node probe does not group by %q: %s", domain.NodeProbeLabel, domain.NodeProbeExpression)
	}

	memory := domain.Expressions[domain.MetricMemory][domain.ScopeNode]
	if !strings.Contains(memory.Template, "container_memory_working_set_bytes") {
		t.Fatal("the node memory expression no longer reads the metric the probe checks")
	}
}

func TestTheStepScalesWithTheRange(t *testing.T) {
	windows := []time.Duration{
		15 * time.Minute,
		time.Hour,
		6 * time.Hour,
		24 * time.Hour,
		7 * 24 * time.Hour,
		30 * 24 * time.Hour,
		365 * 24 * time.Hour,
	}

	previous := time.Duration(0)
	for _, window := range windows {
		step := domain.QueryStep(window)
		if step < previous {
			t.Fatalf("%s: step %s is finer than the shorter range's %s", window, step, previous)
		}
		previous = step

		if points := int(window / step); points > 400 {
			t.Errorf("%s at a %s step is %d points, well past a few hundred", window, step, points)
		}
		if step < 15*time.Second {
			t.Errorf("%s: step %s is finer than most clusters scrape", window, step)
		}
	}
}

func TestARateWindowIsNeverShorterThanFiveMinutes(t *testing.T) {
	for _, step := range []time.Duration{time.Second, 15 * time.Second, time.Minute, 5 * time.Minute, time.Hour} {
		window := domain.RateWindow(step)
		if window < 5*time.Minute {
			t.Errorf("step %s: rate window %s is under five minutes", step, window)
		}
		if window < 2*step {
			t.Errorf("step %s: rate window %s is under twice the step", step, window)
		}
	}
}

// A NAME PASSES THROUGH TWO GRAMMARS AND HAS TO SURVIVE BOTH.
//
// A cloud node name carries dots, and an unescaped dot in a regex matches any
// character — so a filter meant to narrow to this cluster would match another
// cluster's nodes, which is the exact failure the narrowing exists to
// prevent. But the escaped form then sits inside a double-quoted PromQL
// string, where `\.` is not a legal escape and the lexer answers
// `unknown escape sequence U+002E '.'`. Both halves are asserted, because
// getting only the first right is what shipped.
func TestNodeNamesAreEscapedForTheRegexAndForTheStringLiteral(t *testing.T) {
	matcher := domain.NodeMatcher([]string{"ip-10-0-1-23.eu-west-1.compute.internal"})

	if strings.Contains(matcher, "-23.eu") {
		t.Fatalf("the dots were not escaped for the regex: %s", matcher)
	}
	// TWO backslashes: one the regex needs, one the string literal needs to
	// carry it. A single one is the bug.
	if !strings.Contains(matcher, `-23\\.eu-west-1\\.compute\\.internal`) {
		t.Fatalf("unexpected escaping: %s", matcher)
	}
	if !strings.HasPrefix(matcher, `,node=~"`) {
		t.Fatalf("the matcher does not narrow on node: %s", matcher)
	}
	if bad := badEscape(matcher); bad != "" {
		t.Fatalf("the matcher carries %q, which Prometheus refuses: %s", bad, matcher)
	}
}

// A double quote is the second way a name can escape its own string literal,
// and it is a different bug from the dots: regexp.QuoteMeta does not touch a
// quote because it is not a regex metacharacter, so an unescaped one does not
// corrupt the matcher — it ENDS the string and turns the remainder of the
// name into expression text.
//
// A Kubernetes node name is an RFC 1123 subdomain and cannot contain one, so
// this is not reachable through ListNodes today. It is asserted anyway
// because the quoting helper is a PromQL helper: the day it quotes a
// namespace, a label value or anything a user typed, "the caller validates
// it" stops being true, and an escaping function that leans on its callers is
// the shape the dotted-name bug already took once.
func TestAQuoteCannotEndTheStringItIsQuotedInto(t *testing.T) {
	matcher := domain.NodeMatcher([]string{`ends"here`})

	if strings.Contains(matcher, `ends"here`) {
		t.Fatalf("the quote was passed through unescaped: %s", matcher)
	}
	if !strings.Contains(matcher, `ends\"here`) {
		t.Fatalf("unexpected escaping: %s", matcher)
	}
	// The whole point: what follows must still parse as one string literal.
	if bad := badEscape(matcher); bad != "" {
		t.Fatalf("the matcher carries %q, which Prometheus refuses: %s", bad, matcher)
	}
	if scanPromString(matcher[strings.IndexByte(matcher, '"'):]) == -1 {
		t.Fatalf("the matcher is not one well-formed string literal: %s", matcher)
	}
}

// A dotted cloud node name, all the way through ComposeExpression, is the
// case the shipped bug broke — so it is asserted on the composed expression
// and not only on the matcher.
func TestADottedCloudNodeNameComposesToSomethingPrometheusAccepts(t *testing.T) {
	nodes := []string{
		"ip-10-0-1-23.eu-west-1.compute.internal",
		"gke-cluster-pool-abc.c.project.internal",
		"worker-01.dc1.example.internal",
	}

	for metric, byScope := range domain.Expressions {
		for scope := range byScope {
			expression, _, err := domain.ComposeExpression(metric, scope, time.Hour, nodes)
			if err != nil {
				t.Fatalf("%s/%s: composing: %v", metric, scope, err)
			}
			if bad := badEscape(expression); bad != "" {
				t.Fatalf("%s/%s carries %q, which Prometheus refuses as an unknown escape sequence:\n%s",
					metric, scope, bad, expression)
			}
		}
	}
}

// The strict parser has to be able to FAIL, or every assertion built on it is
// worth nothing — which is precisely how the un-doubled escaping shipped.
func TestTheEscapeCheckRejectsWhatPrometheusRejects(t *testing.T) {
	refused := map[string]string{
		"a regex escape left un-doubled": `sum(x{node=~"a\.b"})`,
		"an escaped hyphen":              `sum(x{node=~"a\-b"})`,
		"a trailing backslash":           `sum(x{node=~"ab\"})`,
		"an unterminated string":         `sum(x{node=~"ab})`,
	}
	for name, expression := range refused {
		if bad := badEscape(expression); bad == "" {
			t.Errorf("%s: accepted %s", name, expression)
		}
	}

	accepted := map[string]string{
		"a doubled backslash": `sum(x{node=~"a\\.b"})`,
		"no escapes at all":   `sum(x{node=~"a|b"})`,
		"an escaped quote":    `sum(x{node=~"a\"b"})`,
		"a newline escape":    `sum(x{label="a\nb"})`,
		"a hex escape":        `sum(x{label="a\x41b"})`,
	}
	for name, expression := range accepted {
		if bad := badEscape(expression); bad != "" {
			t.Errorf("%s: refused %q in %s", name, bad, expression)
		}
	}
}

func TestNoNodesMeansNoMatcher(t *testing.T) {
	if matcher := domain.NodeMatcher(nil); matcher != "" {
		t.Fatalf("nil nodes produced %q", matcher)
	}
	if matcher := domain.NodeMatcher([]string{"", ""}); matcher != "" {
		t.Fatalf("empty names produced %q", matcher)
	}
}

// The refusal that keeps a very large fleet-filtered query off the wire. A
// GET URL is what the transport is, so past the budget the honest answer is a
// sentence rather than a request the path will reject.
func TestAnOversizedNodeFilterIsRefusedRatherThanSent(t *testing.T) {
	nodes := make([]string, 0, 400)
	for i := range 400 {
		nodes = append(nodes, fmt.Sprintf("ip-10-0-%d-%d.eu-west-1.compute.internal", i/250, i%250))
	}

	_, _, err := domain.ComposeExpression(domain.MetricCPU, domain.ScopeCluster, time.Hour, nodes)
	if !errors.Is(err, domain.ErrQueryTooLong) {
		t.Fatalf("error %v, want ErrQueryTooLong", err)
	}
}

func TestAnOrdinaryNodeFilterFitsTheBudget(t *testing.T) {
	nodes := make([]string, 0, 40)
	for i := range 40 {
		nodes = append(nodes, fmt.Sprintf("ip-10-0-1-%d.eu-west-1.compute.internal", i))
	}

	expression, step, err := domain.ComposeExpression(domain.MetricMemory, domain.ScopeCluster, 6*time.Hour, nodes)
	if err != nil {
		t.Fatalf("composing: %v", err)
	}
	if step <= 0 {
		t.Fatal("no step was chosen")
	}
	if !domain.WithinQueryURLBudget(expression) {
		t.Fatalf("a forty-node filter did not fit the budget: %d bytes", len(expression))
	}
}

func TestAnUnknownMetricOrScopeIsRefused(t *testing.T) {
	if _, _, err := domain.ComposeExpression("disk", domain.ScopeCluster, time.Hour, nil); !errors.Is(err, domain.ErrUnknownExpression) {
		t.Fatalf("error %v, want ErrUnknownExpression", err)
	}
	if _, _, err := domain.ComposeExpression(domain.MetricCPU, "pod", time.Hour, nil); !errors.Is(err, domain.ErrUnknownExpression) {
		t.Fatalf("error %v, want ErrUnknownExpression", err)
	}
}

// PromQL has no notion of `1m0s`, which is what time.Duration.String writes.
func TestARateWindowIsRenderedAsPromQLWritesOne(t *testing.T) {
	expression, _, err := domain.ComposeExpression(domain.MetricCPU, domain.ScopeCluster, time.Hour, nil)
	if err != nil {
		t.Fatalf("composing: %v", err)
	}
	if strings.Contains(expression, "0s]") {
		t.Fatalf("a Go duration reached the expression: %s", expression)
	}
	if !strings.Contains(expression, "m]") {
		t.Fatalf("no minute-shaped rate window: %s", expression)
	}
}

// No expression may fan out per pod. The `by` clauses are the whole of what
// decides the result set size, so they are asserted against a literal list.
func TestNoExpressionGroupsByPod(t *testing.T) {
	for metric, byScope := range domain.Expressions {
		for scope, entry := range byScope {
			// An INNER `by (namespace, pod)` is how a pod count is taken and
			// is collapsed again by the outer aggregation, so what is
			// asserted is the outer clause rather than the absence of the
			// word anywhere.
			expression, _, err := domain.ComposeExpression(metric, scope, time.Hour, nil)
			if err != nil {
				t.Fatalf("%s/%s: %v", metric, scope, err)
			}
			head := expression
			if open := strings.Index(head, "("); open > 0 {
				head = head[:open]
			}
			if strings.Contains(head, "pod") {
				t.Errorf("%s/%s aggregates by pod: %s", metric, scope, entry.Template)
			}
		}
	}
}
