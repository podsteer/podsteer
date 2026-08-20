package domain

import (
	"fmt"
	"net/url"
	"strings"
)

// ClusterID identifies a cluster connection known to PodSteer.
//
// It is derived from the kubeconfig context name because that is the only
// handle which is simultaneously unique within a kubeconfig, stable across
// restarts and meaningful to the operator reading the UI. The API server URL
// would fail the uniqueness test — several contexts routinely differ only by
// the user or namespace they select.
type ClusterID string

// NewClusterID validates raw and returns it as a ClusterID.
func NewClusterID(raw string) (ClusterID, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", ErrEmptyClusterID
	}
	return ClusterID(trimmed), nil
}

// String renders the identifier.
func (id ClusterID) String() string { return string(id) }

// IsZero reports whether the identifier is unset.
func (id ClusterID) IsZero() bool { return id == "" }

// ServerEndpoint is a validated Kubernetes API server URL.
type ServerEndpoint struct {
	raw  string
	host string
}

// NewServerEndpoint validates raw as an absolute http(s) URL.
func NewServerEndpoint(raw string) (ServerEndpoint, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ServerEndpoint{}, ErrEmptyServerEndpoint
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ServerEndpoint{}, fmt.Errorf("%w: %q: %v", ErrInvalidServerEndpoint, trimmed, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ServerEndpoint{}, fmt.Errorf("%w: %q: scheme must be http or https",
			ErrInvalidServerEndpoint, trimmed)
	}
	if parsed.Host == "" {
		return ServerEndpoint{}, fmt.Errorf("%w: %q: missing host",
			ErrInvalidServerEndpoint, trimmed)
	}

	return ServerEndpoint{raw: trimmed, host: parsed.Host}, nil
}

// String renders the full endpoint URL.
func (e ServerEndpoint) String() string { return e.raw }

// Host returns the host[:port] component, which is what the UI shows when the
// full URL is too long to be useful.
func (e ServerEndpoint) Host() string { return e.host }

// IsZero reports whether the endpoint is unset.
func (e ServerEndpoint) IsZero() bool { return e.raw == "" }

// ServerVersion is the version reported by a cluster's API server.
//
// It doubles as proof of reachability: PodSteer only learns a version by
// completing a round trip, so a Cluster carrying one has demonstrably been
// reached with the credentials in its kubeconfig context.
type ServerVersion struct {
	// GitVersion is the full semantic version, e.g. "v1.31.2+k3s1".
	GitVersion string
	// Major is the major version component, e.g. "1".
	Major string
	// Minor is the minor version component, e.g. "31".
	Minor string
	// Platform is the API server's OS/arch, e.g. "linux/arm64".
	Platform string
}

// IsZero reports whether no version has been observed.
func (v ServerVersion) IsZero() bool { return v == ServerVersion{} }

// ClusterSpec carries the data needed to build a Cluster.
//
// It exists so that NewCluster does not degenerate into a long list of
// positional string parameters that are easy to transpose at a call site.
type ClusterSpec struct {
	// ID is the kubeconfig context name. Required.
	ID ClusterID
	// Server is the API server endpoint. Required.
	Server ServerEndpoint
	// DefaultNamespace is the namespace pinned by the context, if any.
	// NamespaceAll means the context pins none.
	DefaultNamespace NamespaceName
	// AuthInfo is the kubeconfig user this context authenticates as. It is
	// descriptive only — no credential material ever enters the domain.
	AuthInfo string
	// IsCurrent marks the context selected by `current-context`.
	IsCurrent bool
}

// Cluster is a Kubernetes cluster PodSteer can talk to, as described by one
// kubeconfig context.
//
// The entity is identified by its ID; two Cluster values denote the same
// cluster when their IDs match, regardless of any other field.
type Cluster struct {
	id               ClusterID
	server           ServerEndpoint
	defaultNamespace NamespaceName
	authInfo         string
	isCurrent        bool
	version          ServerVersion
}

// NewCluster validates spec and returns the corresponding Cluster.
func NewCluster(spec ClusterSpec) (Cluster, error) {
	if spec.ID.IsZero() {
		return Cluster{}, ErrEmptyClusterID
	}
	if spec.Server.IsZero() {
		return Cluster{}, fmt.Errorf("cluster %q: %w", spec.ID, ErrEmptyServerEndpoint)
	}

	return Cluster{
		id:               spec.ID,
		server:           spec.Server,
		defaultNamespace: spec.DefaultNamespace,
		authInfo:         strings.TrimSpace(spec.AuthInfo),
		isCurrent:        spec.IsCurrent,
	}, nil
}

// ID returns the cluster identifier.
func (c Cluster) ID() ClusterID { return c.id }

// Server returns the API server endpoint.
func (c Cluster) Server() ServerEndpoint { return c.server }

// DefaultNamespace returns the namespace pinned by the kubeconfig context, or
// NamespaceDefault when the context pins none.
func (c Cluster) DefaultNamespace() NamespaceName { return c.defaultNamespace.OrDefault() }

// AuthInfo returns the name of the kubeconfig user this context authenticates as.
func (c Cluster) AuthInfo() string { return c.authInfo }

// IsCurrent reports whether this is the kubeconfig's current context.
func (c Cluster) IsCurrent() bool { return c.isCurrent }

// Version returns the observed API server version. It is the zero value until
// the cluster has been reached.
func (c Cluster) Version() ServerVersion { return c.version }

// IsReachable reports whether PodSteer has completed a round trip to this
// cluster's API server during this session.
func (c Cluster) IsReachable() bool { return !c.version.IsZero() }

// WithVersion returns a copy of the cluster marked as reached, carrying the
// version its API server reported.
//
// This is the entity's single state transition, and it returns a new value
// rather than mutating the receiver so that a Cluster already handed to
// another goroutine cannot change underneath it.
func (c Cluster) WithVersion(version ServerVersion) Cluster {
	c.version = version
	return c
}

// IsZero reports whether the cluster is unset.
func (c Cluster) IsZero() bool { return c.id.IsZero() }
