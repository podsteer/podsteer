package domain

import (
	"strconv"
	"strings"
)

// AppVersion is a release of PodSteer itself, as a comparable value.
//
// Distinct from ServerVersion, which is a Kubernetes API server's — the two
// are never compared and giving them one type would invite exactly that.
type AppVersion struct {
	Major, Minor, Patch int
	// Raw is what was parsed, kept so a message can quote what somebody has
	// rather than a re-rendered approximation of it.
	Raw string
}

// ParseAppVersion reads a tag like "v0.1.1".
//
// A DEVELOPMENT BUILD IS NOT A VERSION. `config.Version()` returns "dev" for
// anything built from a working tree, and that must not parse: comparing a
// tagged release against it would either claim an update is available forever
// or claim the working tree is current, and both are lies to somebody in the
// middle of changing the code.
func ParseAppVersion(raw string) (AppVersion, bool) {
	trimmed := strings.TrimSpace(raw)
	digits := strings.Split(strings.TrimPrefix(trimmed, "v"), ".")
	if len(digits) != 3 {
		return AppVersion{}, false
	}

	parsed := AppVersion{Raw: trimmed}
	for index, part := range digits {
		// Anything suffixed — "1-rc-2", "1+build" — is refused rather than
		// truncated. A pre-release is not the production tag the tap serves,
		// and quietly reading it as its numeric prefix would offer somebody
		// an upgrade to a build they cannot install.
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return AppVersion{}, false
		}

		switch index {
		case 0:
			parsed.Major = value
		case 1:
			parsed.Minor = value
		case 2:
			parsed.Patch = value
		}
	}
	return parsed, true
}

// NewerThan reports whether v is a later release than other.
func (v AppVersion) NewerThan(other AppVersion) bool {
	if v.Major != other.Major {
		return v.Major > other.Major
	}
	if v.Minor != other.Minor {
		return v.Minor > other.Minor
	}
	return v.Patch > other.Patch
}

// UpdateState is what an update check concluded.
type UpdateState string

const (
	// UpdateCurrent means this build is the newest published release.
	UpdateCurrent UpdateState = "current"
	// UpdateAvailable means a newer release exists.
	UpdateAvailable UpdateState = "available"
	// UpdateDisabled means no check was made, because nobody asked for one.
	// A STATE RATHER THAN AN ERROR: it is the configured outcome.
	UpdateDisabled UpdateState = "disabled"
	// UpdateUnknown means the check could not be completed — offline, rate
	// limited, behind a proxy that refused it. Also not an error: a client
	// that cannot reach GitHub is working perfectly well.
	UpdateUnknown UpdateState = "unknown"
)

// UpdateCheck is the result of asking whether a newer release exists.
type UpdateCheck struct {
	State UpdateState
	// Installed is the running build, empty for a development build.
	Installed string
	// Latest is the newest published release, empty unless one was read.
	Latest string
	// URL is where to get it — the release page, never a direct download.
	// PodSteer does not update itself and will not pretend to: the operator
	// installs it the same way they installed it the first time.
	URL string
}

// CompareVersions decides what to tell somebody about the release they have.
//
// A PURE FUNCTION, so the rule that matters — never claim an update when the
// installed build cannot be identified — is settled in a test rather than
// observed in the field.
func CompareVersions(installed, latest string) UpdateCheck {
	result := UpdateCheck{State: UpdateUnknown, Installed: installed, Latest: latest}

	current, ok := ParseAppVersion(installed)
	if !ok {
		// A development build. Saying nothing is the only honest answer.
		return UpdateCheck{State: UpdateUnknown, Installed: installed}
	}

	published, ok := ParseAppVersion(latest)
	if !ok {
		return result
	}

	if published.NewerThan(current) {
		result.State = UpdateAvailable
		return result
	}
	result.State = UpdateCurrent
	return result
}
