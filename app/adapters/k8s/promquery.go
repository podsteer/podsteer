package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	utilnet "k8s.io/apimachinery/pkg/util/net"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// Asking a monitoring backend already running in the cluster a question, and
// the guards on the asking. See ADR 7; the expressions themselves are in
// app/domain/promql.go, where they can be argued with in a test.
//
// THE TRANSPORT IS THE ONE reachability.go ALREADY USES: the API server's own
// service proxy, on the kubeconfig credential already open for that tab. No
// new outbound host, no second credential at rest, no route from this machine
// to the monitoring namespace, and the webview CSP is untouched — so
// SECURITY.md's network claims stay literally true.
//
// WHAT IS NEW IS NOT THE SOCKET. Queries PodSteer composed reach a third
// system that logs them, attributed to the operator's own identity, and each
// call lands in the cluster's audit log as a `get` on `services/proxy` naming
// the monitoring Service. Neither was predictable from the old documents,
// which is why SECURITY.md gained a paragraph in the same commit as this
// file.
//
// GET ONLY, NEVER POST. A POST to the service proxy is the RBAC verb `create`
// on services/proxy — a different permission from the `get` every proxying
// account already holds — so a form-body query would fail for exactly the
// tightly-permissioned accounts this application is careful about, and would
// ask for a privilege it does not need. That is what puts the node filter in
// the URL, and what makes an oversized one a refusal
// (domain.ErrQueryTooLong) rather than a body.
//
// THE CLIENT-SIDE RATE LIMITER DOES NOT APPLY HERE, and that is a real
// difference from every other call in this adapter rather than a detail. The
// limiter lives on client-go's RESTClient, not on the transport, so
// PODSTEER_QPS and PODSTEER_BURST bound the lists and gets around this file
// and not the requests in it. The URL is still composed by the RESTClient's
// own request builder — so the path, the namespace and the scheme:name:port
// form are encoded exactly as everywhere else — but the round trip is made on
// clients.queryHTTP, because a bounded read of a FAILED response needs the
// body client-go's own helpers discard. What makes that acceptable is the
// rule above it: at most one request per user action, and never one on a
// tick. Anything here that grew a second caller, or a retry, would need the
// limiter back.

// maxQueryResponseBytes bounds what is read from a backend's answer.
//
// The body comes from a system PodSteer does not control and did not size, so
// it is read through a LIMITED READER and refused undecoded past the cap —
// decoding first and judging afterwards is how a bounded read becomes an
// unbounded allocation. Four mebibytes is far past anything the expressions
// here produce (one series, or one per node, at a few hundred points) and far
// short of what a fleet backend could be made to return.
const maxQueryResponseBytes = 4 << 20

// queryTimeout bounds one call. A range query is one HTTP request and can
// still be real work on the far side, and nothing here is on a refresh tick —
// somebody pressed something and is waiting.
const queryTimeout = 30 * time.Second

// queryRefusalTTL is how long a refusal to proxy stands.
//
// IT HAS ONE AT ALL BECAUSE A PERMISSION IS A THING SOMEBODY GRANTS. This was
// the only cache in this adapter with no expiry, which meant an administrator
// adding a RoleBinding while the tab was open left a sentence on screen that
// had become false, with no way back but reconnecting — and the sentence did
// not say so. Five minutes, in line with upgradeCache and helmCache, and the
// message in application.failed says the wait exists.
const queryRefusalTTL = 5 * time.Minute

// forbiddenBackends remembers which clusters refused the proxy.
//
// A REFUSAL IS CACHED AND A TRANSPORT FAILURE IS NOT — the discipline
// DiscoverMetricsBackend, ListVulnerabilitySummaries and APIWriters already
// follow, and it is sharper here than for any of them. An account that may
// not use services/proxy is refused every time, and asking again on every
// chart open writes a denied request into somebody's audit log for as long as
// the tab is open, for a feature that is an offer rather than a requirement.
//
// ONLY A 403. A 401 is a CREDENTIAL fact rather than a permission fact — with
// an exec plugin client-go re-runs the plugin on one and the next request
// would succeed — so caching it is the one way to guarantee there is no next
// request. It maps to ErrUnauthenticated like everywhere else in this adapter
// and is not remembered here.
//
// EVERY ENTRY CARRIES THE GENERATION IT WAS WRITTEN UNDER, because a query
// already in flight can pass Invalidate and then write a refusal about the
// cluster this tab USED to be. The reader compares generations, so such a
// write is inert rather than a refusal the new connection never earned.
type forbiddenBackends struct {
	mu      sync.Mutex
	refused map[domain.ClusterID]refusalEntry
}

