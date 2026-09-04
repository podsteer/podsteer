package wails

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"

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
	// SchedulablePercent is the headroom as a share of allocatable, so every
	// figure on the card can show its amount and its proportion.
	SchedulablePercent float64 `json:"schedulablePercent"`
	// The same four shares, rounded and rendered here rather than by the
	// browser. The card prints these; the numbers above drive the bar and the
	// threshold colours.
	RequestPercentLabel     string `json:"requestPercentLabel"`
	UsagePercentLabel       string `json:"usagePercentLabel"`
	SchedulablePercentLabel string `json:"schedulablePercentLabel"`
	EfficiencyLabel         string `json:"efficiencyLabel"`
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
// Every count arrives pre-formatted alongside its raw value: the strings are
// what the card prints, and the numbers are what it makes decisions with —
// whether to colour a figure, whether to show a row at all. The frontend does
// no arithmetic on any of it, so the rounding somebody reads here is the same
// rounding the Go tests assert.
type PodCapacity struct {
	Scheduled      int    `json:"scheduled"`
	ScheduledLabel string `json:"scheduledLabel"`
	Healthy        int    `json:"healthy"`
	HealthyLabel   string `json:"healthyLabel"`
	Capacity       int64  `json:"capacity"`
	CapacityLabel  string `json:"capacityLabel"`
	// Free is the slots nothing occupies.
	Free      int64  `json:"free"`
	FreeLabel string `json:"freeLabel"`
	// Reserved is slots on tainted nodes, which only a pod that tolerates
	// them can use. Zero on most clusters, most of the machine on some.
	Reserved      int64  `json:"reserved"`
	ReservedLabel string `json:"reservedLabel"`
	// ReservedNodes is how many nodes hold them, for the sentence that
	// explains the difference between the two figures.
	ReservedNodes int `json:"reservedNodes"`

	Unschedulable      int    `json:"unschedulable"`
	UnschedulableLabel string `json:"unschedulableLabel"`

	// The shares, already rounded, as strings ending in a per-cent sign.
	UsedPercent    string `json:"usedPercent"`
	FreePercent    string `json:"freePercent"`
	HealthyPercent string `json:"healthyPercent"`
	WaitingPercent string `json:"waitingPercent"`
	// UsedPercentValue drives the bar and the threshold colour.
	UsedPercentValue float64 `json:"usedPercentValue"`
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
	// Largest is the biggest bound volume, pre-formatted, with the name that
	// makes it actionable.
	Largest      string `json:"largest"`
	LargestName  string `json:"largestName"`
	LargestBytes int64  `json:"largestBytes"`
	// UnboundBytes drives whether the waiting row says anything.
	UnboundBytes int64 `json:"unboundBytes"`
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
		Largest:       formatBytesValue(summary.LargestBytes),
		LargestName:   summary.LargestName,
		LargestBytes:  summary.LargestBytes,
		UnboundBytes:  summary.UnboundBytes,
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
	// ShareLabel is that figure formatted, empty when nothing was reserved.
	ShareLabel string `json:"shareLabel"`
	// Percent is this pod's share of the whole list's largest, for the bar.
	Percent float64 `json:"percent"`
}

// TopConsumers is what is actually using the cluster.
type TopConsumers struct {
	ByCPU    []Consumer `json:"byCpu"`
	ByMemory []Consumer `json:"byMemory"`
	Measured bool       `json:"measured"`
}

// shareLabel formats usage against a reservation, which may not exist.
func shareLabel(share float64) string {
	if share < 0 {
		return ""
	}
	return formatPercent(share)
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
			Namespace:  string(consumer.Namespace),
			Name:       consumer.Name,
			Node:       consumer.Node,
			Usage:      format(value(consumer)),
			Request:    reserved,
			Share:      share,
			ShareLabel: shareLabel(share),
			Percent:    percent,
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
	// CompiledAt is when the support table was generated, so an "unknown"
	// verdict can say it is an old table rather than a broken one.
	CompiledAt string `json:"compiledAt"`
}

func toReleaseSupport(support domain.ReleaseSupport) ReleaseSupport {
	out := ReleaseSupport{
		Minor:      support.Minor,
		State:      string(support.State),
		Days:       support.Days,
		CompiledAt: domain.ScheduleCompiledAt().Format("2 January 2006"),
	}
	if !support.EndOfLife.IsZero() {
		out.EndOfLife = support.EndOfLife.Format("2 January 2006")
	}
	return out
}

