package application

import (
	"sync"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// demandingOverview is an assessment that knows when each cluster was last
// asked for — the OverviewService's LastDemanded, settable by the test.
type demandingOverview struct {
	ports.OverviewService

	mu       sync.Mutex
	demanded map[domain.ClusterID]time.Time
}

func (o *demandingOverview) LastDemanded(id domain.ClusterID) time.Time {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.demanded[id]
}

func TestSamplerSlowsDownForAClusterNobodyIsLookingAt(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	overview := &demandingOverview{demanded: map[domain.ClusterID]time.Time{"front": start}}
	service, err := NewHistoryService(HistoryServiceDeps{
		History:  newCountingStore(),
		Overview: overview,
		Registry: NewRegistry(),
	})
	if err != nil {
		t.Fatalf("NewHistoryService() error = %v", err)
	}
	interval := service.SamplingInterval()
	background := domain.BackgroundSamplingInterval(interval)

	// Both are sampled on their first tick: nothing is owed yet.
	if !service.due("front", start) || !service.due("back", start) {
		t.Fatal("a cluster never sampled must be due")
	}

	// One interval later (with the tick a little early): the cluster in
	// front is due again, the one behind is not.
	next := start.Add(interval - interval/10)
	overview.mu.Lock()
	overview.demanded["front"] = next
	overview.mu.Unlock()
	if !service.due("front", next) {
		t.Error("the cluster on screen was skipped")
	}
	if service.due("back", next) {
		t.Error("the cluster behind was sampled at the foreground cadence")
	}

	if got := service.SampledEvery("back"); got != background {
		t.Errorf("SampledEvery(back) = %v, want %v", got, background)
	}

	// Once its background cadence has passed, the one behind is due.
	if !service.due("back", start.Add(background)) {
		t.Error("the cluster behind was not sampled after its background cadence")
	}
}

// TestSamplerWithoutDemandSamplesEveryTick pins the cadence for an assessment
// that cannot say who is looking: every interval, as before the gate existed.
func TestSamplerWithoutDemandSamplesEveryTick(t *testing.T) {
	t.Parallel()

	service, err := NewHistoryService(HistoryServiceDeps{
		History:  newCountingStore(),
		Overview: struct{ ports.OverviewService }{},
		Registry: NewRegistry(),
	})
	if err != nil {
		t.Fatalf("NewHistoryService() error = %v", err)
	}
	now := time.Now()
	interval := service.SamplingInterval()
	if !service.due("dev", now) || !service.due("dev", now.Add(interval)) {
		t.Fatal("a sampler that cannot tell demand must sample every tick")
	}
	if got := service.SampledEvery("dev"); got != interval {
		t.Fatalf("SampledEvery = %v, want %v", got, interval)
	}
}

func TestOverviewDemandIsTheTabsNotTheSamplers(t *testing.T) {
	t.Parallel()

	service := &OverviewService{}
	if !service.LastDemanded("dev").IsZero() {
		t.Fatal("a cluster nobody asked about has a demand")
	}
	service.noteDemand("dev")
	if service.LastDemanded("dev").IsZero() {
		t.Fatal("Overview's demand was not recorded")
	}
	service.forget("dev")
	if !service.LastDemanded("dev").IsZero() {
		t.Fatal("a disconnect kept the demand")
	}
}
