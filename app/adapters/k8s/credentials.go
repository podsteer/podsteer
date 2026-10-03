package k8s

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"slices"
	"strconv"

	"k8s.io/client-go/rest"

	"github.com/podsteer/podsteer/app/domain"
)

// Credentials rewritten under an open tab.
//
// A CLIENT IS CACHED PER CONTEXT FOR THE LIFE OF THE TAB, and what it was
// built from is a snapshot: a static token, a client certificate, an exec
// plugin's command line. Exec plugins are fine — client-go runs them again
// when a credential expires — but everything else is frozen at the moment of
// the first request. So when `oc login`, Teleport, Rancher or
// `az aks get-credentials --overwrite` writes a fresh credential into the
// kubeconfig, the tab that was open keeps presenting the old one and answers
// 401 until it is closed, with a Retry button that reuses the same client.
//
// The fix is to notice. authFingerprint reduces the AUTHENTICATION half of a
// rest.Config to a digest, taken when the client is built; credentialsChanged
// rebuilds the config from the kubeconfig as it is now and compares.
//
// ONLY WHAT AUTHENTICATES IS COMPARED — token, basic auth, client certificate
// and key (the files' CONTENTS as well as their paths, because a rewrite in
// place leaves the path alone), the exec command, its arguments and its
// environment, an auth-provider's configuration, impersonation. The server
// address, the CA and the namespace are deliberately not: a context re-pointed
// somewhere else is the operator's reconnect to make, and one touched file
// must not disturb a tab that is working. The digest is compared and
// discarded; nothing derived from it is logged or stored beyond the client
// set it belongs to.
func authFingerprint(cfg *rest.Config) string {
	hash := sha256.New()
	field := func(name, value string) {
		// Length-prefixed, so ("ab","c") and ("a","bc") differ.
		hash.Write([]byte(name + ":" + strconv.Itoa(len(value)) + ":" + value + "\x00"))
	}
	fileContents := func(path string) string {
		if path == "" {
			return ""
		}
		data, err := os.ReadFile(path)
		if err != nil {
			// An unreadable file is a distinct state from an empty one.
			return "unreadable:" + err.Error()
		}
		return string(data)
	}

	field("username", cfg.Username)
	field("password", cfg.Password)
	field("bearer", cfg.BearerToken)
	// Path only: client-go re-reads a token file on its own schedule.
	field("bearerFile", cfg.BearerTokenFile)

	tls := cfg.TLSClientConfig
	field("certData", string(tls.CertData))
	field("keyData", string(tls.KeyData))
	field("certFile", tls.CertFile)
	field("certFileContents", fileContents(tls.CertFile))
	field("keyFile", tls.KeyFile)
	field("keyFileContents", fileContents(tls.KeyFile))

	if exec := cfg.ExecProvider; exec != nil {
		field("execAPI", exec.APIVersion)
		field("execCommand", exec.Command)
		for _, arg := range exec.Args {
			field("execArg", arg)
		}
		for _, env := range exec.Env {
			field("execEnv", env.Name+"="+env.Value)
		}
		field("execInteractive", string(exec.InteractiveMode))
		field("execClusterInfo", strconv.FormatBool(exec.ProvideClusterInfo))
	}

	if provider := cfg.AuthProvider; provider != nil {
		field("providerName", provider.Name)
		keys := make([]string, 0, len(provider.Config))
		for key := range provider.Config {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			field("providerConfig", key+"="+provider.Config[key])
		}
	}

	field("impersonateUser", cfg.Impersonate.UserName)
	field("impersonateUID", cfg.Impersonate.UID)
	for _, group := range cfg.Impersonate.Groups {
		field("impersonateGroup", group)
	}
	extraKeys := make([]string, 0, len(cfg.Impersonate.Extra))
	for key := range cfg.Impersonate.Extra {
		extraKeys = append(extraKeys, key)
	}
	slices.Sort(extraKeys)
	for _, key := range extraKeys {
		for _, value := range cfg.Impersonate.Extra[key] {
			field("impersonateExtra", key+"="+value)
		}
	}

	return hex.EncodeToString(hash.Sum(nil))
}

// credentialsChanged reports whether the kubeconfig now authenticates a
// context differently from the client set cached for it.
//
// False when nothing is cached (there is nothing stale to replace), when the
// cached set carries no fingerprint (a set not built by clientsFor), and when
// the kubeconfig cannot be read right now — a half-written file is a reason
// to wait for the next change, not to throw a working client away.
func (f *clientFactory) credentialsChanged(id domain.ClusterID) bool {
	f.mu.RLock()
	cached, ok := f.clients[id]
	f.mu.RUnlock()
	if !ok || cached.authFingerprint == "" {
		return false
	}

	cfg, err := f.restConfig(id)
	if err != nil {
		return false
	}
	return authFingerprint(cfg) != cached.authFingerprint
}

// CredentialsChanged reports whether the kubeconfig now holds different
// credentials for an open cluster than the client this adapter is using.
func (a *Adapter) CredentialsChanged(id domain.ClusterID) bool {
	return a.factory.credentialsChanged(id)
}

// RefreshClient drops everything built from id's old credentials — the client,
// the watch, the per-cluster caches — and leaves its port-forwards running.
//
// INVALIDATE WITHOUT THE FORWARD SWEEP, and the difference is deliberate. A
// forward is stopped by Invalidate because a closed tab must not leave a
// supervisor to resurrect the cluster; here the cluster stays open and a
// supervisor that rebuilds the client is rebuilding it with the credentials
// the operator just wrote, which is what it should do. Ending a forward
// because somebody logged in again would be the opposite of helpful.
func (a *Adapter) RefreshClient(id domain.ClusterID) {
	a.release(id)
}
