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
//     same files PodSteer itself reads. current-context is left exactly as it
//     was, and the context of the open tab is stated in a notice rather than
//     imposed — see ContextNotice for why there is no honest way to pin one.
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
	"runtime"
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

	mu     sync.Mutex
	byID   map[string]*session
	nextID int
}

// Compile-time proof that the manager satisfies the port it is injected as.
var _ ports.LocalShellPort = (*Manager)(nil)

// session pairs the record of a shell with the process behind it.
type session struct {
	shell domain.LocalShell
	proc  *ptyProcess
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

	cmd.Env = BuildEnv(os.Environ(), m.kubeconfigFiles(), spec)
	if home, err := m.cfg.Home(); err == nil && home != "" {
		cmd.Dir = home
	}

	// BEFORE the process starts, so the notice cannot land in the middle of a
	// prompt the shell has already drawn.
	if notice := ContextNotice(spec.Context); notice != "" && out != nil {
		// Dim, and on its own line: it is PodSteer talking, not the shell,
		// and it should not be mistaken for the first line of a session.
		// A writer that will not take the notice is the frontend having gone
		// away between the click and here; the shell is still worth opening.
		_, _ = fmt.Fprintf(out, "\x1b[90m%s\x1b[0m\r\n", notice)
	}

	proc, err := startPTY(cmd, spec.Cols, spec.Rows)
	if err != nil {
		return domain.LocalShell{}, fmt.Errorf("opening a local shell: %w", err)
	}

	m.mu.Lock()
	m.nextID++
	id := "local_" + strconv.Itoa(m.nextID)
	shell := domain.LocalShell{
		ID:      id,
		Context: spec.Context,
		Agent:   spec.Agent,
		Command: cmd.Path,
		Started: time.Now(),
	}
	entry := &session{shell: shell, proc: proc, done: make(chan struct{})}
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
		// -l alone is both login AND interactive: a shell with no command
		// argument, on a terminal, decides it is interactive by itself. That
		// is what makes it read the operator's own startup files and draw
		// their own prompt, rather than being a stripped shell wearing their
		// PATH.
		return exec.Command(shell, "-l"), "", nil
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
func (m *Manager) StopAllLocalShells() {
	m.mu.Lock()
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

	// Ignored the hangup. The process group goes, not just the leader: a
	// shell's children are what would otherwise be left running.
	entry.proc.Kill()
	<-entry.done
}

// defaultLoginShell names the shell to run when nothing else says.
//
// $SHELL is the operator's own answer and is preferred whenever it exists;
// the fallbacks are only for a process launched with no environment at all.
func defaultLoginShell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	if runtime.GOOS == "darwin" {
		return "/bin/zsh"
	}
	return "/bin/sh"
}
