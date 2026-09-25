package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	restfake "k8s.io/client-go/rest/fake"

	"github.com/podsteer/podsteer/app/domain"
)

// restOnlyDiscovery hands the adapter a REST client and nothing else.
//
// The vulnerability read goes through discovery's REST client because that is
// where the Table transform lives — the same door ListTable uses. Only
// RESTClient is ever called on this path, so the rest of the interface is
// embedded and left nil: a call to any of it should panic the test rather
// than quietly return a zero value.
type restOnlyDiscovery struct {
	discovery.DiscoveryInterface
	client rest.Interface
}

func (d restOnlyDiscovery) RESTClient() rest.Interface { return d.client }

// reportRow is one VulnerabilityReport as the API server renders it: counts
// in cells, and the operator's labels on the attached metadata.
type reportRow struct {
	namespace, name          string
	subjectKind, subjectName string
	critical, high, med, low int
	repository, tag          string
	// labelled false drops the operator's labels, which is a report nothing
	// can attribute.
	unlabelled bool
}

// severityTable renders rows the way the CRD's additionalPrinterColumns say
// the server will: Repository, Tag, Scanner, Age, then the five counts.
func severityTable(t *testing.T, continueToken string, rows ...reportRow) []byte {
	t.Helper()
	return severityTableWithColumns(t, continueToken,
		[]string{"Repository", "Tag", "Scanner", "Age", "Critical", "High", "Medium", "Low", "Unknown"},
		rows...)
}

func severityTableWithColumns(t *testing.T, continueToken string, columns []string, rows ...reportRow) []byte {
	t.Helper()

	table := metav1.Table{
		TypeMeta: metav1.TypeMeta{Kind: "Table", APIVersion: "meta.k8s.io/v1"},
		ListMeta: metav1.ListMeta{Continue: continueToken},
	}
	for _, name := range columns {
		kind := "string"
		switch name {
		case "Critical", "High", "Medium", "Low", "Unknown":
			kind = "integer"
		}
		table.ColumnDefinitions = append(table.ColumnDefinitions,
			metav1.TableColumnDefinition{Name: name, Type: kind})
	}

	for _, row := range rows {
		meta := metav1.ObjectMeta{Name: row.name, Namespace: row.namespace}
		if !row.unlabelled {
			meta.Labels = map[string]string{
				trivyResourceKindLabel: row.subjectKind,
				trivyResourceNameLabel: row.subjectName,
			}
		}
		raw, err := json.Marshal(&metav1.PartialObjectMetadata{
			TypeMeta:   metav1.TypeMeta{Kind: "PartialObjectMetadata", APIVersion: "meta.k8s.io/v1"},
			ObjectMeta: meta,
		})
		if err != nil {
			t.Fatalf("marshalling row metadata: %v", err)
		}

		cells := make([]any, 0, len(columns))
		for _, name := range columns {
			switch name {
			case "Critical":
				cells = append(cells, float64(row.critical))
			case "High":
				cells = append(cells, float64(row.high))
			case "Medium":
				cells = append(cells, float64(row.med))
			case "Low":
				cells = append(cells, float64(row.low))
			case "Unknown":
				cells = append(cells, float64(0))
			case "Repository":
				cells = append(cells, row.repository)
			case "Tag":
				cells = append(cells, row.tag)
			default:
				cells = append(cells, "x")
			}
		}
		table.Rows = append(table.Rows, metav1.TableRow{
			Cells:  cells,
			Object: runtime.RawExtension{Raw: raw},
		})
	}

	body, err := json.Marshal(&table)
	if err != nil {
		t.Fatalf("marshalling table: %v", err)
	}
	return body
}

// trivyAdapter serves each request from responses in order, and records every
// request made so the test can assert what was asked for.
func trivyAdapter(t *testing.T, id domain.ClusterID, responses ...func() (*http.Response, error)) (*Adapter, *[]*http.Request) {
	t.Helper()

	var requests []*http.Request
	at := 0
	client := &restfake.RESTClient{
		NegotiatedSerializer: nil,
		Client: restfake.CreateHTTPClient(func(request *http.Request) (*http.Response, error) {
			requests = append(requests, request)
			if at >= len(responses) {
				t.Errorf("unexpected request %d: %s", at+1, request.URL)
				return jsonResponse(http.StatusOK, []byte(`{}`))()
			}
			response := responses[at]
			at++
			return response()
		}),
	}

	factory := newClientFactory(Config{})
	factory.clients[id] = &clients{discovery: restOnlyDiscovery{client: client}}
	return &Adapter{factory: factory}, &requests
}

