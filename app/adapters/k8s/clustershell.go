package k8s

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilrand "k8s.io/apimachinery/pkg/util/rand"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// nodeshell.go is the template for this file, and the divergences are marked
// where they occur. What is shared is shared on purpose: the registry shape,
// the closed flag, the delete-on-a-fresh-context rule, the
// activeDeadlineSeconds backstop and waitPodRunning are all the node shell's,
// because the leak they prevent is the same leak.

const (
	// clusterShellNamePrefix leads every in-cluster shell pod's name. A random
	// suffix for the same reason the node shell uses one: the name is what the
	// record, the delete and the attach all key on, and two shells in one
	// namespace must not collide.
	clusterShellNamePrefix = "podsteer-shell-"
	// clusterShellContainerName is the single container in the pod, and what
	// the attach session targets.
	clusterShellContainerName = "shell"

	// The labels marking the pod as PodSteer's. managed-by is the node
	// shell's, deliberately identical so anything sweeping a cluster for what
	// this application left behind finds both with one selector; the purpose
	// label is what tells the two apart, and it is what FindClusterShells
	// selects on so a node shell is never offered as an in-cluster one.
	clusterShellManagedByLabel = "app.kubernetes.io/managed-by"
	clusterShellManagedByValue = "podsteer"
	clusterShellPurposeLabel   = "podsteer.io/purpose"
	clusterShellPurposeValue   = "cluster-shell"

	// clusterShellDeadlineSeconds is a backstop, not the normal lifecycle: the
	// pod is deleted when the terminal session ends or PodSteer closes. This
	// covers the one case that cleanup cannot — PodSteer crashing — so a pod
	// this application created cannot outlive the process that was meant to
	// remove it by more than an hour.
	//
	// The same hour as the node shell's, even though nothing here is
	// privileged. The backstop is not about how dangerous the pod is; it is
	// about a pod nobody can see the reason for, and an hour is how long the
	// node shell already taught operators to expect.
	clusterShellDeadlineSeconds int64 = 3600

	// clusterShellReadyTimeout bounds the wait for the pod to schedule and
	// run.
	clusterShellReadyTimeout = 60 * time.Second
	clusterShellPollInterval = 500 * time.Millisecond
)

// clusterShells holds the live in-cluster shells for one adapter.
//
// The same shape as nodeShells, including the closed flag and every word of
// the reason for it: creating the pod is not instant, nothing cancels the wait
// at shutdown because the framework never cancels its runtime context, and a
// start racing the sweep would otherwise register a pod into a map nobody
// reads again. The pod left behind here is unprivileged rather than a root
// shell on a node, which makes the consequence smaller and the rule no
// different — a record and a pod that parted company is the one thing this
// registry promises cannot happen.
type clusterShells struct {
	mu sync.Mutex
	// closed is set by StopAllClusterShells and never cleared: the process is
	// on its way out. See nodeShells.closed for the full argument.
	closed bool
	byID   map[string]domain.ClusterShell
	nextID int
}

// errClusterShellsClosed reports a shell abandoned because PodSteer is shutting
// down. Its pod is deleted before this is returned.
var errClusterShellsClosed = errors.New("PodSteer is shutting down, so the in-cluster shell was removed")

// The two ways an adoption is refused. Both wrap ports.ErrClusterShellNotReusable
// so the message reaches the operator intact while the classification stays one
// thing — see that sentinel for why they are not two.
//
// The NOT-OURS check is the one that matters structurally: adoption takes
// responsibility for DELETING a pod, so it must not be reachable for an
// arbitrary name, or a reuse request becomes "delete any pod in this namespace
// when the pane closes". The pod's own labels are the evidence, read back from
// the cluster rather than trusted from the caller.
//
// The NOT-RUNNING check exists because the list that produced the offer is a
// moment old, and that moment is exactly long enough for a pod to exit.
var (
	errClusterShellNotOurs    = fmt.Errorf("%w: it was not created by PodSteer as an in-cluster shell", ports.ErrClusterShellNotReusable)
	errClusterShellNotRunning = fmt.Errorf("%w: it is no longer running, so there is nothing to attach to", ports.ErrClusterShellNotReusable)
)

