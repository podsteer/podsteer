//go:build windows

package localshell

import (
	"os"
	"os/exec"
)

// defaultLoginShell names the shell to run on Windows.
//
// $SHELL FIRST, as everywhere else — somebody running PodSteer from a shell
// that set it means it — and then the two shells a Windows machine actually
// has. PowerShell 7 is `pwsh`, Windows PowerShell is `powershell`, and both
// are resolved from PATH by the process start rather than by an absolute
// path: they live in different places on different installs, and a hard-coded
// System32 path is wrong the moment somebody has 7 installed.
//
// COMSPEC is the last resort and is what the operating system itself says
// `cmd.exe` is. Reaching it means neither PowerShell was on PATH, which is
// unusual enough to be worth taking the system's word for rather than
// guessing at a path.
func defaultLoginShell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	for _, candidate := range []string{"pwsh.exe", "powershell.exe"} {
		if _, err := exec.LookPath(candidate); err == nil {
			return candidate
		}
	}
	if comspec := os.Getenv("COMSPEC"); comspec != "" {
		return comspec
	}
	return "cmd.exe"
}

// loginShellArgs is EMPTY ON WINDOWS, and that is not an omission.
//
// `-l` is a POSIX shell's flag for "be a login shell" and neither PowerShell
// nor cmd has one: PowerShell reads the operator's profile whenever it is
// interactive, which on a console it always is, and cmd has no profile to
// read. Passing `-l` would be passing PowerShell a file name to run.
func loginShellArgs() []string { return nil }
