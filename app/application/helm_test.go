package application_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// fakeHelm stands in for the release listing. The listing and the error sit
// beside each other because the adapter reports a refusal ON the listing and
// only a transport failure as an error — the split this service is shaped
// around.
type fakeHelm struct {
	listing domain.HelmListing
	err     error

	calls          int
	askedNamespace domain.NamespaceName
	askedRefresh   bool
}

var _ ports.HelmPort = (*fakeHelm)(nil)

func (f *fakeHelm) ListHelmReleases(
	_ context.Context,
	_ domain.ClusterID,
	namespace domain.NamespaceName,
	refresh bool,
) (domain.HelmListing, error) {
	f.calls++
	f.askedNamespace = namespace
	f.askedRefresh = refresh
	return f.listing, f.err
}

func newHelmService(t *testing.T, helm *fakeHelm) *application.HelmService {
	t.Helper()

	registry := application.NewRegistry()
	registry.Open(mustCluster(t, "dev", true))

	service, err := application.NewHelmService(application.HelmServiceDeps{
		Helm:     helm,
		Registry: registry,
	})
	if err != nil {
		t.Fatalf("NewHelmService() error = %v", err)
	}
	return service
}

func TestHelmServiceRefusesAClusterThatIsNotOpen(t *testing.T) {
	t.Parallel()

	helm := &fakeHelm{}
	service := newHelmService(t, helm)

	if _, err := service.ListReleases(context.Background(), "other", "shop", false); err == nil {
		t.Fatal("want an error for a cluster nothing is connected to")
	}
	if helm.calls != 0 {
		t.Fatalf("the port was called %d times, want 0 — the registry check comes first", helm.calls)
	}
}

func TestHelmServicePassesTheNamespaceThroughUnchanged(t *testing.T) {
	t.Parallel()

	// Unlike a rules review, which has no cluster-wide form and so is
	// normalised to a default namespace, a release listing genuinely can be
	// cluster-wide — so "all namespaces" reaches the port as itself.
	helm := &fakeHelm{listing: domain.HelmListing{Status: domain.HelmListed}}
	service := newHelmService(t, helm)

	if _, err := service.ListReleases(context.Background(), "dev", domain.NamespaceAll, false); err != nil {
		t.Fatalf("ListReleases() error = %v", err)
	}
	if helm.askedNamespace != domain.NamespaceAll {
		t.Fatalf("asked about %q, want the all-namespaces scope unchanged", helm.askedNamespace)
	}
}

func TestHelmServiceCarriesAForbiddenListingThrough(t *testing.T) {
	t.Parallel()

	// The refusal is the adapter's, sentence and all, and the service does
	// not restate or soften it: it names the permission, which is a fact
	// about the cluster's API.
	helm := &fakeHelm{listing: domain.HelmListing{
		Releases: []domain.HelmRelease{},
		Status:   domain.HelmForbidden,
		Refusal:  "Your account may not list Secrets in shop",
		Driver:   domain.HelmSecretDriver,
	}}
	service := newHelmService(t, helm)

	listing, err := service.ListReleases(context.Background(), "dev", "shop", false)
	if err != nil {
		t.Fatalf("ListReleases() error = %v, want a forbidden listing rather than an error", err)
	}
	if listing.Status != domain.HelmForbidden {
		t.Fatalf("status = %q, want forbidden", listing.Status)
	}
	if listing.Refusal != "Your account may not list Secrets in shop" {
		t.Fatalf("refusal = %q, want the adapter's own sentence", listing.Refusal)
	}
}

func TestHelmServiceReportsATransportFailureAsAStatus(t *testing.T) {
	t.Parallel()

	// A failed listing still renders a pane with a reason and a retry, rather
	// than an error that blanks it.
	helm := &fakeHelm{err: fmt.Errorf("listing: %w", ports.ErrUnreachable)}
	service := newHelmService(t, helm)

	listing, err := service.ListReleases(context.Background(), "dev", "shop", false)
	if err != nil {
		t.Fatalf("ListReleases() error = %v, want a failed listing rather than an error", err)
	}
	if listing.Status != domain.HelmFailed {
		t.Fatalf("status = %q, want failed", listing.Status)
	}
	if listing.Refusal == "" {
		t.Fatal("want a sentence saying what happened")
	}
	if listing.Releases == nil {
		t.Fatal("want an empty slice rather than nil, so nothing marshals as null")
	}
}

func TestHelmServiceFailsTheCallOnCancellation(t *testing.T) {
	t.Parallel()

	// Navigating away is not an answer about this cluster. Reported as a
	// failed listing it would leave "the listing failed" on the page when
	// somebody came back to it.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	helm := &fakeHelm{err: context.Canceled}
	service := newHelmService(t, helm)

	if _, err := service.ListReleases(ctx, "dev", "shop", false); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want the cancellation to fail the call", err)
	}
}

func TestHelmServiceCachesNothingOfItsOwn(t *testing.T) {
	t.Parallel()

	// The cache belongs in the adapter, which is the layer Invalidate and
	// forgetReads already reach — a second one here would be a second thing
	// to invalidate and the first one somebody forgot.
	helm := &fakeHelm{listing: domain.HelmListing{Status: domain.HelmListed}}
	service := newHelmService(t, helm)

	for i := 0; i < 3; i++ {
		if _, err := service.ListReleases(context.Background(), "dev", "shop", false); err != nil {
			t.Fatalf("call %d: error = %v", i, err)
		}
	}
	if helm.calls != 3 {
		t.Fatalf("the port was called %d times, want 3 — the service holds nothing", helm.calls)
	}
}

func TestHelmServicePassesTheRefreshFlagThrough(t *testing.T) {
	t.Parallel()

	// Refresh is the operator pressing something, and the service must not
	// swallow it: the cache lives in the adapter, so only the flag reaching
	// the port makes the "as of" time beside that control actionable.
	helm := &fakeHelm{listing: domain.HelmListing{Status: domain.HelmListed}}
	service := newHelmService(t, helm)

	if _, err := service.ListReleases(context.Background(), "dev", "shop", true); err != nil {
		t.Fatalf("ListReleases() error = %v", err)
	}
	if !helm.askedRefresh {
		t.Fatal("want the refresh flag to reach the port")
	}

	if _, err := service.ListReleases(context.Background(), "dev", "shop", false); err != nil {
		t.Fatalf("ListReleases() error = %v", err)
	}
	if helm.askedRefresh {
		t.Fatal("want an ordinary open NOT to bypass the cache")
	}
}

func TestNewHelmServiceRequiresItsCollaborators(t *testing.T) {
	t.Parallel()

	if _, err := application.NewHelmService(application.HelmServiceDeps{Registry: application.NewRegistry()}); err == nil {
		t.Fatal("want an error without a HelmPort")
	}
	if _, err := application.NewHelmService(application.HelmServiceDeps{Helm: &fakeHelm{}}); err == nil {
		t.Fatal("want an error without a Registry")
	}
}