// clusterShellSelector matches the pods PodSteer created as in-cluster shells.
var clusterShellSelector = labels.Set{
	clusterShellManagedByLabel: clusterShellManagedByValue,
	clusterShellPurposeLabel:   clusterShellPurposeValue,
}.String()

// buildClusterShellPod builds the ordinary, unprivileged pod that becomes an
// in-cluster shell.
//
// WHAT IS HERE IS ADMISSIBILITY, AND IT IS THE POINT OF THE WHOLE FEATURE.
// A namespace enforcing Pod Security's `restricted` profile rejects a root
// container outright, before anything starts — so this pod is built to satisfy
// that profile: it runs as non-root (the image defaults to the nonroot
// DockyDEB build for exactly this reason), forbids privilege escalation, drops
// every capability, and asks for the RuntimeDefault seccomp profile. Those
// four are `restricted`'s container requirements, and a pod missing any one of
// them is refused in the namespaces most likely to have the profile on.
//
// WHAT IS ABSENT IS AS DELIBERATE AS WHAT IS PRESENT, and each absence is a
// field buildNodeShellPod sets:
//
//   - no nodeName: this pod is not about a node, so the scheduler places it.
//   - no hostPID, hostNetwork or privileged: the whole point is that this is
//     an ORDINARY pod. It reaches the cluster's network from inside it, which
//     is a vantage point, not a privilege.
//   - no blanket toleration: a node shell tolerates every taint because it has
//     to land on one specific node. This one may land anywhere that will take
//     an ordinary pod, and a blanket toleration would let it schedule onto
//     control-plane or specialised nodes that taints exist to keep pods off.
//
// automountServiceAccountToken is the one place this deliberately does the
// OPPOSITE of the node shell, which sets it false. A node shell talks to a
// node through nsenter and needs no API access at all. This shell exists so
// somebody can run kubectl from inside the cluster, and the token it gets is
// the namespace's own default ServiceAccount — precisely what `kubectl run`
// hands any pod in that namespace, and not a credential of the operator's that
// PodSteer copied anywhere. Set explicitly rather than left to the default, so
// it is a decision on the record instead of an omission.
func buildClusterShellPod(name, namespace, image string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				clusterShellManagedByLabel: clusterShellManagedByValue,
				clusterShellPurposeLabel:   clusterShellPurposeValue,
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy:                corev1.RestartPolicyNever,
			ActiveDeadlineSeconds:        ptrTo(clusterShellDeadlineSeconds),
			AutomountServiceAccountToken: ptrTo(true),
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   ptrTo(true),
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{{
				Name:            clusterShellContainerName,
				Image:           image,
				ImagePullPolicy: corev1.PullIfNotPresent,
				TTY:             true,
				Stdin:           true,
				SecurityContext: &corev1.SecurityContext{
					// RunAsNonRoot and the seccomp profile are repeated at the
					// container level rather than left to the pod's. Pod
					// Security evaluates the EFFECTIVE value per container, and
					// a container-level security context that omits them
					// inherits the pod's — but stating them here means a future
					// edit to the pod block cannot silently make one container
					// inadmissible.
					RunAsNonRoot:             ptrTo(true),
					AllowPrivilegeEscalation: ptrTo(false),
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
					SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
				},
				// No nsenter, and no host to enter: the shell is the image's
				// own, in the pod's own namespaces. bash -l where the image has
				// it, its sh otherwise — the same fallback the node shell uses,
				// because an image without bash is a shell either way.
				Command: []string{"sh", "-c", loginShellCommand},
			}},
		},
	}
}

