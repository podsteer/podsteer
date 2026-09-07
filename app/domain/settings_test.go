package domain_test

// Tests for the two halves of the settings value's contract, which pull in
// opposite directions on purpose:
//
//   - Normalise NEVER fails. It is what a file read at startup goes through,
//     and a hand-edited value must not be able to stop PodSteer opening.
//   - Validate REFUSES. It is what a change from the interface goes through,
//     and a bad value there is a bug worth not persisting.

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

func TestDefaultSettingsAreTodaysBehaviour(t *testing.T) {
	t.Parallel()

	settings := domain.DefaultSettings()

	if settings.History.Retention.Days != 1 {
		t.Errorf("retention = %d days, want 1", settings.History.Retention.Days)
	}
	if settings.History.SamplingInterval != domain.DefaultSamplingInterval {
		t.Errorf("interval = %v, want the default", settings.History.SamplingInterval)
	}
	// `environment` is byte-for-byte what PodSteer did before a proxy setting
	// existed: Go's own transport reading HTTPS_PROXY and NO_PROXY. Deleting
	// the settings file has to be a safe act, and that is only true while the
	// defaults are the previous behaviour.
	if settings.Proxy.Mode != domain.ProxyFromEnvironment {
		t.Errorf("proxy mode = %q, want %q", settings.Proxy.Mode, domain.ProxyFromEnvironment)
	}
	if len(settings.Kubeconfig.Sources) != 0 {
		t.Errorf("sources = %+v, want none", settings.Kubeconfig.Sources)
	}
	if err := settings.Validate(); err != nil {
		t.Errorf("the defaults do not validate: %v", err)
	}
}

func TestNormaliseClampsAndCounts(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in        domain.Settings
		wantReset int
		check     func(*testing.T, domain.Settings)
	}{
		"nothing to do": {
			in:        domain.DefaultSettings(),
			wantReset: 0,
		},
		"retention past the ceiling": {
			in: domain.Settings{
				History: domain.HistorySettings{
					Retention:        domain.Retention{Days: 5000},
					SamplingInterval: domain.DefaultSamplingInterval,
				},
				Proxy: domain.ProxySettings{Mode: domain.ProxyFromEnvironment},
			},
			wantReset: 1,
			check: func(t *testing.T, got domain.Settings) {
				if got.History.Retention.Days != domain.MaxRetentionDays {
					t.Errorf("retention = %d, want %d", got.History.Retention.Days, domain.MaxRetentionDays)
				}
			},
		},
		"a cadence below the floor": {
			in: domain.Settings{
				History: domain.HistorySettings{
					Retention:        domain.NewRetention(1),
					SamplingInterval: time.Second,
				},
				Proxy: domain.ProxySettings{Mode: domain.ProxyFromEnvironment},
			},
			wantReset: 1,
			check: func(t *testing.T, got domain.Settings) {
				if got.History.SamplingInterval != domain.MinSamplingInterval {
					t.Errorf("interval = %v, want the floor", got.History.SamplingInterval)
				}
			},
		},
		"a relative source path": {
			in: settingsWithSources(
				domain.KubeconfigSource{Path: "configs/team.yaml", Kind: domain.SourceFile},
			),
			wantReset: 1,
			check: func(t *testing.T, got domain.Settings) {
				if len(got.Kubeconfig.Sources) != 0 {
					t.Errorf("sources = %+v, want the relative one dropped", got.Kubeconfig.Sources)
				}
			},
		},
		"a source with no kind becomes a file": {
			in: settingsWithSources(
				domain.KubeconfigSource{Path: "/home/op/.kube/team.yaml"},
			),
			wantReset: 0,
			check: func(t *testing.T, got domain.Settings) {
				if len(got.Kubeconfig.Sources) != 1 || got.Kubeconfig.Sources[0].Kind != domain.SourceFile {
					t.Errorf("sources = %+v, want one file", got.Kubeconfig.Sources)
				}
			},
		},
		"a source with a kind nobody understands": {
			in: settingsWithSources(
				domain.KubeconfigSource{Path: "/home/op/.kube", Kind: "symlink"},
			),
			wantReset: 1,
		},
		"the same path listed twice": {
			in: settingsWithSources(
				domain.KubeconfigSource{Path: "/home/op/.kube/a.yaml", Kind: domain.SourceFile},
				domain.KubeconfigSource{Path: "/home/op/.kube/./a.yaml", Kind: domain.SourceFile},
			),
			wantReset: 1,
			check: func(t *testing.T, got domain.Settings) {
				if len(got.Kubeconfig.Sources) != 1 {
					t.Errorf("sources = %+v, want the duplicate collapsed", got.Kubeconfig.Sources)
				}
			},
		},
		"a proxy mode nobody understands": {
			in: domain.Settings{
				History: domain.DefaultSettings().History,
				Proxy:   domain.ProxySettings{Mode: "somehow"},
			},
			wantReset: 1,
			check: func(t *testing.T, got domain.Settings) {
				if got.Proxy.Mode != domain.ProxyFromEnvironment {
					t.Errorf("proxy mode = %q, want the default", got.Proxy.Mode)
				}
			},
		},
		"a manual proxy carrying a credential": {
			in: domain.Settings{
				History: domain.DefaultSettings().History,
				Proxy: domain.ProxySettings{
					Mode: domain.ProxyManual,
					URL:  "http://op:hunter2@proxy.internal:3128",
				},
			},
			wantReset: 1,
			check: func(t *testing.T, got domain.Settings) {
				// The whole proxy goes, not only the userinfo: keeping the
				// host without the credential would silently point PodSteer
				// at a proxy that then refuses every request, which is a
				// worse failure than falling back to the environment.
				if got.Proxy.URL != "" || got.Proxy.Mode != domain.ProxyFromEnvironment {
					t.Errorf("proxy = %+v, want it reset entirely", got.Proxy)
				}
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.in
			reset := got.Normalise()
			if reset != tc.wantReset {
				t.Errorf("Normalise() = %d, want %d", reset, tc.wantReset)
			}
			if tc.check != nil {
				tc.check(t, got)
			}
		})
	}
}

