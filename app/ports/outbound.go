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

	// ListPodsForWorkload returns all pods owned by a specific workload.
	ListPodsForWorkload(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, kind domain.WorkloadKind, name string) ([]domain.Pod, error)
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
	// PodMetrics returns usage keyed by "namespace/name".
	PodMetrics(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) (map[string]domain.Metrics, error)

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

	// GetManifest returns one object serialised as YAML, for the detail view.
	GetManifest(ctx context.Context, ref domain.ResourceRef) (string, error)
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
