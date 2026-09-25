package mcp

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// Tool is one read-only capability.
//
// There is no field saying so. A tool that wrote would need a port this
// package cannot name (see the reading interfaces in server.go), so
// "read-only" is a property of the package rather than a flag on a struct
// that somebody could set to false — which is why tools/list can annotate
// every entry from one constant.
type Tool struct {
	// Name is what tools/call names. Lower snake case, verb first, matching
	// the shape every other MCP server uses.
	Name string
	// Title is the human label a client shows beside the name.
	Title string
	// Description is what a model chooses on. It says what the tool answers
	// AND what it does not, because the second half is what stops a model
	// reaching for a tool that will disappoint it.
	Description string
	// Schema validates the arguments before Call runs.
	Schema Schema
	// Call answers, returning a value to be encoded as the result.
	Call func(ctx context.Context, args Arguments) (any, error)
}

// Bounds on what one call may return.
//
// Every one of these is stated in the tool's own description and reported in
// its answer, because a silent truncation is a lie about a cluster: a model
// told "42 pods" when there were 400 will reason about the 42 with complete
// confidence. Nothing here is dropped without a total beside it.
const (
	// defaultRows is how many rows a list returns unasked.
	defaultRows = 200
	// maxRows caps what a caller may ask for. Above this the answer stops
	// being something a model can hold and starts being a way to spend a
	// context window on a namespace nobody looked at.
	maxRows = 1000
	// defaultLogLines is the tail a log read returns unasked.
	defaultLogLines = 200
	// maxLogLines caps the tail.
	maxLogLines = 2000
	// logLimitBytes caps what the API SERVER sends, whatever the line count
	// works out to: a container writing megabyte lines would otherwise turn a
	// 200-line request into a 200 MB read.
	logLimitBytes = 1 << 20
	// maxSinceSeconds bounds how far back a log read may reach — a day,
	// beyond which the tail is what anyone actually wants.
	maxSinceSeconds = 24 * 60 * 60
)

// toolset holds the readers the handlers close over.
type toolset struct {
	clusters  ClusterReader
	kinds     KindReader
	workloads WorkloadReader
	events    EventReader
	resources ResourceReader
	overview  OverviewReader
	rbac      RBACReader
	logs      LogReader
	now       func() time.Time
}

