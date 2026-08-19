package domain

import "errors"

// Sentinel errors raised when a domain invariant is violated. They are
// deliberately comparable with errors.Is so that outer layers can map them
// onto transport-level concerns (an HTTP status, a UI message) without
// pattern-matching on strings.
var (
	// ErrEmptyClusterID is returned when a cluster identifier is blank.
	ErrEmptyClusterID = errors.New("cluster id must not be empty")

	// ErrClusterNotFound reports that no known cluster carries the given id.
	ErrClusterNotFound = errors.New("cluster not found")

	// ErrNoActiveCluster reports that an operation requiring a connected
	// cluster was attempted before one was selected.
	ErrNoActiveCluster = errors.New("no active cluster")

	// ErrEmptyServerEndpoint is returned when a cluster API endpoint is blank.
	ErrEmptyServerEndpoint = errors.New("server endpoint must not be empty")

	// ErrInvalidServerEndpoint is returned when a cluster API endpoint is not
	// a well-formed absolute http(s) URL.
	ErrInvalidServerEndpoint = errors.New("invalid server endpoint")

	// ErrInvalidNamespaceName is returned when a namespace name violates the
	// DNS-1123 label syntax Kubernetes requires.
	ErrInvalidNamespaceName = errors.New("invalid namespace name")

	// ErrEmptyPodName is returned when a pod is constructed without a name.
	ErrEmptyPodName = errors.New("pod name must not be empty")

	// ErrEmptyContainerName is returned when a container is constructed
	// without a name.
	ErrEmptyContainerName = errors.New("container name must not be empty")

	// ErrInvalidResourceKind is returned when a resource kind identifier is
	// malformed or names a kind the cluster does not serve.
	ErrInvalidResourceKind = errors.New("invalid resource kind")

	// ErrEmptyResourceName is returned when a resource is constructed without
	// a name.
	ErrEmptyResourceName = errors.New("resource name must not be empty")

	// ErrClusterNotConnected reports that an operation was attempted against a
	// cluster K8Sense has not connected to.
	ErrClusterNotConnected = errors.New("cluster not connected")
)
