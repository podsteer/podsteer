package ports

import (
	"context"
	"io"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// Every port below takes the cluster id explicitly rather than being bound to
// one connection. That is what lets PodSteer hold several clusters open at once
// — one per tab — and query them concurrently. A stateful "current cluster"
// would serialise them and make the tab bar a lie.

// KubeconfigPort discovers the clusters this machine is configured to talk to.
//
// Deliberately separate from the API ports: enumerating contexts is a local
// filesystem concern that must keep working when every cluster in the file is
// unreachable. Folding it in would make the cluster picker fail whenever the
// VPN is down — precisely when the operator needs it.
type KubeconfigPort interface {
	// Clusters returns every cluster described by the local kubeconfig.
	//
	// Contexts that cannot be turned into a valid domain.Cluster are skipped
	// rather than failing the call: one malformed entry must not hide the rest
	// of the file. An error is returned only when nothing could be read at
	// all, wrapping ErrKubeconfigUnavailable.
	Clusters(ctx context.Context) ([]domain.Cluster, error)

	// Cluster returns the single cluster with the given id, wrapping
	// domain.ErrClusterNotFound when the kubeconfig describes no such context.
	Cluster(ctx context.Context, id domain.ClusterID) (domain.Cluster, error)

	// PreviewMerge reports what adding raw to the kubeconfig would change,
	// without touching the file. Wraps ErrKubeconfigInvalid when raw is not a
	// kubeconfig, or is one that describes no contexts.
	PreviewMerge(ctx context.Context, raw string) (domain.KubeconfigMerge, error)

	// Merge adds raw to the kubeconfig and reports what changed.
	//
	// This is the ONLY write PodSteer makes to the kubeconfig, and it is
	// deliberately narrow: it adds contexts and refuses to replace them,
	// wrapping ErrKubeconfigConflict when the incoming config names one that
	// already exists.
	Merge(ctx context.Context, raw string) (domain.KubeconfigMerge, error)
}

// ClusterPort reads cluster-scoped facts from an API server.
type ClusterPort interface {
	// ServerVersion reaches the API server and reports its version. It doubles
	// as the reachability probe used when connecting.
	ServerVersion(ctx context.Context, id domain.ClusterID) (domain.ServerVersion, error)

	// ListNamespaces returns every namespace visible to the credentials.
	//
	// projection names the annotation keys each namespace should carry —
	// see domain.Projection. The zero value carries none, and is what every
	// caller that is not the namespace list view passes.
	ListNamespaces(ctx context.Context, id domain.ClusterID, projection domain.Projection) ([]domain.Namespace, error)

	// ListNodes returns the cluster's nodes, each carrying the annotations
	// projection asks for.
	ListNodes(ctx context.Context, id domain.ClusterID, projection domain.Projection) ([]domain.Node, error)

	// ListPersistentVolumes returns the cluster's provisioned volumes.
	ListPersistentVolumes(ctx context.Context, id domain.ClusterID) ([]domain.PersistentVolume, error)

	// ListPersistentVolumeClaims returns the claims made against them.
	ListPersistentVolumeClaims(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) ([]domain.PersistentVolumeClaim, error)

	// DiscoverCustomKinds returns the kinds served by CustomResourceDefinitions
	// installed in the cluster, for the navigator's Custom Resources section.
	//
	// Discovery is per cluster and cannot be cached across them: two clusters
	// routinely run different operators.
	DiscoverCustomKinds(ctx context.Context, id domain.ClusterID) ([]domain.ResourceKind, error)
}

// WorkloadPort reads pods and the controllers that manage them.
type WorkloadPort interface {
	// ListPods returns pods in the given namespace, or across every namespace
	// when it is domain.NamespaceAll.
	//
	// projection names the annotation keys each pod should carry — see
	// domain.Projection. THE PROJECTION IS PART OF THE READ: two calls with
	// different projections are different reads and are not coalesced with
	// each other, so an operator who has put an annotation on a column pays
	// one list per refresh beside the assessment's own instead of sharing
	// it. Labels are unaffected and always carried.
	ListPods(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, projection domain.Projection) ([]domain.Pod, error)

	// ListWorkloads returns controllers of the given kind, each carrying the
	// annotations projection asks for on top of the GitOps keys every row
	// carries anyway.
	ListWorkloads(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName, projection domain.Projection) ([]domain.Workload, error)

	// PodGraphSources reads what one pod's dependency map is drawn from.
	//
	// GATHERS RATHER THAN ASSEMBLES. Which service selects which pod is a
	// rule, and rules belong in the domain where they are tested; this returns
	// what was read and lets NewPodGraph decide what connects. Individual
	// sources may fail without failing the call — an account that can list
	// pods but not ingresses gets a map without an ingress tier, and
	// GraphInput.Unreadable names what was missing.
	PodGraphSources(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName string) (domain.GraphInput, error)

	// WorkloadGraphSources reads what one workload's dependency map is drawn
	// from — the same sources as a pod's, with its pods in place of the one.
	WorkloadGraphSources(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, kind domain.WorkloadKind, name string) (domain.WorkloadGraphInput, error)

	// ListPodsOnNode returns the pods the scheduler has placed on one node,
	// across every namespace.
	//
	// A FIELD SELECTOR, not a client-side filter over every pod in the
	// cluster. `spec.nodeName` is one of the handful of fields the API server
	// indexes for pods, so a cluster of fifty thousand pods returns the
	// hundred on this node rather than all of them for the caller to sift.
	ListPodsOnNode(ctx context.Context, id domain.ClusterID, nodeName string) ([]domain.Pod, error)

	// ListPodsForWorkload returns all pods owned by a specific workload.
	ListPodsForWorkload(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, kind domain.WorkloadKind, name string) ([]domain.Pod, error)

	// DrainCandidates returns the pods on a node with the extra facts a
	// drain plan needs — whether each is a mirror pod the API server cannot
	// delete, and whether it holds local storage a plan should not discard
	// without being told to. See domain.DrainCandidate.
	//
	// Reuses the same field-selected listing as ListPodsOnNode rather than
	// filtering a cluster-wide list, for the same reason: "what is on this
	// machine" costs one indexed request, not every pod in the cluster.
	DrainCandidates(ctx context.Context, id domain.ClusterID, nodeName string) ([]domain.DrainCandidate, error)

	// RolloutHistory returns the recorded revisions of a Deployment,
	// StatefulSet or DaemonSet's pod template, newest first — a
	// Deployment's ReplicaSets or a StatefulSet/DaemonSet's
	// ControllerRevisions, resolved by ownerReference and never by label
	// selector, mirroring ListPodsForWorkload's own rule. Only those three
	// kinds carry a rollout history; the application layer rejects any
	// other kind before this is reached.
	RolloutHistory(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName, name string) ([]domain.Revision, error)
}

// EventPort reads Kubernetes Events.
type EventPort interface {
	// ListEvents returns events in the given namespace, or across every
	// namespace when it is domain.NamespaceAll, each carrying the
	// annotations projection asks for.
	ListEvents(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, projection domain.Projection) ([]domain.Event, error)

	// ListEventsForResource returns events for a specific resource.
	ListEventsForResource(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, kind, name string) ([]domain.Event, error)
}

// MetricsPort reads resource consumption from the metrics API.
//
// Every method may fail with ErrMetricsUnavailable, and callers must treat
// that as normal rather than exceptional: metrics-server is an add-on, plenty
// of clusters do not run it, and a pod list must still render without it. That
// is the whole reason this is a port of its own — so the workload use case can
// degrade instead of failing.
type MetricsPort interface {
	// PodMetrics returns usage keyed by "namespace/name", each carrying both
	// the pod total and the per-container breakdown behind it.
	PodMetrics(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) (map[string]domain.PodUsage, error)

	// NodeMetrics returns usage keyed by node name.
	NodeMetrics(ctx context.Context, id domain.ClusterID) (map[string]domain.Metrics, error)

	// NodeFilesystems returns disk occupancy keyed by node name.
	//
	// Read from each kubelet rather than from an aggregated API, because no
	// aggregated API carries it: metrics-server serves CPU and memory only.
	// That means it needs the nodes/proxy permission, which plenty of
	// clusters do not grant — so like everything else here it may fail with
	// ErrMetricsUnavailable and the assessment must continue without it.
	//
	// A partial answer is a success: on a large cluster one unreachable
	// kubelet must not cost the other fifty.
	NodeFilesystems(ctx context.Context, id domain.ClusterID) (map[string]domain.NodeFilesystems, error)

	// DiscoverMetricsBackend looks for a monitoring system already running in
	// the cluster.
	//
	// FINDING NOTHING IS THE ORDINARY ANSWER, not an error: most clusters run
	// no Prometheus, and plenty of accounts cannot list services across
	// namespaces to find one. Both return a zero MetricsBackend, and the
	// caller carries on with the samples PodSteer takes itself.
	//
	// This exists because PodSteer's own history is bounded by how long the
	// application has been open, and a cluster that already keeps months of
	// the same figures should be pointed at rather than competed with.
	DiscoverMetricsBackend(ctx context.Context, id domain.ClusterID) (domain.MetricsBackend, error)
}

// HistoryPort stores and reads the samples PodSteer takes of a cluster.
//
// A port rather than a detail of the service because "keep this on disk for
// seven days" is a policy an operator sets, and because the store has to be
// swappable: the obvious next implementation records to something outside the
// application entirely.
type HistoryPort interface {
	// Append records one sample for a cluster.
	Append(ctx context.Context, id domain.ClusterID, sample domain.Sample) error

	// Series returns a cluster's samples taken at or after cutoff, oldest
	// first. A cluster with nothing recorded is not an error — it is the
	// ordinary state of one that was just connected.
	Series(ctx context.Context, id domain.ClusterID, cutoff time.Time) (domain.Series, error)

	// Prune discards samples older than cutoff for every cluster, and removes
	// everything when retention is disabled.
	Prune(ctx context.Context, cutoff time.Time) error

	// Forget discards everything recorded for one cluster.
	Forget(ctx context.Context, id domain.ClusterID) error
}

// ResourcePort reads any kind generically, including custom resources.
//
// It is what makes the navigator's long tail work without PodSteer modelling
// every kind by hand — and what makes a freshly installed operator's CRDs
// browsable the moment discovery notices them.
type ResourcePort interface {
	// ListTable returns objects of the given kind as a table, with the columns
	// the API server itself prints. Each row also carries the object's labels
	// and the annotations projection asks for, read from the metadata the
	// server attaches to the row — never from a further request per object.
	ListTable(ctx context.Context, id domain.ClusterID, kind domain.ResourceKind, namespace domain.NamespaceName, projection domain.Projection) (domain.ResourceTable, error)

	// CountResources reports how many objects of kind exist in namespace.
	//
	// ONE OBJECT IS FETCHED, NOT ALL OF THEM. The API server reports how many
	// more it did not send, so a namespace holding ten thousand Secrets costs
	// the same to count as one holding none — and, just as importantly, their
	// contents never leave the cluster to be counted here.
	//
	// Wraps ErrCountUnavailable when the server did not report a total, which
	// is the honest answer for one too old to (before Kubernetes 1.15) rather
	// than the "1" the single fetched object would otherwise imply.
	CountResources(ctx context.Context, id domain.ClusterID, kind domain.ResourceKind, namespace domain.NamespaceName) (int, error)

	// GetManifest returns one object serialised as YAML, for the detail view.
	GetManifest(ctx context.Context, ref domain.ResourceRef, revealSecrets bool) (string, error)

	// ObjectGraphSources reads what one object's neighbourhood map is drawn
	// from: its ownerReference chain upward, whatever its own spec names, and
	// — only for a kind Kubernetes answers cheaply for — what it selects.
	//
	// BOUNDED BY CONSTRUCTION, and that is the contract rather than an
	// implementation detail. Upward is capped at domain.ObjectOwnerDepth hops
	// and terminates on a cycle; outward is capped at a small number of
	// narrowed reads; downward is attempted ONLY where the answer is one list
	// of one kind in one namespace that the API server itself indexes. No
	// implementation may sweep the namespace looking for children — a request
	// per kind per open drawer is the polling storm the read cache exists to
	// prevent — and where the answer would need one, the map says so instead
	// (domain.DownwardBound).
	//
	// Gathers rather than assembles: which field names what is a rule, and
	// rules live in the domain. Individual sources degrade on their own, and
	// ObjectGraphInput.Unreadable names what was missing.
	ObjectGraphSources(ctx context.Context, ref domain.ResourceRef) (domain.ObjectGraphInput, error)

	// RevealSecretKey returns the decoded value of ONE key of one Secret.
	//
	// One key, never the whole Secret, and never as a side effect of anything
	// else — this is only ever called because somebody clicked to reveal that
	// key. Reading Secrets is an audited action that Kubernetes' own
	// good-practices page tells cluster operators to alert on, and Falco
	// ships an enabled rule for; a client that resolves every referenced
	// Secret when a pane opens generates exactly that signature on somebody
	// else's dashboard. Narrowing the call to a deliberate act keeps each
	// audit entry meaningful.
	RevealSecretKey(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name, key string) (string, error)

	// ListVulnerabilitySummaries returns the severity counts a vulnerability
	// scanner already running in the cluster has recorded for the workloads
	// in one namespace, keyed by "Kind/name".
	//
	// A DISCOVERED ADD-ON, QUOTED AND NEVER QUERIED — the same relationship
	// DiscoverMetricsBackend has with a monitoring stack, and it fails the
	// same way: no scanner installed, an account that may not read its
	// reports, and a namespace nothing has been scanned in all return an
	// EMPTY slice and no error. PodSteer scans nothing itself.
	//
	// It is deliberately not part of any list call. The pod list must never
	// wait on it, must never be short of a row because of it, and must never
	// ask for it on a refresh tick — see the adapter's own cache.
	ListVulnerabilitySummaries(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) ([]domain.VulnerabilitySummary, error)

	// InspectTLSSecret parses one Secret's certificate material, on explicit
	// request.
	//
	// The same discipline as RevealSecretKey and for the same reason: the
	// certificate is public material, but it lives inside the same Secret as
	// the private key, and reading that object is reading that object
	// whichever half was wanted. One deliberate act, never a side effect of
	// GetManifest or anything that runs when a pane opens.
	InspectTLSSecret(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name string) (domain.CertificateChain, error)
}

// RBACPort answers the Kubernetes authorization review APIs and reads the
// binding graph behind them.
//
// EVERY METHOD IS A READ, and the two review methods are reads that happen to
// be POSTs: a SubjectAccessReview and its relatives are created rather than
// fetched because the question IS the request body, but nothing is persisted
// and no object exists afterwards. A 403 on any of them is an ordinary answer
// about this account rather than a fault — see domain.ReviewStatus, which is
// modelled on domain.MetricsStatus for exactly that reason.
//
// THE API SERVER DECIDES. These methods carry its answer across unaltered;
// nothing in PodSteer evaluates rules to reach a verdict about what an
// account may do. See app/domain/rbac.go for where that line is drawn and
// what is on the other side of it.
type RBACPort interface {
	// SubjectRules asks what the CURRENT credentials may do in one namespace,
	// through SelfSubjectRulesReview — one request for the whole answer.
	//
	// The namespace is required by that API and a blank one is read as the
	// default namespace, because a rules review has no cluster-wide form: the
	// question "what may I do here" has to name a here.
	SubjectRules(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) (domain.RulesReview, error)

	// AccessReview asks whether one specific action is permitted.
	//
	// WHICH OF TWO APIS IT REACHES is decided by the request's subject: an
	// unnamed subject is the caller's own question and goes to
	// SelfSubjectAccessReview, which any authenticated account may ask; a
	// named user, group or service account goes to SubjectAccessReview, which
	// is privileged and routinely refused. Both answer in the same shape.
	AccessReview(ctx context.Context, id domain.ClusterID, request domain.AccessRequest) (domain.AccessOutcome, error)

	// RoleRules returns one Role or ClusterRole's own rules.
	RoleRules(ctx context.Context, id domain.ClusterID, target domain.RoleTarget) ([]domain.PolicyRule, error)

	// ListBindings returns every RoleBinding in the cluster and every
	// ClusterRoleBinding, with the subjects each carries.
	//
	// TWO LISTS, CLUSTER-WIDE, AND DELIBERATELY NOT NARROWED. A ClusterRole
	// is referenced by ClusterRoleBindings and by RoleBindings in any
	// namespace at all, so a lookup scoped to one namespace would report a
	// widely granted role as bound to nobody. It is bounded by WHEN it runs
	// instead: once, when the panel is opened, and never on a refresh tick.
	//
	// Which of them actually reference a given role is a rule with cases
	// worth arguing about, so it is domain.BindingsReferencing's decision
	// rather than this method's.
	ListBindings(ctx context.Context, id domain.ClusterID) ([]domain.RoleBindingRef, error)
}

// PortForwardPort opens local ports onto container ports.
//
// Deliberately narrow. There is no "restart", no "reconnect" and no
// persistence here, because each of those is a policy decision that belongs
// above the transport — and because every leak in the competing clients comes
// from the record of a forward and the goroutine running it being managed
// separately. Start hands back a forward that is already running; Stop waits
// for it to actually stop.
type PortForwardPort interface {
	// StartPortForward binds localPort onto the pod's remotePort. A localPort
	// of zero lets the operating system choose, and the returned Forward
	// carries whichever port was actually bound.
	//
	// selector is the pod's own labels, kept so a replacement can be found
	// when the pod goes away. Empty disables reconnection.
	StartPortForward(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, pod, podUID string, localPort, remotePort int, portName, protocol string, selector map[string]string) (domain.Forward, error)
	// StopPortForward closes a forward and WAITS for its port to be released,
	// so a caller may immediately rebind it.
	StopPortForward(id string) error
	// ListPortForwards reports what is forwarded right now.
	ListPortForwards() []domain.Forward
	// StopAllPortForwards tears everything down, for shutdown.
	StopAllPortForwards()
	// ProbeLocalPort reports whether a TCP port on THIS machine — not the
	// cluster — is free to bind, refusing anything outside 1-65535.
	//
	// Lives beside the transport rather than in the UI layer because binding
	// is the only truthful way to answer: a stale process, a container
	// runtime's proxy or a leaked Docker Desktop port all show as bound to
	// nothing a port list would show. Offered so the operator can be told
	// before Start is pressed, rather than after the forward fails.
	ProbeLocalPort(port int) (bool, error)
	// FreeLocalPort asks the operating system for a TCP port nothing is
	// using, so the UI can offer one instead of asking the operator to guess.
	//
	// The same race StartPortForward's zero-port case accepts applies here:
	// nothing holds the port between this call and a later bind, so this is
	// a proposal, not a reservation.
	FreeLocalPort() (int, error)
}

// NodeShellPort creates and tears down node shells — privileged pods that
// enter a node's host namespaces, the way `kubectl node-shell` and Lens do.
//
// Deliberately shaped like PortForwardPort, because a node shell has the same
// leak to avoid: the pod is a resource PodSteer created, so the record of it
// and the thing that deletes it must never part company. Start creates the
// pod and returns once it is running; Stop deletes it; and everything still
// running is listed so the activity surface can show it with a stop control
// and StopAll can remove it on shutdown.
type NodeShellPort interface {
	// StartNodeShell creates a privileged pod pinned to nodeName that runs a
	// login shell in the node's host namespaces, waits for it to be running,
	// and returns the descriptor. It does NOT open the terminal — that is the
	// caller's exec/attach session, kept separate so the transport and the
	// record cannot drift.
	StartNodeShell(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, nodeName, image string) (domain.NodeShell, error)
	// StopNodeShell deletes the pod behind one node shell and forgets it.
	// Idempotent: stopping one already gone is not an error, since the
	// terminal session ending and an explicit stop can both reach it.
	StopNodeShell(id string) error
	// ListNodeShells reports what node shells are running right now — the live
	// registry, so the activity list shows only pods that still exist.
	ListNodeShells() []domain.NodeShell
	// StopAllNodeShells deletes every node-shell pod, for shutdown.
	StopAllNodeShells()
}

// LocalShellPort runs a shell on the OPERATOR'S OWN MACHINE, not in a cluster.
//
// Every other terminal port here reaches the API server. This one does not: it
// starts the operator's login shell, or a coding agent they already have, on
// the machine PodSteer is running on, with an environment that names the same
// kubeconfig PodSteer reads and the context of the tab in front.
//
// Three properties are part of the contract rather than of one implementation:
//
//   - NOTHING IS EVER INSTALLED. Agents are FOUND on the adopted PATH and
//     offered; a binary that is not there is not offered and never obtained.
//   - THE READ-ONLY GUARD DOES NOT APPLY. That guard governs PodSteer's own
//     writes to a cluster. A shell the operator opened on their own machine
//     with their own credentials is outside what this application can police,
//     and an implementation must not pretend otherwise.
//   - THE KUBECONFIG IS READ, NEVER WRITTEN. In particular current-context is
//     untouched; the open tab's context is stated to the operator, not pinned.
//
// Shaped like NodeShellPort because the leak to avoid is the same: PodSteer
// started a process, so the record and the thing that kills it are created and
// destroyed together, everything live is listable, and StopAll ends the lot on
// shutdown.
type LocalShellPort interface {
	// LocalShellSupported reports whether this platform can open one, and the
	// sentence to show when it cannot.
	LocalShellSupported() (bool, string)
	// DetectAgents reports the coding agents present on the adopted PATH, in a
	// fixed preference order that does not depend on the shape of that PATH.
	DetectAgents() []domain.CodingAgent
	// StartLocalShell opens a pseudo-terminal, streams its output to out, and
	// calls onExit exactly once after the process has exited and been reaped.
	StartLocalShell(spec domain.LocalShellSpec, out io.Writer, onExit func(reason string)) (domain.LocalShell, error)
	// WriteLocalShell sends keystrokes to a session.
	WriteLocalShell(id string, data []byte) error
	// ResizeLocalShell resizes the pseudo-terminal, which is what makes a
	// full-screen program redraw at the pane's size.
	ResizeLocalShell(id string, cols, rows uint16) error
	// StopLocalShell ends one session and WAITS for the process to be gone.
	// Idempotent: a closing pane and a shutdown can both reach it.
	StopLocalShell(id string) error
	// ListLocalShells reports what is running right now.
	ListLocalShells() []domain.LocalShell
	// StopAllLocalShells ends every session, for shutdown.
	StopAllLocalShells()
}

// TerminalSize represents a terminal window size.
type TerminalSize struct {
	Width  uint16
	Height uint16
}

// TerminalSizeQueue delivers terminal resize events to a running exec session.
//
// Next blocks until a resize event is available or the queue is closed.
// Returning nil signals the exec that no more resizes will come and it should
// stop listening.
type TerminalSizeQueue interface {
	Next() *TerminalSize
}

// ManagementPort performs write operations on Kubernetes resources.
//
// These are the actions an operator takes after reading: scaling a deployment,
// deleting a stuck pod, restarting a rollout, editing a configmap. Each method
// is a single atomic operation; complex workflows (edit-then-apply) compose
// them rather than baking the sequence in.
type ManagementPort interface {
	// StreamLogs streams pod logs to the provided channel. The channel is
	// closed when the stream ends (pod terminates, context cancelled, or an
	// error occurs). The caller must drain the channel.
	//
	// If containerName is empty, logs are streamed from the first container.
	// See domain.LogOptions for what each field of opts does.
	StreamLogs(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName string, containerName string, opts domain.LogOptions, out chan<- string) error

	// DeleteResource deletes a single resource. It returns nil if the resource
	// was deleted or already absent; a non-nil error otherwise.
	DeleteResource(ctx context.Context, ref domain.ResourceRef) error

	// ScaleWorkload sets the replica count for a workload. Only Deployments,
	// StatefulSets, and ReplicaSets support scaling; other kinds return an
	// error.
	ScaleWorkload(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName, name string, replicas int32) error

	// RestartRollout triggers a rolling restart of a Deployment or StatefulSet
	// by patching its pod template annotation. DaemonSets and ReplicaSets do
	// not support this operation.
	RestartRollout(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName, name string) error

	// TriggerCronJob creates a Job from a CronJob's template right now, the
	// way `kubectl create job --from=cronjob/NAME` does: labels and
	// annotations copied from spec.jobTemplate, the manual-instantiate
	// annotation added, and an owner reference back to the CronJob so its
	// controller adopts the Job, counts it as active, and applies history
	// limits to it exactly as it would a scheduled run.
	//
	// A suspended CronJob may still be triggered — kubectl allows it, and an
	// operator reaching for this wants exactly one run regardless of the
	// schedule. Returns the created Job's name, so the caller can show it.
	TriggerCronJob(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name string) (string, error)

	// SuspendWorkload sets or clears spec.suspend on a CronJob or a Job —
	// pausing a CronJob's schedule, or pausing a running Job's pods. Only
	// those two kinds support it; the application layer rejects any other
	// kind before this is reached.
	SuspendWorkload(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName, name string, suspend bool) error

	// UpdateResource applies a YAML manifest of ANY kind — built-in or
	// custom — to the cluster, through the dynamic client rather than a
	// fixed set of typed kinds. The manifest must be valid YAML/JSON for a
	// single Kubernetes object carrying apiVersion, kind and metadata.name;
	// a namespaced kind must also carry metadata.namespace, since there is no
	// separate namespace parameter to fall back to. Returns
	// domain.ErrInvalidManifest, wrapped, for any of those.
	//
	// The write itself is optimistic-locked by the manifest's OWN
	// resourceVersion: present, it is sent as a PUT and the server enforces
	// the lock, reporting a stale one as ports.ErrConflict (HTTP 409); absent,
	// the object is created, and an AlreadyExists on that create falls back
	// to fetching the live resourceVersion and replacing the object with it —
	// full replace semantics, matching what an operator pasting a manifest
	// over an existing object means by Apply.
	//
	// dryRun asks the API server to validate the request (admission, schema,
	// webhooks) without persisting anything, via the DryRun=All option —
	// nothing here diffs the manifest itself. The returned ApplyOutcome
	// reports what happened: whether the object was created, and any warning
	// the API server attached to the request.
	UpdateResource(ctx context.Context, id domain.ClusterID, manifest string, dryRun bool) (domain.ApplyOutcome, error)

	// SetImage sets one container's image on a Deployment, StatefulSet or
	// DaemonSet — the three controller kinds whose pod template sits at
	// spec.template, which is what the patch this sends targets. Only those
	// three kinds support it; the application layer rejects any other kind
	// before this is reached, mirroring SuspendWorkload's own kind check.
	//
	// A STRATEGIC MERGE PATCH, not a JSON merge patch: spec.template.spec.
	// containers is a list, and a JSON merge patch replaces a list wholesale
	// — sending one container would delete every other one in the pod. The
	// strategic merge patch instead merges list entries by their `name` key,
	// which is what lets this name one container and leave the rest of the
	// template untouched, the same way `kubectl set image` does.
	//
	// initContainer redirects the patch to spec.template.spec.initContainers
	// instead, for the same container-by-name merge.
	SetImage(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName, name, container, image string, initContainer bool) error

	// PromoteRollout advances a paused Argo Rollouts Rollout by one step, the
	// way `kubectl argo rollouts promote NAME` does.
	//
	// A MERGE PATCH OF NAMED FIELDS, never an apply: the fields promoting
	// touches live in spec AND status, the controller owns and is rewriting
	// status concurrently, and UpdateResource's full replace would send a
	// stale copy of it back. Which patch is sent depends on what is holding
	// the Rollout, and that decision is domain.PlanRolloutPromote's — the
	// adapter reads the live object immediately beforehand so the plan is
	// made from the step the Rollout is on now, not the one the drawer
	// fetched.
	//
	// Only the plugin's DEFAULT promote is offered. Its --full and
	// --skip-current-step variants are different acts with different blast
	// radii, and a button whose behaviour depends on an invisible flag is not
	// one to put on a production cluster.
	PromoteRollout(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name string) error

	// AbortRollout tells the Argo Rollouts controller to abandon the update
	// in progress, the way `kubectl argo rollouts abort NAME` does.
	//
	// It is not the reverse of a promote: traffic returns to the stable
	// ReplicaSet, but the spec is unchanged, so the Rollout stays Degraded
	// against the revision that was being deployed until something changes
	// its template.
	AbortRollout(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name string) error

	// SetSecretKey writes one key of one Secret, leaving every other key
	// untouched.
	//
	// The same deliberate, audited act as RevealSecretKey, in the other
	// direction: it exists so an operator can fix a value they have already
	// looked at without hand-rolling base64, and every call is one line in a
	// cluster's audit log naming the key — never the value. Refuses with
	// domain.ErrInvalidKey when key is empty or not
	// `[-._a-zA-Z0-9]+`, before any request reaches the cluster.
	SetSecretKey(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name, key string, value []byte) error

	// SetConfigMapKey writes one key of one ConfigMap, leaving every other
	// key untouched.
	//
	// Unlike SetSecretKey this reads the object first: a ConfigMap key can
	// live in `data` (text) or `binaryData` (base64), and a text write that
	// merged into `data` while the key already lived in `binaryData` would
	// silently duplicate it under a new field rather than editing it in
	// place. Refuses with domain.ErrInvalidKey for that case, and for the
	// same key-format check SetSecretKey makes.
	SetConfigMapKey(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name, key, value string) error

	// ExecInPod executes a command in a pod container.
	// Stdin, stdout, and stderr are streamed through the provided readers/writers.
	ExecInPod(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName string, command []string, stdin io.Reader, stdout, stderr io.Writer, tty bool) error

	// ExecInPodWithTTY executes a command in a pod container with full TTY support
	// and terminal resize handling. This enables interactive programs like top,
	// htop, vim, and interactive shells.
	//
	// The sizeQueue delivers resize events to the running process. The session
	// runs until the context is cancelled, the command exits, or an error occurs.
	ExecInPodWithTTY(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName string, command []string, stdin io.Reader, stdout, stderr io.Writer, sizeQueue TerminalSizeQueue) error

	// AttachToPod connects to a container's own running process — PID 1,
	// whatever the image's ENTRYPOINT/CMD started — rather than spawning a
	// new one the way ExecInPod and ExecInPodWithTTY do. It is the only way
	// to interact with a process that reads stdin, and to see its live
	// stdout without a separate log stream.
	//
	// The pod is read once before the attach request is made, so a container
	// whose own spec does not declare both tty and stdin is refused locally
	// with domain.ErrContainerNotAttachable, naming the fields to change,
	// rather than failing on the server once the PTY negotiation begins.
	//
	// The sizeQueue delivers resize events exactly as ExecInPodWithTTY's
	// does. The session runs until the context is cancelled, the attached
	// process exits, or an error occurs.
	AttachToPod(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName string, stdin io.Reader, stdout, stderr io.Writer, sizeQueue TerminalSizeQueue) error

	// AddEphemeralContainer adds an ephemeral debug container to a running
	// pod through the pods/ephemeralcontainers subresource, the way
	// `kubectl debug -it POD --image=… --target=CONTAINER` does. It returns
	// the generated container name, so the caller can wait for it and open a
	// terminal into it.
	//
	// The write is a strategic merge patch of spec.ephemeralContainers, which
	// merges the list by container name and so ADDS the new container without
	// clobbering any already present — a pod can carry several debug
	// containers from several investigations, and losing an earlier one would
	// be losing evidence. An ephemeral container CANNOT be removed once added;
	// it stays in the pod's spec until the pod is deleted, which is
	// Kubernetes' behaviour and what the dialog offering this states plainly.
	//
	// A cluster whose API server does not serve the subresource — one older
	// than 1.23, or with the feature gate off — is reported as
	// ErrEphemeralContainersUnsupported rather than a generic failure.
	AddEphemeralContainer(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName string, spec domain.DebugContainerSpec) (string, error)

	// WaitForEphemeralContainerRunning blocks until the named ephemeral
	// container reports Running in the pod's ephemeralContainerStatuses, or a
	// bounded timeout elapses — polling the pod's status rather than sleeping
	// a guessed interval, because an image pull can take seconds and a shell
	// opened before the container is up fails with nothing to say. A container
	// that reaches a terminated state instead is reported as an error naming
	// why, rather than waited on until the timeout.
	WaitForEphemeralContainerRunning(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName string) error
	// CopyFromPod streams a tar archive of one file or directory out of a
	// container, exactly as `kubectl cp pod:path .` does: it runs
	// `tar cf - -C <dir> <base>` over a non-TTY exec session and writes the
	// command's stdout to out, unmodified. Nothing is written to disk here
	// — unpacking, with every check on where an entry may land, is the
	// ArchivePort's job, so that the process reading the stream and the
	// rules governing it can be tested apart.
	//
	// remotePath must satisfy domain.SplitRemotePath. Stderr from tar is
	// captured: on failure it is carried in the returned error verbatim
	// (wrapping ErrCommandFailed), and a container with no tar binary at
	// all is reported as ErrTarMissing rather than as an opaque exit code.
	CopyFromPod(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName, remotePath string, out io.Writer) error

	// CopyToPod streams a tar archive INTO a container, as `kubectl cp
	// . pod:dir` does: it runs `tar xf - -C <dir>` over a non-TTY exec
	// session with in as the command's stdin. The archive is packed by the
	// ArchivePort from a path the operator chose; this only carries it.
	// Failures are reported exactly as CopyFromPod's are.
	CopyToPod(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName, remoteDir string, in io.Reader) error

	// CordonNode marks a node schedulable or unschedulable, without touching
	// anything already running on it — cordoning removes the node from
	// consideration for NEW pods only. A merge patch of spec.unschedulable,
	// the same field `kubectl cordon`/`uncordon` sets.
	CordonNode(ctx context.Context, id domain.ClusterID, name string, cordon bool) error

	// EvictPod evicts one pod through the policy/v1 Eviction subresource,
	// never a plain delete: an eviction is the one request a
	// PodDisruptionBudget can refuse, which is the entire reason the
	// subresource exists rather than every drain just deleting pods
	// directly. A refusal is reported as ErrDisruptionBudget.
	//
	// gracePeriodSeconds is passed to the eviction's DeleteOptions; negative
	// means "use the pod's own terminationGracePeriodSeconds".
	EvictPod(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name string, gracePeriodSeconds int) error

	// DrainNode cordons a node, plans the drain with domain.PlanDrain, and —
	// if the plan is runnable — evicts every pod it allows, retrying only a
	// PodDisruptionBudget refusal until opts.Timeout elapses.
	//
	// Always returns a report, even when the returned error is non-nil:
	// cordoned, refused and partially evicted are all outcomes worth
	// showing exactly as they happened. Wraps ErrDrainRefused when the plan
	// is not runnable, in which case the node was cordoned but nothing was
	// evicted.
	DrainNode(ctx context.Context, id domain.ClusterID, name string, opts domain.DrainOptions) (domain.DrainReport, error)

	// RollbackWorkload rolls a Deployment, StatefulSet or DaemonSet back to
	// a previously recorded revision, the way `kubectl rollout undo
	// --to-revision` does. Only those three kinds support it; the
	// application layer rejects any other kind before this is reached,
	// mirroring SetImage's own kind check.
	//
	// For a Deployment this copies the target ReplicaSet's spec.template
	// onto the Deployment via a strategic merge patch of spec.template —
	// the same field SetImage patches — plus a `kubernetes.io/change-cause`
	// annotation naming the rollback, and ONLY when the Deployment already
	// carries a change-cause annotation today: a rollback must not start a
	// convention the operator never opted into. For a StatefulSet or
	// DaemonSet this applies the target ControllerRevision's own patch data
	// onto the object as a strategic merge patch, letting the API server do
	// the same reconstruction `kubectl rollout undo` relies on rather than
	// this process re-implementing strategic-merge-patch semantics by hand.
	//
	// Refuses with domain.ErrInvalidRevision when toRevision is not
	// positive, or names the revision already current — there being nothing
	// for it to do is a different problem from toRevision naming no
	// revision at all, which is ports.ErrNotFound.
	//
	// dryRun asks the API server to validate the request via DryRun=All
	// without persisting anything, the same convention UpdateResource's own
	// dry run uses.
	RollbackWorkload(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName, name string, toRevision int64, dryRun bool) (domain.RollbackOutcome, error)
}

// ArchivePort is the LOCAL side of a file copy: the tar stream a container
// produces is unpacked into a directory the operator chose, and the tar
// stream a container receives is packed from a path they chose.
//
// A port rather than a helper the application calls directly, for the usual
// reason the arrows point inward — and for one specific to this feature.
// Everything that decides what a stream from a container may do to the
// operator's machine lives behind this interface: an entry may not escape the
// chosen directory, a symlink may not point outside it, setuid bits are never
// preserved, and TransferLimits are enforced as the bytes arrive. Those are
// the properties SECURITY.md promises, and a fake implementation here is
// what lets ManagementService's orchestration be tested without touching a
// filesystem while the real one is tested on nothing but a temp directory.
type ArchivePort interface {
	// Extract unpacks the tar stream r into dest, an existing directory,
	// applying every rule above. progress, when non-nil, is called with the
	// number of file-content bytes written by each write; it is how the UI
	// shows a transfer moving. Refuses with domain.ErrUnsafeArchiveEntry or
	// domain.ErrTransferTooLarge, leaving whatever had already landed in
	// place rather than attempting to undo a partial extraction.
	Extract(ctx context.Context, r io.Reader, dest string, limits domain.TransferLimits, progress func(int64)) (domain.TransferSummary, error)

	// Pack writes source — one file or a directory tree — to w as a tar
	// stream whose entries are rooted at source's own base name, so the
	// container unpacks `nginx.conf` or `config/…`, never the operator's
	// full local path. Symlinks are never followed: one pointing inside the
	// selection is archived as a link, one pointing outside is left out and
	// named in the summary's Notes. The same limits apply as on Extract.
	Pack(ctx context.Context, w io.Writer, source string, limits domain.TransferLimits, progress func(int64)) (domain.TransferSummary, error)
}

// EventPublisher delivers domain events to whatever is observing the
// application — in the desktop build, the Svelte frontend via the Wails event
// bus.
//
// Publish returns nothing and must not block: an event is a notification, not
// a transaction, and a use case must never fail or stall because the UI is
// slow to listen. Implementations that can fail are expected to log and drop.
type EventPublisher interface {
	// Publish delivers event to all observers.
	Publish(ctx context.Context, event domain.DomainEvent)
}

// InspectPort answers the two questions an operator asks ON REQUEST about
// something already on screen: can this be reached, and what is this image.
//
// A PORT OF ITS OWN RATHER THAN THREE MORE METHODS ON ResourcePort, because
// everything behind it shares one rule the polled ports do not: nothing here
// ever runs on a refresh tick. A probe opens a socket or runs a command in
// somebody's container, and an image report costs a GET of a pod and a GET of
// a node — all of which are fine as answers to a button and none of which are
// fine ten seconds later, unasked, for as long as a pane stays open. Keeping
// them behind one interface is what makes that rule visible to anything
// wiring them, the same way BrowseAPI.ObjectGraph's own comment makes it for
// the dependency map.
type InspectPort interface {
	// ProbeFromHere performs plan from THIS MACHINE, reaching the cluster the
	// only way this process reaches anything: through the API server named in
	// the kubeconfig. A Service is probed through the API server's own
	// service proxy; a pod through an ephemeral port-forward this call opens
	// and tears down again before returning, whatever the outcome.
	//
	// A target that refuses a connection is an ORDINARY ANSWER carried in the
	// observation, never an error. An error here means the probe could not be
	// performed at all — the cluster was unreachable, the account may not
	// proxy, the forward never came up.
	ProbeFromHere(ctx context.Context, id domain.ClusterID, plan domain.ProbePlan) (domain.ProbeObservation, error)

	// ProbeFromPod performs plan from INSIDE a container the operator chose,
	// as one bounded exec of domain.ProbeCommand. Nothing is created: no pod,
	// no sidecar, no file. A container with no tool to probe with wraps
	// ErrProbeToolMissing, which is a fact about that image and not about the
	// target.
	ProbeFromPod(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName string, plan domain.ProbePlan) (domain.ProbeObservation, error)

	// ImageFacts gathers what Kubernetes reports about one container's image:
	// the pod's own view of it, and the image list of the node that pulled
	// it. It reads no registry and no pull Secret — see domain.ImageReport
	// for why that is a decision rather than an omission. A node that cannot
	// be read is reported inside the facts rather than failing the call: an
	// account without `get nodes` should still see the digest and the
	// references, and a refusal is not an absence.
	ImageFacts(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName string) (domain.ImageFacts, error)
}
