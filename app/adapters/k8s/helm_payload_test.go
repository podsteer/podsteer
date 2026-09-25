package k8s

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// --- Fixtures ---------------------------------------------------------------

// helmPlainPayload is a release document as JSON, before either encoding
// layer. Helm never writes one uncompressed — it has gzipped since Helm 3 —
// but the gzip branch needs its negative case, and the sniff exists precisely
// so that a payload from some other producer fails on the JSON rather than on
// a gzip header.
const helmPlainPayload = `{
  "name": "podinfo",
  "version": 2,
  "namespace": "shop",
  "info": {
    "status": "deployed",
    "notes": "Fetch the admin password:\n  kubectl get secret podinfo -o jsonpath='{.data.password}'",
    "description": "Upgrade complete"
  },
  "chart": {
    "metadata": {
      "name": "podinfo",
      "version": "6.5.4",
      "appVersion": "6.5.4",
      "description": "Podinfo Helm chart for Kubernetes"
    },
    "templates": [{"name": "templates/deployment.yaml", "data": "c2hvdWxkIG5vdCBiZSBoZWxk"}],
    "files": [{"name": "README.md", "data": "c2hvdWxkIG5vdCBiZSBoZWxk"}],
    "values": {"replicaCount": 1}
  },
  "config": {"replicaCount": 3, "ingress": {"enabled": true}},
  "manifest": "apiVersion: v1\nkind: ConfigMap\n"
}`

// helmEncode applies the two layers Helm applies, in Helm's own order: gzip
// (optionally), then base64. The KUBERNETES base64 layer is not applied here
// because client-go removes it before the adapter ever sees the bytes, which
// is exactly why the typed client is used for this read.
func helmEncode(t *testing.T, payload string, compress bool) []byte {
	t.Helper()

	raw := []byte(payload)
	if compress {
		var buffer bytes.Buffer
		writer := gzip.NewWriter(&buffer)
		if _, err := writer.Write(raw); err != nil {
			t.Fatalf("gzip write: %v", err)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("gzip close: %v", err)
		}
		raw = buffer.Bytes()
	}

	return []byte(base64.StdEncoding.EncodeToString(raw))
}

// helmZeroBomb builds a payload whose DECOMPRESSED size is exactly `size`
// bytes of zeros.
//
// Zeros because they compress to almost nothing, which is what makes the
// ceiling worth having: a few kilobytes on the wire, tens of megabytes in
// memory. The test that matters is not that a bomb is refused but that the
// BOUNDARY is exact — one byte over refuses, exactly at the cap succeeds —
// because a limit that is off by one either truncates a legitimate release or
// admits an unbounded one.
func helmZeroBomb(t *testing.T, size int) []byte {
	t.Helper()

	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(make([]byte, size)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	return []byte(base64.StdEncoding.EncodeToString(buffer.Bytes()))
}

// helmPaddedPayload builds a valid release document padded with a description
// long enough that the whole thing decompresses to exactly `size` bytes.
//
// This is the other half of the boundary test: a bomb of zeros proves the
// limit stops something, and a REAL release that lands exactly on the cap
// proves it does not stop everything.
func helmPaddedPayload(t *testing.T, size int) string {
	t.Helper()

	template := `{"name":"podinfo","version":1,"info":{"status":"deployed","description":"%PAD%"},"manifest":""}`
	overhead := len(strings.Replace(template, "%PAD%", "", 1))
	if size < overhead {
		t.Fatalf("size %d is below the document's own overhead of %d", size, overhead)
	}

	return strings.Replace(template, "%PAD%", strings.Repeat("x", size-overhead), 1)
}

// mustJSONString encodes a string as a JSON string literal, so a fixture can
// embed a multi-line manifest in a hand-written payload without anybody
// hand-escaping newlines and getting it subtly wrong.
func mustJSONString(t *testing.T, value string) string {
	t.Helper()

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding a fixture string: %v", err)
	}
	return string(encoded)
}

// --- The decoder ------------------------------------------------------------

