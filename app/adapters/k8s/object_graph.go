package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"golang.org/x/sync/errgroup"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/podsteer/podsteer/app/domain"
)

// graphReferenceLimit caps how many distinct references one map resolves.
//
// EACH ONE IS A GET, so this is what keeps a drawer open at "a handful of
// narrowed reads" rather than at whatever an object somebody else wrote
// happens to name. Real objects are far below it — an Ingress with three
// backends and a certificate is four — and the pathological case is a
// generated Ingress with a path per tenant, which is precisely the one that
// must not turn a click into a hundred requests. What is dropped is said
// rather than silently omitted; see the Bounded line built below.
const graphReferenceLimit = 12

// graphReferenceReaders is how many of those GETs run at once. Enough that the
// reads overlap on a slow API server, low enough that opening a drawer never
// looks like a burst to whatever is rate-limiting the client.
const graphReferenceReaders = 4

// ObjectGraphSources reads what one object's neighbourhood map is drawn from.
//
// THE THIRD SHAPE'S READS, and deliberately the cheapest of the three. A pod's
// map costs five concurrent LISTs and a workload's the same again; this costs
// one GET for the subject, at most ObjectOwnerDepth more walking up its owner
// chain, at most graphReferenceLimit resolving what its spec names, and — for
// the one kind that has a cheap downward answer — one cached pod list. Nothing
// here lists a kind speculatively looking for children: a request per kind per
// open drawer is exactly the polling storm readcache.go exists to prevent.
//
// It gathers rather than assembles. Which field of which kind names what is a
// rule, and the rules live in the domain (ReferencesFromManifest) where they
// are argued with in a test; this reads the object, resolves the names it is
// given, and lets NewObjectGraph decide what connects.
func (a *Adapter) ObjectGraphSources(ctx context.Context, ref domain.ResourceRef) (domain.ObjectGraphInput, error) {
	op := fmt.Sprintf("reading the neighbourhood of %s in %q", ref, ref.ClusterID)

	set, err := a.factory.clientsFor(ref.ClusterID)
	if err != nil {
		return domain.ObjectGraphInput{}, err
	}

	namespace := ref.Namespace.String()
	if !ref.Kind.Namespaced {
		namespace = ""
	}

	gvr := schema.GroupVersionResource{
		Group:    ref.Kind.Group,
		Version:  ref.Kind.Version,
		Resource: ref.Kind.Resource,
	}

	object, err := dynamicFor(set.dynamic, gvr, ref.Kind.Namespaced, namespace).
		Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		// The subject is the one source with no partial answer: without it
		// there is nothing to draw a map around.
		return domain.ObjectGraphInput{}, classify(op, err)
	}

	input := domain.ObjectGraphInput{
		Kind:      ref.Kind.Kind,
		Name:      ref.Name,
		Namespace: namespace,
	}

	// A Secret reaches this function like any other kind, and its data must
	// not leave with it. Nothing below reads the object's body — only
	// metadata.ownerReferences and the reference fields the domain names — but
	// the masking is done here anyway, once, so a future rule that reaches
	// into the object cannot quietly start shipping key material through a
	// map. Read on request, never on render.
	maskSecretData(object)

	input.Owners = a.ownerNeighbourhood(ctx, set, ref.ClusterID, object, namespace)
	input.References, input.Unreadable = a.resolveReferences(
		ctx, set, ref.ClusterID, domain.ReferencesFromManifest(ref.Kind.Kind, object.Object), input.Unreadable)

	if selector := domain.SelectorFromManifest(ref.Kind.Kind, object.Object); len(selector) > 0 {
		// READ ONLY WHEN THE SELECTOR COULD MATCH SOMETHING. An empty selector
		// matches nothing — the domain's rule, not a shortcut taken here — so
		// listing a namespace's pods to hand the domain a set it is about to
		// reject would be a read that cannot change the answer.
		input.Selector = selector

		pods, err := a.ListPods(ctx, ref.ClusterID, ref.Namespace, domain.Projection{})
		if err != nil {
			input.Unreadable = append(input.Unreadable, "pods")
			a.logger.DebugContext(ctx, "object map could not read the namespace's pods",
				slog.String("object", ref.String()), slog.String("error", err.Error()))
		} else {
			input.Pods = pods
		}
	}

	return input, nil
}

