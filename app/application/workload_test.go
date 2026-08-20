package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// newWorkloadService wires a workload service sharing session with a connected
// cluster, unless connect is false.
func newWorkloadService(t *testing.T, kubernetes *fakeKubernetes, connect bool) *application.WorkloadService {
	t.Helper()

	registry := application.NewRegistry()
	if connect {
		registry.Open(mustCluster(t, "dev", true))
	}

	service, err := application.NewWorkloadService(application.WorkloadServiceDeps{
		Workloads: kubernetes,
		Metrics:   kubernetes,
		Registry:  registry,
	})
	if err != nil {
		t.Fatalf("NewWorkloadService() error = %v", err)
	}
	return service
}

func TestNewWorkloadServiceRejectsMissingDependencies(t *testing.T) {
	t.Parallel()

	shared := &fakeKubernetes{}

	if _, err := application.NewWorkloadService(application.WorkloadServiceDeps{
		Metrics:  shared,
		Registry: application.NewRegistry(),
	}); err == nil {
		t.Error("NewWorkloadService() succeeded without a WorkloadPort, want an error")
	}

	if _, err := application.NewWorkloadService(application.WorkloadServiceDeps{
		Workloads: shared,
		Metrics:   shared,
	}); err == nil {
		t.Error("NewWorkloadService() succeeded without a Registry, want an error")
	}
}

func TestListPodsRequiresAConnectedCluster(t *testing.T) {
	t.Parallel()

	service := newWorkloadService(t, &fakeKubernetes{}, false)

	if _, err := service.ListPods(context.Background(), "dev", domain.NamespaceAll); !errors.Is(err, domain.ErrClusterNotConnected) {
		t.Errorf("ListPods() error = %v, want %v", err, domain.ErrClusterNotConnected)
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

	pods, err := service.ListPods(context.Background(), "dev", domain.NamespaceAll)
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

	if _, err := service.ListPods(context.Background(), "dev", "platform"); err != nil {
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

	_, err := service.ListPods(context.Background(), "dev", domain.NamespaceAll)
	if !errors.Is(err, ports.ErrForbidden) {
		t.Errorf("ListPods() error = %v, want it to wrap %v", err, ports.ErrForbidden)
	}
}

func TestRegistryLifecycle(t *testing.T) {
	t.Parallel()

	registry := application.NewRegistry()

	if registry.Len() != 0 {
		t.Error("a fresh registry must hold no connections")
	}
	if _, err := registry.Get("dev"); !errors.Is(err, domain.ErrClusterNotConnected) {
		t.Errorf("Get() error = %v, want %v", err, domain.ErrClusterNotConnected)
	}

	registry.Open(mustCluster(t, "dev", true))
	registry.Open(mustCluster(t, "prod", false))

	// Connection order is what the tab bar renders, so it must be preserved.
	all := registry.All()
	if len(all) != 2 || all[0].ID() != "dev" || all[1].ID() != "prod" {
		t.Fatalf("All() = %v, want [dev prod] in connection order", all)
	}

	// Reconnecting must refresh in place rather than move the tab.
	registry.Open(mustCluster(t, "dev", true))
	if all := registry.All(); len(all) != 2 || all[0].ID() != "dev" {
		t.Errorf("reopening moved the tab: All() = %v", all)
	}

	if !registry.Close("dev") {
		t.Error("Close() reported an open cluster as absent")
	}
	if registry.Close("dev") {
		t.Error("Close() reported an already closed cluster as open")
	}
	if all := registry.All(); len(all) != 1 || all[0].ID() != "prod" {
		t.Errorf("All() after close = %v, want [prod]", all)
	}
}