// Order is precedence, so Normalise must never reorder the list on the
// operator's behalf.
func TestNormaliseKeepsTheSourceOrder(t *testing.T) {
	t.Parallel()

	settings := settingsWithSources(
		domain.KubeconfigSource{Path: "/z/last.yaml", Kind: domain.SourceFile},
		domain.KubeconfigSource{Path: "/a/first.yaml", Kind: domain.SourceFile},
	)
	settings.Normalise()

	if settings.Kubeconfig.Sources[0].Path != "/z/last.yaml" {
		t.Errorf("sources were reordered: %+v", settings.Kubeconfig.Sources)
	}
}

func TestValidateRefusesWhatTheInterfaceMustNotPersist(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in   domain.Settings
		want error
	}{
		"a relative source path": {
			in:   settingsWithSources(domain.KubeconfigSource{Path: "configs", Kind: domain.SourceFile}),
			want: domain.ErrSettingsSourcePath,
		},
		"an empty source path": {
			in:   settingsWithSources(domain.KubeconfigSource{Path: "  ", Kind: domain.SourceFile}),
			want: domain.ErrSettingsSourcePath,
		},
		"an unknown source kind": {
			in:   settingsWithSources(domain.KubeconfigSource{Path: "/home/op", Kind: "socket"}),
			want: domain.ErrSettingsSourceKind,
		},
		"an unknown proxy mode": {
			in: domain.Settings{
				History: domain.DefaultSettings().History,
				Proxy:   domain.ProxySettings{Mode: "sometimes"},
			},
			want: domain.ErrSettingsProxyMode,
		},
		"a manual proxy with no URL": {
			in: domain.Settings{
				History: domain.DefaultSettings().History,
				Proxy:   domain.ProxySettings{Mode: domain.ProxyManual},
			},
			want: domain.ErrSettingsProxyURL,
		},
		"a manual proxy that is not a URL": {
			in: domain.Settings{
				History: domain.DefaultSettings().History,
				Proxy:   domain.ProxySettings{Mode: domain.ProxyManual, URL: "proxy.internal:3128"},
			},
			want: domain.ErrSettingsProxyURL,
		},
		"a proxy URL carrying a password": {
			in: domain.Settings{
				History: domain.DefaultSettings().History,
				Proxy: domain.ProxySettings{
					Mode: domain.ProxyManual,
					URL:  "http://op:hunter2@proxy.internal:3128",
				},
			},
			want: domain.ErrSettingsProxyCredential,
		},
		"a proxy URL carrying only a username": {
			in: domain.Settings{
				History: domain.DefaultSettings().History,
				Proxy: domain.ProxySettings{
					Mode: domain.ProxyManual,
					URL:  "http://op@proxy.internal:3128",
				},
			},
			want: domain.ErrSettingsProxyCredential,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := tc.in.Validate(); !errors.Is(err, tc.want) {
				t.Errorf("Validate() = %v, want %v", err, tc.want)
			}
		})
	}
}

