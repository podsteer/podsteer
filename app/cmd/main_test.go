package cmd

import (
	"strings"
	"testing"
)

// The reason this test exists CHANGED with Wails v3, and the test did not.
//
// Under v2 the rule was mechanical: binding generation compiled and RAN this
// binary with no arguments, so an argument-free launch that did anything but
// start the window took every build down with it. `wails3 generate bindings`
// reads the Go source instead — nothing is executed — so that particular
// consequence is gone.
//
// What remains is the product rule, which is the one that was always the
// point: double-clicking PodSteer, or launching it from the Dock, passes no
// arguments, and that MUST open the window. A flag, a prompt or a usage
// message in front of a bare launch would make the application unstartable
// the way anybody actually starts it, and nothing else in the code says so.
// `route` is split out of `dispatch` so this can be asserted without starting
// a window, which is the one thing a test of this path cannot do.
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
