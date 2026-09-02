package domain

import (
	"fmt"
	"sync"
)

// This file is the catalog of kinds PodSteer knows about up front. It is the
// single source of truth for the navigator, so adding a section to the UI is
// an entry here rather than a change in the frontend.
//
// Kinds discovered at runtime — everything defined by a CRD — are not listed
// here. They are appended to this catalog by the discovery adapter, which is
// why the lookup below is guarded: the CRD set of a cluster is not known until
// PodSteer has connected to it.

// WHICH CATEGORY A KIND GOES IN, as a rule rather than by resemblance to
// whatever it would sit next to. Categorise a kind by WHAT ITS STATUS IS
// ABOUT — the thing it acts on — never by its API group and never by what it
// merely mentions. First match wins:
//
//  1. Defined by a CRD                                    → Custom Resources
//  2. Runs containers, or targets a workload to change
//     whether or how many of its pods run                 → Workloads
//  3. Decides who may do what, or what identity
//     something runs as                                   → Access Control
//  4. Decides how traffic reaches, leaves or is refused    → Network
//  5. Decides where bytes persist                          → Storage
//  6. Passive data or ambient policy that pods and
//     namespaces consume                                   → Config
//  7. The cluster's own structure, or its commentary       → Cluster
//
// The ORDER is load-bearing. Rule 2 before rule 6 is what puts an autoscaler
// with the workloads it resizes rather than with the ConfigMaps it does not
// resemble; rule 1 before everything is what keeps per-cluster discovery
// honest.
//
// Built-in kinds. `Rich: true` marks the ones with a purpose-built model and
// column set; the rest are served by the generic table path, which renders
// whatever columns the API server prints.
var builtInKinds = []ResourceKind{
	// --- Cluster ---
	{Version: "v1", Resource: "nodes", Kind: "Node",
		Namespaced: false, Category: CategoryCluster, Title: "Nodes", Singular: "Node", Rich: true},
	{Version: "v1", Resource: "namespaces", Kind: "Namespace",
		Namespaced: false, Category: CategoryCluster, Title: "Namespaces", Singular: "Namespace", Rich: true},
	{Version: "v1", Resource: "events", Kind: "Event",
		Namespaced: true, Category: CategoryCluster, Title: "Events", Singular: "Event", Rich: true},

	// --- Workloads ---
	{Version: "v1", Resource: "pods", Kind: "Pod",
		Namespaced: true, Category: CategoryWorkloads, Title: "Pods", Singular: "Pod", Rich: true},
	{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment",
		Namespaced: true, Category: CategoryWorkloads, Title: "Deployments", Singular: "Deployment", Rich: true},
	{Group: "apps", Version: "v1", Resource: "statefulsets", Kind: "StatefulSet",
		Namespaced: true, Category: CategoryWorkloads, Title: "StatefulSets", Singular: "StatefulSet", Rich: true},
	{Group: "apps", Version: "v1", Resource: "daemonsets", Kind: "DaemonSet",
		Namespaced: true, Category: CategoryWorkloads, Title: "DaemonSets", Singular: "DaemonSet", Rich: true},
	{Group: "apps", Version: "v1", Resource: "replicasets", Kind: "ReplicaSet",
		Namespaced: true, Category: CategoryWorkloads, Title: "ReplicaSets", Singular: "ReplicaSet", Rich: true},
	{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job",
		Namespaced: true, Category: CategoryWorkloads, Title: "Jobs", Singular: "Job", Rich: true},
	{Group: "batch", Version: "v1", Resource: "cronjobs", Kind: "CronJob",
		Namespaced: true, Category: CategoryWorkloads, Title: "CronJobs", Singular: "CronJob", Rich: true},
	// The two that decide how many pods a controller runs, and how few it may
	// be reduced to. Not configuration: nothing consumes them, they are
	// controllers with live status that act ON a workload — and the question
	// somebody holds when opening either ("why is this at twelve replicas",
	// "why will this node not drain") is asked while looking at Workloads.
	{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers", Kind: "HorizontalPodAutoscaler",
		Namespaced: true, Category: CategoryWorkloads, Title: "Autoscalers", Singular: "Autoscaler"},
	{Group: "policy", Version: "v1", Resource: "poddisruptionbudgets", Kind: "PodDisruptionBudget",
		Namespaced: true, Category: CategoryWorkloads, Title: "Disruption Budgets", Singular: "Disruption Budget"},

	// --- Config ---
	{Version: "v1", Resource: "configmaps", Kind: "ConfigMap",
		Namespaced: true, Category: CategoryConfig, Title: "ConfigMaps", Singular: "ConfigMap"},
	{Version: "v1", Resource: "secrets", Kind: "Secret",
		Namespaced: true, Category: CategoryConfig, Title: "Secrets", Singular: "Secret"},
	{Version: "v1", Resource: "resourcequotas", Kind: "ResourceQuota",
		Namespaced: true, Category: CategoryConfig, Title: "Resource Quotas", Singular: "Resource Quota"},
	{Version: "v1", Resource: "limitranges", Kind: "LimitRange",
		Namespaced: true, Category: CategoryConfig, Title: "Limit Ranges", Singular: "Limit Range"},

	// --- Network ---
	{Version: "v1", Resource: "services", Kind: "Service",
		Namespaced: true, Category: CategoryNetwork, Title: "Services", Singular: "Service"},
	{Version: "v1", Resource: "endpoints", Kind: "Endpoints",
		Namespaced: true, Category: CategoryNetwork, Title: "Endpoints", Singular: "Endpoint"},
	{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses", Kind: "Ingress",
		Namespaced: true, Category: CategoryNetwork, Title: "Ingresses", Singular: "Ingress"},
	{Group: "networking.k8s.io", Version: "v1", Resource: "ingressclasses", Kind: "IngressClass",
		Namespaced: false, Category: CategoryNetwork, Title: "Ingress Classes", Singular: "Ingress Class"},
	{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies", Kind: "NetworkPolicy",
		Namespaced: true, Category: CategoryNetwork, Title: "Network Policies", Singular: "Network Policy"},

	// --- Storage ---
	{Version: "v1", Resource: "persistentvolumeclaims", Kind: "PersistentVolumeClaim",
		Namespaced: true, Category: CategoryStorage, Title: "Persistent Volume Claims", Singular: "Claim"},
	{Version: "v1", Resource: "persistentvolumes", Kind: "PersistentVolume",
		Namespaced: false, Category: CategoryStorage, Title: "Persistent Volumes", Singular: "Volume"},
	{Group: "storage.k8s.io", Version: "v1", Resource: "storageclasses", Kind: "StorageClass",
		Namespaced: false, Category: CategoryStorage, Title: "Storage Classes", Singular: "Storage Class"},

	// --- Custom Resources ---
	// The definitions themselves, pinned above the resources they define.
	// Kept as a static entry rather than discovered, because it is the one
	// kind in this category that belongs to Kubernetes: everything else here
	// is present only because somebody installed it.
	//
	// Its Subcategory is empty, which is what sorts it above every API group
	// — see the ordering in BrowseService.Kinds.
	{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions",
		Kind: "CustomResourceDefinition", Namespaced: false, Category: CategoryCustomResources,
		Title: "Definitions", Singular: "Definition"},

	// --- Access Control ---
	{Version: "v1", Resource: "serviceaccounts", Kind: "ServiceAccount",
		Namespaced: true, Category: CategoryAccessControl, Title: "Service Accounts", Singular: "Service Account"},
	{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles", Kind: "Role",
		Namespaced: true, Category: CategoryAccessControl, Title: "Roles", Singular: "Role"},
	{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings", Kind: "RoleBinding",
		Namespaced: true, Category: CategoryAccessControl, Title: "Role Bindings", Singular: "Role Binding"},
	{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles", Kind: "ClusterRole",
		Namespaced: false, Category: CategoryAccessControl, Title: "Cluster Roles", Singular: "Cluster Role"},
	{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings", Kind: "ClusterRoleBinding",
		Namespaced: false, Category: CategoryAccessControl, Title: "Cluster Role Bindings", Singular: "Cluster Role Binding"},
}

