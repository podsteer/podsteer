package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"k8sense/app/application"
	"k8sense/app/domain"
	"k8sense/app/ports"
)

// newClusterService wires a service around the given fakes, filling in the
// ones a test does not care about.
func newClusterService(
	t *testing.T,
	kubeconfig *fakeKubeconfig,
	kubernetes *fakeKubernetes,
	events *recordingPublisher,
) (*application.ClusterService, *application.Session) {
	t.Helper()

	session := application.NewSession()
	service, err := application.NewClusterService(application.ClusterServiceDeps{
		Kubeconfig: kubeconfig,
		Kubernetes: kubernetes,
		Events:     events,
		Session:    session,
		// A fixed clock so event timestamps are assertable.
		Now: func() time.Time { return time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewClusterService() error = %v", err)
	}
	return service, session
}

func TestNewClusterServiceRejectsMissingDependencies(t *testing.T) {
	t.Parallel()

	full := application.ClusterServiceDeps{
		Kubeconfig: &fakeKubeconfig{},
		Kubernetes: &fakeKubernetes{},
		Events:     &recordingPublisher{},
		Session:    application.NewSession(),
	}

	tests := map[string]func(*application.ClusterServiceDeps){
		"no kubeconfig port": func(d *application.ClusterServiceDeps) { d.Kubeconfig = nil },
		"no kubernetes port": func(d *application.ClusterServiceDeps) { d.Kubernetes = nil },
		"no event publisher": func(d *application.ClusterServiceDeps) { d.Events = nil },
		"no session":         func(d *application.ClusterServiceDeps) { d.Session = nil },
	}

	for name, remove := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			deps := full
			remove(&deps)

			if _, err := application.NewClusterService(deps); err == nil {
				t.Fatal("NewClusterService() succeeded with a missing dependency, want an error")
			}
		})
	}
}

// The current context is what the operator almost always wants, and the rest
// must not reshuffle between calls.
func TestListClustersOrdersCurrentFirstThenAlphabetically(t *testing.T) {
	t.Parallel()

	kubeconfig := &fakeKubeconfig{clusters: []domain.Cluster{
		mustCluster(t, "staging", false),
		mustCluster(t, "alpha", false),
		mustCluster(t, "prod", true),
	}}
	service, _ := newClusterService(t, kubeconfig, &fakeKubernetes{}, &recordingPublisher{})

	clusters, err := service.ListClusters(context.Background())
	if err != nil {
		t.Fatalf("ListClusters() error = %v", err)
	}

	want := []domain.ClusterID{"prod", "alpha", "staging"}
	if len(clusters) != len(want) {
		t.Fatalf("ListClusters() returned %d clusters, want %d", len(clusters), len(want))
	}
	for i, id := range want {
		if got := clusters[i].ID(); got != id {
			t.Errorf("clusters[%d].ID() = %q, want %q", i, got, id)
		}
	}
}

func TestConnectActivatesClusterAndPublishesEvent(t *testing.T) {
	t.Parallel()

	kubeconfig := &fakeKubeconfig{clusters: []domain.Cluster{mustCluster(t, "dev", true)}}
	kubernetes := &fakeKubernetes{version: domain.ServerVersion{GitVersion: "v1.31.2", Platform: "linux/arm64"}}
	events := &recordingPublisher{}

	service, session := newClusterService(t, kubeconfig, kubernetes, events)

	connected, err := service.Connect(context.Background(), "dev")
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	if got := connected.Version().GitVersion; got != "v1.31.2" {
		t.Errorf("returned cluster version = %q, want %q", got, "v1.31.2")
	}
	if !connected.IsReachable() {
		t.Error("returned cluster does not report as reachable")
	}

	active, err := session.Active()
	if err != nil {
		t.Fatalf("session.Active() error = %v", err)
	}
	if active.ID() != "dev" {
		t.Errorf("active cluster = %q, want %q", active.ID(), "dev")
	}

	recorded := events.recorded()
	if len(recorded) != 1 {
		t.Fatalf("published %d events, want 1", len(recorded))
	}
	event, ok := recorded[0].(domain.ClusterConnected)
	if !ok {
		t.Fatalf("published %T, want domain.ClusterConnected", recorded[0])
	}
	if event.Cluster.ID() != "dev" {
		t.Errorf("event cluster = %q, want %q", event.Cluster.ID(), "dev")
	}
	if !event.OccurredAt().Equal(time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("event timestamp = %v, want the injected clock's time", event.OccurredAt())
	}
}

