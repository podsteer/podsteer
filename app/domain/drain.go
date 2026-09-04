package domain

import "time"

// DrainOptions are the choices an operator makes before draining a node.
//
// A struct rather than positional parameters because PlanDrain and DrainNode
// share it — the plan the UI previews and the drain the adapter actually runs
// must be built from the same options, or a preview showing "nothing
// refused" could be followed by a drain that refuses everything.
type DrainOptions struct {
	// Force allows evicting a bare pod — one with no controller that will
	// recreate it. Without it, a bare pod refuses the whole plan: kubectl's
	// own default, because deleting the only copy of something is not a
	// decision to make on a node's behalf.
	Force bool
	// DeleteEmptyDirData allows evicting a pod that would lose local storage.
	// An emptyDir volume lives on the node, not in the cluster, so evicting
	// the pod without this set would silently discard whatever it held.
	DeleteEmptyDirData bool
	// GracePeriodSeconds is passed to the eviction. Negative means "use the
	// pod's own terminationGracePeriodSeconds" — the same convention
	// `kubectl drain --grace-period` uses, and the reason this is an int
	// rather than a duration: zero is a meaningful choice (evict now) and
	// must be told apart from "not set".
	GracePeriodSeconds int
	// Timeout bounds how long DrainNode retries an eviction a
	// PodDisruptionBudget has refused. Zero means the adapter's own default.
	Timeout time.Duration
}

// DrainCandidate is one pod on a node, with the facts PlanDrain needs that
// are not part of Pod itself.
//
// Kept separate from domain.Pod rather than added to it: Mirror and
// LocalStorage are true only in the context of "is this pod safe to evict",
// which nothing else in PodSteer asks, and a Pod carrying fields no other
// reader ever looks at is a Pod that has stopped describing what it observed.
type DrainCandidate struct {
	// Pod is the candidate pod.
	Pod Pod
	// Mirror reports whether the pod is a static pod mirrored from a
	// manifest on the node's own disk — the kubelet created it directly and
	// owns it, so the API server cannot delete it even though it can list
	// it. The adapter fills this from the `kubernetes.io/config.mirror`
	// annotation.
	Mirror bool
	// LocalStorage reports whether the pod has an emptyDir volume, which
	// lives on the node and is discarded, not migrated, when the pod is
	// evicted.
	LocalStorage bool
}

// DrainReason names why PlanDrain would not evict a pod as it stands.
//
// One type shared by DrainSkip and DrainRefusal rather than two, because both
// are answers to the same question an operator asks looking at a plan — "why
// not this one" — and a single set of constants means the UI has one place to
// map a reason onto copy.
type DrainReason string

const (
	// DrainReasonMirrorPod means the pod is a static pod the kubelet owns.
	// Always skipped, Force included: the API server has no delete for it to
	// honour.
	DrainReasonMirrorPod DrainReason = "static pod — the kubelet owns it, not the API server"
	// DrainReasonDaemonSetPod means a DaemonSet manages the pod. Always
	// skipped: its controller would recreate it on the same node the moment
	// it left, so evicting it accomplishes nothing a drain is for.
	DrainReasonDaemonSetPod DrainReason = "DaemonSet-managed — its controller would recreate it here"
	// DrainReasonLocalStorage means the pod has an emptyDir volume and
	// DeleteEmptyDirData was not set.
	DrainReasonLocalStorage DrainReason = "uses local storage (emptyDir) that would be discarded"
	// DrainReasonBarePod means nothing controls the pod and Force was not
	// set.
	DrainReasonBarePod DrainReason = "no controller — nothing would recreate it"
)

// DrainSkip is a pod PlanDrain leaves alone regardless of options.
//
// "Skipped" rather than "refused": a mirror or DaemonSet pod is not a
// decision for Force or DeleteEmptyDirData to override, because evicting it
// either cannot work or would not help — there is no option that changes the
// answer, which is what separates it from DrainRefusal below.
type DrainSkip struct {
	Pod    Pod
	Reason DrainReason
}

// DrainRefusal is a pod PlanDrain will not evict UNLESS the matching option
// is set — Force for a bare pod, DeleteEmptyDirData for local storage.
//
// A single refusal makes the whole plan un-runnable. kubectl drains a node
// completely or not at all, on the theory that half a drain is the one
// outcome nobody asked for: some capacity freed, some pods still there, and
// no way to tell from the node alone which promise was kept.
type DrainRefusal struct {
	Pod    Pod
	Reason DrainReason
}

