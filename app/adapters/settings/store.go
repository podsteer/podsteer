// Package settings stores the settings the Go process owns.
//
// # Why there is a file here at all
//
// There already was one. `history.json` has been written beside the history
// directory since retention became configurable: two integers, a plain
// os.WriteFile, no version, no kind, and a whole-document replacement on every
// change. SECURITY.md already discloses it — "sampled capacity history and its
// retention setting". So this package is not PodSteer's first file; it is that
// accident, absorbed and given a shape: one document, versioned, atomic,
// written by one owner, and read by a subcommand that never writes it.
//
// # Where it lives, and why not beside the history
//
// `os.UserConfigDir()/PodSteer/settings.json`, resolved HERE from the user
// configuration directory directly, and deliberately not derived from the
// history directory the way `history.json` was. That dependency ran the wrong
// way: history is state and settings are configuration, and on Linux those are
// meant to part company (~/.local/state against ~/.config). Deriving one from
// the other would drag settings along the day history moves.
//
// # What it will not hold
//
// No credential of any kind, and the name of no object in any cluster. The
// paths of kubeconfig files, and kubeconfig CONTEXT names, are the whole of
// what is cluster-shaped here — a context name is a handle the operator's own
// kubeconfig already gives them, on exactly the terms the history file names
// already carry one. SECURITY.md states this exhaustively and treats a breach
// of it as in scope.
package settings

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

const (
	// Kind marks the document, and is deliberately NOT the exported settings
	// file's "PodSteerSettings".
	//
	// The two files are unrelated: that one is an allowlist of the interface's
	// own arrangements, made to be sent to a colleague; this one is what the
	// Go process acts on. They are both JSON, both have a `_readme`, and both
	// live where a person might drop one in place of the other. A distinct
	// kind is what makes that a refusal rather than a silent misread.
	Kind = "PodSteerBackendSettings"

	// Version is the document version this build writes and understands.
	//
	// A file declaring MORE is read for what is recognisable and never
	// written back — see ports.ErrSettingsFromFuture. A file declaring less
	// is read and rewritten at this version, which is what a migration will
	// hang off when there is one.
	Version = 1

	// FileName is the document's name inside the PodSteer configuration
	// directory.
	FileName = "settings.json"

	// dirMode and fileMode are PodSteer's own, always.
	//
	// NOT PRESERVED FROM AN EXISTING FILE, which is the opposite of what the
	// kubeconfig merge does — and the difference is ownership. A kubeconfig
	// belongs to the operator, who may have deliberately given it a mode of
	// their own, so rewriting it preserves what they set. This file belongs
	// to PodSteer: nothing else has a reason to widen it, so a widened mode
	// is a mistake rather than an intention, and 0600 is restored.
	dirMode  = fs.FileMode(0o700)
	fileMode = fs.FileMode(0o600)
)

// readme is the header the file carries, so that somebody who opens it knows
// what it is before they change anything.
//
// JSON has no comments; an array of strings at the top of the document is the
// same device the exported settings file uses, for the same reason.
var readme = []string{
	"This file is written by PodSteer. It holds the settings the application itself acts on.",
	"It is re-read when PodSteer starts, so an edit made by hand applies on the next launch.",
	"It carries kubeconfig file and folder PATHS and kubeconfig context names — never the",
	"contents of a kubeconfig, never a credential, and never the name of anything in a cluster.",
	"Deleting it restores the defaults. PodSteer rewrites it whole whenever a setting changes.",
}

// Store is the file-backed settings store.
//
// ONE VALUE UNDER ONE MUTEX. Reads take a copy; every write is a
// read-modify-write inside the lock, so a caller changing retention and a
// caller adding a kubeconfig source cannot each write a whole document with
// the other's change missing from it. That is the bug the old two-integer
// file had, and it was invisible because nothing wrote it twice at once yet.
type Store struct {
	path     string
	readOnly bool
	logger   *slog.Logger

	// now is the clock the sidelined file's name is stamped from. A field so
	// a test can assert the name rather than the shape of it.
	now func() time.Time

	mu sync.Mutex
	// value is the settings as they currently stand in this process.
	value domain.Settings
	// unknown holds top-level sections this build does not understand, so
	// they survive a rewrite. See document.Unknown.
	unknown map[string]jsontext.Value
	// state is what the store can say about the file it read.
	state domain.SettingsState
	// sidelined records that the unreadable file has already been moved out
	// of the way, so a second write does not go looking for it again.
	sidelined bool
}

