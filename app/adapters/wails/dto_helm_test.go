package wails

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

// THE ALLOWLIST GUARD ON THE ONE DTO THAT CARRIES SECRET MATERIAL.
//
// This is the same guard notification_api_test.go puts on NotificationRequest
// and web/src/lib/settingsFile.test.ts puts on the settings export, and it is
// here for the same reason both of those exist: a redaction rule that is only
// a comment is a redaction rule that lapses. Every other Helm DTO is built
// from labels and could carry anything without consequence; this one is the
// decoded contents of a Secret, so a field cannot join it without somebody
// editing the literal list below and arguing for what they added.
func TestTheReleasePayloadCarriesExactlyTheAgreedFields(t *testing.T) {
	t.Parallel()

	// A LITERAL LIST, not derived from the type — deriving it from the type
	// would make the test agree with whatever the type says, which is the
	// one thing it must not do.
	want := []string{
		"namespace",
		"name",
		"revision",
		"status",
		"chart",
		"description",
		// The three that are Secret material. `values` and `notes` are under
		// the reveal discipline in the webview; `manifest` arrives already
		// masked from the adapter and needs no timer.
		"values",
		"notes",
		"manifest",
		"maskedDocuments",
		"secretName",
	}

	if got := jsonFieldNames(t, reflect.TypeOf(HelmReleaseDetail{})); !equalSets(got, want) {
		t.Errorf("HelmReleaseDetail fields = %v, want %v", got, want)
	}

	// The chart identity is checked too, because it is the one nested type
	// here and a spread of the payload into it would be invisible above.
	wantChart := []string{"name", "version", "appVersion", "description"}
	if got := jsonFieldNames(t, reflect.TypeOf(HelmChartIdentity{})); !equalSets(got, wantChart) {
		t.Errorf("HelmChartIdentity fields = %v, want %v", got, wantChart)
	}
}

func TestTheReleaseLISTINGGainsNoPayloadField(t *testing.T) {
	t.Parallel()

	// THE LISTING AND THE PAYLOAD ARE SEPARATE ACTS AND MUST STAY SEPARATE
	// TYPES. The listing is what the page renders on open, for every release
	// at once; a payload field arriving on it — a chart name "for the
	// column", a values preview — would mean the list reading every
	// release's Secret on page load, which is the bulk read the whole design
	// refuses and the exact repair decision 6 closes off by name.
	forbidden := []string{"values", "notes", "manifest", "chart", "appVersion"}

	for _, subject := range []struct {
		name   string
		typ    reflect.Type
		fields []string
	}{
		{"HelmListing", reflect.TypeOf(HelmListing{}), jsonFieldNames(t, reflect.TypeOf(HelmListing{}))},
		{"HelmRelease", reflect.TypeOf(HelmRelease{}), jsonFieldNames(t, reflect.TypeOf(HelmRelease{}))},
		{"HelmRevision", reflect.TypeOf(HelmRevision{}), jsonFieldNames(t, reflect.TypeOf(HelmRevision{}))},
	} {
		for _, field := range subject.fields {
			for _, bad := range forbidden {
				if strings.EqualFold(field, bad) {
					t.Errorf("%s gained a %q field; the list is built from labels and reads no payload", subject.name, field)
				}
			}
		}
	}
}

func TestAPayloadIsTranscribedRatherThanRedactedAtThisLayer(t *testing.T) {
	t.Parallel()

	// The masking happens in the ADAPTER, before the string reaches here.
	// Masking at this layer would mean the material had already crossed
	// every boundary in between — the mistake GetManifest's own comment
	// names — so this asserts the conversion changes nothing, which is what
	// makes the adapter's placement the load-bearing one.
	detail := domain.HelmReleaseDetail{
		Namespace: "shop",
		Name:      "podinfo",
		Revision:  3,
		Status:    domain.HelmStatusDeployed,
		Chart: domain.HelmChartIdentity{
			Name:       "podinfo",
			Version:    "6.5.4",
			AppVersion: "6.5.4",
		},
		Description:     "Upgrade complete",
		Values:          "replicaCount: 3",
		Notes:           "the notes",
		Manifest:        "apiVersion: v1\nkind: Secret\ndata:\n  password: <hidden, 7 bytes>\n",
		MaskedDocuments: 1,
		SecretName:      "sh.helm.release.v1.podinfo.v3",
	}

	got := toHelmReleaseDetail(detail)

	if got.Values != detail.Values || got.Notes != detail.Notes || got.Manifest != detail.Manifest {
		t.Fatal("the conversion altered the payload; redaction belongs in the adapter and nowhere else")
	}
	if got.MaskedDocuments != 1 {
		t.Errorf("maskedDocuments = %d, want 1", got.MaskedDocuments)
	}
	if got.Chart.AppVersion != "6.5.4" {
		t.Errorf("app version = %q — the list's missing column has to survive to the pane", got.Chart.AppVersion)
	}
}

// jsonFieldNames returns the wire names of a DTO's fields.
func jsonFieldNames(t *testing.T, typ reflect.Type) []string {
	t.Helper()

	names := make([]string, 0, typ.NumField())
	for i := range typ.NumField() {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			t.Fatalf("%s.%s has no json tag; the wire contract must be explicit", typ.Name(), typ.Field(i).Name)
		}
		names = append(names, strings.Split(tag, ",")[0])
	}
	return names
}

func equalSets(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	left := append([]string(nil), got...)
	right := append([]string(nil), want...)
	sort.Strings(left)
	sort.Strings(right)
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
