package domain_test

import (
	"strings"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

// edgeSet renders a graph's edges as "from -> to" for comparison.
func edgeSet(graph domain.PodGraph) map[string]string {
	out := make(map[string]string, len(graph.Edges))
	for _, edge := range graph.Edges {
		out[edge.From+" -> "+edge.To] = edge.Label
	}
	return out
}

func TestObjectGraphDrawsTheSubjectInTheMiddle(t *testing.T) {
	graph := domain.NewObjectGraph(domain.ObjectGraphInput{
		Kind: "ConfigMap", Name: "settings", Namespace: "shop",
	})

	nodes := nodeIDs(graph)
	subject, found := nodes["configmap/shop/settings"]
	if !found {
		t.Fatalf("no subject node, got %v", nodes)
	}
	if !subject.Subject {
		t.Error("the subject is not marked as the subject")
	}
	if subject.APIKind != "ConfigMap" {
		// VERBATIM, because the drawer resolves a followed node against the
		// navigator catalogue, which is keyed by Kind. A lowercased plural
		// matches nothing and the click silently does nothing at all.
		t.Errorf("APIKind = %q, want the Kubernetes kind verbatim", subject.APIKind)
	}
	if subject.Kind != domain.GraphConfig {
		t.Errorf("graph kind = %q, want %q", subject.Kind, domain.GraphConfig)
	}
	if len(graph.Edges) != 0 {
		t.Errorf("a ConfigMap with nothing around it drew %d edges", len(graph.Edges))
	}
}

func TestObjectGraphOwnerChain(t *testing.T) {
	tests := []struct {
		name       string
		owners     []domain.ObjectReference
		wantNodes  []string
		wantEdges  map[string]string
		wantMissed string
	}{
		{
			name: "two hops read upward from the subject",
			owners: []domain.ObjectReference{
				{Kind: "ReplicaSet", Name: "web-abc", Namespace: "shop"},
				{Kind: "Deployment", Name: "web", Namespace: "shop"},
			},
			wantNodes: []string{"pod/shop/web-abc-1", "replicaset/shop/web-abc", "deployment/shop/web"},
			wantEdges: map[string]string{
				"replicaset/shop/web-abc -> pod/shop/web-abc-1":  "owns",
				"deployment/shop/web -> replicaset/shop/web-abc": "owns",
			},
		},
		{
			name:      "no owners draws the subject alone",
			owners:    nil,
			wantNodes: []string{"pod/shop/web-abc-1"},
			wantEdges: map[string]string{},
		},
		{
			name: "a cluster-scoped owner carries no namespace",
			owners: []domain.ObjectReference{
				{Kind: "CustomResourceDefinition", Name: "widgets.example.com"},
			},
			wantNodes: []string{"pod/shop/web-abc-1", "customresourcedefinition/widgets.example.com"},
			wantEdges: map[string]string{
				"customresourcedefinition/widgets.example.com -> pod/shop/web-abc-1": "owns",
			},
		},
		{
			name: "an owner that could not be found is drawn and marked",
			owners: []domain.ObjectReference{
				{Kind: "Deployment", Name: "gone", Namespace: "shop", Missing: true},
			},
			wantNodes: []string{"pod/shop/web-abc-1", "deployment/shop/gone"},
			wantEdges: map[string]string{
				"deployment/shop/gone -> pod/shop/web-abc-1": "owns",
			},
			wantMissed: "deployment/shop/gone",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph := domain.NewObjectGraph(domain.ObjectGraphInput{
				Kind: "Pod", Name: "web-abc-1", Namespace: "shop", Owners: test.owners,
			})

			nodes := nodeIDs(graph)
			if len(nodes) != len(test.wantNodes) {
				t.Fatalf("got %d nodes %v, want %d", len(nodes), nodes, len(test.wantNodes))
			}
			for _, id := range test.wantNodes {
				if _, found := nodes[id]; !found {
					t.Errorf("node %q missing, got %v", id, nodes)
				}
			}

			edges := edgeSet(graph)
			if len(edges) != len(test.wantEdges) {
				t.Fatalf("got %d edges %v, want %d", len(edges), edges, len(test.wantEdges))
			}
			for edge, label := range test.wantEdges {
				got, found := edges[edge]
				if !found {
					t.Errorf("edge %q missing, got %v", edge, edges)
					continue
				}
				if got != label {
					t.Errorf("edge %q label = %q, want %q", edge, got, label)
				}
			}

			if test.wantMissed != "" && !nodes[test.wantMissed].Missing {
				t.Errorf("node %q is not marked missing", test.wantMissed)
			}
		})
	}
}

