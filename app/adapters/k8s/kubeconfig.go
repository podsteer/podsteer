package k8s

import (
	"context"
	"fmt"
	"log/slog"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"k8sense/app/domain"
	"k8sense/app/ports"
)

// Clusters returns every cluster described by the local kubeconfig.
//
// The file is re-read on every call rather than cached. It is a few kilobytes,
// this runs only when the cluster picker opens, and kubeconfigs are edited
// under a running client all the time — `kubectl config use-context`, a cloud
// CLI adding a freshly provisioned cluster, a token refresh rewriting the
// file. A cache here would show the operator a stale list.
func (a *Adapter) Clusters(ctx context.Context) ([]domain.Cluster, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	raw, err := a.factory.rawConfig()
	if err != nil {
		return nil, err
	}

	clusters := make([]domain.Cluster, 0, len(raw.Contexts))
	skipped := 0
	for name, kubeContext := range raw.Contexts {
		cluster, err := a.toCluster(name, kubeContext, raw)
		if err != nil {
			// A kubeconfig accumulates dead entries — a context pointing at a
			// cluster block that was removed, an endpoint that is no longer a
			// URL. Dropping the whole file over one of them would be the worst
			// possible failure mode, so skip and keep going.
			a.logger.WarnContext(ctx, "skipping unusable kubeconfig context",
				slog.String("context", name),
				slog.String("error", err.Error()))
			skipped++
			continue
		}
		clusters = append(clusters, cluster)
	}

	// Only fail when there was something to read and none of it was usable.
	// A kubeconfig with no contexts at all is an empty list, not an error:
	// that is simply a machine which has not been pointed at a cluster yet.
	if len(clusters) == 0 && skipped > 0 {
		return nil, fmt.Errorf("reading kubeconfig: %w: all %d contexts are unusable",
			ports.ErrKubeconfigUnavailable, skipped)
	}

	return clusters, nil
}

// Cluster returns the single cluster with the given id.
func (a *Adapter) Cluster(ctx context.Context, id domain.ClusterID) (domain.Cluster, error) {
	if id.IsZero() {
		return domain.Cluster{}, domain.ErrEmptyClusterID
	}
	if err := ctx.Err(); err != nil {
		return domain.Cluster{}, err
	}

	raw, err := a.factory.rawConfig()
	if err != nil {
		return domain.Cluster{}, err
	}

	kubeContext, ok := raw.Contexts[id.String()]
	if !ok {
		return domain.Cluster{}, fmt.Errorf("kubeconfig context %q: %w", id, domain.ErrClusterNotFound)
	}

	cluster, err := a.toCluster(id.String(), kubeContext, raw)
	if err != nil {
		return domain.Cluster{}, fmt.Errorf("kubeconfig context %q: %w", id, err)
	}

	return cluster, nil
}

// toCluster translates one kubeconfig context into a domain Cluster.
//
// A context is a triple of references — into the clusters, users and (loosely)
// namespaces sections — so the endpoint has to be resolved through the cluster
// block it names rather than read off the context itself.
func (a *Adapter) toCluster(name string, kubeContext *clientcmdapi.Context, raw clientcmdapi.Config) (domain.Cluster, error) {
	if kubeContext == nil {
		return domain.Cluster{}, fmt.Errorf("context %q is empty", name)
	}

	id, err := domain.NewClusterID(name)
	if err != nil {
		return domain.Cluster{}, err
	}

	entry, ok := raw.Clusters[kubeContext.Cluster]
	if !ok || entry == nil {
		return domain.Cluster{}, fmt.Errorf("context %q references unknown cluster %q",
			name, kubeContext.Cluster)
	}

	endpoint, err := domain.NewServerEndpoint(entry.Server)
	if err != nil {
		return domain.Cluster{}, err
	}

	// An unusable namespace in the context is not worth discarding the whole
	// cluster over: fall back to "no namespace pinned" and let the operator
	// pick one in the UI.
	namespace, err := domain.NewNamespaceName(kubeContext.Namespace)
	if err != nil {
		a.logger.Warn("ignoring invalid namespace in kubeconfig context",
			slog.String("context", name),
			slog.String("namespace", kubeContext.Namespace),
			slog.String("error", err.Error()))
		namespace = domain.NamespaceAll
	}

	return domain.NewCluster(domain.ClusterSpec{
		ID:               id,
		Server:           endpoint,
		DefaultNamespace: namespace,
		AuthInfo:         kubeContext.AuthInfo,
		IsCurrent:        name == raw.CurrentContext,
	})
}
