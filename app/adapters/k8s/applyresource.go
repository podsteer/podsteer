package k8s

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/podsteer/podsteer/app/domain"
)

// fieldManager is the name the API server records against every field
// PodSteer writes.
//
// EXPLICIT, NOT DERIVED, and that is the change. client-go falls back to the
// USER AGENT when no field manager is given, so until now the name in every
// object's managedFields was whatever `defaultUserAgent` happened to be — and
// Config.UserAgent is operator-settable, so an override silently renamed the
// manager on every object PodSteer had ever written. The name is a durable
// mark on somebody's cluster that outlives the uninstall; it should not depend
// on a diagnostic string.
//
// "podsteer" rather than "podsteer-<user>": the value is written into
// metadata.managedFields, readable by anyone with `get`, and putting a
// kubeconfig user's name there breaks the same no-identity discipline this
// codebase keeps for files and notifications. Two operators sharing a cluster
// are one manager, exactly as every kubectl user is "kubectl" — the audit log
// already records who, and coordinating two humans is what the optimistic
// lock is for.
const fieldManager = "podsteer"

// preparedWrite is everything both write verbs need, resolved once.
type preparedWrite struct {
	gvk       schema.GroupVersionKind
	object    *unstructured.Unstructured
	client    dynamic.ResourceInterface
	collector *warningCollector
}

