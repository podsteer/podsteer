package domain

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
)

// Observed traffic between workloads, read from the monitoring backend the
// cluster already runs. See ADR 7 and the topology plan.
//
// A LAYER, NOT AN EDGE KIND. Every topology edge is a relationship Kubernetes
// itself holds; a traffic edge is somebody else's measurement over a window,
// and the two are kept in different types so nothing can draw one as the
// other. A traffic edge is only ever drawn beside the topology, decorated, and
// a pair that exchanged nothing in the window simply has no edge.
//
// THE SAME RULES AS promql.go, WITH ONE WIDENING STATED HERE. Every expression
// below is in a fixed table, every one aggregates (a test parses each), and
// none is ever sent on a tick. The widening is the grouping: a chart groups by
// node at most, and these group by the (source workload, destination
// workload) pair — so the answer grows with the number of workload pairs that
// talk, never with the number of pods. MaxTrafficEdges bounds it on top.
//
// EVERY METRIC AND LABEL NAME BELOW WAS CHECKED AGAINST ITS PROJECT'S OWN
// DOCUMENTATION on 2026-10-02, and the page is cited beside each source. The
// fixtures under testdata/prom/ are built from those documented label sets,
// not from a live capture.

// ErrUnknownTrafficSource is returned for a source name outside the table.
var ErrUnknownTrafficSource = errors.New("unknown traffic source")

// ErrUnknownTrafficWindow is returned for a window outside the three offered.
var ErrUnknownTrafficWindow = errors.New("unknown traffic window")

// MaxTrafficEdges is the most edges a traffic layer carries. Past it the
// answer is refused (BackendTooLarge) rather than drawn: five thousand
// decorated edges is already past what anybody can read, and the cap keeps the
// bridge payload to a couple of megabytes at worst.
const MaxTrafficEdges = 5000

// trafficNamespaceFilterMax is the most namespaces a server-side matcher is
// composed for. Past it the expression is sent unfiltered and MapTraffic
// filters instead — a regex alternation of fifty namespaces buys the backend
// little and costs the URL budget a great deal.
const trafficNamespaceFilterMax = 5

// TrafficSource names a system whose metrics describe traffic.
type TrafficSource string

const (
	// TrafficIstio is Istio's standard metrics, from the Envoy sidecars or
	// waypoints.
	TrafficIstio TrafficSource = "istio"
	// TrafficLinkerd is the Linkerd proxy's metrics, usually in linkerd-viz's
	// own Prometheus.
	TrafficLinkerd TrafficSource = "linkerd"
	// TrafficBeyla is Grafana Beyla's network flow metrics, or the same
	// metrics from OpenTelemetry eBPF Instrumentation (OBI), its upstream.
	TrafficBeyla TrafficSource = "beyla"
	// TrafficCaretta is groundcover's Caretta link metric.
	TrafficCaretta TrafficSource = "caretta"
	// TrafficHubble is Cilium Hubble's Prometheus metrics. Never the Relay
	// gRPC API: that is a second protocol and a second permission.
	TrafficHubble TrafficSource = "hubble"
)

// TrafficSourceNames lists every source, in the order the picker offers them.
func TrafficSourceNames() []TrafficSource {
	return []TrafficSource{TrafficIstio, TrafficLinkerd, TrafficBeyla, TrafficCaretta, TrafficHubble}
}

// ParseTrafficSource validates a source name from the interface.
func ParseTrafficSource(raw string) (TrafficSource, error) {
	source := TrafficSource(raw)
	if _, known := trafficSpecs[source]; !known {
		return "", fmt.Errorf("%w: %q", ErrUnknownTrafficSource, raw)
	}
	return source, nil
}

// TrafficWindow is the window a rate is taken over.
type TrafficWindow string

const (
	// TrafficWindow5m is five minutes, the shortest window a rate survives a
	// one-minute scrape interval at.
	TrafficWindow5m TrafficWindow = "5m"
	// TrafficWindow15m is fifteen minutes.
	TrafficWindow15m TrafficWindow = "15m"
	// TrafficWindow1h is an hour.
	TrafficWindow1h TrafficWindow = "1h"
)

// ParseTrafficWindow validates a window from the interface. The value is
// written into PromQL verbatim, which is exactly why it is one of three
// constants and never a string the interface chose.
func ParseTrafficWindow(raw string) (TrafficWindow, error) {
	switch window := TrafficWindow(raw); window {
	case TrafficWindow5m, TrafficWindow15m, TrafficWindow1h:
		return window, nil
	case "":
		return TrafficWindow5m, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownTrafficWindow, raw)
	}
}

// TrafficRole is what one expression measures.
type TrafficRole string