// UpgradeSummary is the one-line version of what the upgrade-impact findings
// found, for the overview header's "check against" selector — the findings
// themselves are already in Overview.Findings, this only saves the frontend
// from filtering and counting them by category.
type UpgradeSummary struct {
	// TargetMinor is the Kubernetes minor the assessment was made against,
	// e.g. "1.33". Empty means no target could be placed at all — Version
	// was unparseable — which the UI reads differently from "assessed and
	// found nothing".
	TargetMinor string `json:"targetMinor"`
	// Count is how many upgrade-impact findings were raised, at any
	// severity.
	Count int `json:"count"`
}

func toUpgradeSummary(upgrade domain.UpgradeSummary) UpgradeSummary {
	return UpgradeSummary{TargetMinor: upgrade.TargetMinor, Count: upgrade.Count}
}

// NodeLoad is one node's share of the work.
type NodeLoad struct {
	Name         string `json:"name"`
	Ready        bool   `json:"ready"`
	Schedulable  bool   `json:"schedulable"`
	ControlPlane bool   `json:"controlPlane"`
	// Reserved reports a node whose taint refuses untolerated pods. Its
	// shares are still its own; this is what stops them being read as
	// headroom anybody can use.
	Reserved bool `json:"reserved"`
	// The shares drive the bars and the threshold colours.
	CPUPercent float64 `json:"cpuPercent"`
	MemPercent float64 `json:"memoryPercent"`
	PodPercent float64 `json:"podPercent"`
	// DiskPercent is -1 when no kubelet answered for this node.
	DiskPercent float64 `json:"diskPercent"`
	Pods        int     `json:"pods"`
	// What the node is CONSUMING, as opposed to every share above, which is
	// what pods reserved. Raw rather than formatted because the only consumer
	// is the usage chart, which needs numbers to plot.
	//
	// UsageMeasured distinguishes a genuinely idle node from one no metrics
	// API answered for; without it a cluster with no metrics-server would
	// chart a confident flat zero.
	UsageCPUMilli    int64 `json:"usageCpuMilli"`
	UsageMemoryBytes int64 `json:"usageMemoryBytes"`
	UsageMeasured    bool  `json:"usageMeasured"`
	// The amounts and the shares, formatted here rather than by the browser,
	// so a node's row reads the same way a capacity track does: the quantity,
	// then what proportion of the node it is.
	CPUAmount    string `json:"cpuAmount"`
	MemAmount    string `json:"memoryAmount"`
	PodAmount    string `json:"podAmount"`
	DiskAmount   string `json:"diskAmount"`
	CPUShare     string `json:"cpuShare"`
	MemShare     string `json:"memoryShare"`
	PodShare     string `json:"podShare"`
	DiskShare    string `json:"diskShare"`
	DiskMeasured bool   `json:"diskMeasured"`
}

// diskAmount and diskShare render the one dimension that can be absent.
//
// An em dash rather than a zero: nothing in the core API knows how full a
// node's disk is, and "0" would say the opposite of "nobody could be asked".
func diskAmount(used int64, measured bool) string {
	if !measured {
		return "—"
	}
	return formatBytesValue(used)
}

func diskShare(percent float64, measured bool) string {
	if !measured {
		return ""
	}
	return formatPercent(percent)
}