// buildTools returns the tool set, in the order tools/list reports it.
//
// The order is a reading order: what is on this machine, then what a cluster
// holds, then what is wrong with it, then what this account may do. A model
// scanning the list top to bottom meets list_clusters before anything that
// needs a cluster name.
func buildTools(deps Deps) []Tool {
	t := &toolset{
		clusters:  deps.Clusters,
		kinds:     deps.Kinds,
		workloads: deps.Workloads,
		events:    deps.Events,
		resources: deps.Resources,
		overview:  deps.Overview,
		rbac:      deps.RBAC,
		logs:      deps.Logs,
		now:       deps.Now,
	}

	cluster := text("The kubeconfig context name, as list_clusters reports it.")
	namespace := text("Namespace. Omit for every namespace.")
	kind := text(`Kind, either as the catalogue id ("apps/v1/deployments") or by name ("Deployment", "deployments"). list_kinds reports both.`)
	limit := bounded(fmt.Sprintf("Maximum rows to return (default %d). The answer always states the true total.", defaultRows), 1, maxRows, defaultRows)

	return []Tool{
		{
			Name:        "list_clusters",
			Title:       "List clusters",
			Description: "Lists the Kubernetes clusters in this machine's kubeconfig, which is connected, and which one kubectl would use by default. Takes no arguments. Every other tool needs a cluster name from here.",
			Schema:      object(nil, map[string]Property{}),
			Call:        t.listClusters,
		},
		{
			Name:        "list_namespaces",
			Title:       "List namespaces",
			Description: "Lists a cluster's namespaces. Cheap: names and phases only. Use namespace_inventory for what a namespace holds.",
			Schema:      object([]string{"cluster"}, map[string]Property{"cluster": cluster}),
			Call:        t.listNamespaces,
		},
		{
			Name:        "list_kinds",
			Title:       "List resource kinds",
			Description: "Lists every kind this cluster can show, built-in kinds and the CRDs discovery found, with the id the other tools accept. Read this before guessing a kind that might not be installed.",
			Schema:      object([]string{"cluster"}, map[string]Property{"cluster": cluster}),
			Call:        t.listKinds,
		},
		{
			Name:  "list_pods",
			Title: "List pods",
			Description: "Lists pods with their readiness, restarts, node, status and — where the cluster serves metrics — their measured CPU and memory. " +
				"Give node to list what is running on one machine across every namespace instead.",
			Schema: object([]string{"cluster"}, map[string]Property{
				"cluster":   cluster,
				"namespace": namespace,
				"node":      text("List the pods scheduled on this node, across every namespace. Cannot be combined with namespace."),
				"limit":     limit,
			}),
			Call: t.listPods,
		},
		{
			Name:        "list_workloads",
			Title:       "List workloads",
			Description: "Lists controllers of one kind with desired, ready, updated and available counts, their images and whether a rollout is in progress.",
			Schema: object([]string{"cluster", "kind"}, map[string]Property{
				"cluster":   cluster,
				"kind":      choice("Controller kind.", workloadKindNames()...),
				"namespace": namespace,
				"limit":     limit,
			}),
			Call: t.listWorkloads,
		},
		{
			Name:        "list_nodes",
			Title:       "List nodes",
			Description: "Lists the cluster's nodes with their status, roles, allocatable capacity and, where metrics are served, their load. Usage percentages are absent rather than zero on a cluster with no metrics API.",
			Schema:      object([]string{"cluster"}, map[string]Property{"cluster": cluster, "limit": limit}),
			Call:        t.listNodes,
		},
		{
			Name:  "list_resources",
			Title: "List resources of any kind",
			Description: "Lists objects of ANY kind — including custom resources — as the API server itself prints them, the same columns kubectl get shows. " +
				"Use it for kinds the richer tools do not cover: Services, ConfigMaps, Ingresses, PVCs, CRD instances. Secret VALUES are never included.",
			Schema: object([]string{"cluster", "kind"}, map[string]Property{
				"cluster":   cluster,
				"kind":      kind,
				"namespace": namespace,
				"limit":     limit,
			}),
			Call: t.listResources,
		},
		{
			Name:  "get_manifest",
			Title: "Get an object's manifest",
			Description: "Returns one object's full manifest as YAML, for any kind. " +
				"A Secret's values are replaced by their decoded size — there is no tool here that returns a Secret's contents.",
			Schema: object([]string{"cluster", "kind", "name"}, map[string]Property{
				"cluster":   cluster,
				"kind":      kind,
				"name":      text("Object name."),
				"namespace": namespace,
			}),
			Call: t.getManifest,
		},
		{
			Name:  "describe_resource",
			Title: "Describe an object",
			Description: "Returns one object's manifest together with the Kubernetes Events about it, which is what makes kubectl describe worth reading. " +
				"Secret values are redacted here too.",
			Schema: object([]string{"cluster", "kind", "name"}, map[string]Property{
				"cluster":   cluster,
				"kind":      kind,
				"name":      text("Object name."),
				"namespace": namespace,
			}),
			Call: t.describeResource,
		},
		{
			Name:  "get_logs",
			Title: "Read a container's log",
			Description: fmt.Sprintf(
				"Reads the tail of one container's log. Bounded and never follows: at most %d lines and %d MiB, whichever comes first. "+
					"Set previous to read the log of the run BEFORE the current one, which is where a crash-looping container's cause is.",
				maxLogLines, logLimitBytes/(1<<20)),
			Schema: object([]string{"cluster", "namespace", "pod"}, map[string]Property{
				"cluster":       cluster,
				"namespace":     text("The pod's namespace."),
				"pod":           text("Pod name."),
				"container":     text("Container name. Omit for the pod's first container."),
				"tail_lines":    bounded(fmt.Sprintf("Lines from the end of the log (default %d).", defaultLogLines), 1, maxLogLines, defaultLogLines),
				"since_seconds": bounded("Only lines newer than this many seconds. Omit for no lower bound.", 1, maxSinceSeconds, 0),
				"previous":      flag("Read the previous instantiation of the container, as kubectl logs -p does."),
			}),
			Call: t.getLogs,
		},
		{
			Name:  "list_events",
			Title: "List events",
			Description: "Lists Kubernetes Events, most recent first — the cluster's own running commentary on scheduling, image pulls, probes and evictions. " +
				"Events expire; an empty list means nothing recent, not nothing ever.",
			Schema: object([]string{"cluster"}, map[string]Property{
				"cluster":       cluster,
				"namespace":     namespace,
				"warnings_only": flag("Return only Warning events."),
				"limit":         limit,
			}),
			Call: t.listEvents,
		},
		{
			Name:  "assess_cluster",
			Title: "Assess a cluster",
			Description: "Returns PodSteer's own assessment of a cluster: ranked findings with advice, capacity against REQUESTS as well as usage, node and pod summaries. " +
				"This is the analysis, not a list — start here when asked what is wrong with a cluster.",
			Schema: object([]string{"cluster"}, map[string]Property{"cluster": cluster}),
			Call:   t.assessCluster,
		},
		{
			Name:  "assess_pod",
			Title: "Assess a pod",
			Description: "Returns one pod's containers, conditions and PodSteer's findings about it — crash loops with the exit reason that disambiguates the code, " +
				"probes that cannot pass, images pinned to a moving tag. A correctly configured pod produces no findings.",
			Schema: object([]string{"cluster", "namespace", "pod"}, map[string]Property{
				"cluster":   cluster,
				"namespace": text("The pod's namespace."),
				"pod":       text("Pod name."),
			}),
			Call: t.assessPod,
		},
		{
			Name:  "dependency_map",
			Title: "Map an object's dependencies",
			Description: "Returns what one object depends on and what depends on it: for a pod, the chain from whatever routes to it down to its containers and what they mount; " +
				"for a controller, the same fanned over its current pods; for anything else, its owners and what its spec names. Every edge is a relationship Kubernetes actually has.",
			Schema: object([]string{"cluster", "kind", "name"}, map[string]Property{
				"cluster":   cluster,
				"kind":      kind,
				"name":      text("Object name."),
				"namespace": namespace,
			}),
			Call: t.dependencyMap,
		},
		{
			Name:        "namespace_inventory",
			Title:       "Count what a namespace holds",
			Description: "Counts the built-in kinds in one namespace, asking the API server for its own count rather than listing objects. A kind this account may not list is reported unreadable, never as zero.",
			Schema: object([]string{"cluster", "namespace"}, map[string]Property{
				"cluster":   cluster,
				"namespace": text("Namespace to count."),
			}),
			Call: t.namespaceInventory,
		},
		{
			Name:  "rbac_subject_rules",
			Title: "What may I do here",
			Description: "Asks the cluster what THESE credentials may do in one namespace, in a single SelfSubjectRulesReview. " +
				"The API server answers; nothing here evaluates a rule to reach a verdict.",
			Schema: object([]string{"cluster", "namespace"}, map[string]Property{
				"cluster":   cluster,
				"namespace": text("Namespace to review."),
			}),
			Call: t.subjectRules,
		},
		{
			Name:  "rbac_can_i",
			Title: "Can I do this",
			Description: "Asks one access review — the equivalent of kubectl auth can-i. Names a subject to ask about somebody else, which most accounts are not permitted to do; " +
				"that refusal is reported as a refusal. allowed and denied are both returned: an authorizer with no opinion leaves both false, which is not a denial.",
			Schema: object([]string{"cluster", "verb"}, map[string]Property{
				"cluster":           cluster,
				"verb":              text(`Verb, e.g. "get", "list", "create", "delete".`),
				"resource":          text(`Resource, plural and lowercase, e.g. "pods".`),
				"subresource":       text(`Subresource, e.g. "log", "exec".`),
				"group":             text(`API group, e.g. "apps". Omit for the core group.`),
				"namespace":         text("Namespace to ask about. Omit for cluster scope."),
				"name":              text("Ask about one named object rather than the kind."),
				"subject_kind":      choice("Ask about somebody else. Omit to ask about the current credentials.", "User", "Group", "ServiceAccount"),
				"subject_name":      text("The subject's name. Required when subject_kind is given."),
				"subject_namespace": text("A ServiceAccount's namespace."),
			}),
			Call: t.canI,
		},
		{
			Name:  "rbac_inspect_role",
			Title: "Inspect a role",
			Description: "Reads one Role or ClusterRole, finds the bindings that reference it, and flags what its rules permit — wildcards, escalate/bind/impersonate, " +
				"cluster-wide Secret reads, pod creation, cluster-admin. A role with none of them produces no findings.",
			Schema: object([]string{"cluster", "scope", "name"}, map[string]Property{
				"cluster":   cluster,
				"scope":     choice("Whether the role is namespaced (Role) or cluster-scoped (ClusterRole).", "namespace", "cluster"),
				"name":      text("Role name."),
				"namespace": text("The Role's namespace. Required when scope is namespace."),
			}),
			Call: t.inspectRole,
		},
	}
}

