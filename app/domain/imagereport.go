package domain

import (
	"fmt"
	"sort"
	"strings"
)

// What is knowable about a container image WITHOUT pulling it, and where each
// part of that is knowable from, is the whole design of this file.
//
// The layers, their sizes and the commands that created them, the entrypoint,
// the exposed ports and the labels all live in the image's own manifest and
// config blob, which live in a registry. Reading those means opening a
// connection to a host that is not an API server — a new outbound destination,
// for most private images an authenticated one, and the credential is a pull
// Secret sitting in the cluster that the Secrets doctrine says is read on
// explicit request and never on render. That is a decision with a product
// commitment on the other side of it (SECURITY.md: the clusters your
// kubeconfig names, and GitHub for the update check, and nothing else), so it
// is not made here.
//
// What IS knowable is what Kubernetes already reports, and it is more than it
// first appears: the resolved reference and its digest from the kubelet, the
// image's total size on disk from the node that pulled it, every other name
// that node knows the same image by, and whether the pull needed credentials.
// This file turns those into one report and — just as importantly — states in
// the report itself what it did not look at and why, so empty space is never
// read as "this image has no layers".
//
// That last part is the Bounded-not-Unreadable rule PodGraph already makes:
// Unreadable means a read was refused and a permission would fix it; bounded
// means no read was attempted, deliberately, and no permission changes that.

// ImageReference is a container image reference split into its parts.
//
// A reference is not a URL and cannot be parsed as one — "nginx",
// "library/nginx", "ghcr.io/team/app:v1@sha256:…" and "localhost:5000/app" are
// all valid and only the last two carry anything that looks like a host. The
// rules below are the same ones ValidImageReference already applies, which is
// why the two live in the same package and why this calls it rather than
// re-deriving what a valid reference is.
type ImageReference struct {
	// Registry is the host, defaulted to "docker.io" when the reference names
	// none — which is what a runtime does, so reporting the field empty would
	// misdescribe where the image actually came from.
	Registry string
	// Repository is the path, INCLUDING the "library/" Docker Hub inserts for
	// a single-segment name, for the same reason: it is what the runtime
	// asked for.
	Repository string
	// Tag is the tag, or "" when the reference is by digest alone. A
	// reference with neither is tagged "latest" by every runtime, and that
	// default is applied here so the report says what will actually be
	// pulled.
	Tag string
	// Digest is the content digest, or "" when the reference does not pin one.
	Digest string
}

// String rebuilds the reference. Not the input verbatim — a defaulted
// registry, repository or tag is included — so it is only ever shown beside
// the original rather than instead of it.
func (r ImageReference) String() string {
	out := r.Registry + "/" + r.Repository
	if r.Tag != "" {
		out += ":" + r.Tag
	}
	if r.Digest != "" {
		out += "@" + r.Digest
	}
	return out
}

// Repo is registry and repository without any tag or digest — the identity two
// references have to share before their tags are worth comparing.
func (r ImageReference) Repo() string { return r.Registry + "/" + r.Repository }

const (
	// defaultRegistry is where a reference with no host resolves.
	defaultRegistry = "docker.io"
	// defaultNamespace is what Docker Hub prepends to a single-segment name.
	defaultNamespace = "library"
	// defaultTag is what a reference with neither tag nor digest means.
	defaultTag = "latest"
)

