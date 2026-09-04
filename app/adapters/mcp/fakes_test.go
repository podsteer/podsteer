package mcp

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// testClock is the instant every test's ages are measured against, so an age
// in an assertion is a constant rather than something that moves while the
// suite runs.
var testClock = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

// secretManifest is what a fake cluster returns for a Secret when the caller
// asks for values, and it is the string every redaction test looks for.
//
// It is only ever returned when revealSecrets is TRUE, which no tool in this
// package passes — so finding it in a tool's answer means the redaction rule
// has been broken rather than that this fixture is unrealistic.
const secretValue = "aHVudGVyMg==" // "hunter2", base64 as Kubernetes stores it

// redactedManifest is what the Kubernetes adapter's maskSecretData produces:
// the decoded SIZE, never the encoded form, because base64 is an encoding and
// not a cipher.
const redactedManifest = `apiVersion: v1
kind: Secret
metadata:
  name: db
  namespace: shop
data:
  password: <hidden, 7 bytes>
type: Opaque
`

// revealedManifest is the same object with the value in it. Only a caller
// passing revealSecrets true can ever see this.
const revealedManifest = `apiVersion: v1
kind: Secret
metadata:
  name: db
  namespace: shop
data:
  password: ` + secretValue + `
type: Opaque
`

// stubReaders implements every reading interface the server takes.
//
// One type rather than eight, because most tests care about one call and
// need the other seven to exist. Each field lets a test override an answer or
// an error without a bespoke stub per case.
type stubReaders struct {
	// failWith is returned by every read, when set — how a test makes a whole
	// cluster refuse.
	failWith error

	clusters   []domain.Cluster
	connected  []domain.Cluster
	namespaces []domain.Namespace
	kinds      []domain.ResourceKind
	pods       []domain.Pod
	workloads  []domain.Workload
	nodes      []domain.Node
	events     []domain.Event
	table      domain.ResourceTable
	overview   domain.Overview
	graph      domain.PodGraph
	inventory  domain.NamespaceInventory
	rules      domain.SubjectRules
	decision   domain.AccessDecision
	inspection domain.RoleInspection
	logLines   []string

	// Recorded calls. Assertions read these rather than trusting a handler's
	// output to imply what it asked the cluster for.
	connects        []domain.ClusterID
	revealRequested []bool
	logOptions      []domain.LogOptions
	eventsFail      error
}

func (s *stubReaders) ListClusters(context.Context) ([]domain.Cluster, error) {
	return s.clusters, s.failWith
}

func (s *stubReaders) Connect(_ context.Context, id domain.ClusterID) (domain.Cluster, error) {
	s.connects = append(s.connects, id)
	if s.failWith != nil {
		return domain.Cluster{}, s.failWith
	}
	for _, cluster := range s.clusters {
		if cluster.ID() == id {
			s.connected = append(s.connected, cluster)
			return cluster, nil
		}
	}
	return domain.Cluster{}, domain.ErrClusterNotFound
}

func (s *stubReaders) Connections(context.Context) ([]domain.Cluster, error) {
	return s.connected, nil
}

func (s *stubReaders) ListNamespaces(context.Context, domain.ClusterID) ([]domain.Namespace, error) {
	return s.namespaces, s.failWith
}

func (s *stubReaders) ListNamespaceSummaries(context.Context, domain.ClusterID, domain.Projection) ([]domain.NamespaceSummary, error) {
	return nil, s.failWith
}

func (s *stubReaders) ListNodes(context.Context, domain.ClusterID, domain.Projection) ([]domain.Node, error) {
	return s.nodes, s.failWith
}

func (s *stubReaders) Kinds(context.Context, domain.ClusterID) ([]domain.ResourceKind, error) {
	return s.kinds, s.failWith
}

func (s *stubReaders) ListPods(context.Context, domain.ClusterID, domain.NamespaceName, domain.Projection) ([]domain.Pod, error) {
	return s.pods, s.failWith
}

func (s *stubReaders) ListPodsOnNode(context.Context, domain.ClusterID, string) ([]domain.Pod, error) {
	return s.pods, s.failWith
}

func (s *stubReaders) ListWorkloads(context.Context, domain.ClusterID, domain.WorkloadKind, domain.NamespaceName, domain.Projection) ([]domain.Workload, error) {
	return s.workloads, s.failWith
}

func (s *stubReaders) PodGraph(context.Context, domain.ClusterID, domain.NamespaceName, string) (domain.PodGraph, error) {
	return s.graph, s.failWith
}

func (s *stubReaders) WorkloadGraph(context.Context, domain.ClusterID, domain.NamespaceName, domain.WorkloadKind, string) (domain.PodGraph, error) {
	return s.graph, s.failWith
}

func (s *stubReaders) ListEvents(context.Context, domain.ClusterID, domain.NamespaceName, domain.Projection) ([]domain.Event, error) {
	return s.events, s.failWith
}

func (s *stubReaders) ListEventsForResource(context.Context, domain.ClusterID, domain.NamespaceName, string, string) ([]domain.Event, error) {
	if s.eventsFail != nil {
		return nil, s.eventsFail
	}
	return s.events, s.failWith
}

func (s *stubReaders) ListTable(context.Context, domain.ClusterID, string, domain.NamespaceName, domain.Projection) (domain.ResourceTable, error) {
	return s.table, s.failWith
}

// GetManifest answers the way the Kubernetes adapter does: the value is in
// the manifest only when the caller asked for it, and the request is recorded
// so a test can assert what was asked rather than only what came back.
func (s *stubReaders) GetManifest(_ context.Context, _ domain.ClusterID, _ string, _ domain.NamespaceName, _ string, revealSecrets bool) (string, error) {
	s.revealRequested = append(s.revealRequested, revealSecrets)
	if s.failWith != nil {
		return "", s.failWith
	}
	if revealSecrets {
		return revealedManifest, nil
	}
	return redactedManifest, nil
}