// StartClusterShell creates the pod, waits for it to run, and records it. See
// ports.ClusterShellPort.StartClusterShell.
func (a *Adapter) StartClusterShell(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, image string) (domain.ClusterShell, error) {
	op := fmt.Sprintf("starting an in-cluster shell in %q on %q", namespace, id)

	if namespace.IsAll() {
		return domain.ClusterShell{}, fmt.Errorf("%s: %w", op, domain.ErrShellNamespaceRequired)
	}

	client, err := a.factory.clientFor(id)
	if err != nil {
		return domain.ClusterShell{}, err
	}

	ns := namespace.String()
	// Named here rather than through generateName, for the node shell's
	// reason: the name is what the record, the delete and the attach all key
	// on, so it is decided once, before the request, and never read back out
	// of a response.
	name := clusterShellNamePrefix + utilrand.String(5)
	created, err := client.CoreV1().Pods(ns).Create(ctx, buildClusterShellPod(name, ns, image), metav1.CreateOptions{})
	if err != nil {
		return domain.ClusterShell{}, classifyPodCreate(op, err)
	}

	if err := waitPodRunning(ctx, client, ns, created.Name, clusterShellPollInterval, clusterShellReadyTimeout); err != nil {
		// The pod was created but never came up. Delete it rather than leave a
		// pod nobody is attached to — on a fresh context, because the caller's
		// may be the reason the wait ended.
		deleteCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.CoreV1().Pods(ns).Delete(deleteCtx, created.Name, metav1.DeleteOptions{})
		return domain.ClusterShell{}, err
	}

	shell, err := a.registerClusterShell(domain.ClusterShell{
		ClusterID:     id,
		Namespace:     namespace,
		PodName:       created.Name,
		Image:         image,
		ContainerName: clusterShellContainerName,
	})
	if err != nil {
		// StopAllClusterShells swept while this pod was being scheduled, so the
		// sweep did not see it and nothing will read the registry again. Delete
		// the pod HERE, on a fresh bounded context, because the caller's may be
		// cancelled and this delete is the only thing standing between shutdown
		// and a pod left running in somebody's namespace.
		deleteCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if delErr := client.CoreV1().Pods(ns).Delete(deleteCtx, created.Name, metav1.DeleteOptions{}); delErr != nil {
			return domain.ClusterShell{}, fmt.Errorf("%s: %w: %w", op, err, classify("deleting the in-cluster shell pod", delErr))
		}
		return domain.ClusterShell{}, fmt.Errorf("%s: %w", op, err)
	}

	return shell, nil
}

// AdoptClusterShell takes over an existing pod for reuse. See
// ports.ClusterShellPort.AdoptClusterShell.
//
// It reads the pod back from the cluster rather than trusting what the caller
// was offered: the list that produced the offer is a moment old, and both the
// facts that matter — that this is PodSteer's shell pod, and that it is still
// running — can have changed since. Adoption puts the pod on the hook for
// deletion, so both are checked against the live object.
func (a *Adapter) AdoptClusterShell(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName string) (domain.ClusterShell, error) {
	op := fmt.Sprintf("attaching to the in-cluster shell %q in %q on %q", podName, namespace, id)

	if namespace.IsAll() {
		return domain.ClusterShell{}, fmt.Errorf("%s: %w", op, domain.ErrShellNamespaceRequired)
	}

	client, err := a.factory.clientFor(id)
	if err != nil {
		return domain.ClusterShell{}, err
	}

	pod, err := client.CoreV1().Pods(namespace.String()).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return domain.ClusterShell{}, classify(op, err)
	}
	if !isClusterShellPod(pod) {
		return domain.ClusterShell{}, fmt.Errorf("%s: %w", op, errClusterShellNotOurs)
	}
	if domain.PodPhase(pod.Status.Phase) != domain.PodPhaseRunning {
		return domain.ClusterShell{}, fmt.Errorf("%s: %w (%s)", op, errClusterShellNotRunning, pod.Status.Phase)
	}

	shell, err := a.registerClusterShell(domain.ClusterShell{
		ClusterID:     id,
		Namespace:     namespace,
		PodName:       pod.Name,
		Image:         clusterShellImageOf(pod),
		ContainerName: clusterShellContainerName,
		Adopted:       true,
	})
	if err != nil {
		// A sweep landed between the read and the register. Delete the pod:
		// this process is exiting, and an adopted pod is one PodSteer has taken
		// responsibility for exactly as if it had created it — leaving it is
		// the leak the whole registry exists to prevent.
		deleteCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.CoreV1().Pods(namespace.String()).Delete(deleteCtx, pod.Name, metav1.DeleteOptions{})
		return domain.ClusterShell{}, fmt.Errorf("%s: %w", op, err)
	}

	return shell, nil
}

