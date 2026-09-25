package application

// Cadence tests for the sampler.
//
// These live in `package application` rather than beside the rest of the
// history tests in `package application_test`, because they substitute the
// tick source — an unexported field — for a channel the test drives by hand.
//
// That substitution is the whole point. The cadence floor is ten seconds, so a
// test that waited for real ticks would sleep for the better part of a minute
// to observe two of them, and nobody would run it. Driving the ticks makes the
// question "does it fire once per tick, at the interval that was configured,
// and does changing that interval take effect" answerable in microseconds.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// tickerSpy stands in for the sampler's tick source. It records every cadence
// the sampler asks for and delivers ticks only when a test says so.
type tickerSpy struct {
	mu        sync.Mutex
	requested []time.Duration

	ticks chan time.Time
	built chan time.Duration
}

func newTickerSpy() *tickerSpy {
	return &tickerSpy{
		ticks: make(chan time.Time),
		built: make(chan time.Duration, 8),
	}
}

func (spy *tickerSpy) build(d time.Duration) (<-chan time.Time, func()) {
	spy.mu.Lock()
	spy.requested = append(spy.requested, d)
	spy.mu.Unlock()

	select {
	case spy.built <- d:
	default:
	}
	return spy.ticks, func() {}
}

// tick delivers one tick and blocks until the sampler has taken it, so a test
// never races ahead of the loop it is driving.
func (spy *tickerSpy) tick(t *testing.T) {
	t.Helper()
	select {
	case spy.ticks <- time.Now():
	case <-time.After(2 * time.Second):
		t.Fatal("sampler did not accept a tick within 2s")
	}
}

// nextBuild returns the cadence of the next ticker the sampler builds.
func (spy *tickerSpy) nextBuild(t *testing.T) time.Duration {
	t.Helper()
	select {
	case d := <-spy.built:
		return d
	case <-time.After(2 * time.Second):
		t.Fatal("sampler did not build a ticker within 2s")
		return 0
	}
}

// countingStore records how many samples the sampler wrote.
type countingStore struct {
	mu       sync.Mutex
	appended int
	written  chan struct{}
}

var _ ports.HistoryPort = (*countingStore)(nil)

func newCountingStore() *countingStore {
	return &countingStore{written: make(chan struct{}, 32)}
}

func (s *countingStore) Append(context.Context, domain.ClusterID, domain.Sample) error {
	s.mu.Lock()
	s.appended++
	s.mu.Unlock()

	select {
	case s.written <- struct{}{}:
	default:
	}
	return nil
}

func (s *countingStore) Series(context.Context, domain.ClusterID, time.Time) (domain.Series, error) {
	return nil, nil
}
func (s *countingStore) Prune(context.Context, time.Time) error         { return nil }
func (s *countingStore) Forget(context.Context, domain.ClusterID) error { return nil }

func (s *countingStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appended
}

// awaitWrite blocks until one more sample has been written.
func (s *countingStore) awaitWrite(t *testing.T) {
	t.Helper()
	select {
	case <-s.written:
	case <-time.After(2 * time.Second):
		t.Fatal("no sample was written within 2s of a tick")
	}
}

// fixedOverview is an assessment that costs nothing to produce.
type fixedOverview struct{}

var _ ports.OverviewService = (*fixedOverview)(nil)

func (fixedOverview) Overview(context.Context, domain.ClusterID) (domain.Overview, error) {
	return domain.Overview{
		GeneratedAt: time.Now().UTC(),
		Capacity: domain.CapacitySummary{
			CPU:  domain.ResourceUsage{Allocatable: 4000, Requests: 2000, PodUsage: 500, Measured: true},
			Pods: domain.PodCapacity{Scheduled: 7},
		},
		Nodes: domain.NodeSummary{Ready: 1, Total: 1},
	}, nil
}

func (f fixedOverview) OverviewForTarget(ctx context.Context, id domain.ClusterID, _ string) (domain.Overview, error) {
	return f.Overview(ctx, id)
}

// startSampler wires a service around the spy and starts it, returning once the
// sampler's initial ticker exists so a test can drive ticks without racing it.
func startSampler(t *testing.T, interval time.Duration) (*HistoryService, *tickerSpy, *countingStore) {
	t.Helper()

	endpoint, err := domain.NewServerEndpoint("https://dev.example.com:6443")
	if err != nil {
		t.Fatalf("building endpoint: %v", err)
	}
	cluster, err := domain.NewCluster(domain.ClusterSpec{
		ID:        domain.ClusterID("dev"),
		Server:    endpoint,
		IsCurrent: true,
	})
	if err != nil {
		t.Fatalf("building cluster: %v", err)
	}

	registry := NewRegistry()
	registry.Open(cluster)

	store := newCountingStore()
	service, err := NewHistoryService(HistoryServiceDeps{
		History:  store,
		Overview: fixedOverview{},
		Registry: registry,
		// No SettingsPath: the cadence under test is the one this test sets,
		// never one a previous run left on disk.
	})
	if err != nil {
		t.Fatalf("NewHistoryService() error = %v", err)
	}

	// Set directly rather than through SetSamplingInterval, which would also
	// queue a reconfigure. The sampler would then rebuild its ticker once at
	// startup for no reason, and a test watching for the rebuild that a LATER
	// change causes would match that one instead.
	service.interval = domain.NewSamplingInterval(interval)

	spy := newTickerSpy()
	service.newTicker = spy.build

	service.Start(context.Background())
	t.Cleanup(service.Close)

	// The sampler writes one sample immediately, before it waits on a tick.
	store.awaitWrite(t)

	return service, spy, store
}

