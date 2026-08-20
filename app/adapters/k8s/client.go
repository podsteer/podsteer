package k8s

import (
	"fmt"
	"sync"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
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

	// QPS is the sustained request rate allowed per cluster. Zero means
	// defaultQPS.
	QPS float32

	// Burst is the number of requests allowed to exceed QPS momentarily.
	// Zero means defaultBurst.
	Burst int

	// UserAgent identifies PodSteer to the API server. Empty means
	// defaultUserAgent.
	UserAgent string
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
	// config is retained for requests that bypass the typed clients, notably
	// the server-side table printing used by the generic browser.
	config *rest.Config
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
	cfg Config

	mu      sync.RWMutex
	clients map[domain.ClusterID]*clients
}

// newClientFactory returns a factory that builds clients according to cfg.
func newClientFactory(cfg Config) *clientFactory {
	return &clientFactory{
		cfg:     cfg.withDefaults(),
		clients: make(map[domain.ClusterID]*clients),
	}
}

// configFlags returns a cli-runtime loader scoped to one kubeconfig context.
//
// Persistent config is disabled: it would add cli-runtime's own on-disk
// discovery cache and a memoised client config, both of which duplicate the
// caching this factory already does — and the on-disk cache would keep serving
// a stale API surface after a cluster upgrade.
func (f *clientFactory) configFlags(id domain.ClusterID) *genericclioptions.ConfigFlags {
	flags := genericclioptions.NewConfigFlags(false)

	if f.cfg.KubeconfigPath != "" {
		path := f.cfg.KubeconfigPath
		flags.KubeConfig = &path
	}
	if !id.IsZero() {
		name := id.String()
		flags.Context = &name
	}

	return flags
}

// rawConfig returns the parsed kubeconfig, merged across $KUBECONFIG entries
// exactly as kubectl would merge them.
func (f *clientFactory) rawConfig() (clientcmdapi.Config, error) {
	raw, err := f.configFlags("").ToRawKubeConfigLoader().RawConfig()
	if err != nil {
		return clientcmdapi.Config{}, fmt.Errorf("reading kubeconfig: %w: %w",
			ports.ErrKubeconfigUnavailable, err)
	}
	return raw, nil
}

// restConfig builds a tuned REST configuration for one cluster.
func (f *clientFactory) restConfig(id domain.ClusterID) (*rest.Config, error) {
	cfg, err := f.configFlags(id).ToRESTConfig()
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

	built := &clients{
		typed:     typed,
		dynamic:   dyn,
		discovery: disco,
		metrics:   metrics,
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
