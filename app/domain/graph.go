package domain

import (
	"fmt"
	"sort"
	"strings"
)

// GraphKind is what a node in the dependency map represents.
//
// Deliberately not the Kubernetes kind: `Workload` covers Deployment,
// StatefulSet and the rest, because the map is drawn to show a SHAPE and a
// reader following a request from an Ingress does not need the graph to
// distinguish a DaemonSet from a Deployment at that moment. The real kind is
// on the node for the label and for following it.
type GraphKind string

const (
	GraphIngress        GraphKind = "ingress"
	GraphService        GraphKind = "service"
	GraphWorkload       GraphKind = "workload"
	GraphReplicaSet     GraphKind = "replicaset"
	GraphPod            GraphKind = "pod"
	GraphContainer      GraphKind = "container"
	GraphHost           GraphKind = "node"
	GraphConfig         GraphKind = "config"
	GraphSecret         GraphKind = "secret"
	GraphClaim          GraphKind = "claim"
	GraphServiceAccount GraphKind = "serviceaccount"
	// GraphObject is anything the map has no category for — a CRD instance, a
	// StorageClass, an IngressClass. Drawn as a plain box rather than borrowed
	// onto a category it does not belong to: a Deployment's icon on something
	// that is not one is worse than a neutral one.
	GraphObject GraphKind = "object"
)

// GraphTier is how far down the request path a node sits.
//
// The layout is the argument. A dependency map drawn as a free-floating web
// invites the reader to find the shape themselves; drawn in tiers, from what
// is outside the cluster down to what actually runs, it says which way
// traffic goes before anybody reads a label.
type GraphTier int

const (
	TierIngress   GraphTier = 0
	TierService   GraphTier = 1
	TierOwner     GraphTier = 2
	TierPod       GraphTier = 3
	TierContainer GraphTier = 4
	// TierAttached holds what a pod consumes rather than what reaches it —
	// config, secrets, volumes, the node. Off to one side, because they are
	// dependencies but not steps in the path.
	TierAttached GraphTier = 5
)

// GraphNode is one box on the map.
type GraphNode struct {
	// ID is unique within one graph, and stable across refreshes so a chart
	// can animate rather than rebuild.
	ID   string
	Kind GraphKind
	// APIKind is the Kubernetes kind, for the label and for following the
	// node into its own panel. Empty for containers, which are not objects.
	APIKind   string
	Name      string
	Namespace string
	Tier      GraphTier
	// Detail is a short qualifier — a port, an image tag, a host.
	Detail string
	// Healthy is false for anything the reader should look at. A map where
	// everything is one colour tells them where things are; the colour is what
	// tells them where to start.
	Healthy bool
	// Subject marks the object the map was opened from, so it can be drawn as
	// the centre rather than as one box among twenty.
	Subject bool
	// Missing marks a reference that was resolved against the cluster and
	// found to name nothing.
	//
	// DISTINCT FROM Healthy, which a missing node also carries as false. An
	// unwell object exists and is failing; a missing one was named by
	// something and is not there. They call for opposite next steps — read the
	// object's events, or fix whatever names it — so the map draws them
	// differently rather than as one shade of "wrong".
	Missing bool
	// Group names the node whose children this is one of — the workload that
	// manages a pod, the pod that runs a container.
	//
	// FOR FOLDING, NOT FOR DRAWING. The graph itself is always complete: a
	// map that quietly omitted a replica would be a map nobody could trust.
	// This says which nodes form a sibling set, so the interface can collapse
	// thirty replicas into one box somebody expands when they want them. What
	// is drawn is the view's decision; what exists is not.
	//
	// Empty for anything with no natural set — the subject, a Service, an
	// Ingress, and attached resources, which are shared between pods and so
	// belong to no single one of them.
	Group string
}

// GraphEdge is a dependency, drawn from the thing that depends to the thing
// depended on — the direction a request travels.
type GraphEdge struct {
	From string
	To   string
	// Label names the relationship where it is not obvious: a port, a mount
	// path, "selects".
	Label string
}

// PodGraph is the dependency chain around one pod.
type PodGraph struct {
	Nodes []GraphNode
	Edges []GraphEdge
	// Unreadable names the sources that could not be read, so the map can say
	// it is incomplete rather than implying nothing is there. An account
	// without ingress permissions gets a map with no ingress tier, and must be
	// told that is what happened.
	Unreadable []string
	// Bounded is one short line saying why nothing is drawn BELOW the subject,
	// on a map whose subject has no cheap downward answer.
	//
	// NOT THE SAME THING AS Unreadable, and collapsing the two would be the
	// mistake this codebase keeps refusing to make elsewhere: Unreadable means
	// a read was attempted and refused, so a permission would fix it; this
	// means no read was attempted, deliberately, because the only way to
	// answer would be to list every kind in the namespace every time a drawer
	// opens. Empty on the pod and workload maps, whose shapes are complete by
	// construction.
	Bounded string
}

