package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/managedfields"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clientgotesting "k8s.io/client-go/testing"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// The three kinds every apply_test.go case draws from: a namespaced
// built-in (Deployment), a cluster-scoped built-in (ClusterRole), and a
// namespaced custom resource (Widget, standing in for any CRD) — the same
// three shapes the task this file covers names explicitly.
var (
	deploymentGVR  = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	clusterRoleGVR = schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}
	widgetGVR      = schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "widgets"}

	gvrToListKind = map[schema.GroupVersionResource]string{
		deploymentGVR:  "DeploymentList",
		clusterRoleGVR: "ClusterRoleList",
		widgetGVR:      "WidgetList",
	}
)

// testRESTMapper builds a hand-populated mapper covering the three kinds
// above, standing in for what a real cluster's discovery would report. Used
// directly (bypassing discovery) by every test except the two exercising the
// refresh-on-NoKindMatch path itself, which supply their own mapperBuilder.
func testRESTMapper() meta.RESTMapper {
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{
		{Group: "apps", Version: "v1"},
		{Group: "rbac.authorization.k8s.io", Version: "v1"},
		{Group: "example.com", Version: "v1"},
	})
	mapper.Add(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}, meta.RESTScopeNamespace)
	mapper.Add(schema.GroupVersionKind{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRole"}, meta.RESTScopeRoot)
	mapper.Add(schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}, meta.RESTScopeNamespace)
	return mapper
}

// newTestAdapterApply returns an Adapter whose dynamic client and RESTMapper
// for id are the ones given, for tests that exercise UpdateResource without
// a real API server or a real discovery-backed mapper.
//
// dynClient takes the dynamic.Interface the fake satisfies, not the
// concrete *FakeDynamicClient, because the fake embeds a mutex — copying it
// by value would be a copylocks violation golangci-lint's govet flags.
func newTestAdapterApply(id domain.ClusterID, dynClient dynamic.Interface, mapper meta.RESTMapper) *Adapter {
	factory := newClientFactory(Config{})
	factory.clients[id] = &clients{dynamic: dynClient, restMapper: mapper}
	return &Adapter{factory: factory, logger: slog.New(slog.DiscardHandler)}
}

// newApplyTestAdapter returns an Adapter whose dynamic client runs the REAL
// field manager, for the tests that exercise ApplyResource.
//
// THE SIMPLE FAKE CANNOT SERVE AN APPLY. NewSimpleDynamicClientWithCustomListKinds
// uses the plain object tracker, whose Apply is a naive merge with no notion
// of ownership — it cannot create an object that does not exist, and it can
// never produce a conflict, so a test written against it would pass while
// proving nothing about the verb under test. NewFieldManagedObjectTracker runs
// apimachinery's own field manager: it creates, it merges, and it returns real
// NewApplyConflict errors with real causes.
//
// The deduced type converter is the limitation to know about: with no schema
// it treats every list as atomic and every map as granular, so keyed-list
// conflicts cannot be exercised offline. That needs a real API server — and
// one was asked, on 2026-09-11, with a server-side dry run against a live
// Deployment. It answers per container and per field:
//
//	.spec.template.spec.containers[name="authentication-identity-service"].image
//
// so a conflict over one container's image names that container rather than
// the whole list. Recorded here because the shape of that path is what the
// conflict dialog renders, and nothing offline can show it.
func newApplyTestAdapter(id domain.ClusterID, dynClient dynamic.Interface, mapper meta.RESTMapper) *Adapter {
	return newTestAdapterApply(id, dynClient, mapper)
}

// applyFakeClient builds a dynamic fake whose tracker manages fields.
func applyFakeClient(t *testing.T, objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	t.Helper()

	scheme := runtime.NewScheme()
	for gvr, kind := range gvrToListKind {
		single := gvr.GroupVersion().WithKind(strings.TrimSuffix(kind, "List"))
		scheme.AddKnownTypeWithName(single, &unstructured.Unstructured{})
		scheme.AddKnownTypeWithName(gvr.GroupVersion().WithKind(kind), &unstructured.UnstructuredList{})
	}

	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, objects...)
	tracker := clientgotesting.NewFieldManagedObjectTracker(
		scheme,
		serializer.NewCodecFactory(scheme).UniversalDecoder(),
		managedfields.NewDeducedTypeConverter(),
	)
	for _, object := range objects {
		if err := tracker.Add(object); err != nil {
			t.Fatalf("seeding the field-managed tracker: %v", err)
		}
	}
	client.PrependReactor("*", "*", clientgotesting.ObjectReaction(tracker))
	return client
}

// newSeedObject builds an unstructured object for pre-populating the fake
// dynamic client, the way an existing cluster object would look before an
// apply reaches it.
func newSeedObject(apiVersion, kind, namespace, name, resourceVersion string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{}}
	obj.SetAPIVersion(apiVersion)
	obj.SetKind(kind)
	if namespace != "" {
		obj.SetNamespace(namespace)
	}
	obj.SetName(name)
	if resourceVersion != "" {
		obj.SetResourceVersion(resourceVersion)
	}
	return obj
}

