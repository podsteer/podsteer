package domain

import "testing"

// TestParseFieldConflictLiftsTheManagerOutOfTheServersOwnSentence.
//
// The server's wording is `conflict with "argocd-controller" using apps/v1`,
// and an Update manager's cause adds ` at <timestamp>`. Only the quoted name
// is interpreted; everything else is carried verbatim, because a message this
// does not recognise must render as a long label rather than a confident
// wrong one.
func TestParseFieldConflictLiftsTheManagerOutOfTheServersOwnSentence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		message     string
		wantManager string
		wantKind    ManagerKind
	}{
		{
			name:        "an apply manager",
			message:     `conflict with "argocd-controller" using apps/v1`,
			wantManager: "argocd-controller",
			wantKind:    ManagerGitOps,
		},
		{
			name:        "an update manager carries a timestamp",
			message:     `conflict with "kubectl-client-side-apply" using apps/v1 at 2026-09-10T18:00:00Z`,
			wantManager: "kubectl-client-side-apply",
			wantKind:    ManagerKubectl,
		},
		{
			name:        "the control plane",
			message:     `conflict with "kube-controller-manager" using autoscaling/v2`,
			wantManager: "kube-controller-manager",
			wantKind:    ManagerControlPlane,
		},
		{
			name:        "our own earlier write",
			message:     `conflict with "podsteer" using apps/v1`,
			wantManager: "podsteer",
			wantKind:    ManagerPodSteer,
		},
		{
			name:        "somebody's own operator is not guessed at",
			message:     `conflict with "acme-widget-operator" using acme.io/v1`,
			wantManager: "acme-widget-operator",
			wantKind:    ManagerUnknown,
		},
		{
			name: "a message that does not parse keeps all of itself",
			// Better a long label than a wrong one.
			message:     "something the server said that this does not recognise",
			wantManager: "something the server said that this does not recognise",
			wantKind:    ManagerUnknown,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := ParseFieldConflict(".spec.replicas", test.message)
			if got.Manager != test.wantManager {
				t.Errorf("Manager = %q, want %q", got.Manager, test.wantManager)
			}
			if got.Kind != test.wantKind {
				t.Errorf("Kind = %q, want %q", got.Kind, test.wantKind)
			}
			if got.Message != test.message {
				t.Errorf("Message = %q, want the server's own words verbatim", got.Message)
			}
			if got.Field != ".spec.replicas" {
				t.Errorf("Field = %q, want it carried through", got.Field)
			}
		})
	}
}

// TestClassifyManagerNeverGuesses — the table is hand-compiled and stale by
// construction, exactly like the deprecation and release tables. An operator's
// own controller is far more likely to appear than any name we could
// enumerate, and inventing a category for it would put words in its mouth.
func TestClassifyManagerNeverGuesses(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "  ", "my-operator", "kubernetes-thing", "argo-workflows-controller"} {
		if got := ClassifyManager(name); got != ManagerUnknown {
			t.Errorf("ClassifyManager(%q) = %q, want unknown", name, got)
		}
	}

	// The prefix rule exists because kubectl brands its subcommands.
	for _, name := range []string{"kubectl", "kubectl-edit", "kubectl-client-side-apply"} {
		if got := ClassifyManager(name); got != ManagerKubectl {
			t.Errorf("ClassifyManager(%q) = %q, want kubectl", name, got)
		}
	}
}

// TestAllOwnedByIsFalseForNothing — it decides whether a refusal can be
// resolved without asking anybody, so an empty set must not read as "all
// ours". Nothing is not everything.
func TestAllOwnedByIsFalseForNothing(t *testing.T) {
	t.Parallel()

	if FieldConflicts(nil).AllOwnedBy(ManagerPodSteer) {
		t.Error("an empty conflict set reported itself as entirely ours")
	}

	mine := FieldConflicts{
		{Manager: "podsteer", Kind: ManagerPodSteer},
		{Manager: "podsteer", Kind: ManagerPodSteer},
	}
	if !mine.AllOwnedBy(ManagerPodSteer) {
		t.Error("a set entirely ours did not say so")
	}

	mixed := append(FieldConflicts{}, mine...)
	mixed = append(mixed, FieldConflict{Manager: "argocd-controller", Kind: ManagerGitOps})
	if mixed.AllOwnedBy(ManagerPodSteer) {
		t.Error("a set with one foreign owner reported itself as entirely ours")
	}
}
