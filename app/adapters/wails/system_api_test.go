package wails

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// SaveTextFile is the one place PodSteer writes a file wherever the operator
// chose, so its write path is exercised directly rather than through the
// real dialog — chooseSavePath is stubbed instead of popping one, which
// would hang the test run waiting for someone who is not there.

func newTestSystemAPI(t *testing.T) *SystemAPI {
	t.Helper()

	api, err := NewSystemAPI("PodSteer", "test", NewApp(slog.Default(), 0), slog.Default())
	if err != nil {
		t.Fatalf("NewSystemAPI() error = %v", err)
	}
	return api
}

func TestSaveTextFileWritesTheChosenPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	want := filepath.Join(dir, "podsteer-pods-all-20260101-120000.csv")

	api := newTestSystemAPI(t)
	api.chooseSavePath = func(suggestedName string) (string, error) {
		if suggestedName != "podsteer-pods-all-20260101-120000.csv" {
			t.Fatalf("suggestedName = %q, want the filename SaveTextFile was called with", suggestedName)
		}
		return want, nil
	}

	const content = "name,namespace\r\nweb-1,default\r\n"

	got, err := api.SaveTextFile("podsteer-pods-all-20260101-120000.csv", content)
	if err != nil {
		t.Fatalf("SaveTextFile() error = %v", err)
	}
	if got != want {
		t.Fatalf("SaveTextFile() = %q, want %q", got, want)
	}

	written, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", want, err)
	}
	if string(written) != content {
		t.Fatalf("file content = %q, want %q", written, content)
	}

	// CI compares generated bindings with core.fileMode=false, so mode bits
	// are not part of that contract — but 0600 is still what this method
	// promises its own caller, and Windows has no POSIX permission bits to
	// assert on at all.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(want)
		if err != nil {
			t.Fatalf("Stat(%q) error = %v", want, err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("file mode = %v, want 0600", perm)
		}
	}
}

func TestSaveTextFileCancelledDialogIsNotAnError(t *testing.T) {
	t.Parallel()

	api := newTestSystemAPI(t)
	api.chooseSavePath = func(string) (string, error) {
		// The dialog's own convention for "the operator dismissed it".
		return "", nil
	}

	got, err := api.SaveTextFile("export.csv", "a,b\r\n1,2\r\n")
	if err != nil {
		t.Fatalf("SaveTextFile() error = %v, want nil — cancelling is not a failure", err)
	}
	if got != "" {
		t.Fatalf("SaveTextFile() = %q, want empty", got)
	}
}

func TestSaveTextFileRefusesAnEmptySuggestedName(t *testing.T) {
	t.Parallel()

	api := newTestSystemAPI(t)
	api.chooseSavePath = func(string) (string, error) {
		t.Fatal("chooseSavePath must not be reached with no suggested name")
		return "", nil
	}

	if _, err := api.SaveTextFile("", "a,b\r\n"); err == nil {
		t.Fatal("SaveTextFile(\"\", ...) error = nil, want a refusal")
	}

	if _, err := api.SaveTextFile("   ", "a,b\r\n"); err == nil {
		t.Fatal("SaveTextFile(\"   \", ...) error = nil, want a refusal")
	}
}

func TestSaveTextFileReportsADialogFailure(t *testing.T) {
	t.Parallel()

	api := newTestSystemAPI(t)
	wantCause := errors.New("no display server")
	api.chooseSavePath = func(string) (string, error) {
		return "", wantCause
	}

	if _, err := api.SaveTextFile("export.csv", "a,b\r\n"); err == nil {
		t.Fatal("SaveTextFile() error = nil, want the dialog's failure surfaced")
	}
}

func TestSaveTextFileReportsAnUnwritablePath(t *testing.T) {
	t.Parallel()

	api := newTestSystemAPI(t)
	// A directory nothing has created is not a path os.WriteFile can open.
	api.chooseSavePath = func(string) (string, error) {
		return filepath.Join(t.TempDir(), "missing", "export.csv"), nil
	}

	if _, err := api.SaveTextFile("export.csv", "a,b\r\n"); err == nil {
		t.Fatal("SaveTextFile() error = nil, want the write failure surfaced")
	}
}
