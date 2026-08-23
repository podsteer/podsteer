package wails

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/podsteer/podsteer/app/domain"
)

// Wire types for the cluster dashboard. As with the other DTO files these are
// the frontend API contract — Wails generates TypeScript from them — so a
// field rename is a breaking change.
//
// Raw numbers are sent alongside formatted strings wherever the frontend needs
// to compare or draw them: a percentage is used for a bar's width and cannot
// be recovered from "23.5 cores". The strings exist so that every view renders
// the same quantity the same way, which is the same reason the pod table gets
// its CPU pre-formatted.

// Subject is one object a finding is about.
type Subject struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// Detail is the specific fact about this object — an exit reason, a
	// replica shortfall, the scheduler's explanation.
	Detail string `json:"detail"`
}

// Finding is one problem, aggregated across the objects it affects.
type Finding struct {
	ID string `json:"id"`
	// Severity is "critical", "warning" or "info".
	Severity string `json:"severity"`
	// Category groups findings by what an operator would do about them.
	Category string `json:"category"`
	Title    string `json:"title"`
	Summary  string `json:"summary"`
	// Advice says what to do about it, which is the half a raw Kubernetes
	// reason string never carries.
	Advice   string    `json:"advice"`
	Subjects []Subject `json:"subjects"`
	Count    int       `json:"count"`
	// KindID is the navigator target, so the UI can offer to open the list
	// the finding came from.
	KindID string `json:"kindId"`
	// Truncated reports whether more objects are affected than are listed.
	Truncated     bool  `json:"truncated"`
	OldestSeconds int64 `json:"oldestSeconds"`
}

// ResourceUsage is one dimension of cluster capacity.
type ResourceUsage struct {
	// Allocatable, Requests, Limits and Usage are pre-formatted for display.
	Allocatable string `json:"allocatable"`
	Requests    string `json:"requests"`
	Limits      string `json:"limits"`
	Usage       string `json:"usage"`
	// Schedulable is the allocatable amount not already requested — the
	// headroom a new pod can actually claim.
	Schedulable string `json:"schedulable"`
	// PodUsage is what the pods alone consume, which is the half of the
	// efficiency ratio that Usage — measured across whole nodes — is not.
	PodUsage string `json:"podUsage"`
	// The percentages drive the bars, so they are sent as numbers.
	RequestPercent float64 `json:"requestPercent"`
	LimitPercent   float64 `json:"limitPercent"`
	UsagePercent   float64 `json:"usagePercent"`
	// Efficiency is usage as a percentage of requests, or -1 when nothing was
	// measured. It is the number that says how much of the reservation is
	// actually being used.
	Efficiency float64 `json:"efficiency"`
	// Measured distinguishes "measured zero" from "no metrics-server".
	Measured bool `json:"measured"`
	// Reported says whether the cluster published any capacity for this
	// dimension at all.
	//
	// The amounts above are pre-formatted for display, so the frontend cannot
	// tell "0" from "not published" by looking at them — and the difference
	// matters for ephemeral storage, which some providers do not report.
	// Drawing an empty track for it would assert a measurement nobody made.
	Reported bool `json:"reported"`
	// Declared says whether anything requests this dimension. Ephemeral
	// storage is usually declared by nothing at all, which is a finding about
	// the cluster rather than a gap in the data.
	Declared bool `json:"declared"`
}

// PodCapacity is how many pods the cluster runs against how many it can.
type PodCapacity struct {
	Scheduled     int     `json:"scheduled"`
	Capacity      int64   `json:"capacity"`
	Unschedulable int     `json:"unschedulable"`
	UsedPercent   float64 `json:"usedPercent"`
}

// CapacitySummary is the cluster's capacity across every dimension.
type CapacitySummary struct {
	CPU       ResourceUsage `json:"cpu"`
	Memory    ResourceUsage `json:"memory"`
	Ephemeral ResourceUsage `json:"ephemeral"`
	Pods      PodCapacity   `json:"pods"`
}

