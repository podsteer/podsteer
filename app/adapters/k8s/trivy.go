package k8s

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

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

// vulnerabilityListLimit caps one namespace's reports.
//
// One report per container per workload, so a busy namespace holds hundreds
// rather than thousands — but this is a side read that must never become the
// expensive thing on the page, and a cluster that has somehow accumulated
// more than this is one where a partial answer beats a stall.
const vulnerabilityListLimit = 500

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
	summary []domain.VulnerabilitySummary
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
func (a *Adapter) ListVulnerabilitySummaries(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) ([]domain.VulnerabilitySummary, error) {
	if cached, ok := a.vulnerabilities.get(id, namespace); ok {
		return cached, nil
	}

	set, err := a.factory.clientsFor(id)
	if err != nil {
		return nil, err
	}

	op := fmt.Sprintf("listing vulnerability reports in %q of %q", namespace, id)
	reports, err := set.dynamic.Resource(vulnerabilityReportGVR).
		Namespace(namespace.String()).
		List(ctx, metav1.ListOptions{
			Limit: vulnerabilityListLimit,
			// The watch cache, like every other poll-adjacent list here: a
			// report seconds out of date is a report about an image that has
			// not changed in hours.
			ResourceVersion: cachedResourceVersion,
		})
	if err != nil {
		wrapped := classify(op, err)

		// NotFound covers the case this exists for: the CRD is not installed,
		// which is most clusters. It is filed beside the two refusals because
		// the outcome is identical — nothing to show — and because asking
		// again on the next pod list would be asking a question already
		// answered.
		if errors.Is(wrapped, ports.ErrNotFound) ||
			errors.Is(wrapped, ports.ErrForbidden) ||
			errors.Is(wrapped, ports.ErrUnauthenticated) {
			a.vulnerabilities.put(id, namespace, nil)
			return nil, nil
		}
		return nil, wrapped
	}

	summaries := summariseVulnerabilityReports(reports.Items)
	a.vulnerabilities.put(id, namespace, summaries)
	return summaries, nil
}

// summariseVulnerabilityReports groups the operator's reports by the workload
// they name and sums each group.
//
// ONE REPORT PER CONTAINER, so a workload's numbers are a sum rather than a
// single report's summary — reading only the first would under-report every
// multi-container pod, and reading the highest would under-report it
// differently. Sorted by subject so two calls with the same cluster state
// produce the same slice, which is what makes the cache comparable and the
// tests deterministic.
func summariseVulnerabilityReports(items []unstructured.Unstructured) []domain.VulnerabilitySummary {
	bySubject := make(map[string]domain.VulnerabilitySummary, len(items))

	for _, item := range items {
		labels := item.GetLabels()
		subject := domain.VulnerabilitySubject(labels[trivyResourceKindLabel], labels[trivyResourceNameLabel])
		if subject == "" {
			// The operator did not say what this report is about. Attributing
			// it to something by guessing at the object's name would put
			// somebody else's findings on a workload's row.
			continue
		}

		held := bySubject[subject]
		held.Subject = subject
		held.Counts = held.Counts.Add(reportSummary(item.Object))
		held.Reports++
		bySubject[subject] = held
	}

	summaries := make([]domain.VulnerabilitySummary, 0, len(bySubject))
	for _, summary := range bySubject {
		summaries = append(summaries, summary)
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].Subject < summaries[j].Subject })

	return summaries
}

// reportSummary reads `report.summary` — the counts Trivy itself computed.
//
// The individual vulnerability list is deliberately NOT read here. It is tens
// of kilobytes per report and the pod list needs four numbers; the panel that
// wants the list gets it from the one GET the drawer already makes when
// somebody opens a report.
func reportSummary(object map[string]any) domain.VulnerabilityCounts {
	return domain.VulnerabilityCounts{
		Critical: nestedCount(object, "criticalCount"),
		High:     nestedCount(object, "highCount"),
		Medium:   nestedCount(object, "mediumCount"),
		Low:      nestedCount(object, "lowCount"),
		Unknown:  nestedCount(object, "unknownCount"),
	}
}

// nestedCount reads one count out of report.summary, treating an absent or
// non-numeric field as zero — the same reading kubectl gives a printer column
// it cannot find, and the only honest one for a field the CRD may not have
// carried in the version that wrote this object.
func nestedCount(object map[string]any, field string) int {
	value, found, err := unstructured.NestedInt64(object, "report", "summary", field)
	if !found || err != nil {
		return 0
	}
	return int(value)
}

// vulnerabilityCacheKey composes the per-cluster, per-namespace key. The
// separator is one domain.ClusterID is forbidden from containing (see
// NewClusterID), so two clusters cannot collide by naming.
func vulnerabilityCacheKey(id domain.ClusterID, namespace domain.NamespaceName) string {
	return id.String() + "\x00" + namespace.String()
}

func (c *vulnerabilityCache) get(id domain.ClusterID, namespace domain.NamespaceName) ([]domain.VulnerabilitySummary, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[vulnerabilityCacheKey(id, namespace)]
	if !ok || time.Since(entry.at) > vulnerabilityCacheTTL {
		return nil, false
	}
	return entry.summary, true
}

func (c *vulnerabilityCache) put(id domain.ClusterID, namespace domain.NamespaceName, summary []domain.VulnerabilitySummary) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.entries == nil {
		c.entries = make(map[string]vulnerabilityEntry)
	}
	c.entries[vulnerabilityCacheKey(id, namespace)] = vulnerabilityEntry{at: time.Now(), summary: summary}
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
