package k8s

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	unstructuredv1 "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// Reading ONE revision of ONE release, because somebody clicked.
//
// THIS IS THE ONLY FILE IN PODSTEER THAT DECODES A HELM RELEASE, and it sits
// beside helm.go for the reason that one says: the storage format is a
// storage concern. The label names, the object-name shape, the two layers of
// base64, the optional gzip and the JSON document are all things Helm chose
// about how it writes a Secret, and none of them belongs anywhere inward of
// this package.
//
// THE ACT THIS PERFORMS IS RevealSecretKey'S, not GetManifest's. It reads a
// Secret's contents whole — the API offers nothing narrower — on an explicit
// click, one revision at a time, with one audit line in the application layer
// naming cluster, namespace, release and revision and never a value. Nothing
// on a refresh tick reaches this method and nothing may ever call it on
// render; that rule is on ports.HelmPort and it is the whole reason ADR 6
// could recommend building the page at all.
//
// FOUR GUARDS, AND EVERY ONE OF THEM IS LOAD-BEARING:
//
//  1. The Secret's TYPE must be helm.sh/release.v1 and its `owner`, `name`
//     and `version` LABELS must match what was asked for. This is a SHAPE
//     check rather than a boundary — whoever can create a Secret at the
//     derived name can set its type and labels too — and what it buys is
//     that an object which merely SITS at Helm's naming shape without being
//     a release is refused rather than decoded and rendered as one. What
//     makes a hostile document merely a document is guards 2 to 4, not this.
//  2. Decompression stops at helmPayloadLimit and REFUSES rather than
//     truncating. A truncated manifest rendered as though it were whole is
//     the worst outcome available here.
//  3. The document is unmarshalled into a NARROW struct, so the chart's
//     templates and files — the entire chart, byte for byte — are skipped
//     rather than held in memory and then not displayed.
//  4. Every Secret document inside the rendered manifest is MASKED before the
//     string leaves this file. See maskSecretDocuments.

// helmPayloadLimit is how far a release payload may decompress.
//
// THIRTY-TWO MEBIBYTES, and the number is in decision 6 rather than invented
// here: comfortably above the largest rendered manifest anyone has in
// practice, far below anything that troubles a desktop process. It has to be
// a number rather than a word because etcd's 1 MiB object limit bounds the
// COMPRESSED release only, and the bytes are gzip chosen by whoever installed
// the chart — gzip expands by three orders of magnitude when somebody wants
// it to.
//
// The reader is given the limit PLUS ONE BYTE so that reaching the limit
// exactly is a success and exceeding it is observable. Reading exactly the
// limit and stopping cannot tell a payload that just fits from one that was
// cut off, which is the difference between an answer and a lie.
const helmPayloadLimit = 32 << 20

// helmGzipMagic is the first two bytes of a gzip stream (RFC 1952 §2.3.1).
//
// Helm compresses what it stores and has since Helm 3, but its own storage
// driver still SNIFFS rather than assumes — `decodeRelease` in
// `pkg/storage/driver/util.go` checks these two bytes — and so does this,
// because a payload written by some other producer at the same name and type
// is exactly the case guard 1 above exists for and it should fail on the JSON
// rather than on a gzip header.
var helmGzipMagic = []byte{0x1f, 0x8b}

// helmReleaseDataKey is the key Helm puts the payload under inside the Secret.
const helmReleaseDataKey = "release"

// helmSecretName composes the object name Helm writes a revision at.
//
// DERIVED, WHICH IS WHY THE VERIFICATION EXISTS. `sh.helm.release.v1.<release>.v<n>`
// is Helm's own `makeKey`, and it is the one thing about this read that is a
// guess rather than a lookup: the listing carries the Secret's real name, but
// a caller naming a release and a revision is naming a thing rather than an
// object, and composing the name is how a revision picked out of the history
// is opened. Composing it is safe precisely because what comes back is then
// checked against what was asked for.
func helmSecretName(release string, revision int) string {
	return "sh.helm.release.v1." + release + ".v" + strconv.Itoa(revision)
}

