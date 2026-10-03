package domain_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

const appInstance = "shop"

var appLabels = map[string]string{domain.LabelInstance: appInstance}

func appPod(t *testing.T, name string, labels map[string]string, owner domain.OwnerReference) domain.Pod {
	t.Helper()

	spec := domain.PodSpec{
		Name: name, Namespace: "shop-ns", ClusterID: "dev",
		Phase: domain.PodPhaseRunning, Labels: labels,
		Containers: []domain.Container{{Name: "app", Image: "repo/app:1", Ready: true}},
		CreatedAt:  time.Now().Add(-time.Hour),
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

func ctl(kind, name string) domain.OwnerReference {
	return domain.OwnerReference{Kind: kind, Name: name, Controller: true}
}

func edgesInto(graph domain.PodGraph, to string) int {
	n := 0
	for _, edge := range graph.Edges {
		if edge.To == to {
			n++
		}
	}
	return n
}

func appInput(t *testing.T) domain.ApplicationGraphInput {
	t.Helper()
	return domain.ApplicationGraphInput{Instance: appInstance, Namespace: "shop-ns"}
}

func TestApplicationGraphNestsDeploymentReplicaSetPods(t *testing.T) {
	input := appInput(t)
	input.Candidates = []domain.ApplicationCandidate{
		{Kind: "Deployment", Name: "web", Labels: appLabels, Desired: 2, Ready: 2},
		// Unlabelled, as a ReplicaSet usually is; admitted by ownership.
		{Kind: "ReplicaSet", Name: "web-1", Owner: ctl("Deployment", "web"), Desired: 2, Ready: 2},
		// Somebody else's.
		{Kind: "ReplicaSet", Name: "other-1", Owner: ctl("Deployment", "other"), Desired: 1, Ready: 1},
	}
	input.Pods = []domain.Pod{
		appPod(t, "web-1-a", nil, ctl("ReplicaSet", "web-1")),
		appPod(t, "web-1-b", nil, ctl("ReplicaSet", "web-1")),
		appPod(t, "other-1-a", nil, ctl("ReplicaSet", "other-1")),
	}

	graph := domain.NewApplicationGraph(input)
	nodes := nodeIDs(graph)

	if _, ok := nodes["replicaset/other-1"]; ok {
		t.Error("a ReplicaSet neither labelled nor owned by a member must not be drawn")
	}
	if _, ok := nodes["pod/other-1-a"]; ok {
		t.Error("a pod of a non-member must not be drawn")
	}
	rs := nodes["replicaset/web-1"]
	if rs.Kind != domain.GraphReplicaSet || rs.Group != "deployment/web" {
		t.Errorf("owned ReplicaSet = %+v, want GraphReplicaSet grouped under its Deployment", rs)
	}
	if pod := nodes["pod/web-1-a"]; pod.Group != "replicaset/web-1" {
		t.Errorf("pod group = %q, want its ReplicaSet", pod.Group)
	}
	if !hasEdge(graph, "deployment/web", "replicaset/web-1") || !hasEdge(graph, "replicaset/web-1", "pod/web-1-a") {
		t.Errorf("missing a creates/manages edge: %+v", graph.Edges)
	}
	if !nodes["deployment/web"].Healthy || nodes["deployment/web"].Detail != "2/2 ready" {
		t.Errorf("deployment = %+v", nodes["deployment/web"])
	}
}

func TestApplicationGraphAdmitsACronJobsUnlabelledJobByOwnership(t *testing.T) {
	input := appInput(t)
	input.Candidates = []domain.ApplicationCandidate{
		{Kind: "CronJob", Name: "nightly", Labels: appLabels, Suspended: true},
		{Kind: "Job", Name: "nightly-1", Owner: ctl("CronJob", "nightly"), Desired: 1, Ready: 0, Failed: 1},
	}
	input.Pods = []domain.Pod{appPod(t, "nightly-1-x", nil, ctl("Job", "nightly-1"))}

	graph := domain.NewApplicationGraph(input)
	nodes := nodeIDs(graph)

	if got := nodes["cronjob/nightly"]; !got.Healthy || got.Detail != "suspended" {
		t.Errorf("cronjob = %+v, want healthy and suspended", got)
	}
	if got := nodes["job/nightly-1"]; got.Healthy || got.Detail != "0/1" || got.Group != "cronjob/nightly" {
		t.Errorf("job = %+v, want unhealthy 0/1 under the cronjob", got)
	}
	if !hasEdge(graph, "job/nightly-1", "pod/nightly-1-x") {
		t.Error("the Job's pod must hang off the Job")
	}
}

func TestApplicationGraphDrawsAnAttachedResourceOnceWithAnEdgePerPod(t *testing.T) {
	shared := []domain.AttachedRef{{Kind: domain.GraphConfig, Name: "settings", Via: "environment"}}
	input := appInput(t)
	input.Candidates = []domain.ApplicationCandidate{
		{Kind: "Deployment", Name: "web", Labels: appLabels, Desired: 1, Ready: 1, Attached: shared},
		{Kind: "Deployment", Name: "api", Labels: appLabels, Desired: 1, Ready: 1, Attached: shared},
		{Kind: "ReplicaSet", Name: "web-1", Owner: ctl("Deployment", "web"), Attached: shared},
		{Kind: "ReplicaSet", Name: "api-1", Owner: ctl("Deployment", "api"), Attached: shared},
	}
	input.Pods = []domain.Pod{
		appPod(t, "web-1-a", nil, ctl("ReplicaSet", "web-1")),
		appPod(t, "api-1-a", nil, ctl("ReplicaSet", "api-1")),
	}

	graph := domain.NewApplicationGraph(input)

	count := 0
	for _, node := range graph.Nodes {
		if node.ID == "config/settings" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("config drawn %d times, want once", count)
	}
	if edgesInto(graph, "config/settings") != 2 {
		t.Errorf("edges into config = %d, want one per pod", edgesInto(graph, "config/settings"))
	}
	if hasEdge(graph, "deployment/web", "config/settings") {
		t.Error("a Deployment with pods must not draw its own attached edge")
	}
}

func TestApplicationGraphZeroPodMembers(t *testing.T) {
	attached := []domain.AttachedRef{{Kind: domain.GraphSecret, Name: "token", Via: "environment"}}
	input := appInput(t)
	input.Candidates = []domain.ApplicationCandidate{
		{Kind: "Deployment", Name: "web", Labels: appLabels, Attached: attached},
		{Kind: "ReplicaSet", Name: "web-old", Owner: ctl("Deployment", "web"),
			Attached: []domain.AttachedRef{{Kind: domain.GraphSecret, Name: "legacy", Via: "environment"}}},
	}

	graph := domain.NewApplicationGraph(input)

	if !hasEdge(graph, "deployment/web", "secret/token") {
		t.Error("a zero-pod top-level member declares its template's attachments")
	}
	for _, node := range graph.Nodes {
		if node.ID == "secret/legacy" {
			t.Error("a zero-pod owned ReplicaSet declares nothing its Deployment does not")
		}
	}
}

func TestApplicationGraphServices(t *testing.T) {
	web := map[string]string{"app": "web"}
	api := map[string]string{"app": "api"}
	input := appInput(t)
	input.Candidates = []domain.ApplicationCandidate{
		{Kind: "Deployment", Name: "web", Labels: appLabels, Desired: 1, Ready: 1},
		{Kind: "Deployment", Name: "api", Labels: appLabels, Desired: 1, Ready: 1},
	}
	input.Pods = []domain.Pod{
		appPod(t, "web-a", merge(appLabels, web), domain.OwnerReference{}),
		appPod(t, "api-a", merge(appLabels, api), domain.OwnerReference{}),
	}
	input.Services = []domain.ServiceRef{
		// One Service across both workloads' pods: one node.
		{Name: "front", Namespace: "shop-ns", Selector: map[string]string{domain.LabelInstance: appInstance}, Labels: appLabels},
		// Labelled but selecting nothing.
		{Name: "stale", Namespace: "shop-ns", Selector: map[string]string{"app": "gone"}, Labels: appLabels},
		// Empty selector matches nothing, and is labelled, so it is drawn unwell.
		{Name: "external", Namespace: "shop-ns", Labels: appLabels},
		// Unlabelled and selecting nothing of ours: not drawn.
		{Name: "elsewhere", Namespace: "shop-ns", Selector: map[string]string{"app": "gone"}},
	}
	input.Ingresses = []domain.IngressRef{
		{Name: "edge", Namespace: "shop-ns", Backends: []string{"front"}},
		{Name: "unrelated", Namespace: "shop-ns", Backends: []string{"elsewhere"}},
	}

	graph := domain.NewApplicationGraph(input)
	nodes := nodeIDs(graph)

	if edgesInto(graph, "pod/web-a") != 1 || !hasEdge(graph, "service/front", "pod/web-a") || !hasEdge(graph, "service/front", "pod/api-a") {
		t.Errorf("front must select both pods through one node: %+v", graph.Edges)
	}
	for _, name := range []string{"stale", "external"} {
		node, ok := nodes["service/"+name]
		if !ok {
			t.Fatalf("labelled Service %q must be drawn", name)
		}
		if node.Healthy || node.Detail != "selects none of this application's pods" {
			t.Errorf("%s = %+v, want unhealthy with the explanatory detail", name, node)
		}
		for _, edge := range graph.Edges {
			if edge.From == node.ID {
				t.Errorf("%s must have no edges, has %+v", name, edge)
			}
		}
	}
	if _, ok := nodes["service/elsewhere"]; ok {
		t.Error("an unlabelled Service selecting none of our pods must not be drawn")
	}
	if !hasEdge(graph, "ingress/edge", "service/front") {
		t.Error("an Ingress routing to a drawn Service is drawn")
	}
	if _, ok := nodes["ingress/unrelated"]; ok {
		t.Error("an Ingress routing to nothing drawn must not be drawn")
	}
}

func merge(a, b map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func TestApplicationGraphUnownedLabelledPodIsDrawnWithNoEdge(t *testing.T) {
	input := appInput(t)
	input.Pods = []domain.Pod{appPod(t, "loose", appLabels, domain.OwnerReference{})}

	graph := domain.NewApplicationGraph(input)

	if _, ok := nodeIDs(graph)["pod/loose"]; !ok {
		t.Fatal("a labelled pod is a member")
	}
	if len(graph.Edges) != 0 {
		t.Errorf("edges = %+v, want none: a label is not a relationship", graph.Edges)
	}
}

func TestApplicationGraphHasNoContainerOrRootNodes(t *testing.T) {
	input := appInput(t)
	input.Candidates = []domain.ApplicationCandidate{{Kind: "Deployment", Name: "web", Labels: appLabels, Desired: 1, Ready: 1}}
	input.Pods = []domain.Pod{appPod(t, "web-a", appLabels, domain.OwnerReference{})}

	graph := domain.NewApplicationGraph(input)

	for _, node := range graph.Nodes {
		if node.Kind == domain.GraphContainer {
			t.Errorf("container node %q drawn", node.ID)
		}
		if node.Subject {
			t.Errorf("node %q is marked Subject; an application has no centre", node.ID)
		}
	}
}

func TestApplicationGraphCarriesUnreadableAndIsDeterministic(t *testing.T) {
	shared := []domain.AttachedRef{{Kind: domain.GraphConfig, Name: "settings", Via: "environment"}}
	base := appInput(t)
	base.Unreadable = []string{"cronjobs"}
	base.Candidates = []domain.ApplicationCandidate{
		{Kind: "Deployment", Name: "b", Labels: appLabels, Desired: 1, Ready: 1, Attached: shared},
		{Kind: "Deployment", Name: "a", Labels: appLabels, Desired: 1, Ready: 1, Attached: shared},
		{Kind: "StatefulSet", Name: "c", Labels: appLabels, Desired: 1, Ready: 0},
		{Kind: "ReplicaSet", Name: "a-1", Owner: ctl("Deployment", "a"), Desired: 1, Ready: 1, Attached: shared},
		{Kind: "ReplicaSet", Name: "b-1", Owner: ctl("Deployment", "b"), Desired: 1, Ready: 1, Attached: shared},
	}
	base.Pods = []domain.Pod{
		appPod(t, "a-1-x", map[string]string{"app": "a"}, ctl("ReplicaSet", "a-1")),
		appPod(t, "b-1-x", map[string]string{"app": "b"}, ctl("ReplicaSet", "b-1")),
		appPod(t, "c-0", map[string]string{"app": "c"}, ctl("StatefulSet", "c")),
	}
	base.Services = []domain.ServiceRef{
		{Name: "s1", Namespace: "shop-ns", Selector: map[string]string{"app": "a"}, Labels: appLabels},
		{Name: "s2", Namespace: "shop-ns", Selector: map[string]string{"app": "b"}, Labels: appLabels},
		{Name: "s3", Namespace: "shop-ns", Labels: appLabels},
	}
	base.Ingresses = []domain.IngressRef{
		{Name: "i1", Namespace: "shop-ns", Backends: []string{"s1", "s2"}},
		{Name: "i2", Namespace: "shop-ns", Backends: []string{"s3"}},
	}

	want := domain.NewApplicationGraph(base)
	if len(want.Unreadable) != 1 || want.Unreadable[0] != "cronjobs" {
		t.Errorf("Unreadable = %v", want.Unreadable)
	}
	if len(want.Nodes) == 0 || len(want.Edges) == 0 {
		t.Fatal("fixture drew nothing; the comparison below would be vacuous")
	}

	// Every full node and edge, in every shuffled order of every input list,
	// and repeatedly because map iteration inside the builder is random.
	for _, perm := range [][]int{{4, 3, 2, 1, 0}, {2, 0, 4, 1, 3}, {3, 4, 0, 2, 1}} {
		shuffled := base
		shuffled.Candidates = nil
		for _, i := range perm {
			shuffled.Candidates = append(shuffled.Candidates, base.Candidates[i])
		}
		shuffled.Pods = []domain.Pod{base.Pods[2], base.Pods[0], base.Pods[1]}
		shuffled.Services = []domain.ServiceRef{base.Services[2], base.Services[0], base.Services[1]}
		shuffled.Ingresses = []domain.IngressRef{base.Ingresses[1], base.Ingresses[0]}
		for run := 0; run < 10; run++ {
			if got := domain.NewApplicationGraph(shuffled); !reflect.DeepEqual(got, want) {
				t.Fatalf("shuffled input %v changed the graph:\nwant %+v\n got %+v", perm, want, got)
			}
		}
	}
}

func TestApplicationGraphSurvivesAnOwnerReferenceCycle(t *testing.T) {
	// Kubernetes does not forbid it, and a hostile or broken object can write
	// one. The graph must terminate and still draw both members and the pod.
	input := appInput(t)
	input.Candidates = []domain.ApplicationCandidate{
		{Kind: "Deployment", Name: "a", Labels: appLabels, Owner: ctl("Deployment", "b")},
		{Kind: "Deployment", Name: "b", Labels: appLabels, Owner: ctl("Deployment", "a")},
	}
	input.Pods = []domain.Pod{appPod(t, "p", nil, ctl("Deployment", "a"))}

	done := make(chan domain.PodGraph, 1)
	go func() { done <- domain.NewApplicationGraph(input) }()

	select {
	case graph := <-done:
		nodes := nodeIDs(graph)
		for _, id := range []string{"deployment/a", "deployment/b", "pod/p"} {
			if _, ok := nodes[id]; !ok {
				t.Errorf("node %q missing from %+v", id, graph.Nodes)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("NewApplicationGraph did not terminate on an owner cycle")
	}
}

func TestApplicationGraphStatefulSetAndDaemonSetOwnPodsDirectly(t *testing.T) {
	input := appInput(t)
	input.Candidates = []domain.ApplicationCandidate{
		{Kind: "StatefulSet", Name: "db", Labels: appLabels, Desired: 2, Ready: 1},
		{Kind: "DaemonSet", Name: "agent", Labels: appLabels, Desired: 3, Ready: 3},
	}
	input.Pods = []domain.Pod{
		appPod(t, "db-0", nil, ctl("StatefulSet", "db")),
		appPod(t, "agent-x", nil, ctl("DaemonSet", "agent")),
	}

	graph := domain.NewApplicationGraph(input)
	nodes := nodeIDs(graph)

	if !hasEdge(graph, "statefulset/db", "pod/db-0") || !hasEdge(graph, "daemonset/agent", "pod/agent-x") {
		t.Errorf("controllers must manage their pods directly: %+v", graph.Edges)
	}
	if nodes["pod/db-0"].Group != "statefulset/db" || nodes["pod/agent-x"].Group != "daemonset/agent" {
		t.Error("pods must be grouped under their controller")
	}
	if db := nodes["statefulset/db"]; db.Healthy || db.Detail != "1/2 ready" {
		t.Errorf("statefulset = %+v, want unhealthy 1/2 ready", db)
	}
	if !nodes["daemonset/agent"].Healthy {
		t.Error("a fully ready DaemonSet is healthy")
	}
}

func TestApplicationPodsMatchesTheGraphsPodNodes(t *testing.T) {
	input := appInput(t)
	input.Candidates = []domain.ApplicationCandidate{
		{Kind: "Deployment", Name: "web", Labels: appLabels},
		{Kind: "ReplicaSet", Name: "web-1", Owner: ctl("Deployment", "web")},
		{Kind: "ReplicaSet", Name: "other-1", Owner: ctl("Deployment", "other")},
	}
	input.Pods = []domain.Pod{
		appPod(t, "z-owned", nil, ctl("ReplicaSet", "web-1")),
		appPod(t, "a-labelled", appLabels, domain.OwnerReference{}),
		appPod(t, "stranger", nil, ctl("ReplicaSet", "other-1")),
	}

	pods := domain.ApplicationPods(input)
	graph := domain.NewApplicationGraph(input)

	podNodes := 0
	for _, node := range graph.Nodes {
		if node.Kind == domain.GraphPod {
			podNodes++
		}
	}
	if len(pods) != 2 || podNodes != 2 {
		t.Fatalf("pods = %d, pod nodes = %d, want 2 and 2", len(pods), podNodes)
	}
	if pods[0].Name() != "a-labelled" || pods[1].Name() != "z-owned" {
		t.Errorf("pods not in name order: %s, %s", pods[0].Name(), pods[1].Name())
	}
	for _, pod := range pods {
		if _, ok := nodeIDs(graph)["pod/"+pod.Name()]; !ok {
			t.Errorf("pod %q returned by ApplicationPods is not in the graph", pod.Name())
		}
	}
}
