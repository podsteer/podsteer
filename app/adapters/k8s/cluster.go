package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"podsteer/app/domain"
)

// ListNamespaces returns every namespace visible to the configured credentials.
func (a *Adapter) ListNamespaces(ctx context.Context, id domain.ClusterID) ([]domain.Namespace, error) {
	op := fmt.Sprintf("listing namespaces of %q", id)

	client, err := a.factory.clientFor(id)
	if err != nil {
		return nil, err
	}

	list, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
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
func (a *Adapter) ListNodes(ctx context.Context, id domain.ClusterID) ([]domain.Node, error) {
	op := fmt.Sprintf("listing nodes of %q", id)

	client, err := a.factory.clientFor(id)
	if err != nil {
		return nil, err
	}

	list, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
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
				Title:      resource.Kind,
				Singular:   resource.Kind,
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

// isKubernetesGroup reports whether a group is part of Kubernetes rather than
// a custom resource worth listing.
func isKubernetesGroup(group string) bool {
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
	for _, verb := range verbs {
		if verb == "list" {
			return true
		}
	}
	return false
}
