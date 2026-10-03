package k8s

import (
	"context"
	"errors"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	clientgotesting "k8s.io/client-go/testing"

	"github.com/podsteer/podsteer/app/domain"
)

var shopLabels = map[string]string{domain.LabelInstance: "shop"}

func shopDeployment(name string, labels map[string]string, config string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: name, Labels: labels},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name: "app",
				EnvFrom: []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: config},
				}}},
			}},
		}}},
	}
}

// applicationAdapter is newTestAdapter plus the disabled watch manager that
// ListPods, which this read goes through, dereferences.
func applicationAdapter(client *fake.Clientset) *Adapter {
	adapter := newTestAdapter("dev", client)
	adapter.watches = newWatchManager(false, adapter.logger, idleAfter, sweepEvery, recheckEvery)
	return adapter
}

func candidateNames(input domain.ApplicationGraphInput, kind string) []string {
	var out []string
	for _, c := range input.Candidates {
		if c.Kind == kind {
			out = append(out, c.Name)
		}
	}
	return out
}

func TestApplicationGraphSourcesSelectsByLabelPerKindAndReadsTemplates(t *testing.T) {
	yes := true
	controller := &yes
	adapter := applicationAdapter(fake.NewSimpleClientset(
		shopDeployment("web", shopLabels, "web-config"),
		shopDeployment("elsewhere", map[string]string{domain.LabelInstance: "other"}, "x"),
		// Namespace-wide kinds arrive whole, labelled or not.
		&appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns", Name: "web-1",
			OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "web", Controller: controller}},
		}},
		&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "run-1"}},
		&batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "nightly", Labels: shopLabels}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "front", Labels: shopLabels}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "web-1-a", Labels: shopLabels}},
		&networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "edge"},
			Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{
				Host: "shop.example.com",
				IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
					Paths: []networkingv1.HTTPIngressPath{{Backend: networkingv1.IngressBackend{
						Service: &networkingv1.IngressServiceBackend{Name: "front"},
					}}},
				}},
			}}},
		},
	))

	input, err := adapter.ApplicationGraphSources(context.Background(), "dev", "ns", "shop")
	if err != nil {
		t.Fatalf("ApplicationGraphSources() error = %v", err)
	}

	if got := candidateNames(input, "Deployment"); len(got) != 1 || got[0] != "web" {
		t.Errorf("deployments = %v, want only the labelled one", got)
	}
	if got := candidateNames(input, "ReplicaSet"); len(got) != 1 {
		t.Errorf("replicasets = %v, want every one in the namespace", got)
	}
	if got := candidateNames(input, "Job"); len(got) != 1 {
		t.Errorf("jobs = %v, want every one in the namespace", got)
	}
	if got := candidateNames(input, "CronJob"); len(got) != 1 || got[0] != "nightly" {
		t.Errorf("cronjobs = %v", got)
	}
	for _, c := range input.Candidates {
		switch c.Name {
		case "web":
			if !contains(named(c.Attached, domain.GraphConfig), "web-config") {
				t.Errorf("attached = %+v, want the template's ConfigMap", c.Attached)
			}
		case "web-1":
			if c.Owner.Kind != "Deployment" || c.Owner.Name != "web" {
				t.Errorf("replicaset owner = %+v", c.Owner)
			}
		}
	}
	if len(input.Services) != 1 || input.Services[0].Labels[domain.LabelInstance] != "shop" {
		t.Errorf("services = %+v, want the Service with its labels", input.Services)
	}
	if len(input.Pods) != 1 || input.Pods[0].Name() != "web-1-a" {
		t.Errorf("pods = %v, want the namespace's pod", input.Pods)
	}
	if len(input.Ingresses) != 1 || input.Ingresses[0].Name != "edge" || len(input.Ingresses[0].Backends) != 1 || input.Ingresses[0].Backends[0] != "front" {
		t.Errorf("ingresses = %+v, want edge routing to front", input.Ingresses)
	}
	if len(input.Unreadable) != 0 {
		t.Errorf("Unreadable = %v, want none", input.Unreadable)
	}
}

