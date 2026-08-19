package wails

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"sync"

	"k8sense/app/application"
	"k8sense/app/domain"
	"k8sense/app/ports"
)

// terminalSizeQueue implements ports.TerminalSizeQueue for resize events.
type terminalSizeQueue struct {
	ch chan ports.TerminalSize
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
		return "", err
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return "", err
	}

	sessionID := generateTerminalID()

	// Create the stdin pipe
	stdinReader, stdinWriter := io.Pipe()

	// Create the terminal size queue with initial size
	sizeQueue := newTerminalSizeQueue()
	sizeQueue.ch <- ports.TerminalSize{
		Width:  uint16(cols),
		Height: uint16(rows),
	}

	// Create cancellable context from the app context
	ctx, cancel := context.WithCancel(t.app.ctx)

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
			close(sizeQueue.ch)
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

	// Non-blocking send (drop if channel is full — next resize will override)
	select {
	case session.sizeQueue.ch <- ports.TerminalSize{
		Width:  uint16(cols),
		Height: uint16(rows),
	}:
	default:
	}

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
