package k8s

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
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

func TestUpdateResourceCreatesWhenAbsent(t *testing.T) {
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind)
	adapter := newTestAdapterApply("dev", dynClient, testRESTMapper())

	manifest := "apiVersion: apps/v1\n" +
		"kind: Deployment\n" +
		"metadata:\n" +
		"  name: web\n" +
		"  namespace: default\n" +
		"spec:\n" +
		"  replicas: 3\n"

	outcome, err := adapter.UpdateResource(context.Background(), "dev", manifest, false)
	if err != nil {
		t.Fatalf("UpdateResource() error = %v", err)
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

func TestUpdateResourceWithoutAResourceVersionReplacesAnExistingObject(t *testing.T) {
	existing := newSeedObject("apps/v1", "Deployment", "default", "web", "10")
	if err := unstructured.SetNestedField(existing.Object, int64(1), "spec", "replicas"); err != nil {
		t.Fatalf("seeding existing object: %v", err)
	}
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind, existing)
	adapter := newTestAdapterApply("dev", dynClient, testRESTMapper())

	// A pasted manifest with NO resourceVersion at all — the shape an
	// operator gets from `kubectl get -o yaml --export`-style copy/paste, or
	// from writing one by hand.
	manifest := "apiVersion: apps/v1\n" +
		"kind: Deployment\n" +
		"metadata:\n" +
		"  name: web\n" +
		"  namespace: default\n" +
		"spec:\n" +
		"  replicas: 9\n"

	outcome, err := adapter.UpdateResource(context.Background(), "dev", manifest, false)
	if err != nil {
		t.Fatalf("UpdateResource() error = %v", err)
	}
	if outcome.Created {
		t.Error("outcome.Created = true, want false — an existing object was replaced, not created")
	}

	stored, err := dynClient.Resource(deploymentGVR).Namespace("default").Get(context.Background(), "web", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting replaced object: %v", err)
	}
	replicas, _, _ := unstructured.NestedInt64(stored.Object, "spec", "replicas")
	if replicas != 9 {
		t.Errorf("stored spec.replicas = %d, want 9 — the paste must have replaced the object", replicas)
	}
}

func TestUpdateResourceAppliesACustomResource(t *testing.T) {
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind)
	adapter := newTestAdapterApply("dev", dynClient, testRESTMapper())

	manifest := "apiVersion: example.com/v1\n" +
		"kind: Widget\n" +
		"metadata:\n" +
		"  name: gizmo\n" +
		"  namespace: default\n" +
		"spec:\n" +
		"  color: blue\n"

	outcome, err := adapter.UpdateResource(context.Background(), "dev", manifest, false)
	if err != nil {
		t.Fatalf("UpdateResource() error = %v, want a CRD kind to apply through the generic dynamic path", err)
	}
	if !outcome.Created || outcome.Kind != "Widget" {
		t.Errorf("outcome = %+v, want Created=true Kind=Widget", outcome)
	}

	if _, err := dynClient.Resource(widgetGVR).Namespace("default").Get(context.Background(), "gizmo", metav1.GetOptions{}); err != nil {
		t.Fatalf("getting created widget: %v", err)
	}
}

func TestUpdateResourceOnAnUnknownKindRefusesAfterOneMapperRefresh(t *testing.T) {
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

	_, err := adapter.UpdateResource(context.Background(), "dev", manifest, false)
	if !errors.Is(err, domain.ErrInvalidManifest) {
		t.Fatalf("UpdateResource() error = %v, want wrapping domain.ErrInvalidManifest", err)
	}
	if calls != 2 {
		t.Fatalf("mapperBuilder called %d times, want exactly 2 (the initial build plus one refresh)", calls)
	}
}

func TestUpdateResourceOnACRDInstalledAfterTheMapperWasCachedAppliesAfterOneRefresh(t *testing.T) {
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind)

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

	outcome, err := adapter.UpdateResource(context.Background(), "dev", manifest, false)
	if err != nil {
		t.Fatalf("UpdateResource() error = %v, want the CRD to apply after one refresh", err)
	}
	if !outcome.Created {
		t.Error("outcome.Created = false, want true")
	}
	if calls != 2 {
		t.Fatalf("mapperBuilder called %d times, want exactly 2", calls)
	}
}

func TestUpdateResourceStripsManagedFieldsBeforeWriting(t *testing.T) {
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind)
	adapter := newTestAdapterApply("dev", dynClient, testRESTMapper())

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

	if _, err := adapter.UpdateResource(context.Background(), "dev", manifest, false); err != nil {
		t.Fatalf("UpdateResource() error = %v", err)
	}

	stored, err := dynClient.Resource(deploymentGVR).Namespace("default").Get(context.Background(), "web", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting created object: %v", err)
	}
	if _, found, _ := unstructured.NestedFieldNoCopy(stored.Object, "metadata", "managedFields"); found {
		t.Error("stored object still carries metadata.managedFields, want it stripped before writing")
	}
}

func TestUpdateResourceDryRunSendsTheOptionAndStoresNothing(t *testing.T) {
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind)

	var sawDryRun []string
	// The fake tracker has no notion of dry run — unlike a real API server
	// it persists whatever a Create/Update reactor hands back — so the
	// reactor here does what the API server's admission chain does for a
	// dry-run request: report the options it received, then hand back the
	// object WITHOUT letting the default reactor (which would call the
	// tracker) run.
	dynClient.PrependReactor("create", "deployments", func(action clientgotesting.Action) (bool, runtime.Object, error) {
		create := action.(clientgotesting.CreateActionImpl)
		sawDryRun = create.GetCreateOptions().DryRun
		return true, create.GetObject(), nil
	})
	adapter := newTestAdapterApply("dev", dynClient, testRESTMapper())

	manifest := "apiVersion: apps/v1\n" +
		"kind: Deployment\n" +
		"metadata:\n" +
		"  name: web\n" +
		"  namespace: default\n" +
		"spec:\n" +
		"  replicas: 1\n"

	outcome, err := adapter.UpdateResource(context.Background(), "dev", manifest, true)
	if err != nil {
		t.Fatalf("UpdateResource(dryRun=true) error = %v", err)
	}
	if !outcome.DryRun {
		t.Error("outcome.DryRun = false, want true")
	}
	if len(sawDryRun) != 1 || sawDryRun[0] != metav1.DryRunAll {
		t.Errorf("create options DryRun = %v, want [%q]", sawDryRun, metav1.DryRunAll)
	}

	if _, err := dynClient.Resource(deploymentGVR).Namespace("default").Get(context.Background(), "web", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("Get() after a dry run error = %v, want NotFound — a dry run must persist nothing", err)
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

func TestUpdateResourceOnAClusterScopedKindIgnoresANamespace(t *testing.T) {
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind)
	adapter := newTestAdapterApply("dev", dynClient, testRESTMapper())

	// A cluster-scoped kind carrying a namespace anyway — copy/paste from a
	// namespaced object, or a stale field left over from an edit.
	manifest := "apiVersion: rbac.authorization.k8s.io/v1\n" +
		"kind: ClusterRole\n" +
		"metadata:\n" +
		"  name: viewer\n" +
		"  namespace: default\n" +
		"rules: []\n"

	outcome, err := adapter.UpdateResource(context.Background(), "dev", manifest, false)
	if err != nil {
		t.Fatalf("UpdateResource() error = %v, want a cluster-scoped kind to ignore a stray namespace rather than refuse", err)
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
