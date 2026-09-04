package application_test

import (
	"testing"

	"github.com/podsteer/podsteer/app/application"
)

// mustCluster is defined in fakes_test.go, alongside this package's other
// hand-written test fixtures.

// TestRegistryReadOnlyDefaultsFalse asserts a cluster nothing has ever said
// anything about is not read-only. Absence is "not read-only" throughout this
// type, the same as it is throughout the frontend's own organisation store.
func TestRegistryReadOnlyDefaultsFalse(t *testing.T) {
	t.Parallel()

	r := application.NewRegistry()
	r.Open(mustCluster(t, "dev", true))

	if r.ReadOnly("dev") {
		t.Fatal("ReadOnly() = true for a cluster never marked")
	}
	// Never opened at all, not merely unmarked.
	if r.ReadOnly("never-seen") {
		t.Fatal("ReadOnly() = true for a cluster never connected")
	}
}

// TestRegistrySetReadOnlyLifecycle covers marking a cluster, then lifting the
// mark again — the toggle in OrganiseDialog going on and back off.
func TestRegistrySetReadOnlyLifecycle(t *testing.T) {
	t.Parallel()

	r := application.NewRegistry()
	r.Open(mustCluster(t, "prod", true))

	r.SetReadOnly("prod", true)
	if !r.ReadOnly("prod") {
		t.Fatal("ReadOnly() = false after SetReadOnly(true)")
	}

	r.SetReadOnly("prod", false)
	if r.ReadOnly("prod") {
		t.Fatal("ReadOnly() = true after SetReadOnly(false)")
	}
}

// TestRegistryCloseClearsReadOnly is the property CLAUDE.md's read-only
// section promises: a reconnect must not inherit a stale flag from before a
// disconnect, because the group setting that produced it may have changed in
// the meantime and the frontend re-asserts the current one on every Connect —
// but only for clusters that are open enough to receive it.
func TestRegistryCloseClearsReadOnly(t *testing.T) {
	t.Parallel()

	r := application.NewRegistry()
	r.Open(mustCluster(t, "prod", true))
	r.SetReadOnly("prod", true)

	if !r.Close("prod") {
		t.Fatal("Close() = false for an open cluster")
	}
	if r.ReadOnly("prod") {
		t.Fatal("ReadOnly() = true after Close()")
	}

	// A reconnect starts clean rather than reinheriting the pre-close mark.
	r.Open(mustCluster(t, "prod", true))
	if r.ReadOnly("prod") {
		t.Fatal("ReadOnly() = true immediately after reopening a previously read-only cluster")
	}
}

// TestRegistrySetReadOnlyIsIndependentOfConnection asserts the write side
// never consults connection state — Close is what enforces the lifecycle
// rule, not SetReadOnly refusing to act on an id it does not recognise. A
// call that raced a Disconnect must not panic or silently do nothing in a way
// that leaves the two callers disagreeing about what happened.
func TestRegistrySetReadOnlyIsIndependentOfConnection(t *testing.T) {
	t.Parallel()

	r := application.NewRegistry()

	// Never opened at all.
	r.SetReadOnly("ghost", true)
	if !r.ReadOnly("ghost") {
		t.Fatal("ReadOnly() = false right after SetReadOnly(true) on an unconnected id")
	}
}
