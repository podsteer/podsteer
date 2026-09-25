package k8s

// Tests for the only write PodSteer makes to a kubeconfig.
//
// Weighted towards what happens to the EXISTING file rather than towards
// whether the new entry lands. Adding a context that fails to appear is a bug
// somebody notices immediately; a merge that quietly drops another cluster's
// credentials, loosens the file's mode, or replaces a symlink with a regular
// file is one they discover much later, somewhere else.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/client-go/tools/clientcmd"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

const incomingConfig = `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://10.0.0.9:6443
  name: added-cluster
contexts:
- context:
    cluster: added-cluster
    user: added-user
  name: added
users:
- name: added-user
  user:
    token: incoming-token
`

const existingConfig = `apiVersion: v1
kind: Config
current-context: kept
clusters:
- cluster:
    server: https://10.0.0.1:6443
  name: kept-cluster
contexts:
- context:
    cluster: kept-cluster
    user: kept-user
  name: kept
users:
- name: kept-user
  user:
    token: kept-token
`

// adapterFor returns an adapter reading and writing the given kubeconfig path.
func adapterFor(t *testing.T, path string) *Adapter {
	t.Helper()
	return New(Config{KubeconfigPath: path}, nil)
}

// writeExisting seeds a kubeconfig and returns its path.
func writeExisting(t *testing.T, dir, contents string) string {
	t.Helper()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("seeding kubeconfig: %v", err)
	}
	return path
}

func TestMergeAddsContextAndKeepsWhatWasThere(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeExisting(t, dir, existingConfig)

	merge, err := adapterFor(t, path).Merge(context.Background(), incomingConfig)
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	if len(merge.Added) != 1 || merge.Added[0] != "added" {
		t.Errorf("Merge() added %v, want [added]", merge.Added)
	}

	after, err := clientcmd.LoadFromFile(path)
	if err != nil {
		t.Fatalf("re-reading kubeconfig: %v", err)
	}
	for _, name := range []string{"kept", "added"} {
		if _, ok := after.Contexts[name]; !ok {
			t.Errorf("context %q is missing after the merge", name)
		}
	}
	// The credentials of what was already there matter more than the new
	// entry: losing them is the failure nobody notices until they try to use
	// the cluster.
	if user, ok := after.AuthInfos["kept-user"]; !ok || user.Token != "kept-token" {
		t.Error("the existing user's credentials did not survive the merge")
	}
	// Adding a cluster is not a request to switch to it.
	if after.CurrentContext != "kept" {
		t.Errorf("current-context = %q, want it left at %q", after.CurrentContext, "kept")
	}
}

func TestMergeRefusesToReplaceAnExistingContext(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeExisting(t, dir, existingConfig)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the seeded kubeconfig: %v", err)
	}

	// The same name, different credentials — the case where merging silently
	// would cost somebody a working context.
	clashing := strings.ReplaceAll(incomingConfig, "added", "kept")

	merge, err := adapterFor(t, path).Merge(context.Background(), clashing)
	if !errors.Is(err, ports.ErrKubeconfigConflict) {
		t.Fatalf("Merge() error = %v, want ErrKubeconfigConflict", err)
	}
	if len(merge.Conflicts) != 1 || merge.Conflicts[0] != "kept" {
		t.Errorf("Merge() reported conflicts %v, want [kept]", merge.Conflicts)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-reading kubeconfig: %v", err)
	}
	if string(after) != string(before) {
		t.Error("a refused merge still modified the kubeconfig")
	}
}

func TestMergeRejectsWhatIsNotAKubeconfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: "   \n  "},
		{name: "not yaml", raw: "{{{ this is not a kubeconfig"},
		{name: "yaml with no contexts", raw: "apiVersion: v1\nkind: Config\nclusters: []\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			path := writeExisting(t, dir, existingConfig)

			if _, err := adapterFor(t, path).Merge(context.Background(), test.raw); !errors.Is(
				err, ports.ErrKubeconfigInvalid,
			) {
				t.Fatalf("Merge(%q) error = %v, want ErrKubeconfigInvalid", test.name, err)
			}

			// Nothing reached the file: the parse happens before it is opened.
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("re-reading kubeconfig: %v", err)
			}
			if string(after) != existingConfig {
				t.Error("a rejected paste still modified the kubeconfig")
			}
		})
	}
}

