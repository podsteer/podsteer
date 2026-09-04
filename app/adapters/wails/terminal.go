package wails

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// terminalSizeQueue implements ports.TerminalSizeQueue for resize events.
//
// The queue owns its own closing, and that is the whole point of the mutex.
// A send on a closed channel panics — `select` with a `default` does not
// change that, it only avoids blocking — so the send and the close have to be
// serialised or the process dies. It is reachable in ordinary use: the shell
// exits while the window is being dragged, and a drag emits resizes
// continuously.
type terminalSizeQueue struct {
	mu     sync.Mutex
	closed bool
	ch     chan ports.TerminalSize
}

func newTerminalSizeQueue() *terminalSizeQueue {
	return &terminalSizeQueue{
		ch: make(chan ports.TerminalSize, 1),
	}
}

func (q *terminalSizeQueue) Next() *ports.TerminalSize {
	size, ok := <-q.ch
	if !ok {
		return nil
	}
	return &size
}

// send offers a new size, dropping it if the queue is full or already closed.
//
// Dropping a size when one is already waiting is deliberate: only the latest
// matters, and the reader will take it. Holding the lock across the send is
// safe precisely because the send cannot block — the channel is buffered and
// the default arm covers a full buffer.
func (q *terminalSizeQueue) send(size ports.TerminalSize) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return
	}
	select {
	case q.ch <- size:
	default:
	}
}

// close ends the queue, unblocking Next. Safe to call more than once.
func (q *terminalSizeQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return
	}
	q.closed = true
	close(q.ch)
}

// terminalSession represents a live exec session with a pod container.
type terminalSession struct {
	id        string
	cancel    context.CancelFunc
	stdinPipe io.WriteCloser
	sizeQueue *terminalSizeQueue
}

// TerminalDataEvent is the payload of the "terminal:data" event.
type TerminalDataEvent struct {
	// SessionID identifies the terminal session.
	SessionID string `json:"sessionId"`
	// Data is raw terminal output (may include ANSI escape sequences).
	Data string `json:"data"`
}

// TerminalExitEvent is the payload of the "terminal:exit" event.
type TerminalExitEvent struct {
	// SessionID identifies the terminal session.
	SessionID string `json:"sessionId"`
	// Reason explains why the session ended, empty on normal exit.
	Reason string `json:"reason"`
}

// TerminalAPI exposes interactive terminal sessions to the frontend.
//
// Unlike ExecInPod (which is request/response), TerminalAPI maintains
// persistent bidirectional streams with TTY allocation. This enables
// interactive programs like top, htop, vim, less, and interactive shells.
type TerminalAPI struct {
	management *application.ManagementService
	// nodeShells creates and deletes the privileged pod behind a node shell.
	// A node shell's pod is created before its attach session opens and
	// deleted when the session ends, so the terminal API — which owns the
	// session's lifetime — is what ties the two together.
	nodeShells ports.NodeShellPort
	app        *App
	logger     *slog.Logger

	mu       sync.Mutex
	sessions map[string]*terminalSession
}

// NewTerminalAPI returns a new terminal API.
func NewTerminalAPI(management *application.ManagementService, nodeShells ports.NodeShellPort, app *App, logger *slog.Logger) (*TerminalAPI, error) {
	switch {
	case management == nil:
		return nil, errors.New("wails: TerminalAPI requires a ManagementService")
	case nodeShells == nil:
		return nil, errors.New("wails: TerminalAPI requires a NodeShellPort")
	case app == nil:
		return nil, errors.New("wails: TerminalAPI requires an App")
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &TerminalAPI{
		management: management,
		nodeShells: nodeShells,
		app:        app,
		logger:     logger.With(slog.String("api", "terminal")),
		sessions:   make(map[string]*terminalSession),
	}, nil
}

func generateTerminalID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return "term_" + hex.EncodeToString(b)
}

