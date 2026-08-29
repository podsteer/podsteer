package wails

import (
	"fmt"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// The types below are the frontend API contract. Wails generates TypeScript
// declarations from them, so treat a field rename as a breaking change and
// keep the json tags camelCase to stay idiomatic on the JavaScript side.

// Cluster is a cluster as presented to the UI.
type Cluster struct {
	// ID is the kubeconfig context name, and the handle every other call takes.
	ID string `json:"id"`
	// Server is the full API server URL.
	Server string `json:"server"`
	// Host is the host[:port] of the API server, for compact display.
	Host string `json:"host"`
	// DefaultNamespace is the namespace to preselect for this cluster.
	DefaultNamespace string `json:"defaultNamespace"`
	// AuthInfo names the kubeconfig user. Never a credential.
	AuthInfo string `json:"authInfo"`
	// IsCurrent marks the kubeconfig's current context.
	IsCurrent bool `json:"isCurrent"`
	// IsReachable reports whether PodSteer has reached this cluster.
	IsReachable bool `json:"isReachable"`
	// Version is the API server's git version, empty until reached.
	Version string `json:"version"`
	// Platform is the API server's os/arch, empty until reached.
	Platform string `json:"platform"`
}

// toCluster converts a domain cluster into its wire representation.
func toCluster(cluster domain.Cluster) Cluster {
	version := cluster.Version()
	return Cluster{
		ID:               cluster.ID().String(),
		Server:           cluster.Server().String(),
		Host:             cluster.Server().Host(),
		DefaultNamespace: cluster.DefaultNamespace().String(),
		AuthInfo:         cluster.AuthInfo(),
		IsCurrent:        cluster.IsCurrent(),
		IsReachable:      cluster.IsReachable(),
		Version:          version.GitVersion,
		Platform:         version.Platform,
	}
}

// toClusters converts a slice of domain clusters.
//
// The result is always non-nil so it marshals to [] rather than null, sparing
// every call site in the frontend a null check.
func toClusters(clusters []domain.Cluster) []Cluster {
	out := make([]Cluster, 0, len(clusters))
	for _, cluster := range clusters {
		out = append(out, toCluster(cluster))
	}
	return out
}

// Namespace is a namespace as presented to the UI.
type Namespace struct {
	// Name is the namespace name.
	Name string `json:"name"`
	// Phase is the lifecycle phase, e.g. "Active" or "Terminating".
	Phase string `json:"phase"`
	// IsActive reports whether the namespace accepts new objects.
	IsActive bool `json:"isActive"`
	// CreatedAt is the creation timestamp in RFC 3339, empty if unknown.
	CreatedAt string `json:"createdAt"`
	// AgeSeconds is the age at the time of the call.
	AgeSeconds int64 `json:"ageSeconds"`
}

// toNamespace converts a domain namespace, using now as the age reference.
func toNamespace(namespace domain.Namespace, now time.Time) Namespace {
	return Namespace{
		Name:       namespace.Name().String(),
		Phase:      string(namespace.Phase()),
		IsActive:   namespace.IsActive(),
		CreatedAt:  formatTime(namespace.CreatedAt()),
		AgeSeconds: int64(namespace.Age(now).Seconds()),
	}
}

// toNamespaces converts a slice of domain namespaces.
func toNamespaces(namespaces []domain.Namespace, now time.Time) []Namespace {
	out := make([]Namespace, 0, len(namespaces))
	for _, namespace := range namespaces {
		out = append(out, toNamespace(namespace, now))
	}
	return out
}

// Container is a container as presented to the UI.
type Container struct {
	// Name is the container name.
	Name string `json:"name"`
	// Image is the image reference actually running, when known.
	Image string `json:"image"`
	// Ready reports whether the container passes readiness.
	Ready bool `json:"ready"`
	// RestartCount is how many times the kubelet restarted it.
	RestartCount int32 `json:"restartCount"`
	// State is "Waiting", "Running", "Terminated" or "Unknown".
	State string `json:"state"`
	// Reason explains the state, e.g. "CrashLoopBackOff". May be empty.
	Reason string `json:"reason"`
}

// Pod is a pod as presented to the UI.
//
// Several fields are derived rather than raw — Ready, Restarts, StatusReason,
// IsHealthy — because the derivation is domain logic. Computing them in Go
// once keeps a single definition of "healthy" in the codebase instead of
// letting a second, subtly different one grow in the frontend.
type Pod struct {
	// UID is the Kubernetes object UID. May be empty.
	UID string `json:"uid"`
	// Name is the pod name.
	Name string `json:"name"`
	// Namespace is the pod's namespace.
	Namespace string `json:"namespace"`
	// ClusterID is the cluster the pod was read from.
	ClusterID string `json:"clusterId"`
	// Phase is the lifecycle phase, including the derived "Terminating".
	Phase string `json:"phase"`
	// StatusReason explains an unhealthy pod, e.g. "ImagePullBackOff".
	// Empty when nothing is wrong.
	StatusReason string `json:"statusReason"`
	// NodeName is the node running the pod, empty while unscheduled.
	NodeName string `json:"nodeName"`
	// PodIP is the pod's cluster IP, empty before assignment.
	PodIP string `json:"podIp"`
	// Ready is the ready/total container count formatted for display, "2/3".
	Ready string `json:"ready"`
	// ReadyContainers is how many containers pass readiness.
	ReadyContainers int `json:"readyContainers"`
	// TotalContainers is how many containers the pod declares.
	TotalContainers int `json:"totalContainers"`
	// Restarts is the pod's total restart count.
	Restarts int32 `json:"restarts"`
	// IsHealthy reports whether the pod is doing what it should.
	IsHealthy bool `json:"isHealthy"`
	// ControlledBy names the controlling owner as "Kind/name". Empty for a
	// bare pod, which is itself worth knowing: nothing will recreate it.
	ControlledBy string `json:"controlledBy"`
	// QoSClass is Guaranteed, Burstable or BestEffort — the eviction order
	// under node pressure.
	QoSClass string `json:"qosClass"`
	// CPU is measured usage in cores, or an em dash when unmeasured.
	CPU string `json:"cpu"`
	// Memory is measured working-set memory, or an em dash.
	Memory string `json:"memory"`
	// HasMetrics distinguishes a measured zero from no metrics-server.
	HasMetrics bool `json:"hasMetrics"`
	// CPUPercent and MemoryPercent are usage against what the pod REQUESTED,
	// for the meters — a node's meters divide by allocatable, which a pod has
	// no equivalent of. They may exceed 100: a request is a reservation, not
	// a ceiling. See domain.Pod.CPUPercent.
	CPUPercent    float64 `json:"cpuPercent"`
	MemoryPercent float64 `json:"memoryPercent"`
	// CPURequest and MemoryRequest are the reservation itself, formatted, so
	// a meter can name the number it is a proportion OF instead of leaving a
	// bare percentage to be taken on trust.
	CPURequest    string `json:"cpuRequest"`
	MemoryRequest string `json:"memoryRequest"`
	// HasCPURequest and HasMemoryRequest say whether there is a denominator
	// at all. Separate flags because the two are declared independently: a
	// pod may reserve memory and leave CPU unbounded, which is a common and
	// deliberate shape rather than an oversight.
	//
	// A zero percentage cannot stand in for these. An idle pod that DID
	// reserve CPU also reads 0%, and the two must not draw the same thing.
	HasCPURequest    bool `json:"hasCpuRequest"`
	HasMemoryRequest bool `json:"hasMemoryRequest"`
	// Containers are the pod's containers.
	Containers []Container `json:"containers"`
	// Labels are the pod's labels.
	Labels map[string]string `json:"labels"`
	// CreatedAt is the creation timestamp in RFC 3339, empty if unknown.
	CreatedAt string `json:"createdAt"`
	// AgeSeconds is the age at the time of the call.
	AgeSeconds int64 `json:"ageSeconds"`
}

// toPod converts a domain pod, using now as the age reference.
func toPod(pod domain.Pod, now time.Time) Pod {
	domainContainers := pod.Containers()
	containers := make([]Container, 0, len(domainContainers))
	for _, container := range domainContainers {
		containers = append(containers, Container{
			Name:         container.Name,
			Image:        container.Image,
			Ready:        container.Ready,
			RestartCount: container.RestartCount,
			State:        string(container.State),
			Reason:       container.Reason,
		})
	}

	labels := pod.Labels()
	if labels == nil {
		labels = map[string]string{}
	}

	requests := pod.Requests()

	return Pod{
		UID:             pod.UID(),
		Name:            pod.Name(),
		Namespace:       pod.Namespace().String(),
		ClusterID:       pod.ClusterID().String(),
		Phase:           string(pod.Phase()),
		StatusReason:    pod.StatusReason(),
		NodeName:        pod.NodeName(),
		PodIP:           pod.PodIP(),
		Ready:           fmt.Sprintf("%d/%d", pod.ReadyContainers(), pod.TotalContainers()),
		ReadyContainers: pod.ReadyContainers(),
		TotalContainers: pod.TotalContainers(),
		Restarts:        pod.RestartCount(),
		IsHealthy:       pod.IsHealthy(),
		ControlledBy:    ownerLabel(pod.Controller()),
		QoSClass:        string(pod.QoSClass()),
		CPU:             formatCores(pod.Usage()),
		Memory:          formatMemory(pod.Usage()),
		HasMetrics:      !pod.Usage().IsZero(),

		CPUPercent:       pod.CPUPercent(),
		MemoryPercent:    pod.MemoryPercent(),
		CPURequest:       formatMilliCores(requests.CPUMilli),
		MemoryRequest:    formatBytes(requests.MemoryBytes),
		HasCPURequest:    requests.CPUMilli > 0,
		HasMemoryRequest: requests.MemoryBytes > 0,
		Containers:       containers,
		Labels:           labels,
		CreatedAt:        formatTime(pod.CreatedAt()),
		AgeSeconds:       int64(pod.Age(now).Seconds()),
	}
}

// toPods converts a slice of domain pods.
func toPods(pods []domain.Pod, now time.Time) []Pod {
	out := make([]Pod, 0, len(pods))
	for _, pod := range pods {
		out = append(out, toPod(pod, now))
	}
	return out
}

// ClusterConnectedEvent is the payload of the "cluster:connected" event.
type ClusterConnectedEvent struct {
	// Cluster is the cluster that was reached.
	Cluster Cluster `json:"cluster"`
	// At is when contact succeeded, in RFC 3339.
	At string `json:"at"`
}

// ClusterUnreachableEvent is the payload of the "cluster:unreachable" event.
type ClusterUnreachableEvent struct {
	// ClusterID is the cluster that did not answer.
	ClusterID string `json:"clusterId"`
	// Reason is a human-readable explanation.
	Reason string `json:"reason"`
	// At is when the attempt failed, in RFC 3339.
	At string `json:"at"`
}

// LogLinesEvent is the payload of the "log:lines" event.
//
// A batch rather than a line. One event per line meant a pod's backlog
// crossed the bridge as thousands of separate messages — 5000 of them for a
// 5000-line tail, each serialised, posted and dispatched on its own, and each
// waking the frontend to re-render. Coalescing them costs a few milliseconds
// of latency nobody can perceive in a log and removes the burst entirely.
type LogLinesEvent struct {
	// StreamID identifies the log stream (returned by StreamLogs).
	StreamID string `json:"streamId"`
	// Lines are the log line texts, oldest first (may include timestamp
	// prefixes).
	Lines []string `json:"lines"`
}

// LogEndEvent is the payload of the "log:end" event.
type LogEndEvent struct {
	// StreamID identifies the log stream that ended.
	StreamID string `json:"streamId"`
	// Reason explains why the stream ended, empty when it ended normally.
	//
	// Without this every failure looked identical to the operator, and
	// identical to success: the lines simply stopped. A role without the
	// permission to read logs, a container name that does not exist, and a
	// single line over the size cap all presented as "the pod went quiet".
	// TerminalExitEvent already carried a reason; this is the same idea.
	Reason string `json:"reason"`
}

// formatTime renders t as RFC 3339 in UTC, or "" when it is unset.
//
// The empty string rather than "0001-01-01T00:00:00Z" so the frontend can test
// truthiness instead of comparing against a sentinel date.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