func TestApplyResourceCreatesWhenAbsent(t *testing.T) {
	dynClient := applyFakeClient(t)
	adapter := newApplyTestAdapter("dev", dynClient, testRESTMapper())

	manifest := "apiVersion: apps/v1\n" +
		"kind: Deployment\n" +
		"metadata:\n" +
		"  name: web\n" +
		"  namespace: default\n" +
		"spec:\n" +
		"  replicas: 3\n"

	outcome, err := adapter.ApplyResource(context.Background(), "dev", manifest, domain.ApplyOptions{DryRun: false})
	if err != nil {
		t.Fatalf("ApplyResource() error = %v", err)
	}
	if !outcome.Created {
		t.Error("outcome.Created = false, want true — the object did not exist")
	}
	if outcome.Kind != "Deployment" || outcome.Name != "web" || outcome.Namespace != "default" {
		t.Errorf("outcome = %+v, want Kind=Deployment Name=web Namespace=default", outcome)
	}
	if outcome.DryRun {
		t.Error("outcome.DryRun = true, want false — this was a real apply")
	}

	stored, err := dynClient.Resource(deploymentGVR).Namespace("default").Get(context.Background(), "web", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting created object: %v", err)
	}
	replicas, found, err := unstructured.NestedInt64(stored.Object, "spec", "replicas")
	if err != nil || !found || replicas != 3 {
		t.Errorf("stored spec.replicas = %v, found = %v, err = %v, want 3, true, nil", replicas, found, err)
	}
}

func TestUpdateResourceUpdatesWithAMatchingResourceVersion(t *testing.T) {
	existing := newSeedObject("apps/v1", "Deployment", "default", "web", "10")
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind, existing)
	adapter := newTestAdapterApply("dev", dynClient, testRESTMapper())

	manifest := "apiVersion: apps/v1\n" +
		"kind: Deployment\n" +
		"metadata:\n" +
		"  name: web\n" +
		"  namespace: default\n" +
		"  resourceVersion: \"10\"\n" +
		"spec:\n" +
		"  replicas: 5\n"

	outcome, err := adapter.UpdateResource(context.Background(), "dev", manifest, false)
	if err != nil {
		t.Fatalf("UpdateResource() error = %v", err)
	}
	if outcome.Created {
		t.Error("outcome.Created = true, want false — the object already existed")
	}

	// A resourceVersion present in the manifest must have taken the PUT
	// path, not Create — proven by checking the action verb reaching the
	// fake rather than only the end state, which a Create-then-conflict
	// fallback could also have produced.
	found := false
	for _, action := range dynClient.Actions() {
		if action.GetVerb() == "update" && action.GetResource().Resource == "deployments" {
			found = true
		}
		if action.GetVerb() == "create" {
			t.Errorf("unexpected create action %+v — a manifest carrying resourceVersion must PUT, not create", action)
		}
	}
	if !found {
		t.Error("no update action reached the fake client")
	}

	stored, err := dynClient.Resource(deploymentGVR).Namespace("default").Get(context.Background(), "web", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting updated object: %v", err)
	}
	replicas, _, _ := unstructured.NestedInt64(stored.Object, "spec", "replicas")
	if replicas != 5 {
		t.Errorf("stored spec.replicas = %d, want 5 — the update must have replaced the spec", replicas)
	}
}

func TestUpdateResourceOnAStaleResourceVersionIsAConflict(t *testing.T) {
	existing := newSeedObject("apps/v1", "Deployment", "default", "web", "10")
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind, existing)
	dynClient.PrependReactor("update", "deployments", func(clientgotesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewConflict(
			schema.GroupResource{Group: "apps", Resource: "deployments"}, "web", errors.New("stale resourceVersion"))
	})
	adapter := newTestAdapterApply("dev", dynClient, testRESTMapper())

	manifest := "apiVersion: apps/v1\n" +
		"kind: Deployment\n" +
		"metadata:\n" +
		"  name: web\n" +
		"  namespace: default\n" +
		"  resourceVersion: \"1\"\n" // stale on purpose

	_, err := adapter.UpdateResource(context.Background(), "dev", manifest, false)
	if !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("UpdateResource() error = %v, want wrapping ports.ErrConflict", err)
	}
}

// TestUpdateResourceRefusesAManifestWithNoResourceVersion replaces a test
// that pinned a data-loss bug.
//
// WHAT IT USED TO ASSERT. A manifest with no resourceVersion fell back to
// Create, and on AlreadyExists the adapter fetched the live object solely to
// steal its resourceVersion and then REPLACED it. The old test seeded a
// Deployment at replicas 1, applied a manifest saying 9, and asserted the
// object became 9 — which is true, and is also how pasting a manifest that
// omits spec.replicas over an HPA-scaled Deployment silently reset the replica
// count and deleted every field the paste did not mention.
//
// The editor's verb now refuses it. Declared intent goes to ApplyResource,
// which merges and names the owner of anything it cannot change.
func TestUpdateResourceRefusesAManifestWithNoResourceVersion(t *testing.T) {
	existing := newSeedObject("apps/v1", "Deployment", "default", "web", "10")
	if err := unstructured.SetNestedField(existing.Object, int64(1), "spec", "replicas"); err != nil {
		t.Fatalf("seeding existing object: %v", err)
	}
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind, existing)
	adapter := newTestAdapterApply("dev", dynClient, testRESTMapper())

	manifest := "apiVersion: apps/v1\n" +
		"kind: Deployment\n" +
		"metadata:\n" +
		"  name: web\n" +
		"  namespace: default\n" +
		"spec:\n" +
		"  replicas: 9\n"

	_, err := adapter.UpdateResource(context.Background(), "dev", manifest, false)
	if err == nil {
		t.Fatal("UpdateResource() accepted a manifest with no resourceVersion — it would replace the object whole")
	}
	if !errors.Is(err, domain.ErrInvalidManifest) {
		t.Errorf("error = %v, want ErrInvalidManifest", err)
	}

	stored, getErr := dynClient.Resource(deploymentGVR).Namespace("default").Get(context.Background(), "web", metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("getting the object back: %v", getErr)
	}
	replicas, _, _ := unstructured.NestedInt64(stored.Object, "spec", "replicas")
	if replicas != 1 {
		t.Errorf("spec.replicas = %d, want 1 — the refused edit must not have written anything", replicas)
	}
}

