package ports

import (
	"context"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// ClusterService is the use-case surface for choosing, connecting to and
// describing clusters.
//
// Connections are plural on purpose: the UI shows one tab per connected
// cluster, and closing a tab must not disturb the others.
type ClusterService interface {
	// ListClusters returns every cluster PodSteer knows about, in a stable
	// order suitable for direct display.
	ListClusters(ctx context.Context) ([]domain.Cluster, error)

	// Connect verifies that the given cluster answers, opens a connection to
	// it and discovers its custom resource kinds. Connecting an already
	// connected cluster is not an error — it refreshes it.
	Connect(ctx context.Context, id domain.ClusterID) (domain.Cluster, error)

	// Disconnect closes a connection and releases everything cached for it.
	Disconnect(ctx context.Context, id domain.ClusterID) error

	// Connections returns the currently connected clusters, in the order they
	// were connected, so the tab bar does not reorder itself on refresh.
	Connections(ctx context.Context) ([]domain.Cluster, error)

	// ListNamespaces returns the namespaces of a connected cluster.
	ListNamespaces(ctx context.Context, id domain.ClusterID) ([]domain.Namespace, error)

	// ListNamespaceSummaries returns the same namespaces with what is running
	// in each — the list view, where ListNamespaces serves the filter. Each
	// carries the annotations projection asks for; see domain.Projection.
	ListNamespaceSummaries(ctx context.Context, id domain.ClusterID, projection domain.Projection) ([]domain.NamespaceSummary, error)

	// ListNodes returns the nodes of a connected cluster, each carrying the
	// annotations projection asks for.
	ListNodes(ctx context.Context, id domain.ClusterID, projection domain.Projection) ([]domain.Node, error)

	// PreviewKubeconfig reports what adding raw to the kubeconfig would
	// change, without touching the file.
	PreviewKubeconfig(ctx context.Context, raw string) (domain.KubeconfigMerge, error)

	// AddKubeconfig adds raw to the kubeconfig and reports what changed.
	AddKubeconfig(ctx context.Context, raw string) (domain.KubeconfigMerge, error)

	// SetReadOnly marks a connected cluster read-only, or lifts the mark.
	//
	// The policy originates on the client: OrganiseDialog's toggle calls this
	// right after Connect succeeds, and again whenever the group setting or
	// the cluster's group changes. It is a guard against the frontend's own
	// bugs, never a permission — see ports.ErrReadOnly, which every write in
	// ManagementService returns while the mark is set. Fails with
	// domain.ErrClusterNotConnected wrapped if id is not currently open.
	SetReadOnly(ctx context.Context, id domain.ClusterID, readOnly bool) error
}

// NavigationService describes what a connected cluster can show.
//
// The navigator tree is built from this rather than hard-coded in the
// frontend, so a cluster's own CRDs appear in it without a frontend change,
// and a kind the credentials cannot read can be hidden centrally.
type NavigationService interface {
	// Kinds returns every browsable kind in the cluster, built-in first and
	// then whatever discovery found.
	Kinds(ctx context.Context, id domain.ClusterID) ([]domain.ResourceKind, error)
}

// WorkloadService is the use-case surface for reading workloads.
type WorkloadService interface {
	// ListPods returns pods in the given namespace of a connected cluster,
	// enriched with metrics where the cluster provides them, each carrying
	// the annotations projection asks for — see domain.Projection.
	ListPods(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, projection domain.Projection) ([]domain.Pod, error)

	// ListWorkloads returns controllers of the given kind, each carrying the
	// annotations projection asks for.
	ListWorkloads(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName, projection domain.Projection) ([]domain.Workload, error)

	// PodGraph returns the dependency chain around one pod, from whatever
	// routes to it down to its containers and what it consumes.
	PodGraph(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName string) (domain.PodGraph, error)

	// WorkloadGraph returns the dependency chain around one workload: what
	// routes to it, the pods it currently has, and what they consume.
	WorkloadGraph(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, kind domain.WorkloadKind, name string) (domain.PodGraph, error)

	// ListPodsOnNode returns the pods the scheduler has placed on one node,
	// across every namespace — "what is running on this machine" is a question
	// about the machine, not about a namespace.
	ListPodsOnNode(ctx context.Context, id domain.ClusterID, nodeName string) ([]domain.Pod, error)

	// ListPodsForWorkload returns all pods owned by a specific workload.
	ListPodsForWorkload(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, kind domain.WorkloadKind, name string) ([]domain.Pod, error)

	// RolloutHistory returns the recorded revisions of a Deployment,
	// StatefulSet or DaemonSet's pod template, newest first. Only those
	// three kinds carry a rollout history; any other kind is refused with
	// domain.ErrUnsupportedWorkloadKind before the port below is reached.
	RolloutHistory(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName, name string) ([]domain.Revision, error)

	// DrainCandidates returns the pods on a node with the extra facts a
	// drain plan needs. See WorkloadPort.DrainCandidates.
	//
	// Lives here rather than on ManagementService: it is a read like
	// ListPodsOnNode beside it, not a write, and ManagementAPI borrows it —
	// through this service, not the outbound port directly, so a drain
	// preview fails the same way every other read does against a cluster
	// that has since been disconnected — to build the preview PlanDrain
	// shows before a drain runs.
	DrainCandidates(ctx context.Context, id domain.ClusterID, nodeName string) ([]domain.DrainCandidate, error)

	// WorkloadUsage sums what a controller's pods are consuming, against what
	// they reserved and what they will be stopped at.
	WorkloadUsage(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, kind domain.WorkloadKind, name string) (domain.AggregateUsage, error)

	// ListApplications groups a cluster's workloads by the application they
	// belong to, using Kubernetes' own recommended labels.
	ListApplications(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) (domain.ApplicationInventory, error)

	// WorkloadConsumption does the same for a whole list, keyed by
	// "namespace/name".
	//
	// Separate from ListWorkloads so a list still renders on a cluster with
	// no metrics API, or for an account that cannot list pods: the
	// controllers are one cheap read, and this is the namespace's pods.
	WorkloadConsumption(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName) (map[string]domain.AggregateUsage, error)
}

// EventService is the use-case surface for reading Kubernetes Events.
type EventService interface {
	// ListEvents returns events most-recent first, since an event list is
	// almost always read from the top, each carrying the annotations
	// projection asks for.
	ListEvents(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, projection domain.Projection) ([]domain.Event, error)

	// ListEventsForResource returns events for a specific resource.
	ListEventsForResource(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, kind, name string) ([]domain.Event, error)
}

// FleetService is the use-case surface for reading across several open
// clusters at once — the merged Pods, Workloads and Events tables.
//
// Every read answers PER CLUSTER and never fails because one cluster did: a
// refused, unreachable or slow cluster is reported in its own ClusterRead
// beside the others' rows, so the table renders whatever did answer. The one
// error is naming a cluster that is not open, which fails the whole call with
// domain.ErrClusterNotConnected wrapped — the caller asked for something that
// does not exist. Results follow the registry's tab order whatever order the
// ids came in, and a repeated id is read once.
//
// Each cluster's share is the same read its own tab makes, through
// WorkloadService and EventService, so the adapter's read cache coalesces a
// fleet read with the tab's poll when the two land in the same tick.
type FleetService interface {
	// ListPods lists pods in the given namespace of each cluster.
	ListPods(ctx context.Context, ids []domain.ClusterID, namespace domain.NamespaceName) ([]domain.ClusterRead[domain.Pod], error)

	// ListWorkloads lists every controller kind in domain.FleetWorkloadKinds
	// in the given namespace of each cluster. A cluster that refuses some
	// kinds and serves others is reported Partial, naming the kinds missing.
	ListWorkloads(ctx context.Context, ids []domain.ClusterID, namespace domain.NamespaceName) ([]domain.ClusterRead[domain.Workload], error)

	// ListEvents lists events in the given namespace of each cluster.
	ListEvents(ctx context.Context, ids []domain.ClusterID, namespace domain.NamespaceName) ([]domain.ClusterRead[domain.Event], error)
}

// OverviewService is the use-case surface for the cluster dashboard.
//
// It is a use case of its own rather than a method on ClusterService because
// it composes almost every other read — nodes, pods, controllers, events and
// metrics — into a single assessment, and because it must succeed when several
// of those reads fail.
type OverviewService interface {
	// Overview assesses a connected cluster: what is wrong, what capacity is
	// left, and what the cluster is made of.
	//
	// Sources that could not be read are named in the result rather than
	// returned as an error. An error means the whole assessment failed, which
	// in practice means the cluster is not connected.
	Overview(ctx context.Context, id domain.ClusterID) (domain.Overview, error)

	// OverviewForTarget assesses a connected cluster the same way, but scores
	// the upgrade-impact findings against a specific Kubernetes minor rather
	// than the default of the next one after the cluster's current version —
	// what the overview's "check against" selector asks for. targetMinor is
	// e.g. "1.33"; an unparseable or out-of-range one degrades to no
	// upgrade-impact findings rather than an error, the same way an unknown
	// version degrades everywhere else in this package.
	OverviewForTarget(ctx context.Context, id domain.ClusterID, targetMinor string) (domain.Overview, error)
}

// HistoryService is the use-case surface for a cluster's recorded history.
//
// The history covers the window the application has been open, which is a
// weaker promise than a monitoring stack makes and must be presented as one:
// Series reports the span it actually holds so the UI can say so.
type HistoryService interface {
	// Series returns a cluster's samples over the given window, downsampled
	// to at most maxPoints.
	Series(ctx context.Context, id domain.ClusterID, window time.Duration, maxPoints int) (domain.Series, error)

	// Retention reports how long samples are kept.
	Retention(ctx context.Context) (domain.Retention, error)

	// SetRetention changes how long samples are kept, and immediately
	// discards anything already outside the new window — including
	// everything, when retention is set to zero.
	SetRetention(ctx context.Context, retention domain.Retention) error

	// SamplingInterval reports how often each open cluster is sampled.
	SamplingInterval() time.Duration

	// SetSamplingInterval changes the cadence, taking effect at once rather
	// than after the current interval elapses.
	SetSamplingInterval(interval time.Duration) error
}

// RBACService is the use-case surface for the RBAC explorer.
//
// Three questions, three calls, and every one of them a read that runs
// because somebody pressed something — none of it is on the refresh tick, and
// none of it is cached. An allow or deny decision must never be shown from a
// previous instant: a permission revoked a minute ago has to stop reading as
// granted, which a cached answer cannot promise.
//
// The statuses on the returned values do the work an error would otherwise
// do badly. An account not permitted to ask a review is the ordinary case
// here rather than an exceptional one, and it is reported in the result so
// the rest of the panel still renders — the same shape domain.MetricsStatus
// gives the overview.
type RBACService interface {
	// SubjectRules answers "what can this kubeconfig actually do here" for
	// one namespace of a connected cluster.
	SubjectRules(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) (domain.SubjectRules, error)

	// CanI answers one access review, for the current account or for a named
	// user, group or service account.
	//
	// The API server's own allowed, denied and reason are carried across
	// verbatim. Refuses with domain.ErrInvalidAccessRequest, wrapped, when
	// the request names no verb or nothing to act on, before any request
	// reaches the cluster.
	CanI(ctx context.Context, id domain.ClusterID, request domain.AccessRequest) (domain.AccessDecision, error)

	// InspectRole reads one Role or ClusterRole, finds the bindings that
	// reference it, and assesses what its rules permit.
	//
	// ONE CALL FOR THE WHOLE PANEL — the role, two binding lists and the
	// assessment — so opening it is a bounded, countable three requests
	// rather than a fan-out that grows with the cluster. The role and the
	// bindings report separately: an account may read a ClusterRole without
	// being able to list the bindings to it, and one refusal must not blank
	// the half that answered.
	InspectRole(ctx context.Context, id domain.ClusterID, target domain.RoleTarget) (domain.RoleInspection, error)
}

// InspectService is the use-case surface for the on-request inspections: a
// reachability probe, and a container image report.
//
// EVERY METHOD HERE IS THE CONSEQUENCE OF A BUTTON, and nothing on this
// surface may ever be called by the refresh tick — see ports.InspectPort,
// where the same rule is stated for the driven side, and BrowseAPI.ObjectGraph
// for the precedent. A probe opens a socket or runs a command in somebody's
// container; a repeated one would be a stream of execs in an audit log nobody
// asked for.
type InspectService interface {
	// ProbeFromHere probes subject from THIS MACHINE, through the API server
	// named in the kubeconfig and through nothing else. Refuses with a
	// domain.ErrProbe… sentinel when the subject cannot be probed from here —
	// an Ingress host, a headless Service, a UDP port — each of which is a
	// fact the caller renders where a result would have gone.
	ProbeFromHere(ctx context.Context, id domain.ClusterID, subject domain.ProbeSubject) (domain.ProbeResult, error)

	// ProbeFromPod probes subject from inside a container the operator chose,
	// as one bounded exec. Refused on a cluster marked read-only, and audited
	// by cluster, namespace, pod, container and target — never by output.
	// Wraps ErrProbeToolMissing when the container has nothing to probe with.
	ProbeFromPod(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName string, subject domain.ProbeSubject) (domain.ProbeResult, error)

	// ImageReport describes one container's image using only what Kubernetes
	// reports: the resolved reference and digest, the size and names recorded
	// by the node that pulled it, and whether the pull needed credentials. It
	// reads no registry and no pull Secret, and the report says so — see
	// domain.ImageDetailBounded.
	ImageReport(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName string) (domain.ImageReport, error)
}

// ResourceService is the use-case surface for the generic browsing path.
type ResourceService interface {
	// ListTable returns objects of the given kind as a table. The kind is
	// named by its ResourceKind.ID, which is what the navigator hands back.
	// Each row carries its labels and the annotations projection asks for.
	ListTable(ctx context.Context, id domain.ClusterID, kindID string, namespace domain.NamespaceName, projection domain.Projection) (domain.ResourceTable, error)

	// NamespaceInventory reports what one namespace holds, kind by kind.
	//
	// A use case rather than a port method because the answer is assembled:
	// Kubernetes has no endpoint that counts a namespace's contents, so this
	// is one count per kind, and which kinds are worth counting is a decision
	// the domain makes.
	NamespaceInventory(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) (domain.NamespaceInventory, error)

	// GetManifest returns one object as YAML.
	GetManifest(ctx context.Context, id domain.ClusterID, kindID string, namespace domain.NamespaceName, name string, revealSecrets bool) (string, error)

	// ObjectGraph returns the neighbourhood of one object of ANY kind: the
	// subject in the middle, what its spec names below it, what owns it above.
	//
	// The third map shape, beside a pod's chain and a workload's fan. Those
	// two stay separate because the subject decides the structure; this one
	// covers everything the generic table lists — a Service, a ConfigMap, a
	// PVC, a CRD instance — where the only structure that holds is "some
	// objects are named by this one and some name it".
	ObjectGraph(ctx context.Context, id domain.ClusterID, kindID string, namespace domain.NamespaceName, name string) (domain.PodGraph, error)

	// RevealSecretKey returns one decoded Secret value, on explicit request.
	RevealSecretKey(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name, key string) (string, error)

	// InspectTLSSecret returns one Secret's parsed certificate chain, on
	// explicit request — the certificate equivalent of RevealSecretKey.
	InspectTLSSecret(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name string) (domain.CertificateChain, error)

	// VulnerabilitySummaries returns what a vulnerability scanner already
	// running in the cluster has recorded about one namespace's workloads.
	//
	// An empty answer is the ordinary one — most clusters run no scanner —
	// and is never an error. See ports.ResourcePort for why this is not part
	// of any list call.
	VulnerabilitySummaries(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) ([]domain.VulnerabilitySummary, error)
}
