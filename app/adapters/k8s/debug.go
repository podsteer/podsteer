package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	k8swait "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

const (
	// debuggerNamePrefix leads the generated name of every ephemeral debug
	// container — `debugger-xxxxx`, the same shape `kubectl debug` uses when
	// no name is given, so a person reading the pod recognises what added it.
	debuggerNamePrefix = "debugger-"

	// ephemeralReadyTimeout bounds how long AddEphemeralContainer's caller
	// waits for the container to come up before giving up. Generous, because
	// the delay here is an image pull rather than anything PodSteer controls.
	ephemeralReadyTimeout = 60 * time.Second
	// ephemeralPollInterval is how often the pod's status is re-read while
	// waiting. Half a second reads as immediate once the container starts and
	// costs a trivial number of gets against a pod that is pulling an image.
	ephemeralPollInterval = 500 * time.Millisecond
)

// AddEphemeralContainer adds an ephemeral debug container to a running pod
// through the pods/ephemeralcontainers subresource. See
// ports.ManagementPort.AddEphemeralContainer for the full contract.
func (a *Adapter) AddEphemeralContainer(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName string, spec domain.DebugContainerSpec) (string, error) {
	defer a.forgetReads(id)

	client, err := a.factory.clientFor(id)
	if err != nil {
		return "", err
	}

	ns := namespace.String()
	name := debuggerNamePrefix + utilrand.String(5)

	container := corev1.EphemeralContainer{
		EphemeralContainerCommon: corev1.EphemeralContainerCommon{
			Name:                     name,
			Image:                    spec.Image,
			Command:                  spec.Command,
			Stdin:                    spec.Stdin,
			TTY:                      spec.TTY,
			TerminationMessagePolicy: corev1.TerminationMessageReadFile,
			ImagePullPolicy:          corev1.PullIfNotPresent,
		},
		// Sharing the target's process namespace is the whole point of naming
		// one: it lets the debugger see and signal the target's processes.
		// Empty targets only the pod's own namespaces, which is the default.
		TargetContainerName: spec.TargetContainer,
	}

	// A STRATEGIC MERGE PATCH, not a full UpdateEphemeralContainers of a pod
	// read first: spec.ephemeralContainers merges by container name, so this
	// appends the new one and leaves every debug container already on the pod
	// in place — and it does so in one request, with no read-modify-write
	// window in which a concurrent debug session's container could be lost.
	// This is the request `kubectl debug` makes.
	patch := map[string]any{
		"spec": map[string]any{
			"ephemeralContainers": []corev1.EphemeralContainer{container},
		},
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return "", fmt.Errorf("marshaling ephemeral container patch: %w", err)
	}

	_, err = client.CoreV1().Pods(ns).Patch(
		ctx, podName, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{}, "ephemeralcontainers")
	if err != nil {
		// A 404 on this subresource usually means the API server does not
		// serve pods/ephemeralcontainers at all — a cluster older than 1.23,
		// or with the feature gate off — rather than that the pod is gone. The
		// two are told apart by reading the pod: if it is present, the
		// subresource was what was missing, and the operator needs to be told
		// that rather than sent to look for a pod that exists.
		if apierrors.IsNotFound(err) {
			if _, getErr := client.CoreV1().Pods(ns).Get(ctx, podName, metav1.GetOptions{}); getErr == nil {
				return "", fmt.Errorf("adding ephemeral container to pod %q: %w",
					podName, ports.ErrEphemeralContainersUnsupported)
			}
		}
		return "", classify(fmt.Sprintf("adding ephemeral container to pod %q", podName), err)
	}

	return name, nil
}

// WaitForEphemeralContainerRunning blocks until the named ephemeral container
// reports Running. See ports.ManagementPort.WaitForEphemeralContainerRunning.
func (a *Adapter) WaitForEphemeralContainerRunning(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName string) error {
	client, err := a.factory.clientFor(id)
	if err != nil {
		return err
	}
	return waitEphemeralContainerRunning(ctx, client, namespace.String(), podName, containerName, ephemeralPollInterval, ephemeralReadyTimeout)
}

// waitEphemeralContainerRunning is the pollable core, with the interval and
// timeout passed in so a test can drive both the success and the timeout path
// without waiting the production minute.
func waitEphemeralContainerRunning(ctx context.Context, client kubernetes.Interface, ns, podName, containerName string, interval, timeout time.Duration) error {
	op := fmt.Sprintf("waiting for debug container %q in pod %q", containerName, podName)

	err := k8swait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		pod, err := client.CoreV1().Pods(ns).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			// A read failure here — the pod deleted, RBAC revoked — is not
			// worth retrying until the timeout: it will not become a success.
			return false, classify(op, err)
		}
		for _, status := range pod.Status.EphemeralContainerStatuses {
			if status.Name != containerName {
				continue
			}
			if status.State.Running != nil {
				return true, nil
			}
			if terminated := status.State.Terminated; terminated != nil {
				return false, fmt.Errorf("%s: it terminated before it could be used (%s)",
					op, terminatedReason(terminated))
			}
			// Waiting (pulling the image, most often): keep polling.
			return false, nil
		}
		return false, nil
	})
	if err != nil {
		if k8swait.Interrupted(err) {
			return fmt.Errorf("%s: it did not start within %s", op, timeout)
		}
		return err
	}
	return nil
}

// terminatedReason names why a container terminated, for the wait error.
func terminatedReason(state *corev1.ContainerStateTerminated) string {
	if state.Reason != "" {
		return state.Reason
	}
	return fmt.Sprintf("exit code %d", state.ExitCode)
}
