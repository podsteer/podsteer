package k8s

import (
	"context"
	"errors"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// Promoting and aborting an Argo Rollouts Rollout.
//
// The adapter half of app/domain/rollout.go: this gathers the facts and sends
// the requests, and decides nothing. Which patch a promote sends is
// domain.PlanRolloutPromote's answer, the same split PlanDrain and PlanBulk
// use — nothing in the domain touches the network and nothing here decides
// what promoting means.
//
// THE OBJECT IS READ IMMEDIATELY BEFORE THE WRITE. A promote is a function of
// the Rollout's live status — which step it is on, what is holding it — and
// planning it from what the drawer fetched when the operator opened the panel
// would promote a step that has since moved on. One extra GET is the price of
// that not happening.

// rolloutGVR is the Argo Rollouts resource, fixed for the same reason
// vulnerabilityReportGVR is: exactly one kind, present or absent.
var rolloutGVR = schema.GroupVersionResource{
	Group:    "argoproj.io",
	Version:  "v1alpha1",
	Resource: "rollouts",
}

// PromoteRollout advances a paused Rollout by one step, the way
// `kubectl argo rollouts promote NAME` does.
func (a *Adapter) PromoteRollout(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name string) error {
	defer a.forgetReads(id)

	op := fmt.Sprintf("promoting rollout %q in %q of %q", name, namespace, id)

	client, rollout, err := a.rolloutFor(ctx, id, namespace, name, op)
	if err != nil {
		return err
	}

	write := domain.PlanRolloutPromote(rolloutState(rollout))
	if write.IsEmpty() {
		// Nothing to send. Reporting success for a request that made no
		// request would tell an operator a promotion happened, and the panel
		// only offers the control when there IS something to promote — so
		// reaching this means the Rollout moved between the two, which is
		// worth saying rather than swallowing.
		return fmt.Errorf("%s: it is not paused and has no step to advance to", op)
	}

	return a.patchRollout(ctx, client, name, write, op)
}

// AbortRollout tells the controller to abandon the update in progress, the
// way `kubectl argo rollouts abort NAME` does.
//
// It does NOT need to read the Rollout first — the patch is one field
// whatever the strategy — but it does anyway, so that aborting something that
// is not a Rollout, or is not there, fails as a missing object rather than as
// an opaque patch error against a resource nobody confirmed exists.
func (a *Adapter) AbortRollout(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name string) error {
	defer a.forgetReads(id)

	op := fmt.Sprintf("aborting rollout %q in %q of %q", name, namespace, id)

	client, _, err := a.rolloutFor(ctx, id, namespace, name, op)
	if err != nil {
		return err
	}

	return a.patchRollout(ctx, client, name, domain.PlanRolloutAbort(), op)
}

// rolloutFor resolves the namespaced client and reads the live object.
func (a *Adapter) rolloutFor(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name, op string) (dynamic.ResourceInterface, *unstructured.Unstructured, error) {
	set, err := a.factory.clientsFor(id)
	if err != nil {
		return nil, nil, err
	}

	client := set.dynamic.Resource(rolloutGVR).Namespace(namespace.String())

	rollout, err := client.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, nil, classify(op, err)
	}
	return client, rollout, nil
}

// patchRollout sends a plan, handling the status subresource and its absence.
//
// STATUS FIRST, AT THE SUBRESOURCE. A Rollout's status is served separately
// on every recent install, so sending one merge patch at the object would
// have its status half accepted and silently discarded — a promote that
// reports success and changes nothing. Only when the API server answers 404
// for the subresource path (an old CRD that never declared one) is the
// unified body sent at the object instead, which is precisely the fallback
// the Argo Rollouts plugin performs.
func (a *Adapter) patchRollout(ctx context.Context, client dynamic.ResourceInterface, name string, write domain.RolloutWrite, op string) error {
	body := write.Spec

	if write.Status != "" {
		_, err := client.Patch(ctx, name, types.MergePatchType, []byte(write.Status), metav1.PatchOptions{}, "status")
		switch {
		case err == nil:
		case errors.Is(classify(op, err), ports.ErrNotFound):
			// No status subresource. Everything the plan wanted goes in one
			// request at the object itself.
			body = write.Unified
		default:
			return classify(op, err)
		}
	}

	if body == "" {
		return nil
	}

	if _, err := client.Patch(ctx, name, types.MergePatchType, []byte(body), metav1.PatchOptions{}); err != nil {
		return classify(op, err)
	}
	return nil
}

// rolloutState reads the facts the domain's rules compare out of the live
// object.
//
// QUOTATION ON THE WAY IN, exactly as the panel quotes on the way out:
// nothing here interprets a field, and an absent one arrives as the zero
// value that domain/rollout.go documents a meaning for. currentStepIndex is
// the one field whose ABSENCE matters — nil means the controller has not
// begun stepping, which is not the same as being at step zero — so it crosses
// as a pointer rather than as an int.
func rolloutState(rollout *unstructured.Unstructured) domain.RolloutState {
	object := rollout.Object

	paused, _, _ := unstructured.NestedBool(object, "spec", "paused")
	controllerPause, _, _ := unstructured.NestedBool(object, "status", "controllerPause")
	pauseConditions, _, _ := unstructured.NestedSlice(object, "status", "pauseConditions")
	steps, _, _ := unstructured.NestedSlice(object, "spec", "strategy", "canary", "steps")
	canary, canaryFound, _ := unstructured.NestedMap(object, "spec", "strategy", "canary")

	state := domain.RolloutState{
		Paused:          paused,
		PauseConditions: len(pauseConditions),
		ControllerPause: controllerPause,
		Canary:          canaryFound && canary != nil,
		Steps:           len(steps),
		Inconclusive:    rolloutInconclusive(object),
	}

	if index, found, err := unstructured.NestedInt64(object, "status", "currentStepIndex"); found && err == nil {
		step := int(index)
		state.CurrentStepIndex = &step
	}

	return state
}

// rolloutAnalysisPaths are the four places a Rollout's status records an
// analysis run's outcome — two per strategy. Named rather than searched for,
// because a field this list does not cover is one the controller does not
// write.
var rolloutAnalysisPaths = [][]string{
	{"status", "canary", "currentBackgroundAnalysisRunStatus"},
	{"status", "canary", "currentStepAnalysisRunStatus"},
	{"status", "blueGreen", "prePromotionAnalysisRunStatus"},
	{"status", "blueGreen", "postPromotionAnalysisRunStatus"},
}

// rolloutInconclusive reports whether any analysis run the Rollout references
// came back Inconclusive.
//
// It matters because that case pauses the Rollout through controllerPause as
// well as through a pause condition, so a promote that cleared only the
// condition would be undone by the controller at the same step. See
// domain.PlanRolloutPromote.
func rolloutInconclusive(object map[string]any) bool {
	for _, path := range rolloutAnalysisPaths {
		fields := append(append([]string{}, path...), "status")
		if status, found, err := unstructured.NestedString(object, fields...); found && err == nil && status == "Inconclusive" {
			return true
		}
	}
	return false
}
