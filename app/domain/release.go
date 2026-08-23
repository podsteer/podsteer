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
// The dates come from the release team's own schedule, generated into
// release_schedule.go at build time by tools/releasegen rather than typed out
// here. They were maintained by hand first, and four of ten entries were
// wrong by up to a fortnight — a table that looks right, that nobody checks,
// and that is used to tell somebody their cluster is unsupported.
//
// Generated tables still go stale between PodSteer releases, so the rule
// throughout is to say nothing rather than something wrong: a version the
// table does not cover is unknown, never unsupported.

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
		// Older than every entry is a safe inference in the one direction
		// that matters: the table starts at the oldest release the project
		// still publishes dates for, so anything below it stopped receiving
		// patches before that. Newer than the table is not — it is a release
		// made after this build, and calling it unsupported would be exactly
		// backwards.
		if oldest, ok := oldestKnown(); ok && compareMinor(minor, oldest) < 0 {
			return ReleaseSupport{Minor: minor, State: SupportEnded}
		}
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

// CompiledAt returns when the support table was generated.
//
// Exposed so the interface can say how old the answer is instead of implying
// it is current: a build from a year ago knows nothing about releases made
// since, and "unknown" without that context reads as a bug.
func ScheduleCompiledAt() time.Time { return scheduleCompiledAt }

// oldestKnown returns the lowest minor version in the table.
func oldestKnown() (string, bool) {
	oldest := ""
	for minor := range endOfLife {
		if oldest == "" || compareMinor(minor, oldest) < 0 {
			oldest = minor
		}
	}
	return oldest, oldest != ""
}

// compareMinor orders two "major.minor" strings numerically, because "1.9"
// sorts after "1.10" as text and before it as a version.
func compareMinor(left, right string) int {
	leftMajor, leftMinor := numbers(left)
	rightMajor, rightMinor := numbers(right)
	switch {
	case leftMajor != rightMajor:
		return leftMajor - rightMajor
	default:
		return leftMinor - rightMinor
	}
}

func numbers(minor string) (int, int) {
	parts := strings.SplitN(minor, ".", 2)
	if len(parts) < 2 {
		return 0, 0
	}
	major, _ := strconv.Atoi(parts[0])
	small, _ := strconv.Atoi(parts[1])
	return major, small
}

// releaseFindings reports a control plane running out of support.
func releaseFindings(version ServerVersion, support ReleaseSupport) []Finding {
	switch support.State {
	case SupportEnded:
		when := fmt.Sprintf("on %s, %d days ago",
			support.EndOfLife.Format("2 January 2006"), -support.Days)
		if support.EndOfLife.IsZero() {
			// Older than the table goes back, so the date is not known — only
			// that it is long past.
			when = "before any release this build has dates for"
		}
		return []Finding{{
			ID:       "release:ended",
			Severity: SeverityWarning,
			Category: CategoryFindingConfiguration,
			Title:    "Kubernetes version out of support",
			Summary:  fmt.Sprintf("%s reached end of life %s", support.Minor, when),
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
