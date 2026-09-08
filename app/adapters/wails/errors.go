package wails

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// ErrorCode is the stable, machine-readable classification of a failed call.
//
// Wails serialises a returned error to the string produced by Error() and
// rejects the JavaScript promise with it, so there is no room for a structured
// error object on the wire. PodSteer therefore encodes the code as a bracketed
// prefix — "[forbidden] you are not allowed to list pods ..." — which the
// frontend parses back out (see web/src/lib/api/errors.ts).
//
// The prefix is a contract: the frontend branches on it to decide whether to
// offer a reconnect, a namespace change, or just a message.
type ErrorCode string

const (
	// CodeNoActiveCluster means no cluster is connected yet.
	CodeNoActiveCluster ErrorCode = "no_active_cluster"
	// CodeClusterNotFound means the kubeconfig has no such context.
	CodeClusterNotFound ErrorCode = "cluster_not_found"
	// CodeUnreachable means the API server could not be contacted.
	CodeUnreachable ErrorCode = "unreachable"
	// CodeUnauthenticated means the credentials were rejected.
	CodeUnauthenticated ErrorCode = "unauthenticated"
	// CodeForbidden means RBAC denied the operation.
	CodeForbidden ErrorCode = "forbidden"
	// CodeReadOnly means the cluster is marked read-only in PodSteer — a
	// local guard the operator set, not an RBAC refusal. Its own code rather
	// than CodeForbidden because the advice is different: CodeForbidden sends
	// somebody to their cluster administrator, and there is nobody to ask
	// here — the fix is in Organise, on this machine.
	CodeReadOnly ErrorCode = "read_only"
	// CodeNotFound means the requested resource does not exist.
	CodeNotFound ErrorCode = "not_found"
	// CodeKubeconfig means the local kubeconfig could not be read.
	CodeKubeconfig ErrorCode = "kubeconfig_unavailable"
	// CodeLegacyAuthProvider means the kubeconfig authenticates through
	// client-go's legacy `auth-provider`, which PodSteer does not register by
	// decision (ADR 10). Its own code rather than CodeCredentialPlugin: both
	// are about authentication before the request, and the fix is different —
	// one installs a binary, the other converts a context.
	CodeLegacyAuthProvider ErrorCode = "legacy_auth_provider"

	// CodeCredentialPlugin means the kubeconfig authenticates through an
	// executable that is not on PATH. Its own code rather than unreachable,
	// because the cluster was never contacted and offering Retry would repeat
	// a failure nothing about the cluster can fix.
	CodeCredentialPlugin ErrorCode = "credential_plugin_missing"
	// CodeCancelled means the call was cancelled or timed out.
	CodeCancelled ErrorCode = "cancelled"
	// CodeInvalidInput means the frontend sent an unusable argument.
	CodeInvalidInput ErrorCode = "invalid_input"
	// CodeDisruptionBudget means a PodDisruptionBudget refused an eviction.
	// Its own code rather than forbidden: RBAC allowed the request, and the
	// OBJECT'S OWN POLICY declined it — which calls for waiting and trying
	// again, not for different credentials.
	CodeDisruptionBudget ErrorCode = "disruption_budget"
	// CodeConflict means UpdateResource's PUT carried a resourceVersion the
	// API server no longer recognises — the object changed since the
	// manifest was read. Its own code because the recovery is specific and
	// unlike anything else here: reload the object and re-apply the edit,
	// never just retry the same request, which would resend the same stale
	// resourceVersion and fail again.
	CodeConflict ErrorCode = "conflict"
	// CodeEphemeralUnsupported means the cluster will not accept an ephemeral
	// debug container — the pods/ephemeralcontainers subresource is not served
	// (a cluster older than 1.23, or with the feature gate off). Its own code
	// rather than not_found, because the pod is present and the fix is about
	// the cluster, not the object: reporting it as "not found" would send an
	// operator to look for a pod that is right there.
	CodeEphemeralUnsupported ErrorCode = "ephemeral_unsupported"

	// CodeResizeUnsupported means the cluster does not serve pods/resize —
	// older than 1.33, or the feature gate off. Its own code rather than
	// CodeNotFound, which would send somebody to look for a pod that is on
	// screen in front of them.
	CodeResizeUnsupported ErrorCode = "resize_unsupported"
	// CodeTarMissing means a file copy found no tar binary in the
	// container. Its own code because nothing else here fits: the cluster,
	// the credentials and the network are all fine, retrying cannot help,
	// and the advice — the image needs tar, or a sidecar that has it — is
	// unlike any other message this file produces.
	CodeTarMissing ErrorCode = "tar_missing"
	// CodeCommandFailed means a command PodSteer ran inside a container
	// exited non-zero. The message is what the command wrote to stderr,
	// verbatim, because for tar that text IS the diagnosis.
	CodeCommandFailed ErrorCode = "command_failed"
	// CodeTransferLimit means a file copy crossed the configured byte or
	// entry ceiling. Its own code rather than invalid_input because the
	// operator did nothing wrong: the transfer was simply bigger than the
	// default allows, and the message names the setting that raises it.
	CodeTransferLimit ErrorCode = "transfer_limit"
	// CodeProbeToolMissing means an in-cluster reachability probe found
	// nothing in the container to probe with. The direct sibling of
	// CodeTarMissing and it exists for the same reason: nothing about the
	// cluster or the credentials is wrong, retrying cannot help, and — the
	// part that matters here — it is NOT an answer about the target. Folded
	// into a failed probe it would draw a red cross against somebody's
	// Service on the strength of somebody else's image.
	CodeProbeToolMissing ErrorCode = "probe_tool_missing"
	// CodeProbeUnavailable means the probe was never attempted, because the
	// object or the vantage cannot answer: an ExternalName Service has no
	// cluster address, a UDP port cannot be connected to, an Ingress host is
	// not something PodSteer will open a connection to. Its own code because
	// the panel renders these where the result would have gone rather than as
	// a failure — they are facts that stay true however many times the button
	// is pressed, the same Bounded-not-Unreadable distinction the dependency
	// map already makes.
	CodeProbeUnavailable ErrorCode = "probe_unavailable"
	// CodeHelmPayloadTooLarge means a Helm release decompressed past the
	// ceiling and was REFUSED rather than truncated.
	//
	// Its own code because nothing else here fits and because the advice is
	// unlike any of them: the cluster, the credentials and the network are
	// all fine, the object is exactly what it claimed to be, retrying cannot
	// help, and the honest next step is `helm get manifest` in the operator's
	// own shell. Folded into internal it would read as a fault in PodSteer;
	// folded into invalid_input it would read as the operator's mistake. It
	// is neither — it is a release bigger than PodSteer will decompress.
	CodeHelmPayloadTooLarge ErrorCode = "helm_payload_too_large"
	// CodeHelmPayloadUnreadable means a Secret named as a Helm release could
	// not be read as one: wrong type, labels that do not match the release
	// and revision asked for, or contents that are not a release document.
	//
	// Its own code rather than not_found, because the object is THERE — and
	// reporting it as absent would send somebody looking for a release
	// Secret that is sitting in front of them — and rather than internal,
	// because nothing failed: PodSteer declined to decode something that did
	// not verify as what it was asked for.
	CodeHelmPayloadUnreadable ErrorCode = "helm_payload_unreadable"
	// CodeSettingsReadOnly means this process does not write the settings
	// file at all. Its own code rather than internal because nothing failed:
	// the answer is a fact about how PodSteer was started.
	CodeSettingsReadOnly ErrorCode = "settings_read_only"
	// CodeSettingsFromFuture means the settings file was written by a newer
	// PodSteer, so it is read and never saved over. Its own code because the
	// only recovery is outside the application — upgrade, or move the file
	// aside — and offering a retry would repeat a refusal that is working as
	// designed.
	CodeSettingsFromFuture ErrorCode = "settings_from_future"
	// CodeSettingsUnavailable means the settings could not be written: no
	// configuration directory, or a failed write.
	CodeSettingsUnavailable ErrorCode = "settings_unavailable"
	// CodePodRejected means an admission controller declined a pod PodSteer
	// tried to create — Pod Security enforcing `restricted`, or a validating
	// webhook. Its own code rather than forbidden, because the account WAS
	// allowed and the object was declined: reported as forbidden it sends
	// somebody to ask for a permission they already hold. The message is the
	// API server's own, verbatim, exactly as CodeInvalidInput carries
	// ErrManifestRejected's — that text names the field to change and is the
	// only thing anybody can act on.
	CodePodRejected ErrorCode = "pod_rejected"
	// CodePodDidNotStart means a pod PodSteer created was accepted and then
	// never ran: the kubelet could not create its container or could not
	// fetch its image. Its own code rather than internal, because the cluster
	// SAID what was wrong and an operator can act on it — the whole reason
	// this code exists is that the answer used to be "an unexpected error
	// occurred" while the pod's status held "container has runAsNonRoot and
	// image will run as root".
	CodePodDidNotStart ErrorCode = "pod_did_not_start"
	// CodeInternal is the fallback for anything unclassified.
	CodeInternal ErrorCode = "internal"
)

