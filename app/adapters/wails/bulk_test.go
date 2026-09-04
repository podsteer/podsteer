package wails

import (
	"fmt"
	"testing"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// TestToBulkCandidatesCarriesTheRowsFacts pins the wire contract the review
// dialog depends on: every fact the row handed back reaches the plan, the
// ref is pinned to the cluster the call named, and a row with no owner
// produces a ZERO controller rather than one with a blank name.
func TestToBulkCandidatesCarriesTheRowsFacts(t *testing.T) {
	t.Parallel()

	items := []BulkItemDTO{
		{
			Group: "", Version: "v1", Kind: "Pod", Namespace: "web", Name: "web-abc12",
			ControllerKind: "ReplicaSet", ControllerName: "web-abc",
		},
		{
			Group: "apps", Version: "v1", Kind: "Deployment", Namespace: "web", Name: "api",
			Replicas: 3,
		},
		{Version: "v1", Kind: "Node", Name: "node-1", Unschedulable: true},
	}

	candidates, err := toBulkCandidates("dev", items)
	if err != nil {
		t.Fatalf("toBulkCandidates() error = %v", err)
	}
	if len(candidates) != 3 {
		t.Fatalf("candidates = %d, want 3", len(candidates))
	}

	pod := candidates[0]
	if pod.Ref.ClusterID != "dev" || pod.Ref.Kind.Kind != "Pod" || pod.Ref.Namespace != "web" || pod.Ref.Name != "web-abc12" {
		t.Errorf("pod ref = %+v, want dev/Pod/web/web-abc12", pod.Ref)
	}
	if !pod.Controller.Controller || pod.Controller.Kind != "ReplicaSet" || pod.Controller.Name != "web-abc" {
		t.Errorf("pod controller = %+v, want the controlling ReplicaSet/web-abc", pod.Controller)
	}

	deployment := candidates[1]
	if deployment.Ref.Kind.Group != "apps" || deployment.Replicas != 3 {
		t.Errorf("deployment = %+v, want group apps and 3 replicas", deployment)
	}
	if !deployment.Controller.IsZero() {
		t.Errorf("deployment controller = %+v, want zero for a row with no owner", deployment.Controller)
	}

	node := candidates[2]
	if !node.Ref.Namespace.IsAll() || !node.Unschedulable {
		t.Errorf("node = %+v, want cluster-scoped and cordoned", node)
	}
}

func TestToBulkCandidatesRefusesARowWithNoName(t *testing.T) {
	t.Parallel()

	_, err := toBulkCandidates("dev", []BulkItemDTO{{Kind: "Pod", Namespace: "web", Name: "  "}})
	if err == nil {
		t.Fatal("toBulkCandidates() succeeded for a nameless row, want an error")
	}
	if code, _ := classifyError(err); code != CodeInvalidInput {
		t.Errorf("classifyError() code = %q, want %q", code, CodeInvalidInput)
	}
}

// TestToBulkResultsClassifiesEachFailureLikeASingleWrite: a forbidden delete
// inside a bulk delete carries the same code and the same sentence a single
// forbidden delete's rejected promise would, and a success or a skip carries
// no code at all.
func TestToBulkResultsClassifiesEachFailureLikeASingleWrite(t *testing.T) {
	t.Parallel()

	ref := func(name string) domain.ResourceRef {
		return domain.ResourceRef{ClusterID: "dev", Kind: domain.ResourceKind{Kind: "Pod"}, Namespace: "web", Name: name}
	}
	forbidden := fmt.Errorf("deleting resource: %w", ports.ErrForbidden)
	wantCode, wantMessage := classifyError(forbidden)

	results := toBulkResults([]application.BulkResult{
		{Ref: ref("done"), Note: "owned by ReplicaSet/web, which will recreate it"},
		{Ref: ref("failed"), Err: forbidden},
		{Ref: ref("skipped"), Skipped: true, Reason: domain.BulkReasonReadOnly},
	})
	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}

	done := results[0]
	if !done.Done || done.Skipped || done.Code != "" || done.Reason != "" {
		t.Errorf("done = %+v, want Done with no code or reason", done)
	}
	if done.Note != "owned by ReplicaSet/web, which will recreate it" {
		t.Errorf("done.Note = %q, want the plan's note carried through", done.Note)
	}

	failed := results[1]
	if failed.Done || failed.Skipped {
		t.Errorf("failed = %+v, want neither Done nor Skipped", failed)
	}
	if failed.Code != string(wantCode) || failed.Reason != wantMessage {
		t.Errorf("failed code/reason = %q/%q, want %q/%q — the same classification a single write gets", failed.Code, failed.Reason, wantCode, wantMessage)
	}

	skipped := results[2]
	if skipped.Done || !skipped.Skipped || skipped.Code != "" || skipped.Reason != domain.BulkReasonReadOnly {
		t.Errorf("skipped = %+v, want Skipped with the plan's reason and no code", skipped)
	}
}

func TestToBulkPlanCountsActingAndSkipped(t *testing.T) {
	t.Parallel()

	plan := domain.PlanBulk([]domain.BulkCandidate{
		{Ref: domain.ResourceRef{ClusterID: "dev", Kind: domain.ResourceKind{Kind: "Deployment"}, Namespace: "web", Name: "api"}},
		{Ref: domain.ResourceRef{ClusterID: "dev", Kind: domain.ResourceKind{Kind: "Job"}, Namespace: "web", Name: "migrate"}},
	}, domain.BulkOptions{Action: domain.BulkActionRestart})

	dto := toBulkPlan(plan)
	if dto.Action != "restart" || dto.Acting != 1 || dto.Skipped != 1 || len(dto.Lines) != 2 {
		t.Fatalf("toBulkPlan() = %+v, want action restart, 1 acting, 1 skipped, 2 lines", dto)
	}
	if !dto.Lines[0].Act || dto.Lines[0].Name != "api" {
		t.Errorf("Lines[0] = %+v, want api acting", dto.Lines[0])
	}
	if dto.Lines[1].Act || dto.Lines[1].Reason == "" {
		t.Errorf("Lines[1] = %+v, want migrate skipped with a reason", dto.Lines[1])
	}
}

func TestParseBulkActionRefusesAnUnknownVerb(t *testing.T) {
	t.Parallel()

	for _, action := range []string{"delete", "restart", "scale", "cordon", "uncordon"} {
		if _, err := parseBulkAction(action); err != nil {
			t.Errorf("parseBulkAction(%q) error = %v, want nil", action, err)
		}
	}

	_, err := parseBulkAction("evict")
	if err == nil {
		t.Fatal("parseBulkAction(evict) succeeded, want an error")
	}
	if code, _ := classifyError(err); code != CodeInvalidInput {
		t.Errorf("classifyError() code = %q, want %q", code, CodeInvalidInput)
	}
}