// StorageSummary is the cluster's persistent storage at a glance.
type StorageSummary struct {
	// Provisioned, Unbound and Orphaned are pre-formatted for display.
	Provisioned string `json:"provisioned"`
	Unbound     string `json:"unbound"`
	Orphaned    string `json:"orphaned"`
	// OrphanedBytes drives whether the row is shown at all, which a formatted
	// string cannot answer.
	OrphanedBytes int64 `json:"orphanedBytes"`
	// Claims and Volumes count each by phase, most interesting first.
	Claims       []PhaseCount `json:"claims"`
	Volumes      []PhaseCount `json:"volumes"`
	TotalClaims  int          `json:"totalClaims"`
	TotalVolumes int          `json:"totalVolumes"`
	// Classes breaks the provisioned total down, largest first.
	Classes []StorageClassUsage `json:"classes"`
}

// PhaseCount is how many objects are in one phase.
type PhaseCount struct {
	Phase string `json:"phase"`
	Count int    `json:"count"`
}

// StorageClassUsage is one class's share of the provisioned total.
type StorageClassUsage struct {
	Name    string `json:"name"`
	Volumes int    `json:"volumes"`
	Size    string `json:"size"`
	// Share is this class's percentage of the provisioned total, for the bar.
	Share float64 `json:"share"`
}

// toStorage translates the storage summary.
//
// Phases are emitted in a fixed order rather than by count, so the row does not
// rearrange itself between refreshes, and zero counts are dropped: a cluster
// with nothing Lost should say nothing about Lost rather than show a zero
// beside the phases that matter.
func toStorage(summary domain.StorageSummary) StorageSummary {
	claims := make([]PhaseCount, 0, 3)
	for _, phase := range []domain.ClaimPhase{domain.ClaimBound, domain.ClaimPending, domain.ClaimLost} {
		if count := summary.Claims[phase]; count > 0 {
			claims = append(claims, PhaseCount{Phase: string(phase), Count: count})
		}
	}

	volumes := make([]PhaseCount, 0, 4)
	for _, phase := range []domain.VolumePhase{
		domain.VolumeBound, domain.VolumeAvailable, domain.VolumeReleased, domain.VolumeFailed,
	} {
		if count := summary.Volumes[phase]; count > 0 {
			volumes = append(volumes, PhaseCount{Phase: string(phase), Count: count})
		}
	}

	classes := make([]StorageClassUsage, 0, len(summary.Classes))
	for _, class := range summary.Classes {
		share := 0.0
		if summary.ProvisionedBytes > 0 {
			share = float64(class.Bytes) / float64(summary.ProvisionedBytes) * 100
		}
		classes = append(classes, StorageClassUsage{
			Name:    class.Name,
			Volumes: class.Volumes,
			Size:    formatBytesValue(class.Bytes),
			Share:   share,
		})
	}

	return StorageSummary{
		Provisioned:   formatBytesValue(summary.ProvisionedBytes),
		Unbound:       formatBytesValue(summary.UnboundBytes),
		Orphaned:      formatBytesValue(summary.OrphanedBytes),
		OrphanedBytes: summary.OrphanedBytes,
		Claims:        claims,
		Volumes:       volumes,
		TotalClaims:   summary.TotalClaims,
		TotalVolumes:  summary.TotalVolumes,
		Classes:       classes,
	}
}

// Consumer is one pod's measured usage beside what it reserved.
type Consumer struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Node      string `json:"node"`
	// Usage and Request are pre-formatted for the dimension being ranked.
	Usage   string `json:"usage"`
	Request string `json:"request"`
	// Share is usage over the reservation as a percentage, or -1 when nothing
	// was reserved — which is itself worth showing rather than hiding.
	Share float64 `json:"share"`
	// Percent is this pod's share of the whole list's largest, for the bar.
	Percent float64 `json:"percent"`
}

// TopConsumers is what is actually using the cluster.
type TopConsumers struct {
	ByCPU    []Consumer `json:"byCpu"`
	ByMemory []Consumer `json:"byMemory"`
	Measured bool       `json:"measured"`
}

