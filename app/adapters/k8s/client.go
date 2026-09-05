package k8s

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// Default client tuning. See package documentation for the reasoning.
const (
	// defaultQPS replaces client-go's default of 5, which is sized for a
	// controller's steady background load rather than a UI that issues a burst
	// of requests every time the operator opens a view.
	defaultQPS = 50.0

	// defaultBurst allows a whole screen's worth of requests to leave at once
	// instead of queueing behind the rate limiter.
	defaultBurst = 100

	// defaultUserAgent identifies PodSteer in API server audit logs. Clusters
	// with request-tracing or admission policies key off this, so a generic
	// "Go-http-client" would make PodSteer traffic unattributable.
	defaultUserAgent = "podsteer"

	// protobufContentType is the Kubernetes protobuf media type. Core API
	// group objects (pods, namespaces) serialise to it, and decoding it costs
	// a fraction of the equivalent JSON in both time and allocations.
	protobufContentType = "application/vnd.kubernetes.protobuf"

	// acceptContentTypes lists protobuf first but keeps JSON as a fallback, so
	// endpoints that cannot speak protobuf — aggregated APIs, CRDs — still
	// answer instead of failing the request.
	acceptContentTypes = protobufContentType + ",application/json"
)

// Config tunes the Kubernetes adapter.
//
// The zero value is usable: every field falls back to a sane default, so
// callers only set what they mean to change.
type Config struct {
	// KubeconfigPath overrides the kubeconfig location. Empty means the
	// standard client-go resolution order: $KUBECONFIG, then ~/.kube/config.
	KubeconfigPath string

	// KubeconfigDir, when set, names a directory whose kubeconfig files are
	// merged into the loading precedence AFTER KubeconfigPath (or, when that
	// is unset, after whatever the standard resolution already produced).
	// Empty means no directory is read. See loadingRules and
	// kubeconfigDirFiles for what that merge does and what it skips.
	KubeconfigDir string

	// QPS is the sustained request rate allowed per cluster. Zero means
	// defaultQPS.
	QPS float32

	// Burst is the number of requests allowed to exceed QPS momentarily.
	// Zero means defaultBurst.
	Burst int

	// UserAgent identifies PodSteer to the API server. Empty means
	// defaultUserAgent.
	UserAgent string

	// Sources reports the operator's OWN kubeconfig source list — the files
	// and folders added in Settings — in precedence order.
	//
	// A FUNCTION, not a slice, for the same reason the local terminal takes
	// KubeconfigFiles as one: the list changes while PodSteer runs, and every
	// resolution here already re-reads the world so that a file dropped into
	// a folder appears without a restart. Nil means there are none, which is
	// what `podsteer mcp` passes when it is given no store and what every
	// test that does not care about sources leaves unset.
	//
	// It is called on the read path only. Nothing in this adapter writes a
	// source, and nothing can: see loadingRules for why a source is
	// structurally incapable of being the merge's write target.
	Sources func() []domain.KubeconfigSource

	// LiveWatch mirrors a cluster's pods locally instead of re-listing them
	// on every refresh. See watch.go.
	//
	// A SWITCH BECAUSE IT IS AN OPTIMISATION, and an optimisation that talks
	// to somebody's cluster differently deserves a way back. Off restores the
	// polling behaviour exactly — not an approximation of it, the same code
	// path, because the fallback IS that path rather than a reimplementation
	// of it.
	LiveWatch bool

	// EnvReady is closed once the process PATH has been resolved, if it is
	// being resolved at all. Nil means it is not, and nothing waits.
	//
	// WHY BUILDING A CLIENT WAITS ON IT. A kubeconfig context can authenticate
	// through a credential plugin — `aws eks get-token` for EKS,
	// `gke-gcloud-auth-plugin` for GKE — which client-go runs by looking the
	// name up in PATH. A desktop application launched from Finder or by
	// Homebrew inherits launchd's PATH, which has no Homebrew directory in it,
	// so those lookups fail. Recovering the operator's real PATH means asking
	// their login shell, which takes a second or so.
	//
	// A CHANNEL RATHER THAN DOING IT AT STARTUP, because a second of blank
	// window on every launch is a bad trade for something only the first
	// connection needs. The probe runs alongside the window opening; the first
	// client to be built waits for whatever is left of it, and by then the
	// operator has usually spent longer than that choosing a cluster.
	EnvReady <-chan struct{}
}