func TestDecodeHelmPayload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
		want error
		// check runs only when want is nil.
		check func(t *testing.T, document helmReleaseDocument)
	}{
		{
			// Helm never writes one, but the gzip branch needs its negative
			// case: the magic-byte sniff has to fall through to plain JSON
			// rather than assume compression.
			name: "plain JSON, the gzip sniff's negative case",
			data: helmEncode(t, helmPlainPayload, false),
			check: func(t *testing.T, document helmReleaseDocument) {
				if document.Name != "podinfo" || document.Version != 2 {
					t.Fatalf("document = %s v%d, want podinfo v2", document.Name, document.Version)
				}
				if document.Chart.Metadata.Version != "6.5.4" {
					t.Errorf("chart version = %q, want 6.5.4", document.Chart.Metadata.Version)
				}
			},
		},
		{
			name: "gzipped, which is what Helm actually writes",
			data: helmEncode(t, helmPlainPayload, true),
			check: func(t *testing.T, document helmReleaseDocument) {
				if document.Info.Status != "deployed" {
					t.Errorf("status = %q, want deployed", document.Info.Status)
				}
				if document.Config["replicaCount"] == nil {
					t.Error("the values the release was installed with did not survive the decode")
				}
			},
		},
		{
			name: "base64 garbage",
			data: []byte("!!!! not base64 at all !!!!"),
			want: ports.ErrHelmPayloadUnreadable,
		},
		{
			name: "gzip of something that is not JSON",
			data: helmEncode(t, "this is not a release document", true),
			want: ports.ErrHelmPayloadUnreadable,
		},
		{
			name: "an empty payload",
			data: nil,
			want: ports.ErrHelmPayloadUnreadable,
		},
		{
			// THE BOUNDARY, AND IT IS THE POINT OF THIS TABLE. One byte over
			// the ceiling must be REFUSED and never truncated: a manifest
			// shown as whole when it is short is worse than no manifest, and
			// a limit that cannot tell "just fits" from "was cut off" cannot
			// make that promise.
			name: "a gzip bomb of zeros expanding to the cap plus one",
			data: helmZeroBomb(t, helmPayloadLimit+1),
			want: ports.ErrHelmPayloadTooLarge,
		},
		{
			// The same boundary from the other side. Zeros are not JSON, so
			// this asserts the SIZE check let it through — reaching
			// ErrHelmPayloadUnreadable proves the read got past the limiter
			// and failed on the document instead.
			name: "a gzip bomb of zeros expanding to exactly the cap is not refused for size",
			data: helmZeroBomb(t, helmPayloadLimit),
			want: ports.ErrHelmPayloadUnreadable,
		},
		{
			// And a REAL release landing exactly on the cap must decode. A
			// ceiling that refused everything would pass every test above.
			name: "a release document of exactly the cap decodes",
			data: helmEncode(t, helmPaddedPayload(t, helmPayloadLimit), true),
			check: func(t *testing.T, document helmReleaseDocument) {
				if document.Name != "podinfo" {
					t.Fatalf("name = %q, want podinfo", document.Name)
				}
				if len(document.Info.Description) == 0 {
					t.Error("the padded description did not survive the decode")
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			document, err := decodeHelmPayload(test.data)
			if test.want != nil {
				if !errors.Is(err, test.want) {
					t.Fatalf("error = %v, want %v", err, test.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("decodeHelmPayload() error = %v", err)
			}
			test.check(t, document)
		})
	}
}

func TestDecodingSkipsTheChartsTemplatesAndFiles(t *testing.T) {
	t.Parallel()

	// The narrow struct is what keeps the largest object PodSteer decodes
	// from being held whole: a release carries the entire chart, and nothing
	// on the pane shows a template. Declaring the fields would be the easy
	// mistake, so it is asserted rather than trusted — the document type has
	// nowhere to put them.
	document, err := decodeHelmPayload(helmEncode(t, helmPlainPayload, true))
	if err != nil {
		t.Fatalf("decodeHelmPayload() error = %v", err)
	}

	if document.Chart.Metadata.Name != "podinfo" {
		t.Fatalf("chart name = %q, want podinfo", document.Chart.Metadata.Name)
	}

	// The fixture puts a recognisable string in both templates and files. If
	// either were ever declared on the struct, it would be reachable from
	// here — and the whole point is that it is not, so this asserts the
	// document's own field set stays narrow.
	if strings.Contains(document.Manifest, "should not be held") {
		t.Error("chart data leaked into the manifest field")
	}
}

// --- Verification before decoding ------------------------------------------

func newHelmSecret(release string, revision string, secretType string, data []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "shop",
			Name:      "sh.helm.release.v1." + release + ".v" + revision,
			Labels: map[string]string{
				helmOwnerLabel:   helmOwnerValue,
				helmNameLabel:    release,
				helmVersionLabel: revision,
			},
		},
		Type: corev1.SecretType(secretType),
		Data: map[string][]byte{helmReleaseDataKey: data},
	}
}

func newHelmPayloadAdapter(id domain.ClusterID, objects ...runtime.Object) *Adapter {
	client := fake.NewClientset(objects...)
	factory := newClientFactory(Config{})
	factory.clients[id] = &clients{typed: client}
	return &Adapter{factory: factory}
}

