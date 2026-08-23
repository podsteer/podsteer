package domain

// How long a Kubernetes minor version is supported, and what that means for a
// cluster running it.
//
// Every Kubernetes minor gets patches for roughly fourteen months — twelve of
// standard support and two of maintenance — and then nothing. A control plane
// past that date receives no fix for a CVE disclosed tomorrow, which is a
// property of the cluster nothing in the Kubernetes API reports and no client
// surfaces, while the version string sits at the top of every dashboard.
//
// The dates below are a table compiled by hand from the published support
// windows. That has a cost worth being explicit about: it goes stale, and a
// version PodSteer has never heard of must not be guessed at. Both directions
// are handled by saying nothing rather than something wrong — an unknown
// version is unknown, not unsupported.

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SupportState is what the table can say about a version.
type SupportState string

const (
	// SupportUnknown means the version is not in the table: newer than it
	// knows about, or unparseable. Nothing is claimed.
	SupportUnknown SupportState = "unknown"
	// SupportActive means patches are still being published.
	SupportActive SupportState = "supported"
	// SupportEnding means end of life is close enough to plan an upgrade.
	SupportEnding SupportState = "ending"
	// SupportEnded means the version receives no further patches at all.
	SupportEnded SupportState = "ended"
)

// upgradeNotice is how far ahead of end of life a cluster is warned.
//
// A minor version upgrade on a managed cluster is a scheduled change with a
// maintenance window, not an afternoon's work, so the warning has to arrive
// early enough to be planned around.
const upgradeNotice = 60 * 24 * time.Hour

// endOfLife maps a Kubernetes minor version to the day its patches stop.
//
// Compiled from the published support windows. Entries are only added for
// releases whose dates are known; the absence of a newer version here means
// "not in the table", never "unsupported".
var endOfLife = map[string]time.Time{
	"1.26": date(2023, time.October, 28),
	"1.27": date(2024, time.June, 28),
	"1.28": date(2024, time.October, 28),
	"1.29": date(2025, time.February, 28),
	"1.30": date(2025, time.June, 28),
	"1.31": date(2025, time.October, 28),
	"1.32": date(2026, time.February, 28),
	"1.33": date(2026, time.June, 28),
	"1.34": date(2026, time.October, 28),
	"1.35": date(2027, time.February, 28),
}

func date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// ReleaseSupport is what is known about the version a cluster is running.
type ReleaseSupport struct {
	// Minor is the version the verdict applies to, e.g. "1.32".
	Minor string
	State SupportState
	// EndOfLife is when patches stop, zero when the state is unknown.
	EndOfLife time.Time
	// Days is how long until that date, negative once it has passed.
	Days int
}

// SupportFor reports what is known about a server version.
//
// Only the minor version matters: patch releases are published for the whole
// of a minor's window, so 1.32.0 and 1.32.7 stand or fall together.
func SupportFor(version ServerVersion, now time.Time) ReleaseSupport {
	minor, ok := minorOf(version.GitVersion)
	if !ok {
		return ReleaseSupport{State: SupportUnknown}
	}

	ends, known := endOfLife[minor]
	if !known {
		// Newer than the table, or a distribution that versions itself
		// differently. Saying nothing is the only honest answer: claiming a
		// fresh release is unsupported would be worse than silence.
		return ReleaseSupport{Minor: minor, State: SupportUnknown}
	}

	days := int(ends.Sub(now).Hours() / 24)
	support := ReleaseSupport{Minor: minor, EndOfLife: ends, Days: days}

	switch {
	case now.After(ends):
		support.State = SupportEnded
	case ends.Sub(now) <= upgradeNotice:
		support.State = SupportEnding
	default:
		support.State = SupportActive
	}
	return support
}

// minorOf extracts "1.32" from the many shapes a version string takes.
//
// Managed distributions decorate it freely — "v1.32.7-eks-1234567",
// "v1.30.5-gke.1443001", "v1.29.4+rke2r1" — so this reads the two leading
// numeric components and ignores everything after them rather than trying to
// parse the whole string.
func minorOf(gitVersion string) (string, bool) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(gitVersion), "v")
	if trimmed == "" {
		return "", false
	}

	parts := strings.SplitN(trimmed, ".", 3)
	if len(parts) < 2 {
		return "", false
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return "", false
	}
	// The minor component may carry a suffix on distributions that append one
	// before the patch number.
	minorDigits := strings.TrimFunc(parts[1], func(r rune) bool { return r < '0' || r > '9' })
	minor, err := strconv.Atoi(minorDigits)
	if err != nil {
		return "", false
	}

	return fmt.Sprintf("%d.%d", major, minor), true
}

// releaseFindings reports a control plane running out of support.
func releaseFindings(version ServerVersion, support ReleaseSupport) []Finding {
	switch support.State {
	case SupportEnded:
		return []Finding{{
			ID:       "release:ended",
			Severity: SeverityWarning,
			Category: CategoryFindingConfiguration,
			Title:    "Kubernetes version out of support",
			Summary: fmt.Sprintf("%s reached end of life on %s, %d days ago",
				support.Minor, support.EndOfLife.Format("2 January 2006"), -support.Days),
			Advice: "No further patches are published for this minor version, including for " +
				"vulnerabilities disclosed after that date. Managed providers usually keep " +
				"clusters running well past it and stop shipping fixes, so nothing will fail " +
				"to draw attention to this.",
			Subjects: []Subject{{Kind: "Cluster", Name: version.GitVersion, Detail: "control plane"}},
			Count:    1,
		}}

	case SupportEnding:
		return []Finding{{
			ID:       "release:ending",
			Severity: SeverityInfo,
			Category: CategoryFindingConfiguration,
			Title:    "Kubernetes version nearing end of life",
			Summary: fmt.Sprintf("%s stops receiving patches on %s, in %d days",
				support.Minor, support.EndOfLife.Format("2 January 2006"), support.Days),
			Advice: "Worth putting in a maintenance window now rather than discovering it " +
				"alongside a security advisory.",
			Subjects: []Subject{{Kind: "Cluster", Name: version.GitVersion, Detail: "control plane"}},
			Count:    1,
		}}

	default:
		return nil
	}
}
