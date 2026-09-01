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
	// backends caches metrics-backend discovery, which answers a question
	// whose value moves in days: a monitoring stack is installed once.
	backends backendCache
}

// Compile-time proof that the adapter satisfies every outbound port it claims.
var (
	_ ports.KubeconfigPort  = (*Adapter)(nil)
	_ ports.ClusterPort     = (*Adapter)(nil)
	_ ports.WorkloadPort    = (*Adapter)(nil)
	_ ports.EventPort       = (*Adapter)(nil)
	_ ports.MetricsPort     = (*Adapter)(nil)
	_ ports.ResourcePort    = (*Adapter)(nil)
	_ ports.ManagementPort  = (*Adapter)(nil)
	_ ports.PortForwardPort = (*Adapter)(nil)
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
	return &Adapter{
		factory:  newClientFactory(cfg),
		logger:   logger.With(slog.String("adapter", "k8s")),
		forwards: portForwards{byID: make(map[string]*forwarder)},
	}
}

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
	a.factory.invalidate(id)
	a.nodeList.forget(id)
	// The disk sweep goes with them. Its whole value is being a minute stale
	// rather than ten seconds stale, and carrying that across a reconnect
	// would answer the first assessment of a freshly opened cluster with
	// numbers from before it was closed.
	a.filesystems.forget(id)
}
