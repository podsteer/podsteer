package domain

import (
	"fmt"
	"sort"
	"time"
)

// This file is the per-workload half of overview.go's "capacity:waste"
// finding. That rule says the cluster as a whole is over-reserved; it cannot
// say by whom, because it never looks past the sum. These rules do the same
// arithmetic per owner and per pod, so a finding can be clicked through to
// the Deployment actually holding the slack.
//
// wasteRatio is reused rather than redeclared, deliberately: a cluster-wide
// verdict and a per-workload one built from different thresholds could
// disagree with each other, and there is no way to explain that to somebody
// reading both cards.

const (
	// sizingSettleTime is how long a pod must have existed before its usage is
	// judged.
	//
	// A pod's first minutes are start-up — pulling an image, warming a cache,
	// running init containers — and usage there looks nothing like steady
	// state. Judging a reservation on it reports every rollout as a finding.
	sizingSettleTime = 15 * time.Minute
	// cpuWasteFloorMilli is the smallest CPU reclaim worth naming a workload
	// over. Half a core is real capacity; less than that is not worth a
	// change request however lopsided the ratio looks.
	cpuWasteFloorMilli = 500
	// memoryWasteFloorBytes is memory's equivalent of cpuWasteFloorMilli.
	memoryWasteFloorBytes = 512 << 20
	// cpuLimitApproaching is where a container's CPU usage is close enough to
	// its limit to be worth calling throttling rather than noise.
	//
	// Unlike memoryLimitApproaching this is not a prelude to a kill: CPU is
	// compressible, so the kernel slows the container instead of ending it.
	// 90% is still the honest line, for the same reason as the memory one —
	// this competes for space with crash loops, and a container idling at 85%
	// of a limit somebody chose is not news.
	cpuLimitApproaching = 90.0
	// memoryOverRequestRatio is how far usage must exceed the request before a
	// pod is "well over" it rather than just Burstable, which every pod with a
	// request below its limit is by design.
	memoryOverRequestRatio = 1.5
	// memoryOverRequestFloorBytes keeps a pod requesting almost nothing (a
	// request of 8Mi at 20Mi used is technically 2.5x) from being reported
	// over a difference nobody would act on.
	memoryOverRequestFloorBytes = 128 << 20
)

// sizingFindings reports which workloads and pods are responsible for the
// slack (or the risk) the cluster-wide capacity findings can only total.
//
// Attribution runs through ownerIndex, never through a label selector: a
// Service's selector says which pods it reaches, not which controller is
// answerable for their requests, and grouping by it would let one workload's
// waste get folded into whatever else happens to share its labels.
func sizingFindings(pods []Pod, owners ownerIndex, measured bool, now time.Time) []Finding {
	// Without metrics every usage figure is zero, which would report every
	// reservation as 100% waste and every limit as untouched — a false
	// verdict, not an absent one. See memoryLimitFindings for the same call.
	if !measured {
		return nil
	}

	findings := make([]Finding, 0, 4)
	findings = append(findings, cpuRequestWasteFindings(pods, owners, now)...)
	findings = append(findings, memoryRequestWasteFindings(pods, owners, now)...)
	findings = append(findings, cpuLimitFindings(pods, now)...)
	findings = append(findings, memoryOverRequestFindings(pods, now)...)
	return findings
}

// sizingEligible reports whether a pod's usage is settled enough to judge its
// reservation by.
func sizingEligible(pod Pod, now time.Time) bool {
	return pod.OccupiesNode() && pod.Usage().Measured && pod.Age(now) >= sizingSettleTime
}

// sizingSubject resolves who a pod's usage should be attributed to: the
// workload an operator names it belongs to, or the pod itself when nothing
// controls it.
func sizingSubject(pod Pod, owners ownerIndex) (kind string, namespace NamespaceName, name string) {
	owner := owners.resolve(pod)
	if owner.Name == "" {
		return "Pod", pod.Namespace(), pod.Name()
	}
	return owner.Kind, pod.Namespace(), owner.Name
}

// ownerUsage aggregates one resource dimension across a workload's eligible
// pods.
type ownerUsage struct {
	kind      string
	namespace NamespaceName
	name      string
	pods      int
	requests  int64
	usage     int64
}

// aggregateOwnerUsage groups eligible pods by the workload they belong to,
// summing whatever requestOf and usageOf extract from each.
func aggregateOwnerUsage(
	pods []Pod, owners ownerIndex, now time.Time, requestOf, usageOf func(Pod) int64,
) map[string]*ownerUsage {
	groups := make(map[string]*ownerUsage)
	for _, pod := range pods {
		if !sizingEligible(pod, now) {
			continue
		}

		kind, namespace, name := sizingSubject(pod, owners)
		key := ownerKey(namespace, kind, name)
		group, ok := groups[key]
		if !ok {
			group = &ownerUsage{kind: kind, namespace: namespace, name: name}
			groups[key] = group
		}
		group.pods++
		group.requests += requestOf(pod)
		group.usage += usageOf(pod)
	}
	return groups
}

