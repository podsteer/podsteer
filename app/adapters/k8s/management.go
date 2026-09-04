package k8s

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// maxLogLineBytes is the longest single log line the scanner will accept.
//
// One megabyte, which is generous for a log line and small enough that a
// process emitting a multi-megabyte blob cannot exhaust memory here.
const maxLogLineBytes = 1024 * 1024

// StreamLogs streams pod logs to the provided channel.
//
// The channel is closed when the stream ends (pod terminates, context
// cancelled, or an error occurs). The caller must drain the channel.
func (a *Adapter) StreamLogs(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName string, containerName string, opts domain.LogOptions, out chan<- string) error {
	defer close(out)

	client, err := a.factory.clientFor(id)
	if err != nil {
		return err
	}

	logOpts := podLogOptions(containerName, opts)

	req := client.CoreV1().Pods(namespace.String()).GetLogs(podName, logOpts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return classify("streaming logs", err)
	}
	//nolint:errcheck // closing a finished read-only stream has no recoverable failure
	defer stream.Close()

	scanner := bufio.NewScanner(stream)
	// Increase buffer size for long log lines (some apps emit multi-KB lines).
	scanner.Buffer(make([]byte, 0, 64*1024), maxLogLineBytes)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- scanner.Text():
		}
	}

	// A line over the cap ends the whole stream, not just that line, and
	// bufio's own wording ("token too long") says nothing about which token
	// or what the limit is. Named here so the frontend can show an operator
	// why a pod appeared to stop talking.
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return fmt.Errorf(
				"streaming logs: a single log line exceeded %d MiB, which ends the stream",
				maxLogLineBytes/(1024*1024))
		}
		return err
	}
	return nil
}

// podLogOptions builds the corev1 request from domain.LogOptions.
//
// A separate, pure function rather than inline in StreamLogs: it is the one
// piece of this method with no I/O in it, and keeping it out of the loop that
// opens a real connection is what lets a test assert the translation without
// needing a stream to complete.
//
// TailLines, SinceSeconds and LimitBytes are pointer fields on
// corev1.PodLogOptions because zero is a meaningful request there ("give me
// the last 0 lines" is not one anyone means), so each is only set when the
// domain value is positive — the same "0 means unset" convention
// domain.LogOptions documents on every one of them.
func podLogOptions(containerName string, opts domain.LogOptions) *corev1.PodLogOptions {
	logOpts := &corev1.PodLogOptions{
		Container:  containerName,
		Follow:     opts.Follow,
		Previous:   opts.Previous,
		Timestamps: opts.Timestamps,
	}

	if opts.TailLines > 0 {
		logOpts.TailLines = &opts.TailLines
	}
	if opts.SinceSeconds > 0 {
		logOpts.SinceSeconds = &opts.SinceSeconds
	}
	if opts.LimitBytes > 0 {
		logOpts.LimitBytes = &opts.LimitBytes
	}

	return logOpts
}

// ExecInPod executes a command in a pod container.
//
// This uses the Kubernetes exec API to run commands inside a running container.
// The stdin, stdout, and stderr are streamed through the provided readers/writers.
func (a *Adapter) ExecInPod(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName string, command []string, stdin io.Reader, stdout, stderr io.Writer, tty bool) error {
	client, err := a.factory.clientFor(id)
	if err != nil {
		return err
	}

	// Build the exec request
	req := client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace.String()).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   command,
			Stdin:     stdin != nil,
			Stdout:    stdout != nil,
			Stderr:    stderr != nil,
			TTY:       tty,
		}, scheme.ParameterCodec)

	// Get the REST config for the cluster
	config, err := a.factory.restConfig(id)
	if err != nil {
		return fmt.Errorf("getting REST config: %w", err)
	}

	exec, err := newRemoteCommand(ctx, config, req)
	if err != nil {
		return err
	}

	// Execute the command
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
		Tty:    tty,
	})
	if err != nil {
		return fmt.Errorf("executing command: %w", err)
	}

	return nil
}

