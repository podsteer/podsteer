package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiversion "k8s.io/apimachinery/pkg/version"

	"k8sense/app/domain"
	"k8sense/app/ports"
)

// Adapter is the driven adapter for Kubernetes.
//
// It satisfies both Kubernetes outbound ports. They stay separate interfaces
// because they fail independently — kubeconfig discovery is local and keeps
// working when every cluster is down — but a single implementation backs them,
// since both need the same kubeconfig resolution and client cache.
type Adapter struct {
	factory *clientFactory
	logger  *slog.Logger
}

// Compile-time proof that the adapter satisfies both outbound ports.
var (
	_ ports.KubeconfigPort = (*Adapter)(nil)
	_ ports.KubernetesPort = (*Adapter)(nil)
)

// New returns a Kubernetes adapter configured by cfg.
//
// It performs no I/O: the kubeconfig is read on demand and clients are built
// on first use, so a machine with no cluster configured still starts instantly
// instead of blocking the UI at launch.
func New(cfg Config, logger *slog.Logger) *Adapter {
	if logger == nil {
		logger = slog.Default()
	}
	return &Adapter{
		factory: newClientFactory(cfg),
		logger:  logger.With(slog.String("adapter", "k8s")),
	}
}

// ServerVersion reaches the cluster's API server and reports its version.
func (a *Adapter) ServerVersion(ctx context.Context, id domain.ClusterID) (domain.ServerVersion, error) {
	const op = "querying server version"

	client, err := a.factory.clientFor(id)
	if err != nil {
		return domain.ServerVersion{}, err
	}

	restClient := client.Discovery().RESTClient()
	if restClient == nil {
		// Only a fake or stubbed clientset lands here. Fall back to the
		// context-unaware call rather than failing outright.
		info, err := client.Discovery().ServerVersion()
		if err != nil {
			return domain.ServerVersion{}, classify(op, err)
		}
		return mapServerVersion(info), nil
	}

	// /version is one of the few endpoints with no protobuf representation, so
	// the Accept header is pinned to JSON. Without this the request inherits
	// the protobuf-first negotiation set in restConfig and can be refused.
	body, err := restClient.Get().
		AbsPath("/version").
		SetHeader("Accept", "application/json").
		DoRaw(ctx)
	if err != nil {
		return domain.ServerVersion{}, classify(op, err)
	}

	var info apiversion.Info
	if err := json.Unmarshal(body, &info); err != nil {
		return domain.ServerVersion{}, fmt.Errorf("%s: decoding response: %w", op, err)
	}

	return mapServerVersion(&info), nil
}

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
			// One object the domain rejects must not blank the whole list;
			// log it and carry on. See ListPods for the same reasoning.
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

// ListPods returns the pods in namespace, or across every namespace when it is
// domain.NamespaceAll.
func (a *Adapter) ListPods(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) ([]domain.Pod, error) {
	op := fmt.Sprintf("listing pods in %q of %q", namespace, id)

	client, err := a.factory.clientFor(id)
	if err != nil {
		return nil, err
	}

	// NamespaceAll renders as the empty string, which is precisely what the
	// typed client expects for a cross-namespace list.
	list, err := client.CoreV1().Pods(namespace.String()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, classify(op, err)
	}

	pods := make([]domain.Pod, 0, len(list.Items))
	for i := range list.Items {
		pod, err := mapPod(id, &list.Items[i])
		if err != nil {
			// A single object the domain rejects is a mapping bug or an
			// unfamiliar API shape, not a reason to show the operator an empty
			// cluster. Degrade to a partial list and record why.
			a.logger.WarnContext(ctx, "skipping unmappable pod",
				slog.String("cluster", id.String()),
				slog.String("namespace", list.Items[i].Namespace),
				slog.String("name", list.Items[i].Name),
				slog.String("error", err.Error()))
			continue
		}
		pods = append(pods, pod)
	}

	return pods, nil
}

// Invalidate drops the cached client for id.
//
// Exposed beyond the two ports so that the composition root can react to a
// kubeconfig change on disk without restarting the application.
func (a *Adapter) Invalidate(id domain.ClusterID) {
	a.factory.invalidate(id)
}