// The cadence the operator chose in Settings → Data is the cadence the sampler
// actually asks for. Without this, a service could clamp or default silently
// and nothing would notice.
func TestSamplerBuildsItsTickerAtTheConfiguredInterval(t *testing.T) {
	t.Parallel()

	_, spy, _ := startSampler(t, time.Minute)

	if got := spy.nextBuild(t); got != time.Minute {
		t.Errorf("sampler built its ticker at %v, want %v", got, time.Minute)
	}
}

// One tick, one round of samples — the property the whole feature rests on. A
// sampler that fired twice per tick would double the load it puts on an API
// server; one that skipped ticks would leave gaps in a chart that claims to be
// evenly spaced.
func TestSamplerRecordsExactlyOncePerTick(t *testing.T) {
	t.Parallel()

	_, spy, store := startSampler(t, 30*time.Second)
	spy.nextBuild(t)

	const ticks = 4
	for range ticks {
		spy.tick(t)
		store.awaitWrite(t)
	}

	// One for the sample taken at startup, then one per tick.
	if want := ticks + 1; store.count() != want {
		t.Errorf("recorded %d samples after %d ticks, want %d", store.count(), ticks, want)
	}
}

// Changing the cadence has to take effect now rather than after the old,
// longer tick has elapsed — an operator slowing sampling down to spare an API
// server means now, and one speeding it up is usually watching something
// happen. Asserting the REBUILD is what proves it: a Reset would keep the
// remainder of the old interval running.
func TestChangingTheIntervalRebuildsTheTickerImmediately(t *testing.T) {
	t.Parallel()

	service, spy, _ := startSampler(t, 30*time.Second)

	if got := spy.nextBuild(t); got != 30*time.Second {
		t.Fatalf("initial ticker built at %v, want 30s", got)
	}

	if err := service.SetSamplingInterval(5 * time.Minute); err != nil {
		t.Fatalf("SetSamplingInterval() error = %v", err)
	}

	if got := spy.nextBuild(t); got != 5*time.Minute {
		t.Errorf("ticker rebuilt at %v after the interval changed, want 5m", got)
	}
	if got := service.SamplingInterval(); got != 5*time.Minute {
		t.Errorf("SamplingInterval() = %v, want 5m", got)
	}
}

// A cadence outside the supported range must be clamped before it reaches the
// ticker, not after. A zero or negative duration reaching time.NewTicker
// panics, which would take the sampler goroutine — and with it every future
// sample — down with it.
func TestOutOfRangeIntervalsNeverReachTheTicker(t *testing.T) {
	t.Parallel()

	service, spy, _ := startSampler(t, 30*time.Second)
	spy.nextBuild(t)

	for _, interval := range []time.Duration{0, -time.Second, time.Millisecond, 24 * time.Hour} {
		if err := service.SetSamplingInterval(interval); err != nil {
			t.Fatalf("SetSamplingInterval(%v) error = %v", interval, err)
		}

		got := spy.nextBuild(t)
		if got < domain.MinSamplingInterval || got > domain.MaxSamplingInterval {
			t.Errorf("SetSamplingInterval(%v) built a ticker at %v, outside [%v, %v]",
				interval, got, domain.MinSamplingInterval, domain.MaxSamplingInterval)
		}
	}
}

// Recording turned off means no sample is written, on a tick as much as at
// startup — the setting governs what reaches the disk, so a tick must not be
// able to slip one past it.
func TestTicksWriteNothingWhenRecordingIsOff(t *testing.T) {
	t.Parallel()

	service, spy, store := startSampler(t, 30*time.Second)
	spy.nextBuild(t)

	if err := service.SetRetention(context.Background(), domain.NewRetention(0)); err != nil {
		t.Fatalf("SetRetention() error = %v", err)
	}
	before := store.count()

	spy.tick(t)
	// Nothing to wait on when nothing is written, so drive a second tick: the
	// sampler can only accept it once it has finished handling the first.
	spy.tick(t)

	if store.count() != before {
		t.Errorf("recorded %d samples with recording off, want %d", store.count(), before)
	}
}
