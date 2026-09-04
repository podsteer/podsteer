package wails

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
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
	// CodeCredentialPlugin means the kubeconfig authenticates through an
	// executable that is not on PATH. Its own code rather than unreachable,
	// because the cluster was never contacted and offering Retry would repeat
	// a failure nothing about the cluster can fix.
	CodeCredentialPlugin ErrorCode = "credential_plugin_missing"
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

// credentialPluginMessage explains a credential plugin that could not be run.
//
// NAMES THE BINARY, because that is the entire diagnosis and the operator
// cannot get at the log. "Could not authenticate" would send somebody to
// re-run `aws sso login`, which would succeed and change nothing.
//
// The second sentence exists because the commonest form of this is not a
// missing tool at all: the operator has `aws` and uses it daily, and PodSteer
// simply cannot see it. A desktop application launched from Finder or by
// Homebrew inherits launchd's PATH, not a shell's. PodSteer asks the login
// shell at startup to avoid exactly this, so reaching here means that did not
// work — a shell that never finished, or a tool genuinely absent.
func credentialPluginMessage(err error) string {
	name := "a credential plugin"
	if quoted := quotedName(err.Error()); quoted != "" {
		name = quoted
	}

	return fmt.Sprintf(
		"This cluster authenticates by running %s, which PodSteer could not find. "+
			"If it works in your terminal, PodSteer is starting without your shell's PATH — "+
			"launching it from a terminal confirms that. Otherwise the tool needs installing.",
		name)
}

// quotedName pulls the binary name out of the message the adapter composed.
func quotedName(message string) string {
	start := strings.Index(message, `"`)
	if start < 0 {
		return ""
	}
	rest := message[start+1:]

	end := strings.Index(rest, `"`)
	if end <= 0 {
		return ""
	}
	return rest[:end]
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
		return CodeNoActiveCluster, "No cluster is connected yet"

	case errors.Is(err, domain.ErrClusterNotFound):
		// Reads as an accusation without the second half. The commonest cause
		// is not deletion but REPLACEMENT — a cluster rebuilt under a new
		// context name, or a kubeconfig regenerated by whatever provisions it
		// — and saying so turns a dead end into the next step.
		return CodeClusterNotFound, "That cluster is no longer in your kubeconfig. It may have been removed, renamed, or replaced when the cluster was rebuilt."

	// BEFORE THE TRANSPORT CASES. A missing credential plugin arrives wrapped
	// in whatever the caller was doing, and reported as unreachable it sends
	// somebody to check a VPN for a cluster that was never contacted.
	case errors.Is(err, ports.ErrCredentialPluginMissing):
		return CodeCredentialPlugin, credentialPluginMessage(err)

	case errors.Is(err, ports.ErrUnauthenticated):
		return CodeUnauthenticated, "Your credentials were rejected — they may have expired"

	case errors.Is(err, ports.ErrForbidden):
		return CodeForbidden, "Your account is not allowed to perform this operation"

	case errors.Is(err, ports.ErrNotFound):
		return CodeNotFound, "The requested resource no longer exists"

	// The three transport failures are ONE code and three messages. The code is
	// the category the UI branches on — including whether to offer Retry — and
	// splitting it would have meant changing that logic to say the same thing.
	// The message is where the diagnosis belongs.
	//
	// None of them tells the operator to "check your network". That advice fits
	// every one of these situations and helps in none of them, and it implies
	// they did something wrong when the commonest cause here is a cluster that
	// went away. Each message says what was observed and what it usually means,
	// and lets them draw the conclusion.
	case errors.Is(err, ports.ErrNameNotResolved):
		return CodeUnreachable, "The cluster's address could not be looked up — this machine may not be on a network that knows that name"

	case errors.Is(err, ports.ErrConnectionRefused):
		// Deliberately NOT a network suggestion. The packets arrived; routing
		// is fine. Sending somebody to check their connection here is sending
		// them to the one place the problem is not.
		return CodeUnreachable, "The cluster refused the connection — the API server may be stopped, or listening on a different port"

	case errors.Is(err, ports.ErrNoResponse):
		return CodeUnreachable, "The cluster did not respond — it may be offline, or reachable only from a network this machine is not on"

	case errors.Is(err, ports.ErrUnreachable):
		return CodeUnreachable, "The cluster could not be contacted"

	case errors.Is(err, ports.ErrKubeconfigUnavailable):
		return CodeKubeconfig, "Your kubeconfig could not be read"

	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return CodeCancelled, "The request was cancelled or timed out"

	case errors.Is(err, domain.ErrEmptyClusterID),
		errors.Is(err, domain.ErrInvalidNamespaceName),
		errors.Is(err, domain.ErrInvalidResourceKind),
		errors.Is(err, domain.ErrUnsupportedWorkloadKind),
		errors.Is(err, errInvalidURL),
		errors.Is(err, errNotFound):
		return CodeInvalidInput, err.Error()

	case errors.Is(err, domain.ErrClusterNotConnected):
		return CodeNoActiveCluster, "That cluster is no longer connected"

	default:
		return CodeInternal, "An unexpected error occurred"
	}
}
