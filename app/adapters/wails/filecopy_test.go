package wails

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/application"
)

func newTestFileCopyAPI(t *testing.T, registry *application.Registry) *FileCopyAPI {
	t.Helper()

	management, err := application.NewManagementService(application.ManagementServiceDeps{
		Management: stubManagementPort{},
		Registry:   registry,
	})
	if err != nil {
		t.Fatalf("NewManagementService() error = %v", err)
	}

	api, err := NewFileCopyAPI(management, NewApp(nil, 0), nil)
	if err != nil {
		t.Fatalf("NewFileCopyAPI() error = %v", err)
	}
	return api
}

// TestStartUploadRefusesOnReadOnlyCluster pins the fast path: an upload
// into a read-only cluster is refused synchronously, before a goroutine is
// started or a transfer registered — mirroring TerminalAPI.StartSession.
func TestStartUploadRefusesOnReadOnlyCluster(t *testing.T) {
	t.Parallel()

	registry := application.NewRegistry()
	registry.SetReadOnly("prod", true)
	api := newTestFileCopyAPI(t, registry)

	source := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(source, []byte("a: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	transferID, err := api.StartUpload("prod", "default", "web-0", "app", source, "/app")
	if err == nil {
		t.Fatal("StartUpload() error = nil, want a read-only refusal")
	}
	if !strings.Contains(err.Error(), "read_only") {
		t.Fatalf("StartUpload() error = %q, want it classified read_only", err)
	}
	if transferID != "" {
		t.Fatalf("transfer id = %q, want empty on refusal", transferID)
	}

	api.mu.Lock()
	live := len(api.transfers)
	api.mu.Unlock()
	if live != 0 {
		t.Fatalf("live transfers = %d, want 0 — a refused start must never register one", live)
	}
}

// TestStartDownloadIsAllowedOnReadOnlyCluster is the other half: a download
// is a read, so it gets past the guard — proven, as the terminal tests
// prove it, by failing further on at the absent Wails runtime rather than
// at the guard.
func TestStartDownloadIsAllowedOnReadOnlyCluster(t *testing.T) {
	t.Parallel()

	registry := application.NewRegistry()
	registry.SetReadOnly("prod", true)
	api := newTestFileCopyAPI(t, registry)

	_, err := api.StartDownload("prod", "default", "web-0", "app", "/etc/hosts", t.TempDir())
	if err == nil {
		t.Fatal("StartDownload() error = nil, want a failure reaching for the (absent) Wails runtime")
	}
	if strings.Contains(err.Error(), "read_only") {
		t.Fatalf("StartDownload() error = %q, a download must not be refused as read-only", err)
	}
	if !strings.Contains(err.Error(), "shutting down") {
		t.Fatalf("StartDownload() error = %q, want the runtime failure — proof the guard let it through", err)
	}
}

// TestStartDownloadRefusesBadInputBeforeAnyWork: the container's root, and
// a local directory that does not exist, are both invalid input and never
// reach the runtime.
func TestStartDownloadRefusesBadInputBeforeAnyWork(t *testing.T) {
	t.Parallel()

	api := newTestFileCopyAPI(t, application.NewRegistry())

	if _, err := api.StartDownload("dev", "default", "web-0", "app", "/", t.TempDir()); err == nil || !strings.Contains(err.Error(), "invalid_input") {
		t.Fatalf("StartDownload(\"/\") error = %v, want invalid_input", err)
	}

	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := api.StartDownload("dev", "default", "web-0", "app", "/etc/hosts", missing); err == nil || !strings.Contains(err.Error(), "invalid_input") {
		t.Fatalf("StartDownload() into a missing directory error = %v, want invalid_input", err)
	}

	file := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := api.StartDownload("dev", "default", "web-0", "app", "/etc/hosts", file); err == nil || !strings.Contains(err.Error(), "invalid_input") {
		t.Fatalf("StartDownload() into a file error = %v, want invalid_input", err)
	}
}

// TestStartUploadRefusesAMissingLocalPath: nothing to send is invalid
// input, not a transfer that fails a moment later.
func TestStartUploadRefusesAMissingLocalPath(t *testing.T) {
	t.Parallel()

	api := newTestFileCopyAPI(t, application.NewRegistry())

	_, err := api.StartUpload("dev", "default", "web-0", "app", filepath.Join(t.TempDir(), "missing"), "/app")
	if err == nil || !strings.Contains(err.Error(), "invalid_input") {
		t.Fatalf("StartUpload() of a missing path error = %v, want invalid_input", err)
	}
}

// TestCancelIsIdempotent: cancelling something that is not running — or
// already finished — is a no-op, so a double click cannot fail.
func TestCancelIsIdempotent(t *testing.T) {
	t.Parallel()

	api := newTestFileCopyAPI(t, application.NewRegistry())
	if err := api.Cancel("copy_nope"); err != nil {
		t.Fatalf("Cancel() of an unknown transfer error = %v, want nil", err)
	}
}

// TestProgressThrottleEmitsAtMostOncePerInterval pins the shape the
// progress events take: the first report at once, then one per interval
// however many writes land inside it, then the final total on flush.
func TestProgressThrottleEmitsAtMostOncePerInterval(t *testing.T) {
	t.Parallel()

	clock := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }

	var emitted []int64
	throttle := newProgressThrottle(200*time.Millisecond, now, func(bytes int64) {
		emitted = append(emitted, bytes)
	})

	// The first write reports immediately.
	throttle.add(10)
	// Two more inside the interval do not.
	clock = clock.Add(50 * time.Millisecond)
	throttle.add(10)
	clock = clock.Add(50 * time.Millisecond)
	throttle.add(10)
	if len(emitted) != 1 || emitted[0] != 10 {
		t.Fatalf("emitted %v after three writes inside one interval, want [10]", emitted)
	}

	// Past the interval, the next write reports the running total.
	clock = clock.Add(200 * time.Millisecond)
	throttle.add(10)
	if len(emitted) != 2 || emitted[1] != 40 {
		t.Fatalf("emitted %v after the interval, want [10 40]", emitted)
	}

	// Nothing new since: flush is silent.
	throttle.flush()
	if len(emitted) != 2 {
		t.Fatalf("flush with nothing pending emitted: %v", emitted)
	}

	// Something new inside the interval: flush reports it regardless.
	throttle.add(5)
	throttle.flush()
	if len(emitted) != 3 || emitted[2] != 45 {
		t.Fatalf("emitted %v after a final flush, want [10 40 45]", emitted)
	}
}
