package domain_test

import (
	"errors"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

func TestNewServerEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		raw      string
		wantHost string
		wantErr  error
	}{
		{name: "https url", raw: "https://api.example.com:6443", wantHost: "api.example.com:6443"},
		{name: "http url", raw: "http://127.0.0.1:8001", wantHost: "127.0.0.1:8001"},
		{name: "surrounding whitespace", raw: "  https://api.example.com  ", wantHost: "api.example.com"},
		{name: "blank", raw: "   ", wantErr: domain.ErrEmptyServerEndpoint},
		{name: "no scheme", raw: "api.example.com:6443", wantErr: domain.ErrInvalidServerEndpoint},
		{name: "wrong scheme", raw: "ftp://api.example.com", wantErr: domain.ErrInvalidServerEndpoint},
		{name: "no host", raw: "https://", wantErr: domain.ErrInvalidServerEndpoint},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			endpoint, err := domain.NewServerEndpoint(test.raw)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("NewServerEndpoint(%q) error = %v, want %v", test.raw, err, test.wantErr)
			}
			if test.wantErr != nil {
				return
			}
			if got := endpoint.Host(); got != test.wantHost {
				t.Errorf("Host() = %q, want %q", got, test.wantHost)
			}
		})
	}
}

func TestNewClusterRequiresIdentityAndEndpoint(t *testing.T) {
	t.Parallel()

	endpoint, err := domain.NewServerEndpoint("https://api.example.com:6443")
	if err != nil {
		t.Fatalf("NewServerEndpoint() error = %v", err)
	}

	if _, err := domain.NewCluster(domain.ClusterSpec{Server: endpoint}); !errors.Is(err, domain.ErrEmptyClusterID) {
		t.Errorf("NewCluster() without id error = %v, want %v", err, domain.ErrEmptyClusterID)
	}

	if _, err := domain.NewCluster(domain.ClusterSpec{ID: "dev"}); !errors.Is(err, domain.ErrEmptyServerEndpoint) {
		t.Errorf("NewCluster() without endpoint error = %v, want %v", err, domain.ErrEmptyServerEndpoint)
	}
}

func TestClusterDefaultNamespaceFallsBack(t *testing.T) {
	t.Parallel()

	endpoint, err := domain.NewServerEndpoint("https://api.example.com:6443")
	if err != nil {
		t.Fatalf("NewServerEndpoint() error = %v", err)
	}

	unpinned, err := domain.NewCluster(domain.ClusterSpec{ID: "dev", Server: endpoint})
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}
	if got := unpinned.DefaultNamespace(); got != domain.NamespaceDefault {
		t.Errorf("DefaultNamespace() = %q, want %q", got, domain.NamespaceDefault)
	}

	pinned, err := domain.NewCluster(domain.ClusterSpec{
		ID: "dev", Server: endpoint, DefaultNamespace: "platform",
	})
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}
	if got := pinned.DefaultNamespace(); got != "platform" {
		t.Errorf("DefaultNamespace() = %q, want %q", got, "platform")
	}
}

// WithVersion must leave the receiver untouched: a Cluster already shared with
// another goroutine cannot be allowed to change underneath it.
func TestClusterWithVersionDoesNotMutateReceiver(t *testing.T) {
	t.Parallel()

	endpoint, err := domain.NewServerEndpoint("https://api.example.com:6443")
	if err != nil {
		t.Fatalf("NewServerEndpoint() error = %v", err)
	}

	original, err := domain.NewCluster(domain.ClusterSpec{ID: "dev", Server: endpoint})
	if err != nil {
		t.Fatalf("NewCluster() error = %v", err)
	}
	if original.IsReachable() {
		t.Fatal("a freshly built cluster must not report as reachable")
	}

	connected := original.WithVersion(domain.ServerVersion{GitVersion: "v1.31.2"})

	if original.IsReachable() {
		t.Error("WithVersion mutated the receiver")
	}
	if !connected.IsReachable() {
		t.Error("the returned copy does not report as reachable")
	}
	if got := connected.Version().GitVersion; got != "v1.31.2" {
		t.Errorf("Version().GitVersion = %q, want %q", got, "v1.31.2")
	}
}