func jsonResponse(status int, body []byte) func() (*http.Response, error) {
	return func() (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(string(body))),
		}, nil
	}
}

// statusResponse is how the API server refuses: a Status body, which is what
// the client turns into a typed error.
func statusResponse(code int32, reason metav1.StatusReason) func() (*http.Response, error) {
	body, _ := json.Marshal(&metav1.Status{
		TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
		Status:   metav1.StatusFailure,
		Reason:   reason,
		Code:     code,
	})
	return jsonResponse(int(code), body)
}

func TestListVulnerabilitySummariesSumsOneReportPerContainer(t *testing.T) {
	// The Trivy Operator writes ONE report per container, so a two-container
	// workload has two of them. Reading only the first would under-report
	// every sidecar in the cluster.
	adapter, _ := trivyAdapter(t, "dev", jsonResponse(http.StatusOK, severityTable(t, "",
		reportRow{namespace: "shop", name: "a", subjectKind: "ReplicaSet", subjectName: "web", critical: 1, high: 2},
		reportRow{namespace: "shop", name: "b", subjectKind: "ReplicaSet", subjectName: "web", critical: 0, high: 3, med: 4},
	)))

	listing, err := adapter.ListVulnerabilitySummaries(context.Background(), "dev", "shop")
	if err != nil {
		t.Fatalf("ListVulnerabilitySummaries() error = %v", err)
	}

	if len(listing.Summaries) != 1 {
		t.Fatalf("got %d summaries, want 1 — both reports are about the same workload", len(listing.Summaries))
	}
	got := listing.Summaries[0]
	if got.Subject != "ReplicaSet/web" || got.Reports != 2 {
		t.Errorf("summary = %q over %d reports, want ReplicaSet/web over 2", got.Subject, got.Reports)
	}
	if got.Counts.Critical != 1 || got.Counts.High != 5 || got.Counts.Medium != 4 {
		t.Errorf("counts = %+v, want critical 1, high 5, medium 4", got.Counts)
	}
	if !listing.Complete() {
		t.Errorf("status = %q, want complete", listing.Status)
	}
	if listing.Read != 2 {
		t.Errorf("Read = %d, want 2", listing.Read)
	}
}

// THE READ MUST NOT ASK FOR THE WATCH CACHE, and this is the request-level
// guard for it.
//
// A Limit sent with ResourceVersion "0" is DISCARDED by the API server — the
// watch cache answers the list and returns the whole collection with no
// Continue token. That is what the previous read did: it named a cap of 500
// and had no bound at all. Nothing about the response reveals it, so the
// pairing has to be pinned where it is made.
func TestVulnerabilitySummariesNeverAsksTheWatchCacheForALimitedList(t *testing.T) {
	adapter, requests := trivyAdapter(t, "dev", jsonResponse(http.StatusOK, severityTable(t, "")))

	if _, err := adapter.ListVulnerabilitySummaries(context.Background(), "dev", "shop"); err != nil {
		t.Fatalf("ListVulnerabilitySummaries() error = %v", err)
	}
	if len(*requests) != 1 {
		t.Fatalf("made %d requests, want 1", len(*requests))
	}

	query := (*requests)[0].URL.Query()
	if got := query.Get("resourceVersion"); got != "" {
		t.Errorf("resourceVersion = %q, want it absent — the server would drop the limit", got)
	}
	if got := query.Get("limit"); got != fmt.Sprint(vulnerabilityPageSize) {
		t.Errorf("limit = %q, want %d", got, vulnerabilityPageSize)
	}
	if got := query.Get("includeObject"); got != "Metadata" {
		t.Errorf("includeObject = %q, want Metadata — the labels are the only thing naming the workload", got)
	}
	if got := (*requests)[0].Header.Get("Accept"); !strings.Contains(got, "as=Table") {
		t.Errorf("Accept = %q, want the Table transform — the counts are printer columns", got)
	}
}

