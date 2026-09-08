//go:build windows

package localshell

import (
	"os/exec"
	"testing"
)

// The console itself cannot be tested here — allocating one needs a Windows
// machine running the test, and CI packages this platform rather than running
// it. What IS testable is the part that gets Windows wrong most often, and
// which no amount of manual smoke-testing on one machine would catch: turning
// an argv into the single command line Windows hands a process.

func TestCommandLineQuotesAPathWithSpaces(t *testing.T) {
	t.Parallel()

	// THE ORDINARY CASE ON WINDOWS, not an edge one: `C:\Program Files\…`.
	// Unquoted, the C runtime on the far end reads it as two arguments and
	// the shell never starts.
	cmd := exec.Command(`C:\Program Files\PowerShell\7\pwsh.exe`)

	if got, want := commandLine(cmd), `"C:\Program Files\PowerShell\7\pwsh.exe"`; got != want {
		t.Fatalf("commandLine() = %s, want %s", got, want)
	}
}

func TestCommandLineKeepsEveryArgumentSeparate(t *testing.T) {
	t.Parallel()

	cmd := exec.Command(`C:\Windows\System32\cmd.exe`, "/c", "echo hello world")

	got := commandLine(cmd)
	want := `C:\Windows\System32\cmd.exe /c "echo hello world"`
	if got != want {
		t.Fatalf("commandLine() = %s, want %s", got, want)
	}
}

func TestCommandLineSurvivesAnArgumentWithAQuote(t *testing.T) {
	t.Parallel()

	// An agent prompt is operator text and can contain anything. The escaping
	// is the standard library's own, so this asserts that it is USED rather
	// than re-deriving the rule.
	cmd := exec.Command("pwsh.exe", `say "hi"`)

	got := commandLine(cmd)
	if got == `pwsh.exe say "hi"` {
		t.Fatalf("commandLine() = %s, which the far end would re-split", got)
	}
	if got[:len("pwsh.exe ")] != "pwsh.exe " {
		t.Fatalf("commandLine() = %s, want the program first", got)
	}
}

func TestCommandLineWithNoArgsIsJustTheProgram(t *testing.T) {
	t.Parallel()

	cmd := &exec.Cmd{Path: `C:\Windows\System32\cmd.exe`}
	if got, want := commandLine(cmd), `C:\Windows\System32\cmd.exe`; got != want {
		t.Fatalf("commandLine() = %s, want %s", got, want)
	}
}
