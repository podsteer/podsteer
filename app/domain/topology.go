package domain

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// The namespace topology: the FIFTH map shape.
//
// The other four are drawn around a subject — a pod's chain, a workload's fan,
// an object's neighbourhood, an application's set — and this one has none. It
// is everything in a scope of namespaces and every relationship Kubernetes
// itself has between those things, so it is its own type rather than a
// widened PodGraph: a PodGraph has a subject and tiers, and a topology has
// neither. Its edges carry a Kind so the page can toggle a relationship off,
// and its nodes a tri-state State, because "nothing was checked" is not the
// same statement as "it is fine".
//
// EVERY EDGE IS STILL A RELATIONSHIP KUBERNETES HAS. A NetworkPolicy is drawn
// as SELECTING its pods and nothing more — what it allows or blocks is a
// question about every other policy in the namespace and the CNI enforcing
// them, and a line on a diagram would answer it wrongly. ConfigMaps, Secrets
// and claims are NAMES from pod templates, never read, which Bounded says.

// TopologyPodCap is how many pods a topology draws one box each for. Above it
// the pods are folded HERE, in the backend, into one box per sibling set with
// complete counts — the one place the "the backend emits every pod" rule
// bends, and the graph says so with Summarised.
const TopologyPodCap = 3000

// TopologyNamespaceListCap is how many namespaces are read one at a time.
// Beyond it a cluster-wide list is one request instead of many, and the
// answer is filtered to the scope here.
const TopologyNamespaceListCap = 3

// TopologyBounded is the line a topology carries about what it did not read.
const TopologyBounded = "Config, Secrets and claims are named from templates, not read"

// ErrEmptyTopologyScope is returned for a topology asked for no namespace.
var ErrEmptyTopologyScope = errors.New("a topology needs at least one namespace, or all of them")

// TopologyScope is which namespaces a topology covers.
type TopologyScope struct {
	// Namespaces are sorted and distinct. Empty when All.
	Namespaces []NamespaceName
	All        bool
}

