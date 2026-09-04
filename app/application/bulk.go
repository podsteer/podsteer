package application

import (
	"context"
	"fmt"
	"log/slog"

	"golang.org/x/sync/errgroup"

	"github.com/podsteer/podsteer/app/domain"
)

// BulkResult is what happened to one object of a bulk action.
//
// Every candidate produces exactly one, in the candidates' own order, whether
// the plan skipped it, the write succeeded, or the write failed — the same
// place domain.DrainFailure has in a DrainReport: an outcome per object,
// shown exactly as it happened, never collapsed into one error for the lot.
type BulkResult struct {
	// Ref names the object, pinned to the cluster the call named.
	Ref domain.ResourceRef
	// Skipped reports that the plan left the object alone; Reason says why.
	Skipped bool
	// Reason is the plan's reason for skipping. Empty when the write ran.
	Reason string
	// Note carries the plan line's note through unchanged, so the results
	// panel can say "deleted — owned by ReplicaSet/web, which will recreate
	// it" without asking the plan a second time.
	Note string
	// Err is the write's failure, nil when it succeeded or was skipped.
	//
	// The wrapped error itself rather than its text, so the Wails layer can
	// classify it exactly as it classifies a single write's — a forbidden
	// delete inside a bulk delete is the same forbidden, and must reach the
	// operator with the same code and the same advice.
	Err error
}

// bulkConcurrency bounds how many of a bulk action's writes are in flight at
// once.
//
// Four, matching drainConcurrency in the k8s adapter and for the same
// reason: enough that a page of pods is not deleted one at a time, few
// enough that a selection of fifty cannot open fifty requests against an API
// server whose client-side rate limit would queue them anyway.
const bulkConcurrency = 4

// bulkWrite performs one action on one object — a single-object
// ManagementPort method, bound to whatever the action needs beyond the ref.
type bulkWrite func(ctx context.Context, ref domain.ResourceRef) error

// BulkDelete deletes every candidate the plan allows.
//
// Owner facts on the candidates decide the note each line carries — see
// domain.PlanBulk — never whether the delete runs: an operator deleting a
// controller-owned pod is told it will be recreated, and it is deleted.
func (s *ManagementService) BulkDelete(ctx context.Context, id domain.ClusterID, candidates []domain.BulkCandidate) ([]BulkResult, error) {
	return s.runBulk(ctx, id, candidates, domain.BulkOptions{Action: domain.BulkActionDelete},
		func(ctx context.Context, ref domain.ResourceRef) error {
			return s.management.DeleteResource(ctx, ref)
		})
}

// BulkRestart triggers a rolling restart of every Deployment, StatefulSet
// and DaemonSet among the candidates; the plan skips every other kind with
// a reason, so an unsupported kind never costs a round trip to be told no.
func (s *ManagementService) BulkRestart(ctx context.Context, id domain.ClusterID, candidates []domain.BulkCandidate) ([]BulkResult, error) {
	return s.runBulk(ctx, id, candidates, domain.BulkOptions{Action: domain.BulkActionRestart},
		func(ctx context.Context, ref domain.ResourceRef) error {
			return s.management.RestartRollout(ctx, id, domain.WorkloadKind(ref.Kind.Kind), ref.Namespace, ref.Name)
		})
}

// BulkScale sets one replica count on every Deployment, StatefulSet and
// ReplicaSet among the candidates. A negative count is refused before the
// plan is built, mirroring ScaleWorkload's own check.
func (s *ManagementService) BulkScale(ctx context.Context, id domain.ClusterID, candidates []domain.BulkCandidate, replicas int32) ([]BulkResult, error) {
	if replicas < 0 {
		return nil, fmt.Errorf("replicas must be non-negative, got %d", replicas)
	}

	return s.runBulk(ctx, id, candidates, domain.BulkOptions{Action: domain.BulkActionScale, Replicas: replicas},
		func(ctx context.Context, ref domain.ResourceRef) error {
			return s.management.ScaleWorkload(ctx, id, domain.WorkloadKind(ref.Kind.Kind), ref.Namespace, ref.Name, replicas)
		})
}

