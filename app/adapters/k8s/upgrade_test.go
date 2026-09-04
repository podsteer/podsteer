package k8s

import (
	"context"
	"log/slog"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	discoveryfake "k8s.io/client-go/discovery/fake"
	metadatafake "k8s.io/client-go/metadata/fake"
	clientgotesting "k8s.io/client-go/testing"

	"github.com/podsteer/podsteer/app/domain"
)

// widgetKind is the deprecated-looking group/version/resource used
// throughout these tests. It need not be a real table entry — upgrade.go
// works from whatever domain.ResourceKind it is handed.
var widgetKind = domain.ResourceKind{
	Group: "example.com", Version: "v1beta1", Resource: "widgets", Kind: "Widget",
}

// newUpgradeTestAdapter returns an Adapter whose discovery and metadata
// clients for id are the ones given, following newTestAdapterApply
// (apply_test.go).
func newUpgradeTestAdapter(id domain.ClusterID, disco *discoveryfake.FakeDiscovery, meta *metadatafake.FakeMetadataClient) *Adapter {
	factory := newClientFactory(Config{})
	factory.clients[id] = &clients{discovery: disco, meta: meta}
	return &Adapter{factory: factory, logger: slog.New(slog.DiscardHandler)}
}

// newFakeDiscovery builds a FakeDiscovery reporting the given group/versions
// as served, in the shape ServerGroups derives them from — one
// APIResourceList per group/version, with at least one resource on it.
func newFakeDiscovery(groupVersions ...string) *discoveryfake.FakeDiscovery {
	resources := make([]*metav1.APIResourceList, 0, len(groupVersions))
	for _, gv := range groupVersions {
		resources = append(resources, &metav1.APIResourceList{GroupVersion: gv})
	}
	return &discoveryfake.FakeDiscovery{Fake: &clientgotesting.Fake{Resources: resources}}
}

// newPartialObject builds a PartialObjectMetadata carrying the given
// managedFields, the way an object with a write history would look.
func newPartialObject(apiVersion, kind, namespace, name string, managedFields ...metav1.ManagedFieldsEntry) *metav1.PartialObjectMetadata {
	return &metav1.PartialObjectMetadata{
		TypeMeta: metav1.TypeMeta{APIVersion: apiVersion, Kind: kind},
		ObjectMeta: metav1.ObjectMeta{
			Namespace:     namespace,
			Name:          name,
			ManagedFields: managedFields,
		},
	}
}

// newFakeMetadataClient builds a metadata fake pre-populated with objects.
func newFakeMetadataClient(t *testing.T, objects ...runtime.Object) *metadatafake.FakeMetadataClient {
	t.Helper()

	scheme := metadatafake.NewTestScheme()
	if err := metav1.AddMetaToScheme(scheme); err != nil {
		t.Fatalf("AddMetaToScheme() error = %v", err)
	}
	return metadatafake.NewSimpleMetadataClient(scheme, objects...)
}

func TestServedAPIsMapsCoreGroupedAndMultiVersionGroups(t *testing.T) {
	t.Parallel()

	disco := newFakeDiscovery("v1", "apps/v1", "apps/v1beta1", "flowcontrol.apiserver.k8s.io/v1beta3")
	adapter := newUpgradeTestAdapter("dev", disco, newFakeMetadataClient(t))

	served, err := adapter.ServedAPIs(context.Background(), "dev")
	if err != nil {
		t.Fatalf("ServedAPIs() error = %v", err)
	}

	want := map[domain.APIGroupVersion]bool{
		{Group: "", Version: "v1"}:                                  true,
		{Group: "apps", Version: "v1"}:                              true,
		{Group: "apps", Version: "v1beta1"}:                         true,
		{Group: "flowcontrol.apiserver.k8s.io", Version: "v1beta3"}: true,
	}
	if len(served) != len(want) {
		t.Fatalf("served = %v, want %d entries", served, len(want))
	}
	for _, gv := range served {
		if !want[gv] {
			t.Errorf("served contains unexpected %+v", gv)
		}
	}
}

// A writer is anyone who last wrote the object through THIS kind's own API
// version — an Apply through a different, still-current version must not
// count, or every removal finding would falsely implicate whoever last
// touched the object through the replacement.
func TestAPIWritersOnlyForEntriesOnTheDeprecatedVersion(t *testing.T) {
	t.Parallel()

	obj := newPartialObject(widgetKind.GroupVersion(), widgetKind.Kind, "team-a", "thing-1",
		metav1.ManagedFieldsEntry{Manager: "legacy-controller", Operation: metav1.ManagedFieldsOperationUpdate, APIVersion: widgetKind.GroupVersion()},
		metav1.ManagedFieldsEntry{Manager: "new-controller", Operation: metav1.ManagedFieldsOperationApply, APIVersion: "example.com/v1"},
	)
	adapter := newUpgradeTestAdapter("dev", newFakeDiscovery(), newFakeMetadataClient(t, obj))

	usage, err := adapter.APIWriters(context.Background(), "dev", widgetKind, 100)
	if err != nil {
		t.Fatalf("APIWriters() error = %v", err)
	}
	if len(usage.Writers) != 1 || usage.Writers[0].Manager != "legacy-controller" {
		t.Fatalf("writers = %v, want only legacy-controller", usage.Writers)
	}
	if usage.Writers[0].Namespace != "team-a" || usage.Writers[0].Name != "thing-1" {
		t.Errorf("writer = %+v, want the object's namespace and name", usage.Writers[0])
	}
}

