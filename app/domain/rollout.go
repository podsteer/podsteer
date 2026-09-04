package domain

import "fmt"

// Promoting and aborting an Argo Rollouts Rollout, planned here and executed
// in the adapter — the same split PlanDrain and PlanBulk already use, and for
// the same reason: which patch a promote sends depends on what the Rollout's
// own status says, that dependency is a decision, and a decision belongs
// where a test can argue with it.
//
// THESE ARE ARGO'S PATCHES, NOT OURS. Every body below is transcribed from
// `kubectl argo rollouts`' own implementation
// (pkg/kubectl-argo-rollouts/cmd/promote/promote.go and cmd/abort/abort.go in
// argoproj/argo-rollouts), because the controller reads these fields and
// PodSteer inventing its own way to unpause a Rollout would be inventing a
// second protocol for something that already has one. The kubectl-transparency
// strip beside the button names the exact command this reproduces, and that
// claim has to stay true.
//
// WHY A PATCH RATHER THAN AN APPLY. PodSteer's ordinary write path
// (UpdateResource) replaces the whole object, which for a Rollout would send
// back a status the controller owns and is rewriting concurrently. A merge
// patch of two named fields cannot do that.

// The patch bodies, verbatim from the Argo Rollouts plugin.
const (
	rolloutUnpausePatch                      = `{"spec":{"paused":false}}`
	rolloutClearPauseConditionsPatch         = `{"status":{"pauseConditions":null}}`
	rolloutUnpauseAndClearPatch              = `{"spec":{"paused":false},"status":{"pauseConditions":null}}`
	rolloutClearPauseConditionsWithStepPatch = `{"status":{"pauseConditions":null, "currentStepIndex":%d}}`
	rolloutUnpauseAndClearWithStepPatch      = `{"spec":{"paused":false},"status":{"pauseConditions":null, "currentStepIndex":%d}}`
	rolloutClearPauseAndControllerPausePatch = `{"status":{"pauseConditions":null, "controllerPause":false, "currentStepIndex":%d}}`
	rolloutAbortPatch                        = `{"status":{"abort":true}}`
)

// RolloutWrite is the patch sequence one control sends at a Rollout.
//
// TWO PATCHES, NOT ONE, and the reason is the status subresource: a Rollout's
// status is served separately, so a single merge patch at the object would
// have its status half silently dropped on every cluster that serves it — a
// promote button that appears to work and does nothing, which is the worst
// outcome available here. Status is therefore sent at the subresource first;
// only if the API server does not serve one does Unified go at the object
// itself, carrying both halves. That is exactly the fallback the Argo
// Rollouts plugin performs, and it is why a Rollout write is not a single
// call.
type RolloutWrite struct {
	// Status is the merge patch for the status subresource. Empty when the
	// plan changes nothing in status.
	Status string
	// Spec is the merge patch for the object itself. Empty when the plan
	// changes no spec field.
	Spec string
	// Unified carries both halves in one body, for a cluster whose Rollout
	// CRD serves no status subresource. Sent at the object INSTEAD of Spec,
	// never in addition to it.
	Unified string
}

// IsEmpty reports a plan that would send nothing — the honest answer for a
// Rollout there is nothing to promote on, and the signal to refuse rather
// than issue a no-op write and report success.
//
// UNIFIED IS NOT COUNTED, deliberately. It is the fallback body for a Status
// patch that found no subresource, so it is only ever sent when Status was
// attempted; a plan carrying nothing but Unified would send nothing at all,
// and calling that non-empty would let a promote report success for a request
// it never made. The Argo Rollouts plugin has the same shape and simply
// returns the unchanged object in that case, which reads to an operator as a
// promotion that happened.
func (w RolloutWrite) IsEmpty() bool {
	return w.Status == "" && w.Spec == ""
}

