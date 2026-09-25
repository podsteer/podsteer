package application_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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

	// The payload read is a different act from the listing and is counted
	// separately, so a test can assert that listing a cluster's releases
	// reads no payload at all.
	detail       domain.HelmReleaseDetail
	detailErr    error
	payloadReads int
	askedRelease string
	askedRevisio int
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

func (f *fakeHelm) ReadHelmRelease(
	_ context.Context,
	_ domain.ClusterID,
	namespace domain.NamespaceName,
	release string,
	revision int,
) (domain.HelmReleaseDetail, error) {
	f.payloadReads++
	f.askedNamespace = namespace
	f.askedRelease = release
	f.askedRevisio = revision
	return f.detail, f.detailErr
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

func TestHelmServiceReadsOnePayloadOnlyWhenAskedForOne(t *testing.T) {
	t.Parallel()

	// THE TWO ACTS ARE SEPARATE AND THE COUNTERS PROVE IT. Listing a
	// cluster's releases must read no payload at all — that is the whole
	// design — and a payload read must name exactly one release and one
	// revision rather than a scope.
	helm := &fakeHelm{
		listing: domain.HelmListing{Status: domain.HelmListed},
		detail:  domain.HelmReleaseDetail{Name: "podinfo", Revision: 2},
	}
	service := newHelmService(t, helm)

	if _, err := service.ListReleases(context.Background(), "dev", "shop", false); err != nil {
		t.Fatalf("ListReleases() error = %v", err)
	}
	if helm.payloadReads != 0 {
		t.Fatalf("listing read %d payloads, want 0 — a list is built from labels", helm.payloadReads)
	}

	detail, err := service.ReadRelease(context.Background(), "dev", "shop", "podinfo", 2)
	if err != nil {
		t.Fatalf("ReadRelease() error = %v", err)
	}
	if helm.payloadReads != 1 {
		t.Fatalf("payload reads = %d, want exactly 1", helm.payloadReads)
	}
	if helm.askedRelease != "podinfo" || helm.askedRevisio != 2 {
		t.Fatalf("asked for %s v%d, want podinfo v2", helm.askedRelease, helm.askedRevisio)
	}
	if detail.Name != "podinfo" {
		t.Fatalf("detail name = %q, want podinfo", detail.Name)
	}
}

func TestHelmServiceAuditsAPayloadReadByReleaseAndRevisionAndNeverByValue(t *testing.T) {
	t.Parallel()

	// THE AUDIT LINE IS WHY THIS METHOD SITS IN THE APPLICATION LAYER AT
	// ALL. Reading a release payload is reading a Secret's contents, which
	// the doctrine permits only as a deliberate act somebody performed — and
	// an act nothing recorded is indistinguishable from one nobody
	// performed. So the line must name cluster, namespace, release and
	// revision, and must carry no part of the payload: not a value, not a
	// key, not a byte count. This is the same assertion SetSecretKey's own
	// comment describes, made executable.
	const (
		secretValue = "hunter2-do-not-log-me"
		notesValue  = "the admin password is hunter2-do-not-log-me"
	)

	var recorded bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&recorded, &slog.HandlerOptions{Level: slog.LevelDebug}))

	helm := &fakeHelm{detail: domain.HelmReleaseDetail{
		Name:     "podinfo",
		Revision: 2,
		Values:   "password: " + secretValue,
		Notes:    notesValue,
		Manifest: "apiVersion: v1\nkind: ConfigMap\n",
	}}

	registry := application.NewRegistry()
	registry.Open(mustCluster(t, "dev", true))
	service, err := application.NewHelmService(application.HelmServiceDeps{
		Helm:     helm,
		Registry: registry,
		Logger:   logger,
	})
	if err != nil {
		t.Fatalf("NewHelmService() error = %v", err)
	}

	if _, err := service.ReadRelease(context.Background(), "dev", "shop", "podinfo", 2); err != nil {
		t.Fatalf("ReadRelease() error = %v", err)
	}

	logged := recorded.String()

	// What the line MUST carry, because an audit entry that cannot say which
	// object was opened is not an audit entry.
	for _, want := range []string{"dev", "shop", "podinfo", "revision=2"} {
		if !strings.Contains(logged, want) {
			t.Errorf("the audit line does not name %q:\n%s", want, logged)
		}
	}

	// What it must NEVER carry. A log that holds what it was guarding undoes
	// the guard, and unlike the values on screen it outlives the window it
	// was read in.
	for _, forbidden := range []string{secretValue, notesValue, "hunter2"} {
		if strings.Contains(logged, forbidden) {
			t.Fatalf("the payload reached the log:\n%s", logged)
		}
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