// No credential in a file PodSteer writes. This is the file that ends up in a
// support bundle, so the refusal is a policy rather than a syntax check, and a
// test rather than a comment.
func TestAProxyCredentialIsRefusedWhateverTheScheme(t *testing.T) {
	t.Parallel()

	for _, scheme := range []string{"http", "https", "socks5", "socks5h"} {
		settings := domain.Settings{
			History: domain.DefaultSettings().History,
			Proxy: domain.ProxySettings{
				Mode: domain.ProxyManual,
				URL:  scheme + "://op:hunter2@proxy.internal:3128",
			},
		}
		if err := settings.Validate(); !errors.Is(err, domain.ErrSettingsProxyCredential) {
			t.Errorf("%s: Validate() = %v, want ErrSettingsProxyCredential", scheme, err)
		}
	}
}

func TestValidateAcceptsAProxyWithoutOne(t *testing.T) {
	t.Parallel()

	settings := domain.Settings{
		History: domain.DefaultSettings().History,
		Proxy: domain.ProxySettings{
			Mode:    domain.ProxyManual,
			URL:     "http://proxy.internal:3128",
			NoProxy: "localhost,.internal",
		},
	}
	if err := settings.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestCloneSharesNothingWithTheOriginal(t *testing.T) {
	t.Parallel()

	original := settingsWithSources(
		domain.KubeconfigSource{Path: "/home/op/.kube/team.yaml", Kind: domain.SourceFile},
	)
	original.Clusters = map[string]domain.ClusterSettings{"dev": {}}

	clone := original.Clone()
	clone.Kubeconfig.Sources[0].Path = "/tampered"
	clone.Clusters["prod"] = domain.ClusterSettings{}

	if original.Kubeconfig.Sources[0].Path != "/home/op/.kube/team.yaml" {
		t.Errorf("the clone shares its sources: %+v", original.Kubeconfig.Sources)
	}
	if _, leaked := original.Clusters["prod"]; leaked {
		t.Error("the clone shares its per-cluster map")
	}
}

// The interface must never be able to offer a remove button on a row it
// cannot remove.
func TestOnlyASettingsOriginIsEditable(t *testing.T) {
	t.Parallel()

	if !domain.OriginSettings.IsEditable() {
		t.Error("a settings source is not editable")
	}
	for _, origin := range []domain.KubeconfigOrigin{domain.OriginDefault, domain.OriginEnvironment} {
		if origin.IsEditable() {
			t.Errorf("%q is editable, but nothing here can change it", origin)
		}
	}
}

func TestSettingsStateIsWritable(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		state domain.SettingsState
		want  bool
	}{
		"ordinary":                   {state: domain.SettingsState{Path: "/x"}, want: true},
		"read-only":                  {state: domain.SettingsState{ReadOnly: true}, want: false},
		"from a newer PodSteer":      {state: domain.SettingsState{FromFuture: true, Version: 2}, want: false},
		"unreadable but replaceable": {state: domain.SettingsState{Unreadable: true}, want: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := tc.state.IsWritable(); got != tc.want {
				t.Errorf("IsWritable() = %v, want %v", got, tc.want)
			}
		})
	}
}

// settingsWithSources returns valid defaults carrying the given sources.
func settingsWithSources(sources ...domain.KubeconfigSource) domain.Settings {
	settings := domain.DefaultSettings()
	settings.Kubeconfig.Sources = sources
	return settings
}

