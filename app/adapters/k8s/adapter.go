package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	apiversion "k8s.io/apimachinery/pkg/version"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// cachedResourceVersion asks the API server to answer a LIST from its watch
// cache rather than with a quorum read from etcd.
//
// Every list here is a POLL: the UI re-reads the whole cluster on a timer,
// several clusters at once, and the assessment does it again. Quorum reads
// for that are a cost paid on the API server and on etcd for a guarantee
// nothing here needs — a dashboard redrawn seconds from now does not require
// consensus on the exact instant it describes.
//
// The trade is real and worth stating: the watch cache can be marginally
// behind, so an object deleted a moment ago may appear in one more refresh.
// In practice that is invisible — a deleting pod reports Terminating for
// seconds regardless — and it is undone by removing this one field.
const cachedResourceVersion = "0"

// Adapter is the driven adapter for Kubernetes.
//
// It satisfies every Kubernetes outbound port. They stay separate interfaces
// because they fail independently — kubeconfig discovery is local and keeps
// working when every cluster is down, metrics are optional and routinely
// absent — but a single implementation backs them, since they all need the
// same kubeconfig resolution and the same client cache.
type Adapter struct {
	factory *clientFactory
	logger  *slog.Logger
	// filesystems caches node disk sweeps, which are one request per node and
	// answer a question whose value moves in hours.
	filesystems filesystemCache
	// nodeList caches the node names the sweep fans out over, so it does not
	// re-LIST the set the assessment that triggered it just listed.
	nodeList nodeNameCache
	// forwards are the live port-forwards. Owned here rather than by a
	// service, because each one is a goroutine holding a socket and the thing
	// that must not happen is the record and the goroutine parting company.
	forwards portForwards
	// nodeShells are the live node shells. Owned here for the same reason as
	// forwards: each is a privileged pod PodSteer created, and the record of
	// it must never outlive or predecease the pod itself. See nodeshell.go.
	nodeShells nodeShells
	// backends caches metrics-backend discovery, which answers a question
	// whose value moves in days: a monitoring stack is installed once.
	backends backendCache
	// kubeState caches kube-state-metrics discovery — the same question at
	// the same cadence as backends, about a different thing. See kubestate.go.
	kubeState kubeStateCache
	// upgrades caches served API discovery and the writer scans found for
	// deprecated versions of it — see upgrade.go.
	upgrades upgradeCache
	// vulnerabilities caches what a scanner already running in the cluster
	// wrote, per cluster and namespace. Its whole point is being OFF the
	// refresh tick — see trivy.go.
	vulnerabilities vulnerabilityCache
	// watches mirror a cluster's pods locally, so a refresh reads memory
	// rather than the network. An optimisation: see watch.go, where the
	// governing sentence is that polling remains the truth.
	watches *watchManager
	// reads coalesce the whole-collection lists a refresh repeats. Unlike the
	// two caches above it holds nothing for long — it exists to stop the same
	// request leaving twice in one tick. See readcache.go.
	reads readCache
}

// Compile-time proof that the adapter satisfies every outbound port it claims.
var (
	_ ports.KubeconfigPort  = (*Adapter)(nil)
	_ ports.ClusterPort     = (*Adapter)(nil)
	_ ports.WorkloadPort    = (*Adapter)(nil)
	_ ports.EventPort       = (*Adapter)(nil)
	_ ports.MetricsPort     = (*Adapter)(nil)
	_ ports.ResourcePort    = (*Adapter)(nil)
	_ ports.RBACPort        = (*Adapter)(nil)
	_ ports.ManagementPort  = (*Adapter)(nil)
	_ ports.PortForwardPort = (*Adapter)(nil)
	_ ports.NodeShellPort   = (*Adapter)(nil)
	_ ports.InspectPort     = (*Adapter)(nil)
)

// New returns a Kubernetes adapter configured by cfg.
//
// It performs no I/O: the kubeconfig is read on demand and clients are built
// on first use, so a machine with no cluster configured still starts instantly
// instead of blocking the UI at launch.
func New(cfg Config, logger *slog.Logger) *Adapter {
	if logger == nil {
		logger = slog.Default()
	}
	scoped := logger.With(slog.String("adapter", "k8s"))

	factory := newClientFactory(cfg)
	factory.logger = scoped

	return &Adapter{
		factory:    factory,
		logger:     scoped,
		watches:    newWatchManager(cfg.LiveWatch, scoped, idleAfter, sweepEvery, recheckEvery),
		forwards:   portForwards{byID: make(map[string]*forwarder)},
		nodeShells: nodeShells{byID: make(map[string]domain.NodeShell)},
	}
}