type refusalEntry struct {
	at         time.Time
	generation uint64
}

func (f *forbiddenBackends) refusedFor(id domain.ClusterID, generation uint64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	entry, ok := f.refused[id]
	return ok && entry.generation == generation && time.Since(entry.at) <= queryRefusalTTL
}

func (f *forbiddenBackends) remember(id domain.ClusterID, generation uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.refused == nil {
		f.refused = make(map[domain.ClusterID]refusalEntry, 2)
	}
	f.refused[id] = refusalEntry{at: time.Now(), generation: generation}
}

func (f *forbiddenBackends) forget(id domain.ClusterID) {
	f.mu.Lock()
	defer f.mu.Unlock()

	delete(f.refused, id)
}

// generations numbers each cluster's connection, so an answer computed
// against one cannot be cached against the next.
//
// ORDERING CANNOT FIX THIS ON ITS OWN. A request that read the client set
// before Invalidate ran finishes after it, and whatever it writes is about
// the cluster this tab used to be — which for a node list is a set of names
// that would then license a narrowed sum over nodes the new connection has
// never seen. Capturing the number before the request and comparing it before
// the write makes such a write inert instead.
type generations struct {
	mu      sync.Mutex
	current map[domain.ClusterID]uint64
}

func (g *generations) at(id domain.ClusterID) uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.current[id]
}

func (g *generations) bump(id domain.ClusterID) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.current == nil {
		g.current = make(map[domain.ClusterID]uint64, 2)
	}
	g.current[id]++
}

// promResponse is the envelope every Prometheus-compatible query API answers
// with, whether it succeeded or not.
//
// Decoded from the shape rather than through a client library, because the
// two fields that matter on a failure — errorType and error — are exactly
// what a typed client folds into a string of its own, and this feature
// carries the backend's words rather than a paraphrase of them.
type promResponse struct {
	Status    string   `json:"status"`
	Data      promData `json:"data"`
	ErrorType string   `json:"errorType"`
	Error     string   `json:"error"`
}

type promData struct {
	ResultType string       `json:"resultType"`
	Result     []promResult `json:"result"`
}

// promResult is one labelled result: `value` for an instant query, `values`
// for a range one. Both are [unixSeconds, "value"] pairs, and the value is a
// STRING in Prometheus' own encoding rather than a JSON number — JSON has
// neither NaN nor infinity and Prometheus has both.
type promResult struct {
	Metric map[string]string   `json:"metric"`
	Value  []json.RawMessage   `json:"value"`
	Values [][]json.RawMessage `json:"values"`
}