const (
	// TrafficRoleRate is requests per second.
	TrafficRoleRate TrafficRole = "rate"
	// TrafficRoleErrors is failed (5xx or failure-classified) requests per
	// second.
	TrafficRoleErrors TrafficRole = "errors"
	// TrafficRoleBytes is bytes per second.
	TrafficRoleBytes TrafficRole = "bytes"
	// TrafficRoleConnections is connections opened (Istio) or flows observed
	// (Hubble) per second.
	TrafficRoleConnections TrafficRole = "connections"
	// TrafficRoleP50 is the median latency.
	TrafficRoleP50 TrafficRole = "p50"
	// TrafficRoleP95 is the 95th percentile latency.
	TrafficRoleP95 TrafficRole = "p95"
	// TrafficRoleP99 is the 99th percentile latency.
	TrafficRoleP99 TrafficRole = "p99"
)

// TrafficQuery is one expression and what it measures.
type TrafficQuery struct {
	Role       TrafficRole
	Expression string
}

// TrafficEndpoint is one end of an observed edge.
type TrafficEndpoint struct {
	Namespace string
	Workload  string
	Service   string
	// External is a host or address outside the cluster's workloads, when
	// the source names one.
	External string
	// Unknown is set when the source could not say who this was — Istio's
	// literal "unknown", or a flow with no Kubernetes attribution.
	Unknown bool
	// NodeID is the topology node this endpoint maps to, empty when unmapped.
	NodeID string
}

func (e TrafficEndpoint) key() string {
	return strings.Join([]string{e.Namespace, e.Workload, e.Service, e.External, fmt.Sprint(e.Unknown)}, "\x00")
}

// anonymous reports whether the endpoint carries nothing at all — a series
// whose labels named neither end, which is what a source configured without
// its endpoint labels produces.
func (e TrafficEndpoint) anonymous() bool {
	return e.Namespace == "" && e.Workload == "" && e.Service == "" && e.External == ""
}

// TrafficEdge is the traffic observed from one endpoint to another over the
// window.
type TrafficEdge struct {
	Source, Dest   TrafficEndpoint
	Protocol       string
	RequestsPerSec float64
	ErrorsPerSec   float64
	BytesPerSec    float64
	Connections    float64
	// P50, P95, P99 are milliseconds; -1 when the source does not expose
	// latency for this edge.
	P50, P95, P99 float64
	// LatencyBeyondBuckets is set when a percentile fell in the histogram's
	// +Inf bucket: slower than the largest bucket bound, by an amount the
	// histogram cannot say. That percentile stays -1, and this is what tells
	// it apart from "not exposed".
	LatencyBeyondBuckets bool
}

// TrafficLayer is the answer for one source over one window.
type TrafficLayer struct {
	Source      TrafficSource
	Window      TrafficWindow
	Edges       []TrafficEdge
	Unmapped    []TrafficEndpoint
	Status      BackendStatus
	Message     string
	Provenance  SeriesProvenance
	Expressions []string
}

// TrafficSourceStatus says whether one source's metrics were found.
type TrafficSourceStatus struct {
	Source    TrafficSource
	Available bool
	// Detail is what was found, or what the source would need.
	Detail string
}

// TrafficSources is the answer to "which traffic sources does this cluster's
// backend hold".
type TrafficSources struct {
	Backend string
	Sources []TrafficSourceStatus
	Status  BackendStatus
	Message string
}

// TrafficNodeRef is the least MapTraffic needs to know about a topology node
// to attach an endpoint to it. Its own type so this file depends on nothing
// the topology builds; the application layer converts.
type TrafficNodeRef struct {
	ID        string
	APIKind   string
	Name      string
	Namespace string
}

// trafficMetric is one row of a source's table.
type trafficMetric struct {
	role TrafficRole
	// name is the metric, or a `{__name__=~"..."}` selector body when two
	// names are accepted.
	name string
	// matchers are extra label matchers, comma-separated, no braces.
	matchers string
	// quantile, when set, makes this a histogram_quantile over name_bucket.
	quantile float64
	// branches, when set, replaces the spec's branches for this row.
	branches []trafficBranch
}

// trafficBranch is one operand of an expression's `or`.
//
// EVERY ROW OF A SOURCE GROUPS BY THE SAME LABELS — the spec's key labels
// plus the branch's own — and those labels are exactly what the source's
// endpoint reader turns into an edge. That is what makes a percentile mean
// something: a quantile grouped by a label the edge does not carry (an
// authority, a replica set across a rollout) is several quantiles for one
// edge, and keeping any one of them is arbitrary. A label only some edges
// are identified by goes on the branch that selects those edges.
type trafficBranch struct {
	// matchers are label matchers, comma-separated, no braces.
	matchers string
	// extraBy are identity labels only this branch's series carry.
	extraBy []string
	// dstUnknown marks a branch whose destination has no namespace label,
	// so only the source-side namespace filter applies to it.
	dstUnknown bool
}

