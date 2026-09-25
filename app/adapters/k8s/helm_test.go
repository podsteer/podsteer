package k8s

import (
	"context"
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	metadatafake "k8s.io/client-go/metadata/fake"
	clientgotesting "k8s.io/client-go/testing"

	"github.com/podsteer/podsteer/app/domain"
)

// newReleaseSecret builds the PartialObjectMetadata a release Secret arrives
// as through the metadata client — the LABELS Helm's own storage driver
// writes, and the object name it writes them onto.
//
// The object name deliberately follows Helm's own shape
// (`sh.helm.release.v1.<release>.v<n>`) so the tests exercise the case that
// makes the `name` label load-bearing rather than incidental.
func newReleaseSecret(namespace, release string, revision string, status string, labels map[string]string) *metav1.PartialObjectMetadata {
	all := map[string]string{
		helmOwnerLabel:     helmOwnerValue,
		helmNameLabel:      release,
		helmVersionLabel:   revision,
		helmStatusLabel:    status,
		helmCreatedAtLabel: "1000",
	}
	for key, value := range labels {
		if value == "" {
			delete(all, key)
			continue
		}
		all[key] = value
	}

	return &metav1.PartialObjectMetadata{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      "sh.helm.release.v1." + release + ".v" + revision,
			Labels:    all,
		},
	}
}

// newHelmTestAdapter returns an Adapter whose metadata client for id is the
// one given — the same shape newUpgradeTestAdapter uses, minus discovery,
// which this path never touches.
func newHelmTestAdapter(id domain.ClusterID, meta *metadatafake.FakeMetadataClient) *Adapter {
	factory := newClientFactory(Config{})
	factory.clients[id] = &clients{meta: meta}
	return &Adapter{factory: factory}
}

func TestListHelmReleasesReadsTheLabelsAndGroupsThem(t *testing.T) {
	t.Parallel()

	meta := newFakeMetadataClient(t,
		newReleaseSecret("shop", "podinfo", "1", "superseded", nil),
		newReleaseSecret("shop", "podinfo", "2", "deployed", map[string]string{
			helmCreatedAtLabel:  "2000",
			helmModifiedAtLabel: "2500",
		}),
	)
	adapter := newHelmTestAdapter("dev", meta)

	listing, err := adapter.ListHelmReleases(context.Background(), "dev", "shop", false)
	if err != nil {
		t.Fatalf("ListHelmReleases() error = %v", err)
	}

	if listing.Status != domain.HelmListed {
		t.Fatalf("status = %q, want listed", listing.Status)
	}
	if listing.Driver != domain.HelmSecretDriver {
		t.Fatalf("driver = %q, want %q", listing.Driver, domain.HelmSecretDriver)
	}
	if len(listing.Releases) != 1 {
		t.Fatalf("want 1 release, got %d", len(listing.Releases))
	}

	release := listing.Releases[0]
	if release.Name != "podinfo" || release.Namespace != "shop" {
		t.Fatalf("release = %s/%s, want shop/podinfo", release.Namespace, release.Name)
	}
	if release.RevisionCount() != 2 {
		t.Fatalf("revisions = %d, want 2", release.RevisionCount())
	}
	if release.Current.Revision != 2 {
		t.Fatalf("current revision = %d, want 2", release.Current.Revision)
	}
	if release.Current.Status != domain.HelmStatusDeployed {
		t.Fatalf("current status = %q, want deployed", release.Current.Status)
	}
	if release.Current.CreatedAt != 2000 || release.Current.ModifiedAt != 2500 {
		t.Fatalf("timestamps = %d/%d, want 2000/2500",
			release.Current.CreatedAt, release.Current.ModifiedAt)
	}
	// The Secret NAME is carried so the detail can be opened in the Secrets
	// catalogue, and it is not what the release is called.
	if release.Current.SecretName != "sh.helm.release.v1.podinfo.v2" {
		t.Fatalf("secret name = %q", release.Current.SecretName)
	}
	if listing.ListedAt == 0 {
		t.Fatal("want a listedAt stamp: a cached answer that cannot say its age is a cache that lies")
	}
}

func TestListHelmReleasesTakesTheReleaseNameFromTheLabelNotTheSecretName(t *testing.T) {
	t.Parallel()

	// The `name` label and the object name are DIFFERENT strings, and a
	// release name may itself contain dots — so parsing one out of
	// `sh.helm.release.v1.<release>.v<n>` is the guess this must not make.
	secret := newReleaseSecret("shop", "my.app.v2", "3", "deployed", nil)
	adapter := newHelmTestAdapter("dev", newFakeMetadataClient(t, secret))

	listing, err := adapter.ListHelmReleases(context.Background(), "dev", "shop", false)
	if err != nil {
		t.Fatalf("ListHelmReleases() error = %v", err)
	}
	if len(listing.Releases) != 1 {
		t.Fatalf("want 1 release, got %d", len(listing.Releases))
	}
	if listing.Releases[0].Name != "my.app.v2" {
		t.Fatalf("release name = %q, want the label verbatim", listing.Releases[0].Name)
	}
}

