package wails

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// progressInterval is how often a running transfer reports its byte count.
//
// Not per chunk: the archive writes in 32 KiB pieces, so a fast transfer
// would post hundreds of events a second across the bridge for a bar that
// repaints at sixty. Five a second reads as continuous and costs nothing.
const progressInterval = 200 * time.Millisecond

// FileCopyProgressEvent is the payload of the "filecopy:progress" event.
type FileCopyProgressEvent struct {
	// TransferID identifies the transfer (returned by StartDownload or
	// StartUpload).
	TransferID string `json:"transferId"`
	// Bytes is the file content moved so far.
	Bytes int64 `json:"bytes"`
}

// FileCopyDoneEvent is the payload of the "filecopy:done" event, sent once
// per transfer however it ended.
type FileCopyDoneEvent struct {
	TransferID string `json:"transferId"`
	// Direction is "download" or "upload".
	Direction string `json:"direction"`
	// Files, Entries and Bytes are what moved — see domain.TransferSummary.
	Files   int   `json:"files"`
	Entries int   `json:"entries"`
	Bytes   int64 `json:"bytes"`
	// DurationMs is wall time from start to finish.
	DurationMs int64 `json:"durationMs"`
	// Notes lists what was deliberately left out, one line each.
	Notes []string `json:"notes"`
	// LocalPath is where a download landed, or what an upload was read
	// from — the thing to show beside "done".
	LocalPath string `json:"localPath"`
	// Cancelled reports the operator's own Cancel, which is not a failure
	// and carries no Error.
	Cancelled bool `json:"cancelled"`
	// Error is the failure in the same "[code] message" envelope a
	// rejected call carries (see errors.go), or "" on success — so the
	// frontend parses it with the same code it uses everywhere else.
	Error string `json:"error"`
}

// FileCopyAPI exposes copying files to and from a container, `kubectl cp`
// from the pod drawer.
//
// A transfer runs in its own goroutine, like a terminal session or a
// port-forward, because it outlives any single call from the frontend:
// StartDownload returns an id at once, progress and completion arrive as
// events, and Cancel ends it early. Every transfer's goroutine has one owner
// (this API), one way to stop (its context) and one exit (the "done" event).
type FileCopyAPI struct {
	management *application.ManagementService
	app        *App
	logger     *slog.Logger

	mu        sync.Mutex
	transfers map[string]context.CancelFunc
}

// NewFileCopyAPI returns the bound file copy API.
func NewFileCopyAPI(management *application.ManagementService, app *App, logger *slog.Logger) (*FileCopyAPI, error) {
	switch {
	case management == nil:
		return nil, errors.New("wails: FileCopyAPI requires a ManagementService")
	case app == nil:
		return nil, errors.New("wails: FileCopyAPI requires an App")
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &FileCopyAPI{
		management: management,
		app:        app,
		logger:     logger.With(slog.String("api", "filecopy")),
		transfers:  make(map[string]context.CancelFunc),
	}, nil
}

func generateTransferID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return "copy_" + hex.EncodeToString(b)
}

// StartDownload copies remotePath out of a container into localDir, a
// directory the operator chose through SystemAPI.ChooseDirectory, and
// returns the transfer id.
//
// The download lands under localDir by the remote entry's own name —
// `/etc/nginx` becomes `<localDir>/nginx` — exactly as `kubectl cp
// pod:/etc/nginx <localDir>/` would. Nothing is written anywhere the
// operator did not pick, and nothing is written by the webview: every byte
// goes through the ArchivePort's checks in Go.
func (f *FileCopyAPI) StartDownload(clusterID, namespace, podName, containerName, remotePath, localDir string) (string, error) {
	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return "", apiError(f.logger, "StartDownload", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return "", apiError(f.logger, "StartDownload", err)
	}

	_, base, err := domain.SplitRemotePath(remotePath)
	if err != nil {
		return "", apiError(f.logger, "StartDownload", err)
	}

	if err := mustBeDirectory(localDir); err != nil {
		return "", apiError(f.logger, "StartDownload", err)
	}

	transferID, ctx, err := f.begin()
	if err != nil {
		return "", err
	}

	go f.run(ctx, transferID, "download", filepath.Join(localDir, base),
		func(ctx context.Context, progress func(int64)) (domain.TransferSummary, error) {
			return f.management.DownloadFromPod(ctx, id, ns, podName, containerName, remotePath, localDir, progress)
		})

	f.logger.Info("file download started",
		slog.String("transfer", transferID),
		slog.String("cluster", clusterID),
		slog.String("pod", podName),
		slog.String("container", containerName),
		slog.String("remotePath", remotePath))

	return transferID, nil
}

