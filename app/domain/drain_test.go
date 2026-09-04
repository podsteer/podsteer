package domain_test

import (
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

// drainPod builds a pod scheduled on a node, occupying capacity — the
// ordinary case PlanDrain has to decide about. Tests that need a different
// shape start here and mutate.
func drainPod(t *testing.T, name string, owner domain.OwnerReference) domain.Pod {
	t.Helper()

	var owners []domain.OwnerReference
	if !owner.IsZero() {
		owner.Controller = true
		owners = []domain.OwnerReference{owner}
	}

	pod, err := domain.NewPod(domain.PodSpec{
		Name:      name,
		Namespace: "default",
		ClusterID: "dev",
		Phase:     domain.PodPhaseRunning,
		NodeName:  "node-1",
		Owners:    owners,
	})
	if err != nil {
		t.Fatalf("building pod %q: %v", name, err)
	}
	return pod
}

func replicaSetOwner(name string) domain.OwnerReference {
	return domain.OwnerReference{Kind: "ReplicaSet", Name: name}
}

func daemonSetOwner(name string) domain.OwnerReference {
	return domain.OwnerReference{Kind: "DaemonSet", Name: name}
}

func TestPlanDrainRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		candidate  func(t *testing.T) domain.DrainCandidate
		opts       domain.DrainOptions
		wantEvict  bool
		wantSkip   domain.DrainReason
		wantRefuse domain.DrainReason
		wantIgnore bool
	}{
		{
			name: "controller-owned pod evicts",
			candidate: func(t *testing.T) domain.DrainCandidate {
				return domain.DrainCandidate{Pod: drainPod(t, "web-1", replicaSetOwner("web"))}
			},
			wantEvict: true,
		},
		{
			name: "mirror pod is always skipped",
			candidate: func(t *testing.T) domain.DrainCandidate {
				return domain.DrainCandidate{Pod: drainPod(t, "kube-apiserver-node-1", domain.OwnerReference{}), Mirror: true}
			},
			// Force set to prove mirror wins regardless.
			opts:     domain.DrainOptions{Force: true},
			wantSkip: domain.DrainReasonMirrorPod,
		},
		{
			name: "daemonset pod is always skipped",
			candidate: func(t *testing.T) domain.DrainCandidate {
				return domain.DrainCandidate{Pod: drainPod(t, "fluentd-abcde", daemonSetOwner("fluentd"))}
			},
			wantSkip: domain.DrainReasonDaemonSetPod,
		},
		{
			name: "daemonset pod is skipped even with local storage and force",
			candidate: func(t *testing.T) domain.DrainCandidate {
				return domain.DrainCandidate{Pod: drainPod(t, "fluentd-abcde", daemonSetOwner("fluentd")), LocalStorage: true}
			},
			opts:     domain.DrainOptions{Force: true, DeleteEmptyDirData: true},
			wantSkip: domain.DrainReasonDaemonSetPod,
		},
		{
			name: "local storage pod is refused without the flag",
			candidate: func(t *testing.T) domain.DrainCandidate {
				return domain.DrainCandidate{Pod: drainPod(t, "cache-1", replicaSetOwner("cache")), LocalStorage: true}
			},
			wantRefuse: domain.DrainReasonLocalStorage,
		},
		{
			name: "local storage pod evicts once the flag is set",
			candidate: func(t *testing.T) domain.DrainCandidate {
				return domain.DrainCandidate{Pod: drainPod(t, "cache-1", replicaSetOwner("cache")), LocalStorage: true}
			},
			opts:      domain.DrainOptions{DeleteEmptyDirData: true},
			wantEvict: true,
		},
		{
			name: "bare pod is refused without force",
			candidate: func(t *testing.T) domain.DrainCandidate {
				return domain.DrainCandidate{Pod: drainPod(t, "standalone", domain.OwnerReference{})}
			},
			wantRefuse: domain.DrainReasonBarePod,
		},
		{
			name: "bare pod evicts once forced",
			candidate: func(t *testing.T) domain.DrainCandidate {
				return domain.DrainCandidate{Pod: drainPod(t, "standalone", domain.OwnerReference{})}
			},
			opts:      domain.DrainOptions{Force: true},
			wantEvict: true,
		},
		{
			name: "bare pod with local storage is refused for having no controller, not for storage",
			candidate: func(t *testing.T) domain.DrainCandidate {
				return domain.DrainCandidate{Pod: drainPod(t, "standalone", domain.OwnerReference{}), LocalStorage: true}
			},
			opts:       domain.DrainOptions{DeleteEmptyDirData: true},
			wantRefuse: domain.DrainReasonBarePod,
		},
		{
			name: "a terminal pod is ignored entirely",
			candidate: func(t *testing.T) domain.DrainCandidate {
				pod, err := domain.NewPod(domain.PodSpec{
					Name:      "job-run-xyz",
					Namespace: "default",
					ClusterID: "dev",
					Phase:     domain.PodPhaseSucceeded,
					NodeName:  "node-1",
				})
				if err != nil {
					t.Fatalf("building pod: %v", err)
				}
				return domain.DrainCandidate{Pod: pod}
			},
			wantIgnore: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			candidate := test.candidate(t)
			plan := domain.PlanDrain([]domain.DrainCandidate{candidate}, test.opts)

			switch {
			case test.wantIgnore:
				if len(plan.Evict)+len(plan.Skipped)+len(plan.Refused) != 0 {
					t.Fatalf("plan = %+v, want the terminal pod to appear nowhere", plan)
				}

			case test.wantEvict:
				if len(plan.Evict) != 1 || plan.Evict[0].Name() != candidate.Pod.Name() {
					t.Fatalf("Evict = %+v, want exactly %q", plan.Evict, candidate.Pod.Name())
				}
				if len(plan.Skipped) != 0 || len(plan.Refused) != 0 {
					t.Fatalf("plan = %+v, want nothing skipped or refused", plan)
				}
				if !plan.Runnable() {
					t.Error("Runnable() = false, want true")
				}

			case test.wantSkip != "":
				if len(plan.Skipped) != 1 || plan.Skipped[0].Reason != test.wantSkip {
					t.Fatalf("Skipped = %+v, want one entry with reason %q", plan.Skipped, test.wantSkip)
				}
				if len(plan.Evict) != 0 || len(plan.Refused) != 0 {
					t.Fatalf("plan = %+v, want nothing evicted or refused", plan)
				}
				if !plan.Runnable() {
					t.Error("Runnable() = false, want true: a skip must not block the plan")
				}

			case test.wantRefuse != "":
				if len(plan.Refused) != 1 || plan.Refused[0].Reason != test.wantRefuse {
					t.Fatalf("Refused = %+v, want one entry with reason %q", plan.Refused, test.wantRefuse)
				}
				if len(plan.Evict) != 0 || len(plan.Skipped) != 0 {
					t.Fatalf("plan = %+v, want nothing evicted or skipped", plan)
				}
				if plan.Runnable() {
					t.Error("Runnable() = true, want false: a refusal must block the plan")
				}
			}
		})
	}
}

