package k8s

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// The second transport to a monitoring backend: an ephemeral port-forward to
// its pod, used only when the backend itself refused the API server's proxy.
//
// WHY IT EXISTS. A meshed backend can sit behind the mesh's own authorization
// policy. linkerd-viz's Prometheus does (seen live on edge-26.9.3): its
// `prometheus-admin` policy admits only the metrics-api ServiceAccount, so a
// request the API server proxies to the pod IP is refused by the Linkerd proxy
// with a bare 403 — whatever the operator's Kubernetes permissions are. A
// port-forward reaches the container over the pod's own loopback, which the
// mesh does not intercept, so the same GET is answered.
//
// WHAT IT COSTS, stated because it is a second kind of request: it is the
// `create` verb on pods/portforward (plus reading the Service and listing its
// pods to find one), it lands in the audit log as such, and it opens a local
// port on 127.0.0.1 for the length of one query. It is opened through the same
// registry every forward lives in and torn down before the query returns —
// the reachability probe's discipline — and it is never kept.
//
// ONLY ON A 403 FROM THE BACKEND. A refusal by the API server (a Status body)
// is about the account, and a port-forward would only be a way around a
// permission somebody withheld; a 401 is a backend asking for a credential,
// which the pod's loopback does not carry either.

// queryThroughForward performs one query over an ephemeral port-forward to a
// pod behind the backend's Service — the batch's forward when the caller
// opened a batch, otherwise one opened and stopped for this query alone.
func (a *Adapter) queryThroughForward(
	ctx context.Context,
	id domain.ClusterID,
	backend domain.MetricsBackend,
	path string,
	params map[string]string,
	mesh string,
) ([]byte, error) {
	op := fmt.Sprintf("querying %s in %q through a port-forward", backend.Describe(), id)

	localPort, release, err := a.forwardFor(ctx, id, backend)
	if err != nil {
		return nil, forwardFailure(op, mesh, err)
	}
	// TORN DOWN WHATEVER HAPPENS NEXT when this query owns it, and
	// StopPortForward waits, so the local port is released before this
	// returns. A batch's forward is stopped by the batch.
	defer release()

	forward := domain.Forward{LocalPort: localPort}

	query := url.Values{}
	for key, value := range params {
		query.Set(key, value)
	}
	address := (&url.URL{
		Scheme:   "http",
		Host:     net.JoinHostPort("127.0.0.1", strconv.Itoa(forward.LocalPort)),
		Path:     backend.Prefix + path,
		RawQuery: query.Encode(),
	}).String()

	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	body, status, _, err := fetchBoundedWithHeader(ctx, loopbackClient, address, maxQueryResponseBytes)
	if err != nil {
		if errors.Is(err, ports.ErrMetricsQueryTooLarge) {
			return nil, fmt.Errorf("%s: %w", op, err)
		}
		return nil, fmt.Errorf("%s: %w: %s: %v", op, ports.ErrMetricsProxyRefused, mesh, err)
	}

	switch {
	case status >= 200 && status < 300:
		return body, nil
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		// Refused on the loopback too: this one wants a credential of its own.
		return nil, fmt.Errorf("%s: %w: it answered HTTP %d through the forward as well", op, ports.ErrMetricsBackendAuth, status)
	default:
		return nil, fmt.Errorf("%s: %w: %s", op, ports.ErrMetricsQueryRejected, promErrorMessage(status, body))
	}
}

// forwardFailure says why the fallback could not be made, naming the
// permission when that is the reason.
func forwardFailure(op, mesh string, err error) error {
	lower := strings.ToLower(err.Error())
	if errors.Is(err, ports.ErrForbidden) || strings.Contains(lower, "forbidden") {
		return fmt.Errorf("%s: %w: %s", op, ports.ErrMetricsForwardRefused, mesh)
	}
	return fmt.Errorf("%s: %w: %s; a port-forward to its pod was tried and failed: %v", op, ports.ErrMetricsProxyRefused, mesh, err)
}

// proxyRefusalCause names who refused the proxied request, as precisely as
// the answer allows.
//
// LINKERD BY ITS HEADER WHEN IT SURVIVES, AND BY WHAT WAS DISCOVERED WHEN IT
// DOES NOT. The Linkerd proxy marks its own refusals with `l5d-proxy-error`;
// the API server's proxy, in the run that found this, passed on a bare 403
// with an empty body and no such header, so a backend discovered as
// linkerd-viz's Prometheus is named as such.
func proxyRefusalCause(backend domain.MetricsBackend, header http.Header) string {
	switch {
	case header.Get("l5d-proxy-error") != "":
		return fmt.Sprintf("Linkerd's authorization policy refused the request (%s)", header.Get("l5d-proxy-error"))
	case backend.LinkerdViz:
		return "linkerd-viz's Prometheus sits behind Linkerd's authorization policy, which admits only its metrics-api"
	default:
		return "the backend refused the API server's proxy (HTTP 403)"
	}
}

