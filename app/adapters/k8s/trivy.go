package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// Reading what the Trivy Operator already wrote, and nothing more.
//
// The same relationship prometheus.go has with a monitoring stack: the
// add-on is DISCOVERED and QUOTED, never installed, never asked to do work,
// and its absence changes nothing anywhere. PodSteer does not scan an image,
// does not fetch an advisory database and does not grade a finding — an
// operator running a scanner has already chosen one, and a second opinion
// from a desktop client would be worse than none.
//
// THIS IS THE ONE READ THAT IS NOT PART OF THE POLL. The pod list must never
// wait on it and must never be short of a row because of it: the counts are
// fetched separately, bounded, cached, and simply absent until they arrive.
// A cluster with no Trivy Operator returns an empty list from the first call
// and never asks again inside the cache window.

// vulnerabilityReportGVR is the Trivy Operator's own resource.
//
// A FIXED GVR RATHER THAN A RESTMapper LOOKUP. apply.go resolves a kind
// through discovery because it applies whatever manifest it is handed; this
// reads exactly one kind that either exists or does not, so a discovery round
// trip would buy nothing and a NotFound on the list is the same answer at a
// lower cost.
var vulnerabilityReportGVR = schema.GroupVersionResource{
	Group:    "aquasecurity.github.io",
	Version:  "v1alpha1",
	Resource: "vulnerabilityreports",
}

// The labels the Trivy Operator puts on every report it writes, naming what
// was scanned. A report whose labels are absent is skipped rather than
// guessed at: nothing else in the object says which workload it belongs to.
const (
	trivyResourceKindLabel = "trivy-operator.resource.kind"
	trivyResourceNameLabel = "trivy-operator.resource.name"
)

// vulnerabilityPageSize bounds one page of the summary read.
//
// The read is SERVER-RENDERED ROWS, not objects — see readVulnerabilityRows
// — so a page is about 2-3 KB per report rather than the 50-150 KB a
// VulnerabilityReport weighs with its CVE list attached. Five hundred rows is
// therefore about a megabyte, which is the size a page wants to be.
const vulnerabilityPageSize = 500

// vulnerabilityScanCeiling caps how many reports one listing reads in total.
//
// One report per container per workload, so a namespace holds hundreds; five
// thousand in ONE namespace is already extraordinary, and at that point a
// floor that SAYS it is a floor beats both a stall and a silent prefix. Ten
// pages, about twelve megabytes, paid at most once per cache window and never
// on the refresh tick.
//
// This is a real ceiling now. It replaces a `Limit` the API server was
// discarding — see cachedResourceVersion — which meant the read had no bound
// at all on a warm watch cache and pulled every report as a whole object.
const vulnerabilityScanCeiling = 5000

// vulnerabilityCacheTTL is how long one namespace's summary stands.
//
// Ten minutes, between backendCache's thirty (a monitoring stack is installed
// once) and readCache's seconds (a poll must never serve one tick to the
// next). A report changes when the operator rescans, which is hours by
// default, so this is comfortably fresher than the data it holds — and it is
// what keeps this off the refresh tick entirely: an operator watching a pod
// list for ten minutes makes ONE of these calls, not one every five seconds.
const vulnerabilityCacheTTL = 10 * time.Minute

// vulnerabilityCache holds one summary set per cluster and namespace.
//
// Keyed by both because the pod list is scoped to a namespace and reading
// every report in the cluster to answer for one of them would be exactly the
// unbounded read this feature is not allowed to be. "All namespaces" is its
// own key rather than a merge of the others: it is one list call, and
// stitching per-namespace answers together would report a stale namespace
// beside a fresh one.
type vulnerabilityCache struct {
	mu      sync.Mutex
	entries map[string]vulnerabilityEntry
}

