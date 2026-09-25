package domain

import (
	"fmt"
	"strings"
)

// ObjectReference is one object that another object names.
//
// EVERY ONE OF THESE IS A RELATIONSHIP KUBERNETES ACTUALLY HAS: an
// ownerReference, a controller's own status, or a field in a spec that names
// an object by name. Never a label heuristic, and never "the same name in the
// same namespace" — see the dependency-map section of CLAUDE.md, which exists
// because breaking that rule is always the convenient thing to do.
type ObjectReference struct {
	// Group is the referenced object's API group, empty for the core group.
	Group string
	// Version is the API version within that group.
	//
	// EMPTY IS A REAL STATE, not a missing one: several Kubernetes reference
	// fields name only an apiGroup — a roleRef's, a PVC data source's, an
	// Ingress resource backend's — because the version is whichever the
	// cluster serves. The RESTMapper picks it, which is also what makes a
	// reference to a CRD resolve on a cluster that has moved it to v1beta2.
	Version string
	// Kind is the Kubernetes kind VERBATIM — "ConfigMap", not "configmaps".
	// The drawer resolves a followed node against the navigator's catalogue,
	// which is keyed by Kind, so a lowercased plural matches nothing and the
	// click silently does nothing at all.
	Kind string
	Name string
	// Namespace is empty for a cluster-scoped reference. A namespaced
	// reference from a namespaced object is always in the SAME namespace:
	// Kubernetes has no cross-namespace reference except a PersistentVolume's
	// claimRef, which carries its own.
	Namespace string
	// Via names the field that made the reference — "storage class",
	// "tls certificate". It is the edge label, so it says WHY the line is
	// there rather than repeating what the box already shows.
	Via string
	// Missing is true when the reference was resolved against the cluster and
	// the object was not there.
	//
	// A DANGLING REFERENCE IS OFTEN THE ANSWER SOMEBODY OPENED THE MAP FOR —
	// an Ingress naming a Service that was renamed, a PVC waiting on a
	// StorageClass nobody installed — so it is drawn as a box marked missing
	// rather than dropped. Dropping it renders the broken case and the
	// working case identically, which is the one thing a dependency map must
	// never do.
	Missing bool
}

// ObjectGraphInput is everything needed to draw the neighbourhood of one
// object of ANY kind.
//
// THE THIRD SHAPE, and separate from the other two for the same reason they
// are separate from each other: the subject decides the structure. A pod's map
// is a chain, a workload's is a fan, and a general object's is a
// neighbourhood — the subject in the middle, what it names below it, what owns
// it above. A Service, a ConfigMap, a PVC and a CRD instance have nothing in
// common except that each names some objects and is named by others, so that
// is the only structure this one asserts.
type ObjectGraphInput struct {
	// Kind, Name and Namespace identify the subject. Namespace is empty for a
	// cluster-scoped object.
	Kind      string
	Name      string
	Namespace string
	// Owners is the ownerReference chain above the subject, NEAREST FIRST.
	// Already resolved by the caller, one read per hop.
	Owners []ObjectReference
	// References is what the subject's own spec names, in the order
	// ReferencesFromManifest produced them.
	References []ObjectReference
	// Selector is the subject's pod selector where its kind has one — a
	// Service's spec.selector, and nothing else today.
	//
	// EMPTY MEANS THE SUBJECT SELECTS NOTHING, which is the rule
	// selectorMatches enforces and the reason this is passed rather than
	// matched by the caller.
	Selector map[string]string
	// Pods are the pods in the subject's namespace, for a Selector to match
	// against. Empty when the subject's kind has no selector, because nothing
	// then justifies listing them.
	Pods []Pod
	// Unreadable names the sources that could not be read, so the map can say
	// it is incomplete rather than implying nothing is there.
	Unreadable []string
}

// ObjectOwnerDepth is how far up an ownerReference chain a map walks.
//
// THREE HOPS, AND THE CAP IS THE POINT. Upward navigation is cheap — one GET
// per hop against a name Kubernetes already wrote onto the object — but it is
// only cheap while it is bounded. Kubernetes does not forbid an
// ownerReference cycle, an operator can nest controllers as deep as it likes,
// and a drawer that opens by walking an unbounded chain is a drawer that can
// be made to issue reads without limit by an object somebody else created.
// Three covers everything the built-in controllers produce (pod → ReplicaSet →
// Deployment is two) with a hop to spare for an operator that inserts one.
const ObjectOwnerDepth = 3