// --- per-cluster switches ---------------------------------------------------
//
// One section shared by two features (node history, ADR 8; querying a
// discovered monitoring backend, ADR 7), so the properties worth pinning are
// about the SECTION as much as about either: that absence and the defaults are
// the same thing, that a stanza saying nothing does not survive to the file,
// and that the one object name this settings file may hold is refused when it
// could not name anything.

func TestClusterReportsTheDefaultsForAClusterWithNoEntry(t *testing.T) {
	t.Parallel()

	// TOTAL BY CONSTRUCTION. A consumer indexing the map itself would get a
	// value with empty enums in it and would have to decide what "" means —
	// and for a setting that governs whether PromQL reaches somebody's
	// production Prometheus, "off" and "undefined" must not be the same
	// guess made twice.
	settings := domain.DefaultSettings()

	got := settings.Cluster("never-opened")

	if got != domain.DefaultClusterSettings() {
		t.Fatalf("Cluster() = %+v, want the defaults", got)
	}
	if got.MetricsQuery.Mode != domain.MetricsQueryOff {
		t.Errorf("mode = %q, want %q", got.MetricsQuery.Mode, domain.MetricsQueryOff)
	}
	if got.MetricsQuery.Fleet != domain.FleetFilter {
		t.Errorf("fleet = %q, want %q", got.MetricsQuery.Fleet, domain.FleetFilter)
	}
	if !got.MetricsQuery.Preferred.IsZero() {
		t.Errorf("preferred = %+v, want none", got.MetricsQuery.Preferred)
	}
}

func TestClusterFillsTheBlanksAnOlderFileLeft(t *testing.T) {
	t.Parallel()

	// A file written before these fields existed carries an entry with empty
	// enums. Reading it must not hand a consumer "" — and must not report the
	// mode as anything but off, which is the direction a value nobody wrote
	// has to resolve in.
	settings := domain.DefaultSettings()
	settings.Clusters["prod"] = domain.ClusterSettings{NodeHistory: true}

	got := settings.Cluster("prod")

	if !got.NodeHistory {
		t.Error("NodeHistory was lost")
	}
	if got.MetricsQuery.Mode != domain.MetricsQueryOff {
		t.Errorf("mode = %q, want %q", got.MetricsQuery.Mode, domain.MetricsQueryOff)
	}
	if got.MetricsQuery.Fleet != domain.FleetFilter {
		t.Errorf("fleet = %q, want %q", got.MetricsQuery.Fleet, domain.FleetFilter)
	}
}

func TestNormaliseResetsAnUnusableModeOrFleetPolicy(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in        domain.ClusterSettings
		wantReset int
		wantMode  domain.MetricsQueryMode
		wantFleet domain.FleetPolicy
	}{
		"a mode this build has never heard of": {
			in: domain.ClusterSettings{
				NodeHistory:  true,
				MetricsQuery: domain.MetricsQuerySettings{Mode: "always", Fleet: domain.FleetFilter},
			},
			wantReset: 1,
			// OFF, never something that sends a query: a value nobody here
			// wrote must not be resolved in the direction of talking to a
			// third system.
			wantMode:  domain.MetricsQueryOff,
			wantFleet: domain.FleetFilter,
		},
		"a fleet policy this build has never heard of": {
			in: domain.ClusterSettings{
				NodeHistory:  true,
				MetricsQuery: domain.MetricsQuerySettings{Mode: domain.MetricsQueryAuto, Fleet: "merge"},
			},
			wantReset: 1,
			wantMode:  domain.MetricsQueryAuto,
			wantFleet: domain.FleetFilter,
		},
		"both at once": {
			in: domain.ClusterSettings{
				NodeHistory:  true,
				MetricsQuery: domain.MetricsQuerySettings{Mode: "yes", Fleet: "no"},
			},
			wantReset: 2,
			wantMode:  domain.MetricsQueryOff,
			wantFleet: domain.FleetFilter,
		},
		"blanks are not resets": {
			// A file written before the field existed. Filling it is not a
			// repair, and counting it would put a warning in the pane on
			// every launch after an upgrade.
			in:        domain.ClusterSettings{NodeHistory: true},
			wantReset: 0,
			wantMode:  domain.MetricsQueryOff,
			wantFleet: domain.FleetFilter,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			settings := domain.DefaultSettings()
			settings.Clusters["prod"] = test.in

			if got := settings.Normalise(); got != test.wantReset {
				t.Errorf("Normalise() = %d resets, want %d", got, test.wantReset)
			}

			cluster := settings.Cluster("prod")
			if cluster.MetricsQuery.Mode != test.wantMode {
				t.Errorf("mode = %q, want %q", cluster.MetricsQuery.Mode, test.wantMode)
			}
			if cluster.MetricsQuery.Fleet != test.wantFleet {
				t.Errorf("fleet = %q, want %q", cluster.MetricsQuery.Fleet, test.wantFleet)
			}
		})
	}
}