// trafficSpec is everything this file knows about one source.
type trafficSpec struct {
	// probe is the instant count() that says whether the source is present.
	probe string
	// requirement is the sentence shown when it is not.
	requirement string
	// branches are combined with `or`. Two reporters seeing the same request
	// produce identical label sets after aggregation, and `or` keeps the
	// left one — so listing the preferred reporter first counts each request
	// once while still keeping edges only the second reporter saw.
	branches []trafficBranch
	// srcNamespace and dstNamespace are the labels a namespace matcher goes
	// on; empty means the source cannot be filtered server-side.
	srcNamespace, dstNamespace string
	// keyBy are the labels that identify an edge, and every row groups by
	// them (plus `le` for a histogram, plus a branch's extraBy).
	keyBy   []string
	metrics []trafficMetric
	// latencyScale converts the source's latency unit to milliseconds.
	latencyScale float64
	// endpoints reads a series' labels.
	endpoints func(labels map[string]string) (source, dest TrafficEndpoint, protocol string)
	// missingLabels is said when every series came back without endpoint
	// labels.
	missingLabels string
}

// trafficSpecs is the whole table.
var trafficSpecs = map[TrafficSource]trafficSpec{
	// Istio standard metrics:
	// https://istio.io/latest/docs/reference/config/metrics/
	// istio_requests_total, istio_request_duration_milliseconds (histogram),
	// istio_tcp_sent_bytes_total, istio_tcp_received_bytes_total,
	// istio_tcp_connections_opened_total; labels reporter, source_workload,
	// source_workload_namespace, destination_workload,
	// destination_workload_namespace, destination_service,
	// destination_service_name, request_protocol, response_code. Missing
	// values are the literal "unknown".
	TrafficIstio: {
		probe:       `count({__name__=~"istio_requests_total|istio_tcp_sent_bytes_total"})`,
		requirement: "Needs Istio's standard metrics (istio_requests_total, istio_tcp_*) scraped from the sidecars or waypoints into this backend.",
		// The source proxy first, the destination proxy second for callers
		// without a proxy of their own; the two meshed branches group by the
		// same labels, so `or` keeps one answer per request. A destination
		// outside the mesh is only ever seen by the source proxy, and is
		// identified by the host it asked for (destination_service), so that
		// branch alone groups by it.
		//
		// KNOWN DOUBLE COUNT, shared with Kiali: when the source proxy lacks
		// the destination's peer metadata (no metadata exchange — a
		// non-HTTP/2 upstream, a peer outside the mesh's trust, an old
		// sidecar), it reports destination_workload="unknown" and the request
		// lands in the external branch, while the destination proxy reports
		// the same request in the meshed branch. Their label sets differ, so
		// `or` cannot dedupe them and that request is counted on two edges.
		branches: []trafficBranch{
			{matchers: `reporter="source",destination_workload!="unknown"`},
			{matchers: `reporter="destination"`},
			{matchers: `reporter="source",destination_workload="unknown"`, extraBy: []string{"destination_service"}, dstUnknown: true},
		},
		srcNamespace: "source_workload_namespace",
		dstNamespace: "destination_workload_namespace",
		keyBy: []string{
			"source_workload", "source_workload_namespace",
			"destination_workload", "destination_workload_namespace",
			"destination_service_name", "request_protocol",
		},
		metrics: []trafficMetric{
			{role: TrafficRoleRate, name: "istio_requests_total"},
			{role: TrafficRoleErrors, name: "istio_requests_total", matchers: `response_code=~"5.."`},
			{role: TrafficRoleBytes, name: `{__name__=~"istio_tcp_sent_bytes_total|istio_tcp_received_bytes_total"`},
			{role: TrafficRoleConnections, name: "istio_tcp_connections_opened_total"},
			{role: TrafficRoleP50, name: "istio_request_duration_milliseconds", quantile: 0.5},
			{role: TrafficRoleP95, name: "istio_request_duration_milliseconds", quantile: 0.95},
			{role: TrafficRoleP99, name: "istio_request_duration_milliseconds", quantile: 0.99},
		},
		latencyScale:  1,
		endpoints:     istioEndpoints,
		missingLabels: "Istio's series came back without source_workload or destination_workload labels. Those are part of Istio's standard metrics; a telemetry customisation that removed them leaves nothing to draw.",
	},

	// Linkerd proxy metrics:
	// https://linkerd.io/2-edge/reference/proxy-metrics/
	// request_total, response_total (classification="success"|"failure"),
	// response_latency_ms (histogram); direction="inbound"|"outbound";
	// Kubernetes labels namespace, deployment, statefulset, daemonset,
	// replicaset (and the scrape config's k8s_job, renamed from job so it
	// does not collide with Prometheus' own `job`), with dst_-prefixed
	// equivalents on outbound traffic. Outbound is measured at the caller,
	// so the caller's labels are the source and dst_* the destination.
	TrafficLinkerd: {
		probe:       `count(request_total{direction="outbound"})`,
		requirement: "Needs the Linkerd proxy's request_total and response_total, as linkerd-viz's Prometheus scrapes them. When linkerd-viz brings its own Prometheus, choose it under Settings → Clusters.",
		// A meshed destination is identified by its dst_* labels; the
		// authority only identifies one outside the mesh, so only that
		// branch groups by it. replicaset is NOT a key: it changes across a
		// rollout while the Deployment does not, and grouping by it would
		// split one edge's percentile in two.
		branches: []trafficBranch{
			{matchers: `direction="outbound",dst_namespace!=""`},
			{matchers: `direction="outbound",dst_namespace=""`, extraBy: []string{"authority"}, dstUnknown: true},
		},
		srcNamespace: "namespace",
		dstNamespace: "dst_namespace",
		keyBy: []string{
			"namespace", "deployment", "statefulset", "daemonset", "k8s_job",
			"dst_namespace", "dst_deployment", "dst_statefulset", "dst_daemonset", "dst_job",
			"dst_service",
		},
		metrics: []trafficMetric{
			{role: TrafficRoleRate, name: "request_total"},
			{role: TrafficRoleErrors, name: "response_total", matchers: `classification="failure"`},
			{role: TrafficRoleP50, name: "response_latency_ms", quantile: 0.5},
			{role: TrafficRoleP95, name: "response_latency_ms", quantile: 0.95},
			{role: TrafficRoleP99, name: "response_latency_ms", quantile: 0.99},
		},
		latencyScale:  1,
		endpoints:     linkerdEndpoints,
		missingLabels: "Linkerd's series came back without the namespace/deployment and dst_* labels. linkerd-viz's Prometheus adds them; a Prometheus scraping the proxies with its own configuration may not.",
	},

	// Grafana Beyla network metrics:
	// https://grafana.com/docs/beyla/latest/network/
	// and OpenTelemetry eBPF Instrumentation, its upstream:
	// https://opentelemetry.io/docs/zero-code/obi/network/
	// beyla_network_flow_bytes_total or obi_network_flow_bytes_total; default
	// attributes k8s.src.owner.name, k8s.src.namespace, k8s.dst.owner.name,
	// k8s.dst.namespace, k8s.cluster.name (dots become underscores in
	// Prometheus). Bytes only: the network feature has no request or latency
	// view, and the HTTP metrics carry no peer workload.
	TrafficBeyla: {
		probe:        `count({__name__=~"beyla_network_flow_bytes_total|obi_network_flow_bytes_total"})`,
		requirement:  "Needs Beyla's or OBI's network metrics (network.enable, beyla_network_flow_bytes_total or obi_network_flow_bytes_total) with the default k8s.src/dst.owner.name and namespace attributes, exported to this backend.",
		branches:     []trafficBranch{{}},
		srcNamespace: "k8s_src_namespace",
		dstNamespace: "k8s_dst_namespace",
		keyBy:        []string{"k8s_src_owner_name", "k8s_src_namespace", "k8s_dst_owner_name", "k8s_dst_namespace"},
		metrics: []trafficMetric{
			{role: TrafficRoleBytes, name: `{__name__=~"beyla_network_flow_bytes_total|obi_network_flow_bytes_total"`},
		},
		endpoints:     beylaEndpoints,
		missingLabels: "Beyla's flow series came back without k8s_src_owner_name or k8s_dst_owner_name. Those are default attributes when Kubernetes decoration is on (attributes.kubernetes.enable).",
	},

	// Caretta: https://github.com/groundcover-com/caretta
	// caretta_links_observed (a gauge of bytes sent over the link so far),
	// labels client_name, client_namespace, client_kind, server_name,
	// server_namespace, server_kind, server_port, role, link_id. The value
	// only grows while the link lives, so rate() reads it as bytes per
	// second and treats a dropped link as a reset.
	TrafficCaretta: {
		probe:        `count(caretta_links_observed)`,
		requirement:  "Needs Caretta's caretta_links_observed metric scraped into this backend.",
		branches:     []trafficBranch{{}},
		srcNamespace: "client_namespace",
		dstNamespace: "server_namespace",
		keyBy:        []string{"client_name", "client_namespace", "client_kind", "server_name", "server_namespace", "server_kind"},
		metrics: []trafficMetric{
			{role: TrafficRoleBytes, name: "caretta_links_observed"},
		},
		endpoints:     carettaEndpoints,
		missingLabels: "Caretta's series came back without client_name or server_name.",
	},

	// Cilium Hubble metrics:
	// https://docs.cilium.io/en/stable/observability/metrics/
	// hubble_flows_processed_total (flow), hubble_http_requests_total and
	// hubble_http_request_duration_seconds (httpV2; labels method, protocol,
	// status, reporter). Endpoint labels exist only when configured. ONLY
	// labelsContext (source_namespace, source_workload, destination_namespace,
	// destination_workload) is read: the sourceContext/destinationContext
	// `source`/`destination` label changes meaning with its option — `pod`
	// fans out per pod, `workload-name` drops the namespace — so grouping by
	// it is either a pod fan-out or a second identity for the same edge.
	// HTTP metrics are off by default. No server-side namespace filter: the
	// label may not exist.
	TrafficHubble: {
		probe:       `count(hubble_flows_processed_total)`,
		requirement: "Needs Hubble metrics with endpoint labels: flow (and httpV2 for requests and latency) with labelsContext=source_namespace,source_workload,destination_namespace,destination_workload.",
		branches:    []trafficBranch{{}},
		keyBy:       []string{"source_namespace", "source_workload", "destination_namespace", "destination_workload"},
		metrics: []trafficMetric{
			// FLOW EVENTS, NOT CONNECTIONS, AND NOT DEDUPLICATED. A flow
			// between pods on two nodes is observed by both nodes' agents
			// (leaving one, arriving at the other), and a policy verdict is an
			// event of its own, so this counts events per second — an activity
			// measure for comparing edges, not a count of anything. The
			// metrics page documents no subtype filter that observes each flow
			// exactly once (subtype="to-endpoint" would drop egress to the
			// world), so none is applied.
			{role: TrafficRoleConnections, name: "hubble_flows_processed_total", matchers: `verdict="FORWARDED"`},
			// The server's view first, the client's second: both report the
			// same request when both ends are Cilium-managed.
			{role: TrafficRoleRate, name: "hubble_http_requests_total", branches: hubbleHTTPBranches},
			{role: TrafficRoleErrors, name: "hubble_http_requests_total", matchers: `status=~"5.."`, branches: hubbleHTTPBranches},
			{role: TrafficRoleP50, name: "hubble_http_request_duration_seconds", quantile: 0.5, branches: hubbleHTTPBranches},
			{role: TrafficRoleP95, name: "hubble_http_request_duration_seconds", quantile: 0.95, branches: hubbleHTTPBranches},
			{role: TrafficRoleP99, name: "hubble_http_request_duration_seconds", quantile: 0.99, branches: hubbleHTTPBranches},
		},
		latencyScale:  1000,
		endpoints:     hubbleEndpoints,
		missingLabels: "Hubble's series came back without endpoint labels. The flow and httpV2 metrics need labelsContext=source_namespace,source_workload,destination_namespace,destination_workload before they name who talked to whom.",
	},
}

