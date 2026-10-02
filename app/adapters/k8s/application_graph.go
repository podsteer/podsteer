package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/podsteer/podsteer/app/domain"
)

// ApplicationGraphSources reads what one application's map is drawn from.
//
// NINE READS, CONCURRENTLY, AND NEVER A FAILED RESULT ONCE THE CLIENT EXISTS.
// Each failed read names itself in Unreadable and the rest carries on, the
// rule WorkloadGraphSources follows — here with no read exempt, because with no
// single subject there is no read without which there is "a box and nothing
// under it".
//
// Four kinds are listed by the instance label (Deployments, StatefulSets,
// DaemonSets, CronJobs), as full objects so the pod template's attachments
// cost no extra GET. ReplicaSets and Jobs are listed namespace-wide, because
// the ones an application owns are routinely unlabelled — a Deployment's
// ReplicaSet inherits the pod template's labels, a CronJob's Jobs the
// jobTemplate's — and only the domain, holding the owner references, can say
// which are its own. The pods are the cached ListPods the inventory counts
// from, so this read coalesces with the poll rather than adding to it.
func (a *Adapter) ApplicationGraphSources(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, instance string) (domain.ApplicationGraphInput, error) {
	return a.applicationSources(ctx, id, namespace, instance, true)
}

// ApplicationPodSources reads only what membership needs — the six candidate
// kinds and the pods — for the Logs tab, which draws nothing. Services and
// Ingresses are not listed and no pod template is parsed, so Candidates carry
// no Attached.
func (a *Adapter) ApplicationPodSources(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, instance string) (domain.ApplicationGraphInput, error) {
	return a.applicationSources(ctx, id, namespace, instance, false)
}

// applicationSources is the one read both of the above share; full says
// whether the map's extras (templates, Services, Ingresses) are wanted.
func (a *Adapter) applicationSources(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, instance string, full bool) (domain.ApplicationGraphInput, error) {
	// "All namespaces" is a query, not an application's home: the same instance
	// name in two namespaces is two copies, and one map must not merge them.
	if namespace.IsAll() {
		return domain.ApplicationGraphInput{}, fmt.Errorf("mapping application %q: %w: an application lives in one namespace",
			instance, domain.ErrInvalidNamespaceName)
	}
	if instance == "" {
		return domain.ApplicationGraphInput{}, fmt.Errorf("mapping an application in %q: %w",
			namespace, domain.ErrEmptyApplicationInstance)
	}
	// A value that is not a legal label value can match nothing, and building
	// a selector from it would fail later with a message about the selector.
	if problems := validation.IsValidLabelValue(instance); len(problems) > 0 {
		return domain.ApplicationGraphInput{}, fmt.Errorf("mapping application %q in %q: %w: %s",
			instance, namespace, domain.ErrInvalidApplicationInstance, problems[0])
	}

	set, err := a.factory.clientsFor(id)
	if err != nil {
		return domain.ApplicationGraphInput{}, err
	}
	client := set.typed
	ns := namespace.String()
	selector := labels.SelectorFromSet(labels.Set{domain.LabelInstance: instance}).String()

	input := domain.ApplicationGraphInput{Instance: instance, Namespace: namespace}

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)

	degrade := func(source string, err error) {
		mu.Lock()
		defer mu.Unlock()
		input.Unreadable = append(input.Unreadable, source)
		a.logger.DebugContext(ctx, "application graph source unavailable",
			slog.String("source", source), slog.String("error", err.Error()))
	}
	addCandidates := func(found []domain.ApplicationCandidate) {
		mu.Lock()
		input.Candidates = append(input.Candidates, found...)
		mu.Unlock()
	}

	labelled := metav1.ListOptions{LabelSelector: selector, ResourceVersion: cachedResourceVersion}
	everything := metav1.ListOptions{ResourceVersion: cachedResourceVersion}

	wg.Go(func() {
		list, err := client.AppsV1().Deployments(ns).List(ctx, labelled)
		if err != nil {
			degrade("deployments", err)
			return
		}
		found := make([]domain.ApplicationCandidate, 0, len(list.Items))
		for i := range list.Items {
			d := &list.Items[i]
			found = append(found, domain.ApplicationCandidate{
				Kind: "Deployment", Name: d.Name, Labels: d.Labels,
				Owner:   ownerOfObject(d.OwnerReferences),
				Desired: replicasOrOne(d.Spec.Replicas), Ready: d.Status.ReadyReplicas,
				Attached: attachedIf(full, &d.Spec.Template.Spec),
			})
		}
		addCandidates(found)
	})

	wg.Go(func() {
		list, err := client.AppsV1().StatefulSets(ns).List(ctx, labelled)
		if err != nil {
			degrade("statefulsets", err)
			return
		}
		found := make([]domain.ApplicationCandidate, 0, len(list.Items))
		for i := range list.Items {
			s := &list.Items[i]
			found = append(found, domain.ApplicationCandidate{
				Kind: "StatefulSet", Name: s.Name, Labels: s.Labels,
				Owner:   ownerOfObject(s.OwnerReferences),
				Desired: replicasOrOne(s.Spec.Replicas), Ready: s.Status.ReadyReplicas,
				Attached: attachedIf(full, &s.Spec.Template.Spec),
			})
		}
		addCandidates(found)
	})

	wg.Go(func() {
		list, err := client.AppsV1().DaemonSets(ns).List(ctx, labelled)
		if err != nil {
			degrade("daemonsets", err)
			return
		}
		found := make([]domain.ApplicationCandidate, 0, len(list.Items))
		for i := range list.Items {
			d := &list.Items[i]
			found = append(found, domain.ApplicationCandidate{
				Kind: "DaemonSet", Name: d.Name, Labels: d.Labels,
				Owner:   ownerOfObject(d.OwnerReferences),
				Desired: d.Status.DesiredNumberScheduled, Ready: d.Status.NumberReady,
				Attached: attachedIf(full, &d.Spec.Template.Spec),
			})
		}
		addCandidates(found)
	})

	wg.Go(func() {
		list, err := client.BatchV1().CronJobs(ns).List(ctx, labelled)
		if err != nil {
			degrade("cronjobs", err)
			return
		}
		found := make([]domain.ApplicationCandidate, 0, len(list.Items))
		for i := range list.Items {
			c := &list.Items[i]
			found = append(found, domain.ApplicationCandidate{
				Kind: "CronJob", Name: c.Name, Labels: c.Labels,
				Owner:     ownerOfObject(c.OwnerReferences),
				Suspended: c.Spec.Suspend != nil && *c.Spec.Suspend,
				Attached:  attachedIf(full, &c.Spec.JobTemplate.Spec.Template.Spec),
			})
		}
		addCandidates(found)
	})

	wg.Go(func() {
		list, err := client.AppsV1().ReplicaSets(ns).List(ctx, everything)
		if err != nil {
			degrade("replicasets", err)
			return
		}
		found := make([]domain.ApplicationCandidate, 0, len(list.Items))
		for i := range list.Items {
			found = append(found, replicaSetCandidate(&list.Items[i], full))
		}
		addCandidates(found)
	})

	wg.Go(func() {
		list, err := client.BatchV1().Jobs(ns).List(ctx, everything)
		if err != nil {
			degrade("jobs", err)
			return
		}
		found := make([]domain.ApplicationCandidate, 0, len(list.Items))
		for i := range list.Items {
			found = append(found, jobCandidate(&list.Items[i], full))
		}
		addCandidates(found)
	})

	wg.Go(func() {
		pods, err := a.ListPods(ctx, id, namespace, domain.Projection{})
		if err != nil {
			degrade("pods", err)
			return
		}
		mu.Lock()
		input.Pods = pods
		mu.Unlock()
	})

	if full {
		wg.Go(func() {
			list, err := client.CoreV1().Services(ns).List(ctx, everything)
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
			refs, err := ingressRefs(ctx, client, ns)
			if err != nil {
				degrade("ingresses", err)
				return
			}
			mu.Lock()
			input.Ingresses = refs
			mu.Unlock()
		})
	}

	wg.Wait()
	return input, nil
}