// ReadHelmRelease reads and decodes ONE revision of ONE release.
//
// The typed client, exactly as RevealSecretKey uses it: corev1.Secret.Data is
// []byte with the KUBERNETES base64 layer already removed by client-go, so
// nothing here re-implements that and there is no second encoded copy of the
// payload in the process. What remains is HELM's own base64 layer, and then
// its gzip.
func (a *Adapter) ReadHelmRelease(
	ctx context.Context,
	id domain.ClusterID,
	namespace domain.NamespaceName,
	release string,
	revision int,
) (domain.HelmReleaseDetail, error) {
	// The op string names the release and the revision and NEVER a value —
	// the same rule every audit line and every wrapped error here follows.
	op := fmt.Sprintf("reading Helm release %q revision %d in %q of %q", release, revision, namespace, id)

	if release == "" || revision <= 0 {
		return domain.HelmReleaseDetail{}, fmt.Errorf("%s: %w", op, ports.ErrHelmPayloadUnreadable)
	}

	set, err := a.factory.clientsFor(id)
	if err != nil {
		return domain.HelmReleaseDetail{}, err
	}

	name := helmSecretName(release, revision)

	secret, err := set.typed.CoreV1().Secrets(namespace.String()).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		wrapped := classify(op, err)
		// A REAPED REVISION IS THE ORDINARY CASE, not a fault. Helm's own
		// default keeps ten revisions and deletes the rest, so a history
		// entry can outlive the Secret behind it. The sentinel travels
		// ALONGSIDE ports.ErrNotFound so the wire code stays not_found while
		// the sentence can say the useful thing.
		if errors.Is(wrapped, ports.ErrNotFound) {
			return domain.HelmReleaseDetail{}, fmt.Errorf("%s: %w: %w",
				op, wrapped, domain.ErrHelmRevisionNotFound)
		}
		return domain.HelmReleaseDetail{}, wrapped
	}

	// GUARD 1, AND IT RUNS BEFORE ANY DECODING AT ALL. An object that merely
	// sits at Helm's naming shape — a backup, a copy made by hand, a restore
	// under the wrong name — is refused rather than decoded and rendered as
	// the release it is not. See verifyHelmReleaseSecret for what this does
	// and, just as importantly, does not claim.
	if err := verifyHelmReleaseSecret(secret, release, revision); err != nil {
		return domain.HelmReleaseDetail{}, fmt.Errorf("%s: %w", op, err)
	}

	document, err := decodeHelmPayload(secret.Data[helmReleaseDataKey])
	if err != nil {
		return domain.HelmReleaseDetail{}, fmt.Errorf("%s: %w", op, err)
	}

	// GUARD 4. The manifest is masked HERE, before the string is put on a
	// domain value, so the material never reaches the application layer, the
	// bridge or the page — the same placement and the same reasoning as
	// GetManifest's own call to maskSecretData.
	manifest, masked := maskSecretDocuments(document.Manifest)

	return domain.HelmReleaseDetail{
		Namespace:   namespace,
		Name:        release,
		Revision:    revision,
		Status:      domain.HelmStatus(document.Info.Status),
		Chart:       helmChartIdentity(document),
		Description: document.Info.Description,
		Values:      helmValuesYAML(document.Config),
		Notes:       document.Info.Notes,
		Manifest:    manifest,

		MaskedDocuments: masked,
		SecretName:      name,
	}, nil
}