// hubbleHTTPBranches applies to Hubble's httpV2 rows only: the flow metric
// has no reporter label, and `reporter="server"` on it would match nothing.
var hubbleHTTPBranches = []trafficBranch{{matchers: `reporter="server"`}, {matchers: `reporter="client"`}}

// TrafficProbes returns the presence check for every source: one instant
// count() each, so the answer is one number whatever the backend holds.
func TrafficProbes() map[TrafficSource]string {
	probes := make(map[TrafficSource]string, len(trafficSpecs))
	for source, spec := range trafficSpecs {
		probes[source] = spec.probe
	}
	return probes
}

// TrafficRequirement says what a source needs, for the empty state.
func TrafficRequirement(source TrafficSource) string {
	return trafficSpecs[source].requirement
}

// TrafficExpressions composes every expression for a source over a window.
//
// namespaces narrows server-side when there are at most five of them and the
// result fits the URL budget; otherwise the expressions go unfiltered and
// MapTraffic filters, so the answer is the same and only its cost differs.
// Empty namespaces means every namespace.
func TrafficExpressions(source TrafficSource, namespaces []NamespaceName, window TrafficWindow) ([]TrafficQuery, error) {
	spec, known := trafficSpecs[source]
	if !known {
		return nil, fmt.Errorf("%w: %q", ErrUnknownTrafficSource, source)
	}
	if _, err := ParseTrafficWindow(string(window)); err != nil || window == "" {
		return nil, fmt.Errorf("%w: %q", ErrUnknownTrafficWindow, window)
	}

	if alternation := namespaceAlternation(spec, namespaces); alternation != "" {
		queries := composeTraffic(spec, window, alternation)
		if allWithinBudget(queries) {
			return queries, nil
		}
	}

	queries := composeTraffic(spec, window, "")
	if !allWithinBudget(queries) {
		return nil, fmt.Errorf("%w: a %s expression", ErrQueryTooLong, source)
	}
	return queries, nil
}

