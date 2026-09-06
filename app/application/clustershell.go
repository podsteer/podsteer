package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/podsteer/podsteer/app/domain"
)

// The in-cluster shell's writes live on ManagementService rather than beside
// the port, and that is the whole reason this file is here.
//
// The node shell — the template for everything else about this feature — does
// NOT go through this service: TerminalAPI holds ports.NodeShellPort directly,
// makes a synchronous ManagementService.ReadOnly() check of its own, and
// writes its own log line. That works, and it means the guard and the audit
// line are re-implemented at the API layer rather than inherited, so a future
// method added beside it gets neither by default.
//
// Creating a pod is a write like any other, so it goes where every other write
// goes: refuseIfReadOnly first, before anything else, and one audit line
// naming the cluster, the namespace and the pod. TerminalAPI keeps its
// synchronous ReadOnly() pre-check as well — that one avoids allocating a
// session for a start that would be refused — and this is the guard.
//
// NOTHING HERE EVER LOGS WHAT THE SHELL DID. The audit line names the pod
// PodSteer created; the bytes flowing through the terminal are the operator's
// session and are not this application's to record.

// errClusterShellsUnavailable is returned when no ClusterShellPort was wired.
//
// Refused rather than the service failing to construct, exactly as the file
// copy's ArchivePort is: a build or a test that never opens an in-cluster
// shell should not have to supply one, and the refusal says which capability
// is absent rather than failing with a nil dereference.
var errClusterShellsUnavailable = errors.New("in-cluster shells are not available in this build")

// StartClusterShell creates an unprivileged pod in a namespace and waits for
// it to run, returning the descriptor the caller attaches to.
//
// The pod is deleted when the terminal session ends, on shutdown, or by the
// stop control in the activity list — see ports.ClusterShellPort, and
// CLAUDE.md's node-shell lifecycle note, which this follows.
func (s *ManagementService) StartClusterShell(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, image string) (domain.ClusterShell, error) {
	if err := s.refuseIfReadOnly(id); err != nil {
		return domain.ClusterShell{}, err
	}
	if s.clusterShells == nil {
		return domain.ClusterShell{}, errClusterShellsUnavailable
	}

	// REFUSED, NEVER SUBSTITUTED. NewNamespaceName("") is NamespaceAll, which
	// every list here reads as "every namespace" — and a create handed the
	// empty string lands in whatever the kubeconfig's default namespace
	// happens to be. Falling back to a system namespace (which is where the
	// node shell's own default points) would put a pod somewhere nobody chose.
	// See domain.ErrShellNamespaceRequired.
	if namespace.IsAll() {
		return domain.ClusterShell{}, fmt.Errorf("starting an in-cluster shell on %q: %w", id, domain.ErrShellNamespaceRequired)
	}

	if image == "" {
		return domain.ClusterShell{}, errors.New("image must not be empty")
	}
	if !domain.ValidImageReference(image) {
		return domain.ClusterShell{}, fmt.Errorf("%w: %q", domain.ErrInvalidImageReference, image)
	}

	shell, err := s.clusterShells.StartClusterShell(ctx, id, namespace, image)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to start in-cluster shell",
			slog.String("cluster", id.String()),
			slog.String("namespace", namespace.String()),
			slog.String("error", err.Error()))
		return domain.ClusterShell{}, err
	}

	// AFTER the create rather than before it, unlike every other write here,
	// and for one reason: the pod's name is generated, so there is no pod to
	// name until the API server has accepted it. A refused create is still on
	// the record — as the Error line above, which names what could be known.
	s.logger.InfoContext(ctx, "started in-cluster shell",
		slog.String("cluster", id.String()),
		slog.String("namespace", namespace.String()),
		slog.String("pod", shell.PodName))

	return shell, nil
}

// AdoptClusterShell takes over an existing shell pod so the caller can attach
// to it — the reuse path.
//
// GUARDED AS A WRITE even though it creates nothing, because adoption is what
// puts the pod on the hook for DELETION when the session ends, and a delete is
// a write. A read-only cluster refuses it for the same reason it refuses the
// create.
func (s *ManagementService) AdoptClusterShell(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName string) (domain.ClusterShell, error) {
	if err := s.refuseIfReadOnly(id); err != nil {
		return domain.ClusterShell{}, err
	}
	if s.clusterShells == nil {
		return domain.ClusterShell{}, errClusterShellsUnavailable
	}
	if namespace.IsAll() {
		return domain.ClusterShell{}, fmt.Errorf("attaching to an in-cluster shell on %q: %w", id, domain.ErrShellNamespaceRequired)
	}
	if podName == "" {
		return domain.ClusterShell{}, fmt.Errorf("attaching to an in-cluster shell on %q: %w", id, domain.ErrEmptyResourceName)
	}

	shell, err := s.clusterShells.AdoptClusterShell(ctx, id, namespace, podName)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to adopt in-cluster shell",
			slog.String("cluster", id.String()),
			slog.String("namespace", namespace.String()),
			slog.String("pod", podName),
			slog.String("error", err.Error()))
		return domain.ClusterShell{}, err
	}

	s.logger.InfoContext(ctx, "adopted in-cluster shell",
		slog.String("cluster", id.String()),
		slog.String("namespace", namespace.String()),
		slog.String("pod", shell.PodName))

	return shell, nil
}

// FindClusterShells reports PodSteer's own shell pods in a namespace, and how
// the operator's choice between reusing one and creating another is framed.
//
// NO READ-ONLY GUARD AND NO AUDIT LINE: listing pods by label changes nothing
// about the cluster, and the guard has no business refusing a read — the same
// rule StreamLogs follows here. The plan is domain's, so "only a running pod
// is an offer" is decided in one place and asserted without a cluster.
func (s *ManagementService) FindClusterShells(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) (domain.ClusterShellReuse, error) {
	if s.clusterShells == nil {
		return domain.ClusterShellReuse{}, errClusterShellsUnavailable
	}
	if namespace.IsAll() {
		return domain.ClusterShellReuse{}, fmt.Errorf("looking for in-cluster shells on %q: %w", id, domain.ErrShellNamespaceRequired)
	}

	candidates, err := s.clusterShells.FindClusterShells(ctx, id, namespace)
	if err != nil {
		return domain.ClusterShellReuse{}, err
	}
	return domain.PlanClusterShellReuse(candidates), nil
}

// StopClusterShell deletes the pod behind one shell and forgets it.
//
// DELIBERATELY UNGUARDED, the one write-shaped method here that is. It removes
// something PodSteer put in the cluster, and it is what the terminal session's
// exit calls; refusing it because the cluster was marked read-only after the
// pod was created would strand the pod until its deadline — a guard against
// PodSteer's own writes turning into the reason one of them is left behind.
// The node shell's stop is unguarded for the same reason.
func (s *ManagementService) StopClusterShell(shellID string) error {
	if s.clusterShells == nil {
		return errClusterShellsUnavailable
	}
	return s.clusterShells.StopClusterShell(shellID)
}

// ListClusterShells reports the in-cluster shells running right now — the live
// registry, so the activity list shows only pods that still exist.
func (s *ManagementService) ListClusterShells() []domain.ClusterShell {
	if s.clusterShells == nil {
		return nil
	}
	return s.clusterShells.ListClusterShells()
}

// StopAllClusterShells deletes every in-cluster shell pod, for shutdown and
// for the activity list's "Stop all". Unguarded for StopClusterShell's reason.
func (s *ManagementService) StopAllClusterShells() {
	if s.clusterShells == nil {
		return
	}
	s.clusterShells.StopAllClusterShells()
}