// withDefaults returns a copy of c with unset fields filled in.
func (c Config) withDefaults() Config {
	if c.QPS <= 0 {
		c.QPS = defaultQPS
	}
	if c.Burst <= 0 {
		c.Burst = defaultBurst
	}
	if c.UserAgent == "" {
		c.UserAgent = defaultUserAgent
	}
	return c
}

// clients is the set of API clients PodSteer needs against one cluster.
//
// They are built and cached together because they all derive from the same
// REST config and the same expensive credential resolution. Building them
// lazily one at a time would pay that cost several times over for a single
// cluster.
type clients struct {
	// typed serves the built-in kinds with compile-time-checked types.
	typed kubernetes.Interface
	// dynamic serves any kind, including custom resources, as unstructured
	// objects. It is what makes the navigator's long tail work.
	dynamic dynamic.Interface
	// discovery enumerates what the API server actually serves, which is how
	// a cluster's CRDs are found.
	discovery discovery.DiscoveryInterface
	// metrics reads the metrics.k8s.io API. Present even on clusters without
	// metrics-server — calls simply fail, which callers expect.
	metrics metricsclient.Interface
	// meta lists PartialObjectMetadata only — names, labels, managedFields —
	// never a full object body. It exists so a scan for who last wrote an
	// object through a deprecated API version never has to pull object
	// bodies (or Secret contents) to learn that.
	meta metadata.Interface
	// config is retained for requests that bypass the typed clients, notably
	// the server-side table printing used by the generic browser.
	config *rest.Config

	// restMapperMu guards restMapper, which is built lazily on first use
	// rather than alongside the clients above: most connections never apply
	// a manifest, and a discovery-driven mapper walks every API group and
	// version the cluster serves, which is not a cost worth paying on
	// connect for a feature that may never be used.
	restMapperMu sync.RWMutex
	// restMapper resolves a GroupVersionKind to its GroupVersionResource and
	// scope for UpdateResource. Cached because building one re-queries
	// discovery, and rebuilt exactly once when a lookup reports
	// meta.NoKindMatchError — see clientFactory.restMappingFor in apply.go —
	// so a CRD installed a minute ago applies without reconnecting the
	// cluster, while an apply of an ordinary built-in kind never re-queries
	// discovery at all.
	restMapper meta.RESTMapper
}

// clientFactory builds and caches one client set per cluster.
//
// The cache is the point. Building a client parses the kubeconfig, resolves
// TLS material and, for cloud providers, executes a credential plugin as a
// child process — hundreds of milliseconds that must not be paid on every
// list request. Cached clients also pool their HTTP connections, so repeated
// polling reuses an established TLS session instead of renegotiating.
//
// It is safe for concurrent use.
type clientFactory struct {
	cfg    Config
	logger *slog.Logger

	mu      sync.RWMutex
	clients map[domain.ClusterID]*clients

	// mapperBuilder builds a RESTMapper from a cluster's discovery client. A
	// field rather than a bare call to restmapper.GetAPIGroupResources so a
	// test can substitute a counting wrapper and prove a rebuild actually
	// happened — see apply_test.go — rather than inferring it from timing or
	// from a real discovery fake.
	mapperBuilder func(discovery.DiscoveryInterface) (meta.RESTMapper, error)
	// warnedDirs holds the directories already reported as unlistable, so an
	// unreadable one is named once rather than on every one of the reads the
	// kubeconfig gets. See kubeconfigFilesIn: every call re-scans, and an
	// unreadable directory fails the same way each time, so logging it more
	// than once would just repeat the same fact on every refresh.
	//
	// KEYED BY PATH rather than a bare sync.Once, because there is no longer
	// only one directory: PODSTEER_KUBECONFIG_DIR and every folder source in
	// the settings are scanned by the same function, and a single Once would
	// mean the first unreadable folder silenced the report for all the
	// others. An entry is dropped the moment the directory reads again.
	warnedDirs sync.Map
	// warnedFiles holds the directory files already reported as unparsable,
	// so a junk file is named once rather than on every one of the reads the
	// kubeconfig gets — several a second under a 5-second refresh. An entry
	// is dropped the moment the file parses again, so a fixed file that
	// breaks a second time is reported a second time.
	warnedFiles sync.Map
}

