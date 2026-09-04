package wails

import (
	"fmt"
	"strings"
	"time"

	"github.com/podsteer/podsteer/app/application"
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
	// Labels are the namespace's labels.
	Labels map[string]string `json:"labels"`
	// Annotations are only the projected keys — see Pod.Annotations.
	Annotations map[string]string `json:"annotations"`
	// CreatedAt is the creation timestamp in RFC 3339, empty if unknown.
	CreatedAt string `json:"createdAt"`
	// AgeSeconds is the age at the time of the call.
	AgeSeconds int64 `json:"ageSeconds"`
}

// toNamespace converts a domain namespace, using now as the age reference.
func toNamespace(namespace domain.Namespace, now time.Time) Namespace {
	return Namespace{
		Name:        namespace.Name().String(),
		Phase:       string(namespace.Phase()),
		IsActive:    namespace.IsActive(),
		Labels:      emptyIfNil(namespace.Labels()),
		Annotations: emptyIfNil(namespace.Annotations()),
		CreatedAt:   formatTime(namespace.CreatedAt()),
		AgeSeconds:  int64(namespace.Age(now).Seconds()),
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

// NamespaceSummary is a namespace with what is running in it.
type NamespaceSummary struct {
	Namespace
	// NotReady is how many of its pods are not doing their job.
	NotReady int `json:"notReady"`
	// What those pods are using, on exactly the terms a controller's row and
	// the pod list use. See Consumption.
	Consumption
}

// toNamespaceSummary converts one summary, using now as the age reference.
func toNamespaceSummary(summary domain.NamespaceSummary, now time.Time) NamespaceSummary {
	return NamespaceSummary{
		Namespace:   toNamespace(summary.Namespace, now),
		NotReady:    summary.NotReady,
		Consumption: toConsumption(summary.Usage),
	}
}

// toNamespaceSummaries converts a slice of summaries.
func toNamespaceSummaries(summaries []domain.NamespaceSummary, now time.Time) []NamespaceSummary {
	out := make([]NamespaceSummary, 0, len(summaries))
	for _, summary := range summaries {
		out = append(out, toNamespaceSummary(summary, now))
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
	// Started reports whether the startup probe has passed. Distinct from
	// Ready: started with not-ready is a readiness problem, not-started is a
	// startup problem, and they are looked into in different places.
	Started bool `json:"started"`
	// TTY and Stdin quote the container's own spec: whether it allocates a
	// pseudo-terminal and keeps standard input open. Both must be true
	// before the terminal pane offers Attach (connecting to this container's
	// own running process) as an alternative to Shell (starting a new one).
	TTY   bool `json:"tty"`
	Stdin bool `json:"stdin"`
	// Requests and Limits, formatted for display. Empty when undeclared.
	Requests string `json:"requests"`
	Limits   string `json:"limits"`
	// CPU and Memory are what THIS container is using, when anything measured
	// it — the half of a pod's total that says where the total came from. An
	// em dash when unmeasured, the same as the pod's own figures.
	CPU        string `json:"cpu"`
	Memory     string `json:"memory"`
	HasMetrics bool   `json:"hasMetrics"`
	// LastTermination explains how this container's previous life ended, and
	// is absent when it has not restarted. See domain.Termination: this is
	// the ONLY record of it Kubernetes keeps, and only of the most recent one.
	LastTermination *Termination `json:"lastTermination,omitempty"`
}

// Termination is a container's previous death, explained.
type Termination struct {
	ExitCode int32 `json:"exitCode"`
	Signal   int32 `json:"signal"`
	// Reason is the kubelet's word — "OOMKilled", "Error", "Completed".
	Reason string `json:"reason"`
	// Diagnosis is the sentence to show. Written in the domain, because
	// deciding that a 137 without an OOMKilled reason is a grace-period
	// expiry rather than a memory limit is a judgement, not a lookup.
	Diagnosis string `json:"diagnosis"`
	// Alarming says whether to colour it. A clean SIGTERM during a rollout
	// is how every deployment stops a container and is not a fault.
	Alarming bool `json:"alarming"`
	// FinishedAt is RFC 3339, empty if unknown; LifetimeSeconds is how long
	// it ran before dying, zero when the timestamps do not allow the sum.
	FinishedAt      string `json:"finishedAt"`
	LifetimeSeconds int64  `json:"lifetimeSeconds"`
}

// toTermination converts a domain termination, or nil when there was none.
func toTermination(termination domain.Termination) *Termination {
	if termination.IsZero() {
		return nil
	}

	return &Termination{
		ExitCode:        termination.ExitCode,
		Signal:          termination.Signal,
		Reason:          termination.Reason,
		Diagnosis:       termination.Diagnosis(),
		Alarming:        termination.Alarming(),
		FinishedAt:      formatTime(termination.FinishedAt),
		LifetimeSeconds: int64(termination.Lifetime().Seconds()),
	}
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
	// The same four figures against the pod's LIMITS, which is a different
	// question: the request is what it reserved, the limit is what it will be
	// stopped at. Only this one can predict a failure — see
	// domain.Pod.MemoryLimitPercent, and note that its CPU twin predicts
	// throttling rather than death.
	CPULimitPercent    float64 `json:"cpuLimitPercent"`
	MemoryLimitPercent float64 `json:"memoryLimitPercent"`
	CPULimit           string  `json:"cpuLimit"`
	MemoryLimit        string  `json:"memoryLimit"`
	HasCPULimit        bool    `json:"hasCpuLimit"`
	HasMemoryLimit     bool    `json:"hasMemoryLimit"`
	// Containers are the pod's containers.
	Containers []Container `json:"containers"`
	// Findings are what is wrong with this pod, or about to be — each with
	// what to do about it. See domain.AssessPod. Empty for a pod with nothing
	// worth saying about it, which is most of them.
	Findings []PodFinding `json:"findings"`
	// Labels are the pod's labels.
	Labels map[string]string `json:"labels"`
	// Annotations are ONLY the keys the list was asked for — the custom
	// columns' — never the whole map. See domain.Projection.
	Annotations map[string]string `json:"annotations"`
	// CreatedAt is the creation timestamp in RFC 3339, empty if unknown.
	CreatedAt string `json:"createdAt"`
	// AgeSeconds is the age at the time of the call.
	AgeSeconds int64 `json:"ageSeconds"`
}

// emptyIfNil hands the frontend {} rather than null for an absent map, so
// every row's labels and annotations can be indexed without a guard.
func emptyIfNil(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	return values
}

// toPod converts a domain pod, using now as the age reference.
func toPod(pod domain.Pod, now time.Time) Pod {
	domainContainers := pod.Containers()
	containers := make([]Container, 0, len(domainContainers))
	for _, container := range domainContainers {
		containers = append(containers, Container{
			Name:            container.Name,
			Image:           container.Image,
			Ready:           container.Ready,
			RestartCount:    container.RestartCount,
			State:           string(container.State),
			Reason:          container.Reason,
			Started:         container.Started,
			TTY:             container.TTY,
			Stdin:           container.Stdin,
			Requests:        formatResources(container.Requests),
			Limits:          formatResources(container.Limits),
			CPU:             formatCores(container.Usage),
			Memory:          formatMemory(container.Usage),
			HasMetrics:      !container.Usage.IsZero(),
			LastTermination: toTermination(container.LastTermination),
		})
	}

	requests := pod.Requests()
	limits := pod.Limits()

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

		CPULimitPercent:    pod.CPULimitPercent(),
		MemoryLimitPercent: pod.MemoryLimitPercent(),
		CPULimit:           formatMilliCores(limits.CPUMilli),
		MemoryLimit:        formatBytes(limits.MemoryBytes),
		HasCPULimit:        limits.CPUMilli > 0,
		HasMemoryLimit:     limits.MemoryBytes > 0,
		Containers:         containers,
		Findings:           toPodFindings(domain.AssessPod(pod, now)),
		Labels:             emptyIfNil(pod.Labels()),
		Annotations:        emptyIfNil(pod.Annotations()),
		CreatedAt:          formatTime(pod.CreatedAt()),
		AgeSeconds:         int64(pod.Age(now).Seconds()),
	}
}

// PodFinding is one thing worth telling an operator about a pod.
type PodFinding struct {
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Advice   string `json:"advice"`
}

// toPodFindings converts the domain's assessment of one pod.
func toPodFindings(findings []domain.PodFinding) []PodFinding {
	out := make([]PodFinding, 0, len(findings))
	for _, finding := range findings {
		out = append(out, PodFinding{
			Severity: string(finding.Severity),
			Title:    finding.Title,
			Detail:   finding.Detail,
			Advice:   finding.Advice,
		})
	}
	return out
}

// toPods converts a slice of domain pods.
func toPods(pods []domain.Pod, now time.Time) []Pod {
	out := make([]Pod, 0, len(pods))
	for _, pod := range pods {
		out = append(out, toPod(pod, now))
	}
	return out
}

// podRef names a pod as "namespace/name" for the drain DTOs below — a plan or
// a report names many pods across a node's namespaces, so a bare Pod struct
// per entry would repeat every one of its fifty-odd fields for one line of
// UI copy.
func podRef(pod domain.Pod) string {
	return pod.Namespace().String() + "/" + pod.Name()
}

// DrainSkip is one pod a drain plan or report leaves alone, or refuses to
// touch, and why. Reason is shown verbatim: domain.DrainReason values are
// already written as sentences, not codes the frontend has to map.
type DrainSkip struct {
	Pod    string `json:"pod"`
	Reason string `json:"reason"`
}

// DrainFailure is one pod a drain attempted and could not evict, or could not
// confirm gone.
type DrainFailure struct {
	Pod    string `json:"pod"`
	Reason string `json:"reason"`
}

// DrainPlanDTO previews what a drain would do, without doing it — what
// PlanDrain returns, over the wire.
type DrainPlanDTO struct {
	// Evict names the pods the drain would evict.
	Evict []string `json:"evict"`
	// Skipped are pods the drain leaves alone regardless of options.
	Skipped []DrainSkip `json:"skipped"`
	// Refused are pods that block the plan until an option changes.
	Refused []DrainSkip `json:"refused"`
	// Runnable is false the moment Refused is non-empty. The frontend
	// disables its confirm button on this rather than re-deriving it from
	// len(Refused), so the domain's own rule is the one place that decides.
	Runnable bool `json:"runnable"`
}

// toDrainPlan converts a domain drain plan for the preview the UI shows
// before a drain runs.
func toDrainPlan(plan domain.DrainPlan) DrainPlanDTO {
	evict := make([]string, 0, len(plan.Evict))
	for _, pod := range plan.Evict {
		evict = append(evict, podRef(pod))
	}

	skipped := make([]DrainSkip, 0, len(plan.Skipped))
	for _, skip := range plan.Skipped {
		skipped = append(skipped, DrainSkip{Pod: podRef(skip.Pod), Reason: string(skip.Reason)})
	}

	refused := make([]DrainSkip, 0, len(plan.Refused))
	for _, refusal := range plan.Refused {
		refused = append(refused, DrainSkip{Pod: podRef(refusal.Pod), Reason: string(refusal.Reason)})
	}

	return DrainPlanDTO{
		Evict:    evict,
		Skipped:  skipped,
		Refused:  refused,
		Runnable: plan.Runnable(),
	}
}

// DrainReportDTO is what happened when a drain ran, whether or not it
// finished cleanly — what DrainNode returns, over the wire.
type DrainReportDTO struct {
	// Cordoned reports whether the node was marked unschedulable, true even
	// when everything after it failed.
	Cordoned bool `json:"cordoned"`
	// Evicted names the pods successfully evicted and confirmed gone.
	Evicted []string `json:"evicted"`
	// Skipped mirrors the plan's own Skipped.
	Skipped []DrainSkip `json:"skipped"`
	// Refused mirrors the plan's own Refused. Non-empty only when the drain
	// stopped before evicting anything.
	Refused []DrainSkip `json:"refused"`
	// Failed are pods the drain could not evict or could not confirm gone,
	// for a reason other than a timeout.
	Failed []DrainFailure `json:"failed"`
	// TimedOut reports whether the timeout was reached while still waiting
	// on a PodDisruptionBudget or a pod's termination.
	TimedOut bool `json:"timedOut"`
}

// toDrainReport converts a domain drain report for the frontend.
func toDrainReport(report domain.DrainReport) DrainReportDTO {
	evicted := make([]string, 0, len(report.Evicted))
	for _, pod := range report.Evicted {
		evicted = append(evicted, podRef(pod))
	}

	skipped := make([]DrainSkip, 0, len(report.Skipped))
	for _, skip := range report.Skipped {
		skipped = append(skipped, DrainSkip{Pod: podRef(skip.Pod), Reason: string(skip.Reason)})
	}

	refused := make([]DrainSkip, 0, len(report.Refused))
	for _, refusal := range report.Refused {
		refused = append(refused, DrainSkip{Pod: podRef(refusal.Pod), Reason: string(refusal.Reason)})
	}

	failed := make([]DrainFailure, 0, len(report.Failed))
	for _, failure := range report.Failed {
		failed = append(failed, DrainFailure{Pod: failure.Pod, Reason: failure.Reason})
	}

	return DrainReportDTO{
		Cordoned: report.Cordoned,
		Evicted:  evicted,
		Skipped:  skipped,
		Refused:  refused,
		Failed:   failed,
		TimedOut: report.TimedOut,
	}
}

// RevisionDTO is one recorded revision of a Deployment, StatefulSet or
// DaemonSet's pod template — what RolloutHistory returns, over the wire.
type RevisionDTO struct {
	// Number is the revision number Kubernetes assigned.
	Number int64 `json:"number"`
	// Name is the owning ReplicaSet's or ControllerRevision's own name.
	Name string `json:"name"`
	// CreatedAt is the creation timestamp in RFC 3339, empty if unknown.
	CreatedAt string `json:"createdAt"`
	// AgeSeconds is the age at the time of the call.
	AgeSeconds int64 `json:"ageSeconds"`
	// Current marks the revision presently in use.
	Current bool `json:"current"`
	// Replicas is how many pods this revision has — zero for a StatefulSet
	// or DaemonSet revision, which is a stored patch rather than a scaled
	// object. See domain.Revision.Replicas.
	Replicas int32 `json:"replicas"`
	// Images are the pod template's container images.
	Images []string `json:"images"`
	// ChangeCause is the kubernetes.io/change-cause annotation, empty when
	// the object that produced this revision never carried one.
	ChangeCause string `json:"changeCause"`
	// TemplateYAML is the pod template this revision would roll back to,
	// for the History tab's diff view.
	TemplateYAML string `json:"templateYaml"`
}

// toRevision converts a domain revision, using now as the age reference.
func toRevision(revision domain.Revision, now time.Time) RevisionDTO {
	images := revision.Images
	if images == nil {
		images = []string{}
	}
	return RevisionDTO{
		Number:       revision.Number,
		Name:         revision.Name,
		CreatedAt:    formatTime(revision.Created),
		AgeSeconds:   int64(now.Sub(revision.Created).Seconds()),
		Current:      revision.Current,
		Replicas:     revision.Replicas,
		Images:       images,
		ChangeCause:  revision.ChangeCause,
		TemplateYAML: revision.TemplateYAML,
	}
}

// toRevisions converts a slice of domain revisions.
func toRevisions(revisions []domain.Revision, now time.Time) []RevisionDTO {
	out := make([]RevisionDTO, 0, len(revisions))
	for _, revision := range revisions {
		out = append(out, toRevision(revision, now))
	}
	return out
}

// RollbackOutcomeDTO reports what RollbackWorkload actually did — what
// domain.RollbackOutcome carries, over the wire.
type RollbackOutcomeDTO struct {
	// ToRevision is the revision number rolled back to.
	ToRevision int64 `json:"toRevision"`
	// DryRun reports whether this outcome came from a server-side dry run —
	// nothing was persisted when it is true.
	DryRun bool `json:"dryRun"`
}

// toRollbackOutcome converts a domain rollback outcome for the frontend.
func toRollbackOutcome(outcome domain.RollbackOutcome) RollbackOutcomeDTO {
	return RollbackOutcomeDTO{
		ToRevision: outcome.ToRevision,
		DryRun:     outcome.DryRun,
	}
}

// ApplyOutcomeDTO reports what UpdateResource or ValidateResource actually
// did — what domain.ApplyOutcome carries, over the wire.
type ApplyOutcomeDTO struct {
	// Created is true when the object did not exist and was created; false
	// when an existing object was replaced.
	Created bool `json:"created"`
	// Kind is the applied object's Kubernetes kind, e.g. "Deployment".
	Kind string `json:"kind"`
	// Name is the applied object's name.
	Name string `json:"name"`
	// Namespace is empty for a cluster-scoped kind.
	Namespace string `json:"namespace"`
	// DryRun reports whether this outcome came from a server-side dry run —
	// ValidateResource always sets it; UpdateResource never does. Nothing
	// was persisted when it is true.
	DryRun bool `json:"dryRun"`
	// Warnings carries any warning the API server attached to the request.
	// Always empty today — see Adapter.UpdateResource's own comment on why —
	// but present on the wire so the frontend does not need a second shape
	// once it is wired.
	Warnings []string `json:"warnings"`
}

// toApplyOutcome converts a domain apply outcome for the frontend.
func toApplyOutcome(outcome domain.ApplyOutcome) ApplyOutcomeDTO {
	// Never nil on the wire: the frontend's type is string[], and JSON null
	// where an array is expected is a needless special case for every caller
	// to guard against.
	warnings := outcome.Warnings
	if warnings == nil {
		warnings = []string{}
	}

	return ApplyOutcomeDTO{
		Created:   outcome.Created,
		Kind:      outcome.Kind,
		Name:      outcome.Name,
		Namespace: outcome.Namespace.String(),
		DryRun:    outcome.DryRun,
		Warnings:  warnings,
	}
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

// GraphNode is one box on a pod's dependency map.
type GraphNode struct {
	ID string `json:"id"`
	// Kind is the map's own category — "ingress", "service", "workload",
	// "pod", "container" and so on. Not the Kubernetes kind: the map groups a
	// Deployment and a StatefulSet as one thing because a reader following a
	// request does not need them distinguished at that moment.
	Kind string `json:"kind"`
	// APIKind is the Kubernetes kind, for the label and for following the node
	// into its own panel. Empty for containers, which are not objects.
	APIKind   string `json:"apiKind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	// Tier is how far down the request path this sits, and drives the layout.
	Tier int `json:"tier"`
	// Detail is a short qualifier — a port, an image tag, a host.
	Detail string `json:"detail"`
	// Healthy is false for anything worth looking at. A map in one colour says
	// where things are; the colour says where to start.
	Healthy bool `json:"healthy"`
	// Subject marks the object the map was opened from.
	Subject bool `json:"subject"`
	// Group names the node whose children this is one of, for folding a
	// sibling set. Empty for anything with no natural set. The graph is always
	// complete — what is drawn is the view's decision.
	Group string `json:"group"`
}

// GraphEdge is a dependency, drawn the way a request travels.
type GraphEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label"`
}

// PodGraph is the dependency chain around one pod.
type PodGraph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
	// Unreadable names sources that could not be read, so the map can say it
	// is incomplete rather than implying nothing is there.
	Unreadable []string `json:"unreadable"`
}

func toPodGraph(graph domain.PodGraph) PodGraph {
	out := PodGraph{
		Nodes:      make([]GraphNode, 0, len(graph.Nodes)),
		Edges:      make([]GraphEdge, 0, len(graph.Edges)),
		Unreadable: graph.Unreadable,
	}

	for _, node := range graph.Nodes {
		out.Nodes = append(out.Nodes, GraphNode{
			ID:        node.ID,
			Kind:      string(node.Kind),
			APIKind:   node.APIKind,
			Name:      node.Name,
			Namespace: node.Namespace,
			Tier:      int(node.Tier),
			Detail:    node.Detail,
			Healthy:   node.Healthy,
			Subject:   node.Subject,
			Group:     node.Group,
		})
	}
	for _, edge := range graph.Edges {
		out.Edges = append(out.Edges, GraphEdge{From: edge.From, To: edge.To, Label: edge.Label})
	}

	if out.Unreadable == nil {
		out.Unreadable = []string{}
	}
	return out
}

// Certificate is one X.509 certificate as shown by a Secret's certificate
// inspection — see BrowseAPI.InspectTLSSecret. Never sent except in answer to
// that deliberate call.
type Certificate struct {
	Subject string   `json:"subject"`
	Issuer  string   `json:"issuer"`
	SANs    []string `json:"sans"`
	// NotBefore and NotAfter are RFC 3339, in UTC.
	NotBefore string `json:"notBefore"`
	NotAfter  string `json:"notAfter"`
	// ExpiresInSeconds is NotAfter minus the time InspectTLSSecret ran, so
	// the frontend never re-derives it from parsing NotAfter itself.
	// Negative once the certificate has expired.
	ExpiresInSeconds   int64  `json:"expiresInSeconds"`
	SerialNumber       string `json:"serialNumber"`
	SignatureAlgorithm string `json:"signatureAlgorithm"`
	PublicKeyAlgorithm string `json:"publicKeyAlgorithm"`
	KeyBits            int    `json:"keyBits"`
	IsCA               bool   `json:"isCA"`
	SelfSigned         bool   `json:"selfSigned"`
}

// toCertificate converts one domain certificate, using now as the reference
// ExpiresInSeconds is measured against.
func toCertificate(cert domain.Certificate, now time.Time) Certificate {
	sans := cert.SANs
	if sans == nil {
		sans = []string{}
	}
	return Certificate{
		Subject:            cert.Subject,
		Issuer:             cert.Issuer,
		SANs:               sans,
		NotBefore:          formatTime(cert.NotBefore),
		NotAfter:           formatTime(cert.NotAfter),
		ExpiresInSeconds:   int64(cert.ExpiresIn(now).Seconds()),
		SerialNumber:       cert.SerialNumber,
		SignatureAlgorithm: cert.SignatureAlgorithm,
		PublicKeyAlgorithm: cert.PublicKeyAlgorithm,
		KeyBits:            cert.KeyBits,
		IsCA:               cert.IsCA,
		SelfSigned:         cert.SelfSigned,
	}
}

// CertificateInsight is one thing worth telling an operator about an
// inspected certificate chain — the certificate equivalent of PodFinding.
type CertificateInsight struct {
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Advice   string `json:"advice"`
}

// toCertificateInsights converts the domain's assessment of one chain.
func toCertificateInsights(insights []domain.CertificateInsight) []CertificateInsight {
	out := make([]CertificateInsight, 0, len(insights))
	for _, insight := range insights {
		out = append(out, CertificateInsight{
			Severity: string(insight.Severity),
			Title:    insight.Title,
			Detail:   insight.Detail,
			Advice:   insight.Advice,
		})
	}
	return out
}

// CertificateChainDTO is one Secret's parsed certificate material, returned
// only from a deliberate InspectTLSSecret call — never from GetManifest and
// never on render.
type CertificateChainDTO struct {
	Leaf          Certificate   `json:"leaf"`
	Intermediates []Certificate `json:"intermediates"`
	// KeyMatches is null when no key was inspected at all — a Secret
	// carrying ca.crt with no tls.key, say — which is a different fact from
	// false and must not be collapsed into it.
	KeyMatches *bool `json:"keyMatches"`
	// Insights are AssessPod's counterpart for a certificate. See
	// domain.CertificateFindings.
	Insights []CertificateInsight `json:"insights"`
}

// toCertificateChain converts a domain certificate chain, using now as the
// reference both ExpiresInSeconds and the insights are computed against.
func toCertificateChain(chain domain.CertificateChain, now time.Time) CertificateChainDTO {
	intermediates := make([]Certificate, 0, len(chain.Intermediates))
	for _, cert := range chain.Intermediates {
		intermediates = append(intermediates, toCertificate(cert, now))
	}

	return CertificateChainDTO{
		Leaf:          toCertificate(chain.Leaf, now),
		Intermediates: intermediates,
		KeyMatches:    chain.KeyMatches,
		Insights:      toCertificateInsights(domain.CertificateFindings(chain, now)),
	}
}

// --- Bulk actions -----------------------------------------------------------

// BulkItemDTO is one selected row, as the frontend hands it back for a bulk
// action: the object's coordinates plus the facts domain.PlanBulk reads.
//
// Each fact is a QUOTATION of a field the row already carries — a pod's
// "Controlled By" split back into kind and name, a workload's desired count,
// a node's cordoned flag — so planning a selection costs no read at all.
// Absent facts (a generic table row knows no owner) arrive as their zero
// values and produce no note, never a guess.
type BulkItemDTO struct {
	// Group, Version and Kind identify the object's kind; the core group is
	// empty, per domain.ResourceKind.
	Group   string `json:"group"`
	Version string `json:"version"`
	Kind    string `json:"kind"`
	// Namespace is empty for a cluster-scoped object.
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// ControllerKind and ControllerName are the controlling ownerReference,
	// both empty when the object has none or the row did not know.
	ControllerKind string `json:"controllerKind"`
	ControllerName string `json:"controllerName"`
	// Replicas is the current desired replica count, read for scale only.
	Replicas int `json:"replicas"`
	// Unschedulable reports a node is cordoned, read for cordon only.
	Unschedulable bool `json:"unschedulable"`
}

// toBulkCandidates converts the frontend's rows into what PlanBulk decides
// over, pinning every ref to id. A row with no name is refused: there is no
// object for a plan line to describe.
func toBulkCandidates(id domain.ClusterID, items []BulkItemDTO) ([]domain.BulkCandidate, error) {
	candidates := make([]domain.BulkCandidate, 0, len(items))
	for _, item := range items {
		ns, err := domain.NewNamespaceName(item.Namespace)
		if err != nil {
			return nil, err
		}
		name := strings.TrimSpace(item.Name)
		if name == "" {
			return nil, domain.ErrEmptyResourceName
		}

		var controller domain.OwnerReference
		if item.ControllerName != "" {
			controller = domain.OwnerReference{Kind: item.ControllerKind, Name: item.ControllerName, Controller: true}
		}

		candidates = append(candidates, domain.BulkCandidate{
			Ref: domain.ResourceRef{
				ClusterID: id,
				Kind:      domain.ResourceKind{Group: item.Group, Version: item.Version, Kind: item.Kind},
				Namespace: ns,
				Name:      name,
			},
			Controller:    controller,
			Replicas:      int32(item.Replicas),
			Unschedulable: item.Unschedulable,
		})
	}
	return candidates, nil
}

// BulkLineDTO is one object's verdict in a bulk plan — what PlanBulk decided
// for it, over the wire.
type BulkLineDTO struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// Act reports whether the action will be attempted on this object.
	Act bool `json:"act"`
	// Reason says why the object is skipped; empty when Act is true.
	Reason string `json:"reason"`
	// Note is what else happens when it acts — a controller that will
	// recreate a deleted object, the count a scale moves from and to. Empty
	// when there is nothing to add.
	Note string `json:"note"`
}

// BulkPlanDTO previews what a bulk action would do, without doing it — what
// PlanBulk returns, over the wire. Acting and Skipped are counted here rather
// than re-derived from Lines by the frontend, so the domain's own rule is the
// one place that decides.
type BulkPlanDTO struct {
	Action  string        `json:"action"`
	Lines   []BulkLineDTO `json:"lines"`
	Acting  int           `json:"acting"`
	Skipped int           `json:"skipped"`
}

// toBulkPlan converts a domain bulk plan for the review dialog.
func toBulkPlan(plan domain.BulkPlan) BulkPlanDTO {
	lines := make([]BulkLineDTO, 0, len(plan.Lines))
	for _, line := range plan.Lines {
		lines = append(lines, BulkLineDTO{
			Kind:      line.Ref.Kind.Kind,
			Namespace: line.Ref.Namespace.String(),
			Name:      line.Ref.Name,
			Act:       line.Act,
			Reason:    line.Reason,
			Note:      line.Note,
		})
	}
	return BulkPlanDTO{
		Action:  string(plan.Action),
		Lines:   lines,
		Acting:  len(plan.Acting()),
		Skipped: plan.Skipped(),
	}
}

// BulkResultDTO is what happened to one object when a bulk action ran — one
// per selected row, in the selection's order, whether it was skipped, done,
// or failed.
type BulkResultDTO struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// Skipped reports the plan left the object alone; Reason says why.
	Skipped bool `json:"skipped"`
	// Done reports the write succeeded. False with Skipped false means it
	// failed, and Code and Reason say how.
	Done bool `json:"done"`
	// Reason is the skip reason, or the failure's operator-facing message —
	// the same sentence a single write's error would carry, produced by the
	// same classification.
	Reason string `json:"reason"`
	// Note carries the plan line's note through.
	Note string `json:"note"`
	// Code is the failure's ErrorCode, the same the frontend parses out of a
	// single write's rejected promise; empty unless the write failed.
	Code string `json:"code"`
}

// toBulkResults converts the application's per-object outcomes, classifying
// each failure exactly as apiError classifies a single write's — so a
// forbidden delete inside a bulk delete reaches the operator with the same
// code and the same advice as one on its own. The full error chain has
// already been logged by the application layer, per object.
func toBulkResults(results []application.BulkResult) []BulkResultDTO {
	out := make([]BulkResultDTO, 0, len(results))
	for _, result := range results {
		dto := BulkResultDTO{
			Kind:      result.Ref.Kind.Kind,
			Namespace: result.Ref.Namespace.String(),
			Name:      result.Ref.Name,
			Skipped:   result.Skipped,
			Reason:    result.Reason,
			Note:      result.Note,
		}
		switch {
		case result.Skipped:
			// Nothing to add: Reason already says why.
		case result.Err != nil:
			code, message := classifyError(result.Err)
			dto.Code = string(code)
			dto.Reason = message
		default:
			dto.Done = true
		}
		out = append(out, dto)
	}
	return out
}