func toNodeLoads(loads []domain.NodeLoad) []NodeLoad {
	out := make([]NodeLoad, 0, len(loads))
	for _, load := range loads {
		measured := load.DiskPercent >= 0
		out = append(out, NodeLoad{
			Name:         load.Name,
			Ready:        load.Ready,
			Schedulable:  load.Schedulable,
			ControlPlane: load.ControlPlane,
			Reserved:     load.Reserved,
			CPUPercent:   load.CPUPercent,
			MemPercent:   load.MemoryPercent,
			PodPercent:   load.PodPercent,
			DiskPercent:  load.DiskPercent,
			Pods:         load.Pods,

			CPUAmount:  formatMilliValue(load.CPUMilli),
			MemAmount:  formatBytesValue(load.MemoryBytes),
			PodAmount:  formatCount(int64(load.Pods)),
			DiskAmount: diskAmount(load.DiskUsedBytes, measured),
			CPUShare:   formatPercent(load.CPUPercent),
			MemShare:   formatPercent(load.MemoryPercent),
			PodShare:   formatPercent(load.PodPercent),
			DiskShare:  diskShare(load.DiskPercent, measured),

			DiskMeasured: measured,

			UsageCPUMilli:    load.Usage.CPUMilli,
			UsageMemoryBytes: load.Usage.MemoryBytes,
			UsageMeasured:    load.Usage.Measured,
		})
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
	// Schedulable counts nodes an ordinary pod could be placed on, and
	// Tainted those that refuse one without a toleration.
	Schedulable int `json:"schedulable"`
	Tainted     int `json:"tainted"`
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
	NodeLoads   []NodeLoad            `json:"nodeLoads"`
	Pods        PodSummary            `json:"pods"`
	Workloads   []WorkloadKindSummary `json:"workloads"`
	Namespaces  []NamespaceLoad       `json:"namespaces"`
	Restarts    []RestartHotspot      `json:"restarts"`
	// Unavailable names data sources that could not be read, so the UI can
	// say "no metrics" instead of quietly showing zeroes.
	Unavailable []string `json:"unavailable"`
	// Metrics says why usage figures are absent, when they are: "measured",
	// "not-installed", "forbidden" or "failed". The UI needs the distinction
	// because "install metrics-server" is the wrong advice for somebody who
	// is merely not allowed to read the one already running.
	Metrics string `json:"metrics"`
	// Backend names a monitoring system already running in this cluster, when
	// one was found — empty otherwise, which is the ordinary case.
	//
	// It changes nothing PodSteer measures. It is here so the UI can point at
	// a system that already keeps months of the same figures PodSteer keeps
	// minutes of, instead of presenting its own window as the whole picture.
	Backend MetricsBackend `json:"backend"`
	// KubeState names a kube-state-metrics installation found in this
	// cluster, when one was found — empty otherwise, which is the ordinary
	// case.
	//
	// It changes nothing PodSteer measures either, and unlike Backend it is
	// not even a candidate for being read: it is a scrape endpoint, and the
	// note built on it says in as many words that the figures on this screen
	// come from the metrics API and from PodSteer's own samples instead.
	KubeState KubeStateMetrics `json:"kubeState"`
	// Counts the findings by severity, so the header does not have to.
	CriticalCount int `json:"criticalCount"`
	WarningCount  int `json:"warningCount"`
	InfoCount     int `json:"infoCount"`
	// Upgrade summarises the upgrade-impact findings against Support.Minor's
	// next release, or whatever minor the "check against" selector chose —
	// see GetOverviewForTarget.
	Upgrade UpgradeSummary `json:"upgrade"`
	// KnownMinors is every minor the support-window table has an entry for,
	// oldest first — what the "check against" selector offers, bounded to
	// versions this build can actually reason about instead of a free-text
	// field that could ask about one neither table has heard of.
	KnownMinors []string `json:"knownMinors"`
}

// MetricsBackend is a monitoring system found running in the cluster.
type MetricsBackend struct {
	// Kind is "prometheus", or empty when nothing was found.
	Kind string `json:"kind"`
	// Label is what to show a person, e.g. "Prometheus in monitoring".
	Label     string `json:"label"`
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
	Port      string `json:"port"`
}

// KubeStateMetrics is a kube-state-metrics installation found in the cluster.
//
// No proxy target and no query surface, deliberately — see
// domain.KubeStateMetrics. What crosses the bridge is what a person needs to
// go and look at it.
type KubeStateMetrics struct {
	// Found reports whether anything was discovered. A boolean rather than
	// leaving the frontend to test Service for emptiness, so "is it there"
	// is answered by the same rule on both sides of the bridge.
	Found bool `json:"found"`
	// Label is what to show a person, e.g. "kube-state-metrics in monitoring".
	Label     string `json:"label"`
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
	// Port may be empty: a service whose metrics port this build does not
	// recognise is still kube-state-metrics, and nothing here connects to it.
	Port string `json:"port"`
}

func toKubeStateMetrics(state domain.KubeStateMetrics) KubeStateMetrics {
	return KubeStateMetrics{
		Found:     state.Found(),
		Label:     state.Describe(),
		Namespace: string(state.Namespace),
		Service:   state.Service,
		Port:      state.Port,
	}
}

func toMetricsBackend(backend domain.MetricsBackend) MetricsBackend {
	return MetricsBackend{
		Kind:      backend.Kind,
		Label:     backend.Describe(),
		Namespace: string(backend.Namespace),
		Service:   backend.Service,
		Port:      backend.Port,
	}
}

// sources renders the unavailable-source list as an empty array rather than
// null when nothing failed, which is also the truthful encoding: "nothing
// could not be read" is a fact about the assessment, not the absence of one.
func sources(names []string) []string {
	if names == nil {
		return []string{}
	}
	return names
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
		NodeLoads:   toNodeLoads(overview.NodeLoads),
		Consumers: TopConsumers{
			ByCPU:    toConsumers(overview.Consumers.ByCPU, true),
			ByMemory: toConsumers(overview.Consumers.ByMemory, false),
			Measured: overview.Consumers.Measured,
		},
		Pods:       toPodSummary(overview.Pods),
		Workloads:  toWorkloadSummaries(overview.Workloads),
		Namespaces: toNamespaceLoads(overview.Namespaces, overview.Capacity),
		Restarts:   toRestartHotspots(overview.Restarts),
		// NEVER NULL ON THE WIRE. A nil slice marshals as `null`, and both
		// readers of this field test its length on every render — the
		// overview's "assessed without …" line, and the notification rule
		// that compares one refresh's source set with the last. `toFindings`
		// and the rest of this file already build their slices with `make`
		// for the same reason; this one was the exception.
		Unavailable: sources(overview.Unavailable),
		Metrics:     string(overview.Metrics),
		Backend:     toMetricsBackend(overview.Backend),
		KubeState:   toKubeStateMetrics(overview.KubeState),
		Upgrade:     toUpgradeSummary(overview.Upgrade),
		KnownMinors: domain.KnownMinors(),
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
		Pods:      toPodCapacity(capacity.Pods),
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
		Allocatable:        format(usage.Allocatable),
		Requests:           format(usage.Requests),
		Limits:             format(usage.Limits),
		Usage:              measuredUsage,
		PodUsage:           measuredPodUsage,
		Schedulable:        format(usage.Schedulable()),
		RequestPercent:     usage.RequestPercent(),
		LimitPercent:       usage.LimitPercent(),
		UsagePercent:       usage.UsagePercent(),
		SchedulablePercent: usage.SchedulablePercent(),
		Efficiency:         usage.Efficiency(),
		Measured:           usage.Measured,
		Reported:           usage.Allocatable > 0,
		Declared:           usage.Requests > 0,

		RequestPercentLabel:     formatPercent(usage.RequestPercent()),
		UsagePercentLabel:       formatPercent(usage.UsagePercent()),
		SchedulablePercentLabel: formatPercent(usage.SchedulablePercent()),
		EfficiencyLabel:         efficiencyLabel(usage.Efficiency()),
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
		Schedulable:     summary.Schedulable,
		Tainted:         summary.Tainted,
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

// toPodCapacity formats every pod-slot figure for display.
func toPodCapacity(pods domain.PodCapacity) PodCapacity {
	return PodCapacity{
		Scheduled:      pods.Scheduled,
		ScheduledLabel: formatCount(int64(pods.Scheduled)),
		Healthy:        pods.Healthy,
		HealthyLabel:   formatCount(int64(pods.Healthy)),
		Capacity:       pods.Capacity,
		CapacityLabel:  formatCount(pods.Capacity),
		Free:           pods.Free(),
		FreeLabel:      formatCount(pods.Free()),
		Reserved:       pods.Reserved,
		ReservedLabel:  formatCount(pods.Reserved),
		ReservedNodes:  pods.ReservedNodes,

		Unschedulable:      pods.Unschedulable,
		UnschedulableLabel: formatCount(int64(pods.Unschedulable)),

		UsedPercent:      formatPercent(pods.UsedPercent()),
		FreePercent:      formatPercent(pods.FreePercent()),
		HealthyPercent:   formatPercent(pods.HealthyPercent()),
		WaitingPercent:   formatPercent(pods.WaitingPercent()),
		UsedPercentValue: pods.UsedPercent(),
	}
}

// formatCount renders a whole number with thousands separators.
//
// Done here rather than with the browser's toLocaleString so the grouping is
// the same on every machine: a screenshot from one operator's laptop and the
// figure another reads should not differ by locale when the cluster does not.
func formatCount(value int64) string {
	digits := strconv.FormatInt(value, 10)
	if len(digits) <= 3 {
		return digits
	}

	var out []byte
	for i, digit := range []byte(digits) {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, digit)
	}
	return string(out)
}

// efficiencyLabel renders efficiency, which is -1 when nothing was measured.
func efficiencyLabel(efficiency float64) string {
	if efficiency < 0 {
		return ""
	}
	return formatPercent(efficiency)
}

// formatPercent rounds a share and renders it with its sign.
func formatPercent(value float64) string {
	return strconv.FormatFloat(math.Round(value), 'f', 0, 64) + "%"
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
