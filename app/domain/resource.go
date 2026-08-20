package domain

import (
	"fmt"
	"strings"
)

// ResourceCategory groups kinds into the sections of the navigator.
//
// The grouping is a product decision, not a Kubernetes one: the API server has
// no notion of "Workloads". It lives in the domain because the navigator is
// part of what PodSteer *is*, and because the same grouping drives the overview
// counts and the command palette.
type ResourceCategory string

const (
	// CategoryCluster covers cluster-scoped infrastructure: nodes, namespaces,
	// events.
	CategoryCluster ResourceCategory = "Cluster"
	// CategoryWorkloads covers everything that runs containers.
	CategoryWorkloads ResourceCategory = "Workloads"
	// CategoryConfig covers configuration and secret material.
	CategoryConfig ResourceCategory = "Config"
	// CategoryNetwork covers service discovery and ingress.
	CategoryNetwork ResourceCategory = "Network"
	// CategoryStorage covers volumes and their classes.
	CategoryStorage ResourceCategory = "Storage"
	// CategoryAccessControl covers RBAC and identities.
	CategoryAccessControl ResourceCategory = "Access Control"
	// CategoryCustomResources covers anything defined by a CRD.
	CategoryCustomResources ResourceCategory = "Custom Resources"
)

// CategoryOrder is the order the navigator presents categories in.
//
// Deliberately not alphabetical: it runs from what an operator looks at most
// often to what they look at least, which is roughly cluster health, then the
// things they deploy, then the things those depend on.
var CategoryOrder = []ResourceCategory{
	CategoryCluster,
	CategoryWorkloads,
	CategoryConfig,
	CategoryNetwork,
	CategoryStorage,
	CategoryAccessControl,
	CategoryCustomResources,
}

// ResourceKind identifies a type of Kubernetes object.
//
// It carries both the API coordinates needed to fetch the objects and the
// presentation metadata needed to show them, because every consumer needs
// both and splitting them would mean two lookups keyed by the same identity.
type ResourceKind struct {
	// Group is the API group, empty for the core group.
	Group string
	// Version is the API version, e.g. "v1".
	Version string
	// Resource is the lowercase plural used in API paths, e.g. "deployments".
	Resource string
	// Kind is the CamelCase singular, e.g. "Deployment".
	Kind string
	// Namespaced reports whether objects of this kind live in a namespace.
	// Cluster-scoped kinds must not be queried with a namespace, and their
	// tables must not show a namespace column.
	Namespaced bool
	// Category places the kind in the navigator.
	Category ResourceCategory
	// Title is the plural display name, e.g. "Deployments".
	Title string
	// Singular is the singular display name, e.g. "Deployment".
	Singular string
	// Rich reports whether PodSteer has a purpose-built model and column set
	// for this kind. False means it is served by the generic table path,
	// which works for anything — including CRDs — but shows the columns the
	// API server prints rather than columns PodSteer chose.
	Rich bool
}

// ID returns a stable identifier for the kind, used as a navigation key and
// as the handle the frontend passes back when requesting a list.
//
// The form is "group/version/resource" with the core group rendered as "core"
// so the identifier never begins with a slash — an empty leading segment is a
// reliable source of routing bugs.
func (k ResourceKind) ID() string {
	group := k.Group
	if group == "" {
		group = "core"
	}
	return fmt.Sprintf("%s/%s/%s", group, k.Version, k.Resource)
}

// GroupVersion renders the API group and version as the API server writes it:
// "v1" for the core group, "apps/v1" otherwise.
func (k ResourceKind) GroupVersion() string {
	if k.Group == "" {
		return k.Version
	}
	return k.Group + "/" + k.Version
}

// IsZero reports whether the kind is unset.
func (k ResourceKind) IsZero() bool { return k.Resource == "" }

// ParseResourceKindID splits an identifier produced by ResourceKind.ID.
func ParseResourceKindID(id string) (group, version, resource string, err error) {
	parts := strings.Split(strings.TrimSpace(id), "/")
	if len(parts) != 3 || parts[1] == "" || parts[2] == "" {
		return "", "", "", fmt.Errorf("%w: %q is not a group/version/resource identifier",
			ErrInvalidResourceKind, id)
	}
	if parts[0] == "core" {
		parts[0] = ""
	}
	return parts[0], parts[1], parts[2], nil
}

// ResourceRef points at one object in one cluster.
//
// It is what the UI passes back when the operator selects a row, and what
// cross-links resolve to — a pod's "Controlled By" becomes a ref to its
// ReplicaSet, which becomes a ref to its Deployment.
type ResourceRef struct {
	// ClusterID is the cluster the object lives in.
	ClusterID ClusterID
	// Kind identifies the type of object.
	Kind ResourceKind
	// Namespace is empty for cluster-scoped objects.
	Namespace NamespaceName
	// Name is the object name.
	Name string
}

// IsZero reports whether the reference is unset.
func (r ResourceRef) IsZero() bool { return r.Name == "" || r.Kind.IsZero() }

// String renders the reference for logs and error messages.
func (r ResourceRef) String() string {
	if r.Namespace.IsAll() {
		return fmt.Sprintf("%s/%s", r.Kind.Kind, r.Name)
	}
	return fmt.Sprintf("%s/%s/%s", r.Kind.Kind, r.Namespace, r.Name)
}

// OwnerReference records what created an object.
//
// Kubernetes lets an object have several owners but at most one *controller*,
// and the controller is the one worth showing: it is the thing that will
// recreate the object if it is deleted, which is what an operator actually
// wants to know before deleting anything.
type OwnerReference struct {
	// Kind is the owner's kind, e.g. "ReplicaSet".
	Kind string
	// Name is the owner's name.
	Name string
	// Controller reports whether this owner is the controlling one.
	Controller bool
}

// IsZero reports whether the reference is unset.
func (o OwnerReference) IsZero() bool { return o.Name == "" }

// Controller returns the controlling owner from a list, or the zero value when
// nothing controls the object — a bare pod, or one whose controller was
// deleted with an orphan cascade.
func Controller(owners []OwnerReference) OwnerReference {
	for _, owner := range owners {
		if owner.Controller {
			return owner
		}
	}
	return OwnerReference{}
}
