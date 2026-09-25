package k8s

// Tests for the in-app kubeconfig source list.
//
// Weighted towards PRECEDENCE, because that is the half that can be wrong
// without anybody noticing: a source that fails to contribute a cluster is
// reported the same day, while a source that silently SHADOWS a context the
// operator's own kubeconfig already provided is a client quietly talking to
// the wrong cluster — with the right name on the tab.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

// sourcesFor returns an adapter whose settings sources are the given list.
func sourcesFor(t *testing.T, explicit string, sources ...domain.KubeconfigSource) *Adapter {
	t.Helper()
	return New(Config{
		KubeconfigPath: explicit,
		Sources:        func() []domain.KubeconfigSource { return sources },
	}, nil)
}

// writeConfigFile writes a one-context kubeconfig and returns its path.
func writeConfigFile(t *testing.T, dir, name, contextName, server string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(singleContextKubeconfig(contextName, server)), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

// THE RULE THE WHOLE FEATURE RESTS ON. client-go's merge keeps the FIRST
// file's definition of a context name, and sources are appended after the
// environment's entries — so an in-app source can never shadow a context the
// machine's own configuration provided.
func TestAnInAppSourceNeverShadowsTheOperatorsOwnContext(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	explicit := writeConfigFile(t, dir, "config", "shared", "https://10.0.0.1:6443")
	source := writeConfigFile(t, dir, "source.yaml", "shared", "https://10.9.9.9:6443")

	adapter := sourcesFor(t, explicit, domain.KubeconfigSource{Path: source, Kind: domain.SourceFile})

	cluster, err := adapter.Cluster(context.Background(), domain.ClusterID("shared"))
	if err != nil {
		t.Fatalf("Cluster() error = %v", err)
	}
	if got := cluster.Server().String(); got != "https://10.0.0.1:6443" {
		t.Errorf("server = %q, want the explicit kubeconfig's — a source must not shadow it", got)
	}
}

// The environment's directory comes before the in-app sources, for the same
// reason and one more: a packager's or an enterprise's variable beats the UI,
// exactly as PODSTEER_UPDATE_CHECK=false beats the toggle beside it.
func TestTheEnvironmentDirectoryOutranksAnInAppSource(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	explicit := writeConfigFile(t, dir, "config", "only-explicit", "https://10.0.0.1:6443")

	envDir := t.TempDir()
	writeConfigFile(t, envDir, "team.yaml", "shared", "https://10.0.0.2:6443")

	sourceDir := t.TempDir()
	writeConfigFile(t, sourceDir, "team.yaml", "shared", "https://10.9.9.9:6443")

	adapter := New(Config{
		KubeconfigPath: explicit,
		KubeconfigDir:  envDir,
		Sources: func() []domain.KubeconfigSource {
			return []domain.KubeconfigSource{{Path: sourceDir, Kind: domain.SourceDirectory}}
		},
	}, nil)

	cluster, err := adapter.Cluster(context.Background(), domain.ClusterID("shared"))
	if err != nil {
		t.Fatalf("Cluster() error = %v", err)
	}
	if got := cluster.Server().String(); got != "https://10.0.0.2:6443" {
		t.Errorf("server = %q, want the environment directory's", got)
	}
}

func TestASourceFileContributesItsContexts(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	explicit := writeConfigFile(t, dir, "config", "home", "https://10.0.0.1:6443")
	source := writeConfigFile(t, dir, "extra.yaml", "extra", "https://10.0.0.5:6443")

	adapter := sourcesFor(t, explicit, domain.KubeconfigSource{Path: source, Kind: domain.SourceFile})

	clusters, err := adapter.Clusters(context.Background())
	if err != nil {
		t.Fatalf("Clusters() error = %v", err)
	}

	names := make([]string, 0, len(clusters))
	for _, cluster := range clusters {
		names = append(names, cluster.ID().String())
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"extra", "home"}) {
		t.Errorf("clusters = %v, want both the explicit file's and the source's", names)
	}
}

// A folder source is scanned by the SAME function PODSTEER_KUBECONFIG_DIR is,
// so the skip rules cannot drift between the two.
func TestAFolderSourceSkipsExactlyWhatTheEnvironmentDirectorySkips(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	explicit := writeConfigFile(t, dir, "config", "home", "https://10.0.0.1:6443")

	folder := t.TempDir()
	writeConfigFile(t, folder, "good.yaml", "good", "https://10.0.0.7:6443")
	// A dotfile, a subdirectory, and a file that is not a kubeconfig at all —
	// the ordinary contents of a synced folder.
	writeConfigFile(t, folder, ".hidden.yaml", "hidden", "https://10.0.0.8:6443")
	if err := os.Mkdir(filepath.Join(folder, "nested"), 0o700); err != nil {
		t.Fatalf("creating a subdirectory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(folder, "notes.txt"), []byte("not a kubeconfig"), 0o600); err != nil {
		t.Fatalf("writing junk: %v", err)
	}

	adapter := sourcesFor(t, explicit, domain.KubeconfigSource{Path: folder, Kind: domain.SourceDirectory})

	clusters, err := adapter.Clusters(context.Background())
	if err != nil {
		t.Fatalf("Clusters() error = %v", err)
	}

	names := make([]string, 0, len(clusters))
	for _, cluster := range clusters {
		names = append(names, cluster.ID().String())
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"good", "home"}) {
		t.Errorf("clusters = %v, want only the parsable, non-hidden file", names)
	}
}