// TestApplyResourceMergesRatherThanReplaces is the other half, and the reason
// the refusal above is safe: the paste path still works, and now it works the
// way `kubectl apply` does.
//
// A manifest that names the image and nothing else must leave the replica
// count and the labels somebody else set exactly as they were. Under the old
// create-then-replace fallback, all of it was deleted.
func TestApplyResourceMergesRatherThanReplaces(t *testing.T) {
	existing := newSeedObject("apps/v1", "Deployment", "default", "web", "10")
	existing.SetLabels(map[string]string{"team": "payments"})
	if err := unstructured.SetNestedField(existing.Object, int64(10), "spec", "replicas"); err != nil {
		t.Fatalf("seeding existing object: %v", err)
	}

	dynClient := applyFakeClient(t, existing)
	adapter := newApplyTestAdapter("dev", dynClient, testRESTMapper())

	// Names the image. Says nothing about replicas or labels.
	manifest := "apiVersion: apps/v1\n" +
		"kind: Deployment\n" +
		"metadata:\n" +
		"  name: web\n" +
		"  namespace: default\n" +
		"spec:\n" +
		"  template:\n" +
		"    spec:\n" +
		"      containers:\n" +
		"      - name: app\n" +
		"        image: nginx:1.27\n"

	outcome, err := adapter.ApplyResource(context.Background(), "dev", manifest, domain.ApplyOptions{})
	if err != nil {
		t.Fatalf("ApplyResource() error = %v", err)
	}
	if outcome.Created {
		t.Error("outcome.Created = true for an object that already existed")
	}

	stored, err := dynClient.Resource(deploymentGVR).Namespace("default").Get(context.Background(), "web", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting the applied object: %v", err)
	}

	replicas, found, _ := unstructured.NestedInt64(stored.Object, "spec", "replicas")
	if !found || replicas != 10 {
		t.Errorf("spec.replicas = %d (found %v), want 10 — the manifest said nothing about it", replicas, found)
	}
	if team := stored.GetLabels()["team"]; team != "payments" {
		t.Errorf("labels[team] = %q, want payments — the manifest said nothing about it", team)
	}
}

func TestApplyResourceAppliesACustomResource(t *testing.T) {
	dynClient := applyFakeClient(t)
	adapter := newApplyTestAdapter("dev", dynClient, testRESTMapper())

	manifest := "apiVersion: example.com/v1\n" +
		"kind: Widget\n" +
		"metadata:\n" +
		"  name: gizmo\n" +
		"  namespace: default\n" +
		"spec:\n" +
		"  color: blue\n"

	outcome, err := adapter.ApplyResource(context.Background(), "dev", manifest, domain.ApplyOptions{DryRun: false})
	if err != nil {
		t.Fatalf("ApplyResource() error = %v, want a CRD kind to apply through the generic dynamic path", err)
	}
	if !outcome.Created || outcome.Kind != "Widget" {
		t.Errorf("outcome = %+v, want Created=true Kind=Widget", outcome)
	}

	if _, err := dynClient.Resource(widgetGVR).Namespace("default").Get(context.Background(), "gizmo", metav1.GetOptions{}); err != nil {
		t.Fatalf("getting created widget: %v", err)
	}
}

func TestApplyResourceOnAnUnknownKindRefusesAfterOneMapperRefresh(t *testing.T) {
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), nil)

	factory := newClientFactory(Config{})
	calls := 0
	factory.mapperBuilder = func(discovery.DiscoveryInterface) (meta.RESTMapper, error) {
		calls++
		// A cluster that genuinely never serves "Ghost" — refreshing
		// discovery does not change the outcome, but it must still be TRIED
		// exactly once before giving up, because the caller cannot
		// distinguish "never existed" from "installed a moment ago"
		// without asking.
		return meta.NewDefaultRESTMapper(nil), nil
	}
	factory.clients["dev"] = &clients{dynamic: dynClient}
	adapter := &Adapter{factory: factory, logger: slog.New(slog.DiscardHandler)}

	manifest := "apiVersion: example.com/v1\n" +
		"kind: Ghost\n" +
		"metadata:\n" +
		"  name: spooky\n" +
		"  namespace: default\n"

	_, err := adapter.ApplyResource(context.Background(), "dev", manifest, domain.ApplyOptions{DryRun: false})
	if !errors.Is(err, domain.ErrInvalidManifest) {
		t.Fatalf("ApplyResource() error = %v, want wrapping domain.ErrInvalidManifest", err)
	}
	if calls != 2 {
		t.Fatalf("mapperBuilder called %d times, want exactly 2 (the initial build plus one refresh)", calls)
	}
}