type vulnerabilityEntry struct {
	at      time.Time
	listing domain.VulnerabilityListing
	// generation is the connection this answer was computed against. A read
	// that passed Invalidate and writes afterwards lands under a generation
	// nothing will match, so it teaches the new connection nothing — the
	// same guard promquery.go applies to a cached refusal, and needed here
	// for a sharper version of the same reason: what this caches on a
	// refusal is "nothing to show", so a stale one is not a wrong number but
	// a namespace that is never asked about again for ten minutes.
	generation uint64
}

// ListVulnerabilitySummaries returns the severity counts the Trivy Operator
// has recorded for the workloads in one namespace, keyed by "Kind/name".
//
// AN EMPTY ANSWER IS THE ORDINARY ONE and never an error. Three ways to get
// it, all normal: no Trivy Operator installed (the CRD does not exist, so the
// list is a 404), an account that may not read the reports (403), and a
// namespace nothing has been scanned in. All three are cached, for the same
// reason DiscoverMetricsBackend caches a refusal — an account that may never
// list something should not have that retried into its audit log every time
// somebody opens a pod list. A transport failure is NOT cached: a cluster
// that was merely unreachable comes back, and should be asked again when it
// does.
func (a *Adapter) ListVulnerabilitySummaries(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) (domain.VulnerabilityListing, error) {
	// CAPTURED BEFORE ANYTHING ELSE, and carried through to every write
	// below. Invalidate drops this cache, but a list already in flight when
	// it runs writes afterwards; ordering alone cannot close that window.
	generation := a.generations.at(id)

	if cached, ok := a.vulnerabilities.get(id, namespace, generation); ok {
		return cached, nil
	}

	set, err := a.factory.clientsFor(id)
	if err != nil {
		return domain.VulnerabilityListing{}, err
	}

	op := fmt.Sprintf("listing vulnerability reports in %q of %q", namespace, id)
	restClient := set.discovery.RESTClient()
	if restClient == nil {
		return domain.VulnerabilityListing{}, fmt.Errorf("%s: no REST client available", op)
	}

	listing, err := a.readVulnerabilityRows(ctx, restClient, op, namespace)
	if err != nil {
		wrapped := classify(op, err)

		// NotFound covers the case this exists for: the CRD is not installed,
		// which is most clusters. It is filed beside the two refusals because
		// asking again on the next pod list would be asking a question
		// already answered — but the ANSWERS are no longer identical, and
		// that is the change. "No scanner" and "you may not look" are
		// different sentences, and a row with no chip means something
		// different under each.
		switch {
		case errors.Is(wrapped, ports.ErrNotFound):
			listing = domain.VulnerabilityListing{Status: domain.VulnerabilityReadNotInstalled}
		case errors.Is(wrapped, ports.ErrForbidden), errors.Is(wrapped, ports.ErrUnauthenticated):
			listing = domain.VulnerabilityListing{Status: domain.VulnerabilityReadForbidden}
		default:
			// A transport failure is NOT cached: a cluster that was merely
			// unreachable comes back, and should be asked again when it does.
			return domain.VulnerabilityListing{}, wrapped
		}
	}

	a.vulnerabilities.put(id, namespace, generation, listing)
	return listing, nil
}