// A kubeconfig that becomes readable by everyone because a tool rewrote it is
// a real incident, and an easy one to cause with a naive create-and-write.
func TestMergePreservesTheFileMode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeExisting(t, dir, existingConfig)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("setting the mode: %v", err)
	}

	if _, err := adapterFor(t, path).Merge(context.Background(), incomingConfig); err != nil {
		t.Fatalf("Merge() error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode after the merge = %04o, want 0600", got)
	}
}

func TestMergeLeavesABackup(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeExisting(t, dir, existingConfig)

	if _, err := adapterFor(t, path).Merge(context.Background(), incomingConfig); err != nil {
		t.Fatalf("Merge() error = %v", err)
	}

	backup, err := os.ReadFile(path + ".podsteer.bak")
	if err != nil {
		t.Fatalf("reading the backup: %v", err)
	}
	if string(backup) != existingConfig {
		t.Error("the backup does not hold what the kubeconfig held before the merge")
	}
}

// A ~/.kube symlinked into Documents, a dotfile repository or a synced folder
// is common — this machine is set up that way. Writing must follow the link
// rather than replace it with a regular file, which would detach the
// kubeconfig from wherever it is actually kept.
func TestMergeWritesThroughASymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	realDir := filepath.Join(dir, "real")
	if err := os.MkdirAll(realDir, 0o700); err != nil {
		t.Fatalf("making the target directory: %v", err)
	}
	target := writeExisting(t, realDir, existingConfig)

	link := filepath.Join(dir, "config-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("linking: %v", err)
	}

	if _, err := adapterFor(t, link).Merge(context.Background(), incomingConfig); err != nil {
		t.Fatalf("Merge() error = %v", err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was replaced by a regular file")
	}

	after, err := clientcmd.LoadFromFile(target)
	if err != nil {
		t.Fatalf("reading through the link's target: %v", err)
	}
	if _, ok := after.Contexts["added"]; !ok {
		t.Error("the context did not reach the file the link points at")
	}
}

// A machine with no kubeconfig is the most useful moment for this to work.
func TestMergeCreatesAKubeconfigThatDoesNotExistYet(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "config")

	merge, err := adapterFor(t, path).Merge(context.Background(), incomingConfig)
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	if len(merge.Added) != 1 {
		t.Errorf("Merge() added %v, want one context", merge.Added)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the kubeconfig was not created: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("a freshly created kubeconfig has mode %04o, want 0600", got)
	}
}

func TestPreviewMergeReportsWithoutWriting(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeExisting(t, dir, existingConfig)

	merge, err := adapterFor(t, path).PreviewMerge(context.Background(), incomingConfig)
	if err != nil {
		t.Fatalf("PreviewMerge() error = %v", err)
	}
	if len(merge.Added) != 1 || merge.Added[0] != "added" {
		t.Errorf("PreviewMerge() added %v, want [added]", merge.Added)
	}
	// Compared after resolving, because the path reported is resolved — on
	// macOS even a temporary directory sits under /var, which is itself a
	// symlink to /private/var.
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolving the seeded path: %v", err)
	}
	if merge.Path != resolved {
		t.Errorf("PreviewMerge() path = %q, want %q", merge.Path, resolved)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-reading kubeconfig: %v", err)
	}
	if string(after) != existingConfig {
		t.Error("PreviewMerge modified the kubeconfig")
	}
	if _, err := os.Stat(path + ".podsteer.bak"); err == nil {
		t.Error("PreviewMerge left a backup, so it opened the file for writing")
	}
}