// KubeconfigFiles reports the kubeconfig files this adapter reads, in
// precedence order, so a local shell can be given the same KUBECONFIG.
//
// Not part of any port: it is not a Kubernetes operation, it is this adapter
// saying which of the operator's files it is looking at. The composition root
// hands it to the local shell manager as a function.
func (a *Adapter) KubeconfigFiles() []string { return a.factory.KubeconfigFiles() }

// ServerVersion reaches the cluster's API server and reports its version.
func (a *Adapter) ServerVersion(ctx context.Context, id domain.ClusterID) (domain.ServerVersion, error) {
	const op = "querying server version"

	client, err := a.factory.clientFor(id)
	if err != nil {
		return domain.ServerVersion{}, err
	}

	restClient := client.Discovery().RESTClient()
	if restClient == nil {
		// Only a fake or stubbed clientset lands here. Fall back to the
		// context-unaware call rather than failing outright.
		info, err := client.Discovery().ServerVersion()
		if err != nil {
			return domain.ServerVersion{}, classify(op, err)
		}
		return mapServerVersion(info), nil
	}

	// /version is one of the few endpoints with no protobuf representation, so
	// the Accept header is pinned to JSON. Without this the request inherits
	// the protobuf-first negotiation set in restConfig and can be refused.
	body, err := restClient.Get().
		AbsPath("/version").
		SetHeader("Accept", "application/json").
		DoRaw(ctx)
	if err != nil {
		return domain.ServerVersion{}, classify(op, err)
	}

	var info apiversion.Info
	if err := json.Unmarshal(body, &info); err != nil {
		return domain.ServerVersion{}, fmt.Errorf("%s: decoding response: %w", op, err)
	}

	return mapServerVersion(&info), nil
}

// Invalidate drops the cached clients for id.
//
// Exposed beyond the ports so the composition root can react to a kubeconfig
// change on disk, and so disconnecting a cluster genuinely releases its
// connections rather than leaving them pooled.
func (a *Adapter) Invalidate(id domain.ClusterID) {
	// THE CLIENT GOES FIRST, AND THE ORDER IS LOAD-BEARING. A read racing
	// this call can re-`ensure` a watch set at any point, so the invalidation
	// has to happen while `forget` is still ahead of it: the racing read gets
	// a rebuilt client, and `forget` then destroys whatever set exists.
	//
	// Reversed — forget first, factory after — a racing read would ensure a
	// set against the still-cached STALE client, and the reconnect's own
	// `ensure` would find it already running and keep it. A reflector bound
	// to the old server or the old token would then serve reads indefinitely,
	// which is exactly the resurrection a set is never reused to prevent.
	a.factory.invalidate(id)
	a.nodeList.forget(id)
	// The watch is waited for, not merely signalled: its reflectors hold the
	// client just discarded.
	a.watches.forget(id)
	// The disk sweep goes with them. Its whole value is being a minute stale
	// rather than ten seconds stale, and carrying that across a reconnect
	// would answer the first assessment of a freshly opened cluster with
	// numbers from before it was closed.
	a.filesystems.forget(id)
	a.upgrades.forget(id)
	// kube-state-metrics discovery too, and for a sharper reason than the
	// sweep: a tab is routinely reconnected because its kubeconfig context
	// now points at a different cluster, and a half-hour answer carried
	// across that would name a namespace in the cluster this tab used to be.
	a.kubeState.forget(id)
	// Scanner reports go the same way, and for the same reason as the disk
	// sweep: a ten-minute answer carried across a reconnect would put the
	// previous connection's findings on the first pod list of the new one.
	a.vulnerabilities.forget(id)
	a.reads.forget(id.String())
}

// StopAllWatches tears down every cluster's watch, for shutdown.
//
// Beside StopAllPortForwards in the composition root and for the same reason:
// these are goroutines holding sockets, and the thing that must not happen is
// the record and the goroutine parting company.
func (a *Adapter) StopAllWatches() {
	a.watches.stopAll()
}

// forgetReads drops the coalesced lists for one cluster.
//
// Called after every write. Two seconds is short, but it is long enough to
// hand back the list a pod was just deleted from — which reads as the
// application ignoring what it was told to do rather than as a stale cache.
func (a *Adapter) forgetReads(id domain.ClusterID) {
	a.reads.forget(id.String())
}
