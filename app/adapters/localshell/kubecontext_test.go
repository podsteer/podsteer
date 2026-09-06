// Tests for the per-session kubeconfig that selects a shell's context.
//
// THE CENTRAL ONE IS TestContextOverlayResolvesTheContextThroughClientGo, and
// it is deliberately not an assertion about bytes. The claim being made is not
// "PodSteer writes this YAML" — it is "a document holding only current-context,
// placed first in KUBECONFIG, selects that context while every cluster, user
// and namespace still comes from the operator's own file". That is a claim
// about the kubeconfig MERGE, so it is asserted against the merge, using the
// same client-go loader kubectl and the Kubernetes adapter both use. A test on
// the bytes would keep passing on the day client-go changed its precedence.

package localshell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"
)

// operatorKubeconfig is a stand-in for the file PodSteer must never write: two
// contexts, its own current-context, and a namespace on the one being selected
// so the test can tell a context that merely resolved from one that resolved
// in full.
const operatorKubeconfig = `apiVersion: v1
kind: Config
current-context: alpha
clusters:
  - name: alpha-cluster
    cluster:
      server: https://alpha.example:6443
  - name: beta-cluster
    cluster:
      server: https://beta.example:6443
users:
  - name: alpha-user
    user: {}
  - name: beta-user
    user: {}
contexts:
  - name: alpha
    context:
      cluster: alpha-cluster
      user: alpha-user
  - name: beta
    context:
      cluster: beta-cluster
      user: beta-user
      namespace: payments
`

// writeOperatorKubeconfig drops the fixture on disk and returns its path.
func writeOperatorKubeconfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(operatorKubeconfig), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	return path
}