// ServiceRef is the little of a Service the map needs.
type ServiceRef struct {
	Name      string
	Namespace string
	Selector  map[string]string
	Type      string
	Ports     []string
}

// IngressRef is the little of an Ingress the map needs.
type IngressRef struct {
	Name      string
	Namespace string
	// Hosts are the rule hosts, for the label.
	Hosts []string
	// Backends names the services it routes to.
	Backends []string
}

// AttachedRef is something a pod consumes: a ConfigMap, Secret or claim.
type AttachedRef struct {
	Kind GraphKind
	Name string
	// Via says how it is consumed — a mount path, or "environment".
	Via string
}

// GraphInput is everything needed to draw one pod's map.
type GraphInput struct {
	Pod Pod
	// Services in the pod's namespace. Filtered here rather than by the
	// caller, because "does this selector match this pod" is a rule and
	// belongs where rules are tested.
	Services []ServiceRef
	// Ingresses in the pod's namespace, likewise filtered here.
	Ingresses []IngressRef
	// Owner is the controller chain above the pod, nearest first — usually
	// the ReplicaSet then the Deployment.
	Owner []OwnerReference
	// Attached is what the pod mounts or reads.
	Attached []AttachedRef
	// Unreadable names sources that failed.
	Unreadable []string
}

// NewPodGraph assembles the map.
//
// A PURE FUNCTION of what was read, so the rules that decide what connects to
// what — selector matching above all — are settled in a test rather than
// observed on somebody's cluster.
func NewPodGraph(input GraphInput) PodGraph {
	pod := input.Pod
	graph := PodGraph{Unreadable: input.Unreadable}

	podID := "pod/" + pod.Name()
	graph.Nodes = append(graph.Nodes, GraphNode{
		ID:        podID,
		Kind:      GraphPod,
		APIKind:   "Pod",
		Name:      pod.Name(),
		Namespace: pod.Namespace().String(),
		Tier:      TierPod,
		Detail:    string(pod.Phase()),
		Healthy:   pod.IsHealthy(),
		Subject:   true,
	})

	graph.addOwners(input.Owner, podID, pod.Namespace().String())
	graph.addServices(input, podID)
	graph.addContainers(pod, podID)
	graph.addAttached(input.Attached, podID)

	if node := pod.NodeName(); node != "" {
		hostID := "node/" + node
		graph.Nodes = append(graph.Nodes, GraphNode{
			ID: hostID, Kind: GraphHost, APIKind: "Node", Name: node,
			Tier: TierAttached, Detail: "scheduled on", Healthy: true,
		})
		graph.Edges = append(graph.Edges, GraphEdge{From: podID, To: hostID, Label: "runs on"})
	}

	graph.sort()
	return graph
}

// addOwners walks the controller chain upward from the pod.
func (g *PodGraph) addOwners(owners []OwnerReference, podID, namespace string) {
	below := podID

	for _, owner := range owners {
		kind := GraphWorkload
		tier := TierOwner
		if owner.Kind == "ReplicaSet" {
			kind = GraphReplicaSet
		}

		id := strings.ToLower(owner.Kind) + "/" + owner.Name
		g.Nodes = append(g.Nodes, GraphNode{
			ID: id, Kind: kind, APIKind: owner.Kind, Name: owner.Name,
			Namespace: namespace, Tier: tier, Healthy: true,
		})
		// From the owner DOWN to what it created: the edge follows control,
		// which for everything above the pod runs the same way as traffic.
		g.Edges = append(g.Edges, GraphEdge{From: id, To: below, Label: "manages"})
		below = id
	}
}