func TestListHelmReleasesLeavesAMissingModifiedAtAtZero(t *testing.T) {
	t.Parallel()

	// Helm adds `modifiedAt` only on an update, so most revisions have none.
	// Zero, never now: substituting the clock would render as a release that
	// changed a moment ago.
	adapter := newHelmTestAdapter("dev", newFakeMetadataClient(t,
		newReleaseSecret("shop", "podinfo", "1", "deployed", nil),
	))

	listing, err := adapter.ListHelmReleases(context.Background(), "dev", "shop", false)
	if err != nil {
		t.Fatalf("ListHelmReleases() error = %v", err)
	}
	if got := listing.Releases[0].Current.ModifiedAt; got != 0 {
		t.Fatalf("modifiedAt = %d, want 0", got)
	}
}

func TestListHelmReleasesCarriesAnUnrecognisedStatusVerbatim(t *testing.T) {
	t.Parallel()

	adapter := newHelmTestAdapter("dev", newFakeMetadataClient(t,
		newReleaseSecret("shop", "podinfo", "1", "pending-something-new", nil),
	))

	listing, err := adapter.ListHelmReleases(context.Background(), "dev", "shop", false)
	if err != nil {
		t.Fatalf("ListHelmReleases() error = %v", err)
	}
	if got := listing.Releases[0].Current.Status; got != domain.HelmStatus("pending-something-new") {
		t.Fatalf("status = %q, want it carried verbatim", got)
	}
}

func TestListHelmReleasesSkipsASecretWithNoNameLabel(t *testing.T) {
	t.Parallel()

	unnamed := newReleaseSecret("shop", "podinfo", "1", "deployed", map[string]string{helmNameLabel: ""})
	adapter := newHelmTestAdapter("dev", newFakeMetadataClient(t,
		unnamed,
		newReleaseSecret("shop", "web", "1", "deployed", nil),
	))

	listing, err := adapter.ListHelmReleases(context.Background(), "dev", "shop", false)
	if err != nil {
		t.Fatalf("ListHelmReleases() error = %v", err)
	}
	if len(listing.Releases) != 1 || listing.Releases[0].Name != "web" {
		t.Fatalf("releases = %+v, want only web", listing.Releases)
	}
}

func TestListHelmReleasesOnAClusterWithNoneIsListedWithZeroRows(t *testing.T) {
	t.Parallel()

	// THE DISTINCTION THIS FEATURE TURNS ON. A cluster with no Helm releases
	// is LISTED, successfully, with zero rows — never "absent", and never
	// anything a refusal could be confused with.
	adapter := newHelmTestAdapter("dev", newFakeMetadataClient(t))

	listing, err := adapter.ListHelmReleases(context.Background(), "dev", "shop", false)
	if err != nil {
		t.Fatalf("ListHelmReleases() error = %v", err)
	}
	if listing.Status != domain.HelmListed {
		t.Fatalf("status = %q, want listed", listing.Status)
	}
	if len(listing.Releases) != 0 {
		t.Fatalf("want 0 releases, got %d", len(listing.Releases))
	}
	if listing.Refusal != "" {
		t.Fatalf("refusal = %q, want none on a successful listing", listing.Refusal)
	}
}

func TestListHelmReleasesReportsAForbiddenListingRatherThanAnEmptyOne(t *testing.T) {
	t.Parallel()

	// The rule ADR 6 states in as many words: a 403 must never collapse into
	// the zero state. Unlike the metrics-backend and vulnerability caches,
	// which cache a refusal AS an empty answer, this carries the refusal.
	meta := newFakeMetadataClient(t)
	meta.PrependReactor("list", "secrets", func(clientgotesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(
			schema.GroupResource{Resource: "secrets"}, "", errors.New("no list on secrets"))
	})
	adapter := newHelmTestAdapter("dev", meta)

	listing, err := adapter.ListHelmReleases(context.Background(), "dev", "shop", false)
	if err != nil {
		t.Fatalf("ListHelmReleases() error = %v, want a forbidden LISTING rather than an error", err)
	}
	if listing.Status != domain.HelmForbidden {
		t.Fatalf("status = %q, want forbidden", listing.Status)
	}
	if listing.Refusal == "" {
		t.Fatal("want a refusal sentence naming the permission")
	}
	if len(listing.Releases) != 0 {
		t.Fatalf("a refusal carries no releases, got %d", len(listing.Releases))
	}
}