// verifyHelmReleaseSecret refuses an object that is not shaped as Helm's own
// release storage for the release and revision asked for.
//
// WHAT THIS IS AND IS NOT. It is a SHAPE check, not a boundary against an
// attacker, and the difference is worth being exact about because an earlier
// draft of this comment overclaimed. Anybody who can create a Secret at the
// derived name can equally set its type and its labels, so nothing here
// stops a determined forgery — what makes that acceptable is everything
// around it: the decode is bounded at helmPayloadLimit, unmarshalled into a
// narrow struct, and has every Secret in its manifest masked, so a hostile
// document is a document, not an exploit.
//
// What the check DOES buy is real and is about accidents rather than
// attacks: a Secret that happens to sit at Helm's naming shape without being
// a release — a backup, a copy somebody made by hand, an object restored
// under the wrong name — is refused rather than decoded and rendered as a
// release it is not.
//
// FOUR CHECKS. `owner=helm` is the same label the LISTING selects on, and it
// is here so the two acts cannot disagree about what a release is: without
// it, an object invisible to the list would still be readable through this
// method, which is a difference nobody could see and nobody meant. The TYPE
// says this is Helm's storage rather than an ordinary Secret. The `name`
// label says which release — checked rather than the object name, because
// the object name is what was composed to make the request and comparing a
// derived string to itself checks nothing. The `version` label says which
// revision, so a relabelled or restored Secret cannot answer for a revision
// it is not.
//
// A failure here is ErrHelmPayloadUnreadable and never ErrNotFound: the
// object exists, and reporting it as absent would send somebody looking for a
// release Secret that is sitting in front of them.
func verifyHelmReleaseSecret(secret *corev1.Secret, release string, revision int) error {
	if string(secret.Type) != helmReleaseSecretType {
		return ports.ErrHelmPayloadUnreadable
	}

	labels := secret.GetLabels()
	if labels[helmOwnerLabel] != helmOwnerValue {
		return ports.ErrHelmPayloadUnreadable
	}
	if labels[helmNameLabel] != release {
		return ports.ErrHelmPayloadUnreadable
	}
	if labels[helmVersionLabel] != strconv.Itoa(revision) {
		return ports.ErrHelmPayloadUnreadable
	}

	return nil
}

// helmReleaseDocument is the NARROW view of a Helm release document.
//
// THE FIELDS THAT ARE ABSENT ARE THE POINT. Helm's own release JSON carries
// `chart.templates` and `chart.files` — the entire chart, every template and
// every packaged file — plus `chart.values` (the chart's defaults) and
// `hooks`. None of them is displayed, so none is declared: encoding/json
// walks past a field no struct names rather than allocating it, which on the
// largest object PodSteer will ever decode is the difference between holding
// what the pane shows and holding a chart.
//
// Field names are Helm's own JSON tags, which are snake_case in `info` and
// camelCase in `chart.metadata` — that inconsistency is Helm's and is
// transcribed rather than tidied, because a tag that does not match is a
// field that silently arrives empty.
type helmReleaseDocument struct {
	Name      string `json:"name"`
	Version   int    `json:"version"`
	Namespace string `json:"namespace"`

	Info struct {
		Status      string `json:"status"`
		Notes       string `json:"notes"`
		Description string `json:"description"`
	} `json:"info"`

	Chart struct {
		Metadata struct {
			Name        string `json:"name"`
			Version     string `json:"version"`
			AppVersion  string `json:"appVersion"`
			Description string `json:"description"`
		} `json:"metadata"`
	} `json:"chart"`

	// Config is the values the release was installed WITH — Helm's own name
	// for it, and what `helm get values` prints. The chart's defaults live
	// under `chart.values` and are deliberately not read: merging them would
	// show somebody values they never set as though they had.
	Config map[string]any `json:"config"`

	Manifest string `json:"manifest"`
}