// QueryNodes asks a backend which nodes it holds series for. See
// ports.MetricsQueryPort.
func (a *Adapter) QueryNodes(
	ctx context.Context,
	id domain.ClusterID,
	backend domain.MetricsBackend,
) ([]string, error) {
	body, err := a.proxyQuery(ctx, id, backend, "/api/v1/query", map[string]string{
		"query": domain.NodeProbeExpression,
		"time":  formatQueryInstant(time.Now()),
	})
	if err != nil {
		return nil, err
	}

	decoded, err := decodeQueryResponse(body)
	if err != nil {
		return nil, err
	}

	// A SET, because a backend fronting several clusters reports the same
	// node name from many series and the caller compares sets rather than
	// counting them.
	seen := make(map[string]struct{}, len(decoded.Data.Result))
	for _, result := range decoded.Data.Result {
		if name := result.Metric[domain.NodeProbeLabel]; name != "" {
			seen[name] = struct{}{}
		}
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	// Sorted so anything logging or diffing them meets a stable order; map
	// iteration is not one.
	sort.Strings(names)
	return names, nil
}

// QueryRange evaluates one expression over a range. See
// ports.MetricsQueryPort.
func (a *Adapter) QueryRange(
	ctx context.Context,
	id domain.ClusterID,
	backend domain.MetricsBackend,
	expression string,
	start, end time.Time,
	step time.Duration,
) ([]domain.PromSeries, error) {
	// THE BUDGET IS CHECKED HERE TOO, not only where the expression was
	// composed. This is the last place before a request leaves and the whole
	// point of the budget is that nothing over it is sent — a second check
	// against one rule costs nothing and closes the path a future caller
	// composing its own string would open.
	if !domain.WithinQueryURLBudget(expression) {
		return nil, fmt.Errorf("querying %s in %q: %w",
			backend.Describe(), id, domain.ErrQueryTooLong)
	}

	body, err := a.proxyQuery(ctx, id, backend, "/api/v1/query_range", map[string]string{
		"query": expression,
		"start": formatQueryInstant(start),
		"end":   formatQueryInstant(end),
		"step":  strconv.FormatInt(int64(step.Seconds()), 10),
	})
	if err != nil {
		return nil, err
	}

	decoded, err := decodeQueryResponse(body)
	if err != nil {
		return nil, err
	}

	series := make([]domain.PromSeries, 0, len(decoded.Data.Result))
	for _, result := range decoded.Data.Result {
		points := make([]domain.SeriesPoint, 0, len(result.Values))
		for _, pair := range result.Values {
			point, ok := decodePoint(pair)
			if !ok {
				// A point that will not decode is DROPPED rather than
				// failing the whole series: Prometheus writes NaN for a gap
				// in a counter, and a chart short one instant is a far better
				// answer than no chart at all.
				continue
			}
			points = append(points, point)
		}
		series = append(series, domain.PromSeries{Labels: result.Metric, Points: points})
	}
	return series, nil
}

// proxyQuery performs one GET through the API server's service proxy and
// returns the body, bounded.
func (a *Adapter) proxyQuery(
	ctx context.Context,
	id domain.ClusterID,
	backend domain.MetricsBackend,
	path string,
	params map[string]string,
) ([]byte, error) {
	op := fmt.Sprintf("querying %s in %q", backend.Describe(), id)

	if !backend.Found() {
		return nil, fmt.Errorf("%s: %w", op, ports.ErrMetricsUnavailable)
	}

	// CAPTURED BEFORE ANYTHING IS READ. Everything this call may later cache
	// is about the connection as it stands now, and a disconnect between here
	// and the write makes all of it about a cluster this tab has left.
	generation := a.generations.at(id)

	// The cached refusal comes before the client, so a refused account makes
	// no request at all rather than one it is about to be denied.
	if a.queryRefusals.refusedFor(id, generation) {
		return nil, fmt.Errorf("%s: %w", op, ports.ErrForbidden)
	}

	set, err := a.factory.clientsFor(id)
	if err != nil {
		return nil, err
	}

	// The URL is composed by client-go's own request builder rather than by
	// string concatenation, so the API path, the namespace and the
	// scheme:name:port form of the proxy target are encoded exactly as every
	// other call in this adapter encodes them.
	request := set.typed.CoreV1().RESTClient().Get().
		Namespace(backend.Namespace.String()).
		Resource("services").
		Name(utilnet.JoinSchemeNamePort("http", backend.Service, backend.Port)).
		SubResource("proxy").
		Suffix(pathSegments(backend.Prefix + path)...)
	for key, value := range params {
		request = request.Param(key, value)
	}

	// URL() DOES NOT CONSULT THE REQUEST'S OWN ERROR, so a value the builder
	// rejected leaves a URL with that segment simply missing — which for a
	// name and a subresource is a bare LIST of the namespace's Services, sent
	// to the API server and read as a query answer. Checked rather than
	// trusted, because the failure is silent and its shape is a request
	// nobody meant to make.
	if err := request.Error(); err != nil {
		return nil, fmt.Errorf("%s: composing the proxy request: %w", op, err)
	}

	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	body, status, err := fetchBounded(ctx, set.queryHTTP, request.URL().String(), maxQueryResponseBytes)
	if err != nil {
		if errors.Is(err, ports.ErrMetricsQueryTooLarge) {
			return nil, fmt.Errorf("%s: %w", op, err)
		}
		return nil, classify(op, err)
	}

	if err := a.classifyQueryStatus(id, generation, op, status, body); err != nil {
		return nil, err
	}
	return body, nil
}

// fetchBounded performs one GET and reads at most cap bytes of the answer.
//
// THE LIMITED READER IS THE POINT. An over-sized body is refused UNDECODED
// and what was read is dropped: the cap exists so nothing that large is ever
// turned into values, and a truncated JSON document could not be anyway.
// Reading one byte past the cap is what makes an answer AT the cap
// distinguishable from one over it without reading the whole of the latter.
func fetchBounded(ctx context.Context, client *http.Client, url string, cap int64) ([]byte, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Accept", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
		_ = response.Body.Close()
	}()

	body, readErr := io.ReadAll(io.LimitReader(response.Body, cap+1))
	if int64(len(body)) > cap {
		return nil, response.StatusCode, fmt.Errorf("%w: over %d bytes", ports.ErrMetricsQueryTooLarge, cap)
	}
	if readErr != nil {
		return nil, response.StatusCode, readErr
	}
	return body, response.StatusCode, nil
}