func TestApplyResourceOnACRDInstalledAfterTheMapperWasCachedAppliesAfterOneRefresh(t *testing.T) {
	dynClient := applyFakeClient(t)

	factory := newClientFactory(Config{})
	calls := 0
	factory.mapperBuilder = func(discovery.DiscoveryInterface) (meta.RESTMapper, error) {
		calls++
		if calls == 1 {
			// The mapper as it looked before the CRD existed.
			return meta.NewDefaultRESTMapper(nil), nil
		}
		// The mapper as it looks once discovery is asked again — the CRD is
		// now visible, standing in for one installed a minute ago.
		return testRESTMapper(), nil
	}
	factory.clients["dev"] = &clients{dynamic: dynClient}
	adapter := &Adapter{factory: factory, logger: slog.New(slog.DiscardHandler)}

	manifest := "apiVersion: example.com/v1\n" +
		"kind: Widget\n" +
		"metadata:\n" +
		"  name: gizmo\n" +
		"  namespace: default\n"

	outcome, err := adapter.ApplyResource(context.Background(), "dev", manifest, domain.ApplyOptions{DryRun: false})
	if err != nil {
		t.Fatalf("ApplyResource() error = %v, want the CRD to apply after one refresh", err)
	}
	if !outcome.Created {
		t.Error("outcome.Created = false, want true")
	}
	if calls != 2 {
		t.Fatalf("mapperBuilder called %d times, want exactly 2", calls)
	}
}

// TestApplyResourceStripsManagedFieldsBeforeWriting asserts on the REQUEST,
// not on the stored object.
//
// Under apply the server writes managedFields itself — that is the whole
// mechanism — so a stored object legitimately carries them and their presence
// proves nothing. What must not happen is the manifest's OWN managedFields
// travelling: an operator with the managed-fields toggle on copies a block
// claiming kubectl owns everything, and sending it would be PodSteer asserting
// somebody else's ownership on their behalf.
func TestApplyResourceStripsManagedFieldsBeforeWriting(t *testing.T) {
	dynClient := applyFakeClient(t)

	var sent []any
	dynClient.PrependReactor("patch", "deployments", func(action clientgotesting.Action) (bool, runtime.Object, error) {
		patch := action.(clientgotesting.PatchActionImpl)
		var body map[string]any
		if err := json.Unmarshal(patch.GetPatch(), &body); err != nil {
			t.Fatalf("decoding the apply body: %v", err)
		}
		if metadata, ok := body["metadata"].(map[string]any); ok {
			if fields, found := metadata["managedFields"]; found {
				sent = append(sent, fields)
			}
		}
		return false, nil, nil
	})
	adapter := newApplyTestAdapter("dev", dynClient, testRESTMapper())

	manifest := "apiVersion: apps/v1\n" +
		"kind: Deployment\n" +
		"metadata:\n" +
		"  name: web\n" +
		"  namespace: default\n" +
		"  managedFields:\n" +
		"  - manager: kubectl\n" +
		"    operation: Update\n" +
		"spec:\n" +
		"  replicas: 1\n"

	if _, err := adapter.ApplyResource(context.Background(), "dev", manifest, domain.ApplyOptions{DryRun: false}); err != nil {
		t.Fatalf("ApplyResource() error = %v", err)
	}

	if len(sent) != 0 {
		t.Errorf("the apply body carried metadata.managedFields %v — the manifest's own block was sent", sent)
	}
}

// TestApplyResourceDryRunSendsTheOption asserts what left the client.
//
// NOT "AND STORES NOTHING", which is what this used to claim. The fake
// tracker has no notion of a dry run — it persists whatever the reactor hands
// back — so asserting that nothing was stored would be asserting a property of
// the fake rather than of PodSteer. Whether the server honours DryRun=All is
// the server's contract; whether PodSteer asks for it is ours, and that is
// what is checked here.
func TestApplyResourceDryRunSendsTheOption(t *testing.T) {
	dynClient := applyFakeClient(t)

	var sawDryRun []string
	dynClient.PrependReactor("patch", "deployments", func(action clientgotesting.Action) (bool, runtime.Object, error) {
		sawDryRun = action.(clientgotesting.PatchActionImpl).PatchOptions.DryRun
		return false, nil, nil
	})
	adapter := newApplyTestAdapter("dev", dynClient, testRESTMapper())

	manifest := "apiVersion: apps/v1\n" +
		"kind: Deployment\n" +
		"metadata:\n" +
		"  name: web\n" +
		"  namespace: default\n" +
		"spec:\n" +
		"  replicas: 1\n"

	outcome, err := adapter.ApplyResource(context.Background(), "dev", manifest, domain.ApplyOptions{DryRun: true})
	if err != nil {
		t.Fatalf("ApplyResource(dryRun) error = %v", err)
	}
	if !outcome.DryRun {
		t.Error("outcome.DryRun = false, want true")
	}
	if len(sawDryRun) != 1 || sawDryRun[0] != metav1.DryRunAll {
		t.Errorf("apply options DryRun = %v, want [%q]", sawDryRun, metav1.DryRunAll)
	}
}

func TestUpdateResourceRefusesAMultiDocumentManifest(t *testing.T) {
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind)
	adapter := newTestAdapterApply("dev", dynClient, testRESTMapper())

	manifest := "apiVersion: apps/v1\n" +
		"kind: Deployment\n" +
		"metadata:\n" +
		"  name: web\n" +
		"  namespace: default\n" +
		"---\n" +
		"apiVersion: v1\n" +
		"kind: ConfigMap\n" +
		"metadata:\n" +
		"  name: settings\n" +
		"  namespace: default\n"

	_, err := adapter.UpdateResource(context.Background(), "dev", manifest, false)
	if !errors.Is(err, domain.ErrInvalidManifest) {
		t.Fatalf("UpdateResource() error = %v, want wrapping domain.ErrInvalidManifest", err)
	}
	if actions := dynClient.Actions(); len(actions) != 0 {
		t.Errorf("dynClient recorded %d actions, want 0 — refused before any request reached the cluster", len(actions))
	}
}