func TestListVulnerabilitySummariesPagesUntilTheCollectionEnds(t *testing.T) {
	// A limited list that does not ask for the watch cache gets a Continue
	// token, which is the whole point of not asking for it.
	adapter, requests := trivyAdapter(t, "dev",
		jsonResponse(http.StatusOK, severityTable(t, "token-1",
			reportRow{namespace: "shop", name: "a", subjectKind: "ReplicaSet", subjectName: "alpha", critical: 1})),
		jsonResponse(http.StatusOK, severityTable(t, "",
			reportRow{namespace: "shop", name: "b", subjectKind: "ReplicaSet", subjectName: "beta", high: 2})),
	)

	listing, err := adapter.ListVulnerabilitySummaries(context.Background(), "dev", "shop")
	if err != nil {
		t.Fatalf("ListVulnerabilitySummaries() error = %v", err)
	}

	if len(*requests) != 2 {
		t.Fatalf("made %d requests, want 2 — the second page was never fetched", len(*requests))
	}
	if got := (*requests)[1].URL.Query().Get("continue"); got != "token-1" {
		t.Errorf("second request continue = %q, want token-1", got)
	}
	if len(listing.Summaries) != 2 {
		t.Fatalf("got %d summaries, want both pages", len(listing.Summaries))
	}
	if listing.Summaries[0].Subject != "ReplicaSet/alpha" || listing.Summaries[1].Subject != "ReplicaSet/beta" {
		t.Errorf("summaries = %q, %q, want them sorted by subject",
			listing.Summaries[0].Subject, listing.Summaries[1].Subject)
	}
	if !listing.Complete() {
		t.Errorf("status = %q, want complete — the collection ended", listing.Status)
	}
}

func TestListVulnerabilitySummariesSaysWhenItStoppedAtTheCeiling(t *testing.T) {
	// THE FALSE NEGATIVE THIS CLOSES. A workload with no chip means "the
	// scanner found nothing" ONLY if the read was complete. A prefix that
	// says nothing about being a prefix turns "not read" into a clean bill of
	// health for every workload past the cut.
	pages := make([]func() (*http.Response, error), 0, vulnerabilityScanCeiling/vulnerabilityPageSize)
	for page := range vulnerabilityScanCeiling / vulnerabilityPageSize {
		rows := make([]reportRow, 0, vulnerabilityPageSize)
		for i := range vulnerabilityPageSize {
			rows = append(rows, reportRow{
				namespace:   "shop",
				name:        fmt.Sprintf("r-%d-%d", page, i),
				subjectKind: "ReplicaSet",
				subjectName: fmt.Sprintf("w-%d-%d", page, i),
				critical:    1,
			})
		}
		pages = append(pages, jsonResponse(http.StatusOK, severityTable(t, "more", rows...)))
	}

	adapter, _ := trivyAdapter(t, "dev", pages...)

	listing, err := adapter.ListVulnerabilitySummaries(context.Background(), "dev", "shop")
	if err != nil {
		t.Fatalf("ListVulnerabilitySummaries() error = %v", err)
	}

	if listing.Status != domain.VulnerabilityReadTruncated {
		t.Fatalf("status = %q, want truncated", listing.Status)
	}
	if listing.Complete() {
		t.Fatal("Complete() is true on a read that stopped short — an absent chip would read as clean")
	}
	if listing.Read != vulnerabilityScanCeiling {
		t.Errorf("Read = %d, want the ceiling %d", listing.Read, vulnerabilityScanCeiling)
	}
	if listing.Cap != vulnerabilityScanCeiling {
		t.Errorf("Cap = %d, want %d — the sentence has to name the limit", listing.Cap, vulnerabilityScanCeiling)
	}
}