// namespaceAlternation returns the regex alternation that narrows to
// namespaces, or "" when no server-side narrowing applies.
func namespaceAlternation(spec trafficSpec, namespaces []NamespaceName) string {
	if len(namespaces) == 0 || len(namespaces) > trafficNamespaceFilterMax ||
		spec.srcNamespace == "" || spec.dstNamespace == "" {
		return ""
	}

	quoted := make([]string, 0, len(namespaces))
	for _, namespace := range namespaces {
		if namespace != "" {
			quoted = append(quoted, quoteForPromQL(string(namespace)))
		}
	}
	if len(quoted) == 0 {
		return ""
	}
	sort.Strings(quoted)
	return strings.Join(quoted, "|")
}

func allWithinBudget(queries []TrafficQuery) bool {
	for _, query := range queries {
		if !WithinQueryURLBudget(query.Expression) {
			return false
		}
	}
	return true
}

func composeTraffic(spec trafficSpec, window TrafficWindow, alternation string) []TrafficQuery {
	queries := make([]TrafficQuery, 0, len(spec.metrics))

	for _, metric := range spec.metrics {
		branches := spec.branches
		if metric.branches != nil {
			branches = metric.branches
		}

		name := metric.name
		if metric.quantile > 0 {
			name += "_bucket"
		}

		var operands []string
		for _, branch := range branches {
			by := append([]string(nil), spec.keyBy...)
			by = append(by, branch.extraBy...)
			if metric.quantile > 0 {
				by = append([]string{"le"}, by...)
			}

			// EITHER END IN SCOPE. A selector ANDs its matchers, so "source
			// or destination" is two operands joined by `or`, which dedupes
			// the series both return. A branch whose destination has no
			// namespace can only be narrowed by its source.
			filters := []string{""}
			if alternation != "" {
				filters = []string{fmt.Sprintf(`%s=~"%s"`, spec.srcNamespace, alternation)}
				if !branch.dstUnknown {
					filters = append(filters, fmt.Sprintf(`%s=~"%s"`, spec.dstNamespace, alternation))
				}
			}

			for _, filter := range filters {
				operands = append(operands, fmt.Sprintf("sum by (%s) (rate(%s[%s]))",
					strings.Join(by, ", "), selector(name, metric.matchers, branch.matchers, filter), window))
			}
		}

		expression := strings.Join(operands, " or ")
		if metric.quantile > 0 {
			expression = fmt.Sprintf("histogram_quantile(%g, %s)", metric.quantile, expression)
		}
		queries = append(queries, TrafficQuery{Role: metric.role, Expression: expression})
	}
	return queries
}