// ownerNeighbourhood walks up the subject's ownerReferences.
//
// ONE GET PER HOP AND AT MOST ObjectOwnerDepth OF THEM. Upward is the free
// direction — Kubernetes wrote the names onto the object itself — but "free"
// only holds while it is bounded, and Kubernetes does not forbid a cycle. A
// hop that cannot be read stops the walk with what it has rather than failing
// the map: a short chain is still worth drawing, and the alternative is that
// one unreadable ancestor blanks a map somebody could otherwise use.
func (a *Adapter) ownerNeighbourhood(ctx context.Context, set *clients, id domain.ClusterID, object *unstructured.Unstructured, namespace string) []domain.ObjectReference {
	var chain []domain.ObjectReference

	// Keyed by UID, which is the only identifier Kubernetes guarantees unique
	// across a cluster's whole history. A cycle two hops long — an operator
	// that writes an ownerReference back onto its own parent — terminates here
	// rather than in the depth cap, and would terminate here even if the cap
	// were raised.
	seen := map[string]bool{string(object.GetUID()): true}
	current := object

	for len(chain) < domain.ObjectOwnerDepth {
		owner := controllingOwner(current.GetOwnerReferences())
		if owner == nil {
			return chain
		}
		if seen[string(owner.UID)] {
			return chain
		}
		seen[string(owner.UID)] = true

		group, version := domain.SplitAPIVersion(owner.APIVersion)
		ref := domain.ObjectReference{
			Group: group, Version: version, Kind: owner.Kind, Name: owner.Name,
		}

		above, err := a.readObject(ctx, set, id, ref, namespace)
		if err != nil {
			// A refused or absent owner is still a real ownerReference: the
			// name is on the object whether or not this account may read what
			// it names, so the box is drawn and marked rather than dropped —
			// dropping it would make an object owned by something invisible
			// look like an object owned by nothing.
			ref.Missing = apierrors.IsNotFound(err)
			ref.Namespace = ownerNamespaceOf(ref, namespace)
			a.logger.DebugContext(ctx, "object map owner chain stopped",
				slog.String("kind", owner.Kind), slog.String("name", owner.Name),
				slog.String("error", err.Error()))
			return append(chain, ref)
		}

		// From the object itself rather than assumed: a cluster-scoped owner
		// reports none, which is how a Namespace or a CRD above a namespaced
		// object comes out right.
		ref.Namespace = above.GetNamespace()
		chain = append(chain, ref)
		current = above
	}

	return chain
}

// resolveReferences turns the names a spec carries into references that know
// whether the object is actually there.
//
// EXISTENCE IS ESTABLISHED, NOT ASSUMED. A reference that resolves to nothing
// is usually the reason somebody opened the map — an Ingress naming a Service
// that was renamed, a PVC waiting on a StorageClass nobody installed — and a
// map that draws the broken case exactly like the working one answers nothing.
// Bounded by graphReferenceLimit, and what the bound dropped is named rather
// than silently absent.
func (a *Adapter) resolveReferences(ctx context.Context, set *clients, id domain.ClusterID, references []domain.ObjectReference, unreadable []string) ([]domain.ObjectReference, []string) {
	// ONE ENTRY PER REFERENCED OBJECT, however many fields name it: an Ingress
	// with twelve paths onto one Service is one GET, not twelve.
	seen := make(map[string]bool, len(references))
	distinct := make([]domain.ObjectReference, 0, len(references))

	for _, ref := range references {
		key := ref.Kind + "/" + ref.Namespace + "/" + ref.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		distinct = append(distinct, ref)
	}

	if len(distinct) > graphReferenceLimit {
		unreadable = append(unreadable, fmt.Sprintf(
			"%d further references, beyond the %d this map resolves",
			len(distinct)-graphReferenceLimit, graphReferenceLimit))
		distinct = distinct[:graphReferenceLimit]
	}

	group, ctx := errgroup.WithContext(ctx)
	group.SetLimit(graphReferenceReaders)

	var mu sync.Mutex

	for i := range distinct {
		group.Go(func() error {
			_, err := a.readObject(ctx, set, id, distinct[i], distinct[i].Namespace)
			if err == nil {
				return nil
			}

			mu.Lock()
			defer mu.Unlock()

			if apierrors.IsNotFound(err) {
				distinct[i].Missing = true
				return nil
			}

			// FORBIDDEN IS NOT ABSENT, and the difference decides what
			// somebody does next. An account that may not read Secrets must
			// not be told the Secret an Ingress names does not exist — that
			// sends them to recreate an object that is sitting there. The box
			// is drawn unmarked and the refusal is named.
			unreadable = append(unreadable, fmt.Sprintf("%s/%s", distinct[i].Kind, distinct[i].Name))
			a.logger.DebugContext(ctx, "object map could not resolve a reference",
				slog.String("kind", distinct[i].Kind), slog.String("name", distinct[i].Name),
				slog.String("error", err.Error()))
			return nil
		})
	}

	// Nothing returns an error, so this cannot fail — it is the wait, and the
	// place a future rule that does fail would surface.
	_ = group.Wait()

	return distinct, unreadable
}

