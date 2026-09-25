package wails

import (
	"context"
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

func TestClassifyErrorNamesTheFixForALegacyAuthProvider(t *testing.T) {
	// client-go's own words are `no Auth Provider found for name "oidc"`,
	// which reads as something PodSteer failed to install. It is a decision
	// (ADR 10): refreshing through that provider rewrites the operator's
	// kubeconfig, which PodSteer does not do — so the message has to carry
	// the replacement, not the refusal.
	err := fmt.Errorf("connecting: %w: %q: %w",
		ports.ErrLegacyAuthProvider, "oidc",
		fmt.Errorf(`no Auth Provider found for name "oidc"`))

	code, message := classifyError(err)

	if code != CodeLegacyAuthProvider {
		t.Fatalf("code %q, want %q", code, CodeLegacyAuthProvider)
	}
	if !strings.Contains(message, "kubelogin") {
		t.Fatalf("message does not name the replacement: %q", message)
	}
	// And it must not read as PodSteer being broken or the cluster being
	// unreachable — nothing was dialled.
	if strings.Contains(strings.ToLower(message), "unreachable") {
		t.Fatalf("message reads as a transport failure: %q", message)
	}
}

func TestClassifyErrorExplainsAnyOtherLegacyAuthProvider(t *testing.T) {
	// The oidc case is the one people hit; the others still need an answer
	// rather than client-go's sentence.
	err := fmt.Errorf("connecting: %w: %q: %w",
		ports.ErrLegacyAuthProvider, "azure",
		fmt.Errorf(`no Auth Provider found for name "azure"`))

	code, message := classifyError(err)

	if code != CodeLegacyAuthProvider {
		t.Fatalf("code %q, want %q", code, CodeLegacyAuthProvider)
	}
	if !strings.Contains(message, "azure") {
		t.Fatalf("message does not name the provider: %q", message)
	}
	if !strings.Contains(message, "exec credential plugin") {
		t.Fatalf("message does not name the mechanism that replaces it: %q", message)
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

// TestClassifyErrorKeepsTheKubeletsWordsForAPodThatNeverRan is the frontend
// half of the same fix.
//
// The pod was accepted and then never started, and everything anybody can act
// on is in the kubelet's sentence: which image, and which requirement it did
// not meet. Classified as internal — which is where it landed before this
// code existed — the operator is told "an unexpected error occurred" about a
// failure the cluster had explained in full.
func TestClassifyErrorKeepsTheKubeletsWordsForAPodThatNeverRan(t *testing.T) {
	err := fmt.Errorf("waiting for pod %q to run: %w: CreateContainerConfigError: %s",
		"podsteer-shell-abcde", ports.ErrPodDidNotStart,
		"container has runAsNonRoot and image will run as root")

	code, message := classifyError(err)

	if code != CodePodDidNotStart {
		t.Fatalf("code %q, want %q", code, CodePodDidNotStart)
	}
	if !strings.Contains(message, "image will run as root") {
		t.Errorf("message = %q, want the kubelet's own words — they name the fix", message)
	}
}

// TestAPodThatNeverRanIsNotReportedAsAnAdmissionRefusal keeps the two apart.
//
// They are both "the pod you asked for is not there", they arrive from
// different components at different moments, and they send somebody to
// different people: one to whoever owns the namespace's policy, the other to
// the image field in the dialog they just used.
func TestAPodThatNeverRanIsNotReportedAsAnAdmissionRefusal(t *testing.T) {
	if code, _ := classifyError(fmt.Errorf("x: %w", ports.ErrPodDidNotStart)); code == CodePodRejected {
		t.Fatal("a pod that never started was reported as an admission refusal")
	}
	if code, _ := classifyError(fmt.Errorf("x: %w", ports.ErrPodRejectedByAdmission)); code == CodePodDidNotStart {
		t.Fatal("an admission refusal was reported as a pod that never started")
	}
}

// THE BUG THIS PINS. A cancellation and a deadline shared one code and one
// sentence — "The request was cancelled or timed out" — and they are opposite
// facts. A cancellation is the caller giving up; a deadline is the cluster
// failing to answer. Reported as one, a cluster whose network had gone away
// kept a green tab: its calls EXPIRED rather than failing, because a blackholed
// packet is never refused, and an expiry that reads as "cancelled" is correctly
// ignored when deciding whether a cluster is still there.
func TestACancelledRequestIsNotAClusterProblem(t *testing.T) {
	t.Parallel()

	code, message := classifyError(fmt.Errorf("listing pods: %w", context.Canceled))

	if code != CodeCancelled {
		t.Errorf("code = %q, want %q", code, CodeCancelled)
	}
	if strings.Contains(message, "timed out") {
		t.Errorf("message = %q; a cancellation is not a timeout and must not say so", message)
	}
}

func TestARequestThatTimedOutSaysTheClusterDidNotAnswer(t *testing.T) {
	t.Parallel()

	code, message := classifyError(fmt.Errorf("assessing: %w", context.DeadlineExceeded))

	// The same code as the three transport failures, deliberately: the UI
	// branches on the code to decide whether the cluster is still there and
	// whether to offer Retry, and both answers are the ones it gives them.
	if code != CodeUnreachable {
		t.Errorf("code = %q, want %q — a deadline is the cluster not answering", code, CodeUnreachable)
	}
	if !strings.Contains(message, "did not answer") {
		t.Errorf("message = %q, want one that says the cluster did not answer", message)
	}
}

// A transport failure classified by the Kubernetes adapter still wins: it
// carries a more specific diagnosis than "the deadline passed".
func TestAClassifiedTransportFailureKeepsItsOwnMessage(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("listing pods: %w: %w: %w",
		ports.ErrUnreachable, ports.ErrConnectionRefused, context.DeadlineExceeded)

	code, message := classifyError(err)

	if code != CodeUnreachable {
		t.Fatalf("code = %q, want %q", code, CodeUnreachable)
	}
	if !strings.Contains(message, "refused the connection") {
		t.Errorf("message = %q, want the refused-connection diagnosis rather than the deadline's", message)
	}
}
