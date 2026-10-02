package k8s

import (
	"context"
	"slices"
	"sync"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	clientgotesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	"github.com/podsteer/podsteer/app/domain"
)

func topologyScope(t *testing.T, namespaces ...string) domain.TopologyScope {
	t.Helper()
	scope, err := domain.NewTopologyScope(namespaces, len(namespaces) == 0)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

// topologyAdapter is newTestAdapter with the discovery and dynamic clients
// the Gateway API read needs, and the disabled watch ListPods dereferences.
func topologyAdapter(t *testing.T, client *fake.Clientset, dynamicObjects ...*unstructured.Unstructured) *Adapter {
	t.Helper()
	adapter := applicationAdapter(client)
	gvrToListKind := map[schema.GroupVersionResource]string{
		{Group: gatewayGroup, Version: "v1", Resource: "gateways"}:   "GatewayList",
		{Group: gatewayGroup, Version: "v1", Resource: "httproutes"}: "HTTPRouteList",
	}
	dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrToListKind)
	// Created under an explicit resource: the tracker's own guess pluralises
	// "Gateway" as "gatewaies".
	for _, object := range dynamicObjects {
		for plural, kind := range gatewayKinds {
			if kind == object.GetKind() {
				gvr := schema.GroupVersionResource{Group: gatewayGroup, Version: "v1", Resource: plural}
				if err := dynamicClient.Tracker().Create(gvr, object, object.GetNamespace()); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	set := adapter.factory.clients["dev"]
	set.discovery = client.Discovery()
	set.dynamic = dynamicClient
	return adapter
}

var webTemplate = corev1.PodTemplateSpec{Spec: corev1.PodSpec{
	ServiceAccountName: "web-sa",
	Containers: []corev1.Container{{Name: "app", EnvFrom: []corev1.EnvFromSource{{
		SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "web-secret"}},
	}}}},
}}

func shopObjects() []runtime.Object {
	yes := true
	labels := map[string]string{"app": "web"}
	two := int32(2)
	return []runtime.Object{
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "web", Labels: labels},
			Spec:       appsv1.DeploymentSpec{Replicas: &two, Template: webTemplate},
		},
		&appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "db"},
			Spec:       appsv1.StatefulSetSpec{VolumeClaimTemplates: []corev1.PersistentVolumeClaim{{ObjectMeta: metav1.ObjectMeta{Name: "data"}}}},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "shop", Name: "web-1",
				OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "web", Controller: &yes}},
			},
			// A ReplicaSet carries its Deployment's template; its pods' names
			// come from here.
			Spec: appsv1.ReplicaSetSpec{Template: webTemplate},
		},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Namespace: "shop", Name: "web-1-a", Labels: labels,
			OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "web-1", Controller: &yes}},
		}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "web"}, Spec: corev1.ServiceSpec{Selector: labels}},
		&autoscalingv2.HorizontalPodAutoscaler{
			ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "web"},
			Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
				ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{Kind: "Deployment", Name: "web"}, MaxReplicas: 4,
			},
			Status: autoscalingv2.HorizontalPodAutoscalerStatus{CurrentReplicas: 2},
		},
		&policyv1.PodDisruptionBudget{
			ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "web"},
			Spec:       policyv1.PodDisruptionBudgetSpec{Selector: &metav1.LabelSelector{MatchLabels: labels}},
		},
		&networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "deny"},
			Spec: networkingv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
				{Key: "app", Operator: metav1.LabelSelectorOpExists},
			}}, PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}},
		},
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "web-sa"}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "web-secret"}},
		// Outside the scope.
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "other", Name: "elsewhere"}},
	}
}

