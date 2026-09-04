package cmd

import (
	"strings"
	"testing"
)

// The property this test exists for is not tidiness: `wails build` generates
// its TypeScript bindings by compiling and RUNNING this binary with no
// arguments, so an argument-free launch that did anything but start the
// window would take every build and every `make bindings` down with it.
func TestNoArgumentsStillMeansTheDesktopWindow(t *testing.T) {
	chosen, rest, err := route(nil)
	if err != nil {
		t.Fatalf("an argument-free launch failed: %v", err)
	}
	if chosen != commandWindow {
		t.Errorf("routed to %v, want the window", chosen)
	}
	if len(rest) != 0 {
		t.Errorf("carried %v", rest)
	}

	// The empty slice as well as nil: os.Args[1:] is one of those on a bare
	// launch depending on how the process was started.
	if chosen, _, err := route([]string{}); err != nil || chosen != commandWindow {
		t.Errorf("route([]) = %v, %v", chosen, err)
	}
}

func TestTheMCPSubcommandIsRoutedWithItsOwnArguments(t *testing.T) {
	chosen, rest, err := route([]string{"mcp", "--help"})
	if err != nil {
		t.Fatalf("routing mcp failed: %v", err)
	}
	if chosen != commandMCP {
		t.Errorf("routed to %v, want the mcp server", chosen)
	}
	if len(rest) != 1 || rest[0] != "--help" {
		t.Errorf("passed on %v, want the subcommand's own arguments", rest)
	}
}

// A mistyped subcommand answered by opening a window is not what anybody
// piping stdio at this binary is waiting for.
func TestAnUnknownCommandIsRefusedRatherThanTreatedAsALaunch(t *testing.T) {
	_, _, err := route([]string{"mpc"})
	if err == nil {
		t.Fatal("an unknown command was accepted")
	}
	if !strings.Contains(err.Error(), "mpc") {
		t.Errorf("the message must name what was typed: %q", err)
	}
}

// Help is not a failure, and it must not open a window either.
func TestMCPHelpPrintsAndReturns(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "help"} {
		if err := runMCP([]string{arg}); err != nil {
			t.Errorf("runMCP(%q) = %v", arg, err)
		}
	}
}

func TestMCPRefusesAnUnknownFlagRatherThanServingAnyway(t *testing.T) {
	err := runMCP([]string{"--port=8080"})
	if err == nil {
		t.Fatal("an unknown flag was ignored")
	}
	if !strings.Contains(err.Error(), "--port=8080") {
		t.Errorf("the message must name the argument: %q", err)
	}
}