// DeleteResource deletes a single resource.
func (a *Adapter) DeleteResource(ctx context.Context, ref domain.ResourceRef) error {
	// Whatever happens next, the lists this cluster has cached are no longer
	// the truth the operator just asked for. Dropped on entry rather than on
	// success, because a delete that times out has usually still happened.
	defer a.forgetReads(ref.ClusterID)

	client, err := a.factory.clientFor(ref.ClusterID)
	if err != nil {
		return err
	}

	kind := ref.Kind
	ns := ref.Namespace.String()
	name := ref.Name

	var deleteErr error

	switch kind.Group {
	case "":
		// Core API group
		switch kind.Kind {
		case "Pod":
			deleteErr = client.CoreV1().Pods(ns).Delete(ctx, name, metav1.DeleteOptions{})
		case "Service":
			deleteErr = client.CoreV1().Services(ns).Delete(ctx, name, metav1.DeleteOptions{})
		case "ConfigMap":
			deleteErr = client.CoreV1().ConfigMaps(ns).Delete(ctx, name, metav1.DeleteOptions{})
		case "Secret":
			deleteErr = client.CoreV1().Secrets(ns).Delete(ctx, name, metav1.DeleteOptions{})
		case "Namespace":
			deleteErr = client.CoreV1().Namespaces().Delete(ctx, name, metav1.DeleteOptions{})
		case "Node":
			deleteErr = client.CoreV1().Nodes().Delete(ctx, name, metav1.DeleteOptions{})
		case "PersistentVolumeClaim":
			deleteErr = client.CoreV1().PersistentVolumeClaims(ns).Delete(ctx, name, metav1.DeleteOptions{})
		case "PersistentVolume":
			deleteErr = client.CoreV1().PersistentVolumes().Delete(ctx, name, metav1.DeleteOptions{})
		case "ServiceAccount":
			deleteErr = client.CoreV1().ServiceAccounts(ns).Delete(ctx, name, metav1.DeleteOptions{})
		default:
			return fmt.Errorf("unsupported core resource kind: %s", kind.Kind)
		}
	case "apps":
		switch kind.Kind {
		case "Deployment":
			deleteErr = client.AppsV1().Deployments(ns).Delete(ctx, name, metav1.DeleteOptions{})
		case "StatefulSet":
			deleteErr = client.AppsV1().StatefulSets(ns).Delete(ctx, name, metav1.DeleteOptions{})
		case "DaemonSet":
			deleteErr = client.AppsV1().DaemonSets(ns).Delete(ctx, name, metav1.DeleteOptions{})
		case "ReplicaSet":
			deleteErr = client.AppsV1().ReplicaSets(ns).Delete(ctx, name, metav1.DeleteOptions{})
		default:
			return fmt.Errorf("unsupported apps resource kind: %s", kind.Kind)
		}
	case "batch":
		switch kind.Kind {
		case "Job":
			deleteErr = client.BatchV1().Jobs(ns).Delete(ctx, name, metav1.DeleteOptions{})
		case "CronJob":
			deleteErr = client.BatchV1().CronJobs(ns).Delete(ctx, name, metav1.DeleteOptions{})
		default:
			return fmt.Errorf("unsupported batch resource kind: %s", kind.Kind)
		}
	case "networking.k8s.io":
		switch kind.Kind {
		case "Ingress":
			deleteErr = client.NetworkingV1().Ingresses(ns).Delete(ctx, name, metav1.DeleteOptions{})
		case "NetworkPolicy":
			deleteErr = client.NetworkingV1().NetworkPolicies(ns).Delete(ctx, name, metav1.DeleteOptions{})
		default:
			return fmt.Errorf("unsupported networking resource kind: %s", kind.Kind)
		}
	case "rbac.authorization.k8s.io":
		switch kind.Kind {
		case "Role":
			deleteErr = client.RbacV1().Roles(ns).Delete(ctx, name, metav1.DeleteOptions{})
		case "RoleBinding":
			deleteErr = client.RbacV1().RoleBindings(ns).Delete(ctx, name, metav1.DeleteOptions{})
		case "ClusterRole":
			deleteErr = client.RbacV1().ClusterRoles().Delete(ctx, name, metav1.DeleteOptions{})
		case "ClusterRoleBinding":
			deleteErr = client.RbacV1().ClusterRoleBindings().Delete(ctx, name, metav1.DeleteOptions{})
		default:
			return fmt.Errorf("unsupported rbac resource kind: %s", kind.Kind)
		}
	default:
		return fmt.Errorf("unsupported API group: %s", kind.Group)
	}

	if deleteErr != nil {
		return classify("deleting resource", deleteErr)
	}

	return nil
}

