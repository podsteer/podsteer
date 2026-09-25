package domain

import "sort"

// A Helm release as PodSteer knows it: entirely from the LABELS Helm itself
// puts on each release Secret, and never from the payload inside one.
//
// THE PAYLOAD IS NOT READ HERE, AND THAT IS THE WHOLE DESIGN (decision 6 in
// podsteer/business-docs, "Helm releases are listed from labels and read one
// release at a time"). A release lives in a Secret of type
// `helm.sh/release.v1` holding a base64'd gzip'd JSON blob of roughly a
// megabyte — the chart, the values and the rendered manifest. Listing forty
// releases by reading forty of those is a bulk read of Secret contents on
// page open, which is precisely the pattern Kubernetes' own Secret
// good-practices page tells cluster operators to alert on, and which ADR 3
// exists to prevent. Helm labels every Secret it writes with the release
// name, the revision, the status and two timestamps, so a list of releases
// and their revision history can be built without a single byte of a payload
// crossing the wire.
//
// WHAT THAT COSTS IS STATED RATHER THAN HIDDEN: chart name, chart version and
// app version are NOT labels. They exist only inside the payload, and
// Kubernetes offers no server-side projection that would fetch them without
// fetching the megabyte around them. So HelmRelease deliberately carries no
// chart and no app version, `helm list`'s CHART and APP VERSION columns are
// deliberately absent from the list, and the honest place for them is the
// detail after an explicit click that reads ONE revision — which is a
// separate change and is not in this file.
//
// Everything in this file is a pure function of what the labels said. The one
// verdict it makes is GroupHelmRevisions; everything else is quotation.

// HelmListStatus says WHY a listing has no releases in it, when it has none.
//
// Modelled on domain.ReviewStatus and domain.MetricsStatus, and for the same
// reason those exist: an empty pane that cannot say which kind of empty it is
// sends somebody to fix the wrong thing.
//
// THERE IS DELIBERATELY NO "ABSENT" VALUE, and its absence is the point. A
// cluster with no Helm releases is LISTED, successfully, with zero rows —
// that is a real answer about a real cluster, and it is exactly what a GitOps
// estate rendered by Argo CD looks like. Adding an "absent" beside "listed"
// would offer somewhere to file "there was nothing here", and the zero-row
// listing would stop being distinguishable from a refusal, which is the one
// collapse ADR 6 forbids by name: `list secrets` is exactly the permission
// this page's likeliest readers do not hold, and the entry must never read as
// "no Helm here" when it means "not permitted here".
type HelmListStatus string

const (
	// HelmListed means the listing was made. It says nothing about how many
	// releases came back: zero is an ordinary and informative answer.
	HelmListed HelmListStatus = "listed"
	// HelmForbidden means this account may not list Secrets in this scope.
	// HTTP 403 — ordinary rather than exceptional, because plenty of
	// engineers deliberately hold no Secret access at all.
	HelmForbidden HelmListStatus = "forbidden"
	// HelmFailed means the listing could not be made for some other reason,
	// including the cluster being unreachable.
	HelmFailed HelmListStatus = "failed"
)

// HelmSecretDriver is the name of the storage backend PodSteer reads.
//
// Carried on every listing and shown where nothing is found, because Helm has
// three: Secrets (the default since Helm 3), ConfigMaps (`HELM_DRIVER=configmap`)
// and SQL. PodSteer reads only the first, and claiming to cover the other two
// without reading them would be worse than the sentence that says it does not.
const HelmSecretDriver = "secret"

