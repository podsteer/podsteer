package settings

// Tests for the one file the Go process writes on the operator's behalf.
//
// Weighted towards what happens to a file it did NOT write — a hand edit, a
// document from a newer PodSteer, junk dropped in its place — rather than
// towards the round trip. A setting that fails to save is noticed at once; a
// store that quietly destroys somebody's hand-edited file, or silently drops a
// section a newer build added, is found out much later and somewhere else.
//
// An internal test package, because two of the properties worth asserting are
// not on the exported surface: the instant a sidelined file is stamped with
// (`now`), and the exact bytes of the document.

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// openIn returns a store over a fresh file in dir.
func openIn(t *testing.T, dir string) *Store {
	t.Helper()
	store, err := Open(Options{Path: filepath.Join(dir, FileName), Logger: quietLogger()})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	return store
}

// seed writes contents at the settings path inside dir and returns the path.
func seed(t *testing.T, dir, contents string) string {
	t.Helper()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("seeding settings: %v", err)
	}
	return path
}

func TestFirstRunUsesTheDefaultsAndWritesNothing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := openIn(t, dir)

	settings, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settings.History.Retention.Days != 1 {
		t.Errorf("retention = %d days, want the default of 1", settings.History.Retention.Days)
	}
	if settings.History.SamplingInterval != domain.DefaultSamplingInterval {
		t.Errorf("interval = %v, want the default", settings.History.SamplingInterval)
	}

	// READING MUST NOT CREATE THE FILE. Every launch opens this store, and a
	// launch that changed nothing should leave the configuration directory as
	// it found it — otherwise `podsteer mcp`'s promise could not be kept
	// either.
	if _, err := os.Stat(filepath.Join(dir, FileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("opening the store created the file: %v", err)
	}
}

func TestTheWrittenDocumentIsTwoSpaceJSONWithTheEnvelopeAndANewline(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := openIn(t, dir)

	if _, err := store.Update(context.Background(), func(s *domain.Settings) error {
		s.History.Retention = domain.NewRetention(7)
		return nil
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("reading the settings: %v", err)
	}
	text := string(raw)

	// The kind is deliberately NOT the exported document's "PodSteerSettings",
	// so one dropped in as the other is refused rather than misread.
	if !strings.Contains(text, `"kind": "PodSteerBackendSettings"`) {
		t.Errorf("the document does not carry its kind:\n%s", text)
	}
	if !strings.Contains(text, `"version": 1`) {
		t.Errorf("the document does not carry its version:\n%s", text)
	}
	if !strings.Contains(text, `"_readme"`) {
		t.Errorf("the document has no readme:\n%s", text)
	}
	// Two-space indentation, so the file diffs one setting to a line in git.
	if !strings.Contains(text, "\n  \"kind\"") {
		t.Errorf("the document is not indented with two spaces:\n%s", text)
	}
	if !strings.HasSuffix(text, "}\n") {
		t.Errorf("the document is not newline-terminated:\n%q", text[len(text)-5:])
	}

	// Every section has a key, whether or not this build fills it: adding a
	// setting later must be a field rather than a new top-level key, which is
	// the difference between a change an older build round-trips and one it
	// cannot.
	for _, section := range []string{"history", "kubeconfig", "proxy", "clusters", "windows"} {
		if !strings.Contains(text, `"`+section+`"`) {
			t.Errorf("the document has no %q section:\n%s", section, text)
		}
	}
}

func TestTheFileAndItsDirectoryAreAlwaysPodSteersOwnModes(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "PodSteer")
	store, err := Open(Options{Path: filepath.Join(dir, FileName), Logger: quietLogger()})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	if _, err := store.Update(context.Background(), noChange); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != fileMode {
		t.Errorf("file mode = %v, want %v", got, fileMode)
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat directory: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != dirMode {
		t.Errorf("directory mode = %v, want %v", got, dirMode)
	}
}

// PodSteer's file, not the operator's — so a widened mode is restored rather
// than preserved. This is the deliberate opposite of what writeKubeconfig
// does, and the reason is ownership; see the constants' comment.
func TestAWidenedModeIsRestoredRatherThanPreserved(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := seed(t, dir, validDocument(1))
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("widening the mode: %v", err)
	}

	store := openIn(t, dir)
	if _, err := store.Update(context.Background(), noChange); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != fileMode {
		t.Errorf("file mode = %v, want it restored to %v", got, fileMode)
	}
}