// decodeHelmPayload turns what a release Secret holds into a release document.
//
// THE LAYERS, IN ORDER, AND WHY EACH IS WHERE IT IS:
//
//   - The Kubernetes base64 layer is ALREADY GONE. client-go decodes
//     `data` into []byte, which is why the typed client is used here exactly
//     as RevealSecretKey uses it. Nothing in this function undoes it a second
//     time.
//   - HELM'S OWN base64 layer is next, and it is a separate layer that Helm
//     applies before handing the bytes to Kubernetes. This is the one this
//     function removes.
//   - GZIP IS SNIFFED, NOT ASSUMED. Helm 3 compresses, but its own storage
//     driver checks the magic bytes rather than trusting it, so this does
//     too — and the plain-JSON branch is what makes the gzip branch testable
//     against its own negative case.
//   - The LIMIT is applied to the DECOMPRESSED stream and is limit+1, so a
//     payload that reaches the ceiling exactly succeeds and one that exceeds
//     it is observable rather than silently short.
//
// Every failure is ErrHelmPayloadUnreadable except the size one, which is
// ErrHelmPayloadTooLarge, because those two need opposite sentences: one says
// the object is not what it claimed to be, the other says it is exactly what
// it claimed to be and is simply too big.
func decodeHelmPayload(data []byte) (helmReleaseDocument, error) {
	if len(data) == 0 {
		return helmReleaseDocument{}, ports.ErrHelmPayloadUnreadable
	}

	// Helm's own base64 layer. StdEncoding, matching Helm's
	// `base64.StdEncoding` in pkg/storage/driver/util.go.
	decoded, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		return helmReleaseDocument{}, ports.ErrHelmPayloadUnreadable
	}

	var reader io.Reader = bytes.NewReader(decoded)
	if bytes.HasPrefix(decoded, helmGzipMagic) {
		gz, err := gzip.NewReader(bytes.NewReader(decoded))
		if err != nil {
			return helmReleaseDocument{}, ports.ErrHelmPayloadUnreadable
		}
		defer func() { _ = gz.Close() }()
		reader = gz
	}

	// LIMIT PLUS ONE. Reading exactly the limit cannot distinguish a payload
	// that just fits from one that was cut off; reading one byte more can,
	// and refusing is the only honest thing to do with the second — a
	// truncated manifest rendered as whole is worse than no manifest at all.
	plain, err := io.ReadAll(io.LimitReader(reader, helmPayloadLimit+1))
	if err != nil {
		// A corrupt gzip stream surfaces here rather than at NewReader,
		// which only validates the header.
		return helmReleaseDocument{}, ports.ErrHelmPayloadUnreadable
	}
	if len(plain) > helmPayloadLimit {
		return helmReleaseDocument{}, ports.ErrHelmPayloadTooLarge
	}

	var document helmReleaseDocument
	if err := json.Unmarshal(plain, &document); err != nil {
		return helmReleaseDocument{}, ports.ErrHelmPayloadUnreadable
	}

	return document, nil
}

// helmChartIdentity lifts the chart's own metadata off the document.
func helmChartIdentity(document helmReleaseDocument) domain.HelmChartIdentity {
	return domain.HelmChartIdentity{
		Name:        document.Chart.Metadata.Name,
		Version:     document.Chart.Metadata.Version,
		AppVersion:  document.Chart.Metadata.AppVersion,
		Description: document.Chart.Metadata.Description,
	}
}

// helmValuesYAML renders the release's values as YAML.
//
// YAML because that is the form somebody wrote them in and the form
// `helm get values` prints, so what the pane shows can be pasted back into a
// `-f values.yaml` without translation. An empty or absent `config` becomes
// an EMPTY STRING rather than "{}" or "null": a release installed with no
// overrides has no values, and rendering a literal `null` invites somebody to
// think one was set.
func helmValuesYAML(config map[string]any) string {
	if len(config) == 0 {
		return ""
	}

	encoded, err := yaml.Marshal(config)
	if err != nil {
		// Unreachable for anything json.Unmarshal produced, and if it ever
		// happened, an empty values tab is a better answer than a partial
		// one — the tab says the values may hold credentials, and a
		// half-rendered map is not something to invite somebody to read.
		return ""
	}
	return string(encoded)
}