// classifyQueryStatus turns an HTTP status into the sentinel the panel reads,
// or nil when the backend answered.
//
// ROUTED BY THE BODY FIRST AND THE CODE SECOND, and that order is the whole
// design. A Kubernetes Status document means the API SERVER answered and the
// proxy never reached the backend; anything else came from the backend. Read
// by code alone, a throttled or failing API server (429, 500) answering with
// a Status would be reported as the BACKEND rejecting the expression — which
// puts raw Status JSON on screen as somebody's Prometheus explaining itself
// about a query it never saw. So no Status body can reach the rejected
// branch, whatever its code.
//
// Five outcomes, each needing different words, which is the reason
// domain.BackendStatus exists rather than a boolean:
//
//   - The API server refused the proxy subresource (403). About the ACCOUNT
//     and never about the Service, routine on a restricted account, and
//     CACHED for queryRefusalTTL.
//   - The API server rejected the CREDENTIAL (401). Not cached: with an exec
//     plugin client-go re-runs it and the next request may well succeed.
//   - The backend has its OWN authentication in front of it — kube-rbac-proxy,
//     an oauth proxy — and answered 401 or 403 itself. The API server's proxy
//     strips Authorization, so this can never succeed and the operator needs
//     to be told that rather than sent to look at their expression.
//   - The backend understood the expression and declined it. Its message IS
//     the diagnosis, exactly as ErrManifestRejected carries the API server's.
//   - Nothing answered at all.
func (a *Adapter) classifyQueryStatus(
	id domain.ClusterID,
	generation uint64,
	op string,
	status int,
	body []byte,
) error {
	if status >= 200 && status < 300 {
		return nil
	}

	if kubernetesStatus, isKube := decodeKubernetesStatus(body); isKube {
		switch status {
		case http.StatusForbidden:
			// CACHED, and this is the line that keeps a denied request out of
			// somebody's audit log on every chart open. Under the generation
			// captured before the request, so a disconnect in between makes
			// the write inert rather than a refusal the next connection
			// inherits.
			a.queryRefusals.remember(id, generation)
			return fmt.Errorf("%s: %w: %s", op, ports.ErrForbidden, kubernetesStatus)
		case http.StatusUnauthorized:
			// NOT CACHED. See forbiddenBackends.
			return fmt.Errorf("%s: %w: %s", op, ports.ErrUnauthenticated, kubernetesStatus)
		case http.StatusNotFound:
			// The Service, the port or the proxy path is not there. Reported
			// as the metrics API being unavailable rather than as a refusal:
			// nothing about the account is wrong.
			return fmt.Errorf("%s: %w: %s", op, ports.ErrMetricsUnavailable, kubernetesStatus)
		default:
			// EVERYTHING ELSE THE API SERVER SAYS, including 429 and 5xx.
			// Never the rejected branch: the backend did not speak.
			return fmt.Errorf("%s: %w: %s", op, ports.ErrUnreachable, kubernetesStatus)
		}
	}

	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		// The backend's OWN refusal, since the body is not the API server's.
		// It wants a credential of its own and the proxy strips the one this
		// request carried, so retrying cannot help and neither can a change
		// to the expression.
		return fmt.Errorf("%s: %w: it answered HTTP %d", op, ports.ErrMetricsBackendAuth, status)
	case http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusGatewayTimeout:
		return fmt.Errorf("%s: %w: the monitoring backend did not answer (HTTP %d)",
			op, ports.ErrUnreachable, status)
	default:
		return fmt.Errorf("%s: %w: %s", op, ports.ErrMetricsQueryRejected, promErrorMessage(status, body))
	}
}

