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

// ReadRelease reads ONE revision of ONE release, because somebody clicked.
//
// THE AUDIT LINE IS THE POINT OF THIS METHOD EXISTING AT ALL, and it is the
// same line ManagementService writes before every write: cluster, namespace,
// the object and — here — the revision, and NEVER a value. That is not a
// stylistic echo. Reading a release payload is reading a Secret's contents,
// which ADR 3 permits only as a deliberate act somebody performed, and an act
// nothing recorded is indistinguishable from one nobody performed. The line
// is written BEFORE the read rather than after it, so a read that hangs or
// fails is still recorded as having been asked for.
//
// WHAT IS NOT IN THE LINE MATTERS AS MUCH AS WHAT IS. No value, no key, no
// byte count, no chart name — the same omission `slog.String("value", ...)`
// gets in SetSecretKey, and for the same reason: a log that carries what it
// was guarding undoes the guard. A release NAME is an object name and IS in
// the line, which is the one deliberate difference from ListReleases' own
// debug line: that one logs shapes because a listing is about a cluster,
// while this one is about one object and an audit entry that cannot say which
// object was opened is not an audit entry.
//
// A FAILURE IS RETURNED, NOT SOFTENED. Unlike ListReleases, where being
// refused is an ordinary answer that arrives as a status beside the rows,
// there is nothing to render here without the payload — so a refusal, a
// reaped revision, an undecodable Secret and an oversized one all come back
// as errors and the pane shows the sentence in place of the tabs. Collapsing
// them into an empty detail would put an empty values tab in front of
// somebody and let them read it as a release installed with no values.
func (s *HelmService) ReadRelease(
	ctx context.Context,
	id domain.ClusterID,
	namespace domain.NamespaceName,
	release string,
	revision int,
) (domain.HelmReleaseDetail, error) {
	s.logger.InfoContext(ctx, "reading helm release payload",
		slog.String("cluster", id.String()),
		slog.String("namespace", namespace.String()),
		slog.String("release", release),
		slog.Int("revision", revision))

	if _, err := s.registry.Get(id); err != nil {
		return domain.HelmReleaseDetail{}, fmt.Errorf("reading Helm release: %w", err)
	}

	detail, err := s.helm.ReadHelmRelease(ctx, id, namespace, release, revision)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to read helm release payload",
			slog.String("cluster", id.String()),
			slog.String("namespace", namespace.String()),
			slog.String("release", release),
			slog.Int("revision", revision),
			slog.String("error", err.Error()))
		return domain.HelmReleaseDetail{}, err
	}

	return detail, nil
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