// readObject fetches one referenced object through the dynamic client.
//
// Resolved through the cluster's RESTMapper rather than a hand-built path, so
// a reference to a CRD works exactly like one to a built-in kind — and so that
// a reference naming only an API group (a roleRef's, a PVC data source's)
// resolves to whichever version the cluster actually serves.
func (a *Adapter) readObject(ctx context.Context, set *clients, id domain.ClusterID, ref domain.ObjectReference, namespace string) (*unstructured.Unstructured, error) {
	mapping, err := a.factory.restMappingFor(id, set, schema.GroupVersionKind{
		Group: ref.Group, Version: ref.Version, Kind: ref.Kind,
	})
	if err != nil {
		if meta.IsNoMatchError(err) {
			// A kind the cluster does not serve cannot hold the object, but
			// this is NOT the same fact as "the object was deleted" and must
			// not arrive at the map as one — an operator uninstalled is a
			// different problem from an object renamed.
			return nil, fmt.Errorf("the cluster does not serve kind %q: %w", ref.Kind, err)
		}
		return nil, err
	}

	namespaced := mapping.Scope.Name() == meta.RESTScopeNameNamespace
	if !namespaced {
		namespace = ""
	} else if ref.Namespace != "" {
		namespace = ref.Namespace
	}

	return dynamicFor(set.dynamic, mapping.Resource, namespaced, namespace).
		Get(ctx, ref.Name, metav1.GetOptions{})
}

// dynamicFor narrows the dynamic client to a resource, and to a namespace when
// the kind has one.
func dynamicFor(client dynamic.Interface, gvr schema.GroupVersionResource, namespaced bool, namespace string) dynamic.ResourceInterface {
	resource := client.Resource(gvr)
	if namespaced && namespace != "" {
		return resource.Namespace(namespace)
	}
	return resource
}

// controllingOwner returns the owner worth walking to.
//
// THE CONTROLLER WHERE THERE IS ONE. Kubernetes lets an object carry several
// owners and at most one controller, and the controller is the one that would
// recreate it — which is what an owner chain is drawn to show. Where nothing
// controls it, the first plain owner is still a real ownerReference and still
// worth following; a garbage-collection owner with no controller flag is how
// several operators express "this belongs to that".
func controllingOwner(owners []metav1.OwnerReference) *metav1.OwnerReference {
	for i := range owners {
		if owners[i].Controller != nil && *owners[i].Controller {
			return &owners[i]
		}
	}
	if len(owners) > 0 {
		return &owners[0]
	}
	return nil
}

// ownerNamespaceOf guesses the namespace of an owner that could not be read.
//
// Kubernetes forbids a namespaced owner in another namespace, so the subject's
// own is the only possibility — and a cluster-scoped owner has none, which is
// indistinguishable from here without the read that just failed. The subject's
// namespace is the answer that is right for every owner the built-in
// controllers write.
func ownerNamespaceOf(ref domain.ObjectReference, namespace string) string {
	if ref.Namespace != "" {
		return ref.Namespace
	}
	return namespace
}
