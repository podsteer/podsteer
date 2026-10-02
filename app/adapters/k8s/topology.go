package k8s

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// Compile-time proof the adapter reads topologies.
var _ ports.TopologyPort = (*Adapter)(nil)

// gatewayGroup is the Gateway API's group, read only when discovery serves it.
const gatewayGroup = "gateway.networking.k8s.io"

// gatewayVersions are tried in this order for each resource: the first that
// serves it is the one read. TCPRoute and TLSRoute are still alpha.
var gatewayVersions = []string{"v1", "v1beta1", "v1alpha3", "v1alpha2"}

// gatewayKinds are the Gateway API resources the topology draws, by plural.
var gatewayKinds = map[string]string{
	"gateways":   "Gateway",
	"httproutes": "HTTPRoute",
	"grpcroutes": "GRPCRoute",
	"tcproutes":  "TCPRoute",
	"tlsroutes":  "TLSRoute",
}

// TopologySources reads what one scope's topology is drawn from.
//
// ABOUT A DOZEN READS, CONCURRENTLY, AND NEVER A FAILED RESULT ONCE THE CLIENT
// EXISTS: each refusal names itself in Unreadable and the rest carries on.
// The controllers are listed as FULL objects, because their pod templates are
// where the ConfigMap, Secret and claim names come from — ReplicaSets and
// Jobs included, since the watch store strips their templates to images and
// a pod's names come from the template of the controller that made it. Pods
// are the cached ListPods, so this coalesces with the poll.
//
// SECRETS ARE NEVER LISTED. The names a template gives are all the topology
// knows of them; reading the list would be the render-time Secret read the
// Secrets doctrine exists to forbid. TestTopologySourcesNeverListsSecrets
// holds that.
//
// Nothing here runs on a tick: it is called when the page opens, on an
// explicit refresh, and on a Live redraw the operator switched on.
func (a *Adapter) TopologySources(ctx context.Context, id domain.ClusterID, scope domain.TopologyScope) (domain.TopologyInput, error) {
	set, err := a.factory.clientsFor(id)
	if err != nil {
		return domain.TopologyInput{}, err
	}
	client := set.typed

	in := domain.TopologyInput{Scope: scope, ReadAt: time.Now()}

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	degrade := func(source string, err error) {
		mu.Lock()
		in.Unreadable = append(in.Unreadable, source)
		mu.Unlock()
		a.logger.DebugContext(ctx, "topology source unavailable",
			slog.String("source", source), slog.String("error", err.Error()))
	}
	controllers := func(found []domain.TopologyController) {
		mu.Lock()
		in.Controllers = append(in.Controllers, found...)
		mu.Unlock()
	}
	opts := metav1.ListOptions{ResourceVersion: cachedResourceVersion}

	wg.Go(func() {
		controllers(scopedRead(ctx, scope, "deployments", degrade, func(ctx context.Context, ns string) ([]domain.TopologyController, error) {
			list, err := client.AppsV1().Deployments(ns).List(ctx, opts)
			if err != nil {
				return nil, err
			}
			out := make([]domain.TopologyController, 0, len(list.Items))
			for i := range list.Items {
				d := &list.Items[i]
				out = append(out, domain.TopologyController{
					Kind: "Deployment", Name: d.Name, Namespace: domain.NamespaceName(d.Namespace), Labels: d.Labels,
					Owner: ownerOfObject(d.OwnerReferences), Desired: replicasOrOne(d.Spec.Replicas), Ready: d.Status.ReadyReplicas,
					Attached: attachedFromSpec(&d.Spec.Template.Spec),
				})
			}
			return out, nil
		}))
	})
	wg.Go(func() {
		controllers(scopedRead(ctx, scope, "statefulsets", degrade, func(ctx context.Context, ns string) ([]domain.TopologyController, error) {
			list, err := client.AppsV1().StatefulSets(ns).List(ctx, opts)
			if err != nil {
				return nil, err
			}
			out := make([]domain.TopologyController, 0, len(list.Items))
			for i := range list.Items {
				s := &list.Items[i]
				var claims []string
				for _, claim := range s.Spec.VolumeClaimTemplates {
					claims = append(claims, claim.Name)
				}
				out = append(out, domain.TopologyController{
					Kind: "StatefulSet", Name: s.Name, Namespace: domain.NamespaceName(s.Namespace), Labels: s.Labels,
					Owner: ownerOfObject(s.OwnerReferences), Desired: replicasOrOne(s.Spec.Replicas), Ready: s.Status.ReadyReplicas,
					Attached: attachedFromSpec(&s.Spec.Template.Spec), ClaimTemplates: claims,
				})
			}
			return out, nil
		}))
	})
	wg.Go(func() {
		controllers(scopedRead(ctx, scope, "daemonsets", degrade, func(ctx context.Context, ns string) ([]domain.TopologyController, error) {
			list, err := client.AppsV1().DaemonSets(ns).List(ctx, opts)
			if err != nil {
				return nil, err
			}
			out := make([]domain.TopologyController, 0, len(list.Items))
			for i := range list.Items {
				d := &list.Items[i]
				out = append(out, domain.TopologyController{
					Kind: "DaemonSet", Name: d.Name, Namespace: domain.NamespaceName(d.Namespace), Labels: d.Labels,
					Owner: ownerOfObject(d.OwnerReferences), Desired: d.Status.DesiredNumberScheduled, Ready: d.Status.NumberReady,
					Attached: attachedFromSpec(&d.Spec.Template.Spec),
				})
			}
			return out, nil
		}))
	})
	wg.Go(func() {
		controllers(scopedRead(ctx, scope, "cronjobs", degrade, func(ctx context.Context, ns string) ([]domain.TopologyController, error) {
			list, err := client.BatchV1().CronJobs(ns).List(ctx, opts)
			if err != nil {
				return nil, err
			}
			out := make([]domain.TopologyController, 0, len(list.Items))
			for i := range list.Items {
				c := &list.Items[i]
				out = append(out, domain.TopologyController{
					Kind: "CronJob", Name: c.Name, Namespace: domain.NamespaceName(c.Namespace), Labels: c.Labels,
					Owner: ownerOfObject(c.OwnerReferences), Suspended: c.Spec.Suspend != nil && *c.Spec.Suspend,
					Attached: attachedFromSpec(&c.Spec.JobTemplate.Spec.Template.Spec),
				})
			}
			return out, nil
		}))
	})
	wg.Go(func() {
		controllers(scopedRead(ctx, scope, "replicasets", degrade, func(ctx context.Context, ns string) ([]domain.TopologyController, error) {
			list, err := client.AppsV1().ReplicaSets(ns).List(ctx, opts)
			if err != nil {
				return nil, err
			}
			out := make([]domain.TopologyController, 0, len(list.Items))
			for i := range list.Items {
				r := &list.Items[i]
				out = append(out, domain.TopologyController{
					Kind: "ReplicaSet", Name: r.Name, Namespace: domain.NamespaceName(r.Namespace), Labels: r.Labels,
					Owner: ownerOfObject(r.OwnerReferences), Desired: replicasOrOne(r.Spec.Replicas), Ready: r.Status.ReadyReplicas,
					Attached: attachedFromSpec(&r.Spec.Template.Spec),
				})
			}
			return out, nil
		}))
	})
	wg.Go(func() {
		controllers(scopedRead(ctx, scope, "jobs", degrade, func(ctx context.Context, ns string) ([]domain.TopologyController, error) {
			list, err := client.BatchV1().Jobs(ns).List(ctx, opts)
			if err != nil {
				return nil, err
			}
			out := make([]domain.TopologyController, 0, len(list.Items))
			for i := range list.Items {
				j := &list.Items[i]
				desired := int32(1)
				if j.Spec.Completions != nil {
					desired = *j.Spec.Completions
				}
				out = append(out, domain.TopologyController{
					Kind: "Job", Name: j.Name, Namespace: domain.NamespaceName(j.Namespace), Labels: j.Labels,
					Owner: ownerOfObject(j.OwnerReferences), Desired: desired, Ready: j.Status.Succeeded, Failed: j.Status.Failed,
					Attached: attachedFromSpec(&j.Spec.Template.Spec),
				})
			}
			return out, nil
		}))
	})

	wg.Go(func() {
		pods := scopedRead(ctx, scope, "pods", degrade, func(ctx context.Context, ns string) ([]domain.Pod, error) {
			return a.ListPods(ctx, id, domain.NamespaceName(ns), domain.Projection{})
		})
		mu.Lock()
		in.Pods = pods
		mu.Unlock()
	})

	wg.Go(func() {
		refs := scopedRead(ctx, scope, "services", degrade, func(ctx context.Context, ns string) ([]domain.ServiceRef, error) {
			list, err := client.CoreV1().Services(ns).List(ctx, opts)
			if err != nil {
				return nil, err
			}
			out := make([]domain.ServiceRef, 0, len(list.Items))
			for i := range list.Items {
				out = append(out, serviceRef(&list.Items[i]))
			}
			return out, nil
		})
		mu.Lock()
		in.Services = refs
		mu.Unlock()
	})

	wg.Go(func() {
		refs := scopedRead(ctx, scope, "ingresses", degrade, func(ctx context.Context, ns string) ([]domain.IngressRef, error) {
			return ingressRefs(ctx, client, ns)
		})
		mu.Lock()
		in.Ingresses = refs
		mu.Unlock()
	})

	wg.Go(func() {
		refs := scopedRead(ctx, scope, "horizontalpodautoscalers", degrade, func(ctx context.Context, ns string) ([]domain.ScalerRef, error) {
			list, err := client.AutoscalingV2().HorizontalPodAutoscalers(ns).List(ctx, opts)
			if err != nil {
				return nil, err
			}
			out := make([]domain.ScalerRef, 0, len(list.Items))
			for i := range list.Items {
				out = append(out, scalerRef(&list.Items[i]))
			}
			return out, nil
		})
		mu.Lock()
		in.Scalers = refs
		mu.Unlock()
	})

	wg.Go(func() {
		refs := scopedRead(ctx, scope, "poddisruptionbudgets", degrade, func(ctx context.Context, ns string) ([]domain.BudgetRef, error) {
			list, err := client.PolicyV1().PodDisruptionBudgets(ns).List(ctx, opts)
			if err != nil {
				return nil, err
			}
			out := make([]domain.BudgetRef, 0, len(list.Items))
			for i := range list.Items {
				out = append(out, budgetRef(&list.Items[i]))
			}
			return out, nil
		})
		mu.Lock()
		in.Budgets = refs
		mu.Unlock()
	})

	wg.Go(func() {
		refs := scopedRead(ctx, scope, "networkpolicies", degrade, func(ctx context.Context, ns string) ([]domain.PolicyRef, error) {
			list, err := client.NetworkingV1().NetworkPolicies(ns).List(ctx, opts)
			if err != nil {
				return nil, err
			}
			out := make([]domain.PolicyRef, 0, len(list.Items))
			for i := range list.Items {
				out = append(out, policyRef(&list.Items[i]))
			}
			return out, nil
		})
		mu.Lock()
		in.Policies = refs
		mu.Unlock()
	})

	wg.Go(func() {
		failed := false
		refs := scopedRead(ctx, scope, "serviceaccounts", func(source string, err error) {
			failed = true
			degrade(source, err)
		}, func(ctx context.Context, ns string) ([]domain.ServiceAccountRef, error) {
			list, err := client.CoreV1().ServiceAccounts(ns).List(ctx, opts)
			if err != nil {
				return nil, err
			}
			out := make([]domain.ServiceAccountRef, 0, len(list.Items))
			for i := range list.Items {
				out = append(out, domain.ServiceAccountRef{Namespace: domain.NamespaceName(list.Items[i].Namespace), Name: list.Items[i].Name})
			}
			return out, nil
		})
		mu.Lock()
		in.ServiceAccounts = refs
		in.ServiceAccountsListed = !failed
		mu.Unlock()
	})

	wg.Go(func() {
		gateways, routes, served := a.gatewaySources(ctx, set, scope, degrade)
		mu.Lock()
		in.Gateways, in.Routes, in.GatewayAPIServed = gateways, routes, served
		mu.Unlock()
	})

	wg.Wait()
	return in, nil
}

