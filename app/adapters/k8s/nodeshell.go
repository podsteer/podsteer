package k8s

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	k8swait "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"

	"github.com/podsteer/podsteer/app/domain"
)

const (
	// nodeShellNamePrefix leads every node-shell pod's name. A random suffix
	// rather than the node's name: a node name can be a long or awkward
	// string that is not a valid DNS-1123 label, and two shells on one node
	// must not collide.
	nodeShellNamePrefix = "podsteer-node-shell-"
	// nodeShellContainerName is the single container in a node-shell pod, and
	// what the attach session targets.
	nodeShellContainerName = "shell"

	// nodeShellManagedByLabel / nodeShellPurposeLabel mark the pod as
	// PodSteer's, so somebody looking at the cluster — or PodSteer after a
	// crash — can recognise and reap what it created.
	nodeShellManagedByLabel = "app.kubernetes.io/managed-by"
	nodeShellManagedByValue = "podsteer"
	nodeShellPurposeLabel   = "podsteer.io/purpose"
	nodeShellPurposeValue   = "node-shell"

	// nodeShellDeadlineSeconds is a backstop, not the normal lifecycle: the
	// pod is deleted when the terminal session ends or PodSteer closes. This
	// covers the one case that cleanup cannot — PodSteer crashing — so a
	// privileged pod cannot outlive the process that was meant to remove it
	// by more than an hour.
	nodeShellDeadlineSeconds int64 = 3600

	// nodeShellReadyTimeout bounds the wait for the pod to schedule and run.
	nodeShellReadyTimeout = 60 * time.Second
	nodeShellPollInterval = 500 * time.Millisecond
)

// ptrTo returns a pointer to v, for the pod-spec fields Kubernetes models as
// optional pointers (privileged, hostPID, activeDeadlineSeconds).
func ptrTo[T any](v T) *T { return &v }

// nodeShells holds the live node shells for one adapter.
//
// The same shape as portForwards, and for the same reason: the record of a
// pod and the thing that deletes it are created and destroyed together, so a
// node shell can never show as running after its pod is gone, nor linger as a
// pod after it has left the list.
type nodeShells struct {
	mu sync.Mutex
	// closed is set by StopAllNodeShells and never cleared: the process is on
	// its way out.
	//
	// SHUTTING DOWN, SO REGISTER NOTHING. Creating a node shell is not
	// instant — the pod has to be scheduled, its image pulled and the kubelet
	// has to report Running, which waitPodRunning allows a full minute for —
	// and nothing cancels that wait at shutdown, because the framework never
	// cancels its runtime context. So a start that was in flight when the
	// sweep ran would otherwise insert its pod into a map nobody reads again,
	// the process would exit, and a PRIVILEGED pod in the node's process and
	// network namespaces would be left running until its one-hour deadline
	// reaped it. The record and the pod would have parted company, which is
	// the one thing this registry promises cannot happen — the same rule, and
	// the same flag, as watchManager.ensure.
	closed bool
	byID   map[string]domain.NodeShell
	nextID int
}

// errNodeShellsClosed reports a node shell abandoned because PodSteer is
// shutting down. Its pod is deleted before this is returned.
var errNodeShellsClosed = errors.New("PodSteer is shutting down, so the node shell was removed")

// buildNodeShellPod builds the privileged pod that becomes a node shell.
//
// Every field here is load-bearing and asserted in a test: hostPID and
// hostNetwork put the pod in the node's process and network namespaces;
// nodeName pins it to the chosen node; a universal Exists toleration lets it
// land on a node carrying ANY taint (a control-plane node, a cordoned or
// specialised one) the way `kubectl node-shell` does; privileged lets nsenter
// enter PID 1's namespaces; and the command runs a login shell on the host,
// falling back to the host's sh when it has no bash. restartPolicy Never and
// the activeDeadlineSeconds backstop keep a shell from being restarted or
// outliving PodSteer.
func buildNodeShellPod(name, namespace, nodeName, image string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				nodeShellManagedByLabel: nodeShellManagedByValue,
				nodeShellPurposeLabel:   nodeShellPurposeValue,
			},
		},
		Spec: corev1.PodSpec{
			NodeName:                     nodeName,
			HostPID:                      true,
			HostNetwork:                  true,
			RestartPolicy:                corev1.RestartPolicyNever,
			ActiveDeadlineSeconds:        ptrTo(nodeShellDeadlineSeconds),
			AutomountServiceAccountToken: ptrTo(false),
			// Tolerate every taint, so a control-plane or otherwise tainted
			// node can still be reached. An Exists toleration with no key and
			// no effect matches all taints — the blanket toleration
			// `kubectl node-shell` uses.
			Tolerations: []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
			Containers: []corev1.Container{{
				Name:            nodeShellContainerName,
				Image:           image,
				ImagePullPolicy: corev1.PullIfNotPresent,
				TTY:             true,
				Stdin:           true,
				SecurityContext: &corev1.SecurityContext{
					Privileged: ptrTo(true),
				},
				// nsenter enters host PID 1's namespaces; the shell that
				// follows resolves against the HOST filesystem, so `bash`/`sh`
				// here are the node's own. bash -l when the host has it, its
				// sh otherwise.
				Command: []string{
					"nsenter",
					"--target", "1",
					"--mount", "--uts", "--ipc", "--net", "--pid",
					"--",
					"sh", "-c", "exec bash -l 2>/dev/null || exec sh",
				},
			}},
		},
	}
}

