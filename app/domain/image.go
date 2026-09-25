package domain

import (
	"regexp"
	"strings"
)

// imageComponentPattern matches one path segment of an image name — the same
// alphabet Docker's own distribution-reference grammar allows in a
// component: lowercase letters, digits, and separators (., _ or a run of -)
// between runs of alphanumerics. "team", "my-app" and "my.app_v2" all match;
// an uppercase letter, a space or an empty segment does not.
var imageComponentPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-]+[a-z0-9]+)*$`)

// imageHostPattern matches a registry host, with an optional port —
// "docker.io", "localhost:5000", "123.45.67.89:5000". Kept separate from
// imageComponentPattern because a host is allowed dots and a port in a way a
// path segment is not, which is also what makes it possible to tell the two
// apart (see ValidImageReference).
var imageHostPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*(?::[0-9]+)?$`)

// imageTagPattern matches a tag: up to 128 characters of alphanumerics,
// underscore, period or hyphen, and must not start with a period or hyphen —
// the same rule `docker tag` enforces.
var imageTagPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

// imageDigestPattern matches an OCI content digest: a lowercase algorithm
// name followed by a colon and a lowercase hex-encoded hash. Bounded to
// 32-128 hex characters, which covers sha256 (64) and sha512 (128) — the
// only algorithms anything in this project produces or reads — without
// hard-coding either by name.
var imageDigestPattern = regexp.MustCompile(`^[a-z0-9]+(?:[.+_-][a-z0-9]+)*:[a-f0-9]{32,128}$`)

// ValidImageReference reports whether ref could plausibly name a container
// image: a bare name ("nginx"), a name with a registry and/or path
// ("registry.example.com:5000/team/app"), a tag ("app:v1.2.3"), a digest
// ("app@sha256:...", 64 hex characters for sha256), or any combination —
// "registry.example.com/team/app:v1.2.3@sha256:...".
//
// This is deliberately NOT a full implementation of Docker's distribution
// reference grammar. Nobody here needs to reject every string that grammar
// would; the point is to turn an operator's typo — a stray space, an empty
// tag left behind by a half-finished edit, a pasted URL — into a local,
// immediate refusal instead of a round trip that fails on the far side with a
// 422 naming a field the operator cannot see. See ValidDataKey for the same
// trade made for Secret and ConfigMap keys.
func ValidImageReference(ref string) bool {
	if ref == "" || strings.ContainsAny(ref, " \t\n\r") {
		return false
	}

	rest := ref

	// A digest, if present, is the last "@" — an image reference can carry
	// both a tag and a digest ("app:v1@sha256:..."), but only one digest.
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		if !imageDigestPattern.MatchString(rest[at+1:]) {
			return false
		}
		rest = rest[:at]
		if rest == "" {
			return false
		}
	}

	// A colon after the last slash separates a tag; one at or before it is a
	// registry port ("myregistry.io:5000/team/app" has no tag).
	if colon := strings.LastIndex(rest, ":"); colon >= 0 && colon > strings.LastIndex(rest, "/") {
		if !imageTagPattern.MatchString(rest[colon+1:]) {
			return false
		}
		rest = rest[:colon]
		if rest == "" {
			return false
		}
	}

	segments := strings.Split(rest, "/")

	// A first segment containing a '.' or a ':', or literally "localhost", is
	// a registry host rather than a path component — the same heuristic
	// Docker's own reference parser uses to tell "docker.io/library/nginx" (a
	// registry) from "library/nginx" (two path components on the default
	// registry).
	first := segments[0]
	if len(segments) > 1 && (strings.ContainsAny(first, ".:") || first == "localhost") {
		if !imageHostPattern.MatchString(first) {
			return false
		}
		segments = segments[1:]
	}

	if len(segments) == 0 {
		return false
	}
	for _, component := range segments {
		if !imageComponentPattern.MatchString(component) {
			return false
		}
	}
	return true
}