// A CYCLE MUST TERMINATE, and Kubernetes does not forbid one: an operator that
// writes an ownerReference back onto the object it was created from produces a
// chain that repeats forever. It must draw each object once and stop, not loop
// and not draw a second box for the same thing.
func TestObjectGraphOwnerCycleTerminates(t *testing.T) {
	graph := domain.NewObjectGraph(domain.ObjectGraphInput{
		Kind: "Widget", Name: "left", Namespace: "shop",
		Owners: []domain.ObjectReference{
			{Kind: "Widget", Name: "right", Namespace: "shop"},
			{Kind: "Widget", Name: "left", Namespace: "shop"},
			{Kind: "Widget", Name: "right", Namespace: "shop"},
		},
	})

	nodes := nodeIDs(graph)
	if len(nodes) != 2 {
		t.Fatalf("got %d nodes %v, want the subject and its one distinct owner", len(nodes), nodes)
	}
	if len(graph.Edges) != 1 {
		t.Fatalf("got %d edges %v, want one", len(graph.Edges), graph.Edges)
	}
	if graph.Edges[0].From != "widget/shop/right" || graph.Edges[0].To != "widget/shop/left" {
		t.Errorf("edge = %+v, want right owning left", graph.Edges[0])
	}
}

// THE DEPTH CAP HOLDS whatever the caller hands over. The adapter bounds its
// own walk, but the rule is the domain's, so a chain longer than the cap
// arriving here — from a future caller, or a test — must still stop.
func TestObjectGraphOwnerDepthCap(t *testing.T) {
	var owners []domain.ObjectReference
	for _, name := range []string{"one", "two", "three", "four", "five"} {
		owners = append(owners, domain.ObjectReference{Kind: "Widget", Name: name, Namespace: "shop"})
	}

	graph := domain.NewObjectGraph(domain.ObjectGraphInput{
		Kind: "Pod", Name: "leaf", Namespace: "shop", Owners: owners,
	})

	// The subject plus exactly ObjectOwnerDepth hops above it.
	if want := domain.ObjectOwnerDepth + 1; len(graph.Nodes) != want {
		t.Fatalf("got %d nodes %v, want %d", len(graph.Nodes), nodeIDs(graph), want)
	}
	if _, found := nodeIDs(graph)["widget/shop/four"]; found {
		t.Error("the fourth hop was drawn; the depth cap did not hold")
	}
}

