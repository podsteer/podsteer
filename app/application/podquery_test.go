package application_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/podsteer/podsteer/app/adapters/collation"
	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

func podNames(pods []domain.Pod) []string {
	names := make([]string, len(pods))
	for i, pod := range pods {
		names[i] = pod.Name()
	}
	return names
}

func TestQueryPodsAnswersOnePageOfTheSortedList(t *testing.T) {
	t.Parallel()

	kubernetes := &fakeKubernetes{pods: []domain.Pod{
		mustPod(t, "shop", "web-10"),
		mustPod(t, "shop", "web-2"),
		mustPod(t, "shop", "web-1"),
		mustPod(t, "ops", "agent"),
		mustPod(t, "shop", "web-3"),
	}}
	service := newWorkloadService(t, kubernetes, true)
	projection := domain.NewProjection([]string{"owner"})

	tests := []struct {
		name  string
		query domain.PodQuery
		want  []string
	}{
		// ListPods' own order is namespace then name, byte-wise: web-10
		// before web-2. Sorting by name is the table's natural order.
		{"list order", domain.PodQuery{Limit: 2}, []string{"agent", "web-1"}},
		{"second page", domain.PodQuery{Limit: 2, Offset: 2}, []string{"web-10", "web-2"}},
		{"sorted by name", domain.PodQuery{Limit: 2, Offset: 2, SortColumn: "name"}, []string{"web-2", "web-3"}},
		{"sorted by name, last page", domain.PodQuery{Limit: 2, Offset: 4, SortColumn: "name"}, []string{"web-10"}},
		// -web-1 is a substring: it takes web-10 with it.
		{"searched", domain.PodQuery{Limit: 10, Text: "web -web-1"}, []string{"web-2", "web-3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			page, err := service.QueryPods(context.Background(), "dev", domain.NamespaceAll, projection, tt.query)
			if err != nil {
				t.Fatalf("QueryPods() error = %v", err)
			}
			if got := podNames(page.Rows); !slices.Equal(got, tt.want) {
				t.Fatalf("rows = %v, want %v", got, tt.want)
			}
			if page.Total != 5 {
				t.Fatalf("total = %d, want 5", page.Total)
			}
		})
	}

	// The projection reaches the read: an annotation column's values are
	// part of the searchable text, so a page query without it would search
	// rows that cannot carry them.
	kubernetes.mu.Lock()
	defer kubernetes.mu.Unlock()
	for _, got := range kubernetes.requestedProjections {
		if got.String() != projection.String() {
			t.Fatalf("a pod list was read with %v, want %v", got, projection)
		}
	}
}

func TestQueryPodsRefusesTheWholeListAsAPage(t *testing.T) {
	t.Parallel()

	kubernetes := &fakeKubernetes{}
	service := newWorkloadService(t, kubernetes, true)

	_, err := service.QueryPods(context.Background(), "dev", domain.NamespaceAll, domain.Projection{}, domain.PodQuery{Limit: domain.MaxPodPageSize + 1})
	if !errors.Is(err, domain.ErrInvalidPodQuery) {
		t.Fatalf("QueryPods() error = %v, want %v", err, domain.ErrInvalidPodQuery)
	}
	if len(kubernetes.requestedProjections) != 0 {
		t.Fatal("an invalid query still read the pod list")
	}
}

func TestListPodKeysCoversEveryPage(t *testing.T) {
	t.Parallel()

	var pods []domain.Pod
	for i := range 120 {
		pods = append(pods, mustPod(t, "shop", fmt.Sprintf("web-%03d", i)))
	}
	service := newWorkloadService(t, &fakeKubernetes{pods: pods}, true)

	// A page query's page is ignored: the keys are for every match.
	keys, err := service.ListPodKeys(context.Background(), "dev", domain.NamespaceAll, domain.Projection{}, domain.PodQuery{Text: "web-0", Limit: 25, Offset: 50})
	if err != nil {
		t.Fatalf("ListPodKeys() error = %v", err)
	}
	if len(keys) != 100 || keys[0].Name != "web-000" || keys[0].Namespace != "shop" {
		t.Fatalf("got %d keys starting %+v, want 100 starting shop/web-000", len(keys), keys[0])
	}
}