// decodeKubernetesStatus reports whether a body is the API server's own
// Status document, and its message.
func decodeKubernetesStatus(body []byte) (string, bool) {
	var status struct {
		Kind    string `json:"kind"`
		Status  string `json:"status"`
		Message string `json:"message"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal(body, &status); err != nil || status.Kind != "Status" {
		return "", false
	}

	message := strings.TrimSpace(status.Message)
	if message == "" {
		message = status.Reason
	}
	return message, true
}

// decodeQueryResponse reads the envelope and turns a declared failure into
// the rejection sentinel.
//
// A BACKEND CAN FAIL WITH HTTP 200. Prometheus answers some conditions — a
// query that hit its own limits, a partial answer from a Thanos store — with
// a 200 and `"status":"error"` in the body, so a status-code check alone
// would draw those as an empty chart under a green label.
func decodeQueryResponse(body []byte) (promResponse, error) {
	var decoded promResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return promResponse{}, fmt.Errorf("reading the monitoring backend's answer: %w: %v",
			ports.ErrMetricsUnavailable, err)
	}

	if decoded.Status != "success" {
		message := firstNonEmpty(decoded.Error, decoded.ErrorType, "no reason given")
		return promResponse{}, fmt.Errorf("%w: %s", ports.ErrMetricsQueryRejected, message)
	}
	return decoded, nil
}

// promErrorMessage pulls the backend's own words out of what it answered.
//
// VERBATIM WHEREVER THERE IS ANYTHING TO CARRY. A body that is not the
// expected envelope is returned as it stands rather than replaced, because a
// message nobody wrote is worse than a raw one; a body that is empty gets the
// status code, which is at least a fact.
func promErrorMessage(status int, body []byte) string {
	var envelope promResponse
	if err := json.Unmarshal(body, &envelope); err == nil {
		if message := firstNonEmpty(envelope.Error, envelope.ErrorType); message != "" {
			return message
		}
	}

	if text := strings.TrimSpace(string(body)); text != "" {
		return excerpt(text, stderrExcerpt)
	}
	return fmt.Sprintf("HTTP %d", status)
}

// decodePoint reads one [unixSeconds, "value"] pair.
func decodePoint(pair []json.RawMessage) (domain.SeriesPoint, bool) {
	if len(pair) != 2 {
		return domain.SeriesPoint{}, false
	}

	var seconds float64
	if err := json.Unmarshal(pair[0], &seconds); err != nil {
		return domain.SeriesPoint{}, false
	}

	// A STRING, in Prometheus' own encoding: JSON has neither NaN nor
	// infinity and Prometheus writes both.
	var raw string
	if err := json.Unmarshal(pair[1], &raw); err != nil {
		return domain.SeriesPoint{}, false
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return domain.SeriesPoint{}, false
	}

	// NaN AND INFINITY ARE GAPS, NOT VALUES. Prometheus writes NaN where a
	// counter reset or a staleness marker left it nothing to compute, and
	// ParseFloat reads both perfectly happily — so without this the chart
	// draws a point at a number no arithmetic downstream survives, and JSON
	// cannot even carry it across the bridge.
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return domain.SeriesPoint{}, false
	}

	return domain.SeriesPoint{
		At:    time.UnixMilli(int64(seconds * 1000)).UTC(),
		Value: value,
	}, true
}

// formatQueryInstant renders an instant the way the query API reads one.
func formatQueryInstant(at time.Time) string {
	return strconv.FormatFloat(float64(at.UnixMilli())/1000, 'f', 3, 64)
}

// pathSegments splits a URL path for rest.Request.Suffix, which joins what it
// is given rather than accepting one string with slashes in it.
func pathSegments(path string) []string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			segments = append(segments, part)
		}
	}
	return segments
}