func TestListHelmReleasesCachesARefusalWithItsError(t *testing.T) {
	t.Parallel()

	// An account that may never list Secrets must not have that retried into
	// its audit log on every page open — and what is cached must still be the
	// REFUSAL, not an empty listing.
	calls := 0
	meta := newFakeMetadataClient(t)
	meta.PrependReactor("list", "secrets", func(clientgotesting.Action) (bool, runtime.Object, error) {
		calls++
		return true, nil, apierrors.NewForbidden(
			schema.GroupResource{Resource: "secrets"}, "", errors.New("no list on secrets"))
	})
	adapter := newHelmTestAdapter("dev", meta)

	for i := 0; i < 3; i++ {
		listing, err := adapter.ListHelmReleases(context.Background(), "dev", "shop", false)
		if err != nil {
			t.Fatalf("call %d: error = %v", i, err)
		}
		if listing.Status != domain.HelmForbidden {
			t.Fatalf("call %d: status = %q, want the refusal held", i, listing.Status)
		}
	}
	if calls != 1 {
		t.Fatalf("the API server was asked %d times, want 1", calls)
	}
}

func TestListHelmReleasesDoesNotCacheATransportFailure(t *testing.T) {
	t.Parallel()

	// A cluster that was merely unreachable comes back, and should be asked
	// again when it does — the same discipline DiscoverMetricsBackend follows.
	calls := 0
	meta := newFakeMetadataClient(t)
	meta.PrependReactor("list", "secrets", func(clientgotesting.Action) (bool, runtime.Object, error) {
		calls++
		return true, nil, errors.New("dial tcp: connection refused")
	})
	adapter := newHelmTestAdapter("dev", meta)

	for i := 0; i < 2; i++ {
		if _, err := adapter.ListHelmReleases(context.Background(), "dev", "shop", false); err == nil {
			t.Fatalf("call %d: want an error for a transport failure", i)
		}
	}
	if calls != 2 {
		t.Fatalf("the API server was asked %d times, want 2 — a failure must not be cached", calls)
	}
}

func TestListHelmReleasesServesTheSecondCallFromTheCache(t *testing.T) {
	t.Parallel()

	calls := 0
	meta := newFakeMetadataClient(t, newReleaseSecret("shop", "podinfo", "1", "deployed", nil))
	meta.PrependReactor("list", "secrets", func(clientgotesting.Action) (bool, runtime.Object, error) {
		calls++
		return false, nil, nil
	})
	adapter := newHelmTestAdapter("dev", meta)

	for i := 0; i < 3; i++ {
		if _, err := adapter.ListHelmReleases(context.Background(), "dev", "shop", false); err != nil {
			t.Fatalf("call %d: error = %v", i, err)
		}
	}
	if calls != 1 {
		t.Fatalf("the API server was asked %d times, want 1", calls)
	}
}