// addServices connects the services that select this pod, and the ingresses
// that route to those services.
func (g *PodGraph) addServices(input GraphInput, podID string) {
	labels := input.Pod.Labels()
	routed := make(map[string]string)

	for _, service := range input.Services {
		if !selectorMatches(service.Selector, labels) {
			continue
		}

		id := "service/" + service.Name
		routed[service.Name] = id

		g.Nodes = append(g.Nodes, GraphNode{
			ID: id, Kind: GraphService, APIKind: "Service", Name: service.Name,
			Namespace: service.Namespace, Tier: TierService,
			Detail:  strings.Join(service.Ports, ", "),
			Healthy: true,
		})
		g.Edges = append(g.Edges, GraphEdge{From: id, To: podID, Label: "selects"})
	}

	for _, ingress := range input.Ingresses {
		var reaches []string
		for _, backend := range ingress.Backends {
			if id, ok := routed[backend]; ok {
				reaches = append(reaches, id)
			}
		}
		if len(reaches) == 0 {
			// An ingress routing only to services that do not select this pod
			// is not this pod's ingress, and drawing it would imply a path
			// that does not exist.
			continue
		}

		id := "ingress/" + ingress.Name
		g.Nodes = append(g.Nodes, GraphNode{
			ID: id, Kind: GraphIngress, APIKind: "Ingress", Name: ingress.Name,
			Namespace: ingress.Namespace, Tier: TierIngress,
			Detail:  strings.Join(ingress.Hosts, ", "),
			Healthy: true,
		})
		for _, to := range reaches {
			g.Edges = append(g.Edges, GraphEdge{From: id, To: to, Label: "routes to"})
		}
	}
}

// addContainers hangs the pod's containers below it.
func (g *PodGraph) addContainers(pod Pod, podID string) {
	for _, container := range pod.Containers() {
		id := "container/" + container.Name
		g.Nodes = append(g.Nodes, GraphNode{
			ID: id, Kind: GraphContainer, Name: container.Name,
			Namespace: pod.Namespace().String(), Tier: TierContainer,
			Detail: imageTag(container.Image),
			// A container's health is the reader's starting point: this is the
			// tier where "something is wrong" usually resolves to a name.
			Healthy: container.Ready,
			Group:   podID,
		})
		g.Edges = append(g.Edges, GraphEdge{From: podID, To: id})
	}
}

// addAttached hangs what the pod consumes off to one side.
func (g *PodGraph) addAttached(attached []AttachedRef, podID string) {
	// ONE BOX PER RESOURCE, HOWEVER MANY PODS READ IT. Every replica of a
	// workload mounts the same ConfigMap, so this is called once per pod and
	// must add the node only the first time — otherwise three replicas draw
	// three identical boxes for one object.
	drawn := make(map[string]bool)
	for _, node := range g.Nodes {
		drawn[node.ID] = true
	}

	seen := make(map[string]bool)
	for _, ref := range attached {
		id := string(ref.Kind) + "/" + ref.Name
		if seen[id] {
			continue
		}
		seen[id] = true

		if !drawn[id] {
			g.Nodes = append(g.Nodes, GraphNode{
				ID: id, Kind: ref.Kind, APIKind: apiKindOf(ref.Kind), Name: ref.Name,
				Tier: TierAttached, Detail: ref.Via, Healthy: true,
			})
			drawn[id] = true
		}
		g.Edges = append(g.Edges, GraphEdge{From: podID, To: id, Label: ref.Via})
	}
}

// sort puts the graph in a stable order.
//
// So a refresh redraws the same picture. Without it the chart reshuffles on
// every poll — map iteration is unordered — and a map that moves while being
// read is worse than one that is a few seconds stale.
func (g *PodGraph) sort() {
	sort.SliceStable(g.Nodes, func(i, j int) bool {
		if g.Nodes[i].Tier != g.Nodes[j].Tier {
			return g.Nodes[i].Tier < g.Nodes[j].Tier
		}
		return g.Nodes[i].ID < g.Nodes[j].ID
	})
	sort.SliceStable(g.Edges, func(i, j int) bool {
		if g.Edges[i].From != g.Edges[j].From {
			return g.Edges[i].From < g.Edges[j].From
		}
		return g.Edges[i].To < g.Edges[j].To
	})
}

// selectorMatches reports whether a service's selector covers these labels.
//
// EVERY KEY MUST MATCH, and an EMPTY SELECTOR MATCHES NOTHING. The second is
// the one that matters: in the Kubernetes API an empty selector on a Service
// means the Service has no selector at all — an ExternalName, or one whose
// Endpoints are managed by hand — and treating it as "matches everything"
// would draw an edge from it to every pod in the namespace.
func selectorMatches(selector, labels map[string]string) bool {
	if len(selector) == 0 {
		return false
	}
	for key, want := range selector {
		if labels[key] != want {
			return false
		}
	}
	return true
}

// imageTag shortens an image reference to what identifies it.
//
// A registry path is most of the width of a box and none of the information:
// two containers differ by their tag or digest, not by the host they were
// pulled from.
func imageTag(image string) string {
	if image == "" {
		return ""
	}
	if at := strings.LastIndex(image, "@"); at >= 0 {
		digest := image[at+1:]
		if len(digest) > 19 {
			digest = digest[:19] + "…"
		}
		return digest
	}
	if colon := strings.LastIndex(image, ":"); colon > strings.LastIndex(image, "/") {
		return image[colon+1:]
	}
	return "latest"
}

