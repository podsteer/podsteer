package k8s

// Draining a node is planned in the domain and executed here. domain.PlanDrain
// decides what SHOULD happen to each pod — evict, skip, refuse — from facts
// this file gathers; nothing here decides who gets evicted, and nothing in
// the domain touches the network. That split is what lets the UI preview a
// drain (DrainCandidates + domain.PlanDrain, no writes at all) with exactly
// the logic DrainNode itself runs a moment later.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"

	"golang.org/x/sync/errgroup"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

const (
	// mirrorPodAnnotation is set by the kubelet on a static pod mirrored
	// from a manifest on the node's own disk. The API server can list such
	// a pod but cannot delete it — the kubelet owns it — which is why
	// domain.PlanDrain always skips one.
	mirrorPodAnnotation = "kubernetes.io/config.mirror"

	// drainConcurrency bounds how many pods DrainNode evicts at once. The
	// same shape as filesystemConcurrency in filesystems.go — high enough
	// that a large node finishes inside one operator's patience, low enough
	// that a burst of evictions does not itself look like the problem an
	// operator was draining the node to fix.
	drainConcurrency = 5

	// defaultDrainTimeout is how long DrainNode retries a
	// PodDisruptionBudget refusal when the caller leaves DrainOptions.Timeout
	// unset. Long enough for a rolling deployment elsewhere in the cluster to
	// finish and raise the budget's disruptions-allowed back above zero;
	// short enough that an operator is not left waiting indefinitely on a
	// budget that will never permit it.
	defaultDrainTimeout = 5 * time.Minute

	// disruptionBudgetBackoff is how long DrainNode waits between eviction
	// attempts a PodDisruptionBudget has refused.
	disruptionBudgetBackoff = 5 * time.Second

	// podGoneInterval is how often DrainNode polls for a pod's disappearance
	// after its eviction was accepted.
	podGoneInterval = 2 * time.Second
)

// errDrainTimedOut marks a per-pod failure as a timeout rather than an
// ordinary error, so DrainNode can set DrainReport.TimedOut without the
// caller having to parse Reason strings to tell the two apart.
var errDrainTimedOut = errors.New("timed out")

// CordonNode marks a node schedulable or unschedulable.
//
// A merge patch of spec.unschedulable — the single field `kubectl
// cordon`/`uncordon` sets — rather than a full update, so a concurrent change
// to anything else on the node (labels, taints, status) cannot be clobbered
// by a stale read-modify-write.
func (a *Adapter) CordonNode(ctx context.Context, id domain.ClusterID, name string, cordon bool) error {
	defer a.forgetReads(id)

	client, err := a.factory.clientFor(id)
	if err != nil {
		return err
	}

	patch := map[string]any{
		"spec": map[string]any{
			"unschedulable": cordon,
		},
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshaling cordon patch: %w", err)
	}

	if _, err := client.CoreV1().Nodes().Patch(ctx, name, types.MergePatchType, patchBytes, metav1.PatchOptions{}); err != nil {
		return classify(fmt.Sprintf("cordoning node %q of %q", name, id), err)
	}
	return nil
}

// EvictPod evicts one pod through the policy/v1 Eviction subresource.
//
// NEVER A PLAIN DELETE. The eviction subresource is the one request a
// PodDisruptionBudget can refuse — a delete simply removes the pod, budget or
// no budget — which is the entire reason `kubectl drain` and this method both
// go through it rather than DeleteResource.
//
// gracePeriodSeconds negative means "use the pod's own
// terminationGracePeriodSeconds", matching DrainOptions.GracePeriodSeconds.
func (a *Adapter) EvictPod(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name string, gracePeriodSeconds int) error {
	defer a.forgetReads(id)

	client, err := a.factory.clientFor(id)
	if err != nil {
		return err
	}

	eviction := &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace.String(),
		},
	}
	if gracePeriodSeconds >= 0 {
		grace := int64(gracePeriodSeconds)
		eviction.DeleteOptions = &metav1.DeleteOptions{GracePeriodSeconds: &grace}
	}

	if err := client.PolicyV1().Evictions(namespace.String()).Evict(ctx, eviction); err != nil {
		return classify(fmt.Sprintf("evicting pod %q in %q of %q", name, namespace, id), err)
	}
	return nil
}

