package k8s

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	// BEFORE THE STATUS CHECKS, because this failure never reached the API
	// server: client-go runs the credential plugin while building the request,
	// so a missing binary surfaces as a plain exec error wrapped in whatever
	// the caller was doing. Left to the default branch it becomes an opaque
	// "executable file not found in $PATH" attached to an operation name.
	if binary := missingCredentialPlugin(err); binary != "" {
		return fmt.Errorf("%s: %w: %q is not on PATH: %w",
			op, ports.ErrCredentialPluginMissing, binary, err)
	}

	// BESIDE IT, AND FOR A NEARBY REASON. The kubeconfig names an
	// `auth-provider` this binary never registered, so nothing is ever
	// dialled and, left to the default branch, it reads as a bug in PodSteer
	// rather than as a kubeconfig that needs converting.
	//
	// IT IS RAISED WHILE BUILDING THE CLIENT, NOT THE REQUEST — client-go
	// resolves the provider in rest.TransportConfig, which runs inside
	// kubernetes.NewForConfig — which is why clientsFor now classifies its
	// own construction error. It did not, and this branch was therefore
	// unreachable on the only path that produces the failure. The credential
	// plugin above is genuinely request-time by comparison: the binary is
	// resolved when a credential is first fetched.
	// See ports.ErrLegacyAuthProvider.
	if provider := legacyAuthProvider(err); provider != "" {
		return fmt.Errorf("%s: %w: %q: %w", op, ports.ErrLegacyAuthProvider, provider, err)
	}

	switch {
	case apierrors.IsUnauthorized(err):
		return fmt.Errorf("%s: %w: %w", op, ports.ErrUnauthenticated, err)
	case apierrors.IsForbidden(err):
		return fmt.Errorf("%s: %w: %w", op, ports.ErrForbidden, err)
	case apierrors.IsNotFound(err):
		return fmt.Errorf("%s: %w: %w", op, ports.ErrNotFound, err)
	case apierrors.IsConflict(err):
		// The one 409 PodSteer expects: UpdateResource's PUT carried a
		// resourceVersion the server no longer recognises, because the
		// object changed since the manifest was read. Its own sentinel
		// rather than falling through to the opaque default case, because
		// the recovery — reload, then re-apply — is specific to this failure
		// and nothing else here produces it.
		return fmt.Errorf("%s: %w: %w", op, ports.ErrConflict, err)
	case apierrors.IsInvalid(err):
		// The request reached the server and was well-formed enough to
		// route, but the OBJECT itself was declined — a schema violation, or
		// a ValidatingWebhookConfiguration saying no. Surfaces mainly
		// through UpdateResource's dry run, where the message is the reason
		// Validate exists: an operator needs it close to verbatim to fix
		// their manifest, which is why it is wrapped rather than replaced
		// the way most other cases here are.
		return fmt.Errorf("%s: %w: %w", op, ports.ErrManifestRejected, err)
	case apierrors.IsTooManyRequests(err):
		// A RATE LIMIT, unless the caller said otherwise. A 429 on a list or
		// a get is API Priority and Fairness (or a gateway) throttling this
		// account, and calling it a PodDisruptionBudget refusal sent operators
		// off to read budgets for a problem that was load. The one 429 that
		// IS a budget is the eviction subresource's, and only classifyEviction
		// maps it so. Retry-After is quoted when the server sent one.
		if seconds, ok := apierrors.SuggestsClientDelay(err); ok {
			return fmt.Errorf("%s: %w (retry after %ds): %w", op, ports.ErrThrottled, seconds, err)
		}
		return fmt.Errorf("%s: %w: %w", op, ports.ErrThrottled, err)
	case apierrors.IsTimeout(err),
		apierrors.IsServerTimeout(err),
		apierrors.IsServiceUnavailable(err):
		// The API server answered, just not usefully. There is no transport
		// diagnosis to add.
		return fmt.Errorf("%s: %w: %w: %w", op, ports.ErrUnreachable, ports.ErrNoResponse, err)
	case apierrors.IsInternalError(err):
		return fmt.Errorf("%s: %w: %w", op, ports.ErrUnreachable, err)
	default:
		// Transport failures carry a second sentinel naming WHICH one, because
		// they imply different actions. Both are wrapped, so callers that only
		// care that the cluster was unreachable are unaffected.
		if kind := transportFailure(err); kind != nil {
			return fmt.Errorf("%s: %w: %w: %w", op, ports.ErrUnreachable, kind, err)
		}
		return fmt.Errorf("%s: %w", op, err)
	}
}

// classifyEviction is classify for the eviction subresource, where a 429 is
// not a rate limit: it is a PodDisruptionBudget declining the request, which
// the request was permitted to make and the object's own policy refused.
// It gets a sentinel of its own rather than ErrForbidden, which would tell an
// operator to ask for credentials more credentials cannot fix.
func classifyEviction(op string, err error) error {
	if apierrors.IsTooManyRequests(err) {
		return fmt.Errorf("%s: %w: %w", op, ports.ErrDisruptionBudget, err)
	}
	return classify(op, err)
}