// maskSecretDocuments masks every Secret inside a rendered multi-document
// manifest, and returns how many it masked.
//
// A RENDERED MANIFEST IS SECRET MATERIAL, AND THE DECISION RECORD MISSED IT.
// The record guarded the values pane carefully and treated the manifest as an
// innocuous tab — but a chart that renders `kind: Secret` has put base64
// `data:` values into this exact string, and base64 is an encoding and not a
// cipher, which is the doctrine's own sentence. A great many charts render
// one: every chart with a generated admin password, every chart with a
// registry pull Secret, every chart that templates a TLS key.
//
// So each document is parsed, and one that IS a core/v1 Secret has its values
// replaced by maskSecretData — the same helper, the same `<hidden, N bytes>`
// form, the same placement in the adapter before anything is serialised
// outward.
//
// A DOCUMENT THAT FAILS TO PARSE IS PASSED THROUGH UNCHANGED, and so is one
// that parses into something that is not a v1 Secret. That is the same rule
// maskSecretData itself follows for a ConfigMap holding a password: guessing
// which fields of an arbitrary kind are sensitive would mask arbitrarily and
// still miss what matters. A rendered manifest legitimately contains text
// that is not YAML at all — a chart may emit a comment block, a partially
// rendered template, or a document that is empty — and rewriting text nothing
// understood would be a guess of exactly the kind this codebase refuses.
//
// AN UNMASKED DOCUMENT IS BYTE-IDENTICAL. Only a document that was actually
// masked is re-serialised, which means only that document loses its `# Source:`
// comment; every other line of the manifest — comments, blank lines, key
// order, the separators themselves — arrives exactly as Helm rendered it.
// Round-tripping the whole manifest through a YAML encoder to be tidy would
// rewrite a document somebody is reading in order to decide something.
func maskSecretDocuments(manifest string) (string, int) {
	if manifest == "" {
		return "", 0
	}

	var (
		out    []string
		body   []string
		masked int
	)

	// flush decides one document. It is a closure rather than a loop body
	// because a document ends in two places — at a separator, and at the end
	// of the manifest — and the two must not be allowed to drift.
	flush := func() {
		if len(body) == 0 {
			return
		}
		text := strings.Join(body, "\n")
		// COUNTED PER SECRET, NOT PER DOCUMENT. One `List` holding six
		// Secrets is six things hidden, and a count that said "1" would
		// under-report what the operator is not being shown.
		if replaced, count, ok := maskSecretDocument(text); ok {
			masked += count
			text = replaced
		}
		out = append(out, strings.Split(text, "\n")...)
		body = nil
	}

	for _, line := range strings.Split(manifest, "\n") {
		// A document separator is a line that is exactly `---`. Trailing
		// whitespace and a stray carriage return are tolerated because a
		// manifest is text somebody's chart produced; anything else on the
		// line — `--- # Source: x` — is left as part of the document, since
		// splitting on it would need a YAML parser to be safe and the
		// documents this misses are simply not masked rather than mangled.
		if strings.TrimRight(line, " \t\r") == "---" {
			flush()
			out = append(out, line)
			continue
		}
		body = append(body, line)
	}
	flush()

	return strings.Join(out, "\n"), masked
}

// helmListNestingLimit bounds how deep a List may nest before this refuses.
//
// FOUR, AND NESTING IS HANDLED RATHER THAN IGNORED — that is the choice, and
// the alternative is worth naming because it is the one that leaks. Ignoring
// nesting would mean passing a document through untouched while knowing this
// function had not looked inside all of it, which is exactly the outcome the
// masking exists to prevent; a Secret two Lists deep would arrive in the
// clear and `MaskedDocuments` would say nothing was hidden.
//
// So it recurses, and the depth is bounded because the manifest is text a
// chart produced and nothing here should let cluster-controlled input choose
// how deep a walk goes. Four is far past anything real: kubectl and Helm's
// own kube client both flatten a List, nobody writes one inside another, and
// a document that reaches this bound is not something to render on a guess.
// Reaching it REFUSES the document — it is withheld with a line saying so,
// rather than passed through — because "I did not finish looking" and
// "there was nothing to hide" must not produce the same output.
const helmListNestingLimit = 4

// withheldDocument replaces a document this function will not render.
//
// A LINE RATHER THAN A DELETION. An absent document reads as a chart that did
// not render one, which is a different and false claim; this says plainly
// that something was removed and why.
const withheldDocument = "# <a document was withheld by PodSteer: it holds Secret data this build could not safely render>"