// ParseImageReference splits ref into its parts, reporting false for anything
// ValidImageReference refuses.
//
// A MALFORMED DIGEST IS A REFUSAL, not a reference with an empty digest. The
// commonest shape of that is a truncated one pasted out of a log, and treating
// it as "no digest" would silently turn a pinned reference into a tagged one
// in whatever this answer is compared against next.
func ParseImageReference(ref string) (ImageReference, bool) {
	ref = strings.TrimSpace(ref)
	// Some runtimes still prefix the container status's imageID this way. It
	// is not part of the reference grammar, so it is removed before anything
	// tries to validate what is left.
	ref = strings.TrimPrefix(ref, "docker-pullable://")
	ref = strings.TrimPrefix(ref, "docker://")

	if !ValidImageReference(ref) {
		return ImageReference{}, false
	}

	var parsed ImageReference
	rest := ref

	if at := strings.LastIndex(rest, "@"); at >= 0 {
		parsed.Digest = rest[at+1:]
		rest = rest[:at]
	}
	if colon := strings.LastIndex(rest, ":"); colon >= 0 && colon > strings.LastIndex(rest, "/") {
		parsed.Tag = rest[colon+1:]
		rest = rest[:colon]
	}

	segments := strings.Split(rest, "/")
	// The same heuristic ValidImageReference uses to tell a registry host from
	// a path component, and deliberately the same one: two answers to "is this
	// first segment a host" would put an image under a registry it is not in.
	if len(segments) > 1 && (strings.ContainsAny(segments[0], ".:") || segments[0] == "localhost") {
		parsed.Registry = segments[0]
		segments = segments[1:]
	} else {
		parsed.Registry = defaultRegistry
	}

	if parsed.Registry == defaultRegistry && len(segments) == 1 {
		segments = append([]string{defaultNamespace}, segments...)
	}
	parsed.Repository = strings.Join(segments, "/")

	if parsed.Tag == "" && parsed.Digest == "" {
		parsed.Tag = defaultTag
	}

	return parsed, true
}

// NodeImage is one entry of a node's status.images: an image the kubelet has
// on disk, every name it knows it by, and how much room it takes.
type NodeImage struct {
	// Names are the references the runtime recorded for this image — usually
	// a digest form and one or more tagged forms. A node that has garbage-
	// collected an image drops the whole entry rather than emptying it.
	Names []string
	// SizeBytes is the image's size on disk AS THE NODE'S RUNTIME REPORTS IT.
	// Not the sum of the compressed layers a registry would serve, and not
	// comparable with what a registry would report, which is why anything
	// showing it says whose number it is.
	SizeBytes int64
}

// ImageSizeStatus separates the three reasons a size can be missing, for the
// reason MetricsStatus and ClusterReadStatus separate theirs: they call for
// different next steps, and one dash for all three tells nobody anything.
type ImageSizeStatus string

const (
	// ImageSizeMeasured means the node reported a size for this image.
	ImageSizeMeasured ImageSizeStatus = "measured"
	// ImageSizeNotReported means the node was read and does not list this
	// image. Ordinary: the kubelet garbage-collects images, and a pod that has
	// not started yet has not pulled one.
	ImageSizeNotReported ImageSizeStatus = "not_reported"
	// ImageSizeUnreadable means the node could not be read — the pod is
	// unscheduled, or the account may not get nodes. A refusal is not an
	// absence, so this never renders as zero.
	ImageSizeUnreadable ImageSizeStatus = "unreadable"
)

// ImageFacts is everything the adapter gathered, unshaped. Kubernetes' own
// words: nothing here has been compared, defaulted or judged.
type ImageFacts struct {
	// Container is the container's name.
	Container string
	// DeclaredRef is spec.containers[].image — what the manifest asks for.
	DeclaredRef string
	// ResolvedRef is status.containerStatuses[].image — what the kubelet says
	// it is running.
	ResolvedRef string
	// ImageID is status.containerStatuses[].imageID — the pulled content's
	// identity, which is the only digest Kubernetes reports for a container.
	ImageID string
	// PullPolicy is spec.containers[].imagePullPolicy.
	PullPolicy string
	// PullSecrets are the NAMES in spec.imagePullSecrets. Names only: the
	// Secrets they refer to are not read here, by this call or any other on
	// this path.
	PullSecrets []string
	// NodeName is spec.nodeName, empty on an unscheduled pod.
	NodeName string
	// NodeImages is that node's status.images, or nil when the node could not
	// be read.
	NodeImages []NodeImage
	// NodeUnreadable is why the node could not be read, or "" when it was.
	NodeUnreadable string
}

