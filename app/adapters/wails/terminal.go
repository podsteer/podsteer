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
	app        *App
	logger     *slog.Logger

	mu       sync.Mutex
	sessions map[string]*terminalSession
}

// NewTerminalAPI returns a new terminal API.
func NewTerminalAPI(management *application.ManagementService, app *App, logger *slog.Logger) (*TerminalAPI, error) {
	switch {
	case management == nil:
		return nil, errors.New("wails: TerminalAPI requires a ManagementService")
	case app == nil:
		return nil, errors.New("wails: TerminalAPI requires an App")
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &TerminalAPI{
		management: management,
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
