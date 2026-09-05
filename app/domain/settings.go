package domain

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// This file models the settings the GO PROCESS owns, as against the ones the
// interface keeps for itself in the webview's own storage.
//
// # The ownership rule
//
// A setting is backend-owned when the Go process must act on it BEFORE or
// WITHOUT a window, or when it decides WHAT REACHES DISK OR THE NETWORK.
// Everything else stays in the webview's storage, where it already is.
//
// The test to apply to a new setting is: "if this process had no webview —
// `podsteer mcp`, a sampler tick before any pane has loaded, the cluster
// picker on launch — would the setting still have to exist?" If it would, it
// belongs here. Retention answers yes twice over: it governs what is written
// to somebody's disk, and the sampler consults it on a tick that can happen
// before the first render. A column width answers no to both.
//
// The rule cuts the other way just as firmly, and the reasons already recorded
// for the existing split hold. OBJECT NAMES STAY OUT. The snoozed findings
// (keyed by a namespace and an object name) and the per-cluster namespace
// filter keep their current home in the webview, because moving them into a
// file this process writes would break the exhaustive claim SECURITY.md makes
// about it: that it carries the name of no object in any cluster. That claim
// is worth more than the tidiness of having one settings file.
//
// What this value may hold, therefore: a recording policy, paths on this
// machine, a proxy, and kubeconfig CONTEXT names — which are handles the
// operator's own kubeconfig already gives them, and which the history file
// names already carry for the same reason.

// Sentinel errors raised when a settings value is not usable.
//
// They exist so that Validate's refusals can be told apart by the layer
// surfacing them, rather than matched on their text.
var (
	// ErrSettingsSourcePath is returned when a kubeconfig source names
	// something that is not an absolute path.
	//
	// ABSOLUTE ONLY, because there is no working directory this could be
	// relative TO that means anything: a desktop application launched from
	// Finder starts in `/`, and one launched from a terminal starts wherever
	// that terminal happened to be. A relative entry would name a different
	// file depending on how PodSteer was started, which is the worst
	// property a path in a persisted file can have.
	ErrSettingsSourcePath = errors.New("a kubeconfig source must be an absolute path")

	// ErrSettingsSourceKind is returned when a source is neither a file nor a
	// directory.
	ErrSettingsSourceKind = errors.New("a kubeconfig source must be a file or a directory")

	// ErrSettingsProxyMode is returned when the proxy mode is not one of the
	// three this build understands.
	ErrSettingsProxyMode = errors.New("unknown proxy mode")

	// ErrSettingsProxyURL is returned when a manual proxy has no usable URL.
	ErrSettingsProxyURL = errors.New("a manual proxy needs an absolute http(s) URL")

	// ErrSettingsProxyCredential is returned when a proxy URL carries
	// userinfo.
	//
	// REFUSED RATHER THAN STORED, and this is the one refusal in this file
	// that is a policy rather than a correctness check. `http://user:pass@…`
	// is a credential, and no credential is ever written to a file PodSteer
	// owns — this is precisely the file that ends up in a support bundle or
	// a screenshot. The environment is where a proxy credential belongs, and
	// the message says so.
	ErrSettingsProxyCredential = errors.New("a proxy URL must not carry a username or password")
)

// SourceKind distinguishes a single kubeconfig file from a folder of them.
type SourceKind string

const (
	// SourceFile is one kubeconfig file.
	SourceFile SourceKind = "file"
	// SourceDirectory is a folder scanned for kubeconfig files, the shape a
	// synced folder or a password manager's export leaves behind.
	SourceDirectory SourceKind = "directory"
)

// IsValid reports whether the kind is one this build understands.
func (k SourceKind) IsValid() bool {
	return k == SourceFile || k == SourceDirectory
}

// KubeconfigSource is one entry of the in-app kubeconfig source list.
//
// A PATH AND NOTHING ELSE. PodSteer never copies a kubeconfig, never reads one
// into this file, and never writes to one of these — see KubeconfigSettings
// for why a source can never become the write target.
type KubeconfigSource struct {
	// Path is an absolute path on this machine.
	Path string
	// Kind says whether Path is a single file or a folder to scan.
	Kind SourceKind
}

