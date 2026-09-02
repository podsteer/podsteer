package domain_test

import (
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// graphPod builds a pod with the labels a selector will be tested against.
//
// Local to this file rather than shared with overview_test.go: what a graph
// test needs from a pod is its name, namespace and labels, and borrowing a
// fixture built for capacity arithmetic would tie two unrelated suites
// together through a struct neither fully uses.
func graphPod(t *testing.T, name, namespace string, labels map[string]string) domain.Pod {
	t.Helper()

	pod, err := domain.NewPod(domain.PodSpec{
		UID:       name + "-uid",
		Name:      name,
		Namespace: domain.NamespaceName(namespace),
		ClusterID: "dev",
		Phase:     domain.PodPhaseRunning,
		Labels:    labels,
		CreatedAt: time.Now().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("building pod %q: %v", name, err)
	}
	return pod
}

// graphPodWithContainer builds a pod carrying one named container.
func graphPodWithContainer(t *testing.T, name, container string) domain.Pod {
	t.Helper()

	pod, err := domain.NewPod(domain.PodSpec{
		UID:        name + "-uid",
		Name:       name,
		Namespace:  "default",
		ClusterID:  "dev",
		Phase:      domain.PodPhaseRunning,
		Containers: []domain.Container{{Name: container, Image: "repo/app:1.2.3", Ready: true}},
		CreatedAt:  time.Now().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("building pod %q: %v", name, err)
	}
	return pod
}

func nodeIDs(graph domain.PodGraph) map[string]domain.GraphNode {
	out := make(map[string]domain.GraphNode, len(graph.Nodes))
	for _, node := range graph.Nodes {
		out[node.ID] = node
	}
	return out
}

func hasEdge(graph domain.PodGraph, from, to string) bool {
	for _, edge := range graph.Edges {
		if edge.From == from && edge.To == to {
			return true
		}
	}
	return false
}

func TestAServiceWhoseSelectorMatchesIsConnected(t *testing.T) {
	pod := graphPod(t, "api-0", "default", map[string]string{"app": "api", "tier": "web"})

	graph := domain.NewPodGraph(domain.GraphInput{
		Pod: pod,
		Services: []domain.ServiceRef{
			{Name: "api", Namespace: "default", Selector: map[string]string{"app": "api"}},
			{Name: "other", Namespace: "default", Selector: map[string]string{"app": "worker"}},
		},
	})

	if !hasEdge(graph, "service/api", "pod/api-0") {
		t.Error("the matching service is not connected to the pod")
	}
	if _, drawn := nodeIDs(graph)["service/other"]; drawn {
		t.Error("a service that does not select this pod was drawn")
	}
}

func TestAnEmptySelectorMatchesNothing(t *testing.T) {
	// THE ONE THAT WOULD BE WRONG QUIETLY. In the Kubernetes API an empty
	// selector on a Service means it has no selector at all — an ExternalName,
	// or Endpoints managed by hand. Read as "matches everything" it would draw
	// an edge to every pod in the namespace.
	pod := graphPod(t, "api-0", "default", map[string]string{"app": "api"})

	graph := domain.NewPodGraph(domain.GraphInput{
		Pod:      pod,
		Services: []domain.ServiceRef{{Name: "external", Selector: map[string]string{}}},
	})

	if _, drawn := nodeIDs(graph)["service/external"]; drawn {
		t.Error("a selectorless service was connected to a pod")
	}
}

func TestASelectorNeedsEveryKeyToMatch(t *testing.T) {
	pod := graphPod(t, "api-0", "default", map[string]string{"app": "api"})

	graph := domain.NewPodGraph(domain.GraphInput{
		Pod: pod,
		Services: []domain.ServiceRef{
			{Name: "narrow", Selector: map[string]string{"app": "api", "tier": "web"}},
		},
	})

	if _, drawn := nodeIDs(graph)["service/narrow"]; drawn {
		t.Error("a selector requiring a label the pod lacks was treated as matching")
	}
}

func TestAnIngressIsDrawnOnlyWhenItReachesThisPod(t *testing.T) {
	// An ingress routing to services that do not select this pod is not this
	// pod's ingress, and drawing it would show a path that does not exist.
	pod := graphPod(t, "api-0", "default", map[string]string{"app": "api"})

	graph := domain.NewPodGraph(domain.GraphInput{
		Pod:      pod,
		Services: []domain.ServiceRef{{Name: "api", Selector: map[string]string{"app": "api"}}},
		Ingresses: []domain.IngressRef{
			{Name: "public", Hosts: []string{"api.example.com"}, Backends: []string{"api"}},
			{Name: "unrelated", Backends: []string{"worker"}},
		},
	})

	if !hasEdge(graph, "ingress/public", "service/api") {
		t.Error("the ingress that reaches this pod is not connected")
	}
	if _, drawn := nodeIDs(graph)["ingress/unrelated"]; drawn {
		t.Error("an ingress routing elsewhere was drawn")
	}
}

func TestTheOwnerChainRunsDownwardToThePod(t *testing.T) {
	pod := graphPod(t, "api-0", "default", map[string]string{"app": "api"})

	graph := domain.NewPodGraph(domain.GraphInput{
		Pod: pod,
		Owner: []domain.OwnerReference{
			{Kind: "ReplicaSet", Name: "api-7d4f"},
			{Kind: "Deployment", Name: "api"},
		},
	})

	if !hasEdge(graph, "replicaset/api-7d4f", "pod/api-0") {
		t.Error("the replicaset does not point at the pod")
	}
	if !hasEdge(graph, "deployment/api", "replicaset/api-7d4f") {
		t.Error("the deployment does not point at the replicaset")
	}
}

func TestTheSubjectPodIsMarked(t *testing.T) {
	// The map is drawn around one object, and it has to be findable in it.
	graph := domain.NewPodGraph(domain.GraphInput{
		Pod: graphPod(t, "api-0", "default", nil),
	})

	pod := nodeIDs(graph)["pod/api-0"]
	if !pod.Subject {
		t.Error("the pod the map was opened from is not marked as the subject")
	}
}

func TestAttachedResourcesAreNotDuplicated(t *testing.T) {
	// One ConfigMap mounted twice, or mounted and read from the environment,
	// is one dependency and one box.
	graph := domain.NewPodGraph(domain.GraphInput{
		Pod: graphPod(t, "api-0", "default", nil),
		Attached: []domain.AttachedRef{
			{Kind: domain.GraphConfig, Name: "settings", Via: "/etc/config"},
			{Kind: domain.GraphConfig, Name: "settings", Via: "environment"},
		},
	})

	count := 0
	for _, node := range graph.Nodes {
		if node.ID == "config/settings" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("the same ConfigMap was drawn %d times", count)
	}
}

func TestTheGraphIsStableAcrossRebuilds(t *testing.T) {
	// A map that reshuffles on every poll is worse than one a few seconds
	// stale: map iteration in Go is unordered, so without sorting the chart
	// would redraw differently each refresh while nothing changed.
	build := func() domain.PodGraph {
		return domain.NewPodGraph(domain.GraphInput{
			Pod: graphPod(t, "api-0", "default", map[string]string{"app": "api"}),
			Services: []domain.ServiceRef{
				{Name: "b", Selector: map[string]string{"app": "api"}},
				{Name: "a", Selector: map[string]string{"app": "api"}},
			},
			Attached: []domain.AttachedRef{
				{Kind: domain.GraphSecret, Name: "z"},
				{Kind: domain.GraphConfig, Name: "y"},
			},
		})
	}

	first, second := build(), build()

	for i := range first.Nodes {
		if first.Nodes[i].ID != second.Nodes[i].ID {
			t.Fatalf("node %d differs between builds: %q then %q",
				i, first.Nodes[i].ID, second.Nodes[i].ID)
		}
	}
}

func TestUnreadableSourcesAreCarried(t *testing.T) {
	// An account without ingress permissions gets a map with no ingress tier,
	// and must be told that is what happened rather than shown an empty top.
	graph := domain.NewPodGraph(domain.GraphInput{
		Pod:        graphPod(t, "api-0", "default", nil),
		Unreadable: []string{"ingresses"},
	})

	if len(graph.Unreadable) != 1 || graph.Unreadable[0] != "ingresses" {
		t.Fatalf("unreadable sources were lost: %v", graph.Unreadable)
	}
}

func TestAWorkloadMapFansOutToItsPods(t *testing.T) {
	graph := domain.NewWorkloadGraph(domain.WorkloadGraphInput{
		Kind: "Deployment", Name: "api", Namespace: "default", Healthy: true,
		Pods: []domain.Pod{
			graphPod(t, "api-1", "default", map[string]string{"app": "api"}),
			graphPod(t, "api-2", "default", map[string]string{"app": "api"}),
		},
	})

	if !hasEdge(graph, "deployment/api", "pod/api-1") ||
		!hasEdge(graph, "deployment/api", "pod/api-2") {
		t.Fatal("the workload does not reach both of its pods")
	}
	if !nodeIDs(graph)["deployment/api"].Subject {
		t.Error("the workload the map was opened from is not marked as the subject")
	}
}

func TestAServiceConnectsToTheWorkloadNotEveryReplica(t *testing.T) {
	// A fan of edges from one Service into every replica says the same thing
	// several times, and is unreadable at three replicas let alone thirty.
	graph := domain.NewWorkloadGraph(domain.WorkloadGraphInput{
		Kind: "Deployment", Name: "api", Namespace: "default",
		Pods: []domain.Pod{
			graphPod(t, "api-1", "default", map[string]string{"app": "api"}),
			graphPod(t, "api-2", "default", map[string]string{"app": "api"}),
		},
		Services: []domain.ServiceRef{{Name: "api", Selector: map[string]string{"app": "api"}}},
	})

	if !hasEdge(graph, "service/api", "deployment/api") {
		t.Fatal("the service is not connected to the workload")
	}
	if hasEdge(graph, "service/api", "pod/api-1") {
		t.Error("the service was also wired to an individual replica")
	}
}

func TestAServiceMatchingNoReplicaIsNotDrawn(t *testing.T) {
	graph := domain.NewWorkloadGraph(domain.WorkloadGraphInput{
		Kind: "Deployment", Name: "api", Namespace: "default",
		Pods:     []domain.Pod{graphPod(t, "api-1", "default", map[string]string{"app": "api"})},
		Services: []domain.ServiceRef{{Name: "other", Selector: map[string]string{"app": "worker"}}},
	})

	if _, drawn := nodeIDs(graph)["service/other"]; drawn {
		t.Error("a service selecting none of the workload's pods was drawn")
	}
}

func TestReplicaContainersAreNotCollapsedTogether(t *testing.T) {
	// Every replica of a Deployment runs containers with the SAME names, so
	// keying a container box on its name alone would collapse three replicas'
	// containers into one box with three edges into it.
	pods := []domain.Pod{
		graphPodWithContainer(t, "api-1", "app"),
		graphPodWithContainer(t, "api-2", "app"),
	}

	graph := domain.NewWorkloadGraph(domain.WorkloadGraphInput{
		Kind: "Deployment", Name: "api", Namespace: "default", Pods: pods,
	})

	nodes := nodeIDs(graph)
	if _, ok := nodes["container/api-1/app"]; !ok {
		t.Error("the first replica's container is missing")
	}
	if _, ok := nodes["container/api-2/app"]; !ok {
		t.Error("the second replica's container is missing")
	}
}

func TestAttachedResourcesHangOffTheWorkloadOnce(t *testing.T) {
	// Config and secrets come from the pod TEMPLATE, so every replica reads
	// the same ones: an edge per pod would draw one dependency three times.
	graph := domain.NewWorkloadGraph(domain.WorkloadGraphInput{
		Kind: "Deployment", Name: "api", Namespace: "default",
		Pods: []domain.Pod{
			graphPod(t, "api-1", "default", nil),
			graphPod(t, "api-2", "default", nil),
		},
		Attached: []domain.AttachedRef{{Kind: domain.GraphConfig, Name: "settings"}},
	})

	edges := 0
	for _, edge := range graph.Edges {
		if edge.To == "config/settings" {
			edges++
		}
	}
	if edges != 1 {
		t.Fatalf("the ConfigMap was connected %d times, want once", edges)
	}
}

func TestAWorkloadWithNoPodsStillDraws(t *testing.T) {
	// A Deployment scaled to zero, or a CronJob between runs. A real state,
	// not an error.
	graph := domain.NewWorkloadGraph(domain.WorkloadGraphInput{
		Kind: "CronJob", Name: "nightly", Namespace: "default",
	})

	subject := nodeIDs(graph)["cronjob/nightly"]
	if subject.Name != "nightly" {
		t.Fatal("the workload itself was not drawn")
	}
	if subject.Detail != "no pods" {
		t.Errorf("detail is %q; it should say the workload has none", subject.Detail)
	}
}

func TestACronJobReachesItsPodsThroughItsJobs(t *testing.T) {
	// A CRONJOB DOES NOT OWN PODS. It owns Jobs, and those own pods. Hanging
	// pods straight off the CronJob would draw a relationship Kubernetes does
	// not have, and would lose the only thing that says which run a pod
	// belongs to.
	graph := domain.NewWorkloadGraph(domain.WorkloadGraphInput{
		Kind: "CronJob", Name: "nightly", Namespace: "default",
		Pods: []domain.Pod{
			graphPod(t, "nightly-28-abc", "default", nil),
			graphPod(t, "nightly-29-def", "default", nil),
		},
		Intermediates: []domain.IntermediateRef{
			{Kind: "Job", Name: "nightly-28", Pods: []string{"nightly-28-abc"}},
			{Kind: "Job", Name: "nightly-29", Pods: []string{"nightly-29-def"}},
		},
	})

	if !hasEdge(graph, "cronjob/nightly", "job/nightly-28") {
		t.Error("the cronjob does not reach the job it created")
	}
	if !hasEdge(graph, "job/nightly-28", "pod/nightly-28-abc") {
		t.Error("the job does not reach its own pod")
	}
	if hasEdge(graph, "cronjob/nightly", "pod/nightly-28-abc") {
		t.Error("a pod was hung directly off the cronjob, skipping its job")
	}
}

func TestAJobWithNoPodsLeftIsStillDrawn(t *testing.T) {
	// A completed run whose pods were reaped is part of the history the map
	// shows: dropping it would make a CronJob look like it had never run.
	graph := domain.NewWorkloadGraph(domain.WorkloadGraphInput{
		Kind: "CronJob", Name: "nightly", Namespace: "default",
		Intermediates: []domain.IntermediateRef{{Kind: "Job", Name: "nightly-27"}},
	})

	if _, drawn := nodeIDs(graph)["job/nightly-27"]; !drawn {
		t.Error("a job whose pods have been reaped was dropped from the map")
	}
}

func TestAWorkloadThatOwnsItsPodsDirectlyStillDoes(t *testing.T) {
	// Everything but a CronJob owns its pods, and must not grow a tier.
	graph := domain.NewWorkloadGraph(domain.WorkloadGraphInput{
		Kind: "DaemonSet", Name: "agent", Namespace: "default",
		Pods: []domain.Pod{graphPod(t, "agent-xyz", "default", nil)},
	})

	if !hasEdge(graph, "daemonset/agent", "pod/agent-xyz") {
		t.Error("the daemonset does not reach its pod directly")
	}
}
