package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"

	"github.com/podsteer/podsteer/app/domain"
)

// deploymentRevisionAnnotation is the annotation the deployment controller
// stamps on a Deployment AND on each of its ReplicaSets with the revision
// number — the same field `kubectl rollout history` reads.
const deploymentRevisionAnnotation = "deployment.kubernetes.io/revision"

// changeCauseAnnotation is set by kubectl's deprecated `--record` flag, or by
// hand, and is the one thing `kubectl rollout history` shows beside the
// revision number.
const changeCauseAnnotation = "kubernetes.io/change-cause"

// RolloutHistory returns the recorded revisions of a Deployment,
// StatefulSet or DaemonSet's pod template, newest first. See
// ports.WorkloadPort.RolloutHistory for the full contract.
func (a *Adapter) RolloutHistory(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName, name string) ([]domain.Revision, error) {
	client, err := a.factory.clientFor(id)
	if err != nil {
		return nil, err
	}
	ns := namespace.String()

	switch kind {
	case domain.WorkloadDeployment:
		return deploymentRevisions(ctx, client, ns, name)
	case domain.WorkloadStatefulSet, domain.WorkloadDaemonSet:
		return controllerRevisionHistory(ctx, client, ns, kind, name)
	default:
		return nil, fmt.Errorf("rollout history not supported for kind: %s", kind)
	}
}

// deploymentRevisions lists the ReplicaSets a Deployment owns and turns them
// into revisions, newest first.
//
// Shared with rollbackDeployment below, so the list a rollback checks
// "already current" against and the list an operator was just shown in the
// History tab can never disagree — the same reasoning WorkloadUsage shares
// domain.WorkloadConsumption with the list it sums.
func deploymentRevisions(ctx context.Context, client kubernetes.Interface, ns, name string) ([]domain.Revision, error) {
	op := fmt.Sprintf("listing rollout history for deployment %q in %q", name, ns)

	list, err := client.AppsV1().ReplicaSets(ns).List(ctx, metav1.ListOptions{ResourceVersion: cachedResourceVersion})
	if err != nil {
		return nil, classify(op, err)
	}

	revisions := make([]domain.Revision, 0, len(list.Items))
	var maxRevision int64 = -1
	for i := range list.Items {
		rs := &list.Items[i]
		if !ownedBy(rs.OwnerReferences, "Deployment", name) {
			continue
		}

		number := revisionNumber(rs.Annotations)
		if number < 0 {
			// A ReplicaSet the deployment controller has not annotated
			// yet — a brand new one mid-rollout, most often — has no
			// revision to show or roll back to. Skipped rather than
			// failing the whole history, the same "one bad object must
			// not empty the list" rule ListWorkloads follows.
			continue
		}
		if number > maxRevision {
			maxRevision = number
		}

		templateYAML, err := templateToYAML(rs.Spec.Template)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", op, err)
		}

		revisions = append(revisions, domain.Revision{
			Number:       number,
			Name:         rs.Name,
			Created:      rs.CreationTimestamp.Time,
			Replicas:     derefInt32(rs.Spec.Replicas, 0),
			Images:       podTemplateImages(rs.Spec.Template),
			ChangeCause:  rs.Annotations[changeCauseAnnotation],
			TemplateYAML: templateYAML,
		})
	}

	markCurrent(revisions, maxRevision)
	sortRevisionsDescending(revisions)
	return revisions, nil
}