// KubeconfigSettings is the operator's own list of kubeconfig sources.
//
// ORDER IS MEANING: the list is appended to the loading precedence in this
// order, after the environment's own entries. It is never sorted on the
// operator's behalf.
//
// THERE IS NO WRITE-TARGET FLAG, and that absence is structural rather than a
// feature nobody has got to yet. The one write PodSteer makes to a kubeconfig
// goes to the FIRST entry of the loading precedence, and sources are only ever
// appended after the environment's entries — so a source cannot be first, and
// therefore cannot be written to. Adding a flag would mean writing to files
// the operator may be syncing from somewhere PodSteer has no business writing
// to.
type KubeconfigSettings struct {
	// Sources are the extra files and folders to read, in precedence order.
	Sources []KubeconfigSource
}

// ProxyMode says how PodSteer chooses a proxy for its own outbound calls.
type ProxyMode string

const (
	// ProxyFromEnvironment is today's behaviour exactly: whatever
	// HTTPS_PROXY, HTTP_PROXY and NO_PROXY say, as Go's own default
	// transport reads them. The default, and byte-for-byte what PodSteer
	// did before this setting existed.
	ProxyFromEnvironment ProxyMode = "environment"
	// ProxyNone forces a direct connection regardless of the environment.
	ProxyNone ProxyMode = "none"
	// ProxyManual uses the URL configured here.
	ProxyManual ProxyMode = "manual"
)

// IsValid reports whether the mode is one this build understands.
func (m ProxyMode) IsValid() bool {
	return m == ProxyFromEnvironment || m == ProxyNone || m == ProxyManual
}

// ProxySettings is the proxy PodSteer's own outbound calls go through.
//
// THE VALUE ONLY. Nothing in this build applies it yet — the transport work is
// a separate change — but the shape is settled here so the file it lives in
// does not have to change version to gain it, and so the credential refusal
// above exists before anything can write one.
type ProxySettings struct {
	// Mode selects between the environment, no proxy, and URL.
	Mode ProxyMode
	// URL is the proxy address, used only in ProxyManual.
	URL string
	// NoProxy is a comma-separated exception list, in the same syntax the
	// NO_PROXY environment variable uses.
	NoProxy string
}

// HistorySettings is the recording policy the sampler acts on.
type HistorySettings struct {
	// Retention is how long samples are kept. Zero records nothing.
	Retention Retention
	// SamplingInterval is how often each open cluster is sampled.
	SamplingInterval time.Duration
}

// ClusterSettings holds the per-cluster switches, keyed by kubeconfig context
// name in Settings.Clusters.
//
// EMPTY ON PURPOSE, AND RESERVED. The per-cluster opt-ins that belong here —
// node history and Prometheus queries — are not part of this change; the
// section exists so that adding one is a field rather than a new top-level
// key, which is the difference between a change an older build round-trips
// and one it cannot.
//
// The honest limit of that: an unknown top-level SECTION round-trips
// verbatim, but an unknown FIELD inside a section this build knows does not.
// That is why a file written by a newer version is read and never saved over
// — see the store.
type ClusterSettings struct{}

// WindowSettings holds window geometry.
//
// Empty and reserved, on the same terms as ClusterSettings. Windows carry no
// cluster identity, so persisting geometry will write no names.
type WindowSettings struct{}

// Settings is the whole of what the Go process owns.
//
// A VALUE, copied in and out. Nothing here holds a reference into the store,
// so a caller reading settings cannot mutate what another caller is about to
// write; every change goes through the store's Update instead.
type Settings struct {
	// History is the recording policy.
	History HistorySettings
	// Kubeconfig is the in-app source list.
	Kubeconfig KubeconfigSettings
	// Proxy is the outbound proxy policy.
	Proxy ProxySettings
	// Clusters holds per-cluster switches, keyed by kubeconfig context name.
	Clusters map[string]ClusterSettings
	// Windows holds window geometry.
	Windows WindowSettings
}

// DefaultSettings returns the settings a machine with no file has.
//
// These are the values PodSteer behaved by before this file existed, which is
// what makes deleting the file a safe thing for an operator to do.
func DefaultSettings() Settings {
	return Settings{
		History: HistorySettings{
			// A day: long enough for the dashboard's trend to be useful
			// across a working day, short enough that nobody discovers
			// PodSteer has been keeping a month of data they never asked for.
			Retention:        NewRetention(1),
			SamplingInterval: DefaultSamplingInterval,
		},
		Proxy:    ProxySettings{Mode: ProxyFromEnvironment},
		Clusters: map[string]ClusterSettings{},
	}
}