// NewObjectGraph assembles the neighbourhood of one object.
//
// A PURE FUNCTION of what was read, like the other two, so every rule about
// what connects to what is settled in a test rather than observed on
// somebody's cluster.
func NewObjectGraph(input ObjectGraphInput) PodGraph {
	graph := PodGraph{Unreadable: input.Unreadable}

	subjectID := objectNodeID(input.Kind, input.Namespace, input.Name)
	graph.Nodes = append(graph.Nodes, GraphNode{
		ID:        subjectID,
		Kind:      GraphKindOf(input.Kind),
		APIKind:   input.Kind,
		Name:      input.Name,
		Namespace: input.Namespace,
		Tier:      TierPod,
		// NO HEALTH VERDICT. Kubernetes has no general notion of whether an
		// arbitrary object is well, and inventing one from a status condition
		// nobody agreed the meaning of would be a verdict this layer has no
		// business reaching. The colour is reserved for the two things here
		// that DO have an answer: a pod's readiness, and a reference that
		// resolved to nothing.
		Healthy: true,
		Subject: true,
	})

	drawn := map[string]bool{subjectID: true}

	graph.addObjectOwners(input.Owners, subjectID, drawn)
	members := graph.addSelected(input, subjectID, drawn)
	graph.addObjectReferences(input.References, subjectID, drawn)

	if members == 0 {
		graph.Bounded = DownwardBound(input.Kind)
	}

	graph.sort()
	return graph
}

// addObjectOwners walks the ownerReference chain upward from the subject.
//
// UPWARD IS THE FREE DIRECTION: the names are already on the object, so each
// hop is one GET of a name Kubernetes itself wrote. Bounded anyway — see
// ObjectOwnerDepth — and stopped early at the first owner already on the map,
// which is what terminates a cycle: an object owning something that owns it
// back arrives here as a chain that repeats, and the second sighting ends it
// rather than drawing a second box and a second edge for the same object.
func (g *PodGraph) addObjectOwners(owners []ObjectReference, subjectID string, drawn map[string]bool) {
	below := subjectID

	for hop, owner := range owners {
		if hop >= ObjectOwnerDepth {
			return
		}

		id := objectNodeID(owner.Kind, owner.Namespace, owner.Name)
		if drawn[id] {
			return
		}
		drawn[id] = true

		// Each hop sits one tier higher, so the chain reads upward whatever
		// its length. Floored at TierIngress because ObjectOwnerDepth stops
		// the walk before it could run off the top anyway.
		tier := TierOwner - GraphTier(hop)
		if tier < TierIngress {
			tier = TierIngress
		}

		g.Nodes = append(g.Nodes, GraphNode{
			ID: id, Kind: GraphKindOf(owner.Kind), APIKind: owner.Kind,
			Name: owner.Name, Namespace: owner.Namespace, Tier: tier,
			Healthy: true, Missing: owner.Missing,
		})
		// From the owner DOWN to what it owns: the edge follows control, the
		// same direction the other two maps draw it.
		g.Edges = append(g.Edges, GraphEdge{From: id, To: below, Label: "owns"})
		below = id
	}
}

// addSelected hangs the pods the subject's selector actually matches beneath
// it, and reports how many there were.
//
// THE ONE DOWNWARD ANSWER KUBERNETES GIVES CHEAPLY for a general object: a
// Service's selector against one list of one kind in one namespace. Everything
// else somebody might want below an object — which pods mount this ConfigMap,
// which PVCs use this StorageClass — has no server-side index at all, and
// answering it would mean listing every kind in the namespace on every drawer
// open. That is the polling storm the read-cache section of CLAUDE.md exists
// to prevent, so it is not done and DownwardBound says so instead.
func (g *PodGraph) addSelected(input ObjectGraphInput, subjectID string, drawn map[string]bool) int {
	matched := 0

	for _, pod := range input.Pods {
		// AN EMPTY SELECTOR MATCHES NOTHING. In the Kubernetes API an empty
		// selector on a Service means it has none at all — an ExternalName, or
		// Endpoints managed by hand — and reading it as "matches everything"
		// draws an edge to every pod in the namespace.
		if !selectorMatches(input.Selector, pod.Labels()) {
			continue
		}

		id := objectNodeID("Pod", pod.Namespace().String(), pod.Name())
		if drawn[id] {
			continue
		}
		drawn[id] = true
		matched++

		g.Nodes = append(g.Nodes, GraphNode{
			ID: id, Kind: GraphPod, APIKind: "Pod", Name: pod.Name(),
			Namespace: pod.Namespace().String(), Tier: TierContainer,
			Detail: string(pod.Phase()), Healthy: pod.IsHealthy(),
			// A sibling set, so thirty selected pods fold into one box. The
			// graph still carries all thirty — folding is a view decision.
			Group: subjectID,
		})
		g.Edges = append(g.Edges, GraphEdge{From: subjectID, To: id, Label: "selects"})
	}

	return matched
}

