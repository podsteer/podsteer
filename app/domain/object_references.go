package domain

import "strings"

// Reference extraction for the general object map.
//
// PURE, AND OVER THE DECODED MANIFEST RATHER THAN A TYPED OBJECT. Every rule
// here is "this field of this kind names that object", which is a rule and so
// belongs where rules are argued with in a test — and a decoded manifest is
// map[string]any, which needs nothing outside the standard library. The
// adapter reads the object and resolves the names; it decides nothing about
// what a name means.
//
// WHAT IS DELIBERATELY ABSENT is as load-bearing as what is here. There is no
// rule matching objects by label, by name, or by "it is in the same namespace
// and looks related". Kubernetes either names a thing or it does not, and a
// map drawn from anything weaker is a map that invents relationships on
// clusters where the convention it assumed was not followed.

// ReferencesFromManifest lists what one object's own spec names.
//
// Ordered as the fields are read rather than sorted, so a caller resolving a
// bounded prefix of them resolves the ones nearest the top of the spec — and
// NewObjectGraph's sort settles the drawing order anyway.
func ReferencesFromManifest(kind string, object map[string]any) []ObjectReference {
	namespace := nestedString(object, "metadata", "namespace")
	spec := nestedMap(object, "spec")

	switch kind {
	case "Ingress":
		return ingressReferences(spec, namespace)
	case "PersistentVolumeClaim":
		return claimReferences(spec, namespace)
	case "PersistentVolume":
		return volumeReferences(spec)
	case "ServiceAccount":
		return serviceAccountReferences(object, namespace)
	case "HorizontalPodAutoscaler":
		return autoscalerReferences(spec, namespace)
	case "RoleBinding", "ClusterRoleBinding":
		return bindingReferences(object, namespace)
	default:
		// A kind with no rule here draws its owner chain and nothing else,
		// which is honest. Guessing at a field name that looks like a
		// reference — anything ending in "Ref", say — would draw edges out of
		// a CRD's spec that the operator that wrote it never meant as
		// references at all.
		return nil
	}
}

// ingressReferences reads what an Ingress routes to and terminates with.
func ingressReferences(spec map[string]any, namespace string) []ObjectReference {
	var refs []ObjectReference

	if class := nestedString(spec, "ingressClassName"); class != "" {
		// Cluster-scoped, hence no namespace: an IngressClass is shared by
		// every namespace that names it.
		refs = append(refs, ObjectReference{
			Group: "networking.k8s.io", Version: "v1", Kind: "IngressClass",
			Name: class, Via: "ingress class",
		})
	}

	// THE DEFAULT BACKEND COUNTS. An Ingress with no rules still routes
	// everything to it, so an Ingress whose only backend is the default one
	// would otherwise draw as routing nowhere.
	refs = append(refs, backendReference(nestedMap(spec, "defaultBackend"), namespace, "default backend")...)

	for _, rule := range nestedSlice(spec, "rules") {
		for _, path := range nestedSlice(nestedMap(rule, "http"), "paths") {
			via := "routes to"
			if value := nestedString(path, "path"); value != "" {
				via = value
			}
			refs = append(refs, backendReference(nestedMap(path, "backend"), namespace, via)...)
		}
	}

	for _, tls := range nestedSlice(spec, "tls") {
		if name := nestedString(tls, "secretName"); name != "" {
			refs = append(refs, ObjectReference{
				Version: "v1", Kind: "Secret", Name: name,
				Namespace: namespace, Via: "tls certificate",
			})
		}
	}

	return refs
}

// backendReference reads one Ingress backend, which is a Service or — since
// networking.k8s.io/v1 — any resource an ingress controller understands.
func backendReference(backend map[string]any, namespace, via string) []ObjectReference {
	if backend == nil {
		return nil
	}

	if service := nestedMap(backend, "service"); service != nil {
		if name := nestedString(service, "name"); name != "" {
			return []ObjectReference{{
				Version: "v1", Kind: "Service", Name: name,
				Namespace: namespace, Via: via,
			}}
		}
	}

	// A resource backend names a kind in an arbitrary API group — how an
	// ingress controller is pointed at its own CRD for a static asset bucket
	// or a redirect. Drawn like any other reference: the kind travels
	// verbatim, so the drawer can follow it.
	if resource := nestedMap(backend, "resource"); resource != nil {
		name := nestedString(resource, "name")
		kind := nestedString(resource, "kind")
		if name != "" && kind != "" {
			return []ObjectReference{{
				Group: nestedString(resource, "apiGroup"),
				Kind:  kind, Name: name, Namespace: namespace, Via: via,
			}}
		}
	}

	return nil
}

