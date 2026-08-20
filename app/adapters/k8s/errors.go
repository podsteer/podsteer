package k8s

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"syscall"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/podsteer/podsteer/app/ports"
)

// classify turns a client-go failure into an application-level error.
//
// This is the other half of the anti-corruption layer: mapper.go keeps
// Kubernetes *types* out of the inner layers, classify keeps Kubernetes
// *failure modes* out. The original error is always wrapped alongside the
// sentinel — Go 1.20 multi-%w keeps both reachable through errors.Is — so
// logs stay diagnosable while callers still get a stable category to branch on.
//
// op names the operation for the message, e.g. `listing pods in "default"`.
func classify(op string, err error) error {
	if err == nil {
		return nil
	}

	// Cancellation is not a cluster problem; it is normally the operator
	// navigating away mid-request, and must not be reported as an outage.
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%s: %w", op, err)
	}

	switch {
	case apierrors.IsUnauthorized(err):
		return fmt.Errorf("%s: %w: %w", op, ports.ErrUnauthenticated, err)
	case apierrors.IsForbidden(err):
		return fmt.Errorf("%s: %w: %w", op, ports.ErrForbidden, err)
	case apierrors.IsNotFound(err):
		return fmt.Errorf("%s: %w: %w", op, ports.ErrNotFound, err)
	case apierrors.IsTimeout(err),
		apierrors.IsServerTimeout(err),
		apierrors.IsServiceUnavailable(err),
		apierrors.IsInternalError(err),
		isTransportFailure(err):
		return fmt.Errorf("%s: %w: %w", op, ports.ErrUnreachable, err)
	default:
		return fmt.Errorf("%s: %w", op, err)
	}
}

// isTransportFailure reports whether err is a network-level failure rather
// than a response from the API server.
//
// These are the everyday failures for a desktop client — laptop asleep, VPN
// down, port-forward closed, cluster deleted — and they arrive as plain Go
// network errors that carry no HTTP status for apierrors to inspect.
func isTransportFailure(err error) bool {
	// context.DeadlineExceeded satisfies net.Error, so it is caught below and
	// correctly reported as unreachable: from the operator's point of view a
	// cluster that did not answer in time is a cluster that did not answer.
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}

	return errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EHOSTUNREACH) ||
		errors.Is(err, syscall.ENETUNREACH)
}