// Catalog holds the kinds PodSteer can browse, per cluster.
//
// Built-in kinds are shared by every cluster. Custom resource definitions are
// not: two clusters routinely run different operators, so a CRD registered
// from one cluster must never appear in another's navigator. That is why the
// catalog is keyed by cluster rather than being a package-level slice.
//
// It is safe for concurrent use.
type Catalog struct {
	mu     sync.RWMutex
	custom map[ClusterID][]ResourceKind
}

// NewCatalog returns a catalog holding only the built-in kinds.
func NewCatalog() *Catalog {
	return &Catalog{custom: make(map[ClusterID][]ResourceKind)}
}

// BuiltIn returns the kinds every cluster has.
func BuiltIn() []ResourceKind {
	return append([]ResourceKind(nil), builtInKinds...)
}

// SetCustom replaces the custom resource kinds known for a cluster.
//
// Replace rather than merge: an operator uninstalled from the cluster should
// disappear from the navigator on the next discovery, and merging would leave
// its kinds behind forever.
func (c *Catalog) SetCustom(cluster ClusterID, kinds []ResourceKind) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.custom[cluster] = append([]ResourceKind(nil), kinds...)
}

// Forget drops everything known about a cluster, for use on disconnect.
func (c *Catalog) Forget(cluster ClusterID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.custom, cluster)
}

// Kinds returns every kind browsable in the given cluster: the built-ins
// followed by whatever custom resources discovery found.
func (c *Catalog) Kinds(cluster ClusterID) []ResourceKind {
	c.mu.RLock()
	defer c.mu.RUnlock()

	kinds := make([]ResourceKind, 0, len(builtInKinds)+len(c.custom[cluster]))
	kinds = append(kinds, builtInKinds...)
	kinds = append(kinds, c.custom[cluster]...)
	return kinds
}

// Lookup resolves a kind identifier within a cluster.
func (c *Catalog) Lookup(cluster ClusterID, id string) (ResourceKind, error) {
	group, version, resource, err := ParseResourceKindID(id)
	if err != nil {
		return ResourceKind{}, err
	}

	for _, kind := range c.Kinds(cluster) {
		if kind.Group == group && kind.Version == version && kind.Resource == resource {
			return kind, nil
		}
	}

	return ResourceKind{}, fmt.Errorf("%w: %q is not available in cluster %q",
		ErrInvalidResourceKind, id, cluster)
}
