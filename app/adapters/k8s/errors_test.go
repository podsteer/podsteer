package k8s

import (
	"errors"
	"fmt"
	"os/exec"
	"testing"

	"github.com/podsteer/podsteer/app/ports"
)

func TestClassifyNamesAMissingCredentialPlugin(t *testing.T) {
	// What client-go surfaces when a kubeconfig context runs `aws eks
	// get-token` and the binary is not on PATH — the everyday failure for an
	// EKS or GKE context in a desktop application launched from Finder.
	_, lookErr := exec.LookPath("podsteer-no-such-binary-aws")
	wrapped := fmt.Errorf("getting credentials: %w", lookErr)

	err := classify("listing pods", wrapped)

	if !errors.Is(err, ports.ErrCredentialPluginMissing) {
		t.Fatalf("not classified as a missing plugin: %v", err)
	}

	// AND NOT AS AN OUTAGE. Reported unreachable it sends somebody to check a
	// VPN for a cluster that was never contacted.
	if errors.Is(err, ports.ErrUnreachable) {
		t.Fatalf("also classified as unreachable: %v", err)
	}
}

func TestMissingCredentialPluginNamesTheBinary(t *testing.T) {
	// The name is the entire diagnosis: "could not authenticate" would send
	// somebody to re-run `aws sso login`, which succeeds and changes nothing.
	err := &exec.Error{Name: "gke-gcloud-auth-plugin", Err: exec.ErrNotFound}

	if got := missingCredentialPlugin(fmt.Errorf("building client: %w", err)); got != "gke-gcloud-auth-plugin" {
		t.Fatalf("named %q", got)
	}
}

func TestMissingCredentialPluginIgnoresEverythingElse(t *testing.T) {
	if got := missingCredentialPlugin(errors.New("connection refused")); got != "" {
		t.Fatalf("named %q for an unrelated error", got)
	}
}