func TestNormaliseDropsAPreferredBackendThatCouldNameNothing(t *testing.T) {
	t.Parallel()

	// DROPPED RATHER THAN DEFAULTED, for the reason a bad kubeconfig source
	// is dropped: a pick has no default other than having no pick, and having
	// no pick is exactly what falling back to the ranked candidate means.
	settings := domain.DefaultSettings()
	settings.Clusters["prod"] = domain.ClusterSettings{
		MetricsQuery: domain.MetricsQuerySettings{
			Mode:      domain.MetricsQueryManual,
			Preferred: domain.PreferredBackend{Namespace: "Monitoring", Service: "prometheus"},
			Fleet:     domain.FleetFilter,
		},
	}

	if got := settings.Normalise(); got != 1 {
		t.Fatalf("Normalise() = %d resets, want 1", got)
	}

	cluster := settings.Cluster("prod")
	if !cluster.MetricsQuery.Preferred.IsZero() {
		t.Errorf("preferred = %+v, want it dropped", cluster.MetricsQuery.Preferred)
	}
	// The rest of the entry survives: one unusable field must not cost the
	// operator the decision they made beside it.
	if cluster.MetricsQuery.Mode != domain.MetricsQueryManual {
		t.Errorf("mode = %q, want it kept", cluster.MetricsQuery.Mode)
	}
}

func TestNormaliseDropsAClusterEntryThatSaysNothing(t *testing.T) {
	t.Parallel()

	// So the file does not grow a stanza per cluster ever connected, each of
	// them naming a context for no reason. NOT counted as a reset: that count
	// becomes "some settings held values PodSteer could not use", and an
	// empty stanza held no such value.
	settings := domain.DefaultSettings()
	settings.Clusters["opened-once"] = domain.ClusterSettings{}
	settings.Clusters["turned-back-off"] = domain.DefaultClusterSettings()
	settings.Clusters["still-says-something"] = domain.ClusterSettings{
		MetricsQuery: domain.MetricsQuerySettings{Mode: domain.MetricsQueryManual},
	}

	if got := settings.Normalise(); got != 0 {
		t.Fatalf("Normalise() = %d resets, want 0 — an empty stanza is not a repair", got)
	}

	if _, kept := settings.Clusters["opened-once"]; kept {
		t.Error("an entry that says nothing survived")
	}
	if _, kept := settings.Clusters["turned-back-off"]; kept {
		t.Error("an entry equal to the defaults survived")
	}
	if _, kept := settings.Clusters["still-says-something"]; !kept {
		t.Error("an entry carrying a decision was dropped")
	}
}

