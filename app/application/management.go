package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

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
	// archive and limits serve the file copy in filecopy.go; archive is nil
	// when nothing was wired, and the copy methods refuse rather than the
	// service failing to construct.
	archive ports.ArchivePort
	limits  domain.TransferLimits
	// clusterShells creates and deletes the unprivileged pods behind in-cluster
	// shells — see clustershell.go for why those writes live here rather than
	// beside the port the way the node shell's do. Optional, like archive: nil
	// means the methods refuse rather than the service failing to construct.
	clusterShells ports.ClusterShellPort
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
	// Archive packs and unpacks the LOCAL side of a file copy — see
	// ports.ArchivePort for the rules it enforces. Optional: without it
	// DownloadFromPod and UploadToPod refuse, and every other method is
	// unaffected, so a caller that never copies files need not wire one.
	Archive ports.ArchivePort
	// TransferLimits caps a copy in either direction; the zero value means
	// the domain's defaults (1 GiB, 100k entries).
	TransferLimits domain.TransferLimits
	// ClusterShells creates and deletes the pods behind in-cluster shells.
	// Optional, on the same terms as Archive: without it the ClusterShell
	// methods refuse and every other method is unaffected, so a caller that
	// never opens one need not wire it.
	ClusterShells ports.ClusterShellPort
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
		archive:    deps.Archive,
		limits:     deps.TransferLimits.WithDefaults(),

		clusterShells: deps.ClusterShells,
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
// See domain.LogOptions for what each field of opts does.
func (s *ManagementService) StreamLogs(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName string, containerName string, opts domain.LogOptions, out chan<- string) error {
	s.logger.InfoContext(ctx, "streaming logs",
		slog.String("cluster", id.String()),
		slog.String("namespace", namespace.String()),
		slog.String("pod", podName),
		slog.String("container", containerName),
		slog.Bool("follow", opts.Follow),
		slog.Int64("tailLines", opts.TailLines),
		slog.Int64("sinceSeconds", opts.SinceSeconds),
		slog.Bool("previous", opts.Previous),
		slog.Int64("limitBytes", opts.LimitBytes))

	err := s.management.StreamLogs(ctx, id, namespace, podName, containerName, opts, out)
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
	if err := s.refuseIfReadOnly(id); err != nil {
		return "", err
	}

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
	if err := s.refuseIfReadOnly(id); err != nil {
		return err
	}

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

// PromoteRollout advances a paused Argo Rollouts Rollout by one step.
//
// A WRITE LIKE ANY OTHER, and it goes through the same three things every
// write here does: the read-only guard first, one audit line naming the
// cluster, namespace and object, and the port beneath deciding nothing about
// policy. What makes it unusual is only the object — the Rollout controller
// owns the fields being patched, so the adapter reads the live object and
// asks domain.PlanRolloutPromote which patch to send.
func (s *ManagementService) PromoteRollout(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name string) error {
	if err := s.refuseIfReadOnly(id); err != nil {
		return err
	}

	s.logger.InfoContext(ctx, "promoting rollout",
		slog.String("cluster", id.String()),
		slog.String("namespace", namespace.String()),
		slog.String("name", name))

	if err := s.management.PromoteRollout(ctx, id, namespace, name); err != nil {
		s.logger.ErrorContext(ctx, "failed to promote rollout",
			slog.String("cluster", id.String()),
			slog.String("namespace", namespace.String()),
			slog.String("name", name),
			slog.String("error", err.Error()))
		return err
	}

	return nil
}

// AbortRollout abandons the update an Argo Rollouts Rollout is part way
// through, returning traffic to the stable ReplicaSet.
func (s *ManagementService) AbortRollout(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name string) error {
	if err := s.refuseIfReadOnly(id); err != nil {
		return err
	}

	s.logger.InfoContext(ctx, "aborting rollout",
		slog.String("cluster", id.String()),
		slog.String("namespace", namespace.String()),
		slog.String("name", name))

	if err := s.management.AbortRollout(ctx, id, namespace, name); err != nil {
		s.logger.ErrorContext(ctx, "failed to abort rollout",
			slog.String("cluster", id.String()),
			slog.String("namespace", namespace.String()),
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

// ApplyResource applies a manifest as declared intent, through server-side
// apply. See ports.ManagementPort.ApplyResource for the contract.
//
// A REFUSAL IS NOT A FAILURE, and the logging says so. An apply turned away
// over field ownership is logged at info with the managers named — it is an
// ordinary answer that an operator will act on — where a genuine error stays
// at error level. Logging a refusal as an error would put an operator's
// routine "Argo CD owns this" in the same bucket as a cluster that fell over.
func (s *ManagementService) ApplyResource(
	ctx context.Context,
	id domain.ClusterID,
	manifest string,
	options domain.ApplyOptions,
) (domain.ApplyOutcome, error) {
	// A dry run persists nothing, so it is allowed on a read-only cluster for
	// the same reason UpdateResource's is: asking the server what WOULD happen
	// is a read, and refusing it would take away the one control that answers
	// "may I" without doing anything.
	if !options.DryRun {
		if err := s.refuseIfReadOnly(id); err != nil {
			return domain.ApplyOutcome{}, err
		}
	}

	if manifest == "" {
		return domain.ApplyOutcome{}, errors.New("manifest cannot be empty")
	}

	// FORCE WITHOUT A CONFIRMED SET IS REFUSED BEFORE ANYTHING LEAVES. A
	// force that carries nothing is a retry that wins — an interface turning
	// "the server declined" into "press again" with nobody having read who
	// owns what. See domain.ErrForceUnconfirmed.
	if options.Force && len(options.Confirmed) == 0 {
		return domain.ApplyOutcome{}, domain.ErrForceUnconfirmed
	}

	s.logger.InfoContext(ctx, "applying resource",
		slog.String("cluster", id.String()),
		slog.Int("manifestLength", len(manifest)),
		slog.Bool("dryRun", options.DryRun))

	outcome, err := s.management.ApplyResource(ctx, id, manifest, options)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to apply resource",
			slog.String("cluster", id.String()),
			slog.Bool("dryRun", options.DryRun),
			slog.String("error", err.Error()))
		return domain.ApplyOutcome{}, err
	}

	if options.Force && !outcome.Refused() && !options.DryRun {
		// AUDITED, because taking a field from another manager is a
		// consequential act somebody should be able to find afterwards. The
		// MANAGERS are named and the object is not beyond its kind: a field
		// manager's name is not an object name, and the no-object-names rule
		// governs the latter.
		s.logger.InfoContext(ctx, "apply forced ownership away from other managers",
			slog.String("cluster", id.String()),
			slog.String("kind", outcome.Kind),
			slog.Int("fields", len(options.Confirmed)),
			slog.String("managers", strings.Join(options.Confirmed.Managers(), ", ")))
	}

	if outcome.Refused() {
		// The MANAGERS are logged and the object is not: a field manager's
		// name is not an object name, and the no-object-names rule that
		// governs what reaches disk is about the latter.
		s.logger.InfoContext(ctx, "apply refused over field ownership",
			slog.String("cluster", id.String()),
			slog.Int("fields", len(outcome.Conflicts)),
			slog.String("managers", strings.Join(outcome.Conflicts.Managers(), ", ")))
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

// ResizeContainer changes a running container's CPU and memory in place.
//
// THE PLAN IS MADE HERE, FROM THE POD, not from what the interface believed
// when the dialog opened. Between opening it and pressing the button the
// container may have been resized by somebody else, by a VPA, or restarted
// with a different spec — and every refusal PlanResize makes (a limit below
// its request, a change that changes nothing, a container that is no longer
// there) is only true against the CURRENT figures. Planning against a stale
// read would refuse valid edits and permit invalid ones.
//
// Read-only is refused first, as everywhere: PodSteer's own guard, before the
// request exists.
func (s *ManagementService) ResizeContainer(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName string, request domain.ResizeRequest) (domain.ResizePlan, error) {
	if err := s.refuseIfReadOnly(id); err != nil {
		return domain.ResizePlan{}, err
	}

	current, err := s.management.ContainerResizeSpec(ctx, id, namespace, podName, request.Container)
	if err != nil {
		return domain.ResizePlan{}, err
	}

	plan, err := domain.PlanResize(current, request)
	if err != nil {
		return domain.ResizePlan{}, err
	}

	s.logger.InfoContext(ctx, "resizing container",
		slog.String("cluster", id.String()),
		slog.String("namespace", namespace.String()),
		slog.String("pod", podName),
		slog.String("container", plan.Container),
		slog.String("cpuRequest", plan.CPURequest),
		slog.String("cpuLimit", plan.CPULimit),
		slog.String("memoryRequest", plan.MemoryRequest),
		slog.String("memoryLimit", plan.MemoryLimit),
		slog.Bool("restarts", plan.Restarts))

	if err := s.management.ResizePod(ctx, id, namespace, podName, plan); err != nil {
		s.logger.ErrorContext(ctx, "failed to resize container",
			slog.String("cluster", id.String()),
			slog.String("pod", podName),
			slog.String("container", plan.Container),
			slog.String("error", err.Error()))
		return domain.ResizePlan{}, err
	}

	// THE PLAN COMES BACK so the caller can say what was asked for — and, in
	// particular, whether it restarts the container. What the kubelet then
	// does with it arrives as a condition on the pod, which the assessment
	// already reads and reports; nothing here claims it was applied.
	return plan, nil
}

// RollbackWorkload rolls a Deployment, StatefulSet or DaemonSet back to a
// previously recorded revision.
//
// dryRun is NOT gated by the read-only check below, mirroring
// UpdateResource's own dry run: it validates against the API server and
// persists nothing, so it is exactly as safe against a read-only cluster as
// any other read. A real rollback (dryRun false) is refused exactly like
// every other write here.
//
// Only the three kinds that carry a rollout history support it, and that is
// checked HERE rather than left to the adapter — mirroring SetImage's own
// kind check — so an unsupported kind never costs a round trip to the
// cluster to be told no. toRevision must be positive; whether it names the
// revision already current can only be told by reading the cluster, which
// is why that half of the check lives in the adapter.
func (s *ManagementService) RollbackWorkload(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName, name string, toRevision int64, dryRun bool) (domain.RollbackOutcome, error) {
	if !dryRun {
		if err := s.refuseIfReadOnly(id); err != nil {
			return domain.RollbackOutcome{}, err
		}
	}

	s.logger.InfoContext(ctx, "rolling back workload",
		slog.String("cluster", id.String()),
		slog.String("kind", string(kind)),
		slog.String("namespace", namespace.String()),
		slog.String("name", name),
		slog.Int64("toRevision", toRevision),
		slog.Bool("dryRun", dryRun))

	if kind != domain.WorkloadDeployment && kind != domain.WorkloadStatefulSet && kind != domain.WorkloadDaemonSet {
		return domain.RollbackOutcome{}, fmt.Errorf("%w: rollback is only supported for Deployments, StatefulSets and DaemonSets, got %s",
			domain.ErrUnsupportedWorkloadKind, kind)
	}
	if toRevision <= 0 {
		return domain.RollbackOutcome{}, fmt.Errorf("%w: revision must be positive, got %d", domain.ErrInvalidRevision, toRevision)
	}

	outcome, err := s.management.RollbackWorkload(ctx, id, kind, namespace, name, toRevision, dryRun)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to roll back workload",
			slog.String("cluster", id.String()),
			slog.String("kind", string(kind)),
			slog.String("name", name),
			slog.Int64("toRevision", toRevision),
			slog.String("error", err.Error()))
		return domain.RollbackOutcome{}, err
	}

	return outcome, nil
}

// SetSecretKey writes one key of one Secret.
//
// The audit line below is what an entry in a cluster's audit log should be:
// cluster, namespace, name and key — and NEVER the value, and never its
// length, which is why slog.String("value", ...) does not appear anywhere in
// this method. RevealSecretKey's own doc comment is the reason a write of
// this shape exists at all; logging the material it decodes would undo it.
func (s *ManagementService) SetSecretKey(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name, key string, value []byte) error {
	if err := s.refuseIfReadOnly(id); err != nil {
		return err
	}

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
	if err := s.refuseIfReadOnly(id); err != nil {
		return err
	}

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

// AddEphemeralContainer adds an ephemeral debug container to a running pod.
//
// A write — it mutates the pod's spec — so it is refused on a read-only
// cluster exactly like the methods above. The audit line names the cluster,
// namespace, pod, target and image, and NEVER the command: an ephemeral
// container is `kubectl debug`, and what an audit log needs is which pod grew
// a debugger and from what image, not a transcript of what was typed into it —
// the same discipline SetSecretKey follows for a Secret's value.
func (s *ManagementService) AddEphemeralContainer(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName string, spec domain.DebugContainerSpec) (string, error) {
	if err := s.refuseIfReadOnly(id); err != nil {
		return "", err
	}

	s.logger.InfoContext(ctx, "adding ephemeral debug container",
		slog.String("cluster", id.String()),
		slog.String("namespace", namespace.String()),
		slog.String("pod", podName),
		slog.String("target", spec.TargetContainer),
		slog.String("image", spec.Image))

	if !domain.ValidImageReference(spec.Image) {
		return "", fmt.Errorf("%w: %q", domain.ErrInvalidImageReference, spec.Image)
	}

	name, err := s.management.AddEphemeralContainer(ctx, id, namespace, podName, spec)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to add ephemeral debug container",
			slog.String("cluster", id.String()),
			slog.String("namespace", namespace.String()),
			slog.String("pod", podName),
			slog.String("error", err.Error()))
		return "", err
	}

	return name, nil
}

// WaitForEphemeralContainerRunning blocks until the named ephemeral container
// is running, so a caller can open a terminal into it without racing the pull.
//
// A read, not a write: it polls the pod's status and changes nothing, so it
// is NOT gated by the read-only guard — the refusal belongs on the write that
// added the container, which already happened.
func (s *ManagementService) WaitForEphemeralContainerRunning(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName string) error {
	return s.management.WaitForEphemeralContainerRunning(ctx, id, namespace, podName, containerName)
}

// CordonNode marks a node schedulable or unschedulable.
func (s *ManagementService) CordonNode(ctx context.Context, id domain.ClusterID, name string, cordon bool) error {
	if err := s.refuseIfReadOnly(id); err != nil {
		return err
	}

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
	if err := s.refuseIfReadOnly(id); err != nil {
		return err
	}

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
	if err := s.refuseIfReadOnly(id); err != nil {
		return domain.DrainReport{}, err
	}

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
