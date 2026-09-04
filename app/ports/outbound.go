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
	ListNamespaces(ctx context.Context, id domain.ClusterID) ([]domain.Namespace, error)

	// ListNodes returns the cluster's nodes.
	ListNodes(ctx context.Context, id domain.ClusterID) ([]domain.Node, error)

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
	ListPods(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) ([]domain.Pod, error)

	// ListWorkloads returns controllers of the given kind.
	ListWorkloads(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName) ([]domain.Workload, error)

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
}

// EventPort reads Kubernetes Events.
type EventPort interface {
	// ListEvents returns events in the given namespace, or across every
	// namespace when it is domain.NamespaceAll.
	ListEvents(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) ([]domain.Event, error)

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
	// the API server itself prints.
	ListTable(ctx context.Context, id domain.ClusterID, kind domain.ResourceKind, namespace domain.NamespaceName) (domain.ResourceTable, error)

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
	// If tailLines is 0, all available logs are streamed.
	// If follow is true, the stream remains open for new log lines.
	StreamLogs(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName string, containerName string, follow bool, tailLines int64, out chan<- string) error

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

	// UpdateResource applies a YAML manifest to the cluster, creating or
	// updating the resource. The manifest must be valid YAML for a Kubernetes
	// object; the kind and namespace in the manifest override the parameters.
	UpdateResource(ctx context.Context, id domain.ClusterID, manifest string) error

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