// selector renders `name{a,b,c}`, or extends a `{__name__=~...` body.
func selector(name string, matchers ...string) string {
	parts := make([]string, 0, len(matchers))
	for _, matcher := range matchers {
		if matcher != "" {
			parts = append(parts, matcher)
		}
	}

	if strings.HasPrefix(name, "{") {
		// A __name__ alternation is already an open selector body.
		if len(parts) == 0 {
			return name + "}"
		}
		return name + "," + strings.Join(parts, ",") + "}"
	}
	if len(parts) == 0 {
		return name
	}
	return name + "{" + strings.Join(parts, ",") + "}"
}

// MapTraffic turns the answers to a source's expressions into a layer.
//
// nodes may be nil, in which case nothing is resolved and nothing is listed as
// unmapped: an endpoint is only "unmapped" against a topology that was given.
// namespaces, when non-empty, keeps the edges with either end in one of them.
//
// The returned layer carries Status answered or answered-empty; the caller
// fills the provenance and the expressions.
func MapTraffic(
	source TrafficSource,
	window TrafficWindow,
	results map[TrafficRole][]PromSeries,
	nodes []TrafficNodeRef,
	namespaces []NamespaceName,
) TrafficLayer {
	layer := TrafficLayer{Source: source, Window: window, Edges: []TrafficEdge{}, Unmapped: []TrafficEndpoint{}}

	spec, known := trafficSpecs[source]
	if !known {
		layer.Status = BackendAnsweredEmpty
		layer.Message = fmt.Sprintf("%q is not a traffic source PodSteer reads.", source)
		return layer
	}

	scope := make(map[string]struct{}, len(namespaces))
	for _, namespace := range namespaces {
		scope[string(namespace)] = struct{}{}
	}

	edges := make(map[string]*TrafficEdge)
	seriesSeen, anonymous := 0, 0

	for _, role := range orderedRoles(results) {
		for _, series := range results[role] {
			if len(series.Points) == 0 {
				continue
			}
			value := series.Points[len(series.Points)-1].Value
			beyond := math.IsInf(value, 1) && isQuantileRole(role)
			if math.IsNaN(value) || (math.IsInf(value, 0) && !beyond) {
				continue
			}
			seriesSeen++

			src, dst, protocol := spec.endpoints(series.Labels)
			if src.anonymous() && dst.anonymous() {
				anonymous++
				continue
			}
			if len(scope) > 0 && !inScope(scope, src) && !inScope(scope, dst) {
				continue
			}

			key := src.key() + "\x01" + dst.key() + "\x01" + protocol
			edge, ok := edges[key]
			if !ok {
				edge = &TrafficEdge{Source: src, Dest: dst, Protocol: protocol, P50: -1, P95: -1, P99: -1}
				edges[key] = edge
			}
			if beyond {
				edge.LatencyBeyondBuckets = true
				continue
			}
			applyRole(edge, role, value, spec.latencyScale)
		}
	}

	for _, edge := range edges {
		if edge.RequestsPerSec <= 0 && edge.ErrorsPerSec <= 0 && edge.BytesPerSec <= 0 && edge.Connections <= 0 {
			// A pair that exchanged nothing in the window has no edge, and a
			// latency with no requests behind it is a histogram's leftover.
			continue
		}
		if edge.Protocol == "" {
			edge.Protocol = "tcp"
			if edge.RequestsPerSec > 0 {
				edge.Protocol = "http"
			}
		}
		layer.Edges = append(layer.Edges, *edge)
	}

	if nodes != nil {
		layer.Unmapped = resolveTrafficNodes(layer.Edges, nodes)
	}

	sort.Slice(layer.Edges, func(i, j int) bool {
		a, b := layer.Edges[i], layer.Edges[j]
		if ka, kb := a.Source.key(), b.Source.key(); ka != kb {
			return ka < kb
		}
		if ka, kb := a.Dest.key(), b.Dest.key(); ka != kb {
			return ka < kb
		}
		return a.Protocol < b.Protocol
	})

	switch {
	case len(layer.Edges) > 0:
		layer.Status = BackendAnswered
	case seriesSeen > 0 && anonymous == seriesSeen:
		layer.Status = BackendAnsweredEmpty
		layer.Message = spec.missingLabels
	case seriesSeen > 0:
		layer.Status = BackendAnsweredEmpty
		layer.Message = fmt.Sprintf("%s answered, and nothing in scope exchanged traffic over the last %s.", sourceTitle(source), window)
	default:
		layer.Status = BackendAnsweredEmpty
		layer.Message = fmt.Sprintf("No %s series over the last %s. %s", sourceTitle(source), window, spec.requirement)
	}
	return layer
}