// cpuRequestWasteFindings reports workloads reserving far more CPU than they
// use, the per-owner counterpart to capacity:waste.
func cpuRequestWasteFindings(pods []Pod, owners ownerIndex, now time.Time) []Finding {
	groups := aggregateOwnerUsage(pods, owners, now,
		func(p Pod) int64 { return p.Requests().CPUMilli },
		func(p Pod) int64 { return p.Usage().CPUMilli })

	wasteful := wastefulOwners(groups, cpuWasteFloorMilli)
	if len(wasteful) == 0 {
		return nil
	}

	subjects := make([]Subject, 0, min(len(wasteful), maxSubjects))
	var totalRequests, totalUsage int64
	for _, owner := range wasteful {
		totalRequests += owner.requests
		totalUsage += owner.usage
		if len(subjects) < maxSubjects {
			subjects = append(subjects, Subject{
				Kind:      owner.kind,
				Namespace: owner.namespace,
				Name:      owner.name,
				Detail: fmt.Sprintf("requests %s across %s, uses %s (%.0f%%)",
					formatCPU(owner.requests), plural(owner.pods, "pod", "pods"),
					formatCPU(owner.usage), percent(owner.usage, owner.requests)),
			})
		}
	}

	return []Finding{{
		ID:       "sizing:cpu-requests",
		Severity: SeverityInfo,
		Category: CategoryFindingCapacity,
		Title:    "Workloads reserving far more CPU than they use",
		Summary: fmt.Sprintf("%s %s of CPU requests and %s %s — %.0f%% of the reservation",
			plural(len(wasteful), "workload holds", "workloads hold"), formatCPU(totalRequests),
			singularOrPlural(len(wasteful), "uses", "use"), formatCPU(totalUsage),
			percent(totalUsage, totalRequests)),
		Advice: "Lower the request once a longer window agrees — the usage chart shows the last " +
			"minutes, and a monitoring stack shows months. Requests are what fill the cluster; " +
			"reclaiming them is what lets more pods schedule.",
		Subjects: subjects,
		Count:    len(wasteful),
		KindID:   workloadKindID(WorkloadKind(subjects[0].Kind)),
	}}
}

// memoryRequestWasteFindings is cpuRequestWasteFindings' memory counterpart.
func memoryRequestWasteFindings(pods []Pod, owners ownerIndex, now time.Time) []Finding {
	groups := aggregateOwnerUsage(pods, owners, now,
		func(p Pod) int64 { return p.Requests().MemoryBytes },
		func(p Pod) int64 { return p.Usage().MemoryBytes })

	wasteful := wastefulOwners(groups, memoryWasteFloorBytes)
	if len(wasteful) == 0 {
		return nil
	}

	subjects := make([]Subject, 0, min(len(wasteful), maxSubjects))
	var totalRequests, totalUsage int64
	for _, owner := range wasteful {
		totalRequests += owner.requests
		totalUsage += owner.usage
		if len(subjects) < maxSubjects {
			subjects = append(subjects, Subject{
				Kind:      owner.kind,
				Namespace: owner.namespace,
				Name:      owner.name,
				Detail: fmt.Sprintf("requests %s across %s, uses %s (%.0f%%)",
					formatBytes(owner.requests), plural(owner.pods, "pod", "pods"),
					formatBytes(owner.usage), percent(owner.usage, owner.requests)),
			})
		}
	}

	return []Finding{{
		ID:       "sizing:memory-requests",
		Severity: SeverityInfo,
		Category: CategoryFindingCapacity,
		Title:    "Workloads reserving far more memory than they use",
		Summary: fmt.Sprintf("%s %s of memory requests and %s %s — %.0f%% of the reservation",
			plural(len(wasteful), "workload holds", "workloads hold"), formatBytes(totalRequests),
			singularOrPlural(len(wasteful), "uses", "use"), formatBytes(totalUsage),
			percent(totalUsage, totalRequests)),
		Advice: "Memory requests are what the scheduler packs nodes by, and an overstated one strands " +
			"memory nothing else can use. Lower it once a longer window agrees — the usage chart shows " +
			"the last minutes, and a monitoring stack shows months.",
		Subjects: subjects,
		Count:    len(wasteful),
		KindID:   workloadKindID(WorkloadKind(subjects[0].Kind)),
	}}
}

// wastefulOwners filters and orders the owners whose ratio and unused amount
// clear the waste rule, worst (most unused) first.
func wastefulOwners(groups map[string]*ownerUsage, floor int64) []*ownerUsage {
	wasteful := make([]*ownerUsage, 0, len(groups))
	for _, owner := range groups {
		if owner.requests <= 0 {
			continue
		}
		unused := owner.requests - owner.usage
		if percent(owner.usage, owner.requests) > wasteRatio*100 || unused < floor {
			continue
		}
		wasteful = append(wasteful, owner)
	}

	// Namespace before name in the tie-break: two workloads called "api" in
	// different namespaces wasting the same amount would otherwise swap
	// places between refreshes.
	sort.Slice(wasteful, func(i, j int) bool {
		unusedI := wasteful[i].requests - wasteful[i].usage
		unusedJ := wasteful[j].requests - wasteful[j].usage
		if unusedI != unusedJ {
			return unusedI > unusedJ
		}
		if wasteful[i].namespace != wasteful[j].namespace {
			return wasteful[i].namespace < wasteful[j].namespace
		}
		return wasteful[i].name < wasteful[j].name
	})
	return wasteful
}

