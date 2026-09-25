package domain

import "errors"

// ONE REVISION OF ONE RELEASE, READ BECAUSE SOMEBODY CLICKED.
//
// This file is the other half of helm.go, and the two are deliberately
// separate because they are separate ACTS. helm.go is built from labels and
// transfers no Secret contents at all; everything here is the contents of a
// Secret, read on an explicit click, one revision at a time (decision 6 in
// podsteer/business-docs, which is decision 3 — the Secrets doctrine —
// applied rather than excepted).
//
// THE SHAPE THIS INHERITS IS RevealSecretKey'S, and for the same reason. A
// chart that puts a database password in its values has put it in this blob,
// and PodSteer cannot know which key that is — so it neither redacts the
// values (which would hide the thing people open a values tab to read) nor
// pretends they are safe. What governs them instead is ADR 3's controls
// verbatim: shown on an explicit click, re-hideable, expiring after thirty
// seconds, and hidden again on window blur, which is when a screen share
// usually starts.
//
// THE RENDERED MANIFEST IS SECRET MATERIAL TOO, and that is the finding the
// decision record did not make. A chart that renders `kind: Secret` puts
// base64 `data:` values into the manifest string, and base64 is an encoding
// and not a cipher — the doctrine's own words. So the manifest is MASKED in
// the adapter before it crosses any boundary, with the same
// `<hidden, N bytes>` form the YAML tab uses, and a masked manifest then
// needs no reveal timer because there is nothing left in it to time out.
// Values and notes still need one: Helm's NOTES.txt is a template that
// routinely echoes `.Values` back, so notes are values wearing a different
// heading.
//
// NOTHING HERE IS A VERDICT. Every field is a quotation of what the payload
// said, which is why this file holds types and one error and no functions:
// the decode is a storage concern and lives in the adapter, beside the label
// names and the object-name shape it already owns.

// ErrHelmRevisionNotFound means the release Secret for one revision is not
// there.
//
// Its own sentinel ALONGSIDE ports.ErrNotFound rather than instead of it, so
// every existing errors.Is(err, ports.ErrNotFound) still answers true and the
// wire code stays not_found — while the message can say the thing that is
// actually useful here, which is that Helm keeps only the last few revisions.
// A release upgraded thirty times has ten Secrets by Helm's own default, so a
// history entry pointing at a revision whose Secret has been reaped is the
// ORDINARY case rather than a fault, and reporting it as a bare "no longer
// exists" reads as though something went wrong.
var ErrHelmRevisionNotFound = errors.New("that revision's release Secret is not in the cluster")

// HelmChartIdentity is what a release says about the chart it came from.
//
// THIS IS WHERE THE LIST'S MISSING COLUMNS FINALLY APPEAR. Chart name, chart
// version and app version are not labels — they exist only inside the payload
// — so the release list ships without them by design (see helm.go), and the
// honest place for them is here, after the explicit click that reads one
// revision. Filling two columns on a list would have meant reading every
// release's payload on page open, which is the bulk Secret read the whole
// design refuses.
//
// Every field is VERBATIM from the payload's chart metadata and any of them
// may be empty: a chart is not obliged to declare an appVersion, and one
// built by hand may declare almost nothing.
type HelmChartIdentity struct {
	// Name is the chart's own name, which is not the release name — one
	// chart installs under as many release names as somebody likes.
	Name string
	// Version is the chart version, as the chart declared it.
	Version string
	// AppVersion is the version of the application the chart packages, which
	// the chart's author sets and nothing verifies against what is running.
	AppVersion string
	// Description is the chart's own one-line description, when it has one.
	Description string
}

// HelmReleaseDetail is one revision of one release, decoded from its Secret.
//
// EVERY FIELD IS SOMETHING THE PANE SHOWS, which is the whole reason the
// adapter unmarshals into a narrow struct rather than into a general map: a
// release payload carries the chart's TEMPLATES and FILES — the whole chart,
// byte for byte — and nothing here displays them, so they are skipped at the
// decoder rather than held in memory and then ignored.
type HelmReleaseDetail struct {
	// Namespace is where Helm stored the release.
	Namespace NamespaceName
	// Name is the release name, as the payload states it. Verified against
	// the request before the payload was decoded at all.
	Name string
	// Revision is the revision read.
	Revision int
	// Status is the release status from inside the payload, verbatim, in
	// Helm's own vocabulary. It comes from the payload rather than from the
	// Secret's `status` label so the detail quotes what it decoded.
	Status HelmStatus
	// Chart is the chart identity — the list's two missing columns.
	Chart HelmChartIdentity
	// Description is Helm's own one-line account of what this revision was
	// ("Install complete", "Upgrade complete", or the failure's own text).
	Description string

	// Values are the values the release was installed or upgraded WITH, as
	// YAML — Helm's `config`, which is what `helm get values` prints, and
	// never the chart's own defaults merged in.
	//
	// SECRET MATERIAL, AND TREATED AS IT. This is the field a chart puts a
	// database password in. It is under the reveal discipline in full: an
	// explicit click, re-hideable, expiring, and dropped on window blur.
	Values string

	// Notes are the release's rendered NOTES.txt.
	//
	// ALSO SECRET MATERIAL, and it is the one people assume is not. A NOTES
	// template is rendered with the same `.Values` the values tab shows, and
	// the commonest thing a chart's notes do is print how to fetch the
	// admin password — several of them print it inline. So notes sit under
	// exactly the same discipline as values rather than beside them.
	Notes string

	// Manifest is the rendered manifest, WITH EVERY SECRET DOCUMENT IN IT
	// ALREADY MASKED.
	//
	// The masking happens in the adapter, before this string crosses any
	// boundary, exactly as GetManifest's does and for the identical reason:
	// masking after the fact means the material had already travelled
	// through every layer in between. A document that fails to parse is
	// passed through unchanged — it is text, not a Secret, and re-writing
	// text nothing understood would be the guess this codebase keeps
	// refusing to make.
	//
	// Because it arrives masked, it needs NO reveal timer and is not held
	// under one.
	Manifest string

	// MaskedDocuments is how many documents in the manifest were masked.
	//
	// Shown beside the manifest, because a masked value that does not say it
	// was masked reads as a Secret with an odd-looking value in it. Zero is
	// the ordinary answer: most charts render no Secret at all.
	MaskedDocuments int

	// SecretName is the release Secret this was read from, so the pane can
	// name exactly which object was opened. A handle, never a payload.
	SecretName string
}