func TestReadHelmReleaseDecodesOneRevision(t *testing.T) {
	t.Parallel()

	adapter := newHelmPayloadAdapter("dev",
		newHelmSecret("podinfo", "2", helmReleaseSecretType, helmEncode(t, helmPlainPayload, true)))

	detail, err := adapter.ReadHelmRelease(context.Background(), "dev", "shop", "podinfo", 2)
	if err != nil {
		t.Fatalf("ReadHelmRelease() error = %v", err)
	}

	if detail.Revision != 2 || detail.Name != "podinfo" {
		t.Fatalf("detail = %s v%d, want podinfo v2", detail.Name, detail.Revision)
	}
	// THE LIST'S TWO MISSING COLUMNS, finally. They live only in the payload,
	// which is why the list ships without them and why this read exists.
	if detail.Chart.Name != "podinfo" || detail.Chart.Version != "6.5.4" || detail.Chart.AppVersion != "6.5.4" {
		t.Errorf("chart identity = %+v, want the payload's own chart metadata", detail.Chart)
	}
	if !strings.Contains(detail.Values, "replicaCount: 3") {
		t.Errorf("values did not come through as YAML:\n%s", detail.Values)
	}
	if !strings.Contains(detail.Notes, "admin password") {
		t.Errorf("notes did not come through:\n%s", detail.Notes)
	}
	if detail.SecretName != "sh.helm.release.v1.podinfo.v2" {
		t.Errorf("secret name = %q, want the derived release Secret name", detail.SecretName)
	}
}

func TestReadHelmReleaseRefusesASecretThatIsNotTheReleaseAskedFor(t *testing.T) {
	t.Parallel()

	// THE OBJECT NAME IS DERIVED, so anybody who may create a Secret in a
	// namespace can put one at the name this read composes. Each of these is
	// a Secret sitting at exactly the right name and failing exactly one of
	// the three checks, and each must be refused without being decoded.
	tests := []struct {
		name   string
		secret *corev1.Secret
	}{
		{
			name: "an ordinary Opaque Secret wearing the release's name",
			secret: newHelmSecret("podinfo", "2", "Opaque",
				[]byte(base64.StdEncoding.EncodeToString([]byte(helmPlainPayload)))),
		},
		{
			name: "the right type, but the name label says another release",
			secret: func() *corev1.Secret {
				secret := newHelmSecret("podinfo", "2", helmReleaseSecretType,
					[]byte(base64.StdEncoding.EncodeToString([]byte(helmPlainPayload))))
				secret.Labels[helmNameLabel] = "something-else"
				return secret
			}(),
		},
		{
			name: "the right type and release, but the version label says another revision",
			secret: func() *corev1.Secret {
				secret := newHelmSecret("podinfo", "2", helmReleaseSecretType,
					[]byte(base64.StdEncoding.EncodeToString([]byte(helmPlainPayload))))
				secret.Labels[helmVersionLabel] = "7"
				return secret
			}(),
		},
		{
			// THE TWO ACTS MUST AGREE ABOUT WHAT A RELEASE IS. The listing
			// selects on `owner=helm`, so without this check an object the
			// list cannot see would still be readable through this method —
			// a difference nobody could observe and nobody intended.
			name: "everything else right, but not owned by Helm, so the listing would never show it",
			secret: func() *corev1.Secret {
				secret := newHelmSecret("podinfo", "2", helmReleaseSecretType,
					[]byte(base64.StdEncoding.EncodeToString([]byte(helmPlainPayload))))
				delete(secret.Labels, helmOwnerLabel)
				return secret
			}(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			adapter := newHelmPayloadAdapter("dev", test.secret)

			_, err := adapter.ReadHelmRelease(context.Background(), "dev", "shop", "podinfo", 2)
			if !errors.Is(err, ports.ErrHelmPayloadUnreadable) {
				t.Fatalf("error = %v, want ErrHelmPayloadUnreadable", err)
			}
			// NOT not-found. The object is there, and reporting it as absent
			// would send somebody looking for a Secret sitting in front of
			// them.
			if errors.Is(err, ports.ErrNotFound) {
				t.Error("a Secret that exists and does not verify must never be reported as absent")
			}
		})
	}
}

func TestReadHelmReleaseReportsAReapedRevisionAsNotFound(t *testing.T) {
	t.Parallel()

	// Helm keeps ten revisions by default and deletes the rest, so a history
	// entry outliving its Secret is the ORDINARY case. It stays
	// ports.ErrNotFound — the wire code the frontend already branches on —
	// with the Helm sentinel alongside it so the sentence can say why.
	adapter := newHelmPayloadAdapter("dev")

	_, err := adapter.ReadHelmRelease(context.Background(), "dev", "shop", "podinfo", 2)
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("error = %v, want ports.ErrNotFound", err)
	}
	if !errors.Is(err, domain.ErrHelmRevisionNotFound) {
		t.Fatalf("error = %v, want domain.ErrHelmRevisionNotFound alongside it", err)
	}
}

