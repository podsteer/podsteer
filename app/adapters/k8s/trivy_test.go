package k8s

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clientgotesting "k8s.io/client-go/testing"

	"github.com/podsteer/podsteer/app/domain"
)

// vulnerabilityListKinds is what the fake dynamic client needs to answer a
// list of a kind no scheme knows — the same registration apply_test.go makes
// for its Widget.
var vulnerabilityListKinds = map[schema.GroupVersionResource]string{
	vulnerabilityReportGVR: "VulnerabilityReportList",
}

// report builds a VulnerabilityReport shaped the way the Trivy Operator
// writes them: the workload it is about in labels, the counts under
// report.summary.
func report(namespace, name, subjectKind, subjectName string, critical, high, medium, low int) *unstructured.Unstructured {
	object := &unstructured.Unstructured{Object: map[string]any{
		"report": map[string]any{
			"summary": map[string]any{
				"criticalCount": int64(critical),
				"highCount":     int64(high),
				"mediumCount":   int64(medium),
				"lowCount":      int64(low),
			},
		},
	}}
	object.SetAPIVersion("aquasecurity.github.io/v1alpha1")
	object.SetKind("VulnerabilityReport")
	object.SetNamespace(namespace)
	object.SetName(name)
	object.SetLabels(map[string]string{
		trivyResourceKindLabel: subjectKind,
		trivyResourceNameLabel: subjectName,
	})
	return object
}

func newTrivyAdapter(id domain.ClusterID, objects ...runtime.Object) (*Adapter, *dynamicfake.FakeDynamicClient) {
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), vulnerabilityListKinds, objects...)
	factory := newClientFactory(Config{})
	factory.clients[id] = &clients{dynamic: client}
	return &Adapter{factory: factory}, client
}

