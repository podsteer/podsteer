// Package localshell runs a shell on the operator's own machine.
//
// WHAT MAKES THIS DIFFERENT TO EVERY OTHER TERMINAL HERE. An exec, an attach,
// a debug container and a node shell all start a process in the cluster,
// through the API server, with the kubeconfig's credentials. This starts a
// process on the laptop PodSteer is running on. Nothing crosses the network,
// nothing is created in a cluster, and there is nothing to clean up anywhere
// but here.
//
// Three consequences follow, and all three are deliberate:
//
//   - NOTHING IS BUNDLED. PodSteer does not ship, download, or install
//     kubectl, helm, or a coding agent, and never offers to. The shell runs
//     what the operator already has; a machine without kubectl gets the
//     shell's own "command not found", which is the honest answer and their
//     business to fix.
//   - THE READ-ONLY GUARD DOES NOT APPLY. That guard is about PodSteer's own
//     writes — a local guard against the operator's own mistakes in this
//     application's buttons. A shell the operator opened on their own machine,
//     with their own credentials, is not something this application can or
//     should police, and pretending otherwise would be a claim of a
//     restriction that does not exist. The panel says so, and so does
//     SECURITY.md.
//   - THE OPERATOR'S KUBECONFIG IS READ, NEVER WRITTEN. KUBECONFIG names the
//     same files PodSteer itself reads, with one PodSteer-owned file in front
//     of them holding nothing but the open tab's `current-context` — no
//     clusters, no users, no credentials. That selects the context for this
//     shell through the ordinary kubeconfig merge, while their own files stay
//     byte-for-byte as they were and current-context in them is untouched.
//     See ContextNotice and kubecontext.go. The overlay is written per
//     session and removed with it, like every other thing this package
//     starts.
//
// The lifecycle is shaped like the port-forward and node-shell registries in
// the Kubernetes adapter, for the same reason both of those are: PodSteer
// started a process, so the record of it and the thing that kills it are
// created and destroyed together. A session ends when its pane closes and
// every session ends on shutdown.
package localshell

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// hangupGrace is how long a shell is given to leave on its own after the
// terminal hangs up, before it is killed outright.
//
// A shell asked to go away normally does within milliseconds. This is for the
// one that will not — something ignoring SIGHUP, a foreground program mid-write
// — and it is short because the caller is either closing a pane or shutting the
// application down, and neither can wait.
const hangupGrace = 2 * time.Second

// readBuffer sizes one read off the pseudo-terminal.
//
// The same order as the exec sessions' chunking: large enough that a burst of
// output is a handful of events rather than hundreds, small enough that an
// interactive keystroke echo is not held back waiting for a full buffer.
const readBuffer = 32 * 1024

// Config wires the manager to the rest of the application.
type Config struct {
	// KubeconfigFiles resolves the kubeconfig precedence list, in order.
	//
	// A function rather than a value because the list is re-scanned on every
	// call in the Kubernetes adapter — a file dropped into the directory named
	// by PODSTEER_KUBECONFIG_DIR appears without a restart, and a shell opened
	// afterwards must see it too.
	KubeconfigFiles func() []string
	// Lookup finds an executable, defaulting to exec.LookPath.
	Lookup LookupFunc
	// Shell resolves the operator's login shell, defaulting to shellpath's.
	Shell func() string
	// Home is the directory a shell starts in, defaulting to the operator's
	// home. A shell that starts wherever the application happened to be
	// launched is disorienting; a shell that starts at home is the one every
	// terminal emulator opens.
	Home func() (string, error)
	// Logger records starts and stops. A local shell is the most powerful
	// thing on this machine PodSteer can start, so it leaves a line.
	Logger *slog.Logger
}

