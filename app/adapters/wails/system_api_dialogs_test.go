package wails

import (
	"errors"
	"testing"
)

// The two pickers behind a file copy, exercised through their seams rather
// than a real dialog, for the same reason SaveTextFile's tests stub
// chooseSavePath: a native dialog in `go test` waits for an operator who is
// not there.

func TestChooseDirectoryReturnsTheChoice(t *testing.T) {
	t.Parallel()

	api := newTestSystemAPI(t)
	api.chooseDirectory = func(title string) (string, error) {
		if title != "Save into" {
			t.Fatalf("title = %q, want the caller's", title)
		}
		return "/Users/me/Downloads", nil
	}

	got, err := api.ChooseDirectory("Save into")
	if err != nil || got != "/Users/me/Downloads" {
		t.Fatalf("ChooseDirectory() = %q, %v", got, err)
	}
}

func TestChooseDirectoryCancelledIsNotAnError(t *testing.T) {
	t.Parallel()

	api := newTestSystemAPI(t)
	api.chooseDirectory = func(string) (string, error) { return "", nil }

	got, err := api.ChooseDirectory("")
	if err != nil {
		t.Fatalf("ChooseDirectory() error = %v, want nil — cancelling is not a failure", err)
	}
	if got != "" {
		t.Fatalf("ChooseDirectory() = %q, want empty", got)
	}
}

func TestChooseDirectoryDefaultsTheTitle(t *testing.T) {
	t.Parallel()

	api := newTestSystemAPI(t)
	api.chooseDirectory = func(title string) (string, error) {
		if title == "" {
			t.Fatal("the dialog was opened with no title")
		}
		return "", nil
	}

	if _, err := api.ChooseDirectory("   "); err != nil {
		t.Fatalf("ChooseDirectory() error = %v", err)
	}
}

func TestChooseFileReportsADialogFailure(t *testing.T) {
	t.Parallel()

	api := newTestSystemAPI(t)
	api.chooseFile = func(string) (string, error) { return "", errors.New("no display server") }

	if _, err := api.ChooseFile("Upload"); err == nil {
		t.Fatal("ChooseFile() error = nil, want the dialog's failure surfaced")
	}
}

func TestChooseFileReturnsTheChoice(t *testing.T) {
	t.Parallel()

	api := newTestSystemAPI(t)
	api.chooseFile = func(string) (string, error) { return "/Users/me/config.yaml", nil }

	got, err := api.ChooseFile("Upload")
	if err != nil || got != "/Users/me/config.yaml" {
		t.Fatalf("ChooseFile() = %q, %v", got, err)
	}
}
