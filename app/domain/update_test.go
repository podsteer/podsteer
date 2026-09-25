package domain_test

import (
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

func TestParseAppVersionRefusesAnythingButAProductionTag(t *testing.T) {
	// A pre-release is not what the Homebrew tap serves, and reading it as its
	// numeric prefix would offer somebody an upgrade to a build they cannot
	// install.
	for _, raw := range []string{"dev", "", "v1.2", "v1.2.3.4", "1.2.3-rc-1", "v1.2.3-rc-1", "vX.Y.Z", "v1.2.x"} {
		if _, ok := domain.ParseAppVersion(raw); ok {
			t.Errorf("%q parsed, want refused", raw)
		}
	}
}

func TestParseAppVersionAcceptsRealTags(t *testing.T) {
	for _, raw := range []string{"v0.1.1", "0.1.1", "v10.20.30"} {
		if _, ok := domain.ParseAppVersion(raw); !ok {
			t.Errorf("%q refused, want parsed", raw)
		}
	}
}

func TestNewerThanOrdersByEachComponent(t *testing.T) {
	cases := []struct {
		a, b  string
		newer bool
	}{
		{"v0.2.0", "v0.1.9", true},
		{"v1.0.0", "v0.99.99", true},
		{"v0.1.2", "v0.1.1", true},
		{"v0.1.1", "v0.1.1", false},
		{"v0.1.1", "v0.1.2", false},
		// The one a naive string compare gets wrong.
		{"v0.10.0", "v0.9.0", true},
	}

	for _, c := range cases {
		a, _ := domain.ParseAppVersion(c.a)
		b, _ := domain.ParseAppVersion(c.b)

		if got := a.NewerThan(b); got != c.newer {
			t.Errorf("%s newer than %s = %v, want %v", c.a, c.b, got, c.newer)
		}
	}
}

func TestCompareVersionsSaysNothingAboutADevelopmentBuild(t *testing.T) {
	result := domain.CompareVersions("dev", "v9.9.9")

	// NOT Unknown, and the distinction is the whole point of this test. It
	// used to be, and the settings pane renders Unknown as "Could not reach
	// GitHub" — so every development build reported a network failure that
	// had not happened, on a machine where the request returned 200. The
	// check succeeded; there is simply nothing to compare it against.
	if result.State != domain.UpdateNotComparable {
		t.Fatalf("state %q, want not-comparable", result.State)
	}
	// And it must not leak the latest version into a build that cannot be
	// compared — the UI would have something to show and no basis for it.
	if result.Latest != "" {
		t.Fatalf("latest %q, want empty", result.Latest)
	}
}

// TestCompareVersionsKeepsAFailedCheckApartFromAnUncomparableBuild is the
// regression this pair exists for: two outcomes that need opposite sentences.
// One says look at your network, the other says there is nothing to look at.
func TestCompareVersionsKeepsAFailedCheckApartFromAnUncomparableBuild(t *testing.T) {
	cases := []struct {
		name      string
		installed string
		latest    string
		want      domain.UpdateState
	}{
		{"a development build, with a good answer from GitHub", "dev", "v1.2.3", domain.UpdateNotComparable},
		{"a build with no version at all", "", "v1.2.3", domain.UpdateNotComparable},
		{"a real build, with an answer that will not parse", "v1.0.0", "latest", domain.UpdateUnknown},
		{"a real build, with no answer at all", "v1.0.0", "", domain.UpdateUnknown},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := domain.CompareVersions(tc.installed, tc.latest).State; got != tc.want {
				t.Fatalf("state %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCompareVersionsDoesNotOfferADowngrade(t *testing.T) {
	// Somebody running a build newer than the latest release — a maintainer on
	// a release candidate — must not be told to install an older one.
	if result := domain.CompareVersions("v0.9.0", "v0.1.1"); result.State != domain.UpdateCurrent {
		t.Fatalf("state %q, want current", result.State)
	}
}

func TestCompareVersionsIsUnknownWhenTheAnswerIsUnusable(t *testing.T) {
	if result := domain.CompareVersions("v0.1.1", "latest"); result.State != domain.UpdateUnknown {
		t.Fatalf("state %q, want unknown", result.State)
	}
}