// toConsumers translates one ranking, scaling the bars to its own leader.
//
// Scaled to the list's own maximum rather than to the cluster total: five pods
// out of two hundred are all tiny against the cluster, and bars that are all
// two pixels long say nothing about which is the big one.
func toConsumers(consumers []domain.Consumer, cpu bool) []Consumer {
	if len(consumers) == 0 {
		return []Consumer{}
	}

	value := func(c domain.Consumer) int64 {
		if cpu {
			return c.CPUMilli
		}
		return c.MemoryBytes
	}
	request := func(c domain.Consumer) int64 {
		if cpu {
			return c.CPURequestMilli
		}
		return c.MemoryRequestBytes
	}
	format := formatBytesValue
	if cpu {
		format = formatMilliValue
	}

	largest := value(consumers[0])
	out := make([]Consumer, 0, len(consumers))
	for _, consumer := range consumers {
		share := consumer.MemoryOfRequest()
		if cpu {
			share = consumer.CPUOfRequest()
		}

		percent := 0.0
		if largest > 0 {
			percent = float64(value(consumer)) / float64(largest) * 100
		}

		reserved := "—"
		if request(consumer) > 0 {
			reserved = format(request(consumer))
		}

		out = append(out, Consumer{
			Namespace: string(consumer.Namespace),
			Name:      consumer.Name,
			Node:      consumer.Node,
			Usage:     format(value(consumer)),
			Request:   reserved,
			Share:     share,
			Percent:   percent,
		})
	}
	return out
}

// ReleaseSupport is what is known about the version the cluster runs.
type ReleaseSupport struct {
	Minor string `json:"minor"`
	// State is unknown, supported, ending or ended. Unknown means the table
	// does not cover this release, not that anything is wrong with it.
	State string `json:"state"`
	// EndOfLife is formatted for display, empty when nothing is claimed.
	EndOfLife string `json:"endOfLife"`
	// Days until that date, negative once it has passed.
	Days int `json:"days"`
}

func toReleaseSupport(support domain.ReleaseSupport) ReleaseSupport {
	out := ReleaseSupport{
		Minor: support.Minor,
		State: string(support.State),
		Days:  support.Days,
	}
	if !support.EndOfLife.IsZero() {
		out.EndOfLife = support.EndOfLife.Format("2 January 2006")
	}
	return out
}

// NodeSummary counts nodes by condition.
type NodeSummary struct {
	Total         int `json:"total"`
	Ready         int `json:"ready"`
	NotReady      int `json:"notReady"`
	Cordoned      int `json:"cordoned"`
	UnderPressure int `json:"underPressure"`
	ControlPlane  int `json:"controlPlane"`
	// Pressure counts nodes per condition raised, most affected first.
	Pressure []ConditionCount `json:"pressure"`
	// Disks summarises node filesystem occupancy, when kubelets answered.
	Disks DiskSummary `json:"disks"`
	// KubeletVersions counts nodes per version, most common first.
	KubeletVersions []VersionCount `json:"kubeletVersions"`
	OldestSeconds   int64          `json:"oldestSeconds"`
}

// VersionCount is one kubelet version and how many nodes run it.
//
// A slice rather than a map because Wails generates a TypeScript index
// signature for maps, which is awkward to iterate in order — and order is the
// point here.
type VersionCount struct {
	Version string `json:"version"`
	Nodes   int    `json:"nodes"`
}

// PodSummary counts pods by the state an operator cares about.
type PodSummary struct {
	Total       int   `json:"total"`
	Running     int   `json:"running"`
	Pending     int   `json:"pending"`
	Succeeded   int   `json:"succeeded"`
	Failed      int   `json:"failed"`
	Terminating int   `json:"terminating"`
	Unknown     int   `json:"unknown"`
	NotReady    int   `json:"notReady"`
	Restarts    int32 `json:"restarts"`
	BestEffort  int   `json:"bestEffort"`
}

// WorkloadKindSummary counts one controller kind.
type WorkloadKindSummary struct {
	Kind     string `json:"kind"`
	KindID   string `json:"kindId"`
	Title    string `json:"title"`
	Total    int    `json:"total"`
	Healthy  int    `json:"healthy"`
	Rolling  int    `json:"rolling"`
	Degraded int    `json:"degraded"`
}

// NamespaceLoad is one namespace's share of the cluster.
type NamespaceLoad struct {
	Name     string `json:"name"`
	Pods     int    `json:"pods"`
	NotReady int    `json:"notReady"`
	// Formatted for display, plus the percentages the bars need.
	CPURequests    string  `json:"cpuRequests"`
	MemoryRequests string  `json:"memoryRequests"`
	CPUUsage       string  `json:"cpuUsage"`
	MemoryUsage    string  `json:"memoryUsage"`
	CPUShare       float64 `json:"cpuShare"`
	MemoryShare    float64 `json:"memoryShare"`
	Measured       bool    `json:"measured"`
}

// RestartHotspot is a pod worth looking at because it keeps restarting.
type RestartHotspot struct {
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	Restarts   int32  `json:"restarts"`
	Reason     string `json:"reason"`
	AgeSeconds int64  `json:"ageSeconds"`
	Healthy    bool   `json:"healthy"`
}

