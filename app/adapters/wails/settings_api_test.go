package wails

// Tests for the two pieces of judgement this adapter adds on top of the
// service: which contexts a merged entry lost and to whom, and the ONE line
// the pane shows when the settings file is not in an ordinary state.
//
// Both are here rather than in the interface deliberately. Shadowing is a
// statement about client-go's merge rule and belongs beside the code that
// composes the precedence; the notice is the whole of what an operator can act
// on, and a sentence assembled from flags in two layers ends up saying two
// things.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

func TestShadowedContextsNameTheEntryThatWon(t *testing.T) {
	t.Parallel()

	entries := []domain.KubeconfigEntry{
		{
			Path:     "/home/op/.kube/config",
			Kind:     domain.SourceFile,
			Origin:   domain.OriginDefault,
			Contexts: []string{"prod", "staging"},
		},
		{
			Path:     "/home/op/synced",
			Kind:     domain.SourceDirectory,
			Origin:   domain.OriginSettings,
			Contexts: []string{"prod", "sandbox"},
		},
	}

	got := toKubeconfigSources(entries)
	if len(got) != 2 {
		t.Fatalf("sources = %d, want 2", len(got))
	}

	// The winner shadows nothing.
	if len(got[0].ShadowedBy) != 0 {
		t.Errorf("the first entry reports %v as shadowed", got[0].ShadowedBy)
	}

	// The loser names the file that won, so an operator whose source is being
	// ignored can see why rather than concluding the source is broken.
	if won := got[1].ShadowedBy["prod"]; won != "/home/op/.kube/config" {
		t.Errorf("prod shadowed by %q, want the explicit kubeconfig", won)
	}
	if _, shadowed := got[1].ShadowedBy["sandbox"]; shadowed {
		t.Error("a context nothing else defines was reported as shadowed")
	}
}

func TestOnlyASettingsEntryIsEditable(t *testing.T) {
	t.Parallel()

	got := toKubeconfigSources([]domain.KubeconfigEntry{
		{Path: "/home/op/.kube/config", Origin: domain.OriginDefault},
		{Path: "/opt/kubeconfigs", Origin: domain.OriginEnvironment},
		{Path: "/home/op/mine.yaml", Origin: domain.OriginSettings},
	})

	// Nothing in this application can change an environment variable or the
	// operator's own $KUBECONFIG, so a remove button on those rows would be
	// a control that lies.
	if got[0].Editable || got[1].Editable {
		t.Error("an environment-derived row was reported as editable")
	}
	if !got[2].Editable {
		t.Error("the operator's own source was reported as read-only")
	}
}

// The bindings type every list as `T[] | null`, and a component reading a nil
// slice as null is the exact bug Overview.unavailable already caused once.
func TestEveryListOnASourceIsNonNil(t *testing.T) {
	t.Parallel()

	got := toKubeconfigSources([]domain.KubeconfigEntry{
		{Path: "/gone", Origin: domain.OriginSettings, Missing: true},
	})
	if got[0].Files == nil || got[0].Contexts == nil || got[0].ShadowedBy == nil {
		t.Errorf("a missing source carries nil lists: %+v", got[0])
	}
}

func TestTheReportIsAlwaysASliceEvenWhenEmpty(t *testing.T) {
	t.Parallel()

	if got := toKubeconfigSources(nil); got == nil {
		t.Error("an empty report marshals to null rather than []")
	}
}

func TestTheNoticeSaysTheOneThingWorthActingOn(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		state domain.SettingsState
		// want is a phrase the sentence must contain; empty means no notice.
		want string
	}{
		"an ordinary file": {
			state: domain.SettingsState{Path: "/x/settings.json"},
			want:  "",
		},
		"from a newer PodSteer": {
			state: domain.SettingsState{FromFuture: true, Version: 2},
			want:  "newer version of PodSteer",
		},
		"a process that does not save settings": {
			state: domain.SettingsState{ReadOnly: true, Path: "/x/settings.json"},
			want:  "not saving settings",
		},
		"no configuration directory": {
			state: domain.SettingsState{ReadOnly: true, Path: ""},
			want:  "could not find a configuration directory",
		},
		"unreadable": {
			state: domain.SettingsState{Path: "/x/settings.json", Unreadable: true},
			want:  "renamed with an .invalid suffix",
		},
		"some values repaired": {
			state: domain.SettingsState{Path: "/x/settings.json", Repaired: 2},
			want:  "fell back to their defaults",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := settingsNotice(tc.state)
			if tc.want == "" {
				if got != "" {
					t.Errorf("notice = %q, want none", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("notice = %q, want it to mention %q", got, tc.want)
			}
		})
	}
}