func TestListVulnerabilitySummariesSkipsAReportThatNamesNoWorkload(t *testing.T) {
	// Attributing a report by guessing at the object's name would put
	// somebody else's findings on a workload's row.
	adapter, _ := trivyAdapter(t, "dev", jsonResponse(http.StatusOK, severityTable(t, "",
		reportRow{namespace: "shop", name: "orphan", critical: 9, unlabelled: true},
		reportRow{namespace: "shop", name: "a", subjectKind: "ReplicaSet", subjectName: "web", critical: 1},
	)))

	listing, err := adapter.ListVulnerabilitySummaries(context.Background(), "dev", "shop")
	if err != nil {
		t.Fatalf("ListVulnerabilitySummaries() error = %v", err)
	}

	if len(listing.Summaries) != 1 || listing.Summaries[0].Subject != "ReplicaSet/web" {
		t.Fatalf("summaries = %+v, want only the attributable one", listing.Summaries)
	}
	if listing.Read != 2 {
		t.Errorf("Read = %d, want 2 — an unattributable report was still read", listing.Read)
	}
}

func TestListVulnerabilitySummariesRefusesToInventZerosWhenTheColumnsAreGone(t *testing.T) {
	// The counts are printer columns and have been since Starboard, so their
	// absence means a CRD change. Reporting zeros would be issuing a clean
	// bill of health for every workload in the namespace — the one answer
	// this must never give.
	adapter, _ := trivyAdapter(t, "dev", jsonResponse(http.StatusOK, severityTableWithColumns(t, "",
		[]string{"Repository", "Tag", "Scanner", "Age"},
		reportRow{namespace: "shop", name: "a", subjectKind: "ReplicaSet", subjectName: "web"},
	)))

	_, err := adapter.ListVulnerabilitySummaries(context.Background(), "dev", "shop")
	if err == nil {
		t.Fatal("a report set with no severity columns was read as zero findings")
	}
	if !strings.Contains(err.Error(), "severity columns") {
		t.Errorf("error = %v, want it to name the missing columns", err)
	}
}

func TestListVulnerabilitySummariesTellsNoScannerFromNoPermission(t *testing.T) {
	// Both leave every row undecorated, and they are not the same fact: one
	// says nothing is watching this cluster, the other says this account may
	// not see what is.
	tests := []struct {
		name     string
		response func() (*http.Response, error)
		want     domain.VulnerabilityRead
	}{
		{"no CRD, so no scanner", statusResponse(http.StatusNotFound, metav1.StatusReasonNotFound), domain.VulnerabilityReadNotInstalled},
		{"refused", statusResponse(http.StatusForbidden, metav1.StatusReasonForbidden), domain.VulnerabilityReadForbidden},
		{"unauthenticated", statusResponse(http.StatusUnauthorized, metav1.StatusReasonUnauthorized), domain.VulnerabilityReadForbidden},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, _ := trivyAdapter(t, "dev", test.response)

			listing, err := adapter.ListVulnerabilitySummaries(context.Background(), "dev", "shop")
			if err != nil {
				t.Fatalf("ListVulnerabilitySummaries() error = %v, want an ordinary answer", err)
			}
			if listing.Status != test.want {
				t.Errorf("status = %q, want %q", listing.Status, test.want)
			}
			if listing.Complete() {
				t.Error("Complete() is true, so an absent chip would be read as a clean workload")
			}
		})
	}
}

func TestListVulnerabilitySummariesCachesARefusalRatherThanRetryingIt(t *testing.T) {
	// An account that may never list these should not have that retried into
	// its audit log every time somebody opens a pod list.
	adapter, requests := trivyAdapter(t, "dev", statusResponse(http.StatusForbidden, metav1.StatusReasonForbidden))

	for range 3 {
		if _, err := adapter.ListVulnerabilitySummaries(context.Background(), "dev", "shop"); err != nil {
			t.Fatalf("ListVulnerabilitySummaries() error = %v", err)
		}
	}

	if len(*requests) != 1 {
		t.Errorf("asked %d times, want 1 — the refusal was not cached", len(*requests))
	}
}

