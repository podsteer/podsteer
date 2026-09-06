package wails

import (
	"errors"
	"log/slog"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// HelmAPI exposes what Helm has installed.
//
// ONE METHOD, IT IS A READ, AND IT IS NEVER ON THE REFRESH TICK. The page
// calls it when it opens and when somebody presses Refresh, and nothing else
// does. A navigator entry on the ordinary ten-second poll would issue a
// metadata LIST of Secrets every ten seconds — six `list secrets` lines a
// minute in the operator's audit log for as long as the page were left open,
// which is the Secrets doctrine's own signature with the bytes removed and
// the pattern intact.
//
// NOTHING HERE READS A RELEASE PAYLOAD. The list is built entirely from the
// labels Helm puts on each release Secret, through the metadata client, so no
// Secret contents cross the wire at all. Reading a payload is a separate,
// explicitly-clicked act with its own controls and is deliberately not on
// this surface yet.
type HelmAPI struct {
	helm ports.HelmService
	app  *App
	// logger receives the operation and the error, never a release name —
	// see apiError.
	logger *slog.Logger
}

// NewHelmAPI returns the bound Helm API.
func NewHelmAPI(helm ports.HelmService, app *App, logger *slog.Logger) (*HelmAPI, error) {
	switch {
	case helm == nil:
		return nil, errors.New("wails: HelmAPI requires a HelmService")
	case app == nil:
		return nil, errors.New("wails: HelmAPI requires an App")
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &HelmAPI{
		helm:   helm,
		app:    app,
		logger: logger.With(slog.String("api", "helm")),
	}, nil
}

// ListReleases returns what Helm has installed in one namespace, or
// cluster-wide when the namespace is blank or selects every namespace.
//
// Being refused is an ordinary answer and arrives as a STATUS on the listing
// rather than as a rejection, so the pane renders with a sentence naming the
// permission instead of blanking. A cluster with no Helm releases is a
// successful listing with zero rows, which is a different answer and reads as
// one.
//
// refresh bypasses the five-minute cache for this one call. The page's own
// Refresh control passes true; the page's first load passes false, and
// NOTHING on a timer calls this method at all.
func (h *HelmAPI) ListReleases(clusterID, namespace string, refresh bool) (HelmListing, error) {
	ctx, cancel := h.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return HelmListing{}, apiError(h.logger, "ListReleases", err)
	}

	name, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return HelmListing{}, apiError(h.logger, "ListReleases", err)
	}

	listing, err := h.helm.ListReleases(ctx, id, name, refresh)
	if err != nil {
		return HelmListing{}, apiError(h.logger, "ListReleases", err)
	}

	return toHelmListing(listing), nil
}

// ReadRelease reads ONE revision of ONE release, because somebody clicked.
//
// THE SECOND METHOD ON THIS SURFACE, AND A DIFFERENT ACT FROM THE FIRST.
// ListReleases transfers no Secret contents whatsoever; this reads a release
// payload whole, which is the act the Secrets doctrine governs. So it is
// shaped exactly as RevealSecretKey is: it happens because somebody pressed
// something, on one named revision, and it is audited in the application
// layer by cluster, namespace, release and revision — never a value.
//
// NOTHING MAY EVER CALL THIS ON RENDER OR ON A TICK. The page calls it from
// a button's handler and from nowhere else; a $effect that reached it would
// turn opening a drawer into a Secret read, which is the pattern Kubernetes'
// own guidance tells cluster operators to alert on and the exact thing this
// whole feature was permitted on the condition of not doing.
//
// A FAILURE IS A REJECTION HERE, unlike ListReleases where a refusal is an
// ordinary answer carried beside the rows. There is nothing to render without
// the payload, and an empty detail would let an empty values tab read as a
// release installed with no values. The two new codes — helm_payload_too_large
// and helm_payload_unreadable — and the Helm-specific not_found sentence are
// in errors.go.
func (h *HelmAPI) ReadRelease(clusterID, namespace, release string, revision int) (HelmReleaseDetail, error) {
	ctx, cancel := h.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return HelmReleaseDetail{}, apiError(h.logger, "ReadRelease", err)
	}

	name, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return HelmReleaseDetail{}, apiError(h.logger, "ReadRelease", err)
	}

	// A RELEASE PAYLOAD LIVES IN ONE NAMESPACE'S SECRET, so "all namespaces"
	// is not a scope this read has. Refused here rather than passed down,
	// because the alternative — composing a Secret name against an empty
	// namespace — is a GET against whatever the client's default happens to
	// be, which is exactly the class of quiet wrong-object read the
	// verification in the adapter exists to catch.
	if name.IsAll() {
		return HelmReleaseDetail{}, apiError(h.logger, "ReadRelease", errHelmNamespaceRequired)
	}

	detail, err := h.helm.ReadRelease(ctx, id, name, release, revision)
	if err != nil {
		return HelmReleaseDetail{}, apiError(h.logger, "ReadRelease", err)
	}

	return toHelmReleaseDetail(detail), nil
}
