package k8s

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

// proxyFactory builds a factory over a one-context kubeconfig, which is all
// restConfig needs to resolve a config at all.
func proxyFactory(t *testing.T, proxy func() domain.ProxySettings) *clientFactory {
	t.Helper()

	path := writeFile(t, t.TempDir(), "config",
		singleContextKubeconfig("proxy-test", "https://api.example:6443"))

	adapter := New(Config{KubeconfigPath: path, Proxy: proxy}, slog.New(slog.DiscardHandler))
	return adapter.factory
}

// TestRestConfigLeavesTheProxyToTheEnvironmentByDefault is the property that
// must survive this feature existing.
//
// A nil rest.Config.Proxy means client-go reads HTTPS_PROXY, HTTP_PROXY and
// NO_PROXY, which is what every operator behind a corporate proxy has been
// relying on without configuring anything. Installing a function that returns
// nil would look identical in review and would silently mean "never proxy" for
// all of them.
func TestRestConfigLeavesTheProxyToTheEnvironmentByDefault(t *testing.T) {
	t.Parallel()

	for name, proxy := range map[string]func() domain.ProxySettings{
		"no setting at all": nil,
		"the default mode":  func() domain.ProxySettings { return domain.ProxySettings{} },
		"environment":       func() domain.ProxySettings { return domain.ProxySettings{Mode: domain.ProxyFromEnvironment} },
		"a refused value": func() domain.ProxySettings {
			return domain.ProxySettings{Mode: domain.ProxyManual, URL: "not a url"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			factory := proxyFactory(t, proxy)

			cfg, err := factory.restConfig("proxy-test")
			if err != nil {
				t.Fatalf("restConfig() error = %v", err)
			}
			if cfg.Proxy != nil {
				t.Fatal("a dialer was installed, which replaces client-go's own reading of HTTPS_PROXY")
			}
		})
	}
}

// TestRestConfigAppliesAChosenProxy covers the two modes that do change it.
func TestRestConfigAppliesAChosenProxy(t *testing.T) {
	t.Parallel()

	t.Run("manual sends through the proxy", func(t *testing.T) {
		factory := proxyFactory(t, func() domain.ProxySettings {
			return domain.ProxySettings{Mode: domain.ProxyManual, URL: "http://proxy.corp:3128"}
		})

		cfg, err := factory.restConfig("proxy-test")
		if err != nil {
			t.Fatalf("restConfig() error = %v", err)
		}
		if cfg.Proxy == nil {
			t.Fatal("restConfig() installed no dialer for a manual proxy")
		}

		target, err := cfg.Proxy(httptest.NewRequest(http.MethodGet, "https://api.example:6443/api", nil))
		if err != nil {
			t.Fatalf("dialer error = %v", err)
		}
		if target == nil || target.Host != "proxy.corp:3128" {
			t.Fatalf("dialer = %v, want the configured proxy", target)
		}
	})

	t.Run("none refuses even an environment proxy", func(t *testing.T) {
		factory := proxyFactory(t, func() domain.ProxySettings {
			return domain.ProxySettings{Mode: domain.ProxyNone}
		})

		cfg, err := factory.restConfig("proxy-test")
		if err != nil {
			t.Fatalf("restConfig() error = %v", err)
		}
		if cfg.Proxy == nil {
			t.Fatal("none installed no dialer, so the environment would still be read")
		}

		target, err := cfg.Proxy(httptest.NewRequest(http.MethodGet, "https://api.example:6443/api", nil))
		if err != nil {
			t.Fatalf("dialer error = %v", err)
		}
		if target != nil {
			t.Fatalf("dialer = %v, want a direct connection", target)
		}
	})
}