// missingCredentialPlugin names the executable a kubeconfig wanted and could
// not find, or "" when that is not what went wrong.
//
// Matched through exec.ErrNotFound rather than on the message text, so it
// holds whatever wording the standard library uses. The NAME still comes from
// the error string: exec.Error carries it in a field, but client-go wraps the
// failure several layers deep and does not always preserve the concrete type,
// so the typed path is tried first and the string is the fallback.
func missingCredentialPlugin(err error) string {
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return execErr.Name
	}

	if !errors.Is(err, exec.ErrNotFound) {
		return ""
	}

	// `exec: "aws": executable file not found in $PATH`
	message := err.Error()
	start := strings.Index(message, `exec: "`)
	if start < 0 {
		return "the credential plugin"
	}
	rest := message[start+len(`exec: "`):]

	end := strings.Index(rest, `"`)
	if end < 0 {
		return "the credential plugin"
	}
	return rest[:end]
}

// legacyAuthProvider names the `auth-provider` a kubeconfig asked for and this
// binary does not register, or "" when that is not what failed.
//
// MATCHED ON THE MESSAGE, because client-go offers nothing else: the failure
// is `fmt.Errorf("no Auth Provider found for name %q", name)` in
// client-go/rest/plugin.go, with no typed error and no sentinel to compare
// against. The prefix is stable across every release since the mechanism was
// deprecated in 1.22 and is what the provider name follows.
func legacyAuthProvider(err error) string {
	const marker = `no Auth Provider found for name `

	message := err.Error()
	start := strings.Index(message, marker)
	if start < 0 {
		return ""
	}

	rest := message[start+len(marker):]
	if len(rest) == 0 || rest[0] != '"' {
		// The shape changed. Still this failure, still worth naming as one —
		// the provider is simply unknown.
		return "that auth-provider"
	}

	end := strings.Index(rest[1:], `"`)
	if end <= 0 {
		return "that auth-provider"
	}
	return rest[1 : end+1]
}

// transportFailure names the network-level failure behind err, or nil when it
// is not one.
//
// These are the everyday failures for a desktop client — laptop asleep, VPN
// down, port-forward closed, cluster deleted — and they arrive as plain Go
// network errors that carry no HTTP status for apierrors to inspect.
//
// ORDER IS LOAD-BEARING. *net.DNSError satisfies net.Error, so testing the
// interface first would swallow every DNS failure into the generic case and
// the operator would be told nothing answered when in fact the name never
// resolved — which sends them to check a route to an address that was never
// looked up.
func transportFailure(err error) error {
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return ports.ErrNameNotResolved
	}

	if errors.Is(err, syscall.ECONNREFUSED) {
		return ports.ErrConnectionRefused
	}

	// Reset is grouped with refused rather than with silence: in both, the
	// packets arrived and something on the other end declined to serve them.
	if errors.Is(err, syscall.ECONNRESET) {
		return ports.ErrConnectionRefused
	}

	if errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) {
		return ports.ErrNoResponse
	}

	// context.DeadlineExceeded satisfies net.Error, so it is caught here and
	// correctly reported as no response: from the operator's point of view a
	// cluster that did not answer in time is a cluster that did not answer.
	if _, ok := errors.AsType[net.Error](err); ok {
		return ports.ErrNoResponse
	}

	// *url.Error wraps whatever the transport actually hit, so it is tested
	// last — by now anything more specific has already matched through it.
	if _, ok := errors.AsType[*url.Error](err); ok {
		return ports.ErrNoResponse
	}

	return nil
}

// kubeconfigPermissionHint explains a kubeconfig that exists but may not be
// read, or returns "" when that is not what went wrong.
//
// macOS gates Documents, Desktop, Downloads and network or removable volumes
// for EVERY process, sandboxed or not. A kubeconfig inside one of them is
// refused until the operator grants access — and `~/.kube` symlinked into such
// a folder is a common way to keep the file in a synced or backed-up
// directory, which means the refusal arrives for a path that does not look
// protected at all.
//
// Two things make the stock error useless here. It reports only that the file
// could not be read, and it names the path as written — so an operator whose
// ~/.kube points into Documents is told that ~/.kube/config failed, with
// nothing connecting that to the permission dialog they just dismissed.
// Resolving the symlink is the whole diagnosis.
//
// Diagnosed by opening the file rather than by unwrapping: client-go collects
// load failures into an aggregate that does not always preserve the chain
// errors.Is needs, and the question — may this process read this file — is one
// the filesystem answers directly.
func kubeconfigPermissionHint(path string) string {
	if path == "" {
		return ""
	}

	file, err := os.Open(path)
	if err == nil {
		_ = file.Close()
		return ""
	}
	if !errors.Is(err, fs.ErrPermission) {
		return ""
	}

	// Best effort: an unresolvable symlink is still worth reporting, just
	// without the extra detail.
	resolved := path
	if target, err := filepath.EvalSymlinks(path); err == nil {
		resolved = target
	}

	if resolved != path {
		return fmt.Sprintf(
			"the operating system denied access to %s, which %s points to. "+
				"Grant PodSteer access under System Settings → Privacy & Security → Files and Folders",
			resolved, path)
	}
	return fmt.Sprintf(
		"the operating system denied access to %s. "+
			"Grant PodSteer access under System Settings → Privacy & Security → Files and Folders",
		resolved)
}
