package k8s

// Tests for KubeconfigFiles, the list a LOCAL shell is handed as KUBECONFIG.
//
// It exists so a shell opened beside a cluster tab reads the same clusters
// PodSteer does. That makes two things worth pinning: the list is the SAME
// resolution the client factory uses — explicit file or default chain, plus
// every file the kubeconfig directory contributes, in precedence order — and
// reading it never writes anything, because the file it names holds
// credentials and its current-context belongs to the operator.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestKubeconfigFilesNamesTheExplicitFileFirstThenTheDirectory pins the order
// a shell is given. Precedence is what governs client-go's merge, and the
// explicit file winning a collision is the property CLAUDE.md's kubeconfig
// section promises — a shell whose KUBECONFIG reversed the order would resolve
// a shared context name to a different cluster than the tab beside it.
func TestKubeconfigFilesNamesTheExplicitFileFirstThenTheDirectory(t *testing.T) {
	t.Parallel()

	mainPath := writeFile(t, t.TempDir(), "config", singleContextKubeconfig("main", "https://main:6443"))

	dir := t.TempDir()
	second := writeFile(t, dir, "b.yaml", singleContextKubeconfig("from-b", "https://b:6443"))
	first := writeFile(t, dir, "a.yaml", singleContextKubeconfig("from-a", "https://a:6443"))

	adapter := New(Config{KubeconfigPath: mainPath, KubeconfigDir: dir}, nil)

	got := adapter.KubeconfigFiles()
	want := []string{mainPath, first, second}

	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("KubeconfigFiles() = %v, want %v — explicit file first, directory files after it, sorted by name", got, want)
	}
}

// TestKubeconfigFilesFallsBackToTheDefaultChain covers the ordinary machine,
// which sets neither variable. The answer has to be the same one client-go
// would resolve on its own, since that is what the tab is reading.
func TestKubeconfigFilesFallsBackToTheDefaultChain(t *testing.T) {
	explicit := writeFile(t, t.TempDir(), "config", emptyKubeconfig)
	t.Setenv("KUBECONFIG", explicit)

	adapter := New(Config{}, nil)

	got := adapter.KubeconfigFiles()
	if len(got) == 0 || got[0] != explicit {
		t.Fatalf("KubeconfigFiles() = %v, want it to start with $KUBECONFIG (%s)", got, explicit)
	}
}

// TestKubeconfigFilesSeesAFileDroppedInAfterwards pins the re-scan. The
// directory is read on every call for the same reason the kubeconfig itself
// is, and a shell opened after a file appeared must be given it — otherwise
// the tab and the terminal beside it disagree about which clusters exist.
func TestKubeconfigFilesSeesAFileDroppedInAfterwards(t *testing.T) {
	t.Parallel()

	mainPath := writeFile(t, t.TempDir(), "config", emptyKubeconfig)
	dir := t.TempDir()
	adapter := New(Config{KubeconfigPath: mainPath, KubeconfigDir: dir}, nil)

	if got := adapter.KubeconfigFiles(); len(got) != 1 {
		t.Fatalf("KubeconfigFiles() = %v, want only the explicit file before anything is dropped in", got)
	}

	added := writeFile(t, dir, "later.yaml", singleContextKubeconfig("later", "https://later:6443"))

	got := adapter.KubeconfigFiles()
	if len(got) != 2 || got[1] != added {
		t.Fatalf("KubeconfigFiles() = %v, want the newly dropped file appended without a restart", got)
	}
}

// TestKubeconfigFilesWritesNothing is the rule stated absolutely in CLAUDE.md:
// resolving what a shell should read must never touch the files, and in
// particular must never set current-context — kubectl in another terminal
// would change target because somebody opened a pane here.
//
// Asserted on the bytes and on the directory listing: any write at all is the
// failure, and so is a new file appearing beside the originals.
func TestKubeconfigFilesWritesNothing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mainPath := filepath.Join(root, "config")
	original := []byte(singleContextKubeconfig("alpha", "https://alpha:6443") + "current-context: alpha\n")
	if err := os.WriteFile(mainPath, original, 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	dir := t.TempDir()
	dirFile := writeFile(t, dir, "b.yaml", singleContextKubeconfig("beta", "https://beta:6443"))
	dirOriginal, err := os.ReadFile(dirFile)
	if err != nil {
		t.Fatalf("reading the directory fixture: %v", err)
	}

	adapter := New(Config{KubeconfigPath: mainPath, KubeconfigDir: dir}, nil)
	for i := 0; i < 3; i++ {
		adapter.KubeconfigFiles()
	}

	after, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("re-reading the fixture: %v", err)
	}
	if !bytes.Equal(original, after) {
		t.Fatalf("the kubeconfig was rewritten:\nbefore %q\nafter  %q", original, after)
	}

	dirAfter, err := os.ReadFile(dirFile)
	if err != nil {
		t.Fatalf("re-reading the directory fixture: %v", err)
	}
	if !bytes.Equal(dirOriginal, dirAfter) {
		t.Fatal("a file in the kubeconfig directory was rewritten; the directory is never a write target")
	}

	for _, checked := range []string{root, dir} {
		entries, err := os.ReadDir(checked)
		if err != nil {
			t.Fatalf("listing %s: %v", checked, err)
		}
		if len(entries) != 1 {
			names := make([]string, 0, len(entries))
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
			t.Fatalf("%s holds %v, want only the original file — no copy, no backup, nothing written", checked, names)
		}
	}
}