func TestObjectGraphSelectedPods(t *testing.T) {
	web := graphPod(t, "web-1", "shop", map[string]string{"app": "web"})
	other := graphPod(t, "batch-1", "shop", map[string]string{"app": "batch"})

	tests := []struct {
		name     string
		selector map[string]string
		pods     []domain.Pod
		want     []string
	}{
		{
			name:     "a selector draws an edge to the pods it matches",
			selector: map[string]string{"app": "web"},
			pods:     []domain.Pod{web, other},
			want:     []string{"pod/shop/web-1"},
		},
		{
			// AN EMPTY SELECTOR MATCHES NOTHING. In the Kubernetes API an
			// empty selector on a Service means it has none at all — an
			// ExternalName, or Endpoints managed by hand — and reading it as
			// "matches everything" draws an edge to every pod in the
			// namespace. This is the negative case that rule exists for.
			name:     "an empty selector matches nothing",
			selector: nil,
			pods:     []domain.Pod{web, other},
			want:     nil,
		},
		{
			name:     "a selector matching no pod draws none",
			selector: map[string]string{"app": "nothing-here"},
			pods:     []domain.Pod{web, other},
			want:     nil,
		},
		{
			// EVERY KEY MUST MATCH: a selector is a conjunction, so a pod
			// carrying one of two required labels is not selected.
			name:     "a partly matching pod is not selected",
			selector: map[string]string{"app": "web", "tier": "front"},
			pods:     []domain.Pod{web},
			want:     nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph := domain.NewObjectGraph(domain.ObjectGraphInput{
				Kind: "Service", Name: "web", Namespace: "shop",
				Selector: test.selector, Pods: test.pods,
			})

			nodes := nodeIDs(graph)
			for _, id := range test.want {
				if _, found := nodes[id]; !found {
					t.Errorf("pod %q not selected, got %v", id, nodes)
				}
			}
			// The subject and nothing but the wanted pods.
			if want := len(test.want) + 1; len(nodes) != want {
				t.Fatalf("got %d nodes %v, want %d", len(nodes), nodes, want)
			}

			// A Service HAS a cheap downward answer, so an empty result is a
			// real answer and must never be explained away with the bound.
			if graph.Bounded != "" {
				t.Errorf("a Service map carried a downward bound: %q", graph.Bounded)
			}
		})
	}
}

// A SELECTED POD IS A SIBLING SET, so thirty of them fold into one box. The
// graph still carries all thirty — folding is a view decision — and the Group
// is what tells the view which nodes form the set.
func TestObjectGraphSelectedPodsAreGrouped(t *testing.T) {
	graph := domain.NewObjectGraph(domain.ObjectGraphInput{
		Kind: "Service", Name: "web", Namespace: "shop",
		Selector: map[string]string{"app": "web"},
		Pods: []domain.Pod{
			graphPod(t, "web-1", "shop", map[string]string{"app": "web"}),
			graphPod(t, "web-2", "shop", map[string]string{"app": "web"}),
		},
	})

	for _, id := range []string{"pod/shop/web-1", "pod/shop/web-2"} {
		node := nodeIDs(graph)[id]
		if node.Group != "service/shop/web" {
			t.Errorf("pod %q group = %q, want the Service", id, node.Group)
		}
	}
}