// HelmRevision is one release Secret, quoted from its labels.
//
// EVERY FIELD IS A LABEL HELM WROTE, with one exception named below. The
// labels are set by `newSecretsObject` in Helm's own
// `pkg/storage/driver/secrets.go`: `name`, `owner=helm`, `status`, `version`
// and `createdAt`, plus `modifiedAt` when a revision is updated.
type HelmRevision struct {
	// Namespace is the namespace the release Secret is in — which is where
	// Helm stored the release, and is not always where the chart's own
	// objects landed.
	Namespace NamespaceName

	// Name is the RELEASE name, read from the `name` LABEL and never from the
	// Secret's own object name.
	//
	// THE TWO ARE NOT THE SAME STRING and reading the wrong one is a bug that
	// looks like it works: Helm names the object `sh.helm.release.v1.<release>.v<n>`,
	// so parsing a release name out of it would mean splitting on a dot in a
	// string whose middle segment is a release name that may itself contain
	// dots. The label is what Helm's own storage driver queries on, and it is
	// the only field here that says what the release is called.
	Name string

	// Revision is the release revision, from the `version` label. Helm
	// numbers these from 1 upwards and never reuses one.
	Revision int

	// Status is the release status, VERBATIM, from the `status` label.
	//
	// Typed for readability at call sites, and deliberately NOT validated
	// into a closed set. Helm's own vocabulary is `unknown`, `deployed`,
	// `uninstalled`, `superseded`, `failed`, `uninstalling`,
	// `pending-install`, `pending-upgrade` and `pending-rollback` — and a
	// future Helm may add a tenth. A value none of the constants below names
	// arrives here unaltered and renders as itself, the same rule the
	// operator panels follow for a controller's own enum.
	Status HelmStatus

	// CreatedAt is when Helm wrote this revision, from the `createdAt` label,
	// as the seconds since the Unix epoch that label holds. Zero when the
	// label was absent or unparseable — never "now", which would make a
	// missing timestamp read as a release that just happened.
	CreatedAt int64

	// ModifiedAt is when Helm last updated this revision's Secret, from the
	// `modifiedAt` label. Zero when absent, which is the ORDINARY case: Helm
	// adds the label only when a revision is updated in place, so a revision
	// written once and never touched has none.
	ModifiedAt int64

	// SecretName is the release Secret's own object name.
	//
	// Carried so the detail can be opened in the Secrets catalogue — the one
	// place a Secret is already browsable, under the ordinary per-key reveal
	// discipline. It is a handle, never a payload: nothing in this package
	// reads what is inside it.
	SecretName string
}

// HelmStatus is a Helm release status, as Helm wrote it.
//
// The constants name what Helm's `release.Status` defines today, for readable
// comparisons. They are NOT an exhaustive set and nothing validates against
// them — see HelmRevision.Status.
type HelmStatus string

const (
	// HelmStatusDeployed is the currently deployed revision.
	HelmStatusDeployed HelmStatus = "deployed"
	// HelmStatusFailed is a revision whose install or upgrade failed.
	HelmStatusFailed HelmStatus = "failed"
	// HelmStatusSuperseded is a revision a later one replaced.
	HelmStatusSuperseded HelmStatus = "superseded"
	// HelmStatusUninstalled is a release uninstalled with history kept.
	HelmStatusUninstalled HelmStatus = "uninstalled"
	// HelmStatusUninstalling is a release being uninstalled right now.
	HelmStatusUninstalling HelmStatus = "uninstalling"
	// HelmStatusPendingInstall is an install in flight.
	HelmStatusPendingInstall HelmStatus = "pending-install"
	// HelmStatusPendingUpgrade is an upgrade in flight.
	HelmStatusPendingUpgrade HelmStatus = "pending-upgrade"
	// HelmStatusPendingRollback is a rollback in flight.
	HelmStatusPendingRollback HelmStatus = "pending-rollback"
	// HelmStatusUnknown is Helm's own word for a status it does not know. It
	// is NOT what an unrecognised label becomes — that arrives verbatim.
	HelmStatusUnknown HelmStatus = "unknown"
)

// HelmRelease is one release: its current revision, and every revision found.
//
// NO CHART AND NO APP VERSION, and that is the record's explicit and stated
// cost rather than an oversight — see the file comment. Both live inside a
// payload nothing on this path reads.
type HelmRelease struct {
	// Namespace is where Helm stored the release.
	Namespace NamespaceName
	// Name is the release name, from the `name` label.
	Name string
	// Current is the highest-numbered revision found — Helm's own rule for
	// which revision a release IS. See GroupHelmRevisions.
	Current HelmRevision
	// Revisions is every revision found for this release, newest first, so a
	// history drawer needs no second read.
	Revisions []HelmRevision
}

// RevisionCount is how many revisions of this release were found.
//
// A method rather than a field so it can never disagree with the slice. It is
// what the listing came back with, not what Helm has kept: a truncated
// listing says so on the HelmListing rather than here.
func (r HelmRelease) RevisionCount() int { return len(r.Revisions) }