// ImageReport is what a container's image pane shows.
type ImageReport struct {
	Container string
	// Declared and Resolved are the two references verbatim, and Drift
	// reports that they differ — a comparison, so it is made here rather than
	// in the pane.
	Declared string
	Resolved string
	Drift    bool
	// Reference is the resolved reference parsed, with the registry,
	// repository and tag a runtime would default. Zero when neither reference
	// could be parsed, which ReferenceReadable reports.
	Reference         ImageReference
	ReferenceReadable bool
	// Digest is the content digest Kubernetes reported, preferring the
	// container status's imageID over anything a reference pinned, because
	// that is the identity of what is actually running.
	Digest string
	// DigestNote is set when the node and the container status disagree about
	// this image's digest. Not an error: it is what a multi-platform image
	// looks like, where one digest names the index and the other the manifest
	// for the node's own architecture.
	DigestNote string
	// SizeBytes and SizeStatus are the image's size on the node that pulled
	// it. Read SizeStatus first: SizeBytes is zero for every status but
	// measured, and a zero-byte image does not exist.
	SizeBytes  int64
	SizeStatus ImageSizeStatus
	// SizeSource names whose number SizeBytes is, for the pane to print
	// beside it.
	SizeSource string
	// OtherNames are the other references the node knows the same image by,
	// sorted, with the one already shown above removed. Frequently the most
	// useful thing here: a moved tag shows up as one image carrying two.
	OtherNames []string
	// PullPolicy and PullSecrets quote the spec.
	PullPolicy  string
	PullSecrets []string
	// Credentialed reports that the pull used a Secret. The pane says so and
	// stops there: the Secret is not read, here or anywhere on this path.
	Credentialed bool
	// Bounded is the one line saying what was NOT looked at, and why. Always
	// set. See ImageDetailBounded.
	Bounded string
}

// ImageDetailBounded is the line every image report carries.
//
// It is a Bounded line, not an Unreadable one: no read was attempted, and no
// permission changes that. Worded as a fact about PodSteer rather than about
// the image, because the operator has done nothing wrong and there is nothing
// for them to fix — the layers exist and are perfectly readable by anything
// that talks to a registry.
const ImageDetailBounded = "Layers, their creation commands, the entrypoint, the exposed ports and the labels are recorded in the image's manifest and config blob, which live in a registry. PodSteer does not read either: the only things it contacts are the API servers your kubeconfig names. Everything above is what Kubernetes itself reports about this image."

// ImageCredentialNote is what a report says when the pull needed credentials.
const ImageCredentialNote = "This image is pulled with credentials the cluster holds in a Secret. PodSteer does not read that Secret to describe an image — a Secret is read on an explicit, per-key request and never as a side effect of opening a pane."

// NewImageReport shapes gathered facts into the report a pane renders.
//
// Every comparison in this feature is here — declared against resolved, the
// node's digest against the kubelet's, which of several names to show — for
// the reason the pod pane's rules are in the domain: they are judgements two
// people would implement differently, and a test is where that argument
// belongs.
func NewImageReport(facts ImageFacts) ImageReport {
	report := ImageReport{
		Container:   facts.Container,
		Declared:    facts.DeclaredRef,
		Resolved:    facts.ResolvedRef,
		PullPolicy:  facts.PullPolicy,
		PullSecrets: facts.PullSecrets,
		Bounded:     ImageDetailBounded,
	}

	report.Drift = facts.DeclaredRef != "" &&
		facts.ResolvedRef != "" &&
		facts.DeclaredRef != facts.ResolvedRef

	report.Credentialed = len(facts.PullSecrets) > 0

	// The RESOLVED reference is the subject, falling back to the declared one
	// on a pod that has not started. What the kubelet says it is running is a
	// fact; what the manifest asks for is an intention, and on a moved tag the
	// two name different content.
	subject := firstNonEmptyRef(facts.ResolvedRef, facts.DeclaredRef)
	if parsed, ok := ParseImageReference(subject); ok {
		report.Reference = parsed
		report.ReferenceReadable = true
		report.Digest = parsed.Digest
	}

	// imageID wins over a digest the reference happened to pin: the reference
	// is what was asked for and imageID is what arrived, and on a mirrored or
	// re-tagged image those differ.
	if id, ok := ParseImageReference(facts.ImageID); ok && id.Digest != "" {
		report.Digest = id.Digest
	}

	applyNodeImage(&report, facts)

	return report
}

