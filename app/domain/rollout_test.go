package domain_test

import (
	"strings"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

func stepIndex(index int) *int { return &index }

func TestPlanRolloutPromoteClearsThePauseHoldingIt(t *testing.T) {
	// The common case: a canary sitting on a pause step. Clearing the pause
	// condition is the whole of the promotion — the controller moves the
	// step on itself once nothing holds it — so nothing here writes a step
	// index.
	write := domain.PlanRolloutPromote(domain.RolloutState{
		Canary:           true,
		Steps:            4,
		CurrentStepIndex: stepIndex(1),
		PauseConditions:  1,
	})

	if write.Status != `{"status":{"pauseConditions":null}}` {
		t.Errorf("status patch = %q, want the bare pause-condition clear", write.Status)
	}
	if write.Spec != "" {
		t.Errorf("spec patch = %q, want none — spec.paused was not set", write.Spec)
	}
	if !strings.Contains(write.Unified, `"paused":false`) {
		t.Errorf("unified patch %q must still carry the unpause for a cluster with no status subresource", write.Unified)
	}
}

func TestPlanRolloutPromoteUnpausesAManuallyPausedRollout(t *testing.T) {
	// spec.paused is an OPERATOR holding the Rollout, and it lives in the
	// spec rather than the status — so it needs the second patch, at the
	// object itself. Sending only the status half would clear the
	// controller's hold and leave the operator's in place, which reads as a
	// promote button that did nothing.
	write := domain.PlanRolloutPromote(domain.RolloutState{
		Canary:           true,
		Steps:            3,
		CurrentStepIndex: stepIndex(0),
		PauseConditions:  1,
		Paused:           true,
	})

	if write.Spec != `{"spec":{"paused":false}}` {
		t.Errorf("spec patch = %q, want the unpause", write.Spec)
	}
	if write.Status == "" {
		t.Error("status patch is empty — the controller's own pause is still holding it")
	}
}

func TestPlanRolloutPromoteAdvancesTheStepWhenNothingIsHoldingIt(t *testing.T) {
	// With no pause condition there is nothing to clear, so promoting means
	// naming the next step outright. The index is the API's own zero-based
	// one: at step index 1 of four, promoting moves to 2.
	write := domain.PlanRolloutPromote(domain.RolloutState{
		Canary:           true,
		Steps:            4,
		CurrentStepIndex: stepIndex(1),
	})

	if !strings.Contains(write.Status, `"currentStepIndex":2`) {
		t.Errorf("status patch = %q, want currentStepIndex 2", write.Status)
	}
	if !strings.Contains(write.Unified, `"currentStepIndex":2`) {
		t.Errorf("unified patch = %q, want the same step", write.Unified)
	}
}

func TestPlanRolloutPromoteDoesNotAdvancePastTheLastStep(t *testing.T) {
	// A Rollout already at the end of its step list stays there. Writing an
	// index past the end would put the status somewhere the spec cannot
	// describe.
	write := domain.PlanRolloutPromote(domain.RolloutState{
		Canary:           true,
		Steps:            3,
		CurrentStepIndex: stepIndex(3),
	})

	if !strings.Contains(write.Status, `"currentStepIndex":3`) {
		t.Errorf("status patch = %q, want the index left at 3", write.Status)
	}
}

func TestPlanRolloutPromoteClearsControllerPauseAfterAnInconclusiveAnalysis(t *testing.T) {
	// An inconclusive analysis holds the Rollout through controllerPause as
	// well as through a pause condition. Clearing only the condition lets the
	// controller re-pause at the same step, so the promote would appear to
	// take and then undo itself.
	write := domain.PlanRolloutPromote(domain.RolloutState{
		Canary:           true,
		Steps:            5,
		CurrentStepIndex: stepIndex(2),
		PauseConditions:  1,
		ControllerPause:  true,
		Inconclusive:     true,
	})

	if !strings.Contains(write.Status, `"controllerPause":false`) {
		t.Errorf("status patch = %q, want controllerPause cleared", write.Status)
	}
	if !strings.Contains(write.Status, `"currentStepIndex":3`) {
		t.Errorf("status patch = %q, want the step advanced to 3", write.Status)
	}
}

func TestPlanRolloutPromoteOnBlueGreenOnlyUnpauses(t *testing.T) {
	// A blue-green Rollout has no steps, so there is no index to move. The
	// unpause and the pause-condition clear are the only meaning promote has
	// for it, and a step index written into its status would describe a
	// strategy it is not running.
	write := domain.PlanRolloutPromote(domain.RolloutState{Paused: true, PauseConditions: 1})

	if strings.Contains(write.Status, "currentStepIndex") {
		t.Errorf("status patch = %q, want no step index on a blue-green Rollout", write.Status)
	}
	if write.Spec != `{"spec":{"paused":false}}` {
		t.Errorf("spec patch = %q, want the unpause", write.Spec)
	}
}

func TestPlanRolloutPromoteWithNoStepIndexYetChangesOnlyTheHold(t *testing.T) {
	// A canary the controller has not begun stepping has a nil
	// currentStepIndex. Nil is not zero: writing 1 would skip the first step
	// of a Rollout that has not run it.
	write := domain.PlanRolloutPromote(domain.RolloutState{Canary: true, Steps: 3})

	if strings.Contains(write.Status, "currentStepIndex") {
		t.Errorf("status patch = %q, want no step index when the controller has set none", write.Status)
	}
}

func TestPlanRolloutPromoteIsEmptyWhenThereIsNothingHoldingItAndNoStepToTake(t *testing.T) {
	// A blue-green Rollout that is neither paused nor held has no promotion
	// to make. The plan still carries a Unified body — that is only ever the
	// fallback for a status patch — so IsEmpty has to ignore it, or the
	// adapter would report success for a request it never sent.
	write := domain.PlanRolloutPromote(domain.RolloutState{})

	if !write.IsEmpty() {
		t.Fatalf("PlanRolloutPromote() = %+v, want an empty plan", write)
	}
}

func TestPlanRolloutAbortSetsTheOneFieldTheControllerReads(t *testing.T) {
	// Abort is one field whatever the strategy, and it goes in the status —
	// so it needs the subresource path with the same unified fallback every
	// other Rollout write uses.
	write := domain.PlanRolloutAbort()

	if write.Status != `{"status":{"abort":true}}` {
		t.Errorf("status patch = %q, want the abort flag", write.Status)
	}
	if write.Spec != "" {
		t.Errorf("spec patch = %q, want none — aborting touches no spec field", write.Spec)
	}
	if write.Unified != write.Status {
		t.Errorf("unified patch = %q, want the same body for a cluster with no status subresource", write.Unified)
	}
	if write.IsEmpty() {
		t.Error("IsEmpty() = true for an abort, which always has something to send")
	}
}
