package application_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// bulkCandidate builds one selected object of kind, the way the Wails layer
// hands them in: a ref naming the cluster, and whatever facts the row had.
func bulkCandidate(kind, namespace, name string) domain.BulkCandidate {
	return domain.BulkCandidate{
		Ref: domain.ResourceRef{
			ClusterID: "dev",
			Kind:      domain.ResourceKind{Kind: kind},
			Namespace: domain.NamespaceName(namespace),
			Name:      name,
		},
	}
}

// names lists the results' object names in order, for a one-line assertion.
func names(results []application.BulkResult) []string {
	out := make([]string, 0, len(results))
	for _, result := range results {
		out = append(out, result.Ref.Name)
	}
	return out
}

// TestBulkDeleteReportsEveryObjectAndNeverStopsOnTheFirstFailure is the
// property the bulk methods exist for: one forbidden delete is recorded
// against its own object, and the others still run. A run that aborted on
// the first failure would leave the operator unable to tell which of the
// remaining rows were touched.
func TestBulkDeleteReportsEveryObjectAndNeverStopsOnTheFirstFailure(t *testing.T) {
	t.Parallel()

	port := &fakeManagementPort{
		bulkFailFor: map[string]error{"web-1": fmt.Errorf("deleting: %w", ports.ErrForbidden)},
	}
	service := newManagementService(t, port, application.NewRegistry())

	candidates := []domain.BulkCandidate{
		bulkCandidate("Pod", "web", "web-1"),
		bulkCandidate("Pod", "web", "web-2"),
		bulkCandidate("Pod", "web", "web-3"),
	}

	results, err := service.BulkDelete(context.Background(), "dev", candidates)
	if err != nil {
		t.Fatalf("BulkDelete() error = %v, want nil — a per-object failure is a result, not an error", err)
	}
	if got := names(results); strings.Join(got, ",") != "web-1,web-2,web-3" {
		t.Fatalf("results = %v, want one per candidate in the candidates' order", got)
	}

	if !errors.Is(results[0].Err, ports.ErrForbidden) {
		t.Errorf("results[0].Err = %v, want wrapping ports.ErrForbidden", results[0].Err)
	}
	for _, result := range results[1:] {
		if result.Err != nil || result.Skipped {
			t.Errorf("%s: Err = %v, Skipped = %v, want a clean success", result.Ref.Name, result.Err, result.Skipped)
		}
	}

	if got := port.bulkAttempted(); strings.Join(got, ",") != "web-1,web-2,web-3" {
		t.Errorf("port attempted %v, want every object regardless of the first failing", got)
	}
}

// TestBulkDeleteBoundsConcurrency observes the limit rather than trusting
// the constant: twelve slow deletes must never have more than four in flight,
// and must have more than one, or the group is not fanning out at all.
func TestBulkDeleteBoundsConcurrency(t *testing.T) {
	t.Parallel()

	port := &fakeManagementPort{bulkDelay: 10 * time.Millisecond}
	service := newManagementService(t, port, application.NewRegistry())

	candidates := make([]domain.BulkCandidate, 0, 12)
	for i := range 12 {
		candidates = append(candidates, bulkCandidate("Pod", "web", fmt.Sprintf("web-%d", i)))
	}

	results, err := service.BulkDelete(context.Background(), "dev", candidates)
	if err != nil {
		t.Fatalf("BulkDelete() error = %v", err)
	}
	if len(results) != 12 {
		t.Fatalf("results = %d, want 12", len(results))
	}

	if peak := port.maxInFlight(); peak > 4 {
		t.Errorf("in-flight writes peaked at %d, want at most 4", peak)
	} else if peak < 2 {
		t.Errorf("in-flight writes peaked at %d, want at least 2 — the writes ran one at a time", peak)
	}
}