func TestSettingsSurviveAReopen(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	folder := t.TempDir()

	first := openIn(t, dir)
	if _, err := first.Update(context.Background(), func(s *domain.Settings) error {
		s.History.Retention = domain.NewRetention(7)
		s.History.SamplingInterval = 5 * time.Minute
		s.Kubeconfig.Sources = []domain.KubeconfigSource{
			{Path: folder, Kind: domain.SourceDirectory},
		}
		return nil
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	second := openIn(t, dir)
	settings, err := second.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if settings.History.Retention.Days != 7 {
		t.Errorf("retention = %d days, want 7", settings.History.Retention.Days)
	}
	if settings.History.SamplingInterval != 5*time.Minute {
		t.Errorf("interval = %v, want 5m", settings.History.SamplingInterval)
	}
	if len(settings.Kubeconfig.Sources) != 1 || settings.Kubeconfig.Sources[0].Path != folder {
		t.Errorf("sources = %+v, want the one folder", settings.Kubeconfig.Sources)
	}
}

// A reader must never be handed something a later Update can mutate under it.
func TestLoadReturnsACopy(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	folder := t.TempDir()
	store := openIn(t, dir)

	if _, err := store.Update(context.Background(), func(s *domain.Settings) error {
		s.Kubeconfig.Sources = []domain.KubeconfigSource{{Path: folder, Kind: domain.SourceFile}}
		return nil
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	settings, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	settings.Kubeconfig.Sources[0].Path = "/tampered"
	settings.Clusters["injected"] = domain.ClusterSettings{}

	again, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if again.Kubeconfig.Sources[0].Path != folder {
		t.Errorf("a caller mutated the stored sources: %q", again.Kubeconfig.Sources[0].Path)
	}
	if _, injected := again.Clusters["injected"]; injected {
		t.Error("a caller mutated the stored per-cluster map")
	}
}

// An older build must not erase a newer one's section.
//
// Run v0.4 once, let it add a section this build has never heard of, then open
// this one: the next save has to write that section back out unchanged. This
// is the only protection against an ADDED section; a field that MOVED between
// sections is what the from-the-future refusal below is for.
func TestAnUnknownTopLevelSectionSurvivesARewrite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	seed(t, dir, `{
  "kind": "PodSteerBackendSettings",
  "version": 1,
  "history": {"retentionDays": 2, "samplingIntervalSeconds": 60},
  "terminal": {"shell": "/bin/fish", "columns": [1, 2, 3]}
}
`)

	store := openIn(t, dir)
	if _, err := store.Update(context.Background(), func(s *domain.Settings) error {
		s.History.Retention = domain.NewRetention(5)
		return nil
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("reading the settings: %v", err)
	}
	text := string(raw)

	if !strings.Contains(text, `"terminal"`) {
		t.Fatalf("the unknown section was erased:\n%s", text)
	}
	if !strings.Contains(text, `"/bin/fish"`) {
		t.Errorf("the unknown section lost its contents:\n%s", text)
	}
	if !strings.Contains(text, `"retentionDays": 5`) {
		t.Errorf("the change this build made did not land:\n%s", text)
	}
}

func TestAFileFromANewerPodSteerIsReadAndNeverSavedOver(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := seed(t, dir, `{
  "kind": "PodSteerBackendSettings",
  "version": 99,
  "history": {"retentionDays": 3, "samplingIntervalSeconds": 60}
}
`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the seed: %v", err)
	}

	store := openIn(t, dir)

	// READ for what is understood: the operator's retention still applies, so
	// a newer file does not silently start recording a day of history they
	// turned down.
	settings, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settings.History.Retention.Days != 3 {
		t.Errorf("retention = %d days, want the 3 the file carries", settings.History.Retention.Days)
	}

	state := store.State()
	if !state.FromFuture || state.Version != 99 {
		t.Errorf("state = %+v, want FromFuture at version 99", state)
	}
	if state.IsWritable() {
		t.Error("a file from the future was reported as writable")
	}

	// AND NEVER WRITTEN. Preserving unknown sections protects against a newer
	// build ADDING one; it cannot protect against a field having MOVED, which
	// this build would read into the old section and write back there.
	// Refusing is the only outcome that cannot lose anything.
	_, err = store.Update(context.Background(), func(s *domain.Settings) error {
		s.History.Retention = domain.NewRetention(30)
		return nil
	})
	if !errors.Is(err, ports.ErrSettingsFromFuture) {
		t.Errorf("Update() error = %v, want ErrSettingsFromFuture", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-reading the file: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("the file was modified:\n%s", after)
	}
}

func TestAMalformedFileFallsBackToTheDefaultsWithoutRefusingStartup(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"not JSON at all":     "this is not json {{{",
		"an unrelated kind":   `{"kind": "PodSteerSettings", "version": 1, "preferences": {}}`,
		"no kind of its own":  `{"version": 1, "history": {"retentionDays": 9}}`,
		"a JSON array":        `[1, 2, 3]`,
		"an empty JSON value": `""`,
	}

	for name, contents := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			seed(t, dir, contents)

			store := openIn(t, dir)
			settings, err := store.Load(context.Background())
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if settings.History.Retention.Days != 1 {
				t.Errorf("retention = %d days, want the default", settings.History.Retention.Days)
			}
			if !store.State().Unreadable {
				t.Error("state does not report the file as unreadable")
			}
		})
	}
}

// A hand edit is set aside, never overwritten.
//
// Whatever somebody meant by what they typed, PodSteer does not get to destroy
// the evidence of it — so the first save renames the file rather than
// replacing it, and the name carries the instant so a second bad edit does not
// overwrite the first one that was set aside.
func TestAnUnreadableFileIsSetAsideBeforeTheFirstSave(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	original := "operator wrote this by hand and got it wrong"
	path := seed(t, dir, original)

	store := openIn(t, dir)
	store.now = func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

	if _, err := store.Update(context.Background(), noChange); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	aside := path + ".invalid-1700000000"
	kept, err := os.ReadFile(aside)
	if err != nil {
		t.Fatalf("the unreadable file was not set aside: %v", err)
	}
	if string(kept) != original {
		t.Errorf("the sidelined file was altered: %q", kept)
	}

	// And the new file is a valid document at this path.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the new settings: %v", err)
	}
	if !strings.Contains(string(raw), Kind) {
		t.Errorf("the replacement is not a settings document:\n%s", raw)
	}
}

