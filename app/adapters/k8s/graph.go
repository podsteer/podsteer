package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/podsteer/podsteer/app/domain"
)

// PodGraphSources reads everything the dependency map is drawn from.
//
// FIVE READS, CONCURRENTLY, AND EACH MAY FAIL ON ITS OWN. The map is worth
// drawing without its ingress tier — plenty of accounts can list pods and not
// ingresses — so a refusal on one source names itself and the rest carries on.
// The alternative is an all-or-nothing map that shows nothing to the people
// most likely to be looking at somebody else's namespace.
//
// It gathers rather than assembles: which service selects which pod is a rule,
// and rules live in the domain where they are tested. This returns what was
// read and lets NewPodGraph decide what connects.
func (a *Adapter) PodGraphSources(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName string) (domain.GraphInput, error) {
	set, err := a.factory.clientsFor(id)
	if err != nil {
		return domain.GraphInput{}, err
	}
	client := set.typed

	raw, err := client.CoreV1().Pods(namespace.String()).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		// The pod is the one source with no partial answer: without it there
		// is nothing to draw a map around.
		return domain.GraphInput{}, classify(fmt.Sprintf("reading pod %q", podName), err)
	}

	pod, err := mapPod(id, raw)
	if err != nil {
		return domain.GraphInput{}, fmt.Errorf("mapping pod %q: %w", podName, err)
	}

	input := domain.GraphInput{
		Pod:      pod,
		Owner:    ownerChain(ctx, client, namespace.String(), raw.OwnerReferences, a.logger),
		Attached: attachedRefs(raw),
	}

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)

	degrade := func(source string, err error) {
		mu.Lock()
		defer mu.Unlock()
		input.Unreadable = append(input.Unreadable, source)
		a.logger.DebugContext(ctx, "graph source unavailable",
			slog.String("source", source), slog.String("error", err.Error()))
	}

	wg.Go(func() {
		list, err := client.CoreV1().Services(namespace.String()).
			List(ctx, metav1.ListOptions{ResourceVersion: cachedResourceVersion})
		if err != nil {
			degrade("services", err)
			return
		}

		refs := make([]domain.ServiceRef, 0, len(list.Items))
		for i := range list.Items {
			refs = append(refs, serviceRef(&list.Items[i]))
		}

		mu.Lock()
		input.Services = refs
		mu.Unlock()
	})

	wg.Go(func() {
		list, err := client.NetworkingV1().Ingresses(namespace.String()).
			List(ctx, metav1.ListOptions{ResourceVersion: cachedResourceVersion})
		if err != nil {
			degrade("ingresses", err)
			return
		}

		refs := make([]domain.IngressRef, 0, len(list.Items))
		for i := range list.Items {
			item := &list.Items[i]
			ref := domain.IngressRef{Name: item.Name, Namespace: item.Namespace}

			// The default backend counts: an ingress with no rules still
			// routes everything to it.
			if item.Spec.DefaultBackend != nil && item.Spec.DefaultBackend.Service != nil {
				ref.Backends = append(ref.Backends, item.Spec.DefaultBackend.Service.Name)
			}
			for _, rule := range item.Spec.Rules {
				if rule.Host != "" {
					ref.Hosts = append(ref.Hosts, rule.Host)
				}
				if rule.HTTP == nil {
					continue
				}
				for _, path := range rule.HTTP.Paths {
					if path.Backend.Service != nil {
						ref.Backends = append(ref.Backends, path.Backend.Service.Name)
					}
				}
			}
			refs = append(refs, ref)
		}

		mu.Lock()
		input.Ingresses = refs
		mu.Unlock()
	})

	wg.Wait()
	return input, nil
}

// serviceRef reduces a Service to what the map needs.
func serviceRef(service *corev1.Service) domain.ServiceRef {
	ref := domain.ServiceRef{
		Name:      service.Name,
		Namespace: service.Namespace,
		Selector:  service.Spec.Selector,
		Type:      string(service.Spec.Type),
	}

	for _, port := range service.Spec.Ports {
		text := strconv.Itoa(int(port.Port))
		if port.Name != "" {
			text = port.Name + ":" + text
		}
		ref.Ports = append(ref.Ports, text)
	}
	return ref
}

