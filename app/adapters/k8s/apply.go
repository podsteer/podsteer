package k8s

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

	obj, err := decodeManifest(manifest)
	if err != nil {
		return domain.ApplyOutcome{}, err
	}

	set, err := a.factory.clientsFor(id)
	if err != nil {
		return domain.ApplyOutcome{}, err
	}

	gvk := obj.GroupVersionKind()
	mapping, err := a.factory.restMappingFor(id, set, gvk)
	if err != nil {
		if meta.IsNoMatchError(err) {
			return domain.ApplyOutcome{}, fmt.Errorf("%w: the cluster does not serve kind %q (%s)",
				domain.ErrInvalidManifest, gvk.Kind, gvk.GroupVersion())
		}
		return domain.ApplyOutcome{}, err
	}

	namespaced := mapping.Scope.Name() == meta.RESTScopeNameNamespace
	namespace := obj.GetNamespace()
	switch {
	case namespaced && namespace == "":
		// There is no separate namespace parameter to fall back to — see
		// ManagementPort's doc comment — so a namespaced kind with nothing in
		// metadata.namespace is refused rather than guessed at (`default`,
		// say, would silently apply somewhere the operator did not ask for).
		return domain.ApplyOutcome{}, fmt.Errorf("%w: %s %q is namespaced and the manifest has no metadata.namespace",
			domain.ErrInvalidManifest, gvk.Kind, obj.GetName())
	case !namespaced && namespace != "":
		// A cluster-scoped kind has no namespace to apply into. The API
		// server ignores one on a cluster-scoped object anyway, so this is
		// dropped rather than treated as a reason to refuse.
		obj.SetNamespace("")
		namespace = ""
	}

	// The server rejects or ignores managedFields on a write, and the
	// editor's YAML tab can include them when the managed-fields toggle is
	// on (see ManagedFieldsToggle) — stripped here so create, update and dry
	// run all see the same object rather than depending on server-side
	// handling that differs by API server version.
	unstructured.RemoveNestedField(obj.Object, "metadata", "managedFields")

	namespaceable := set.dynamic.Resource(mapping.Resource)
	var client dynamic.ResourceInterface = namespaceable
	if namespaced {
		client = namespaceable.Namespace(namespace)
	}

	var dryRunOpt []string
	if dryRun {
		dryRunOpt = []string{metav1.DryRunAll}
	}

	// Warnings are not captured here. The API server attaches them as a
	// response header, and client-go's WarningHandler is configured once per
	// REST config — set.config is shared by every dynamic-client call this
	// cluster ever makes — rather than per request, so wiring it without
	// misattributing one apply's warnings to a concurrent one on the same
	// cluster (two tabs open on the same context, say) would need a
	// context-keyed collector this method does not build. Left empty rather
	// than wired unsafely; ApplyOutcome.Warnings is ready for it.
	if obj.GetResourceVersion() != "" {
		return a.putResource(ctx, client, gvk, obj, dryRun, dryRunOpt)
	}
	return a.createResource(ctx, client, gvk, obj, dryRun, dryRunOpt)
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

// createResource sends obj as a Create — the manifest carried no
// resourceVersion, so there is nothing to lock against yet. An AlreadyExists
// means the object exists despite that: an operator pasting a whole manifest
// over an existing object, treated as a replace by fetching the live
// resourceVersion and sending the pasted manifest as an Update with it.
func (a *Adapter) createResource(ctx context.Context, client dynamic.ResourceInterface, gvk schema.GroupVersionKind, obj *unstructured.Unstructured, dryRun bool, dryRunOpt []string) (domain.ApplyOutcome, error) {
	result, err := client.Create(ctx, obj, metav1.CreateOptions{DryRun: dryRunOpt})
	if err == nil {
		return outcomeFrom(gvk, result, true, dryRun), nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return domain.ApplyOutcome{}, classify("applying resource", err)
	}

	existing, err := client.Get(ctx, obj.GetName(), metav1.GetOptions{})
	if err != nil {
		return domain.ApplyOutcome{}, classify("applying resource", err)
	}
	obj.SetResourceVersion(existing.GetResourceVersion())

	result, err = client.Update(ctx, obj, metav1.UpdateOptions{DryRun: dryRunOpt})
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

	jsonBytes, err := sigsyaml.YAMLToJSON(docs[0])
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrInvalidManifest, err)
	}

	obj := &unstructured.Unstructured{}
	if err := obj.UnmarshalJSON(jsonBytes); err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrInvalidManifest, err)
	}

	if obj.GetAPIVersion() == "" || obj.GetKind() == "" || obj.GetName() == "" {
		return nil, fmt.Errorf("%w: apiVersion, kind and metadata.name are required",
			domain.ErrInvalidManifest)
	}

	return obj, nil
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