// StartUpload copies localPath — a file or directory the operator chose
// through SystemAPI.ChooseFile or ChooseDirectory — into remoteDir inside a
// container, and returns the transfer id.
//
// A write into the cluster's workload, so it gets the same synchronous
// read-only refusal StartSession gives a shell, checked HERE before a
// goroutine is started. ManagementService.UploadToPod checks again; this is
// the fast path that avoids a transfer that would appear to start and
// immediately fail.
func (f *FileCopyAPI) StartUpload(clusterID, namespace, podName, containerName, localPath, remoteDir string) (string, error) {
	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return "", apiError(f.logger, "StartUpload", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return "", apiError(f.logger, "StartUpload", err)
	}

	if f.management.ReadOnly(id) {
		return "", apiError(f.logger, "StartUpload",
			fmt.Errorf("starting upload: %w", ports.ErrReadOnly))
	}

	if _, err := domain.CleanRemoteDir(remoteDir); err != nil {
		return "", apiError(f.logger, "StartUpload", err)
	}

	if err := mustExist(localPath); err != nil {
		return "", apiError(f.logger, "StartUpload", err)
	}

	transferID, ctx, err := f.begin()
	if err != nil {
		return "", err
	}

	go f.run(ctx, transferID, "upload", localPath,
		func(ctx context.Context, progress func(int64)) (domain.TransferSummary, error) {
			return f.management.UploadToPod(ctx, id, ns, podName, containerName, localPath, remoteDir, progress)
		})

	f.logger.Info("file upload started",
		slog.String("transfer", transferID),
		slog.String("cluster", clusterID),
		slog.String("pod", podName),
		slog.String("container", containerName),
		slog.String("remoteDir", remoteDir))

	return transferID, nil
}

// Cancel stops a running transfer. Whatever had already landed stays; the
// "done" event that follows says it was cancelled. Unknown ids are a no-op,
// so cancelling twice is safe.
func (f *FileCopyAPI) Cancel(transferID string) error {
	f.mu.Lock()
	cancel, ok := f.transfers[transferID]
	f.mu.Unlock()

	if !ok {
		return nil
	}

	cancel()
	f.logger.Info("file transfer cancelled", slog.String("transfer", transferID))
	return nil
}

// begin derives a transfer's context from the application's and registers
// its cancel function under a fresh id.
//
// Through the accessor, not the field: app.ctx is guarded by app.mu and set
// to nil on shutdown, so a transfer asked for as the window closes is
// refused rather than handed a nil parent WithCancel would panic on.
func (f *FileCopyAPI) begin() (string, context.Context, error) {
	parent, ok := f.app.runtimeContext()
	if !ok {
		return "", nil, errors.New("application is shutting down")
	}
	ctx, cancel := context.WithCancel(parent)

	transferID := generateTransferID()
	f.mu.Lock()
	f.transfers[transferID] = cancel
	f.mu.Unlock()

	return transferID, ctx, nil
}

// run drives one transfer to its end and reports it.
func (f *FileCopyAPI) run(ctx context.Context, transferID, direction, localPath string, transfer func(context.Context, func(int64)) (domain.TransferSummary, error)) {
	defer func() {
		f.mu.Lock()
		cancel, ok := f.transfers[transferID]
		delete(f.transfers, transferID)
		f.mu.Unlock()
		if ok {
			cancel()
		}
	}()

	started := time.Now()
	throttle := newProgressThrottle(progressInterval, time.Now, func(bytes int64) {
		f.app.emit("filecopy:progress", FileCopyProgressEvent{TransferID: transferID, Bytes: bytes})
	})

	summary, err := transfer(ctx, throttle.add)
	// The final figure, whatever the interval says: the last event before
	// "done" must show the whole transfer.
	throttle.flush()

	event := FileCopyDoneEvent{
		TransferID: transferID,
		Direction:  direction,
		Files:      summary.Files,
		Entries:    summary.Entries,
		Bytes:      summary.Bytes,
		DurationMs: time.Since(started).Milliseconds(),
		Notes:      summary.Notes,
		LocalPath:  localPath,
	}
	if event.Notes == nil {
		event.Notes = []string{}
	}

	switch {
	case err == nil:
	case ctx.Err() != nil:
		// The operator's Cancel, or the window closing. Not a failure, and
		// whatever the pipe said as it was torn down is not a diagnosis.
		event.Cancelled = true
	default:
		event.Error = apiError(f.logger, "FileCopy", err).Error()
	}

	f.app.emit("filecopy:done", event)
}

// mustBeDirectory refuses a download destination that is not an existing
// directory — the dialog only ever returns one, so reaching this from the
// frontend with anything else is a bug worth naming.
func mustBeDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%w: %v", errNoLocalPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", errNoLocalPath, path)
	}
	return nil
}

// mustExist refuses an upload source that is not there.
func mustExist(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("%w: %v", errNoLocalPath, err)
	}
	return nil
}

// progressThrottle coalesces byte-count reports into at most one event per
// interval, plus a final one on flush.
//
// Safe for concurrent use: add is called from whichever goroutine is
// writing files, and flush from the one that owns the transfer.
type progressThrottle struct {
	mu       sync.Mutex
	interval time.Duration
	now      func() time.Time
	emit     func(int64)
	total    int64
	last     time.Time
	dirty    bool
}

func newProgressThrottle(interval time.Duration, now func() time.Time, emit func(int64)) *progressThrottle {
	return &progressThrottle{interval: interval, now: now, emit: emit}
}

// add records n more bytes and emits if the interval has passed. The first
// report always goes out at once, so a transfer shows movement the moment
// the first bytes land rather than an interval later.
func (p *progressThrottle) add(n int64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.total += n
	p.dirty = true

	now := p.now()
	if !p.last.IsZero() && now.Sub(p.last) < p.interval {
		return
	}
	p.last = now
	p.dirty = false
	p.emit(p.total)
}

// flush emits the current total if anything arrived since the last report.
func (p *progressThrottle) flush() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.dirty {
		return
	}
	p.dirty = false
	p.last = p.now()
	p.emit(p.total)
}