func TestObjectGraphReferences(t *testing.T) {
	tests := []struct {
		name       string
		references []domain.ObjectReference
		wantNodes  []string
		wantEdges  map[string]string
		wantMissed []string
	}{
		{
			name: "a reference is drawn with the field that named it",
			references: []domain.ObjectReference{
				{Version: "v1", Kind: "PersistentVolume", Name: "pv-7", Via: "bound to"},
			},
			wantNodes: []string{"persistentvolume/pv-7"},
			wantEdges: map[string]string{
				"persistentvolumeclaim/shop/data -> persistentvolume/pv-7": "bound to",
			},
		},
		{
			// ONE BOX PER REFERENCED OBJECT, however many fields name it. An
			// Ingress with twelve paths onto one Service must draw one box
			// with one line, not twelve of each.
			name: "one box however many fields name the same object",
			references: []domain.ObjectReference{
				{Version: "v1", Kind: "Service", Name: "web", Namespace: "shop", Via: "/"},
				{Version: "v1", Kind: "Service", Name: "web", Namespace: "shop", Via: "/api"},
			},
			wantNodes: []string{"service/shop/web"},
			wantEdges: map[string]string{
				"persistentvolumeclaim/shop/data -> service/shop/web": "/api",
			},
		},
		{
			// A DANGLING REFERENCE IS THE ANSWER SOMEBODY OPENED THE MAP FOR.
			// Dropping it renders the broken case and the working case
			// identically, which is the one thing a dependency map must never
			// do.
			name: "a reference to something that is not there is drawn and marked",
			references: []domain.ObjectReference{
				{Group: "storage.k8s.io", Version: "v1", Kind: "StorageClass",
					Name: "fast", Via: "storage class", Missing: true},
			},
			wantNodes: []string{"storageclass/fast"},
			wantEdges: map[string]string{
				"persistentvolumeclaim/shop/data -> storageclass/fast": "storage class",
			},
			wantMissed: []string{"storageclass/fast"},
		},
		{
			name: "a reference with no name is not drawn",
			references: []domain.ObjectReference{
				{Version: "v1", Kind: "Secret", Namespace: "shop", Via: "tls certificate"},
			},
			wantNodes: nil,
			wantEdges: map[string]string{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph := domain.NewObjectGraph(domain.ObjectGraphInput{
				Kind: "PersistentVolumeClaim", Name: "data", Namespace: "shop",
				References: test.references,
			})

			nodes := nodeIDs(graph)
			if want := len(test.wantNodes) + 1; len(nodes) != want {
				t.Fatalf("got %d nodes %v, want %d", len(nodes), nodes, want)
			}
			for _, id := range test.wantNodes {
				if _, found := nodes[id]; !found {
					t.Errorf("node %q missing, got %v", id, nodes)
				}
			}

			edges := edgeSet(graph)
			if len(edges) != len(test.wantEdges) {
				t.Fatalf("got %d edges %v, want %d", len(edges), edges, len(test.wantEdges))
			}
			for edge, label := range test.wantEdges {
				if got := edges[edge]; got != label {
					t.Errorf("edge %q label = %q, want %q", edge, got, label)
				}
			}

			for _, id := range test.wantMissed {
				node := nodes[id]
				if !node.Missing {
					t.Errorf("node %q is not marked missing", id)
				}
				if node.Healthy {
					t.Errorf("node %q is missing and still drawn as well", id)
				}
				if node.Detail != "not found" {
					t.Errorf("node %q detail = %q, want %q", id, node.Detail, "not found")
				}
			}
		})
	}
}

// SAYING WHY IS THE WHOLE POINT of the downward bound. An empty space under an
// object reads as "nothing depends on this", which is a claim nothing checked.
func TestDownwardBound(t *testing.T) {
	tests := []struct {
		name     string
		kind     string
		wantLine bool
	}{
		{name: "a Service has a cheap answer and needs no line", kind: "Service", wantLine: false},
		{name: "a ConfigMap has none", kind: "ConfigMap", wantLine: true},
		{name: "a custom resource has none", kind: "Widget", wantLine: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			line := domain.DownwardBound(test.kind)
			if test.wantLine {
				if line == "" {
					t.Fatal("no line, want one saying why nothing is drawn below")
				}
				if !strings.Contains(line, test.kind) {
					t.Errorf("line %q does not name the kind", line)
				}
			} else if line != "" {
				t.Errorf("got %q, want no line", line)
			}

			if want := domain.HasDownwardAnswer(test.kind); want == test.wantLine {
				t.Errorf("HasDownwardAnswer(%q) = %v, which disagrees with DownwardBound",
					test.kind, want)
			}
		})
	}
}

// The bound is carried on the graph itself, so the panel can say it in one
// line — and never on a kind whose empty space is a real answer.
func TestObjectGraphCarriesTheDownwardBound(t *testing.T) {
	configMap := domain.NewObjectGraph(domain.ObjectGraphInput{
		Kind: "ConfigMap", Name: "settings", Namespace: "shop",
	})
	if configMap.Bounded == "" {
		t.Error("a ConfigMap map carries no downward bound")
	}

	service := domain.NewObjectGraph(domain.ObjectGraphInput{
		Kind: "Service", Name: "web", Namespace: "shop",
	})
	if service.Bounded != "" {
		t.Errorf("a Service with no pods carried a bound: %q", service.Bounded)
	}
}

