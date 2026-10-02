package wails

import (
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// The namespace topology's wire shape. The names here are the contract in
// web/src/lib/topology/contract.ts, which re-exports these generated types —
// change one and the other changes with it.

// NodeState is how healthy a box is: ok, warn, bad or neutral (domain.NodeState
// names them). `neutral` means nothing was checked, not that it is fine.
//
// NO GO CONSTANTS, DELIBERATELY: the binding generator turns a string type
// with constants into a TypeScript enum, which a contract written as a string
// union cannot accept. Without them it is `string`, which the contract
// narrows.
type NodeState string

// TopologyEdgeKind is the relationship an edge draws: owns, selects, routes,
// scales, protects, policy-selects, attaches or runs-as (domain.EdgeKind
// names them). No constants, for the reason NodeState has none.
type TopologyEdgeKind string

// TopologyPodSummary is a pod set folded by the backend above the summary cap. The
// counts are complete.
type TopologyPodSummary struct {
	Total     int `json:"total"`
	Ready     int `json:"ready"`
	Unhealthy int `json:"unhealthy"`
	// Members are the folded pods' names, sorted — what a finding about one
	// of them is matched against.
	Members []string `json:"members,omitempty"`
}

// TopologyNode is one box on the topology.
type TopologyNode struct {
	ID string `json:"id"`
	// Kind is the graph kind: pod, workload, replicaset, service, ingress,
	// gateway, route, scaler, budget, policy, config, secret, claim,
	// serviceaccount, node, object.
	Kind string `json:"kind"`
	// APIKind is the Kubernetes Kind, verbatim, for navigation.
	APIKind   string    `json:"apiKind"`
	Name      string    `json:"name"`
	Namespace string    `json:"namespace"`
	State     NodeState `json:"state"`
	Detail    string    `json:"detail"`
	// Group is the sibling set for folding, as in the other map shapes.
	Group string `json:"group"`
	// Labels are set on top-level objects only, for grouping by app or label.
	Labels map[string]string `json:"labels,omitempty"`
	// PodSummary is set only on a backend-folded pod set.
	PodSummary *TopologyPodSummary `json:"podSummary,omitempty"`
}

// TopologyEdge is one relationship.
type TopologyEdge struct {
	From  string           `json:"from"`
	To    string           `json:"to"`
	Kind  TopologyEdgeKind `json:"kind"`
	Label string           `json:"label"`
}

// TopologyGraph is a scope of namespaces and everything between its objects.
type TopologyGraph struct {
	Nodes []TopologyNode `json:"nodes"`
	Edges []TopologyEdge `json:"edges"`
	// Counts is per Kubernetes Kind, COMPLETE even when pods are summarised.
	Counts     map[string]int `json:"counts"`
	Unreadable []string       `json:"unreadable"`
	Bounded    string         `json:"bounded"`
	// Summarised is true when pods were folded in the backend because there
	// were too many to draw.
	Summarised bool `json:"summarised"`
	// GeneratedAt is when the sources were read, RFC 3339.
	GeneratedAt string `json:"generatedAt"`
}

// TopologyChanged is the payload of the `topology:changed` event.
type TopologyChanged struct {
	ClusterID string `json:"clusterId"`
	// Namespaces are where it changed; empty means somewhere in an
	// all-namespaces scope.
	Namespaces []string `json:"namespaces"`
}

func toTopologyGraph(graph domain.TopologyGraph) TopologyGraph {
	out := TopologyGraph{
		Nodes:      make([]TopologyNode, 0, len(graph.Nodes)),
		Edges:      make([]TopologyEdge, 0, len(graph.Edges)),
		Counts:     graph.Counts,
		Unreadable: graph.Unreadable,
		Bounded:    graph.Bounded,
		Summarised: graph.Summarised,
	}
	if !graph.GeneratedAt.IsZero() {
		out.GeneratedAt = graph.GeneratedAt.UTC().Format(time.RFC3339)
	}
	for _, node := range graph.Nodes {
		dto := TopologyNode{
			ID: node.ID, Kind: string(node.Kind), APIKind: node.APIKind, Name: node.Name,
			Namespace: node.Namespace, State: NodeState(node.State), Detail: node.Detail,
			Group: node.Group, Labels: node.Labels,
		}
		if s := node.PodSummary; s != nil {
			dto.PodSummary = &TopologyPodSummary{Total: s.Total, Ready: s.Ready, Unhealthy: s.Unhealthy, Members: s.Members}
		}
		out.Nodes = append(out.Nodes, dto)
	}
	for _, edge := range graph.Edges {
		out.Edges = append(out.Edges, TopologyEdge{
			From: edge.From, To: edge.To, Kind: TopologyEdgeKind(edge.Kind), Label: edge.Label,
		})
	}
	if out.Counts == nil {
		out.Counts = map[string]int{}
	}
	if out.Unreadable == nil {
		out.Unreadable = []string{}
	}
	return out
}

func toTopologyChanged(change domain.ClusterChange) TopologyChanged {
	out := TopologyChanged{ClusterID: change.ClusterID.String(), Namespaces: make([]string, 0, len(change.Namespaces))}
	for _, ns := range change.Namespaces {
		out.Namespaces = append(out.Namespaces, ns.String())
	}
	return out
}
