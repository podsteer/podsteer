//go:build windows

package vendorcli

// refuseUnsafePath is a no-op on Windows.
//
// NOT AN OMISSION. Access there is decided by an ACL, and the permission bits
// Go reports for a file say nothing about who may write it — a cheap check
// would be a check that always passes while looking like one that means
// something, which is worse than none. Doing it properly means reading the
// DACL and resolving its entries, which is real work for a marginal gain over
// the refusals in safety.go that DO apply on every platform: PATH only, no
// working directory, no configurable path.
func refuseUnsafePath(string) error { return nil }