func TestValidateRefusesAMetricsQueryValueTheInterfaceMustNotPersist(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		query domain.MetricsQuerySettings
		want  error
	}{
		"an unknown mode": {
			query: domain.MetricsQuerySettings{Mode: "always", Fleet: domain.FleetFilter},
			want:  domain.ErrSettingsMetricsQueryMode,
		},
		"an unknown fleet policy": {
			query: domain.MetricsQuerySettings{Mode: domain.MetricsQueryOff, Fleet: "merge"},
			want:  domain.ErrSettingsFleetPolicy,
		},
		"a namespace that is not a DNS-1123 label": {
			query: domain.MetricsQuerySettings{
				Mode:      domain.MetricsQueryManual,
				Preferred: domain.PreferredBackend{Namespace: "Monitoring", Service: "prometheus"},
				Fleet:     domain.FleetFilter,
			},
			want: domain.ErrSettingsPreferredBackend,
		},
		"a service that is not a DNS-1123 label": {
			query: domain.MetricsQuerySettings{
				Mode:      domain.MetricsQueryManual,
				Preferred: domain.PreferredBackend{Namespace: "monitoring", Service: "prom_operated"},
				Fleet:     domain.FleetFilter,
			},
			want: domain.ErrSettingsPreferredBackend,
		},
		"half a pick, which could never resolve": {
			query: domain.MetricsQuerySettings{
				Mode:      domain.MetricsQueryManual,
				Preferred: domain.PreferredBackend{Namespace: "monitoring"},
				Fleet:     domain.FleetFilter,
			},
			want: domain.ErrSettingsPreferredBackend,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			settings := domain.DefaultSettings()
			settings.Clusters["prod"] = domain.ClusterSettings{MetricsQuery: test.query}

			err := settings.Validate()
			if !errors.Is(err, test.want) {
				t.Fatalf("Validate() error = %v, want %v", err, test.want)
			}
			// The refusal names the cluster, because an operator with twelve
			// tabs open cannot act on one that does not.
			if !strings.Contains(err.Error(), "prod") {
				t.Errorf("Validate() error = %v, want it to name the cluster", err)
			}
		})
	}
}

func TestValidateAcceptsAPickThatNamesRealLabels(t *testing.T) {
	t.Parallel()

	// The one object name this file may hold, and it is only usable when it
	// could actually name a Service the API server would serve.
	settings := domain.DefaultSettings()
	settings.Clusters["prod"] = domain.ClusterSettings{
		NodeHistory: true,
		MetricsQuery: domain.MetricsQuerySettings{
			Mode:      domain.MetricsQueryAuto,
			Preferred: domain.PreferredBackend{Namespace: "monitoring", Service: "prometheus-operated"},
			Fleet:     domain.FleetRefuse,
		},
	}

	if err := settings.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want it accepted", err)
	}
}

func TestValidateAcceptsAnEntryWithNoPickAtAll(t *testing.T) {
	t.Parallel()

	// The ordinary case, and the one in which no object name is persisted:
	// the empty pick means "whatever discovery ranks first".
	settings := domain.DefaultSettings()
	settings.Clusters["prod"] = domain.ClusterSettings{
		MetricsQuery: domain.MetricsQuerySettings{Mode: domain.MetricsQueryManual, Fleet: domain.FleetFilter},
	}

	if err := settings.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want it accepted", err)
	}
}

func TestValidateAcceptsAnEntryWrittenBeforeTheseFieldsExisted(t *testing.T) {
	t.Parallel()

	// An entry with empty enums is "not set", not invalid. Refusing it would
	// make a file this build itself can read unwritable, which is the one
	// outcome the read and write paths must never disagree about.
	settings := domain.DefaultSettings()
	settings.Clusters["prod"] = domain.ClusterSettings{NodeHistory: true}

	if err := settings.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want it accepted", err)
	}
}

// TestProxyDialerLeavesTheEnvironmentAloneByDefault is the mode that must not
// change behaviour, and the reason Dialer returns nil rather than a no-op.
//
// net/http and client-go both read HTTPS_PROXY, HTTP_PROXY and NO_PROXY when
// no dialer is set. An operator on a corporate laptop depends on that without
// having configured anything here, and a no-op function would silently take it
// away the first time this setting was written for an unrelated reason.
func TestProxyDialerLeavesTheEnvironmentAloneByDefault(t *testing.T) {
	t.Parallel()

	for _, mode := range []domain.ProxyMode{domain.ProxyFromEnvironment, ""} {
		dialer, err := domain.ProxySettings{Mode: mode}.Dialer()
		if err != nil && mode == domain.ProxyFromEnvironment {
			t.Fatalf("Dialer() error = %v", err)
		}
		if mode == domain.ProxyFromEnvironment && dialer != nil {
			t.Fatal("the environment mode installed a dialer, which replaces Go's own reading of HTTPS_PROXY")
		}
	}
}

