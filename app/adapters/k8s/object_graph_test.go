package k8s

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/podsteer/podsteer/app/domain"
)

// The kinds the neighbourhood tests draw from: an Ingress and the Services it
// routes to, plus a Widget standing in for any custom resource — which is the
// case the owner walk has to survive, since nothing here has a Go type for it.
var (
	ingressGVR = schema.GroupVersionResource{
		Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}
	serviceGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}

	graphListKinds = map[schema.GroupVersionResource]string{
		ingressGVR:    "IngressList",
		serviceGVR:    "ServiceList",
		widgetGVR:     "WidgetList",
		deploymentGVR: "DeploymentList",
	}
)

// graphRESTMapper covers the kinds above the way a cluster's discovery would.
func graphRESTMapper() meta.RESTMapper {
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{
		{Group: "networking.k8s.io", Version: "v1"},
		{Group: "", Version: "v1"},
		{Group: "example.com", Version: "v1"},
		{Group: "apps", Version: "v1"},
	})
	mapper.Add(schema.GroupVersionKind{
		Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"}, meta.RESTScopeNamespace)
	mapper.Add(schema.GroupVersionKind{
		Group: "", Version: "v1", Kind: "Service"}, meta.RESTScopeNamespace)
	mapper.Add(schema.GroupVersionKind{
		Group: "example.com", Version: "v1", Kind: "Widget"}, meta.RESTScopeNamespace)
	mapper.Add(schema.GroupVersionKind{
		Group: "apps", Version: "v1", Kind: "Deployment"}, meta.RESTScopeNamespace)
	return mapper
}

// graphWidgetKind is the navigator catalogue entry for the custom resource above.
var graphWidgetKind = domain.ResourceKind{
	Group: "example.com", Version: "v1", Resource: "widgets",
	Kind: "Widget", Namespaced: true,
}

var graphIngressKind = domain.ResourceKind{
	Group: "networking.k8s.io", Version: "v1", Resource: "ingresses",
	Kind: "Ingress", Namespaced: true,
}

// widget builds a custom resource, optionally owned by another one.
func graphWidget(name string, owner *metav1.OwnerReference) *unstructured.Unstructured {
	object := newSeedObject("example.com/v1", "Widget", "shop", name, "1")
	object.SetUID(graphUID(name))
	if owner != nil {
		object.SetOwnerReferences([]metav1.OwnerReference{*owner})
	}
	return object
}

// uidOf gives an object a UID derived from its name, so the cycle guard —
// which keys on UID, the only cluster-unique identifier Kubernetes promises —
// has something stable to work with.
func graphUID(name string) types.UID { return types.UID(name + "-uid") }

// widgetOwner is the ownerReference pointing at another Widget.
func graphWidgetOwner(name string) *metav1.OwnerReference {
	controller := true
	return &metav1.OwnerReference{
		APIVersion: "example.com/v1", Kind: "Widget", Name: name,
		UID: graphUID(name), Controller: &controller,
	}
}

func graphAdapter(t *testing.T, objects ...runtime.Object) *Adapter {
	t.Helper()

	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(), graphListKinds, objects...)
	return newTestAdapterApply("dev", dynClient, graphRESTMapper())
}

func graphRef(kind domain.ResourceKind, name string) domain.ResourceRef {
	return domain.ResourceRef{ClusterID: "dev", Kind: kind, Namespace: "shop", Name: name}
}

func TestObjectGraphSourcesWalksTheOwnerChainUpward(t *testing.T) {
	adapter := graphAdapter(t,
		graphWidget("leaf", graphWidgetOwner("middle")),
		graphWidget("middle", graphWidgetOwner("root")),
		graphWidget("root", nil),
	)

	input, err := adapter.ObjectGraphSources(context.Background(), graphRef(graphWidgetKind, "leaf"))
	if err != nil {
		t.Fatalf("ObjectGraphSources() error = %v", err)
	}

	if len(input.Owners) != 2 {
		t.Fatalf("got %d owners %+v, want middle then root", len(input.Owners), input.Owners)
	}
	if input.Owners[0].Name != "middle" || input.Owners[1].Name != "root" {
		t.Errorf("owners = %+v, want middle nearest first", input.Owners)
	}
	// From the object itself rather than assumed, so a cluster-scoped owner
	// above a namespaced object comes out right.
	if input.Owners[0].Namespace != "shop" {
		t.Errorf("owner namespace = %q, want the one the object reports", input.Owners[0].Namespace)
	}
	if input.Owners[0].Kind != "Widget" {
		t.Errorf("owner kind = %q, want the Kubernetes kind verbatim", input.Owners[0].Kind)
	}
}

