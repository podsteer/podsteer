package domain_test

import (
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

// TestReuseIsOfferedOnlyForARunningPod is the whole of the reuse rule, and the
// reason it lives in the domain rather than in the dialog.
//
// A pod that has exited cannot be attached to, so offering one would hand
// somebody an attach that fails for a reason the offer gave them no way to
// see. Every other phase is still REPORTED — a namespace accumulating exited
// shell pods is exactly the question an operator would otherwise have to ask
// the cluster — and reporting is not offering.
func TestReuseIsOfferedOnlyForARunningPod(t *testing.T) {
	t.Parallel()

	candidates := []domain.ClusterShellCandidate{
		{PodName: "running", Phase: domain.PodPhaseRunning},
		{PodName: "pending", Phase: domain.PodPhasePending},
		{PodName: "succeeded", Phase: domain.PodPhaseSucceeded},
		{PodName: "failed", Phase: domain.PodPhaseFailed},
		{PodName: "unknown", Phase: domain.PodPhaseUnknown},
		{PodName: "unreported", Phase: ""},
	}

	plan := domain.PlanClusterShellReuse(candidates)

	if len(plan.Reusable) != 1 {
		t.Fatalf("reusable = %d, want exactly the one Running pod: %+v", len(plan.Reusable), plan.Reusable)
	}
	if plan.Reusable[0].PodName != "running" {
		t.Errorf("reusable pod = %q, want %q", plan.Reusable[0].PodName, "running")
	}

	if len(plan.Other) != len(candidates)-1 {
		t.Fatalf("other = %d, want the remaining %d — a pod in another state is reported, not dropped",
			len(plan.Other), len(candidates)-1)
	}
	for _, candidate := range plan.Other {
		if candidate.Phase == domain.PodPhaseRunning {
			t.Errorf("pod %q is Running and was not offered", candidate.PodName)
		}
	}
}

// TestNothingIsOfferedWhenTheNamespaceHoldsNothingOfOurs states the ordinary
// case: an empty candidate list produces an empty plan on both sides rather
// than a nil that a caller has to remember to guard.
func TestNothingIsOfferedWhenTheNamespaceHoldsNothingOfOurs(t *testing.T) {
	t.Parallel()

	plan := domain.PlanClusterShellReuse(nil)

	if len(plan.Reusable) != 0 || len(plan.Other) != 0 {
		t.Fatalf("plan = %+v, want both halves empty", plan)
	}
}

// TestEveryCandidateIsInExactlyOneHalfOfThePlan is the completeness rule the
// folded dependency map and the grouped timeline already hold to: nothing may
// be silently dropped on the way to a decision, because a pod PodSteer created
// and did not mention is a pod nobody can account for.
func TestEveryCandidateIsInExactlyOneHalfOfThePlan(t *testing.T) {
	t.Parallel()

	candidates := []domain.ClusterShellCandidate{
		{PodName: "a", Phase: domain.PodPhaseRunning},
		{PodName: "b", Phase: domain.PodPhaseFailed},
		{PodName: "c", Phase: domain.PodPhaseRunning},
	}

	plan := domain.PlanClusterShellReuse(candidates)

	seen := map[string]int{}
	for _, candidate := range plan.Reusable {
		seen[candidate.PodName]++
	}
	for _, candidate := range plan.Other {
		seen[candidate.PodName]++
	}

	for _, candidate := range candidates {
		if seen[candidate.PodName] != 1 {
			t.Errorf("pod %q appears %d times in the plan, want exactly once", candidate.PodName, seen[candidate.PodName])
		}
	}
}
