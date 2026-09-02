package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/podsteer/podsteer/app/domain"
)

// ListNamespaces returns every namespace visible to the credentials.
//
// Cached: the assessment and the namespace list both ask on the same tick.
func (a *Adapter) ListNamespaces(ctx context.Context, id domain.ClusterID) ([]domain.Namespace, error) {
	return cachedSlice(&a.reads, ctx, readKey(id.String(), "namespaces"), func(ctx context.Context) ([]domain.Namespace, error) {
		return a.listNamespaces(ctx, id)
	})
}

// ListNamespaces returns every namespace visible to the configured credentials.
func (a *Adapter) listNamespaces(ctx context.Context, id domain.ClusterID) ([]domain.Namespace, error) {
	op := fmt.Sprintf("listing namespaces of %q", id)

	client, err := a.factory.clientFor(id)
	if err != nil {
		return nil, err
	}

	list, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{ResourceVersion: cachedResourceVersion})
	if err != nil {
		return nil, classify(op, err)
	}

	namespaces := make([]domain.Namespace, 0, len(list.Items))
	for i := range list.Items {
		namespace, err := mapNamespace(&list.Items[i])
		if err != nil {
			a.logger.WarnContext(ctx, "skipping unmappable namespace",
				slog.String("cluster", id.String()),
				slog.String("name", list.Items[i].Name),
				slog.String("error", err.Error()))
			continue
		}
		namespaces = append(namespaces, namespace)
	}

	return namespaces, nil
}

// ListNodes returns the cluster's nodes.
//
// Cached: the assessment reads them on every refresh, and the node list reads
// them again in the same instant.
func (a *Adapter) ListNodes(ctx context.Context, id domain.ClusterID) ([]domain.Node, error) {
	return cachedSlice(&a.reads, ctx, readKey(id.String(), "nodes"), func(ctx context.Context) ([]domain.Node, error) {
		return a.listNodes(ctx, id)
	})
}

// ListNodes returns the cluster's nodes.
func (a *Adapter) listNodes(ctx context.Context, id domain.ClusterID) ([]domain.Node, error) {
	op := fmt.Sprintf("listing nodes of %q", id)

	client, err := a.factory.clientFor(id)
	if err != nil {
		return nil, err
	}

	list, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{ResourceVersion: cachedResourceVersion})
	if err != nil {
		return nil, classify(op, err)
	}

	nodes := make([]domain.Node, 0, len(list.Items))
	for i := range list.Items {
		node, err := mapNode(id, &list.Items[i])
		if err != nil {
			a.logger.WarnContext(ctx, "skipping unmappable node",
				slog.String("cluster", id.String()),
				slog.String("name", list.Items[i].Name),
				slog.String("error", err.Error()))
			continue
		}
		nodes = append(nodes, node)
	}

	return nodes, nil
}

// DiscoverCustomKinds returns the kinds served by CustomResourceDefinitions.
//
// It reads the API server's own discovery document rather than listing
// CustomResourceDefinition objects, for two reasons: listing CRDs requires a
// cluster-scoped permission many read-only accounts lack, and discovery
// reports what the server *actually serves right now* — a CRD whose controller
// has not established it yet is absent, which is the correct answer.
//
// Groups belonging to Kubernetes itself are filtered out. They are either
// already in the built-in catalog or are internal machinery no operator
// browses, and including them would bury a cluster's real operators under
// forty entries nobody asked for.
func (a *Adapter) DiscoverCustomKinds(ctx context.Context, id domain.ClusterID) ([]domain.ResourceKind, error) {
	op := fmt.Sprintf("discovering custom resources of %q", id)

	set, err := a.factory.clientsFor(id)
	if err != nil {
		return nil, err
	}

	// ServerPreferredResources returns one version per group — the one the
	// server prefers — which is exactly what a browser wants. It also returns
	// partial results alongside an error when some aggregated API is down, and
	// those partial results are still worth showing.
	groups, err := set.discovery.ServerPreferredResources()
	if err != nil {
		if len(groups) == 0 {
			return nil, classify(op, err)
		}
		a.logger.WarnContext(ctx, "partial discovery; some API groups did not respond",
			slog.String("cluster", id.String()),
			slog.String("error", err.Error()))
	}

	kinds := make([]domain.ResourceKind, 0)
	seen := make(map[string]struct{})

	for _, group := range groups {
		if group == nil {
			continue
		}

		groupVersion := group.GroupVersion
		groupName, version, found := strings.Cut(groupVersion, "/")
		if !found {
			// Core group: "v1" rather than "group/version". Never custom.
			continue
		}
		if isKubernetesGroup(groupName) {
			continue
		}

		for _, resource := range group.APIResources {
			// Subresources ("deployments/status") are not browsable objects.
			if strings.Contains(resource.Name, "/") {
				continue
			}
			if !supportsList(resource.Verbs) {
				continue
			}

			kind := domain.ResourceKind{
				Group:      groupName,
				Version:    version,
				Resource:   resource.Name,
				Kind:       resource.Kind,
				Namespaced: resource.Namespaced,
				Category:   domain.CategoryCustomResources,
				// THE RAW API GROUP, and it took a curated table of project
				// names to find out why. A table naming "Argo" for
				// argoproj.io and "cert-manager" for cert-manager.io covered
				// five groups of the twenty-five on a real cluster, which
				// produced a navigator speaking two vocabularies at once —
				// five friendly names among twenty raw ones, with no way for
				// a reader to tell which kind of thing a heading was.
				//
				// The group is the only label that can never be wrong, never
				// needs a maintainer, and can be grepped straight out of
				// `kubectl api-resources`. Its cost is real and smaller: a
				// project publishing eleven groups gets eleven headings.
				Subcategory: groupName,
				Title:       resource.Kind,
				Singular:    resource.Kind,
			}
			if _, duplicate := seen[kind.ID()]; duplicate {
				continue
			}
			seen[kind.ID()] = struct{}{}
			kinds = append(kinds, kind)
		}
	}

	return kinds, nil
}

