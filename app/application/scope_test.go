package application_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/adapters/collation"
	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

func scopeOf(t *testing.T, names ...string) domain.NamespaceScope {
	t.Helper()
	scope, err := domain.NewNamespaceScope(names)
	if err != nil {
		t.Fatalf("NewNamespaceScope(%v) error = %v", names, err)
	}
	return scope
}

// scopedKube answers per namespace, and records which namespaces were asked
// for — "" being the cluster-wide read.
type scopedKube struct {
	*fakeKubernetes

	byNamespace map[string][]domain.Pod
	// forbidWide refuses the cluster-wide read; failIn fails one namespace.
	forbidWide bool
	failIn     string

	mu    sync.Mutex
	asked []string
}

func (f *scopedKube) ListPods(_ context.Context, _ domain.ClusterID, namespace domain.NamespaceName, _ domain.Projection) ([]domain.Pod, error) {
	f.mu.Lock()
	f.asked = append(f.asked, namespace.String())
	f.mu.Unlock()

	switch {
	case namespace.IsAll() && f.forbidWide:
		return nil, fmt.Errorf("listing pods: %w", ports.ErrForbidden)
	case namespace.String() == f.failIn && f.failIn != "":
		return nil, errors.New("boom")
	case namespace.IsAll():
		var all []domain.Pod
		for _, name := range []string{"a", "b", "c", "d", "e"} {
			all = append(all, f.byNamespace[name]...)
		}
		return all, nil
	}
	return slices.Clone(f.byNamespace[namespace.String()]), nil
}

func (f *scopedKube) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := slices.Clone(f.asked)
	slices.Sort(out)
	return out
}

func newScopedKube(t *testing.T) *scopedKube {
	t.Helper()
	by := map[string][]domain.Pod{}
	for _, ns := range []string{"a", "b", "c", "d", "e"} {
		for _, name := range []string{"p3", "p1", "p2"} {
			by[ns] = append(by[ns], mustPod(t, ns, name))
		}
	}
	return &scopedKube{fakeKubernetes: &fakeKubernetes{}, byNamespace: by}
}

func newScopedWorkloadService(t *testing.T, kube *scopedKube) *application.WorkloadService {
	t.Helper()

	registry := application.NewRegistry()
	registry.Open(mustCluster(t, "dev", true))
	service, err := application.NewWorkloadService(application.WorkloadServiceDeps{
		Workloads: kube,
		Metrics:   kube,
		Registry:  registry,
		TextOrder: collation.Key,
	})
	if err != nil {
		t.Fatalf("NewWorkloadService() error = %v", err)
	}
	return service
}

func TestListPodsInReadsAShortScopePerNamespace(t *testing.T) {
	t.Parallel()

	kube := newScopedKube(t)
	service := newScopedWorkloadService(t, kube)

	pods, err := service.ListPodsIn(context.Background(), "dev", scopeOf(t, "c", "a"), domain.Projection{})
	if err != nil {
		t.Fatalf("ListPodsIn() error = %v", err)
	}
	if got := kube.calls(); !slices.Equal(got, []string{"a", "c"}) {
		t.Fatalf("asked %q, want one read each of a and c", got)
	}
	if len(pods) != 6 || pods[0].Namespace() != "a" || pods[3].Namespace() != "c" {
		t.Fatalf("pods = %v, want a's then c's, sorted", podNames(pods))
	}
}

func TestListPodsInReadsAWideScopeClusterWideAndFilters(t *testing.T) {
	t.Parallel()

	kube := newScopedKube(t)
	service := newScopedWorkloadService(t, kube)

	pods, err := service.ListPodsIn(context.Background(), "dev", scopeOf(t, "a", "b", "c", "d"), domain.Projection{})
	if err != nil {
		t.Fatalf("ListPodsIn() error = %v", err)
	}
	if got := kube.calls(); !slices.Equal(got, []string{""}) {
		t.Fatalf("asked %q, want exactly one cluster-wide read", got)
	}
	if len(pods) != 12 {
		t.Fatalf("got %d pods, want 12 (e filtered out)", len(pods))
	}
	for _, pod := range pods {
		if pod.Namespace() == "e" {
			t.Fatal("a pod outside the scope came through")
		}
	}
}