var _ ports.SettingsPort = (*Store)(nil)

// DefaultPath returns the per-user settings file.
//
// FROM THE USER CONFIGURATION DIRECTORY DIRECTLY. See the package comment for
// why this must not be derived from the history directory.
func DefaultPath() (string, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("settings: locating the user config directory: %w", err)
	}
	return filepath.Join(config, "PodSteer", FileName), nil
}

// Options configures a store.
type Options struct {
	// Path is the settings file.
	//
	// Empty means there is nowhere to write — a machine whose configuration
	// directory could not be located at all. The store still works: it serves
	// the defaults and refuses writes, so the application runs on a machine
	// PodSteer cannot store anything on rather than failing to start there.
	Path string

	// ReadOnly opens the store without the ability to write.
	//
	// `podsteer mcp` sets it, and that is the whole reason it exists:
	// SECURITY.md promises the subcommand writes nothing anywhere, and a
	// promise kept by everyone remembering not to call Update is not kept.
	// A read-only store also performs no adoption write and creates no
	// directory, so an MCP run leaves the configuration directory exactly as
	// it found it.
	ReadOnly bool

	// AdoptFrom is the pre-0.3 `history.json`, read once if this file does
	// not exist yet.
	//
	// PASSED IN RATHER THAN DERIVED, because the whole point of this package
	// is that the settings path does not depend on where the history lives.
	// The composition root knows both and is the only place that should.
	// Empty means there is nothing to adopt.
	AdoptFrom string

	// Logger receives diagnostics. Optional; defaults to slog.Default.
	Logger *slog.Logger
}

// Open reads the settings file and returns a store over it.
//
// NEVER FAILS. A missing file is the ordinary first run; an unreadable one, a
// wrong kind, one from a newer PodSteer, and a machine with no configuration
// directory at all each produce a working store on the defaults, with the
// reason recorded in State for the interface to show. The error return exists
// for the shape of the port and for whatever a later revision needs it for;
// nothing on somebody's disk reaches it.
func Open(opts Options) (*Store, error) {
	// Nowhere to write is read-only by construction rather than by a second
	// flag every caller would have to check: "this process will not write the
	// settings" is one fact, and it has one field.
	readOnly := opts.ReadOnly || opts.Path == ""

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	store := &Store{
		path:     opts.Path,
		readOnly: readOnly,
		logger:   logger.With(slog.String("store", "settings")),
		now:      time.Now,
		value:    domain.DefaultSettings(),
		state:    domain.SettingsState{Path: opts.Path, ReadOnly: readOnly},
	}

	if opts.Path == "" {
		return store, nil
	}

	store.load(opts.AdoptFrom)
	return store, nil
}

// Load returns a copy of the current settings.
func (s *Store) Load(context.Context) (domain.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value.Clone(), nil
}