// controllerRevisionHistory lists the ControllerRevisions a StatefulSet or
// DaemonSet owns and turns them into revisions, newest first. Shared with
// rollbackControllerRevision below, for the same reason deploymentRevisions
// is shared with rollbackDeployment.
func controllerRevisionHistory(ctx context.Context, client kubernetes.Interface, ns string, kind domain.WorkloadKind, name string) ([]domain.Revision, error) {
	op := fmt.Sprintf("listing rollout history for %s %q in %q", strings.ToLower(string(kind)), name, ns)

	list, err := client.AppsV1().ControllerRevisions(ns).List(ctx, metav1.ListOptions{ResourceVersion: cachedResourceVersion})
	if err != nil {
		return nil, classify(op, err)
	}

	revisions := make([]domain.Revision, 0, len(list.Items))
	var maxRevision int64 = -1
	for i := range list.Items {
		cr := &list.Items[i]
		if !ownedBy(cr.OwnerReferences, string(kind), name) {
			continue
		}
		if cr.Revision > maxRevision {
			maxRevision = cr.Revision
		}

		template, err := controllerRevisionTemplate(cr.Data.Raw)
		if err != nil {
			// A ControllerRevision whose patch this cannot decode is a
			// mapping bug or an unfamiliar shape, not a reason to show an
			// operator an empty history — skip it, the same "one bad
			// object" rule the Deployment branch above follows.
			continue
		}
		templateYAML, err := templateToYAML(template)
		if err != nil {
			continue
		}

		revisions = append(revisions, domain.Revision{
			Number:       cr.Revision,
			Name:         cr.Name,
			Created:      cr.CreationTimestamp.Time,
			Images:       podTemplateImages(template),
			ChangeCause:  cr.Annotations[changeCauseAnnotation],
			TemplateYAML: templateYAML,
		})
	}

	markCurrent(revisions, maxRevision)
	sortRevisionsDescending(revisions)
	return revisions, nil
}

// revisionNumber parses the deployment.kubernetes.io/revision annotation,
// returning -1 when it is absent or unparseable — a caller-friendly sentinel
// rather than an error, since a missing annotation is routine (a ReplicaSet
// the controller has not stamped yet) and must not fail the whole read.
func revisionNumber(annotations map[string]string) int64 {
	raw, ok := annotations[deploymentRevisionAnnotation]
	if !ok {
		return -1
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return -1
	}
	return n
}

// markCurrent flags the revision carrying maxRevision as Current.
//
// THE HIGHEST REVISION NUMBER, not the largest replica count or the newest
// timestamp. Kubernetes' deployment controller and the shared
// ControllerRevision history package both REUSE an existing revision object
// and bump its number to one past the previous maximum when a rollback's
// target template already exists, rather than creating a new object — so
// the active revision is always the one carrying the highest number, in
// steady state and immediately after a rollback alike. This is also why a
// StatefulSet's Status.CurrentRevision field is deliberately not read here:
// the same rule applies uniformly to all three kinds, DaemonSet included,
// which carries no such status field at all.
func markCurrent(revisions []domain.Revision, maxRevision int64) {
	for i := range revisions {
		revisions[i].Current = revisions[i].Number == maxRevision
	}
}

// sortRevisionsDescending orders revisions newest-number-first, the order
// `kubectl rollout history` prints and the order the History tab shows.
func sortRevisionsDescending(revisions []domain.Revision) {
	sort.Slice(revisions, func(i, j int) bool {
		return revisions[i].Number > revisions[j].Number
	})
}

// templateToYAML renders a pod template as YAML for the diff view, through
// the same JSON round trip GetManifest uses so field ordering matches what
// the API server would have printed.
func templateToYAML(template corev1.PodTemplateSpec) (string, error) {
	encoded, err := json.Marshal(template)
	if err != nil {
		return "", fmt.Errorf("encoding pod template: %w", err)
	}
	manifest, err := yaml.JSONToYAML(encoded)
	if err != nil {
		return "", fmt.Errorf("converting pod template to YAML: %w", err)
	}
	return string(manifest), nil
}

// controllerRevisionTemplate decodes the pod template a ControllerRevision's
// raw patch data encodes.
//
// A ControllerRevision.Data.Raw is a strategic-merge-patch-shaped JSON
// document of the parent object as it looked at that revision — the format
// StatefulSet's and DaemonSet's shared history controller
// (k8s.io/kubernetes/pkg/controller/history, not vendored here) both write.
// spec.template is the part of it worth reading: the same field a
// Deployment's ReplicaSet carries directly, and everything RolloutHistory
// and a rollback need from it.
func controllerRevisionTemplate(raw []byte) (corev1.PodTemplateSpec, error) {
	var decoded struct {
		Spec struct {
			Template corev1.PodTemplateSpec `json:"template"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return corev1.PodTemplateSpec{}, fmt.Errorf("decoding controller revision data: %w", err)
	}
	return decoded.Spec.Template, nil
}