// THE LOAD-BEARING TEST FOR THE IN-APP SOURCE LIST.
//
// A source is structurally incapable of being written to: the merge writes
// Precedence[0], and sources are only ever APPENDED after the explicit or
// default chain and after the environment's directory. That is why there is no
// "write here" flag to offer and no way to ask for one — and this asserts the
// property rather than the comment, because the day somebody prepends a source
// to the precedence list for a plausible reason, this is what says the write
// target moved with it.
func TestMergeStillWritesTheExplicitFileWhenSourcesArePresent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeExisting(t, dir, existingConfig)

	sourceDir := t.TempDir()
	sourceFile := filepath.Join(sourceDir, "team.yaml")
	if err := os.WriteFile(sourceFile, []byte(existingConfig), 0o600); err != nil {
		t.Fatalf("seeding a source: %v", err)
	}
	sourceBefore, err := os.ReadFile(sourceFile)
	if err != nil {
		t.Fatalf("reading the source: %v", err)
	}

	adapter := New(Config{
		KubeconfigPath: path,
		Sources: func() []domain.KubeconfigSource {
			return []domain.KubeconfigSource{
				{Path: sourceFile, Kind: domain.SourceFile},
				{Path: sourceDir, Kind: domain.SourceDirectory},
			}
		},
	}, nil)

	merge, err := adapter.Merge(context.Background(), incomingConfig)
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolving the seeded path: %v", err)
	}
	if merge.Path != resolved {
		t.Errorf("Merge() wrote %q, want the explicit kubeconfig %q", merge.Path, resolved)
	}

	written, err := clientcmd.LoadFromFile(path)
	if err != nil {
		t.Fatalf("re-reading the kubeconfig: %v", err)
	}
	if _, added := written.Contexts["added"]; !added {
		t.Error("the new context did not land in the explicit kubeconfig")
	}

	// AND THE SOURCE IS UNTOUCHED, byte for byte. The operator may be syncing
	// it from a password manager or a shared folder, which PodSteer has no
	// business writing to.
	sourceAfter, err := os.ReadFile(sourceFile)
	if err != nil {
		t.Fatalf("re-reading the source: %v", err)
	}
	if string(sourceAfter) != string(sourceBefore) {
		t.Errorf("the merge wrote to a source file:\n%s", sourceAfter)
	}
	if _, err := os.Stat(sourceFile + ".podsteer.bak"); err == nil {
		t.Error("the merge opened a source file for writing")
	}
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		t.Fatalf("listing the source folder: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("the merge left %d entries in the source folder, want the one that was there", len(entries))
	}
}

// A context a SOURCE already defines is refused the same as one the explicit
// file defines.
//
// The existence check reads the MERGED view for exactly this reason: PodSteer
// would otherwise add the name to the explicit file while the source's own
// definition still won the read, which is a confusing way to discover a name
// was never free. It is the rule PODSTEER_KUBECONFIG_DIR already follows, and
// a source has to follow it too or the two behave differently for no reason
// the operator can see.
func TestMergeRefusesAContextASourceAlreadyDefines(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeExisting(t, dir, existingConfig)

	sourceDir := t.TempDir()
	sourceFile := filepath.Join(sourceDir, "team.yaml")
	if err := os.WriteFile(sourceFile, []byte(incomingConfig), 0o600); err != nil {
		t.Fatalf("seeding a source: %v", err)
	}

	adapter := New(Config{
		KubeconfigPath: path,
		Sources: func() []domain.KubeconfigSource {
			return []domain.KubeconfigSource{{Path: sourceFile, Kind: domain.SourceFile}}
		},
	}, nil)

	merge, err := adapter.Merge(context.Background(), incomingConfig)
	if !errors.Is(err, ports.ErrKubeconfigConflict) {
		t.Fatalf("Merge() error = %v, want ErrKubeconfigConflict", err)
	}
	if len(merge.Conflicts) != 1 || merge.Conflicts[0] != "added" {
		t.Errorf("Merge() conflicts = %v, want [added]", merge.Conflicts)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-reading the kubeconfig: %v", err)
	}
	if string(after) != existingConfig {
		t.Error("a refused merge modified the kubeconfig")
	}
}