func replicaSetCandidate(r *appsv1.ReplicaSet, full bool) domain.ApplicationCandidate {
	return domain.ApplicationCandidate{
		Kind: "ReplicaSet", Name: r.Name, Labels: r.Labels,
		Owner:   ownerOfObject(r.OwnerReferences),
		Desired: replicasOrOne(r.Spec.Replicas), Ready: r.Status.ReadyReplicas,
		Attached: attachedIf(full, &r.Spec.Template.Spec),
	}
}

func jobCandidate(j *batchv1.Job, full bool) domain.ApplicationCandidate {
	desired := int32(1)
	if j.Spec.Completions != nil {
		desired = *j.Spec.Completions
	}
	return domain.ApplicationCandidate{
		Kind: "Job", Name: j.Name, Labels: j.Labels,
		Owner:   ownerOfObject(j.OwnerReferences),
		Desired: desired, Ready: j.Status.Succeeded, Failed: j.Status.Failed,
		Attached: attachedIf(full, &j.Spec.Template.Spec),
	}
}

// replicasOrOne reads spec.replicas, which the API server defaults to one.
func replicasOrOne(replicas *int32) int32 {
	if replicas == nil {
		return 1
	}
	return *replicas
}

// ownerOfObject returns the controlling owner in domain terms, zero when
// nothing controls the object.
func ownerOfObject(owners []metav1.OwnerReference) domain.OwnerReference {
	if controller := controllerOf(owners); controller != nil {
		return domain.OwnerReference{Kind: controller.Kind, Name: controller.Name, Controller: true}
	}
	return domain.OwnerReference{}
}

// attachedIf parses a pod template only when the map wants it.
func attachedIf(full bool, spec *corev1.PodSpec) []domain.AttachedRef {
	if !full {
		return nil
	}
	return attachedFromSpec(spec)
}
