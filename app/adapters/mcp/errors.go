package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// failureCode classifies why a tool could not answer.
//
// Deliberately the same vocabulary the frontend gets from
// app/adapters/wails/errors.go, and deliberately NOT the same code: this
// package must not import that adapter, and the two lists exist for different
// readers — that one branches a UI, this one is read by a model deciding what
// to do next. What matters is that both keep the distinctions the codebase
// keeps everywhere else, and the sharpest of those is a refusal being its own
// answer rather than an absence.
type failureCode string

const (
	// codeForbidden means the cluster's RBAC refused the read. NOT an empty
	// result: an agent handed an empty list reports that the cluster has no
	// such objects, which is a claim nothing checked.
	codeForbidden failureCode = "forbidden"
	// codeUnauthenticated means the credentials were rejected outright.
	codeUnauthenticated failureCode = "unauthenticated"
	// codeCredentialPlugin means the kubeconfig authenticates by running a
	// binary that is not on PATH. Its own code because the cluster was never
	// contacted, so retrying is pointless and the fix is on this machine.
	codeCredentialPlugin failureCode = "credential_plugin_missing"
	// codeUnreachable means the API server could not be contacted.
	codeUnreachable failureCode = "unreachable"
	// codeNotFound means the object or the kind does not exist.
	codeNotFound failureCode = "not_found"
	// codeClusterNotFound means the kubeconfig has no such context.
	codeClusterNotFound failureCode = "cluster_not_found"
	// codeKubeconfig means the kubeconfig itself could not be read.
	codeKubeconfig failureCode = "kubeconfig_unavailable"
	// codeMetricsUnavailable means the cluster serves no metrics API. An
	// ordinary condition rather than a fault — see domain.MetricsStatus.
	codeMetricsUnavailable failureCode = "metrics_unavailable"
	// codeInvalidInput means the arguments were unusable in a way the schema
	// could not catch: a namespace that is not a DNS label, a kind that is
	// not in this cluster's catalogue.
	codeInvalidInput failureCode = "invalid_input"
	// codeTimeout means the read did not finish inside the call's budget.
	codeTimeout failureCode = "timeout"
	// codeInternal is the fallback, and carries the message verbatim.
	codeInternal failureCode = "internal"
)

// failure is the body of a failed tool result.
type failure struct {
	Error   failureCode `json:"error"`
	Message string      `json:"message"`
}

// toolFailure renders err as a tool result the model can read and act on.
//
// A RESULT, NOT A PROTOCOL ERROR. The Model Context Protocol reserves
// JSON-RPC errors for a message that was wrong — an unknown tool, arguments
// that do not fit the schema — and asks that a tool which ran and could not
// answer report isError instead, precisely so the model sees why. A 403
// hidden in the agent's transport layer becomes "the tool failed", which is
// what an agent then tells the operator.
func toolFailure(err error) callResult {
	code, message := classify(err)

	body, marshalErr := json.MarshalIndent(failure{Error: code, Message: message}, "", "  ")
	if marshalErr != nil {
		// A struct of two strings cannot fail to encode; if it somehow does,
		// the refusal still has to reach the caller.
		body = []byte(fmt.Sprintf("%s: %s", code, message))
	}

	return callResult{Content: []content{{Type: "text", Text: string(body)}}, IsError: true}
}

// classify maps an internal error onto a code and a sentence.
//
// Order matters, exactly as it does in the Wails adapter: one error routinely
// wraps several sentinels, and a missing credential plugin arrives wrapped in
// whatever the caller was doing. Reported as unreachable it would send an
// agent to diagnose a network for a cluster that was never contacted.
func classify(err error) (failureCode, string) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return codeTimeout, "The cluster did not answer within the time allowed for one call."

	case errors.Is(err, ports.ErrCredentialPluginMissing):
		return codeCredentialPlugin, "This cluster authenticates by running a credential plugin that could not be found on PATH: " + err.Error()

	case errors.Is(err, ports.ErrForbidden):
		// The wrapped chain names the operation and the resource, which is
		// the whole diagnosis — "cannot list secrets in namespace x" tells
		// the model what permission is missing and lets it say so rather
		// than guess. It is the operator's own message about their own
		// cluster, on their own machine.
		return codeForbidden, "Refused by the cluster's RBAC (this is a refusal, not an empty result): " + err.Error()

	case errors.Is(err, ports.ErrUnauthenticated):
		return codeUnauthenticated, "The cluster rejected these credentials: " + err.Error()

	case errors.Is(err, domain.ErrClusterNotFound):
		return codeClusterNotFound, "No such context in this machine's kubeconfig. Call list_clusters for the names that exist."

	case errors.Is(err, domain.ErrClusterNotConnected), errors.Is(err, domain.ErrNoActiveCluster):
		// Reached only if a connect attempt was itself lost; every tool
		// connects on demand, so this is a bug rather than a state an agent
		// can recover from by connecting.
		return codeInternal, err.Error()

	case errors.Is(err, ports.ErrKubeconfigUnavailable):
		return codeKubeconfig, "This machine's kubeconfig could not be read: " + err.Error()

	case errors.Is(err, ports.ErrMetricsUnavailable):
		return codeMetricsUnavailable, "This cluster serves no metrics API, so usage figures are absent. Everything else was read normally."

	case errors.Is(err, ports.ErrNotFound):
		return codeNotFound, err.Error()

	case errors.Is(err, ports.ErrUnreachable):
		return codeUnreachable, "The cluster could not be reached: " + err.Error()

	case errors.Is(err, domain.ErrInvalidNamespaceName),
		errors.Is(err, domain.ErrInvalidResourceKind),
		errors.Is(err, domain.ErrEmptyResourceName),
		errors.Is(err, domain.ErrUnsupportedWorkloadKind),
		errors.Is(err, domain.ErrInvalidAccessRequest),
		errors.Is(err, domain.ErrInvalidRoleTarget),
		errors.Is(err, domain.ErrEmptyClusterID),
		errors.Is(err, domain.ErrInvalidClusterID):
		return codeInvalidInput, err.Error()

	default:
		return codeInternal, err.Error()
	}
}