// FindClusterShells lists PodSteer's own shell pods in a namespace, minus the
// ones this process already tracks. See ports.ClusterShellPort.
func (a *Adapter) FindClusterShells(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) ([]domain.ClusterShellCandidate, error) {
	op := fmt.Sprintf("looking for in-cluster shells in %q on %q", namespace, id)

	if namespace.IsAll() {
		return nil, fmt.Errorf("%s: %w", op, domain.ErrShellNamespaceRequired)
	}

	client, err := a.factory.clientFor(id)
	if err != nil {
		return nil, err
	}

	pods, err := client.CoreV1().Pods(namespace.String()).List(ctx, metav1.ListOptions{LabelSelector: clusterShellSelector})
	if err != nil {
		return nil, classify(op, err)
	}

	tracked := a.trackedClusterShellPods(id, namespace)

	out := make([]domain.ClusterShellCandidate, 0, len(pods.Items))
	for i := range pods.Items {
		pod := &pods.Items[i]
		if tracked[pod.Name] {
			continue
		}
		out = append(out, domain.ClusterShellCandidate{
			PodName: pod.Name,
			Image:   clusterShellImageOf(pod),
			// Quoted, never judged: domain.PlanClusterShellReuse decides what
			// may be offered, and it needs the phase the API server reported.
			Phase: domain.PodPhase(pod.Status.Phase),
		})
	}
	return out, nil
}

// StopClusterShell deletes the pod behind one shell and forgets it.
func (a *Adapter) StopClusterShell(id string) error {
	a.clusterShells.mu.Lock()
	shell, ok := a.clusterShells.byID[id]
	if ok {
		delete(a.clusterShells.byID, id)
	}
	a.clusterShells.mu.Unlock()

	if !ok {
		// Already gone. The terminal session ending and an explicit stop can
		// both reach here for the same shell, so this is not an error.
		return nil
	}

	return a.deleteClusterShellPod(shell)
}

// ListClusterShells reports the in-cluster shells running right now.
func (a *Adapter) ListClusterShells() []domain.ClusterShell {
	a.clusterShells.mu.Lock()
	defer a.clusterShells.mu.Unlock()

	out := make([]domain.ClusterShell, 0, len(a.clusterShells.byID))
	for _, shell := range a.clusterShells.byID {
		out = append(out, shell)
	}
	return out
}

// StopAllClusterShells deletes every in-cluster shell pod, for shutdown.
//
// It also CLOSES the registry, permanently — see nodeShells.closed for the
// argument, which applies here unchanged. Safe to call twice.
func (a *Adapter) StopAllClusterShells() {
	a.clusterShells.mu.Lock()
	a.clusterShells.closed = true
	shells := make([]domain.ClusterShell, 0, len(a.clusterShells.byID))
	for id, shell := range a.clusterShells.byID {
		shells = append(shells, shell)
		delete(a.clusterShells.byID, id)
	}
	a.clusterShells.mu.Unlock()

	for _, shell := range shells {
		_ = a.deleteClusterShellPod(shell)
	}
}

// registerClusterShell puts a shell in the registry, or refuses because the
// registry is closed.
//
// Shared by the create and the adopt paths so there is exactly ONE place that
// tests the closed flag and assigns an id — two would be two chances for the
// sweep window to be handled differently in each.
func (a *Adapter) registerClusterShell(shell domain.ClusterShell) (domain.ClusterShell, error) {
	a.clusterShells.mu.Lock()
	defer a.clusterShells.mu.Unlock()

	if a.clusterShells.closed {
		return domain.ClusterShell{}, errClusterShellsClosed
	}

	a.clusterShells.nextID++
	shell.ID = strconv.Itoa(a.clusterShells.nextID)
	a.clusterShells.byID[shell.ID] = shell
	return shell, nil
}