func TestListPodsInAllIsOneClusterWideRead(t *testing.T) {
	t.Parallel()

	kube := newScopedKube(t)
	service := newScopedWorkloadService(t, kube)

	pods, err := service.ListPodsIn(context.Background(), "dev", domain.NamespaceScope{All: true}, domain.Projection{})
	if err != nil || len(pods) != 15 {
		t.Fatalf("pods = %d, err = %v; want all 15", len(pods), err)
	}
	if got := kube.calls(); !slices.Equal(got, []string{""}) {
		t.Fatalf("asked %q, want one cluster-wide read", got)
	}
}

func TestListPodsInFallsBackPerNamespaceWhenWideIsForbidden(t *testing.T) {
	t.Parallel()

	kube := newScopedKube(t)
	kube.forbidWide = true
	service := newScopedWorkloadService(t, kube)

	pods, err := service.ListPodsIn(context.Background(), "dev", scopeOf(t, "a", "b", "c", "d"), domain.Projection{})
	if err != nil {
		t.Fatalf("ListPodsIn() error = %v", err)
	}
	if got := kube.calls(); !slices.Equal(got, []string{"", "a", "b", "c", "d"}) {
		t.Fatalf("asked %q, want the refused wide read then one per namespace", got)
	}
	if len(pods) != 12 {
		t.Fatalf("got %d pods, want 12", len(pods))
	}

	// For All the refusal is the answer: there is no namespace set to fall
	// back to.
	if _, err := service.ListPodsIn(context.Background(), "dev", domain.NamespaceScope{All: true}, domain.Projection{}); !errors.Is(err, ports.ErrForbidden) {
		t.Fatalf("All err = %v, want the refusal", err)
	}
}