// State reports where the settings live and whether they can be written.
func (s *Store) State() domain.SettingsState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Update applies mutate and writes the whole document atomically.
//
// The order is the contract: refuse, copy, mutate, validate, WRITE, and only
// then swap the in-memory value. Swapping first would leave the process
// acting on a setting the disk does not carry, so a failed write would show
// as a setting that worked until the next launch.
func (s *Store) Update(
	ctx context.Context,
	mutate func(*domain.Settings) error,
) (domain.Settings, error) {
	if mutate == nil {
		return domain.Settings{}, errors.New("settings: Update needs a mutation")
	}
	if err := ctx.Err(); err != nil {
		return domain.Settings{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// BEFORE mutate runs, so a caller can never mistake a refusal for a
	// change that was made and then lost.
	switch {
	case s.readOnly:
		return domain.Settings{}, fmt.Errorf("changing settings: %w", ports.ErrSettingsReadOnly)
	case s.state.FromFuture:
		return domain.Settings{}, fmt.Errorf("changing settings: %w (version %d)",
			ports.ErrSettingsFromFuture, s.state.Version)
	}

	next := s.value.Clone()
	if err := mutate(&next); err != nil {
		return domain.Settings{}, err
	}

	// VALIDATE BEFORE NORMALISE, and the order is the whole difference
	// between the two. Normalise is the READ path: it repairs whatever it
	// finds so that a hand-edited file cannot stop PodSteer starting.
	// Validate is the WRITE path: a bad value arriving from the interface is
	// a bug in the interface, and persisting it would persist the bug.
	// Running Normalise first would repair the value out from under Validate
	// and make every refusal here unreachable.
	if err := next.Validate(); err != nil {
		return domain.Settings{}, err
	}
	// Now only the clamps are left to apply — retention and the sampling
	// cadence, which are deliberately clamped rather than refused, because an
	// out-of-range cadence is a UI bug and refusing it would leave the
	// operator with no recording at all rather than a slightly different one.
	next.Normalise()

	if err := s.write(next); err != nil {
		return domain.Settings{}, err
	}

	s.value = next
	s.state.Unreadable = false
	s.state.Repaired = 0
	return next.Clone(), nil
}

// --- reading ---------------------------------------------------------------

// document is the on-disk shape.
//
// The envelope comes first so that `head` on the file answers what it is and
// which version wrote it, before any setting. The sections follow, one
// top-level key each.
type document struct {
	Readme  []string `json:"_readme"`
	Kind    string   `json:"kind"`
	Version int      `json:"version"`

	History    historySection            `json:"history"`
	Kubeconfig kubeconfigSection         `json:"kubeconfig"`
	Proxy      proxySection              `json:"proxy"`
	Clusters   map[string]clusterSection `json:"clusters"`
	Windows    windowSection             `json:"windows"`

	// Unknown collects top-level members this build does not recognise and
	// writes them back out unchanged.
	//
	// THIS IS WHAT STOPS AN OLDER BUILD ERASING A NEWER ONE'S SECTION. Run
	// v0.4 once, let it add a `terminal` section, then open v0.3: without
	// this the next save would write a document with no `terminal` in it and
	// the operator would lose the settings silently. It protects against an
	// added SECTION only — a field that MOVED between sections is not
	// recoverable this way, which is why a newer version refuses writes
	// outright rather than relying on this alone.
	Unknown map[string]jsontext.Value `json:",embed"`
}

type historySection struct {
	RetentionDays           int `json:"retentionDays"`
	SamplingIntervalSeconds int `json:"samplingIntervalSeconds"`
}

type kubeconfigSection struct {
	Sources []sourceEntry `json:"sources"`
}

type sourceEntry struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type proxySection struct {
	Mode string `json:"mode"`
	// omitzero on both: an unset proxy should read as three words in the
	// file, not as two empty strings somebody has to decide the meaning of.
	URL     string `json:"url,omitzero"`
	NoProxy string `json:"noProxy,omitzero"`
}

// clusterSection and windowSection are the reserved per-cluster and window
// sections. See domain.ClusterSettings.
type clusterSection struct{}

type windowSection struct{}

// load reads the file into the store, falling back to defaults on anything it
// cannot use.
func (s *Store) load(adoptFrom string) {
	raw, err := os.ReadFile(s.path)
	switch {
	case err == nil:
		s.decode(raw)
		return
	case errors.Is(err, fs.ErrNotExist):
		// The ordinary first run — and the one moment adoption applies.
		s.adopt(adoptFrom)
		return
	default:
		// Present but unreadable: a permission problem, a directory in its
		// place. Defaults, one line, and the file is left where it is.
		s.state.Unreadable = true
		s.logger.Warn("settings could not be read, using the defaults",
			slog.String("path", s.path), slog.String("error", err.Error()))
	}
}

// decode turns the file's bytes into the stored value.
func (s *Store) decode(raw []byte) {
	var doc document
	if err := json.Unmarshal(raw, &doc); err != nil {
		s.state.Unreadable = true
		// ONE LINE, AND NOT THE CONTENT. What failed to parse is somebody's
		// file; the path and the parser's own complaint are enough to fix it.
		s.logger.Warn("settings are unreadable, using the defaults",
			slog.String("path", s.path), slog.String("error", err.Error()))
		return
	}

	if doc.Kind != Kind {
		s.state.Unreadable = true
		s.logger.Warn("that file is not a PodSteer settings document, using the defaults",
			slog.String("path", s.path), slog.String("kind", doc.Kind))
		return
	}

	if doc.Version > Version {
		// Read what is understood, and never write. See
		// ports.ErrSettingsFromFuture.
		s.state.FromFuture = true
		s.state.Version = doc.Version
		s.logger.Warn("the settings file was written by a newer PodSteer; it will not be saved over",
			slog.String("path", s.path),
			slog.Int("fileVersion", doc.Version),
			slog.Int("thisVersion", Version))
	}

	value := doc.toDomain()
	repaired := value.Normalise()
	if repaired > 0 {
		s.logger.Warn("some settings held values this build cannot use and fell back to their defaults",
			slog.String("path", s.path), slog.Int("count", repaired))
	}

	s.value = value
	s.unknown = doc.Unknown
	s.state.Repaired = repaired
}

// toDomain translates the document into the value the rest of the application
// works with.
func (d document) toDomain() domain.Settings {
	value := domain.DefaultSettings()

	value.History = domain.HistorySettings{
		Retention:        domain.Retention{Days: d.History.RetentionDays},
		SamplingInterval: time.Duration(d.History.SamplingIntervalSeconds) * time.Second,
	}

	sources := make([]domain.KubeconfigSource, 0, len(d.Kubeconfig.Sources))
	for _, entry := range d.Kubeconfig.Sources {
		sources = append(sources, domain.KubeconfigSource{
			Path: entry.Path,
			Kind: domain.SourceKind(entry.Kind),
		})
	}
	value.Kubeconfig = domain.KubeconfigSettings{Sources: sources}

	value.Proxy = domain.ProxySettings{
		Mode:    domain.ProxyMode(d.Proxy.Mode),
		URL:     d.Proxy.URL,
		NoProxy: d.Proxy.NoProxy,
	}

	value.Clusters = make(map[string]domain.ClusterSettings, len(d.Clusters))
	for id := range d.Clusters {
		value.Clusters[id] = domain.ClusterSettings{}
	}

	return value
}

// toDocument is the reverse, carrying the unknown sections back out.
func toDocument(value domain.Settings, unknown map[string]jsontext.Value) document {
	sources := make([]sourceEntry, 0, len(value.Kubeconfig.Sources))
	for _, source := range value.Kubeconfig.Sources {
		sources = append(sources, sourceEntry{Path: source.Path, Kind: string(source.Kind)})
	}

	clusters := make(map[string]clusterSection, len(value.Clusters))
	for id := range value.Clusters {
		clusters[id] = clusterSection{}
	}

	return document{
		Readme:  readme,
		Kind:    Kind,
		Version: Version,
		History: historySection{
			RetentionDays:           value.History.Retention.Days,
			SamplingIntervalSeconds: int(value.History.SamplingInterval.Seconds()),
		},
		Kubeconfig: kubeconfigSection{Sources: sources},
		Proxy: proxySection{
			Mode:    string(value.Proxy.Mode),
			URL:     value.Proxy.URL,
			NoProxy: value.Proxy.NoProxy,
		},
		Clusters: clusters,
		Windows:  windowSection{},
		Unknown:  unknown,
	}
}

// adopt reads the pre-0.3 history.json, if there is one, and removes it once
// its two settings are safely in the new file.
//
// ONE WAY AND ONCE. v0.2.0 shipped that file, so the settings it holds are on
// real machines and an operator who set retention to zero must not find
// PodSteer recording again after an upgrade. Nothing writes the old file
// afterwards, and nothing reads it once it is gone.
func (s *Store) adopt(from string) {
	if from == "" {
		return
	}

	raw, err := os.ReadFile(from)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.logger.Warn("the previous settings file could not be read",
				slog.String("path", from), slog.String("error", err.Error()))
		}
		return
	}

	var legacy struct {
		RetentionDays   int `json:"retentionDays"`
		IntervalSeconds int `json:"intervalSeconds"`
	}
	if err := json.Unmarshal(raw, &legacy); err != nil {
		s.logger.Warn("the previous settings file is unreadable, using the defaults",
			slog.String("path", from), slog.String("error", err.Error()))
		return
	}

	adopted := domain.DefaultSettings()
	adopted.History = domain.HistorySettings{
		Retention:        domain.Retention{Days: legacy.RetentionDays},
		SamplingInterval: time.Duration(legacy.IntervalSeconds) * time.Second,
	}
	adopted.Normalise()
	s.value = adopted

	if s.readOnly {
		// The settings are adopted in memory so the subcommand behaves the
		// same as the window, and NOTHING is written or removed: this process
		// creates no file and deletes none.
		return
	}

	if err := s.write(adopted); err != nil {
		// The old file stays exactly where it is, so the next run tries
		// again rather than losing the setting.
		s.logger.Warn("adopting the previous settings failed", slog.String("error", err.Error()))
		return
	}

	// Only now, and best effort: the setting is already safe in the new file,
	// so a removal that fails is a stray file rather than a lost setting.
	if err := os.Remove(from); err != nil {
		s.logger.Warn("the previous settings file could not be removed",
			slog.String("path", from), slog.String("error", err.Error()))
		return
	}
	s.logger.Info("adopted the previous settings file",
		slog.String("from", from), slog.String("to", s.path))
}