// errProbeNoContainer is raised when a probe or an image report is asked for
// without naming the pod and container it runs in or describes.
var errProbeNoContainer = errors.New("a pod and a container are required")

// errNoLocalPath is raised when a file copy names a local path that does not
// exist, or is not the kind of thing the direction needs — a file where a
// directory was asked for.
var errNoLocalPath = errors.New("local path unusable")

// errInvalidURL is raised when the frontend asks the shell to open something
// that is not a plain http(s) address.
var errInvalidURL = errors.New("invalid URL")

// errNotFound is raised when the frontend asks for something the backend has
// no record of — a licence text whose id does not exist, say.
var errNotFound = errors.New("not found")

// errEmptySuggestedName is raised when SaveTextFile is asked to save without
// a filename to seed the dialog with.
var errEmptySuggestedName = errors.New("a suggested filename is required")

// errUnreadableTextFile is raised when ReadTextFile is pointed at a file it
// will not hand the webview — one past maxTextFileBytes, or an empty one,
// which is otherwise indistinguishable from the operator cancelling.
//
// Invalid input rather than an internal failure: the operator chose the file,
// so the message names what is wrong with it and they choose again.
var errUnreadableTextFile = errors.New("that file cannot be read as text")

// errHelmNamespaceRequired is raised when a release payload read arrives
// scoped to every namespace.
//
// A release payload lives in ONE namespace's Secret, so "all namespaces" is
// not a scope this read has. Refused as invalid input rather than passed down
// to become a GET against whatever the client's default namespace happens to
// be — which would be a read of the wrong object under a name that composed
// perfectly well, the class of quiet mistake the adapter's own verification
// exists to catch.
var errHelmNamespaceRequired = errors.New("a namespace is required to read a Helm release")

