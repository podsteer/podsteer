package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"k8sense/app/domain"
	"k8sense/app/ports"
)

// ManagementService orchestrates write operations on Kubernetes resources.
//
// It sits between the Wails API and the management port, handling cross-cutting
// concerns like logging and validation. Each method is a thin wrapper around
// the port — the real work happens in the adapter.
type ManagementService struct {
	management ports.ManagementPort
	logger     *slog.Logger
}

// ManagementServiceDeps are the dependencies required to build a ManagementService.
type ManagementServiceDeps struct {
	Management ports.ManagementPort
	Logger     *slog.Logger
}

// NewManagementService returns a management service wired with its dependencies.
func NewManagementService(deps ManagementServiceDeps) (*ManagementService, error) {
	if deps.Management == nil {
		return nil, errors.New("application: ManagementService requires a ManagementPort")
	}

	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &ManagementService{
		management: deps.Management,
		logger:     logger.With(slog.String("service", "management")),
	}, nil
}

// StreamLogs streams pod logs to the provided channel.
//
// The channel is closed when the stream ends. The caller must drain it.
// If containerName is empty, logs are streamed from the first container.
// If tailLines is 0, all available logs are streamed.
// If follow is true, the stream remains open for new log lines.
func (s *ManagementService) StreamLogs(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName string, containerName string, follow bool, tailLines int64, out chan<- string) error {
	s.logger.InfoContext(ctx, "streaming logs",
		slog.String("cluster", id.String()),
		slog.String("namespace", namespace.String()),
		slog.String("pod", podName),
		slog.String("container", containerName),
		slog.Bool("follow", follow),
		slog.Int64("tailLines", tailLines))

	err := s.management.StreamLogs(ctx, id, namespace, podName, containerName, follow, tailLines, out)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to stream logs",
			slog.String("cluster", id.String()),
			slog.String("pod", podName),
			slog.String("error", err.Error()))
		return err
	}

	return nil
}

// DeleteResource deletes a single resource.
func (s *ManagementService) DeleteResource(ctx context.Context, ref domain.ResourceRef) error {
	s.logger.InfoContext(ctx, "deleting resource",
		slog.String("cluster", ref.ClusterID.String()),
		slog.String("kind", ref.Kind.Kind),
		slog.String("namespace", ref.Namespace.String()),
		slog.String("name", ref.Name))

	err := s.management.DeleteResource(ctx, ref)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to delete resource",
			slog.String("cluster", ref.ClusterID.String()),
			slog.String("kind", ref.Kind.Kind),
			slog.String("name", ref.Name),
			slog.String("error", err.Error()))
		return err
	}

	return nil
}

// ScaleWorkload sets the replica count for a workload.
func (s *ManagementService) ScaleWorkload(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName, name string, replicas int32) error {
	s.logger.InfoContext(ctx, "scaling workload",
		slog.String("cluster", id.String()),
		slog.String("kind", string(kind)),
		slog.String("namespace", namespace.String()),
		slog.String("name", name),
		slog.Int("replicas", int(replicas)))

	if replicas < 0 {
		return fmt.Errorf("replicas must be non-negative, got %d", replicas)
	}

	err := s.management.ScaleWorkload(ctx, id, kind, namespace, name, replicas)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to scale workload",
			slog.String("cluster", id.String()),
			slog.String("kind", string(kind)),
			slog.String("name", name),
			slog.String("error", err.Error()))
		return err
	}

	return nil
}

// RestartRollout triggers a rolling restart of a Deployment or StatefulSet.
func (s *ManagementService) RestartRollout(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName, name string) error {
	s.logger.InfoContext(ctx, "restarting rollout",
		slog.String("cluster", id.String()),
		slog.String("kind", string(kind)),
		slog.String("namespace", namespace.String()),
		slog.String("name", name))

	err := s.management.RestartRollout(ctx, id, kind, namespace, name)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to restart rollout",
			slog.String("cluster", id.String()),
			slog.String("kind", string(kind)),
			slog.String("name", name),
			slog.String("error", err.Error()))
		return err
	}

	return nil
}

// UpdateResource applies a YAML manifest to the cluster.
func (s *ManagementService) UpdateResource(ctx context.Context, id domain.ClusterID, manifest string) error {
	s.logger.InfoContext(ctx, "updating resource",
		slog.String("cluster", id.String()),
		slog.Int("manifestLength", len(manifest)))

	if manifest == "" {
		return errors.New("manifest cannot be empty")
	}

	err := s.management.UpdateResource(ctx, id, manifest)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to update resource",
			slog.String("cluster", id.String()),
			slog.String("error", err.Error()))
		return err
	}

	return nil
}

// ExecInPod executes a command in a pod container.
func (s *ManagementService) ExecInPod(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName string, command []string, stdin io.Reader, stdout, stderr io.Writer, tty bool) error {
	return s.management.ExecInPod(ctx, id, namespace, podName, containerName, command, stdin, stdout, stderr, tty)
}

// ExecInPodWithTTY executes a command in a pod container with full TTY and
// resize support. This enables interactive programs like top, htop, vim.
func (s *ManagementService) ExecInPodWithTTY(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName string, command []string, stdin io.Reader, stdout, stderr io.Writer, sizeQueue ports.TerminalSizeQueue) error {
	s.logger.InfoContext(ctx, "starting terminal session",
		slog.String("cluster", id.String()),
		slog.String("namespace", namespace.String()),
		slog.String("pod", podName),
		slog.String("container", containerName))

	return s.management.ExecInPodWithTTY(ctx, id, namespace, podName, containerName, command, stdin, stdout, stderr, sizeQueue)
}