// maskSecretDocument masks ONE document, reporting whether anything was
// masked and how many Secrets it touched.
//
// THREE SHAPES ARE RECOGNISED, and the second and third exist because the
// first was not enough:
//
//   - a core/v1 Secret, masked directly;
//   - a core/v1 `List` or `SecretList`, whose `items[]` are walked and each
//     v1 Secret in them masked — a chart that templates several Secrets from
//     one `range` loop emits exactly this, and both kubectl and Helm's own
//     kube client expand it, so it is an ordinary shape rather than an exotic
//     one. Matching only a bare Secret let one straight through with its
//     base64 intact while the count said nothing had been masked.
//   - anything else, which is passed through UNCHANGED. That includes a
//     document that does not parse at all: it is text, not a Secret, and
//     rewriting what nothing understood would be the guess this codebase
//     keeps refusing to make.
//
// ok reports whether the returned text should replace the original; masked is
// how many Secrets were hidden, so a List of six counts six rather than one.
func maskSecretDocument(document string) (string, int, bool) {
	if strings.TrimSpace(document) == "" {
		return "", 0, false
	}

	// sigs.k8s.io/yaml, the same package GetManifest serialises through, so
	// a document round-trips the way the YAML tab's own objects do rather
	// than through a second YAML implementation with its own opinions.
	var fields map[string]any
	if err := yaml.Unmarshal([]byte(document), &fields); err != nil || fields == nil {
		return "", 0, false
	}

	masked, refused := maskSecretsWithin(fields, 0)
	if refused {
		// AT LEAST ONE, ALWAYS. A withheld document may have had nothing
		// masked before the walk gave up, but the manifest on screen is
		// missing a document either way — and a pane reporting "nothing was
		// masked" beside a manifest with something removed from it is the
		// one thing the count exists to prevent.
		if masked == 0 {
			masked = 1
		}
		return withheldDocument, masked, true
	}
	if masked == 0 {
		// Nothing was a Secret, so the ORIGINAL text is returned rather than
		// a re-encoding of it: an untouched document must arrive exactly as
		// Helm rendered it, comments and key order included.
		return "", 0, false
	}

	encoded, err := yaml.Marshal(fields)
	if err != nil {
		// UNREACHABLE FOR ANYTHING THAT PARSED, and the branch still has to
		// decide something, because by now the object has been masked IN
		// PLACE — so reporting "nothing masked" would hand the caller the
		// ORIGINAL text with its values intact, which is the one outcome
		// this function must never produce.
		return withheldDocument, masked, true
	}

	return strings.TrimRight(string(encoded), "\n"), masked, true
}

// maskSecretsWithin masks every v1 Secret reachable in one parsed document,
// in place, and reports how many it masked.
//
// refused is true when the walk hit helmListNestingLimit with a List still
// unexamined — the caller then withholds the whole document rather than
// rendering a partially inspected one.
func maskSecretsWithin(fields map[string]any, depth int) (masked int, refused bool) {
	object := &unstructuredv1.Unstructured{Object: fields}
	apiVersion, kind := object.GetAPIVersion(), object.GetKind()

	if apiVersion == "v1" && kind == "Secret" {
		// The same helper the YAML tab uses, so the two can never mask
		// differently — a manifest showing a value the Secret pane hides
		// would be the doctrine leaking out through the door nobody watched.
		maskSecretData(object)
		return 1, false
	}

	// `List` is the generic container kubectl and Helm both emit; `SecretList`
	// is what the API server itself returns for a Secret collection, and a
	// chart that captured one into its templates renders it verbatim. Both
	// carry their entries under `items`.
	if apiVersion != "v1" || (kind != "List" && kind != "SecretList") {
		return 0, false
	}
	if depth >= helmListNestingLimit {
		return 0, true
	}

	items, found, err := unstructuredv1.NestedSlice(fields, "items")
	if err != nil {
		// REFUSED RATHER THAN PASSED THROUGH, and the asymmetry with the
		// line below is the point. This document SAYS it is a List, so a
		// failure to read its entries means the walk did not finish looking
		// — and "I could not look" must not produce the same output as
		// "there was nothing to hide". Unreachable in practice, because
		// sigs.k8s.io/yaml goes through JSON and so yields only types
		// NestedSlice accepts; it is written for the day that stops being
		// true rather than for today.
		return 0, true
	}
	if !found {
		// A List with no entries, which is genuinely nothing to hide.
		return 0, false
	}

	for i := range items {
		entry, isObject := items[i].(map[string]any)
		if !isObject {
			// A non-object entry is not a Kubernetes object and holds no
			// Secret data. Left exactly as it was, for the same reason a
			// ConfigMap beside a Secret is.
			continue
		}
		count, hitLimit := maskSecretsWithin(entry, depth+1)
		masked += count
		refused = refused || hitLimit
		items[i] = entry
	}

	// NestedSlice deep-copies, so the masked entries have to be written back
	// or the whole walk would be masking a throwaway.
	if err := unstructuredv1.SetNestedSlice(fields, items, "items"); err != nil {
		// The slice came out of this map a moment ago, so this cannot fail
		// for anything that parsed — and if it ever did, the masked copy
		// would be discarded and the original returned, so the document is
		// withheld instead.
		return masked, true
	}

	return masked, refused
}
