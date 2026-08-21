package application_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// Hand-written fakes rather than a mocking framework. The ports are small
// enough that a struct with a couple of fields says more about each test's
// setup than a chain of expectation builders would, and it keeps the test
// suite dependency-free.

// fakeKubeconfig is a stand-in for the local kubeconfig.
type fakeKubeconfig struct {
	clusters []domain.Cluster
	err      error

	// merged records what Merge was asked to add, so a test can assert the
	// service forwarded the paste rather than inventing one.
	merged string
	merge  domain.KubeconfigMerge
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

	nodes       []domain.Node
	customKinds []domain.ResourceKind

	workloads    []domain.Workload
	workloadsErr error

	podUsage  map[string]domain.Metrics
	nodeUsage map[string]domain.Metrics

	mu               sync.Mutex
	requestedCluster domain.ClusterID
	requestedNS      domain.NamespaceName
	requestedKind    map[domain.WorkloadKind]bool
}

var (
	_ ports.ClusterPort  = (*fakeKubernetes)(nil)
	_ ports.WorkloadPort = (*fakeKubernetes)(nil)
	_ ports.MetricsPort  = (*fakeKubernetes)(nil)
)

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

func (f *fakeKubernetes) ListNodes(_ context.Context, id domain.ClusterID) ([]domain.Node, error) {
	f.record(id, "")
	return append([]domain.Node(nil), f.nodes...), nil
}

func (f *fakeKubernetes) DiscoverCustomKinds(_ context.Context, id domain.ClusterID) ([]domain.ResourceKind, error) {
	f.record(id, "")
	return append([]domain.ResourceKind(nil), f.customKinds...), nil
}

func (f *fakeKubernetes) ListWorkloads(_ context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName) ([]domain.Workload, error) {
	f.record(id, namespace)
	f.recordKind(kind)
	if f.workloadsErr != nil {
		return nil, f.workloadsErr
	}
	return append([]domain.Workload(nil), f.workloads...), nil
}

// ListPodsForWorkload returns the fake's pods unfiltered: ownership is
// resolved by the k8s adapter from label selectors, so there is nothing for a
// port-level fake to filter on.
func (f *fakeKubernetes) ListPodsForWorkload(_ context.Context, id domain.ClusterID, namespace domain.NamespaceName, _ domain.WorkloadKind, _ string) ([]domain.Pod, error) {
	f.record(id, namespace)
	if f.podsErr != nil {
		return nil, f.podsErr
	}
	return append([]domain.Pod(nil), f.pods...), nil
}

// The fake reports no metrics API, which is the configuration the services
// have to keep working under — so every test exercises that path by default.
func (f *fakeKubernetes) PodMetrics(_ context.Context, _ domain.ClusterID, _ domain.NamespaceName) (map[string]domain.Metrics, error) {
	if f.podUsage == nil {
		return nil, ports.ErrMetricsUnavailable
	}
	return f.podUsage, nil
}

func (f *fakeKubernetes) NodeMetrics(_ context.Context, _ domain.ClusterID) (map[string]domain.Metrics, error) {
	if f.nodeUsage == nil {
		return nil, ports.ErrMetricsUnavailable
	}
	return f.nodeUsage, nil
}

func (f *fakeKubernetes) record(id domain.ClusterID, namespace domain.NamespaceName) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requestedCluster = id
	f.requestedNS = namespace
}

// recordKind notes that a workload kind was asked for.
func (f *fakeKubernetes) recordKind(kind domain.WorkloadKind) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.requestedKind == nil {
		f.requestedKind = make(map[domain.WorkloadKind]bool, 6)
	}
	f.requestedKind[kind] = true
}

// requestedKinds returns the workload kinds asked for so far.
func (f *fakeKubernetes) requestedKinds() map[domain.WorkloadKind]bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	kinds := make(map[domain.WorkloadKind]bool, len(f.requestedKind))
	for kind, seen := range f.requestedKind {
		kinds[kind] = seen
	}
	return kinds
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
	events []domain.DomainEvent
}

var _ ports.EventPublisher = (*recordingPublisher)(nil)

func (p *recordingPublisher) Publish(_ context.Context, event domain.DomainEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, event)
}

func (p *recordingPublisher) recorded() []domain.DomainEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]domain.DomainEvent(nil), p.events...)
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

// fakeEvents is a stand-in for the cluster's event stream.
//
// Separate from fakeKubernetes so a test can make events fail on their own,
// which is the case the overview has to survive: plenty of credentials can
// list pods but not events.
type fakeEvents struct {
	events []domain.Event
	err    error
}

var _ ports.EventPort = (*fakeEvents)(nil)

func (f *fakeEvents) ListEvents(context.Context, domain.ClusterID, domain.NamespaceName) ([]domain.Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	return append([]domain.Event(nil), f.events...), nil
}

func (f *fakeEvents) ListEventsForResource(context.Context, domain.ClusterID, domain.NamespaceName, string, string) ([]domain.Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	return append([]domain.Event(nil), f.events...), nil
}

func (f *fakeKubeconfig) PreviewMerge(_ context.Context, raw string) (domain.KubeconfigMerge, error) {
	if f.err != nil {
		return domain.KubeconfigMerge{}, f.err
	}
	_ = raw
	return f.merge, nil
}

func (f *fakeKubeconfig) Merge(_ context.Context, raw string) (domain.KubeconfigMerge, error) {
	if f.err != nil {
		return domain.KubeconfigMerge{}, f.err
	}
	f.merged = raw
	return f.merge, nil
}