// newClientFactory returns a factory that builds clients according to cfg.
//
// logger defaults to slog.Default(); New overwrites it with the adapter's own
// scoped logger once one is available, so a factory built directly in a test
// still has somewhere to log without every test needing to supply one.
func newClientFactory(cfg Config) *clientFactory {
	return &clientFactory{
		cfg:           cfg.withDefaults(),
		logger:        slog.Default(),
		clients:       make(map[domain.ClusterID]*clients),
		mapperBuilder: buildDiscoveryRESTMapper,
	}
}

// buildDiscoveryRESTMapper is the production mapperBuilder: it walks every
// API group and version the cluster's discovery endpoint reports and builds
// a RESTMapper from the result — the same mechanism `kubectl` itself uses to
// turn a Kind into the REST resource it lives at.
func buildDiscoveryRESTMapper(disco discovery.DiscoveryInterface) (meta.RESTMapper, error) {
	groupResources, err := restmapper.GetAPIGroupResources(disco)
	if err != nil {
		return nil, err
	}
	return restmapper.NewDiscoveryRESTMapper(groupResources), nil
}

// restMapper returns the cached RESTMapper for set, building it on first use.
func (f *clientFactory) restMapper(id domain.ClusterID, set *clients) (meta.RESTMapper, error) {
	set.restMapperMu.RLock()
	mapper := set.restMapper
	set.restMapperMu.RUnlock()
	if mapper != nil {
		return mapper, nil
	}
	return f.rebuildRESTMapper(id, set)
}

// rebuildRESTMapper re-queries discovery and replaces set's cached mapper.
//
// Called on first use and, from restMappingFor in apply.go, exactly once
// more when a lookup reports meta.NoKindMatchError: a CRD registered after
// the mapper was built is invisible to it until discovery is asked again,
// and an operator applying a manifest for a CRD that was installed a minute
// ago must not have to reconnect the cluster first.
func (f *clientFactory) rebuildRESTMapper(id domain.ClusterID, set *clients) (meta.RESTMapper, error) {
	set.restMapperMu.Lock()
	defer set.restMapperMu.Unlock()

	mapper, err := f.mapperBuilder(set.discovery)
	if err != nil {
		return nil, fmt.Errorf("discovering API resources for %q: %w", id, err)
	}
	set.restMapper = mapper
	return mapper, nil
}

// awaitEnv blocks until the process environment is settled.
//
// Returns immediately once the channel is closed, which it is for every
// connection after the first, and immediately when nothing is resolving the
// environment at all.
func (f *clientFactory) awaitEnv() {
	if f.cfg.EnvReady == nil {
		return
	}
	<-f.cfg.EnvReady
}

// loadingRules returns the kubeconfig loading rules shared by rawConfig and
// restConfig.
//
// Built directly rather than through genericclioptions.ConfigFlags (the
// package's own config_flags.go used to do exactly that): routing
// KubeconfigPath through ConfigFlags.KubeConfig sets
// clientcmd.ClientConfigLoadingRules.ExplicitPath, and
// (*ClientConfigLoadingRules).Load ignores Precedence ENTIRELY once
// ExplicitPath is set — which would silently drop every file
// PODSTEER_KUBECONFIG_DIR names whenever PODSTEER_KUBECONFIG is also set.
// Building Precedence ourselves is what lets both be read through the one
// merge.
//
// Precedence[0] is always KubeconfigPath, or — when that is unset — whatever
// clientcmd.NewDefaultClientConfigLoadingRules already resolved from
// $KUBECONFIG (itself possibly a path list) or ~/.kube/config. Directory
// files are appended AFTER, sorted by filename, so a context name one of them
// shares with anything already in Precedence never wins: client-go's merge
// keeps the first file's definition of a map key (confirmed empirically —
// see kubeconfig_dir_test.go — because the doc comment on
// (*ClientConfigLoadingRules).Load is easy to misread against the generic
// merge() it now delegates to).
func (f *clientFactory) loadingRules() *clientcmd.ClientConfigLoadingRules {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if f.cfg.KubeconfigPath != "" {
		// An explicit override REPLACES the default chain rather than adding
		// to it — the same "this is the one file" meaning it has always had —
		// so it, not the untouched default Precedence, is what the directory
		// is appended after.
		rules.Precedence = []string{f.cfg.KubeconfigPath}
	}
	rules.Precedence = append(rules.Precedence, f.kubeconfigDirFiles()...)
	rules.Precedence = append(rules.Precedence, f.sourceFiles()...)
	return rules
}

