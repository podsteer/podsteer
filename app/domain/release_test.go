package domain_test

// Tests for the support-window verdict.
//
// Weighted heavily towards what happens with versions the table does NOT know,
// because that is where this feature can do harm. Telling somebody their
// supported cluster is out of support, on the strength of a table compiled by
// hand months earlier, would be worse than saying nothing at all.

import (
	"strings"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

func TestSupportForKnownVersions(t *testing.T) {
	t.Parallel()

	// 1.32 stops receiving patches on 28 February 2026.
	tests := []struct {
		name    string
		version string
		now     time.Time
		want    domain.SupportState
	}{
		{
			name:    "well inside the window",
			version: "v1.32.7",
			now:     time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC),
			want:    domain.SupportActive,
		},
		{
			name:    "inside the upgrade notice",
			version: "v1.32.7",
			now:     time.Date(2026, time.January, 20, 0, 0, 0, 0, time.UTC),
			want:    domain.SupportEnding,
		},
		{
			name:    "past end of life",
			version: "v1.32.7",
			now:     time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC),
			want:    domain.SupportEnded,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			support := domain.SupportFor(domain.ServerVersion{GitVersion: test.version}, test.now)
			if support.State != test.want {
				t.Errorf("state = %q, want %q", support.State, test.want)
			}
			if support.Minor != "1.32" {
				t.Errorf("minor = %q, want 1.32", support.Minor)
			}
		})
	}
}

// Managed distributions decorate the version string freely, and every one of
// these is a real shape seen in the wild.
func TestSupportForReadsDecoratedVersions(t *testing.T) {
	t.Parallel()

	tests := []struct{ version, minor string }{
		{version: "v1.32.7-eks-1234567", minor: "1.32"},
		{version: "v1.30.5-gke.1443001", minor: "1.30"},
		{version: "v1.29.4+rke2r1", minor: "1.29"},
		{version: "v1.31.2+k3s1", minor: "1.31"},
		{version: "1.28.9", minor: "1.28"},
	}

	now := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	for _, test := range tests {
		t.Run(test.version, func(t *testing.T) {
			t.Parallel()

			support := domain.SupportFor(domain.ServerVersion{GitVersion: test.version}, now)
			if support.Minor != test.minor {
				t.Errorf("minor = %q, want %q", support.Minor, test.minor)
			}
			if support.State == domain.SupportUnknown {
				t.Errorf("state is unknown for %q, which the table covers", test.version)
			}
		})
	}
}

// The table goes stale by construction. A release newer than it knows about
// must be reported as unknown, never as unsupported.
func TestSupportForSaysNothingAboutVersionsItDoesNotKnow(t *testing.T) {
	t.Parallel()

	tests := []struct{ name, version string }{
		{name: "newer than the table", version: "v1.99.0"},
		{name: "empty", version: ""},
		{name: "not a version", version: "unknown"},
		{name: "no minor component", version: "v2"},
	}

	now := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			support := domain.SupportFor(domain.ServerVersion{GitVersion: test.version}, now)
			if support.State != domain.SupportUnknown {
				t.Errorf("state = %q for %q, want unknown", support.State, test.version)
			}
			if !support.EndOfLife.IsZero() {
				t.Errorf("end of life = %v, want nothing claimed", support.EndOfLife)
			}
		})
	}
}

// Below the table is out of support, and inferring that is safe in the one
// direction that matters — unlike inferring it above the table, which would
// call a release made after this build unsupported.
func TestSupportForTreatsVersionsBelowTheTableAsEnded(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	support := domain.SupportFor(domain.ServerVersion{GitVersion: "v1.1.0"}, now)

	if support.State != domain.SupportEnded {
		t.Errorf("state = %q, want ended for a release older than the table", support.State)
	}
	// The date is genuinely unknown, and must not be invented.
	if !support.EndOfLife.IsZero() {
		t.Errorf("end of life = %v, want no date claimed", support.EndOfLife)
	}
}

// The table is generated, so its vintage is a fact the interface can state
// rather than something it has to imply.
func TestScheduleRecordsWhenItWasCompiled(t *testing.T) {
	t.Parallel()

	if domain.ScheduleCompiledAt().IsZero() {
		t.Error("the generated schedule carries no compilation date")
	}
}

// A cluster past end of life is worth a finding; one comfortably inside its
// window must not produce one.
func TestReleaseFindingOnlyAppearsWhenSupportIsRunningOut(t *testing.T) {
	t.Parallel()

	assess := func(now time.Time) domain.Overview {
		return domain.NewOverview(domain.OverviewInput{
			ClusterID: "dev",
			Version:   domain.ServerVersion{GitVersion: "v1.32.7"},
			Now:       now,
		})
	}

	inside := assess(time.Date(2025, time.June, 1, 0, 0, 0, 0, time.UTC))
	if _, ok := findingByTitle(inside.Findings, "Kubernetes version out of support"); ok {
		t.Error("a supported version produced an end-of-life finding")
	}
	if inside.Support.State != domain.SupportActive {
		t.Errorf("state = %q, want supported", inside.Support.State)
	}

	after := assess(time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC))
	finding, ok := findingByTitle(after.Findings, "Kubernetes version out of support")
	if !ok {
		t.Fatalf("findings = %v, want the end-of-life finding", titles(after.Findings))
	}
	if finding.Severity != domain.SeverityWarning {
		t.Errorf("severity = %q, want warning", finding.Severity)
	}
	// The date has to be in the text: "out of support" without it is an
	// assertion nobody can check.
	if want := "28 February 2026"; !strings.Contains(finding.Summary, want) {
		t.Errorf("summary = %q, want it to name %q", finding.Summary, want)
	}
}