func TestUpdateResourceRefusesANamespacedKindWithNoNamespace(t *testing.T) {
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind)
	adapter := newTestAdapterApply("dev", dynClient, testRESTMapper())

	manifest := "apiVersion: apps/v1\n" +
		"kind: Deployment\n" +
		"metadata:\n" +
		"  name: web\n" // no namespace

	_, err := adapter.UpdateResource(context.Background(), "dev", manifest, false)
	if !errors.Is(err, domain.ErrInvalidManifest) {
		t.Fatalf("UpdateResource() error = %v, want wrapping domain.ErrInvalidManifest", err)
	}
	if actions := dynClient.Actions(); len(actions) != 0 {
		t.Errorf("dynClient recorded %d actions, want 0 — refused before guessing a namespace", len(actions))
	}
}

func TestApplyResourceOnAClusterScopedKindIgnoresANamespace(t *testing.T) {
	dynClient := applyFakeClient(t)
	adapter := newApplyTestAdapter("dev", dynClient, testRESTMapper())

	// A cluster-scoped kind carrying a namespace anyway — copy/paste from a
	// namespaced object, or a stale field left over from an edit.
	manifest := "apiVersion: rbac.authorization.k8s.io/v1\n" +
		"kind: ClusterRole\n" +
		"metadata:\n" +
		"  name: viewer\n" +
		"  namespace: default\n" +
		"rules: []\n"

	outcome, err := adapter.ApplyResource(context.Background(), "dev", manifest, domain.ApplyOptions{DryRun: false})
	if err != nil {
		t.Fatalf("ApplyResource() error = %v, want a cluster-scoped kind to ignore a stray namespace rather than refuse", err)
	}
	if outcome.Namespace != "" {
		t.Errorf("outcome.Namespace = %q, want empty for a cluster-scoped kind", outcome.Namespace)
	}

	stored, err := dynClient.Resource(clusterRoleGVR).Get(context.Background(), "viewer", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting created cluster role: %v", err)
	}
	if stored.GetNamespace() != "" {
		t.Errorf("stored object namespace = %q, want empty", stored.GetNamespace())
	}
}

// TestApplyResourceReportsWhoOwnsAConflictingFieldAndWritesNothing is what
// this verb exists for.
//
// The old create-then-replace fallback would have taken the field and told
// nobody. Server-side apply refuses, names the manager, and leaves the object
// alone — and that refusal comes back as an OUTCOME rather than an error,
// because a list of fields and owners cannot travel in one sentence.
func TestApplyResourceReportsWhoOwnsAConflictingFieldAndWritesNothing(t *testing.T) {
	existing := newSeedObject("apps/v1", "Deployment", "default", "web", "10")
	dynClient := applyFakeClient(t, existing)
	adapter := newApplyTestAdapter("dev", dynClient, testRESTMapper())

	// Somebody else takes ownership of spec.replicas first.
	theirs := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": "web", "namespace": "default"},
		"spec":       map[string]any{"replicas": int64(10)},
	}}
	if _, err := dynClient.Resource(deploymentGVR).Namespace("default").
		Apply(context.Background(), "web", theirs, metav1.ApplyOptions{FieldManager: "argocd-controller"}); err != nil {
		t.Fatalf("seeding the other manager's ownership: %v", err)
	}

	manifest := "apiVersion: apps/v1\n" +
		"kind: Deployment\n" +
		"metadata:\n" +
		"  name: web\n" +
		"  namespace: default\n" +
		"spec:\n" +
		"  replicas: 3\n"

	outcome, err := adapter.ApplyResource(context.Background(), "dev", manifest, domain.ApplyOptions{})
	if err != nil {
		t.Fatalf("ApplyResource() error = %v — a conflict is an outcome, not an error", err)
	}
	if !outcome.Refused() {
		t.Fatal("outcome.Refused() = false, want the apply turned away over ownership")
	}
	if len(outcome.Conflicts) != 1 {
		t.Fatalf("conflicts = %+v, want exactly one", outcome.Conflicts)
	}

	conflict := outcome.Conflicts[0]
	if conflict.Manager != "argocd-controller" {
		t.Errorf("manager = %q, want argocd-controller", conflict.Manager)
	}
	if conflict.Kind != domain.ManagerGitOps {
		t.Errorf("kind = %q, want gitops — the sentence for a reconciler differs from kubectl's", conflict.Kind)
	}
	if !strings.Contains(conflict.Field, "replicas") {
		t.Errorf("field = %q, want it to name replicas", conflict.Field)
	}

	// AND NOTHING WAS WRITTEN. This is the half that distinguishes a refusal
	// from a failed write: the object must be exactly as it was.
	stored, err := dynClient.Resource(deploymentGVR).Namespace("default").
		Get(context.Background(), "web", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting the object back: %v", err)
	}
	replicas, _, _ := unstructured.NestedInt64(stored.Object, "spec", "replicas")
	if replicas != 10 {
		t.Errorf("spec.replicas = %d, want 10 — a refused apply must not have written", replicas)
	}
}