// addObjectReferences hangs what the subject's spec names off to one side.
func (g *PodGraph) addObjectReferences(references []ObjectReference, subjectID string, drawn map[string]bool) {
	// ONE BOX PER REFERENCED OBJECT, however many fields name it. An Ingress
	// with twelve paths onto one Service must draw one box with one line, not
	// twelve of each.
	for _, ref := range references {
		if ref.Name == "" {
			continue
		}

		id := objectNodeID(ref.Kind, ref.Namespace, ref.Name)
		if !drawn[id] {
			drawn[id] = true
			g.Nodes = append(g.Nodes, GraphNode{
				ID: id, Kind: GraphKindOf(ref.Kind), APIKind: ref.Kind,
				Name: ref.Name, Namespace: ref.Namespace, Tier: TierAttached,
				Detail: missingDetail(ref),
				// A reference resolving to nothing is worth the colour: it is
				// the difference between a map that shows a dependency and one
				// that shows a broken one.
				Healthy: !ref.Missing,
				Missing: ref.Missing,
				Group:   subjectID,
			})
		}

		g.Edges = append(g.Edges, GraphEdge{From: subjectID, To: id, Label: ref.Via})
	}
}

// missingDetail is what a referenced box says under its name.
func missingDetail(ref ObjectReference) string {
	if ref.Missing {
		return "not found"
	}
	return ""
}

// DownwardBound is the one short line a map shows when nothing is drawn BELOW
// the subject because Kubernetes offers no cheap way to find it.
//
// SAYING WHY IS THE WHOLE POINT. An empty space under an object reads as "this
// object has nothing depending on it", which is a claim nothing here checked —
// and it is exactly the reading MetricsStatus and ClusterReadStatus exist
// elsewhere in this codebase to prevent. Kinds that DO have a cheap downward
// answer get no line, because for them the empty space is a real answer.
func DownwardBound(kind string) string {
	if _, cheap := downwardKinds[kind]; cheap {
		return ""
	}
	return fmt.Sprintf(
		"Kubernetes does not index what refers to a %s, so nothing is drawn below it.", kind)
}

// downwardKinds are the kinds whose members Kubernetes reports cheaply — one
// list of one kind in one namespace, narrowed by something the API server
// itself indexes.
//
// A map rather than a switch so DownwardBound and the adapter that does the
// reading cannot drift into disagreeing about which kinds have an answer.
var downwardKinds = map[string]struct{}{
	"Service": {},
}

// HasDownwardAnswer reports whether a kind's members can be read cheaply, so
// the adapter reads only for those and never speculatively for the rest.
func HasDownwardAnswer(kind string) bool {
	_, cheap := downwardKinds[kind]
	return cheap
}

// objectNodeID is a node id unique within one object's map.
//
// Namespaced because a neighbourhood can reach a cluster-scoped object beside
// a namespaced one of the same kind and name — a PersistentVolume and a PVC
// are different kinds, but an operator's CRD instances are not so reliably
// distinguishable. Never displayed; only ever matched against itself.
func objectNodeID(kind, namespace, name string) string {
	if namespace == "" {
		return strings.ToLower(kind) + "/" + name
	}
	return strings.ToLower(kind) + "/" + namespace + "/" + name
}

// GraphKindOf maps a Kubernetes kind onto the map's own coarse category, which
// is what decides the icon.
//
// THE COARSENESS IS DELIBERATE — see GraphKind. A reader following a reference
// does not need a StatefulSet distinguished from a Deployment at that moment,
// and anything the map has no category for is drawn as a plain object rather
// than borrowed onto a category it does not belong to. Miscategorising a CRD
// instance as a workload would put a Deployment's icon on something that is
// not one, which is worse than a neutral box.
func GraphKindOf(kind string) GraphKind {
	switch kind {
	case "Pod":
		return GraphPod
	case "Service":
		return GraphService
	case "Ingress":
		return GraphIngress
	case "Node":
		return GraphHost
	case "ConfigMap":
		return GraphConfig
	case "Secret":
		return GraphSecret
	case "PersistentVolumeClaim", "PersistentVolume":
		return GraphClaim
	case "ServiceAccount":
		return GraphServiceAccount
	case "ReplicaSet":
		return GraphReplicaSet
	case "Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob":
		return GraphWorkload
	default:
		return GraphObject
	}
}
