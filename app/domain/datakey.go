package domain

import "regexp"

// dataKeyPattern matches the characters Kubernetes accepts in a Secret or
// ConfigMap data key — the same rule `kubectl` enforces client-side and the
// API server enforces again on the way in. Checking it here turns a
// malformed key into a local, immediate ErrInvalidKey instead of a round trip
// that fails on the far side with a 422 an operator has to decode.
var dataKeyPattern = regexp.MustCompile(`^[-._a-zA-Z0-9]+$`)

// ValidDataKey reports whether key is an acceptable Secret or ConfigMap data
// key: non-empty, and built only from the characters the API server permits
// in `data`, `stringData` or `binaryData`.
//
// Shared by ManagementService and the k8s adapter's write paths so the two
// layers cannot drift into checking different things — the same shape of
// duplication SuspendWorkload already has between its kind whitelist in
// application and the defence-in-depth switch in the adapter.
func ValidDataKey(key string) bool {
	return dataKeyPattern.MatchString(key)
}