func TestTheFileIsSetAsideOnlyOnce(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := seed(t, dir, "junk")

	store := openIn(t, dir)
	store.now = func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

	for range 3 {
		if _, err := store.Update(context.Background(), noChange); err != nil {
			t.Fatalf("Update() error = %v", err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	sidelined := 0
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".invalid-") {
			sidelined++
		}
	}
	if sidelined != 1 {
		t.Errorf("sidelined %d files, want exactly 1", sidelined)
	}
	// The second save must not have set the now-valid file aside again.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the settings file is missing after three saves: %v", err)
	}
}

// A known field with an unusable value falls back to its default and is
// counted; the rest of the file is still used.
func TestAnInvalidValueFallsBackToItsDefaultAndIsCounted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	seed(t, dir, `{
  "kind": "PodSteerBackendSettings",
  "version": 1,
  "history": {"retentionDays": 5000, "samplingIntervalSeconds": 1},
  "kubeconfig": {"sources": [{"path": "relative/not/absolute", "kind": "file"}]},
  "proxy": {"mode": "somehow", "url": ""}
}
`)

	store := openIn(t, dir)
	settings, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if settings.History.Retention.Days != domain.MaxRetentionDays {
		t.Errorf("retention = %d days, want the clamp at %d",
			settings.History.Retention.Days, domain.MaxRetentionDays)
	}
	if settings.History.SamplingInterval != domain.MinSamplingInterval {
		t.Errorf("interval = %v, want the floor", settings.History.SamplingInterval)
	}
	if len(settings.Kubeconfig.Sources) != 0 {
		t.Errorf("sources = %+v, want the relative path dropped", settings.Kubeconfig.Sources)
	}
	if settings.Proxy.Mode != domain.ProxyFromEnvironment {
		t.Errorf("proxy mode = %q, want the default", settings.Proxy.Mode)
	}

	if got := store.State().Repaired; got != 4 {
		t.Errorf("repaired = %d, want 4 (retention, interval, one source, the proxy)", got)
	}
	// The file is NOT repaired in place — nothing is written until something
	// asks for a write.
	if store.State().Unreadable {
		t.Error("a parseable file with bad values was reported unreadable")
	}
}