func newQueryFleet(t *testing.T, source *fakeFleetSource, open ...string) *application.FleetService {
	t.Helper()

	registry := application.NewRegistry()
	for _, id := range open {
		registry.Open(mustCluster(t, id, false))
	}
	service, err := application.NewFleetService(application.FleetServiceDeps{
		Workloads: source,
		Events:    source,
		Resources: source,
		Catalog:   domain.NewCatalog(),
		Registry:  registry,
		TextOrder: collation.Key,
	})
	if err != nil {
		t.Fatalf("NewFleetService() error = %v", err)
	}
	return service
}

// TestFleetQueryPodsKeepsAnUnreachableClustersRowsAndDropsARefusedOnes pins
// the merge rules the webview used to apply (mergeFleet) now that it holds
// one page: a cluster that stopped answering keeps its last rows, marked
// stale; one that refused loses them.
func TestFleetQueryPodsKeepsAnUnreachableClustersRowsAndDropsARefusedOnes(t *testing.T) {
	t.Parallel()

	source := &fakeFleetSource{pods: map[domain.ClusterID][]domain.Pod{
		"prod":    {clusterPod(t, "prod", "api-0"), clusterPod(t, "prod", "api-1")},
		"staging": {clusterPod(t, "staging", "api-0")},
	}}
	service := newQueryFleet(t, source, "prod", "staging")
	query := domain.PodQuery{Limit: 50}

	read := func() domain.FleetPodPage {
		t.Helper()
		page, err := service.QueryPods(context.Background(), ids("prod", "staging"), domain.NamespaceAll, query)
		if err != nil {
			t.Fatalf("QueryPods() error = %v", err)
		}
		return page
	}

	first := read()
	if first.Matched != 3 || first.Clusters[0].Stale || first.Clusters[0].RowsAt.IsZero() {
		t.Fatalf("first read: %+v", first)
	}

	source.errs = map[domain.ClusterID]error{"prod": fmt.Errorf("dial: %w", ports.ErrUnreachable)}
	second := read()
	prod := second.Clusters[0]
	if prod.Status != domain.ClusterReadUnreachable || !prod.Stale || prod.Rows != 2 || prod.RowsAt != first.Clusters[0].RowsAt {
		t.Fatalf("unreachable prod = %+v, want its two rows kept from the first read, stale", prod)
	}
	if second.Matched != 3 {
		t.Fatalf("matched %d, want the kept rows still on the table", second.Matched)
	}

	source.errs = map[domain.ClusterID]error{"prod": fmt.Errorf("list pods: %w", ports.ErrForbidden)}
	third := read()
	if prod := third.Clusters[0]; prod.Status != domain.ClusterReadForbidden || prod.Rows != 0 || prod.Stale {
		t.Fatalf("forbidden prod = %+v, want no rows", prod)
	}
	if third.Matched != 1 {
		t.Fatalf("matched %d, want staging's one row only", third.Matched)
	}

	// And forgotten: answering unreachable afterwards has nothing to keep.
	source.errs = map[domain.ClusterID]error{"prod": fmt.Errorf("dial: %w", ports.ErrUnreachable)}
	if prod := read().Clusters[0]; prod.Rows != 0 {
		t.Fatalf("rows came back from before the refusal: %+v", prod)
	}
}

func TestFleetQueryPodsNarrowsRowsNotReads(t *testing.T) {
	t.Parallel()

	source := &fakeFleetSource{pods: map[domain.ClusterID][]domain.Pod{
		"prod":    {clusterPod(t, "prod", "api-0")},
		"staging": {clusterPod(t, "staging", "api-0"), clusterPod(t, "staging", "api-1")},
	}}
	service := newQueryFleet(t, source, "prod", "staging")

	page, err := service.QueryPods(context.Background(), ids("prod", "staging"), domain.NamespaceAll, domain.PodQuery{Limit: 50, Clusters: []string{"staging"}, SortColumn: "name", Descending: true})
	if err != nil {
		t.Fatalf("QueryPods() error = %v", err)
	}
	if len(page.Clusters) != 2 {
		t.Fatalf("strip has %d clusters, want both — a deselected cluster is still reported", len(page.Clusters))
	}
	if got := podNames(page.Rows); !slices.Equal(got, []string{"api-1", "api-0"}) || page.Rows[0].ClusterID() != "staging" {
		t.Fatalf("rows = %v, want staging's two, sorted descending", got)
	}
}