func TestTopologySourcesReadsEverySource(t *testing.T) {
	adapter := topologyAdapter(t, fake.NewSimpleClientset(shopObjects()...))

	in, err := adapter.TopologySources(context.Background(), "dev", topologyScope(t, "shop"))
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Unreadable) != 0 {
		t.Fatalf("Unreadable = %v", in.Unreadable)
	}
	kinds := map[string]int{}
	for _, c := range in.Controllers {
		kinds[c.Kind]++
	}
	if kinds["Deployment"] != 1 || kinds["ReplicaSet"] != 1 || kinds["StatefulSet"] != 1 {
		t.Errorf("controllers = %v", kinds)
	}
	if len(in.Pods) != 1 || len(in.Services) != 1 || len(in.Scalers) != 1 || len(in.Budgets) != 1 || len(in.Policies) != 1 {
		t.Errorf("pods %d services %d scalers %d budgets %d policies %d",
			len(in.Pods), len(in.Services), len(in.Scalers), len(in.Budgets), len(in.Policies))
	}
	if !in.ServiceAccountsListed || len(in.ServiceAccounts) != 1 {
		t.Errorf("service accounts = %v listed %v", in.ServiceAccounts, in.ServiceAccountsListed)
	}
	if in.GatewayAPIServed {
		t.Error("discovery serves no Gateway API here")
	}

	graph := domain.NewTopologyGraph(in)
	ids := make(map[string]bool)
	for _, node := range graph.Nodes {
		ids[node.ID] = true
	}
	for _, want := range []string{
		"deployment/shop/web", "replicaset/shop/web-1", "pod/shop/web-1-a", "service/shop/web",
		"horizontalpodautoscaler/shop/web", "poddisruptionbudget/shop/web", "networkpolicy/shop/deny",
		"secret/shop/web-secret", "serviceaccount/shop/web-sa",
	} {
		if !ids[want] {
			t.Errorf("node %q missing", want)
		}
	}
	if ids["service/other/elsewhere"] {
		t.Error("out-of-scope Service drawn")
	}
}

func TestTopologySourcesNeverListsSecrets(t *testing.T) {
	client := fake.NewSimpleClientset(shopObjects()...)
	adapter := topologyAdapter(t, client)

	for _, scope := range []domain.TopologyScope{topologyScope(t, "shop"), topologyScope(t)} {
		if _, err := adapter.TopologySources(context.Background(), "dev", scope); err != nil {
			t.Fatal(err)
		}
	}
	for _, action := range client.Actions() {
		if action.GetResource().Resource == "secrets" {
			t.Errorf("the topology touched Secrets: %s %v", action.GetVerb(), action.GetResource())
		}
	}
}

func TestTopologySourcesNamesEachRefusal(t *testing.T) {
	client := fake.NewSimpleClientset(shopObjects()...)
	refused := []string{
		"deployments", "statefulsets", "daemonsets", "cronjobs", "replicasets", "jobs", "pods",
		"services", "ingresses", "horizontalpodautoscalers", "poddisruptionbudgets", "networkpolicies", "serviceaccounts",
	}
	client.PrependReactor("list", "*", func(action clientgotesting.Action) (bool, runtime.Object, error) {
		resource := action.GetResource().Resource
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: resource}, "", nil)
	})
	adapter := topologyAdapter(t, client)

	in, err := adapter.TopologySources(context.Background(), "dev", topologyScope(t, "shop"))
	if err != nil {
		t.Fatalf("a refusal is never an error: %v", err)
	}
	for _, source := range refused {
		if !slices.Contains(in.Unreadable, domain.TopologySourceIn(source, "shop")) {
			t.Errorf("Unreadable %v does not name %q", in.Unreadable, source)
		}
	}
	if in.ServiceAccountsListed {
		t.Error("a refused ServiceAccount list is not listed")
	}
}

func TestTopologySourcesFallsBackPerNamespaceWhenClusterWideIsRefused(t *testing.T) {
	client := fake.NewSimpleClientset(shopObjects()...)
	client.PrependReactor("list", "services", func(action clientgotesting.Action) (bool, runtime.Object, error) {
		if action.GetNamespace() == "" {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "services"}, "", nil)
		}
		return false, nil, nil
	})
	adapter := topologyAdapter(t, client)

	// Four namespaces: wide enough to list cluster-wide first.
	in, err := adapter.TopologySources(context.Background(), "dev", topologyScope(t, "a", "b", "c", "shop"))
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Services) != 1 || in.Services[0].Name != "web" {
		t.Errorf("services = %v", in.Services)
	}
	if slices.Contains(in.Unreadable, "services") {
		t.Errorf("a refused cluster-wide list with a working fallback is not unreadable: %v", in.Unreadable)
	}

	// For All there is nothing to fall back to.
	in, _ = adapter.TopologySources(context.Background(), "dev", topologyScope(t))
	if !slices.Contains(in.Unreadable, "services") {
		t.Errorf("Unreadable = %v, want services", in.Unreadable)
	}
}