// RolloutState is what a Rollout says about itself, as the facts these plans
// compare.
//
// FIELDS, NOT AN OBJECT, exactly as CertificateRenewal is: app/domain imports
// nothing outside the standard library, so a Rollout crosses this boundary as
// the handful of values the rules actually read. The adapter fills it from
// the live object immediately before writing — never from a list row, and
// never from what the drawer happened to fetch a minute ago, because a
// promote decided from a stale status is a promote of a step that has already
// moved on.
type RolloutState struct {
	// Paused is spec.paused — set by hand or by `kubectl argo rollouts
	// pause`. A different fact from PauseConditions: that is the CONTROLLER
	// holding at a step, this is an operator holding the whole Rollout.
	Paused bool
	// PauseConditions is how many entries status.pauseConditions holds.
	PauseConditions int
	// ControllerPause is status.controllerPause — whether the pause now in
	// effect was the controller's own doing.
	ControllerPause bool
	// Canary reports whether spec.strategy.canary is set. A blue-green
	// Rollout has no steps, so nothing here bumps its step index.
	Canary bool
	// Steps is how many steps the canary strategy declares.
	Steps int
	// CurrentStepIndex is status.currentStepIndex, ZERO-BASED, or nil when
	// the controller has not set one. Nil and zero are different: zero means
	// "at the first step", nil means the controller has not begun.
	CurrentStepIndex *int
	// Inconclusive reports whether any analysis run the Rollout's status
	// references came back Inconclusive. It is the one case that also clears
	// controllerPause, because an inconclusive analysis pauses through a
	// different route than a pause step and leaving the flag set would have
	// the controller re-pause immediately.
	Inconclusive bool
}

// PlanRolloutPromote decides which patches promote a Rollout by one step.
//
// This is the plugin's DEFAULT promote — the one `kubectl argo rollouts
// promote NAME` performs, with no --full and no --skip-current-step. Those
// flags are deliberately not offered: each is a different act with a
// different blast radius, and a button whose behaviour depends on a flag
// nobody can see is not a button anyone should press on a production cluster.
func PlanRolloutPromote(state RolloutState) RolloutWrite {
	write := RolloutWrite{Unified: rolloutUnpauseAndClearPatch}

	if state.Paused {
		write.Spec = rolloutUnpausePatch
	}

	switch {
	case state.Inconclusive && state.PauseConditions > 0 && state.ControllerPause:
		// An inconclusive analysis holds the Rollout through controllerPause
		// as well as through a pause condition; clearing only the condition
		// leaves the controller free to re-pause at the same step.
		if index, ok := nextCanaryStep(state); ok {
			write.Status = fmt.Sprintf(rolloutClearPauseAndControllerPausePatch, index)
		}

	case state.PauseConditions > 0:
		// Held at a pause step. Clearing the condition is the whole of the
		// promotion — the controller advances the step itself once nothing
		// is holding it.
		write.Status = rolloutClearPauseConditionsPatch

	case state.Canary:
		// Nothing is holding it, so promoting means moving the step on
		// explicitly. A blue-green Rollout has no steps and falls through
		// with an unpause and nothing else, which is the only meaning
		// "promote" has for it.
		if index, ok := nextCanaryStep(state); ok {
			write.Status = fmt.Sprintf(rolloutClearPauseConditionsWithStepPatch, index)
			write.Unified = fmt.Sprintf(rolloutUnpauseAndClearWithStepPatch, index)
		}
	}

	return write
}

// PlanRolloutAbort is the patch that aborts a Rollout: one field, whatever
// the strategy and whatever the Rollout is doing.
//
// Aborting is not the reverse of promoting. It tells the controller to scale
// the canary or preview back down and return traffic to the stable
// ReplicaSet; it does NOT roll the spec back, so the Rollout stays Degraded
// against the revision that was being deployed until something changes the
// template. The dialog offering it says so.
func PlanRolloutAbort() RolloutWrite {
	return RolloutWrite{Status: rolloutAbortPatch, Unified: rolloutAbortPatch}
}

// nextCanaryStep returns the step index a promote should move to, and whether
// there is one at all.
//
// The index is ZERO-BASED throughout — it is the API's own field, and
// converting to the 1-based number a panel displays is the panel's job. The
// clamp mirrors the plugin's: a Rollout already at its last step is left
// there rather than advanced past the end of its own step list.
func nextCanaryStep(state RolloutState) (int, bool) {
	if !state.Canary || state.Steps == 0 || state.CurrentStepIndex == nil {
		return 0, false
	}

	index := *state.CurrentStepIndex
	if index < state.Steps {
		index++
	}
	return index, true
}