// A CYCLE MUST TERMINATE. Kubernetes does not forbid an object owning
// something that owns it back, and a walk that followed one would issue reads
// without limit for as long as the drawer stayed open.
func TestObjectGraphSourcesTerminatesAnOwnerCycle(t *testing.T) {
	adapter := graphAdapter(t,
		graphWidget("left", graphWidgetOwner("right")),
		graphWidget("right", graphWidgetOwner("left")),
	)

	input, err := adapter.ObjectGraphSources(context.Background(), graphRef(graphWidgetKind, "left"))
	if err != nil {
		t.Fatalf("ObjectGraphSources() error = %v", err)
	}

	if len(input.Owners) != 1 {
		t.Fatalf("got %d owners %+v, want the walk to stop at the first repeat",
			len(input.Owners), input.Owners)
	}
	if input.Owners[0].Name != "right" {
		t.Errorf("owner = %q, want right", input.Owners[0].Name)
	}
}

// THE DEPTH CAP HOLDS on a chain longer than it, which is what stops an
// operator's deeply nested controllers turning one drawer open into an
// unbounded run of reads.
func TestObjectGraphSourcesStopsAtTheDepthCap(t *testing.T) {
	names := []string{"one", "two", "three", "four", "five", "six"}

	var objects []runtime.Object
	for i, name := range names {
		var owner *metav1.OwnerReference
		if i+1 < len(names) {
			owner = graphWidgetOwner(names[i+1])
		}
		objects = append(objects, graphWidget(name, owner))
	}

	adapter := graphAdapter(t, objects...)

	input, err := adapter.ObjectGraphSources(context.Background(), graphRef(graphWidgetKind, "one"))
	if err != nil {
		t.Fatalf("ObjectGraphSources() error = %v", err)
	}

	if len(input.Owners) != domain.ObjectOwnerDepth {
		t.Fatalf("got %d owners %+v, want exactly the cap of %d",
			len(input.Owners), input.Owners, domain.ObjectOwnerDepth)
	}
}

// AN OWNER THAT IS NOT THERE IS STILL A REAL ownerReference. The name is on
// the object whether or not what it names still exists, so the chain carries
// it marked rather than dropping it — an object owned by something deleted
// must not read as an object owned by nothing.
func TestObjectGraphSourcesMarksAnOwnerThatIsGone(t *testing.T) {
	adapter := graphAdapter(t, graphWidget("leaf", graphWidgetOwner("vanished")))

	input, err := adapter.ObjectGraphSources(context.Background(), graphRef(graphWidgetKind, "leaf"))
	if err != nil {
		t.Fatalf("ObjectGraphSources() error = %v", err)
	}

	if len(input.Owners) != 1 {
		t.Fatalf("got %d owners %+v, want the one that is gone", len(input.Owners), input.Owners)
	}
	if !input.Owners[0].Missing {
		t.Error("the absent owner is not marked missing")
	}
	if input.Owners[0].Namespace != "shop" {
		t.Errorf("owner namespace = %q, want the subject's", input.Owners[0].Namespace)
	}
}

// ingress builds an Ingress routing to the named services.
func graphIngress(name string, backends ...string) *unstructured.Unstructured {
	paths := make([]any, 0, len(backends))
	for _, backend := range backends {
		paths = append(paths, map[string]any{
			"path":    "/" + backend,
			"backend": map[string]any{"service": map[string]any{"name": backend}},
		})
	}

	object := newSeedObject("networking.k8s.io/v1", "Ingress", "shop", name, "1")
	object.Object["spec"] = map[string]any{
		"rules": []any{map[string]any{"http": map[string]any{"paths": paths}}},
	}
	return object
}

// service builds a Service with no selector, so nothing here lists pods.
func graphService(name string) *unstructured.Unstructured {
	object := newSeedObject("v1", "Service", "shop", name, "1")
	object.Object["spec"] = map[string]any{"type": "ClusterIP"}
	return object
}

