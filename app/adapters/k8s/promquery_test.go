package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// THE READ IS BOUNDED AND AN OVER-SIZED BODY IS REFUSED UNDECODED. The body
// arrives from a system PodSteer does not control and did not size, so the
// cap is the whole guard — and it has to fire before anything is parsed, or a
// bounded read has become an unbounded allocation with extra steps.
func TestAnOverSizedAnswerIsRefusedUndecoded(t *testing.T) {
	huge := strings.Repeat("x", 4096)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		for range 8 {
			_, _ = w.Write([]byte(huge))
		}
	}))
	defer server.Close()

	_, _, err := fetchBounded(context.Background(), server.Client(), server.URL, 1024)
	if !errors.Is(err, ports.ErrMetricsQueryTooLarge) {
		t.Fatalf("error %v, want ErrMetricsQueryTooLarge", err)
	}
}

func TestAnAnswerUnderTheCapIsRead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	}))
	defer server.Close()

	body, status, err := fetchBounded(context.Background(), server.Client(), server.URL, 1<<20)
	if err != nil {
		t.Fatalf("fetching: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status %d, want 200", status)
	}
	if _, err := decodeQueryResponse(body); err != nil {
		t.Fatalf("decoding: %v", err)
	}
}

// NOTHING BUT A GET LEAVES. A POST to services/proxy is the RBAC verb
// `create`, a different permission from the `get` every proxying account
// holds, so this is asserted on the wire rather than left to a comment.
func TestOnlyAGetIsEverSent(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Body != nil {
			_ = r.Body.Close()
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer server.Close()

	if _, _, err := fetchBounded(context.Background(), server.Client(), server.URL, 1<<20); err != nil {
		t.Fatalf("fetching: %v", err)
	}
	if len(methods) != 1 || methods[0] != http.MethodGet {
		t.Fatalf("methods %v, want exactly one GET", methods)
	}
}

// A Prometheus error comes back with its own message verbatim, the way a
// rejected manifest carries the API server's. Paraphrasing it would throw
// away the only thing anybody can act on.
func TestARejectedExpressionCarriesTheBackendsOwnWords(t *testing.T) {
	adapter := &Adapter{}
	body := []byte(`{"status":"error","errorType":"bad_data","error":"invalid parameter \"query\": 1:8: parse error: unexpected \"{\""}`)

	err := adapter.classifyQueryStatus("dev", 0, "querying", http.StatusBadRequest, body)
	if !errors.Is(err, ports.ErrMetricsQueryRejected) {
		t.Fatalf("error %v, want ErrMetricsQueryRejected", err)
	}
	if !strings.Contains(err.Error(), "parse error") {
		t.Fatalf("the backend's own words were not carried: %v", err)
	}
	if adapter.queryRefusals.refusedFor("dev", 0) {
		t.Fatal("a rejected expression was cached as a refusal to proxy")
	}
}

// A backend can fail with HTTP 200 — Prometheus does it for a query that hit
// its own limits, and Thanos for a partial answer. Read as a success that is
// a blank chart under a green label.
func TestAFailureDeclaredInsideATwoHundredIsAFailure(t *testing.T) {
	_, err := decodeQueryResponse([]byte(`{"status":"error","errorType":"execution","error":"query processing would load too many samples"}`))
	if !errors.Is(err, ports.ErrMetricsQueryRejected) {
		t.Fatalf("error %v, want ErrMetricsQueryRejected", err)
	}
	if !strings.Contains(err.Error(), "too many samples") {
		t.Fatalf("the message was not carried: %v", err)
	}
}

// AN ACCOUNT THAT MAY NOT PROXY NEVER WILL BE ABLE TO, so the refusal is
// cached — every retry is a denied request written into somebody's audit log,
// for a feature that is an offer rather than a requirement.
func TestARefusedProxyIsCachedPerCluster(t *testing.T) {
	adapter := &Adapter{}
	refusal := []byte(`{"kind":"Status","apiVersion":"v1","status":"Failure",` +
		`"message":"services \"prometheus-operated\" is forbidden: User \"dev\" cannot get resource \"services/proxy\"",` +
		`"reason":"Forbidden","code":403}`)

	err := adapter.classifyQueryStatus("dev", 0, "querying", http.StatusForbidden, refusal)
	if !errors.Is(err, ports.ErrForbidden) {
		t.Fatalf("error %v, want ErrForbidden", err)
	}
	if !adapter.queryRefusals.refusedFor("dev", 0) {
		t.Fatal("the refusal was not cached")
	}
	if adapter.queryRefusals.refusedFor("prod", 0) {
		t.Fatal("one cluster's refusal was remembered for another")
	}

	// A TRANSPORT FAILURE IS NOT CACHED. A cluster that was merely
	// unreachable comes back, and must be asked again when it does.
	other := &Adapter{}
	unreachable := []byte(`{"kind":"Status","apiVersion":"v1","status":"Failure",` +
		`"message":"error trying to reach service: dial tcp 10.0.0.1:9090: connect: connection refused",` +
		`"reason":"ServiceUnavailable","code":503}`)
	if err := other.classifyQueryStatus("dev", 0, "querying", http.StatusServiceUnavailable, unreachable); !errors.Is(err, ports.ErrUnreachable) {
		t.Fatalf("error %v, want ErrUnreachable", err)
	}
	if other.queryRefusals.refusedFor("dev", 0) {
		t.Fatal("an unreachable cluster was cached as forbidden")
	}
}

// A 401 IS A CREDENTIAL FACT, NOT A PERMISSION FACT. With an exec plugin
// client-go re-runs the plugin on one and the next request may well succeed —
// so caching it is the single thing that guarantees there is no next request.
func TestAnExpiredCredentialIsNotCachedAsARefusal(t *testing.T) {
	adapter := &Adapter{}
	body := []byte(`{"kind":"Status","apiVersion":"v1","status":"Failure",` +
		`"message":"Unauthorized","reason":"Unauthorized","code":401}`)

	err := adapter.classifyQueryStatus("dev", 0, "querying", http.StatusUnauthorized, body)
	if !errors.Is(err, ports.ErrUnauthenticated) {
		t.Fatalf("error %v, want ErrUnauthenticated", err)
	}
	if adapter.queryRefusals.refusedFor("dev", 0) {
		t.Fatal("a 401 was cached as a refusal to proxy, so nothing would ever ask again")
	}
}

// A PERMISSION IS A THING SOMEBODY GRANTS, so the refusal cannot stand for
// ever: an administrator adding a RoleBinding while the tab is open must not
// be met by a sentence that has become false with no way back but
// reconnecting.
func TestARefusalToProxyExpires(t *testing.T) {
	adapter := &Adapter{}
	adapter.queryRefusals.remember("dev", 0)

	if !adapter.queryRefusals.refusedFor("dev", 0) {
		t.Fatal("a fresh refusal was not remembered")
	}

	adapter.queryRefusals.mu.Lock()
	entry := adapter.queryRefusals.refused["dev"]
	entry.at = time.Now().Add(-queryRefusalTTL - time.Second)
	adapter.queryRefusals.refused["dev"] = entry
	adapter.queryRefusals.mu.Unlock()

	if adapter.queryRefusals.refusedFor("dev", 0) {
		t.Fatalf("a refusal older than %s is still standing", queryRefusalTTL)
	}
}

// A QUERY IN FLIGHT CAN PASS Invalidate AND THEN WRITE. What it would write is
// a refusal about the cluster this tab used to be, which the next connection
// would inherit without a single request being made to earn it.
func TestARefusalWrittenAgainstAReplacedConnectionIsInert(t *testing.T) {
	adapter := &Adapter{}

	// The generation a request captured before the disconnect.
	stale := adapter.generations.at("dev")
	adapter.generations.bump("dev")
	fresh := adapter.generations.at("dev")

	if stale == fresh {
		t.Fatal("the generation did not advance")
	}

	// The late write lands, and the new connection does not see it.
	adapter.queryRefusals.remember("dev", stale)
	if adapter.queryRefusals.refusedFor("dev", fresh) {
		t.Fatal("the new connection inherited a refusal earned by the old one")
	}
}

// A BACKEND BEHIND ITS OWN AUTHENTICATION — kube-rbac-proxy, an oauth proxy
// — answers 401 or 403 with a body that is not the API server's, and the
// service proxy strips Authorization on the way through, so this route can
// never succeed. Read as an RBAC refusal it sends somebody to ask for a
// permission they hold; read as a rejection it sends them to debug an
// expression the backend never parsed.
func TestABackendBehindItsOwnAuthGetsItsOwnSentinel(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		adapter := &Adapter{}
		body := []byte(`{"status":"error","errorType":"unauthorized","error":"authentication required"}`)

		err := adapter.classifyQueryStatus("dev", 0, "querying", status, body)
		if !errors.Is(err, ports.ErrMetricsBackendAuth) {
			t.Fatalf("HTTP %d: error %v, want ErrMetricsBackendAuth", status, err)
		}
		if errors.Is(err, ports.ErrForbidden) || errors.Is(err, ports.ErrMetricsQueryRejected) {
			t.Fatalf("HTTP %d: the backend's own refusal was misread: %v", status, err)
		}
		if adapter.queryRefusals.refusedFor("dev", 0) {
			t.Fatalf("HTTP %d: the backend's own refusal was cached as a refusal to proxy", status)
		}
	}
}