func TestApplicationPodSourcesListsNeitherServicesNorIngressesNorTemplates(t *testing.T) {
	client := fake.NewSimpleClientset(
		shopDeployment("web", shopLabels, "web-config"),
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "front", Labels: shopLabels}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "web-1-a", Labels: shopLabels}},
	)
	adapter := applicationAdapter(client)

	input, err := adapter.ApplicationPodSources(context.Background(), "dev", "ns", "shop")
	if err != nil {
		t.Fatalf("ApplicationPodSources() error = %v", err)
	}

	for _, action := range client.Actions() {
		switch action.GetResource().Resource {
		case "services", "ingresses":
			t.Errorf("the pod read issued %s %s; it must not touch Services or Ingresses",
				action.GetVerb(), action.GetResource().Resource)
		}
	}
	if len(input.Services) != 0 || len(input.Ingresses) != 0 {
		t.Errorf("services/ingresses = %v / %v, want none", input.Services, input.Ingresses)
	}
	if len(input.Pods) != 1 || len(candidateNames(input, "Deployment")) != 1 {
		t.Errorf("candidates and pods must still be read: %+v", input)
	}
	for _, c := range input.Candidates {
		if len(c.Attached) != 0 {
			t.Errorf("candidate %s carries Attached %+v; templates are not parsed for membership", c.Name, c.Attached)
		}
	}
}

func TestApplicationGraphSourcesNamesAForbiddenKindAndKeepsTheRest(t *testing.T) {
	client := fake.NewSimpleClientset(
		shopDeployment("web", shopLabels, "web-config"),
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "front", Labels: shopLabels}},
	)
	client.PrependReactor("list", "cronjobs", func(clientgotesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "cronjobs"}, "", errors.New("nope"))
	})
	adapter := applicationAdapter(client)

	input, err := adapter.ApplicationGraphSources(context.Background(), "dev", "ns", "shop")
	if err != nil {
		t.Fatalf("a refused kind must not fail the read, got %v", err)
	}

	if len(input.Unreadable) != 1 || input.Unreadable[0] != "cronjobs" {
		t.Errorf("Unreadable = %v, want [cronjobs]", input.Unreadable)
	}
	if len(candidateNames(input, "Deployment")) != 1 || len(input.Services) != 1 {
		t.Errorf("the other sources must still be present: %+v", input)
	}
}

func TestApplicationGraphSourcesRefusesAnUnscopedQuestion(t *testing.T) {
	adapter := applicationAdapter(fake.NewSimpleClientset())

	if _, err := adapter.ApplicationGraphSources(context.Background(), "dev", domain.NamespaceAll, "shop"); !errors.Is(err, domain.ErrInvalidNamespaceName) {
		t.Errorf("all namespaces: error = %v, want %v", err, domain.ErrInvalidNamespaceName)
	}
	if _, err := adapter.ApplicationGraphSources(context.Background(), "dev", "ns", ""); !errors.Is(err, domain.ErrEmptyApplicationInstance) {
		t.Errorf("empty instance: error = %v, want %v", err, domain.ErrEmptyApplicationInstance)
	}
	for _, bad := range []string{"has space", "-leading", "x/y", "a,b=c"} {
		if _, err := adapter.ApplicationGraphSources(context.Background(), "dev", "ns", bad); !errors.Is(err, domain.ErrInvalidApplicationInstance) {
			t.Errorf("instance %q: error = %v, want %v", bad, err, domain.ErrInvalidApplicationInstance)
		}
		if _, err := adapter.ApplicationPodSources(context.Background(), "dev", "ns", bad); !errors.Is(err, domain.ErrInvalidApplicationInstance) {
			t.Errorf("pod sources, instance %q: error = %v, want %v", bad, err, domain.ErrInvalidApplicationInstance)
		}
	}
}
