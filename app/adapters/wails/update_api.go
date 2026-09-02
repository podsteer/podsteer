package wails

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
)

// UpdateStatus is what the interface shows about newer releases.
type UpdateStatus struct {
	// State is "current", "available", "disabled" or "unknown".
	//
	// FOUR STATES, NOT A BOOLEAN, because they need different treatment and
	// three of them must be silent. "unknown" in particular is the ordinary
	// condition on an airgapped machine or behind a corporate NAT that has
	// spent GitHub's 60-an-hour anonymous budget, and rendering it as a
	// failure would put a permanent warning in front of somebody whose only
	// problem is a firewall doing its job.
	State string `json:"state"`
	// Installed is the running build.
	Installed string `json:"installed"`
	// Latest is the newest published release, empty unless one was read.
	Latest string `json:"latest"`
	// URL is the release page. Never a direct download: PodSteer does not
	// update itself, and the operator installs it however they did before.
	URL string `json:"url"`
}

// UpdateAPI answers whether a newer PodSteer has been published.
//
// NOTHING CALLS THIS UNLESS THE OPERATOR LEFT THE CHECK ON. There is no timer
// behind it and no goroutine: it acts when the interface asks, and the
// interface asks only when the setting says so. `PODSTEER_UPDATE_CHECK=false`
// stops it regardless, in the service, so no mistake in the UI can defeat it.
type UpdateAPI struct {
	updates *application.UpdateService
	logger  *slog.Logger
}

// NewUpdateAPI returns the bound update API.
func NewUpdateAPI(updates *application.UpdateService, logger *slog.Logger) (*UpdateAPI, error) {
	if updates == nil {
		return nil, fmt.Errorf("wails: UpdateAPI requires an UpdateService")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &UpdateAPI{updates: updates, logger: logger}, nil
}

// CheckForUpdate reports what is known about newer releases.
//
// Returns a status rather than an error in every case. A check that could not
// be made is not a failed operation — it is the "unknown" state, and raising
// it as an error would put a dialog in front of somebody about a network they
// may have deliberately closed.
func (a *UpdateAPI) CheckForUpdate(force bool) UpdateStatus {
	result := a.updates.Check(context.Background(), force)
	return toUpdateStatus(result)
}

// UpdateChecksPermitted reports whether this machine allows checking at all.
//
// So the Settings toggle can show itself as overridden rather than pretending
// to control something an administrator has already decided.
func (a *UpdateAPI) UpdateChecksPermitted() bool {
	return a.updates.Enabled()
}

func toUpdateStatus(result domain.UpdateCheck) UpdateStatus {
	return UpdateStatus{
		State:     string(result.State),
		Installed: result.Installed,
		Latest:    result.Latest,
		URL:       result.URL,
	}
}