// The same manager writing through the deprecated version twice on one
// object — an Update followed later by another Update, say — must be
// reported once, not once per managedFields entry.
func TestAPIWritersDedupesTheSameManagerOnOneObject(t *testing.T) {
	t.Parallel()

	obj := newPartialObject(widgetKind.GroupVersion(), widgetKind.Kind, "team-a", "thing-1",
		metav1.ManagedFieldsEntry{Manager: "helm", Operation: metav1.ManagedFieldsOperationUpdate, APIVersion: widgetKind.GroupVersion()},
		metav1.ManagedFieldsEntry{Manager: "helm", Operation: metav1.ManagedFieldsOperationApply, APIVersion: widgetKind.GroupVersion()},
	)
	adapter := newUpgradeTestAdapter("dev", newFakeDiscovery(), newFakeMetadataClient(t, obj))

	usage, err := adapter.APIWriters(context.Background(), "dev", widgetKind, 100)
	if err != nil {
		t.Fatalf("APIWriters() error = %v", err)
	}
	if len(usage.Writers) != 1 {
		t.Fatalf("writers = %v, want the duplicate manager collapsed to one", usage.Writers)
	}
}

// A limit smaller than the object count must stop the scan and say so,
// rather than silently reporting a partial answer as the whole truth.
func TestAPIWritersReportsScannedAndTruncated(t *testing.T) {
	t.Parallel()

	objA := newPartialObject(widgetKind.GroupVersion(), widgetKind.Kind, "team-a", "thing-1",
		metav1.ManagedFieldsEntry{Manager: "helm", APIVersion: widgetKind.GroupVersion()})
	objB := newPartialObject(widgetKind.GroupVersion(), widgetKind.Kind, "team-a", "thing-2",
		metav1.ManagedFieldsEntry{Manager: "helm", APIVersion: widgetKind.GroupVersion()})
	adapter := newUpgradeTestAdapter("dev", newFakeDiscovery(), newFakeMetadataClient(t, objA, objB))

	usage, err := adapter.APIWriters(context.Background(), "dev", widgetKind, 1)
	if err != nil {
		t.Fatalf("APIWriters() error = %v", err)
	}
	if usage.Scanned != 1 {
		t.Errorf("scanned = %d, want 1: the limit was 1", usage.Scanned)
	}
	if !usage.Truncated {
		t.Error("truncated = false, want true: an object was left unscanned")
	}
}

// A cluster-scoped object carries no namespace, and a namespaced one carries
// its own — both must reach the writer unchanged.
func TestAPIWritersCarriesTheRightNamespaceForBothScopes(t *testing.T) {
	t.Parallel()

	namespaced := newPartialObject(widgetKind.GroupVersion(), widgetKind.Kind, "team-a", "namespaced-thing",
		metav1.ManagedFieldsEntry{Manager: "helm", APIVersion: widgetKind.GroupVersion()})
	clusterScoped := newPartialObject(widgetKind.GroupVersion(), widgetKind.Kind, "", "cluster-thing",
		metav1.ManagedFieldsEntry{Manager: "helm", APIVersion: widgetKind.GroupVersion()})
	adapter := newUpgradeTestAdapter("dev", newFakeDiscovery(), newFakeMetadataClient(t, namespaced, clusterScoped))

	usage, err := adapter.APIWriters(context.Background(), "dev", widgetKind, 100)
	if err != nil {
		t.Fatalf("APIWriters() error = %v", err)
	}

	byName := make(map[string]domain.APIWriter, len(usage.Writers))
	for _, w := range usage.Writers {
		byName[w.Name] = w
	}
	if got := byName["namespaced-thing"].Namespace; got != "team-a" {
		t.Errorf("namespaced object's writer namespace = %q, want %q", got, "team-a")
	}
	if got := byName["cluster-thing"].Namespace; got != "" {
		t.Errorf("cluster-scoped object's writer namespace = %q, want empty", got)
	}
}