// scopedRead reads one source over a scope: one cluster-wide list when the
// scope is All or wide, otherwise one list per namespace.
//
// A cluster-wide list REFUSED for a named scope falls back to per-namespace
// lists, because an account bound to five namespaces cannot list across the
// cluster and may list every one of the five. For an All scope the refusal is
// the answer. Each refusal is named — the source, or "<source> in <ns>".
func scopedRead[T any](
	ctx context.Context,
	scope domain.TopologyScope,
	source string,
	degrade func(string, error),
	list func(ctx context.Context, namespace string) ([]T, error),
) []T {
	if scope.ListsClusterWide() {
		items, err := list(ctx, metav1.NamespaceAll)
		if err == nil {
			return items
		}
		if scope.All || !isForbidden(err) {
			degrade(source, err)
			return nil
		}
	}

	var out []T
	for _, ns := range scope.Namespaces {
		items, err := list(ctx, ns.String())
		if err != nil {
			degrade(domain.TopologySourceIn(source, ns), err)
			continue
		}
		out = append(out, items...)
	}
	return out
}

// isForbidden reports a 403, raw or classified.
func isForbidden(err error) bool {
	return apierrors.IsForbidden(err) || errors.Is(err, ports.ErrForbidden)
}

func scalerRef(h *autoscalingv2.HorizontalPodAutoscaler) domain.ScalerRef {
	minimum := int32(1)
	if h.Spec.MinReplicas != nil {
		minimum = *h.Spec.MinReplicas
	}
	return domain.ScalerRef{
		Name: h.Name, Namespace: domain.NamespaceName(h.Namespace), Labels: h.Labels,
		Target:  domain.ObjectKey{Kind: h.Spec.ScaleTargetRef.Kind, Namespace: domain.NamespaceName(h.Namespace), Name: h.Spec.ScaleTargetRef.Name},
		Current: h.Status.CurrentReplicas, Min: minimum, Max: h.Spec.MaxReplicas,
	}
}