// BulkCordon marks every node among the candidates unschedulable (cordon
// true) or schedulable again (cordon false). A node already in the asked-for
// state is skipped by the plan rather than patched to what it already is.
func (s *ManagementService) BulkCordon(ctx context.Context, id domain.ClusterID, candidates []domain.BulkCandidate, cordon bool) ([]BulkResult, error) {
	action := domain.BulkActionUncordon
	if cordon {
		action = domain.BulkActionCordon
	}

	return s.runBulk(ctx, id, candidates, domain.BulkOptions{Action: action},
		func(ctx context.Context, ref domain.ResourceRef) error {
			return s.management.CordonNode(ctx, id, ref.Name, cordon)
		})
}

// runBulk plans the action with domain.PlanBulk — THE SAME FUNCTION the
// review dialog's preview went through, so what runs is what was shown —
// and fans the acting lines out over the single-object port methods.
//
// Two rules, each with a test:
//
//   - The read-only guard runs ONCE, up front, for the whole selection.
//     Fifty refusals for fifty pods would be fifty copies of the same
//     sentence; one refusal before anything is planned is the answer.
//
//   - A failure never stops the rest. The group below has no context to
//     cancel and every goroutine returns nil, because one forbidden delete
//     is an ordinary outcome the results record — abandoning the other
//     forty-nine over it would leave the operator unable to tell from the
//     list which of them were touched.
func (s *ManagementService) runBulk(ctx context.Context, id domain.ClusterID, candidates []domain.BulkCandidate, opts domain.BulkOptions, write bulkWrite) ([]BulkResult, error) {
	if err := s.refuseIfReadOnly(id); err != nil {
		return nil, err
	}

	// Every ref is pinned to the cluster this call named, on a copy rather
	// than the caller's slice. A candidate carrying another tab's cluster id
	// — a stale selection, a frontend bug — would otherwise send a write to
	// a cluster the operator is not looking at.
	pinned := make([]domain.BulkCandidate, len(candidates))
	for i, candidate := range candidates {
		candidate.Ref.ClusterID = id
		pinned[i] = candidate
	}

	plan := domain.PlanBulk(pinned, opts)
	acting := plan.Acting()

	// Counts only, never the object names: a summary line per bulk action is
	// what a log reader needs, and the per-object failures below name what
	// they must.
	s.logger.InfoContext(ctx, "running bulk action",
		slog.String("cluster", id.String()),
		slog.String("action", string(opts.Action)),
		slog.Int("selected", len(plan.Lines)),
		slog.Int("acting", len(acting)),
		slog.Int("skipped", plan.Skipped()))

	results := make([]BulkResult, len(plan.Lines))

	// Not errgroup.WithContext: a shared context would be cancelled by the
	// first goroutine to return an error, and none of them ever does — see
	// the doc comment above. SetLimit is the only thing the group is for.
	var group errgroup.Group
	group.SetLimit(bulkConcurrency)

	for i, line := range plan.Lines {
		results[i] = BulkResult{Ref: line.Ref, Skipped: !line.Act, Reason: line.Reason, Note: line.Note}
		if !line.Act {
			continue
		}

		group.Go(func() error {
			// Each goroutine writes its own index and nothing else; Wait
			// below is what makes those writes visible to the reader.
			if err := write(ctx, line.Ref); err != nil {
				results[i].Err = err
				s.logger.ErrorContext(ctx, "bulk write failed",
					slog.String("cluster", id.String()),
					slog.String("action", string(opts.Action)),
					slog.String("kind", line.Ref.Kind.Kind),
					slog.String("namespace", line.Ref.Namespace.String()),
					slog.String("name", line.Ref.Name),
					slog.String("error", err.Error()))
			}
			return nil
		})
	}
	// Never returns an error — every goroutine returns nil — so there is
	// nothing to check.
	_ = group.Wait()

	failed := 0
	for _, result := range results {
		if result.Err != nil {
			failed++
		}
	}
	s.logger.InfoContext(ctx, "bulk action finished",
		slog.String("cluster", id.String()),
		slog.String("action", string(opts.Action)),
		slog.Int("done", len(acting)-failed),
		slog.Int("failed", failed),
		slog.Int("skipped", plan.Skipped()))

	return results, nil
}