// workloadKindNames lists the controller kinds, for the schema enum.
func workloadKindNames() []string {
	kinds := domain.WorkloadKinds()
	names := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		names = append(names, string(kind))
	}
	return names
}

// --- shared argument handling -------------------------------------------

// cluster resolves the cluster argument, opening the connection if needed.
//
// CONNECTED ON DEMAND, never at startup. A kubeconfig routinely names a dozen
// contexts, most of them clusters the operator is not using today and some of
// them unreachable; connecting them all when the server starts would spend
// the first minute contacting clusters nobody asked about, and would make an
// agent's first question wait on the slowest of them. Connect is not an error
// on an already-open cluster, but it does re-run discovery, so a connection
// already in the registry is left alone.
func (t *toolset) cluster(ctx context.Context, args Arguments) (domain.ClusterID, error) {
	id, err := domain.NewClusterID(args.String("cluster"))
	if err != nil {
		return "", err
	}

	open, err := t.clusters.Connections(ctx)
	if err != nil {
		return "", err
	}
	for _, cluster := range open {
		if cluster.ID() == id {
			return id, nil
		}
	}

	if _, err := t.clusters.Connect(ctx, id); err != nil {
		return "", err
	}
	return id, nil
}

// namespace parses a namespace argument. Absent means every namespace.
func namespaceOf(args Arguments, key string) (domain.NamespaceName, error) {
	return domain.NewNamespaceName(args.String(key))
}