// A file from the future outranks everything else it could also be: an
// operator whose changes are not sticking needs to be told THAT, not that
// three fields were clamped.
func TestTheFromFutureNoticeWinsOverTheOthers(t *testing.T) {
	t.Parallel()

	notice := settingsNotice(domain.SettingsState{
		Path:       "/x/settings.json",
		FromFuture: true,
		Version:    9,
		Repaired:   3,
	})
	if !strings.Contains(notice, "newer version of PodSteer") {
		t.Errorf("notice = %q, want the from-the-future sentence", notice)
	}
	if strings.Contains(notice, "fell back") {
		t.Errorf("notice = %q, want only one sentence", notice)
	}
}

func TestClusterSettingsCarriesOnlyTheFieldsItWasArguedFor(t *testing.T) {
	t.Parallel()

	// THE GUARD, and it is the same shape notification_api_test.go uses for
	// NotificationRequest and settingsFile.test.ts uses for the export: a
	// LITERAL field list, so nothing can join this DTO without somebody
	// editing this list and arguing for it.
	//
	// It matters more here than on most DTOs, because two of these fields ARE
	// object names. preferredNamespace and preferredService name a monitoring
	// Service, and they are a NAMED, DISCLOSED EXCEPTION to the claim
	// SECURITY.md makes about PodSteer's settings file — see
	// domain.PreferredBackend. An exception stays an exception only while it
	// is a fixed, short list, which is what this asserts.
	//
	// clusterId is a kubeconfig CONTEXT NAME, travelling on exactly the terms
	// the settings file and a desktop notification already let one travel: a
	// handle the operator's own machine gives them, identifying nothing
	// inside any cluster.
	want := []string{
		"clusterId",
		"nodeHistory",
		"metricsQueryMode",
		"preferredNamespace",
		"preferredService",
		"fleetPolicy",
	}

	shape := reflect.TypeOf(ClusterSettings{})
	got := make([]string, 0, shape.NumField())
	for i := range shape.NumField() {
		tag, _, _ := strings.Cut(shape.Field(i).Tag.Get("json"), ",")
		got = append(got, tag)
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ClusterSettings fields = %v, want exactly %v", got, want)
	}
}

func TestClusterSettingsIsEveryFieldStringsAndOneBool(t *testing.T) {
	t.Parallel()

	// The DTO is deliberately flat and stringly typed: every enum crosses as
	// its own wire value and is validated in the domain, so the bridge cannot
	// be the place a new mode is silently accepted. A field of any other kind
	// arriving here would be a nested shape — which is how a map of object
	// names gets in without anybody noticing.
	shape := reflect.TypeOf(ClusterSettings{})
	for i := range shape.NumField() {
		field := shape.Field(i)
		switch field.Type.Kind() {
		case reflect.String, reflect.Bool:
		default:
			t.Errorf("field %s is a %s; only strings and bools belong here", field.Name, field.Type)
		}
	}
}

func TestToClusterSettingsRendersTheDefaultsAsWordsRatherThanBlanks(t *testing.T) {
	t.Parallel()

	// An unconfigured cluster crosses the bridge as "off" and "filter", never
	// as two empty strings the interface would have to know the defaults for.
	// The same reason domain.Settings.Cluster is total: absence must not
	// become a second place the default is decided.
	got := toClusterSettings("prod", domain.DefaultClusterSettings())

	if got.ClusterId != "prod" {
		t.Errorf("clusterId = %q, want prod", got.ClusterId)
	}
	if got.MetricsQueryMode != string(domain.MetricsQueryOff) {
		t.Errorf("mode = %q, want off", got.MetricsQueryMode)
	}
	if got.FleetPolicy != string(domain.FleetFilter) {
		t.Errorf("fleetPolicy = %q, want filter", got.FleetPolicy)
	}
	if got.PreferredNamespace != "" || got.PreferredService != "" {
		t.Errorf("preferred = %q/%q, want both empty", got.PreferredNamespace, got.PreferredService)
	}
	if got.NodeHistory {
		t.Error("nodeHistory = true, want false by default")
	}
}