// loopbackClient queries through a forward. NO PROXY: a request to
// 127.0.0.1 sent through HTTPS_PROXY would leave this machine.
var loopbackClient = &http.Client{Transport: &http.Transport{Proxy: nil}}

// forwardFor returns a local port onto one of the backend's pods, and what to
// call when this query is done with it.
func (a *Adapter) forwardFor(ctx context.Context, id domain.ClusterID, backend domain.MetricsBackend) (int, func(), error) {
	batch, _ := ctx.Value(queryBatchKey{}).(*queryBatch)
	if batch != nil {
		batch.mu.Lock()
		defer batch.mu.Unlock()
		key := string(id) + "\x00" + backendKey(backend)
		if forward, ok := batch.forwards[key]; ok && !batch.closed {
			return forward.LocalPort, func() {}, nil
		}
		if !batch.closed {
			forward, err := a.openBackendForward(ctx, id, backend)
			if err != nil {
				return 0, nil, err
			}
			batch.forwards[key] = forward
			return forward.LocalPort, func() {}, nil
		}
		// A batch already ended — a detached probe finishing after its
		// caller returned — gets a forward of its own below.
	}

	forward, err := a.openBackendForward(ctx, id, backend)
	if err != nil {
		return 0, nil, err
	}
	return forward.LocalPort, func() { _ = a.StopPortForward(forward.ID) }, nil
}

// openBackendForward resolves a ready pod behind the backend's Service and
// opens an INTERNAL forward to it.
func (a *Adapter) openBackendForward(ctx context.Context, id domain.ClusterID, backend domain.MetricsBackend) (domain.Forward, error) {
	target, err := a.ServiceForwardTarget(ctx, id, backend.Namespace, backend.Service, backend.Port)
	if err != nil {
		return domain.Forward{}, err
	}
	return a.startInternalForward(id, backend.Namespace, target.Pod, target.PodUID, target.ContainerPort, target.PortName)
}

// queryBatchKey carries a *queryBatch on a context.
type queryBatchKey struct{}

// queryBatch is the forwards one batch of queries shares.
type queryBatch struct {
	mu       sync.Mutex
	closed   bool
	forwards map[string]domain.Forward
}

// BeginQueryBatch marks a run of queries made for one gesture — a source
// probe and a traffic layer's expressions — so a backend that has to be
// reached through a port-forward is reached through ONE, opened on first use
// and stopped by end. See ports.TrafficQueryPort.
//
// end MUST be called, and stops every forward the batch opened; a query
// arriving after it opens and stops its own.
func (a *Adapter) BeginQueryBatch(ctx context.Context) (context.Context, func()) {
	batch := &queryBatch{forwards: make(map[string]domain.Forward, 1)}
	end := func() {
		batch.mu.Lock()
		batch.closed = true
		forwards := batch.forwards
		batch.forwards = nil
		batch.mu.Unlock()
		for _, forward := range forwards {
			_ = a.StopPortForward(forward.ID)
		}
	}
	return context.WithValue(ctx, queryBatchKey{}, batch), end
}

// forwardRoutes remembers which backends refused the proxy, so the next query
// goes straight to the forward instead of earning another 403 — the
// queryRefusals discipline, per backend and connection generation.
type forwardRoutes struct {
	mu      sync.Mutex
	entries map[domain.ClusterID]forwardRoute
}

type forwardRoute struct {
	at         time.Time
	generation uint64
	backend    string
}

func (f *forwardRoutes) uses(id domain.ClusterID, generation uint64, backend domain.MetricsBackend) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry, ok := f.entries[id]
	return ok && entry.generation == generation && entry.backend == backendKey(backend) &&
		time.Since(entry.at) <= queryRefusalTTL
}

func (f *forwardRoutes) remember(id domain.ClusterID, generation uint64, backend domain.MetricsBackend) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.entries == nil {
		f.entries = make(map[domain.ClusterID]forwardRoute, 2)
	}
	f.entries[id] = forwardRoute{at: time.Now(), generation: generation, backend: backendKey(backend)}
}

func (f *forwardRoutes) forget(id domain.ClusterID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.entries, id)
}

func backendKey(backend domain.MetricsBackend) string {
	return string(backend.Namespace) + "/" + backend.Service
}
