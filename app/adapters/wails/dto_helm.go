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

// --- The payload: one revision, read because somebody clicked ---------------
//
// EVERYTHING ABOVE THIS LINE IS BUILT FROM LABELS AND CROSSES NO SECRET
// CONTENTS AT ALL. Everything below it IS the contents of a Secret, and the
// difference is the whole design (decision 6, which is the Secrets doctrine
// applied rather than excepted). These types are reached only by
// HelmAPI.ReadRelease, which is called when somebody presses a button on one
// revision and by nothing else — never on render, never on the refresh tick.
//
// THE FIELD SET IS AN ALLOWLIST AND IS ASSERTED AGAINST A LITERAL LIST in
// dto_helm_test.go, the same guard notification_api_test.go puts on
// NotificationRequest and settingsFile.test.ts puts on the settings export.
// Adding a field here means editing that list, which is the point: this is
// the one DTO in PodSteer that carries Secret material, and the next field
// somebody puts on it should have to be argued for rather than merged.

// HelmChartIdentity is what a release says about the chart it came from.
//
// THE LIST'S TWO MISSING COLUMNS LIVE HERE. Chart name, chart version and app
// version are not labels — they exist only inside the payload — so the
// release list ships without them and they appear here, after the explicit
// click that reads one revision. Any field may be empty: a chart is not
// obliged to declare an appVersion.
type HelmChartIdentity struct {
	// Name is the chart's own name, which is NOT the release name — one
	// chart installs under as many release names as somebody likes.
	Name string `json:"name"`
	// Version is the chart version as the chart declared it.
	Version string `json:"version"`
	// AppVersion is the version of the application the chart packages, as
	// its author set it — nothing verifies it against what is running.
	AppVersion string `json:"appVersion"`
	// Description is the chart's own one-line description, when it has one.
	Description string `json:"description"`
}

// HelmReleaseDetail is one decoded revision.
//
// THREE OF ITS FIELDS ARE SECRET MATERIAL AND THEY ARE NOT ALIKE. `manifest`
// arrives ALREADY MASKED — the adapter replaced every value in every Secret
// document inside it with that value's decoded size, before the string
// crossed this boundary — so it needs no reveal timer. `values` and `notes`
// do: a chart puts a database password in its values and PodSteer cannot know
// which key that is, and a NOTES template is rendered from those same values
// and routinely prints one back. Both are governed in the webview by the same
// re-hideable, thirty-second, hidden-on-blur discipline a revealed Secret key
// already has — see web/src/stores/helmPayloads.svelte.ts.
type HelmReleaseDetail struct {
	// Namespace is where Helm stored the release.
	Namespace string `json:"namespace"`
	// Name is the release name, verified against the request before the
	// payload was decoded at all.
	Name string `json:"name"`
	// Revision is the revision read.
	Revision int `json:"revision"`
	// Status is the release status from INSIDE the payload, verbatim.
	Status string `json:"status"`
	// Chart is the chart identity — the list's two missing columns.
	Chart HelmChartIdentity `json:"chart"`
	// Description is Helm's own account of this revision ("Upgrade
	// complete", or the failure's own text).
	Description string `json:"description"`
	// Values are the values the release was installed WITH, as YAML —
	// Helm's `config`, which is what `helm get values` prints, never the
	// chart's defaults merged in. SECRET MATERIAL, under the reveal
	// discipline in the webview.
	Values string `json:"values"`
	// Notes is the rendered NOTES.txt. ALSO SECRET MATERIAL, and the one
	// people assume is not.
	Notes string `json:"notes"`
	// Manifest is the rendered manifest with every Secret document in it
	// ALREADY MASKED in the adapter. Needs no reveal timer, because there is
	// nothing left in it to time out.
	Manifest string `json:"manifest"`
	// MaskedDocuments is how many documents were masked. Shown beside the
	// manifest, because a masked value that does not say it was masked reads
	// as a Secret with an odd-looking value in it. Zero is the ordinary
	// answer: most charts render no Secret at all.
	MaskedDocuments int `json:"maskedDocuments"`
	// SecretName is the release Secret that was read. A handle, never a
	// payload.
	SecretName string `json:"secretName"`
}

// toHelmReleaseDetail converts one decoded revision for the wire.
//
// A STRAIGHT TRANSCRIPTION AND NOTHING ELSE. Nothing is redacted here and
// nothing may be: the manifest was masked in the ADAPTER, before this, and
// masking at this layer would mean the material had already crossed every
// boundary in between — which is the mistake GetManifest's own comment names.
func toHelmReleaseDetail(detail domain.HelmReleaseDetail) HelmReleaseDetail {
	return HelmReleaseDetail{
		Namespace: detail.Namespace.String(),
		Name:      detail.Name,
		Revision:  detail.Revision,
		Status:    string(detail.Status),
		Chart: HelmChartIdentity{
			Name:        detail.Chart.Name,
			Version:     detail.Chart.Version,
			AppVersion:  detail.Chart.AppVersion,
			Description: detail.Chart.Description,
		},
		Description:     detail.Description,
		Values:          detail.Values,
		Notes:           detail.Notes,
		Manifest:        detail.Manifest,
		MaskedDocuments: detail.MaskedDocuments,
		SecretName:      detail.SecretName,
	}
}