// DrainCandidates returns the pods on a node with the extra facts
// domain.PlanDrain needs, read from the raw corev1.Pod rather than the
// already-mapped domain.Pod — Mirror and LocalStorage come from the
// annotation and the volume list, neither of which domain.Pod carries, and
// they are true only in a drain's context. See domain.DrainCandidate for why
// they do not belong on domain.Pod itself.
//
// Shares ListPodsOnNode's field-selected listing rather than its result: the
// two need different shapes from the same corev1.Pod, and building
// DrainCandidate from an already-mapped domain.Pod would mean re-deriving
// Mirror and LocalStorage from fields ListPodsOnNode's mapper already
// discarded.
func (a *Adapter) DrainCandidates(ctx context.Context, id domain.ClusterID, nodeName string) ([]domain.DrainCandidate, error) {
	op := fmt.Sprintf("listing drain candidates on node %q of %q", nodeName, id)

	if strings.TrimSpace(nodeName) == "" {
		return nil, fmt.Errorf("%s: no node named", op)
	}

	client, err := a.factory.clientFor(id)
	if err != nil {
		return nil, err
	}

	list, err := client.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
		FieldSelector:   fields.OneTermEqualSelector("spec.nodeName", nodeName).String(),
		ResourceVersion: cachedResourceVersion,
	})
	if err != nil {
		return nil, classify(op, err)
	}

	candidates := make([]domain.DrainCandidate, 0, len(list.Items))
	for i := range list.Items {
		raw := &list.Items[i]

		pod, err := mapPod(id, raw)
		if err != nil {
			a.logger.WarnContext(ctx, "skipping unmappable pod",
				slog.String("cluster", id.String()),
				slog.String("node", nodeName),
				slog.String("name", raw.Name),
				slog.String("error", err.Error()))
			continue
		}

		candidates = append(candidates, domain.DrainCandidate{
			Pod:          pod,
			Mirror:       raw.Annotations[mirrorPodAnnotation] != "",
			LocalStorage: hasEmptyDirVolume(raw),
		})
	}
	return candidates, nil
}

// hasEmptyDirVolume reports whether pod declares any emptyDir volume — local
// scratch space that lives on the node and is discarded, not migrated, when
// the pod is evicted.
func hasEmptyDirVolume(pod *corev1.Pod) bool {
	for _, volume := range pod.Spec.Volumes {
		if volume.EmptyDir != nil {
			return true
		}
	}
	return false
}

