package ports

import "errors"

// Sentinel errors that outbound adapters map their infrastructure failures
// onto.
//
// They exist so the application layer can react to *why* a call failed without
// importing client-go to inspect a *k8serrors.StatusError. "Your token
// expired" and "the API server is down" call for different UI, but only the
// adapter is in a position to tell them apart — so the adapter classifies, and
// everything inward compares with errors.Is.
//
// Adapters must wrap rather than replace the underlying error, so the original
// cause survives for logging.
var (
	// ErrUnreachable means the API server could not be contacted at all:
	// DNS failure, refused connection, timeout, or a dead tunnel.
	ErrUnreachable = errors.New("cluster unreachable")

	// ErrUnauthenticated means the credentials were rejected (HTTP 401),
	// typically an expired token or a stale exec-plugin cache.
	ErrUnauthenticated = errors.New("not authenticated")

	// ErrForbidden means the credentials were accepted but RBAC denied the
	// operation (HTTP 403).
	ErrForbidden = errors.New("forbidden")

	// ErrNotFound means the requested resource does not exist (HTTP 404).
	ErrNotFound = errors.New("resource not found")

	// ErrKubeconfigUnavailable means the local kubeconfig could not be read
	// or parsed, so no cluster can be discovered.
	ErrKubeconfigUnavailable = errors.New("kubeconfig unavailable")
)
