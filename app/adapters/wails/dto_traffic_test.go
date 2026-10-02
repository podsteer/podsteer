package wails

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

// contractFields reads the field names of one interface in contract.ts.
func contractFields(t *testing.T, source, name string) []string {
	t.Helper()

	start := strings.Index(source, "export interface "+name+" {")
	if start < 0 {
		t.Fatalf("no interface %s", name)
	}
	body := source[start:]
	body = body[strings.Index(body, "{")+1 : strings.Index(body, "\n}")]

	// Hand-written two-space fields, or the generator's quoted ones.
	field := regexp.MustCompile(`(?m)^\s+"?(\w+)"?\??:`)
	var names []string
	for _, match := range field.FindAllStringSubmatch(body, -1) {
		names = append(names, match[1])
	}
	sort.Strings(names)
	return names
}

func jsonFields(value any) []string {
	kind := reflect.TypeOf(value)
	names := make([]string, 0, kind.NumField())
	for i := range kind.NumField() {
		tag := strings.Split(kind.Field(i).Tag.Get("json"), ",")[0]
		names = append(names, tag)
	}
	sort.Strings(names)
	return names
}

// THE CONTRACT WAS WRITTEN FIRST, and the interface was built against it at
// the same time as this file, so a renamed tag here is a silently missing
// field there. Read from the file itself rather than restated.
func TestTrafficDTOsMatchTheContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "src", "lib", "topology", "contract.ts"))
	if err != nil {
		t.Fatalf("reading contract.ts: %v", err)
	}
	source := string(raw)

	// A type contract.ts re-exports from the bindings is checked against the
	// generated declaration it re-exports.
	generated, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "src", "lib", "bindings",
		"github.com", "podsteer", "podsteer", "app", "adapters", "wails", "models.ts"))
	if err != nil {
		t.Fatalf("reading the generated models: %v", err)
	}
	reexported := map[string]bool{}
	for _, match := range regexp.MustCompile(`(?m)^export type \{([^}]*)\}`).FindAllStringSubmatch(source, -1) {
		for _, exported := range strings.Split(match[1], ",") {
			reexported[strings.TrimSpace(exported)] = true
		}
	}

	for name, value := range map[string]any{
		"TrafficSourceStatus": TrafficSourceStatus{},
		"TrafficSources":      TrafficSources{},
		"TrafficEndpoint":     TrafficEndpoint{},
		"TrafficEdge":         TrafficEdge{},
		"TrafficLayer":        TrafficLayer{},
	} {
		from := source
		if !strings.Contains(source, "export interface "+name+" {") && reexported[name] {
			from = string(generated)
		}
		want, got := contractFields(t, from, name), jsonFields(value)
		if !reflect.DeepEqual(want, got) {
			t.Errorf("%s: contract.ts has %v, the DTO has %v", name, want, got)
		}
	}
}

type stubTraffic struct {
	layer   domain.TrafficLayer
	sources domain.TrafficSources
	calls   int
	scope   []domain.NamespaceName
}

func (s *stubTraffic) Sources(context.Context, domain.ClusterID) (domain.TrafficSources, error) {
	s.calls++
	return s.sources, nil
}

func (s *stubTraffic) Traffic(_ context.Context, _ domain.ClusterID, namespaces []domain.NamespaceName, _ bool, _ domain.TrafficSource, _ domain.TrafficWindow) (domain.TrafficLayer, error) {
	s.calls++
	s.scope = namespaces
	return s.layer, nil
}

func TestAnEmptyLayerCrossesAsArraysNotNull(t *testing.T) {
	stub := &stubTraffic{layer: domain.TrafficLayer{Source: domain.TrafficIstio, Status: domain.BackendNotEnabled}}
	api, err := NewTrafficAPI(stub, &App{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	layer, err := api.Traffic("dev", nil, true, "istio", "5m")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(layer)
	for _, field := range []string{`"edges":[]`, `"unmapped":[]`, `"expressions":[]`} {
		if !strings.Contains(string(encoded), field) {
			t.Errorf("%s missing from %s", field, encoded)
		}
	}
}

func TestTrafficRefusesAnythingOutsideTheFixedSetsBeforeAsking(t *testing.T) {
	stub := &stubTraffic{}
	api, err := NewTrafficAPI(stub, &App{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		namespaces     []string
		source, window string
	}{
		{nil, "prometheus", "5m"},
		{nil, "istio", "5m]) or vector(1"},
		{[]string{`shop"}`}, "istio", "5m"},
		{[]string{}, "istio", "5m"},
	}
	for _, c := range cases {
		if _, err := api.Traffic("dev", c.namespaces, false, c.source, c.window); err == nil {
			t.Errorf("accepted %+v", c)
		}
	}
	if stub.calls != 0 {
		t.Fatalf("%d calls reached the service", stub.calls)
	}

	if _, err := api.Traffic("dev", []string{"shop", "pay"}, false, "linkerd", ""); err != nil {
		t.Fatal(err)
	}
	if len(stub.scope) != 2 {
		t.Errorf("scope %v", stub.scope)
	}
}

// As MetricsQueryAPI: the bound surface takes no expression and no URL.
func TestTheTrafficSurfaceIsTwoMethods(t *testing.T) {
	api := reflect.TypeOf(&TrafficAPI{})
	var names []string
	for i := range api.NumMethod() {
		names = append(names, api.Method(i).Name)
	}
	if !reflect.DeepEqual(names, []string{"Sources", "Traffic"}) {
		t.Fatalf("TrafficAPI exposes %v; every exported method of a bound service is callable from the page", names)
	}
}