// DrainPlan is what PlanDrain decided to do with each candidate.
type DrainPlan struct {
	// Evict are the pods PlanDrain would evict.
	Evict []Pod
	// Skipped are pods left alone regardless of options. See DrainSkip.
	Skipped []DrainSkip
	// Refused are pods that block the plan until an option is changed. See
	// DrainRefusal.
	Refused []DrainRefusal
}

// Runnable reports whether the plan may proceed.
//
// False the moment ANY pod is refused — not merely the refused pods
// themselves. kubectl refuses a drain outright rather than evicting
// everything it can and leaving the rest, because a drain that silently did
// less than asked is worse than one that explains why it did nothing: the
// caller might act as though the node were empty when it is not.
func (p DrainPlan) Runnable() bool {
	return len(p.Refused) == 0
}

// PlanDrain decides what a drain of candidates would do, without touching
// the cluster.
//
// A pure function so the rules can be argued with in a table-driven test
// rather than observed against a real node — see CLAUDE.md on where logic
// belongs. The same function is used to build the preview the UI shows
// before a drain runs and the plan DrainNode actually executes, so the two
// can never disagree.
//
// Rule order matters and mirrors kubectl's own drain: a terminal pod is
// ignored outright (it holds no capacity to free), a mirror pod is always
// skipped, a DaemonSet pod is always skipped, THEN local storage and bare
// ownership are checked as refusals an option can lift. Checking Mirror and
// DaemonSet first means Force and DeleteEmptyDirData can never make PodSteer
// attempt something the API server would refuse anyway.
func PlanDrain(candidates []DrainCandidate, opts DrainOptions) DrainPlan {
	var plan DrainPlan

	for _, candidate := range candidates {
		pod := candidate.Pod

		// A pod already gone from the node's capacity — Succeeded or Failed
		// — is neither evicted nor reported: there is nothing here for a
		// drain to do or explain.
		if !pod.OccupiesNode() {
			continue
		}

		switch {
		case candidate.Mirror:
			plan.Skipped = append(plan.Skipped, DrainSkip{Pod: pod, Reason: DrainReasonMirrorPod})

		case pod.Controller().Kind == "DaemonSet":
			plan.Skipped = append(plan.Skipped, DrainSkip{Pod: pod, Reason: DrainReasonDaemonSetPod})

		case candidate.LocalStorage && !opts.DeleteEmptyDirData:
			plan.Refused = append(plan.Refused, DrainRefusal{Pod: pod, Reason: DrainReasonLocalStorage})

		case pod.Controller().IsZero() && !opts.Force:
			plan.Refused = append(plan.Refused, DrainRefusal{Pod: pod, Reason: DrainReasonBarePod})

		default:
			plan.Evict = append(plan.Evict, pod)
		}
	}

	return plan
}

// DrainFailure is one pod DrainNode tried and failed to evict, or to confirm
// gone.
type DrainFailure struct {
	// Pod names the failed pod as "namespace/name".
	Pod string
	// Reason is the failure, as text: the causes are as varied as any other
	// cluster write, and unlike DrainReason above there is no fixed set of
	// them to name as constants.
	Reason string
}

// DrainReport is what happened when DrainNode ran, whether or not it
// finished cleanly.
//
// Returned alongside an error rather than only on success: cordoned,
// refused, and partially evicted are all outcomes worth showing the operator
// exactly as they happened, not exceptions to be collapsed into one failure
// message.
type DrainReport struct {
	// Cordoned reports whether the node was marked unschedulable. True even
	// when everything after it failed — cordoning is the first and
	// least reversible-looking step, and an operator needs to know it
	// happened even if nothing else did.
	Cordoned bool
	// Evicted are the pods successfully evicted and confirmed gone.
	Evicted []Pod
	// Skipped mirrors the plan's own Skipped — DaemonSet and mirror pods the
	// drain never touched.
	Skipped []DrainSkip
	// Refused mirrors the plan's own Refused. Non-empty only when the drain
	// stopped before evicting anything — see PlanDrain.Runnable.
	Refused []DrainRefusal
	// Failed are pods the drain attempted and could not evict, or could not
	// confirm gone, for a reason other than a PodDisruptionBudget timing
	// out — that case is TimedOut instead.
	Failed []DrainFailure
	// TimedOut reports whether opts.Timeout (or the context) was reached
	// while still waiting on a PodDisruptionBudget or a pod's termination.
	// A timeout is not the same fact as a failure: the eviction may still
	// succeed a moment later, on its own, which is worth saying differently
	// from "refused".
	TimedOut bool
}