// StartSession creates a new interactive terminal session in a pod container.
//
// It allocates a PTY and starts a shell (/bin/sh). The session streams
// stdout/stderr to the frontend via "terminal:data" events. The frontend
// sends keystrokes via TerminalWrite.
//
// Returns the session ID.
func (t *TerminalAPI) StartSession(clusterID, namespace, podName, containerName string, cols, rows int) (string, error) {
	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return "", apiError(t.logger, "StartSession", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return "", apiError(t.logger, "StartSession", err)
	}

	// An interactive shell can mutate the cluster as freely as any other
	// write, so it gets the same refusal — but checked HERE, synchronously,
	// before a PTY is allocated and a goroutine started. Letting the session
	// open and fail on its first write would read as a terminal that
	// connected and then immediately died for no visible reason.
	// ManagementService.ExecInPodWithTTY checks again; this is only the fast
	// path that avoids the false start.
	if t.management.ReadOnly(id) {
		return "", apiError(t.logger, "StartSession",
			fmt.Errorf("starting terminal session: %w", ports.ErrReadOnly))
	}

	sessionID := generateTerminalID()

	// Create the stdin pipe
	stdinReader, stdinWriter := io.Pipe()

	// Create the terminal size queue with initial size
	sizeQueue := newTerminalSizeQueue()
	sizeQueue.send(ports.TerminalSize{
		Width:  uint16(cols),
		Height: uint16(rows),
	})

	// Through the accessor, not the field. app.ctx is guarded by app.mu and is
	// set to nil on shutdown, so reading it bare both races that write and
	// hands a nil parent to WithCancel — which panics. A session asked for as
	// the window closes should be refused, the way late events are dropped.
	parent, ok := t.app.runtimeContext()
	if !ok {
		return "", errors.New("application is shutting down")
	}
	ctx, cancel := context.WithCancel(parent)

	session := &terminalSession{
		id:        sessionID,
		cancel:    cancel,
		stdinPipe: stdinWriter,
		sizeQueue: sizeQueue,
	}

	t.mu.Lock()
	t.sessions[sessionID] = session
	t.mu.Unlock()

	// Create a custom stdout writer that emits events to the frontend
	stdoutWriter := &terminalOutputWriter{
		sessionID: sessionID,
		app:       t.app,
	}

	// Start the exec in a goroutine
	go func() {
		defer func() {
			t.mu.Lock()
			delete(t.sessions, sessionID)
			t.mu.Unlock()

			// Closing signals EOF to the remote shell; the exec is already unwinding.
			_ = stdinWriter.Close()
			sizeQueue.close()
		}()

		// Determine the shell to use
		shell := []string{"/bin/sh"}

		err := t.management.ExecInPodWithTTY(
			ctx,
			id,
			ns,
			podName,
			containerName,
			shell,
			stdinReader,
			stdoutWriter,
			stdoutWriter, // stderr goes to same output in TTY mode
			sizeQueue,
		)

		reason := ""
		if err != nil && !errors.Is(err, context.Canceled) {
			reason = err.Error()
			t.logger.Error("terminal session ended with error",
				slog.String("session", sessionID),
				slog.String("error", err.Error()))
		}

		// Notify frontend that the session ended
		t.app.emit("terminal:exit", TerminalExitEvent{
			SessionID: sessionID,
			Reason:    reason,
		})
	}()

	t.logger.Info("terminal session started",
		slog.String("session", sessionID),
		slog.String("pod", podName),
		slog.String("container", containerName))

	return sessionID, nil
}