// claimReferences reads what a PersistentVolumeClaim is bound to and asks for.
func claimReferences(spec map[string]any, namespace string) []ObjectReference {
	var refs []ObjectReference

	// spec.volumeName is written by the binder once a volume is actually
	// bound, so its absence means "still pending" rather than "no volume" —
	// which is why an empty one draws nothing instead of a missing box.
	if name := nestedString(spec, "volumeName"); name != "" {
		refs = append(refs, ObjectReference{
			Version: "v1", Kind: "PersistentVolume", Name: name, Via: "bound to",
		})
	}

	// An EMPTY storageClassName is not an absent one: "" explicitly opts out
	// of dynamic provisioning, where absent means "use the default class".
	// Neither names an object, so neither draws one.
	if name := nestedString(spec, "storageClassName"); name != "" {
		refs = append(refs, ObjectReference{
			Group: "storage.k8s.io", Version: "v1", Kind: "StorageClass",
			Name: name, Via: "storage class",
		})
	}

	// dataSourceRef supersedes dataSource and may name a cross-namespace
	// source; dataSource is still what most clusters carry. Read both, in
	// that order, and let the map's own deduplication collapse them when they
	// agree — which is the normal case, because the API server copies one
	// onto the other.
	for _, field := range []string{"dataSourceRef", "dataSource"} {
		source := nestedMap(spec, field)
		if source == nil {
			continue
		}
		name := nestedString(source, "name")
		kind := nestedString(source, "kind")
		if name == "" || kind == "" {
			continue
		}

		from := namespace
		if other := nestedString(source, "namespace"); other != "" {
			from = other
		}
		refs = append(refs, ObjectReference{
			Group: nestedString(source, "apiGroup"),
			Kind:  kind, Name: name, Namespace: from, Via: "cloned from",
		})
	}

	return refs
}

// volumeReferences reads what a PersistentVolume is claimed by and provisioned
// from.
func volumeReferences(spec map[string]any) []ObjectReference {
	var refs []ObjectReference

	// THE ONE GENUINELY CROSS-NAMESPACE REFERENCE in the Kubernetes API a map
	// of this shape meets: a PersistentVolume is cluster-scoped and its
	// claimRef names a namespace of its own, so the namespace has to come
	// from the reference rather than from the subject.
	if claim := nestedMap(spec, "claimRef"); claim != nil {
		if name := nestedString(claim, "name"); name != "" {
			refs = append(refs, ObjectReference{
				Version: "v1", Kind: "PersistentVolumeClaim", Name: name,
				Namespace: nestedString(claim, "namespace"), Via: "claimed by",
			})
		}
	}

	if name := nestedString(spec, "storageClassName"); name != "" {
		refs = append(refs, ObjectReference{
			Group: "storage.k8s.io", Version: "v1", Kind: "StorageClass",
			Name: name, Via: "storage class",
		})
	}

	return refs
}

// serviceAccountReferences reads the Secrets a ServiceAccount names.
//
// NAMED, NOT READ. These are references drawn from the ServiceAccount's own
// fields; nothing here fetches a Secret's contents, and the boxes carry only
// names — the same discipline the rest of the application follows for
// Secrets, which are read on request and never on render.
func serviceAccountReferences(object map[string]any, namespace string) []ObjectReference {
	var refs []ObjectReference

	for _, entry := range nestedSlice(object, "secrets") {
		if name := nestedString(entry, "name"); name != "" {
			refs = append(refs, ObjectReference{
				Version: "v1", Kind: "Secret", Name: name,
				Namespace: namespace, Via: "token",
			})
		}
	}

	for _, entry := range nestedSlice(object, "imagePullSecrets") {
		if name := nestedString(entry, "name"); name != "" {
			refs = append(refs, ObjectReference{
				Version: "v1", Kind: "Secret", Name: name,
				Namespace: namespace, Via: "pulls images",
			})
		}
	}

	return refs
}

// autoscalerReferences reads what an HPA scales.
func autoscalerReferences(spec map[string]any, namespace string) []ObjectReference {
	target := nestedMap(spec, "scaleTargetRef")
	if target == nil {
		return nil
	}

	name := nestedString(target, "name")
	kind := nestedString(target, "kind")
	if name == "" || kind == "" {
		return nil
	}

	group, version := SplitAPIVersion(nestedString(target, "apiVersion"))
	return []ObjectReference{{
		Group: group, Version: version, Kind: kind,
		Name: name, Namespace: namespace, Via: "scales",
	}}
}