func TestListVulnerabilitySummariesDoesNotCacheATransportFailure(t *testing.T) {
	// A cluster that was merely unreachable comes back, and should be asked
	// again when it does.
	fail := func() (*http.Response, error) { return nil, errors.New("connection refused") }
	adapter, requests := trivyAdapter(t, "dev", fail, fail)

	for range 2 {
		if _, err := adapter.ListVulnerabilitySummaries(context.Background(), "dev", "shop"); err == nil {
			t.Fatal("a transport failure was reported as an ordinary answer")
		}
	}

	if len(*requests) != 2 {
		t.Errorf("asked %d times, want 2 — an unreachable cluster must be retried", len(*requests))
	}
}

func TestListVulnerabilitySummariesServesTheSecondReadFromTheCache(t *testing.T) {
	adapter, requests := trivyAdapter(t, "dev", jsonResponse(http.StatusOK, severityTable(t, "",
		reportRow{namespace: "shop", name: "a", subjectKind: "ReplicaSet", subjectName: "web", critical: 1})))

	for range 2 {
		if _, err := adapter.ListVulnerabilitySummaries(context.Background(), "dev", "shop"); err != nil {
			t.Fatalf("ListVulnerabilitySummaries() error = %v", err)
		}
	}

	if len(*requests) != 1 {
		t.Errorf("asked %d times, want 1 — the second read must come from the cache", len(*requests))
	}
}

func TestVulnerabilityCacheIsDroppedWhenAClusterIsInvalidated(t *testing.T) {
	// A ten-minute answer carried across a reconnect would put the previous
	// connection's findings on the first pod list of the new one — the same
	// reason the disk sweep is forgotten there.
	body := severityTable(t, "", reportRow{namespace: "shop", name: "a", subjectKind: "ReplicaSet", subjectName: "web", critical: 1})
	adapter, requests := trivyAdapter(t, "dev",
		jsonResponse(http.StatusOK, body), jsonResponse(http.StatusOK, body))

	if _, err := adapter.ListVulnerabilitySummaries(context.Background(), "dev", "shop"); err != nil {
		t.Fatalf("ListVulnerabilitySummaries() error = %v", err)
	}
	adapter.vulnerabilities.forget("dev")
	if _, err := adapter.ListVulnerabilitySummaries(context.Background(), "dev", "shop"); err != nil {
		t.Fatalf("ListVulnerabilitySummaries() error = %v", err)
	}

	if len(*requests) != 2 {
		t.Errorf("listed %d times, want 2 — the cache must not survive an invalidation", len(*requests))
	}
}

func TestVulnerabilityCacheKeepsNamespacesApart(t *testing.T) {
	cache := &vulnerabilityCache{}
	cache.put("dev", "shop", 0, domain.VulnerabilityListing{Status: domain.VulnerabilityReadComplete})

	if _, ok := cache.get("dev", "billing", 0); ok {
		t.Error("one namespace's answer was served for another")
	}
	if _, ok := cache.get("dev", "shop", 0); !ok {
		t.Error("the namespace that was cached is not there")
	}
}

// A LIST IN FLIGHT CAN PASS Invalidate AND THEN WRITE, and what it writes is
// most often "nothing to show" — the CRD is not installed, or this account
// may not read the reports. Inherited by the next connection that would be a
// namespace nobody asks about again for the whole of vulnerabilityCacheTTL,
// with no request ever made to earn the silence.
func TestAVulnerabilityAnswerWrittenAgainstAReplacedConnectionIsInert(t *testing.T) {
	adapter := &Adapter{}

	stale := adapter.generations.at("dev")
	adapter.generations.bump("dev")
	fresh := adapter.generations.at("dev")

	if stale == fresh {
		t.Fatal("the generation did not advance")
	}

	adapter.vulnerabilities.put("dev", "shop", stale, domain.VulnerabilityListing{
		Status: domain.VulnerabilityReadNotInstalled,
	})

	if _, ok := adapter.vulnerabilities.get("dev", "shop", fresh); ok {
		t.Fatal("the replaced connection's answer was served to its successor")
	}
}

