//go:build !windows

// Shutdown-race and escalation tests for the local shell.
//
// UNIX ONLY for the reason localshell_unix_test.go is: there is no
// pseudo-terminal on Windows in this build, so there is no process to start.

package localshell

import (
	"errors"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

// TestStartLocalShellRefusesAfterShutdown is the shutdown race, the local
// twin of the node-shell one.
//
// StartLocalShell registers its session AFTER starting the process, so a
// StopAllLocalShells landing between the two copies a map that does not hold
// it and the session is left in a registry nothing reads again. Belt and
// braces rather than a live leak — the kernel hangs the child up when the
// master descriptor closes at process exit — but the registry's rule is that
// the record and the process are created and destroyed together, and a
// session nothing can ever stop is not that.
func TestStartLocalShellRefusesAfterShutdown(t *testing.T) {
	manager := testManager(t, nil)

	manager.StopAllLocalShells()

	_, err := manager.StartLocalShell(
		domain.LocalShellSpec{Context: "staging", Cols: 80, Rows: 24}, nil, nil)
	if !errors.Is(err, errShellsClosed) {
		t.Fatalf("StartLocalShell() after StopAllLocalShells error = %v, want errShellsClosed", err)
	}

	if shells := manager.ListLocalShells(); len(shells) != 0 {
		t.Fatalf("ListLocalShells() = %d, want 0 — nothing may be registered into a closed registry", len(shells))
	}
}

// TestStopAllLocalShellsIsIdempotentOnceClosed guards the other half of that
// flag: closing twice is what a pane teardown landing beside a shutdown does,
// and it must not be an error or a panic.
func TestStopAllLocalShellsIsIdempotentOnceClosed(t *testing.T) {
	manager := testManager(t, nil)

	manager.StopAllLocalShells()
	manager.StopAllLocalShells()

	if shells := manager.ListLocalShells(); len(shells) != 0 {
		t.Fatalf("ListLocalShells() = %d, want 0", len(shells))
	}
}

// TestShouldKillRefusesToSignalAReapedSession is the decision the escalation
// path makes immediately before signalling a process GROUP by raw id.
//
// The window it guards is between the hangup grace elapsing and the signal:
// the pump can reap the child in it, and a reaped pid — which for a session
// leader is also the group id — is one the kernel may reassign, so the signal
// would land on whatever holds it now. `os.Process.Signal` refuses to signal
// a process it knows is done; `syscall.Kill(-pid, …)` cannot know.
//
// The decision is asserted here rather than the race, deliberately: after the
// re-check the remaining window is a few instructions wide and unobservable
// without putting an interface in front of the process purely to watch it.
// What is testable, and what actually changed, is that a session already
// reaped is not signalled at all.
func TestShouldKillRefusesToSignalAReapedSession(t *testing.T) {
	reaped := make(chan struct{})
	close(reaped)
	if shouldKill(reaped) {
		t.Error("shouldKill(reaped) = true, want false — the pid may already belong to something else")
	}

	running := make(chan struct{})
	if !shouldKill(running) {
		t.Error("shouldKill(running) = false, want true — a shell that ignored the hangup must still be killed")
	}
}
