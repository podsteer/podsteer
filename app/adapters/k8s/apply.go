package k8s

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/podsteer/podsteer/app/domain"
)

// UpdateResource applies manifest to the cluster through the DYNAMIC client,
// so any kind the cluster serves — built-in or custom — can be applied, not
// only a fixed set of typed kinds this method used to switch over. The kind
// is resolved to its REST resource and scope via the cluster's RESTMapper
// (restMappingFor, below), which is discovery-backed and therefore already
// knows about every CRD the cluster has installed.
//
// The write is optimistic-locked by the manifest's OWN resourceVersion, not
// by a fresh read taken here: present, it is sent as a PUT and the API
// server enforces the lock, reporting a stale one as ports.ErrConflict via
// classify; absent, the object is created, and an AlreadyExists on that
// create falls back to fetching the live resourceVersion and replacing the
// object with it. That fallback is full REPLACE semantics — the pasted
// manifest becomes the object's entire spec, not a merge — which is what an
// operator pasting a whole manifest over an existing object means by Apply,
// the same way `kubectl apply` differs from `kubectl patch`.
//
// dryRun sends DryRun=All on the create/update request, asking the API
// server to run every admission check (schema validation, webhooks) without
// persisting anything. See ports.ManagementPort.UpdateResource for the full
// contract.
func (a *Adapter) UpdateResource(ctx context.Context, id domain.ClusterID, manifest string, dryRun bool) (domain.ApplyOutcome, error) {
	// A dry run persists nothing, so the cached reads it might otherwise
	// invalidate are still the truth once it returns — dropping them would
	// throw away a perfectly good cache for a call that changed nothing on
	// the cluster.
	if !dryRun {
		defer a.forgetReads(id)
	}

	prepared, err := a.prepareWrite(id, manifest)
	if err != nil {
		return domain.ApplyOutcome{}, err
	}

	// AN EDIT MUST CARRY THE VERSION IT WAS READ AT, and a manifest without
	// one is refused rather than quietly turned into something else.
	//
	// This path used to fall back to Create, and on AlreadyExists it fetched
	// the object solely to steal its resourceVersion and then REPLACED it
	// with the manifest. Pasting a Deployment that omits spec.replicas over
	// one an HPA had scaled to ten set replicas back to whatever the manifest
	// said; every label, annotation and field the paste did not mention was
	// deleted. That is not what `kubectl apply` does — it three-way merges
	// precisely to avoid it — and it was the one place in this write path
	// that could damage a cluster silently.
	//
	// Declared intent belongs to ApplyResource, which merges and names the
	// owner of anything it cannot change. This method is the EDITOR's verb:
	// a draft of the live object, replaced whole, under an optimistic lock.
	if prepared.object.GetResourceVersion() == "" {
		return domain.ApplyOutcome{}, fmt.Errorf(
			"%w: this manifest carries no metadata.resourceVersion, so it cannot be edited safely — "+
				"apply it as a new manifest instead", domain.ErrInvalidManifest)
	}

	var dryRunOpt []string
	if dryRun {
		dryRunOpt = []string{metav1.DryRunAll}
	}

	outcome, err := a.putResource(ctx, prepared.client, prepared.gvk, prepared.object, dryRun, dryRunOpt)
	if err != nil {
		return domain.ApplyOutcome{}, err
	}
	// COLLECTED AFTER THE WRITE RETURNS, which is what makes this correct
	// rather than merely wired: client-go calls the handler synchronously
	// while the response is being read, so by the time the call has returned
	// every warning it produced is in.
	outcome.Warnings = prepared.collector.collected()
	return outcome, nil
}

// putResource sends obj as an Update, carrying whatever resourceVersion the
// manifest already had — the caller has already confirmed it is non-empty.
// The API server enforces the optimistic lock; a stale version comes back as
// HTTP 409, which classify maps onto ports.ErrConflict.
func (a *Adapter) putResource(ctx context.Context, client dynamic.ResourceInterface, gvk schema.GroupVersionKind, obj *unstructured.Unstructured, dryRun bool, dryRunOpt []string) (domain.ApplyOutcome, error) {
	result, err := client.Update(ctx, obj, metav1.UpdateOptions{DryRun: dryRunOpt})
	if err != nil {
		return domain.ApplyOutcome{}, classify("applying resource", err)
	}
	return outcomeFrom(gvk, result, false, dryRun), nil
}

