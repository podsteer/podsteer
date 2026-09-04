package domain

import "fmt"

// BulkAction is one of the actions a multi-selection can be put through at
// once: several rows of one list, one verb.
type BulkAction string

const (
	// BulkActionDelete deletes every selected object.
	BulkActionDelete BulkAction = "delete"
	// BulkActionRestart triggers a rolling restart of every selected
	// Deployment, StatefulSet or DaemonSet.
	BulkActionRestart BulkAction = "restart"
	// BulkActionScale sets one replica count on every selected Deployment,
	// StatefulSet or ReplicaSet.
	BulkActionScale BulkAction = "scale"
	// BulkActionCordon marks every selected node unschedulable.
	BulkActionCordon BulkAction = "cordon"
	// BulkActionUncordon marks every selected node schedulable again.
	BulkActionUncordon BulkAction = "uncordon"
)

// BulkCandidate is one selected object, with the facts PlanBulk needs that
// the list row already carries.
//
// FACTS, NOT A FRESH READ. The plan is built from what the list on screen
// already knows — a pod's controlling owner, a workload's desired replicas,
// whether a node is cordoned — rather than re-fetching every selected object,
// which would cost one GET per row on a selection that can be a whole page.
// The same shape is what DrainCandidate does for a drain: the adapter (here,
// the frontend's rows) gathers, the domain decides.
type BulkCandidate struct {
	// Ref names the object.
	Ref ResourceRef
	// Controller is the object's controlling ownerReference, zero when it
	// has none — or when the caller could not know, which is the case for a
	// generic table row, whose server-printed cells carry no owner. Absent
	// facts produce no note, never a guess.
	Controller OwnerReference
	// Replicas is the current desired replica count, read for scale only.
	Replicas int32
	// Unschedulable reports whether a node is already cordoned, read for
	// cordon and uncordon only.
	Unschedulable bool
}

// BulkOptions are the choices that shape a bulk plan.
//
// A struct rather than positional parameters for the same reason
// DrainOptions is: PlanBulk builds both the preview the review dialog shows
// and the plan the application layer executes, and the two must be built
// from the same options or the preview could promise something the run
// then refuses.
type BulkOptions struct {
	// Action is what to do to each candidate.
	Action BulkAction
	// Replicas is the target count for BulkActionScale; ignored otherwise.
	Replicas int32
	// ReadOnly reports whether the cluster is marked read-only in PodSteer.
	// With it set, every line is skipped: the preview says so per object so
	// an operator reading it knows why nothing will happen, rather than
	// finding out from one error after confirming.
	ReadOnly bool
}

// BulkLine is what PlanBulk decided to do with one candidate.
//
// One line per candidate, in the candidates' own order, so the review
// dialog can list the selection exactly as the operator made it with a
// verdict beside each row. Act and skip share one type rather than two
// lists because the question a reviewer asks is per object — "what happens
// to this one" — and answering it from two lists means finding the row in
// whichever one it landed in.
type BulkLine struct {
	// Ref names the object.
	Ref ResourceRef
	// Act reports whether the action will be attempted on this object.
	Act bool
	// Reason says why the object is skipped. Empty when Act is true.
	Reason string
	// Note is what else an operator should know before acting: a controller
	// that will recreate a deleted object, the replica count a scale moves
	// from and to. Empty when there is nothing to add — a note on every
	// line is a note nobody reads.
	Note string
}

// BulkPlan is what PlanBulk decided, for every candidate.
type BulkPlan struct {
	// Action is the action the lines were planned for.
	Action BulkAction
	// Lines holds one entry per candidate, in the candidates' order.
	Lines []BulkLine
}

// Acting returns the lines the action will be attempted on.
func (p BulkPlan) Acting() []BulkLine {
	var acting []BulkLine
	for _, line := range p.Lines {
		if line.Act {
			acting = append(acting, line)
		}
	}
	return acting
}

// Skipped returns how many lines the plan leaves alone.
func (p BulkPlan) Skipped() int {
	return len(p.Lines) - len(p.Acting())
}

// Reasons a bulk plan skips an object, as the review dialog shows them.
//
// Constants for the same reason DrainReason has them: the UI has one place
// to read them from, and a test can name the rule it is arguing with.
const (
	// BulkReasonReadOnly means the cluster is marked read-only in PodSteer.
	BulkReasonReadOnly = "cluster is marked read-only in PodSteer"
	// BulkReasonAlreadyCordoned means a cordon would change nothing.
	BulkReasonAlreadyCordoned = "already cordoned"
	// BulkReasonAlreadySchedulable means an uncordon would change nothing.
	BulkReasonAlreadySchedulable = "already schedulable"
)

// PlanBulk decides what a bulk action would do to each candidate, without
// touching the cluster.
//
// A pure function so the rules can be argued with in a table-driven test
// rather than observed against a real cluster — see CLAUDE.md on where logic
// belongs. The same function builds the preview the review dialog shows and
// the plan ManagementService's Bulk methods actually execute, so the two can
// never disagree: an operator who read "3 of 5 will be restarted" must not
// then watch a different three restart.
//
// Rule order: the read-only flag first, because it overrides every other
// answer; then whether the kind supports the action at all; then whether
// acting would change anything (a node already cordoned, a workload already
// at the target count). A candidate that passes every rule acts, carrying a
// note when there is something worth knowing about what follows.
func PlanBulk(candidates []BulkCandidate, opts BulkOptions) BulkPlan {
	plan := BulkPlan{Action: opts.Action, Lines: make([]BulkLine, 0, len(candidates))}

	for _, candidate := range candidates {
		plan.Lines = append(plan.Lines, planBulkLine(candidate, opts))
	}

	return plan
}