// A source that does not exist must not take the picker down with it. A
// synced folder is routinely absent for the first minute after a login.
func TestAMissingSourceIsReportedRatherThanBreakingTheRead(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	explicit := writeConfigFile(t, dir, "config", "home", "https://10.0.0.1:6443")
	absent := filepath.Join(dir, "not-here")

	adapter := sourcesFor(t, explicit,
		domain.KubeconfigSource{Path: absent, Kind: domain.SourceDirectory})

	clusters, err := adapter.Clusters(context.Background())
	if err != nil {
		t.Fatalf("Clusters() error = %v", err)
	}
	if len(clusters) != 1 {
		t.Errorf("clusters = %d, want the explicit file's one", len(clusters))
	}

	entries, err := adapter.KubeconfigSources(context.Background())
	if err != nil {
		t.Fatalf("KubeconfigSources() error = %v", err)
	}

	found := false
	for _, entry := range entries {
		if entry.Path != absent {
			continue
		}
		found = true
		if !entry.Missing {
			t.Error("the absent source was not reported as missing")
		}
	}
	if !found {
		t.Error("the absent source was dropped from the report rather than kept")
	}
}

// A context carries the file it came from, so the picker can say which of
// several merged files won it.
func TestAClusterCarriesTheFileItWasReadFrom(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	explicit := writeConfigFile(t, dir, "config", "home", "https://10.0.0.1:6443")
	source := writeConfigFile(t, dir, "extra.yaml", "extra", "https://10.0.0.5:6443")

	adapter := sourcesFor(t, explicit, domain.KubeconfigSource{Path: source, Kind: domain.SourceFile})

	fromSource, err := adapter.Cluster(context.Background(), domain.ClusterID("extra"))
	if err != nil {
		t.Fatalf("Cluster() error = %v", err)
	}
	if got := fromSource.Source().String(); got != source {
		t.Errorf("source = %q, want %q", got, source)
	}

	fromExplicit, err := adapter.Cluster(context.Background(), domain.ClusterID("home"))
	if err != nil {
		t.Fatalf("Cluster() error = %v", err)
	}
	if got := fromExplicit.Source().String(); got != explicit {
		t.Errorf("source = %q, want %q", got, explicit)
	}
}

// A shadowed context reports the file that WON, which is the whole point: an
// operator whose source is being ignored can see why.
func TestAShadowedContextReportsTheFileThatWon(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	explicit := writeConfigFile(t, dir, "config", "shared", "https://10.0.0.1:6443")
	source := writeConfigFile(t, dir, "source.yaml", "shared", "https://10.9.9.9:6443")

	adapter := sourcesFor(t, explicit, domain.KubeconfigSource{Path: source, Kind: domain.SourceFile})

	cluster, err := adapter.Cluster(context.Background(), domain.ClusterID("shared"))
	if err != nil {
		t.Fatalf("Cluster() error = %v", err)
	}
	if got := cluster.Source().String(); got != explicit {
		t.Errorf("source = %q, want the file that won the merge (%q)", got, explicit)
	}
}