// StartAttachSession creates a new interactive session attached to a
// container's own running process — PID 1, whatever the image's
// ENTRYPOINT/CMD started — rather than the new shell StartSession opens.
//
// It shares StartSession's machinery end to end: the same session registry,
// the same "terminal:data"/"terminal:exit" events, and Write/Resize/StopSession
// work identically on either kind of session, so the frontend need not know
// which one it opened once it has the session ID back.
//
// Returns the session ID.
func (t *TerminalAPI) StartAttachSession(clusterID, namespace, podName, containerName string, cols, rows int) (string, error) {
	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return "", apiError(t.logger, "StartAttachSession", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return "", apiError(t.logger, "StartAttachSession", err)
	}

	// Attaching can type into the container's process as freely as an
	// interactive shell can, so it gets StartSession's identical synchronous
	// refusal, checked HERE before a PTY is allocated or a goroutine
	// started. ManagementService.AttachToPod checks again; this is only the
	// fast path that avoids the false start.
	if t.management.ReadOnly(id) {
		return "", apiError(t.logger, "StartAttachSession",
			fmt.Errorf("starting attach session: %w", ports.ErrReadOnly))
	}

	sessionID := generateTerminalID()

	// Create the stdin pipe
	stdinReader, stdinWriter := io.Pipe()

	// Create the terminal size queue with initial size
	sizeQueue := newTerminalSizeQueue()
	sizeQueue.send(ports.TerminalSize{
		Width:  uint16(cols),
		Height: uint16(rows),
	})

	parent, ok := t.app.runtimeContext()
	if !ok {
		return "", errors.New("application is shutting down")
	}
	ctx, cancel := context.WithCancel(parent)

	session := &terminalSession{
		id:        sessionID,
		cancel:    cancel,
		stdinPipe: stdinWriter,
		sizeQueue: sizeQueue,
	}

	t.mu.Lock()
	t.sessions[sessionID] = session
	t.mu.Unlock()

	stdoutWriter := &terminalOutputWriter{
		sessionID: sessionID,
		app:       t.app,
	}

	go func() {
		defer func() {
			t.mu.Lock()
			delete(t.sessions, sessionID)
			t.mu.Unlock()

			// Closing signals EOF to the remote process; the attach is
			// already unwinding.
			_ = stdinWriter.Close()
			sizeQueue.close()
		}()

		err := t.management.AttachToPod(
			ctx,
			id,
			ns,
			podName,
			containerName,
			stdinReader,
			stdoutWriter,
			stdoutWriter, // stderr goes to same output in TTY mode
			sizeQueue,
		)

		reason := ""
		if err != nil && !errors.Is(err, context.Canceled) {
			reason = err.Error()
			t.logger.Error("attach session ended with error",
				slog.String("session", sessionID),
				slog.String("error", err.Error()))
		}

		// Reported through the SAME event StartSession uses: the frontend
		// treats an attach session's exit identically to a shell's.
		t.app.emit("terminal:exit", TerminalExitEvent{
			SessionID: sessionID,
			Reason:    reason,
		})
	}()

	t.logger.Info("attach session started",
		slog.String("session", sessionID),
		slog.String("pod", podName),
		slog.String("container", containerName))

	return sessionID, nil
}

// debugPrepTimeout bounds adding the ephemeral container and waiting for it to
// run — an image pull, mostly. Generous, because the shell must not be opened
// before the container is up, and the alternative to waiting is a session that
// connects to nothing.
const debugPrepTimeout = 90 * time.Second

// nodeShellPrepTimeout bounds creating the node-shell pod and waiting for it to
// schedule and run, for the same reason.
const nodeShellPrepTimeout = 90 * time.Second

// StartDebugSession adds an ephemeral debug container to a pod — the way
// `kubectl debug -it POD --image=… --target=CONTAINER` does — waits for it to
// run, and opens an interactive shell into it through the SAME exec path
// StartSession uses. It returns the session ID.
//
// The container it adds cannot be removed: an ephemeral container stays in the
// pod's spec until the pod is deleted, which is Kubernetes' behaviour and what
// the dialog offering this states. There is therefore nothing to track and no
// teardown here — unlike a node shell, whose pod PodSteer must delete.
func (t *TerminalAPI) StartDebugSession(clusterID, namespace, podName, targetContainer, image string, command []string, cols, rows int) (string, error) {
	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return "", apiError(t.logger, "StartDebugSession", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return "", apiError(t.logger, "StartDebugSession", err)
	}

	// Adding a debug container mutates the pod, so it gets the same
	// synchronous read-only refusal StartSession makes — before any container
	// is added, not after. ManagementService.AddEphemeralContainer checks
	// again; this is the fast path that avoids growing a pod a debugger nobody
	// will be allowed to use.
	if t.management.ReadOnly(id) {
		return "", apiError(t.logger, "StartDebugSession",
			fmt.Errorf("starting debug session: %w", ports.ErrReadOnly))
	}

	if len(command) == 0 {
		command = []string{"sh"}
	}
	spec := domain.DebugContainerSpec{
		Image:           image,
		TargetContainer: targetContainer,
		Command:         command,
		TTY:             true,
		Stdin:           true,
	}

	parent, ok := t.app.runtimeContext()
	if !ok {
		return "", errors.New("application is shutting down")
	}

	// Adding and waiting run on a bounded context of their own, separate from
	// the session's: this call returns once the shell is open, and the shell
	// then lives on the application-lifetime context like every other session.
	prepCtx, cancelPrep := context.WithTimeout(parent, debugPrepTimeout)
	defer cancelPrep()

	containerName, err := t.management.AddEphemeralContainer(prepCtx, id, ns, podName, spec)
	if err != nil {
		return "", apiError(t.logger, "StartDebugSession", err)
	}

	if err := t.management.WaitForEphemeralContainerRunning(prepCtx, id, ns, podName, containerName); err != nil {
		return "", apiError(t.logger, "StartDebugSession", err)
	}

	sessionID, err := t.openExecSession(parent, id, ns, podName, containerName, cols, rows, "debug session")
	if err != nil {
		return "", err
	}

	t.logger.Info("debug session started",
		slog.String("session", sessionID),
		slog.String("pod", podName),
		slog.String("container", containerName),
		slog.String("image", image))

	return sessionID, nil
}