// --- writing ---------------------------------------------------------------

// write replaces the file with value, atomically.
//
// Caller holds the lock.
func (s *Store) write(value domain.Settings) error {
	encoded, err := json.Marshal(toDocument(value, s.unknown), jsontext.WithIndent("  "))
	if err != nil {
		return fmt.Errorf("encoding settings: %w: %w", ports.ErrSettingsUnavailable, err)
	}
	// Newline-terminated: it is a text file in somebody's configuration
	// directory, and every tool that touches one expects the last line to end.
	encoded = append(encoded, '\n')

	// BEFORE the write, not after: an unreadable file is somebody's hand edit
	// and replacing it would destroy the evidence of whatever they meant.
	s.sideline()

	if err := writeFile(s.path, encoded); err != nil {
		return err
	}
	return nil
}

// sideline moves an unreadable file out of the way, once.
//
// Named with the instant rather than a fixed suffix, so a second bad edit does
// not overwrite the first one that was set aside.
func (s *Store) sideline() {
	if !s.state.Unreadable || s.sidelined {
		return
	}
	s.sidelined = true

	aside := s.path + ".invalid-" + strconv.FormatInt(s.now().UTC().Unix(), 10)
	if err := os.Rename(s.path, aside); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.logger.Warn("the unreadable settings file could not be set aside",
				slog.String("path", s.path), slog.String("error", err.Error()))
		}
		return
	}
	s.logger.Warn("the unreadable settings file was set aside rather than replaced",
		slog.String("from", s.path), slog.String("to", aside))
}

