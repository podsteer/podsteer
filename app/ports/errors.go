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

	// ErrLegacyAuthProvider means the kubeconfig authenticates through
	// client-go's built-in `auth-provider` mechanism, which PodSteer does not
	// register.
	//
	// ITS OWN SENTINEL FOR THE SAME REASON AS THE ONE ABOVE: the cluster was
	// never contacted, nothing is wrong with the credentials, and the failure
	// client-go raises — `no Auth Provider found for name "oidc"` — reads as a
	// bug in PodSteer to anybody who has not met it before.
	//
	// It is not registered by decision rather than by omission (ADR 10):
	// refreshing a token through the legacy oidc provider WRITES the
	// operator's kubeconfig, and SECURITY.md's account of what PodSteer puts
	// on disk does not include somebody's kubeconfig. The supported path is
	// the same one every managed provider already uses — an exec credential
	// plugin, `kubelogin` for OIDC — which PodSteer runs, with the login-shell
	// PATH resolution that makes it work from a Dock launch.
	ErrLegacyAuthProvider = errors.New("legacy auth-provider not registered")

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

	// ErrConflict means UpdateResource sent a PUT carrying the resourceVersion
	// the manifest was opened with, and the API server rejected it (HTTP 409)
	// because the object has since changed. This is optimistic concurrency
	// working as designed, not a fault: somebody or something else wrote the
	// object between the read that seeded the editor and this write, and the
	// only honest recovery is to reload the current object and re-apply the
	// edit against it — never to retry the same request, which would carry
	// the same stale resourceVersion and fail again.
	ErrConflict = errors.New("object changed on the cluster since it was read")

	// ErrEphemeralContainersUnsupported means the API server would not accept a
	// write to a pod's ephemeralcontainers subresource.
	//
	// Its own sentinel because the advice is unlike a plain 404. The
	// subresource is absent on a cluster older than 1.23, or one where the
	// EphemeralContainers feature gate is off — the pod is right there and the
	// request is well-formed, so reporting the API server's bare "not found"
	// would send an operator to look for a pod that exists. The adapter
	// distinguishes the two by reading the pod after the failure: a pod that
	// is present means the subresource, not the object, is what was missing.
	ErrEphemeralContainersUnsupported = errors.New("this cluster does not support ephemeral debug containers")

	// ErrResizeUnsupported means the API server would not accept a write to a
	// pod's resize subresource.
	//
	// The same shape as the sentinel above and told apart the same way: the
	// subresource is absent before Kubernetes 1.33, or where the
	// InPlacePodVerticalScaling gate is off, and its 404 would otherwise send
	// an operator to look for a pod that is right there. The adapter reads the
	// pod after the failure to tell the two apart.
	ErrResizeUnsupported = errors.New("this cluster does not support resizing a running pod")

	// ErrManifestRejected means the API server accepted the REQUEST but
	// declined the OBJECT (HTTP 422/Invalid) — a schema validation failure,
	// or an admission webhook's rejection. Distinct from
	// domain.ErrInvalidManifest, which is caught locally before any request
	// leaves this process: this is the cluster's own verdict, most often
	// surfaced through UpdateResource's dry run, and its message is the
	// diagnosis an operator needs to fix their manifest — not something
	// PodSteer can usefully paraphrase.
	ErrManifestRejected = errors.New("the cluster rejected the manifest")

	// ErrPodRejectedByAdmission means an admission controller declined a pod
	// PodSteer tried to create — Pod Security enforcing `restricted`, or a
	// validating webhook.
	//
	// THE DIRECT SIBLING OF ErrManifestRejected, and it exists because this
	// one arrives as HTTP 403 rather than 422. Left to classify's Forbidden
	// case it would be reported as "your account is not allowed to perform
	// this operation", which is false — the account was allowed, and the
	// OBJECT was declined — and it would send an operator to ask an
	// administrator for a permission they already hold. Worse, the canned
	// sentence throws away the API server's own message, and that message
	// (`violates PodSecurity "restricted:latest": allowPrivilegeEscalation
	// != false …`) names the exact field to change: it is the only thing
	// anybody can act on, so it travels verbatim exactly as a rejected
	// manifest's does.
	//
	// RBAC denials on the same request keep ErrForbidden. The two are told
	// apart by the API server's own wording, which is the only evidence
	// there is — a 403 carries no machine-readable reason distinguishing an
	// admission refusal from an authorisation one.
	ErrPodRejectedByAdmission = errors.New("an admission controller rejected the pod")

	// ErrPodDidNotStart means a pod PodSteer created was ACCEPTED by the API
	// server and then never ran — the kubelet could not create its container,
	// or could not fetch its image.
	//
	// A DIFFERENT FAILURE FROM ErrPodRejectedByAdmission, arriving at a
	// different moment and from a different component. Admission refuses at
	// create time and the refusal comes back on that call; this one happens
	// afterwards, on a node, and is readable only in the pod's own status.
	// Both are "the pod you asked for is not there", and collapsing them
	// would send somebody to argue with a policy about an image that does not
	// exist.
	//
	// The message carries the kubelet's reason and its own words, verbatim,
	// for ErrPodRejectedByAdmission's reason: "container has runAsNonRoot and
	// image will run as root" names the fix, and nothing PodSteer could write
	// in its place would.
	ErrPodDidNotStart = errors.New("the pod never started")

	// ErrClusterShellNotReusable means a pod offered for an in-cluster shell
	// could not be taken over: it is not one PodSteer created, or it has left
	// the Running phase since it was listed.
	//
	// One sentinel for both halves deliberately, on the reasoning
	// ErrHelmPayloadUnreadable states: the next step is the same either way —
	// create a fresh shell — and splitting them would mean one branch telling
	// somebody about the state of a pod they did not choose. The MESSAGE says
	// which of the two it was, because a pod that has exited explains itself
	// and one that is not ours explains something different.
	ErrClusterShellNotReusable = errors.New("that pod cannot be reused as an in-cluster shell")

	// ErrTarMissing means a file copy could not run because the container
	// has no `tar` binary. Copying is `kubectl cp`'s mechanism exactly — a
	// tar stream over an exec session — so an image built FROM scratch, or a
	// distroless one, cannot take part, and that is the ordinary failure
	// for this feature rather than a fault in anything. Its own sentinel
	// because the advice is unlike every other error here: nothing about
	// credentials, the network or the cluster is wrong, and retrying
	// cannot help — the image needs tar, or a sidecar that has it.
	ErrTarMissing = errors.New("the container has no tar binary")

	// ErrCommandFailed means a command PodSteer ran inside a container
	// exited non-zero. The message carries what the command wrote to
	// stderr, verbatim, because that text — `tar: /nope: Cannot stat: No
	// such file or directory` — IS the diagnosis, and paraphrasing it would
	// throw away the only thing the operator can act on.
	ErrCommandFailed = errors.New("the command failed inside the container")

	// ErrProbeToolMissing means an in-cluster reachability probe found
	// nothing in the container to probe with — no nc, no curl, no wget. The
	// direct sibling of ErrTarMissing and it exists for the same reason: a
	// distroless or scratch image carries no such tool, that is the ordinary
	// failure for this feature rather than a fault in anything, and the
	// alternative wording — "unreachable" — would be a claim about somebody's
	// Service made on the strength of somebody else's image. Nothing about
	// the cluster, the credentials or the network is wrong, and retrying
	// cannot help; another container is the answer.
	ErrProbeToolMissing = errors.New("the container has nothing to probe with")

	// ErrHelmPayloadTooLarge means a Helm release payload expanded past the
	// ceiling decompression is allowed to reach (see helmPayloadLimit in
	// app/adapters/k8s/helm_payload.go) and was REFUSED rather than truncated.
	//
	// ITS OWN SENTINEL BECAUSE THE ALTERNATIVE IS THE DANGEROUS ONE. etcd's
	// 1 MiB object limit bounds the COMPRESSED release, and gzip expands by
	// three orders of magnitude when somebody wants it to — so the ceiling
	// is real rather than theoretical. A truncated release rendered as if it
	// were whole is worse than a refusal by a wide margin: the manifest tab
	// is read in order to decide something, and a manifest missing its last
	// documents looks exactly like a manifest that never had them. So the
	// read stops, says which release and revision it stopped on, and shows
	// nothing.
	//
	// Not retryable, and nothing about the cluster or the credentials is
	// wrong: the release is simply bigger than PodSteer will decompress.
	ErrHelmPayloadTooLarge = errors.New("the Helm release payload is larger than PodSteer will decompress")

	// ErrHelmPayloadUnreadable means a Secret named as a Helm release could
	// not be read as one — it is not of type `helm.sh/release.v1`, its
	// `owner`/`name`/`version` labels do not match what was asked for, or
	// what it holds is not a base64'd (optionally gzip'd) release document.
	//
	// THE TYPE AND LABEL CHECKS ARE A SHAPE CHECK, NOT A BOUNDARY, and that
	// distinction is worth keeping straight here because it is easy to
	// overclaim. The Secret's name is DERIVED —
	// `sh.helm.release.v1.<release>.v<n>` — so anybody who can create a
	// Secret in that namespace can put an object at it, and can set its type
	// and labels while they are there. Verifying is therefore about
	// ACCIDENTS rather than forgery: a backup, a hand-made copy or a restore
	// under the wrong name is refused instead of being decoded and rendered
	// as the release it is not. What makes a hostile document merely a
	// document is the rest of the read — bounded decompression, a narrow
	// unmarshal target, and a manifest whose Secrets are masked.
	//
	// The `owner=helm` label is checked because the release LISTING selects
	// on it: without it, an object the list cannot see would still be
	// readable here, and the two acts would disagree about what a release is.
	//
	// One sentinel for both halves deliberately: "this is not a release" and
	// "this release will not decode" lead to the same next step, which is
	// looking at the Secret itself in the Secrets catalogue, and splitting
	// them would mean the mismatch case telling somebody something about the
	// contents of an object nothing here decoded.
	ErrHelmPayloadUnreadable = errors.New("that Secret could not be read as a Helm release")

	// ErrMetricsQueryRejected means a monitoring backend answered and
	// declined the expression PodSteer sent it.
	//
	// THE DIRECT SIBLING OF ErrManifestRejected, and for the identical
	// reason: the request was made, something on the other side understood it
	// and said no, and its own words are the diagnosis. A backend rejecting
	// PromQL says which function or label it objected to, and PodSteer
	// paraphrasing that would throw away the only thing anybody can act on —
	// including, in the case that matters most, evidence that the expression
	// table needs changing.
	ErrMetricsQueryRejected = errors.New("the monitoring backend rejected the query")

	// ErrMetricsBackendAuth means a monitoring backend has authentication of
	// its own in front of it and refused the request on that ground.
	//
	// ITS OWN SENTINEL BECAUSE NOTHING THE OPERATOR CAN DO TO THE QUERY WILL
	// HELP. A kube-rbac-proxy or an oauth proxy in front of Prometheus wants
	// a credential of its own, and the API server's service proxy strips the
	// Authorization header on the way through — so this can never succeed by
	// this route, however the expression is changed and whatever the account
	// is granted. Folded into a rejection it sends somebody to debug PromQL;
	// folded into a refusal it sends them to ask for a Kubernetes permission
	// they already have.
	ErrMetricsBackendAuth = errors.New("the monitoring backend requires its own credential")

	// ErrMetricsQueryTooLarge means a backend's answer exceeded what PodSteer
	// will read.
	//
	// REFUSED UNDECODED. The body arrives from a system PodSteer does not
	// control and did not size, so it is read through a limited reader and
	// abandoned at the cap rather than decoded and then judged — decoding
	// first is how a bounded read becomes an unbounded allocation.
	ErrMetricsQueryTooLarge = errors.New("the monitoring backend's answer is too large to read")

	// ErrSettingsReadOnly means this process opened the settings without the
	// ability to write them.
	//
	// It exists for `podsteer mcp`, which SECURITY.md promises writes nothing
	// anywhere. The promise is kept structurally rather than by everyone
	// remembering: the subcommand opens the store read-only, and any code
	// path that later tried to change a setting is refused here rather than
	// quietly creating a file in somebody's configuration directory.
	ErrSettingsReadOnly = errors.New("settings are open read-only")

	// ErrSettingsFromFuture means the settings file declares a version this
	// build does not understand, so it is read for what is recognisable and
	// never written back.
	//
	// REFUSING TO WRITE IS THE ONLY OUTCOME THAT CANNOT LOSE ANYTHING.
	// Unknown top-level sections round-trip verbatim, which protects against
	// a newer build ADDING one — but not against a field having MOVED between
	// sections, which this build would read into the old section and write
	// back there, silently undoing the newer build's migration. So a newer
	// file is treated as read-only: the operator keeps their settings, and
	// the interface says why a change did not stick.
	ErrSettingsFromFuture = errors.New("the settings file was written by a newer version of PodSteer")

	// ErrSettingsUnavailable means the settings file could not be written —
	// the configuration directory could not be located, or the write itself
	// failed.
	ErrSettingsUnavailable = errors.New("settings could not be saved")
)