// Manager owns every local shell PodSteer has started.
type Manager struct {
	cfg    Config
	logger *slog.Logger

	mu sync.Mutex
	// closed is set by StopAllLocalShells and never cleared: the process is on
	// its way out.
	//
	// SHUTTING DOWN, SO REGISTER NOTHING — the same flag, and the same rule,
	// as the node-shell registry and watchManager.ensure. BELT AND BRACES
	// RATHER THAN A LIVE BUG, and it is worth saying which: a local shell
	// leaks nothing into a cluster, and the kernel hangs the child up anyway
	// when the master descriptor closes as the process exits, so a session
	// registered after the sweep dies with PodSteer regardless. What it buys
	// is that the rule holds without depending on that: a session started
	// during shutdown is refused outright and torn down here, rather than
	// left in a map nobody reads again and reaped by a side effect of process
	// exit. The node shell next door has no such side effect to fall back on,
	// and consistency between the two registries is what stops somebody
	// reading one and assuming the other.
	closed bool
	byID   map[string]*session
	nextID int
}

// errShellsClosed reports a local shell refused because PodSteer is shutting
// down. The process it started is ended before this is returned.
var errShellsClosed = errors.New("PodSteer is shutting down, so no local shell was opened")

// Compile-time proof that the manager satisfies the port it is injected as.
var _ ports.LocalShellPort = (*Manager)(nil)

// session pairs the record of a shell with the process behind it.
type session struct {
	shell domain.LocalShell
	proc  *ptyProcess
	// overlayDir holds the per-session kubeconfig that selects this shell's
	// context, empty when there is none to remove.
	//
	// ON THE SESSION RATHER THAN IN A SWEEP, for the reason the port-forward's
	// listener and the node shell's pod are: the record and the thing it names
	// are created and destroyed together. A directory left in the temp folder
	// after its shell has gone is the same class of leak as a goroutine nobody
	// stops.
	overlayDir string
	// done closes once the process has exited and been reaped, so a caller
	// stopping a session can wait for it the way the port-forward registry
	// waits for a released socket.
	done chan struct{}
	// hangup runs once however many callers reach it — an explicit stop and a
	// shutdown can both arrive.
	hangup sync.Once
}

// New returns a manager. It starts nothing and touches no process.
func New(cfg Config, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.Lookup == nil {
		cfg.Lookup = exec.LookPath
	}
	if cfg.Home == nil {
		cfg.Home = os.UserHomeDir
	}
	return &Manager{
		cfg:    cfg,
		logger: logger.With(slog.String("adapter", "localshell")),
		byID:   make(map[string]*session),
	}
}

// LocalShellSupported reports whether this platform can open a local shell,
// and why not when it cannot.
func (m *Manager) LocalShellSupported() (bool, string) { return supported() }

// DetectAgents reports the coding agents on the adopted PATH.
func (m *Manager) DetectAgents() []domain.CodingAgent { return DetectAgents(m.cfg.Lookup) }

