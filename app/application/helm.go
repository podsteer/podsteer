package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// The Helm page's use case: what has Helm installed here.
//
// SHAPED LIKE RBACService, and for the same reason its shape is what it is.
// A registry check, one delegation, and no verdict of its own — being refused
// is an ordinary answer here rather than a fault, so a 403 arrives as a
// domain.HelmListStatus on the returned listing rather than as an error, and
// the pane still renders with a sentence saying which kind of nothing it is
// looking at.
//
// IT CACHES NOTHING, and that is deliberate rather than an omission. The
// adapter holds the five-minute cache (see app/adapters/k8s/helm.go), which
// is where a per-cluster cache belongs here, because that is the layer
// Adapter.Invalidate and Adapter.forgetReads already reach: a write PodSteer
// made drops the listing without anything in this file knowing about it. A
// second cache here would be a second thing to invalidate and the first one
// somebody forgot.

// HelmServiceDeps are the collaborators the Helm use case needs.
type HelmServiceDeps struct {
	// Helm lists releases from their Secrets' labels. Required.
	Helm ports.HelmPort
	// Registry tracks open connections. Required.
	Registry *Registry
	// Logger receives diagnostics. Optional; defaults to slog.Default.
	Logger *slog.Logger
}

// HelmService implements the Helm page's use case.
type HelmService struct {
	helm     ports.HelmPort
	registry *Registry
	logger   *slog.Logger
}

// Compile-time proof that the service satisfies its inbound port.
var _ ports.HelmService = (*HelmService)(nil)

// NewHelmService validates deps and returns the service.
func NewHelmService(deps HelmServiceDeps) (*HelmService, error) {
	switch {
	case deps.Helm == nil:
		return nil, errors.New("application: HelmService requires a HelmPort")
	case deps.Registry == nil:
		return nil, errors.New("application: HelmService requires a Registry")
	}

	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &HelmService{
		helm:     deps.Helm,
		registry: deps.Registry,
		logger:   logger.With(slog.String("service", "helm")),
	}, nil
}

// ListReleases returns what Helm has installed in one namespace, or
// cluster-wide when the namespace selects every namespace.
//
// A TRANSPORT FAILURE BECOMES A STATUS RATHER THAN AN ERROR, exactly as a
// refusal does, so the page can offer Retry beside the reason instead of
// blanking. The one thing that still fails the call is CANCELLATION: the
// caller navigating away is not an answer about this cluster, and reporting
// it as one would put "the listing failed" on a page nobody is looking at
// and, worse, leave that sentence there when they came back. That is the same
// line RBACService.SubjectRules draws.
func (s *HelmService) ListReleases(
	ctx context.Context,
	id domain.ClusterID,
	namespace domain.NamespaceName,
	refresh bool,
) (domain.HelmListing, error) {
	if _, err := s.registry.Get(id); err != nil {
		return domain.HelmListing{}, fmt.Errorf("listing Helm releases: %w", err)
	}

	listing, err := s.helm.ListHelmReleases(ctx, id, namespace, refresh)
	if err != nil {
		if ctx.Err() != nil {
			return domain.HelmListing{}, fmt.Errorf(
				"listing Helm releases in %q of %q: %w", namespace, id, err)
		}
		return domain.HelmListing{
			Releases: []domain.HelmRelease{},
			Status:   domain.HelmFailed,
			Refusal:  helmFailureReason(err),
			Driver:   domain.HelmSecretDriver,
		}, nil
	}

	// Logged by SHAPE and never by content: how many releases, and whether
	// the listing was permitted. A release name is an object name, and object
	// names do not go into a log file that outlives the window they were read
	// in — the same rule ManagementService's own audit lines follow.
	s.logger.DebugContext(ctx, "listed helm releases",
		slog.String("cluster", id.String()),
		slog.String("namespace", namespace.String()),
		slog.String("status", string(listing.Status)),
		slog.Int("releases", len(listing.Releases)),
		slog.Bool("truncated", listing.Truncated),
		slog.Bool("refresh", refresh))

	return listing, nil
}

// helmFailureReason is the sentence shown in place of a listing that failed
// for something other than a refusal.
//
// It is kept apart from the adapter's refusal sentence because the two need
// opposite advice: a refusal names a permission somebody could be granted, and
// this names a cluster that might come back on its own.
func helmFailureReason(err error) string {
	if errors.Is(err, ports.ErrUnreachable) {
		return "The cluster could not be reached, so the release list could not be read. Try again."
	}
	return "The release list could not be read. The cluster may be unreachable; try again."
}