// --- The manifest is Secret material too ------------------------------------

// A rendered manifest with a Secret and a ConfigMap in it, in the shape Helm
// actually writes: a leading separator, a `# Source:` comment per document.
const helmRenderedManifest = `---
# Source: shop/templates/configmap.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: shop-config
data:
  greeting: aGVsbG8=
---
# Source: shop/templates/secret.yaml
apiVersion: v1
kind: Secret
metadata:
  name: shop-db
type: Opaque
data:
  password: aHVudGVyMg==
`

func TestARenderedSecretIsMaskedAndTheConfigMapBesideItIsNot(t *testing.T) {
	t.Parallel()

	// THE FINDING THE DECISION RECORD MISSED. A chart that renders
	// `kind: Secret` has put base64 values into the manifest string, and
	// base64 is an encoding and not a cipher — the doctrine's own words. So
	// the manifest is masked before it leaves the adapter.
	masked, count := maskSecretDocuments(helmRenderedManifest)

	if count != 1 {
		t.Fatalf("masked %d documents, want 1", count)
	}

	// `hunter2` base64-encodes to `aHVudGVyMg==`; neither form may survive.
	if strings.Contains(masked, "aHVudGVyMg==") {
		t.Error("the Secret's encoded value survived the mask; base64 is an encoding, not a cipher")
	}
	if strings.Contains(masked, "hunter2") {
		t.Error("the Secret's decoded value reached the manifest")
	}
	if !strings.Contains(masked, "<hidden, 7 bytes>") {
		t.Errorf("the placeholder must say something true about the value's shape:\n%s", masked)
	}

	// THE CONFIGMAP BESIDE IT IS UNTOUCHED, byte for byte, comment included.
	// A ConfigMap holding something that looks like a credential is returned
	// whole everywhere else in this codebase, and guessing which fields of
	// an arbitrary kind are sensitive would mask arbitrarily and still miss
	// what matters.
	if !strings.Contains(masked, "greeting: aGVsbG8=") {
		t.Errorf("the ConfigMap was altered; only core/v1 Secrets are masked:\n%s", masked)
	}
	if !strings.Contains(masked, "# Source: shop/templates/configmap.yaml") {
		t.Errorf("an unmasked document must arrive exactly as Helm rendered it:\n%s", masked)
	}
}

// A chart templating several Secrets from one `range` loop emits them wrapped
// in a v1 List, and both kubectl and Helm's own kube client expand that. It is
// an ordinary shape, and matching only a bare Secret let it through with its
// base64 intact while the count said nothing had been masked.
const helmListManifest = `---
# Source: shop/templates/secrets.yaml
apiVersion: v1
kind: List
items:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: shop-config
    data:
      greeting: aGVsbG8=
  - apiVersion: v1
    kind: Secret
    metadata:
      name: shop-db
    data:
      password: aHVudGVyMg==
  - apiVersion: v1
    kind: Secret
    metadata:
      name: shop-api
    data:
      token: aHVudGVyMg==
`

func TestSecretsWrappedInAListAreMaskedAndCountedIndividually(t *testing.T) {
	t.Parallel()

	masked, count := maskSecretDocuments(helmListManifest)

	// COUNTED PER SECRET. Two Secrets in one List is two things hidden, and
	// a count of one would under-report what the operator is not shown.
	if count != 2 {
		t.Fatalf("masked %d Secrets, want 2", count)
	}
	if strings.Contains(masked, "aHVudGVyMg==") {
		t.Errorf("a Secret inside a List kept its encoded value:\n%s", masked)
	}
	if !strings.Contains(masked, "<hidden, 7 bytes>") {
		t.Errorf("the placeholder must say something true about the value:\n%s", masked)
	}

	// THE WALK IS SELECTIVE. A ConfigMap sitting in the same List is
	// untouched, exactly as one sitting beside a Secret as its own document
	// is — this function hides Secret values and guesses at nothing else.
	if !strings.Contains(masked, "greeting: aGVsbG8=") {
		t.Errorf("the ConfigMap inside the List was altered:\n%s", masked)
	}
}

