package k8s

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestAuthFingerprintTracksOnlyWhatAuthenticates(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "client.crt")
	if err := os.WriteFile(certFile, []byte("cert-one"), 0o600); err != nil {
		t.Fatal(err)
	}

	base := func() *rest.Config {
		return &rest.Config{
			Host:        "https://api.example:6443",
			BearerToken: "token-one",
			ExecProvider: &clientcmdapi.ExecConfig{
				Command: "aws", Args: []string{"eks", "get-token"},
			},
		}
	}

	tests := []struct {
		name        string
		mutate      func(*rest.Config)
		wantChanged bool
	}{
		{"nothing changed", func(*rest.Config) {}, false},
		{"the token", func(c *rest.Config) { c.BearerToken = "token-two" }, true},
		{"basic auth", func(c *rest.Config) { c.Username = "admin" }, true},
		{"a client certificate", func(c *rest.Config) { c.CertData = []byte("c") }, true},
		{"a client key", func(c *rest.Config) { c.KeyData = []byte("k") }, true},
		{"the exec command's arguments", func(c *rest.Config) { c.ExecProvider.Args = []string{"eks", "get-token", "--role", "x"} }, true},
		{"an exec environment variable", func(c *rest.Config) {
			c.ExecProvider.Env = []clientcmdapi.ExecEnvVar{{Name: "AWS_PROFILE", Value: "prod"}}
		}, true},
		{"an auth-provider setting", func(c *rest.Config) {
			c.ExecProvider = nil
			c.AuthProvider = &clientcmdapi.AuthProviderConfig{Name: "oidc", Config: map[string]string{"id-token": "x"}}
		}, true},
		{"impersonation", func(c *rest.Config) { c.Impersonate.UserName = "someone" }, true},
		{"the server address is not authentication", func(c *rest.Config) { c.Host = "https://other:6443" }, false},
		{"the rate limit is not authentication", func(c *rest.Config) { c.QPS = 500 }, false},
		{"the CA is not authentication", func(c *rest.Config) { c.CAData = []byte("ca") }, false},
	}

	want := authFingerprint(base())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base()
			tt.mutate(cfg)
			if changed := authFingerprint(cfg) != want; changed != tt.wantChanged {
				t.Fatalf("fingerprint changed = %v, want %v", changed, tt.wantChanged)
			}
		})
	}

	t.Run("a certificate file rewritten in place", func(t *testing.T) {
		cfg := base()
		cfg.CertFile = certFile
		before := authFingerprint(cfg)
		if err := os.WriteFile(certFile, []byte("cert-two"), 0o600); err != nil {
			t.Fatal(err)
		}
		if authFingerprint(cfg) == before {
			t.Fatal("the same path with new contents did not change the fingerprint")
		}
	})
}

const credentialsKubeconfig = `apiVersion: v1
kind: Config
current-context: dev
clusters:
- name: dev
  cluster:
    server: https://dev.example:6443
contexts:
- name: dev
  context: {cluster: dev, user: dev%s}
users:
- name: dev
  user:
    token: %s
`

// The scenario from the field: a static token expires, the operator logs in
// again in a terminal, and the open tab must stop presenting the old one.
func TestCredentialsChangedNoticesARewrittenKubeconfig(t *testing.T) {
	tests := []struct {
		name        string
		rewrite     func(path string) string
		wantChanged bool
	}{
		{"a new token", func(string) string { return sprintKubeconfig("", "token-two") }, true},
		{"the same credentials in a touched file", func(string) string { return sprintKubeconfig("", "token-one") + "# touched\n" }, false},
		{"only the namespace moved", func(string) string { return sprintKubeconfig(", namespace: other", "token-one") }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config")
			if err := os.WriteFile(path, []byte(sprintKubeconfig("", "token-one")), 0o600); err != nil {
				t.Fatal(err)
			}
			adapter := New(Config{KubeconfigPath: path}, nil)

			if _, err := adapter.factory.clientsFor("dev"); err != nil {
				t.Fatalf("clientsFor() error = %v", err)
			}
			if adapter.CredentialsChanged("dev") {
				t.Fatal("credentials reported changed before anything was rewritten")
			}

			if err := os.WriteFile(path, []byte(tt.rewrite(path)), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := adapter.CredentialsChanged("dev"); got != tt.wantChanged {
				t.Fatalf("CredentialsChanged() = %v, want %v", got, tt.wantChanged)
			}

			if tt.wantChanged {
				stale := adapter.factory.clients["dev"]
				adapter.RefreshClient("dev")
				fresh, err := adapter.factory.clientsFor("dev")
				if err != nil {
					t.Fatalf("clientsFor() after refresh error = %v", err)
				}
				if fresh == stale {
					t.Fatal("RefreshClient left the stale client set cached")
				}
				if adapter.CredentialsChanged("dev") {
					t.Fatal("the rebuilt client still reports changed credentials")
				}
			}
		})
	}

	t.Run("a cluster with nothing cached is never reported", func(t *testing.T) {
		adapter := New(Config{KubeconfigPath: filepath.Join(t.TempDir(), "absent")}, nil)
		if adapter.CredentialsChanged("dev") {
			t.Fatal("CredentialsChanged() = true with no client cached")
		}
	})
}

func sprintKubeconfig(contextExtra, token string) string {
	out := credentialsKubeconfig
	out = strings.Replace(out, "%s", contextExtra, 1)
	out = strings.Replace(out, "%s", token, 1)
	return out
}
