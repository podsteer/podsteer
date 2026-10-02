package domain

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrEmptyApplicationInstance is returned when an application's map or pods are
// asked for without the instance label value that defines it. An empty
// selector value would match every object that carries the label at all.
var ErrEmptyApplicationInstance = errors.New("application instance must not be empty")

// ErrInvalidApplicationInstance is returned when the instance is not a legal
// label value, so it could not have been written on any object.
var ErrInvalidApplicationInstance = errors.New("application instance is not a valid label value")

// ApplicationGraphInput is everything needed to draw one application's map.
//
// A FOURTH SHAPE, because the subject is a set rather than an object. A pod's
// map is a chain, a workload's a fan, anything else's a neighbourhood; an
// application is "everything carrying this label, plus what those objects
// own", and has no single object to put in the middle.
type ApplicationGraphInput struct {
	// Instance is the app.kubernetes.io/instance value that defines the
	// application.
	Instance  string
	Namespace NamespaceName
	// Candidates are the labelled top-level kinds AND every ReplicaSet and Job
	// in the namespace. The adapter cannot know which of the latter belong —
	// a CronJob's Jobs are usually unlabelled — so membership is decided here.
	Candidates []ApplicationCandidate
	// Pods is every pod in the namespace; membership is decided here too.
	Pods     []Pod
	Services []ServiceRef
	// Ingresses are drawn only when they route to a drawn Service.
	Ingresses []IngressRef
	// Unreadable names sources that failed.
	Unreadable []string
}

// ApplicationCandidate is a controller that may belong to the application.
type ApplicationCandidate struct {
	// Kind is the Kubernetes kind: Deployment, StatefulSet, DaemonSet,
	// CronJob, ReplicaSet or Job.
	Kind   string
	Name   string
	Labels map[string]string
	// Owner is the controlling owner, zero when nothing controls it.
	Owner OwnerReference
	// Desired, Ready and Failed are the counts the node's health is read from.
	Desired, Ready, Failed int32
	Suspended              bool
	// Attached is what the object's own pod template mounts or reads.
	Attached []AttachedRef
}

func (c ApplicationCandidate) id() string { return strings.ToLower(c.Kind) + "/" + c.Name }

// applicationMembers decides what belongs to the application.
//
// THE LABEL SEEDS, OWNERSHIP EXTENDS. The label is the application's
// definition, but a CronJob's Jobs carry the jobTemplate's labels and are
// usually unlabelled, and under the ownership rule (a pod belongs to the
// controller that OWNS it) their pods are the application's. So a candidate
// owned by a member is a member, repeated until nothing changes. A candidate
// that is neither labelled nor owned by a member is not: every ReplicaSet in
// the namespace is offered and most are somebody else's.
func applicationMembers(input ApplicationGraphInput) (members map[string]ApplicationCandidate, pods []Pod) {
	members = make(map[string]ApplicationCandidate)
	for _, c := range input.Candidates {
		if c.Labels[LabelInstance] == input.Instance {
			members[c.id()] = c
		}
	}

	for changed := true; changed; {
		changed = false
		for _, c := range input.Candidates {
			if _, in := members[c.id()]; in || c.Owner.IsZero() {
				continue
			}
			if _, owned := members[strings.ToLower(c.Owner.Kind)+"/"+c.Owner.Name]; owned {
				members[c.id()] = c
				changed = true
			}
		}
	}

	for _, pod := range input.Pods {
		if pod.Labels()[LabelInstance] == input.Instance {
			pods = append(pods, pod)
			continue
		}
		if owner := pod.Controller(); !owner.IsZero() {
			if _, owned := members[strings.ToLower(owner.Kind)+"/"+owner.Name]; owned {
				pods = append(pods, pod)
			}
		}
	}
	return members, pods
}

// ApplicationPods returns the pods the application's map draws, by the same
// rule, so the Logs tab and the Map tab cannot disagree about what is in it.
func ApplicationPods(input ApplicationGraphInput) []Pod {
	_, pods := applicationMembers(input)
	sort.SliceStable(pods, func(i, j int) bool { return pods[i].Name() < pods[j].Name() })
	return pods
}

