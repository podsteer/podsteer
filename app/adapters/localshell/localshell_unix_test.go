//go:build !windows

// Lifecycle tests for the local shell.
//
// UNIX ONLY, deliberately: there is no pseudo-terminal on Windows in this
// build, so there is no process to start and nothing here would be exercising
// the implementation that ships there. What Windows does instead —
// LocalShellSupported reporting false with a sentence — is asserted in
// terminal_test.go against the port, on every platform.
//
// Every test drives a real /bin/sh on a real terminal. A fake would prove
// nothing about the two things that actually go wrong with a child process:
// that closing a pane leaves it running, and that a resize never reaches it.

package localshell

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// safeBuffer collects a session's output across the reader goroutine and the
// test's own goroutine.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// exitRecorder captures the one call a session's exit hook is allowed.
type exitRecorder struct {
	mu     sync.Mutex
	calls  int
	reason string
	done   chan struct{}
}

func newExitRecorder() *exitRecorder {
	return &exitRecorder{done: make(chan struct{})}
}

func (e *exitRecorder) hook(reason string) {
	e.mu.Lock()
	e.calls++
	e.reason = reason
	first := e.calls == 1
	e.mu.Unlock()
	if first {
		close(e.done)
	}
}

func (e *exitRecorder) waitExited(t *testing.T) {
	t.Helper()
	select {
	case <-e.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the session never reported an exit")
	}
}

// testManager returns a manager that runs /bin/sh rather than whatever the
// machine running the suite has as a login shell.
//
// $SHELL on a developer's machine is fish or nushell as often as not, and this
// package is not testing their startup files.
func testManager(t *testing.T, files []string) *Manager {
	t.Helper()
	return New(Config{
		Shell:           func() string { return "/bin/sh" },
		KubeconfigFiles: func() []string { return files },
		Home:            func() (string, error) { return t.TempDir(), nil },
	}, nil)
}