func (s *stubReaders) ObjectGraph(context.Context, domain.ClusterID, string, domain.NamespaceName, string) (domain.PodGraph, error) {
	return s.graph, s.failWith
}

func (s *stubReaders) NamespaceInventory(context.Context, domain.ClusterID, domain.NamespaceName) (domain.NamespaceInventory, error) {
	return s.inventory, s.failWith
}

func (s *stubReaders) Overview(context.Context, domain.ClusterID) (domain.Overview, error) {
	return s.overview, s.failWith
}

func (s *stubReaders) SubjectRules(context.Context, domain.ClusterID, domain.NamespaceName) (domain.SubjectRules, error) {
	return s.rules, s.failWith
}

func (s *stubReaders) CanI(context.Context, domain.ClusterID, domain.AccessRequest) (domain.AccessDecision, error) {
	return s.decision, s.failWith
}

func (s *stubReaders) InspectRole(context.Context, domain.ClusterID, domain.RoleTarget) (domain.RoleInspection, error) {
	return s.inspection, s.failWith
}

func (s *stubReaders) StreamLogs(_ context.Context, _ domain.ClusterID, _ domain.NamespaceName, _ string, _ string, opts domain.LogOptions, out chan<- string) error {
	defer close(out)
	s.logOptions = append(s.logOptions, opts)
	if s.failWith != nil {
		return s.failWith
	}
	for _, line := range s.logLines {
		out <- line
	}
	return nil
}

// newStub returns readers describing one small, healthy cluster.
func newStub(t *testing.T) *stubReaders {
	t.Helper()

	server, err := domain.NewServerEndpoint("https://kube.example:6443")
	if err != nil {
		t.Fatalf("building endpoint: %v", err)
	}
	cluster, err := domain.NewCluster(domain.ClusterSpec{
		ID:        "staging",
		Server:    server,
		IsCurrent: true,
	})
	if err != nil {
		t.Fatalf("building cluster: %v", err)
	}

	pod, err := domain.NewPod(domain.PodSpec{
		Name:      "web-1",
		Namespace: "shop",
		ClusterID: "staging",
		Phase:     domain.PodPhaseRunning,
		NodeName:  "node-a",
		Containers: []domain.Container{
			{Name: "web", Image: "registry.example/web:1.4.0", Ready: true, State: domain.ContainerStateRunning},
		},
		CreatedAt: testClock.Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("building pod: %v", err)
	}

	namespace, err := domain.NewNamespace("shop", domain.NamespacePhaseActive, testClock.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("building namespace: %v", err)
	}

	return &stubReaders{
		clusters:   []domain.Cluster{cluster},
		namespaces: []domain.Namespace{namespace},
		kinds: []domain.ResourceKind{
			{Group: "", Version: "v1", Resource: "pods", Kind: "Pod", Singular: "pod", Namespaced: true, Category: domain.CategoryWorkloads, Title: "Pods", Rich: true},
			{Group: "", Version: "v1", Resource: "secrets", Kind: "Secret", Singular: "secret", Namespaced: true, Category: domain.CategoryConfig, Title: "Secrets"},
			{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Singular: "deployment", Namespaced: true, Category: domain.CategoryWorkloads, Title: "Deployments", Rich: true},
			{Group: "", Version: "v1", Resource: "nodes", Kind: "Node", Singular: "node", Namespaced: false, Category: domain.CategoryCluster, Title: "Nodes", Rich: true},
		},
		pods:     []domain.Pod{pod},
		table:    domain.NewResourceTable(domain.ResourceKind{Version: "v1", Resource: "secrets", Kind: "Secret"}, nil, nil),
		logLines: []string{"2026-09-04T11:59:00Z starting", "2026-09-04T11:59:01Z ready"},
	}
}

// newServer builds a server over the given readers.
func newServer(t *testing.T, stub *stubReaders) *Server {
	t.Helper()

	server, err := New(Deps{
		Clusters:  stub,
		Kinds:     stub,
		Workloads: stub,
		Events:    stub,
		Resources: stub,
		Overview:  stub,
		RBAC:      stub,
		Logs:      stub,
		Version:   "0.0.0-test",
		Now:       func() time.Time { return testClock },
		// Discarded so a failing test's output is the assertion rather than
		// the server's own commentary about the failure it was given.
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("building server: %v", err)
	}
	return server
}

// call runs one tool by name with the given arguments, through the same
// validation and classification a real tools/call goes through.
func call(t *testing.T, server *Server, name string, args map[string]any) callResult {
	t.Helper()

	request := requestLine(t, 1, "tools/call", map[string]any{"name": name, "arguments": args})
	answer, replied := server.handle(context.Background(), request)
	if !replied {
		t.Fatalf("tools/call %s was not answered", name)
	}
	if answer.Error != nil {
		t.Fatalf("tools/call %s failed at the protocol level: %s", name, answer.Error.Message)
	}

	result, isCallResult := answer.Result.(callResult)
	if !isCallResult {
		t.Fatalf("tools/call %s returned %T, want callResult", name, answer.Result)
	}
	return result
}

// text returns the single text content of a tool result.
func resultText(t *testing.T, result callResult) string {
	t.Helper()
	if len(result.Content) != 1 {
		t.Fatalf("result carried %d content blocks, want 1", len(result.Content))
	}
	return result.Content[0].Text
}

// contains reports whether the result's text holds needle.
func contains(t *testing.T, result callResult, needle string) bool {
	t.Helper()
	return strings.Contains(resultText(t, result), needle)
}