// TestBulkActionsRefuseAReadOnlyClusterUpFront: one refusal for the whole
// selection, before anything is planned, and the port never sees a write.
func TestBulkActionsRefuseAReadOnlyClusterUpFront(t *testing.T) {
	t.Parallel()

	const id domain.ClusterID = "prod"
	registry := application.NewRegistry()
	registry.SetReadOnly(id, true)

	port := &fakeManagementPort{}
	service := newManagementService(t, port, registry)

	ctx := context.Background()
	candidates := []domain.BulkCandidate{
		bulkCandidate("Deployment", "web", "api"),
		{Ref: domain.ResourceRef{ClusterID: id, Kind: domain.ResourceKind{Kind: "Node"}, Name: "node-1"}},
	}

	cases := []struct {
		name string
		call func() ([]application.BulkResult, error)
	}{
		{"BulkDelete", func() ([]application.BulkResult, error) { return service.BulkDelete(ctx, id, candidates) }},
		{"BulkRestart", func() ([]application.BulkResult, error) { return service.BulkRestart(ctx, id, candidates) }},
		{"BulkScale", func() ([]application.BulkResult, error) { return service.BulkScale(ctx, id, candidates, 2) }},
		{"BulkCordon", func() ([]application.BulkResult, error) { return service.BulkCordon(ctx, id, candidates, true) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results, err := tc.call()
			if !errors.Is(err, ports.ErrReadOnly) {
				t.Fatalf("%s() error = %v, want wrapping ports.ErrReadOnly", tc.name, err)
			}
			if results != nil {
				t.Errorf("%s() results = %v, want none — nothing was planned", tc.name, results)
			}
		})
	}

	if calls := port.recordedCalls(); len(calls) != 0 {
		t.Fatalf("port recorded calls %v, want none — a refused bulk action must never reach the adapter", calls)
	}
	if attempted := port.bulkAttempted(); len(attempted) != 0 {
		t.Fatalf("port attempted %v, want nothing", attempted)
	}
}

// TestBulkRestartSkipsWhatThePlanSkipsWithoutReachingThePort: the plan the
// review showed is the plan that runs, so a Job the review marked as skipped
// is skipped here for the same reason and costs no round trip.
func TestBulkRestartSkipsWhatThePlanSkipsWithoutReachingThePort(t *testing.T) {
	t.Parallel()

	port := &fakeManagementPort{}
	service := newManagementService(t, port, application.NewRegistry())

	candidates := []domain.BulkCandidate{
		bulkCandidate("Deployment", "web", "api"),
		bulkCandidate("Job", "web", "migrate"),
	}

	results, err := service.BulkRestart(context.Background(), "dev", candidates)
	if err != nil {
		t.Fatalf("BulkRestart() error = %v", err)
	}

	if results[0].Skipped || results[0].Err != nil {
		t.Errorf("api: Skipped = %v, Err = %v, want restarted", results[0].Skipped, results[0].Err)
	}
	if !results[1].Skipped || !strings.Contains(results[1].Reason, "Job") {
		t.Errorf("migrate: Skipped = %v, Reason = %q, want skipped with the Job reason", results[1].Skipped, results[1].Reason)
	}

	if got := port.bulkAttempted(); strings.Join(got, ",") != "api" {
		t.Errorf("port attempted %v, want only api", got)
	}
	if calls := port.recordedCalls(); len(calls) != 1 || calls[0] != "RestartRollout" {
		t.Errorf("port recorded %v, want exactly one RestartRollout", calls)
	}
}

func TestBulkScaleRejectsANegativeCountBeforeThePort(t *testing.T) {
	t.Parallel()

	port := &fakeManagementPort{}
	service := newManagementService(t, port, application.NewRegistry())

	_, err := service.BulkScale(context.Background(), "dev", []domain.BulkCandidate{bulkCandidate("Deployment", "web", "api")}, -1)
	if err == nil {
		t.Fatal("BulkScale(-1) succeeded, want an error")
	}
	if calls := port.recordedCalls(); len(calls) != 0 {
		t.Errorf("port recorded %v, want none", calls)
	}
}

