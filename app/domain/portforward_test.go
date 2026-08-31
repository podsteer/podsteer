package domain_test

import (
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

func TestSchemeIsGuessedFromThePortName(t *testing.T) {
	t.Parallel()

	// The name is the only hint Kubernetes offers — a port number tells you
	// nothing, since anything can listen anywhere.
	if got := domain.SchemeForPort("https"); got != "https" {
		t.Errorf("SchemeForPort(https) = %q, want https", got)
	}
	if got := domain.SchemeForPort("HTTPS"); got != "https" {
		t.Errorf("the name is a convention, not a keyword; case must not matter: got %q", got)
	}
	// Everything else is http, because being wrong that way costs one
	// redirect and being wrong the other way costs a confusing TLS error.
	for _, name := range []string{"http", "web", "grpc", ""} {
		if got := domain.SchemeForPort(name); got != "http" {
			t.Errorf("SchemeForPort(%q) = %q, want http", name, got)
		}
	}
}

func TestForwardKeySeparatesIdenticallyNamedPodsInDifferentClusters(t *testing.T) {
	t.Parallel()

	// Two clusters routinely run identically named pods in identically named
	// namespaces — that is what a staging environment IS — and a key without
	// the cluster returns one cluster's forward for another's request.
	// Headlamp has an open bug that is exactly this.
	dev := domain.ForwardKey("dev", "default", "api-7d9f", 8080)
	prod := domain.ForwardKey("prod", "default", "api-7d9f", 8080)

	if dev == prod {
		t.Errorf("two clusters produced the same key: %q", dev)
	}
}

func TestForwardAddressCarriesItsScheme(t *testing.T) {
	t.Parallel()

	forward := domain.Forward{LocalPort: 59595, Scheme: "https"}
	if got := forward.Address(); got != "https://localhost:59595" {
		t.Errorf("Address() = %q, want the scheme and the bound local port", got)
	}
}
