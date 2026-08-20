package k8s

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/yaml"

	"podsteer/app/domain"
	"podsteer/app/ports"
)

// StreamLogs streams pod logs to the provided channel.
//
// The channel is closed when the stream ends (pod terminates, context
// cancelled, or an error occurs). The caller must drain the channel.
func (a *Adapter) StreamLogs(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName string, containerName string, follow bool, tailLines int64, out chan<- string) error {
	defer close(out)

	client, err := a.factory.clientFor(id)
	if err != nil {
		return err
	}

	opts := &corev1.PodLogOptions{
		Container:  containerName,
		Follow:     follow,
		TailLines:  &tailLines,
		Timestamps: true,
	}

	if tailLines == 0 {
		opts.TailLines = nil
	}

	req := client.CoreV1().Pods(namespace.String()).GetLogs(podName, opts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return classify("streaming logs", err)
	}
	//nolint:errcheck // closing a finished read-only stream has no recoverable failure
	defer stream.Close()

	scanner := bufio.NewScanner(stream)
	// Increase buffer size for long log lines (some apps emit multi-KB lines).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- scanner.Text():
		}
	}

	return scanner.Err()
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

	// Create the executor
	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("creating executor: %w", err)
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
	client, err := a.factory.clientFor(id)
	if err != nil {
		return err
	}

	ns := namespace.String()
	restartedAt := time.Now().Format(time.RFC3339)

	// Patch the pod template annotation to trigger a rollout.
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
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

// UpdateResource applies a YAML manifest to the cluster.
func (a *Adapter) UpdateResource(ctx context.Context, id domain.ClusterID, manifest string) error {
	client, err := a.factory.clientFor(id)
	if err != nil {
		return err
	}

	// Decode the manifest to determine the resource type.
	decode := scheme.Codecs.UniversalDeserializer().Decode
	obj, gvk, err := decode([]byte(manifest), nil, nil)
	if err != nil {
		// Try YAML to JSON conversion first.
		jsonBytes, yamlErr := yaml.YAMLToJSON([]byte(manifest))
		if yamlErr != nil {
			return fmt.Errorf("invalid manifest: %w (yaml error: %v)", err, yamlErr)
		}
		obj, gvk, err = decode(jsonBytes, nil, nil)
		if err != nil {
			return fmt.Errorf("invalid manifest after yaml conversion: %w", err)
		}
	}

	// Apply based on the resource type.
	switch gvk.Kind {
	case "Pod":
		pod := obj.(*corev1.Pod)
		existing, err := client.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
		if err != nil {
			// Create if not found.
			_, err = client.CoreV1().Pods(pod.Namespace).Create(ctx, pod, metav1.CreateOptions{})
			if err != nil {
				return classify("creating pod", err)
			}
		} else {
			// Update existing.
			pod.ResourceVersion = existing.ResourceVersion
			_, err = client.CoreV1().Pods(pod.Namespace).Update(ctx, pod, metav1.UpdateOptions{})
			if err != nil {
				return classify("updating pod", err)
			}
		}

	case "Deployment":
		deployment := obj.(*appsv1.Deployment)
		existing, err := client.AppsV1().Deployments(deployment.Namespace).Get(ctx, deployment.Name, metav1.GetOptions{})
		if err != nil {
			_, err = client.AppsV1().Deployments(deployment.Namespace).Create(ctx, deployment, metav1.CreateOptions{})
			if err != nil {
				return classify("creating deployment", err)
			}
		} else {
			deployment.ResourceVersion = existing.ResourceVersion
			_, err = client.AppsV1().Deployments(deployment.Namespace).Update(ctx, deployment, metav1.UpdateOptions{})
			if err != nil {
				return classify("updating deployment", err)
			}
		}

	case "ConfigMap":
		cm := obj.(*corev1.ConfigMap)
		existing, err := client.CoreV1().ConfigMaps(cm.Namespace).Get(ctx, cm.Name, metav1.GetOptions{})
		if err != nil {
			_, err = client.CoreV1().ConfigMaps(cm.Namespace).Create(ctx, cm, metav1.CreateOptions{})
			if err != nil {
				return classify("creating configmap", err)
			}
		} else {
			cm.ResourceVersion = existing.ResourceVersion
			_, err = client.CoreV1().ConfigMaps(cm.Namespace).Update(ctx, cm, metav1.UpdateOptions{})
			if err != nil {
				return classify("updating configmap", err)
			}
		}

	case "Secret":
		secret := obj.(*corev1.Secret)
		existing, err := client.CoreV1().Secrets(secret.Namespace).Get(ctx, secret.Name, metav1.GetOptions{})
		if err != nil {
			_, err = client.CoreV1().Secrets(secret.Namespace).Create(ctx, secret, metav1.CreateOptions{})
			if err != nil {
				return classify("creating secret", err)
			}
		} else {
			secret.ResourceVersion = existing.ResourceVersion
			_, err = client.CoreV1().Secrets(secret.Namespace).Update(ctx, secret, metav1.UpdateOptions{})
			if err != nil {
				return classify("updating secret", err)
			}
		}

	case "Service":
		svc := obj.(*corev1.Service)
		existing, err := client.CoreV1().Services(svc.Namespace).Get(ctx, svc.Name, metav1.GetOptions{})
		if err != nil {
			_, err = client.CoreV1().Services(svc.Namespace).Create(ctx, svc, metav1.CreateOptions{})
			if err != nil {
				return classify("creating service", err)
			}
		} else {
			svc.ResourceVersion = existing.ResourceVersion
			svc.Spec.ClusterIP = existing.Spec.ClusterIP // Preserve ClusterIP
			_, err = client.CoreV1().Services(svc.Namespace).Update(ctx, svc, metav1.UpdateOptions{})
			if err != nil {
				return classify("updating service", err)
			}
		}

	default:
		return fmt.Errorf("update not supported for kind: %s", gvk.Kind)
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

	// Create the executor
	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("creating SPDY executor: %w", err)
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

// Ensure Adapter implements ManagementPort.
var _ ports.ManagementPort = (*Adapter)(nil)
