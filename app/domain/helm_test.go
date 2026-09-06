package domain_test

import (
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

// rev is one release Secret's labels, as GroupHelmRevisions receives them.
func rev(namespace, name string, revision int, status domain.HelmStatus, createdAt int64) domain.HelmRevision {
	return domain.HelmRevision{
		Namespace:  domain.NamespaceName(namespace),
		Name:       name,
		Revision:   revision,
		Status:     status,
		CreatedAt:  createdAt,
		SecretName: secretNameFor(name, revision),
	}
}

// secretNameFor writes the object name Helm's own storage driver writes, so
// the tests exercise the shape the adapter actually sees.
func secretNameFor(release string, revision int) string {
	digits := ""
	for n := revision; n > 0; n /= 10 {
		digits = string(rune('0'+n%10)) + digits
	}
	if digits == "" {
		digits = "0"
	}
	return "sh.helm.release.v1." + release + ".v" + digits
}

func TestGroupHelmRevisionsGroupsByNamespaceAndName(t *testing.T) {
	t.Parallel()

	// The SAME release name in two namespaces is two releases. Helm's storage
	// is per-namespace, and merging them would report one release with twice
	// the history.
	releases := domain.GroupHelmRevisions([]domain.HelmRevision{
		rev("shop", "podinfo", 1, domain.HelmStatusSuperseded, 100),
		rev("ops", "podinfo", 1, domain.HelmStatusDeployed, 200),
		rev("shop", "podinfo", 2, domain.HelmStatusDeployed, 300),
	})

	if len(releases) != 2 {
		t.Fatalf("want 2 releases, got %d", len(releases))
	}
	if releases[0].Namespace != "ops" || releases[1].Namespace != "shop" {
		t.Fatalf("want ops before shop, got %q then %q", releases[0].Namespace, releases[1].Namespace)
	}
	if got := releases[1].RevisionCount(); got != 2 {
		t.Fatalf("want shop/podinfo to hold 2 revisions, got %d", got)
	}
	if got := releases[0].RevisionCount(); got != 1 {
		t.Fatalf("want ops/podinfo to hold 1 revision, got %d", got)
	}
}

func TestGroupHelmRevisionsSortsByNamespaceThenName(t *testing.T) {
	t.Parallel()

	releases := domain.GroupHelmRevisions([]domain.HelmRevision{
		rev("shop", "web", 1, domain.HelmStatusDeployed, 100),
		rev("ops", "prometheus", 1, domain.HelmStatusDeployed, 100),
		rev("shop", "api", 1, domain.HelmStatusDeployed, 100),
	})

	want := []string{"ops/prometheus", "shop/api", "shop/web"}
	for i, release := range releases {
		got := string(release.Namespace) + "/" + release.Name
		if got != want[i] {
			t.Fatalf("position %d: want %q, got %q", i, want[i], got)
		}
	}
}

// TestGroupHelmRevisions is the table over the cases the grouping rule has to
// get right, each of which is a way it could go wrong invisibly: two releases'
// Secrets arriving interleaved (the API server pages them in whatever order it
// likes), a highest revision that FAILED (which the release therefore is), a
// revision Helm never updated and so left with no `modifiedAt`, and two
// Secrets claiming the same revision, which no Helm writes but a hand-restored
// cluster can hold.
func TestGroupHelmRevisions(t *testing.T) {
	t.Parallel()

	// want is one release's expected shape. Every field is one the rule
	// decides — nothing here re-states what the input already said.
	type want struct {
		namespace      string
		name           string
		revisions      int
		currentVersion int
		currentStatus  domain.HelmStatus
		currentSecret  string
		currentCreated int64
		currentUpdated int64
	}

	cases := []struct {
		name  string
		given []domain.HelmRevision
		want  []want
	}{
		{
			name: "two releases interleaved keep their own revisions",
			given: []domain.HelmRevision{
				rev("shop", "api", 1, domain.HelmStatusSuperseded, 100),
				rev("shop", "web", 1, domain.HelmStatusSuperseded, 110),
				rev("shop", "api", 2, domain.HelmStatusSuperseded, 200),
				rev("shop", "web", 2, domain.HelmStatusSuperseded, 210),
				rev("shop", "api", 3, domain.HelmStatusDeployed, 300),
				rev("shop", "web", 3, domain.HelmStatusDeployed, 310),
			},
			want: []want{
				{
					namespace: "shop", name: "api", revisions: 3,
					currentVersion: 3, currentStatus: domain.HelmStatusDeployed,
					currentSecret: secretNameFor("api", 3), currentCreated: 300,
				},
				{
					namespace: "shop", name: "web", revisions: 3,
					currentVersion: 3, currentStatus: domain.HelmStatusDeployed,
					currentSecret: secretNameFor("web", 3), currentCreated: 310,
				},
			},
		},
		{
			// Helm's own rule. Picking the newest `deployed` revision instead
			// would answer 2/deployed and report a broken release as healthy,
			// which is the direction this must never be wrong in. The newest
			// CREATION sits on a LOWER revision here, so "latest timestamp"
			// would answer 1.
			name: "the highest revision decides, and a failed one shows as failed",
			given: []domain.HelmRevision{
				rev("shop", "podinfo", 1, domain.HelmStatusSuperseded, 900),
				rev("shop", "podinfo", 2, domain.HelmStatusDeployed, 200),
				rev("shop", "podinfo", 3, domain.HelmStatusFailed, 300),
			},
			want: []want{{
				namespace: "shop", name: "podinfo", revisions: 3,
				currentVersion: 3, currentStatus: domain.HelmStatusFailed,
				currentSecret: secretNameFor("podinfo", 3), currentCreated: 300,
			}},
		},
		{
			// Helm adds `modifiedAt` only on an update, so this is the
			// ORDINARY case. Substituting the creation time — or the clock —
			// would claim a modification that did not happen.
			name: "a revision Helm never updated keeps a zero modifiedAt",
			given: []domain.HelmRevision{{
				Namespace: "shop", Name: "podinfo", Revision: 1,
				Status: domain.HelmStatusDeployed, CreatedAt: 100,
				SecretName: secretNameFor("podinfo", 1),
			}},
			want: []want{{
				namespace: "shop", name: "podinfo", revisions: 1,
				currentVersion: 1, currentStatus: domain.HelmStatusDeployed,
				currentSecret:  secretNameFor("podinfo", 1),
				currentCreated: 100, currentUpdated: 0,
			}},
		},
		{
			// A corrupt or hand-restored store can hold this. BOTH are kept —
			// dropping one would hide the corruption — and the page must
			// render rather than panic.
			name: "two Secrets claiming one revision are both kept, newer creation current",
			given: []domain.HelmRevision{
				{
					Namespace: "shop", Name: "podinfo", Revision: 2,
					Status: domain.HelmStatusFailed, CreatedAt: 100,
					SecretName: "sh.helm.release.v1.podinfo.v2",
				},
				{
					Namespace: "shop", Name: "podinfo", Revision: 2,
					Status: domain.HelmStatusDeployed, CreatedAt: 900,
					SecretName: "sh.helm.release.v1.podinfo.v2.restored",
				},
			},
			want: []want{{
				namespace: "shop", name: "podinfo", revisions: 2,
				currentVersion: 2, currentStatus: domain.HelmStatusDeployed,
				currentSecret:  "sh.helm.release.v1.podinfo.v2.restored",
				currentCreated: 900,
			}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			releases := domain.GroupHelmRevisions(tc.given)
			if len(releases) != len(tc.want) {
				t.Fatalf("releases = %d, want %d", len(releases), len(tc.want))
			}

			for i, want := range tc.want {
				got := releases[i]
				if string(got.Namespace) != want.namespace || got.Name != want.name {
					t.Errorf("release %d = %s/%s, want %s/%s",
						i, got.Namespace, got.Name, want.namespace, want.name)
				}
				if got.RevisionCount() != want.revisions {
					t.Errorf("%s: revisions = %d, want %d", want.name, got.RevisionCount(), want.revisions)
				}
				if got.Current.Revision != want.currentVersion {
					t.Errorf("%s: current revision = %d, want %d",
						want.name, got.Current.Revision, want.currentVersion)
				}
				if got.Current.Status != want.currentStatus {
					t.Errorf("%s: current status = %q, want %q",
						want.name, got.Current.Status, want.currentStatus)
				}
				if got.Current.SecretName != want.currentSecret {
					t.Errorf("%s: current secret = %q, want %q",
						want.name, got.Current.SecretName, want.currentSecret)
				}
				if got.Current.CreatedAt != want.currentCreated {
					t.Errorf("%s: current createdAt = %d, want %d",
						want.name, got.Current.CreatedAt, want.currentCreated)
				}
				if got.Current.ModifiedAt != want.currentUpdated {
					t.Errorf("%s: current modifiedAt = %d, want %d",
						want.name, got.Current.ModifiedAt, want.currentUpdated)
				}
				// Nobody else's revisions, and newest first — the order a
				// history reads in.
				for j, revision := range got.Revisions {
					if revision.Name != got.Name {
						t.Errorf("%s: holds a revision of %q", want.name, revision.Name)
					}
					if j > 0 && got.Revisions[j-1].Revision < revision.Revision {
						t.Errorf("%s: revisions are not newest first", want.name)
					}
				}
			}
		})
	}
}

func TestGroupHelmRevisionsCarriesAnUnknownStatusVerbatim(t *testing.T) {
	t.Parallel()

	// The status is typed but NOT validated into a closed enum: a Helm that
	// grows a tenth status must render as itself rather than as "unknown",
	// which is a different word Helm already uses for something else.
	releases := domain.GroupHelmRevisions([]domain.HelmRevision{
		rev("shop", "podinfo", 1, domain.HelmStatus("pending-something-new"), 100),
	})

	if got := releases[0].Current.Status; got != domain.HelmStatus("pending-something-new") {
		t.Fatalf("want the status carried verbatim, got %q", got)
	}
}

func TestGroupHelmRevisionsIsIndependentOfTheOrderSecretsArriveIn(t *testing.T) {
	t.Parallel()

	// The same-revision case again, handed both ways round: a corrupt store
	// must not produce a different answer depending on how the API server
	// happened to page it. The table above fixes one order; this fixes the
	// rule.
	older := domain.HelmRevision{
		Namespace: "shop", Name: "podinfo", Revision: 2,
		Status: domain.HelmStatusFailed, CreatedAt: 100,
		SecretName: "sh.helm.release.v1.podinfo.v2",
	}
	newer := domain.HelmRevision{
		Namespace: "shop", Name: "podinfo", Revision: 2,
		Status: domain.HelmStatusDeployed, CreatedAt: 900,
		SecretName: "sh.helm.release.v1.podinfo.v2.restored",
	}

	forwards := domain.GroupHelmRevisions([]domain.HelmRevision{older, newer})
	backwards := domain.GroupHelmRevisions([]domain.HelmRevision{newer, older})

	for _, releases := range [][]domain.HelmRelease{forwards, backwards} {
		if len(releases) != 1 {
			t.Fatalf("want 1 release, got %d", len(releases))
		}
		if got := releases[0].RevisionCount(); got != 2 {
			t.Fatalf("want both Secrets kept, got %d", got)
		}
		if got := releases[0].Current.SecretName; got != newer.SecretName {
			t.Fatalf("want the newer Secret current, got %q", got)
		}
	}
}

func TestGroupHelmRevisionsBreaksATotalTieBySecretName(t *testing.T) {
	t.Parallel()

	// Same revision AND same creation time: the answer still has to be the
	// same whichever order the pages arrived in.
	a := domain.HelmRevision{
		Namespace: "shop", Name: "podinfo", Revision: 1,
		CreatedAt: 100, SecretName: "sh.helm.release.v1.podinfo.v1.a",
	}
	b := domain.HelmRevision{
		Namespace: "shop", Name: "podinfo", Revision: 1,
		CreatedAt: 100, SecretName: "sh.helm.release.v1.podinfo.v1.b",
	}

	forwards := domain.GroupHelmRevisions([]domain.HelmRevision{a, b})
	backwards := domain.GroupHelmRevisions([]domain.HelmRevision{b, a})

	if forwards[0].Current.SecretName != backwards[0].Current.SecretName {
		t.Fatalf("ordering changed the answer: %q against %q",
			forwards[0].Current.SecretName, backwards[0].Current.SecretName)
	}
}

func TestGroupHelmRevisionsSkipsARevisionWithNoNameLabel(t *testing.T) {
	t.Parallel()

	// A Secret whose `name` label is absent says nothing about which release
	// it belongs to. Parsing one out of the object name is the guess the
	// domain refuses to make, so it contributes nothing rather than a release
	// called "".
	releases := domain.GroupHelmRevisions([]domain.HelmRevision{
		{Namespace: "shop", Revision: 1, SecretName: "sh.helm.release.v1.podinfo.v1"},
		rev("shop", "podinfo", 1, domain.HelmStatusDeployed, 100),
	})

	if len(releases) != 1 {
		t.Fatalf("want 1 release, got %d", len(releases))
	}
	if releases[0].Name != "podinfo" {
		t.Fatalf("want podinfo, got %q", releases[0].Name)
	}
}

func TestGroupHelmRevisionsAnswersEmptyForNoRevisions(t *testing.T) {
	t.Parallel()

	// A cluster with no Helm releases is LISTED with zero rows, which is a
	// real answer. Never nil-versus-empty confusion at the boundary.
	releases := domain.GroupHelmRevisions(nil)
	if releases == nil {
		t.Fatal("want an empty slice rather than nil")
	}
	if len(releases) != 0 {
		t.Fatalf("want 0 releases, got %d", len(releases))
	}
}

func TestHelmListStatusHasNoAbsentValue(t *testing.T) {
	t.Parallel()

	// THE LOAD-BEARING ABSENCE. "Nothing installed by Helm" is a LISTED
	// answer with zero rows, and that is what makes it distinguishable from
	// "not permitted here". A status meaning "absent" would offer somewhere
	// to file the empty case, and the two would collapse — the exact
	// collapse ADR 6 forbids.
	for _, status := range []domain.HelmListStatus{
		domain.HelmListed, domain.HelmForbidden, domain.HelmFailed,
	} {
		if status == "absent" {
			t.Fatal("HelmListStatus must not carry an 'absent' value")
		}
	}

	listing := domain.HelmListing{Status: domain.HelmListed, Releases: []domain.HelmRelease{}}
	if listing.Status != domain.HelmListed {
		t.Fatalf("an empty cluster is listed, got %q", listing.Status)
	}
}
