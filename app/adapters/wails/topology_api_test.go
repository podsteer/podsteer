package wails

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/podsteer/podsteer/app/domain"
)

// fakeTopologyService records what the API asked and hands back a graph.
type fakeTopologyService struct {
	mu       sync.Mutex
	scope    domain.TopologyScope
	graph    domain.TopologyGraph
	released []domain.ClusterID
	sub      func(domain.ClusterChange)
}

func (f *fakeTopologyService) Topology(_ context.Context, _ domain.ClusterID, scope domain.TopologyScope) (domain.TopologyGraph, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scope = scope
	return f.graph, nil
}

func (f *fakeTopologyService) Subscribe(fn func(domain.ClusterChange)) func() {
	f.mu.Lock()
	f.sub = fn
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		f.sub = nil
		f.mu.Unlock()
	}
}

func (f *fakeTopologyService) Release(id domain.ClusterID) {
	f.mu.Lock()
	f.released = append(f.released, id)
	f.mu.Unlock()
}

func newTestTopologyAPI(t *testing.T, service *fakeTopologyService) *TopologyAPI {
	t.Helper()
	api, err := NewTopologyAPI(service, NewApp(nil, 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	return api
}

func TestTopologyAPIValidatesAndConverts(t *testing.T) {
	service := &fakeTopologyService{graph: domain.TopologyGraph{
		Nodes: []domain.TopologyNode{{
			ID: "fold/replicaset/shop/web-1/pod", Kind: domain.GraphPod, APIKind: "Pod", State: domain.StateWarn,
			PodSummary: &domain.TopologyPodSummary{Total: 3, Ready: 2, Unhealthy: 1},
		}},
		Edges:       []domain.TopologyEdge{{From: "a", To: "b", Kind: domain.EdgePolicySelects, Label: "selects"}},
		Counts:      map[string]int{"Pod": 3},
		GeneratedAt: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC),
		Summarised:  true,
	}}
	api := newTestTopologyAPI(t, service)

	if _, err := api.Topology("dev", nil, false); err == nil {
		t.Error("an empty scope must be refused")
	}
	if code, _ := classifyError(domain.ErrEmptyTopologyScope); code != CodeInvalidInput {
		t.Errorf("empty scope code = %q", code)
	}

	graph, err := api.Topology("dev", []string{"shop"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(service.scope.Namespaces) != 1 || service.scope.All {
		t.Errorf("scope = %+v", service.scope)
	}
	node := graph.Nodes[0]
	if node.State != NodeState(domain.StateWarn) || node.PodSummary == nil || node.PodSummary.Unhealthy != 1 {
		t.Errorf("node = %+v", node)
	}
	if graph.Edges[0].Kind != TopologyEdgeKind(domain.EdgePolicySelects) || graph.GeneratedAt != "2026-10-02T12:00:00Z" || !graph.Summarised {
		t.Errorf("graph = %+v", graph)
	}
	if graph.Unreadable == nil {
		t.Error("unreadable must serialise as [] not null")
	}

	if err := api.Release("dev"); err != nil || len(service.released) != 1 {
		t.Errorf("release = %v, %v", err, service.released)
	}
}

// TestTopologyDTOMatchesTheContract pins the JSON names contract.ts declares.
func TestTopologyDTOMatchesTheContract(t *testing.T) {
	raw, err := json.Marshal(TopologyGraph{
		Nodes:  []TopologyNode{{Labels: map[string]string{"a": "b"}, PodSummary: &TopologyPodSummary{}}},
		Edges:  []TopologyEdge{{}},
		Counts: map[string]int{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var shape struct {
		Nodes []map[string]json.RawMessage `json:"nodes"`
		Edges []map[string]json.RawMessage `json:"edges"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	_ = json.Unmarshal(raw, &top)

	expect := func(what string, got map[string]json.RawMessage, keys ...string) {
		t.Helper()
		if len(got) != len(keys) {
			t.Errorf("%s has %d fields, want %d: %s", what, len(got), len(keys), raw)
		}
		for _, key := range keys {
			if _, ok := got[key]; !ok {
				t.Errorf("%s lacks %q", what, key)
			}
		}
	}
	expect("graph", top, "nodes", "edges", "counts", "unreadable", "bounded", "summarised", "generatedAt")
	expect("node", shape.Nodes[0], "id", "kind", "apiKind", "name", "namespace", "state", "detail", "group", "labels", "podSummary")
	expect("edge", shape.Edges[0], "from", "to", "kind", "label")

	var summary map[string]json.RawMessage
	_ = json.Unmarshal(shape.Nodes[0]["podSummary"], &summary)
	expect("podSummary", summary, "total", "ready", "unhealthy")

	changed, _ := json.Marshal(TopologyChanged{})
	var event map[string]json.RawMessage
	_ = json.Unmarshal(changed, &event)
	expect("topology:changed", event, "clusterId", "namespaces")

	// The wire values are the domain's, verbatim; contract.ts spells them.
	states := []domain.NodeState{domain.StateOK, domain.StateWarn, domain.StateBad, domain.StateNeutral}
	if fmt.Sprint(states) != "[ok warn bad neutral]" {
		t.Errorf("states = %v", states)
	}
	kinds := []domain.EdgeKind{
		domain.EdgeOwns, domain.EdgeSelects, domain.EdgeRoutes, domain.EdgeScales,
		domain.EdgeProtects, domain.EdgePolicySelects, domain.EdgeAttaches, domain.EdgeRunsAs,
	}
	if fmt.Sprint(kinds) != "[owns selects routes scales protects policy-selects attaches runs-as]" {
		t.Errorf("edge kinds = %v", kinds)
	}
}

// TestTopologyPayloadBudget holds a 5000-node topology under 2.5 MB on the
// bridge, with realistic names, labels on top-level objects and edges.
func TestTopologyPayloadBudget(t *testing.T) {
	const budget = 2_500_000
	var graph domain.TopologyGraph
	for i := range 5000 {
		node := domain.TopologyNode{
			ID:   fmt.Sprintf("pod/payments/checkout-api-7d9f8c6b5-%05d", i),
			Kind: domain.GraphPod, APIKind: "Pod", Name: fmt.Sprintf("checkout-api-7d9f8c6b5-%05d", i),
			Namespace: "payments", State: domain.StateOK, Detail: "Running",
			Group: "replicaset/payments/checkout-api-7d9f8c6b5",
		}
		if i%10 == 0 {
			node.Labels = map[string]string{
				"app.kubernetes.io/name": "checkout-api", "app.kubernetes.io/instance": "checkout",
				"app.kubernetes.io/part-of": "payments", "app.kubernetes.io/managed-by": "Helm",
			}
		}
		graph.Nodes = append(graph.Nodes, node)
		graph.Edges = append(graph.Edges,
			domain.TopologyEdge{From: node.Group, To: node.ID, Kind: domain.EdgeOwns},
			domain.TopologyEdge{From: "service/payments/checkout-api", To: node.ID, Kind: domain.EdgeSelects},
		)
	}
	raw, err := json.Marshal(toTopologyGraph(graph))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("5000 nodes, 10000 edges: %d bytes", len(raw))
	if len(raw) > budget {
		t.Errorf("payload %d bytes exceeds the %d budget", len(raw), budget)
	}
}

func TestTopologyChangesBecomeEvents(t *testing.T) {
	service := &fakeTopologyService{}
	api := newTestTopologyAPI(t, service)
	if err := api.ServiceStartup(context.Background(), application.ServiceOptions{}); err != nil {
		t.Fatal(err)
	}
	service.mu.Lock()
	sub := service.sub
	service.mu.Unlock()
	if sub == nil {
		t.Fatal("ServiceStartup did not subscribe")
	}
	// No window in a test: the emit is dropped with a debug line, not a panic.
	sub(domain.ClusterChange{ClusterID: "dev", Namespaces: []domain.NamespaceName{"shop"}})

	if got := toTopologyChanged(domain.ClusterChange{ClusterID: "dev"}); got.Namespaces == nil || got.ClusterID != "dev" {
		t.Errorf("changed = %+v; namespaces must be [] not null", got)
	}
	_ = api.ServiceShutdown()
	if service.sub != nil {
		t.Error("ServiceShutdown did not unsubscribe")
	}
}

func TestExportTopologyPNG(t *testing.T) {
	api := newTestTopologyAPI(t, &fakeTopologyService{})
	dir := t.TempDir()
	path := filepath.Join(dir, "map.png")
	api.chooseSavePath = func(name string) (string, error) {
		if name != "map.png" {
			t.Errorf("suggested %q", name)
		}
		return path, nil
	}
	png := append(append([]byte{}, pngSignature...), 1, 2, 3)

	got, err := api.ExportTopologyPNG("map.png", "data:image/png;base64,"+base64.StdEncoding.EncodeToString(png))
	if err != nil || got != path {
		t.Fatalf("export = %q, %v", got, err)
	}
	written, err := os.ReadFile(path)
	if err != nil || string(written) != string(png) {
		t.Fatalf("written %v, %v", written, err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}

	if _, err := api.ExportTopologyPNG("map.png", base64.StdEncoding.EncodeToString([]byte("<svg/>"))); err == nil {
		t.Error("a non-PNG was written")
	}
	if _, err := decodePNG("%%%"); !errors.Is(err, errNotPNG) {
		t.Errorf("garbage err = %v", err)
	}
	if code, _ := classifyError(errNotPNG); code != CodeInvalidInput {
		t.Errorf("code = %q", code)
	}

	api.chooseSavePath = func(string) (string, error) { return "", nil }
	if got, err := api.ExportTopologyPNG("map.png", base64.StdEncoding.EncodeToString(png)); err != nil || got != "" {
		t.Errorf("cancel = %q, %v", got, err)
	}
	if title, filters := saveDialogFor("map.png"); title == "Save" || len(filters) != 1 {
		t.Errorf("png dialog = %q %v", title, filters)
	}
}