// NewTopologyScope validates a scope. Blank entries are ignored; a scope with
// nothing left in it and not All is an error rather than a silent "all".
func NewTopologyScope(namespaces []string, all bool) (TopologyScope, error) {
	if all {
		return TopologyScope{All: true}, nil
	}

	seen := make(map[NamespaceName]bool, len(namespaces))
	var out []NamespaceName
	for _, raw := range namespaces {
		name, err := NewNamespaceName(raw)
		if err != nil {
			return TopologyScope{}, err
		}
		if name.IsAll() || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	if len(out) == 0 {
		return TopologyScope{}, ErrEmptyTopologyScope
	}
	slices.Sort(out)
	return TopologyScope{Namespaces: out}, nil
}

// Includes reports whether a namespace is inside the scope.
func (s TopologyScope) Includes(namespace NamespaceName) bool {
	if s.All {
		return true
	}
	_, found := slices.BinarySearch(s.Namespaces, namespace)
	return found
}

// ListsClusterWide reports whether the sources are better read with one
// cluster-wide list per kind than with one list per namespace.
func (s TopologyScope) ListsClusterWide() bool {
	return s.All || len(s.Namespaces) > TopologyNamespaceListCap
}

// EdgeKind is which relationship an edge draws.
type EdgeKind string

const (
	// EdgeOwns is an ownerReference, from the owner to what it owns.
	EdgeOwns EdgeKind = "owns"
	// EdgeSelects is a Service's selector matching a pod.
	EdgeSelects EdgeKind = "selects"
	// EdgeRoutes is an Ingress or a route naming a Service as its backend, or
	// a route naming a Gateway as its parent.
	EdgeRoutes EdgeKind = "routes"
	// EdgeScales is a HorizontalPodAutoscaler's scaleTargetRef.
	EdgeScales EdgeKind = "scales"
	// EdgeProtects is a PodDisruptionBudget's selector matching a pod.
	EdgeProtects EdgeKind = "protects"
	// EdgePolicySelects is a NetworkPolicy's podSelector matching a pod. It
	// says the policy GOVERNS the pod — never what it allows.
	EdgePolicySelects EdgeKind = "policy-selects"
	// EdgeAttaches is a pod template naming a ConfigMap, Secret or claim.
	EdgeAttaches EdgeKind = "attaches"
	// EdgeRunsAs is a pod template naming its ServiceAccount.
	EdgeRunsAs EdgeKind = "runs-as"
)

// NodeState is how healthy a box is.
type NodeState string

const (
	StateOK   NodeState = "ok"
	StateWarn NodeState = "warn"
	StateBad  NodeState = "bad"
	// StateNeutral means NOTHING WAS CHECKED, not that it is fine: an
	// Ingress, a ServiceAccount, a NetworkPolicy, a name from a template.
	StateNeutral NodeState = "neutral"
)

// stateRank orders states by how much they want attention.
func stateRank(s NodeState) int {
	switch s {
	case StateBad:
		return 3
	case StateWarn:
		return 2
	case StateOK:
		return 1
	default:
		return 0
	}
}

// TopologyPodSummary is a backend-folded pod set. The counts are complete.
type TopologyPodSummary struct {
	Total     int
	Ready     int
	Unhealthy int
}

// TopologyNode is one box.
type TopologyNode struct {
	ID        string
	Kind      GraphKind
	APIKind   string
	Name      string
	Namespace string
	State     NodeState
	Detail    string
	// Group is the sibling set for folding, as in the other shapes: a pod's
	// controller, an owned ReplicaSet's or Job's owner.
	Group string
	// Labels are set on TOP-LEVEL objects only — the ones grouping by app or
	// label is decided on. A pod's own labels would multiply the payload by
	// the replica count for nothing a group-by needs.
	Labels map[string]string
	// PodSummary is set only on a backend-folded pod set.
	PodSummary *TopologyPodSummary
}

// TopologyEdge is one relationship.
type TopologyEdge struct {
	From string
	To   string
	Kind EdgeKind
	// Label adds what the Kind does not say — a mount path, "parent" or
	// "backend" on a route, "selects" on a policy so it never reads as
	// "allows". Empty where the Kind says it all, because on a five-thousand
	// node map a repeated word per edge is a tenth of the payload.
	Label string
}

// TopologyGraph is a scope of namespaces and everything between its objects.
type TopologyGraph struct {
	Nodes []TopologyNode
	Edges []TopologyEdge
	// Counts is per Kubernetes Kind, and COMPLETE even when pods are
	// summarised: a folded map must never under-report a cluster.
	Counts map[string]int
	// Unreadable names the sources that were refused or failed.
	Unreadable []string
	// Bounded says what was deliberately not read. See PodGraph.Bounded.
	Bounded string
	// Summarised is true when pods were folded here because there were more
	// than TopologyPodCap of them.
	Summarised  bool
	GeneratedAt time.Time
}

// ClusterChange says something in a drawn scope changed.
//
// Namespaces are the namespaces it changed in. Empty means "somewhere in the
// cluster": a write drops a cluster's reads without saying where, and a
// change in an All scope has no narrower answer worth sending.
type ClusterChange struct {
	ClusterID  ClusterID
	Namespaces []NamespaceName
}

// TopologyController is a Deployment, StatefulSet, DaemonSet, CronJob,
// ReplicaSet or Job, with what its own pod template names.
type TopologyController struct {
	Kind      string
	Name      string
	Namespace NamespaceName
	Labels    map[string]string
	// Owner is the controlling owner, zero when nothing controls it.
	Owner OwnerReference
	// Desired, Ready and Failed are what health is read from.
	Desired, Ready, Failed int32
	Suspended              bool
	// Attached is what the object's own pod template names.
	Attached []AttachedRef
	// ClaimTemplates are a StatefulSet's volumeClaimTemplates. Each pod's
	// claim is named <template>-<pod>, which is the name drawn.
	ClaimTemplates []string
}

// ObjectKey names an object some other object refers to.
type ObjectKey struct {
	Kind      string
	Namespace NamespaceName
	Name      string
}

// GatewayRef is the little of a Gateway API Gateway the map needs.
type GatewayRef struct {
	Name      string
	Namespace NamespaceName
	Labels    map[string]string
	ClassName string
	Listeners int
}

// RouteRef is an HTTPRoute, GRPCRoute, TCPRoute or TLSRoute.
type RouteRef struct {
	Kind      string
	Name      string
	Namespace NamespaceName
	Labels    map[string]string
	Hostnames []string
	// Parents are the parentRefs, defaulted (kind Gateway, the route's own
	// namespace) as the API defines them.
	Parents []ObjectKey
	// Backends are the backendRefs, defaulted (kind Service, the route's own
	// namespace) likewise.
	Backends []ObjectKey
}

// ScalerRef is a HorizontalPodAutoscaler.
type ScalerRef struct {
	Name      string
	Namespace NamespaceName
	Labels    map[string]string
	// Target is the scaleTargetRef; always in the scaler's namespace.
	Target            ObjectKey
	Current, Min, Max int32
}

// BudgetRef is a PodDisruptionBudget.
type BudgetRef struct {
	Name      string
	Namespace NamespaceName
	Labels    map[string]string
	// Selector is nil when the budget has none, which in policy/v1 selects no
	// pods; an EMPTY one selects every pod in the namespace.
	Selector                                           *LabelSelector
	DisruptionsAllowed, DesiredHealthy, CurrentHealthy int32
}

// PolicyRef is a NetworkPolicy — its podSelector, and nothing about its rules.
type PolicyRef struct {
	Name      string
	Namespace NamespaceName
	Labels    map[string]string
	// PodSelector is required by the API; an EMPTY one selects every pod in
	// the namespace — the opposite of a Service's empty selector.
	PodSelector LabelSelector
	PolicyTypes []string
}

// ServiceAccountRef names a ServiceAccount that exists.
type ServiceAccountRef struct {
	Namespace NamespaceName
	Name      string
}

// LabelSelector is a metav1.LabelSelector in domain terms.
type LabelSelector struct {
	MatchLabels      map[string]string
	MatchExpressions []SelectorRequirement
}

// SelectorRequirement is one matchExpressions entry.
type SelectorRequirement struct {
	Key      string
	Operator string
	Values   []string
}

// Matches applies the label-selector rules: every matchLabels pair and every
// expression must hold, and an EMPTY selector matches everything. An operator
// this does not know matches nothing, so an unknown rule never draws an edge.
func (s LabelSelector) Matches(labels map[string]string) bool {
	for key, want := range s.MatchLabels {
		if got, ok := labels[key]; !ok || got != want {
			return false
		}
	}
	for _, req := range s.MatchExpressions {
		value, has := labels[req.Key]
		switch req.Operator {
		case "In":
			if !has || !slices.Contains(req.Values, value) {
				return false
			}
		case "NotIn":
			if has && slices.Contains(req.Values, value) {
				return false
			}
		case "Exists":
			if !has {
				return false
			}
		case "DoesNotExist":
			if has {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// TopologyInput is everything a topology is drawn from. The adapter may hand
// over more than the scope — a cluster-wide list for five namespaces — and
// NewTopologyGraph filters, because "is this in the scope" is a rule.
type TopologyInput struct {
	Scope       TopologyScope
	Controllers []TopologyController
	Pods        []Pod
	Services    []ServiceRef
	Ingresses   []IngressRef
	Gateways    []GatewayRef
	Routes      []RouteRef
	Scalers     []ScalerRef
	Budgets     []BudgetRef
	Policies    []PolicyRef
	// ServiceAccounts exist; ServiceAccountsListed says the list was read, so
	// a referenced name that is not in it can be said to be absent.
	ServiceAccounts       []ServiceAccountRef
	ServiceAccountsListed bool
	Unreadable            []string
	// GatewayAPIServed says discovery serves gateway.networking.k8s.io.
	GatewayAPIServed bool
	ReadAt           time.Time
}

// topologyID is a node id unique within one topology: kind, namespace, name.
func topologyID(kind string, namespace NamespaceName, name string) string {
	return objectNodeID(kind, namespace.String(), name)
}

type edgeKey struct {
	from, to string
	kind     EdgeKind
}

// scopedPod is a pod in scope with its labels read once — Labels() clones.
type scopedPod struct {
	pod    Pod
	labels map[string]string
	node   string
}

type topologyBuilder struct {
	nodes  map[string]*TopologyNode
	edges  map[edgeKey]string
	counts map[string]int
}

// add puts a node on the map once; counted says whether it is an object that
// was read (and so belongs in Counts) rather than a name nothing resolved.
func (b *topologyBuilder) add(node TopologyNode, counted bool) {
	if _, found := b.nodes[node.ID]; found {
		return
	}
	b.nodes[node.ID] = &node
	if counted && node.APIKind != "" {
		b.counts[node.APIKind]++
	}
}

func (b *topologyBuilder) edge(from, to string, kind EdgeKind, label string) {
	key := edgeKey{from: from, to: to, kind: kind}
	if _, found := b.edges[key]; !found {
		b.edges[key] = label
	}
}

// unresolved draws a name something referred to that is not among what was
// read: a CRD owner (from ownerReferences, never a GET), a Service an Ingress
// names and nobody created, a Gateway in a namespace outside the scope.
// Neutral, because nothing about it was checked.
func (b *topologyBuilder) unresolved(kind string, namespace NamespaceName, name, detail string) string {
	if kind == "Node" {
		// A mirror pod's owner is its Node, which belongs to no namespace.
		namespace = NamespaceAll
	}
	id := topologyID(kind, namespace, name)
	b.add(TopologyNode{
		ID: id, Kind: GraphKindOf(kind), APIKind: kind, Name: name,
		Namespace: namespace.String(), State: StateNeutral, Detail: detail,
	}, false)
	return id
}

// NewTopologyGraph draws a scope.
//
// A PURE FUNCTION of what was read, sorted, so the same cluster draws the same
// map and every rule — which selector matches what, when pods fold — is
// settled in a test.
func NewTopologyGraph(in TopologyInput) TopologyGraph {
	b := &topologyBuilder{
		nodes:  make(map[string]*TopologyNode),
		edges:  make(map[edgeKey]string),
		counts: make(map[string]int),
	}
	scope := in.Scope

	graph := TopologyGraph{
		Unreadable:  slices.Clone(in.Unreadable),
		Bounded:     TopologyBounded,
		GeneratedAt: in.ReadAt,
	}
	unreadable := make(map[string]bool, len(in.Unreadable))
	for _, source := range in.Unreadable {
		unreadable[source] = true
	}

	// --- Controllers ---------------------------------------------------
	controllers := make(map[string]TopologyController)
	for _, c := range in.Controllers {
		if scope.Includes(c.Namespace) {
			controllers[topologyID(c.Kind, c.Namespace, c.Name)] = c
		}
	}
	ownerOf := make(map[string]string, len(controllers))
	for id, c := range controllers {
		if c.Owner.IsZero() {
			continue
		}
		owner := topologyID(c.Owner.Kind, c.Namespace, c.Owner.Name)
		if _, listed := controllers[owner]; !listed {
			// Not among what was read — an operator's CRD, most often.
			// Drawn by kind and name from the ownerReference; never fetched.
			owner = b.unresolved(c.Owner.Kind, c.Namespace, c.Owner.Name, "owner")
		}
		ownerOf[id] = owner
		b.edge(owner, id, EdgeOwns, "")
	}
	for id, c := range controllers {
		node := TopologyNode{
			ID: id, Kind: GraphWorkload, APIKind: c.Kind, Name: c.Name,
			Namespace: c.Namespace.String(),
		}
		node.State, node.Detail = controllerState(c)
		if owner, owned := ownerOf[id]; owned {
			node.Group = owner
			if c.Kind == "ReplicaSet" || c.Kind == "Job" {
				node.Kind = GraphReplicaSet
			}
		} else {
			node.Labels = c.Labels
			if c.Kind == "ReplicaSet" {
				node.Kind = GraphReplicaSet
			}
		}
		b.add(node, true)
	}

	// --- Pods ----------------------------------------------------------
	var pods []scopedPod
	for _, pod := range in.Pods {
		if scope.Includes(pod.Namespace()) {
			pods = append(pods, scopedPod{pod: pod, labels: pod.Labels()})
		}
	}
	summarised := len(pods) > TopologyPodCap
	graph.Summarised = summarised

	summaries := make(map[string]*TopologyPodSummary)
	worst := make(map[string]NodeState)
	podsUnder := make(map[string]int)

	for i := range pods {
		sp := &pods[i]
		pod := sp.pod
		ns := pod.Namespace()

		parent := ""
		if owner := pod.Controller(); !owner.IsZero() {
			parent = topologyID(owner.Kind, ns, owner.Name)
			if _, listed := controllers[parent]; !listed {
				parent = b.unresolved(owner.Kind, ns, owner.Name, "owner")
			}
			// Counted up the whole chain, bounded because Kubernetes does not
			// forbid an ownerReference cycle.
			for id, hops := parent, 0; id != "" && hops <= len(controllers); id, hops = ownerOf[id], hops+1 {
				podsUnder[id]++
			}
		}

		state, detail := podState(pod)
		if summarised {
			group := parent
			if group == "" {
				group = "namespace/" + ns.String()
			}
			sp.node = "fold/" + group + "/" + string(GraphPod)
			summary := summaries[sp.node]
			if summary == nil {
				summary = &TopologyPodSummary{}
				summaries[sp.node] = summary
				b.add(TopologyNode{
					ID: sp.node, Kind: GraphPod, APIKind: "Pod",
					Namespace: ns.String(), PodSummary: summary,
				}, false)
			}
			summary.Total++
			if pod.IsReady() {
				summary.Ready++
			}
			if !pod.IsHealthy() {
				summary.Unhealthy++
			}
			if stateRank(state) > stateRank(worst[sp.node]) {
				worst[sp.node] = state
			}
		} else {
			sp.node = topologyID("Pod", ns, pod.Name())
			node := TopologyNode{
				ID: sp.node, Kind: GraphPod, APIKind: "Pod", Name: pod.Name(),
				Namespace: ns.String(), State: state, Detail: detail, Group: parent,
			}
			if parent == "" {
				node.Labels = sp.labels
			}
			b.add(node, false)
		}

		if parent != "" {
			b.edge(parent, sp.node, EdgeOwns, "")
			// ATTACHED BELONGS TO THE PODS, from the template of the
			// controller that created each — see NewWorkloadGraph.
			if c, listed := controllers[parent]; listed {
				b.attach(sp.node, c.Namespace, c.Attached)
				for _, template := range c.ClaimTemplates {
					b.attach(sp.node, c.Namespace, []AttachedRef{{
						Kind: GraphClaim, Name: template + "-" + pod.Name(), Via: "claim template",
					}})
				}
			}
		}
	}
	b.counts["Pod"] = len(pods)

	for id, summary := range summaries {
		node := b.nodes[id]
		node.Name = fmt.Sprintf("%d Pod", summary.Total)
		if summary.Total != 1 {
			node.Name += "s"
		}
		node.State = worst[id]
		node.Detail = fmt.Sprintf("%d/%d ready", summary.Ready, summary.Total)
		if summary.Unhealthy > 0 {
			node.Detail += fmt.Sprintf(", %d unhealthy", summary.Unhealthy)
		}
	}

	// A controller with no pods under it — scaled to zero, a CronJob between
	// runs — declares its template's names itself, but only a TOP-LEVEL one:
	// an old zero-replica ReplicaSet declares nothing its Deployment does not.
	for id, c := range controllers {
		if _, owned := ownerOf[id]; !owned && podsUnder[id] == 0 {
			b.attach(id, c.Namespace, c.Attached)
		}
	}

	index := newPodIndex(pods)

	// --- Services and what routes to them ------------------------------
	services := make(map[string]bool)
	for _, service := range in.Services {
		ns := NamespaceName(service.Namespace)
		if !scope.Includes(ns) {
			continue
		}
		id := topologyID("Service", ns, service.Name)
		services[id] = true

		matched := 0
		if len(service.Selector) > 0 {
			for _, sp := range index.candidates(ns, service.Selector) {
				if selectorMatches(service.Selector, sp.labels) {
					b.edge(id, sp.node, EdgeSelects, "")
					matched++
				}
			}
		}
		node := TopologyNode{
			ID: id, Kind: GraphService, APIKind: "Service", Name: service.Name,
			Namespace: service.Namespace, Labels: service.Labels,
			State: StateOK, Detail: strings.Join(service.Ports, ", "),
		}
		switch {
		case len(service.Selector) == 0:
			// An empty selector means NO selector: an ExternalName, or
			// Endpoints managed by hand. Nothing here can check it.
			node.State = StateNeutral
			if service.Type == "ExternalName" {
				node.Detail = "ExternalName"
			} else {
				node.Detail = "no selector"
			}
		case matched == 0:
			node.State = StateWarn
			node.Detail = "selects no pods"
		}
		b.add(node, true)
	}

	service := func(namespace NamespaceName, name string) string {
		id := topologyID("Service", namespace, name)
		if services[id] {
			return id
		}
		detail := "not found"
		if !scope.Includes(namespace) {
			detail = "outside the scope"
		} else if unreadableSource(unreadable, "services", namespace) {
			detail = "not read"
		}
		return b.unresolved("Service", namespace, name, detail)
	}

	for _, ingress := range in.Ingresses {
		ns := NamespaceName(ingress.Namespace)
		if !scope.Includes(ns) {
			continue
		}
		id := topologyID("Ingress", ns, ingress.Name)
		b.add(TopologyNode{
			ID: id, Kind: GraphIngress, APIKind: "Ingress", Name: ingress.Name,
			Namespace: ingress.Namespace, Labels: ingress.Labels,
			State: StateNeutral, Detail: strings.Join(ingress.Hosts, ", "),
		}, true)
		for _, backend := range ingress.Backends {
			b.edge(id, service(ns, backend), EdgeRoutes, "routes to")
		}
	}

	gateways := make(map[string]bool)
	for _, gateway := range in.Gateways {
		if !scope.Includes(gateway.Namespace) {
			continue
		}
		id := topologyID("Gateway", gateway.Namespace, gateway.Name)
		gateways[id] = true
		b.add(TopologyNode{
			ID: id, Kind: GraphGateway, APIKind: "Gateway", Name: gateway.Name,
			Namespace: gateway.Namespace.String(), Labels: gateway.Labels,
			State: StateNeutral, Detail: gateway.ClassName,
		}, true)
	}
	for _, route := range in.Routes {
		if !scope.Includes(route.Namespace) {
			continue
		}
		id := topologyID(route.Kind, route.Namespace, route.Name)
		b.add(TopologyNode{
			ID: id, Kind: GraphRoute, APIKind: route.Kind, Name: route.Name,
			Namespace: route.Namespace.String(), Labels: route.Labels,
			State: StateNeutral, Detail: strings.Join(route.Hostnames, ", "),
		}, true)
		for _, parent := range route.Parents {
			from := topologyID(parent.Kind, parent.Namespace, parent.Name)
			switch {
			case parent.Kind == "Service":
				// A mesh (GAMMA) route attaches to a Service, not a Gateway.
				from = service(parent.Namespace, parent.Name)
			case !gateways[from]:
				detail := "not found"
				if !scope.Includes(parent.Namespace) {
					detail = "outside the scope"
				}
				from = b.unresolved(parent.Kind, parent.Namespace, parent.Name, detail)
			}
			b.edge(from, id, EdgeRoutes, "parent")
		}
		for _, backend := range route.Backends {
			to := ""
			if backend.Kind == "Service" {
				to = service(backend.Namespace, backend.Name)
			} else {
				to = b.unresolved(backend.Kind, backend.Namespace, backend.Name, "backend")
			}
			b.edge(id, to, EdgeRoutes, "backend")
		}
	}

	// --- Scalers, budgets, policies ------------------------------------
	for _, scaler := range in.Scalers {
		if !scope.Includes(scaler.Namespace) {
			continue
		}
		id := topologyID("HorizontalPodAutoscaler", scaler.Namespace, scaler.Name)
		node := TopologyNode{
			ID: id, Kind: GraphScaler, APIKind: "HorizontalPodAutoscaler", Name: scaler.Name,
			Namespace: scaler.Namespace.String(), Labels: scaler.Labels,
			State: StateOK, Detail: fmt.Sprintf("%d of %d–%d", scaler.Current, scaler.Min, scaler.Max),
		}
		if scaler.Max > 0 && scaler.Current >= scaler.Max {
			node.State = StateWarn
			node.Detail = fmt.Sprintf("at its maximum of %d", scaler.Max)
		}
		b.add(node, true)

		target := topologyID(scaler.Target.Kind, scaler.Namespace, scaler.Target.Name)
		if _, listed := controllers[target]; !listed {
			target = b.unresolved(scaler.Target.Kind, scaler.Namespace, scaler.Target.Name, "scale target")
		}
		b.edge(id, target, EdgeScales, "")
	}

	for _, budget := range in.Budgets {
		if !scope.Includes(budget.Namespace) {
			continue
		}
		id := topologyID("PodDisruptionBudget", budget.Namespace, budget.Name)
		node := TopologyNode{
			ID: id, Kind: GraphBudget, APIKind: "PodDisruptionBudget", Name: budget.Name,
			Namespace: budget.Namespace.String(), Labels: budget.Labels,
			State:  StateOK,
			Detail: fmt.Sprintf("%d disruptions allowed", budget.DisruptionsAllowed),
		}
		if budget.DisruptionsAllowed == 0 && budget.DesiredHealthy > 0 {
			// A drain will stop here: nothing it guards may be evicted now.
			node.State = StateWarn
			node.Detail = "no disruptions allowed"
		}
		b.add(node, true)

		if budget.Selector != nil {
			for _, sp := range index.candidates(budget.Namespace, budget.Selector.MatchLabels) {
				if budget.Selector.Matches(sp.labels) {
					b.edge(id, sp.node, EdgeProtects, "")
				}
			}
		}
	}

	for _, policy := range in.Policies {
		if !scope.Includes(policy.Namespace) {
			continue
		}
		id := topologyID("NetworkPolicy", policy.Namespace, policy.Name)
		b.add(TopologyNode{
			ID: id, Kind: GraphPolicy, APIKind: "NetworkPolicy", Name: policy.Name,
			Namespace: policy.Namespace.String(), Labels: policy.Labels,
			State: StateNeutral, Detail: strings.Join(policy.PolicyTypes, ", "),
		}, true)
		// SELECTS, AND ONLY SELECTS. Whether traffic is allowed is a
		// question about every policy in the namespace and the CNI that
		// enforces them; a line here would answer it, wrongly.
		for _, sp := range index.candidates(policy.Namespace, policy.PodSelector.MatchLabels) {
			if policy.PodSelector.Matches(sp.labels) {
				b.edge(id, sp.node, EdgePolicySelects, "selects")
			}
		}
	}

	// A referenced ServiceAccount that the list says does not exist is worth
	// a word; it stays neutral, because nothing else about it was checked.
	if in.ServiceAccountsListed {
		exists := make(map[string]bool, len(in.ServiceAccounts))
		for _, sa := range in.ServiceAccounts {
			exists[topologyID("ServiceAccount", sa.Namespace, sa.Name)] = true
		}
		for id, node := range b.nodes {
			if node.Kind == GraphServiceAccount && !exists[id] {
				node.Detail = "not found"
			}
		}
	}

	graph.Counts = b.counts
	graph.Nodes = make([]TopologyNode, 0, len(b.nodes))
	for _, node := range b.nodes {
		graph.Nodes = append(graph.Nodes, *node)
	}
	sort.Slice(graph.Nodes, func(i, j int) bool { return graph.Nodes[i].ID < graph.Nodes[j].ID })

	graph.Edges = make([]TopologyEdge, 0, len(b.edges))
	for key, label := range b.edges {
		graph.Edges = append(graph.Edges, TopologyEdge{From: key.from, To: key.to, Kind: key.kind, Label: label})
	}
	sort.Slice(graph.Edges, func(i, j int) bool {
		a, c := graph.Edges[i], graph.Edges[j]
		if a.From != c.From {
			return a.From < c.From
		}
		if a.To != c.To {
			return a.To < c.To
		}
		return a.Kind < c.Kind
	})
	if graph.Unreadable == nil {
		graph.Unreadable = []string{}
	}
	return graph
}

// podIndex finds the pods a selector can match without testing every pod in
// the namespace against every selector — a hundred Services over a thousand
// pods is a hundred thousand map walks otherwise, and this map is drawn three
// times over (Services, budgets, policies).
type podIndex struct {
	byNamespace map[NamespaceName][]*scopedPod
	byLabel     map[NamespaceName]map[string][]*scopedPod
}

func newPodIndex(pods []scopedPod) podIndex {
	index := podIndex{
		byNamespace: make(map[NamespaceName][]*scopedPod),
		byLabel:     make(map[NamespaceName]map[string][]*scopedPod),
	}
	for i := range pods {
		sp := &pods[i]
		ns := sp.pod.Namespace()
		index.byNamespace[ns] = append(index.byNamespace[ns], sp)
		labels := index.byLabel[ns]
		if labels == nil {
			labels = make(map[string][]*scopedPod)
			index.byLabel[ns] = labels
		}
		for key, value := range sp.labels {
			labels[key+"="+value] = append(labels[key+"="+value], sp)
		}
	}
	return index
}

// candidates returns the pods that could satisfy matchLabels: the smallest
// set carrying one of its pairs, or every pod when it names none. The caller
// still applies the whole selector.
func (x podIndex) candidates(namespace NamespaceName, matchLabels map[string]string) []*scopedPod {
	if len(matchLabels) == 0 {
		return x.byNamespace[namespace]
	}
	var best []*scopedPod
	first := true
	for key, value := range matchLabels {
		bucket := x.byLabel[namespace][key+"="+value]
		if first || len(bucket) < len(best) {
			best, first = bucket, false
		}
		if len(best) == 0 {
			break
		}
	}
	return best
}

// attach draws what a template names, one box per name, from the node that
// reads it.
func (b *topologyBuilder) attach(from string, namespace NamespaceName, attached []AttachedRef) {
	for _, ref := range attached {
		apiKind := apiKindOf(ref.Kind)
		id := topologyID(apiKind, namespace, ref.Name)
		b.add(TopologyNode{
			ID: id, Kind: ref.Kind, APIKind: apiKind, Name: ref.Name,
			Namespace: namespace.String(), State: StateNeutral,
		}, true)
		kind := EdgeAttaches
		if ref.Kind == GraphServiceAccount {
			kind = EdgeRunsAs
		}
		b.edge(from, id, kind, ref.Via)
	}
}

// unreadableSource reports whether a source was refused, cluster-wide or for
// this one namespace.
func unreadableSource(unreadable map[string]bool, source string, namespace NamespaceName) bool {
	return unreadable[source] || unreadable[TopologySourceIn(source, namespace)]
}

// TopologySourceIn names a source read for one namespace, as Unreadable says
// it.
func TopologySourceIn(source string, namespace NamespaceName) string {
	return source + " in " + namespace.String()
}

// controllerState reads a controller's colour and short qualifier.
func controllerState(c TopologyController) (NodeState, string) {
	switch c.Kind {
	case "CronJob":
		// A schedule is not up or down; its Jobs carry that.
		if c.Suspended {
			return StateNeutral, "suspended"
		}
		return StateNeutral, "scheduled"
	case "Job":
		detail := fmt.Sprintf("%d/%d succeeded", c.Ready, c.Desired)
		if c.Failed > 0 {
			return StateBad, fmt.Sprintf("%s, %d failed", detail, c.Failed)
		}
		return StateOK, detail
	default:
		detail := fmt.Sprintf("%d/%d ready", c.Ready, c.Desired)
		switch {
		case c.Desired > 0 && c.Ready == 0:
			return StateBad, detail
		case c.Ready < c.Desired:
			return StateWarn, detail
		default:
			return StateOK, detail
		}
	}
}

// podState reads a pod's colour from the status the rest of PodSteer derives.
func podState(pod Pod) (NodeState, string) {
	detail := pod.StatusReason()
	if detail == "" {
		detail = string(pod.Phase())
	}
	switch {
	case pod.IsHealthy():
		return StateOK, detail
	case pod.Phase() == PodPhasePending && pod.StatusReason() == "":
		return StateWarn, detail
	default:
		return StateBad, detail
	}
}