// DrainNode cordons a node, plans the drain, and — if the plan is runnable —
// evicts every pod it allows.
//
// The report is built up as the drain proceeds rather than assembled only on
// success, because CORDONED IS TRUE THE MOMENT IT HAPPENS regardless of what
// follows: an operator who sees the request fail still needs to know the
// node stopped accepting new pods, which nothing else here would tell them.
func (a *Adapter) DrainNode(ctx context.Context, id domain.ClusterID, name string, opts domain.DrainOptions) (domain.DrainReport, error) {
	defer a.forgetReads(id)

	if err := a.CordonNode(ctx, id, name, true); err != nil {
		return domain.DrainReport{}, err
	}
	report := domain.DrainReport{Cordoned: true}

	candidates, err := a.DrainCandidates(ctx, id, name)
	if err != nil {
		return report, err
	}

	plan := domain.PlanDrain(candidates, opts)
	report.Skipped = plan.Skipped
	report.Refused = plan.Refused

	if !plan.Runnable() {
		// Nothing is evicted. Cordoning already happened and stays true in
		// the report — refusing the DRAIN is not a reason to uncordon a node
		// an operator asked to take out of service.
		return report, fmt.Errorf("draining node %q of %q: %w", name, id, ports.ErrDrainRefused)
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultDrainTimeout
	}
	deadline := time.Now().Add(timeout)

	var (
		mu       sync.Mutex
		evicted  []domain.Pod
		failed   []domain.DrainFailure
		timedOut bool
	)

	// Bounded fan-out, not a group whose failure cancels the rest: an
	// eviction failing is an ORDINARY outcome the report records, not a
	// reason to abandon every other pod still draining cleanly. Every
	// goroutine below therefore always returns nil to the group — see the
	// comment at group.Wait().
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(drainConcurrency)

	for _, pod := range plan.Evict {
		group.Go(func() error {
			evictErr := a.evictWithRetry(groupCtx, id, pod, opts.GracePeriodSeconds, deadline)

			mu.Lock()
			defer mu.Unlock()
			switch {
			case evictErr == nil:
				evicted = append(evicted, pod)
			case errors.Is(evictErr, errDrainTimedOut):
				timedOut = true
				failed = append(failed, domain.DrainFailure{
					Pod:    podRef(pod),
					Reason: evictErr.Error(),
				})
			default:
				failed = append(failed, domain.DrainFailure{
					Pod:    podRef(pod),
					Reason: evictErr.Error(),
				})
			}
			// A per-pod failure is reported, not propagated: propagating it
			// would cancel groupCtx and abort every eviction still in
			// flight for pods that have nothing wrong with them.
			return nil
		})
	}
	// Never returns an error — see above — so there is nothing to check.
	_ = group.Wait()

	report.Evicted = evicted
	report.Failed = failed
	report.TimedOut = timedOut
	return report, nil
}

// podRef names a pod as "namespace/name", for a DrainFailure.
func podRef(pod domain.Pod) string {
	return pod.Namespace().String() + "/" + pod.Name()
}

// evictWithRetry evicts one pod, retrying ONLY a PodDisruptionBudget refusal
// — every other failure is returned at once, because nothing about waiting
// changes a bare-pod refusal or a not-found — and then waits for it to
// actually go.
//
// deadline bounds both halves: retrying the eviction and waiting for
// termination share one budget rather than each getting the full timeout, so
// a pod that took most of it to satisfy its budget does not then get a
// second full timeout to terminate.
func (a *Adapter) evictWithRetry(ctx context.Context, id domain.ClusterID, pod domain.Pod, gracePeriodSeconds int, deadline time.Time) error {
	for {
		err := a.EvictPod(ctx, id, pod.Namespace(), pod.Name(), gracePeriodSeconds)
		if err == nil {
			break
		}
		if !errors.Is(err, ports.ErrDisruptionBudget) {
			return err
		}
		if time.Now().Add(disruptionBudgetBackoff).After(deadline) {
			return fmt.Errorf("%w: a PodDisruptionBudget had not allowed evicting %s within the timeout",
				errDrainTimedOut, podRef(pod))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(disruptionBudgetBackoff):
		}
	}

	return a.waitForPodGone(ctx, id, pod, deadline)
}

// waitForPodGone polls until pod has disappeared or been replaced.
//
// NotFound or a changed UID both mean gone: a UID change means the API
// server already recreated an object with this name — which does not happen
// to a pod, but a caller confirming "the pod I evicted is no longer the pod
// running here" is the same check either way, and the one that does not race
// a kubelet slow to finish deleting the old object.
func (a *Adapter) waitForPodGone(ctx context.Context, id domain.ClusterID, pod domain.Pod, deadline time.Time) error {
	client, err := a.factory.clientFor(id)
	if err != nil {
		return err
	}
	ns := pod.Namespace().String()

	for {
		current, getErr := client.CoreV1().Pods(ns).Get(ctx, pod.Name(), metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(getErr):
			return nil
		case getErr != nil:
			return classify(fmt.Sprintf("waiting for pod %q to terminate", podRef(pod)), getErr)
		case string(current.UID) != pod.UID():
			return nil
		}

		if time.Now().Add(podGoneInterval).After(deadline) {
			return fmt.Errorf("%w: %s had not terminated within the timeout", errDrainTimedOut, podRef(pod))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(podGoneInterval):
		}
	}
}
