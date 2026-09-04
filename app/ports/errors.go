package ports

import "errors"

// Sentinel errors that outbound adapters map their infrastructure failures
// onto.
//
// They exist so the application layer can react to *why* a call failed without
// importing client-go to inspect a *k8serrors.StatusError. "Your token
// expired" and "the API server is down" call for different UI, but only the
// adapter is in a position to tell them apart — so the adapter classifies, and
// everything inward compares with errors.Is.
//
// Adapters must wrap rather than replace the underlying error, so the original
// cause survives for logging.
var (
	// ErrUnreachable means the API server could not be contacted at all:
	// DNS failure, refused connection, timeout, or a dead tunnel.
	ErrUnreachable = errors.New("cluster unreachable")

	// The three transport failures worth telling apart. Each is wrapped
	// ALONGSIDE ErrUnreachable, never instead of it, so every existing
	// errors.Is(err, ErrUnreachable) — including the assessment's retry
	// decision — keeps working unchanged.
	//
	// They exist because they imply OPPOSITE actions and were being reported
	// with identical text. "Nothing answered" and "something answered and
	// refused" are different diagnoses, and telling an operator to check their
	// network when the API server actively refused them sends them to look in
	// the wrong place entirely.

	// ErrNameNotResolved means the server's hostname did not resolve. The
	// machine is not on a network that knows that name — or the name is wrong.
	ErrNameNotResolved = errors.New("name not resolved")

	// ErrConnectionRefused means something at that address answered and
	// declined the connection. The route works; the API server is not
	// listening — wrong port, or the cluster is down.
	ErrConnectionRefused = errors.New("connection refused")

	// ErrNoResponse means nothing answered at all: a timeout, or no route to
	// the host. The usual cause is being off the network that reaches it.
	ErrNoResponse = errors.New("no response")

	// ErrUnauthenticated means the credentials were rejected (HTTP 401),
	// typically an expired token or a stale exec-plugin cache.
	ErrUnauthenticated = errors.New("not authenticated")

	// ErrForbidden means the credentials were accepted but RBAC denied the
	// operation (HTTP 403).
	ErrForbidden = errors.New("forbidden")

	// ErrNotFound means the requested resource does not exist (HTTP 404).
	ErrNotFound = errors.New("resource not found")

	// ErrKubeconfigUnavailable means the local kubeconfig could not be read
	// or parsed, so no cluster can be discovered.
	ErrKubeconfigUnavailable = errors.New("kubeconfig unavailable")

	// ErrKubeconfigInvalid means text offered as a kubeconfig could not be
	// parsed as one, or parsed but described nothing to add.
	ErrKubeconfigInvalid = errors.New("not a usable kubeconfig")

	// ErrKubeconfigConflict means the incoming kubeconfig names a context the
	// local one already defines. Refused rather than merged: replacing a
	// working context's credentials is not something to do on a paste.
	ErrKubeconfigConflict = errors.New("context already exists")

	// ErrCredentialPluginMissing means the kubeconfig authenticates through an
	// executable that is not on PATH.
	//
	// ITS OWN SENTINEL BECAUSE THE ADVICE IS UNRELATED to everything else here.
	// Every managed cluster authenticates this way — `aws eks get-token` for
	// EKS, `gke-gcloud-auth-plugin` for GKE, `kubelogin` for AKS — and when the
	// binary cannot be found the failure surfaces as a cluster that will not
	// connect. It is neither: the cluster was never contacted, the credentials
	// are fine, and a program is missing. Reported as unreachable it sends
	// somebody to check a VPN.
	ErrCredentialPluginMissing = errors.New("credential plugin not found")

	// ErrCountUnavailable means the API server did not report how many objects
	// a list holds.
	//
	// Counting asks for one object and reads the server's own count of the
	// rest, which every server since Kubernetes 1.15 reports. One that does
	// not leaves the caller holding a single object and no idea whether it is
	// the only one — so this is returned rather than the 1 that would imply.
	ErrCountUnavailable = errors.New("list total unavailable")

	// ErrMetricsUnavailable means the cluster serves no metrics API.
	//
	// This is an ordinary condition, not a fault: metrics-server is an add-on
	// and plenty of clusters run without it. Callers are expected to carry on
	// and render usage columns as unmeasured.
	ErrMetricsUnavailable = errors.New("metrics API unavailable")

	// ErrDisruptionBudget means a PodDisruptionBudget refused an eviction
	// (HTTP 429). Its own sentinel rather than folding into ErrForbidden:
	// RBAC allowed the request and the object's OWN policy declined it,
	// which calls for waiting and retrying rather than for different
	// credentials — the two look identical as a bare "denied" otherwise.
	ErrDisruptionBudget = errors.New("disruption budget refused eviction")

	// ErrDrainRefused means PlanDrain found at least one pod DrainNode may
	// not evict as the caller asked. Mirrors kubectl's own behaviour:
	// draining stops before anything is evicted rather than doing part of a
	// drain and leaving the rest, because a caller cannot tell "capacity
	// freed" from "capacity freed except for the pods that mattered" without
	// reading the report.
	ErrDrainRefused = errors.New("drain refused: at least one pod cannot be evicted as asked")
	// ErrReadOnly means the cluster is marked read-only in PodSteer.
	//
	// THIS IS A GUARD AGAINST THE UI'S OWN BUGS, NOT A PERMISSION. The flag
	// lives entirely on the client — an operator ticks it in OrganiseDialog,
	// the frontend calls ClusterAPI.SetReadOnly, and application.Registry
	// remembers it per cluster. Checking it again here means a write control
	// the frontend forgot to disable, or a stale cache, is refused instead of
	// reaching the cluster — but it is never a security boundary: RBAC is the
	// only thing that actually decides what these credentials may do, and an
	// operator who clears the flag in Organise can make the exact same write
	// a moment later. See SECURITY.md, "What PodSteer can do".
	ErrReadOnly = errors.New("cluster is read-only")
	// ErrInvalidPort means a port number handed to a local-port operation —
	// probing or binding one on the operator's own machine — falls outside
	// the range TCP actually has, 1-65535.
	ErrInvalidPort = errors.New("port must be between 1 and 65535")
)