// Overview is the assessed state of one cluster.
type Overview struct {
	ClusterID string `json:"clusterId"`
	Version   string `json:"version"`
	Platform  string `json:"platform"`
	// Health is "healthy", "degraded" or "critical".
	Health      string                `json:"health"`
	GeneratedAt string                `json:"generatedAt"`
	Findings    []Finding             `json:"findings"`
	Capacity    CapacitySummary       `json:"capacity"`
	Nodes       NodeSummary           `json:"nodes"`
	Storage     StorageSummary        `json:"storage"`
	Consumers   TopConsumers          `json:"consumers"`
	Support     ReleaseSupport        `json:"support"`
	Pods        PodSummary            `json:"pods"`
	Workloads   []WorkloadKindSummary `json:"workloads"`
	Namespaces  []NamespaceLoad       `json:"namespaces"`
	Restarts    []RestartHotspot      `json:"restarts"`
	// Unavailable names data sources that could not be read, so the UI can
	// say "no metrics" instead of quietly showing zeroes.
	Unavailable []string `json:"unavailable"`
	// Counts the findings by severity, so the header does not have to.
	CriticalCount int `json:"criticalCount"`
	WarningCount  int `json:"warningCount"`
	InfoCount     int `json:"infoCount"`
}

func toOverview(overview domain.Overview) Overview {
	out := Overview{
		ClusterID:   string(overview.ClusterID),
		Version:     overview.Version.GitVersion,
		Platform:    overview.Version.Platform,
		Health:      string(overview.Health),
		GeneratedAt: formatTime(overview.GeneratedAt),
		Findings:    toFindings(overview.Findings),
		Capacity:    toCapacity(overview.Capacity),
		Nodes:       toNodeSummary(overview.Nodes),
		Storage:     toStorage(overview.Storage),
		Support:     toReleaseSupport(overview.Support),
		Consumers: TopConsumers{
			ByCPU:    toConsumers(overview.Consumers.ByCPU, true),
			ByMemory: toConsumers(overview.Consumers.ByMemory, false),
			Measured: overview.Consumers.Measured,
		},
		Pods:        toPodSummary(overview.Pods),
		Workloads:   toWorkloadSummaries(overview.Workloads),
		Namespaces:  toNamespaceLoads(overview.Namespaces, overview.Capacity),
		Restarts:    toRestartHotspots(overview.Restarts),
		Unavailable: overview.Unavailable,
	}

	for _, finding := range overview.Findings {
		switch finding.Severity {
		case domain.SeverityCritical:
			out.CriticalCount++
		case domain.SeverityWarning:
			out.WarningCount++
		case domain.SeverityInfo:
			out.InfoCount++
		}
	}

	if out.Unavailable == nil {
		out.Unavailable = []string{}
	}
	return out
}

func toFindings(findings []domain.Finding) []Finding {
	out := make([]Finding, 0, len(findings))
	for _, finding := range findings {
		subjects := make([]Subject, 0, len(finding.Subjects))
		for _, subject := range finding.Subjects {
			subjects = append(subjects, Subject{
				Kind:      subject.Kind,
				Namespace: string(subject.Namespace),
				Name:      subject.Name,
				Detail:    subject.Detail,
			})
		}

		out = append(out, Finding{
			ID:            finding.ID,
			Severity:      string(finding.Severity),
			Category:      string(finding.Category),
			Title:         finding.Title,
			Summary:       finding.Summary,
			Advice:        finding.Advice,
			Subjects:      subjects,
			Count:         finding.Count,
			KindID:        finding.KindID,
			Truncated:     finding.Truncated(),
			OldestSeconds: finding.OldestSeconds,
		})
	}
	return out
}

func toCapacity(capacity domain.CapacitySummary) CapacitySummary {
	return CapacitySummary{
		CPU:       toResourceUsage(capacity.CPU, formatMilliValue),
		Memory:    toResourceUsage(capacity.Memory, formatBytesValue),
		Ephemeral: toResourceUsage(capacity.Ephemeral, formatBytesValue),
		Pods: PodCapacity{
			Scheduled:     capacity.Pods.Scheduled,
			Capacity:      capacity.Pods.Capacity,
			Unschedulable: capacity.Pods.Unschedulable,
			UsedPercent:   capacity.Pods.UsedPercent(),
		},
	}
}

