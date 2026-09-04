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

// TriggerCronJob creates a Job from a CronJob's template right now, outside
// its schedule.
func (s *ManagementService) TriggerCronJob(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name string) (string, error) {
	s.logger.InfoContext(ctx, "triggering cronjob",
		slog.String("cluster", id.String()),
		slog.String("namespace", namespace.String()),
		slog.String("name", name))

	jobName, err := s.management.TriggerCronJob(ctx, id, namespace, name)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to trigger cronjob",
			slog.String("cluster", id.String()),
			slog.String("name", name),
			slog.String("error", err.Error()))
		return "", err
	}

	return jobName, nil
}

// SuspendWorkload sets or clears suspend on a CronJob or a Job.
//
// Only those two kinds support it, and that is checked HERE rather than left
// to the adapter — mirroring how ScaleWorkload validates its replica count
// before the adapter is ever reached, so an unsupported kind never costs a
// round trip to the cluster to be told no.
func (s *ManagementService) SuspendWorkload(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName, name string, suspend bool) error {
	s.logger.InfoContext(ctx, "suspending workload",
		slog.String("cluster", id.String()),
		slog.String("kind", string(kind)),
		slog.String("namespace", namespace.String()),
		slog.String("name", name),
		slog.Bool("suspend", suspend))

	if kind != domain.WorkloadCronJob && kind != domain.WorkloadJob {
		return fmt.Errorf("%w: suspend is only supported for CronJobs and Jobs, got %s",
			domain.ErrUnsupportedWorkloadKind, kind)
	}

	err := s.management.SuspendWorkload(ctx, id, kind, namespace, name, suspend)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to suspend workload",
			slog.String("cluster", id.String()),
			slog.String("kind", string(kind)),
			slog.String("name", name),
			slog.String("error", err.Error()))
		return err
	}

	return nil
}

// UpdateResource applies a YAML manifest of any kind to the cluster.
//
// dryRun is NOT gated by the read-only check below. A dry run asks the API
// server to validate the request and persists nothing — see
// ports.ManagementPort.UpdateResource — so it is exactly as safe against a
// read-only cluster as any other read, and refusing it would block the one
// action ("Validate") an operator on a read-only cluster is otherwise
// invited to take before asking someone else to apply the change for real. A
// real apply (dryRun false) is refused exactly like every other write here.
func (s *ManagementService) UpdateResource(ctx context.Context, id domain.ClusterID, manifest string, dryRun bool) (domain.ApplyOutcome, error) {
	if !dryRun {
		if err := s.refuseIfReadOnly(id); err != nil {
			return domain.ApplyOutcome{}, err
		}
	}

	s.logger.InfoContext(ctx, "updating resource",
		slog.String("cluster", id.String()),
		slog.Int("manifestLength", len(manifest)),
		slog.Bool("dryRun", dryRun))

	if manifest == "" {
		return domain.ApplyOutcome{}, errors.New("manifest cannot be empty")
	}

	outcome, err := s.management.UpdateResource(ctx, id, manifest, dryRun)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to update resource",
			slog.String("cluster", id.String()),
			slog.Bool("dryRun", dryRun),
			slog.String("error", err.Error()))
		return domain.ApplyOutcome{}, err
	}

	return outcome, nil
}

// SetImage sets one container's image on a Deployment, StatefulSet or
// DaemonSet.
//
// Only those three kinds support it, and that is checked HERE rather than
// left to the adapter — mirroring SuspendWorkload's own kind check — so an
// unsupported kind never costs a round trip to the cluster to be told no. The
// image is checked with domain.ValidImageReference for the same reason
// SetSecretKey checks domain.ValidDataKey: a malformed value becomes a local,
// immediate refusal instead of a 422 from the API server naming a field the
// operator cannot see.
func (s *ManagementService) SetImage(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName, name, container, image string, initContainer bool) error {
	if err := s.refuseIfReadOnly(id); err != nil {
		return err
	}

	s.logger.InfoContext(ctx, "setting image",
		slog.String("cluster", id.String()),
		slog.String("kind", string(kind)),
		slog.String("namespace", namespace.String()),
		slog.String("name", name),
		slog.String("container", container),
		slog.String("image", image),
		slog.Bool("initContainer", initContainer))

	if kind != domain.WorkloadDeployment && kind != domain.WorkloadStatefulSet && kind != domain.WorkloadDaemonSet {
		return fmt.Errorf("%w: set image is only supported for Deployments, StatefulSets and DaemonSets, got %s",
			domain.ErrUnsupportedWorkloadKind, kind)
	}
	if container == "" {
		return errors.New("container name must not be empty")
	}
	if image == "" {
		return errors.New("image must not be empty")
	}
	if !domain.ValidImageReference(image) {
		return fmt.Errorf("%w: %q", domain.ErrInvalidImageReference, image)
	}

	err := s.management.SetImage(ctx, id, kind, namespace, name, container, image, initContainer)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to set image",
			slog.String("cluster", id.String()),
			slog.String("kind", string(kind)),
			slog.String("name", name),
			slog.String("container", container),
			slog.String("error", err.Error()))
		return err
	}

	return nil
}