// errInvalidBulkAction is raised when PlanBulk is asked to plan an action
// that is not one of domain's BulkAction values — a frontend bug, reported
// as invalid input rather than swallowed into an empty plan.
var errInvalidBulkAction = errors.New("unknown bulk action")

// apiError logs the full failure and returns the sanitised error the frontend
// receives.
//
// The split matters: the log keeps the entire wrapped chain, including the
// client-go detail an engineer needs, while the UI gets one sentence an
// operator can act on. Sending the raw chain to the frontend would put API
// server URLs and internal paths on screen for no benefit.
func apiError(logger *slog.Logger, op string, err error) error {
	code, message := classifyError(err)

	logger.Error("call failed",
		slog.String("op", op),
		slog.String("code", string(code)),
		slog.String("error", err.Error()))

	return fmt.Errorf("[%s] %s", code, message)
}

// credentialPluginMessage explains a credential plugin that could not be run.
//
// NAMES THE BINARY, because that is the entire diagnosis and the operator
// cannot get at the log. "Could not authenticate" would send somebody to
// re-run `aws sso login`, which would succeed and change nothing.
//
// The second sentence exists because the commonest form of this is not a
// missing tool at all: the operator has `aws` and uses it daily, and PodSteer
// simply cannot see it. A desktop application launched from Finder or by
// Homebrew inherits launchd's PATH, not a shell's. PodSteer asks the login
// shell at startup to avoid exactly this, so reaching here means that did not
// work — a shell that never finished, or a tool genuinely absent.
func credentialPluginMessage(err error) string {
	name := "a credential plugin"
	if quoted := quotedName(err.Error()); quoted != "" {
		name = quoted
	}

	return fmt.Sprintf(
		"This cluster authenticates by running %s, which PodSteer could not find. "+
			"If it works in your terminal, PodSteer is starting without your shell's PATH — "+
			"launching it from a terminal confirms that. Otherwise the tool needs installing.",
		name)
}