// ScaleWorkload sets the replica count for a workload.
func (a *Adapter) ScaleWorkload(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName, name string, replicas int32) error {
	defer a.forgetReads(id)

	client, err := a.factory.clientFor(id)
	if err != nil {
		return err
	}

	ns := namespace.String()

	switch kind {
	case domain.WorkloadDeployment:
		scale, err := client.AppsV1().Deployments(ns).GetScale(ctx, name, metav1.GetOptions{})
		if err != nil {
			return classify("getting deployment scale", err)
		}
		scale.Spec.Replicas = replicas
		_, err = client.AppsV1().Deployments(ns).UpdateScale(ctx, name, scale, metav1.UpdateOptions{})
		if err != nil {
			return classify("updating deployment scale", err)
		}

	case domain.WorkloadStatefulSet:
		scale, err := client.AppsV1().StatefulSets(ns).GetScale(ctx, name, metav1.GetOptions{})
		if err != nil {
			return classify("getting statefulset scale", err)
		}
		scale.Spec.Replicas = replicas
		_, err = client.AppsV1().StatefulSets(ns).UpdateScale(ctx, name, scale, metav1.UpdateOptions{})
		if err != nil {
			return classify("updating statefulset scale", err)
		}

	case domain.WorkloadReplicaSet:
		scale, err := client.AppsV1().ReplicaSets(ns).GetScale(ctx, name, metav1.GetOptions{})
		if err != nil {
			return classify("getting replicaset scale", err)
		}
		scale.Spec.Replicas = replicas
		_, err = client.AppsV1().ReplicaSets(ns).UpdateScale(ctx, name, scale, metav1.UpdateOptions{})
		if err != nil {
			return classify("updating replicaset scale", err)
		}

	default:
		return fmt.Errorf("scaling not supported for kind: %s", kind)
	}

	return nil
}

// RestartRollout triggers a rolling restart by patching the pod template annotation.
func (a *Adapter) RestartRollout(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName, name string) error {
	defer a.forgetReads(id)

	client, err := a.factory.clientFor(id)
	if err != nil {
		return err
	}

	ns := namespace.String()
	restartedAt := time.Now().Format(time.RFC3339)

	// Patch the pod template annotation to trigger a rollout.
	patch := map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"metadata": map[string]any{
					"annotations": map[string]string{
						"kubectl.kubernetes.io/restartedAt": restartedAt,
					},
				},
			},
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshaling restart patch: %w", err)
	}

	switch kind {
	case domain.WorkloadDeployment:
		_, err = client.AppsV1().Deployments(ns).Patch(ctx, name, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
		if err != nil {
			return classify("restarting deployment", err)
		}

	case domain.WorkloadStatefulSet:
		_, err = client.AppsV1().StatefulSets(ns).Patch(ctx, name, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
		if err != nil {
			return classify("restarting statefulset", err)
		}

	case domain.WorkloadDaemonSet:
		_, err = client.AppsV1().DaemonSets(ns).Patch(ctx, name, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
		if err != nil {
			return classify("restarting daemonset", err)
		}

	default:
		return fmt.Errorf("restart not supported for kind: %s", kind)
	}

	return nil
}

// manualInstantiateAnnotation marks a Job as created outside its CronJob's
// schedule, the same annotation `kubectl create job --from=cronjob` sets.
const manualInstantiateAnnotation = "cronjob.kubernetes.io/instantiate"

// manualJobRandomSuffixLength is how many random characters follow
// "-manual-" in a manually triggered Job's name.
const manualJobRandomSuffixLength = 5

// maxObjectNameLength is Kubernetes' limit on an object name. A Job's name
// feeds its pods' names, so a manually triggered Job must stay under it even
// when the CronJob it came from is named right up to the edge.
const maxObjectNameLength = 63

// TriggerCronJob creates a Job from a CronJob's template right now, the way
// `kubectl create job --from=cronjob/NAME` does.
func (a *Adapter) TriggerCronJob(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name string) (string, error) {
	defer a.forgetReads(id)

	client, err := a.factory.clientFor(id)
	if err != nil {
		return "", err
	}

	ns := namespace.String()

	// A suspended CronJob may still be triggered — kubectl allows exactly
	// this, and an operator reaching for "run now" wants one run regardless
	// of the schedule, so suspension is never checked here.
	cronJob, err := client.BatchV1().CronJobs(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", classify("creating job from cronjob", err)
	}

	labels := make(map[string]string, len(cronJob.Spec.JobTemplate.Labels))
	maps.Copy(labels, cronJob.Spec.JobTemplate.Labels)

	annotations := make(map[string]string, len(cronJob.Spec.JobTemplate.Annotations)+1)
	maps.Copy(annotations, cronJob.Spec.JobTemplate.Annotations)
	annotations[manualInstantiateAnnotation] = "manual"

	controller := true
	blockOwnerDeletion := true

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:        manualJobName(name),
			Namespace:   ns,
			Labels:      labels,
			Annotations: annotations,
			// The single owner reference is what makes the CronJob controller
			// adopt this Job: count it as active, apply
			// successfulJobsHistoryLimit/failedJobsHistoryLimit to it, and show
			// it under the CronJob exactly as a scheduled run would.
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion:         "batch/v1",
				Kind:               "CronJob",
				Name:               cronJob.Name,
				UID:                cronJob.UID,
				Controller:         &controller,
				BlockOwnerDeletion: &blockOwnerDeletion,
			}},
		},
		Spec: cronJob.Spec.JobTemplate.Spec,
	}

	created, err := client.BatchV1().Jobs(ns).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return "", classify("creating job from cronjob", err)
	}

	return created.Name, nil
}