// StartNodeShellSession creates a privileged pod on a node that enters the
// node's host namespaces — the way `kubectl node-shell` and Lens do — and
// attaches to its login shell. It returns the session ID.
//
// The pod is DELETED when this session ends, so the pod and the terminal that
// makes it useful are bound together the way CLAUDE.md's node-shell lifecycle
// requires: nothing privileged is left running on a node once the operator has
// closed the shell. The activeDeadlineSeconds the pod carries is only a
// backstop for the one case this cannot cover — PodSteer crashing.
func (t *TerminalAPI) StartNodeShellSession(clusterID, namespace, nodeName, image string, cols, rows int) (string, error) {
	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return "", apiError(t.logger, "StartNodeShellSession", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return "", apiError(t.logger, "StartNodeShellSession", err)
	}

	// Creating a privileged pod is a write, so it gets the same synchronous
	// read-only refusal — before the pod is created, not after.
	if t.management.ReadOnly(id) {
		return "", apiError(t.logger, "StartNodeShellSession",
			fmt.Errorf("starting node shell: %w", ports.ErrReadOnly))
	}

	parent, ok := t.app.runtimeContext()
	if !ok {
		return "", errors.New("application is shutting down")
	}

	prepCtx, cancelPrep := context.WithTimeout(parent, nodeShellPrepTimeout)
	defer cancelPrep()

	shell, err := t.nodeShells.StartNodeShell(prepCtx, id, ns, nodeName, image)
	if err != nil {
		return "", apiError(t.logger, "StartNodeShellSession", err)
	}

	// The pod is deleted when the attach session ends — see the onExit hook.
	sessionID, err := t.openAttachSession(parent, id, ns, shell.PodName, shell.ContainerName, cols, rows, "node shell session", func() {
		if err := t.nodeShells.StopNodeShell(shell.ID); err != nil {
			t.logger.Error("failed to delete node shell pod",
				slog.String("pod", shell.PodName),
				slog.String("error", err.Error()))
		}
	})
	if err != nil {
		// The session never started, so nothing will delete the pod on exit.
		// Remove it here rather than leak a privileged pod nobody is attached
		// to.
		_ = t.nodeShells.StopNodeShell(shell.ID)
		return "", err
	}

	t.logger.Info("node shell session started",
		slog.String("session", sessionID),
		slog.String("node", nodeName),
		slog.String("pod", shell.PodName),
		slog.String("image", image))

	return sessionID, nil
}

// openExecSession allocates a session, spawns the exec goroutine and returns
// the session ID. Shared by StartDebugSession and, below, by the plumbing that
// makes a debug shell indistinguishable from an ordinary one — the only thing
// that differs is which container name is passed in.
//
// parent is the application-lifetime context; the session runs on a cancelable
// child of it, exactly as StartSession does.
func (t *TerminalAPI) openExecSession(parent context.Context, id domain.ClusterID, ns domain.NamespaceName, podName, containerName string, cols, rows int, label string) (string, error) {
	ctx, cancel := context.WithCancel(parent)

	sessionID := generateTerminalID()
	stdinReader, stdinWriter := io.Pipe()
	sizeQueue := newTerminalSizeQueue()
	sizeQueue.send(ports.TerminalSize{Width: uint16(cols), Height: uint16(rows)})

	session := &terminalSession{
		id:        sessionID,
		cancel:    cancel,
		stdinPipe: stdinWriter,
		sizeQueue: sizeQueue,
	}

	t.mu.Lock()
	t.sessions[sessionID] = session
	t.mu.Unlock()

	stdoutWriter := &terminalOutputWriter{sessionID: sessionID, app: t.app}

	go func() {
		defer func() {
			t.mu.Lock()
			delete(t.sessions, sessionID)
			t.mu.Unlock()

			_ = stdinWriter.Close()
			sizeQueue.close()
		}()

		err := t.management.ExecInPodWithTTY(ctx, id, ns, podName, containerName,
			[]string{"/bin/sh"}, stdinReader, stdoutWriter, stdoutWriter, sizeQueue)

		reason := ""
		if err != nil && !errors.Is(err, context.Canceled) {
			reason = err.Error()
			t.logger.Error(label+" ended with error",
				slog.String("session", sessionID),
				slog.String("error", err.Error()))
		}

		t.app.emit("terminal:exit", TerminalExitEvent{SessionID: sessionID, Reason: reason})
	}()

	return sessionID, nil
}

