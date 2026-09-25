package wails

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

type stubQueries struct {
	result domain.BackendSeriesResult
	err    error
}

func (s stubQueries) Series(
	context.Context, domain.ClusterID, domain.MetricID, domain.MetricScope, time.Duration,
) (domain.BackendSeriesResult, error) {
	return s.result, s.err
}

// THERE IS NO QUERY BOX AND NO URL TO TYPE, and the bound surface is where
// that would first become possible. Asserted by reflection over the method's
// parameters, the way tools_test.go asserts the MCP surface, because a string
// parameter added later would compile perfectly and be discovered by whoever
// found their Prometheus melted.
func TestTheBoundSurfaceTakesNoExpressionAndNoURL(t *testing.T) {
	api := reflect.TypeOf(&MetricsQueryAPI{})

	method, found := api.MethodByName("GetSeries")
	if !found {
		t.Fatal("GetSeries is not bound")
	}

	// Receiver plus clusterID, metric, scope, windowMinutes — and nothing
	// else. A fifth parameter is a conversation, not a refactor.
	if got := method.Type.NumIn(); got != 5 {
		t.Fatalf("GetSeries takes %d parameters, want 5 (receiver, cluster, metric, scope, window)", got)
	}
	if got := method.Type.In(4).Kind(); got != reflect.Int {
		t.Fatalf("the fourth parameter is %s, want an int window", got)
	}

	// And no OTHER exported method may take one either: every exported method
	// of a bound service is callable from the page.
	for i := range api.NumMethod() {
		exported := api.Method(i)
		if exported.Name == "GetSeries" {
			continue
		}
		t.Fatalf("MetricsQueryAPI exposes %s; every exported method of a bound "+
			"service is callable from the page, so a new one needs arguing for", exported.Name)
	}
}

func newQueryAPI(t *testing.T, result domain.BackendSeriesResult) *MetricsQueryAPI {
	t.Helper()

	api, err := NewMetricsQueryAPI(stubQueries{result: result}, &App{}, nil)
	if err != nil {
		t.Fatalf("wiring: %v", err)
	}
	return api
}

// Every series that crosses the bridge carries its provenance, so no
// component can draw a backend's line as PodSteer's own.
func TestEverySeriesCrossesTheBridgeWithItsProvenance(t *testing.T) {
	now := time.Now().UTC()
	api := newQueryAPI(t, domain.BackendSeriesResult{
		Status: domain.BackendAnswered,
		Provenance: domain.SeriesProvenance{
			Origin:       domain.OriginBackend,
			Source:       "Prometheus in monitoring",
			Verification: domain.VerificationFleet,
			Filtered:     true,
		},
		Expression: `sum(container_memory_working_set_bytes{node=~"a"})`,
		Step:       time.Minute,
		Series: []domain.PromSeries{{
			Labels: map[string]string{"node": "node-a"},
			Points: []domain.SeriesPoint{{At: now.Add(-time.Hour), Value: 1}, {At: now, Value: 2}},
		}},
	})

	result, err := api.GetSeries("dev", string(domain.MetricMemory), string(domain.ScopeNode), 60)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	if result.Provenance.Origin != string(domain.OriginBackend) {
		t.Fatalf("origin %q, want backend", result.Provenance.Origin)
	}
	if result.Provenance.Source != "Prometheus in monitoring" {
		t.Fatalf("source %q", result.Provenance.Source)
	}
	if result.Provenance.Verification != string(domain.VerificationFleet) {
		t.Fatalf("verification %q, want fleet", result.Provenance.Verification)
	}
	if !result.Provenance.Filtered {
		t.Fatal("a narrowed series did not say so")
	}
	if result.Unit != "bytes" {
		t.Fatalf("unit %q, want bytes", result.Unit)
	}
	if len(result.Series) != 1 || result.Series[0].Label != "node-a" {
		t.Fatalf("series %+v", result.Series)
	}
	if result.SpanSeconds < 3500 || result.SpanSeconds > 3700 {
		t.Fatalf("span %ds, want about an hour", result.SpanSeconds)
	}
	if result.Expression == "" {
		t.Fatal("the expression that was sent did not cross the bridge")
	}
}

// answered-empty must survive the crossing: collapsed into answered on this
// side it is a blank chart under a green label on the other.
func TestAnsweredEmptyIsDistinctOnTheWire(t *testing.T) {
	api := newQueryAPI(t, domain.BackendSeriesResult{
		Status:  domain.BackendAnsweredEmpty,
		Message: "holds nothing for this cluster's nodes",
	})

	result, err := api.GetSeries("dev", string(domain.MetricCPU), "", 60)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if result.Status != string(domain.BackendAnsweredEmpty) {
		t.Fatalf("status %q, want answered-empty", result.Status)
	}
	if result.Status == string(domain.BackendAnswered) {
		t.Fatal("an empty answer arrived as an answer")
	}
	if result.Message == "" {
		t.Fatal("the message did not cross")
	}
}

// A nil slice marshals as null, and the chart tests its length on every
// render — the rule Overview.unavailable already carries.
func TestSeriesIsNeverNullOnTheWire(t *testing.T) {
	api := newQueryAPI(t, domain.NotEnabled())

	result, err := api.GetSeries("dev", string(domain.MetricCPU), string(domain.ScopeCluster), 0)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if result.Series == nil {
		t.Fatal("Series is nil and will arrive as null")
	}
	if result.Status != string(domain.BackendNotEnabled) {
		t.Fatalf("status %q, want not-enabled", result.Status)
	}
}