func sourceTitle(source TrafficSource) string {
	switch source {
	case TrafficIstio:
		return "Istio"
	case TrafficLinkerd:
		return "Linkerd"
	case TrafficBeyla:
		return "Beyla"
	case TrafficCaretta:
		return "Caretta"
	case TrafficHubble:
		return "Hubble"
	}
	return string(source)
}

// orderedRoles makes the walk deterministic.
func orderedRoles(results map[TrafficRole][]PromSeries) []TrafficRole {
	roles := make([]TrafficRole, 0, len(results))
	for role := range results {
		roles = append(roles, role)
	}
	slices.Sort(roles)
	return roles
}

func inScope(scope map[string]struct{}, endpoint TrafficEndpoint) bool {
	_, ok := scope[endpoint.Namespace]
	return ok
}

func applyRole(edge *TrafficEdge, role TrafficRole, value, latencyScale float64) {
	if latencyScale == 0 {
		latencyScale = 1
	}
	switch role {
	case TrafficRoleRate:
		edge.RequestsPerSec += value
	case TrafficRoleErrors:
		edge.ErrorsPerSec += value
	case TrafficRoleBytes:
		edge.BytesPerSec += value
	case TrafficRoleConnections:
		edge.Connections += value
	// A PERCENTILE IS NOT SUMMED, AND TWO FOR ONE EDGE KEEP THE WORSE. Every
	// row groups by exactly the edge's key labels, so two answers for one edge
	// should not happen; when a backend's labels make it happen anyway, the
	// larger is kept so the result does not depend on answer order.
	case TrafficRoleP50:
		edge.P50 = max(edge.P50, value*latencyScale)
	case TrafficRoleP95:
		edge.P95 = max(edge.P95, value*latencyScale)
	case TrafficRoleP99:
		edge.P99 = max(edge.P99, value*latencyScale)
	}
}

func isQuantileRole(role TrafficRole) bool {
	return role == TrafficRoleP50 || role == TrafficRoleP95 || role == TrafficRoleP99
}

// workloadKinds are the topology kinds a workload name can resolve to.
var workloadKinds = map[string]bool{
	"Deployment": true, "StatefulSet": true, "DaemonSet": true,
	"ReplicaSet": true, "Job": true, "CronJob": true,
}

// resolveTrafficNodes sets NodeID on every endpoint it can and returns the
// distinct endpoints that name a workload or Service it could not attach.
//
// BY (NAMESPACE, NAME) ACROSS EVERY CONTROLLER KIND, because no source says
// which kind it means — Istio's source_workload is a Deployment's name or a
// bare pod's, Beyla's owner is whatever it resolved. Two kinds sharing a name
// in one namespace is ambiguous, and an ambiguous endpoint is listed as
// unmapped rather than attached to whichever came first.
func resolveTrafficNodes(edges []TrafficEdge, nodes []TrafficNodeRef) []TrafficEndpoint {
	workloads := make(map[string][]string)
	services := make(map[string][]string)
	for _, node := range nodes {
		key := node.Namespace + "/" + node.Name
		switch {
		case workloadKinds[node.APIKind]:
			workloads[key] = append(workloads[key], node.ID)
		case node.APIKind == "Service":
			services[key] = append(services[key], node.ID)
		}
	}

	unmapped := make(map[string]TrafficEndpoint)
	resolve := func(endpoint *TrafficEndpoint) {
		if endpoint.Unknown || endpoint.External != "" || endpoint.Namespace == "" {
			return
		}
		if endpoint.Workload != "" {
			if ids := workloads[endpoint.Namespace+"/"+endpoint.Workload]; len(ids) == 1 {
				endpoint.NodeID = ids[0]
				return
			} else if len(ids) > 1 {
				unmapped[endpoint.key()] = *endpoint
				return
			}
		}
		if endpoint.Service != "" {
			if ids := services[endpoint.Namespace+"/"+endpoint.Service]; len(ids) == 1 {
				endpoint.NodeID = ids[0]
				return
			}
		}
		if endpoint.Workload != "" || endpoint.Service != "" {
			unmapped[endpoint.key()] = *endpoint
		}
	}

	for i := range edges {
		resolve(&edges[i].Source)
		resolve(&edges[i].Dest)
	}

	list := make([]TrafficEndpoint, 0, len(unmapped))
	for _, endpoint := range unmapped {
		list = append(list, endpoint)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].key() < list[j].key() })
	return list
}

