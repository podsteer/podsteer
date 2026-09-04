package k8s

import (
	"context"
	"errors"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clientgotesting "k8s.io/client-go/testing"

	"github.com/podsteer/podsteer/app/ports"
)

var rolloutListKinds = map[schema.GroupVersionResource]string{rolloutGVR: "RolloutList"}

// pausedCanary is a Rollout held at a pause step — the state an operator
// presses Promote on, and the one every case below varies from.
func pausedCanary() *unstructured.Unstructured {
	object := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{
			"paused": true,
			"strategy": map[string]any{
				"canary": map[string]any{
					"steps": []any{
						map[string]any{"setWeight": int64(20)},
						map[string]any{"pause": map[string]any{}},
						map[string]any{"setWeight": int64(60)},
					},
				},
			},
		},
		"status": map[string]any{
			"currentStepIndex": int64(1),
			"controllerPause":  true,
			"pauseConditions": []any{
				map[string]any{"reason": "CanaryPauseStep", "startTime": "2026-09-04T10:00:00Z"},
			},
		},
	}}
	object.SetAPIVersion("argoproj.io/v1alpha1")
	object.SetKind("Rollout")
	object.SetNamespace("shop")
	object.SetName("checkout")
	return object
}

func newRolloutAdapter(objects ...runtime.Object) (*Adapter, *dynamicfake.FakeDynamicClient) {
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), rolloutListKinds, objects...)
	factory := newClientFactory(Config{})
	factory.clients["dev"] = &clients{dynamic: client}
	return &Adapter{factory: factory}, client
}

// recordPatches captures every patch the adapter sends, with the subresource
// it was sent at — which is the whole thing worth asserting here, since a
// status patch that quietly went to the object instead is a promote that
// reports success and changes nothing.
type recordedPatch struct {
	subresource string
	body        string
}

func recordPatches(client *dynamicfake.FakeDynamicClient, sent *[]recordedPatch, fail func(clientgotesting.PatchAction) error) {
	client.PrependReactor("patch", "rollouts", func(action clientgotesting.Action) (bool, runtime.Object, error) {
		patch, ok := action.(clientgotesting.PatchAction)
		if !ok {
			return false, nil, nil
		}
		if err := fail(patch); err != nil {
			return true, nil, err
		}
		*sent = append(*sent, recordedPatch{subresource: action.GetSubresource(), body: string(patch.GetPatch())})
		return true, &unstructured.Unstructured{Object: map[string]any{}}, nil
	})
}

func never(clientgotesting.PatchAction) error { return nil }

func TestPromoteRolloutSendsTheStatusPatchAtTheSubresource(t *testing.T) {
	// A Rollout's status is served separately on every recent install, so a
	// merge patch at the object would have its status half accepted and
	// discarded. The subresource is not an optimisation; it is the difference
	// between promoting and appearing to.
	adapter, client := newRolloutAdapter(pausedCanary())

	var sent []recordedPatch
	recordPatches(client, &sent, never)

	if err := adapter.PromoteRollout(context.Background(), "dev", "shop", "checkout"); err != nil {
		t.Fatalf("PromoteRollout() error = %v", err)
	}

	if len(sent) != 2 {
		t.Fatalf("sent %d patches, want 2 — the status half and the spec unpause", len(sent))
	}
	if sent[0].subresource != "status" {
		t.Errorf("first patch went to %q, want the status subresource", sent[0].subresource)
	}
	if !strings.Contains(sent[0].body, `"pauseConditions":null`) {
		t.Errorf("status patch = %q, want the pause condition cleared", sent[0].body)
	}
	if sent[1].subresource != "" {
		t.Errorf("second patch went to %q, want the object itself", sent[1].subresource)
	}
	if !strings.Contains(sent[1].body, `"paused":false`) {
		t.Errorf("spec patch = %q, want the unpause", sent[1].body)
	}
}

func TestPromoteRolloutFallsBackToOnePatchWithoutAStatusSubresource(t *testing.T) {
	// An older CRD that never declared one answers 404 for the status path.
	// The unified body then carries both halves at the object, which is
	// exactly what the Argo Rollouts plugin does.
	adapter, client := newRolloutAdapter(pausedCanary())

	var sent []recordedPatch
	recordPatches(client, &sent, func(patch clientgotesting.PatchAction) error {
		if patch.GetSubresource() == "status" {
			return apierrors.NewNotFound(rolloutGVR.GroupResource(), "checkout")
		}
		return nil
	})

	if err := adapter.PromoteRollout(context.Background(), "dev", "shop", "checkout"); err != nil {
		t.Fatalf("PromoteRollout() error = %v", err)
	}

	if len(sent) != 1 {
		t.Fatalf("sent %d patches, want 1 unified one", len(sent))
	}
	if sent[0].subresource != "" {
		t.Errorf("the fallback patch went to %q, want the object itself", sent[0].subresource)
	}
	if !strings.Contains(sent[0].body, `"paused":false`) || !strings.Contains(sent[0].body, `"pauseConditions":null`) {
		t.Errorf("unified patch = %q, want both halves in one body", sent[0].body)
	}
}