// TestPlanDrainHealthyNodeEvictsEverything is the case named explicitly in
// the design: a node carrying only controller-owned pods must evict all of
// them and refuse none.
func TestPlanDrainHealthyNodeEvictsEverything(t *testing.T) {
	t.Parallel()

	candidates := []domain.DrainCandidate{
		{Pod: drainPod(t, "web-1", replicaSetOwner("web"))},
		{Pod: drainPod(t, "web-2", replicaSetOwner("web"))},
		{Pod: drainPod(t, "api-1", replicaSetOwner("api"))},
	}

	plan := domain.PlanDrain(candidates, domain.DrainOptions{})

	if len(plan.Evict) != 3 {
		t.Fatalf("Evict = %d pods, want 3: %+v", len(plan.Evict), plan.Evict)
	}
	if len(plan.Skipped) != 0 {
		t.Errorf("Skipped = %+v, want none", plan.Skipped)
	}
	if len(plan.Refused) != 0 {
		t.Errorf("Refused = %+v, want none", plan.Refused)
	}
	if !plan.Runnable() {
		t.Error("Runnable() = false, want true")
	}
}

// TestPlanDrainASingleRefusalBlocksTheWholePlan proves the plan-wide veto: a
// mix of otherwise-evictable pods and one refusal must still come back
// un-runnable, mirroring kubectl refusing a drain outright rather than doing
// part of it.
func TestPlanDrainASingleRefusalBlocksTheWholePlan(t *testing.T) {
	t.Parallel()

	candidates := []domain.DrainCandidate{
		{Pod: drainPod(t, "web-1", replicaSetOwner("web"))},
		{Pod: drainPod(t, "web-2", replicaSetOwner("web"))},
		{Pod: drainPod(t, "standalone", domain.OwnerReference{})}, // bare, no Force
	}

	plan := domain.PlanDrain(candidates, domain.DrainOptions{})

	if plan.Runnable() {
		t.Fatal("Runnable() = true, want false: one bare pod without Force must veto the plan")
	}
	if len(plan.Refused) != 1 {
		t.Errorf("Refused = %+v, want exactly 1", plan.Refused)
	}
	// The pods that WOULD be fine still show up as evictable in the plan —
	// Runnable is what gates whether the caller may act on it, not whether
	// the plan says anything about them.
	if len(plan.Evict) != 2 {
		t.Errorf("Evict = %+v, want 2", plan.Evict)
	}
}

func TestDrainPlanRunnable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		plan domain.DrainPlan
		want bool
	}{
		{name: "empty plan", plan: domain.DrainPlan{}, want: true},
		{
			name: "evicts and skips only",
			plan: domain.DrainPlan{
				Evict:   []domain.Pod{drainPod(t, "a", replicaSetOwner("a"))},
				Skipped: []domain.DrainSkip{{Pod: drainPod(t, "b", daemonSetOwner("b")), Reason: domain.DrainReasonDaemonSetPod}},
			},
			want: true,
		},
		{
			name: "any refusal blocks it",
			plan: domain.DrainPlan{
				Refused: []domain.DrainRefusal{{Pod: drainPod(t, "c", domain.OwnerReference{}), Reason: domain.DrainReasonBarePod}},
			},
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := test.plan.Runnable(); got != test.want {
				t.Errorf("Runnable() = %v, want %v", got, test.want)
			}
		})
	}
}
