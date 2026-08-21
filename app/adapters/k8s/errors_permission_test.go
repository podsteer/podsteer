package k8s

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A kubeconfig that cannot be read has to say so, and say WHERE. macOS gates
// Documents, Desktop, Downloads and network volumes for every process, so a
// ~/.kube symlinked into one of them is refused for a path that does not look
// protected — and the stock error names the symlink, which explains nothing.
func TestKubeconfigPermissionHint(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("running as root: mode 0000 is still readable, so there is nothing to detect")
	}

	dir := t.TempDir()

	readable := filepath.Join(dir, "readable.yaml")
	if err := os.WriteFile(readable, []byte("apiVersion: v1\n"), 0o600); err != nil {
		t.Fatalf("writing readable kubeconfig: %v", err)
	}

	unreadable := filepath.Join(dir, "unreadable.yaml")
	if err := os.WriteFile(unreadable, []byte("apiVersion: v1\n"), 0o000); err != nil {
		t.Fatalf("writing unreadable kubeconfig: %v", err)
	}

	link := filepath.Join(dir, "link.yaml")
	if err := os.Symlink(unreadable, link); err != nil {
		t.Fatalf("linking to the unreadable kubeconfig: %v", err)
	}

	tests := []struct {
		name     string
		path     string
		wantHint bool
		contains []string
	}{
		{
			name: "no path configured is not a permission problem",
			path: "",
		},
		{
			name: "a readable file produces no hint",
			path: readable,
		},
		{
			name: "a missing file is someone else's error to report",
			path: filepath.Join(dir, "absent.yaml"),
		},
		{
			name:     "an unreadable file names itself",
			path:     unreadable,
			wantHint: true,
			contains: []string{unreadable, "Privacy & Security"},
		},
		{
			// The case that matters: the operator sees a dialog about one
			// folder and an error about a path in a different one.
			name:     "a symlink names both itself and its target",
			path:     link,
			wantHint: true,
			contains: []string{link, unreadable, "points to"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			hint := kubeconfigPermissionHint(test.path)

			if !test.wantHint {
				if hint != "" {
					t.Fatalf("kubeconfigPermissionHint(%q) = %q, want no hint", test.path, hint)
				}
				return
			}

			if hint == "" {
				t.Fatalf("kubeconfigPermissionHint(%q) returned no hint, want one", test.path)
			}
			for _, want := range test.contains {
				if !strings.Contains(hint, want) {
					t.Errorf("hint %q does not mention %q", hint, want)
				}
			}
		})
	}
}
