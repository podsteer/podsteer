package domain_test

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

// The fixtures under testdata/prom/ are Prometheus instant-query answers built
// from each source's DOCUMENTED label set (checked 2026-10-02):
//
//   - istio.json   https://istio.io/latest/docs/reference/config/metrics/
//   - linkerd.json https://linkerd.io/2-edge/reference/proxy-metrics/
//   - beyla.json   https://grafana.com/docs/beyla/latest/network/ and
//     https://opentelemetry.io/docs/zero-code/obi/network/
//   - caretta.json https://github.com/groundcover-com/caretta (README example)
//   - hubble.json  https://docs.cilium.io/en/stable/observability/metrics/
//     (labelsContext labels; an empty destination for the world; a +Inf p99)
//   - linkerd-two-authorities.json — one meshed pair answered twice, as a
//     grouping by authority returns it
//   - hubble-nolabels.json — Hubble's default flow metric, no context options
//
// Each file maps a role to the answer that role's expression would get.

type fixtureEnvelope struct {
	Status string `json:"status"`
	Data   struct {
		Result []struct {
			Metric map[string]string `json:"metric"`
			Value  []json.RawMessage `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

func loadTrafficFixture(t *testing.T, name string) map[domain.TrafficRole][]domain.PromSeries {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("testdata", "prom", name+".json"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	var byRole map[string]fixtureEnvelope
	if err := json.Unmarshal(raw, &byRole); err != nil {
		t.Fatalf("decoding fixture: %v", err)
	}

	results := make(map[domain.TrafficRole][]domain.PromSeries, len(byRole))
	for role, envelope := range byRole {
		if envelope.Status != "success" {
			t.Fatalf("fixture %s/%s is not a success envelope", name, role)
		}
		for _, result := range envelope.Data.Result {
			var text string
			if err := json.Unmarshal(result.Value[1], &text); err != nil {
				t.Fatalf("fixture value: %v", err)
			}
			value, err := strconv.ParseFloat(text, 64)
			if err != nil {
				t.Fatalf("fixture value %q: %v", text, err)
			}
			series := domain.PromSeries{Labels: result.Metric}
			// NaN is dropped the way the adapter's decodePoint drops it.
			if !math.IsNaN(value) {
				series.Points = []domain.SeriesPoint{{Value: value}}
			}
			results[domain.TrafficRole(role)] = append(results[domain.TrafficRole(role)], series)
		}
	}
	return results
}

// splitTopLevelOr splits an expression on ` or ` outside parentheses and
// strings.
func splitTopLevelOr(expression string) []string {
	var parts []string
	depth, start := 0, 0
	for i := 0; i < len(expression); i++ {
		switch expression[i] {
		case '"':
			end := scanPromString(expression[i:])
			if end < 0 {
				return nil
			}
			i += end - 1
		case '(':
			depth++
		case ')':
			depth--
		case ' ':
			if depth == 0 && strings.HasPrefix(expression[i:], " or ") {
				parts = append(parts, expression[start:i])
				start = i + len(" or ")
				i += len(" or ") - 1
			}
		}
	}
	return append(parts, expression[start:])
}

// trafficAggregates reports whether every top-level operand of an expression
// is an aggregation, looking through one histogram_quantile.
func trafficAggregates(expression string) bool {
	if rest, ok := strings.CutPrefix(expression, "histogram_quantile("); ok {
		if matchingParen("("+rest) != len(rest) {
			return false
		}
		comma := strings.Index(rest, ", ")
		if comma < 0 {
			return false
		}
		inner := rest[comma+2 : len(rest)-1]
		for _, operand := range splitTopLevelOr(inner) {
			// The buckets must survive the aggregation or there is nothing
			// to take a quantile of.
			if outerAggregation(operand) != "sum" || !strings.Contains(operand, "by (le, ") {
				return false
			}
		}
		return true
	}

	operands := splitTopLevelOr(expression)
	if len(operands) == 0 {
		return false
	}
	for _, operand := range operands {
		if outerAggregation(operand) == "" {
			return false
		}
	}
	return true
}

func TestEveryTrafficExpressionAggregates(t *testing.T) {
	scopes := map[string][]domain.NamespaceName{
		"all":      nil,
		"two":      {"storefront", "payments.eu"},
		"too many": {"a", "b", "c", "d", "e", "f"},
	}
	windows := []domain.TrafficWindow{domain.TrafficWindow5m, domain.TrafficWindow15m, domain.TrafficWindow1h}

	for _, source := range domain.TrafficSourceNames() {
		for scopeName, namespaces := range scopes {
			for _, window := range windows {
				queries, err := domain.TrafficExpressions(source, namespaces, window)
				if err != nil {
					t.Fatalf("%s/%s/%s: %v", source, scopeName, window, err)
				}
				if len(queries) == 0 {
					t.Fatalf("%s: no expressions", source)
				}
				for _, query := range queries {
					if !trafficAggregates(query.Expression) {
						t.Errorf("%s %s (%s): not aggregated: %s", source, query.Role, scopeName, query.Expression)
					}
					if bad := badEscape(query.Expression); bad != "" {
						t.Errorf("%s %s: escape %q Prometheus would refuse: %s", source, query.Role, bad, query.Expression)
					}
					if !domain.WithinQueryURLBudget(query.Expression) {
						t.Errorf("%s %s: over the URL budget", source, query.Role)
					}
					if !strings.Contains(query.Expression, "["+string(window)+"]") {
						t.Errorf("%s %s: window %s missing: %s", source, query.Role, window, query.Expression)
					}
					if strings.Contains(query.Expression, "pod") && !strings.Contains(query.Expression, "_pod") {
						t.Errorf("%s %s: groups by pod: %s", source, query.Role, query.Expression)
					}
				}
			}
		}
	}
}

func TestTheAggregationCheckRejectsAnUnaggregatedBranch(t *testing.T) {
	if trafficAggregates(`sum by (a) (rate(x[5m])) or rate(y[5m])`) {
		t.Error("an `or` with a bare rate branch passed")
	}
	if trafficAggregates(`histogram_quantile(0.5, rate(x_bucket[5m]))`) {
		t.Error("a quantile over unaggregated buckets passed")
	}
	if !trafficAggregates(`histogram_quantile(0.5, sum by (le, a) (rate(x_bucket[5m])) or sum by (le, a) (rate(x_bucket[5m])))`) {
		t.Error("a well-formed quantile failed")
	}
}

func TestEveryTrafficProbeIsACount(t *testing.T) {
	probes := domain.TrafficProbes()
	if len(probes) != len(domain.TrafficSourceNames()) {
		t.Fatalf("%d probes for %d sources", len(probes), len(domain.TrafficSourceNames()))
	}
	for source, probe := range probes {
		if outerAggregation(probe) != "count" {
			t.Errorf("%s probe is not one count(): %s", source, probe)
		}
		if domain.TrafficRequirement(source) == "" {
			t.Errorf("%s has no requirement sentence for the empty state", source)
		}
	}
}

func TestProbesNameTheDocumentedSignatureMetrics(t *testing.T) {
	probes := domain.TrafficProbes()
	want := map[domain.TrafficSource][]string{
		domain.TrafficIstio:   {"istio_requests_total"},
		domain.TrafficLinkerd: {"request_total", `direction="outbound"`},
		domain.TrafficBeyla:   {"beyla_network_flow_bytes_total", "obi_network_flow_bytes_total"},
		domain.TrafficCaretta: {"caretta_links_observed"},
		domain.TrafficHubble:  {"hubble_flows_processed_total"},
	}
	for source, fragments := range want {
		for _, fragment := range fragments {
			if !strings.Contains(probes[source], fragment) {
				t.Errorf("%s probe lacks %q: %s", source, fragment, probes[source])
			}
		}
	}
}

func TestNamespaceFilterNarrowsEitherEnd(t *testing.T) {
	queries, err := domain.TrafficExpressions(domain.TrafficIstio, []domain.NamespaceName{"storefront"}, domain.TrafficWindow5m)
	if err != nil {
		t.Fatal(err)
	}
	rate := queries[0].Expression
	for _, fragment := range []string{`source_workload_namespace=~"storefront"`, `destination_workload_namespace=~"storefront"`} {
		if !strings.Contains(rate, fragment) {
			t.Errorf("missing %s in %s", fragment, rate)
		}
	}

	unfiltered, err := domain.TrafficExpressions(domain.TrafficIstio, []domain.NamespaceName{"a", "b", "c", "d", "e", "f"}, domain.TrafficWindow5m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(unfiltered[0].Expression, "=~\"a|") {
		t.Errorf("six namespaces were filtered server-side: %s", unfiltered[0].Expression)
	}

	// Hubble's namespace labels exist only when configured, so it is never
	// filtered server-side.
	hubble, err := domain.TrafficExpressions(domain.TrafficHubble, []domain.NamespaceName{"storefront"}, domain.TrafficWindow5m)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range hubble {
		if strings.Contains(query.Expression, "storefront") {
			t.Errorf("hubble filtered server-side: %s", query.Expression)
		}
	}
}

func TestUnknownSourceAndWindowAreRefused(t *testing.T) {
	if _, err := domain.TrafficExpressions("zipkin", nil, domain.TrafficWindow5m); !errors.Is(err, domain.ErrUnknownTrafficSource) {
		t.Errorf("unknown source: %v", err)
	}
	if _, err := domain.TrafficExpressions(domain.TrafficIstio, nil, "7m"); !errors.Is(err, domain.ErrUnknownTrafficWindow) {
		t.Errorf("unknown window: %v", err)
	}
	if _, err := domain.ParseTrafficWindow("1h]) or vector(1"); !errors.Is(err, domain.ErrUnknownTrafficWindow) {
		t.Errorf("an injected window was accepted: %v", err)
	}
}

func findEdge(t *testing.T, layer domain.TrafficLayer, src, dst string) domain.TrafficEdge {
	t.Helper()
	name := func(e domain.TrafficEndpoint) string {
		switch {
		case e.Workload != "":
			return e.Namespace + "/" + e.Workload
		case e.Service != "":
			return e.Namespace + "/svc/" + e.Service
		case e.External != "":
			return "external:" + e.External
		default:
			return "unknown"
		}
	}
	for _, edge := range layer.Edges {
		if name(edge.Source) == src && name(edge.Dest) == dst {
			return edge
		}
	}
	var have []string
	for _, edge := range layer.Edges {
		have = append(have, name(edge.Source)+" -> "+name(edge.Dest))
	}
	t.Fatalf("no edge %s -> %s; have %v", src, dst, have)
	return domain.TrafficEdge{}
}

func storefrontNodes() []domain.TrafficNodeRef {
	return []domain.TrafficNodeRef{
		{ID: "d/frontend", APIKind: "Deployment", Name: "frontend", Namespace: "storefront"},
		{ID: "d/cart", APIKind: "Deployment", Name: "cart", Namespace: "storefront"},
		{ID: "s/cart", APIKind: "Service", Name: "cart", Namespace: "storefront"},
		// redis exists twice — a Deployment and a StatefulSet of one name —
		// so an endpoint naming it is ambiguous.
		{ID: "d/redis", APIKind: "Deployment", Name: "redis", Namespace: "storefront"},
		{ID: "ss/redis", APIKind: "StatefulSet", Name: "redis", Namespace: "storefront"},
		{ID: "d/web", APIKind: "Deployment", Name: "web", Namespace: "storefront"},
		{ID: "d/api", APIKind: "Deployment", Name: "api", Namespace: "storefront"},
		{ID: "ss/db", APIKind: "StatefulSet", Name: "db", Namespace: "storefront"},
	}
}

func TestMapTrafficIstio(t *testing.T) {
	layer := domain.MapTraffic(domain.TrafficIstio, domain.TrafficWindow5m, loadTrafficFixture(t, "istio"), storefrontNodes(), nil)

	if layer.Status != domain.BackendAnswered {
		t.Fatalf("status %s: %s", layer.Status, layer.Message)
	}

	cart := findEdge(t, layer, "storefront/frontend", "storefront/cart")
	if cart.RequestsPerSec != 12.5 || cart.ErrorsPerSec != 0.25 || cart.P50 != 4.2 || cart.P95 != 18 || cart.P99 != 42.5 {
		t.Errorf("frontend->cart: %+v", cart)
	}
	if cart.Source.NodeID != "d/frontend" || cart.Dest.NodeID != "d/cart" || cart.Dest.Service != "cart" {
		t.Errorf("frontend->cart mapping: %+v -> %+v", cart.Source, cart.Dest)
	}
	if cart.Protocol != "http" {
		t.Errorf("protocol %q", cart.Protocol)
	}

	external := findEdge(t, layer, "storefront/frontend", "external:httpbin.org")
	if external.P50 != -1 {
		t.Errorf("a NaN quantile became %v rather than not-exposed", external.P50)
	}

	inbound := findEdge(t, layer, "unknown", "storefront/frontend")
	if !inbound.Source.Unknown || inbound.RequestsPerSec != 3 {
		t.Errorf("unknown caller: %+v", inbound)
	}

	redis := findEdge(t, layer, "storefront/cart", "storefront/redis")
	if redis.BytesPerSec != 2048 || redis.Connections != 0.5 || redis.Protocol != "tcp" || redis.P50 != -1 {
		t.Errorf("cart->redis: %+v", redis)
	}
	if redis.Dest.NodeID != "" {
		t.Errorf("an ambiguous name was attached to %q", redis.Dest.NodeID)
	}
	if len(layer.Unmapped) != 1 || layer.Unmapped[0].Workload != "redis" {
		t.Errorf("unmapped: %+v", layer.Unmapped)
	}
}

func TestMapTrafficLinkerd(t *testing.T) {
	layer := domain.MapTraffic(domain.TrafficLinkerd, domain.TrafficWindow15m, loadTrafficFixture(t, "linkerd"), storefrontNodes(), nil)

	api := findEdge(t, layer, "storefront/web", "storefront/api")
	if api.RequestsPerSec != 20 || api.ErrorsPerSec != 1 || api.P99 != 90 || api.Dest.NodeID != "d/api" {
		t.Errorf("web->api: %+v", api)
	}
	db := findEdge(t, layer, "storefront/api", "storefront/db")
	if db.Dest.NodeID != "ss/db" || db.P99 != -1 || db.P95 != 9 {
		t.Errorf("api->db: %+v", db)
	}
	ext := findEdge(t, layer, "storefront/web", "external:api.github.com:443")
	if ext.RequestsPerSec != 0.1 {
		t.Errorf("web->external: %+v", ext)
	}
	if len(layer.Unmapped) != 0 {
		t.Errorf("unmapped: %+v", layer.Unmapped)
	}
}

func TestMapTrafficBeyla(t *testing.T) {
	layer := domain.MapTraffic(domain.TrafficBeyla, domain.TrafficWindow1h, loadTrafficFixture(t, "beyla"), storefrontNodes(), nil)

	if got := findEdge(t, layer, "storefront/web", "storefront/api"); got.BytesPerSec != 5120 || got.P50 != -1 || got.RequestsPerSec != 0 {
		t.Errorf("web->api: %+v", got)
	}
	findEdge(t, layer, "storefront/api", "storefront/web")
	if got := findEdge(t, layer, "storefront/api", "unknown"); !got.Dest.Unknown {
		t.Errorf("an unattributed flow: %+v", got)
	}
	// The idle pair answered 0 and is not an edge.
	for _, edge := range layer.Edges {
		if edge.Source.Workload == "idle" {
			t.Errorf("an idle pair became an edge: %+v", edge)
		}
	}
}

func TestMapTrafficCaretta(t *testing.T) {
	layer := domain.MapTraffic(domain.TrafficCaretta, domain.TrafficWindow5m, loadTrafficFixture(t, "caretta"), nil, nil)

	svc := findEdge(t, layer, "demo-ng/checkoutservice", "demo-ng/svc/productcatalogservice")
	if svc.BytesPerSec != 84.5 {
		t.Errorf("checkout->catalog: %+v", svc)
	}
	findEdge(t, layer, "demo-ng/checkoutservice", "external:stripe.com")

	// No topology given: nothing resolved, nothing listed as unmapped.
	if len(layer.Unmapped) != 0 || svc.Source.NodeID != "" {
		t.Errorf("resolved without a topology: %+v %+v", layer.Unmapped, svc.Source)
	}
}

func TestMapTrafficHubble(t *testing.T) {
	layer := domain.MapTraffic(domain.TrafficHubble, domain.TrafficWindow5m, loadTrafficFixture(t, "hubble"), storefrontNodes(), nil)

	web := findEdge(t, layer, "storefront/web", "storefront/api")
	if web.Connections != 40 || web.RequestsPerSec != 10 || web.P95 != 20 || web.Protocol != "http" {
		t.Errorf("web->api: %+v", web)
	}
	// p99 fell in the +Inf bucket: still -1, but said to be beyond the
	// buckets rather than not exposed.
	if web.P99 != -1 || !web.LatencyBeyondBuckets {
		t.Errorf("a +Inf percentile: p99 %v, beyond %v", web.P99, web.LatencyBeyondBuckets)
	}
	db := findEdge(t, layer, "storefront/api", "storefront/db")
	if db.Connections != 7 || db.Protocol != "tcp" || db.Dest.NodeID != "ss/db" || db.LatencyBeyondBuckets {
		t.Errorf("api->db: %+v", db)
	}
	// The world: labelsContext leaves the destination empty.
	findEdge(t, layer, "storefront/web", "unknown")
}

// ONE PERCENTILE PER EDGE, whatever order the answers arrive in. The
// expressions no longer group a meshed Linkerd pair by authority; this is
// the answer such a grouping produced, and the edge still gets one value.
func TestTwoAnswersForOneEdgeGiveOneDeterministicPercentile(t *testing.T) {
	results := loadTrafficFixture(t, "linkerd-two-authorities")

	for range 2 {
		layer := domain.MapTraffic(domain.TrafficLinkerd, domain.TrafficWindow5m, results, nil, nil)
		if len(layer.Edges) != 1 {
			t.Fatalf("edges %+v", layer.Edges)
		}
		edge := layer.Edges[0]
		if edge.RequestsPerSec != 10 || edge.P95 != 80 {
			t.Errorf("rate %v (want the sum, 10), p95 %v (want the worse, 80)", edge.RequestsPerSec, edge.P95)
		}
		slices.Reverse(results[domain.TrafficRoleP95])
	}
}

// Every row of a source groups by the same labels in each branch, so a
// percentile and a rate describe the same edge — and a meshed Linkerd or
// Istio branch never groups by the label that only identifies an external
// destination.
func TestQuantilesGroupByExactlyTheEdgeKey(t *testing.T) {
	byClause := regexp.MustCompile(`sum by \(([^)]*)\)`)
	for _, source := range domain.TrafficSourceNames() {
		queries, err := domain.TrafficExpressions(source, nil, domain.TrafficWindow5m)
		if err != nil {
			t.Fatal(err)
		}
		var rateGroups []string
		for _, query := range queries {
			if query.Role == domain.TrafficRoleRate || (rateGroups == nil && query.Role == domain.TrafficRoleBytes) {
				for _, match := range byClause.FindAllStringSubmatch(query.Expression, -1) {
					rateGroups = append(rateGroups, match[1])
				}
			}
		}
		for _, query := range queries {
			if !strings.HasPrefix(query.Expression, "histogram_quantile(") {
				continue
			}
			for i, match := range byClause.FindAllStringSubmatch(query.Expression, -1) {
				if want := "le, " + rateGroups[i]; match[1] != want {
					t.Errorf("%s %s operand %d groups by (%s), the rate by (%s)", source, query.Role, i, match[1], rateGroups[i])
				}
			}
		}
	}

	linkerd, _ := domain.TrafficExpressions(domain.TrafficLinkerd, nil, domain.TrafficWindow5m)
	for _, operand := range splitTopLevelOr(strings.TrimSuffix(strings.TrimPrefix(linkerd[2].Expression, "histogram_quantile(0.5, "), ")")) {
		if strings.Contains(operand, `dst_namespace!=""`) && strings.Contains(operand, "authority") {
			t.Errorf("a meshed Linkerd operand groups by authority: %s", operand)
		}
		if strings.Contains(operand, "replicaset") {
			t.Errorf("a Linkerd operand groups by replicaset: %s", operand)
		}
	}
	istio, _ := domain.TrafficExpressions(domain.TrafficIstio, nil, domain.TrafficWindow5m)
	for _, operand := range splitTopLevelOr(istio[0].Expression) {
		if strings.Contains(operand, `destination_workload!="unknown"`) && strings.Contains(operand, "destination_service,") {
			t.Errorf("a meshed Istio operand groups by destination_service: %s", operand)
		}
	}
}

func TestMapTrafficSaysWhichLabelsWereMissing(t *testing.T) {
	layer := domain.MapTraffic(domain.TrafficHubble, domain.TrafficWindow5m, loadTrafficFixture(t, "hubble-nolabels"), nil, nil)

	if layer.Status != domain.BackendAnsweredEmpty || len(layer.Edges) != 0 {
		t.Fatalf("status %s, %d edges", layer.Status, len(layer.Edges))
	}
	if !strings.Contains(layer.Message, "labelsContext") {
		t.Errorf("message does not say what is missing: %s", layer.Message)
	}
}

func TestMapTrafficWithNoSeriesNamesTheRequirement(t *testing.T) {
	layer := domain.MapTraffic(domain.TrafficCaretta, domain.TrafficWindow5m, nil, nil, nil)
	if layer.Status != domain.BackendAnsweredEmpty || !strings.Contains(layer.Message, "caretta_links_observed") {
		t.Errorf("%s: %s", layer.Status, layer.Message)
	}
	if layer.Edges == nil || layer.Unmapped == nil {
		t.Error("nil slices would cross the bridge as null")
	}
}

func TestMapTrafficKeepsEdgesTouchingTheScope(t *testing.T) {
	results := map[domain.TrafficRole][]domain.PromSeries{
		domain.TrafficRoleBytes: {
			{Labels: map[string]string{"k8s_src_owner_name": "a", "k8s_src_namespace": "in", "k8s_dst_owner_name": "b", "k8s_dst_namespace": "out"}, Points: []domain.SeriesPoint{{Value: 1}}},
			{Labels: map[string]string{"k8s_src_owner_name": "c", "k8s_src_namespace": "out", "k8s_dst_owner_name": "d", "k8s_dst_namespace": "elsewhere"}, Points: []domain.SeriesPoint{{Value: 1}}},
		},
	}
	layer := domain.MapTraffic(domain.TrafficBeyla, domain.TrafficWindow5m, results, nil, []domain.NamespaceName{"in"})
	if len(layer.Edges) != 1 || layer.Edges[0].Source.Workload != "a" {
		t.Errorf("edges: %+v", layer.Edges)
	}
}