// sourceFiles returns the files the operator's own settings sources
// contribute, in list order.
//
// LAST, AFTER THE ENVIRONMENT, ALWAYS. Three reasons, and the first is the one
// that makes it structural rather than a preference:
//
//   - client-go's merge keeps the FIRST file's definition of a context name.
//     Appending here means an in-app source can never shadow a context the
//     machine's own configuration already provided — the operator's kubeconfig
//     keeps winning, whatever they add in the interface.
//   - The one write PodSteer makes to a kubeconfig goes to Precedence[0].
//     A source can never be first, so a source can never be written to, so
//     there is no "write here" flag to offer and no way to ask for one.
//   - A packager's or an enterprise's environment variable beats the UI, the
//     same precedence PODSTEER_UPDATE_CHECK=false already has over the toggle
//     beside it.
//
// A DIRECTORY SOURCE IS SCANNED BY THE SAME FUNCTION the environment's
// directory is — kubeconfigFilesIn, which is kubeconfigDirFiles generalised to
// take a path — so the skip rules cannot drift between the two: dotfiles,
// subdirectories, non-regular files and anything that does not parse as a
// kubeconfig are excluded identically wherever the folder came from.
func (f *clientFactory) sourceFiles() []string {
	if f.cfg.Sources == nil {
		return nil
	}

	var files []string
	for _, source := range f.cfg.Sources() {
		switch source.Kind {
		case domain.SourceDirectory:
			files = append(files, f.kubeconfigFilesIn(source.Path)...)
		default:
			// A file is taken at its word rather than parsed first. A listed
			// path that has gone missing, or that is temporarily unreadable
			// while something syncs it, stays in the precedence list where
			// client-go skips it — the same leniency the loading rules
			// already show a missing ~/.kube/config — and the settings pane
			// reports it as missing rather than the list quietly shrinking.
			files = append(files, source.Path)
		}
	}
	return files
}

// KubeconfigFiles reports the kubeconfig files this adapter reads, in
// precedence order.
//
// Exists so the local terminal can hand a shell the SAME KUBECONFIG PodSteer
// itself uses — the explicit override or the default chain, plus every file
// the kubeconfig directory contributes — rather than a second, hand-built
// answer that would drift the moment either resolution changed. One
// implementation of "which files", quoted in two places.
//
// Re-resolved on every call, like everything else about the kubeconfig here: a
// file dropped into the directory appears in the next shell without a restart.
// The paths are the operator's own; nothing is copied and nothing is written.
func (f *clientFactory) KubeconfigFiles() []string {
	return f.loadingRules().Precedence
}

