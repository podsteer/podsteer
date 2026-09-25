package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// InspectService orchestrates the on-request inspections: a reachability
// probe, and an image report.
//
// ONE RULE GOVERNS EVERYTHING HERE AND IT IS WHY THEY SHARE A SERVICE:
// nothing on this path ever runs on a refresh tick. Every method is the
// consequence of somebody pressing a button, once, and the panels above them
// are built to show a stale answer with the time it was taken rather than to
// keep it fresh. A probe that ran every ten seconds would put an exec into
// somebody's audit log all afternoon, and an image report that did would cost
// a pod and a node GET per open pane.
//
// The second rule is narrower and applies to one method. An in-cluster probe
// RUNS A COMMAND IN SOMEBODY'S CONTAINER. It reads nothing and writes nothing
// — it is a connect attempt — but the act itself is write-shaped: the exec
// subresource is the same one a shell uses, it appears in the cluster's audit
// log as one, and an operator who marked a cluster read-only to stop PodSteer
// touching it did not mean "except to run things in the containers". So it
// goes through the same guard and leaves the same one audit line every other
// exec-shaped action here does — cluster, namespace, pod, container and
// target, and NEVER the probe's output, for the reason a file transfer's audit
// line never carries a file's contents.
type InspectService struct {
	inspect  ports.InspectPort
	registry *Registry
	logger   *slog.Logger
}

// InspectServiceDeps are what an InspectService needs.
type InspectServiceDeps struct {
	Inspect ports.InspectPort
	// Registry supplies the read-only policy — the same one ManagementService
	// reads and ClusterAPI.SetReadOnly writes. A service-local copy would let
	// a cluster marked read-only still accept an exec issued through this one.
	Registry *Registry
	Logger   *slog.Logger
}

// NewInspectService returns an inspect service wired with its dependencies.
func NewInspectService(deps InspectServiceDeps) (*InspectService, error) {
	switch {
	case deps.Inspect == nil:
		return nil, errors.New("application: InspectService requires an InspectPort")
	case deps.Registry == nil:
		return nil, errors.New("application: InspectService requires a Registry")
	}

	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &InspectService{
		inspect:  deps.Inspect,
		registry: deps.Registry,
		logger:   logger.With(slog.String("service", "inspect")),
	}, nil
}

// ProbeFromHere plans and performs a probe from this machine.
//
// NOT GUARDED BY THE READ-ONLY FLAG, and that is the same line
// DownloadFromPod and StreamLogs sit on: opening a socket to something and
// closing it again changes nothing in the cluster, and the guard is about
// PodSteer's own writes. What it does do is open a port-forward, which the
// guard has never governed either.
func (s *InspectService) ProbeFromHere(ctx context.Context, id domain.ClusterID, subject domain.ProbeSubject) (domain.ProbeResult, error) {
	plan, err := domain.PlanProbe(subject, domain.VantageLocal)
	if err != nil {
		return domain.ProbeResult{}, err
	}

	observation, err := s.inspect.ProbeFromHere(ctx, id, plan)
	if err != nil {
		s.logger.WarnContext(ctx, "reachability probe could not be performed",
			slog.String("vantage", string(domain.VantageLocal)),
			slog.String("cluster", id.String()),
			slog.String("namespace", plan.Namespace.String()),
			slog.String("kind", plan.Kind),
			slog.String("name", plan.Name),
			slog.String("target", plan.Address()),
			slog.String("error", err.Error()))
		return domain.ProbeResult{}, err
	}

	return domain.NewProbeResult(plan, observation), nil
}

// ProbeFromPod plans and performs a probe from inside a container.
//
// GUARDED AND AUDITED, for the reasons in this service's own doc comment. The
// guard runs before the plan, so a read-only cluster costs no work at all and
// the refusal an operator sees is about their setting rather than about their
// Service.
func (s *InspectService) ProbeFromPod(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName string, subject domain.ProbeSubject) (domain.ProbeResult, error) {
	if err := s.refuseIfReadOnly(id); err != nil {
		return domain.ProbeResult{}, err
	}

	plan, err := domain.PlanProbe(subject, domain.VantageInCluster)
	if err != nil {
		return domain.ProbeResult{}, err
	}

	observation, err := s.inspect.ProbeFromPod(ctx, id, namespace, podName, containerName, plan)
	s.auditProbe(ctx, id, namespace, podName, containerName, plan, err)
	if err != nil {
		return domain.ProbeResult{}, err
	}

	return domain.NewProbeResult(plan, observation), nil
}

// refuseIfReadOnly refuses an exec-shaped action on a cluster the operator
// marked read-only, wrapping ports.ErrReadOnly the way every other refusal
// here does.
func (s *InspectService) refuseIfReadOnly(id domain.ClusterID) error {
	if s.registry.ReadOnly(id) {
		return fmt.Errorf("cluster %q: %w", id, ports.ErrReadOnly)
	}
	return nil
}

// auditProbe writes the one line an in-cluster probe leaves behind.
//
// WHAT WAS RUN AND WHERE, NEVER WHAT CAME BACK. The output of a probe is a
// resolved address and a status code — small, but it is the container's
// answer about somebody else's Service, and the discipline that keeps a file
// transfer's audit line free of file contents and a Secret write's free of
// values is worth more than one more field here would be.
func (s *InspectService) auditProbe(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName string, plan domain.ProbePlan, err error) {
	attrs := []slog.Attr{
		slog.String("cluster", id.String()),
		slog.String("namespace", namespace.String()),
		slog.String("pod", podName),
		slog.String("container", containerName),
		slog.String("target", plan.Address()),
		slog.String("targetKind", plan.Kind),
		slog.String("targetName", plan.Name),
	}
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
		s.logger.LogAttrs(ctx, slog.LevelWarn, "in-cluster reachability probe failed", attrs...)
		return
	}
	s.logger.LogAttrs(ctx, slog.LevelInfo, "in-cluster reachability probe ran", attrs...)
}

// ImageReport gathers what Kubernetes reports about one container's image and
// shapes it in the domain.
//
// A plain read, so no guard: it GETs a pod and a node and nothing else. What
// it deliberately does NOT do — read the image's registry manifest, or the
// pull Secret that would authenticate that read — is documented on
// domain.ImageReport, and the report carries the line saying so, so the pane
// can never quietly imply it looked.
func (s *InspectService) ImageReport(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName string) (domain.ImageReport, error) {
	facts, err := s.inspect.ImageFacts(ctx, id, namespace, podName, containerName)
	if err != nil {
		s.logger.WarnContext(ctx, "image facts could not be read",
			slog.String("cluster", id.String()),
			slog.String("namespace", namespace.String()),
			slog.String("pod", podName),
			slog.String("container", containerName),
			slog.String("error", err.Error()))
		return domain.ImageReport{}, err
	}

	return domain.NewImageReport(facts), nil
}