// TestProxyDialerNoneRefusesEvenAnEnvironmentProxy is the whole reason "none"
// exists as a mode separate from the default: an operator whose HTTPS_PROXY
// reaches the internet but not their private API server.
func TestProxyDialerNoneRefusesEvenAnEnvironmentProxy(t *testing.T) {
	t.Parallel()

	dialer, err := domain.ProxySettings{Mode: domain.ProxyNone}.Dialer()
	if err != nil {
		t.Fatalf("Dialer() error = %v", err)
	}
	if dialer == nil {
		t.Fatal("Dialer() = nil for none, which would fall back to the environment")
	}

	request := httptest.NewRequest(http.MethodGet, "https://10.22.0.5:6443/api", nil)
	proxy, err := dialer(request)
	if err != nil {
		t.Fatalf("dialer error = %v", err)
	}
	if proxy != nil {
		t.Fatalf("proxy = %v, want a direct connection", proxy)
	}
}

// TestProxyDialerManualSendsThroughTheConfiguredProxy covers the ordinary case.
func TestProxyDialerManualSendsThroughTheConfiguredProxy(t *testing.T) {
	t.Parallel()

	dialer, err := domain.ProxySettings{
		Mode: domain.ProxyManual,
		URL:  "http://proxy.corp:3128",
	}.Dialer()
	if err != nil {
		t.Fatalf("Dialer() error = %v", err)
	}

	for _, target := range []string{"https://api.example:6443/api", "http://api.example/api"} {
		proxy, err := dialer(httptest.NewRequest(http.MethodGet, target, nil))
		if err != nil {
			t.Fatalf("dialer(%q) error = %v", target, err)
		}
		if proxy == nil || proxy.Host != "proxy.corp:3128" {
			t.Fatalf("dialer(%q) = %v, want the configured proxy", target, proxy)
		}
	}
}

// TestProxyDialerHonoursNoProxyLikeTheEnvironmentVariableDoes is why the
// exception list is not parsed here.
//
// The syntax is the one operators already know from NO_PROXY — suffixes,
// CIDRs, a bare port — and it is handled by the same implementation net/http
// uses for the environment variable. A second dialect of a familiar syntax is
// a trap: it works for the cases somebody tests and fails for the one they
// relied on.
func TestProxyDialerHonoursNoProxyLikeTheEnvironmentVariableDoes(t *testing.T) {
	t.Parallel()

	dialer, err := domain.ProxySettings{
		Mode:    domain.ProxyManual,
		URL:     "http://proxy.corp:3128",
		NoProxy: "10.22.0.0/16,.internal",
	}.Dialer()
	if err != nil {
		t.Fatalf("Dialer() error = %v", err)
	}

	direct := []string{"https://10.22.0.5:6443/api", "https://api.cluster.internal/api"}
	for _, target := range direct {
		proxy, err := dialer(httptest.NewRequest(http.MethodGet, target, nil))
		if err != nil {
			t.Fatalf("dialer(%q) error = %v", target, err)
		}
		if proxy != nil {
			t.Errorf("dialer(%q) = %v, want a direct connection — it is in NoProxy", target, proxy)
		}
	}

	proxied, err := dialer(httptest.NewRequest(http.MethodGet, "https://api.example:6443/api", nil))
	if err != nil {
		t.Fatalf("dialer error = %v", err)
	}
	if proxied == nil {
		t.Fatal("a host outside NoProxy went direct")
	}
}

// TestProxyDialerRefusesWhatValidateRefuses keeps the two answers together: a
// setting that cannot be written must not be able to build a transport either.
func TestProxyDialerRefusesWhatValidateRefuses(t *testing.T) {
	t.Parallel()

	for _, settings := range []domain.ProxySettings{
		{Mode: domain.ProxyManual, URL: ""},
		{Mode: domain.ProxyManual, URL: "proxy.corp:3128"},
		{Mode: domain.ProxyManual, URL: "ftp://proxy.corp"},
		{Mode: domain.ProxyManual, URL: "http://user:pass@proxy.corp:3128"},
		{Mode: "sideways"},
	} {
		if _, err := settings.Dialer(); err == nil {
			t.Errorf("Dialer() accepted %+v", settings)
		}
	}
}