func TestTopologySourcesReadsGatewayAPIWhenServed(t *testing.T) {
	client := fake.NewSimpleClientset(shopObjects()...)
	client.Resources = []*metav1.APIResourceList{{
		GroupVersion: gatewayGroup + "/v1",
		APIResources: []metav1.APIResource{{Name: "gateways", Kind: "Gateway"}, {Name: "httproutes", Kind: "HTTPRoute"}},
	}}
	gateway := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": gatewayGroup + "/v1", "kind": "Gateway",
		"metadata": map[string]any{"namespace": "shop", "name": "public"},
		"spec":     map[string]any{"gatewayClassName": "istio", "listeners": []any{map[string]any{"name": "http"}}},
	}}
	route := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": gatewayGroup + "/v1", "kind": "HTTPRoute",
		"metadata": map[string]any{"namespace": "shop", "name": "web"},
		"spec": map[string]any{
			"hostnames":  []any{"shop.example"},
			"parentRefs": []any{map[string]any{"name": "public"}},
			"rules":      []any{map[string]any{"backendRefs": []any{map[string]any{"name": "web", "port": int64(80)}}}},
		},
	}}
	adapter := topologyAdapter(t, client, gateway, route)

	in, err := adapter.TopologySources(context.Background(), "dev", topologyScope(t, "shop"))
	if err != nil {
		t.Fatal(err)
	}
	if !in.GatewayAPIServed || len(in.Gateways) != 1 || len(in.Routes) != 1 {
		t.Fatalf("served %v gateways %v routes %v unreadable %v", in.GatewayAPIServed, in.Gateways, in.Routes, in.Unreadable)
	}
	got := in.Routes[0]
	if got.Kind != "HTTPRoute" || !slices.Equal(got.Hostnames, []string{"shop.example"}) ||
		!slices.Equal(got.Parents, []domain.ObjectKey{{Kind: "Gateway", Namespace: "shop", Name: "public"}}) ||
		!slices.Equal(got.Backends, []domain.ObjectKey{{Kind: "Service", Namespace: "shop", Name: "web"}}) {
		t.Errorf("route = %+v", got)
	}
	if in.Gateways[0].ClassName != "istio" || in.Gateways[0].Listeners != 1 {
		t.Errorf("gateway = %+v", in.Gateways[0])
	}
}

// recordingSink is a ChangeSink that remembers what it heard.
type recordingSink struct {
	mu    sync.Mutex
	heard []string
}

func (s *recordingSink) Changed(id domain.ClusterID, namespace domain.NamespaceName) {
	s.mu.Lock()
	s.heard = append(s.heard, id.String()+"/"+namespace.String())
	s.mu.Unlock()
}

func (s *recordingSink) got() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.heard)
}

func TestChangeHandlerAnnouncesChangesNotTheInitialList(t *testing.T) {
	sink := &recordingSink{}
	notifier := &changeNotifier{}
	notifier.set(sink)
	handler := changeHandler("dev", notifier)

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "a"}}
	handler.OnAdd(pod, true)
	handler.OnAdd(pod, false)
	handler.OnUpdate(pod, pod)
	handler.OnDelete(cache.DeletedFinalStateUnknown{Key: "batch/b", Obj: nil})

	want := []string{"dev/shop", "dev/shop", "dev/batch"}
	if got := sink.got(); !slices.Equal(got, want) {
		t.Errorf("heard %v, want %v", got, want)
	}
}

func TestForgetReadsAnnouncesAWrite(t *testing.T) {
	adapter := New(Config{}, nil)
	defer adapter.StopAllWatches()
	sink := &recordingSink{}
	adapter.SetChangeSink(sink)

	adapter.forgetReads("dev")
	if got := sink.got(); !slices.Equal(got, []string{"dev/"}) {
		t.Errorf("heard %v", got)
	}
	if adapter.watches.changes != adapter.changes {
		t.Error("the watch stores announce through the adapter's notifier")
	}
}

// TestChangeNotifierRace sets and clears the sink while changes arrive, for
// -race: the composition root sets it once, but nothing may tear.
func TestChangeNotifierRace(t *testing.T) {
	notifier := &changeNotifier{}
	sink := &recordingSink{}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 200 {
				notifier.changed("dev", "shop")
			}
		})
	}
	wg.Go(func() {
		for i := range 200 {
			if i%2 == 0 {
				notifier.set(sink)
			} else {
				notifier.set(nil)
			}
		}
	})
	wg.Wait()

	var nilNotifier *changeNotifier
	nilNotifier.changed("dev", "shop") // a no-op, never a panic
}