// HelmListing is one answer to "what has Helm installed here".
//
// The status sits BESIDE the releases rather than replacing them with an
// error, exactly as domain.SubjectRules and domain.ClusterRead do, because
// being refused is an ordinary answer here rather than a fault.
type HelmListing struct {
	// Releases are the releases found, by namespace then name. Empty with
	// Status HelmListed means the cluster genuinely holds no Helm releases —
	// which is the common case on a GitOps estate and is not a failure.
	Releases []HelmRelease
	// Status is listed, forbidden or failed.
	Status HelmListStatus
	// Refusal is the sentence to show when Status is not "listed". It names
	// the permission that would fix a refusal, because that is a fact about
	// the cluster's API rather than about the screen.
	Refusal string
	// Truncated reports that the listing hit its own cap and there are more
	// release Secrets than were read. A release with two hundred upgrades has
	// two hundred Secrets, so this is reachable on a cluster that raised
	// Helm's default history limit of ten.
	Truncated bool
	// ListedAt is when the listing was made, as seconds since the Unix epoch.
	//
	// Carried because the listing is CACHED for minutes rather than polled —
	// releases change on deploy cadence, not on a ten-second one, and a
	// navigator entry on the refresh tick would put six `list secrets` lines
	// a minute into somebody's audit log for as long as the page was left
	// open. A cached answer has to say how old it is or the cache is a lie.
	ListedAt int64
	// Driver is the Helm storage driver these releases were read from, which
	// is always HelmSecretDriver. Carried so the empty state can say which
	// storage was looked in rather than implying it looked in all of them.
	Driver string
}

// GroupHelmRevisions turns a flat set of release Secrets into releases.
//
// THIS IS THE ONE VERDICT IN THE FILE, and there are exactly three rules:
//
//   - Revisions are grouped by NAMESPACE AND NAME together. Two releases
//     called `podinfo` in two namespaces are two releases; Helm's own storage
//     is per-namespace and merging them would report one release with twice
//     the history and the wrong current revision.
//
//   - The HIGHEST revision is the current one — Helm's own rule, and not "the
//     newest timestamp" and not "the one whose status is deployed". The
//     consequence is deliberate and is worth having: a release whose newest
//     revision FAILED shows as failed. That is a fact an operator wants on
//     the row, and picking the newest `deployed` revision instead would
//     quietly report a broken release as healthy, which is the direction this
//     must never be wrong in.
//
//   - Releases are sorted by namespace, then name, so two calls over the same
//     cluster state produce the same slice — which is what makes a cached
//     answer comparable and a test deterministic.
//
// Within a release, revisions come back NEWEST FIRST, which is the order a
// history reads in.
//
// A CORRUPT STORE MUST NOT PANIC THE PAGE. Two Secrets claiming the same
// revision is not something Helm writes, but it is something a hand-edited or
// restored cluster can hold, so BOTH ARE KEPT — dropping one would hide the
// corruption — and the one with the newer creation timestamp wins the
// tie-break for "current". A tie on that too falls back to the Secret's own
// name, so the answer is still deterministic rather than dependent on the
// order the API server happened to page them in.
func GroupHelmRevisions(revisions []HelmRevision) []HelmRelease {
	type key struct {
		namespace NamespaceName
		name      string
	}

	grouped := make(map[key][]HelmRevision, len(revisions))
	order := make([]key, 0, len(revisions))

	for _, revision := range revisions {
		if revision.Name == "" {
			// A Secret whose `name` label is absent says nothing about which
			// release it belongs to. Attributing it to something by parsing
			// the object name would be the guess this file refuses to make.
			continue
		}
		id := key{namespace: revision.Namespace, name: revision.Name}
		if _, seen := grouped[id]; !seen {
			order = append(order, id)
		}
		grouped[id] = append(grouped[id], revision)
	}

	releases := make([]HelmRelease, 0, len(order))
	for _, id := range order {
		found := grouped[id]

		// Newest first, and total: a higher revision leads; a tie is broken
		// by the newer creation, then by the Secret name so nothing is left
		// to the order the pages arrived in.
		sort.SliceStable(found, func(i, j int) bool {
			left, right := found[i], found[j]
			if left.Revision != right.Revision {
				return left.Revision > right.Revision
			}
			if left.CreatedAt != right.CreatedAt {
				return left.CreatedAt > right.CreatedAt
			}
			return left.SecretName > right.SecretName
		})

		releases = append(releases, HelmRelease{
			Namespace: id.namespace,
			Name:      id.name,
			Current:   found[0],
			Revisions: found,
		})
	}

	sort.SliceStable(releases, func(i, j int) bool {
		if releases[i].Namespace != releases[j].Namespace {
			return releases[i].Namespace < releases[j].Namespace
		}
		return releases[i].Name < releases[j].Name
	})

	return releases
}