// A second call within the TTL must not hit the fake at all — that is the
// whole point of caching a question whose answer moves in minutes, not
// seconds.
func TestAPIWritersIsCachedWithinTheTTL(t *testing.T) {
	t.Parallel()

	obj := newPartialObject(widgetKind.GroupVersion(), widgetKind.Kind, "team-a", "thing-1",
		metav1.ManagedFieldsEntry{Manager: "helm", APIVersion: widgetKind.GroupVersion()})
	meta := newFakeMetadataClient(t, obj)
	adapter := newUpgradeTestAdapter("dev", newFakeDiscovery(), meta)

	if _, err := adapter.APIWriters(context.Background(), "dev", widgetKind, 100); err != nil {
		t.Fatalf("APIWriters() error = %v", err)
	}
	after := len(meta.Actions())

	if _, err := adapter.APIWriters(context.Background(), "dev", widgetKind, 100); err != nil {
		t.Fatalf("APIWriters() error = %v", err)
	}
	if got := len(meta.Actions()); got != after {
		t.Errorf("actions after second call = %d, want %d: it should have been served from cache", got, after)
	}
}

// Invalidate must drop the cache, so the next call reaches the cluster
// again — the same discipline every other cache on the adapter follows.
func TestInvalidateMakesTheNextAPIWritersCallHitTheCluster(t *testing.T) {
	t.Parallel()

	obj := newPartialObject(widgetKind.GroupVersion(), widgetKind.Kind, "team-a", "thing-1",
		metav1.ManagedFieldsEntry{Manager: "helm", APIVersion: widgetKind.GroupVersion()})
	meta := newFakeMetadataClient(t, obj)
	adapter := newUpgradeTestAdapter("dev", newFakeDiscovery(), meta)

	if _, err := adapter.APIWriters(context.Background(), "dev", widgetKind, 100); err != nil {
		t.Fatalf("APIWriters() error = %v", err)
	}
	after := len(meta.Actions())

	// upgrades.forget is exercised directly rather than through
	// Adapter.Invalidate: Invalidate also tears down the watch manager,
	// which this minimal test adapter (like newTestAdapterApply) never
	// builds — the cache-dropping behaviour Invalidate wires in is what is
	// under test here, not the rest of what a real disconnect does.
	adapter.upgrades.forget("dev")

	if _, err := adapter.APIWriters(context.Background(), "dev", widgetKind, 100); err != nil {
		t.Fatalf("APIWriters() error = %v", err)
	}
	if got := len(meta.Actions()); got <= after {
		t.Errorf("actions after Invalidate = %d, want more than %d: it should have hit the cluster again", got, after)
	}
}

// The adapter's whole job here is to REPORT the fact, never to decide what
// it means — that judgement belongs to domain.operatorWriters, which is
// tested separately. This only asserts the annotation is read correctly.
func TestAPIWritersMarksControlPlaneMaintainedObjects(t *testing.T) {
	t.Parallel()

	maintained := &metav1.PartialObjectMetadata{
		TypeMeta: metav1.TypeMeta{APIVersion: widgetKind.GroupVersion(), Kind: widgetKind.Kind},
		ObjectMeta: metav1.ObjectMeta{
			Name:        "maintained",
			Annotations: map[string]string{"apf.kubernetes.io/autoupdate-spec": "true"},
			ManagedFields: []metav1.ManagedFieldsEntry{
				{Manager: "api-priority-and-fairness-config-producer-v1", APIVersion: widgetKind.GroupVersion()},
			},
		},
	}
	falseAnnotation := &metav1.PartialObjectMetadata{
		TypeMeta: metav1.TypeMeta{APIVersion: widgetKind.GroupVersion(), Kind: widgetKind.Kind},
		ObjectMeta: metav1.ObjectMeta{
			Name:        "false-annotation",
			Annotations: map[string]string{"apf.kubernetes.io/autoupdate-spec": "false"},
			ManagedFields: []metav1.ManagedFieldsEntry{
				{Manager: "helm", APIVersion: widgetKind.GroupVersion()},
			},
		},
	}
	unannotated := newPartialObject(widgetKind.GroupVersion(), widgetKind.Kind, "", "unannotated",
		metav1.ManagedFieldsEntry{Manager: "kubectl-client-side-apply", APIVersion: widgetKind.GroupVersion()})

	adapter := newUpgradeTestAdapter("dev", newFakeDiscovery(), newFakeMetadataClient(t, maintained, falseAnnotation, unannotated))

	usage, err := adapter.APIWriters(context.Background(), "dev", widgetKind, 100)
	if err != nil {
		t.Fatalf("APIWriters() error = %v", err)
	}

	byManager := make(map[string]domain.APIWriter, len(usage.Writers))
	for _, w := range usage.Writers {
		byManager[w.Manager] = w
	}
	if got := byManager["api-priority-and-fairness-config-producer-v1"]; !got.SelfManaged {
		t.Errorf("writer with annotation \"true\" = %+v, want SelfManaged true", got)
	}
	if got := byManager["helm"]; got.SelfManaged {
		t.Errorf("writer with annotation \"false\" = %+v, want SelfManaged false", got)
	}
	if got := byManager["kubectl-client-side-apply"]; got.SelfManaged {
		t.Errorf("writer with no annotation = %+v, want SelfManaged false", got)
	}
}