// outcomeFrom builds the ApplyOutcome the caller sees from what the server
// actually stored (or would have, under a dry run) — read back from result
// rather than echoed from the request, so a mutating admission webhook's
// changes (a default injected, a label added) are reflected in Name and
// Namespace exactly as they would be on the object itself.
func outcomeFrom(gvk schema.GroupVersionKind, result *unstructured.Unstructured, created, dryRun bool) domain.ApplyOutcome {
	return domain.ApplyOutcome{
		Created:   created,
		Kind:      gvk.Kind,
		Name:      result.GetName(),
		Namespace: domain.NamespaceName(result.GetNamespace()),
		DryRun:    dryRun,
	}
}

// decodeManifest parses manifest into exactly one Kubernetes object.
//
// sigs.k8s.io/yaml converts YAML to JSON and unmarshals into a generic map,
// which is what lets an *unstructured.Unstructured hold ANY kind — built-in
// or custom — without a Go type registered for it, unlike the typed
// apimachinery scheme this method used to decode through.
//
// Kubernetes' own YAML documents are separated by a "---" line, and
// utilyaml.NewYAMLReader is the same document splitter kubectl itself uses,
// so a manifest containing more than one is recognised and refused rather
// than silently applying only the first — UpdateResource is one atomic
// operation (see ManagementPort's own doc comment), and a multi-document
// apply would need per-document outcomes and per-document failure handling
// this method does not offer.
func decodeManifest(manifest string) (*unstructured.Unstructured, error) {
	reader := utilyaml.NewYAMLReader(bufio.NewReader(strings.NewReader(manifest)))

	var docs [][]byte
	for {
		doc, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", domain.ErrInvalidManifest, err)
		}
		if len(bytes.TrimSpace(doc)) == 0 {
			continue
		}
		docs = append(docs, doc)
	}

	switch len(docs) {
	case 0:
		return nil, fmt.Errorf("%w: manifest is empty", domain.ErrInvalidManifest)
	case 1:
		// Continues below.
	default:
		return nil, fmt.Errorf("%w: manifest contains %d objects — one object per apply",
			domain.ErrInvalidManifest, len(docs))
	}

	// NEITHER DECODE ERROR IS PASSED THROUGH, AND THAT IS A SECRETS RULE.
	//
	// Both libraries quote the document in their message. `UnmarshalJSON` on
	// a manifest with no `kind` reports
	//
	//   Object 'Kind' is missing in '{"apiVersion":"v1","data":{"password":"…"}}'
	//
	// — the WHOLE object, base64 Secret data and all. That error is rendered
	// in the dialog and passed through apiError, which logs it, so one
	// mistyped Secret manifest writes its contents to the log. PodSteer reads
	// Secrets only on request and never writes their values anywhere; an
	// error path that does it by accident is the same breach as doing it on
	// purpose.
	//
	// So the manifest is decoded into a plain map — which never calls
	// UnmarshalJSON — and every failure gets a sentence written here. The
	// operator has the manifest open in an editor in front of them; the byte
	// offset a library would quote back is not what they are short of.
	jsonBytes, err := sigsyaml.YAMLToJSON(docs[0])
	if err != nil {
		return nil, fmt.Errorf("%w: this is not valid YAML", domain.ErrInvalidManifest)
	}

	// UnmarshalJSON, NOT json.Unmarshal INTO THE MAP DIRECTLY. The two are not
	// interchangeable: encoding/json decodes every number as float64, and
	// unstructured requires int64 — so `replicas: 5` becomes a float nothing
	// downstream can read, NestedInt64 returns zero, and the object is quietly
	// wrong in a way only a test that reads a number back catches. It did.
	obj := &unstructured.Unstructured{}
	if err := obj.UnmarshalJSON(jsonBytes); err != nil {
		// Its message quotes the whole document, so it is never surfaced.
		// The document is decoded a second time — as a plain map, where the
		// float problem does not matter because nothing but the three
		// identity strings is read — purely to say WHICH field is missing.
		return nil, identityError(jsonBytes)
	}

	// NAMES WHAT IS ACTUALLY MISSING, not the whole requirement.
	//
	// This used to report "apiVersion, kind and metadata.name are required"
	// whichever one was absent — so a manifest with a perfectly good
	// apiVersion and kind and an empty name told the operator that three
	// things were wrong and left them to work out which. The Duplicate dialog
	// seeds exactly that manifest, deliberately: a duplicate needs a new name,
	// so it clears the field and marks it. Answering "all three" to the one
	// case the product itself produces is the message being wrong in the
	// place it is read most.
	if err := requireIdentity(obj.GetAPIVersion(), obj.GetKind(), obj.GetName()); err != nil {
		return nil, err
	}

	return obj, nil
}