// TestApplyResourceSendsTheFieldManager pins the name written into every
// object PodSteer touches.
//
// It used to be derived from the user agent, which Config.UserAgent can
// override — so an operator changing a diagnostic string silently renamed the
// manager on every object. The name is a durable mark on somebody's cluster
// that outlives the uninstall.
func TestApplyResourceSendsTheFieldManager(t *testing.T) {
	dynClient := applyFakeClient(t)

	var sawManager string
	dynClient.PrependReactor("patch", "deployments", func(action clientgotesting.Action) (bool, runtime.Object, error) {
		sawManager = action.(clientgotesting.PatchActionImpl).PatchOptions.FieldManager
		return false, nil, nil
	})
	adapter := newApplyTestAdapter("dev", dynClient, testRESTMapper())

	manifest := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  namespace: default\n"

	if _, err := adapter.ApplyResource(context.Background(), "dev", manifest, domain.ApplyOptions{}); err != nil {
		t.Fatalf("ApplyResource() error = %v", err)
	}
	if sawManager != fieldManager {
		t.Errorf("fieldManager = %q, want %q", sawManager, fieldManager)
	}
}

// seedOtherManager gives argocd-controller ownership of spec.replicas.
func seedOtherManager(t *testing.T, dynClient dynamic.Interface, replicas int64, manager string) {
	t.Helper()

	theirs := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": "web", "namespace": "default"},
		"spec":       map[string]any{"replicas": replicas},
	}}
	if _, err := dynClient.Resource(deploymentGVR).Namespace("default").
		Apply(context.Background(), "web", theirs, metav1.ApplyOptions{FieldManager: manager}); err != nil {
		t.Fatalf("seeding %s ownership: %v", manager, err)
	}
}

// seedEditorWrite is PodSteer's OWN earlier write, made the way the editor
// makes it: a PUT under the podsteer manager.
//
// The operation is the whole point. Seeding this with Apply would record
// podsteer/Apply, which is the same manager entry the apply verb writes
// under, so nothing would conflict and a test built on it would pass with the
// resolution removed. The split only exists between operations.
func seedEditorWrite(t *testing.T, dynClient dynamic.Interface, replicas int64) {
	t.Helper()

	ours := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": "web", "namespace": "default"},
		"spec":       map[string]any{"replicas": replicas},
	}}
	if _, err := dynClient.Resource(deploymentGVR).Namespace("default").
		Update(context.Background(), ours, metav1.UpdateOptions{FieldManager: fieldManager}); err != nil {
		t.Fatalf("seeding podsteer's editor write: %v", err)
	}
}

const replicaManifest = "apiVersion: apps/v1\n" +
	"kind: Deployment\n" +
	"metadata:\n" +
	"  name: web\n" +
	"  namespace: default\n" +
	"spec:\n" +
	"  replicas: 3\n"

// TestApplyResourceForcedTakesOwnershipOfWhatWasConfirmed is the happy path:
// the operator was shown the owner, agreed, and the cluster still agrees.
func TestApplyResourceForcedTakesOwnershipOfWhatWasConfirmed(t *testing.T) {
	existing := newSeedObject("apps/v1", "Deployment", "default", "web", "10")
	dynClient := applyFakeClient(t, existing)
	seedOtherManager(t, dynClient, 10, "argocd-controller")
	adapter := newApplyTestAdapter("dev", dynClient, testRESTMapper())

	// What the dialog showed, and what the operator agreed to.
	confirmed := domain.FieldConflicts{
		{Field: ".spec.replicas", Manager: "argocd-controller", Kind: domain.ManagerGitOps},
	}

	outcome, err := adapter.ApplyResource(context.Background(), "dev", replicaManifest,
		domain.ApplyOptions{Force: true, Confirmed: confirmed})
	if err != nil {
		t.Fatalf("ApplyResource(force) error = %v", err)
	}
	if outcome.Refused() {
		t.Fatalf("outcome refused with %+v, want the forced write to go through", outcome.Conflicts)
	}

	stored, err := dynClient.Resource(deploymentGVR).Namespace("default").
		Get(context.Background(), "web", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting the object back: %v", err)
	}
	replicas, _, _ := unstructured.NestedInt64(stored.Object, "spec", "replicas")
	if replicas != 3 {
		t.Errorf("spec.replicas = %d, want 3 — the forced apply did not take the field", replicas)
	}
}

// TestApplyResourceForceIsRefusedWhenOwnershipChangedUnderneath is the whole
// reason force is a precondition rather than a retry.
//
// The dialog named one manager. Between the operator reading it and pressing
// the button, a second took a field. Forcing then would override somebody
// they were never shown, so the write does not happen and the NEW set comes
// back for them to read.
func TestApplyResourceForceIsRefusedWhenOwnershipChangedUnderneath(t *testing.T) {
	existing := newSeedObject("apps/v1", "Deployment", "default", "web", "10")
	dynClient := applyFakeClient(t, existing)
	seedOtherManager(t, dynClient, 10, "argocd-controller")
	adapter := newApplyTestAdapter("dev", dynClient, testRESTMapper())

	// The operator confirmed a set naming a DIFFERENT manager — which is what
	// a dialog drawn before argocd-controller took the field would have said.
	stale := domain.FieldConflicts{
		{Field: ".spec.replicas", Manager: "kubectl", Kind: domain.ManagerKubectl},
	}

	outcome, err := adapter.ApplyResource(context.Background(), "dev", replicaManifest,
		domain.ApplyOptions{Force: true, Confirmed: stale})
	if err != nil {
		t.Fatalf("ApplyResource(force) error = %v", err)
	}
	if !outcome.Refused() {
		t.Fatal("the forced apply went through against a manager the operator never confirmed")
	}
	if len(outcome.Conflicts) != 1 || outcome.Conflicts[0].Manager != "argocd-controller" {
		t.Fatalf("conflicts = %+v, want the manager who actually holds it now", outcome.Conflicts)
	}

	stored, err := dynClient.Resource(deploymentGVR).Namespace("default").
		Get(context.Background(), "web", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting the object back: %v", err)
	}
	replicas, _, _ := unstructured.NestedInt64(stored.Object, "spec", "replicas")
	if replicas != 10 {
		t.Errorf("spec.replicas = %d, want 10 — a refused force must not have written", replicas)
	}
}