// manualJobName builds the name a manually triggered Job gets:
// <cronjob>-manual-<5 random lowercase alphanumerics>, with the CronJob's own
// name truncated so the result stays within maxObjectNameLength. Kubernetes
// rejects an over-length name outright rather than truncating it, and a Job's
// name feeds its pods' names, so this has to be done before Create rather
// than left to the API server.
func manualJobName(cronJobName string) string {
	suffix := "-manual-" + utilrand.String(manualJobRandomSuffixLength)

	prefix := cronJobName
	if maxPrefix := maxObjectNameLength - len(suffix); len(prefix) > maxPrefix {
		prefix = prefix[:maxPrefix]
	}

	return prefix + suffix
}

// SuspendWorkload sets or clears spec.suspend on a CronJob or a Job.
func (a *Adapter) SuspendWorkload(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName, name string, suspend bool) error {
	defer a.forgetReads(id)

	client, err := a.factory.clientFor(id)
	if err != nil {
		return err
	}

	ns := namespace.String()

	patch := map[string]any{
		"spec": map[string]any{
			"suspend": suspend,
		},
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshaling suspend patch: %w", err)
	}

	switch kind {
	case domain.WorkloadCronJob:
		_, err = client.BatchV1().CronJobs(ns).Patch(ctx, name, types.MergePatchType, patchBytes, metav1.PatchOptions{})
		if err != nil {
			return classify("suspending cronjob", err)
		}

	case domain.WorkloadJob:
		_, err = client.BatchV1().Jobs(ns).Patch(ctx, name, types.MergePatchType, patchBytes, metav1.PatchOptions{})
		if err != nil {
			return classify("suspending job", err)
		}

	default:
		// Defence in depth: the application layer rejects any other kind
		// before this is reached, mirroring ScaleWorkload and RestartRollout's
		// own kind switches above.
		return fmt.Errorf("suspend not supported for kind: %s", kind)
	}

	return nil
}