func TestListPodsInNamesTheNamespaceThatFailed(t *testing.T) {
	t.Parallel()

	kube := newScopedKube(t)
	kube.failIn = "b"
	service := newScopedWorkloadService(t, kube)

	pods, err := service.ListPodsIn(context.Background(), "dev", scopeOf(t, "a", "b"), domain.Projection{})
	if err == nil {
		t.Fatalf("got %d pods and no error; a failed namespace must not become a shorter list", len(pods))
	}
	if want := `listing in "b"`; !contains(err.Error(), want) {
		t.Fatalf("err = %q, want it to name %s", err, want)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func TestQueryPodsInPagesTheMergedSet(t *testing.T) {
	t.Parallel()

	kube := newScopedKube(t)
	service := newScopedWorkloadService(t, kube)

	// Sorted by name, the merged set is p1 p1 p1 p2 p2 p2 ... over three
	// namespaces: nine rows (p1 p1 p1 p2 p2 p2 p3 p3 p3). The page is cut from all of them.
	page, err := service.QueryPodsIn(context.Background(), "dev", scopeOf(t, "a", "b", "c"), domain.Projection{},
		domain.PodQuery{Limit: 4, Offset: 4, SortColumn: "name"})
	if err != nil {
		t.Fatalf("QueryPodsIn() error = %v", err)
	}
	if page.Total != 9 {
		t.Fatalf("Total = %d, want 9 across three namespaces", page.Total)
	}
	if got := podNames(page.Rows); len(got) != 4 || got[0] != "p2" || got[1] != "p2" || got[2] != "p3" {
		t.Fatalf("rows = %v, want the second page of the merged, name-sorted set", got)
	}
}

func TestListPodKeysInSpansTheScope(t *testing.T) {
	t.Parallel()

	service := newScopedWorkloadService(t, newScopedKube(t))

	keys, err := service.ListPodKeysIn(context.Background(), "dev", scopeOf(t, "a", "e"), domain.Projection{}, domain.PodQuery{})
	if err != nil || len(keys) != 6 {
		t.Fatalf("keys = %d, err = %v; want 6", len(keys), err)
	}
}

// scopedUsage is a metrics port that counts its reads per namespace.
type scopedUsage struct {
	*fakeKubernetes
	mu    sync.Mutex
	asked []string
	fail  string
}

func (f *scopedUsage) PodMetrics(_ context.Context, _ domain.ClusterID, ns domain.NamespaceName) (map[string]domain.PodUsage, error) {
	f.mu.Lock()
	f.asked = append(f.asked, ns.String())
	f.mu.Unlock()
	if ns.String() == f.fail && f.fail != "" {
		return nil, ports.ErrMetricsUnavailable
	}
	return map[string]domain.PodUsage{}, nil
}

func TestPodMetricsFollowTheScope(t *testing.T) {
	t.Parallel()

	kube := newScopedKube(t)
	usage := &scopedUsage{fakeKubernetes: kube.fakeKubernetes}
	registry := application.NewRegistry()
	registry.Open(mustCluster(t, "dev", true))
	service, err := application.NewWorkloadService(application.WorkloadServiceDeps{Workloads: kube, Metrics: usage, Registry: registry})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.ListPodsIn(context.Background(), "dev", scopeOf(t, "a", "b"), domain.Projection{}); err != nil {
		t.Fatal(err)
	}
	slices.Sort(usage.asked)
	if !slices.Equal(usage.asked, []string{"a", "b"}) {
		t.Fatalf("metrics asked %q, want per namespace", usage.asked)
	}

	usage.asked = nil
	if _, err := service.ListPodsIn(context.Background(), "dev", scopeOf(t, "a", "b", "c", "d"), domain.Projection{}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(usage.asked, []string{""}) {
		t.Fatalf("metrics asked %q, want one cluster-wide read", usage.asked)
	}
}

// scopedResources serves one table per namespace.
type scopedResources struct {
	*fakeResources
	tables     map[string]domain.ResourceTable
	forbidWide bool
	mu         sync.Mutex
	asked      []string
}

func (f *scopedResources) ListTable(_ context.Context, _ domain.ClusterID, _ domain.ResourceKind, ns domain.NamespaceName, _ domain.Projection) (domain.ResourceTable, error) {
	f.mu.Lock()
	f.asked = append(f.asked, ns.String())
	f.mu.Unlock()
	if ns.IsAll() {
		if f.forbidWide {
			return domain.ResourceTable{}, ports.ErrForbidden
		}
		var rows []domain.TableRow
		var first domain.ResourceTable
		for _, name := range []string{"a", "b", "c", "d", "e"} {
			table := f.tables[name]
			if first.Columns() == nil {
				first = table
			}
			rows = append(rows, table.Rows()...)
		}
		return domain.NewResourceTable(first.Kind(), first.Columns(), rows), nil
	}
	return f.tables[ns.String()], nil
}

func configTable(ns string, truncatedAt int, names ...string) domain.ResourceTable {
	rows := make([]domain.TableRow, 0, len(names))
	for _, name := range names {
		rows = append(rows, domain.TableRow{Name: name, Namespace: domain.NamespaceName(ns), Cells: []string{name}})
	}
	table := domain.NewResourceTable(domain.ResourceKind{Kind: "ConfigMap"}, []domain.TableColumn{{Name: "Name", Type: "string"}}, rows)
	if truncatedAt > 0 {
		table = table.WithTruncation(truncatedAt)
	}
	return table
}

func newScopedBrowse(t *testing.T, resources *scopedResources) *application.BrowseService {
	t.Helper()
	registry := application.NewRegistry()
	registry.Open(mustCluster(t, "dev", true))
	service, err := application.NewBrowseService(application.BrowseServiceDeps{
		Resources: resources, Events: &fakeEvents{}, Registry: registry, Catalog: domain.NewCatalog(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestListTableInMergesNamespaceTables(t *testing.T) {
	t.Parallel()

	resources := &scopedResources{fakeResources: &fakeResources{}, tables: map[string]domain.ResourceTable{
		"a": configTable("a", 0, "a1", "a2"),
		"b": configTable("b", 50, "b1"),
		"c": configTable("c", 0, "c1"),
	}}
	service := newScopedBrowse(t, resources)

	table, err := service.ListTableIn(context.Background(), "dev", "core/v1/configmaps", scopeOf(t, "b", "a"), domain.Projection{})
	if err != nil {
		t.Fatalf("ListTableIn() error = %v", err)
	}

	var names []string
	for _, row := range table.Rows() {
		names = append(names, row.Namespace.String()+"/"+row.Name)
	}
	if !slices.Equal(names, []string{"a/a1", "a/a2", "b/b1"}) {
		t.Fatalf("rows = %v, want a's then b's in scope order", names)
	}
	if len(table.Columns()) != 1 {
		t.Fatalf("columns = %v, want the kind's one column", table.Columns())
	}
	if !table.Truncated() || table.Cap() != 50 {
		t.Fatalf("truncated = %v cap = %d, want one truncated read to mark the whole", table.Truncated(), table.Cap())
	}
	if slices.Contains(resources.asked, "") {
		t.Fatalf("asked %q, want per-namespace reads only", resources.asked)
	}
}

func TestListTableInWideScopeFiltersAndFallsBack(t *testing.T) {
	t.Parallel()

	tables := map[string]domain.ResourceTable{}
	for _, ns := range []string{"a", "b", "c", "d", "e"} {
		tables[ns] = configTable(ns, 0, ns+"1")
	}
	resources := &scopedResources{fakeResources: &fakeResources{}, tables: tables}
	service := newScopedBrowse(t, resources)
	wide := scopeOf(t, "a", "b", "c", "d")

	table, err := service.ListTableIn(context.Background(), "dev", "core/v1/configmaps", wide, domain.Projection{})
	if err != nil || table.Len() != 4 {
		t.Fatalf("rows = %d, err = %v; want 4 with e filtered out", table.Len(), err)
	}
	if !slices.Equal(resources.asked, []string{""}) {
		t.Fatalf("asked %q, want one cluster-wide read", resources.asked)
	}

	resources.asked, resources.forbidWide = nil, true
	table, err = service.ListTableIn(context.Background(), "dev", "core/v1/configmaps", wide, domain.Projection{})
	if err != nil || table.Len() != 4 {
		t.Fatalf("rows = %d, err = %v; want 4 from the fallback", table.Len(), err)
	}
	if len(resources.asked) != 5 {
		t.Fatalf("asked %q, want the refused wide read and four per-namespace reads", resources.asked)
	}
}

// scopedHelm answers one listing per namespace.
type scopedHelm struct {
	*fakeHelm
	byNamespace map[string]domain.HelmListing
}

func (f *scopedHelm) ListHelmReleases(_ context.Context, _ domain.ClusterID, ns domain.NamespaceName, _ bool) (domain.HelmListing, error) {
	return f.byNamespace[ns.String()], nil
}

func TestListReleasesInMergesAndKeepsTheWorstStatus(t *testing.T) {
	t.Parallel()

	helm := &scopedHelm{fakeHelm: &fakeHelm{}, byNamespace: map[string]domain.HelmListing{
		"a": {Status: domain.HelmListed, Releases: []domain.HelmRelease{{Namespace: "a", Name: "one"}}, ListedAt: 20},
		"b": {Status: domain.HelmListed, Releases: []domain.HelmRelease{{Namespace: "b", Name: "two"}}, Truncated: true, ListedAt: 10},
	}}
	registry := application.NewRegistry()
	registry.Open(mustCluster(t, "dev", true))
	service, err := application.NewHelmService(application.HelmServiceDeps{Helm: helm, Registry: registry})
	if err != nil {
		t.Fatal(err)
	}

	listing, err := service.ListReleasesIn(context.Background(), "dev", scopeOf(t, "a", "b"), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(listing.Releases) != 2 || !listing.Truncated || listing.ListedAt != 10 || listing.Status != domain.HelmListed {
		t.Fatalf("listing = %+v, want both releases, truncated, aged by the oldest read", listing)
	}

	helm.byNamespace["b"] = domain.HelmListing{Status: domain.HelmForbidden, Refusal: "no"}
	listing, err = service.ListReleasesIn(context.Background(), "dev", scopeOf(t, "a", "b"), false)
	if err != nil || listing.Status != domain.HelmForbidden || listing.Refusal != "no" {
		t.Fatalf("listing = %+v, err = %v; want the refusal to stand for the whole", listing, err)
	}
}

func TestFleetListPodsInReadsEachClusterOverTheScope(t *testing.T) {
	t.Parallel()

	source := &fakeFleetSource{pods: map[domain.ClusterID][]domain.Pod{
		"one": {clusterPod(t, "one", "x"), mustPod(t, "shop", "y")},
		"two": {clusterPod(t, "two", "z")},
	}}
	service := newFleetService(t, source, time.Second, "one", "two")

	// Two namespaces: one read per namespace per cluster.
	reads, err := service.ListPodsIn(context.Background(), ids("one", "two"), scopeOf(t, "default", "shop"))
	if err != nil {
		t.Fatalf("ListPodsIn() error = %v", err)
	}
	if got := source.started(); got != 4 {
		t.Fatalf("started %d reads, want 4 (2 clusters x 2 namespaces)", got)
	}
	if len(reads) != 2 || reads[0].Status != domain.ClusterReadOK {
		t.Fatalf("reads = %+v", reads)
	}

	// Four namespaces: one cluster-wide read per cluster, filtered.
	wide := scopeOf(t, "default", "b", "c", "d")
	reads, err = service.ListPodsIn(context.Background(), ids("one", "two"), wide)
	if err != nil {
		t.Fatal(err)
	}
	if got := source.started(); got != 6 {
		t.Fatalf("started %d reads in total, want 2 more", got)
	}
	if len(reads[0].Items) != 1 || reads[0].Items[0].Name() != "x" {
		t.Fatalf("one's items = %v, want only the default pod", reads[0].Items)
	}
}

func TestFleetQueryPodsInPagesTheMergedScope(t *testing.T) {
	t.Parallel()

	source := &fakeFleetSource{pods: map[domain.ClusterID][]domain.Pod{
		"one": {clusterPod(t, "one", "x"), clusterPod(t, "one", "y")},
		"two": {clusterPod(t, "two", "z")},
	}}
	service := newFleetService(t, source, time.Second, "one", "two")

	page, err := service.QueryPodsIn(context.Background(), ids("one", "two"), scopeOf(t, "default"), domain.PodQuery{Limit: 2})
	if err != nil {
		t.Fatalf("QueryPodsIn() error = %v", err)
	}
	if page.Total != 3 || len(page.Rows) != 2 || len(page.Clusters) != 2 {
		t.Fatalf("page = total %d rows %d clusters %d; want 3 rows total cut to 2, two verdicts", page.Total, len(page.Rows), len(page.Clusters))
	}
}

// scopedVulns serves one listing per namespace, and one for the whole cluster.
type scopedVulns struct {
	*fakeResources
	wide        domain.VulnerabilityListing
	byNamespace map[string]domain.VulnerabilityListing
	asked       []string
	mu          sync.Mutex
}

func (f *scopedVulns) ListVulnerabilitySummaries(_ context.Context, _ domain.ClusterID, ns domain.NamespaceName) (domain.VulnerabilityListing, error) {
	f.mu.Lock()
	f.asked = append(f.asked, ns.String())
	f.mu.Unlock()
	if ns.IsAll() {
		return f.wide, nil
	}
	return f.byNamespace[ns.String()], nil
}

func summary(ns, subject string, critical int) domain.VulnerabilitySummary {
	return domain.VulnerabilitySummary{
		Namespace: domain.NamespaceName(ns), Subject: subject, Reports: 1,
		Counts: domain.VulnerabilityCounts{Critical: critical},
	}
}

func TestVulnerabilitySummariesInKeepsSameNamedWorkloadsApart(t *testing.T) {
	t.Parallel()

	complete := domain.VulnerabilityReadComplete
	vulns := &scopedVulns{
		fakeResources: &fakeResources{},
		wide: domain.VulnerabilityListing{Status: complete, Summaries: []domain.VulnerabilitySummary{
			summary("a", "Deployment/web", 1), summary("b", "Deployment/web", 5), summary("c", "Deployment/web", 9),
			summary("d", "Deployment/api", 2), summary("e", "Deployment/web", 7),
		}},
		byNamespace: map[string]domain.VulnerabilityListing{
			"a": {Status: complete, Summaries: []domain.VulnerabilitySummary{summary("a", "Deployment/web", 1)}},
			"b": {Status: complete, Summaries: []domain.VulnerabilitySummary{summary("b", "Deployment/web", 5)}},
		},
	}
	service := newBrowseWith(t, vulns)

	// Short scope: per namespace, merged without summing across namespaces.
	got, err := service.VulnerabilitySummariesIn(context.Background(), "dev", scopeOf(t, "a", "b"))
	if err != nil || len(got.Summaries) != 2 {
		t.Fatalf("summaries = %+v, err = %v; want a/web and b/web apart", got.Summaries, err)
	}
	if got.Summaries[0].Counts.Critical != 1 || got.Summaries[1].Counts.Critical != 5 {
		t.Fatalf("counts = %+v, want 1 and 5, not summed", got.Summaries)
	}

	// Wide scope: one cluster-wide read, filtered to the scope.
	got, err = service.VulnerabilitySummariesIn(context.Background(), "dev", scopeOf(t, "a", "b", "c", "d"))
	if err != nil || len(got.Summaries) != 4 {
		t.Fatalf("summaries = %+v, err = %v; want e filtered out", got.Summaries, err)
	}
	for _, s := range got.Summaries {
		if s.Namespace == "e" {
			t.Fatal("a summary outside the scope came through")
		}
	}
}

func TestVulnerabilitySummariesInFallsBackWhenTheWideReadIsRefused(t *testing.T) {
	t.Parallel()

	complete := domain.VulnerabilityReadComplete
	vulns := &scopedVulns{
		fakeResources: &fakeResources{},
		wide:          domain.VulnerabilityListing{Status: domain.VulnerabilityReadForbidden},
		byNamespace: map[string]domain.VulnerabilityListing{
			"a": {Status: complete, Summaries: []domain.VulnerabilitySummary{summary("a", "Deployment/web", 1)}},
			"b": {Status: complete}, "c": {Status: complete}, "d": {Status: complete},
		},
	}
	service := newBrowseWith(t, vulns)

	got, err := service.VulnerabilitySummariesIn(context.Background(), "dev", scopeOf(t, "a", "b", "c", "d"))
	if err != nil || got.Status != complete || len(got.Summaries) != 1 {
		t.Fatalf("listing = %+v, err = %v; want the per-namespace answer, not the refusal", got, err)
	}
	if len(vulns.asked) != 5 {
		t.Fatalf("asked %q, want the refused wide read then four per-namespace", vulns.asked)
	}

	// For All the refusal stands.
	got, err = service.VulnerabilitySummariesIn(context.Background(), "dev", domain.NamespaceScope{All: true})
	if err != nil || got.Status != domain.VulnerabilityReadForbidden {
		t.Fatalf("All = %+v, %v; want the refusal as the answer", got, err)
	}
}

func newBrowseWith(t *testing.T, resources ports.ResourcePort) *application.BrowseService {
	t.Helper()
	registry := application.NewRegistry()
	registry.Open(mustCluster(t, "dev", true))
	service, err := application.NewBrowseService(application.BrowseServiceDeps{
		Resources: resources, Events: &fakeEvents{}, Registry: registry, Catalog: domain.NewCatalog(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestASingleNamespaceErrorIsNotPrefixed(t *testing.T) {
	t.Parallel()

	kube := newScopedKube(t)
	kube.failIn = "b"
	service := newScopedWorkloadService(t, kube)

	_, err := service.ListPods(context.Background(), "dev", "b", domain.Projection{})
	if err == nil || strings.Contains(err.Error(), "listing in") {
		t.Fatalf("err = %v, want the old text without a namespace prefix", err)
	}
}

func TestTheZeroScopeReadsEverythingRatherThanNothing(t *testing.T) {
	t.Parallel()

	kube := newScopedKube(t)
	service := newScopedWorkloadService(t, kube)

	pods, err := service.ListPodsIn(context.Background(), "dev", domain.NamespaceScope{}, domain.Projection{})
	if err != nil || len(pods) != 15 {
		t.Fatalf("pods = %d, err = %v; want all 15", len(pods), err)
	}
	if got := kube.calls(); !slices.Equal(got, []string{""}) {
		t.Fatalf("asked %q, want one cluster-wide read", got)
	}
}

func TestListReleasesInKeepsTheWorstNonListedStatus(t *testing.T) {
	t.Parallel()

	helm := &scopedHelm{fakeHelm: &fakeHelm{}, byNamespace: map[string]domain.HelmListing{
		"a": {Status: domain.HelmFailed, Refusal: "down"},
		"b": {Status: domain.HelmForbidden, Refusal: "no"},
	}}
	registry := application.NewRegistry()
	registry.Open(mustCluster(t, "dev", true))
	service, err := application.NewHelmService(application.HelmServiceDeps{Helm: helm, Registry: registry})
	if err != nil {
		t.Fatal(err)
	}

	// Failed is first here and forbidden second; reversing must not matter.
	for _, scope := range []domain.NamespaceScope{scopeOf(t, "a", "b")} {
		listing, err := service.ListReleasesIn(context.Background(), "dev", scope, false)
		if err != nil || listing.Status != domain.HelmFailed || listing.Refusal != "down" {
			t.Fatalf("listing = %+v, err = %v; want failed to outrank forbidden", listing, err)
		}
	}
	helm.byNamespace["a"], helm.byNamespace["b"] = helm.byNamespace["b"], helm.byNamespace["a"]
	listing, _ := service.ListReleasesIn(context.Background(), "dev", scopeOf(t, "a", "b"), false)
	if listing.Status != domain.HelmFailed {
		t.Fatalf("status = %q, want failed regardless of order", listing.Status)
	}
}