// trackedClusterShellPods names the pods this process already owns in one
// namespace, so a reuse offer never includes a shell that already has a pane.
func (a *Adapter) trackedClusterShellPods(id domain.ClusterID, namespace domain.NamespaceName) map[string]bool {
	a.clusterShells.mu.Lock()
	defer a.clusterShells.mu.Unlock()

	out := make(map[string]bool, len(a.clusterShells.byID))
	for _, shell := range a.clusterShells.byID {
		if shell.ClusterID == id && shell.Namespace == namespace {
			out[shell.PodName] = true
		}
	}
	return out
}

// deleteClusterShellPod removes one shell's pod from the cluster.
//
// On a fresh, bounded context rather than a caller's: the commonest caller is
// a shutdown or a session ending, and a delete that has to happen must not be
// tied to a context those have already cancelled.
func (a *Adapter) deleteClusterShellPod(shell domain.ClusterShell) error {
	client, err := a.factory.clientFor(shell.ClusterID)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = client.CoreV1().Pods(shell.Namespace.String()).Delete(ctx, shell.PodName, metav1.DeleteOptions{})
	if err != nil {
		return classify(fmt.Sprintf("deleting in-cluster shell pod %q", shell.PodName), err)
	}
	return nil
}

// isClusterShellPod reports whether a pod carries both of PodSteer's
// in-cluster-shell labels.
func isClusterShellPod(pod *corev1.Pod) bool {
	return pod.Labels[clusterShellManagedByLabel] == clusterShellManagedByValue &&
		pod.Labels[clusterShellPurposeLabel] == clusterShellPurposeValue
}

// clusterShellImageOf reports the image of a shell pod's single container, or
// "" for a pod with no containers — which cannot happen through Kubernetes'
// own validation, and is guarded rather than indexed blindly because this
// reads objects the cluster returned rather than ones this process built.
func clusterShellImageOf(pod *corev1.Pod) string {
	if len(pod.Spec.Containers) == 0 {
		return ""
	}
	return pod.Spec.Containers[0].Image
}

// classifyPodCreate is classify with one extra distinction, for the create of a
// pod PodSteer builds itself.
//
// AN ADMISSION REFUSAL IS A 403, AND SO IS AN RBAC DENIAL. classify maps every
// Forbidden to ports.ErrForbidden, whose operator-facing sentence is "your
// account is not allowed to perform this operation" — true for RBAC and false
// for admission, where the account was allowed and the OBJECT was declined.
// Reported that way it sends somebody to ask an administrator for a permission
// they already hold, and it discards the API server's message, which names the
// exact field to change and is the only thing anybody can act on.
//
// Told apart by the API server's own wording, because there is no other
// evidence: a 403 carries no machine-readable reason separating the two. Both
// forms are stable, user-visible API server text — Pod Security's
// `violates PodSecurity "…"` and the generic `admission webhook "…" denied the
// request` — and anything that matches neither keeps classify's answer, so a
// wording change costs the sharper message and never the diagnosis.
func classifyPodCreate(op string, err error) error {
	if err == nil {
		return nil
	}
	if apierrors.IsForbidden(err) && isAdmissionRefusal(err) {
		// The original error is wrapped rather than reformatted, so the API
		// server's own words reach the operator verbatim — see
		// ports.ErrPodRejectedByAdmission.
		return fmt.Errorf("%s: %w: %w", op, ports.ErrPodRejectedByAdmission, err)
	}
	return classify(op, err)
}

// isAdmissionRefusal reports whether a Forbidden came from an admission
// controller rather than from authorisation.
func isAdmissionRefusal(err error) bool {
	message := err.Error()
	return strings.Contains(message, "violates PodSecurity") ||
		strings.Contains(message, "admission webhook")
}