// readVulnerabilityRows reads one namespace's reports as SERVER-RENDERED
// TABLE ROWS, paged, and sums them by subject.
//
// WHY ROWS AND NOT OBJECTS, which is the whole reason this is cheap. Every
// number PodSteer wants is already a printer column on the CRD: Trivy
// declares Critical/High/Medium/Low/Unknown off `.report.summary.*`, plus
// Scanner, in additionalPrinterColumns. The API server renders those into a
// Table for any client that asks for one — the same mechanism ListTable uses
// for every CRD PodSteer has no model for. So the severity counts arrive in
// about 2-3 KB per report instead of the 50-150 KB a VulnerabilityReport
// weighs once its CVE list is attached, which is what the previous read
// transferred, per namespace, per cache window.
//
// includeObject=Metadata is what carries the operator's labels, and those are
// the only thing that says which workload a report is about.
//
// AND IT PAGES, which the previous read only appeared to do. See
// cachedResourceVersion: a Limit sent with ResourceVersion "0" is discarded
// by the server, so the old read had no bound and no Continue token. Nothing
// here asks for the watch cache.
func (a *Adapter) readVulnerabilityRows(
	ctx context.Context,
	restClient rest.Interface,
	op string,
	namespace domain.NamespaceName,
) (domain.VulnerabilityListing, error) {
	kind := domain.ResourceKind{
		Group:      vulnerabilityReportGVR.Group,
		Version:    vulnerabilityReportGVR.Version,
		Resource:   vulnerabilityReportGVR.Resource,
		Namespaced: true,
	}

	bySubject := make(map[string]domain.VulnerabilitySummary)
	listing := domain.VulnerabilityListing{Status: domain.VulnerabilityReadComplete}
	continueToken := ""

	for listing.Read < vulnerabilityScanCeiling {
		pageSize := min(int64(vulnerabilityScanCeiling-listing.Read), vulnerabilityPageSize)

		request := restClient.Get().
			AbsPath(resourcePath(kind, namespace, "")).
			SetHeader("Accept", tableMediaType).
			Param("includeObject", "Metadata").
			Param("limit", strconv.FormatInt(pageSize, 10))
		if continueToken != "" {
			request = request.Param("continue", continueToken)
		}

		body, err := request.DoRaw(ctx)
		if err != nil {
			return domain.VulnerabilityListing{}, err
		}

		var table metav1.Table
		if err := json.Unmarshal(body, &table); err != nil {
			return domain.VulnerabilityListing{}, fmt.Errorf("%s: decoding table: %w", op, err)
		}

		columns := severityColumns(table.ColumnDefinitions)
		if !columns.usable() {
			// The counts are printer columns and always have been, so this
			// means a rename in some future CRD version. Reporting zeros
			// would be inventing a clean bill of health for every workload in
			// the namespace, which is the one answer this must never give.
			return domain.VulnerabilityListing{}, fmt.Errorf(
				"%s: the reports carry no severity columns — the scanner's CRD may have changed", op)
		}

		for i := range table.Rows {
			row := &table.Rows[i]
			labels := rowMetadata(row, domain.Projection{}).labels
			subject := domain.VulnerabilitySubject(
				labels[trivyResourceKindLabel], labels[trivyResourceNameLabel])
			listing.Read++
			if subject == "" {
				// The operator did not say what this report is about.
				// Attributing it by guessing at the object's name would put
				// somebody else's findings on a workload's row.
				continue
			}

			held := bySubject[subject]
			held.Subject = subject
			held.Counts = held.Counts.Add(columns.counts(row.Cells))
			held.Reports++
			bySubject[subject] = held
		}

		continueToken = table.Continue
		if continueToken == "" {
			break
		}
		listing.Remaining = int(ptrValue(table.RemainingItemCount))
	}

	if continueToken != "" {
		// Stopped at the ceiling with reports left. Everything downstream
		// must be able to say so: a subject with no summary in a truncated
		// listing has not been shown to be clean.
		listing.Status = domain.VulnerabilityReadTruncated
		listing.Cap = vulnerabilityScanCeiling
	} else {
		listing.Remaining = 0
	}

	listing.Summaries = make([]domain.VulnerabilitySummary, 0, len(bySubject))
	for _, summary := range bySubject {
		listing.Summaries = append(listing.Summaries, summary)
	}
	sort.Slice(listing.Summaries, func(i, j int) bool {
		return listing.Summaries[i].Subject < listing.Summaries[j].Subject
	})

	return listing, nil
}