// openAttachSession is openExecSession's attach twin — it connects to the
// container's OWN running process rather than starting a new one, which is
// what makes a node shell a node shell: the pod's process is the login shell
// in the host's namespaces, and attaching lands the operator on it. onExit, if
// set, runs after the session ends — a node shell uses it to delete its pod.
func (t *TerminalAPI) openAttachSession(parent context.Context, id domain.ClusterID, ns domain.NamespaceName, podName, containerName string, cols, rows int, label string, onExit func()) (string, error) {
	ctx, cancel := context.WithCancel(parent)

	sessionID := generateTerminalID()
	stdinReader, stdinWriter := io.Pipe()
	sizeQueue := newTerminalSizeQueue()
	sizeQueue.send(ports.TerminalSize{Width: uint16(cols), Height: uint16(rows)})

	session := &terminalSession{
		id:        sessionID,
		cancel:    cancel,
		stdinPipe: stdinWriter,
		sizeQueue: sizeQueue,
	}

	t.mu.Lock()
	t.sessions[sessionID] = session
	t.mu.Unlock()

	stdoutWriter := &terminalOutputWriter{sessionID: sessionID, app: t.app}

	go func() {
		defer func() {
			t.mu.Lock()
			delete(t.sessions, sessionID)
			t.mu.Unlock()

			_ = stdinWriter.Close()
			sizeQueue.close()

			if onExit != nil {
				onExit()
			}
		}()

		err := t.management.AttachToPod(ctx, id, ns, podName, containerName,
			stdinReader, stdoutWriter, stdoutWriter, sizeQueue)

		reason := ""
		if err != nil && !errors.Is(err, context.Canceled) {
			reason = err.Error()
			t.logger.Error(label+" ended with error",
				slog.String("session", sessionID),
				slog.String("error", err.Error()))
		}

		t.app.emit("terminal:exit", TerminalExitEvent{SessionID: sessionID, Reason: reason})
	}()

	return sessionID, nil
}

// Write sends data to the terminal's stdin.
//
// This is called by the frontend for every keystroke or paste operation.
// The data may contain raw bytes including escape sequences for special keys.
func (t *TerminalAPI) Write(sessionID, data string) error {
	t.mu.Lock()
	session, ok := t.sessions[sessionID]
	t.mu.Unlock()

	if !ok {
		return errors.New("terminal session not found")
	}

	_, err := session.stdinPipe.Write([]byte(data))
	return err
}

// Resize notifies the terminal session of a window size change.
//
// This triggers the Kubernetes API to send a SIGWINCH to the process
// running in the container, so programs like top and vim redraw correctly.
func (t *TerminalAPI) Resize(sessionID string, cols, rows int) error {
	t.mu.Lock()
	session, ok := t.sessions[sessionID]
	t.mu.Unlock()

	if !ok {
		return errors.New("terminal session not found")
	}

	// The queue decides whether it can still take one. Reading the session
	// out of the map and then sending is inherently a window in which the
	// exec goroutine can finish and close the queue underneath us.
	session.sizeQueue.send(ports.TerminalSize{
		Width:  uint16(cols),
		Height: uint16(rows),
	})

	return nil
}

// StopSession terminates an active terminal session.
func (t *TerminalAPI) StopSession(sessionID string) error {
	t.mu.Lock()
	session, ok := t.sessions[sessionID]
	t.mu.Unlock()

	if !ok {
		return nil // Already stopped, idempotent
	}

	session.cancel()
	// The context is already cancelled, so the exec is tearing down regardless.
	_ = session.stdinPipe.Close()

	t.logger.Info("terminal session stopped", slog.String("session", sessionID))
	return nil
}

// terminalOutputWriter forwards writes to the frontend as terminal:data events.
type terminalOutputWriter struct {
	sessionID string
	app       *App
}

func (w *terminalOutputWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	w.app.emit("terminal:data", TerminalDataEvent{
		SessionID: w.sessionID,
		Data:      string(p),
	})

	return len(p), nil
}