// applyNodeImage finds this image among the node's, and reports the size, the
// other names and any disagreement about the digest.
func applyNodeImage(report *ImageReport, facts ImageFacts) {
	report.SizeStatus = ImageSizeUnreadable
	switch {
	case facts.NodeUnreadable != "":
		report.SizeSource = facts.NodeUnreadable
		return
	case facts.NodeName == "":
		report.SizeSource = "this pod has not been scheduled onto a node, so nothing has pulled the image yet"
		return
	}

	match, ok := matchNodeImage(facts.NodeImages, report.Reference, report.Digest)
	if !ok {
		report.SizeStatus = ImageSizeNotReported
		report.SizeSource = fmt.Sprintf("node %s does not list this image; the kubelet garbage-collects images it is not using", facts.NodeName)
		return
	}

	report.SizeStatus = ImageSizeMeasured
	report.SizeBytes = match.SizeBytes
	report.SizeSource = fmt.Sprintf("on disk on node %s, as its container runtime reports it", facts.NodeName)
	report.OtherNames = otherNames(match.Names, report)

	if note := digestNote(match.Names, report.Digest); note != "" {
		report.DigestNote = note
	}
}

// matchNodeImage finds the node entry for one image.
//
// DIGEST FIRST, THEN THE REPOSITORY AND TAG, and the fallback is the whole
// point rather than a convenience. A multi-platform image is an INDEX in the
// registry, and a node routinely records one digest for it while the container
// status records another — the index's and the platform manifest's. Matching
// on the digest alone would report "the node does not list this image" for an
// image the node is plainly running, which reads as a bug in PodSteer rather
// than as the ordinary shape of a multi-arch pull.
func matchNodeImage(images []NodeImage, reference ImageReference, digest string) (NodeImage, bool) {
	if digest != "" {
		for _, image := range images {
			for _, name := range image.Names {
				if parsed, ok := ParseImageReference(name); ok && parsed.Digest == digest {
					return image, true
				}
			}
		}
	}

	if reference.Repository == "" {
		return NodeImage{}, false
	}

	for _, image := range images {
		for _, name := range image.Names {
			parsed, ok := ParseImageReference(name)
			if !ok || parsed.Repo() != reference.Repo() {
				continue
			}
			// A tagged reference has to match its tag; a digest-only one
			// matches the repository, since the digest arm above already
			// failed and the repository is all that is left to go on.
			if reference.Tag == "" || parsed.Tag == reference.Tag {
				return image, true
			}
		}
	}

	return NodeImage{}, false
}

// digestNote reports a node that records a different digest for this image
// than the container status did.
func digestNote(names []string, digest string) string {
	if digest == "" {
		return ""
	}

	for _, name := range names {
		parsed, ok := ParseImageReference(name)
		if !ok || parsed.Digest == "" {
			continue
		}
		if parsed.Digest == digest {
			return ""
		}
		return fmt.Sprintf(
			"The node records this image as %s while the kubelet recorded %s for this container. That is what a multi-platform image looks like: one of these names the index and the other the manifest built for this node's architecture.",
			parsed.Digest, digest)
	}

	return ""
}

// otherNames is every name the node knows this image by except the one
// already on screen, sorted so the list is stable between refreshes.
func otherNames(names []string, report *ImageReport) []string {
	shown := map[string]bool{
		report.Resolved: true,
		report.Declared: true,
	}
	if report.ReferenceReadable {
		shown[report.Reference.String()] = true
	}

	out := make([]string, 0, len(names))
	for _, name := range names {
		if name == "" || shown[name] {
			continue
		}
		shown[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func firstNonEmptyRef(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