// NewApplicationGraph assembles the map around an application.
//
// EVERY EDGE IS A RELATIONSHIP KUBERNETES HAS, and there is NO ROOT NODE. An
// edge from "the application" would assert a label as a relationship, which
// is the thing the GitOps panels refuse to do; the pane's caption says what
// the label is instead. A pod with no member owner is drawn with no edge for
// the same reason. There are no container nodes — the pod's own map has them.
func NewApplicationGraph(input ApplicationGraphInput) PodGraph {
	graph := PodGraph{Unreadable: input.Unreadable}
	namespace := input.Namespace.String()

	members, pods := applicationMembers(input)

	// Which members are owned by another member: those fold under their owner
	// rather than standing as workloads of their own.
	ownerOf := make(map[string]string)
	for id, c := range members {
		if c.Owner.IsZero() {
			continue
		}
		if parent := strings.ToLower(c.Owner.Kind) + "/" + c.Owner.Name; members[parent].Name != "" {
			ownerOf[id] = parent
		}
	}

	// A pod's parent is its controller, when that is a member.
	parentOf := make(map[string]string)
	podCount := make(map[string]int)
	for _, pod := range pods {
		owner := pod.Controller()
		if owner.IsZero() {
			continue
		}
		parent := strings.ToLower(owner.Kind) + "/" + owner.Name
		if _, in := members[parent]; !in {
			continue
		}
		parentOf[pod.Name()] = parent
		// Counted up the whole chain: a Deployment whose only pods sit under a
		// ReplicaSet still has pods.
		// Bounded, because Kubernetes does not forbid an ownerReference cycle.
		for id, hops := parent, 0; id != "" && hops <= len(members); id, hops = ownerOf[id], hops+1 {
			podCount[id]++
		}
	}

	for id, c := range members {
		node := GraphNode{
			ID: id, Kind: GraphWorkload, APIKind: c.Kind, Name: c.Name,
			Namespace: namespace, Tier: TierOwner,
		}
		node.Healthy, node.Detail = applicationHealth(c)

		if parent, owned := ownerOf[id]; owned {
			// FOLDED PER OWNER: ten old ReplicaSets or twenty finished Jobs
			// are one sibling set, and graphFold collapses past five.
			node.Kind = GraphReplicaSet
			node.Group = parent
			graph.Edges = append(graph.Edges, GraphEdge{From: parent, To: id, Label: "creates"})
		}
		graph.Nodes = append(graph.Nodes, node)
	}

	for _, pod := range pods {
		podID := "pod/" + pod.Name()
		parent := parentOf[pod.Name()]

		graph.Nodes = append(graph.Nodes, GraphNode{
			ID: podID, Kind: GraphPod, APIKind: "Pod", Name: pod.Name(),
			Namespace: pod.Namespace().String(), Tier: TierPod,
			Detail: string(pod.Phase()), Healthy: pod.IsHealthy(), Group: parent,
		})
		if parent != "" {
			graph.Edges = append(graph.Edges, GraphEdge{From: parent, To: podID, Label: "manages"})
		}
	}

	graph.addSelectedServices(pods, input.Services, input.Ingresses, func(s ServiceRef) bool {
		return s.Labels[LabelInstance] == input.Instance
	})

	// ATTACHED BELONGS TO THE PODS, from the template of the controller that
	// created each (see NewWorkloadGraph). A member with no pods beneath it
	// declares its template's attachments itself — but only a TOP-LEVEL one:
	// an old zero-replica ReplicaSet declares nothing its Deployment does not.
	for _, pod := range pods {
		if parent := parentOf[pod.Name()]; parent != "" {
			graph.addAttached(members[parent].Attached, "pod/"+pod.Name())
		}
	}
	for id, c := range members {
		if _, owned := ownerOf[id]; !owned && podCount[id] == 0 {
			graph.addAttached(c.Attached, id)
		}
	}

	graph.sort()
	return graph
}

// applicationHealth derives a member's colour and its short qualifier.
func applicationHealth(c ApplicationCandidate) (bool, string) {
	switch c.Kind {
	case "CronJob":
		// A schedule is not a thing that is up or down; its Jobs carry that.
		if c.Suspended {
			return true, "suspended"
		}
		return true, "scheduled"
	case "Job":
		// Judged by failure, not by finishing — see Workload.IsHealthy.
		return c.Failed == 0, fmt.Sprintf("%d/%d", c.Ready, c.Desired)
	default:
		return c.Ready >= c.Desired, fmt.Sprintf("%d/%d ready", c.Ready, c.Desired)
	}
}