func TestVulnerabilityCacheForgetsOnlyTheClusterNamed(t *testing.T) {
	// Closing one tab must not cost every other tab its answers.
	cache := &vulnerabilityCache{}
	listing := domain.VulnerabilityListing{
		Summaries: []domain.VulnerabilitySummary{{Subject: "ReplicaSet/web", Reports: 1}},
		Status:    domain.VulnerabilityReadComplete,
	}
	cache.put("dev", "shop", 0, listing)
	cache.put("prod", "shop", 0, listing)

	cache.forget("dev")

	if _, ok := cache.get("dev", "shop", 0); ok {
		t.Error("dev is still cached after being forgotten")
	}
	if _, ok := cache.get("prod", "shop", 0); !ok {
		t.Error("prod lost its cached summaries when dev was forgotten")
	}
}

func TestListVulnerabilitySummariesCarriesTheImageEachReportScanned(t *testing.T) {
	// THE IMAGE IS WHAT GETS FIXED, NOT THE WORKLOAD. One report per
	// container means the same image in twelve Deployments produces twelve
	// summaries with identical counts — twelve rows for one bump. Nothing
	// else in the read can group them, because the workload names have
	// nothing in common.
	adapter, _ := trivyAdapter(t, "dev", jsonResponse(http.StatusOK, severityTable(t, "",
		reportRow{namespace: "shop", name: "a", subjectKind: "ReplicaSet", subjectName: "web",
			repository: "library/nginx", tag: "1.27", critical: 1},
		reportRow{namespace: "shop", name: "b", subjectKind: "ReplicaSet", subjectName: "web",
			repository: "acme/sidecar", tag: "2.0", high: 1},
		// The same container scanned again — one image, not two.
		reportRow{namespace: "shop", name: "c", subjectKind: "ReplicaSet", subjectName: "api",
			repository: "library/nginx", tag: "1.27", critical: 1},
		// Pinned by digest, so no tag: the repository stands alone rather
		// than carrying a dangling colon.
		reportRow{namespace: "shop", name: "d", subjectKind: "ReplicaSet", subjectName: "worker",
			repository: "acme/worker", high: 2},
	)))

	listing, err := adapter.ListVulnerabilitySummaries(context.Background(), "dev", "shop")
	if err != nil {
		t.Fatalf("ListVulnerabilitySummaries() error = %v", err)
	}

	bySubject := map[string][]string{}
	for _, summary := range listing.Summaries {
		bySubject[summary.Subject] = summary.Images
	}

	want := map[string][]string{
		"ReplicaSet/api":    {"library/nginx:1.27"},
		"ReplicaSet/web":    {"acme/sidecar:2.0", "library/nginx:1.27"},
		"ReplicaSet/worker": {"acme/worker"},
	}
	for subject, images := range want {
		got := bySubject[subject]
		if len(got) != len(images) {
			t.Fatalf("%s images = %v, want %v", subject, got, images)
		}
		for at := range images {
			if got[at] != images[at] {
				t.Errorf("%s images = %v, want %v (sorted)", subject, got, images)
				break
			}
		}
	}
}

func TestListVulnerabilitySummariesDeduplicatesAnImageAcrossReports(t *testing.T) {
	// A workload whose two containers run the SAME image has one image, not
	// two — otherwise "used by N workloads" counts the same fix twice.
	adapter, _ := trivyAdapter(t, "dev", jsonResponse(http.StatusOK, severityTable(t, "",
		reportRow{namespace: "shop", name: "a", subjectKind: "ReplicaSet", subjectName: "web",
			repository: "library/nginx", tag: "1.27", critical: 1},
		reportRow{namespace: "shop", name: "b", subjectKind: "ReplicaSet", subjectName: "web",
			repository: "library/nginx", tag: "1.27", critical: 1},
	)))

	listing, err := adapter.ListVulnerabilitySummaries(context.Background(), "dev", "shop")
	if err != nil {
		t.Fatalf("ListVulnerabilitySummaries() error = %v", err)
	}

	if len(listing.Summaries) != 1 {
		t.Fatalf("got %d summaries, want 1", len(listing.Summaries))
	}
	if images := listing.Summaries[0].Images; len(images) != 1 || images[0] != "library/nginx:1.27" {
		t.Errorf("images = %v, want one library/nginx:1.27", images)
	}
	if reports := listing.Summaries[0].Reports; reports != 2 {
		t.Errorf("Reports = %d, want 2 — deduplicating images must not lose a report", reports)
	}
}