func planBulkLine(candidate BulkCandidate, opts BulkOptions) BulkLine {
	line := BulkLine{Ref: candidate.Ref}

	if opts.ReadOnly {
		line.Reason = BulkReasonReadOnly
		return line
	}

	if reason := bulkUnsupported(candidate.Ref.Kind.Kind, opts.Action); reason != "" {
		line.Reason = reason
		return line
	}

	switch opts.Action {
	case BulkActionDelete:
		line.Note = recreationNote(candidate.Controller)

	case BulkActionScale:
		if candidate.Replicas == opts.Replicas {
			line.Reason = fmt.Sprintf("already at %d %s", opts.Replicas, pluralReplicas(opts.Replicas))
			return line
		}
		line.Note = fmt.Sprintf("%d → %d %s", candidate.Replicas, opts.Replicas, pluralReplicas(opts.Replicas))

	case BulkActionCordon:
		if candidate.Unschedulable {
			line.Reason = BulkReasonAlreadyCordoned
			return line
		}

	case BulkActionUncordon:
		if !candidate.Unschedulable {
			line.Reason = BulkReasonAlreadySchedulable
			return line
		}
	}

	line.Act = true
	return line
}

// bulkUnsupported names why kind cannot take action, or "" when it can.
//
// The reasons are specific rather than a generic "not supported": an
// operator who selected a Job alongside three Deployments and asked for a
// restart should read WHY the Job sits out, in terms of what a Job is.
func bulkUnsupported(kind string, action BulkAction) string {
	switch action {
	case BulkActionDelete:
		// Every kind can be deleted. Whether THIS account may is the
		// cluster's answer, reported per object when the delete runs.
		return ""

	case BulkActionRestart:
		switch kind {
		case string(WorkloadDeployment), string(WorkloadStatefulSet), string(WorkloadDaemonSet):
			return ""
		case string(WorkloadJob):
			return "a Job runs to completion; there is no rollout to restart"
		case string(WorkloadCronJob):
			return "a CronJob has no pods of its own; its next run is a new Job"
		case string(WorkloadReplicaSet):
			return "a ReplicaSet has no rollout; restart the Deployment that owns it"
		case "Pod":
			return "a pod cannot be restarted; delete it and its controller replaces it"
		default:
			return fmt.Sprintf("a %s has no rollout to restart", kindOrObject(kind))
		}

	case BulkActionScale:
		switch kind {
		case string(WorkloadDeployment), string(WorkloadStatefulSet), string(WorkloadReplicaSet):
			return ""
		case string(WorkloadDaemonSet):
			return "a DaemonSet runs one pod per node; it has no replica count"
		default:
			return fmt.Sprintf("a %s has no replica count", kindOrObject(kind))
		}

	case BulkActionCordon, BulkActionUncordon:
		if kind == "Node" {
			return ""
		}
		return fmt.Sprintf("only a node can be %sed; this is a %s", action, kindOrObject(kind))

	default:
		return fmt.Sprintf("unknown action %q", action)
	}
}

// kindOrObject names a kind in prose, with a fallback for a candidate whose
// kind the caller left blank.
func kindOrObject(kind string) string {
	if kind == "" {
		return "object"
	}
	return kind
}

// recreationNote says what deleting an object with this controller leads to.
//
// FROM ownerReferences, NEVER FROM A LABEL — the candidate's Controller is
// the controlling ownerReference the adapter mapped, and a label such as
// `app` or `job-name` says nothing about who will act when the object is
// gone (see CLAUDE.md, "A pod belongs to the controller that OWNS it").
//
// The kinds listed reconcile a count: delete one of their objects and the
// controller notices the shortfall and creates a replacement, which is the
// one thing an operator deleting "to restart it" wants confirmed and an
// operator deleting "to get rid of it" wants warned about. A CronJob does
// not: it creates a NEW Job on its next schedule and never recreates the one
// removed. Anything else — an operator's own kind — may or may not, and the
// note says exactly that much.
func recreationNote(controller OwnerReference) string {
	if controller.IsZero() {
		return ""
	}

	owner := controller.Kind + "/" + controller.Name
	switch controller.Kind {
	case string(WorkloadReplicaSet), "ReplicationController", string(WorkloadStatefulSet),
		string(WorkloadDaemonSet), string(WorkloadJob), string(WorkloadDeployment):
		return fmt.Sprintf("owned by %s, which will recreate it", owner)
	case string(WorkloadCronJob):
		return fmt.Sprintf("owned by %s; not recreated — its next run is a new Job", owner)
	default:
		return fmt.Sprintf("owned by %s, which may recreate it", owner)
	}
}

func pluralReplicas(count int32) string {
	if count == 1 {
		return "replica"
	}
	return "replicas"
}
