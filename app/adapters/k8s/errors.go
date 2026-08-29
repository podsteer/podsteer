package k8s

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
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
