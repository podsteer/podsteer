//go:build !windows

// Lifecycle tests for the local shell.
//
// UNIX ONLY, deliberately: these drive a real /bin/sh on a real terminal, and
// Windows has neither. Its own path — ConPTY, a command LINE rather than an
// argv, and a kill by pid because there is no process group — is exercised by
// the parts of it that are not the console itself: see pty_windows_test.go
// for the quoting, and shell_windows_test.go for which shell it chooses.
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

// overlayDirOf reports the overlay directory the manager recorded for one
// session, for the lifecycle assertions below. Reaching into the registry is
// the point: the promise is that the record and the file are created and
// destroyed together, so the test has to be able to see both.
func overlayDirOf(t *testing.T, manager *Manager, id string) string {
	t.Helper()
	manager.mu.Lock()
	defer manager.mu.Unlock()
	entry, ok := manager.byID[id]
	if !ok {
		t.Fatalf("session %s is not registered", id)
	}
	return entry.overlayDir
}

// TestLocalShellSelectsTheTabsContextThroughAnOverlay is the end-to-end half of
// the pinning decision: the shell's own KUBECONFIG starts with a PodSteer file
// that names the open tab's context, and the operator's file is still behind
// it. Read out of the running shell rather than off the manager, because what
// matters is what a command typed into that terminal would see.
func TestLocalShellSelectsTheTabsContextThroughAnOverlay(t *testing.T) {
	operator := writeOperatorKubeconfig(t)
	manager := testManager(t, []string{operator})
	out := &safeBuffer{}

	shell, err := manager.StartLocalShell(
		domain.LocalShellSpec{Context: "beta", Cols: 200, Rows: 24}, out, nil)
	if err != nil {
		t.Fatalf("StartLocalShell() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.StopLocalShell(shell.ID) })

	dir := overlayDirOf(t, manager, shell.ID)
	if dir == "" {
		t.Fatal("no overlay was written for a session opened on a context")
	}
	overlay := filepath.Join(dir, overlayFileName)
	body, err := os.ReadFile(overlay)
	if err != nil {
		t.Fatalf("reading the overlay: %v", err)
	}
	if !strings.Contains(string(body), "current-context: beta") {
		t.Errorf("overlay =\n%s\nwant it to select beta", body)
	}

	// PODSTEER-KC is a marker the shell itself prints, so this asserts the
	// environment the process actually got rather than the slice built for it.
	if err := manager.WriteLocalShell(shell.ID, []byte("printf 'PODSTEER-KC=%s\\n' \"$KUBECONFIG\"\n")); err != nil {
		t.Fatalf("WriteLocalShell() error = %v", err)
	}
	waitFor(t, "the shell's KUBECONFIG", func() bool {
		return strings.Contains(out.String(), "PODSTEER-KC="+overlay)
	})

	want := overlay + string(os.PathListSeparator) + operator
	if !strings.Contains(out.String(), "PODSTEER-KC="+want) {
		t.Errorf("KUBECONFIG did not read %q; output so far:\n%s", want, out.String())
	}
}

// TestLocalShellRemovesItsOverlayWhenTheSessionEnds is the lifecycle rule this
// package already holds every process to, applied to the file: the record and
// the thing it names go together. A directory left behind after its shell is
// gone is the same class of leak as a goroutine nobody stops.
//
// The check is made AFTER StopLocalShell returns, which is the strong form:
// stop waits on the pump, and the pump removes the directory before it closes
// done, so "stopped" has to mean the file is gone and not merely that the
// process is.
func TestLocalShellRemovesItsOverlayWhenTheSessionEnds(t *testing.T) {
	operator := writeOperatorKubeconfig(t)
	manager := testManager(t, []string{operator})

	shell, err := manager.StartLocalShell(
		domain.LocalShellSpec{Context: "beta", Cols: 80, Rows: 24}, &safeBuffer{}, nil)
	if err != nil {
		t.Fatalf("StartLocalShell() error = %v", err)
	}
	dir := overlayDirOf(t, manager, shell.ID)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the overlay directory should exist while the session does: %v", err)
	}

	if err := manager.StopLocalShell(shell.ID); err != nil {
		t.Fatalf("StopLocalShell() error = %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the overlay directory survived its session: stat err = %v", err)
	}
}

// TestStopAllLocalShellsRemovesEveryOverlay covers the shutdown path, where
// leaks are least likely to be noticed: the application is going away, so
// nothing is left to report a directory nobody removed.
func TestStopAllLocalShellsRemovesEveryOverlay(t *testing.T) {
	operator := writeOperatorKubeconfig(t)
	manager := testManager(t, []string{operator})

	dirs := make([]string, 0, 2)
	for _, context := range []string{"alpha", "beta"} {
		shell, err := manager.StartLocalShell(
			domain.LocalShellSpec{Context: context, Cols: 80, Rows: 24}, &safeBuffer{}, nil)
		if err != nil {
			t.Fatalf("StartLocalShell(%s) error = %v", context, err)
		}
		dirs = append(dirs, overlayDirOf(t, manager, shell.ID))
	}

	manager.StopAllLocalShells()

	for _, dir := range dirs {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("overlay directory %s survived shutdown: stat err = %v", dir, err)
		}
	}
}

// TestLocalShellPinsNothingWhenNoKubeconfigResolved guards the interaction
// between the overlay and BuildEnv's deliberate silence.
//
// BuildEnv leaves KUBECONFIG alone when nothing resolved, so a shell keeps
// seeing whatever clusters the operator's own environment gave it. Prepending
// an overlay would turn that empty list into a one-element one — KUBECONFIG
// set to a file naming a context nothing defines — and the shell would go from
// seeing their clusters to seeing none. Worse than not pinning, which is why
// not pinning is what happens.
func TestLocalShellPinsNothingWhenNoKubeconfigResolved(t *testing.T) {
	manager := testManager(t, nil)
	out := &safeBuffer{}

	shell, err := manager.StartLocalShell(
		domain.LocalShellSpec{Context: "beta", Cols: 80, Rows: 24}, out, nil)
	if err != nil {
		t.Fatalf("StartLocalShell() error = %v", err)
	}
	t.Cleanup(func() { _ = manager.StopLocalShell(shell.ID) })

	if dir := overlayDirOf(t, manager, shell.ID); dir != "" {
		t.Errorf("overlay directory = %q, want none when no kubeconfig resolved", dir)
	}
	// And the notice must say --context, because nothing selected one.
	if !strings.Contains(out.String(), "--context beta") {
		t.Errorf("notice did not fall back to the flag; output so far:\n%s", out.String())
	}
}