// A validation failure leaves the stored value and the file untouched.
func TestAnUpdateThatFailsValidationChangesNothing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := openIn(t, dir)

	if _, err := store.Update(context.Background(), func(s *domain.Settings) error {
		s.History.Retention = domain.NewRetention(7)
		return nil
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	_, err := store.Update(context.Background(), func(s *domain.Settings) error {
		s.Proxy = domain.ProxySettings{
			Mode: domain.ProxyManual,
			URL:  "http://someone:hunter2@proxy.internal:3128",
		}
		return nil
	})
	if !errors.Is(err, domain.ErrSettingsProxyCredential) {
		t.Fatalf("Update() error = %v, want ErrSettingsProxyCredential", err)
	}

	settings, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settings.Proxy.Mode != domain.ProxyFromEnvironment {
		t.Errorf("proxy mode = %q, want it unchanged", settings.Proxy.Mode)
	}

	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("reading the settings: %v", err)
	}
	if strings.Contains(string(raw), "hunter2") {
		t.Fatalf("a credential reached the settings file:\n%s", raw)
	}
}

// An error from the mutation is the caller's, and nothing is written.
func TestAMutationThatReturnsAnErrorWritesNothing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := openIn(t, dir)

	sentinel := errors.New("the caller changed its mind")
	if _, err := store.Update(context.Background(), func(*domain.Settings) error {
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("Update() error = %v, want the caller's own error", err)
	}

	if _, err := os.Stat(filepath.Join(dir, FileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a failed mutation created the file: %v", err)
	}
}

// --- read-only, which is what `podsteer mcp` promises ----------------------

func TestAReadOnlyStoreRefusesEveryWriteAndTouchesNothing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := Open(Options{
		Path:     filepath.Join(dir, FileName),
		ReadOnly: true,
		Logger:   quietLogger(),
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	if store.State().IsWritable() {
		t.Error("a read-only store reported itself writable")
	}

	_, err = store.Update(context.Background(), func(s *domain.Settings) error {
		s.History.Retention = domain.NewRetention(30)
		return nil
	})
	if !errors.Is(err, ports.ErrSettingsReadOnly) {
		t.Errorf("Update() error = %v, want ErrSettingsReadOnly", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("a read-only store left %d entries behind", len(entries))
	}
}

// A machine with no configuration directory still runs, on the defaults.
func TestAStoreWithNowhereToWriteServesTheDefaultsAndRefuses(t *testing.T) {
	t.Parallel()

	store, err := Open(Options{Logger: quietLogger()})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	settings, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settings.History.Retention.Days != 1 {
		t.Errorf("retention = %d days, want the default", settings.History.Retention.Days)
	}
	if _, err := store.Update(context.Background(), noChange); !errors.Is(err, ports.ErrSettingsReadOnly) {
		t.Errorf("Update() error = %v, want ErrSettingsReadOnly", err)
	}
}

// --- adoption --------------------------------------------------------------

// v0.2.0 shipped `history.json`, so its two settings are on real machines and
// an operator who turned recording off must not find it back on after an
// upgrade.
func TestThePreviousHistoryJSONIsAdoptedOnceAndThenRemoved(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	legacy := filepath.Join(dir, "history.json")
	if err := os.WriteFile(legacy, []byte(`{"retentionDays":0,"intervalSeconds":300}`), 0o600); err != nil {
		t.Fatalf("seeding the previous file: %v", err)
	}

	store, err := Open(Options{
		Path:      filepath.Join(dir, FileName),
		AdoptFrom: legacy,
		Logger:    quietLogger(),
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	settings, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// Zero is a real choice — "record nothing" — and adopting it as the
	// default of one day would start recording on a machine that had said no.
	if settings.History.Retention.Days != 0 {
		t.Errorf("retention = %d days, want the 0 the previous file carried", settings.History.Retention.Days)
	}
	if settings.History.SamplingInterval != 5*time.Minute {
		t.Errorf("interval = %v, want the 5m the previous file carried", settings.History.SamplingInterval)
	}

	if _, err := os.Stat(filepath.Join(dir, FileName)); err != nil {
		t.Errorf("adoption did not write the new file: %v", err)
	}
	// ONLY AFTER the new file is safely written.
	if _, err := os.Stat(legacy); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the previous file was not removed: %v", err)
	}
}

// Adoption is a first-run act. A settings file that already exists is the
// answer, whatever a stray `history.json` beside it says.
func TestAnExistingSettingsFileIsNotOverwrittenByAdoption(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	seed(t, dir, validDocument(1))

	legacy := filepath.Join(dir, "history.json")
	if err := os.WriteFile(legacy, []byte(`{"retentionDays":30,"intervalSeconds":600}`), 0o600); err != nil {
		t.Fatalf("seeding the previous file: %v", err)
	}

	store, err := Open(Options{
		Path:      filepath.Join(dir, FileName),
		AdoptFrom: legacy,
		Logger:    quietLogger(),
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	settings, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settings.History.Retention.Days != 2 {
		t.Errorf("retention = %d days, want the 2 the settings file carries", settings.History.Retention.Days)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Errorf("a file adoption did not read was removed anyway: %v", err)
	}
}

// A read-only store adopts in memory and removes nothing.
func TestAReadOnlyStoreAdoptsWithoutWritingOrRemoving(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	legacy := filepath.Join(dir, "history.json")
	if err := os.WriteFile(legacy, []byte(`{"retentionDays":0,"intervalSeconds":60}`), 0o600); err != nil {
		t.Fatalf("seeding the previous file: %v", err)
	}

	store, err := Open(Options{
		Path:      filepath.Join(dir, FileName),
		ReadOnly:  true,
		AdoptFrom: legacy,
		Logger:    quietLogger(),
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	settings, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settings.History.Retention.Days != 0 {
		t.Errorf("retention = %d days, want the previous file's 0", settings.History.Retention.Days)
	}

	if _, err := os.Stat(legacy); err != nil {
		t.Errorf("a read-only run removed the previous file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a read-only run wrote the settings file: %v", err)
	}
}

// --- helpers ---------------------------------------------------------------

// noChange is an Update that changes nothing, for tests about the WRITE rather
// than about what was written.
func noChange(*domain.Settings) error { return nil }

// validDocument returns a well-formed settings file at the given version.
func validDocument(version int) string {
	return `{
  "kind": "PodSteerBackendSettings",
  "version": ` + strconv.Itoa(version) + `,
  "history": {"retentionDays": 2, "samplingIntervalSeconds": 60}
}
`
}

// quietLogger discards the store's diagnostics.
//
// Every malformed-file test deliberately triggers a warning, and a suite that
// printed all of them would bury a real failure in expected noise.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}