// The report's order IS the loading precedence, and each entry says where it
// came from — because only a settings entry may be removed or reordered.
func TestTheReportIsInPrecedenceOrderAndNamesEachOrigin(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	explicit := writeConfigFile(t, dir, "config", "home", "https://10.0.0.1:6443")

	envDir := t.TempDir()
	writeConfigFile(t, envDir, "env.yaml", "from-env", "https://10.0.0.2:6443")

	sourceFile := writeConfigFile(t, dir, "mine.yaml", "mine", "https://10.0.0.3:6443")

	adapter := New(Config{
		KubeconfigPath: explicit,
		KubeconfigDir:  envDir,
		Sources: func() []domain.KubeconfigSource {
			return []domain.KubeconfigSource{{Path: sourceFile, Kind: domain.SourceFile}}
		},
	}, nil)

	entries, err := adapter.KubeconfigSources(context.Background())
	if err != nil {
		t.Fatalf("KubeconfigSources() error = %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}

	want := []struct {
		path   string
		origin domain.KubeconfigOrigin
		kind   domain.SourceKind
	}{
		{explicit, domain.OriginDefault, domain.SourceFile},
		{envDir, domain.OriginEnvironment, domain.SourceDirectory},
		{sourceFile, domain.OriginSettings, domain.SourceFile},
	}
	for i, expected := range want {
		got := entries[i]
		if got.Path != expected.path || got.Origin != expected.origin || got.Kind != expected.kind {
			t.Errorf("entry %d = %+v, want %q/%q/%q", i, got, expected.path, expected.origin, expected.kind)
		}
	}

	// And only the settings entry may be edited: nothing in this application
	// can change an environment variable or the operator's own $KUBECONFIG.
	if entries[0].Origin.IsEditable() || entries[1].Origin.IsEditable() {
		t.Error("an environment-derived entry was reported as editable")
	}
	if !entries[2].Origin.IsEditable() {
		t.Error("the operator's own source was reported as read-only")
	}
}

// A folder entry lists the files it contributed and the contexts they carry,
// which is what lets the pane explain an empty-looking folder.
func TestAFolderEntryReportsTheFilesAndContextsItContributed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	explicit := writeConfigFile(t, dir, "config", "home", "https://10.0.0.1:6443")

	folder := t.TempDir()
	first := writeConfigFile(t, folder, "a.yaml", "alpha", "https://10.0.0.4:6443")
	second := writeConfigFile(t, folder, "b.yaml", "beta", "https://10.0.0.5:6443")

	adapter := sourcesFor(t, explicit, domain.KubeconfigSource{Path: folder, Kind: domain.SourceDirectory})

	entries, err := adapter.KubeconfigSources(context.Background())
	if err != nil {
		t.Fatalf("KubeconfigSources() error = %v", err)
	}

	entry := entries[len(entries)-1]
	if !slices.Equal(entry.Files, []string{first, second}) {
		t.Errorf("files = %v, want both in filename order", entry.Files)
	}
	if !slices.Contains(entry.Contexts, "alpha") || !slices.Contains(entry.Contexts, "beta") {
		t.Errorf("contexts = %v, want both", entry.Contexts)
	}
}

// The local terminal's KUBECONFIG is exactly what the adapter reads, so a
// source added in Settings reaches the operator's own shell without a second,
// hand-built answer to the same question.
func TestKubeconfigFilesIncludesTheSettingsSources(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	explicit := writeConfigFile(t, dir, "config", "home", "https://10.0.0.1:6443")
	source := writeConfigFile(t, dir, "extra.yaml", "extra", "https://10.0.0.5:6443")

	adapter := sourcesFor(t, explicit, domain.KubeconfigSource{Path: source, Kind: domain.SourceFile})

	files := adapter.KubeconfigFiles()
	if !slices.Equal(files, []string{explicit, source}) {
		t.Errorf("KubeconfigFiles() = %v, want the explicit file then the source", files)
	}
}

// An unreadable directory is named once per directory, not once per process:
// the environment's folder and every folder source are scanned by the same
// function, and a single sync.Once would let the first silence the rest.
func TestEachUnreadableDirectoryIsReportedOnItsOwn(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	explicit := writeConfigFile(t, dir, "config", "home", "https://10.0.0.1:6443")

	// A REGULAR FILE where a directory was named. os.ReadDir answers ENOTDIR,
	// which is not fs.ErrNotExist, so it takes the warn path deterministically
	// for any user — unlike a chmod 000 directory, which root reads happily.
	unlistableOne := filepath.Join(dir, "not-a-directory-one")
	unlistableTwo := filepath.Join(dir, "not-a-directory-two")
	for _, path := range []string{unlistableOne, unlistableTwo} {
		if err := os.WriteFile(path, []byte("i am a file"), 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}

	var buf bytes.Buffer
	adapter := New(Config{
		KubeconfigPath: explicit,
		Sources: func() []domain.KubeconfigSource {
			return []domain.KubeconfigSource{
				{Path: unlistableOne, Kind: domain.SourceDirectory},
				{Path: unlistableTwo, Kind: domain.SourceDirectory},
			}
		},
	}, captureLogger(&buf))

	// Twice, so the once-per-directory rule is exercised as well as the
	// once-per-process one it replaced.
	adapter.factory.KubeconfigFiles()
	adapter.factory.KubeconfigFiles()

	// Counted on the `path=` attribute rather than the bare path: the failing
	// path also appears inside the error the OS produced, so a bare count
	// reports two for one warning.
	logged := buf.String()
	if count := strings.Count(logged, "path="+unlistableOne+" "); count != 1 {
		t.Errorf("the first directory was named %d times, want 1:\n%s", count, logged)
	}
	if count := strings.Count(logged, "path="+unlistableTwo+" "); count != 1 {
		t.Errorf("the second directory was named %d times, want 1:\n%s", count, logged)
	}
}
