package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// ManagementService orchestrates write operations on Kubernetes resources.
//
// It sits between the Wails API and the management port, handling cross-cutting
// concerns like logging and validation. Each method is a thin wrapper around
// the port — the real work happens in the adapter.
//
// It is also where the read-only policy is enforced. That policy ORIGINATES
// on the client — an operator ticks "Read-only" for a group in OrganiseDialog,
// the frontend calls ClusterAPI.SetReadOnly, and Registry remembers it — so
// checking it again here can never be a security boundary; RBAC is the only
// thing that decides what these credentials may actually do. What it guards
// against is the UI's OWN bugs: a write control a future change forgets to
// disable, a stale cache, a row menu that outlives the toggle that should
// have hidden it. See SECURITY.md, "What PodSteer can do".
type ManagementService struct {
	management ports.ManagementPort
	registry   *Registry
	logger     *slog.Logger
}

// ManagementServiceDeps are the dependencies required to build a ManagementService.
type ManagementServiceDeps struct {
	Management ports.ManagementPort
	// Registry supplies the read-only policy — see ManagementService's own
	// doc comment. The same *Registry every other service shares, not a
	// service-local copy: a policy set through ClusterAPI.SetReadOnly must be
	// visible to every write this process makes, not just the ones issued
	// through whichever service happened to be asked first.
	Registry *Registry
	Logger   *slog.Logger
}

// NewManagementService returns a management service wired with its dependencies.
func NewManagementService(deps ManagementServiceDeps) (*ManagementService, error) {
	switch {
	case deps.Management == nil:
		return nil, errors.New("application: ManagementService requires a ManagementPort")
	case deps.Registry == nil:
		return nil, errors.New("application: ManagementService requires a Registry")
	}

	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &ManagementService{
		management: deps.Management,
		registry:   deps.Registry,
		logger:     logger.With(slog.String("service", "management")),
	}, nil
}

// ReadOnly reports whether id is currently marked read-only.
//
// Exposed alongside the enforcing methods below so a caller that wants to
// fail BEFORE doing any setup — TerminalAPI.StartSession allocates a PTY and
// starts a goroutine, which is wasted work if the session would refuse its
// first write anyway — can ask first rather than start and immediately tear
// down. It is a convenience, not a second source of truth: every write below
// checks the registry itself regardless of whether a caller checked first.
func (s *ManagementService) ReadOnly(id domain.ClusterID) bool {
	return s.registry.ReadOnly(id)
}

// refuseIfReadOnly returns ports.ErrReadOnly, wrapped with the cluster id,
// when id is marked read-only. Every mutating method below calls this first,
// before anything else — including StreamLogs's neighbours here that do NOT
// call it, because reading logs or opening a port-forward changes nothing
// about the cluster and the guard has no business refusing them.
func (s *ManagementService) refuseIfReadOnly(id domain.ClusterID) error {
	if s.registry.ReadOnly(id) {
		return fmt.Errorf("cluster %q: %w", id, ports.ErrReadOnly)
	}
	return nil
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
	if err := s.refuseIfReadOnly(ref.ClusterID); err != nil {
		return err
	}

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
	if err := s.refuseIfReadOnly(id); err != nil {
		return err
	}

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
	if err := s.refuseIfReadOnly(id); err != nil {
		return err
	}

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
	if err := s.refuseIfReadOnly(id); err != nil {
		return err
	}

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
//
// An exec session can run anything, including a write to the cluster
// disguised as a diagnostic command, so it is refused exactly like the
// methods above.
func (s *ManagementService) ExecInPod(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName string, command []string, stdin io.Reader, stdout, stderr io.Writer, tty bool) error {
	if err := s.refuseIfReadOnly(id); err != nil {
		return err
	}
	return s.management.ExecInPod(ctx, id, namespace, podName, containerName, command, stdin, stdout, stderr, tty)
}

// ExecInPodWithTTY executes a command in a pod container with full TTY and
// resize support. This enables interactive programs like top, htop, vim.
//
// An interactive shell is the least controllable write there is — anything
// typed into it can mutate the cluster — so TerminalAPI.StartSession also
// checks ReadOnly before it allocates a PTY at all; this is the check that
// still fires if a future caller ever reaches this method some other way.
func (s *ManagementService) ExecInPodWithTTY(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName string, command []string, stdin io.Reader, stdout, stderr io.Writer, sizeQueue ports.TerminalSizeQueue) error {
	if err := s.refuseIfReadOnly(id); err != nil {
		return err
	}

	s.logger.InfoContext(ctx, "starting terminal session",
		slog.String("cluster", id.String()),
		slog.String("namespace", namespace.String()),
		slog.String("pod", podName),
		slog.String("container", containerName))

	return s.management.ExecInPodWithTTY(ctx, id, namespace, podName, containerName, command, stdin, stdout, stderr, sizeQueue)
}
