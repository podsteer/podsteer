package cmd

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
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

// SECURITY.md says of `podsteer mcp` that "nothing is written anywhere". This
// asserts the composition that has to keep it true.
//
// The settings store is the one thing in that subcommand's wiring that owns a
// file, and it is opened read-only there. Read-only is not only a refusal to
// save: it also means no directory is created, no adoption is performed and
// the pre-0.3 `history.json` is left where it is — so a whole MCP run leaves
// the configuration directory byte-identical to how it found it.
//
// A DIRECTORY SNAPSHOT rather than an assertion about one file, because the
// promise is about the directory: a temporary file left behind by a failed
// write, a `.invalid-` sideline, or a freshly created `PodSteer/` would each
// break it while every individual file check still passed.
func TestTheMCPCompositionLeavesTheConfigurationDirectoryByteIdentical(t *testing.T) {
	dir := t.TempDir()
	// The store resolves its own path from the user config directory, so the
	// test points that at a temporary one. Not parallel: it sets an
	// environment variable os.UserConfigDir reads.
	pointConfigDirAt(t, dir)

	// Seed the directory with what a machine upgrading from v0.2 has: the
	// `history.json` adoption would consume if this were the window.
	legacy := seedPreviousSettings(t, `{"retentionDays":7,"intervalSeconds":60}`)

	before := snapshotDir(t, dir)

	// EXACTLY WHAT runMCP DOES with the settings, and through the same
	// function, so a change there is a change here.
	store := openSettings(true, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if store == nil {
		t.Fatal("openSettings returned no store")
	}

	// Everything the subcommand asks of it: the kubeconfig sources callback
	// is invoked on every client build and on every cluster listing.
	sources := kubeconfigSources(store)
	if sources == nil {
		t.Fatal("no kubeconfig source callback was produced")
	}
	_ = sources()

	// And the write that must be refused rather than performed.
	if _, err := store.Update(context.Background(), func(*domain.Settings) error {
		return nil
	}); !errors.Is(err, ports.ErrSettingsReadOnly) {
		t.Errorf("Update() error = %v, want ErrSettingsReadOnly", err)
	}

	after := snapshotDir(t, dir)
	if !maps.Equal(before, after) {
		t.Errorf("the MCP composition changed the configuration directory:\nbefore %v\nafter  %v",
			before, after)
	}

	// Named explicitly as well, because this is the specific way adoption
	// could break the promise: it removes the file it read.
	if _, err := os.Stat(legacy); err != nil {
		t.Errorf("the previous settings file was removed by a read-only run: %v", err)
	}
}

// The window's composition, by contrast, DOES adopt — otherwise an operator
// who turned recording off in v0.2 would find it back on after upgrading.
func TestTheWindowCompositionAdoptsThePreviousSettingsFile(t *testing.T) {
	dir := t.TempDir()
	pointConfigDirAt(t, dir)

	legacy := seedPreviousSettings(t, `{"retentionDays":0,"intervalSeconds":60}`)

	store := openSettings(false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if store == nil {
		t.Fatal("openSettings returned no store")
	}

	settings, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settings.History.Retention.Days != 0 {
		t.Errorf("retention = %d days, want the 0 the previous file carried", settings.History.Retention.Days)
	}
	if _, err := os.Stat(legacy); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the previous file was not removed after adoption: %v", err)
	}
}

// pointConfigDirAt makes os.UserConfigDir answer with dir.
//
// The variable differs per platform, which is exactly what os.UserConfigDir
// abstracts; the test has to name them because there is no seam for it.
func pointConfigDirAt(t *testing.T, dir string) {
	t.Helper()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("AppData", dir)
	case "darwin":
		// os.UserConfigDir derives ~/Library/Application Support from HOME on
		// darwin, so the snapshot has to be taken of the same tree.
		t.Setenv("HOME", dir)
	default:
		t.Setenv("XDG_CONFIG_HOME", dir)
	}
}

// seedPreviousSettings writes a pre-0.3 `history.json` where the composition
// root looks for it, and returns its path.
//
// Derived through os.UserConfigDir rather than assembled by hand, because that
// is what openSettings does: on macOS the configuration directory is
// ~/Library/Application Support, not the home directory the environment
// variable names.
func seedPreviousSettings(t *testing.T, contents string) string {
	t.Helper()

	config, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("locating the user config directory: %v", err)
	}
	dir := filepath.Join(config, "PodSteer")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	path := filepath.Join(dir, "history.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("seeding the previous settings: %v", err)
	}
	return path
}

// snapshotDir maps every file under root to its size and contents hash.
func snapshotDir(t *testing.T, root string) map[string]string {
	t.Helper()

	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			out[relative+"/"] = "dir"
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[relative] = fmt.Sprintf("%x", sha256.Sum256(contents))
		return nil
	})
	if err != nil {
		t.Fatalf("snapshotting %s: %v", root, err)
	}
	return out
}