// EXISTENCE IS ESTABLISHED, NOT ASSUMED. A reference that resolves to nothing
// is usually the reason somebody opened the map, and a map drawing the broken
// case exactly like the working one answers nothing.
func TestObjectGraphSourcesResolvesWhetherAReferenceExists(t *testing.T) {
	adapter := graphAdapter(t, graphIngress("shop", "web", "gone"), graphService("web"))

	input, err := adapter.ObjectGraphSources(context.Background(), graphRef(graphIngressKind, "shop"))
	if err != nil {
		t.Fatalf("ObjectGraphSources() error = %v", err)
	}

	found := make(map[string]bool, len(input.References))
	for _, ref := range input.References {
		found[ref.Name] = !ref.Missing
	}

	if len(input.References) != 2 {
		t.Fatalf("got %d references %+v, want two", len(input.References), input.References)
	}
	if !found["web"] {
		t.Error("the Service that exists was marked missing")
	}
	if found["gone"] {
		t.Error("the Service that does not exist was not marked missing")
	}
}

// ONE ENTRY PER REFERENCED OBJECT, however many fields name it: an Ingress
// with twelve paths onto one Service must cost one read, not twelve.
func TestObjectGraphSourcesDeduplicatesReferencesBeforeReading(t *testing.T) {
	adapter := graphAdapter(t, graphIngress("shop", "web", "web", "web"), graphService("web"))

	input, err := adapter.ObjectGraphSources(context.Background(), graphRef(graphIngressKind, "shop"))
	if err != nil {
		t.Fatalf("ObjectGraphSources() error = %v", err)
	}

	if len(input.References) != 1 {
		t.Fatalf("got %d references %+v, want one", len(input.References), input.References)
	}
}

// THE REFERENCE BOUND HOLDS, and what it dropped is NAMED. Each reference is a
// GET, so a generated Ingress with a path per tenant is exactly the object
// that must not turn one click into a hundred requests — and an operator
// looking at the truncated map has to be told it is truncated.
func TestObjectGraphSourcesBoundsHowManyReferencesItResolves(t *testing.T) {
	backends := make([]string, 0, graphReferenceLimit+3)
	for i := range graphReferenceLimit + 3 {
		backends = append(backends, fmt.Sprintf("tenant-%02d", i))
	}

	adapter := graphAdapter(t, graphIngress("shop", backends...))

	input, err := adapter.ObjectGraphSources(context.Background(), graphRef(graphIngressKind, "shop"))
	if err != nil {
		t.Fatalf("ObjectGraphSources() error = %v", err)
	}

	if len(input.References) != graphReferenceLimit {
		t.Fatalf("got %d references, want the bound of %d",
			len(input.References), graphReferenceLimit)
	}

	var said bool
	for _, line := range input.Unreadable {
		if strings.Contains(line, "further references") {
			said = true
		}
	}
	if !said {
		t.Errorf("the bound dropped references without saying so: %v", input.Unreadable)
	}
}

// A Service with no selector lists no pods: an empty selector matches nothing,
// so a read to hand the domain a set it is about to reject cannot change the
// answer and is not made.
func TestObjectGraphSourcesReadsNoPodsWithoutASelector(t *testing.T) {
	adapter := graphAdapter(t, graphService("web"))

	kind := domain.ResourceKind{Version: "v1", Resource: "services", Kind: "Service", Namespaced: true}
	input, err := adapter.ObjectGraphSources(context.Background(), graphRef(kind, "web"))
	if err != nil {
		t.Fatalf("ObjectGraphSources() error = %v", err)
	}

	if len(input.Selector) != 0 {
		t.Errorf("selector = %v, want none", input.Selector)
	}
	if len(input.Pods) != 0 {
		t.Errorf("got %d pods, want none read at all", len(input.Pods))
	}
}

// The subject is the one source with no partial answer: without it there is
// nothing to draw a map around, so it fails rather than returning an empty map
// that would read as "this object has no relationships".
func TestObjectGraphSourcesFailsWhenTheSubjectIsGone(t *testing.T) {
	adapter := graphAdapter(t)

	if _, err := adapter.ObjectGraphSources(context.Background(), graphRef(graphWidgetKind, "absent")); err == nil {
		t.Fatal("ObjectGraphSources() error = nil, want a failure naming the missing subject")
	}
}
