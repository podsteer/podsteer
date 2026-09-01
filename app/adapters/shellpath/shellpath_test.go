package shellpath

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestLooksInheritedRecognisesTheSystemDefault(t *testing.T) {
	// What launchd hands a .app when nothing set a PATH.
	if !looksInherited("/usr/bin:/bin:/usr/sbin:/sbin") {
		t.Fatal("the launchd default should look inherited")
	}
	if !looksInherited("") {
		t.Fatal("an empty PATH should look inherited")
	}
}

func TestLooksInheritedLeavesATerminalsPathAlone(t *testing.T) {
	// THE IMPORTANT HALF. An operator who launched from a terminal, or who
	// set a PATH deliberately, must keep exactly what they set — otherwise
	// the application's behaviour depends on a login shell they were not
	// using, and `make run` stops reproducing what the installed build does.
	if looksInherited("/opt/homebrew/bin:/usr/bin:/bin") {
		t.Fatal("a PATH carrying Homebrew came from a shell and must be kept")
	}
}

func TestBetweenSentinelsIgnoresShellChatter(t *testing.T) {
	// An interactive shell prints whatever the operator's configuration
	// prints. Taking the last line would work until somebody's prompt hook
	// wrote to stdout.
	output := "nvm: version 20 in use\n" + sentinel + "/opt/homebrew/bin:/usr/bin" + sentinel + "\nbye\n"

	value, err := betweenSentinels(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value != "/opt/homebrew/bin:/usr/bin" {
		t.Fatalf("extracted %q", value)
	}
}

func TestBetweenSentinelsRejectsOutputWithNoValue(t *testing.T) {
	if _, err := betweenSentinels("just some noise\n"); err == nil {
		t.Fatal("expected an error when the shell printed no PATH")
	}
	if _, err := betweenSentinels("noise" + sentinel + "half"); err == nil {
		t.Fatal("expected an error when the output was truncated")
	}
}

func TestMissingFromNamesOnlyTheGain(t *testing.T) {
	added := missingFrom("/usr/bin:/bin", "/opt/homebrew/bin:/usr/bin:/bin:/opt/homebrew/bin")

	if len(added) != 1 || added[0] != "/opt/homebrew/bin" {
		t.Fatalf("added %v, want [/opt/homebrew/bin] once", added)
	}
}

func TestResolveKeepsAPathThatCameFromAShell(t *testing.T) {
	t.Setenv("PATH", "/opt/homebrew/bin:/usr/bin:/bin")

	result := Resolve(context.Background())

	if !strings.Contains(result, "already inherited") {
		t.Fatalf("result %q; the shell should not have been asked", result)
	}
	if os.Getenv("PATH") != "/opt/homebrew/bin:/usr/bin:/bin" {
		t.Fatalf("PATH was rewritten to %q", os.Getenv("PATH"))
	}
}

func TestResolveSurvivesAShellThatCannotRun(t *testing.T) {
	// Failing to enrich PATH must never stop the window from opening.
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("SHELL", "/nonexistent/shell")

	result := Resolve(context.Background())

	if !strings.Contains(result, "kept the inherited PATH") {
		t.Fatalf("result %q", result)
	}
	if os.Getenv("PATH") != "/usr/bin:/bin" {
		t.Fatalf("PATH became %q", os.Getenv("PATH"))
	}
}

func TestResolveRecoversTheShellsPath(t *testing.T) {
	// The end-to-end path, against the real login shell. Skipped rather than
	// failed where there is none, because CI containers often have no $SHELL.
	shell := os.Getenv("SHELL")
	if shell == "" {
		t.Skip("no $SHELL on this machine")
	}
	t.Setenv("SHELL", shell)
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")

	result := Resolve(context.Background())
	t.Logf("%s", result)

	if strings.Contains(result, "kept the inherited PATH") {
		t.Skipf("the shell could not be asked here: %s", result)
	}
	if os.Getenv("PATH") == "/usr/bin:/bin:/usr/sbin:/sbin" {
		t.Fatal("PATH was not enriched despite a successful probe")
	}
}