// rows caps a list at the requested limit, reporting the true total.
type rows[T any] struct {
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace,omitempty"`
	Items     []T    `json:"items"`
	Total     int    `json:"total"`
	Truncated bool   `json:"truncated,omitempty"`
}

// limited truncates items to limit, saying so.
func limited[T any](id domain.ClusterID, namespace domain.NamespaceName, items []T, total, limit int) rows[T] {
	kept := items
	if len(kept) > limit {
		kept = kept[:limit]
	}
	return rows[T]{
		Cluster:   id.String(),
		Namespace: namespace.String(),
		Items:     kept,
		Total:     total,
		Truncated: len(kept) < total,
	}
}

// resolveKind turns a kind argument into a kind this cluster actually serves.
//
// The catalogue is the authority rather than a parsed identifier, for two
// reasons. A kind that is not installed must be reported as absent from the
// CLUSTER rather than as a 404 from an API path, since a model asked about a
// CRD needs to know the operator is not there. And a model writes
// "Deployment" or "deployments" far more readily than "apps/v1/deployments",
// so both are accepted — but an ambiguous name is refused with the
// candidates rather than resolved by picking one, because "Application"
// exists in three API groups and choosing wrongly answers a question about a
// different object entirely.
func (t *toolset) resolveKind(ctx context.Context, id domain.ClusterID, raw string) (domain.ResourceKind, error) {
	kinds, err := t.kinds.Kinds(ctx, id)
	if err != nil {
		return domain.ResourceKind{}, err
	}

	wanted := strings.TrimSpace(raw)
	lowered := strings.ToLower(wanted)

	var matches []domain.ResourceKind
	for _, kind := range kinds {
		if kind.ID() == wanted {
			return kind, nil
		}
		if strings.EqualFold(kind.Kind, wanted) || strings.EqualFold(kind.Resource, wanted) || strings.EqualFold(kind.Singular, lowered) {
			matches = append(matches, kind)
		}
	}

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return domain.ResourceKind{}, invalidArgument(
			"cluster %q serves no kind %q — call list_kinds for what it does serve", id, wanted)
	default:
		ids := make([]string, 0, len(matches))
		for _, match := range matches {
			ids = append(ids, match.ID())
		}
		slices.Sort(ids)
		return domain.ResourceKind{}, invalidArgument(
			"%q is ambiguous in cluster %q: %s — name one of those ids", wanted, id, strings.Join(ids, ", "))
	}
}

