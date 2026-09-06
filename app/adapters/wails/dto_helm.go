package wails

import (
	"github.com/podsteer/podsteer/app/domain"
)

// Wire types for the Helm page. As with dto.go these are the frontend API
// contract — Wails generates TypeScript from them — so a field rename is a
// breaking change.
//
// EVERY SLICE IS BUILT NON-NIL, because a nil Go slice marshals as `null` and
// that has already been a real bug here: Overview.unavailable reached a
// component that dereferenced it, and was latent only because the field was
// rarely nil. The v3 bindings type these `T[] | null` honestly, and `callList`
// normalises a top-level list at the seam — but a slice NESTED on a DTO does
// not pass through that, so it is built here instead.
//
// THE LISTING CARRIES A STATUS BESIDE ITS ROWS rather than relying on an
// error, for the reason the RBAC DTOs do: being refused is an ordinary answer
// here. What makes it sharper than RBAC is that zero rows is ALSO an ordinary
// answer and a completely different one — see HelmListing.status.

// HelmRevision is one release revision, quoted from its Secret's labels.
type HelmRevision struct {
	// Namespace is where Helm stored the release, which is not always where
	// the chart's own objects landed.
	Namespace string `json:"namespace"`
	// Name is the RELEASE name, from Helm's `name` label — never parsed out
	// of the Secret's own object name, which is a different string.
	Name string `json:"name"`
	// Revision is the revision number, from Helm's `version` label.
	Revision int `json:"revision"`
	// Status is Helm's own status word, verbatim: deployed, superseded,
	// failed, pending-upgrade and the rest. Not validated into a closed set,
	// so a status this build has never seen renders as itself.
	Status string `json:"status"`
	// CreatedAt is when Helm wrote this revision, as seconds since the Unix
	// epoch. Zero when the label was absent — never the current time.
	CreatedAt int64 `json:"createdAt"`
	// ModifiedAt is when Helm last updated it, as Unix seconds. Zero is the
	// ORDINARY case: Helm writes the label only on an update.
	ModifiedAt int64 `json:"modifiedAt"`
	// SecretName is the release Secret's own object name, so the detail can
	// be opened in the Secrets catalogue. A handle, never a payload.
	SecretName string `json:"secretName"`
}

// HelmRelease is one release: its current revision and every revision found.
//
// NO CHART AND NO APP VERSION, deliberately. Both live only inside the
// release payload — a base64'd gzip'd megabyte — and Kubernetes offers no
// server-side projection that would fetch them without fetching everything
// around them. Reading forty of those to fill two columns on page open is the
// bulk Secret read the whole design refuses; the stated cost is that
// `helm list`'s CHART and APP VERSION columns are absent here.
type HelmRelease struct {
	// Namespace is where Helm stored the release.
	Namespace string `json:"namespace"`
	// Name is the release name.
	Name string `json:"name"`
	// Current is the HIGHEST-numbered revision, which is Helm's own rule for
	// which revision a release is — so a release whose newest revision failed
	// shows as failed, which is a fact worth showing.
	Current HelmRevision `json:"current"`
	// RevisionCount is how many revisions the listing found. Every revision
	// is its own Secret, so a release upgraded two hundred times has two
	// hundred of them.
	RevisionCount int `json:"revisionCount"`
	// Revisions is every revision found, newest first, so a history drawer
	// costs no further read.
	Revisions []HelmRevision `json:"revisions"`
}

// HelmListing is one answer to "what has Helm installed here".
type HelmListing struct {
	// Releases are the releases found, by namespace then name. Never null.
	Releases []HelmRelease `json:"releases"`
	// Status is "listed", "forbidden" or "failed".
	//
	// THERE IS NO "ABSENT". A cluster with no Helm releases is LISTED with
	// zero rows, and that is what keeps "Helm installed nothing here"
	// distinguishable from "your account may not look" — the one collapse
	// this feature's decision record forbids by name.
	Status string `json:"status"`
	// Refusal is the sentence to show when Status is not "listed". It names
	// the permission that would fix a refusal.
	Refusal string `json:"refusal"`
	// Truncated reports that the listing hit its own cap and more release
	// Secrets exist than were read.
	Truncated bool `json:"truncated"`
	// ListedAt is when the listing was made, as seconds since the Unix epoch.
	// The page shows it as an "as of" time, because the answer is cached for
	// minutes rather than polled and a cache that cannot say its age is one
	// that lies.
	ListedAt int64 `json:"listedAt"`
	// Driver is the Helm storage driver read — always "secret". Carried so
	// the empty state can say which storage was looked in rather than
	// implying every driver was.
	Driver string `json:"driver"`
}

// toHelmRevision converts one revision for the wire.
func toHelmRevision(revision domain.HelmRevision) HelmRevision {
	return HelmRevision{
		Namespace:  revision.Namespace.String(),
		Name:       revision.Name,
		Revision:   revision.Revision,
		Status:     string(revision.Status),
		CreatedAt:  revision.CreatedAt,
		ModifiedAt: revision.ModifiedAt,
		SecretName: revision.SecretName,
	}
}

// toHelmRevisions converts a release's history. Always non-nil.
func toHelmRevisions(revisions []domain.HelmRevision) []HelmRevision {
	out := make([]HelmRevision, 0, len(revisions))
	for _, revision := range revisions {
		out = append(out, toHelmRevision(revision))
	}
	return out
}

// toHelmReleases converts the releases found. Always non-nil.
func toHelmReleases(releases []domain.HelmRelease) []HelmRelease {
	out := make([]HelmRelease, 0, len(releases))
	for _, release := range releases {
		out = append(out, HelmRelease{
			Namespace:     release.Namespace.String(),
			Name:          release.Name,
			Current:       toHelmRevision(release.Current),
			RevisionCount: release.RevisionCount(),
			Revisions:     toHelmRevisions(release.Revisions),
		})
	}
	return out
}

// toHelmListing converts a whole listing for the wire.
func toHelmListing(listing domain.HelmListing) HelmListing {
	driver := listing.Driver
	if driver == "" {
		driver = domain.HelmSecretDriver
	}
	return HelmListing{
		Releases:  toHelmReleases(listing.Releases),
		Status:    string(listing.Status),
		Refusal:   listing.Refusal,
		Truncated: listing.Truncated,
		ListedAt:  listing.ListedAt,
		Driver:    driver,
	}
}
