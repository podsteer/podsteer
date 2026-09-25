package k8s

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/podsteer/podsteer/app/ports"
)

func TestClassifyMapsAConflictToItsOwnSentinel(t *testing.T) {
	// The one 409 UpdateResource expects: a PUT carrying a resourceVersion
	// the server no longer recognises.
	apiErr := apierrors.NewConflict(schema.GroupResource{Group: "apps", Resource: "deployments"}, "web", errors.New("stale"))

	err := classify("applying resource", apiErr)

	if !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("not classified as a conflict: %v", err)
	}
	// AND NOT AS FORBIDDEN. RBAC allowed the request; the object's own
	// resourceVersion is simply stale, which calls for a reload, not
	// different credentials.
	if errors.Is(err, ports.ErrForbidden) {
		t.Fatalf("also classified as forbidden: %v", err)
	}
}

func TestClassifyMapsAnInvalidObjectToManifestRejected(t *testing.T) {
	// What the server returns when a request is well-formed but the OBJECT
	// fails schema validation or an admission webhook declines it — the
	// failure UpdateResource's dry run exists to surface.
	apiErr := apierrors.NewInvalid(schema.GroupKind{Group: "apps", Kind: "Deployment"}, "web", nil)

	err := classify("applying resource", apiErr)

	if !errors.Is(err, ports.ErrManifestRejected) {
		t.Fatalf("not classified as a rejected manifest: %v", err)
	}
	// The server's own message must survive the wrap — Validate's whole
	// point is showing it close to verbatim.
	if !errors.Is(err, apiErr) {
		t.Fatalf("original API error not reachable through errors.Is: %v", err)
	}
}

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

// TestLegacyAuthProviderIsRecognisedBeforeTheStatusChecks covers the failure
// client-go raises while BUILDING the request: the kubeconfig names an
// auth-provider this binary never registered, so nothing is dialled and the
// default branch would report it as an opaque internal error.
func TestLegacyAuthProviderIsRecognisedBeforeTheStatusChecks(t *testing.T) {
	t.Parallel()

	// The exact sentence client-go composes — clientcmd has no typed error
	// and no sentinel, so the prefix is the only thing there is to match on.
	raw := fmt.Errorf(`no Auth Provider found for name "oidc"`)

	err := classify("listing pods", raw)

	if !errors.Is(err, ports.ErrLegacyAuthProvider) {
		t.Fatalf("classify() = %v, want it wrapped in ErrLegacyAuthProvider", err)
	}
	if !strings.Contains(err.Error(), `"oidc"`) {
		t.Errorf("classify() = %q, want the provider named", err)
	}
	if errors.Is(err, ports.ErrUnreachable) {
		t.Error("classified as unreachable; nothing was ever dialled")
	}
}

func TestLegacyAuthProviderSurvivesAnUnfamiliarShape(t *testing.T) {
	t.Parallel()

	// A future client-go that drops the quotes is still this failure, and is
	// still worth naming as one rather than falling through to "internal".
	err := classify("listing pods", fmt.Errorf("no Auth Provider found for name oidc"))

	if !errors.Is(err, ports.ErrLegacyAuthProvider) {
		t.Fatalf("classify() = %v, want it wrapped in ErrLegacyAuthProvider", err)
	}
}

func TestOrdinaryErrorsAreNotReadAsAnAuthProvider(t *testing.T) {
	t.Parallel()

	err := classify("listing pods", fmt.Errorf("connection refused"))
	if errors.Is(err, ports.ErrLegacyAuthProvider) {
		t.Fatalf("classify() = %v, want nothing to do with auth providers", err)
	}
}