// writeFile replaces path's contents atomically.
//
// The same sequence app/adapters/k8s/kubeconfig.go's writeKubeconfig uses —
// temporary file in the SAME directory, chmod, write, sync, close, rename,
// remove the temporary file on any failure — and deliberately NOT shared with
// it. That one belongs to the operator: it preserves an existing mode and
// keeps a backup, because the file it replaces holds credentials somebody else
// manages. This one is PodSteer's: it restores 0600 whatever it finds and
// keeps no backup, because the document can be regenerated from defaults.
// Consolidating them would mean one function with a flag for each of those
// differences, and the flag that mattered would eventually be passed wrong.
func writeFile(path string, content []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("preparing %s: %w: %w", dir, ports.ErrSettingsUnavailable, err)
	}

	temp, err := os.CreateTemp(dir, ".settings-*.tmp")
	if err != nil {
		return fmt.Errorf("writing settings: %w: %w", ports.ErrSettingsUnavailable, err)
	}
	name := temp.Name()
	defer func() { _ = os.Remove(name) }() // A no-op once the rename succeeds.

	if err := temp.Chmod(fileMode); err != nil {
		_ = temp.Close()
		return fmt.Errorf("securing %s: %w: %w", name, ports.ErrSettingsUnavailable, err)
	}
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return fmt.Errorf("writing %s: %w: %w", name, ports.ErrSettingsUnavailable, err)
	}
	// Synced before the rename: the rename is atomic with respect to the
	// directory, but the CONTENT still has to have reached the disk or a
	// crash could leave the new name pointing at an empty file.
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("syncing %s: %w: %w", name, ports.ErrSettingsUnavailable, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w: %w", name, ports.ErrSettingsUnavailable, err)
	}

	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replacing %s: %w: %w", path, ports.ErrSettingsUnavailable, err)
	}
	return nil
}