// prepareWrite decodes a manifest, resolves its kind against the cluster and
// returns a client scoped to where the object belongs.
//
// Shared by UpdateResource and ApplyResource because every rule here is about
// the MANIFEST rather than about the verb: a namespaced kind with no namespace
// is refused either way, a cluster-scoped kind's namespace is dropped either
// way, and managedFields is stripped either way.
func (a *Adapter) prepareWrite(id domain.ClusterID, manifest string) (*preparedWrite, error) {
	obj, err := decodeManifest(manifest)
	if err != nil {
		return nil, err
	}

	set, err := a.factory.clientsFor(id)
	if err != nil {
		return nil, err
	}

	gvk := obj.GroupVersionKind()
	mapping, err := a.factory.restMappingFor(id, set, gvk)
	if err != nil {
		if meta.IsNoMatchError(err) {
			return nil, fmt.Errorf("%w: the cluster does not serve kind %q (%s)",
				domain.ErrInvalidManifest, gvk.Kind, gvk.GroupVersion())
		}
		return nil, err
	}

	namespaced := mapping.Scope.Name() == meta.RESTScopeNameNamespace
	namespace := obj.GetNamespace()
	switch {
	case namespaced && namespace == "":
		// There is no separate namespace parameter to fall back to — see
		// ManagementPort's doc comment — so a namespaced kind with nothing in
		// metadata.namespace is refused rather than guessed at (`default`,
		// say, would silently apply somewhere the operator did not ask for).
		return nil, fmt.Errorf("%w: %s %q is namespaced and the manifest has no metadata.namespace",
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
	// on (see ManagedFieldsToggle) — stripped here so every verb sees the
	// same object rather than depending on server-side handling that differs
	// by API server version.
	unstructured.RemoveNestedField(obj.Object, "metadata", "managedFields")

	// A CLIENT OF THIS WRITE'S OWN, so the API server's warnings can be
	// attributed to it. See warningCollector.
	warned, collector, err := applyClient(set)
	if err != nil {
		return nil, classify(fmt.Sprintf("preparing write for %q", id), err)
	}

	namespaceable := warned.Resource(mapping.Resource)
	var client dynamic.ResourceInterface = namespaceable
	if namespaced {
		client = namespaceable.Namespace(namespace)
	}

	return &preparedWrite{gvk: gvk, object: obj, client: client, collector: collector}, nil
}

// ApplyResource applies a manifest as DECLARED INTENT, through server-side
// apply.
//
// THE OTHER WRITE VERB, AND THE DISTINCTION IS THE WHOLE DESIGN.
// UpdateResource is the EDITOR's: a draft of the live object, carrying the
// resourceVersion it was read at, replaced whole under an optimistic lock —
// which is the only thing a full-object editor can honestly promise, because
// deleting a line there must delete the field.
//
// This one is for a manifest somebody wrote or pasted: the Create dialog,
// Duplicate, a file dropped in. Those name the fields they care about and say
// nothing about the rest, and the rest must be LEFT ALONE. What this replaces
// did the opposite — a Create that hit AlreadyExists fetched the live object
// solely for its resourceVersion and then replaced it, so pasting a Deployment
// manifest without spec.replicas over one an HPA had scaled to ten reset the
// replica count and deleted every label, annotation and field the paste did
// not mention.
//
// A CONFLICT IS AN OUTCOME, NOT AN ERROR. Where another manager owns a field
// this manifest would change, the server refuses and names them, nothing is
// written, and the refusal comes back in ApplyOutcome.Conflicts for the
// interface to render. That refusal is strictly better than what it replaces:
// the same paste over an Argo CD-managed object used to succeed, wipe fields,
// and be reverted on the next sync with nobody told.
func (a *Adapter) ApplyResource(
	ctx context.Context,
	id domain.ClusterID,
	manifest string,
	options domain.ApplyOptions,
) (domain.ApplyOutcome, error) {
	if !options.DryRun {
		defer a.forgetReads(id)
	}

	prepared, err := a.prepareWrite(id, manifest)
	if err != nil {
		return domain.ApplyOutcome{}, err
	}

	// WHETHER IT EXISTS IS ASKED BEFORE, because an apply cannot tell.
	// Create and Update are different calls and say which they were; apply is
	// one call that does either. A NotFound here means it did not exist, and
	// the only way this is wrong is an object created between this read and
	// the apply — which misreports "Applied" as "Created". That is a word,
	// not a write.
	created := false
	if _, err := prepared.client.Get(ctx, prepared.object.GetName(), metav1.GetOptions{}); err != nil {
		if !apierrors.IsNotFound(err) {
			return domain.ApplyOutcome{}, classify("applying resource", err)
		}
		created = true
	}

	var dryRunOpt []string
	if options.DryRun {
		dryRunOpt = []string{metav1.DryRunAll}
	}

	result, err := prepared.client.Apply(ctx, prepared.object.GetName(), prepared.object, metav1.ApplyOptions{
		FieldManager: fieldManager,
		DryRun:       dryRunOpt,
		// NEVER Force IN THIS INCREMENT. Taking a field from another manager
		// is a decision an operator makes with the owner's name in front of
		// them, and there is nowhere yet to show them that. Until there is, a
		// conflict is reported rather than won.
		Force: false,
	})
	if err != nil {
		// READ BEFORE classify, which would fold this into ErrConflict and
		// lose the causes — the list of fields and owners is the entire
		// answer here, and a single sentence cannot carry it.
		if conflicts := fieldConflictsFrom(err); len(conflicts) > 0 {
			return domain.ApplyOutcome{
				Kind:      prepared.gvk.Kind,
				Name:      prepared.object.GetName(),
				Namespace: domain.NamespaceName(prepared.object.GetNamespace()),
				DryRun:    options.DryRun,
				Conflicts: conflicts,
				Warnings:  prepared.collector.collected(),
			}, nil
		}
		return domain.ApplyOutcome{}, classify("applying resource", err)
	}

	outcome := outcomeFrom(prepared.gvk, result, created, options.DryRun)
	outcome.Warnings = prepared.collector.collected()
	return outcome, nil
}

// fieldConflictsFrom reads the fields and owners out of a 409, or returns
// nothing when the error is not an ownership conflict.
//
// The server puts one StatusCause per contested field in Details.Causes, with
// Type FieldManagerConflict, the field path in Field and its owner named in
// Message. A stale-resourceVersion conflict is also a 409 and carries no such
// causes, which is what keeps the two apart here.
func fieldConflictsFrom(err error) domain.FieldConflicts {
	if !apierrors.IsConflict(err) {
		return nil
	}

	status, ok := err.(apierrors.APIStatus)
	if !ok || status.Status().Details == nil {
		return nil
	}

	conflicts := make(domain.FieldConflicts, 0, len(status.Status().Details.Causes))
	for _, cause := range status.Status().Details.Causes {
		if cause.Type != metav1.CauseTypeFieldManagerConflict {
			continue
		}
		conflicts = append(conflicts, domain.ParseFieldConflict(cause.Field, cause.Message))
	}
	if len(conflicts) == 0 {
		return nil
	}
	return conflicts
}