// scoped checks a namespace against a kind's scope.
//
// A namespace on a cluster-scoped kind is refused rather than ignored: it
// means the caller believes the object lives somewhere it cannot, and
// answering anyway confirms a false belief.
func scoped(kind domain.ResourceKind, namespace domain.NamespaceName) (domain.NamespaceName, error) {
	if !kind.Namespaced {
		if !namespace.IsAll() {
			return "", invalidArgument("%s is cluster-scoped and takes no namespace", kind.Kind)
		}
		return domain.NamespaceAll, nil
	}
	return namespace, nil
}

// --- handlers -------------------------------------------------------------

func (t *toolset) listClusters(ctx context.Context, _ Arguments) (any, error) {
	clusters, err := t.clusters.ListClusters(ctx)
	if err != nil {
		return nil, err
	}

	connections, err := t.clusters.Connections(ctx)
	if err != nil {
		return nil, err
	}
	open := make(map[domain.ClusterID]domain.Cluster, len(connections))
	for _, cluster := range connections {
		open[cluster.ID()] = cluster
	}

	return map[string]any{"clusters": renderClusters(clusters, open)}, nil
}

func (t *toolset) listNamespaces(ctx context.Context, args Arguments) (any, error) {
	id, err := t.cluster(ctx, args)
	if err != nil {
		return nil, err
	}

	namespaces, err := t.clusters.ListNamespaces(ctx, id)
	if err != nil {
		return nil, err
	}

	rendered := renderNamespaces(namespaces, t.now())
	return limited(id, domain.NamespaceAll, rendered, len(rendered), maxRows), nil
}

func (t *toolset) listKinds(ctx context.Context, args Arguments) (any, error) {
	id, err := t.cluster(ctx, args)
	if err != nil {
		return nil, err
	}

	kinds, err := t.kinds.Kinds(ctx, id)
	if err != nil {
		return nil, err
	}

	rendered := renderKinds(kinds)
	return limited(id, domain.NamespaceAll, rendered, len(rendered), maxRows), nil
}

func (t *toolset) listPods(ctx context.Context, args Arguments) (any, error) {
	id, err := t.cluster(ctx, args)
	if err != nil {
		return nil, err
	}

	namespace, err := namespaceOf(args, "namespace")
	if err != nil {
		return nil, err
	}

	node := args.String("node")
	if node != "" && !namespace.IsAll() {
		// Two scopes at once. Answering one of them would silently ignore
		// half of what was asked for.
		return nil, invalidArgument("give either namespace or node, not both: pods on a node are listed across every namespace")
	}

	var pods []domain.Pod
	if node != "" {
		pods, err = t.workloads.ListPodsOnNode(ctx, id, node)
	} else {
		// The empty projection: annotations travel only where somebody
		// configured a column for them, and there are no columns here.
		pods, err = t.workloads.ListPods(ctx, id, namespace, domain.Projection{})
	}
	if err != nil {
		return nil, err
	}

	rendered := renderPods(pods, t.now())
	return limited(id, namespace, rendered, len(rendered), int(args.Int("limit", defaultRows))), nil
}

func (t *toolset) listWorkloads(ctx context.Context, args Arguments) (any, error) {
	id, err := t.cluster(ctx, args)
	if err != nil {
		return nil, err
	}

	namespace, err := namespaceOf(args, "namespace")
	if err != nil {
		return nil, err
	}

	kind := domain.WorkloadKind(args.String("kind"))
	workloads, err := t.workloads.ListWorkloads(ctx, id, kind, namespace, domain.Projection{})
	if err != nil {
		return nil, err
	}

	rendered := renderWorkloads(workloads, t.now())
	return limited(id, namespace, rendered, len(rendered), int(args.Int("limit", defaultRows))), nil
}

func (t *toolset) listNodes(ctx context.Context, args Arguments) (any, error) {
	id, err := t.cluster(ctx, args)
	if err != nil {
		return nil, err
	}

	nodes, err := t.clusters.ListNodes(ctx, id, domain.Projection{})
	if err != nil {
		return nil, err
	}

	rendered := renderNodes(nodes, t.now())
	return limited(id, domain.NamespaceAll, rendered, len(rendered), int(args.Int("limit", defaultRows))), nil
}