// ptrValue reads an optional count the server may not have sent.
func ptrValue(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

// severityColumns is where each severity's count sits in a rendered row.
//
// BY COLUMN NAME, case-insensitively, because a Table carries no jsonPath —
// the name is the only thing identifying a column, and position is whatever
// the CRD author chose. Trivy has declared these since Starboard; a rename is
// the realistic skew, and it is reported rather than guessed around.
type severityColumnIndex struct {
	critical, high, medium, low, unknown int
}

func severityColumns(definitions []metav1.TableColumnDefinition) severityColumnIndex {
	index := severityColumnIndex{critical: -1, high: -1, medium: -1, low: -1, unknown: -1}
	for at, definition := range definitions {
		switch strings.ToLower(definition.Name) {
		case "critical":
			index.critical = at
		case "high":
			index.high = at
		case "medium":
			index.medium = at
		case "low":
			index.low = at
		case "unknown":
			index.unknown = at
		}
	}
	return index
}

// usable reports whether the two columns anybody acts on were found.
//
// Critical and High decide it. Medium, Low and Unknown are read when present
// and left at zero when not, because a CRD that stopped printing Low would
// still let this answer the question the row asks; one that stopped printing
// Critical would not.
func (i severityColumnIndex) usable() bool { return i.critical >= 0 && i.high >= 0 }

// counts reads one row's cells into the domain's buckets.
func (i severityColumnIndex) counts(cells []any) domain.VulnerabilityCounts {
	return domain.VulnerabilityCounts{
		Critical: cellCount(cells, i.critical),
		High:     cellCount(cells, i.high),
		Medium:   cellCount(cells, i.medium),
		Low:      cellCount(cells, i.low),
		Unknown:  cellCount(cells, i.unknown),
	}
}

// cellCount reads one integer cell, treating anything unreadable as zero —
// the same reading nestedCount gives a field the CRD may not have carried.
func cellCount(cells []any, at int) int {
	if at < 0 || at >= len(cells) {
		return 0
	}
	switch value := cells[at].(type) {
	case float64:
		return int(value)
	case int64:
		return int(value)
	case json.Number:
		parsed, err := value.Int64()
		if err != nil {
			return 0
		}
		return int(parsed)
	default:
		return 0
	}
}

// The full-object summarisation that used to live here is gone with the read
// that needed it. Every number PodSteer shows is a printer column now, so
// nothing decodes report.summary out of an object body — see
// readVulnerabilityRows. The per-report DETAIL panel still parses a manifest,
// but it does that in the frontend, from the one object the drawer already
// fetched: web/src/lib/operators/trivy.ts.

// vulnerabilityCacheKey composes the per-cluster, per-namespace key. The
// separator is one domain.ClusterID is forbidden from containing (see
// NewClusterID), so two clusters cannot collide by naming.
func vulnerabilityCacheKey(id domain.ClusterID, namespace domain.NamespaceName) string {
	return id.String() + "\x00" + namespace.String()
}

func (c *vulnerabilityCache) get(id domain.ClusterID, namespace domain.NamespaceName, generation uint64) (domain.VulnerabilityListing, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[vulnerabilityCacheKey(id, namespace)]
	if !ok || entry.generation != generation || time.Since(entry.at) > vulnerabilityCacheTTL {
		return domain.VulnerabilityListing{}, false
	}
	return entry.listing, true
}

func (c *vulnerabilityCache) put(id domain.ClusterID, namespace domain.NamespaceName, generation uint64, listing domain.VulnerabilityListing) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.entries == nil {
		c.entries = make(map[string]vulnerabilityEntry)
	}
	c.entries[vulnerabilityCacheKey(id, namespace)] = vulnerabilityEntry{
		at:         time.Now(),
		listing:    listing,
		generation: generation,
	}
}

// forget drops one cluster's cached summaries, for a tab being closed or a
// connection being invalidated — the same lifecycle every other per-cluster
// cache here follows, so a reconnect cannot be answered from the previous
// connection's reads.
func (c *vulnerabilityCache) forget(id domain.ClusterID) {
	c.mu.Lock()
	defer c.mu.Unlock()

	prefix := id.String() + "\x00"
	for key := range c.entries {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			delete(c.entries, key)
		}
	}
}
