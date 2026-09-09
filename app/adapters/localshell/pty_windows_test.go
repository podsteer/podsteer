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

	// An agent prompt is operator text and can contain anything, and an
	// unescaped quote in it is re-split by whatever parses the far end's
	// command line.
	//
	// THE Cmd IS CONSTRUCTED RATHER THAN LOOKED UP. exec.Command resolves its
	// program through PATH, so on a real Windows machine "pwsh.exe" becomes
	// `C:\Program Files\PowerShell\7\pwsh.exe` — quoted, because it has a
	// space in it — and this test was asserting on whichever PowerShell that
	// machine happened to have installed. It never noticed, because nothing
	// ran it until CI did.
	const program = `C:\tools\pwsh.exe`
	cmd := &exec.Cmd{Path: program, Args: []string{program, `say "hi"`}}

	// The literal that syscall.EscapeArg produces, written out rather than
	// computed: the point of the test is that the command line the far end
	// receives is the C runtime's own escaping, and re-deriving it here with
	// the same call the code makes would assert nothing.
	if got, want := commandLine(cmd), program+` "say \"hi\""`; got != want {
		t.Fatalf("commandLine() = %s, want %s", got, want)
	}
}

func TestCommandLineWithNoArgsIsJustTheProgram(t *testing.T) {
	t.Parallel()

	cmd := &exec.Cmd{Path: `C:\Windows\System32\cmd.exe`}
	if got, want := commandLine(cmd), `C:\Windows\System32\cmd.exe`; got != want {
		t.Fatalf("commandLine() = %s, want %s", got, want)
	}
}
