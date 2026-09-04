package wails

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ReadTextFile is the settings import's way in, and the second thing PodSteer
// reads from disk that is not a kubeconfig. Its picker is stubbed for the
// reason every other dialog here is: a native dialog in `go test` waits for
// an operator who is not there.

func TestReadTextFileReturnsTheChosenFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "podsteer-settings.json")
	want := `{"kind":"PodSteerSettings"}`
	if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	api := newTestSystemAPI(t)
	api.chooseTextPath = func(title string) (string, error) {
		if title != "Choose a settings file" {
			t.Fatalf("title = %q, want the caller's", title)
		}
		return path, nil
	}

	got, err := api.ReadTextFile("Choose a settings file")
	if err != nil {
		t.Fatalf("ReadTextFile() error = %v", err)
	}
	if got != want {
		t.Fatalf("ReadTextFile() = %q, want %q", got, want)
	}
}

func TestReadTextFileCancelledIsNotAnError(t *testing.T) {
	t.Parallel()

	api := newTestSystemAPI(t)
	api.chooseTextPath = func(string) (string, error) { return "", nil }

	got, err := api.ReadTextFile("Choose a settings file")
	if err != nil {
		t.Fatalf("ReadTextFile() error = %v, want nil — cancelling is not a failure", err)
	}
	if got != "" {
		t.Fatalf("ReadTextFile() = %q, want empty", got)
	}
}

func TestReadTextFileDefaultsTheTitle(t *testing.T) {
	t.Parallel()

	api := newTestSystemAPI(t)
	api.chooseTextPath = func(title string) (string, error) {
		if title == "" {
			t.Fatal("the dialog was opened with no title")
		}
		return "", nil
	}

	if _, err := api.ReadTextFile("   "); err != nil {
		t.Fatalf("ReadTextFile() error = %v", err)
	}
}

func TestReadTextFileReportsADialogFailure(t *testing.T) {
	t.Parallel()

	api := newTestSystemAPI(t)
	api.chooseTextPath = func(string) (string, error) { return "", errors.New("no display server") }

	if _, err := api.ReadTextFile("Import"); err == nil {
		t.Fatal("ReadTextFile() error = nil, want the dialog's failure surfaced")
	}
}

func TestReadTextFileRefusesAFileTooLargeToParse(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "huge.json")
	if err := os.WriteFile(path, make([]byte, maxTextFileBytes+1), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	api := newTestSystemAPI(t)
	api.chooseTextPath = func(string) (string, error) { return path, nil }

	_, err := api.ReadTextFile("Import")
	if err == nil {
		t.Fatal("ReadTextFile() error = nil, want a refusal past the cap")
	}
	// Classified as invalid input, not an internal failure: the operator
	// picked the file and can pick another.
	if !strings.HasPrefix(err.Error(), "["+string(CodeInvalidInput)+"]") {
		t.Fatalf("ReadTextFile() error = %q, want the invalid_input code", err)
	}
}

// An empty file is refused rather than returned, because "" is already what a
// cancelled dialog means — a caller cannot tell the two apart.
func TestReadTextFileRefusesAnEmptyFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	api := newTestSystemAPI(t)
	api.chooseTextPath = func(string) (string, error) { return path, nil }

	if _, err := api.ReadTextFile("Import"); err == nil {
		t.Fatal("ReadTextFile() error = nil, want an empty file refused")
	}
}

func TestReadTextFileReportsAnUnreadablePath(t *testing.T) {
	t.Parallel()

	api := newTestSystemAPI(t)
	api.chooseTextPath = func(string) (string, error) {
		return filepath.Join(t.TempDir(), "does-not-exist.json"), nil
	}

	if _, err := api.ReadTextFile("Import"); err == nil {
		t.Fatal("ReadTextFile() error = nil, want the read failure surfaced")
	}
}

// The save dialog's filter follows the suggested name's extension. A fixed
// CSV filter would have made the settings export arrive as `.json.csv` on
// macOS, where the panel enforces the filter as the extension.
func TestSaveDialogFollowsTheSuggestedExtension(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		suggested  string
		wantTitle  string
		wantFilter string
	}{
		{"csv keeps its own dialog", "cluster-pods-all-20260101-120000.csv", "Export CSV", "*.csv"},
		{"settings file offers json", "podsteer-settings-20260101-120000.json", "Export", "*.json"},
		{"a log download offers log", "web-nginx-20260101-120000.log", "Download logs", "*.log"},
		{"an upper-case extension is the same extension", "EXPORT.JSON", "Export", "*.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			title, filters := saveDialogFor(tt.suggested)
			if title != tt.wantTitle {
				t.Fatalf("title = %q, want %q", title, tt.wantTitle)
			}
			if len(filters) == 0 || filters[0].Pattern != tt.wantFilter {
				t.Fatalf("filters = %#v, want the first to be %q", filters, tt.wantFilter)
			}
		})
	}
}

// Anything unrecognised is offered unrestricted rather than forced into one of
// the known extensions: the operator named the file.
func TestSaveDialogDoesNotGuessAnUnknownExtension(t *testing.T) {
	t.Parallel()

	title, filters := saveDialogFor("notes.txt")
	if title != "Save" {
		t.Fatalf("title = %q, want the neutral one", title)
	}
	if filters != nil {
		t.Fatalf("filters = %#v, want none", filters)
	}
}
