package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// RollbackWorkload rolls a Deployment, StatefulSet or DaemonSet back to a
// previously recorded revision. See ports.ManagementPort.RollbackWorkload
// for the full contract.
func (a *Adapter) RollbackWorkload(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName, name string, toRevision int64, dryRun bool) (domain.RollbackOutcome, error) {
	// A dry run persists nothing, so the cached reads it might otherwise
	// invalidate are still the truth once it returns — the same reasoning
	// UpdateResource's own dry run follows.
	if !dryRun {
		defer a.forgetReads(id)
	}

	client, err := a.factory.clientFor(id)
	if err != nil {
		return domain.RollbackOutcome{}, err
	}
	ns := namespace.String()

	patchOpts := metav1.PatchOptions{}
	if dryRun {
		patchOpts.DryRun = []string{metav1.DryRunAll}
	}

	switch kind {
	case domain.WorkloadDeployment:
		return rollbackDeployment(ctx, client, ns, name, toRevision, dryRun, patchOpts)
	case domain.WorkloadStatefulSet, domain.WorkloadDaemonSet:
		return rollbackControllerRevision(ctx, client, ns, kind, name, toRevision, dryRun, patchOpts)
	default:
		return domain.RollbackOutcome{}, fmt.Errorf("rollback not supported for kind: %s", kind)
	}
}

// rollbackDeployment copies the target ReplicaSet's spec.template onto the
// Deployment, the way `kubectl rollout undo --to-revision` does for this
// kind — a strategic merge patch of spec.template, the same field SetImage
// patches, plus a change-cause annotation when the Deployment already uses
// the convention.
func rollbackDeployment(ctx context.Context, client kubernetes.Interface, ns, name string, toRevision int64, dryRun bool, patchOpts metav1.PatchOptions) (domain.RollbackOutcome, error) {
	op := fmt.Sprintf("rolling back deployment %q in %q", name, ns)

	// THE SAME LIST RolloutHistory BUILDS, so the revision an operator was
	// just shown as "current" in the History tab is the one this refuses to
	// roll back to — the two can never disagree about which revision that is.
	revisions, err := deploymentRevisions(ctx, client, ns, name)
	if err != nil {
		return domain.RollbackOutcome{}, err
	}

	var target *domain.Revision
	for i := range revisions {
		if revisions[i].Number == toRevision {
			target = &revisions[i]
			break
		}
	}
	if target == nil {
		return domain.RollbackOutcome{}, fmt.Errorf("%s: %w", op, ports.ErrNotFound)
	}
	if target.Current {
		return domain.RollbackOutcome{}, fmt.Errorf("%s: %w: revision %d is already the current one",
			op, domain.ErrInvalidRevision, toRevision)
	}

	rs, err := client.AppsV1().ReplicaSets(ns).Get(ctx, target.Name, metav1.GetOptions{})
	if err != nil {
		return domain.RollbackOutcome{}, classify(op, err)
	}

	patch := map[string]any{
		"spec": map[string]any{
			"template": rs.Spec.Template,
		},
	}

	// kubectl's own deprecated `--record` flag sets kubernetes.io/change-cause
	// on every mutation, and plenty of clusters still carry what it wrote. A
	// rollback keeps the convention going ONLY when the Deployment already
	// uses it — starting it on an object that has never carried one would be
	// PodSteer imposing a convention its operator never opted into.
	deployment, err := client.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return domain.RollbackOutcome{}, classify(op, err)
	}
	if _, hasChangeCause := deployment.Annotations[changeCauseAnnotation]; hasChangeCause {
		patch["metadata"] = map[string]any{
			"annotations": map[string]string{
				changeCauseAnnotation: fmt.Sprintf("rollback to revision %d", toRevision),
			},
		}
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return domain.RollbackOutcome{}, fmt.Errorf("%s: marshaling rollback patch: %w", op, err)
	}

	if _, err := client.AppsV1().Deployments(ns).Patch(ctx, name, types.StrategicMergePatchType, patchBytes, patchOpts); err != nil {
		return domain.RollbackOutcome{}, classify(op, err)
	}

	return domain.RollbackOutcome{ToRevision: toRevision, DryRun: dryRun}, nil
}

// rollbackControllerRevision applies a StatefulSet's or DaemonSet's target
// ControllerRevision patch data directly onto the object as a strategic
// merge patch — the way `kubectl rollout undo` does for these two kinds,
// letting the API server do the same reconstruction rather than this
// process re-implementing strategic-merge-patch semantics by hand.
func rollbackControllerRevision(ctx context.Context, client kubernetes.Interface, ns string, kind domain.WorkloadKind, name string, toRevision int64, dryRun bool, patchOpts metav1.PatchOptions) (domain.RollbackOutcome, error) {
	op := fmt.Sprintf("rolling back %s %q in %q", strings.ToLower(string(kind)), name, ns)

	list, err := client.AppsV1().ControllerRevisions(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return domain.RollbackOutcome{}, classify(op, err)
	}

	var target *appsv1.ControllerRevision
	var maxRevision int64 = -1
	for i := range list.Items {
		cr := &list.Items[i]
		if !ownedBy(cr.OwnerReferences, string(kind), name) {
			continue
		}
		if cr.Revision > maxRevision {
			maxRevision = cr.Revision
		}
		if cr.Revision == toRevision {
			target = cr
		}
	}
	if target == nil {
		return domain.RollbackOutcome{}, fmt.Errorf("%s: %w", op, ports.ErrNotFound)
	}
	if toRevision == maxRevision {
		return domain.RollbackOutcome{}, fmt.Errorf("%s: %w: revision %d is already the current one",
			op, domain.ErrInvalidRevision, toRevision)
	}

	switch kind {
	case domain.WorkloadStatefulSet:
		_, err = client.AppsV1().StatefulSets(ns).Patch(ctx, name, types.StrategicMergePatchType, target.Data.Raw, patchOpts)
	case domain.WorkloadDaemonSet:
		_, err = client.AppsV1().DaemonSets(ns).Patch(ctx, name, types.StrategicMergePatchType, target.Data.Raw, patchOpts)
	}
	if err != nil {
		return domain.RollbackOutcome{}, classify(op, err)
	}

	return domain.RollbackOutcome{ToRevision: toRevision, DryRun: dryRun}, nil
}
