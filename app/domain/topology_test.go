package domain_test

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

func topoPod(t testing.TB, ns, name string, labels map[string]string, owner domain.OwnerReference, healthy bool) domain.Pod {
	t.Helper()
	spec := domain.PodSpec{
		Name: name, Namespace: domain.NamespaceName(ns), ClusterID: "dev",
		Phase: domain.PodPhaseRunning, Labels: labels,
		Containers: []domain.Container{{Name: "app", Image: "repo/app:1", Ready: healthy}},
	}
	if !owner.IsZero() {
		spec.Owners = []domain.OwnerReference{owner}
	}
	pod, err := domain.NewPod(spec)
	if err != nil {
		t.Fatalf("building pod %q: %v", name, err)
	}
	return pod
}

func scopeOf(t testing.TB, namespaces ...string) domain.TopologyScope {
	t.Helper()
	scope, err := domain.NewTopologyScope(namespaces, len(namespaces) == 0)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

// checkTopology asserts the invariants every topology holds: every edge joins
// two drawn nodes, ids are unique, and the order is stable.
func checkTopology(t *testing.T, graph domain.TopologyGraph) map[string]domain.TopologyNode {
	t.Helper()
	nodes := make(map[string]domain.TopologyNode, len(graph.Nodes))
	for i, node := range graph.Nodes {
		if _, dup := nodes[node.ID]; dup {
			t.Errorf("node %q drawn twice", node.ID)
		}
		nodes[node.ID] = node
		if i > 0 && graph.Nodes[i-1].ID >= node.ID {
			t.Errorf("nodes not sorted at %q", node.ID)
		}
		if node.State == "" {
			t.Errorf("node %q has no state", node.ID)
		}
	}
	for _, edge := range graph.Edges {
		if _, ok := nodes[edge.From]; !ok {
			t.Errorf("edge %v starts at no node", edge)
		}
		if _, ok := nodes[edge.To]; !ok {
			t.Errorf("edge %v ends at no node", edge)
		}
	}
	return nodes
}

func hasTopoEdge(graph domain.TopologyGraph, from, to string, kind domain.EdgeKind) bool {
	for _, edge := range graph.Edges {
		if edge.From == from && edge.To == to && edge.Kind == kind {
			return true
		}
	}
	return false
}

func edgesFrom(graph domain.TopologyGraph, from string) []domain.TopologyEdge {
	var out []domain.TopologyEdge
	for _, edge := range graph.Edges {
		if edge.From == from {
			out = append(out, edge)
		}
	}
	return out
}

var webLabels = map[string]string{"app": "web"}

func shopInput(t *testing.T) domain.TopologyInput {
	t.Helper()
	config := []domain.AttachedRef{
		{Kind: domain.GraphConfig, Name: "web-config", Via: "environment"},
		{Kind: domain.GraphSecret, Name: "web-tls", Via: "/tls"},
		{Kind: domain.GraphServiceAccount, Name: "web-sa", Via: "runs as"},
	}
	return domain.TopologyInput{
		Scope: scopeOf(t, "shop"),
		Controllers: []domain.TopologyController{
			{Kind: "Deployment", Name: "web", Namespace: "shop", Labels: webLabels, Desired: 2, Ready: 2, Attached: config},
			{Kind: "ReplicaSet", Name: "web-1", Namespace: "shop", Owner: ctl("Deployment", "web"), Desired: 2, Ready: 2, Attached: config},
		},
		Pods: []domain.Pod{
			topoPod(t, "shop", "web-1-a", webLabels, ctl("ReplicaSet", "web-1"), true),
			topoPod(t, "shop", "web-1-b", webLabels, ctl("ReplicaSet", "web-1"), true),
		},
		Services:              []domain.ServiceRef{{Name: "web", Namespace: "shop", Selector: webLabels, Ports: []string{"80"}}},
		Ingresses:             []domain.IngressRef{{Name: "edge", Namespace: "shop", Hosts: []string{"shop.example"}, Backends: []string{"web"}}},
		ServiceAccounts:       []domain.ServiceAccountRef{{Namespace: "shop", Name: "web-sa"}},
		ServiceAccountsListed: true,
	}
}

func TestTopologyDrawsEveryRealRelationship(t *testing.T) {
	graph := domain.NewTopologyGraph(shopInput(t))
	nodes := checkTopology(t, graph)

	deploy, rs := "deployment/shop/web", "replicaset/shop/web-1"
	podA, podB := "pod/shop/web-1-a", "pod/shop/web-1-b"
	svc, ing := "service/shop/web", "ingress/shop/edge"

	for _, want := range []struct {
		from, to string
		kind     domain.EdgeKind
	}{
		{deploy, rs, domain.EdgeOwns},
		{rs, podA, domain.EdgeOwns},
		{rs, podB, domain.EdgeOwns},
		{svc, podA, domain.EdgeSelects},
		{svc, podB, domain.EdgeSelects},
		{ing, svc, domain.EdgeRoutes},
		{podA, "configmap/shop/web-config", domain.EdgeAttaches},
		{podB, "secret/shop/web-tls", domain.EdgeAttaches},
		{podA, "serviceaccount/shop/web-sa", domain.EdgeRunsAs},
	} {
		if !hasTopoEdge(graph, want.from, want.to, want.kind) {
			t.Errorf("missing %s edge %s -> %s", want.kind, want.from, want.to)
		}
	}

	// A controller with pods does not read its template's names; its pods do.
	for _, edge := range edgesFrom(graph, deploy) {
		if edge.Kind == domain.EdgeAttaches || edge.Kind == domain.EdgeRunsAs {
			t.Errorf("controller with pods drew %v", edge)
		}
	}
	// One box per name however many pods read it.
	if got := graph.Counts["ConfigMap"]; got != 1 {
		t.Errorf("Counts[ConfigMap] = %d, want 1", got)
	}
	if nodes[rs].Group != deploy || nodes[podA].Group != rs {
		t.Errorf("groups: rs %q, pod %q", nodes[rs].Group, nodes[podA].Group)
	}
	if nodes[deploy].Labels["app"] != "web" || nodes[podA].Labels != nil || nodes[rs].Labels != nil {
		t.Errorf("labels belong on top-level objects only")
	}
	if nodes[ing].State != domain.StateNeutral || nodes["configmap/shop/web-config"].State != domain.StateNeutral {
		t.Errorf("ingress and template names are neutral")
	}
	if nodes["serviceaccount/shop/web-sa"].Detail == "not found" {
		t.Errorf("a listed ServiceAccount marked not found")
	}
	if graph.Bounded != domain.TopologyBounded {
		t.Errorf("Bounded = %q", graph.Bounded)
	}
	if graph.Counts["Pod"] != 2 || graph.Counts["Deployment"] != 1 || graph.Counts["Service"] != 1 {
		t.Errorf("Counts = %v", graph.Counts)
	}
}

func TestTopologyIsDeterministic(t *testing.T) {
	in := shopInput(t)
	if a, b := domain.NewTopologyGraph(in), domain.NewTopologyGraph(in); !reflect.DeepEqual(a, b) {
		t.Fatal("the same input drew two different graphs")
	}
}

func TestTopologyEmptyServiceSelectorMatchesNothing(t *testing.T) {
	in := shopInput(t)
	in.Services = append(in.Services, domain.ServiceRef{Name: "external", Namespace: "shop", Type: "ExternalName"})
	graph := domain.NewTopologyGraph(in)
	nodes := checkTopology(t, graph)

	if edges := edgesFrom(graph, "service/shop/external"); len(edges) != 0 {
		t.Errorf("empty selector drew %v", edges)
	}
	if got := nodes["service/shop/external"].State; got != domain.StateNeutral {
		t.Errorf("selectorless Service state = %q, want neutral", got)
	}
}

func TestTopologyServiceSelectingNothingWarns(t *testing.T) {
	in := shopInput(t)
	in.Services = append(in.Services, domain.ServiceRef{Name: "stale", Namespace: "shop", Selector: map[string]string{"app": "gone"}})
	nodes := checkTopology(t, domain.NewTopologyGraph(in))
	if got := nodes["service/shop/stale"].State; got != domain.StateWarn {
		t.Errorf("state = %q, want warn", got)
	}
}

func TestTopologyCronJobOwnsJobsNotPods(t *testing.T) {
	in := domain.TopologyInput{
		Scope: scopeOf(t, "batch"),
		Controllers: []domain.TopologyController{
			{Kind: "CronJob", Name: "nightly", Namespace: "batch"},
			{Kind: "Job", Name: "nightly-1", Namespace: "batch", Owner: ctl("CronJob", "nightly"), Desired: 1, Failed: 1},
		},
		Pods: []domain.Pod{topoPod(t, "batch", "nightly-1-x", nil, ctl("Job", "nightly-1"), false)},
	}
	graph := domain.NewTopologyGraph(in)
	nodes := checkTopology(t, graph)

	if !hasTopoEdge(graph, "cronjob/batch/nightly", "job/batch/nightly-1", domain.EdgeOwns) ||
		!hasTopoEdge(graph, "job/batch/nightly-1", "pod/batch/nightly-1-x", domain.EdgeOwns) {
		t.Fatalf("CronJob -> Job -> Pod tier missing: %v", graph.Edges)
	}
	if hasTopoEdge(graph, "cronjob/batch/nightly", "pod/batch/nightly-1-x", domain.EdgeOwns) {
		t.Error("CronJob drawn owning a pod")
	}
	if got := nodes["job/batch/nightly-1"]; got.State != domain.StateBad || got.Kind != domain.GraphReplicaSet {
		t.Errorf("failed owned Job = %+v", got)
	}
	if nodes["cronjob/batch/nightly"].State != domain.StateNeutral {
		t.Error("a schedule is neutral")
	}
}

func TestTopologyZeroPodFallbackIsTopLevelOnly(t *testing.T) {
	cfg := []domain.AttachedRef{{Kind: domain.GraphConfig, Name: "cfg", Via: "environment"}}
	in := domain.TopologyInput{
		Scope: scopeOf(t, "shop"),
		Controllers: []domain.TopologyController{
			{Kind: "Deployment", Name: "idle", Namespace: "shop", Attached: cfg},
			{Kind: "ReplicaSet", Name: "idle-old", Namespace: "shop", Owner: ctl("Deployment", "idle"), Attached: cfg},
		},
	}
	graph := domain.NewTopologyGraph(in)
	checkTopology(t, graph)

	if !hasTopoEdge(graph, "deployment/shop/idle", "configmap/shop/cfg", domain.EdgeAttaches) {
		t.Error("a workload with no pods should declare its template's names")
	}
	if hasTopoEdge(graph, "replicaset/shop/idle-old", "configmap/shop/cfg", domain.EdgeAttaches) {
		t.Error("an owned zero-replica ReplicaSet declares nothing its owner does not")
	}
}

func TestTopologyNetworkPolicySelectsOnly(t *testing.T) {
	in := shopInput(t)
	in.Pods = append(in.Pods, topoPod(t, "shop", "db-0", map[string]string{"app": "db"}, domain.OwnerReference{}, true))
	in.Policies = []domain.PolicyRef{
		{Name: "deny-all", Namespace: "shop", PolicyTypes: []string{"Ingress"}}, // empty selector: every pod
		{Name: "db-only", Namespace: "shop", PodSelector: domain.LabelSelector{MatchLabels: map[string]string{"app": "db"}}},
	}
	graph := domain.NewTopologyGraph(in)
	nodes := checkTopology(t, graph)

	all := edgesFrom(graph, "networkpolicy/shop/deny-all")
	if len(all) != 3 {
		t.Errorf("empty podSelector selected %d pods, want 3", len(all))
	}
	for _, edge := range append(all, edgesFrom(graph, "networkpolicy/shop/db-only")...) {
		if edge.Kind != domain.EdgePolicySelects || edge.Label != "selects" {
			t.Errorf("policy drew %v; a policy only selects", edge)
		}
		if nodes[edge.To].Kind != domain.GraphPod {
			t.Errorf("policy edge to a %s", nodes[edge.To].Kind)
		}
	}
	if len(edgesFrom(graph, "networkpolicy/shop/db-only")) != 1 {
		t.Error("db-only should select one pod")
	}
	if nodes["networkpolicy/shop/deny-all"].State != domain.StateNeutral {
		t.Error("a policy is neutral")
	}
	// The bare pod is top-level and carries its labels.
	if nodes["pod/shop/db-0"].Labels["app"] != "db" {
		t.Error("a pod with no controller is top-level")
	}
}

func TestTopologyCRDOwnerDrawnFromReferenceAlone(t *testing.T) {
	in := domain.TopologyInput{
		Scope: scopeOf(t, "shop"),
		Controllers: []domain.TopologyController{
			{Kind: "ReplicaSet", Name: "canary-1", Namespace: "shop", Owner: ctl("Rollout", "canary"), Desired: 1, Ready: 1},
		},
		Pods: []domain.Pod{
			topoPod(t, "shop", "canary-1-a", nil, ctl("ReplicaSet", "canary-1"), true),
			topoPod(t, "shop", "static-node-a", nil, ctl("Node", "node-a"), true),
		},
	}
	graph := domain.NewTopologyGraph(in)
	nodes := checkTopology(t, graph)

	owner, ok := nodes["rollout/shop/canary"]
	if !ok || owner.Kind != domain.GraphObject || owner.State != domain.StateNeutral || owner.APIKind != "Rollout" {
		t.Fatalf("CRD owner = %+v", owner)
	}
	if !hasTopoEdge(graph, "rollout/shop/canary", "replicaset/shop/canary-1", domain.EdgeOwns) {
		t.Error("CRD owner edge missing")
	}
	if _, ok := nodes["node/node-a"]; !ok {
		t.Error("a mirror pod's Node owner is cluster-scoped")
	}
	if graph.Counts["Rollout"] != 0 {
		t.Error("an unread owner is not counted as read")
	}
}

func TestTopologyScalersBudgetsHealth(t *testing.T) {
	in := shopInput(t)
	in.Scalers = []domain.ScalerRef{
		{Name: "web", Namespace: "shop", Target: domain.ObjectKey{Kind: "Deployment", Name: "web"}, Current: 5, Min: 1, Max: 5},
		{Name: "gone", Namespace: "shop", Target: domain.ObjectKey{Kind: "Deployment", Name: "missing"}, Current: 1, Min: 1, Max: 3},
	}
	in.Budgets = []domain.BudgetRef{
		{Name: "web", Namespace: "shop", Selector: &domain.LabelSelector{MatchLabels: webLabels}, DisruptionsAllowed: 0, DesiredHealthy: 2},
		{Name: "none", Namespace: "shop"}, // nil selector: nothing
		{Name: "all", Namespace: "shop", Selector: &domain.LabelSelector{}, DisruptionsAllowed: 1, DesiredHealthy: 1},
	}
	graph := domain.NewTopologyGraph(in)
	nodes := checkTopology(t, graph)

	if !hasTopoEdge(graph, "horizontalpodautoscaler/shop/web", "deployment/shop/web", domain.EdgeScales) {
		t.Error("scales edge missing")
	}
	if nodes["horizontalpodautoscaler/shop/web"].State != domain.StateWarn {
		t.Error("an HPA at its maximum warns")
	}
	if nodes["horizontalpodautoscaler/shop/gone"].State != domain.StateOK || nodes["deployment/shop/missing"].State != domain.StateNeutral {
		t.Error("an unresolved scale target is a neutral name")
	}
	if nodes["poddisruptionbudget/shop/web"].State != domain.StateWarn {
		t.Error("a budget allowing no disruptions warns")
	}
	if n := len(edgesFrom(graph, "poddisruptionbudget/shop/web")); n != 2 {
		t.Errorf("budget protects %d pods, want 2", n)
	}
	if n := len(edgesFrom(graph, "poddisruptionbudget/shop/none")); n != 0 {
		t.Errorf("a nil selector protected %d pods", n)
	}
	if n := len(edgesFrom(graph, "poddisruptionbudget/shop/all")); n != 2 {
		t.Errorf("an empty selector protected %d pods, want every pod", n)
	}
	if nodes["poddisruptionbudget/shop/all"].State != domain.StateOK {
		t.Error("a budget with room is ok")
	}
}

func TestTopologyGatewayRoutes(t *testing.T) {
	in := shopInput(t)
	in.GatewayAPIServed = true
	in.Gateways = []domain.GatewayRef{{Name: "public", Namespace: "shop", ClassName: "istio"}}
	in.Routes = []domain.RouteRef{{
		Kind: "HTTPRoute", Name: "web", Namespace: "shop",
		Parents: []domain.ObjectKey{
			{Kind: "Gateway", Namespace: "shop", Name: "public"},
			{Kind: "Gateway", Namespace: "infra", Name: "shared"},
		},
		Backends: []domain.ObjectKey{
			{Kind: "Service", Namespace: "shop", Name: "web"},
			{Kind: "Service", Namespace: "shop", Name: "nobody"},
		},
	}}
	graph := domain.NewTopologyGraph(in)
	nodes := checkTopology(t, graph)

	route := "httproute/shop/web"
	if !hasTopoEdge(graph, "gateway/shop/public", route, domain.EdgeRoutes) ||
		!hasTopoEdge(graph, route, "service/shop/web", domain.EdgeRoutes) {
		t.Fatalf("route edges missing: %v", graph.Edges)
	}
	if got := nodes["gateway/infra/shared"].Detail; got != "outside the scope" {
		t.Errorf("out-of-scope gateway detail = %q", got)
	}
	if got := nodes["service/shop/nobody"].Detail; got != "not found" {
		t.Errorf("absent backend detail = %q", got)
	}
	if graph.Counts["Service"] != 1 {
		t.Errorf("an unresolved name was counted: %v", graph.Counts)
	}
}

func TestTopologyScopeFilters(t *testing.T) {
	in := shopInput(t)
	in.Pods = append(in.Pods, topoPod(t, "other", "x", webLabels, domain.OwnerReference{}, true))
	in.Services = append(in.Services, domain.ServiceRef{Name: "web", Namespace: "other", Selector: webLabels})
	graph := domain.NewTopologyGraph(in)
	nodes := checkTopology(t, graph)

	for id := range nodes {
		if node := nodes[id]; node.Namespace == "other" {
			t.Errorf("out-of-scope node %q drawn", id)
		}
	}
	if graph.Counts["Pod"] != 2 {
		t.Errorf("Counts[Pod] = %d, want 2", graph.Counts["Pod"])
	}
}

func TestTopologyUnreadableServicesMarkNamesNotRead(t *testing.T) {
	in := shopInput(t)
	in.Services = nil
	in.Unreadable = []string{domain.TopologySourceIn("services", "shop")}
	graph := domain.NewTopologyGraph(in)
	nodes := checkTopology(t, graph)
	if got := nodes["service/shop/web"].Detail; got != "not read" {
		t.Errorf("detail = %q, want not read", got)
	}
	if !slices.Equal(graph.Unreadable, in.Unreadable) {
		t.Errorf("Unreadable = %v", graph.Unreadable)
	}
}

func TestTopologyMissingServiceAccountIsSaid(t *testing.T) {
	in := shopInput(t)
	in.ServiceAccounts = nil
	nodes := checkTopology(t, domain.NewTopologyGraph(in))
	if got := nodes["serviceaccount/shop/web-sa"]; got.Detail != "not found" || got.State != domain.StateNeutral {
		t.Errorf("sa = %+v", got)
	}
}

// bigInput is n pods over workloads of ten replicas in ten namespaces, each
// workload with a Service, a budget and a policy.
func bigInput(t testing.TB, n int) domain.TopologyInput {
	in := domain.TopologyInput{Scope: domain.TopologyScope{All: true}}
	perWorkload := 10
	for w := 0; w*perWorkload < n; w++ {
		ns := domain.NamespaceName(fmt.Sprintf("ns-%d", w%10))
		name := fmt.Sprintf("app-%d", w)
		labels := map[string]string{"app": name}
		cfg := []domain.AttachedRef{{Kind: domain.GraphConfig, Name: name + "-cfg", Via: "environment"}}
		in.Controllers = append(in.Controllers,
			domain.TopologyController{Kind: "Deployment", Name: name, Namespace: ns, Labels: labels, Desired: 10, Ready: 10, Attached: cfg},
			domain.TopologyController{Kind: "ReplicaSet", Name: name + "-1", Namespace: ns, Owner: ctl("Deployment", name), Desired: 10, Ready: 10, Attached: cfg},
		)
		in.Services = append(in.Services, domain.ServiceRef{Name: name, Namespace: ns.String(), Selector: labels, Ports: []string{"80"}})
		in.Budgets = append(in.Budgets, domain.BudgetRef{Name: name, Namespace: ns, Selector: &domain.LabelSelector{MatchLabels: labels}, DisruptionsAllowed: 1, DesiredHealthy: 9})
		in.Policies = append(in.Policies, domain.PolicyRef{Name: name, Namespace: ns, PodSelector: domain.LabelSelector{MatchLabels: labels}})
		for p := 0; p < perWorkload && w*perWorkload+p < n; p++ {
			in.Pods = append(in.Pods, topoPod(t, ns.String(), fmt.Sprintf("%s-1-%d", name, p), labels, ctl("ReplicaSet", name+"-1"), p != 0))
		}
	}
	return in
}

func TestTopologySummaryTierKeepsCountsComplete(t *testing.T) {
	in := bigInput(t, domain.TopologyPodCap+10)
	graph := domain.NewTopologyGraph(in)
	nodes := checkTopology(t, graph)

	if !graph.Summarised {
		t.Fatal("not summarised above the cap")
	}
	if graph.Counts["Pod"] != domain.TopologyPodCap+10 {
		t.Errorf("Counts[Pod] = %d", graph.Counts["Pod"])
	}
	total := 0
	for _, node := range nodes {
		if node.Kind != domain.GraphPod {
			continue
		}
		if node.PodSummary == nil {
			t.Fatalf("an unfolded pod %q in the summary tier", node.ID)
		}
		total += node.PodSummary.Total
		if node.PodSummary.Unhealthy > 0 && node.State != domain.StateBad {
			t.Errorf("fold %q hides an unwell member", node.ID)
		}
	}
	if total != domain.TopologyPodCap+10 {
		t.Errorf("folds stand for %d pods", total)
	}

	fold := "fold/replicaset/ns-0/app-0-1/pod"
	got := nodes[fold]
	if got.PodSummary == nil || got.PodSummary.Total != 10 || got.PodSummary.Ready != 9 || got.PodSummary.Unhealthy != 1 {
		t.Fatalf("fold = %+v", got)
	}
	// Edges re-pointed to the fold and deduplicated: one each.
	for _, from := range []string{"service/ns-0/app-0", "poddisruptionbudget/ns-0/app-0", "networkpolicy/ns-0/app-0", "replicaset/ns-0/app-0-1"} {
		n := 0
		for _, edge := range graph.Edges {
			if edge.From == from && edge.To == fold {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s -> fold: %d edges, want 1", from, n)
		}
	}
	if !hasTopoEdge(graph, fold, "configmap/ns-0/app-0-cfg", domain.EdgeAttaches) {
		t.Error("the fold reads what its members read")
	}
}

func TestLabelSelectorMatches(t *testing.T) {
	labels := map[string]string{"app": "web", "tier": "front"}
	tests := []struct {
		name     string
		selector domain.LabelSelector
		want     bool
	}{
		{"empty matches all", domain.LabelSelector{}, true},
		{"labels", domain.LabelSelector{MatchLabels: map[string]string{"app": "web"}}, true},
		{"labels miss", domain.LabelSelector{MatchLabels: map[string]string{"app": "db"}}, false},
		{"in", domain.LabelSelector{MatchExpressions: []domain.SelectorRequirement{{Key: "tier", Operator: "In", Values: []string{"front", "back"}}}}, true},
		{"notin", domain.LabelSelector{MatchExpressions: []domain.SelectorRequirement{{Key: "tier", Operator: "NotIn", Values: []string{"front"}}}}, false},
		{"notin absent", domain.LabelSelector{MatchExpressions: []domain.SelectorRequirement{{Key: "x", Operator: "NotIn", Values: []string{"y"}}}}, true},
		{"exists", domain.LabelSelector{MatchExpressions: []domain.SelectorRequirement{{Key: "app", Operator: "Exists"}}}, true},
		{"doesnotexist", domain.LabelSelector{MatchExpressions: []domain.SelectorRequirement{{Key: "app", Operator: "DoesNotExist"}}}, false},
		{"unknown operator", domain.LabelSelector{MatchExpressions: []domain.SelectorRequirement{{Key: "app", Operator: "Gt"}}}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.selector.Matches(labels); got != test.want {
				t.Errorf("Matches = %v, want %v", got, test.want)
			}
		})
	}
}

func TestNewTopologyScope(t *testing.T) {
	if _, err := domain.NewTopologyScope(nil, false); err == nil {
		t.Error("an empty scope must be refused")
	}
	if _, err := domain.NewTopologyScope([]string{"Not_Valid"}, false); err == nil {
		t.Error("an invalid namespace must be refused")
	}
	scope, err := domain.NewTopologyScope([]string{"b", "a", "b", " "}, false)
	if err != nil || !slices.Equal(scope.Namespaces, []domain.NamespaceName{"a", "b"}) {
		t.Fatalf("scope = %+v, %v", scope, err)
	}
	if scope.ListsClusterWide() || !scope.Includes("a") || scope.Includes("c") {
		t.Errorf("scope = %+v", scope)
	}
	wide, _ := domain.NewTopologyScope([]string{"a", "b", "c", "d"}, false)
	if !wide.ListsClusterWide() {
		t.Error("more than three namespaces list cluster-wide")
	}
}

func BenchmarkNewTopologyGraph10kPods(b *testing.B) {
	in := bigInput(b, 10_000)
	in.ReadAt = time.Now()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = domain.NewTopologyGraph(in)
	}
}