// SetSecretKey writes one key of one Secret.
//
// The audit line below is what an entry in a cluster's audit log should be:
// cluster, namespace, name and key — and NEVER the value, and never its
// length, which is why slog.String("value", ...) does not appear anywhere in
// this method. RevealSecretKey's own doc comment is the reason a write of
// this shape exists at all; logging the material it decodes would undo it.
func (s *ManagementService) SetSecretKey(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name, key string, value []byte) error {
	s.logger.InfoContext(ctx, "writing secret key",
		slog.String("cluster", id.String()),
		slog.String("namespace", namespace.String()),
		slog.String("name", name),
		slog.String("key", key))

	if !domain.ValidDataKey(key) {
		return fmt.Errorf("%w: %q", domain.ErrInvalidKey, key)
	}

	err := s.management.SetSecretKey(ctx, id, namespace, name, key, value)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to write secret key",
			slog.String("cluster", id.String()),
			slog.String("namespace", namespace.String()),
			slog.String("name", name),
			slog.String("key", key),
			slog.String("error", err.Error()))
		return err
	}

	return nil
}

// SetConfigMapKey writes one key of one ConfigMap.
//
// A ConfigMap is not secret, so this exists for the same reason
// SetSecretKey does — fixing a value already on screen without hand-rolling
// anything — rather than for confidentiality. The audit line still omits the
// value, matching every other write here: what changed is cluster,
// namespace, name and key, not the contents.
func (s *ManagementService) SetConfigMapKey(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name, key, value string) error {
	s.logger.InfoContext(ctx, "writing configmap key",
		slog.String("cluster", id.String()),
		slog.String("namespace", namespace.String()),
		slog.String("name", name),
		slog.String("key", key))

	if !domain.ValidDataKey(key) {
		return fmt.Errorf("%w: %q", domain.ErrInvalidKey, key)
	}

	err := s.management.SetConfigMapKey(ctx, id, namespace, name, key, value)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to write configmap key",
			slog.String("cluster", id.String()),
			slog.String("namespace", namespace.String()),
			slog.String("name", name),
			slog.String("key", key),
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

// AttachToPod connects to a container's own running process rather than
// starting a new one — the only way to interact with a process that reads
// stdin, and to see its live stdout without a separate log stream.
//
// It can type into that process exactly as an interactive shell can, so it
// is refused exactly like ExecInPodWithTTY — TerminalAPI.StartAttachSession
// also checks ReadOnly before it allocates a PTY at all; this is the check
// that still fires if a future caller ever reaches this method some other
// way.
func (s *ManagementService) AttachToPod(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName string, stdin io.Reader, stdout, stderr io.Writer, sizeQueue ports.TerminalSizeQueue) error {
	if err := s.refuseIfReadOnly(id); err != nil {
		return err
	}

	s.logger.InfoContext(ctx, "attaching to pod",
		slog.String("cluster", id.String()),
		slog.String("namespace", namespace.String()),
		slog.String("pod", podName),
		slog.String("container", containerName))

	return s.management.AttachToPod(ctx, id, namespace, podName, containerName, stdin, stdout, stderr, sizeQueue)
}

// CordonNode marks a node schedulable or unschedulable.
func (s *ManagementService) CordonNode(ctx context.Context, id domain.ClusterID, name string, cordon bool) error {
	s.logger.InfoContext(ctx, "cordoning node",
		slog.String("cluster", id.String()),
		slog.String("name", name),
		slog.Bool("cordon", cordon))

	if err := s.management.CordonNode(ctx, id, name, cordon); err != nil {
		s.logger.ErrorContext(ctx, "failed to cordon node",
			slog.String("cluster", id.String()),
			slog.String("name", name),
			slog.String("error", err.Error()))
		return err
	}

	return nil
}

// EvictPod evicts one pod through the eviction subresource, which a
// PodDisruptionBudget may refuse.
func (s *ManagementService) EvictPod(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name string, gracePeriodSeconds int) error {
	s.logger.InfoContext(ctx, "evicting pod",
		slog.String("cluster", id.String()),
		slog.String("namespace", namespace.String()),
		slog.String("name", name),
		slog.Int("gracePeriodSeconds", gracePeriodSeconds))

	if err := s.management.EvictPod(ctx, id, namespace, name, gracePeriodSeconds); err != nil {
		s.logger.ErrorContext(ctx, "failed to evict pod",
			slog.String("cluster", id.String()),
			slog.String("namespace", namespace.String()),
			slog.String("name", name),
			slog.String("error", err.Error()))
		return err
	}

	return nil
}

// DrainNode cordons a node and evicts every pod the drain plan allows.
//
// The report is logged and returned even when the call also returns an
// error — an ErrDrainRefused still cordoned the node, which is worth a line
// in the log the same way a completed drain's counts are.
func (s *ManagementService) DrainNode(ctx context.Context, id domain.ClusterID, name string, opts domain.DrainOptions) (domain.DrainReport, error) {
	s.logger.InfoContext(ctx, "draining node",
		slog.String("cluster", id.String()),
		slog.String("name", name),
		slog.Bool("force", opts.Force),
		slog.Bool("deleteEmptyDirData", opts.DeleteEmptyDirData))

	report, err := s.management.DrainNode(ctx, id, name, opts)

	// Counts only — never the pod names. This is the one write on
	// ManagementPort whose outcome is worth a summary line regardless of
	// whether it succeeded: an operator reading the log later needs to know
	// a node was taken out of service even if nobody was watching the UI
	// when it happened.
	s.logger.InfoContext(ctx, "drain finished",
		slog.String("cluster", id.String()),
		slog.String("name", name),
		slog.Bool("cordoned", report.Cordoned),
		slog.Int("evicted", len(report.Evicted)),
		slog.Int("skipped", len(report.Skipped)),
		slog.Int("refused", len(report.Refused)),
		slog.Int("failed", len(report.Failed)),
		slog.Bool("timedOut", report.TimedOut))

	if err != nil {
		s.logger.ErrorContext(ctx, "failed to drain node",
			slog.String("cluster", id.String()),
			slog.String("name", name),
			slog.String("error", err.Error()))
		return report, err
	}

	return report, nil
}
