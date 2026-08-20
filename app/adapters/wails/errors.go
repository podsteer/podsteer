package wails

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"podsteer/app/domain"
	"podsteer/app/ports"
)

// ErrorCode is the stable, machine-readable classification of a failed call.
//
// Wails serialises a returned error to the string produced by Error() and
// rejects the JavaScript promise with it, so there is no room for a structured
// error object on the wire. PodSteer therefore encodes the code as a bracketed
// prefix — "[forbidden] you are not allowed to list pods ..." — which the
// frontend parses back out (see web/src/lib/api/errors.ts).
//
// The prefix is a contract: the frontend branches on it to decide whether to
// offer a reconnect, a namespace change, or just a message.
type ErrorCode string

const (
	// CodeNoActiveCluster means no cluster is connected yet.
	CodeNoActiveCluster ErrorCode = "no_active_cluster"
	// CodeClusterNotFound means the kubeconfig has no such context.
	CodeClusterNotFound ErrorCode = "cluster_not_found"
	// CodeUnreachable means the API server could not be contacted.
	CodeUnreachable ErrorCode = "unreachable"
	// CodeUnauthenticated means the credentials were rejected.
	CodeUnauthenticated ErrorCode = "unauthenticated"
	// CodeForbidden means RBAC denied the operation.
	CodeForbidden ErrorCode = "forbidden"
	// CodeNotFound means the requested resource does not exist.
	CodeNotFound ErrorCode = "not_found"
	// CodeKubeconfig means the local kubeconfig could not be read.
	CodeKubeconfig ErrorCode = "kubeconfig_unavailable"
	// CodeCancelled means the call was cancelled or timed out.
	CodeCancelled ErrorCode = "cancelled"
	// CodeInvalidInput means the frontend sent an unusable argument.
	CodeInvalidInput ErrorCode = "invalid_input"
	// CodeInternal is the fallback for anything unclassified.
	CodeInternal ErrorCode = "internal"
)

// errInvalidURL is raised when the frontend asks the shell to open something
// that is not a plain http(s) address.
var errInvalidURL = errors.New("invalid URL")

// errNotFound is raised when the frontend asks for something the backend has
// no record of — a licence text whose id does not exist, say.
var errNotFound = errors.New("not found")

// apiError logs the full failure and returns the sanitised error the frontend
// receives.
//
// The split matters: the log keeps the entire wrapped chain, including the
// client-go detail an engineer needs, while the UI gets one sentence an
// operator can act on. Sending the raw chain to the frontend would put API
// server URLs and internal paths on screen for no benefit.
func apiError(logger *slog.Logger, op string, err error) error {
	code, message := classifyError(err)

	logger.Error("call failed",
		slog.String("op", op),
		slog.String("code", string(code)),
		slog.String("error", err.Error()))

	return fmt.Errorf("[%s] %s", code, message)
}

// classifyError maps an internal error onto a code and an operator-facing
// message.
//
// Order matters: the most specific conditions are tested first, because a
// single error routinely wraps several sentinels — a failed connect carries
// both "cluster unreachable" and the underlying network error.
func classifyError(err error) (ErrorCode, string) {
	switch {
	case errors.Is(err, domain.ErrNoActiveCluster):
		return CodeNoActiveCluster, "no cluster is connected yet"

	case errors.Is(err, domain.ErrClusterNotFound):
		return CodeClusterNotFound, "that cluster is not in your kubeconfig any more"

	case errors.Is(err, ports.ErrUnauthenticated):
		return CodeUnauthenticated, "your credentials were rejected — they may have expired"

	case errors.Is(err, ports.ErrForbidden):
		return CodeForbidden, "your account is not allowed to perform this operation"

	case errors.Is(err, ports.ErrNotFound):
		return CodeNotFound, "the requested resource no longer exists"

	case errors.Is(err, ports.ErrUnreachable):
		return CodeUnreachable, "the cluster did not respond — check your network or VPN"

	case errors.Is(err, ports.ErrKubeconfigUnavailable):
		return CodeKubeconfig, "your kubeconfig could not be read"

	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return CodeCancelled, "the request was cancelled or timed out"

	case errors.Is(err, domain.ErrEmptyClusterID),
		errors.Is(err, domain.ErrInvalidNamespaceName),
		errors.Is(err, domain.ErrInvalidResourceKind),
		errors.Is(err, errInvalidURL),
		errors.Is(err, errNotFound):
		return CodeInvalidInput, err.Error()

	case errors.Is(err, domain.ErrClusterNotConnected):
		return CodeNoActiveCluster, "that cluster is no longer connected"

	default:
		return CodeInternal, "an unexpected error occurred"
	}
}