func budgetRef(p *policyv1.PodDisruptionBudget) domain.BudgetRef {
	return domain.BudgetRef{
		Name: p.Name, Namespace: domain.NamespaceName(p.Namespace), Labels: p.Labels,
		Selector:           labelSelector(p.Spec.Selector),
		DisruptionsAllowed: p.Status.DisruptionsAllowed,
		DesiredHealthy:     p.Status.DesiredHealthy,
		CurrentHealthy:     p.Status.CurrentHealthy,
	}
}

func policyRef(p *networkingv1.NetworkPolicy) domain.PolicyRef {
	ref := domain.PolicyRef{
		Name: p.Name, Namespace: domain.NamespaceName(p.Namespace), Labels: p.Labels,
		PodSelector: *labelSelector(&p.Spec.PodSelector),
	}
	for _, t := range p.Spec.PolicyTypes {
		ref.PolicyTypes = append(ref.PolicyTypes, string(t))
	}
	return ref
}

// labelSelector converts a selector; nil stays nil, which a budget reads as
// "selects nothing".
func labelSelector(s *metav1.LabelSelector) *domain.LabelSelector {
	if s == nil {
		return nil
	}
	out := &domain.LabelSelector{MatchLabels: s.MatchLabels}
	for _, req := range s.MatchExpressions {
		out.MatchExpressions = append(out.MatchExpressions, domain.SelectorRequirement{
			Key: req.Key, Operator: string(req.Operator), Values: req.Values,
		})
	}
	return out
}

