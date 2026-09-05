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
