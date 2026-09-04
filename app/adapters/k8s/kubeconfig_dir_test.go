package k8s

// Tests for PODSTEER_KUBECONFIG_DIR: a directory of kubeconfig files merged
// into the loading precedence AFTER the explicit (or default) file, the way
// Radar's --kubeconfig-dir and a synced Lens folder both work.
//
// Weighted towards precedence and failure isolation rather than towards the
// happy path of one file, because those are the two ways this feature could
// quietly do the wrong thing: silently letting a directory file shadow a
// context the operator already has, or letting one malformed file in the
// folder take the rest of the picker down with it.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// emptyKubeconfig is a syntactically valid kubeconfig with no contexts —
// the ordinary state of a file this feature has not touched yet.
const emptyKubeconfig = "apiVersion: v1\nkind: Config\n"

// singleContextKubeconfig returns a minimal, self-contained kubeconfig
// defining one context, cluster and user, all derived from contextName so
// that two files built from different names never collide by accident.
func singleContextKubeconfig(contextName, server string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: %s
  name: %s-cluster
contexts:
- context:
    cluster: %s-cluster
    user: %s-user
  name: %s
users:
- name: %s-user
  user:
    token: %s-token
`, server, contextName, contextName, contextName, contextName, contextName, contextName)
}

// captureLogger returns a logger writing text records to buf, for tests that
// need to assert a warning was actually logged rather than only that the
// call which should log it otherwise succeeded.
func captureLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// writeFile writes contents to dir/name and returns the full path.
func writeFile(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

func TestKubeconfigDirMergesValidFilesAndSkipsJunk(t *testing.T) {
	t.Parallel()

	mainPath := writeFile(t, t.TempDir(), "config", emptyKubeconfig)

	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", singleContextKubeconfig("from-a", "https://a:6443"))
	writeFile(t, dir, "b.yaml", singleContextKubeconfig("from-b", "https://b:6443"))
	// A plain scalar, not a mapping: valid YAML on its own, but not a document
	// clientcmd can unmarshal into a Config at all — unlike a syntactically
	// empty mapping (`{}`), which it accepts as a kubeconfig with nothing in
	// it and is therefore not "junk" by the definition this test cares about.
	writeFile(t, dir, "junk.yaml", "this is just a text file, not a kubeconfig\n")

	var logs bytes.Buffer
	adapter := New(Config{KubeconfigPath: mainPath, KubeconfigDir: dir}, captureLogger(&logs))

	clusters, err := adapter.Clusters(context.Background())
	if err != nil {
		t.Fatalf("Clusters() error = %v", err)
	}

	got := make(map[string]bool, len(clusters))
	for _, c := range clusters {
		got[c.ID().String()] = true
	}
	if !got["from-a"] || !got["from-b"] {
		t.Fatalf("Clusters() = %v, want both from-a and from-b", got)
	}
	if len(clusters) != 2 {
		t.Fatalf("Clusters() returned %d clusters, want exactly 2 (junk.yaml must not contribute one)", len(clusters))
	}

	if !strings.Contains(logs.String(), "junk.yaml") {
		t.Error("the unparsable file's path was not logged")
	}
	if !strings.Contains(strings.ToLower(logs.String()), "level=warn") {
		t.Error("the unparsable file was not logged at warn")
	}
}

func TestKubeconfigDirPrecedenceExplicitFileWins(t *testing.T) {
	t.Parallel()

	mainPath := writeFile(t, t.TempDir(), "config",
		singleContextKubeconfig("dupe", "https://explicit:6443"))

	dir := t.TempDir()
	writeFile(t, dir, "z-dupe.yaml", singleContextKubeconfig("dupe", "https://directory:6443"))

	adapter := New(Config{KubeconfigPath: mainPath, KubeconfigDir: dir}, slog.New(slog.DiscardHandler))

	clusters, err := adapter.Clusters(context.Background())
	if err != nil {
		t.Fatalf("Clusters() error = %v", err)
	}
	if len(clusters) != 1 {
		t.Fatalf("Clusters() returned %d clusters, want exactly 1 (one context name, one entry)", len(clusters))
	}

	cluster, err := adapter.Cluster(context.Background(), domain.ClusterID("dupe"))
	if err != nil {
		t.Fatalf("Cluster() error = %v", err)
	}
	if got := cluster.Server().String(); got != "https://explicit:6443" {
		t.Errorf("Cluster(%q).Server() = %q, want the explicit file's server (https://explicit:6443), "+
			"not the directory file's", "dupe", got)
	}
}

func TestKubeconfigDirSymlinkedFileIsIncluded(t *testing.T) {
	t.Parallel()

	realDir := t.TempDir()
	target := writeFile(t, realDir, "real-config.yaml",
		singleContextKubeconfig("linked", "https://linked:6443"))

	dir := t.TempDir()
	link := filepath.Join(dir, "link.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("linking: %v", err)
	}

	mainPath := writeFile(t, t.TempDir(), "config", emptyKubeconfig)
	adapter := New(Config{KubeconfigPath: mainPath, KubeconfigDir: dir}, slog.New(slog.DiscardHandler))

	clusters, err := adapter.Clusters(context.Background())
	if err != nil {
		t.Fatalf("Clusters() error = %v", err)
	}
	if len(clusters) != 1 || clusters[0].ID().String() != "linked" {
		t.Fatalf("Clusters() = %v, want exactly one cluster named 'linked' reached through the symlink", clusters)
	}
}

func TestKubeconfigDirMissingIsNotAnError(t *testing.T) {
	t.Parallel()

	mainPath := writeFile(t, t.TempDir(), "config",
		singleContextKubeconfig("main-only", "https://main:6443"))
	missingDir := filepath.Join(t.TempDir(), "does-not-exist")

	var logs bytes.Buffer
	adapter := New(Config{KubeconfigPath: mainPath, KubeconfigDir: missingDir}, captureLogger(&logs))

	clusters, err := adapter.Clusters(context.Background())
	if err != nil {
		t.Fatalf("Clusters() error = %v, want no error for a directory that does not exist", err)
	}
	if len(clusters) != 1 || clusters[0].ID().String() != "main-only" {
		t.Fatalf("Clusters() = %v, want just the explicit file's one cluster", clusters)
	}
	if strings.Contains(logs.String(), "kubeconfig directory") {
		t.Error("a missing PODSTEER_KUBECONFIG_DIR was logged, want silence — it is the ordinary state of a machine that has not set it up")
	}
}

func TestMergeRefusesAContextThatExistsOnlyInTheDirectory(t *testing.T) {
	t.Parallel()

	mainPath := writeFile(t, t.TempDir(), "config", emptyKubeconfig)

	dir := t.TempDir()
	writeFile(t, dir, "taken.yaml", singleContextKubeconfig("taken", "https://directory:6443"))

	adapter := New(Config{KubeconfigPath: mainPath, KubeconfigDir: dir}, slog.New(slog.DiscardHandler))

	incoming := singleContextKubeconfig("taken", "https://incoming:6443")
	merge, err := adapter.Merge(context.Background(), incoming)
	if !errors.Is(err, ports.ErrKubeconfigConflict) {
		t.Fatalf("Merge() error = %v, want ErrKubeconfigConflict for a name the directory already defines", err)
	}
	if len(merge.Conflicts) != 1 || merge.Conflicts[0] != "taken" {
		t.Errorf("Merge() conflicts = %v, want [taken]", merge.Conflicts)
	}

	after, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("re-reading the explicit kubeconfig: %v", err)
	}
	if string(after) != emptyKubeconfig {
		t.Error("a refused merge still modified the explicit kubeconfig")
	}

	// The directory itself must never be written to: PreviewMerge and Merge
	// both operate through planMerge, and only ever open `path` (the
	// explicit file) for writing.
	dirContents, err := os.ReadFile(filepath.Join(dir, "taken.yaml"))
	if err != nil {
		t.Fatalf("re-reading the directory file: %v", err)
	}
	if string(dirContents) != singleContextKubeconfig("taken", "https://directory:6443") {
		t.Error("the kubeconfig directory file was modified; it must never be written to")
	}
}

func TestKubeconfigDirFilesAreSortedByFilename(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, dir, "zeta.yaml", singleContextKubeconfig("zeta", "https://zeta:6443"))
	writeFile(t, dir, "alpha.yaml", singleContextKubeconfig("alpha", "https://alpha:6443"))
	writeFile(t, dir, "mike.yaml", singleContextKubeconfig("mike", "https://mike:6443"))

	factory := newClientFactory(Config{KubeconfigDir: dir})

	files := factory.kubeconfigDirFiles()
	if len(files) != 3 {
		t.Fatalf("kubeconfigDirFiles() returned %d files, want 3", len(files))
	}

	got := make([]string, len(files))
	for i, path := range files {
		got[i] = filepath.Base(path)
	}
	want := []string{"alpha.yaml", "mike.yaml", "zeta.yaml"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kubeconfigDirFiles() order = %v, want %v (sorted by filename)", got, want)
		}
	}
}