func (t *toolset) listResources(ctx context.Context, args Arguments) (any, error) {
	id, err := t.cluster(ctx, args)
	if err != nil {
		return nil, err
	}

	kind, err := t.resolveKind(ctx, id, args.String("kind"))
	if err != nil {
		return nil, err
	}

	namespace, err := namespaceOf(args, "namespace")
	if err != nil {
		return nil, err
	}
	namespace, err = scoped(kind, namespace)
	if err != nil {
		return nil, err
	}

	table, err := t.resources.ListTable(ctx, id, kind.ID(), namespace, domain.Projection{})
	if err != nil {
		return nil, err
	}

	return renderTable(table, int(args.Int("limit", defaultRows))), nil
}

// manifestOf reads one object, always with secrets redacted.
//
// The single place a manifest is fetched in this package, so revealSecrets is
// false in exactly one expression: two call sites would be two chances for
// the next one to be written differently.
func (t *toolset) manifestOf(ctx context.Context, id domain.ClusterID, kind domain.ResourceKind, namespace domain.NamespaceName, name string) (string, error) {
	return t.resources.GetManifest(ctx, id, kind.ID(), namespace, name, false)
}

// objectArgs resolves the cluster, kind, namespace and name every
// single-object tool takes.
func (t *toolset) objectArgs(ctx context.Context, args Arguments) (domain.ClusterID, domain.ResourceKind, domain.NamespaceName, string, error) {
	id, err := t.cluster(ctx, args)
	if err != nil {
		return "", domain.ResourceKind{}, "", "", err
	}

	kind, err := t.resolveKind(ctx, id, args.String("kind"))
	if err != nil {
		return "", domain.ResourceKind{}, "", "", err
	}

	namespace, err := namespaceOf(args, "namespace")
	if err != nil {
		return "", domain.ResourceKind{}, "", "", err
	}
	namespace, err = scoped(kind, namespace)
	if err != nil {
		return "", domain.ResourceKind{}, "", "", err
	}

	return id, kind, namespace, args.String("name"), nil
}