// SetImage sets one container's image on a Deployment, StatefulSet or
// DaemonSet's pod template.
//
// A STRATEGIC MERGE PATCH, not a JSON merge patch. spec.template.spec.
// containers is a LIST, and a JSON merge patch replaces a list wholesale —
// sending one container would delete every other container in the pod. The
// strategic merge patch instead merges list entries by their `name` key
// (containers carries a `patchMergeKey` struct tag naming it, which is what
// the apiserver uses to tell "replace the list" from "merge into it"), so
// this names exactly the one container being changed and every other
// container, its env, volumes and probes included, is left exactly as it
// was — the same way `kubectl set image` does it.
func (a *Adapter) SetImage(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName, name, container, image string, initContainer bool) error {
	defer a.forgetReads(id)

	client, err := a.factory.clientFor(id)
	if err != nil {
		return err
	}

	ns := namespace.String()

	containerField := "containers"
	if initContainer {
		containerField = "initContainers"
	}

	patch := map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					containerField: []map[string]any{
						{"name": container, "image": image},
					},
				},
			},
		},
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshaling set image patch: %w", err)
	}

	switch kind {
	case domain.WorkloadDeployment:
		_, err = client.AppsV1().Deployments(ns).Patch(ctx, name, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
		if err != nil {
			return classify("setting deployment image", err)
		}

	case domain.WorkloadStatefulSet:
		_, err = client.AppsV1().StatefulSets(ns).Patch(ctx, name, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
		if err != nil {
			return classify("setting statefulset image", err)
		}

	case domain.WorkloadDaemonSet:
		_, err = client.AppsV1().DaemonSets(ns).Patch(ctx, name, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
		if err != nil {
			return classify("setting daemonset image", err)
		}

	default:
		// Defence in depth: the application layer rejects any other kind
		// before this is reached, mirroring SuspendWorkload's own switch above.
		return fmt.Errorf("set image not supported for kind: %s", kind)
	}

	return nil
}

// SetSecretKey writes one key of one Secret via a JSON merge patch on `data`.
//
// NO GET IS MADE. Reading the whole Secret to write one key is exactly the
// pattern RevealSecretKey — and the CLAUDE.md section above it — exists to
// avoid: this patches blind, the same way `kubectl patch secret --type
// merge` does. json.Marshal encodes a []byte as base64 automatically, which
// is precisely the wire format the Secret `data` field expects, so nothing
// here does its own encoding — mirroring how RevealSecretKey relies on
// client-go to decode rather than doing it by hand.
func (a *Adapter) SetSecretKey(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name, key string, value []byte) error {
	defer a.forgetReads(id)

	if !domain.ValidDataKey(key) {
		return fmt.Errorf("writing key %q of secret %q: %w", key, name, domain.ErrInvalidKey)
	}

	client, err := a.factory.clientFor(id)
	if err != nil {
		return err
	}

	patch := map[string]any{
		"data": map[string]any{
			key: value,
		},
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshaling secret key patch: %w", err)
	}

	_, err = client.CoreV1().Secrets(namespace.String()).Patch(ctx, name, types.MergePatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		return classify("writing key of secret", err)
	}

	return nil
}

// SetConfigMapKey writes one key of one ConfigMap via a JSON merge patch on
// `data`.
//
// ONE GET FIRST, CONFIGMAPS ONLY. A ConfigMap key lives in either `data`
// (text) or `binaryData` (base64), and merging a text value into `data`
// while the key currently lives in `binaryData` would not edit it — it would
// leave the binary entry in place and add a second, text one under the same
// key name in a different field, which is not what a save button means. The
// existing object is read once to refuse that case; a Secret has no such
// split (everything is `data`), which is why SetSecretKey makes no read at
// all.
func (a *Adapter) SetConfigMapKey(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name, key, value string) error {
	defer a.forgetReads(id)

	if !domain.ValidDataKey(key) {
		return fmt.Errorf("writing key %q of configmap %q: %w", key, name, domain.ErrInvalidKey)
	}

	client, err := a.factory.clientFor(id)
	if err != nil {
		return err
	}

	ns := namespace.String()

	existing, err := client.CoreV1().ConfigMaps(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return classify("writing key of configmap", err)
	}
	if _, inBinary := existing.BinaryData[key]; inBinary {
		return fmt.Errorf("writing key %q of configmap %q: %w: key holds binary data, not text",
			key, name, domain.ErrInvalidKey)
	}

	patch := map[string]any{
		"data": map[string]any{
			key: value,
		},
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshaling configmap key patch: %w", err)
	}

	_, err = client.CoreV1().ConfigMaps(ns).Patch(ctx, name, types.MergePatchType, patchBytes, metav1.PatchOptions{})
	if err != nil {
		return classify("writing key of configmap", err)
	}

	return nil
}

// terminalSizeQueueAdapter wraps ports.TerminalSizeQueue to satisfy
// remotecommand.TerminalSizeQueue.
type terminalSizeQueueAdapter struct {
	queue ports.TerminalSizeQueue
}

func (a *terminalSizeQueueAdapter) Next() *remotecommand.TerminalSize {
	size := a.queue.Next()
	if size == nil {
		return nil
	}
	return &remotecommand.TerminalSize{
		Width:  size.Width,
		Height: size.Height,
	}
}

// ExecInPodWithTTY executes a command in a pod container with full TTY support
// and terminal resize handling.
//
// This is the enterprise-grade terminal implementation. It allocates a PTY,
// streams stdin/stdout bidirectionally, and forwards window resize events so
// interactive programs (top, htop, vim, less) render correctly.
func (a *Adapter) ExecInPodWithTTY(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName string, command []string, stdin io.Reader, stdout, stderr io.Writer, sizeQueue ports.TerminalSizeQueue) error {
	client, err := a.factory.clientFor(id)
	if err != nil {
		return err
	}

	// Build the exec request with TTY enabled
	req := client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace.String()).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   command,
			Stdin:     stdin != nil,
			Stdout:    stdout != nil,
			Stderr:    stderr != nil,
			TTY:       true,
		}, scheme.ParameterCodec)

	// Get the REST config for the cluster
	config, err := a.factory.restConfig(id)
	if err != nil {
		return fmt.Errorf("getting REST config: %w", err)
	}

	exec, err := newRemoteCommand(ctx, config, req)
	if err != nil {
		return err
	}

	// Wrap the size queue to satisfy remotecommand.TerminalSizeQueue
	var tsq remotecommand.TerminalSizeQueue
	if sizeQueue != nil {
		tsq = &terminalSizeQueueAdapter{queue: sizeQueue}
	}

	// Execute with TTY and resize support
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:             stdin,
		Stdout:            stdout,
		Stderr:            stderr,
		Tty:               true,
		TerminalSizeQueue: tsq,
	})
	if err != nil {
		return fmt.Errorf("terminal exec: %w", err)
	}

	return nil
}