// Unreadable and Bounded are different facts and must not be confused: a read
// refused is fixed by a permission, a read not attempted is not.
func TestObjectGraphKeepsUnreadableSeparateFromBounded(t *testing.T) {
	graph := domain.NewObjectGraph(domain.ObjectGraphInput{
		Kind: "ConfigMap", Name: "settings", Namespace: "shop",
		Unreadable: []string{"pods"},
	})

	if len(graph.Unreadable) != 1 || graph.Unreadable[0] != "pods" {
		t.Errorf("Unreadable = %v, want the source that was refused", graph.Unreadable)
	}
	if graph.Bounded == "" {
		t.Error("Bounded was emptied by an unreadable source; they are different facts")
	}
}

// An owner and a reference that name the SAME object draw one box, and the
// owner wins: it is the stronger relationship and sits above rather than
// beside.
func TestObjectGraphDeduplicatesAcrossOwnersAndReferences(t *testing.T) {
	graph := domain.NewObjectGraph(domain.ObjectGraphInput{
		Kind: "Widget", Name: "left", Namespace: "shop",
		Owners: []domain.ObjectReference{
			{Kind: "Deployment", Name: "web", Namespace: "shop"},
		},
		References: []domain.ObjectReference{
			{Kind: "Deployment", Name: "web", Namespace: "shop", Via: "scales"},
		},
	})

	nodes := nodeIDs(graph)
	if len(nodes) != 2 {
		t.Fatalf("got %d nodes %v, want the subject and one box for the Deployment", len(nodes), nodes)
	}
	if got := nodes["deployment/shop/web"].Tier; got != domain.TierOwner {
		t.Errorf("tier = %d, want the owner tier %d", got, domain.TierOwner)
	}
	// Both relationships are still drawn: the object is one box, the two lines
	// mean different things.
	if len(graph.Edges) != 2 {
		t.Errorf("got %d edges %v, want the owns edge and the reference edge",
			len(graph.Edges), graph.Edges)
	}
}

func TestGraphKindOf(t *testing.T) {
	tests := []struct {
		kind string
		want domain.GraphKind
	}{
		{kind: "Pod", want: domain.GraphPod},
		{kind: "Service", want: domain.GraphService},
		{kind: "Ingress", want: domain.GraphIngress},
		{kind: "ConfigMap", want: domain.GraphConfig},
		{kind: "Secret", want: domain.GraphSecret},
		{kind: "PersistentVolumeClaim", want: domain.GraphClaim},
		{kind: "PersistentVolume", want: domain.GraphClaim},
		{kind: "ServiceAccount", want: domain.GraphServiceAccount},
		{kind: "ReplicaSet", want: domain.GraphReplicaSet},
		{kind: "Deployment", want: domain.GraphWorkload},
		{kind: "Node", want: domain.GraphHost},
		// ANYTHING WITH NO CATEGORY IS A PLAIN OBJECT, never borrowed onto one
		// it does not belong to: a Deployment's icon on a CRD instance is
		// worse than a neutral box.
		{kind: "StorageClass", want: domain.GraphObject},
		{kind: "Widget", want: domain.GraphObject},
		{kind: "", want: domain.GraphObject},
	}

	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			if got := domain.GraphKindOf(test.kind); got != test.want {
				t.Errorf("GraphKindOf(%q) = %q, want %q", test.kind, got, test.want)
			}
		})
	}
}

// The sort is what stops a map reshuffling under a reader between two opens of
// the same object — map iteration is unordered, and the references arrive
// through one.
func TestObjectGraphIsStablyOrdered(t *testing.T) {
	build := func() domain.PodGraph {
		return domain.NewObjectGraph(domain.ObjectGraphInput{
			Kind: "Ingress", Name: "shop", Namespace: "shop",
			References: []domain.ObjectReference{
				{Version: "v1", Kind: "Service", Name: "web", Namespace: "shop", Via: "/"},
				{Version: "v1", Kind: "Service", Name: "api", Namespace: "shop", Via: "/api"},
				{Version: "v1", Kind: "Secret", Name: "tls", Namespace: "shop", Via: "tls certificate"},
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
	for i := range first.Edges {
		if first.Edges[i] != second.Edges[i] {
			t.Fatalf("edge %d differs between builds: %+v then %+v",
				i, first.Edges[i], second.Edges[i])
		}
	}
}
