package domain

import "errors"

// Sentinel errors raised when a domain invariant is violated. They are
// deliberately comparable with errors.Is so that outer layers can map them
// onto transport-level concerns (an HTTP status, a UI message) without
// pattern-matching on strings.
var (
	// ErrEmptyClusterID is returned when a cluster identifier is blank.
	ErrEmptyClusterID = errors.New("cluster id must not be empty")
	// A vertical bar separates the cluster from the rest of a cache key, so a
	// context name containing one could have another cluster's entries
	// dropped along with its own.
	ErrInvalidClusterID = errors.New(`cluster id must not contain "|"`)

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
	// cluster PodSteer has not connected to.
	ErrClusterNotConnected = errors.New("cluster not connected")

	// ErrSecretKeyNotFound reports that a Secret exists but does not hold the
	// key that was asked for.
	//
	// Distinct from an empty value on purpose: a pod naming a key its Secret
	// does not have is misconfigured, or the Secret changed underneath it,
	// and either is worth saying rather than rendering nothing and letting it
	// read as "this variable is blank".
	ErrSecretKeyNotFound = errors.New("secret key not found")

	// ErrUnsupportedWorkloadKind is returned when an operation is attempted
	// against a WorkloadKind that does not support it — suspending a
	// Deployment, say. The application layer checks this before an adapter is
	// ever reached, mirroring how ScaleWorkload validates its replica count.
	ErrUnsupportedWorkloadKind = errors.New("unsupported workload kind")

	// ErrInvalidKey is returned when a Secret or ConfigMap data key is empty,
	// contains characters Kubernetes does not allow in one, or — for a
	// ConfigMap — names a key that currently holds binary data rather than
	// text.
	//
	// The application layer checks the format before an adapter is ever
	// reached, mirroring ErrUnsupportedWorkloadKind above; the binaryData
	// case can only be checked in the adapter, because it requires reading
	// the object first.
	ErrInvalidKey = errors.New("invalid key")

	// ErrInvalidImageReference is returned when SetImage is asked to write an
	// image string that does not look like an image reference — empty,
	// containing whitespace, or an empty tag — checked by
	// ValidImageReference. Caught in the application layer before an adapter
	// is ever reached, mirroring ErrInvalidKey above.
	ErrInvalidImageReference = errors.New("invalid image reference")

	// ErrNotTLSSecret reports that InspectTLSSecret was asked to parse a
	// Secret that is neither type kubernetes.io/tls nor carries a tls.crt
	// key by convention — there is no certificate here to inspect at all.
	ErrNotTLSSecret = errors.New("secret is not a TLS secret")

	// ErrInvalidCertificate reports that certificate material could not be
	// parsed as PEM-encoded X.509 — a Secret whose tls.crt or ca.crt holds
	// something else, or nothing.
	ErrInvalidCertificate = errors.New("invalid certificate data")

	// ErrContainerNotAttachable reports that AttachToPod was asked to attach
	// to a container whose own spec does not declare both a tty and stdin.
	// Kubernetes' attach subresource accepts the request regardless and only
	// fails once the PTY negotiation begins, with a server error that names
	// neither the pod nor the reason — so this is checked locally, before any
	// request reaches the cluster, and says exactly what the container needs.
	ErrContainerNotAttachable = errors.New("container has no tty; attach needs `tty: true` and `stdin: true` on the container")

	// ErrInvalidManifest reports that a manifest offered to UpdateResource
	// could not be applied as written: it is not valid YAML/JSON for a
	// Kubernetes object, it is missing apiVersion, kind or metadata.name, it
	// names a namespaced kind with no metadata.namespace, or it contains more
	// than one object. Checked before any request reaches the cluster, the
	// same way ErrInvalidKey is checked before SetSecretKey ever dials out.
	ErrInvalidManifest = errors.New("invalid manifest")
)