type manifestOut struct {
	Cluster   string `json:"cluster"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	// Manifest is YAML. A Secret's values are replaced by their decoded size
	// before the object is serialised, so this can be read and quoted freely.
	Manifest string `json:"manifest"`
	// SecretsRedacted is stated rather than assumed: a reader that did not
	// know values were hidden could report an empty-looking Secret as
	// misconfigured.
	SecretsRedacted bool `json:"secretsRedacted"`
}

func (t *toolset) getManifest(ctx context.Context, args Arguments) (any, error) {
	id, kind, namespace, name, err := t.objectArgs(ctx, args)
	if err != nil {
		return nil, err
	}

	manifest, err := t.manifestOf(ctx, id, kind, namespace, name)
	if err != nil {
		return nil, err
	}

	return manifestOut{
		Cluster:         id.String(),
		Kind:            kind.Kind,
		Namespace:       namespace.String(),
		Name:            name,
		Manifest:        manifest,
		SecretsRedacted: kind.Kind == "Secret",
	}, nil
}

type describeOut struct {
	manifestOut
	Events []eventRow `json:"events"`
}

func (t *toolset) describeResource(ctx context.Context, args Arguments) (any, error) {
	id, kind, namespace, name, err := t.objectArgs(ctx, args)
	if err != nil {
		return nil, err
	}

	manifest, err := t.manifestOf(ctx, id, kind, namespace, name)
	if err != nil {
		return nil, err
	}

	// The events are the half kubectl describe is read for. They are not
	// worth failing the whole call over: an account may read an object and
	// not its namespace's events, and a manifest with no commentary beats a
	// refusal with neither.
	events, eventsErr := t.events.ListEventsForResource(ctx, id, namespace, kind.Kind, name)

	described := describeOut{
		manifestOut: manifestOut{
			Cluster:         id.String(),
			Kind:            kind.Kind,
			Namespace:       namespace.String(),
			Name:            name,
			Manifest:        manifest,
			SecretsRedacted: kind.Kind == "Secret",
		},
		Events: renderEvents(events, t.now()),
	}
	if eventsErr != nil {
		return struct {
			describeOut
			EventsUnreadable string `json:"eventsUnreadable"`
		}{describeOut: described, EventsUnreadable: eventsErr.Error()}, nil
	}

	return described, nil
}

type logsOut struct {
	Cluster   string   `json:"cluster"`
	Namespace string   `json:"namespace"`
	Pod       string   `json:"pod"`
	Container string   `json:"container,omitempty"`
	Previous  bool     `json:"previous,omitempty"`
	Lines     []string `json:"lines"`
	// Truncated says the tail was longer than what was returned, so an
	// absence of the line somebody is looking for is not evidence.
	Truncated bool `json:"truncated,omitempty"`
}

func (t *toolset) getLogs(ctx context.Context, args Arguments) (any, error) {
	id, err := t.cluster(ctx, args)
	if err != nil {
		return nil, err
	}

	namespace, err := namespaceOf(args, "namespace")
	if err != nil {
		return nil, err
	}
	if namespace.IsAll() {
		return nil, invalidArgument("a log read needs the pod's namespace")
	}

	tail := args.Int("tail_lines", defaultLogLines)
	opts := domain.LogOptions{
		// NEVER FOLLOWS. A follow has no end, and a tool call that does not
		// return is an agent that has stopped. Somebody watching a stream
		// wants the log viewer in the window.
		Follow:       false,
		TailLines:    tail,
		SinceSeconds: args.Int("since_seconds", 0),
		Previous:     args.Bool("previous"),
		Timestamps:   true,
		LimitBytes:   logLimitBytes,
	}

	// StreamLogs blocks until the stream ends and closes the channel itself,
	// so it runs beside the drain rather than before it.
	out := make(chan string, 64)
	failed := make(chan error, 1)
	go func() {
		failed <- t.logs.StreamLogs(ctx, id, namespace, args.String("pod"), args.String("container"), opts, out)
	}()

	lines := make([]string, 0, int(min(tail, 512)))
	truncated := false
	for line := range out {
		if int64(len(lines)) >= tail {
			// Drained rather than abandoned: leaving the channel unread
			// would block the streaming goroutine on its next send and leak
			// it for as long as the process runs.
			truncated = true
			continue
		}
		lines = append(lines, line)
	}
	if err := <-failed; err != nil {
		return nil, err
	}

	return logsOut{
		Cluster:   id.String(),
		Namespace: namespace.String(),
		Pod:       args.String("pod"),
		Container: args.String("container"),
		Previous:  opts.Previous,
		Lines:     lines,
		Truncated: truncated,
	}, nil
}

func (t *toolset) listEvents(ctx context.Context, args Arguments) (any, error) {
	id, err := t.cluster(ctx, args)
	if err != nil {
		return nil, err
	}

	namespace, err := namespaceOf(args, "namespace")
	if err != nil {
		return nil, err
	}

	events, err := t.events.ListEvents(ctx, id, namespace, domain.Projection{})
	if err != nil {
		return nil, err
	}

	if args.Bool("warnings_only") {
		warnings := make([]domain.Event, 0, len(events))
		for _, event := range events {
			if event.IsWarning() {
				warnings = append(warnings, event)
			}
		}
		events = warnings
	}

	rendered := renderEvents(events, t.now())
	return limited(id, namespace, rendered, len(rendered), int(args.Int("limit", defaultRows))), nil
}

func (t *toolset) assessCluster(ctx context.Context, args Arguments) (any, error) {
	id, err := t.cluster(ctx, args)
	if err != nil {
		return nil, err
	}

	overview, err := t.overview.Overview(ctx, id)
	if err != nil {
		return nil, err
	}

	return renderOverview(overview), nil
}

func (t *toolset) assessPod(ctx context.Context, args Arguments) (any, error) {
	id, err := t.cluster(ctx, args)
	if err != nil {
		return nil, err
	}

	namespace, err := namespaceOf(args, "namespace")
	if err != nil {
		return nil, err
	}
	if namespace.IsAll() {
		return nil, invalidArgument("an assessment needs the pod's namespace")
	}

	name := args.String("pod")

	// The namespace's list rather than a GET of the one pod, because that is
	// what the assessment is a function of: domain.AssessPod reads a
	// domain.Pod carrying its metrics and its derived state, which is what
	// the list path builds. It is also the read the pod list already makes,
	// so it coalesces with anything else asking in the same instant.
	pods, err := t.workloads.ListPods(ctx, id, namespace, domain.Projection{})
	if err != nil {
		return nil, err
	}

	for _, pod := range pods {
		if pod.Name() != name {
			continue
		}
		now := t.now()
		return podAssessmentOut{
			Pod:        renderPod(pod, now),
			Containers: renderContainers(pod.Containers()),
			Findings:   renderPodFindings(domain.AssessPod(pod, now)),
			Conditions: renderConditions(pod.Conditions()),
		}, nil
	}

	// NOT an empty result. A pod the list did not carry may have been
	// deleted, may be in another namespace, or may never have existed, and
	// all three are worth saying rather than returning an assessment with no
	// findings — which reads as a healthy pod.
	return nil, fmt.Errorf("no pod %q in namespace %q of cluster %q: %w", name, namespace, id, ports.ErrNotFound)
}

func (t *toolset) dependencyMap(ctx context.Context, args Arguments) (any, error) {
	id, kind, namespace, name, err := t.objectArgs(ctx, args)
	if err != nil {
		return nil, err
	}

	// THREE SHAPES, NOT ONE, and the subject decides which — a pod's map is a
	// chain, a controller's is a fan over the pods it currently has, and any
	// other object's is a neighbourhood. See app/domain/graph.go.
	var graph domain.PodGraph
	switch {
	case kind.Kind == "Pod":
		if namespace.IsAll() {
			return nil, invalidArgument("a pod's map needs its namespace")
		}
		graph, err = t.workloads.PodGraph(ctx, id, namespace, name)

	case slices.Contains(domain.WorkloadKinds(), domain.WorkloadKind(kind.Kind)):
		if namespace.IsAll() {
			return nil, invalidArgument("a workload's map needs its namespace")
		}
		graph, err = t.workloads.WorkloadGraph(ctx, id, namespace, domain.WorkloadKind(kind.Kind), name)

	default:
		graph, err = t.resources.ObjectGraph(ctx, id, kind.ID(), namespace, name)
	}
	if err != nil {
		return nil, err
	}

	return renderGraph(graph), nil
}

func (t *toolset) namespaceInventory(ctx context.Context, args Arguments) (any, error) {
	id, err := t.cluster(ctx, args)
	if err != nil {
		return nil, err
	}

	namespace, err := namespaceOf(args, "namespace")
	if err != nil {
		return nil, err
	}

	inventory, err := t.resources.NamespaceInventory(ctx, id, namespace)
	if err != nil {
		return nil, err
	}

	return renderInventory(inventory), nil
}

func (t *toolset) subjectRules(ctx context.Context, args Arguments) (any, error) {
	id, err := t.cluster(ctx, args)
	if err != nil {
		return nil, err
	}

	namespace, err := namespaceOf(args, "namespace")
	if err != nil {
		return nil, err
	}

	rules, err := t.rbac.SubjectRules(ctx, id, namespace)
	if err != nil {
		return nil, err
	}

	return renderSubjectRules(rules), nil
}

func (t *toolset) canI(ctx context.Context, args Arguments) (any, error) {
	id, err := t.cluster(ctx, args)
	if err != nil {
		return nil, err
	}

	namespace, err := namespaceOf(args, "namespace")
	if err != nil {
		return nil, err
	}

	subjectNamespace, err := namespaceOf(args, "subject_namespace")
	if err != nil {
		return nil, err
	}

	request := domain.AccessRequest{
		Verb:        args.String("verb"),
		Group:       args.String("group"),
		Resource:    args.String("resource"),
		Subresource: args.String("subresource"),
		Namespace:   namespace,
		Name:        args.String("name"),
	}

	if kind := args.String("subject_kind"); kind != "" {
		if args.String("subject_name") == "" {
			return nil, invalidArgument("subject_kind needs subject_name")
		}
		request.Subject = domain.RBACSubject{
			Kind:      domain.SubjectKind(kind),
			Name:      args.String("subject_name"),
			Namespace: subjectNamespace,
		}
	}

	decision, err := t.rbac.CanI(ctx, id, request)
	if err != nil {
		return nil, err
	}

	return renderAccessDecision(decision), nil
}

func (t *toolset) inspectRole(ctx context.Context, args Arguments) (any, error) {
	id, err := t.cluster(ctx, args)
	if err != nil {
		return nil, err
	}

	namespace, err := namespaceOf(args, "namespace")
	if err != nil {
		return nil, err
	}

	scope := domain.RoleScope(args.String("scope"))
	if scope == domain.RoleScopeNamespace && namespace.IsAll() {
		return nil, invalidArgument("a Role is namespaced: give the namespace it lives in")
	}

	inspection, err := t.rbac.InspectRole(ctx, id, domain.RoleTarget{
		Scope:     scope,
		Namespace: namespace,
		Name:      args.String("name"),
	})
	if err != nil {
		return nil, err
	}

	return renderRoleInspection(inspection), nil
}
