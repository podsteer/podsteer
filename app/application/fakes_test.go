package application_test

import (
	"context"
	"io"
	"maps"
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

	podUsage        map[string]domain.PodUsage
	nodeUsage       map[string]domain.Metrics
	metricsErr      error
	nodeFilesystems map[string]domain.NodeFilesystems
	metricsBackend  domain.MetricsBackend
	volumes         []domain.PersistentVolume
	claims          []domain.PersistentVolumeClaim

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
func (f *fakeKubernetes) PodMetrics(_ context.Context, _ domain.ClusterID, _ domain.NamespaceName) (map[string]domain.PodUsage, error) {
	if f.podUsage == nil {
		return nil, ports.ErrMetricsUnavailable
	}
	return f.podUsage, nil
}

func (f *fakeKubernetes) NodeMetrics(_ context.Context, _ domain.ClusterID) (map[string]domain.Metrics, error) {
	// metricsErr lets a test choose WHICH failure, which is what the metrics
	// status derives from. Absent it, the historical default stands: no usage
	// configured means no metrics API, the commonest real case.
	if f.metricsErr != nil {
		return nil, f.metricsErr
	}
	if f.nodeUsage == nil {
		return nil, ports.ErrMetricsUnavailable
	}
	return f.nodeUsage, nil
}

func (f *fakeKubernetes) ListPersistentVolumes(_ context.Context, _ domain.ClusterID) ([]domain.PersistentVolume, error) {
	return f.volumes, nil
}

func (f *fakeKubernetes) ListPersistentVolumeClaims(_ context.Context, _ domain.ClusterID, _ domain.NamespaceName) ([]domain.PersistentVolumeClaim, error) {
	return f.claims, nil
}

func (f *fakeKubernetes) WorkloadGraphSources(_ context.Context, _ domain.ClusterID, ns domain.NamespaceName, kind domain.WorkloadKind, name string) (domain.WorkloadGraphInput, error) {
	return domain.WorkloadGraphInput{
		Kind: string(kind), Name: name, Namespace: ns, Pods: f.pods,
	}, nil
}

func (f *fakeKubernetes) PodGraphSources(_ context.Context, _ domain.ClusterID, _ domain.NamespaceName, name string) (domain.GraphInput, error) {
	for _, pod := range f.pods {
		if pod.Name() == name {
			return domain.GraphInput{Pod: pod}, nil
		}
	}
	return domain.GraphInput{}, ports.ErrNotFound
}

func (f *fakeKubernetes) ListPodsOnNode(_ context.Context, _ domain.ClusterID, node string) ([]domain.Pod, error) {
	var on []domain.Pod
	for _, pod := range f.pods {
		if pod.NodeName() == node {
			on = append(on, pod)
		}
	}
	return on, nil
}

// DrainCandidates returns the fake's pods on node as candidates with no
// Mirror or LocalStorage set: the k8s adapter is what fills those from raw
// corev1.Pod fields the port-level fake has no equivalent of, so a test that
// needs one true builds its own domain.DrainCandidate directly.
func (f *fakeKubernetes) DrainCandidates(_ context.Context, _ domain.ClusterID, node string) ([]domain.DrainCandidate, error) {
	var candidates []domain.DrainCandidate
	for _, pod := range f.pods {
		if pod.NodeName() == node {
			candidates = append(candidates, domain.DrainCandidate{Pod: pod})
		}
	}
	return candidates, nil
}

func (f *fakeKubernetes) NodeFilesystems(_ context.Context, _ domain.ClusterID) (map[string]domain.NodeFilesystems, error) {
	if f.nodeFilesystems == nil {
		return nil, ports.ErrMetricsUnavailable
	}
	return f.nodeFilesystems, nil
}

func (f *fakeKubernetes) DiscoverMetricsBackend(_ context.Context, _ domain.ClusterID) (domain.MetricsBackend, error) {
	return f.metricsBackend, nil
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
	maps.Copy(kinds, f.requestedKind)
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

// fakeManagementPort is a stand-in for the write side of a cluster.
//
// Every method not exercised by a given test is still implemented, so the
// fake satisfies ports.ManagementPort in full — a Go interface has no partial
// implementations, and a test for TriggerCronJob must not have to know
// anything about StreamLogs to compile.
type fakeManagementPort struct {
	mu sync.Mutex
	// calls records every write that reached the fake, in order, for the
	// read-only tests that assert a refused write never got this far.
	calls []string
	// err, when set, is returned by every tracked write.
	err error

	triggerJobName string
	triggerErr     error
	triggeredID    domain.ClusterID
	triggeredNS    domain.NamespaceName
	triggeredName  string

	suspendErr     error
	suspendCalled  bool
	suspendedID    domain.ClusterID
	suspendedKind  domain.WorkloadKind
	suspendedNS    domain.NamespaceName
	suspendedName  string
	suspendedValue bool

	setSecretKeyErr    error
	setSecretKeyCalled bool
	setSecretID        domain.ClusterID
	setSecretNS        domain.NamespaceName
	setSecretName      string
	setSecretKeyName   string
	setSecretValue     []byte

	setConfigMapKeyErr    error
	setConfigMapKeyCalled bool
	setConfigMapID        domain.ClusterID
	setConfigMapNS        domain.NamespaceName
	setConfigMapName      string
	setConfigMapKeyName   string
	setConfigMapValue     string

	setImageErr       error
	setImageCalled    bool
	setImageID        domain.ClusterID
	setImageKind      domain.WorkloadKind
	setImageNS        domain.NamespaceName
	setImageName      string
	setImageContainer string
	setImageValue     string
	setImageInit      bool

	cordonErr     error
	cordonCalled  bool
	cordonedID    domain.ClusterID
	cordonedName  string
	cordonedValue bool

	evictErr     error
	evictCalled  bool
	evictedID    domain.ClusterID
	evictedNS    domain.NamespaceName
	evictedName  string
	evictedGrace int

	drainErr    error
	drainCalled bool
	drainedID   domain.ClusterID
	drainedName string
	drainedOpts domain.DrainOptions
	drainReport domain.DrainReport
}

var _ ports.ManagementPort = (*fakeManagementPort)(nil)

func (f *fakeManagementPort) StreamLogs(context.Context, domain.ClusterID, domain.NamespaceName, string, string, bool, int64, chan<- string) error {
	f.record("StreamLogs")
	return f.err
}

func (f *fakeManagementPort) DeleteResource(context.Context, domain.ResourceRef) error {
	f.record("DeleteResource")
	return f.err
}

func (f *fakeManagementPort) ScaleWorkload(context.Context, domain.ClusterID, domain.WorkloadKind, domain.NamespaceName, string, int32) error {
	f.record("ScaleWorkload")
	return f.err
}

func (f *fakeManagementPort) RestartRollout(context.Context, domain.ClusterID, domain.WorkloadKind, domain.NamespaceName, string) error {
	f.record("RestartRollout")
	return f.err
}

func (f *fakeManagementPort) UpdateResource(context.Context, domain.ClusterID, string, bool) (domain.ApplyOutcome, error) {
	f.record("UpdateResource")
	return domain.ApplyOutcome{}, f.err
}

func (f *fakeManagementPort) ExecInPod(context.Context, domain.ClusterID, domain.NamespaceName, string, string, []string, io.Reader, io.Writer, io.Writer, bool) error {
	f.record("ExecInPod")
	return f.err
}

func (f *fakeManagementPort) ExecInPodWithTTY(context.Context, domain.ClusterID, domain.NamespaceName, string, string, []string, io.Reader, io.Writer, io.Writer, ports.TerminalSizeQueue) error {
	f.record("ExecInPodWithTTY")
	return f.err
}

func (f *fakeManagementPort) AttachToPod(context.Context, domain.ClusterID, domain.NamespaceName, string, string, io.Reader, io.Writer, io.Writer, ports.TerminalSizeQueue) error {
	f.record("AttachToPod")
	return f.err
}

func (f *fakeManagementPort) TriggerCronJob(_ context.Context, id domain.ClusterID, namespace domain.NamespaceName, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.triggeredID = id
	f.triggeredNS = namespace
	f.triggeredName = name
	if f.triggerErr != nil {
		return "", f.triggerErr
	}
	return f.triggerJobName, nil
}

func (f *fakeManagementPort) SuspendWorkload(_ context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName, name string, suspend bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.suspendCalled = true
	f.suspendedID = id
	f.suspendedKind = kind
	f.suspendedNS = namespace
	f.suspendedName = name
	f.suspendedValue = suspend
	return f.suspendErr
}

func (f *fakeManagementPort) SetSecretKey(_ context.Context, id domain.ClusterID, namespace domain.NamespaceName, name, key string, value []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setSecretKeyCalled = true
	f.setSecretID = id
	f.setSecretNS = namespace
	f.setSecretName = name
	f.setSecretKeyName = key
	f.setSecretValue = value
	return f.setSecretKeyErr
}

func (f *fakeManagementPort) SetConfigMapKey(_ context.Context, id domain.ClusterID, namespace domain.NamespaceName, name, key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setConfigMapKeyCalled = true
	f.setConfigMapID = id
	f.setConfigMapNS = namespace
	f.setConfigMapName = name
	f.setConfigMapKeyName = key
	f.setConfigMapValue = value
	return f.setConfigMapKeyErr
}

func (f *fakeManagementPort) SetImage(_ context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName, name, container, image string, initContainer bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setImageCalled = true
	f.setImageID = id
	f.setImageKind = kind
	f.setImageNS = namespace
	f.setImageName = name
	f.setImageContainer = container
	f.setImageValue = image
	f.setImageInit = initContainer
	return f.setImageErr
}

func (f *fakeManagementPort) CordonNode(_ context.Context, id domain.ClusterID, name string, cordon bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cordonCalled = true
	f.cordonedID = id
	f.cordonedName = name
	f.cordonedValue = cordon
	return f.cordonErr
}
func (f *fakeManagementPort) EvictPod(_ context.Context, id domain.ClusterID, namespace domain.NamespaceName, name string, gracePeriodSeconds int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.evictCalled = true
	f.evictedID = id
	f.evictedNS = namespace
	f.evictedName = name
	f.evictedGrace = gracePeriodSeconds
	return f.evictErr
}
func (f *fakeManagementPort) DrainNode(_ context.Context, id domain.ClusterID, name string, opts domain.DrainOptions) (domain.DrainReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.drainCalled = true
	f.drainedID = id
	f.drainedName = name
	f.drainedOpts = opts
	return f.drainReport, f.drainErr
}

func (f *fakeManagementPort) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

// recordedCalls returns what reached the port, in order.
func (f *fakeManagementPort) recordedCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}
