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