// TestApplyResourceForceChecksTheClusterRatherThanTrustingTheDialog pins the
// extra round trip, because without it the two tests above would both pass
// while the check did nothing.
func TestApplyResourceForceChecksTheClusterRatherThanTrustingTheDialog(t *testing.T) {
	existing := newSeedObject("apps/v1", "Deployment", "default", "web", "10")
	dynClient := applyFakeClient(t, existing)
	seedOtherManager(t, dynClient, 10, "argocd-controller")

	var dryRuns, writes int
	dynClient.PrependReactor("patch", "deployments", func(action clientgotesting.Action) (bool, runtime.Object, error) {
		patch := action.(clientgotesting.PatchActionImpl)
		if len(patch.PatchOptions.DryRun) > 0 {
			dryRuns++
		} else if patch.PatchOptions.FieldManager == fieldManager {
			writes++
		}
		return false, nil, nil
	})
	adapter := newApplyTestAdapter("dev", dynClient, testRESTMapper())

	confirmed := domain.FieldConflicts{
		{Field: ".spec.replicas", Manager: "argocd-controller", Kind: domain.ManagerGitOps},
	}
	if _, err := adapter.ApplyResource(context.Background(), "dev", replicaManifest,
		domain.ApplyOptions{Force: true, Confirmed: confirmed}); err != nil {
		t.Fatalf("ApplyResource(force) error = %v", err)
	}

	if dryRuns != 1 {
		t.Errorf("dry-run applies = %d, want exactly 1 — the live set must be re-read before forcing", dryRuns)
	}
	if writes != 1 {
		t.Errorf("real writes = %d, want exactly 1", writes)
	}
}

// TestAnInvalidManifestNamesTheFieldThatIsMissing.
//
// The message used to be "apiVersion, kind and metadata.name are required"
// whichever one was absent. The Duplicate dialog seeds a manifest with a good
// apiVersion, a good kind and an empty name — deliberately, because a
// duplicate needs a new one — so the product's own most common invalid
// manifest was answered with a sentence naming two fields that were fine.
func TestAnInvalidManifestNamesTheFieldThatIsMissing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		manifest string
		want     string
		notWant  []string
	}{
		{
			name:     "the Duplicate dialog's own seed, unnamed",
			manifest: "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: \"\"\n  namespace: shop\n",
			want:     "metadata.name is required",
			notWant:  []string{"apiVersion", "kind,"},
		},
		{
			name:     "no kind",
			manifest: "apiVersion: apps/v1\nmetadata:\n  name: web\n",
			want:     "kind is required",
			notWant:  []string{"metadata.name is", "apiVersion,"},
		},
		{
			name:     "genuinely nothing",
			manifest: "metadata:\n  namespace: shop\n",
			want:     "apiVersion, kind, metadata.name are required",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := decodeManifest(test.manifest)
			if err == nil {
				t.Fatal("decodeManifest() accepted an invalid manifest")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %q, want it to contain %q", err, test.want)
			}
			for _, absent := range test.notWant {
				if strings.Contains(err.Error(), absent) {
					t.Errorf("error = %q, names %q which the manifest supplies", err, absent)
				}
			}
		})
	}
}

// TestAnInvalidManifestNeverQuotesTheManifestBack is a Secrets rule with a
// test, not a comment.
//
// Both decode libraries quote the whole document in their error message.
// `UnmarshalJSON` on a manifest with no `kind` reports "Object 'Kind' is
// missing in '{...}'" — the entire object, base64 Secret data included — and
// that error is rendered in the dialog AND passed through apiError, which
// logs it. One mistyped Secret manifest wrote its contents to the log.
//
// PodSteer reads Secrets only on request and never writes their values
// anywhere. An error path that does it by accident is the same breach as
// doing it on purpose, so every decode failure now gets a sentence written
// here rather than the library's.
func TestAnInvalidManifestNeverQuotesTheManifestBack(t *testing.T) {
	t.Parallel()

	const secret = "c3VwZXItc2VjcmV0"

	tests := []struct {
		name     string
		manifest string
	}{
		{
			name:     "no kind",
			manifest: "apiVersion: v1\nmetadata:\n  name: db\ndata:\n  password: " + secret + "\n",
		},
		{
			name:     "not an object at all",
			manifest: "- apiVersion: v1\n  data:\n    password: " + secret + "\n",
		},
		{
			name:     "broken YAML",
			manifest: "apiVersion: v1\nkind: Secret\ndata:\n  password: \"" + secret + "\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := decodeManifest(test.manifest)
			if err == nil {
				t.Fatal("decodeManifest() accepted an invalid manifest")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("the error carries the Secret's data: %q", err)
			}
			// And it is still a manifest problem, not something generic.
			if !errors.Is(err, domain.ErrInvalidManifest) {
				t.Errorf("error = %v, want ErrInvalidManifest", err)
			}
		})
	}
}