// StartLocalShell opens a pseudo-terminal running the operator's login shell,
// or the coding agent the spec names, and streams its output to out.
//
// out is written to by ONE goroutine, the reader started here, and nothing
// else — the context notice is written before that goroutine exists, so there
// is no interleaving to guard against.
//
// onExit is called exactly once, after the process has exited and been reaped,
// with the reason for the log and the pane, empty on an ordinary exit.
func (m *Manager) StartLocalShell(spec domain.LocalShellSpec, out io.Writer, onExit func(reason string)) (domain.LocalShell, error) {
	if ok, why := supported(); !ok {
		return domain.LocalShell{}, errors.New(why)
	}

	cmd, agentPath, err := m.command(spec)
	if err != nil {
		return domain.LocalShell{}, err
	}

	files, overlayDir, pinned := m.contextFiles(spec.Context)

	cmd.Env = BuildEnv(os.Environ(), files, spec)
	if home, err := m.cfg.Home(); err == nil && home != "" {
		cmd.Dir = home
	}

	// BEFORE the process starts, so the notice cannot land in the middle of a
	// prompt the shell has already drawn.
	if notice := ContextNotice(spec.Context, pinned); notice != "" && out != nil {
		// Dim, and on its own line: it is PodSteer talking, not the shell,
		// and it should not be mistaken for the first line of a session.
		// A writer that will not take the notice is the frontend having gone
		// away between the click and here; the shell is still worth opening.
		_, _ = fmt.Fprintf(out, "\x1b[90m%s\x1b[0m\r\n", notice)
	}

	proc, err := startPTY(cmd, spec.Cols, spec.Rows)
	if err != nil {
		// No session exists to carry the overlay, so nothing would ever come
		// back for it. Every path out of this function from here on either
		// registers the session or removes the directory itself.
		m.dropOverlay(overlayDir)
		return domain.LocalShell{}, fmt.Errorf("opening a local shell: %w", err)
	}

	m.mu.Lock()
	if m.closed {
		// StopAllLocalShells swept before this process was started, so the
		// sweep did not see it. End it here rather than register it. No pump
		// goroutine exists yet, so end's wait on done has nothing to wait
		// for and the teardown is written out: hang up, kill, reap, close.
		// No grace period either — nothing has been typed into a shell
		// nobody was ever shown, and the caller is a shutdown in progress.
		m.mu.Unlock()

		proc.Hangup()
		proc.Kill()
		_ = proc.Wait()
		proc.Close()
		m.dropOverlay(overlayDir)
		return domain.LocalShell{}, errShellsClosed
	}
	m.nextID++
	id := "local_" + strconv.Itoa(m.nextID)
	shell := domain.LocalShell{
		ID:      id,
		Context: spec.Context,
		Agent:   spec.Agent,
		Command: cmd.Path,
		Started: time.Now(),
	}
	entry := &session{shell: shell, proc: proc, done: make(chan struct{}), overlayDir: overlayDir}
	m.byID[id] = entry
	m.mu.Unlock()

	go m.pump(entry, out, onExit)

	m.logger.Info("local shell started",
		slog.String("session", id),
		slog.String("context", spec.Context),
		slog.String("command", cmd.Path),
		slog.String("agent", agentPath))

	return shell, nil
}

// command builds the process to run: the login shell, or an agent with its
// opening prompt.
func (m *Manager) command(spec domain.LocalShellSpec) (*exec.Cmd, string, error) {
	if spec.Agent == "" {
		shell := m.loginShell()
		if shell == "" {
			return nil, "", errors.New("opening a local shell: no login shell to run")
		}
		// What makes it a LOGIN shell is per-platform: `-l` on a POSIX shell,
		// and nothing at all on PowerShell, which reads the operator's profile
		// whenever it is interactive. See loginShellArgs.
		return exec.Command(shell, loginShellArgs()...), "", nil
	}

	// NEVER INSTALLED, ONLY FOUND. An agent that is not on the PATH is simply
	// not offered, and reaching here for one that has since gone is an error
	// naming it rather than anything that tries to obtain it.
	path, err := m.cfg.Lookup(spec.Agent)
	if err != nil || path == "" {
		return nil, "", fmt.Errorf("starting %s: not found on PATH", spec.Agent)
	}

	args, err := AgentArgs(spec.Agent, AgentPrompt(spec))
	if err != nil {
		return nil, "", err
	}
	return exec.Command(path, args...), path, nil
}

// loginShell resolves the shell to run.
func (m *Manager) loginShell() string {
	if m.cfg.Shell != nil {
		return m.cfg.Shell()
	}
	return defaultLoginShell()
}

// kubeconfigFiles resolves the precedence list, tolerating no resolver.
func (m *Manager) kubeconfigFiles() []string {
	if m.cfg.KubeconfigFiles == nil {
		return nil
	}
	return m.cfg.KubeconfigFiles()
}

