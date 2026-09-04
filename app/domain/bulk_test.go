package domain_test

import (
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

// bulkRef names one object of kind in namespace ns — the shape every bulk
// candidate starts from. Group and version are left blank: PlanBulk decides
// on the Kind alone, and a test that fills the rest in is asserting nothing
// extra.
func bulkRef(kind, ns, name string) domain.ResourceRef {
	return domain.ResourceRef{
		ClusterID: "dev",
		Kind:      domain.ResourceKind{Kind: kind},
		Namespace: domain.NamespaceName(ns),
		Name:      name,
	}
}

func bulkController(kind, name string) domain.OwnerReference {
	return domain.OwnerReference{Kind: kind, Name: name, Controller: true}
}

func TestPlanBulkRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		candidate  domain.BulkCandidate
		opts       domain.BulkOptions
		wantAct    bool
		wantReason string
		wantNote   string
	}{
		// --- delete -----------------------------------------------------------
		{
			name:      "deleting a ReplicaSet-owned pod says its controller recreates it",
			candidate: domain.BulkCandidate{Ref: bulkRef("Pod", "web", "web-abc12"), Controller: bulkController("ReplicaSet", "web-abc")},
			opts:      domain.BulkOptions{Action: domain.BulkActionDelete},
			wantAct:   true,
			wantNote:  "owned by ReplicaSet/web-abc, which will recreate it",
		},
		{
			name:      "deleting a StatefulSet-owned pod says its controller recreates it",
			candidate: domain.BulkCandidate{Ref: bulkRef("Pod", "data", "db-0"), Controller: bulkController("StatefulSet", "db")},
			opts:      domain.BulkOptions{Action: domain.BulkActionDelete},
			wantAct:   true,
			wantNote:  "owned by StatefulSet/db, which will recreate it",
		},
		{
			name:      "deleting a bare pod carries no note — nothing will act",
			candidate: domain.BulkCandidate{Ref: bulkRef("Pod", "web", "standalone")},
			opts:      domain.BulkOptions{Action: domain.BulkActionDelete},
			wantAct:   true,
		},
		{
			name:      "deleting a CronJob's Job says it is not recreated",
			candidate: domain.BulkCandidate{Ref: bulkRef("Job", "batch", "nightly-1"), Controller: bulkController("CronJob", "nightly")},
			opts:      domain.BulkOptions{Action: domain.BulkActionDelete},
			wantAct:   true,
			wantNote:  "owned by CronJob/nightly; not recreated — its next run is a new Job",
		},
		{
			name:      "deleting an object owned by an operator's kind only says it may be recreated",
			candidate: domain.BulkCandidate{Ref: bulkRef("Pod", "db", "pg-1"), Controller: bulkController("Cluster", "pg")},
			opts:      domain.BulkOptions{Action: domain.BulkActionDelete},
			wantAct:   true,
			wantNote:  "owned by Cluster/pg, which may recreate it",
		},
		{
			name:      "deleting a generic-table row with no owner facts carries no note",
			candidate: domain.BulkCandidate{Ref: bulkRef("ConfigMap", "web", "settings")},
			opts:      domain.BulkOptions{Action: domain.BulkActionDelete},
			wantAct:   true,
		},

		// --- restart ----------------------------------------------------------
		{
			name:      "a Deployment restarts",
			candidate: domain.BulkCandidate{Ref: bulkRef("Deployment", "web", "api")},
			opts:      domain.BulkOptions{Action: domain.BulkActionRestart},
			wantAct:   true,
		},
		{
			name:      "a StatefulSet restarts",
			candidate: domain.BulkCandidate{Ref: bulkRef("StatefulSet", "data", "db")},
			opts:      domain.BulkOptions{Action: domain.BulkActionRestart},
			wantAct:   true,
		},
		{
			name:      "a DaemonSet restarts",
			candidate: domain.BulkCandidate{Ref: bulkRef("DaemonSet", "kube-system", "agent")},
			opts:      domain.BulkOptions{Action: domain.BulkActionRestart},
			wantAct:   true,
		},
		{
			name:       "a Job cannot be restarted",
			candidate:  domain.BulkCandidate{Ref: bulkRef("Job", "batch", "nightly-1")},
			opts:       domain.BulkOptions{Action: domain.BulkActionRestart},
			wantReason: "a Job runs to completion; there is no rollout to restart",
		},
		{
			name:       "a CronJob cannot be restarted",
			candidate:  domain.BulkCandidate{Ref: bulkRef("CronJob", "batch", "nightly")},
			opts:       domain.BulkOptions{Action: domain.BulkActionRestart},
			wantReason: "a CronJob has no pods of its own; its next run is a new Job",
		},
		{
			name:       "a ReplicaSet cannot be restarted",
			candidate:  domain.BulkCandidate{Ref: bulkRef("ReplicaSet", "web", "api-abc")},
			opts:       domain.BulkOptions{Action: domain.BulkActionRestart},
			wantReason: "a ReplicaSet has no rollout; restart the Deployment that owns it",
		},
		{
			name:       "a pod cannot be restarted",
			candidate:  domain.BulkCandidate{Ref: bulkRef("Pod", "web", "api-abc12")},
			opts:       domain.BulkOptions{Action: domain.BulkActionRestart},
			wantReason: "a pod cannot be restarted; delete it and its controller replaces it",
		},

		// --- scale ------------------------------------------------------------
		{
			name:      "a Deployment scales, noting where from and to",
			candidate: domain.BulkCandidate{Ref: bulkRef("Deployment", "web", "api"), Replicas: 3},
			opts:      domain.BulkOptions{Action: domain.BulkActionScale, Replicas: 5},
			wantAct:   true,
			wantNote:  "3 → 5 replicas",
		},
		{
			name:      "a ReplicaSet scales",
			candidate: domain.BulkCandidate{Ref: bulkRef("ReplicaSet", "web", "api-abc"), Replicas: 2},
			opts:      domain.BulkOptions{Action: domain.BulkActionScale, Replicas: 0},
			wantAct:   true,
			wantNote:  "2 → 0 replicas",
		},
		{
			name:      "scaling to one replica reads in the singular",
			candidate: domain.BulkCandidate{Ref: bulkRef("StatefulSet", "data", "db"), Replicas: 3},
			opts:      domain.BulkOptions{Action: domain.BulkActionScale, Replicas: 1},
			wantAct:   true,
			wantNote:  "3 → 1 replica",
		},
		{
			name:       "a workload already at the target count is skipped",
			candidate:  domain.BulkCandidate{Ref: bulkRef("Deployment", "web", "api"), Replicas: 3},
			opts:       domain.BulkOptions{Action: domain.BulkActionScale, Replicas: 3},
			wantReason: "already at 3 replicas",
		},
		{
			name:       "a DaemonSet has no replica count",
			candidate:  domain.BulkCandidate{Ref: bulkRef("DaemonSet", "kube-system", "agent")},
			opts:       domain.BulkOptions{Action: domain.BulkActionScale, Replicas: 2},
			wantReason: "a DaemonSet runs one pod per node; it has no replica count",
		},
		{
			name:       "a Job has no replica count",
			candidate:  domain.BulkCandidate{Ref: bulkRef("Job", "batch", "nightly-1")},
			opts:       domain.BulkOptions{Action: domain.BulkActionScale, Replicas: 2},
			wantReason: "a Job has no replica count",
		},

		// --- cordon / uncordon ------------------------------------------------
		{
			name:      "a schedulable node cordons",
			candidate: domain.BulkCandidate{Ref: bulkRef("Node", "", "node-1")},
			opts:      domain.BulkOptions{Action: domain.BulkActionCordon},
			wantAct:   true,
		},
		{
			name:       "a cordoned node is not cordoned again",
			candidate:  domain.BulkCandidate{Ref: bulkRef("Node", "", "node-1"), Unschedulable: true},
			opts:       domain.BulkOptions{Action: domain.BulkActionCordon},
			wantReason: domain.BulkReasonAlreadyCordoned,
		},
		{
			name:      "a cordoned node uncordons",
			candidate: domain.BulkCandidate{Ref: bulkRef("Node", "", "node-1"), Unschedulable: true},
			opts:      domain.BulkOptions{Action: domain.BulkActionUncordon},
			wantAct:   true,
		},
		{
			name:       "a schedulable node is not uncordoned",
			candidate:  domain.BulkCandidate{Ref: bulkRef("Node", "", "node-1")},
			opts:       domain.BulkOptions{Action: domain.BulkActionUncordon},
			wantReason: domain.BulkReasonAlreadySchedulable,
		},
		{
			name:       "only a node can be cordoned",
			candidate:  domain.BulkCandidate{Ref: bulkRef("Pod", "web", "api-abc12")},
			opts:       domain.BulkOptions{Action: domain.BulkActionCordon},
			wantReason: "only a node can be cordoned; this is a Pod",
		},

		// --- read-only --------------------------------------------------------
		{
			name:       "a read-only cluster refuses a supported write",
			candidate:  domain.BulkCandidate{Ref: bulkRef("Deployment", "web", "api")},
			opts:       domain.BulkOptions{Action: domain.BulkActionRestart, ReadOnly: true},
			wantReason: domain.BulkReasonReadOnly,
		},
		{
			name: "read-only is the reason even where the kind would have been",
			// A Job cannot be restarted either way; on a read-only cluster the
			// line must say read-only, because that is the one rule the
			// operator can change and the review should point them at it.
			candidate:  domain.BulkCandidate{Ref: bulkRef("Job", "batch", "nightly-1")},
			opts:       domain.BulkOptions{Action: domain.BulkActionRestart, ReadOnly: true},
			wantReason: domain.BulkReasonReadOnly,
		},
		{
			name:       "a read-only cluster refuses a delete",
			candidate:  domain.BulkCandidate{Ref: bulkRef("Pod", "web", "api-abc12"), Controller: bulkController("ReplicaSet", "api-abc")},
			opts:       domain.BulkOptions{Action: domain.BulkActionDelete, ReadOnly: true},
			wantReason: domain.BulkReasonReadOnly,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			plan := domain.PlanBulk([]domain.BulkCandidate{test.candidate}, test.opts)
			if len(plan.Lines) != 1 {
				t.Fatalf("Lines = %d, want exactly one per candidate", len(plan.Lines))
			}
			line := plan.Lines[0]

			if line.Ref != test.candidate.Ref {
				t.Errorf("Ref = %+v, want the candidate's own %+v", line.Ref, test.candidate.Ref)
			}
			if line.Act != test.wantAct {
				t.Errorf("Act = %v, want %v (reason %q)", line.Act, test.wantAct, line.Reason)
			}
			if line.Reason != test.wantReason {
				t.Errorf("Reason = %q, want %q", line.Reason, test.wantReason)
			}
			if line.Note != test.wantNote {
				t.Errorf("Note = %q, want %q", line.Note, test.wantNote)
			}
			// An acting line explains nothing away; a skipped line adds no
			// note. Either combination would read as a contradiction.
			if line.Act && line.Reason != "" {
				t.Errorf("an acting line carries a skip reason %q", line.Reason)
			}
			if !line.Act && line.Note != "" {
				t.Errorf("a skipped line carries a note %q", line.Note)
			}
		})
	}
}