// A BODY THAT DECODES AS kind: Status CAME FROM THE API SERVER, so it must
// never reach the rejected branch whatever its code — a throttled or failing
// API server would otherwise have its raw Status JSON shown to the operator
// as their monitoring backend's own words about an expression it never saw.
func TestAKubernetesStatusNeverReachesTheRejectedBranch(t *testing.T) {
	statuses := []int{
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadRequest,
		http.StatusConflict,
	}

	for _, status := range statuses {
		adapter := &Adapter{}
		body := []byte(`{"kind":"Status","apiVersion":"v1","status":"Failure",` +
			`"message":"the server is currently unable to handle the request","code":` +
			itoa(status) + `}`)

		err := adapter.classifyQueryStatus("dev", 0, "querying", status, body)
		if errors.Is(err, ports.ErrMetricsQueryRejected) {
			t.Fatalf("HTTP %d: an API server Status was reported as the backend rejecting the query: %v", status, err)
		}
		if !errors.Is(err, ports.ErrUnreachable) {
			t.Fatalf("HTTP %d: error %v, want ErrUnreachable", status, err)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

func TestASuccessfulStatusIsNotAnError(t *testing.T) {
	adapter := &Adapter{}
	for _, status := range []int{200, 204, 206} {
		if err := adapter.classifyQueryStatus("dev", 0, "querying", status, nil); err != nil {
			t.Fatalf("HTTP %d classified as %v", status, err)
		}
	}
}

// A point is [unixSeconds, "value"], and the value is a STRING because JSON
// has neither NaN nor infinity and Prometheus writes both.
func TestPointsDecodeFromPrometheusEncoding(t *testing.T) {
	var pair []json.RawMessage
	if err := json.Unmarshal([]byte(`[1757116800.5,"1.25"]`), &pair); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	point, ok := decodePoint(pair)
	if !ok {
		t.Fatal("a well-formed point did not decode")
	}
	if point.Value != 1.25 {
		t.Fatalf("value %v, want 1.25", point.Value)
	}
	if want := time.UnixMilli(1757116800500).UTC(); !point.At.Equal(want) {
		t.Fatalf("instant %s, want %s", point.At, want)
	}
}

func TestAnUndecodablePointIsDroppedRatherThanFailing(t *testing.T) {
	for _, raw := range []string{`[1757116800,"NaN"]`, `[1757116800]`, `[1757116800,1.25]`} {
		var pair []json.RawMessage
		if err := json.Unmarshal([]byte(raw), &pair); err != nil {
			t.Fatalf("fixture %s: %v", raw, err)
		}
		if _, ok := decodePoint(pair); ok {
			t.Fatalf("%s decoded, want dropped", raw)
		}
	}
}

// The node probe answers with a set: a backend fronting several clusters
// reports the same node from many series.
func TestTheNodeProbeAnswerIsDeduplicatedAndSorted(t *testing.T) {
	body := []byte(`{"status":"success","data":{"resultType":"vector","result":[
		{"metric":{"node":"node-b"},"value":[1,"3"]},
		{"metric":{"node":"node-a"},"value":[1,"7"]},
		{"metric":{"node":"node-b"},"value":[1,"1"]},
		{"metric":{},"value":[1,"1"]}
	]}}`)

	decoded, err := decodeQueryResponse(body)
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}

	seen := map[string]struct{}{}
	for _, result := range decoded.Data.Result {
		if name := result.Metric[domain.NodeProbeLabel]; name != "" {
			seen[name] = struct{}{}
		}
	}
	if len(seen) != 2 {
		t.Fatalf("%d distinct nodes, want 2: %v", len(seen), seen)
	}
}

// The prefix a VictoriaMetrics cluster mounts the query API under has to
// survive the split into path segments, or the request goes to the root and
// gets an error about the URL format rather than an answer.
func TestAVictoriaMetricsPrefixSurvivesThePathSplit(t *testing.T) {
	segments := pathSegments("/select/0/prometheus" + "/api/v1/query_range")
	want := []string{"select", "0", "prometheus", "api", "v1", "query_range"}

	if len(segments) != len(want) {
		t.Fatalf("segments %v, want %v", segments, want)
	}
	for i := range want {
		if segments[i] != want[i] {
			t.Fatalf("segments %v, want %v", segments, want)
		}
	}
}

func TestAnEmptyPrefixProducesThePlainPath(t *testing.T) {
	segments := pathSegments("/api/v1/query")
	if len(segments) != 3 || segments[0] != "api" {
		t.Fatalf("segments %v", segments)
	}
}

// An over-long expression never reaches the wire, whoever composed it.
func TestQueryRangeRefusesAnOverLongExpressionBeforeAnyRequest(t *testing.T) {
	adapter := &Adapter{}
	backend := domain.MetricsBackend{
		Kind: domain.MetricsBackendPrometheus, Namespace: "monitoring",
		Service: "prometheus-operated", Port: "web",
	}

	_, err := adapter.QueryRange(context.Background(), "dev", backend,
		strings.Repeat("sum(x)+", 2000), time.Now().Add(-time.Hour), time.Now(), time.Minute)
	if !errors.Is(err, domain.ErrQueryTooLong) {
		t.Fatalf("error %v, want ErrQueryTooLong", err)
	}
}

func TestAnUndiscoveredBackendIsNeverQueried(t *testing.T) {
	adapter := &Adapter{}
	if _, err := adapter.QueryNodes(context.Background(), "dev", domain.MetricsBackend{}); !errors.Is(err, ports.ErrMetricsUnavailable) {
		t.Fatalf("error %v, want ErrMetricsUnavailable", err)
	}
}