// kubernetesGroupSuffixes marks API groups that belong to Kubernetes itself or
// to its ecosystem's own machinery, rather than to something an operator
// installed to run their workloads.
var kubernetesGroupSuffixes = []string{
	"k8s.io",
	"kubernetes.io",
}

// builtInGroups are Kubernetes' own API groups that carry no `.k8s.io`
// suffix, and so slip past the rule below.
//
// A REAL DUPLICATION, AND IT WAS ON SCREEN. `apps`, `batch`, `autoscaling`
// and `policy` are as much a part of Kubernetes as anything ending in
// k8s.io, but the suffix rule never matched them — so every Deployment,
// StatefulSet, Job, CronJob, HorizontalPodAutoscaler and
// PodDisruptionBudget was discovered a second time and listed again under
// Custom Resources, beside the catalog entry that already had it. Grouping
// custom resources by publisher is what finally made it visible: the
// duplicates gathered under headings reading "apps" and "batch".
//
// The core group is excluded above, by having no "group/version" to split.
var builtInGroups = map[string]bool{
	"apps":        true,
	"batch":       true,
	"autoscaling": true,
	"policy":      true,
	"extensions":  true,
}

// adoptedGroups are Kubernetes-owned groups that are nonetheless installed by
// an operator and are worth browsing.
//
// THE SUFFIX RULE WAS TOO BROAD AND HID REAL THINGS. Gateway API is the
// declared successor to Ingress and ships as CRDs under a k8s.io group;
// VolumeSnapshots are how anybody takes a backup of a PVC and ship the same
// way. Both were being filtered out as "part of Kubernetes" — which is true
// of their names and false of their availability: a cluster only has them
// because somebody installed them.
var adoptedGroups = map[string]bool{
	"gateway.networking.k8s.io": true,
	"snapshot.storage.k8s.io":   true,
	// VerticalPodAutoscaler, which is as widely installed as anything on this
	// list and was invisible in the navigator on every cluster running it.
	"autoscaling.k8s.io": true,
	// VolumeGroupSnapshot, shipped by the same external-snapshotter whose
	// sibling group is already here. Showing one and hiding the other is
	// incoherent on a CSI cluster.
	"groupsnapshot.storage.k8s.io": true,
	// AdminNetworkPolicy, installed by a CNI rather than by Kubernetes.
	"policy.networking.k8s.io": true,
}

// Worth knowing before adding to the list above: `x-k8s.io` groups — Cluster
// API's cluster.x-k8s.io, Kueue, JobSet — do NOT need an entry. The suffix
// rule matches ".k8s.io", and the hyphen means they never did match.

// isKubernetesGroup reports whether a group is part of Kubernetes rather than
// a custom resource worth listing.
func isKubernetesGroup(group string) bool {
	if adoptedGroups[group] {
		return false
	}
	if builtInGroups[group] {
		return true
	}
	for _, suffix := range kubernetesGroupSuffixes {
		if group == suffix || strings.HasSuffix(group, "."+suffix) {
			return true
		}
	}
	return false
}

// supportsList reports whether a resource can be listed at all. A resource
// that cannot — SubjectAccessReview, TokenReview and friends exist only to be
// created — must not appear in a browser.
func supportsList(verbs metav1.Verbs) bool {
	return slices.Contains(verbs, "list")
}

// ListPersistentVolumes returns the cluster's provisioned volumes.
func (a *Adapter) ListPersistentVolumes(ctx context.Context, id domain.ClusterID) ([]domain.PersistentVolume, error) {
	op := fmt.Sprintf("listing persistent volumes of %q", id)

	client, err := a.factory.clientFor(id)
	if err != nil {
		return nil, err
	}

	list, err := client.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{ResourceVersion: cachedResourceVersion})
	if err != nil {
		return nil, classify(op, err)
	}

	volumes := make([]domain.PersistentVolume, 0, len(list.Items))
	for i := range list.Items {
		volume, err := mapPersistentVolume(id, &list.Items[i])
		if err != nil {
			a.logger.WarnContext(ctx, "skipping unmappable persistent volume",
				slog.String("cluster", id.String()),
				slog.String("name", list.Items[i].Name),
				slog.String("error", err.Error()))
			continue
		}
		volumes = append(volumes, volume)
	}
	return volumes, nil
}

// ListPersistentVolumeClaims returns the claims made against them.
func (a *Adapter) ListPersistentVolumeClaims(
	ctx context.Context,
	id domain.ClusterID,
	namespace domain.NamespaceName,
) ([]domain.PersistentVolumeClaim, error) {
	op := fmt.Sprintf("listing persistent volume claims in %q of %q", namespace, id)

	client, err := a.factory.clientFor(id)
	if err != nil {
		return nil, err
	}

	list, err := client.CoreV1().PersistentVolumeClaims(namespace.String()).
		List(ctx, metav1.ListOptions{ResourceVersion: cachedResourceVersion})
	if err != nil {
		return nil, classify(op, err)
	}

	claims := make([]domain.PersistentVolumeClaim, 0, len(list.Items))
	for i := range list.Items {
		claim, err := mapPersistentVolumeClaim(id, &list.Items[i])
		if err != nil {
			a.logger.WarnContext(ctx, "skipping unmappable persistent volume claim",
				slog.String("cluster", id.String()),
				slog.String("name", list.Items[i].Name),
				slog.String("error", err.Error()))
			continue
		}
		claims = append(claims, claim)
	}
	return claims, nil
}