// toResourceUsage formats one capacity dimension with the unit's own renderer.
func toResourceUsage(usage domain.ResourceUsage, format func(int64) string) ResourceUsage {
	measuredUsage, measuredPodUsage := "—", "—"
	if usage.Measured {
		measuredUsage = format(usage.Usage)
		measuredPodUsage = format(usage.PodUsage)
	}

	return ResourceUsage{
		Allocatable:    format(usage.Allocatable),
		Requests:       format(usage.Requests),
		Limits:         format(usage.Limits),
		Usage:          measuredUsage,
		PodUsage:       measuredPodUsage,
		Schedulable:    format(usage.Schedulable()),
		RequestPercent: usage.RequestPercent(),
		LimitPercent:   usage.LimitPercent(),
		UsagePercent:   usage.UsagePercent(),
		Efficiency:     usage.Efficiency(),
		Measured:       usage.Measured,
		Reported:       usage.Allocatable > 0,
		Declared:       usage.Requests > 0,
	}
}

func toNodeSummary(summary domain.NodeSummary) NodeSummary {
	versions := make([]VersionCount, 0, len(summary.KubeletVersions))
	for version, nodes := range summary.KubeletVersions {
		versions = append(versions, VersionCount{Version: version, Nodes: nodes})
	}
	// Most common first, then by version, so the list is stable between
	// refreshes rather than following Go's map iteration order.
	sortVersionCounts(versions)

	return NodeSummary{
		Total:           summary.Total,
		Ready:           summary.Ready,
		NotReady:        summary.NotReady,
		Cordoned:        summary.Cordoned,
		UnderPressure:   summary.UnderPressure,
		ControlPlane:    summary.ControlPlane,
		Pressure:        pressureCounts(summary.Pressure),
		Disks:           toDiskSummary(summary.Disks),
		KubeletVersions: versions,
		OldestSeconds:   summary.OldestSeconds,
	}
}

// DiskSummary is what the kubelets said about node filesystems.
type DiskSummary struct {
	// Measured is how many nodes answered. Zero means the cluster did not
	// grant nodes/proxy, or no kubelet could be reached.
	Measured int `json:"measured"`
	// Fullest is the highest occupancy across every node that answered, which
	// is the one that decides when eviction starts.
	FullestPercent float64 `json:"fullestPercent"`
	// FullestNode names it, so the figure can be acted on.
	FullestNode string `json:"fullestNode"`
	// Filling counts nodes past the warning threshold.
	Filling int `json:"filling"`
}

// toDiskSummary translates the domain's reduction.
func toDiskSummary(summary domain.DiskSummary) DiskSummary {
	return DiskSummary{
		Measured:       summary.Measured,
		FullestPercent: summary.FullestPercent,
		FullestNode:    summary.FullestNode,
		Filling:        summary.Filling,
	}
}

// ConditionCount is how many nodes are raising one condition.
type ConditionCount struct {
	Condition string `json:"condition"`
	Nodes     int    `json:"nodes"`
}

// pressureCounts flattens the per-condition map into a stable slice.
//
// Ordered by the domain's own list of conditions rather than by count, so the
// row does not reorder itself between refreshes as numbers move — a strip that
// rearranges is one an operator has to re-read every time.
func pressureCounts(pressure map[domain.NodeCondition]int) []ConditionCount {
	counts := make([]ConditionCount, 0, len(pressure))
	for _, condition := range domain.KnownPressureConditions() {
		if nodes := pressure[condition]; nodes > 0 {
			counts = append(counts, ConditionCount{Condition: string(condition), Nodes: nodes})
		}
	}
	return counts
}

func toPodSummary(summary domain.PodSummary) PodSummary {
	return PodSummary{
		Total:       summary.Total,
		Running:     summary.Running,
		Pending:     summary.Pending,
		Succeeded:   summary.Succeeded,
		Failed:      summary.Failed,
		Terminating: summary.Terminating,
		Unknown:     summary.Unknown,
		NotReady:    summary.NotReady,
		Restarts:    summary.Restarts,
		BestEffort:  summary.BestEffort,
	}
}

func toWorkloadSummaries(summaries []domain.WorkloadKindSummary) []WorkloadKindSummary {
	out := make([]WorkloadKindSummary, 0, len(summaries))
	for _, summary := range summaries {
		out = append(out, WorkloadKindSummary{
			Kind:     string(summary.Kind),
			KindID:   summary.KindID,
			Title:    workloadTitle(summary.Kind),
			Total:    summary.Total,
			Healthy:  summary.Healthy,
			Rolling:  summary.Rolling,
			Degraded: summary.Degraded,
		})
	}
	return out
}