// clientConfig returns a client-go ClientConfig scoped to id's context, or to
// whatever current-context the merged kubeconfig itself names when id is
// zero.
func (f *clientFactory) clientConfig(id domain.ClusterID) clientcmd.ClientConfig {
	overrides := &clientcmd.ConfigOverrides{ClusterDefaults: clientcmd.ClusterDefaults}
	if !id.IsZero() {
		overrides.CurrentContext = id.String()
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(f.loadingRules(), overrides)
}

// kubeconfigDirFiles returns the kubeconfig files found directly inside
// KubeconfigDir, sorted by filename, for appending to the loading precedence.
//
// RE-SCANNED ON EVERY CALL, consistent with the kubeconfig itself never being
// cached (see Clusters' doc comment): this runs only when the cluster picker
// opens or a client is (re)built, the directory holds a handful of files at
// most, and re-scanning is what makes a file dropped into the folder appear
// without restarting PodSteer.
//
// A directory that does not exist is the ordinary state of a machine that has
// not set PODSTEER_KUBECONFIG_DIR up, and is not logged. One that exists but
// cannot be listed is logged once per directory rather than on every call —
// warnedDirs — because the cluster picker and every connection attempt would
// otherwise repeat the same fact for the same unchanging reason.
//
// Dotfiles, subdirectories, and anything that is not a regular file after
// following at most one symlink hop (the shape a synced folder or a password
// manager's export leaves behind) are skipped without comment: those are
// ordinary directory contents, not malformed kubeconfigs. A file that IS
// considered but fails to parse as one is skipped and logged at warn, naming
// only its path — never its contents — the same discipline Clusters already
// applies to one bad context inside a single file.
func (f *clientFactory) kubeconfigDirFiles() []string {
	return f.kubeconfigFilesIn(f.cfg.KubeconfigDir)
}

// kubeconfigFilesIn is the scan itself, over any directory.
//
// Split out of kubeconfigDirFiles so an in-app folder source and
// PODSTEER_KUBECONFIG_DIR are scanned by ONE function rather than two that
// agree today. Everything the doc comment above describes — the skips, the
// sort, the once-per-path warning — applies to both, because it is this code
// in both cases.
func (f *clientFactory) kubeconfigFilesIn(dir string) []string {
	if dir == "" {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			if _, already := f.warnedDirs.LoadOrStore(dir, struct{}{}); !already {
				f.logger.Warn("kubeconfig directory cannot be listed",
					slog.String("path", dir), slog.String("error", err.Error()))
			}
		}
		return nil
	}
	f.warnedDirs.Delete(dir)

	candidates := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}

		path := filepath.Join(dir, name)
		// os.Stat follows a symlink to its target — the one hop a synced
		// folder or a password manager's export needs. A symlink to a
		// directory is excluded the same way a plain subdirectory is, by the
		// IsDir check below; a broken link is excluded by the error check.
		info, err := os.Stat(path)
		if err != nil || info.IsDir() || !info.Mode().IsRegular() {
			continue
		}
		candidates = append(candidates, path)
	}
	sort.Strings(candidates)

	files := make([]string, 0, len(candidates))
	for _, path := range candidates {
		if _, err := clientcmd.LoadFromFile(path); err != nil {
			if _, already := f.warnedFiles.LoadOrStore(path, struct{}{}); !already {
				f.logger.Warn("skipping unparsable file in the kubeconfig directory",
					slog.String("path", path), slog.String("error", err.Error()))
			}
			continue
		}
		f.warnedFiles.Delete(path)
		files = append(files, path)
	}
	return files
}

// rawConfig returns the parsed kubeconfig, merged across $KUBECONFIG entries
// and PODSTEER_KUBECONFIG_DIR exactly as loadingRules orders them.
func (f *clientFactory) rawConfig() (clientcmdapi.Config, error) {
	loader := f.clientConfig(domain.ClusterID(""))

	raw, err := loader.RawConfig()
	if err != nil {
		// Say so when the file is merely unreadable, before reporting it as
		// unparseable. On macOS this is the difference between "kubeconfig
		// unavailable" and the one sentence that explains the permission
		// dialog the operator just saw.
		if hint := kubeconfigPermissionHint(f.kubeconfigPath(loader)); hint != "" {
			return clientcmdapi.Config{}, fmt.Errorf("reading kubeconfig: %w: %s",
				ports.ErrKubeconfigUnavailable, hint)
		}
		return clientcmdapi.Config{}, fmt.Errorf("reading kubeconfig: %w: %w",
			ports.ErrKubeconfigUnavailable, err)
	}
	return raw, nil
}

// kubeconfigPath returns the file a permission failure would be about.
//
// The configured override wins; otherwise it is the first entry of the
// loader's precedence list, which is $KUBECONFIG's first path or
// ~/.kube/config. Only the first is named: a merge list that fails on its
// second entry is rare, and naming every candidate would bury the one that
// matters.
func (f *clientFactory) kubeconfigPath(loader clientcmd.ClientConfig) string {
	if f.cfg.KubeconfigPath != "" {
		return f.cfg.KubeconfigPath
	}
	if access, ok := loader.ConfigAccess().(*clientcmd.ClientConfigLoadingRules); ok {
		if len(access.Precedence) > 0 {
			return access.Precedence[0]
		}
	}
	return clientcmd.NewDefaultClientConfigLoadingRules().GetDefaultFilename()
}