// TestPlanBulkKeepsTheSelectionOrder pins what the review dialog depends on:
// one line per candidate, in the order the operator selected them, whatever
// mix of verdicts they get — so the dialog lists the selection as it was
// made and the counts add up to it.
func TestPlanBulkKeepsTheSelectionOrder(t *testing.T) {
	t.Parallel()

	candidates := []domain.BulkCandidate{
		{Ref: bulkRef("Deployment", "web", "api")},
		{Ref: bulkRef("Job", "web", "migrate")},
		{Ref: bulkRef("StatefulSet", "web", "cache")},
		{Ref: bulkRef("CronJob", "web", "report")},
	}

	plan := domain.PlanBulk(candidates, domain.BulkOptions{Action: domain.BulkActionRestart})

	if plan.Action != domain.BulkActionRestart {
		t.Errorf("Action = %q, want %q", plan.Action, domain.BulkActionRestart)
	}
	if len(plan.Lines) != len(candidates) {
		t.Fatalf("Lines = %d, want %d", len(plan.Lines), len(candidates))
	}
	for i, line := range plan.Lines {
		if line.Ref != candidates[i].Ref {
			t.Errorf("Lines[%d].Ref = %+v, want %+v", i, line.Ref, candidates[i].Ref)
		}
	}

	acting := plan.Acting()
	if len(acting) != 2 || acting[0].Ref.Name != "api" || acting[1].Ref.Name != "cache" {
		t.Errorf("Acting() = %+v, want api and cache in that order", acting)
	}
	if plan.Skipped() != 2 {
		t.Errorf("Skipped() = %d, want 2", plan.Skipped())
	}
}

// TestPlanBulkEmptySelectionIsAnEmptyPlan: nothing selected, nothing planned
// — and no nil slice that a caller then ranges over differently.
func TestPlanBulkEmptySelectionIsAnEmptyPlan(t *testing.T) {
	t.Parallel()

	plan := domain.PlanBulk(nil, domain.BulkOptions{Action: domain.BulkActionDelete})
	if plan.Lines == nil || len(plan.Lines) != 0 {
		t.Fatalf("Lines = %#v, want an empty, non-nil slice", plan.Lines)
	}
	if len(plan.Acting()) != 0 || plan.Skipped() != 0 {
		t.Errorf("Acting()/Skipped() = %d/%d, want 0/0", len(plan.Acting()), plan.Skipped())
	}
}