// waitFor polls until cond holds or the test gives up, for the handful of
// facts that become true a moment after a signal rather than at the call.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestLocalShellStreamsOutputAndPrintsTheContextNotice covers the pane's whole
// first second: the notice is printed before the shell starts, and what the
// shell writes reaches the writer the session was given.
func TestLocalShellStreamsOutputAndPrintsTheContextNotice(t *testing.T) {
	manager := testManager(t, nil)
	out := &safeBuffer{}
	exit := newExitRecorder()

	shell, err := manager.StartLocalShell(
		domain.LocalShellSpec{Context: "staging", Cols: 80, Rows: 24}, out, exit.hook)
	if err != nil {
		t.Fatalf("StartLocalShell() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.StopLocalShell(shell.ID) })

	if !strings.Contains(out.String(), "staging") {
		t.Fatalf("the notice was not printed before the shell started; output so far:\n%s", out.String())
	}

	if err := manager.WriteLocalShell(shell.ID, []byte("printf 'PODSTEER-MARK\\n'\n")); err != nil {
		t.Fatalf("WriteLocalShell() error = %v", err)
	}
	waitFor(t, "the shell's output", func() bool {
		return strings.Count(out.String(), "PODSTEER-MARK") >= 2 // the echo, then the result
	})
}

// TestStopLocalShellEndsTheProcess is the "killed when its tab closes" half of
// the lifecycle. Stop must not merely ask: it waits, and the registry is empty
// when it returns, because a session still listed after being stopped is one
// nothing will ever stop again.
func TestStopLocalShellEndsTheProcess(t *testing.T) {
	manager := testManager(t, nil)
	exit := newExitRecorder()

	shell, err := manager.StartLocalShell(
		domain.LocalShellSpec{Context: "staging", Cols: 80, Rows: 24}, &safeBuffer{}, exit.hook)
	if err != nil {
		t.Fatalf("StartLocalShell() error = %v", err)
	}

	if err := manager.StopLocalShell(shell.ID); err != nil {
		t.Fatalf("StopLocalShell() error = %v", err)
	}
	if live := manager.ListLocalShells(); len(live) != 0 {
		t.Fatalf("ListLocalShells() = %v after Stop, want none", live)
	}

	exit.waitExited(t)
	exit.mu.Lock()
	calls := exit.calls
	exit.mu.Unlock()
	if calls != 1 {
		t.Fatalf("exit hook ran %d times, want exactly 1", calls)
	}

	// Idempotent: the pane closing and a shutdown can both reach it.
	if err := manager.StopLocalShell(shell.ID); err != nil {
		t.Fatalf("second StopLocalShell() error = %v, want a no-op", err)
	}
}

// TestStopAllLocalShellsEndsEverySession is the shutdown half. A shell whose
// window has gone cannot be seen, typed into or ended, and it holds its own
// children with it.
func TestStopAllLocalShellsEndsEverySession(t *testing.T) {
	manager := testManager(t, nil)

	for i := 0; i < 3; i++ {
		if _, err := manager.StartLocalShell(
			domain.LocalShellSpec{Context: "staging", Cols: 80, Rows: 24}, &safeBuffer{}, nil); err != nil {
			t.Fatalf("StartLocalShell() error = %v", err)
		}
	}
	if live := manager.ListLocalShells(); len(live) != 3 {
		t.Fatalf("ListLocalShells() = %d, want 3 before shutdown", len(live))
	}

	manager.StopAllLocalShells()

	if live := manager.ListLocalShells(); len(live) != 0 {
		t.Fatalf("ListLocalShells() = %v after StopAll, want none", live)
	}
}

// TestLocalShellRetiresItselfWhenTheShellExits covers the other direction:
// the operator typed exit. Nothing called Stop, so the reader goroutine is
// what has to notice, reap the child and drop the record.
func TestLocalShellRetiresItselfWhenTheShellExits(t *testing.T) {
	manager := testManager(t, nil)
	exit := newExitRecorder()

	shell, err := manager.StartLocalShell(
		domain.LocalShellSpec{Context: "staging", Cols: 80, Rows: 24}, &safeBuffer{}, exit.hook)
	if err != nil {
		t.Fatalf("StartLocalShell() error = %v", err)
	}

	if err := manager.WriteLocalShell(shell.ID, []byte("exit 3\n")); err != nil {
		t.Fatalf("WriteLocalShell() error = %v", err)
	}

	exit.waitExited(t)
	waitFor(t, "the session to be forgotten", func() bool { return len(manager.ListLocalShells()) == 0 })

	// A non-zero status is an ordinary way to leave a shell, not something to
	// report as a failure in the pane.
	exit.mu.Lock()
	reason := exit.reason
	exit.mu.Unlock()
	if reason != "" {
		t.Fatalf("exit reason = %q, want empty for an ordinary exit status", reason)
	}
}

// TestResizeLocalShellChangesTheTerminalSize pins that a resize reaches the
// pseudo-terminal, which is what delivers SIGWINCH to whatever is running in
// it. Asserted on the terminal rather than on a program's redraw, because the
// ioctl is the thing this code is responsible for.
func TestResizeLocalShellChangesTheTerminalSize(t *testing.T) {
	manager := testManager(t, nil)

	shell, err := manager.StartLocalShell(
		domain.LocalShellSpec{Context: "staging", Cols: 80, Rows: 24}, &safeBuffer{}, nil)
	if err != nil {
		t.Fatalf("StartLocalShell() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.StopLocalShell(shell.ID) })

	manager.mu.Lock()
	entry := manager.byID[shell.ID]
	manager.mu.Unlock()
	if entry == nil {
		t.Fatal("the session is not in the registry")
	}

	if cols, rows, err := entry.proc.size(); err != nil || cols != 80 || rows != 24 {
		t.Fatalf("initial size = %dx%d (err %v), want 80x24 — a prompt drawn at the wrong width stays wrong", cols, rows, err)
	}

	if err := manager.ResizeLocalShell(shell.ID, 132, 43); err != nil {
		t.Fatalf("ResizeLocalShell() error = %v", err)
	}
	cols, rows, err := entry.proc.size()
	if err != nil {
		t.Fatalf("reading the size back: %v", err)
	}
	if cols != 132 || rows != 43 {
		t.Fatalf("size = %dx%d, want 132x43", cols, rows)
	}
}

// TestResizeAndWriteRefuseAnUnknownSession covers the window between a shell
// exiting on its own and the pane noticing. Both must fail with a sentence
// rather than reach into a session that is gone.
func TestResizeAndWriteRefuseAnUnknownSession(t *testing.T) {
	t.Parallel()

	manager := testManager(t, nil)

	if err := manager.WriteLocalShell("local_404", []byte("x")); err == nil {
		t.Error("WriteLocalShell() error = nil, want a refusal for a session that is not running")
	}
	if err := manager.ResizeLocalShell("local_404", 80, 24); err == nil {
		t.Error("ResizeLocalShell() error = nil, want a refusal for a session that is not running")
	}
	if err := manager.StopLocalShell("local_404"); err != nil {
		t.Errorf("StopLocalShell() error = %v, want a no-op for a session already gone", err)
	}
}

// TestLocalShellNeverWritesTheKubeconfig is the rule CLAUDE.md states
// absolutely: opening a shell for a cluster tab must not touch the operator's
// kubeconfig, and in particular must not set current-context, because kubectl
// in the terminal next to this one would silently change target.
//
// Asserted on the bytes rather than on the parsed document: any write at all
// is the failure, whatever it changed.
func TestLocalShellNeverWritesTheKubeconfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	original := []byte("apiVersion: v1\nkind: Config\ncurrent-context: alpha\ncontexts: []\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	manager := testManager(t, []string{path})
	shell, err := manager.StartLocalShell(
		domain.LocalShellSpec{Context: "beta", Cols: 80, Rows: 24}, &safeBuffer{}, nil)
	if err != nil {
		t.Fatalf("StartLocalShell() error = %v", err)
	}
	if err := manager.StopLocalShell(shell.ID); err != nil {
		t.Fatalf("StopLocalShell() error = %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-reading the fixture: %v", err)
	}
	if !bytes.Equal(original, after) {
		t.Fatalf("the kubeconfig was rewritten:\nbefore %q\nafter  %q", original, after)
	}

	// And nothing was written beside it either — a per-session copy of
	// somebody's credentials is the other way this rule gets broken.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("listing the fixture directory: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("files beside the kubeconfig = %v, want only the original", names)
	}
}