// apiKindOf maps a graph kind back to the Kubernetes kind, for following.
func apiKindOf(kind GraphKind) string {
	switch kind {
	case GraphConfig:
		return "ConfigMap"
	case GraphSecret:
		return "Secret"
	case GraphClaim:
		return "PersistentVolumeClaim"
	case GraphServiceAccount:
		return "ServiceAccount"
	default:
		return ""
	}
}

// String renders a graph for a log line or a test failure.
func (g PodGraph) String() string {
	return fmt.Sprintf("%d nodes, %d edges", len(g.Nodes), len(g.Edges))
}

// WorkloadGraphInput is everything needed to draw one workload's map.
//
// SEPARATE FROM GraphInput BECAUSE THE SUBJECT IS DIFFERENT, and the subject
// decides the shape. A pod's map is a chain with the pod in the middle; a
// workload's is a fan — one controller over however many pods it currently
// has — and pretending they are the same structure would mean a pod field
// that is sometimes a list and edges that mean different things depending on
// which it was.
type WorkloadGraphInput struct {
	// Kind and Name identify the workload the map is drawn around.
	Kind      string
	Name      string
	Namespace NamespaceName
	// Healthy drives the colour of the subject box.
	Healthy bool
	// Pods are the pods it currently has. Empty is a real state — a scaled-to-
	// zero Deployment, or a CronJob between runs — and draws a workload with
	// nothing under it rather than an error.
	Pods []Pod
	// Intermediates are the controllers BETWEEN the workload and its pods: the
	// Jobs a CronJob has created.
	//
	// A CronJob does not own pods. It owns Jobs, and those own pods, so a map
	// that hung pods straight off the CronJob would draw a relationship
	// Kubernetes does not have — and would lose the only thing that says which
	// run a pod belongs to.
	Intermediates []IntermediateRef
	// Services and Ingresses in the namespace, filtered here.
	Services  []ServiceRef
	Ingresses []IngressRef
	// Owner is what controls the workload, if anything — a Job's CronJob.
	Owner []OwnerReference
	// Attached is what the pod template mounts or reads.
	Attached []AttachedRef
	// Unreadable names sources that failed.
	Unreadable []string
}

// IntermediateRef is a controller between a workload and its pods.
type IntermediateRef struct {
	Kind string
	Name string
	// Pods names the pods this one owns.
	Pods []string
}

