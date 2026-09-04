package wails

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/podsteer/podsteer/app/ports"
)

func TestClassifyErrorExplainsAMissingCredentialPlugin(t *testing.T) {
	err := fmt.Errorf("listing pods: %w: %q is not on PATH: %w",
		ports.ErrCredentialPluginMissing, "aws", &exec.Error{Name: "aws", Err: exec.ErrNotFound})

	code, message := classifyError(err)

	if code != CodeCredentialPlugin {
		t.Fatalf("code %q, want %q", code, CodeCredentialPlugin)
	}

	// The binary has to be named. Without it the operator is told
	// authentication failed and re-runs `aws sso login`, which succeeds and
	// changes nothing.
	if !strings.Contains(message, "aws") {
		t.Fatalf("message does not name the binary: %q", message)
	}

	// And it must not read as a cluster problem: the commonest cause is a tool
	// the operator has and PodSteer cannot see.
	if !strings.Contains(message, "PATH") {
		t.Fatalf("message does not mention PATH: %q", message)
	}
}

func TestClassifyErrorPrefersThePluginOverTheTransport(t *testing.T) {
	// A real chain carries several sentinels. The plugin one has to win: it is
	// the actionable half, and unreachable sends somebody to check a VPN.
	err := fmt.Errorf("connecting: %w: %w", ports.ErrCredentialPluginMissing, ports.ErrUnreachable)

	if code, _ := classifyError(err); code != CodeCredentialPlugin {
		t.Fatalf("code %q, want %q", code, CodeCredentialPlugin)
	}
}

func TestClassifyErrorDistinguishesADisruptionBudgetFromForbidden(t *testing.T) {
	// A PodDisruptionBudget refusal must read as its own code, not as
	// forbidden — the two send an operator to fix opposite things.
	err := fmt.Errorf("evicting pod: %w: some error", ports.ErrDisruptionBudget)

	code, message := classifyError(err)
	if code != CodeDisruptionBudget {
		t.Fatalf("code %q, want %q", code, CodeDisruptionBudget)
	}
	if !strings.Contains(message, "PodDisruptionBudget") {
		t.Fatalf("message does not mention a PodDisruptionBudget: %q", message)
	}

	// And must never be told apart as forbidden — the RBAC message would
	// send the operator to ask for a permission they already have.
	if forbiddenCode, _ := classifyError(ports.ErrForbidden); forbiddenCode == code {
		t.Fatal("CodeDisruptionBudget must differ from CodeForbidden")
	}
}

// TestClassifyErrorReadOnlyIsNotForbidden pins the reason CodeReadOnly is its
// own code: CodeForbidden's message sends somebody to a cluster administrator,
// and a read-only refusal has no administrator to ask — the fix is under
// Organise, on this machine, so the two must never collapse into one code.
func TestClassifyErrorReadOnlyIsNotForbidden(t *testing.T) {
	err := fmt.Errorf("deleting resource: cluster %q: %w", "prod", ports.ErrReadOnly)

	code, message := classifyError(err)

	if code != CodeReadOnly {
		t.Fatalf("code %q, want %q", code, CodeReadOnly)
	}
	if code == CodeForbidden {
		t.Fatal("a read-only refusal must never classify as forbidden")
	}
	if !strings.Contains(message, "read-only") {
		t.Fatalf("message does not say the cluster is read-only: %q", message)
	}
	if !strings.Contains(message, "Organise") {
		t.Fatalf("message does not point at where to change it: %q", message)
	}
}

// TestClassifyErrorConflictIsNotForbidden pins the reason CodeConflict is its
// own code: a stale resourceVersion is a normal outcome of optimistic
// concurrency, not RBAC, and the fix is reloading the object — never
// re-requesting different credentials the way CodeForbidden's message
// implies.
func TestClassifyErrorConflictIsNotForbidden(t *testing.T) {
	err := fmt.Errorf("applying resource: %w: some error", ports.ErrConflict)

	code, message := classifyError(err)

	if code != CodeConflict {
		t.Fatalf("code %q, want %q", code, CodeConflict)
	}
	if code == CodeForbidden {
		t.Fatal("a conflict must never classify as forbidden")
	}
	if !strings.Contains(message, "Reload") {
		t.Fatalf("message does not point at reloading the object: %q", message)
	}
}

// TestClassifyErrorManifestRejectedShowsTheServerMessageVerbatim pins the
// reason ErrManifestRejected reuses CodeInvalidInput's err.Error() message
// rather than a paraphrase: Validate exists specifically to hand an operator
// the API server's own diagnosis of what is wrong with their manifest, and
// "An unexpected error occurred" (CodeInternal's fallback) would defeat that.
func TestClassifyErrorManifestRejectedShowsTheServerMessageVerbatim(t *testing.T) {
	err := fmt.Errorf("applying resource: %w: Deployment.apps \"web\" is invalid: spec.replicas: Invalid value",
		ports.ErrManifestRejected)

	code, message := classifyError(err)

	if code != CodeInvalidInput {
		t.Fatalf("code %q, want %q", code, CodeInvalidInput)
	}
	if !strings.Contains(message, "spec.replicas") {
		t.Fatalf("message does not carry the server's own diagnosis verbatim: %q", message)
	}
}

// TestClassifyErrorEphemeralUnsupportedIsNotNotFound pins the reason
// CodeEphemeralUnsupported is its own code: the pod is present and the
// subresource is what is missing, so classifying it as not_found would send an
// operator to look for a pod that exists.
func TestClassifyErrorEphemeralUnsupportedIsNotNotFound(t *testing.T) {
	err := fmt.Errorf("adding ephemeral container to pod %q: %w", "web-0", ports.ErrEphemeralContainersUnsupported)

	code, message := classifyError(err)

	if code != CodeEphemeralUnsupported {
		t.Fatalf("code %q, want %q", code, CodeEphemeralUnsupported)
	}
	if code == CodeNotFound {
		t.Fatal("an unsupported ephemeral-containers subresource must never classify as not_found")
	}
	if !strings.Contains(message, "ephemeral") {
		t.Fatalf("message does not mention ephemeral debug containers: %q", message)
	}
}
