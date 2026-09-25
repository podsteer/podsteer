package k8s

// A kubeconfig that signs in with the removed built-in OIDC auth-provider.
//
// THE FAILURE THESE TESTS EXIST FOR IS NOT THE REFUSAL — that is deliberate,
// and ADR 10 records why — but the SENTENCE the operator gets. PodSteer
// writes a specific one: it names the provider, says why refreshing through
// it would rewrite their kubeconfig, and names kubelogin as the replacement.
// It was unreachable. client-go resolves an auth-provider while building the
// CLIENT (rest.TransportConfig, inside kubernetes.NewForConfig), not while
// making a request, and the factory returned that error unclassified — so it
// carried no sentinel and the frontend reported "An unexpected error
// occurred" for the one failure this package has the most to say about.
//
// The existing tests could not catch it: they hand the raw message straight
// to classify(), which is the step that was being skipped. These go through a
// kubeconfig on disk instead.

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

func legacyOIDCKubeconfig(contextName string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://%s:6443
  name: %s-cluster
contexts:
- context:
    cluster: %s-cluster
    user: %s-user
  name: %s
users:
- name: %s-user
  user:
    auth-provider:
      name: oidc
      config:
        client-id: podsteer
        idp-issuer-url: https://issuer.example
`, contextName, contextName, contextName, contextName, contextName, contextName)
}

func TestALegacyAuthProviderIsNamedRatherThanCalledUnexpected(t *testing.T) {
	t.Parallel()

	path := writeFile(t, t.TempDir(), "config", legacyOIDCKubeconfig("legacy"))
	adapter := New(Config{KubeconfigPath: path}, nil)

	_, err := adapter.factory.clientFor(domain.ClusterID("legacy"))
	if err == nil {
		t.Fatal("building a client for a kubeconfig with auth-provider: oidc succeeded; want a refusal")
	}

	if !errors.Is(err, ports.ErrLegacyAuthProvider) {
		t.Fatalf("error = %v\nwant one wrapping ErrLegacyAuthProvider, so the frontend can say which provider and what replaces it", err)
	}
}

// The provider's NAME travels with the sentinel, because the message quotes
// it: a kubeconfig can name any provider, and "the old built-in provider"
// without saying which is not something an operator can act on.
func TestTheRefusalCarriesTheProviderName(t *testing.T) {
	t.Parallel()

	path := writeFile(t, t.TempDir(), "config", legacyOIDCKubeconfig("legacy"))
	adapter := New(Config{KubeconfigPath: path}, nil)

	_, err := adapter.factory.clientFor(domain.ClusterID("legacy"))
	if err == nil {
		t.Fatal("expected a refusal")
	}

	if got := err.Error(); !strings.Contains(got, `"oidc"`) {
		t.Errorf("error = %q, want the provider name in it", got)
	}
}

// A kubeconfig with nothing exotic in it still builds a client. The
// classification added to the factory must not turn ordinary construction
// into a refusal.
func TestAnOrdinaryKubeconfigStillBuildsAClient(t *testing.T) {
	t.Parallel()

	path := writeFile(t, t.TempDir(), "config", singleContextKubeconfig("plain", "https://plain:6443"))
	adapter := New(Config{KubeconfigPath: path}, nil)

	if _, err := adapter.factory.clientFor(domain.ClusterID("plain")); err != nil {
		t.Fatalf("clientFor() = %v, want a client: nothing here is unusual", err)
	}
}