// gatewaySources reads Gateways and routes, only when discovery serves the
// Gateway API. A cluster without it is the common case and is not a refusal:
// it says nothing in Unreadable.
func (a *Adapter) gatewaySources(ctx context.Context, set *clients, scope domain.TopologyScope, degrade func(string, error)) ([]domain.GatewayRef, []domain.RouteRef, bool) {
	if set.discovery == nil || set.dynamic == nil {
		return nil, nil, false
	}

	// plural -> the first version that serves it.
	served := make(map[string]string)
	for _, version := range gatewayVersions {
		list, err := set.discovery.ServerResourcesForGroupVersion(gatewayGroup + "/" + version)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				degrade("gateway api discovery", err)
				return nil, nil, false
			}
			continue
		}
		for _, resource := range list.APIResources {
			if _, wanted := gatewayKinds[resource.Name]; wanted && served[resource.Name] == "" {
				served[resource.Name] = version
			}
		}
	}
	if len(served) == 0 {
		return nil, nil, false
	}

	var (
		gateways []domain.GatewayRef
		routes   []domain.RouteRef
	)
	for plural, version := range served {
		gvr := schema.GroupVersionResource{Group: gatewayGroup, Version: version, Resource: plural}
		kind := gatewayKinds[plural]
		items := scopedRead(ctx, scope, plural, degrade, func(ctx context.Context, ns string) ([]unstructured.Unstructured, error) {
			list, err := set.dynamic.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{ResourceVersion: cachedResourceVersion})
			if err != nil {
				return nil, err
			}
			return list.Items, nil
		})
		for i := range items {
			if kind == "Gateway" {
				gateways = append(gateways, gatewayRef(&items[i]))
			} else {
				routes = append(routes, routeRef(&items[i], kind))
			}
		}
	}
	return gateways, routes, true
}