// Clone returns a deep copy, so a reader can never mutate the store's value.
func (s Settings) Clone() Settings {
	out := s
	out.Kubeconfig.Sources = slices.Clone(s.Kubeconfig.Sources)
	out.Clusters = make(map[string]ClusterSettings, len(s.Clusters))
	for id, cluster := range s.Clusters {
		out.Clusters[id] = cluster
	}
	return out
}

// Normalise clamps every field into range and reports how many had to be
// reset.
//
// THIS IS THE READ PATH, and its contract is that it never fails. A settings
// file that parses but holds a nonsense value must not stop PodSteer starting
// and must not be silently repaired in place either: each bad field falls back
// to its default, the count comes back here, and the store logs it once. What
// the operator hand-edited is still on disk, unaltered, until something asks
// for a write.
//
// A source whose path is unusable is DROPPED rather than defaulted, because a
// list entry has no default to fall back to; that counts as a reset too, so
// the number the store logs is the number of things that did not survive.
func (s *Settings) Normalise() int {
	reset := 0

	if clamped := NewRetention(s.History.Retention.Days); clamped != s.History.Retention {
		s.History.Retention = clamped
		reset++
	}
	if clamped := NewSamplingInterval(s.History.SamplingInterval); clamped != s.History.SamplingInterval {
		s.History.SamplingInterval = clamped
		reset++
	}

	kept := make([]KubeconfigSource, 0, len(s.Kubeconfig.Sources))
	seen := make(map[string]struct{}, len(s.Kubeconfig.Sources))
	for _, source := range s.Kubeconfig.Sources {
		cleaned, err := source.normalise()
		if err != nil {
			reset++
			continue
		}
		// A path listed twice is one source. Two entries would scan the same
		// folder twice and put the same file in the precedence list twice,
		// which client-go tolerates but which reads as a duplicate in every
		// place the list is shown.
		if _, already := seen[cleaned.Path]; already {
			reset++
			continue
		}
		seen[cleaned.Path] = struct{}{}
		kept = append(kept, cleaned)
	}
	s.Kubeconfig.Sources = kept

	if proxyReset := s.Proxy.normalise(); proxyReset {
		reset++
	}

	if s.Clusters == nil {
		s.Clusters = map[string]ClusterSettings{}
	}

	return reset
}

// normalise returns the source with its path cleaned, or an error when it is
// not usable at all.
func (k KubeconfigSource) normalise() (KubeconfigSource, error) {
	path := strings.TrimSpace(k.Path)
	if path == "" || !filepath.IsAbs(path) {
		return KubeconfigSource{}, ErrSettingsSourcePath
	}
	kind := k.Kind
	if kind == "" {
		// A file is the assumption a bare path carries everywhere else — a
		// `$KUBECONFIG` entry, a `--kubeconfig` flag — so it is the one a
		// file written by hand should inherit rather than being refused.
		kind = SourceFile
	}
	if !kind.IsValid() {
		return KubeconfigSource{}, ErrSettingsSourceKind
	}
	return KubeconfigSource{Path: filepath.Clean(path), Kind: kind}, nil
}

// normalise resets an unusable proxy to the environment default, reporting
// whether it had to.
func (p *ProxySettings) normalise() bool {
	if p.Mode == "" {
		p.Mode = ProxyFromEnvironment
	}
	if err := p.validate(); err != nil {
		*p = ProxySettings{Mode: ProxyFromEnvironment}
		return true
	}
	p.URL = strings.TrimSpace(p.URL)
	p.NoProxy = strings.TrimSpace(p.NoProxy)
	return false
}

// Validate reports whether these settings may be written.
//
// THIS IS THE WRITE PATH, and unlike Normalise it refuses. The difference is
// deliberate: a bad value that arrived from a file is somebody's hand edit and
// PodSteer carries on around it, while a bad value that arrived from the
// interface is a bug in the interface, and writing it would persist the bug.
func (s Settings) Validate() error {
	for _, source := range s.Kubeconfig.Sources {
		if _, err := source.normalise(); err != nil {
			return fmt.Errorf("kubeconfig source %q: %w", source.Path, err)
		}
	}
	return s.Proxy.validate()
}