// bindingReferences reads the role a binding grants and who it grants it to.
//
// ONLY ServiceAccount SUBJECTS DRAW A BOX. A User or a Group subject is a
// string an authenticator produced, not an object in the cluster, so there is
// nothing to navigate to and nothing to check the existence of.
func bindingReferences(object map[string]any, namespace string) []ObjectReference {
	var refs []ObjectReference

	if role := nestedMap(object, "roleRef"); role != nil {
		name := nestedString(role, "name")
		kind := nestedString(role, "kind")
		if name != "" && kind != "" {
			// A ClusterRole is cluster-scoped whatever binds it; a Role is
			// always in the binding's own namespace.
			from := namespace
			if kind == "ClusterRole" {
				from = ""
			}
			refs = append(refs, ObjectReference{
				Group: nestedString(role, "apiGroup"),
				Kind:  kind, Name: name, Namespace: from, Via: "grants",
			})
		}
	}

	for _, subject := range nestedSlice(object, "subjects") {
		if nestedString(subject, "kind") != "ServiceAccount" {
			continue
		}
		name := nestedString(subject, "name")
		if name == "" {
			continue
		}

		// A ClusterRoleBinding's subjects carry their own namespace, and a
		// RoleBinding's may name another one.
		from := nestedString(subject, "namespace")
		if from == "" {
			from = namespace
		}
		refs = append(refs, ObjectReference{
			Version: "v1", Kind: "ServiceAccount", Name: name,
			Namespace: from, Via: "granted to",
		})
	}

	return refs
}

// SelectorFromManifest reads the pod selector of a kind that has one.
//
// SERVICE AND NOTHING ELSE TODAY, because a Service's selector is the one
// selector this map follows to pods — and because a selector that reaches the
// domain lossily is worse than none at all. spec.selector on a Service is a
// flat map of equality requirements with no matchExpressions form to lose,
// unlike a workload's, which is precisely why attribution elsewhere in this
// codebase goes through ownerReferences instead.
func SelectorFromManifest(kind string, object map[string]any) map[string]string {
	if kind != "Service" {
		return nil
	}

	raw := nestedMap(nestedMap(object, "spec"), "selector")
	if len(raw) == 0 {
		return nil
	}

	selector := make(map[string]string, len(raw))
	for key, value := range raw {
		text, ok := value.(string)
		if !ok {
			// A non-string selector value is not something Kubernetes accepts,
			// and treating it as a match would widen the selector rather than
			// narrow it — the direction that draws edges that do not exist.
			return nil
		}
		selector[key] = text
	}
	return selector
}

// SplitAPIVersion separates an apiVersion field into its group and version.
//
// The core group is written as a bare version ("v1") and everything else as
// "group/version". Getting this backwards is the commonest way to build a
// Kubernetes API path that 404s, so it is one function with one test rather
// than a split repeated at each call site.
func SplitAPIVersion(apiVersion string) (group, version string) {
	if slash := strings.Index(apiVersion, "/"); slash >= 0 {
		return apiVersion[:slash], apiVersion[slash+1:]
	}
	return "", apiVersion
}

// nestedMap reads a nested object, or nil.
func nestedMap(object map[string]any, path ...string) map[string]any {
	current := object
	for _, key := range path {
		if current == nil {
			return nil
		}
		next, ok := current[key].(map[string]any)
		if !ok {
			return nil
		}
		current = next
	}
	return current
}

// nestedSlice reads a nested list of objects, skipping any element that is not
// one.
func nestedSlice(object map[string]any, path ...string) []map[string]any {
	if object == nil || len(path) == 0 {
		return nil
	}

	parent := object
	if len(path) > 1 {
		parent = nestedMap(object, path[:len(path)-1]...)
	}
	if parent == nil {
		return nil
	}

	raw, ok := parent[path[len(path)-1]].([]any)
	if !ok {
		return nil
	}

	items := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		if item, ok := entry.(map[string]any); ok {
			items = append(items, item)
		}
	}
	return items
}

// nestedString reads a nested string, or "".
func nestedString(object map[string]any, path ...string) string {
	if object == nil || len(path) == 0 {
		return ""
	}

	parent := object
	if len(path) > 1 {
		parent = nestedMap(object, path[:len(path)-1]...)
	}
	if parent == nil {
		return ""
	}

	text, _ := parent[path[len(path)-1]].(string)
	return text
}