// newRemoteCommand builds the SPDY executor that streams both an exec and an
// attach session — the two subresources differ only in the request VersionedParams
// carries, never in how the resulting stream is driven, so ExecInPodWithTTY and
// AttachToPod share this rather than each constructing their own.
//
// ctx is checked before the (cheap, but not free) executor is built: both
// callers stream on the same context immediately afterwards, so a context
// already cancelled — a session stopped between the caller's checks and this
// call — is reported here rather than after a connection was opened for it.
func newRemoteCommand(ctx context.Context, config *rest.Config, req *rest.Request) (remotecommand.Executor, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return nil, fmt.Errorf("creating SPDY executor: %w", err)
	}
	return exec, nil
}

// containerSpec finds the named container's own declaration in pod's spec,
// searching ordinary, then (restartable or not) init, then ephemeral
// containers — everywhere a container name can live and everywhere the
// attach subresource itself is willing to look.
func containerSpec(pod *corev1.Pod, name string) (tty, stdin, found bool) {
	for _, c := range pod.Spec.Containers {
		if c.Name == name {
			return c.TTY, c.Stdin, true
		}
	}
	for _, c := range pod.Spec.InitContainers {
		if c.Name == name {
			return c.TTY, c.Stdin, true
		}
	}
	for _, c := range pod.Spec.EphemeralContainers {
		if c.Name == name {
			return c.TTY, c.Stdin, true
		}
	}
	return false, false, false
}

// AttachToPod connects to a container's own running process — PID 1,
// whatever the image's ENTRYPOINT/CMD started — via the pod's attach
// subresource, in place of ExecInPodWithTTY's exec subresource which starts
// a new one. See ports.ManagementPort for the full contract.
func (a *Adapter) AttachToPod(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName string, stdin io.Reader, stdout, stderr io.Writer, sizeQueue ports.TerminalSizeQueue) error {
	client, err := a.factory.clientFor(id)
	if err != nil {
		return err
	}

	ns := namespace.String()

	// Read the pod once so a container whose own spec has no tty/stdin is
	// refused HERE, with a message naming the fields to change, rather than
	// failing on the server once the PTY negotiation begins — see
	// domain.ErrContainerNotAttachable's doc comment.
	pod, err := client.CoreV1().Pods(ns).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return classify("reading pod for attach", err)
	}

	tty, hasStdin, found := containerSpec(pod, containerName)
	if !found {
		return fmt.Errorf("attaching to pod %q: %w: no container named %q",
			podName, ports.ErrNotFound, containerName)
	}
	if !tty || !hasStdin {
		return fmt.Errorf("attaching to pod %q container %q: %w",
			podName, containerName, domain.ErrContainerNotAttachable)
	}

	req := client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(ns).
		SubResource("attach").
		VersionedParams(&corev1.PodAttachOptions{
			Container: containerName,
			Stdin:     stdin != nil,
			Stdout:    stdout != nil,
			Stderr:    stderr != nil,
			TTY:       true,
		}, scheme.ParameterCodec)

	config, err := a.factory.restConfig(id)
	if err != nil {
		return fmt.Errorf("getting REST config: %w", err)
	}

	exec, err := newRemoteCommand(ctx, config, req)
	if err != nil {
		return err
	}

	var tsq remotecommand.TerminalSizeQueue
	if sizeQueue != nil {
		tsq = &terminalSizeQueueAdapter{queue: sizeQueue}
	}

	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:             stdin,
		Stdout:            stdout,
		Stderr:            stderr,
		Tty:               true,
		TerminalSizeQueue: tsq,
	})
	if err != nil {
		return fmt.Errorf("attaching to pod: %w", err)
	}

	return nil
}

// Ensure Adapter implements ManagementPort.
var _ ports.ManagementPort = (*Adapter)(nil)