// contextFiles returns the KUBECONFIG list for one session, the directory
// holding its context overlay, and whether the context is actually selected.
//
// THE OVERLAY GOES FIRST because client-go keeps the first definition of
// anything it merges, which is what makes a document holding only
// `current-context` decide the context while every cluster and user still
// comes from the operator's own files behind it.
//
// NO OVERLAY WHEN THE LIST IS EMPTY, and that is not tidiness. BuildEnv leaves
// KUBECONFIG alone when nothing resolved, precisely so a shell keeps seeing
// whatever clusters the operator's own environment gave it. Prepending an
// overlay would turn that empty list into a one-element one, KUBECONFIG would
// be set to a file naming a context nothing defines, and the shell would go
// from seeing their clusters to seeing none.
//
// A FAILURE HERE IS NOT A FAILURE TO OPEN A SHELL. The one thing that can go
// wrong is a temp directory that will not take a file, and a terminal is worth
// more than a pin: the session opens with exactly the behaviour it had before
// the overlay existed, and the notice says --context instead of claiming a
// context that is not selected.
func (m *Manager) contextFiles(kubeContext string) (files []string, overlayDir string, pinned bool) {
	files = m.kubeconfigFiles()
	if kubeContext == "" || len(files) == 0 {
		return files, "", false
	}

	dir, path, err := writeContextOverlay(kubeContext)
	if err != nil {
		m.logger.Warn("the shell's context could not be selected",
			slog.String("context", kubeContext),
			slog.String("error", err.Error()))
		return files, "", false
	}
	return append([]string{path}, files...), dir, true
}

// dropOverlay removes a session's overlay directory, best effort.
//
// Best effort AND LOGGED rather than returned: every caller is either retiring
// a session or unwinding a start that failed, and neither has anywhere to put
// an error. A line is left because a leak that says nothing is the one that
// accumulates.
func (m *Manager) dropOverlay(dir string) {
	if err := removeContextOverlay(dir); err != nil {
		m.logger.Warn("the shell's context overlay was left behind",
			slog.String("path", dir),
			slog.String("error", err.Error()))
	}
}