// toNamespaceLoads formats the namespace breakdown, expressing each namespace
// as a share of what the cluster has rather than as a bare number: "1.2 cores"
// means nothing without knowing the cluster is 4 or 400.
func toNamespaceLoads(loads []domain.NamespaceLoad, capacity domain.CapacitySummary) []NamespaceLoad {
	out := make([]NamespaceLoad, 0, len(loads))
	for _, load := range loads {
		entry := NamespaceLoad{
			Name:     string(load.Name),
			Pods:     load.Pods,
			NotReady: load.NotReady,
			// Unlike the capacity bars, these stand alone with no unit in a
			// header above them, so they carry their own.
			CPURequests:    formatCPUValue(load.CPURequests),
			MemoryRequests: formatBytesValue(load.MemoryRequests),
			CPUUsage:       "—",
			MemoryUsage:    "—",
			Measured:       load.Measured,
		}
		if load.Measured {
			entry.CPUUsage = formatCPUValue(load.CPUUsage)
			entry.MemoryUsage = formatBytesValue(load.MemoryUsage)
		}
		if capacity.CPU.Allocatable > 0 {
			entry.CPUShare = float64(load.CPURequests) / float64(capacity.CPU.Allocatable) * 100
		}
		if capacity.Memory.Allocatable > 0 {
			entry.MemoryShare = float64(load.MemoryRequests) / float64(capacity.Memory.Allocatable) * 100
		}
		out = append(out, entry)
	}
	return out
}

func toRestartHotspots(hotspots []domain.RestartHotspot) []RestartHotspot {
	out := make([]RestartHotspot, 0, len(hotspots))
	for _, hotspot := range hotspots {
		out = append(out, RestartHotspot{
			Namespace:  string(hotspot.Namespace),
			Name:       hotspot.Name,
			Restarts:   hotspot.Restarts,
			Reason:     hotspot.Reason,
			AgeSeconds: hotspot.AgeSeconds,
			Healthy:    hotspot.Healthy,
		})
	}
	return out
}

// workloadTitle returns the plural display name for a controller kind.
func workloadTitle(kind domain.WorkloadKind) string {
	switch kind {
	case domain.WorkloadDeployment:
		return "Deployments"
	case domain.WorkloadStatefulSet:
		return "StatefulSets"
	case domain.WorkloadDaemonSet:
		return "DaemonSets"
	case domain.WorkloadReplicaSet:
		return "ReplicaSets"
	case domain.WorkloadJob:
		return "Jobs"
	case domain.WorkloadCronJob:
		return "CronJobs"
	default:
		return string(kind)
	}
}

// formatMilliValue renders millicores as cores, keeping "0" as a real zero:
// unlike a measurement, a request of zero is a fact rather than an absence.
func formatMilliValue(milli int64) string {
	if milli == 0 {
		return "0"
	}
	if milli < 1000 {
		return fmt.Sprintf("%dm", milli)
	}
	return fmt.Sprintf("%.2f", float64(milli)/1000)
}

// formatCPUValue renders millicores WITH their unit, for figures shown on
// their own.
//
// The bar charts get the unit-less form because their header already says
// "cores"; a namespace row saying "22.66" next to another saying "118.9GiB"
// says nothing at all about what 22.66 is.
func formatCPUValue(milli int64) string {
	if milli == 0 {
		return "0"
	}
	if milli < 1000 {
		return fmt.Sprintf("%dm", milli)
	}
	return fmt.Sprintf("%.2f cores", float64(milli)/1000)
}

// formatBytesValue renders a byte count, keeping a real zero as "0".
func formatBytesValue(bytes int64) string {
	if bytes == 0 {
		return "0"
	}
	return formatBytes(bytes)
}

// sortVersionCounts orders versions by node count, then by version, so the
// list does not reshuffle between refreshes with Go's map iteration order.
func sortVersionCounts(versions []VersionCount) {
	slices.SortFunc(versions, func(left, right VersionCount) int {
		if by := cmp.Compare(right.Nodes, left.Nodes); by != 0 {
			return by
		}
		return cmp.Compare(left.Version, right.Version)
	})
}