// TestDecodeManifestKeepsIntegersAsIntegers is the guard on a mistake I made
// while closing the Secrets leak above.
//
// Replacing UnmarshalJSON with json.Unmarshal into a plain map looks
// equivalent and is not: encoding/json decodes every number as float64, and
// unstructured requires int64. `replicas: 5` became a float nothing
// downstream could read — NestedInt64 returned zero, and the object went to
// the cluster quietly wrong. Only a test that reads a NUMBER back catches it,
// which is why this one exists rather than a comment.
func TestDecodeManifestKeepsIntegersAsIntegers(t *testing.T) {
	t.Parallel()

	obj, err := decodeManifest("apiVersion: apps/v1\nkind: Deployment\n" +
		"metadata:\n  name: web\n  namespace: shop\nspec:\n  replicas: 5\n")
	if err != nil {
		t.Fatalf("decodeManifest() error = %v", err)
	}

	replicas, found, err := unstructured.NestedInt64(obj.Object, "spec", "replicas")
	if err != nil || !found {
		t.Fatalf("NestedInt64() = %d, found %v, err %v — the number is not an int64", replicas, found, err)
	}
	if replicas != 5 {
		t.Errorf("spec.replicas = %d, want 5", replicas)
	}
}

// TestApplyResourceResolvesAConflictWithItsOwnEarlierEdit is increment 3, and
// it is not hypothetical: a live cluster showed `podsteer` co-owning a
// container image beside argocd-controller, because a forced apply had run.
//
// PodSteer writes under one manager NAME but two OPERATIONS — the editor PUTs
// as podsteer/Update, the apply verb applies as podsteer/Apply — and the
// server treats those as two managers. So editing an object and then applying
// a manifest over it asked the operator to take a field from PodSteer.
func TestApplyResourceResolvesAConflictWithItsOwnEarlierEdit(t *testing.T) {
	existing := newSeedObject("apps/v1", "Deployment", "default", "web", "10")
	dynClient := applyFakeClient(t, existing)
	// PodSteer's own earlier write, as the EDITOR makes it: a PUT.
	seedEditorWrite(t, dynClient, 10)
	adapter := newApplyTestAdapter("dev", dynClient, testRESTMapper())

	outcome, err := adapter.ApplyResource(context.Background(), "dev", replicaManifest, domain.ApplyOptions{})
	if err != nil {
		t.Fatalf("ApplyResource() error = %v", err)
	}
	if outcome.Refused() {
		t.Fatalf("asked the operator to take a field from PodSteer: %+v", outcome.Conflicts)
	}

	stored, err := dynClient.Resource(deploymentGVR).Namespace("default").
		Get(context.Background(), "web", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting the object back: %v", err)
	}
	replicas, _, _ := unstructured.NestedInt64(stored.Object, "spec", "replicas")
	if replicas != 3 {
		t.Errorf("spec.replicas = %d, want 3 — the self-conflict was not resolved", replicas)
	}
}

// TestApplyResourceStillAsksWhenOneForeignManagerIsInTheSet is the other half,
// and the more important one.
//
// A live cluster produced exactly this shape: .image owned by BOTH
// argocd-controller and podsteer. An operator must never have a field taken
// from Argo CD because PodSteer happened to own something else in the same
// apply.
func TestApplyResourceStillAsksWhenOneForeignManagerIsInTheSet(t *testing.T) {
	existing := newSeedObject("apps/v1", "Deployment", "default", "web", "10")
	dynClient := applyFakeClient(t, existing)
	seedOtherManager(t, dynClient, 10, "argocd-controller")
	adapter := newApplyTestAdapter("dev", dynClient, testRESTMapper())

	outcome, err := adapter.ApplyResource(context.Background(), "dev", replicaManifest, domain.ApplyOptions{})
	if err != nil {
		t.Fatalf("ApplyResource() error = %v", err)
	}
	if !outcome.Refused() {
		t.Fatal("a field owned by argocd-controller was taken without asking")
	}
	if !outcome.Conflicts.AllOwnedBy(domain.ManagerGitOps) {
		t.Errorf("Conflicts = %+v, want the GitOps owner named", outcome.Conflicts)
	}

	stored, err := dynClient.Resource(deploymentGVR).Namespace("default").
		Get(context.Background(), "web", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting the object back: %v", err)
	}
	replicas, _, _ := unstructured.NestedInt64(stored.Object, "spec", "replicas")
	if replicas != 10 {
		t.Errorf("spec.replicas = %d, want 10 — a refused apply must not have written", replicas)
	}
}

// TestApplyResourceDryRunNeverResolvesASelfConflict — a dry run's promise is
// that nothing is written, and a self-conflict resolution is a write.
func TestApplyResourceDryRunNeverResolvesASelfConflict(t *testing.T) {
	existing := newSeedObject("apps/v1", "Deployment", "default", "web", "10")
	dynClient := applyFakeClient(t, existing)
	seedEditorWrite(t, dynClient, 10)
	adapter := newApplyTestAdapter("dev", dynClient, testRESTMapper())

	outcome, err := adapter.ApplyResource(context.Background(), "dev", replicaManifest,
		domain.ApplyOptions{DryRun: true})
	if err != nil {
		t.Fatalf("ApplyResource(dryRun) error = %v", err)
	}
	if !outcome.Refused() {
		t.Fatal("a dry run resolved a self-conflict — it must report, not act")
	}

	stored, err := dynClient.Resource(deploymentGVR).Namespace("default").
		Get(context.Background(), "web", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting the object back: %v", err)
	}
	replicas, _, _ := unstructured.NestedInt64(stored.Object, "spec", "replicas")
	if replicas != 10 {
		t.Errorf("spec.replicas = %d, want 10 — a dry run wrote", replicas)
	}
}