// StartNodeShell creates the pod, waits for it to run, and records it. See
// ports.NodeShellPort.StartNodeShell.
func (a *Adapter) StartNodeShell(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, nodeName, image string) (domain.NodeShell, error) {
	op := fmt.Sprintf("starting a node shell on %q in %q", nodeName, id)

	client, err := a.factory.clientFor(id)
	if err != nil {
		return domain.NodeShell{}, err
	}

	ns := namespace.String()
	// Named here rather than through generateName: the name is what the
	// record, the delete and the attach all key on, so it is decided once,
	// before the request, and never read back out of a response.
	name := nodeShellNamePrefix + utilrand.String(5)
	created, err := client.CoreV1().Pods(ns).Create(ctx, buildNodeShellPod(name, ns, nodeName, image), metav1.CreateOptions{})
	if err != nil {
		return domain.NodeShell{}, classify(op, err)
	}

	if err := waitPodRunning(ctx, client, ns, created.Name, nodeShellPollInterval, nodeShellReadyTimeout); err != nil {
		// The pod was created but never came up. Delete it rather than leak a
		// privileged pod nobody is attached to — on a fresh context, because
		// the caller's may be the reason the wait ended.
		deleteCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.CoreV1().Pods(ns).Delete(deleteCtx, created.Name, metav1.DeleteOptions{})
		return domain.NodeShell{}, err
	}

	a.nodeShells.mu.Lock()
	if a.nodeShells.closed {
		// StopAllNodeShells swept while this pod was being scheduled, so the
		// sweep did not see it and nothing will read this map again. Delete
		// the pod HERE — on the same fresh, bounded context the failed-wait
		// path above already uses, because the caller's may be cancelled and
		// this delete is the only thing standing between shutdown and a
		// privileged pod left running on a node.
		a.nodeShells.mu.Unlock()

		deleteCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.CoreV1().Pods(ns).Delete(deleteCtx, created.Name, metav1.DeleteOptions{}); err != nil {
			return domain.NodeShell{}, fmt.Errorf("%s: %w: %w", op, errNodeShellsClosed, classify("deleting the node shell pod", err))
		}
		return domain.NodeShell{}, fmt.Errorf("%s: %w", op, errNodeShellsClosed)
	}
	a.nodeShells.nextID++
	shell := domain.NodeShell{
		ID:            strconv.Itoa(a.nodeShells.nextID),
		ClusterID:     id,
		Namespace:     namespace,
		PodName:       created.Name,
		NodeName:      nodeName,
		Image:         image,
		ContainerName: nodeShellContainerName,
	}
	a.nodeShells.byID[shell.ID] = shell
	a.nodeShells.mu.Unlock()

	return shell, nil
}

// StopNodeShell deletes the pod behind one node shell and forgets it.
func (a *Adapter) StopNodeShell(id string) error {
	a.nodeShells.mu.Lock()
	shell, ok := a.nodeShells.byID[id]
	if ok {
		delete(a.nodeShells.byID, id)
	}
	a.nodeShells.mu.Unlock()

	if !ok {
		// Already gone. The terminal session ending and an explicit stop can
		// both reach here for the same shell, so this is not an error.
		return nil
	}

	return a.deleteNodeShellPod(shell)
}

// ListNodeShells reports the node shells running right now.
func (a *Adapter) ListNodeShells() []domain.NodeShell {
	a.nodeShells.mu.Lock()
	defer a.nodeShells.mu.Unlock()

	out := make([]domain.NodeShell, 0, len(a.nodeShells.byID))
	for _, shell := range a.nodeShells.byID {
		out = append(out, shell)
	}
	return out
}

// StopAllNodeShells deletes every node-shell pod, for shutdown.
//
// It also CLOSES the registry, permanently. A start racing this sweep would
// otherwise register its pod after the copy was taken and leave a privileged
// pod behind; see the closed field. Safe to call twice.
func (a *Adapter) StopAllNodeShells() {
	a.nodeShells.mu.Lock()
	a.nodeShells.closed = true
	shells := make([]domain.NodeShell, 0, len(a.nodeShells.byID))
	for id, shell := range a.nodeShells.byID {
		shells = append(shells, shell)
		delete(a.nodeShells.byID, id)
	}
	a.nodeShells.mu.Unlock()

	for _, shell := range shells {
		_ = a.deleteNodeShellPod(shell)
	}
}

// deleteNodeShellPod removes one node shell's pod from the cluster.
//
// On a fresh, bounded context rather than a caller's: the commonest caller is
// a shutdown or a session ending, and a delete that has to happen must not be
// tied to a context those have already cancelled.
func (a *Adapter) deleteNodeShellPod(shell domain.NodeShell) error {
	client, err := a.factory.clientFor(shell.ClusterID)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = client.CoreV1().Pods(shell.Namespace.String()).Delete(ctx, shell.PodName, metav1.DeleteOptions{})
	if err != nil {
		return classify(fmt.Sprintf("deleting node shell pod %q", shell.PodName), err)
	}
	return nil
}

// waitPodRunning blocks until the pod reaches the Running phase, or a bounded
// timeout elapses. The interval and timeout are passed in so a test can drive
// both paths without the production wait.
func waitPodRunning(ctx context.Context, client kubernetes.Interface, ns, podName string, interval, timeout time.Duration) error {
	op := fmt.Sprintf("waiting for pod %q to run", podName)

	err := k8swait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		pod, err := client.CoreV1().Pods(ns).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			return false, classify(op, err)
		}
		switch pod.Status.Phase {
		case corev1.PodRunning:
			return true, nil
		case corev1.PodFailed, corev1.PodSucceeded:
			return false, fmt.Errorf("%s: it reached %s before running", op, pod.Status.Phase)
		default:
			return false, nil
		}
	})
	if err != nil {
		if k8swait.Interrupted(err) {
			return fmt.Errorf("%s: it did not start within %s", op, timeout)
		}
		return err
	}
	return nil
}