// validate reports whether the proxy settings describe something reachable.
func (p ProxySettings) validate() error {
	if !p.Mode.IsValid() {
		return fmt.Errorf("%w: %q", ErrSettingsProxyMode, p.Mode)
	}
	if p.Mode != ProxyManual {
		return nil
	}

	parsed, err := url.Parse(strings.TrimSpace(p.URL))
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return ErrSettingsProxyURL
	}
	switch parsed.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return fmt.Errorf("%w: %q", ErrSettingsProxyURL, parsed.Scheme)
	}
	if parsed.User != nil {
		return ErrSettingsProxyCredential
	}
	return nil
}

// SettingsState is what the store can say about the file behind the settings.
//
// It exists because two of the store's failure modes are things an operator
// has to be TOLD rather than have worked around silently: a file that could
// not be read at all, and one written by a newer PodSteer than this. Both
// leave the application working on defaults, so without a line in the
// interface the only evidence would be a setting that quietly refuses to
// stick.
type SettingsState struct {
	// Path is the settings file, whether or not it exists yet.
	Path string
	// ReadOnly reports that this process will never write the file. True for
	// `podsteer mcp`, which writes nothing anywhere.
	ReadOnly bool
	// FromFuture reports that the file on disk declares a version this build
	// does not understand. What it could read has been applied; nothing will
	// be saved over it.
	FromFuture bool
	// Version is the version the file declared, when FromFuture is set.
	Version int
	// Unreadable reports that the file exists but is not a settings document
	// — malformed JSON, or the wrong kind. Defaults are in use, and the file
	// will be set aside rather than overwritten when something first saves.
	Unreadable bool
	// Repaired counts the fields that held an invalid value and fell back to
	// their default on the last read.
	Repaired int
}

// IsWritable reports whether a change made now would reach the disk.
func (s SettingsState) IsWritable() bool { return !s.ReadOnly && !s.FromFuture }

// KubeconfigOrigin says which of the three places a kubeconfig entry came
// from.
//
// It exists so the interface can show an environment-derived entry as a row
// it must not offer to remove: PODSTEER_KUBECONFIG_DIR is set by a packager,
// by an enterprise's policy, or in somebody's shell profile, and a button that
// appeared to delete it would be lying.
type KubeconfigOrigin string

const (
	// OriginDefault is the explicit or default kubeconfig chain: PodSteer's
	// own override, else $KUBECONFIG, else ~/.kube/config.
	OriginDefault KubeconfigOrigin = "default"
	// OriginEnvironment is PODSTEER_KUBECONFIG_DIR.
	OriginEnvironment KubeconfigOrigin = "environment"
	// OriginSettings is the in-app source list.
	OriginSettings KubeconfigOrigin = "settings"
)

// IsEditable reports whether the interface may offer to remove or reorder an
// entry from this origin.
func (o KubeconfigOrigin) IsEditable() bool { return o == OriginSettings }

// KubeconfigEntry is one entry of the composed loading list, as the settings
// pane shows it.
//
// A REPORT, not a setting: it is derived on every read from the environment
// plus the stored sources, and nothing writes it back.
type KubeconfigEntry struct {
	// Path is the file or folder this entry names.
	Path string
	// Kind says which of the two it is.
	Kind SourceKind
	// Origin says where the entry came from, and therefore whether the
	// interface may edit it.
	Origin KubeconfigOrigin
	// Missing reports that nothing is at Path right now.
	//
	// KEPT AND REPORTED, NEVER DROPPED. A folder synced from a password
	// manager or a cloud drive is routinely absent for the first minute
	// after a login, and an entry that silently deleted itself in that
	// window would be a setting that disappears when the machine is slow.
	Missing bool
	// Files are the kubeconfig files this entry contributed to the loading
	// precedence: itself for a file, whatever the scan found for a folder.
	Files []string
	// Contexts are the kubeconfig context names defined in those files,
	// whether or not this entry is the one that won them.
	Contexts []string
}

// KubeconfigLocation is the file a context was read from, as client-go
// reports it.
//
// Empty when the context did not come from a file at all, which is what a
// synthetic or in-memory configuration looks like.
type KubeconfigLocation string

// String returns the path, or the empty string.
func (l KubeconfigLocation) String() string { return string(l) }

// IsZero reports whether no origin was recorded.
func (l KubeconfigLocation) IsZero() bool { return l == "" }