// singularOrPlural picks the verb form for a count without printing the count
// — the count is already in the sentence, printed once by plural.
func singularOrPlural(count int, singular, pluralForm string) string {
	if count == 1 {
		return singular
	}
	return pluralForm
}

// cpuLimitFindings reports pods throttled at their CPU limit.
//
// The memory equivalent, memoryLimitFindings, is a warning about an
// imminent kill; this one is not. Exceeding a memory limit gets a container
// killed by the OOM killer; exceeding a CPU limit gets it slowed down by the
// kernel, invisibly, with every probe still green. See Pod.CPULimitPercent.
func cpuLimitFindings(pods []Pod, now time.Time) []Finding {
	subjects := make([]Subject, 0, maxSubjects)
	count := 0
	var worst float64

	for _, pod := range pods {
		if !sizingEligible(pod, now) {
			continue
		}
		// A partial limit makes the pod's total meaningless: one container
		// throttling while another is free would still show up as "the pod is
		// fine" if the free one's headroom is averaged in.
		if !allContainersLimitCPU(pod) {
			continue
		}

		used := pod.CPULimitPercent()
		if used < cpuLimitApproaching {
			continue
		}

		count++
		if used > worst {
			worst = used
		}
		if len(subjects) < maxSubjects {
			subjects = append(subjects, Subject{
				Kind:      "Pod",
				Namespace: pod.Namespace(),
				Name:      pod.Name(),
				Detail:    fmt.Sprintf("%.0f%% of its CPU limit", used),
			})
		}
	}

	if count == 0 {
		return nil
	}

	return []Finding{{
		ID:       "sizing:cpu-limit",
		Severity: SeverityWarning,
		Category: CategoryFindingWorkload,
		Title:    "Pods throttled at their CPU limit",
		Summary: fmt.Sprintf("%s at or above %.0f%% of its CPU limit, the worst at %.0f%%",
			plural(count, "pod is", "pods are"), cpuLimitApproaching, worst),
		Advice: "CPU is compressible: at the limit a container is not killed, it is throttled, and a " +
			"throttled request is a slow one. Raise the limit or remove it — a CPU limit rarely " +
			"protects anything the request does not already reserve.",
		Subjects: subjects,
		Count:    count,
		KindID:   podKindID,
	}}
}

// allContainersLimitCPU reports whether every container in the pod declares a
// CPU limit, so the pod's aggregate is a real ceiling rather than one
// container's alone.
func allContainersLimitCPU(pod Pod) bool {
	containers := pod.Containers()
	if len(containers) == 0 {
		return false
	}
	for _, container := range containers {
		if container.Limits.CPUMilli <= 0 {
			return false
		}
	}
	return true
}

// memoryOverRequestFindings reports pods using more memory than they
// reserved.
//
// Requests, not limits, decide what the scheduler thinks a node has room for.
// A pod that has outgrown its request is standing on ground the scheduler
// already promised to somebody else, and under pressure the kubelet evicts
// exactly these pods first — by how far they exceed their request, not by
// how close they are to a limit.
func memoryOverRequestFindings(pods []Pod, now time.Time) []Finding {
	subjects := make([]Subject, 0, maxSubjects)
	count := 0
	var worst float64

	for _, pod := range pods {
		if !sizingEligible(pod, now) {
			continue
		}

		requests := pod.Requests().MemoryBytes
		if requests <= 0 {
			continue
		}
		usage := pod.Usage().MemoryBytes
		over := usage - requests
		ratio := float64(usage) / float64(requests)
		if ratio < memoryOverRequestRatio || over < memoryOverRequestFloorBytes {
			continue
		}

		count++
		if ratio > worst {
			worst = ratio
		}
		if len(subjects) < maxSubjects {
			subjects = append(subjects, Subject{
				Kind:      "Pod",
				Namespace: pod.Namespace(),
				Name:      pod.Name(),
				Detail: fmt.Sprintf("uses %s against a %s request",
					formatBytes(usage), formatBytes(requests)),
			})
		}
	}

	if count == 0 {
		return nil
	}

	return []Finding{{
		ID:       "sizing:memory-request-exceeded",
		Severity: SeverityWarning,
		Category: CategoryFindingWorkload,
		Title:    "Pods using more memory than they reserved",
		Summary: fmt.Sprintf("%s well over its memory request, the worst at %.1f× the reservation",
			plural(count, "pod is", "pods are"), worst),
		Advice: "The scheduler placed these on memory they never reserved. Under node pressure the " +
			"kubelet evicts pods exceeding their request first, and a node full of them is " +
			"overcommitted while every reservation looks fine. Raise the request to what the workload " +
			"actually uses.",
		Subjects: subjects,
		Count:    count,
		KindID:   podKindID,
	}}
}