// ownerChain walks upward from a pod's owners to the workload above them.
//
// ONE HOP, NOT A FULL WALK. A pod's controller is a ReplicaSet, and the
// ReplicaSet's is the Deployment; beyond that nothing in Kubernetes owns a
// Deployment, so a general walk would spend reads discovering that. A failure
// to read the ReplicaSet leaves the chain short rather than failing the map —
// the pod and its ReplicaSet are still worth drawing.
func ownerChain(ctx context.Context, client kubernetes.Interface, namespace string, owners []metav1.OwnerReference, logger *slog.Logger) []domain.OwnerReference {
	controller := controllerOf(owners)
	if controller == nil {
		return nil
	}

	chain := []domain.OwnerReference{{Kind: controller.Kind, Name: controller.Name, Controller: true}}
	if controller.Kind != "ReplicaSet" {
		return chain
	}

	replicaSet, err := client.AppsV1().ReplicaSets(namespace).Get(ctx, controller.Name, metav1.GetOptions{})
	if err != nil {
		logger.DebugContext(ctx, "graph owner chain stopped at the replicaset",
			slog.String("name", controller.Name), slog.String("error", err.Error()))
		return chain
	}

	if above := controllerOf(replicaSet.OwnerReferences); above != nil {
		chain = append(chain, domain.OwnerReference{Kind: above.Kind, Name: above.Name, Controller: true})
	}
	return chain
}

// controllerOf returns the owning controller, or nil.
func controllerOf(owners []metav1.OwnerReference) *metav1.OwnerReference {
	for i := range owners {
		if owners[i].Controller != nil && *owners[i].Controller {
			return &owners[i]
		}
	}
	return nil
}

// attachedRefs lists what the pod consumes.
//
// VOLUMES AND ENVIRONMENT BOTH, because a ConfigMap read through envFrom is
// every bit as much a dependency as one mounted at a path — and it is the one
// people forget, since nothing in the pod's volume list mentions it.
func attachedRefs(pod *corev1.Pod) []domain.AttachedRef {
	var refs []domain.AttachedRef

	for _, volume := range pod.Spec.Volumes {
		switch {
		case volume.ConfigMap != nil:
			refs = append(refs, domain.AttachedRef{Kind: domain.GraphConfig, Name: volume.ConfigMap.Name, Via: mountPath(pod, volume.Name)})
		case volume.Secret != nil:
			refs = append(refs, domain.AttachedRef{Kind: domain.GraphSecret, Name: volume.Secret.SecretName, Via: mountPath(pod, volume.Name)})
		case volume.PersistentVolumeClaim != nil:
			refs = append(refs, domain.AttachedRef{Kind: domain.GraphClaim, Name: volume.PersistentVolumeClaim.ClaimName, Via: mountPath(pod, volume.Name)})
		}
	}

	for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
		for _, source := range container.EnvFrom {
			if source.ConfigMapRef != nil {
				refs = append(refs, domain.AttachedRef{Kind: domain.GraphConfig, Name: source.ConfigMapRef.Name, Via: "environment"})
			}
			if source.SecretRef != nil {
				refs = append(refs, domain.AttachedRef{Kind: domain.GraphSecret, Name: source.SecretRef.Name, Via: "environment"})
			}
		}
		for _, env := range container.Env {
			if env.ValueFrom == nil {
				continue
			}
			if ref := env.ValueFrom.ConfigMapKeyRef; ref != nil {
				refs = append(refs, domain.AttachedRef{Kind: domain.GraphConfig, Name: ref.Name, Via: "environment"})
			}
			if ref := env.ValueFrom.SecretKeyRef; ref != nil {
				refs = append(refs, domain.AttachedRef{Kind: domain.GraphSecret, Name: ref.Name, Via: "environment"})
			}
		}
	}

	// The service account is a dependency the pod did not ask for in its
	// volumes and cannot run without.
	if name := pod.Spec.ServiceAccountName; name != "" && name != "default" {
		refs = append(refs, domain.AttachedRef{Kind: domain.GraphServiceAccount, Name: name, Via: "runs as"})
	}
	return refs
}

// mountPath finds where a volume is mounted, for the edge label.
func mountPath(pod *corev1.Pod, volume string) string {
	for _, container := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
		for _, mount := range container.VolumeMounts {
			if mount.Name == volume {
				return mount.MountPath
			}
		}
	}
	// Declared and mounted nowhere. Worth drawing — an unused volume is a
	// finding of its own — but not worth claiming a path for.
	return "not mounted"
}