// A failed connect must leave the previous selection intact — replacing it
// with a cluster that does not answer would strand the UI.
func TestConnectFailureLeavesSessionUntouchedAndNotifies(t *testing.T) {
	t.Parallel()

	kubeconfig := &fakeKubeconfig{clusters: []domain.Cluster{
		mustCluster(t, "dev", true),
		mustCluster(t, "prod", false),
	}}
	kubernetes := &fakeKubernetes{version: domain.ServerVersion{GitVersion: "v1.31.2"}}
	events := &recordingPublisher{}

	service, session := newClusterService(t, kubeconfig, kubernetes, events)

	if _, err := service.Connect(context.Background(), "dev"); err != nil {
		t.Fatalf("Connect(dev) error = %v", err)
	}

	kubernetes.versionErr = ports.ErrUnreachable

	if _, err := service.Connect(context.Background(), "prod"); !errors.Is(err, ports.ErrUnreachable) {
		t.Fatalf("Connect(prod) error = %v, want %v", err, ports.ErrUnreachable)
	}

	active, err := session.Active()
	if err != nil {
		t.Fatalf("session.Active() error = %v", err)
	}
	if active.ID() != "dev" {
		t.Errorf("active cluster = %q, want the previously connected %q", active.ID(), "dev")
	}

	recorded := events.recorded()
	if len(recorded) != 2 {
		t.Fatalf("published %d events, want 2", len(recorded))
	}
	unreachable, ok := recorded[1].(domain.ClusterUnreachable)
	if !ok {
		t.Fatalf("published %T, want domain.ClusterUnreachable", recorded[1])
	}
	if unreachable.ClusterID != "prod" {
		t.Errorf("event cluster = %q, want %q", unreachable.ClusterID, "prod")
	}
}

func TestConnectRejectsUnknownCluster(t *testing.T) {
	t.Parallel()

	service, _ := newClusterService(t, &fakeKubeconfig{}, &fakeKubernetes{}, &recordingPublisher{})

	if _, err := service.Connect(context.Background(), "ghost"); !errors.Is(err, domain.ErrClusterNotFound) {
		t.Errorf("Connect() error = %v, want %v", err, domain.ErrClusterNotFound)
	}
}

func TestConnectRejectsBlankID(t *testing.T) {
	t.Parallel()

	service, _ := newClusterService(t, &fakeKubeconfig{}, &fakeKubernetes{}, &recordingPublisher{})

	if _, err := service.Connect(context.Background(), ""); !errors.Is(err, domain.ErrEmptyClusterID) {
		t.Errorf("Connect(\"\") error = %v, want %v", err, domain.ErrEmptyClusterID)
	}
}

func TestListNamespacesRequiresAnActiveCluster(t *testing.T) {
	t.Parallel()

	service, _ := newClusterService(t, &fakeKubeconfig{}, &fakeKubernetes{}, &recordingPublisher{})

	if _, err := service.ListNamespaces(context.Background()); !errors.Is(err, domain.ErrNoActiveCluster) {
		t.Errorf("ListNamespaces() error = %v, want %v", err, domain.ErrNoActiveCluster)
	}
}

func TestListNamespacesSortsByNameAndTargetsActiveCluster(t *testing.T) {
	t.Parallel()

	kubeconfig := &fakeKubeconfig{clusters: []domain.Cluster{mustCluster(t, "dev", true)}}
	kubernetes := &fakeKubernetes{
		namespaces: []domain.Namespace{
			mustNamespace(t, "kube-system"),
			mustNamespace(t, "default"),
			mustNamespace(t, "argocd"),
		},
	}
	service, _ := newClusterService(t, kubeconfig, kubernetes, &recordingPublisher{})

	if _, err := service.Connect(context.Background(), "dev"); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	namespaces, err := service.ListNamespaces(context.Background())
	if err != nil {
		t.Fatalf("ListNamespaces() error = %v", err)
	}

	want := []domain.NamespaceName{"argocd", "default", "kube-system"}
	for i, name := range want {
		if got := namespaces[i].Name(); got != name {
			t.Errorf("namespaces[%d].Name() = %q, want %q", i, got, name)
		}
	}

	if cluster, _ := kubernetes.lastRequest(); cluster != "dev" {
		t.Errorf("queried cluster %q, want the active %q", cluster, "dev")
	}
}