// legacyAuthProviderMessage says which mechanism the kubeconfig asked for and
// what to do instead.
//
// IT NAMES THE FIX, which is the whole point of the code existing: client-go's
// own words are `no Auth Provider found for name "oidc"`, which sounds like
// something PodSteer failed to install. The oidc case gets the specific
// answer because it is the one people actually hit — every other provider in
// that mechanism belongs to a cloud whose own plugin already replaced it.
func legacyAuthProviderMessage(err error) string {
	provider := quotedName(err.Error())

	if provider == "oidc" {
		return "This context signs in with the old built-in OIDC provider, which PodSteer does not use: " +
			"refreshing a token through it rewrites your kubeconfig, and PodSteer does not write that file. " +
			"Convert the context to an exec credential plugin — kubelogin is the usual one — and PodSteer " +
			"will run it the same way it runs the AWS, GKE and AKS plugins."
	}

	name := "an auth-provider"
	if provider != "" {
		name = fmt.Sprintf("the %q auth-provider", provider)
	}
	return fmt.Sprintf(
		"This context signs in with %s, a kubectl mechanism deprecated since Kubernetes 1.22 that "+
			"PodSteer does not carry. Convert the context to an exec credential plugin — the same "+
			"mechanism the AWS, GKE and AKS plugins already use — and PodSteer will run it.",
		name)
}

// quotedName pulls the binary name out of the message the adapter composed.
func quotedName(message string) string {
	start := strings.Index(message, `"`)
	if start < 0 {
		return ""
	}
	rest := message[start+1:]

	end := strings.Index(rest, `"`)
	if end <= 0 {
		return ""
	}
	return rest[:end]
}

