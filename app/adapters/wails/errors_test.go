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