// --- Label schemas, one reader per source ----------------------------------

func meshKnown(value string) string {
	if value == "unknown" {
		return ""
	}
	return value
}

func istioEndpoints(labels map[string]string) (TrafficEndpoint, TrafficEndpoint, string) {
	src := TrafficEndpoint{
		Namespace: meshKnown(labels["source_workload_namespace"]),
		Workload:  meshKnown(labels["source_workload"]),
	}
	if src.Workload == "" {
		src.Unknown = true
	}

	dst := TrafficEndpoint{
		Namespace: meshKnown(labels["destination_workload_namespace"]),
		Workload:  meshKnown(labels["destination_workload"]),
	}
	service := meshKnown(labels["destination_service_name"])
	switch {
	case dst.Workload != "":
		dst.Service = service
	case meshKnown(labels["destination_service"]) != "":
		// No workload behind it: a destination outside the mesh, named by
		// the host the caller asked for.
		dst.External = meshKnown(labels["destination_service"])
	case service != "":
		dst.External = service
	default:
		dst.Unknown = true
	}

	protocol := labels["request_protocol"]
	if protocol == "unknown" {
		protocol = ""
	}
	return src, dst, protocol
}

func linkerdEndpoints(labels map[string]string) (TrafficEndpoint, TrafficEndpoint, string) {
	src := TrafficEndpoint{
		Namespace: labels["namespace"],
		Workload:  firstNonEmptyLabel(labels, "deployment", "statefulset", "daemonset", "k8s_job"),
	}
	if src.Workload == "" {
		src.Unknown = true
	}

	dst := TrafficEndpoint{
		Namespace: labels["dst_namespace"],
		Workload:  firstNonEmptyLabel(labels, "dst_deployment", "dst_statefulset", "dst_daemonset", "dst_job"),
		Service:   labels["dst_service"],
	}
	if dst.Namespace == "" && dst.Workload == "" {
		// Not a meshed destination: the authority the caller asked for.
		dst.Service = ""
		if authority := labels["authority"]; authority != "" {
			dst.External = authority
		} else {
			dst.Unknown = true
		}
	}
	return src, dst, "http"
}

func beylaEndpoints(labels map[string]string) (TrafficEndpoint, TrafficEndpoint, string) {
	read := func(owner, namespace string) TrafficEndpoint {
		endpoint := TrafficEndpoint{Namespace: labels[namespace], Workload: labels[owner]}
		if endpoint.Workload == "" {
			endpoint.Unknown = true
		}
		return endpoint
	}
	return read("k8s_src_owner_name", "k8s_src_namespace"), read("k8s_dst_owner_name", "k8s_dst_namespace"), "tcp"
}

func carettaEndpoints(labels map[string]string) (TrafficEndpoint, TrafficEndpoint, string) {
	read := func(name, namespace, kind string) TrafficEndpoint {
		endpoint := TrafficEndpoint{Namespace: labels[namespace]}
		value := labels[name]
		switch strings.ToLower(labels[kind]) {
		case "":
			if value == "" {
				endpoint.Unknown = true
			} else {
				endpoint.Workload = value
			}
		case "service":
			endpoint.Service = value
		case "external":
			endpoint.Namespace = ""
			endpoint.External = value
		case "node":
			endpoint.Namespace = ""
			endpoint.External = "node/" + value
		default:
			endpoint.Workload = value
		}
		if value == "" {
			endpoint.Unknown = true
		}
		return endpoint
	}
	return read("client_name", "client_namespace", "client_kind"), read("server_name", "server_namespace", "server_kind"), "tcp"
}

func hubbleEndpoints(labels map[string]string) (TrafficEndpoint, TrafficEndpoint, string) {
	read := func(namespace, workload string) TrafficEndpoint {
		endpoint := TrafficEndpoint{Namespace: labels[namespace], Workload: labels[workload]}
		if endpoint.Workload == "" {
			// The world, the host, or a pod with no workload: labelsContext
			// leaves the label empty and says nothing more.
			endpoint.Unknown = true
		}
		return endpoint
	}
	// Protocol left empty so flows and HTTP for the same pair land on one
	// edge; MapTraffic names it from what was measured.
	return read("source_namespace", "source_workload"), read("destination_namespace", "destination_workload"), ""
}

func firstNonEmptyLabel(labels map[string]string, names ...string) string {
	for _, name := range names {
		if value := labels[name]; value != "" {
			return value
		}
	}
	return ""
}