// TestBulkScaleSkipsAWorkloadAlreadyAtTheTarget pins the "would change
// nothing" rule through the service: the plan's note and reason both reach
// the results, so the panel can show them without a second lookup.
func TestBulkScaleSkipsAWorkloadAlreadyAtTheTarget(t *testing.T) {
	t.Parallel()

	port := &fakeManagementPort{}
	service := newManagementService(t, port, application.NewRegistry())

	at3 := bulkCandidate("Deployment", "web", "api")
	at3.Replicas = 3
	at1 := bulkCandidate("Deployment", "web", "worker")
	at1.Replicas = 1

	results, err := service.BulkScale(context.Background(), "dev", []domain.BulkCandidate{at3, at1}, 3)
	if err != nil {
		t.Fatalf("BulkScale() error = %v", err)
	}

	if !results[0].Skipped || results[0].Reason != "already at 3 replicas" {
		t.Errorf("api: Skipped = %v, Reason = %q, want skipped as already at 3", results[0].Skipped, results[0].Reason)
	}
	if results[1].Skipped || results[1].Note != "1 → 3 replicas" {
		t.Errorf("worker: Skipped = %v, Note = %q, want scaled with the from/to note", results[1].Skipped, results[1].Note)
	}
	if got := port.bulkAttempted(); strings.Join(got, ",") != "worker" {
		t.Errorf("port attempted %v, want only worker", got)
	}
}

// TestBulkCordonPassesTheDirectionThrough: cordon false is an uncordon, and
// the plan reads the node's current state against that direction.
func TestBulkCordonPassesTheDirectionThrough(t *testing.T) {
	t.Parallel()

	port := &fakeManagementPort{}
	service := newManagementService(t, port, application.NewRegistry())

	cordoned := domain.BulkCandidate{
		Ref:           domain.ResourceRef{ClusterID: "dev", Kind: domain.ResourceKind{Kind: "Node"}, Name: "node-1"},
		Unschedulable: true,
	}
	schedulable := domain.BulkCandidate{
		Ref: domain.ResourceRef{ClusterID: "dev", Kind: domain.ResourceKind{Kind: "Node"}, Name: "node-2"},
	}

	results, err := service.BulkCordon(context.Background(), "dev", []domain.BulkCandidate{cordoned, schedulable}, false)
	if err != nil {
		t.Fatalf("BulkCordon() error = %v", err)
	}

	if results[0].Skipped || results[0].Err != nil {
		t.Errorf("node-1: Skipped = %v, Err = %v, want uncordoned", results[0].Skipped, results[0].Err)
	}
	if !results[1].Skipped || results[1].Reason != domain.BulkReasonAlreadySchedulable {
		t.Errorf("node-2: Skipped = %v, Reason = %q, want skipped as already schedulable", results[1].Skipped, results[1].Reason)
	}

	port.mu.Lock()
	defer port.mu.Unlock()
	if !port.cordonCalled || port.cordonedName != "node-1" || port.cordonedValue {
		t.Errorf("port cordon call = (%v, %q, cordon=%v), want node-1 with cordon=false", port.cordonCalled, port.cordonedName, port.cordonedValue)
	}
}

// TestBulkResultsCarryThePlansNoteAndPinTheCluster: the owner note the review
// showed travels with the result, and a ref that arrived naming another
// cluster leaves naming this one.
func TestBulkResultsCarryThePlansNoteAndPinTheCluster(t *testing.T) {
	t.Parallel()

	port := &fakeManagementPort{}
	service := newManagementService(t, port, application.NewRegistry())

	owned := bulkCandidate("Pod", "web", "web-abc12")
	owned.Ref.ClusterID = "somewhere-else"
	owned.Controller = domain.OwnerReference{Kind: "ReplicaSet", Name: "web-abc", Controller: true}

	results, err := service.BulkDelete(context.Background(), "dev", []domain.BulkCandidate{owned})
	if err != nil {
		t.Fatalf("BulkDelete() error = %v", err)
	}

	if results[0].Note != "owned by ReplicaSet/web-abc, which will recreate it" {
		t.Errorf("Note = %q, want the plan's recreation note", results[0].Note)
	}
	if results[0].Ref.ClusterID != "dev" {
		t.Errorf("Ref.ClusterID = %q, want pinned to %q", results[0].Ref.ClusterID, "dev")
	}
}