// pump copies the pseudo-terminal to out until the process ends, then reaps it.
//
// THE ONLY PLACE A SESSION IS RETIRED. Whether the shell exited on its own, a
// pane closed, or the application shut down, the process ends the same way:
// this loop sees the terminal close, waits for the child, forgets the record
// and closes done. A caller that stopped the session is waiting on that
// channel, which is what makes "stopped" mean "gone" rather than "asked".
func (m *Manager) pump(entry *session, out io.Writer, onExit func(reason string)) {
	buf := make([]byte, readBuffer)
	for {
		n, err := entry.proc.Read(buf)
		if n > 0 && out != nil {
			// A failed write is the frontend having gone away, which is not a
			// reason to kill somebody's shell mid-command — the pane's own
			// teardown decides that.
			_, _ = out.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}

	// Reaped before the record is dropped, so a stopped session is never
	// reported gone while a zombie is still on the process table.
	waitErr := entry.proc.Wait()
	entry.proc.Close()

	m.mu.Lock()
	delete(m.byID, entry.shell.ID)
	m.mu.Unlock()

	// The one place a session is retired is the one place its overlay is
	// removed, and BEFORE done is closed: a caller that stopped this session —
	// StopLocalShell, or StopAllLocalShells on the way out — is waiting on that
	// channel, and "stopped" has to mean the file is gone too, not merely that
	// the process is.
	m.dropOverlay(entry.overlayDir)

	reason := exitReason(waitErr)
	m.logger.Info("local shell ended",
		slog.String("session", entry.shell.ID),
		slog.String("reason", reason))

	close(entry.done)
	if onExit != nil {
		onExit(reason)
	}
}

// exitReason describes how a shell ended, for the pane.
//
// A non-zero status is NOT a failure worth reporting: `exit 1` is an ordinary
// way to leave a shell, and a killed one is what closing the pane does. Only
// something that is not an exit status at all — a process that could not be
// reaped — is worth a line.
func exitReason(err error) string {
	if err == nil {
		return ""
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return ""
	}
	return err.Error()
}

// WriteLocalShell sends keystrokes to a session.
func (m *Manager) WriteLocalShell(id string, data []byte) error {
	m.mu.Lock()
	entry, ok := m.byID[id]
	m.mu.Unlock()

	if !ok {
		return fmt.Errorf("local shell %s is not running", id)
	}
	_, err := entry.proc.Write(data)
	return err
}

// ResizeLocalShell tells the pseudo-terminal its new size, which is what
// delivers SIGWINCH to whatever is running in it.
//
// Without this a full-screen program keeps drawing at the size it started
// with, which is the visible half of "resizing is honoured"; the invisible
// half is that the shell's own line editing wraps in the wrong column.
func (m *Manager) ResizeLocalShell(id string, cols, rows uint16) error {
	m.mu.Lock()
	entry, ok := m.byID[id]
	m.mu.Unlock()

	if !ok {
		return fmt.Errorf("local shell %s is not running", id)
	}
	return entry.proc.Resize(cols, rows)
}

// StopLocalShell ends one session and waits for the process to be gone.
//
// Idempotent: a pane closing and a shutdown can both reach it, and a session
// that has already exited on its own is not an error.
func (m *Manager) StopLocalShell(id string) error {
	m.mu.Lock()
	entry, ok := m.byID[id]
	m.mu.Unlock()

	if !ok {
		return nil
	}
	m.end(entry)
	return nil
}

// StopAllLocalShells ends every session, for shutdown.
//
// The map is copied under the lock and the stopping happens outside it, for
// the reason the port-forward registry does the same: ending a session blocks
// until its reader goroutine has retired the record, and that goroutine needs
// this mutex.
//
// It also CLOSES the registry, permanently, so a start racing this sweep is
// refused rather than registered behind it. See the closed field. Safe to
// call twice.
//
// Each session's context overlay goes with it, because end waits on the pump,
// and the pump removes the directory before it closes done. There is no
// separate sweep for the files: a second list of things to clean up is how the
// two lists come to disagree.
func (m *Manager) StopAllLocalShells() {
	m.mu.Lock()
	m.closed = true
	entries := make([]*session, 0, len(m.byID))
	for _, entry := range m.byID {
		entries = append(entries, entry)
	}
	m.mu.Unlock()

	for _, entry := range entries {
		m.end(entry)
	}
}

// ListLocalShells reports what is running right now.
func (m *Manager) ListLocalShells() []domain.LocalShell {
	m.mu.Lock()
	defer m.mu.Unlock()

	shells := make([]domain.LocalShell, 0, len(m.byID))
	for _, entry := range m.byID {
		shells = append(shells, entry.shell)
	}
	return shells
}

// end hangs the terminal up, escalates if that is ignored, and waits.
func (m *Manager) end(entry *session) {
	entry.hangup.Do(func() { entry.proc.Hangup() })

	select {
	case <-entry.done:
		return
	case <-time.After(hangupGrace):
	}

	if !shouldKill(entry.done) {
		<-entry.done
		return
	}

	// Ignored the hangup. The process group goes, not just the leader: a
	// shell's children are what would otherwise be left running.
	entry.proc.Kill()
	<-entry.done
}

// shouldKill reports whether a session that outlasted the hangup grace is
// still there to be signalled.
//
// RE-CHECKED IMMEDIATELY BEFORE Kill, BECAUSE THE TIMER FIRING IS NOT THE
// SAME ANSWER. The pump can reap the child and close done in the window
// between the grace elapsing and the signal, and Kill signals the process
// GROUP by raw id — `syscall.Kill(-pid, …)`. os.Process.Signal would have
// refused to signal a process it knows has been reaped; a raw group signal
// has no such knowledge, and once the child is reaped its pid, and therefore
// its group id, is free for the kernel to hand to something else.
//
// A RESIDUAL WINDOW IS INHERENT to signalling a group by id, and this does
// not close it: the process can still be reaped between this check and the
// syscall a few instructions later. What it does is shrink the window from
// the whole grace period — two seconds, during which a shell that was merely
// slow to leave routinely finishes — to those few instructions. Closing it
// entirely would mean giving up the group signal, and the group is the whole
// point: it is what stops a shell's children being left running.
func shouldKill(done <-chan struct{}) bool {
	select {
	case <-done:
		return false
	default:
		return true
	}
}