// classifyError maps an internal error onto a code and an operator-facing
// message.
//
// Order matters: the most specific conditions are tested first, because a
// single error routinely wraps several sentinels — a failed connect carries
// both "cluster unreachable" and the underlying network error.
func classifyError(err error) (ErrorCode, string) {
	switch {
	case errors.Is(err, domain.ErrNoActiveCluster):
		return CodeNoActiveCluster, "No cluster is connected yet"

	case errors.Is(err, domain.ErrClusterNotFound):
		// Reads as an accusation without the second half. The commonest cause
		// is not deletion but REPLACEMENT — a cluster rebuilt under a new
		// context name, or a kubeconfig regenerated by whatever provisions it
		// — and saying so turns a dead end into the next step.
		return CodeClusterNotFound, "That cluster is no longer in your kubeconfig. It may have been removed, renamed, or replaced when the cluster was rebuilt."

	// BEFORE THE TRANSPORT CASES. A missing credential plugin arrives wrapped
	// in whatever the caller was doing, and reported as unreachable it sends
	// somebody to check a VPN for a cluster that was never contacted.
	case errors.Is(err, ports.ErrCredentialPluginMissing):
		return CodeCredentialPlugin, credentialPluginMessage(err)

	// AND BEFORE THEM TOO. Left to the transport cases this reads as a
	// cluster that will not answer, when nothing was ever dialled: the
	// kubeconfig names an auth mechanism this build does not carry, and the
	// message has to say which and what replaces it — or the failure looks
	// like a bug in PodSteer.
	case errors.Is(err, ports.ErrLegacyAuthProvider):
		return CodeLegacyAuthProvider, legacyAuthProviderMessage(err)

	// ALSO BEFORE THE RBAC CASES, for the same reason: ErrReadOnly is PodSteer
	// refusing on its own, before the request ever reaches the API server, so
	// it must never be reported as CodeForbidden — that would send somebody
	// to argue with an administrator about a restriction the administrator
	// never set.
	case errors.Is(err, ports.ErrReadOnly):
		return CodeReadOnly, "This cluster is marked read-only in PodSteer. Change that under Organise."

	// BEFORE THE TRANSPORT CASES TOO: a runtime that cannot start tar
	// answers the exec with an internal error, which classify wraps as
	// unreachable — and the cluster was reached perfectly well.
	case errors.Is(err, ports.ErrTarMissing):
		return CodeTarMissing, "The container has no tar binary, and copying files runs tar inside it — the same way kubectl cp does. Add tar to the image, or copy through a container that has it."

	// BEFORE THE TRANSPORT CASES for the same reason tar's sentinel is: the
	// exec reached the container perfectly well, and the container simply has
	// no tool in it.
	case errors.Is(err, ports.ErrProbeToolMissing):
		return CodeProbeToolMissing, "This container has nothing to probe with — no nc, curl or wget. That says nothing about whether the target is reachable; probe from a container that has one of them."

	// BEFORE THE INVALID-INPUT CASES: these are facts about the object, and
	// their own sentences are already the explanation, so they travel
	// verbatim rather than being replaced by a generic one.
	case errors.Is(err, domain.ErrProbeNoPort),
		errors.Is(err, domain.ErrProbeNotTCP),
		errors.Is(err, domain.ErrProbeNoAddress),
		errors.Is(err, domain.ErrProbeVantageUnavailable),
		errors.Is(err, domain.ErrProbeOutputUnreadable):
		return CodeProbeUnavailable, err.Error()

	// Verbatim, because the message is what tar wrote to stderr and that
	// is the diagnosis — see ports.ErrCommandFailed.
	case errors.Is(err, ports.ErrCommandFailed):
		return CodeCommandFailed, err.Error()

	case errors.Is(err, domain.ErrTransferTooLarge):
		return CodeTransferLimit, err.Error() + ". PODSTEER_COPY_MAX_BYTES and PODSTEER_COPY_MAX_ENTRIES raise the ceiling."

	case errors.Is(err, ports.ErrUnauthenticated):
		return CodeUnauthenticated, "Your credentials were rejected — they may have expired"

	// BEFORE ErrForbidden, which it is also wrapped in — an admission refusal
	// arrives as HTTP 403 and classify has no way to tell one from an RBAC
	// denial. VERBATIM, on the line ErrManifestRejected and ErrCommandFailed
	// sit on: the API server's message names the exact field the pod violated
	// and paraphrasing it throws away the only actionable thing in it.
	case errors.Is(err, ports.ErrPodRejectedByAdmission):
		return CodePodRejected, err.Error()

	// VERBATIM, for the same reason and with more force: this message is the
	// KUBELET's, and it names the mismatch between the image somebody chose
	// and the security context the pod asks for. Nothing PodSteer could
	// summarise in its place would tell them which of the two to change.
	case errors.Is(err, ports.ErrPodDidNotStart):
		return CodePodDidNotStart, err.Error()

	// The four ways resolving a Service to a forwardable pod can fail, each
	// with its own sentence and each actionable: name a port, pick a Service
	// that selects pods, fix a targetPort no container declares — or wait,
	// for the one that is the cluster's state rather than the request's.
	case errors.Is(err, domain.ErrNoReadyEndpoint):
		return CodeNotFound, err.Error()

	case errors.Is(err, domain.ErrServiceHasNoSelector),
		errors.Is(err, domain.ErrServicePortNotFound),
		errors.Is(err, domain.ErrServicePortAmbiguous),
		errors.Is(err, domain.ErrTargetPortUnresolved):
		return CodeInvalidInput, err.Error()

	case errors.Is(err, ports.ErrForbidden):
		return CodeForbidden, "Your account is not allowed to perform this operation"

	// BEFORE ErrNotFound, and it KEEPS the not_found code deliberately. A
	// reaped revision genuinely is not there, so the code is right and the
	// frontend's branching on it should not change; what is wrong is the
	// generic sentence, because this is the ORDINARY case rather than a
	// fault — Helm's own default keeps ten revisions and deletes the rest,
	// so a history entry routinely outlives the Secret behind it. "The
	// requested resource no longer exists" reads as though something went
	// wrong; this says what actually happened.
	case errors.Is(err, domain.ErrHelmRevisionNotFound):
		return CodeNotFound, "Helm no longer has that revision. Helm keeps only the last few revisions of a release — ten by default — and deletes the rest, so an older entry in a release's history has no Secret behind it any more."

	// BEFORE THE TRANSPORT AND RBAC CASES, on the same line ErrTarMissing
	// sits: the cluster was reached perfectly well and the object was
	// returned. What follows is PodSteer declining to decode it, or refusing
	// to decompress it further, and neither is a fault in anything the
	// operator can fix by retrying or by different credentials.
	case errors.Is(err, ports.ErrHelmPayloadTooLarge):
		return CodeHelmPayloadTooLarge, "That release is larger than PodSteer will decompress — 32 MiB of rendered manifest, chart and values. It was refused rather than cut short, because a manifest shown as whole when it is not is worse than no manifest: read it with `helm get manifest` instead."

	case errors.Is(err, ports.ErrHelmPayloadUnreadable):
		return CodeHelmPayloadUnreadable, "That Secret is not the Helm release it was asked for: its type or its release and revision labels do not match, or what it holds is not a release document. PodSteer will not decode a Secret that does not verify as the release requested — the object name Helm uses is derived, so anyone who can create a Secret can put one at that name."

	// BEFORE ErrNotFound: the subresource being unavailable arrives as its own
	// sentinel (the adapter already told a missing subresource apart from a
	// missing pod), and it must never read as "the pod is gone".
	// The same shape as the ephemeral-container case below it, and told apart
	// from a missing pod in the adapter for the same reason: the pod is on
	// screen in front of them.
	case errors.Is(err, ports.ErrResizeUnsupported):
		return CodeResizeUnsupported, "This cluster cannot resize a running pod — its API server is older than " +
			"Kubernetes 1.33, or in-place vertical scaling is turned off. Changing the workload's own " +
			"resources and letting it roll is the way to do it here."

	case errors.Is(err, ports.ErrEphemeralContainersUnsupported):
		return CodeEphemeralUnsupported, "This cluster does not support ephemeral debug containers — its API server is too old, or the feature is turned off."

	case errors.Is(err, ports.ErrNotFound):
		return CodeNotFound, "The requested resource no longer exists"

	// BEFORE ErrForbidden's message would apply: a 429 from the eviction
	// subresource is not RBAC, and telling an operator their account is not
	// allowed to evict a pod sends them to ask for a permission they already
	// have.
	case errors.Is(err, ports.ErrDisruptionBudget):
		return CodeDisruptionBudget, "A PodDisruptionBudget refused the eviction: it would leave the workload below its minimum."

	// ALSO BEFORE ErrForbidden's message would apply, for the same shape of
	// reason: a 409 from an Update is not RBAC — the request was permitted
	// and the object's resourceVersion is simply stale.
	case errors.Is(err, ports.ErrConflict):
		return CodeConflict, "This object changed on the cluster since you opened it. Reload it and re-apply your edit."

	// The three transport failures are ONE code and three messages. The code is
	// the category the UI branches on — including whether to offer Retry — and
	// splitting it would have meant changing that logic to say the same thing.
	// The message is where the diagnosis belongs.
	//
	// None of them tells the operator to "check your network". That advice fits
	// every one of these situations and helps in none of them, and it implies
	// they did something wrong when the commonest cause here is a cluster that
	// went away. Each message says what was observed and what it usually means,
	// and lets them draw the conclusion.
	case errors.Is(err, ports.ErrNameNotResolved):
		return CodeUnreachable, "The cluster's address could not be looked up — this machine may not be on a network that knows that name"

	case errors.Is(err, ports.ErrConnectionRefused):
		// Deliberately NOT a network suggestion. The packets arrived; routing
		// is fine. Sending somebody to check their connection here is sending
		// them to the one place the problem is not.
		return CodeUnreachable, "The cluster refused the connection — the API server may be stopped, or listening on a different port"

	case errors.Is(err, ports.ErrNoResponse):
		return CodeUnreachable, "The cluster did not respond — it may be offline, or reachable only from a network this machine is not on"

	case errors.Is(err, ports.ErrUnreachable):
		return CodeUnreachable, "The cluster could not be contacted"

	// BEFORE the generic cases: none of the three is a failure of anything.
	// Each says what PodSteer will not do and why, and each has a different
	// remedy — none of which is trying again.
	case errors.Is(err, ports.ErrSettingsReadOnly):
		return CodeSettingsReadOnly, "This PodSteer is not saving settings, so that change was not stored."

	case errors.Is(err, ports.ErrSettingsFromFuture):
		return CodeSettingsFromFuture, "Your settings file was written by a newer version of PodSteer. PodSteer will not save over it, because doing so could discard settings this version does not understand. Upgrade PodSteer, or move the file aside to start again from the defaults."

	case errors.Is(err, ports.ErrSettingsUnavailable):
		return CodeSettingsUnavailable, "That setting could not be saved to disk."

	case errors.Is(err, domain.ErrSettingsSourcePath),
		errors.Is(err, domain.ErrSettingsSourceKind),
		errors.Is(err, domain.ErrSettingsProxyMode),
		errors.Is(err, domain.ErrSettingsProxyURL):
		return CodeInvalidInput, err.Error()

	// Its own message rather than the verbatim one above, because this is the
	// one refusal in the settings that is a POLICY: no credential is written
	// to a file PodSteer owns, and the operator needs to be told where the
	// credential does belong rather than only that this was rejected.
	case errors.Is(err, domain.ErrSettingsProxyCredential):
		return CodeInvalidInput, "A proxy URL must not contain a username or password: PodSteer never writes a credential to its own settings file. Set HTTPS_PROXY in your environment instead."

	case errors.Is(err, ports.ErrKubeconfigUnavailable):
		return CodeKubeconfig, "Your kubeconfig could not be read"

	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return CodeCancelled, "The request was cancelled or timed out"

	case errors.Is(err, domain.ErrEmptyClusterID),
		errors.Is(err, domain.ErrInvalidNamespaceName),
		errors.Is(err, domain.ErrInvalidResourceKind),
		errors.Is(err, domain.ErrUnsupportedWorkloadKind),
		errors.Is(err, domain.ErrInvalidKey),
		errors.Is(err, domain.ErrInvalidImageReference),
		errors.Is(err, domain.ErrInvalidManifest),
		errors.Is(err, domain.ErrInvalidRevision),
		// A malformed access review or role target, refused before it
		// reaches the cluster: the API server would answer an empty
		// question with a flat "no", which reads as a denial rather than
		// as the question having been empty.
		errors.Is(err, domain.ErrInvalidAccessRequest),
		errors.Is(err, domain.ErrInvalidRoleTarget),
		// The cluster's own verdict on an applied object (schema validation,
		// an admission webhook) rather than PodSteer's local pre-flight
		// check, but the SAME treatment applies: err.Error() is shown
		// verbatim rather than paraphrased, because Validate exists
		// specifically to hand an operator the server's diagnosis.
		errors.Is(err, ports.ErrManifestRejected),
		// "All namespaces" is not a namespace to create a pod in, and the
		// sentence says so — see domain.ErrShellNamespaceRequired, which
		// exists because the empty string is NamespaceAll everywhere else.
		errors.Is(err, domain.ErrShellNamespaceRequired),
		// A pod offered for reuse that has exited, or that is not one of
		// PodSteer's. Verbatim, because the message says which of the two and
		// that is the whole of what the operator needs.
		errors.Is(err, ports.ErrClusterShellNotReusable),
		errors.Is(err, domain.ErrNotTLSSecret),
		errors.Is(err, domain.ErrInvalidCertificate),
		errors.Is(err, domain.ErrContainerNotAttachable),
		errors.Is(err, domain.ErrInvalidRemotePath),
		// A hostile archive entry is reported with its name, so the
		// operator can see what the container tried to plant — and
		// decide what to make of the image that sent it.
		errors.Is(err, domain.ErrUnsafeArchiveEntry),
		errors.Is(err, errInvalidURL),
		errors.Is(err, errNotFound),
		errors.Is(err, errEmptySuggestedName),
		errors.Is(err, errUnreadableTextFile),
		// Both notification refusals are the frontend asking for something
		// it should not have — an empty headline, or a body long enough to
		// have started listing objects. Invalid input rather than internal,
		// so the message reaches whoever is looking at it.
		errors.Is(err, errEmptyNotification),
		errors.Is(err, errNotificationTooLong),
		errors.Is(err, errNotificationUnavailable),
		errors.Is(err, errInvalidBulkAction),
		errors.Is(err, errHelmNamespaceRequired),
		errors.Is(err, domain.ErrEmptyResourceName),
		errors.Is(err, errNoLocalPath),
		errors.Is(err, errProbeNoContainer),
		errors.Is(err, ports.ErrInvalidPort):
		return CodeInvalidInput, err.Error()

	case errors.Is(err, domain.ErrClusterNotConnected):
		return CodeNoActiveCluster, "That cluster is no longer connected"

	default:
		return CodeInternal, "An unexpected error occurred"
	}
}