// identityError says which identifying field a manifest the decoder refused
// is missing, without quoting the manifest.
//
// The decoder rejects an object with no `kind` before any of PodSteer's own
// checks run, and its message embeds the entire document — Secret data
// included. This re-reads the same bytes as a plain map, which is safe here
// because only three strings are read out of it, and produces the sentence
// the operator needs. A document that will not decode at all is not a
// Kubernetes object, and says so.
func identityError(jsonBytes []byte) error {
	var body map[string]any
	if err := json.Unmarshal(jsonBytes, &body); err != nil {
		return fmt.Errorf("%w: a manifest must be a single Kubernetes object",
			domain.ErrInvalidManifest)
	}

	text := func(key string) string {
		value, _ := body[key].(string)
		return value
	}
	name := ""
	if metadata, ok := body["metadata"].(map[string]any); ok {
		name, _ = metadata["name"].(string)
	}

	if err := requireIdentity(text("apiVersion"), text("kind"), name); err != nil {
		return err
	}
	return fmt.Errorf("%w: a manifest must be a single Kubernetes object",
		domain.ErrInvalidManifest)
}

// requireIdentity names the identifying fields that are missing, and only
// those.
//
// This used to report "apiVersion, kind and metadata.name are required"
// whichever one was absent — so a manifest with a perfectly good apiVersion
// and kind and an empty name told the operator three things were wrong and
// left them to work out which. The Duplicate dialog seeds exactly that
// manifest, deliberately: a duplicate needs a new name, so it clears the
// field and marks it. Answering "all three" to the one case the product
// itself produces is the message being wrong where it is read most.
func requireIdentity(apiVersion, kind, name string) error {
	var missing []string
	if apiVersion == "" {
		missing = append(missing, "apiVersion")
	}
	if kind == "" {
		missing = append(missing, "kind")
	}
	if name == "" {
		missing = append(missing, "metadata.name")
	}
	if len(missing) == 0 {
		return nil
	}

	verb := "are"
	if len(missing) == 1 {
		verb = "is"
	}
	return fmt.Errorf("%w: %s %s required",
		domain.ErrInvalidManifest, strings.Join(missing, ", "), verb)
}

// restMappingFor resolves gvk to its REST mapping (GVR and scope), rebuilding
// the cluster's cached RESTMapper exactly once when the lookup reports
// meta.NoKindMatchError. See clientFactory.rebuildRESTMapper's own doc
// comment for why exactly once: a CRD installed a minute ago must apply
// without reconnecting the cluster, but re-querying discovery on every apply
// of an ordinary built-in kind would erase the whole point of caching it.
func (f *clientFactory) restMappingFor(id domain.ClusterID, set *clients, gvk schema.GroupVersionKind) (*meta.RESTMapping, error) {
	mapper, err := f.restMapper(id, set)
	if err != nil {
		return nil, err
	}

	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err == nil {
		return mapping, nil
	}
	if !meta.IsNoMatchError(err) {
		return nil, err
	}

	mapper, err = f.rebuildRESTMapper(id, set)
	if err != nil {
		return nil, err
	}
	return mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
}
