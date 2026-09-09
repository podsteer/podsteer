//go:build windows

package localshell

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// THE CHECK THAT USED TO BE A SENTENCE IN A COMMIT MESSAGE.
//
// The rest of this package's Windows tests are about argument quoting, which
// is checkable anywhere because it is pure string handling. The console
// itself is not: allocating one calls into kernel32 and needs a Windows
// machine actually running the test. That was true when the ConPTY
// implementation landed, so the only evidence it worked was somebody opening
// a shell once on a Windows build — a verification nobody can repeat on a
// pull request, and one that says nothing about the release after it.
//
// CI already runs a windows-latest job to package the application, and the
// workflow now runs this package's tests on it. What follows is therefore the
// smoke test that was previously done by hand: a real pseudo-console, a real
// child process, and its bytes coming back.

// probeMarker is what the child is asked to print.
//
// PASSED THROUGH THE ENVIRONMENT AND EXPANDED BY THE CHILD, deliberately.
// Putting the marker on the command line would mean the command line itself
// contains it — and ConPTY renders what it is given, so a test that merely
// looked for the string could pass with the child never having run at all.
// `%PODSTEER_PROBE%` is expanded by cmd.exe, so the marker can only appear in
// the output if a process on the far end expanded it.
const (
	probeMarker = "conpty-round-trip-9f3c"
	probeVar    = "PODSTEER_PROBE"
)

func TestAConsoleStartsAChildAndItsOutputComesBack(t *testing.T) {
	pty := startProbe(t)

	waitFor(t, pty, probeMarker)
}

// Resizing is the other call that cannot be checked without a console: it is
// a kernel32 entry point taking a handle this package allocated, and getting
// it wrong is a use-after-free rather than an error return.
func TestAConsoleCanBeResizedWhileItRuns(t *testing.T) {
	pty := startProbe(t)
	waitFor(t, pty, probeMarker)

	if err := pty.Resize(120, 40); err != nil {
		t.Fatalf("Resize() on a live console = %v, want no error", err)
	}
}

// And after Close it must be a no-op rather than a call into a freed console
// — the ordinary way to reach it is a window dragged as a session ends.
func TestResizingAClosedConsoleIsNotAnError(t *testing.T) {
	pty := startProbe(t)
	waitFor(t, pty, probeMarker)

	pty.Close()
	pty.Close() // idempotent: the pump closes it, then Manager closes it again

	if err := pty.Resize(100, 30); err != nil {
		t.Fatalf("Resize() after Close() = %v, want no error", err)
	}
}

// startProbe opens a console running `cmd.exe /c echo %PODSTEER_PROBE%`.
func startProbe(t *testing.T) *ptyProcess {
	t.Helper()

	if ok, notice := supported(); !ok {
		// A SKIP EVERYWHERE IS HOW THIS ENDS UP UNTESTED AGAIN. On a
		// developer's older Windows an absent ConPTY is a handled condition
		// — it is why supported() exists — but on the runner that packages
		// the release it means this check silently stopped happening.
		if os.Getenv("CI") != "" {
			t.Fatalf("ConPTY unavailable on a CI runner: %s", notice)
		}
		t.Skipf("ConPTY unavailable on this Windows, so the console round trip was not exercised: %s", notice)
	}

	command := exec.Command("cmd.exe", "/c", "echo %"+probeVar+"%")
	command.Env = append(os.Environ(), probeVar+"="+probeMarker)

	pty, err := startPTY(command, 80, 24)
	if err != nil {
		t.Fatalf("startPTY() = %v, want a console", err)
	}
	t.Cleanup(pty.Close)
	return pty
}

// waitFor reads until the console has produced want, or the test gives up.
//
// It does NOT read to EOF. The pseudo-console's output handle stays open
// after the child exits — it belongs to the console, not the process — so a
// read to EOF would park until something closed it, which is this test's own
// cleanup.
func waitFor(t *testing.T, pty *ptyProcess, want string) {
	t.Helper()

	var (
		mu    sync.Mutex
		seen  strings.Builder
		found = make(chan struct{})
	)

	go func() {
		buffer := make([]byte, 4096)
		for {
			n, err := pty.Read(buffer)
			if n > 0 {
				mu.Lock()
				seen.Write(buffer[:n])
				hit := strings.Contains(seen.String(), want)
				mu.Unlock()
				if hit {
					close(found)
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	select {
	case <-found:
	case <-time.After(30 * time.Second):
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("the console never produced %q in 30s; it said %q", want, seen.String())
	}
}