func TestListVulnerabilitySummariesSumsOneReportPerContainer(t *testing.T) {
	// The Trivy Operator writes ONE report per container, so a two-container
	// workload has two of them. Reading only the first would under-report
	// every sidecar in the cluster.
	adapter, _ := newTrivyAdapter("dev",
		report("shop", "replicaset-web-abc123-app", "ReplicaSet", "web-abc123", 1, 4, 9, 2),
		report("shop", "replicaset-web-abc123-proxy", "ReplicaSet", "web-abc123", 2, 1, 0, 0),
	)

	summaries, err := adapter.ListVulnerabilitySummaries(context.Background(), "dev", "shop")
	if err != nil {
		t.Fatalf("ListVulnerabilitySummaries() error = %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("got %d summaries, want 1 — both reports are about the same workload", len(summaries))
	}

	got := summaries[0]
	if got.Subject != "ReplicaSet/web-abc123" {
		t.Errorf("subject = %q, want the Kind/name a pod row carries", got.Subject)
	}
	want := domain.VulnerabilityCounts{Critical: 3, High: 5, Medium: 9, Low: 2}
	if got.Counts != want {
		t.Errorf("counts = %+v, want %+v", got.Counts, want)
	}
	if got.Reports != 2 {
		t.Errorf("reports = %d, want 2 — the count is what tells scanned-and-clean from unscanned", got.Reports)
	}
}

func TestListVulnerabilitySummariesIsOrderedBySubject(t *testing.T) {
	// The API server's ordering is not something to depend on, and a chip
	// that moved between two identical reads would look like the numbers had
	// changed.
	adapter, _ := newTrivyAdapter("dev",
		report("shop", "b", "ReplicaSet", "zeta", 1, 0, 0, 0),
		report("shop", "a", "ReplicaSet", "alpha", 0, 1, 0, 0),
	)

	summaries, err := adapter.ListVulnerabilitySummaries(context.Background(), "dev", "shop")
	if err != nil {
		t.Fatalf("ListVulnerabilitySummaries() error = %v", err)
	}
	if len(summaries) != 2 || summaries[0].Subject != "ReplicaSet/alpha" {
		t.Fatalf("summaries = %+v, want them sorted by subject", summaries)
	}
}

func TestListVulnerabilitySummariesSkipsAReportThatNamesNoWorkload(t *testing.T) {
	// Without the operator's labels nothing in the object says what was
	// scanned. Guessing from the object's own name would put one workload's
	// findings on another's row.
	adapter, _ := newTrivyAdapter("dev", report("shop", "orphan", "", "", 5, 5, 5, 5))

	summaries, err := adapter.ListVulnerabilitySummaries(context.Background(), "dev", "shop")
	if err != nil {
		t.Fatalf("ListVulnerabilitySummaries() error = %v", err)
	}
	if len(summaries) != 0 {
		t.Fatalf("summaries = %+v, want none from an unlabelled report", summaries)
	}
}

func TestListVulnerabilitySummariesReadsAReportWithNoSummaryAsZero(t *testing.T) {
	// A report written by a version of the CRD that carried the counts
	// elsewhere, or one still being filled in. Zero is the honest reading;
	// failing the whole namespace over one object is not.
	bare := &unstructured.Unstructured{Object: map[string]any{}}
	bare.SetAPIVersion("aquasecurity.github.io/v1alpha1")
	bare.SetKind("VulnerabilityReport")
	bare.SetNamespace("shop")
	bare.SetName("bare")
	bare.SetLabels(map[string]string{trivyResourceKindLabel: "ReplicaSet", trivyResourceNameLabel: "web"})

	adapter, _ := newTrivyAdapter("dev", bare)

	summaries, err := adapter.ListVulnerabilitySummaries(context.Background(), "dev", "shop")
	if err != nil {
		t.Fatalf("ListVulnerabilitySummaries() error = %v", err)
	}
	if len(summaries) != 1 || summaries[0].Counts.Total() != 0 || summaries[0].Reports != 1 {
		t.Fatalf("summaries = %+v, want one summary of zero findings from one report", summaries)
	}
}

func TestListVulnerabilitySummariesAnswersNothingWhenNoScannerIsInstalled(t *testing.T) {
	// The ordinary case: no Trivy Operator, so the CRD is not served and the
	// list is a 404. That is not an error anybody should see — a cluster with
	// no scanner is not a degraded cluster.
	adapter, client := newTrivyAdapter("dev")
	client.PrependReactor("list", "vulnerabilityreports", func(clientgotesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(vulnerabilityReportGVR.GroupResource(), "")
	})

	summaries, err := adapter.ListVulnerabilitySummaries(context.Background(), "dev", "shop")
	if err != nil {
		t.Fatalf("ListVulnerabilitySummaries() error = %v, want the absence to be ordinary", err)
	}
	if len(summaries) != 0 {
		t.Fatalf("summaries = %+v, want none", summaries)
	}
}

func TestListVulnerabilitySummariesCachesARefusalRatherThanRetryingIt(t *testing.T) {
	// The same discipline DiscoverMetricsBackend follows: an account that may
	// never list these will never be able to, and retrying on every open of a
	// pod list writes a denied request into somebody's audit log for a
	// feature that is an offer rather than a requirement.
	adapter, client := newTrivyAdapter("dev")

	var lists int
	client.PrependReactor("list", "vulnerabilityreports", func(clientgotesting.Action) (bool, runtime.Object, error) {
		lists++
		return true, nil, apierrors.NewForbidden(vulnerabilityReportGVR.GroupResource(), "", nil)
	})

	for range 3 {
		if _, err := adapter.ListVulnerabilitySummaries(context.Background(), "dev", "shop"); err != nil {
			t.Fatalf("ListVulnerabilitySummaries() error = %v, want a refusal to read as nothing", err)
		}
	}

	if lists != 1 {
		t.Errorf("listed %d times, want 1 — a refusal must be cached", lists)
	}
}

func TestListVulnerabilitySummariesServesTheSecondReadFromTheCache(t *testing.T) {
	// The whole point of the cache: this must never ride the refresh tick. An
	// operator staring at a pod list for ten minutes makes ONE of these
	// calls.
	adapter, client := newTrivyAdapter("dev", report("shop", "a", "ReplicaSet", "web", 1, 0, 0, 0))

	var lists int
	client.PrependReactor("list", "vulnerabilityreports", func(clientgotesting.Action) (bool, runtime.Object, error) {
		lists++
		// Fall through to the tracker, which holds the seeded report.
		return false, nil, nil
	})

	for range 4 {
		if _, err := adapter.ListVulnerabilitySummaries(context.Background(), "dev", "shop"); err != nil {
			t.Fatalf("ListVulnerabilitySummaries() error = %v", err)
		}
	}

	if lists != 1 {
		t.Errorf("listed %d times, want 1 — the rest must come from the cache", lists)
	}
}

func TestVulnerabilityCacheKeepsNamespacesApart(t *testing.T) {
	// The pod list is scoped to a namespace, so the cache has to be too —
	// otherwise the first namespace an operator opened would answer for every
	// other one.
	adapter, client := newTrivyAdapter("dev",
		report("shop", "a", "ReplicaSet", "web", 1, 0, 0, 0),
		report("admin", "b", "ReplicaSet", "console", 0, 7, 0, 0),
	)

	var lists int
	client.PrependReactor("list", "vulnerabilityreports", func(clientgotesting.Action) (bool, runtime.Object, error) {
		lists++
		return false, nil, nil
	})

	shop, err := adapter.ListVulnerabilitySummaries(context.Background(), "dev", "shop")
	if err != nil {
		t.Fatalf("ListVulnerabilitySummaries(shop) error = %v", err)
	}
	admin, err := adapter.ListVulnerabilitySummaries(context.Background(), "dev", "admin")
	if err != nil {
		t.Fatalf("ListVulnerabilitySummaries(admin) error = %v", err)
	}

	if lists != 2 {
		t.Errorf("listed %d times, want 2 — one per namespace", lists)
	}
	if len(shop) != 1 || shop[0].Subject != "ReplicaSet/web" {
		t.Errorf("shop = %+v, want only its own workload", shop)
	}
	if len(admin) != 1 || admin[0].Subject != "ReplicaSet/console" {
		t.Errorf("admin = %+v, want only its own workload", admin)
	}
}

func TestVulnerabilityCacheIsDroppedWhenAClusterIsInvalidated(t *testing.T) {
	// A ten-minute answer carried across a reconnect would put the previous
	// connection's findings on the first pod list of the new one — the same
	// reason the disk sweep is forgotten there.
	adapter, client := newTrivyAdapter("dev", report("shop", "a", "ReplicaSet", "web", 1, 0, 0, 0))

	var lists int
	client.PrependReactor("list", "vulnerabilityreports", func(clientgotesting.Action) (bool, runtime.Object, error) {
		lists++
		return false, nil, nil
	})

	if _, err := adapter.ListVulnerabilitySummaries(context.Background(), "dev", "shop"); err != nil {
		t.Fatalf("ListVulnerabilitySummaries() error = %v", err)
	}
	adapter.vulnerabilities.forget("dev")
	if _, err := adapter.ListVulnerabilitySummaries(context.Background(), "dev", "shop"); err != nil {
		t.Fatalf("ListVulnerabilitySummaries() error = %v", err)
	}

	if lists != 2 {
		t.Errorf("listed %d times, want 2 — the cache must not survive an invalidation", lists)
	}
}

func TestVulnerabilityCacheForgetsOnlyTheClusterNamed(t *testing.T) {
	// Closing one tab must not cost every other tab its answers.
	cache := &vulnerabilityCache{}
	cache.put("dev", "shop", []domain.VulnerabilitySummary{{Subject: "ReplicaSet/web", Reports: 1}})
	cache.put("prod", "shop", []domain.VulnerabilitySummary{{Subject: "ReplicaSet/web", Reports: 1}})

	cache.forget("dev")

	if _, ok := cache.get("dev", "shop"); ok {
		t.Error("dev is still cached after being forgotten")
	}
	if _, ok := cache.get("prod", "shop"); !ok {
		t.Error("prod lost its cached summaries when dev was forgotten")
	}
}