// NewWorkloadGraph assembles the map around a workload.
//
// The same rules as a pod's map, applied to a fan instead of a chain: a
// service matches on the labels of the pods BENEATH the workload rather than
// the workload's own, because a Service selects pods and knows nothing about
// what created them.
func NewWorkloadGraph(input WorkloadGraphInput) PodGraph {
	graph := PodGraph{Unreadable: input.Unreadable}

	subjectID := strings.ToLower(input.Kind) + "/" + input.Name
	graph.Nodes = append(graph.Nodes, GraphNode{
		ID:        subjectID,
		Kind:      GraphWorkload,
		APIKind:   input.Kind,
		Name:      input.Name,
		Namespace: input.Namespace.String(),
		Tier:      TierOwner,
		Detail:    replicaSummary(input.Pods),
		Healthy:   input.Healthy,
		Subject:   true,
	})

	graph.addOwners(input.Owner, subjectID, input.Namespace.String())

	// The controllers between, when there are any, and which pod belongs to
	// which of them.
	parentOf := make(map[string]string)
	for _, mid := range input.Intermediates {
		id := strings.ToLower(mid.Kind) + "/" + mid.Name
		graph.Nodes = append(graph.Nodes, GraphNode{
			ID: id, Kind: GraphReplicaSet, APIKind: mid.Kind, Name: mid.Name,
			Namespace: input.Namespace.String(), Tier: TierOwner, Healthy: true,
		})
		graph.Edges = append(graph.Edges, GraphEdge{From: subjectID, To: id, Label: "creates"})

		for _, pod := range mid.Pods {
			parentOf[pod] = id
		}
	}

	// The pods, and their containers under them.
	for _, pod := range input.Pods {
		podID := "pod/" + pod.Name()
		// From whatever actually created it: the Job for a CronJob's pods, the
		// workload itself for everything else.
		from := subjectID
		if parent, ok := parentOf[pod.Name()]; ok {
			from = parent
		}

		graph.Nodes = append(graph.Nodes, GraphNode{
			ID:        podID,
			Kind:      GraphPod,
			APIKind:   "Pod",
			Name:      pod.Name(),
			Namespace: pod.Namespace().String(),
			Tier:      TierPod,
			Detail:    string(pod.Phase()),
			Healthy:   pod.IsHealthy(),
			Group:     from,
		})
		graph.Edges = append(graph.Edges, GraphEdge{From: from, To: podID, Label: "manages"})

		// CONTAINERS ARE KEYED BY POD, unlike a pod's own map. Every replica
		// of a Deployment runs containers with the same names, so keying on
		// the name alone would collapse three replicas' containers into one
		// box with three edges into it.
		for _, container := range pod.Containers() {
			id := "container/" + pod.Name() + "/" + container.Name
			graph.Nodes = append(graph.Nodes, GraphNode{
				ID: id, Kind: GraphContainer, Name: container.Name,
				Namespace: pod.Namespace().String(), Tier: TierContainer,
				Detail: imageTag(container.Image), Healthy: container.Ready,
				Group: podID,
			})
			graph.Edges = append(graph.Edges, GraphEdge{From: podID, To: id})
		}
	}

	graph.addWorkloadServices(input)

	// ATTACHED BELONGS TO THE PODS, because the pod is what mounts it. A
	// ReplicaSet does not read a Secret: it carries a template that DECLARES
	// what the pods it creates will read, and drawing an edge from the
	// controller asserts a relationship Kubernetes does not have. It was
	// convenient — one edge instead of one per replica — and convenience is
	// not a reason to draw something untrue on a diagram people use to reason
	// about a cluster.
	//
	// With no pods there is nothing mounting anything, and the only true
	// statement left is that the workload's template declares them. That is
	// what the edge from the subject means in that case, and only that case.
	if len(input.Pods) == 0 {
		graph.addAttached(input.Attached, subjectID)
	} else {
		for _, pod := range input.Pods {
			graph.addAttached(input.Attached, "pod/"+pod.Name())
		}
	}

	graph.sort()
	return graph
}

// addWorkloadServices connects services selecting any pod of the workload.
func (g *PodGraph) addWorkloadServices(input WorkloadGraphInput) {
	routed := make(map[string]string)

	for _, service := range input.Services {
		// TO THE PODS IT SELECTS, not to the workload above them. A Service
		// matches labels on PODS and knows nothing about what created them —
		// so an edge to the controller is a relationship Kubernetes does not
		// have, and it hides the case that matters most: a Service that
		// reaches only SOME of a workload's pods, which is what a half-failed
		// rollout looks like.
		var selected []string
		for _, pod := range input.Pods {
			if selectorMatches(service.Selector, pod.Labels()) {
				selected = append(selected, "pod/"+pod.Name())
			}
		}

		// With no pods there is nothing to select, and a Service drawn against
		// a workload it cannot currently reach would assert a path that does
		// not exist.
		if len(selected) == 0 {
			continue
		}

		id := "service/" + service.Name
		routed[service.Name] = id

		g.Nodes = append(g.Nodes, GraphNode{
			ID: id, Kind: GraphService, APIKind: "Service", Name: service.Name,
			Namespace: service.Namespace, Tier: TierService,
			Detail: strings.Join(service.Ports, ", "), Healthy: true,
		})
		for _, podID := range selected {
			g.Edges = append(g.Edges, GraphEdge{From: id, To: podID, Label: "selects"})
		}
	}

	for _, ingress := range input.Ingresses {
		var reaches []string
		for _, backend := range ingress.Backends {
			if id, ok := routed[backend]; ok {
				reaches = append(reaches, id)
			}
		}
		if len(reaches) == 0 {
			continue
		}

		id := "ingress/" + ingress.Name
		g.Nodes = append(g.Nodes, GraphNode{
			ID: id, Kind: GraphIngress, APIKind: "Ingress", Name: ingress.Name,
			Namespace: ingress.Namespace, Tier: TierIngress,
			Detail: strings.Join(ingress.Hosts, ", "), Healthy: true,
		})
		for _, to := range reaches {
			g.Edges = append(g.Edges, GraphEdge{From: id, To: to, Label: "routes to"})
		}
	}
}

// replicaSummary says how many pods a workload has and how many are well.
func replicaSummary(pods []Pod) string {
	if len(pods) == 0 {
		return "no pods"
	}

	healthy := 0
	for _, pod := range pods {
		if pod.IsHealthy() {
			healthy++
		}
	}
	return fmt.Sprintf("%d/%d ready", healthy, len(pods))
}