func TestPromoteRolloutReportsAFailedStatusPatchRatherThanFallingBack(t *testing.T) {
	// The fallback exists for ONE condition — no subresource served. A
	// forbidden or conflicted status patch must surface, not be retried
	// against the object where it would be refused again for a different
	// reason.
	adapter, client := newRolloutAdapter(pausedCanary())

	var sent []recordedPatch
	recordPatches(client, &sent, func(patch clientgotesting.PatchAction) error {
		if patch.GetSubresource() == "status" {
			return apierrors.NewForbidden(rolloutGVR.GroupResource(), "checkout", errors.New("denied"))
		}
		return nil
	})

	err := adapter.PromoteRollout(context.Background(), "dev", "shop", "checkout")
	if !errors.Is(err, ports.ErrForbidden) {
		t.Fatalf("PromoteRollout() error = %v, want ErrForbidden", err)
	}
	if len(sent) != 0 {
		t.Errorf("sent %v after a refused status patch, want nothing", sent)
	}
}

func TestPromoteRolloutRefusesWhenThereIsNothingToPromote(t *testing.T) {
	// A blue-green Rollout that is neither paused nor holding anything has no
	// promotion to make. Reporting success for a request nothing was sent for
	// would tell an operator something happened.
	idle := pausedCanary()
	unstructured.RemoveNestedField(idle.Object, "spec", "paused")
	unstructured.RemoveNestedField(idle.Object, "spec", "strategy", "canary")
	unstructured.RemoveNestedField(idle.Object, "status", "pauseConditions")

	adapter, client := newRolloutAdapter(idle)

	var sent []recordedPatch
	recordPatches(client, &sent, never)

	if err := adapter.PromoteRollout(context.Background(), "dev", "shop", "checkout"); err == nil {
		t.Fatal("PromoteRollout() succeeded with nothing to promote")
	}
	if len(sent) != 0 {
		t.Errorf("sent %v, want no request at all", sent)
	}
}

func TestPromoteRolloutOnAMissingObjectFailsBeforeAnyPatch(t *testing.T) {
	// The read happens first precisely so this is a missing Rollout rather
	// than an opaque patch failure against a resource nobody confirmed.
	adapter, client := newRolloutAdapter()

	var sent []recordedPatch
	recordPatches(client, &sent, never)

	err := adapter.PromoteRollout(context.Background(), "dev", "shop", "checkout")
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("PromoteRollout() error = %v, want ErrNotFound", err)
	}
	if len(sent) != 0 {
		t.Errorf("sent %v against an object that is not there", sent)
	}
}

func TestAbortRolloutSetsTheOneFieldAtTheStatusSubresource(t *testing.T) {
	adapter, client := newRolloutAdapter(pausedCanary())

	var sent []recordedPatch
	recordPatches(client, &sent, never)

	if err := adapter.AbortRollout(context.Background(), "dev", "shop", "checkout"); err != nil {
		t.Fatalf("AbortRollout() error = %v", err)
	}

	if len(sent) != 1 {
		t.Fatalf("sent %d patches, want 1 — aborting touches no spec field", len(sent))
	}
	if sent[0].subresource != "status" || sent[0].body != `{"status":{"abort":true}}` {
		t.Errorf("patch = %+v, want the abort flag at the status subresource", sent[0])
	}
}

func TestRolloutStateReadsWhatTheDomainCompares(t *testing.T) {
	// Quotation on the way in: the adapter reads fields and interprets none
	// of them. currentStepIndex is the one whose ABSENCE has to survive the
	// crossing, so it is checked in both directions.
	state := rolloutState(pausedCanary())

	if !state.Paused || !state.Canary || state.Steps != 3 || state.PauseConditions != 1 || !state.ControllerPause {
		t.Fatalf("state = %+v, want the paused canary's own facts", state)
	}
	if state.CurrentStepIndex == nil || *state.CurrentStepIndex != 1 {
		t.Fatalf("CurrentStepIndex = %v, want a pointer to 1", state.CurrentStepIndex)
	}
	if state.Inconclusive {
		t.Error("Inconclusive = true with no analysis run recorded")
	}

	begun := pausedCanary()
	unstructured.RemoveNestedField(begun.Object, "status", "currentStepIndex")
	if index := rolloutState(begun).CurrentStepIndex; index != nil {
		t.Errorf("CurrentStepIndex = %v for a Rollout the controller has not stepped, want nil", *index)
	}
}

func TestRolloutStateNoticesAnInconclusiveAnalysisRun(t *testing.T) {
	// It matters because that case holds the Rollout through controllerPause
	// too, so a promote clearing only the pause condition would be undone at
	// the same step.
	rollout := pausedCanary()
	if err := unstructured.SetNestedMap(rollout.Object,
		map[string]any{"name": "checkout-analysis", "status": "Inconclusive"},
		"status", "canary", "currentStepAnalysisRunStatus"); err != nil {
		t.Fatalf("seeding the analysis run: %v", err)
	}

	if !rolloutState(rollout).Inconclusive {
		t.Error("Inconclusive = false with an Inconclusive analysis run in status")
	}
}
