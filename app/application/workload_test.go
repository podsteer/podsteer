package application_test

import (
	"context"
	"errors"
	"testing"

	"k8sense/app/application"
	"k8sense/app/domain"
	"k8sense/app/ports"
)

// newWorkloadService wires a workload service sharing session with a connected
// cluster, unless connect is false.
func newWorkloadService(t *testing.T, kubernetes *fakeKubernetes, connect bool) *application.WorkloadService {
	t.Helper()

	session := application.NewSession()
	if connect {
		session.Activate(mustCluster(t, "dev", true))
	}

	service, err := application.NewWorkloadService(application.WorkloadServiceDeps{
		Kubernetes: kubernetes,
		Session:    session,
	})
	if err != nil {
		t.Fatalf("NewWorkloadService() error = %v", err)
	}
	return service
}

func TestNewWorkloadServiceRejectsMissingDependencies(t *testing.T) {
	t.Parallel()

	if _, err := application.NewWorkloadService(application.WorkloadServiceDeps{
		Session: application.NewSession(),
	}); err == nil {
		t.Error("NewWorkloadService() succeeded without a KubernetesPort, want an error")
	}

	if _, err := application.NewWorkloadService(application.WorkloadServiceDeps{
		Kubernetes: &fakeKubernetes{},
	}); err == nil {
		t.Error("NewWorkloadService() succeeded without a Session, want an error")
	}
}

func TestListPodsRequiresAnActiveCluster(t *testing.T) {
	t.Parallel()

	service := newWorkloadService(t, &fakeKubernetes{}, false)

	if _, err := service.ListPods(context.Background(), domain.NamespaceAll); !errors.Is(err, domain.ErrNoActiveCluster) {
		t.Errorf("ListPods() error = %v, want %v", err, domain.ErrNoActiveCluster)
	}
}

// The API server returns pods in etcd key order, which shifts as objects come
// and go. Sorting is what stops rows jumping under the operator's cursor.
func TestListPodsSortsByNamespaceThenName(t *testing.T) {
	t.Parallel()

	kubernetes := &fakeKubernetes{pods: []domain.Pod{
		mustPod(t, "kube-system", "coredns-b"),
		mustPod(t, "default", "web-2"),
		mustPod(t, "kube-system", "coredns-a"),
		mustPod(t, "default", "web-1"),
	}}
	service := newWorkloadService(t, kubernetes, true)

	pods, err := service.ListPods(context.Background(), domain.NamespaceAll)
	if err != nil {
		t.Fatalf("ListPods() error = %v", err)
	}

	want := []string{"default/web-1", "default/web-2", "kube-system/coredns-a", "kube-system/coredns-b"}
	if len(pods) != len(want) {
		t.Fatalf("ListPods() returned %d pods, want %d", len(pods), len(want))
	}
	for i, key := range want {
		got := pods[i].Namespace().String() + "/" + pods[i].Name()
		if got != key {
			t.Errorf("pods[%d] = %q, want %q", i, got, key)
		}
	}
}

func TestListPodsPassesNamespaceThrough(t *testing.T) {
	t.Parallel()

	kubernetes := &fakeKubernetes{}
	service := newWorkloadService(t, kubernetes, true)

	if _, err := service.ListPods(context.Background(), "platform"); err != nil {
		t.Fatalf("ListPods() error = %v", err)
	}

	cluster, namespace := kubernetes.lastRequest()
	if cluster != "dev" {
		t.Errorf("queried cluster %q, want %q", cluster, "dev")
	}
	if namespace != "platform" {
		t.Errorf("queried namespace %q, want %q", namespace, "platform")
	}
}

// The port's classification has to survive the use case, or the UI cannot tell
// an RBAC denial from an outage.
func TestListPodsPreservesPortErrorClassification(t *testing.T) {
	t.Parallel()

	service := newWorkloadService(t, &fakeKubernetes{podsErr: ports.ErrForbidden}, true)

	_, err := service.ListPods(context.Background(), domain.NamespaceAll)
	if !errors.Is(err, ports.ErrForbidden) {
		t.Errorf("ListPods() error = %v, want it to wrap %v", err, ports.ErrForbidden)
	}
}

func TestSessionLifecycle(t *testing.T) {
	t.Parallel()

	session := application.NewSession()

	if session.HasActive() {
		t.Error("a fresh session must not report an active cluster")
	}
	if _, err := session.Active(); !errors.Is(err, domain.ErrNoActiveCluster) {
		t.Errorf("Active() error = %v, want %v", err, domain.ErrNoActiveCluster)
	}

	session.Activate(mustCluster(t, "dev", true))
	active, err := session.Active()
	if err != nil {
		t.Fatalf("Active() error = %v", err)
	}
	if active.ID() != "dev" {
		t.Errorf("Active().ID() = %q, want %q", active.ID(), "dev")
	}

	session.Clear()
	if session.HasActive() {
		t.Error("Clear() left an active cluster behind")
	}
}