func gatewayRef(object *unstructured.Unstructured) domain.GatewayRef {
	class, _, _ := unstructured.NestedString(object.Object, "spec", "gatewayClassName")
	listeners, _, _ := unstructured.NestedSlice(object.Object, "spec", "listeners")
	return domain.GatewayRef{
		Name: object.GetName(), Namespace: domain.NamespaceName(object.GetNamespace()), Labels: object.GetLabels(),
		ClassName: class, Listeners: len(listeners),
	}
}

// routeRef reads a route's parentRefs and backendRefs, with the defaults the
// Gateway API defines: a parent is a Gateway and a backend a Service unless
// they say otherwise, and both are in the route's own namespace.
func routeRef(object *unstructured.Unstructured, kind string) domain.RouteRef {
	ns := domain.NamespaceName(object.GetNamespace())
	ref := domain.RouteRef{Kind: kind, Name: object.GetName(), Namespace: ns, Labels: object.GetLabels()}

	ref.Hostnames, _, _ = unstructured.NestedStringSlice(object.Object, "spec", "hostnames")

	parents, _, _ := unstructured.NestedSlice(object.Object, "spec", "parentRefs")
	for _, raw := range parents {
		if key, ok := objectKeyOf(raw, "Gateway", ns); ok {
			ref.Parents = append(ref.Parents, key)
		}
	}

	rules, _, _ := unstructured.NestedSlice(object.Object, "spec", "rules")
	for _, rule := range rules {
		fields, ok := rule.(map[string]any)
		if !ok {
			continue
		}
		backends, _, _ := unstructured.NestedSlice(fields, "backendRefs")
		for _, raw := range backends {
			if key, ok := objectKeyOf(raw, "Service", ns); ok {
				ref.Backends = append(ref.Backends, key)
			}
		}
	}
	return ref
}

// objectKeyOf reads a parentRef or backendRef.
func objectKeyOf(raw any, defaultKind string, defaultNamespace domain.NamespaceName) (domain.ObjectKey, bool) {
	fields, ok := raw.(map[string]any)
	if !ok {
		return domain.ObjectKey{}, false
	}
	name, _, _ := unstructured.NestedString(fields, "name")
	if name == "" {
		return domain.ObjectKey{}, false
	}
	kind, _, _ := unstructured.NestedString(fields, "kind")
	if kind == "" {
		kind = defaultKind
	}
	namespace, _, _ := unstructured.NestedString(fields, "namespace")
	ns := defaultNamespace
	if namespace != "" {
		ns = domain.NamespaceName(namespace)
	}
	return domain.ObjectKey{Kind: kind, Namespace: ns, Name: name}, true
}

// changeNotifier hands "something changed" to whatever sink is set.
//
// THE SINK IS SET AFTER THE ADAPTER IS BUILT — the composition root builds the
// adapter before the use case that coalesces changes — so it is an atomic
// pointer rather than a constructor argument, and a nil notifier or a nil sink
// is a no-op. The watch stores and every write call it; it never blocks
// beyond what the sink does, and ports.ChangeSink forbids the sink blocking.
type changeNotifier struct {
	sink atomic.Pointer[ports.ChangeSink]
}

func (n *changeNotifier) set(sink ports.ChangeSink) {
	if sink == nil {
		n.sink.Store(nil)
		return
	}
	n.sink.Store(&sink)
}

func (n *changeNotifier) changed(id domain.ClusterID, namespace domain.NamespaceName) {
	if n == nil {
		return
	}
	if sink := n.sink.Load(); sink != nil {
		(*sink).Changed(id, namespace)
	}
}

// SetChangeSink names who hears that a cluster changed: the watch stores'
// events and every write PodSteer makes. Not part of a port; the composition
// root calls it once, before anything is read.
func (a *Adapter) SetChangeSink(sink ports.ChangeSink) {
	a.changes.set(sink)
}