func TestListHelmReleasesKeysTheCacheByNamespaceScope(t *testing.T) {
	t.Parallel()

	// "All namespaces" is its own read rather than a merge of the others, so
	// it must not be answered from a namespace-scoped entry.
	calls := 0
	meta := newFakeMetadataClient(t, newReleaseSecret("shop", "podinfo", "1", "deployed", nil))
	meta.PrependReactor("list", "secrets", func(clientgotesting.Action) (bool, runtime.Object, error) {
		calls++
		return false, nil, nil
	})
	adapter := newHelmTestAdapter("dev", meta)

	if _, err := adapter.ListHelmReleases(context.Background(), "dev", "shop", false); err != nil {
		t.Fatalf("scoped listing error = %v", err)
	}
	if _, err := adapter.ListHelmReleases(context.Background(), "dev", domain.NamespaceAll, false); err != nil {
		t.Fatalf("cluster-wide listing error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("the API server was asked %d times, want 2 — the scopes are different reads", calls)
	}
}

func TestListHelmReleasesRefreshBypassesTheCacheAndReplacesIt(t *testing.T) {
	t.Parallel()

	// Without a bypass the page's own Refresh would re-show the same
	// five-minute-old listing and look broken, which would make the "as of"
	// time beside it decorative rather than actionable.
	calls := 0
	meta := newFakeMetadataClient(t, newReleaseSecret("shop", "podinfo", "1", "deployed", nil))
	meta.PrependReactor("list", "secrets", func(clientgotesting.Action) (bool, runtime.Object, error) {
		calls++
		return false, nil, nil
	})
	adapter := newHelmTestAdapter("dev", meta)

	if _, err := adapter.ListHelmReleases(context.Background(), "dev", "shop", false); err != nil {
		t.Fatalf("first listing error = %v", err)
	}
	if _, err := adapter.ListHelmReleases(context.Background(), "dev", "shop", true); err != nil {
		t.Fatalf("refresh error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("the API server was asked %d times, want 2 — refresh must bypass", calls)
	}

	// And the bypassed call REPLACES what was held, so the next ordinary read
	// is served from the fresh answer rather than the one it went around.
	if _, err := adapter.ListHelmReleases(context.Background(), "dev", "shop", false); err != nil {
		t.Fatalf("third listing error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("the API server was asked %d times, want 2 — refresh must repopulate", calls)
	}
}

func TestListHelmReleasesRefreshReAsksAfterARefusal(t *testing.T) {
	t.Parallel()

	// A refusal is cached so it is not retried on every page open — but an
	// operator who has just been granted the permission has asked for exactly
	// one more attempt, by pressing something.
	calls := 0
	meta := newFakeMetadataClient(t)
	meta.PrependReactor("list", "secrets", func(clientgotesting.Action) (bool, runtime.Object, error) {
		calls++
		return true, nil, apierrors.NewForbidden(
			schema.GroupResource{Resource: "secrets"}, "", errors.New("no list on secrets"))
	})
	adapter := newHelmTestAdapter("dev", meta)

	if _, err := adapter.ListHelmReleases(context.Background(), "dev", "shop", false); err != nil {
		t.Fatalf("first listing error = %v", err)
	}
	if _, err := adapter.ListHelmReleases(context.Background(), "dev", "shop", true); err != nil {
		t.Fatalf("refresh error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("the API server was asked %d times, want 2", calls)
	}
}

func TestListHelmReleasesForgetsACluster(t *testing.T) {
	t.Parallel()

	// forget is wired into both Invalidate and forgetReads, so a reconnect
	// and a write PodSteer made each drop the listing.
	calls := 0
	meta := newFakeMetadataClient(t, newReleaseSecret("shop", "podinfo", "1", "deployed", nil))
	meta.PrependReactor("list", "secrets", func(clientgotesting.Action) (bool, runtime.Object, error) {
		calls++
		return false, nil, nil
	})
	adapter := newHelmTestAdapter("dev", meta)

	if _, err := adapter.ListHelmReleases(context.Background(), "dev", "shop", false); err != nil {
		t.Fatalf("first listing error = %v", err)
	}
	adapter.helm.forget("dev")
	if _, err := adapter.ListHelmReleases(context.Background(), "dev", "shop", false); err != nil {
		t.Fatalf("second listing error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("the API server was asked %d times, want 2 after a forget", calls)
	}
}

func TestListHelmReleasesSelectsOnTheOwnerLabelAndTheReleaseType(t *testing.T) {
	t.Parallel()

	// The label selector is primary and the field selector rides alongside.
	// Asserted on the ACTION rather than on the result, because the fake's
	// tracker does not apply a field selector — the point here is that the
	// request carries both.
	var (
		labelSelector string
		fieldSelector string
	)
	meta := newFakeMetadataClient(t)
	meta.PrependReactor("list", "secrets", func(action clientgotesting.Action) (bool, runtime.Object, error) {
		list, ok := action.(clientgotesting.ListActionImpl)
		if ok {
			labelSelector = list.ListRestrictions.Labels.String()
			fieldSelector = list.ListRestrictions.Fields.String()
		}
		return false, nil, nil
	})
	adapter := newHelmTestAdapter("dev", meta)

	if _, err := adapter.ListHelmReleases(context.Background(), "dev", "shop", false); err != nil {
		t.Fatalf("ListHelmReleases() error = %v", err)
	}
	if labelSelector != "owner=helm" {
		t.Fatalf("label selector = %q, want owner=helm", labelSelector)
	}
	if fieldSelector != "type=helm.sh/release.v1" {
		t.Fatalf("field selector = %q, want type=helm.sh/release.v1", fieldSelector)
	}
}

func TestHelmTimestampReadsSecondsAndRefusesAnythingElse(t *testing.T) {
	t.Parallel()

	cases := map[string]int64{
		"1725580800": 1725580800,
		" 1000 ":     1000,
		"":           0,
		"not a time": 0,
		"-5":         0,
	}
	for value, want := range cases {
		if got := helmTimestamp(value); got != want {
			t.Errorf("helmTimestamp(%q) = %d, want %d", value, got, want)
		}
	}
}
