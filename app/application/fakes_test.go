package application_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"k8sense/app/domain"
	"k8sense/app/ports"
)

// Hand-written fakes rather than a mocking framework. The ports are small
// enough that a struct with a couple of fields says more about each test's
// setup than a chain of expectation builders would, and it keeps the test
// suite dependency-free.

// fakeKubeconfig is a stand-in for the local kubeconfig.
type fakeKubeconfig struct {
	clusters []domain.Cluster
	err      error
}

var _ ports.KubeconfigPort = (*fakeKubeconfig)(nil)

func (f *fakeKubeconfig) Clusters(context.Context) ([]domain.Cluster, error) {
	if f.err != nil {
		return nil, f.err
	}
	// A copy, so a service that sorts in place cannot disturb the fixture and
	// make a later assertion in the same test pass for the wrong reason.
	return append([]domain.Cluster(nil), f.clusters...), nil
}

func (f *fakeKubeconfig) Cluster(_ context.Context, id domain.ClusterID) (domain.Cluster, error) {
	if f.err != nil {
		return domain.Cluster{}, f.err
	}
	for _, cluster := range f.clusters {
		if cluster.ID() == id {
			return cluster, nil
		}
	}
	return domain.Cluster{}, domain.ErrClusterNotFound
}

// fakeKubernetes is a stand-in for a cluster's API server.
type fakeKubernetes struct {
	version    domain.ServerVersion
	versionErr error

	namespaces    []domain.Namespace
	namespacesErr error

	pods    []domain.Pod
	podsErr error

	mu               sync.Mutex
	requestedCluster domain.ClusterID
	requestedNS      domain.NamespaceName
}

var _ ports.KubernetesPort = (*fakeKubernetes)(nil)

func (f *fakeKubernetes) ServerVersion(_ context.Context, id domain.ClusterID) (domain.ServerVersion, error) {
	f.record(id, "")
	if f.versionErr != nil {
		return domain.ServerVersion{}, f.versionErr
	}
	return f.version, nil
}

func (f *fakeKubernetes) ListNamespaces(_ context.Context, id domain.ClusterID) ([]domain.Namespace, error) {
	f.record(id, "")
	if f.namespacesErr != nil {
		return nil, f.namespacesErr
	}
	return append([]domain.Namespace(nil), f.namespaces...), nil
}

func (f *fakeKubernetes) ListPods(_ context.Context, id domain.ClusterID, namespace domain.NamespaceName) ([]domain.Pod, error) {
	f.record(id, namespace)
	if f.podsErr != nil {
		return nil, f.podsErr
	}
	return append([]domain.Pod(nil), f.pods...), nil
}

func (f *fakeKubernetes) record(id domain.ClusterID, namespace domain.NamespaceName) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requestedCluster = id
	f.requestedNS = namespace
}

// lastRequest reports the cluster and namespace of the most recent call.
func (f *fakeKubernetes) lastRequest() (domain.ClusterID, domain.NamespaceName) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requestedCluster, f.requestedNS
}

// recordingPublisher captures the domain events a use case raises.
type recordingPublisher struct {
	mu     sync.Mutex
	events []domain.Event
}

var _ ports.EventPublisher = (*recordingPublisher)(nil)

func (p *recordingPublisher) Publish(_ context.Context, event domain.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, event)
}

func (p *recordingPublisher) recorded() []domain.Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]domain.Event(nil), p.events...)
}

// --- fixtures -------------------------------------------------------------

// mustCluster builds a cluster or fails the test.
func mustCluster(t *testing.T, id string, current bool) domain.Cluster {
	t.Helper()

	endpoint, err := domain.NewServerEndpoint("https://" + id + ".example.com:6443")
	if err != nil {
		t.Fatalf("building endpoint for %q: %v", id, err)
	}

	cluster, err := domain.NewCluster(domain.ClusterSpec{
		ID:        domain.ClusterID(id),
		Server:    endpoint,
		IsCurrent: current,
	})
	if err != nil {
		t.Fatalf("building cluster %q: %v", id, err)
	}
	return cluster
}

// mustPod builds a pod or fails the test.
func mustPod(t *testing.T, namespace, name string) domain.Pod {
	t.Helper()

	pod, err := domain.NewPod(domain.PodSpec{
		Name:      name,
		Namespace: domain.NamespaceName(namespace),
		ClusterID: "dev",
		Phase:     domain.PodPhaseRunning,
	})
	if err != nil {
		t.Fatalf("building pod %s/%s: %v", namespace, name, err)
	}
	return pod
}

// mustNamespace builds a namespace or fails the test.
func mustNamespace(t *testing.T, name string) domain.Namespace {
	t.Helper()

	namespace, err := domain.NewNamespace(name, domain.NamespacePhaseActive, time.Time{})
	if err != nil {
		t.Fatalf("building namespace %q: %v", name, err)
	}
	return namespace
}