// TestContextOverlayResolvesTheContextThroughClientGo is the whole mechanism in
// one assertion: the overlay first, the operator's file behind it, and the
// merged result naming the overlay's context with the operator's own namespace
// attached to it.
func TestContextOverlayResolvesTheContextThroughClientGo(t *testing.T) {
	t.Parallel()

	operator := writeOperatorKubeconfig(t)
	dir, overlay, err := writeContextOverlay("beta")
	if err != nil {
		t.Fatalf("writeContextOverlay() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.Precedence = []string{overlay, operator}
	config := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{})

	raw, err := config.RawConfig()
	if err != nil {
		t.Fatalf("RawConfig() error = %v", err)
	}
	if raw.CurrentContext != "beta" {
		t.Errorf("current-context = %q, want beta — the overlay did not win the merge", raw.CurrentContext)
	}

	// The context resolving is half the claim; resolving in FULL is the other
	// half. A namespace only the operator's file carries proves the overlay
	// selected their context rather than shadowing it with an empty one.
	namespace, _, err := config.Namespace()
	if err != nil {
		t.Fatalf("Namespace() error = %v", err)
	}
	if namespace != "payments" {
		t.Errorf("namespace = %q, want payments from the operator's own file", namespace)
	}

	if _, ok := raw.Clusters["beta-cluster"]; !ok {
		t.Error("the merged config lost the operator's clusters")
	}
}

// TestContextOverlayCarriesNoCredentials is the reason this option is
// acceptable where a per-session copy of a kubeconfig was refused. Asserted on
// the parsed document rather than by grepping the bytes: an empty clusters or
// users list is the thing being promised, and a substring search would pass on
// a file that spelled a credential differently.
func TestContextOverlayCarriesNoCredentials(t *testing.T) {
	t.Parallel()

	dir, overlay, err := writeContextOverlay("beta")
	if err != nil {
		t.Fatalf("writeContextOverlay() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	body, err := os.ReadFile(overlay)
	if err != nil {
		t.Fatalf("reading the overlay: %v", err)
	}

	loaded, err := clientcmd.Load(body)
	if err != nil {
		t.Fatalf("the overlay is not a kubeconfig: %v", err)
	}
	if len(loaded.Clusters) != 0 {
		t.Errorf("clusters = %v, want none", loaded.Clusters)
	}
	if len(loaded.AuthInfos) != 0 {
		t.Errorf("users = %v, want none — this file must never hold a credential", loaded.AuthInfos)
	}
	if len(loaded.Contexts) != 0 {
		t.Errorf("contexts = %v, want none; the operator's own file defines them", loaded.Contexts)
	}
	if loaded.CurrentContext != "beta" {
		t.Errorf("current-context = %q, want beta", loaded.CurrentContext)
	}
}

// TestContextOverlayQuotesAnAwkwardContextName covers the names clouds
// actually generate. An EKS context is an ARN and a GKE one is
// underscore-joined; both carry colons and slashes, and a hand-built
// `current-context: ` + name would have produced a file kubectl cannot parse
// for the first of them.
func TestContextOverlayQuotesAnAwkwardContextName(t *testing.T) {
	t.Parallel()

	names := []string{
		"arn:aws:eks:eu-west-1:123456789012:cluster/prod",
		"gke_my-project_europe-west1_prod",
		"a context with spaces",
		"true",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			dir, overlay, err := writeContextOverlay(name)
			if err != nil {
				t.Fatalf("writeContextOverlay() error = %v", err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })

			body, err := os.ReadFile(overlay)
			if err != nil {
				t.Fatalf("reading the overlay: %v", err)
			}
			loaded, err := clientcmd.Load(body)
			if err != nil {
				t.Fatalf("the overlay is not a kubeconfig: %v\n%s", err, body)
			}
			if loaded.CurrentContext != name {
				t.Errorf("current-context = %q, want %q", loaded.CurrentContext, name)
			}
		})
	}
}

// TestContextOverlayIsPrivateOnDisk asserts the modes rather than trusting
// os.MkdirTemp and os.WriteFile to stay as they are. The file holds no secret,
// but every other file PodSteer writes is 0600 and a reader who finds one that
// is not will reasonably wonder what makes it different.
func TestContextOverlayIsPrivateOnDisk(t *testing.T) {
	t.Parallel()

	dir, overlay, err := writeContextOverlay("beta")
	if err != nil {
		t.Fatalf("writeContextOverlay() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat on the overlay directory: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("directory mode = %04o, want 0700", perm)
	}

	fileInfo, err := os.Stat(overlay)
	if err != nil {
		t.Fatalf("stat on the overlay: %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Errorf("overlay mode = %04o, want 0600", perm)
	}

	// The directory name is what somebody clearing a temp folder reads, so it
	// has to say whose it is.
	if !strings.HasPrefix(filepath.Base(dir), overlayDirPrefix) {
		t.Errorf("directory = %q, want a name that says PodSteer wrote it", dir)
	}
}

// TestRemoveContextOverlayTakesTheWholeDirectory covers what a shell leaves
// behind. `kubectl config use-context` inside the session writes to the FIRST
// file in KUBECONFIG, which is the overlay, and client-go writes through a
// temporary file in the same directory — so removing only the file we wrote
// would leave the directory, and sometimes something in it.
func TestRemoveContextOverlayTakesTheWholeDirectory(t *testing.T) {
	t.Parallel()

	dir, overlay, err := writeContextOverlay("beta")
	if err != nil {
		t.Fatalf("writeContextOverlay() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.lock"), nil, 0o600); err != nil {
		t.Fatalf("writing a file beside the overlay: %v", err)
	}

	if err := removeContextOverlay(dir); err != nil {
		t.Fatalf("removeContextOverlay() error = %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the overlay directory survived: stat err = %v", err)
	}
	if _, err := os.Stat(overlay); !os.IsNotExist(err) {
		t.Errorf("the overlay survived: stat err = %v", err)
	}

	// Idempotent, because a session can be stopped by its pane and by the
	// shutdown sweep, and neither is an error.
	if err := removeContextOverlay(dir); err != nil {
		t.Errorf("second removeContextOverlay() error = %v, want nil", err)
	}
	if err := removeContextOverlay(""); err != nil {
		t.Errorf("removeContextOverlay(\"\") error = %v, want nil for a session that never had one", err)
	}
}

// TestContextOverlayIsTheDocumentedShape pins the file an operator will cat
// after seeing an unfamiliar path in KUBECONFIG. Three lines and nothing else
// is the promise CLAUDE.md and SECURITY.md both make in words.
func TestContextOverlayIsTheDocumentedShape(t *testing.T) {
	t.Parallel()

	body, err := yaml.Marshal(contextOverlay{APIVersion: "v1", Kind: "Config", CurrentContext: "beta"})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	want := "apiVersion: v1\ncurrent-context: beta\nkind: Config\n"
	if string(body) != want {
		t.Errorf("overlay =\n%s\nwant\n%s", body, want)
	}
}
