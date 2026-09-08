//go:build windows

package localshell

import "testing"

// TestDefaultLoginShellPrefersTheOperatorsOwn asserts the one rule this
// resolver shares with every other platform: $SHELL is the operator's answer
// and beats anything PodSteer would pick.
func TestDefaultLoginShellPrefersTheOperatorsOwn(t *testing.T) {
	t.Setenv("SHELL", `C:\tools\nu.exe`)

	if got := defaultLoginShell(); got != `C:\tools\nu.exe` {
		t.Fatalf("defaultLoginShell() = %q, want the operator's own", got)
	}
}

// TestDefaultLoginShellFallsBackToComspec covers the last resort: neither
// PowerShell on PATH, so the operating system's own answer for cmd.exe.
func TestDefaultLoginShellFallsBackToComspec(t *testing.T) {
	t.Setenv("SHELL", "")
	t.Setenv("PATH", "")
	t.Setenv("COMSPEC", `C:\Windows\System32\cmd.exe`)

	if got := defaultLoginShell(); got != `C:\Windows\System32\cmd.exe` {
		t.Fatalf("defaultLoginShell() = %q, want COMSPEC", got)
	}
}

// TestLoginShellArgsAreEmptyOnWindows states the decision rather than leaving
// it to be rediscovered: `-l` is a POSIX shell's flag, and passing it to
// PowerShell would be passing it a file name to run.
func TestLoginShellArgsAreEmptyOnWindows(t *testing.T) {
	t.Parallel()

	if args := loginShellArgs(); len(args) != 0 {
		t.Fatalf("loginShellArgs() = %v, want none", args)
	}
}
