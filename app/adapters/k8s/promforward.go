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
// pod behind the backend's Service.
func (a *Adapter) queryThroughForward(
	ctx context.Context,
	id domain.ClusterID,
	backend domain.MetricsBackend,
	path string,
	params map[string]string,
	mesh string,
) ([]byte, error) {
	op := fmt.Sprintf("querying %s in %q through a port-forward", backend.Describe(), id)

	target, err := a.ServiceForwardTarget(ctx, id, backend.Namespace, backend.Service, backend.Port)
	if err != nil {
		return nil, forwardFailure(op, mesh, err)
	}

	forward, err := a.StartPortForward(ctx, id, backend.Namespace, target.Pod, target.PodUID,
		0, target.ContainerPort, target.PortName, "TCP", nil)
	if err != nil {
		return nil, forwardFailure(op, mesh, err)
	}
	// TORN DOWN WHATEVER HAPPENS NEXT, and StopPortForward waits, so the
	// local port is released before this returns.
	defer func() { _ = a.StopPortForward(forward.ID) }()

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
