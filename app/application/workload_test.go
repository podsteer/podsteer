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

	if _, err := service.ListPods(context.Background(), "dev", domain.NamespaceAll, domain.Projection{}); !errors.Is(err, domain.ErrClusterNotConnected) {
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

	pods, err := service.ListPods(context.Background(), "dev", domain.NamespaceAll, domain.Projection{})
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

	if _, err := service.ListPods(context.Background(), "dev", "platform", domain.Projection{}); err != nil {
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

	_, err := service.ListPods(context.Background(), "dev", domain.NamespaceAll, domain.Projection{})
	if !errors.Is(err, ports.ErrForbidden) {
		t.Errorf("ListPods() error = %v, want it to wrap %v", err, ports.ErrForbidden)
	}
}

func TestRolloutHistoryRequiresAConnectedCluster(t *testing.T) {
	t.Parallel()

	service := newWorkloadService(t, &fakeKubernetes{}, false)

	if _, err := service.RolloutHistory(context.Background(), "dev", domain.WorkloadDeployment, "web", "api"); !errors.Is(err, domain.ErrClusterNotConnected) {
		t.Errorf("RolloutHistory() error = %v, want %v", err, domain.ErrClusterNotConnected)
	}
}

// TestRolloutHistoryAcceptsTheThreeSupportedKinds mirrors how SetImage and
// RollbackWorkload are checked: RolloutHistory supports exactly the three
// controller kinds whose pod template sits at spec.template.
func TestRolloutHistoryAcceptsTheThreeSupportedKinds(t *testing.T) {
	t.Parallel()

	for _, kind := range []domain.WorkloadKind{domain.WorkloadDeployment, domain.WorkloadStatefulSet, domain.WorkloadDaemonSet} {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()

			kubernetes := &fakeKubernetes{revisions: []domain.Revision{{Number: 1, Name: "api-111", Current: true}}}
			service := newWorkloadService(t, kubernetes, true)

			revisions, err := service.RolloutHistory(context.Background(), "dev", kind, "web", "api")
			if err != nil {
				t.Fatalf("RolloutHistory() error = %v", err)
			}
			if len(revisions) != 1 {
				t.Fatalf("len(revisions) = %d, want 1", len(revisions))
			}
		})
	}
}

func TestRolloutHistoryRejectsAKindThatDoesNotCarryOne(t *testing.T) {
	t.Parallel()

	kubernetes := &fakeKubernetes{revisions: []domain.Revision{{Number: 1}}}
	service := newWorkloadService(t, kubernetes, true)

	_, err := service.RolloutHistory(context.Background(), "dev", domain.WorkloadCronJob, "batch", "nightly")
	if !errors.Is(err, domain.ErrUnsupportedWorkloadKind) {
		t.Errorf("RolloutHistory() error = %v, want %v", err, domain.ErrUnsupportedWorkloadKind)
	}
}

func TestRolloutHistoryPassesNamespaceAndNameThrough(t *testing.T) {
	t.Parallel()

	kubernetes := &fakeKubernetes{}
	service := newWorkloadService(t, kubernetes, true)

	if _, err := service.RolloutHistory(context.Background(), "dev", domain.WorkloadDeployment, "platform", "api"); err != nil {
		t.Fatalf("RolloutHistory() error = %v", err)
	}

	cluster, namespace := kubernetes.lastRequest()
	if cluster != "dev" {
		t.Errorf("queried cluster %q, want %q", cluster, "dev")
	}
	if namespace != "platform" {
		t.Errorf("queried namespace %q, want %q", namespace, "platform")
	}
}

func TestRolloutHistoryPreservesPortErrorClassification(t *testing.T) {
	t.Parallel()

	service := newWorkloadService(t, &fakeKubernetes{revisionsErr: ports.ErrForbidden}, true)

	_, err := service.RolloutHistory(context.Background(), "dev", domain.WorkloadDeployment, "web", "api")
	if !errors.Is(err, ports.ErrForbidden) {
		t.Errorf("RolloutHistory() error = %v, want it to wrap %v", err, ports.ErrForbidden)
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

// The projection is the list view's, and the port is what acts on it: the
// service must hand it over untouched rather than normalising it away.
func TestListPodsHandsTheProjectionToThePort(t *testing.T) {
	t.Parallel()

	kubernetes := &fakeKubernetes{}
	service := newWorkloadService(t, kubernetes, true)

	projection := domain.NewProjection([]string{"team", "owner"})
	if _, err := service.ListPods(context.Background(), "dev", domain.NamespaceAll, projection); err != nil {
		t.Fatalf("ListPods() error = %v", err)
	}

	kubernetes.mu.Lock()
	defer kubernetes.mu.Unlock()
	if len(kubernetes.requestedProjections) != 1 || kubernetes.requestedProjections[0].String() != "owner,team" {
		t.Fatalf("port received projections %v, want [owner,team]", kubernetes.requestedProjections)
	}
}

// The sums beside a controller list read the same lists the assessment does,
// and they must keep coalescing with it: every read they issue carries the
// EMPTY projection, whatever columns the list view itself asked for.
func TestWorkloadConsumptionReadsWithoutAProjection(t *testing.T) {
	t.Parallel()

	kubernetes := &fakeKubernetes{}
	service := newWorkloadService(t, kubernetes, true)

	if _, err := service.WorkloadConsumption(context.Background(), "dev", domain.WorkloadDeployment, domain.NamespaceAll); err != nil {
		t.Fatalf("WorkloadConsumption() error = %v", err)
	}

	kubernetes.mu.Lock()
	defer kubernetes.mu.Unlock()
	if len(kubernetes.requestedProjections) == 0 {
		t.Fatal("no list reached the port")
	}
	for _, projection := range kubernetes.requestedProjections {
		if !projection.IsEmpty() {
			t.Fatalf("a consumption read carried projection %q, want none", projection)
		}
	}
}