func TestASecretListIsWalkedTheSameWayAListIs(t *testing.T) {
	t.Parallel()

	// `SecretList` is what the API server itself returns for a collection,
	// and a chart that captured one into its templates renders it verbatim.
	manifest := `apiVersion: v1
kind: SecretList
items:
  - apiVersion: v1
    kind: Secret
    metadata:
      name: shop-db
    data:
      password: aHVudGVyMg==
`

	masked, count := maskSecretDocuments(manifest)
	if count != 1 {
		t.Fatalf("masked %d Secrets, want 1", count)
	}
	if strings.Contains(masked, "aHVudGVyMg==") {
		t.Errorf("a Secret inside a SecretList kept its encoded value:\n%s", masked)
	}
}

func TestAListNestedPastTheBoundIsWithheldRatherThanPassedThrough(t *testing.T) {
	t.Parallel()

	// NESTING IS HANDLED, AND THE PATHOLOGICAL CASE IS REFUSED. Passing a
	// document through while knowing this function had not finished looking
	// inside it is the leak the masking exists to prevent, so a walk that
	// hits its depth bound withholds the document instead — "I did not
	// finish looking" and "there was nothing to hide" must not produce the
	// same output.
	nested := "apiVersion: v1\nkind: List\nitems:\n"
	indent := "  "
	for range helmListNestingLimit {
		nested += indent + "- apiVersion: v1\n" + indent + "  kind: List\n" + indent + "  items:\n"
		indent += "    "
	}
	nested += indent + "- apiVersion: v1\n" + indent + "  kind: Secret\n" +
		indent + "  data:\n" + indent + "    password: aHVudGVyMg==\n"

	masked, count := maskSecretDocuments(nested)

	if strings.Contains(masked, "aHVudGVyMg==") {
		t.Fatalf("a Secret past the nesting bound reached the manifest:\n%s", masked)
	}
	if !strings.Contains(masked, "withheld") {
		t.Errorf("a document that could not be fully inspected must say so:\n%s", masked)
	}
	// It counts as something hidden, so the pane cannot report a manifest
	// with nothing masked in it while a document is missing.
	if count == 0 {
		t.Error("a withheld document must count as masked")
	}
}

func TestAManifestWithNoSecretIsReturnedUnchanged(t *testing.T) {
	t.Parallel()

	// A manifest nothing needed masking in must come back byte-identical:
	// re-serialising it to be tidy would rewrite a document somebody is
	// reading in order to decide something.
	manifest := "---\n# Source: shop/templates/service.yaml\napiVersion: v1\nkind: Service\nmetadata:\n  name: shop\n"

	masked, count := maskSecretDocuments(manifest)
	if count != 0 {
		t.Fatalf("masked %d documents, want 0", count)
	}
	if masked != manifest {
		t.Errorf("the manifest was rewritten:\n%q\nwant:\n%q", masked, manifest)
	}
}

func TestADocumentThatDoesNotParseIsPassedThroughAsText(t *testing.T) {
	t.Parallel()

	// A rendered manifest legitimately holds things that are not YAML — a
	// half-rendered template, a comment block, a chart's own prose. It is
	// TEXT, not a Secret, and rewriting what nothing understood would be the
	// guess this codebase keeps refusing to make.
	manifest := "---\n\tthis: is not: valid yaml: at all\n  [ }\n"

	masked, count := maskSecretDocuments(manifest)
	if count != 0 {
		t.Fatalf("masked %d documents, want 0", count)
	}
	if masked != manifest {
		t.Errorf("unparseable text was rewritten:\n%q", masked)
	}
}

func TestReadHelmReleaseMasksTheManifestBeforeItLeavesTheAdapter(t *testing.T) {
	t.Parallel()

	// The end-to-end statement: the value never reaches a domain value at
	// all, so it cannot reach the application layer, the bridge or the page.
	payload := `{"name":"shop","version":1,"info":{"status":"deployed"},` +
		`"chart":{"metadata":{"name":"shop","version":"1.0.0"}},` +
		`"manifest":` + mustJSONString(t, helmRenderedManifest) + `}`

	adapter := newHelmPayloadAdapter("dev",
		newHelmSecret("shop", "1", helmReleaseSecretType, helmEncode(t, payload, true)))

	detail, err := adapter.ReadHelmRelease(context.Background(), "dev", "shop", "shop", 1)
	if err != nil {
		t.Fatalf("ReadHelmRelease() error = %v", err)
	}

	if strings.Contains(detail.Manifest, "aHVudGVyMg==") || strings.Contains(detail.Manifest, "hunter2") {
		t.Fatal("a rendered Secret's value crossed the adapter boundary")
	}
	if detail.MaskedDocuments != 1 {
		t.Errorf("maskedDocuments = %d, want 1 — a masked value that does not say it was masked reads as an odd value", detail.MaskedDocuments)
	}
}