// restConfig builds a tuned REST configuration for one cluster.
func (f *clientFactory) restConfig(id domain.ClusterID) (*rest.Config, error) {
	cfg, err := f.clientConfig(id).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("building client config for %q: %w: %w",
			id, ports.ErrKubeconfigUnavailable, err)
	}

	cfg.QPS = f.cfg.QPS
	cfg.Burst = f.cfg.Burst
	cfg.UserAgent = f.cfg.UserAgent
	cfg.ContentType = protobufContentType
	cfg.AcceptContentTypes = acceptContentTypes

	// Timeout is deliberately left unset. It would apply to every request made
	// through this config, including the long-lived watches PodSteer will open
	// for live resource updates. Per-request deadlines belong on the context,
	// which the inbound adapter attaches.

	return cfg, nil
}

// clientsFor returns the cached client set for id, building it on first use.
func (f *clientFactory) clientsFor(id domain.ClusterID) (*clients, error) {
	if id.IsZero() {
		return nil, domain.ErrEmptyClusterID
	}

	f.mu.RLock()
	cached, ok := f.clients[id]
	f.mu.RUnlock()
	if ok {
		return cached, nil
	}

	// BEFORE THE LOCK, not inside it: this waits on the PATH probe, and
	// holding the write lock across it would serialise every other cluster
	// behind the first one to connect.
	f.awaitEnv()

	f.mu.Lock()
	defer f.mu.Unlock()

	// Re-check: another goroutine may have built this set while we waited for
	// the write lock.
	if cached, ok := f.clients[id]; ok {
		return cached, nil
	}

	cfg, err := f.restConfig(id)
	if err != nil {
		return nil, err
	}

	// Construction happens under the write lock, which serialises concurrent
	// first-connects to *different* clusters. That is intentional: it costs a
	// few hundred milliseconds once, and it stops a UI that opens several
	// tabs at once from spawning duplicate credential plugin processes.
	typed, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating client for %q: %w", id, err)
	}

	// The dynamic client speaks JSON only — protobuf has no representation for
	// unstructured objects — so it gets a config of its own rather than
	// inheriting the protobuf negotiation set in restConfig.
	dynamicConfig := rest.CopyConfig(cfg)
	dynamicConfig.ContentType = "application/json"
	dynamicConfig.AcceptContentTypes = "application/json"

	dyn, err := dynamic.NewForConfig(dynamicConfig)
	if err != nil {
		return nil, fmt.Errorf("creating dynamic client for %q: %w", id, err)
	}

	disco, err := discovery.NewDiscoveryClientForConfig(dynamicConfig)
	if err != nil {
		return nil, fmt.Errorf("creating discovery client for %q: %w", id, err)
	}

	metrics, err := metricsclient.NewForConfig(dynamicConfig)
	if err != nil {
		return nil, fmt.Errorf("creating metrics client for %q: %w", id, err)
	}

	meta, err := metadata.NewForConfig(rest.CopyConfig(cfg))
	if err != nil {
		return nil, fmt.Errorf("creating metadata client for %q: %w", id, err)
	}

	built := &clients{
		typed:     typed,
		dynamic:   dyn,
		discovery: disco,
		metrics:   metrics,
		meta:      meta,
		config:    dynamicConfig,
	}

	f.clients[id] = built
	return built, nil
}

// clientFor returns just the typed client, which most call sites need.
func (f *clientFactory) clientFor(id domain.ClusterID) (kubernetes.Interface, error) {
	set, err := f.clientsFor(id)
	if err != nil {
		return nil, err
	}
	return set.typed, nil
}

// invalidate drops the cached client for id, so the next call rebuilds it.
//
// Needed whenever the credentials behind a client may have changed — the
// kubeconfig was rewritten, or a token expired and the exec plugin must run
// again.
func (f *clientFactory) invalidate(id domain.ClusterID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.clients, id)
}
