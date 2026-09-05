package application_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// recordingHistory captures what the sampler writes.
type recordingHistory struct {
	mu       sync.Mutex
	appended []domain.Sample
	pruned   []time.Time
	written  chan struct{}
}

var _ ports.HistoryPort = (*recordingHistory)(nil)

func newRecordingHistory() *recordingHistory {
	return &recordingHistory{written: make(chan struct{}, 16)}
}

func (h *recordingHistory) Append(_ context.Context, _ domain.ClusterID, sample domain.Sample) error {
	h.mu.Lock()
	h.appended = append(h.appended, sample)
	h.mu.Unlock()

	select {
	case h.written <- struct{}{}:
	default:
	}
	return nil
}

func (h *recordingHistory) Series(context.Context, domain.ClusterID, time.Time) (domain.Series, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append(domain.Series(nil), h.appended...), nil
}

func (h *recordingHistory) Prune(_ context.Context, cutoff time.Time) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pruned = append(h.pruned, cutoff)
	return nil
}

func (h *recordingHistory) Forget(context.Context, domain.ClusterID) error { return nil }

func (h *recordingHistory) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.appended)
}

// stubOverview returns a fixed assessment without touching a cluster.
type stubOverview struct{}

var _ ports.OverviewService = (*stubOverview)(nil)

func (stubOverview) Overview(context.Context, domain.ClusterID) (domain.Overview, error) {
	return domain.Overview{
		GeneratedAt: time.Now().UTC(),
		Capacity: domain.CapacitySummary{
			CPU:  domain.ResourceUsage{Allocatable: 4000, Requests: 2000, PodUsage: 500, Measured: true},
			Pods: domain.PodCapacity{Scheduled: 7},
		},
		Nodes: domain.NodeSummary{Ready: 1, Total: 1},
	}, nil
}

func (s stubOverview) OverviewForTarget(ctx context.Context, id domain.ClusterID, _ string) (domain.Overview, error) {
	return s.Overview(ctx, id)
}

// memorySettings is a settings store that keeps one value.
//
// It stands in for the file-backed one so that the SERVICE'S half of the
// contract can be asserted here — that it reads the recording policy when it
// is constructed, and writes a change back through Update rather than
// replacing a whole document — while the file's own behaviour (the envelope,
// the atomic write, the malformed and from-the-future cases) is asserted in
// app/adapters/settings, where it belongs.
type memorySettings struct {
	mu    sync.Mutex
	value domain.Settings
	// updates counts the read-modify-writes, so a test can assert a change
	// went through one rather than being applied only in memory.
	updates int
}

func newMemorySettings() *memorySettings {
	return &memorySettings{value: domain.DefaultSettings()}
}

func (m *memorySettings) Load(context.Context) (domain.Settings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.value.Clone(), nil
}

func (m *memorySettings) Update(
	_ context.Context,
	mutate func(*domain.Settings) error,
) (domain.Settings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	next := m.value.Clone()
	if err := mutate(&next); err != nil {
		return domain.Settings{}, err
	}
	next.Normalise()
	m.value = next
	m.updates++
	return next.Clone(), nil
}

// refusingSettings refuses every write, the way the store does under
// `podsteer mcp` and against a file from a newer PodSteer.
type refusingSettings struct{ *memorySettings }

func (refusingSettings) Update(context.Context, func(*domain.Settings) error) (domain.Settings, error) {
	return domain.Settings{}, ports.ErrSettingsReadOnly
}

// newHistoryService wires a service around the fakes, with one cluster open.
func newHistoryService(
	t *testing.T,
	store ports.HistoryPort,
	settings application.HistorySettingsStore,
) *application.HistoryService {
	t.Helper()

	registry := application.NewRegistry()
	registry.Open(mustCluster(t, "dev", true))

	service, err := application.NewHistoryService(application.HistoryServiceDeps{
		History:  store,
		Overview: stubOverview{},
		Registry: registry,
		Settings: settings,
	})
	if err != nil {
		t.Fatalf("NewHistoryService() error = %v", err)
	}
	return service
}

func TestNewHistoryServiceRejectsMissingDependencies(t *testing.T) {
	t.Parallel()

	full := application.HistoryServiceDeps{
		History:  newRecordingHistory(),
		Overview: stubOverview{},
		Registry: application.NewRegistry(),
	}

	tests := map[string]func(*application.HistoryServiceDeps){
		"no history port":     func(d *application.HistoryServiceDeps) { d.History = nil },
		"no overview service": func(d *application.HistoryServiceDeps) { d.Overview = nil },
		"no registry":         func(d *application.HistoryServiceDeps) { d.Registry = nil },
	}

	for name, breakIt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			deps := full
			breakIt(&deps)
			if _, err := application.NewHistoryService(deps); err == nil {
				t.Error("expected an error for an incompletely wired service")
			}
		})
	}
}

// A sample within seconds of launch, rather than after a full interval, is
// what stops the chart being empty when somebody opens the dashboard.
func TestSamplerRecordsImmediatelyOnStart(t *testing.T) {
	t.Parallel()

	store := newRecordingHistory()
	service := newHistoryService(t, store, nil)
	if err := service.SetRetention(context.Background(), domain.NewRetention(1)); err != nil {
		t.Fatalf("SetRetention() error = %v", err)
	}

	service.Start(context.Background())
	defer service.Close()

	select {
	case <-store.written:
	case <-time.After(5 * time.Second):
		t.Fatal("no sample was recorded within five seconds of starting")
	}

	if store.count() == 0 {
		t.Error("expected at least one sample")
	}
}

// "Don't record" has to mean the sampler writes nothing at all, not that it
// writes and prunes immediately afterwards.
func TestSamplerWritesNothingWhenRecordingIsOff(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		store := newRecordingHistory()
		service := newHistoryService(t, store, nil)
		if err := service.SetRetention(context.Background(), domain.NewRetention(0)); err != nil {
			t.Fatalf("SetRetention() error = %v", err)
		}

		service.Start(context.Background())
		defer service.Close()

		// A fake clock, so this costs no wall-clock time and — far more
		// importantly — actually reaches a tick. The old form waited 250ms, but
		// the cadence floor is ten seconds, so it never covered a single one: it
		// proved only that nothing was written AT STARTUP, and a sampler that
		// ignored the setting on every subsequent tick would have passed it.
		// An hour here is many ticks at any supported cadence.
		time.Sleep(time.Hour)

		// Wait until every goroutine in the bubble is durably blocked, so "nothing
		// was written" is a settled fact rather than a race the test happened to
		// win.
		synctest.Wait()

		select {
		case <-store.written:
			t.Fatal("a sample was written while recording was turned off")
		default:
		}

		if store.count() != 0 {
			t.Errorf("appended = %d samples, want 0", store.count())
		}

	})
}

func TestSamplingIntervalIsClamped(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		set  time.Duration
		want time.Duration
	}{
		{name: "zero falls back to the default", set: 0, want: domain.DefaultSamplingInterval},
		{name: "below the floor is raised", set: time.Second, want: domain.MinSamplingInterval},
		{name: "above the ceiling is lowered", set: time.Hour, want: domain.MaxSamplingInterval},
		{name: "a supported value survives", set: time.Minute, want: time.Minute},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			service := newHistoryService(t, newRecordingHistory(), nil)
			if err := service.SetSamplingInterval(test.set); err != nil {
				t.Fatalf("SetSamplingInterval() error = %v", err)
			}
			if got := service.SamplingInterval(); got != test.want {
				t.Errorf("interval = %v, want %v", got, test.want)
			}
		})
	}
}

// Changing the cadence while the sampler is running must not block on it, or a
// click in Settings would hang the UI for up to a full interval.
func TestSetSamplingIntervalDoesNotBlockOnTheSampler(t *testing.T) {
	t.Parallel()

	service := newHistoryService(t, newRecordingHistory(), nil)
	service.Start(context.Background())
	defer service.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Several in a row: the reconfigure signal is buffered at one, so this
		// also proves a burst collapses rather than deadlocking.
		for _, interval := range []time.Duration{time.Minute, 30 * time.Second, 5 * time.Minute} {
			if err := service.SetSamplingInterval(interval); err != nil {
				t.Errorf("SetSamplingInterval() error = %v", err)
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SetSamplingInterval blocked")
	}

	if got := service.SamplingInterval(); got != 5*time.Minute {
		t.Errorf("interval = %v, want the last value set", got)
	}
}

// Both settings survive a restart, and one store carries them together.
func TestSettingsPersistAcrossRestart(t *testing.T) {
	t.Parallel()

	settings := newMemorySettings()

	first := newHistoryService(t, newRecordingHistory(), settings)
	if err := first.SetRetention(context.Background(), domain.NewRetention(7)); err != nil {
		t.Fatalf("SetRetention() error = %v", err)
	}
	if err := first.SetSamplingInterval(5 * time.Minute); err != nil {
		t.Fatalf("SetSamplingInterval() error = %v", err)
	}

	second := newHistoryService(t, newRecordingHistory(), settings)

	retention, err := second.Retention(context.Background())
	if err != nil {
		t.Fatalf("Retention() error = %v", err)
	}
	if retention.Days != 7 {
		t.Errorf("retention = %d days, want 7", retention.Days)
	}
	if got := second.SamplingInterval(); got != 5*time.Minute {
		t.Errorf("interval = %v, want 5m", got)
	}
}

// Each change is a read-modify-write on the shared settings, never a
// whole-document replacement.
//
// This is the bug the old `history.json` had and could not show: it marshalled
// its two integers and called os.WriteFile, so anything else in the file was
// discarded on every retention change. Nothing else was in the file yet. Now
// there is, and the guard is that a change to retention leaves an unrelated
// section exactly as it was.
func TestChangingRetentionLeavesTheRestOfTheSettingsAlone(t *testing.T) {
	t.Parallel()

	settings := newMemorySettings()
	if _, err := settings.Update(context.Background(), func(s *domain.Settings) error {
		s.Kubeconfig.Sources = []domain.KubeconfigSource{
			{Path: filepath.Join(t.TempDir(), "team"), Kind: domain.SourceDirectory},
		}
		return nil
	}); err != nil {
		t.Fatalf("seeding settings: %v", err)
	}

	service := newHistoryService(t, newRecordingHistory(), settings)
	if err := service.SetRetention(context.Background(), domain.NewRetention(3)); err != nil {
		t.Fatalf("SetRetention() error = %v", err)
	}

	stored, err := settings.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stored.History.Retention.Days != 3 {
		t.Errorf("retention = %d days, want 3", stored.History.Retention.Days)
	}
	if len(stored.Kubeconfig.Sources) != 1 {
		t.Fatalf("kubeconfig sources = %d, want the one that was there before", len(stored.Kubeconfig.Sources))
	}
}

// A store that refuses writes must not stop the setting taking effect in this
// process.
//
// `podsteer mcp` opens the store read-only and a file from a newer PodSteer is
// refused the same way. Neither is a reason for SetRetention to fail after the
// retention has already changed — the caller would be left unable to tell what
// state anything is in — so the refusal is logged and the change stands for
// the life of the process.
func TestARefusedSaveStillChangesTheRetentionInThisProcess(t *testing.T) {
	t.Parallel()

	service := newHistoryService(t, newRecordingHistory(), refusingSettings{newMemorySettings()})

	if err := service.SetRetention(context.Background(), domain.NewRetention(7)); err != nil {
		t.Fatalf("SetRetention() error = %v", err)
	}

	retention, err := service.Retention(context.Background())
	if err != nil {
		t.Fatalf("Retention() error = %v", err)
	}
	if retention.Days != 7 {
		t.Errorf("retention = %d days, want 7 even though the save was refused", retention.Days)
	}
}

// Closing must be safe however it is reached: the composition root defers it
// AND the window's shutdown hook calls it, so it runs twice on every exit.
func TestCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	service := newHistoryService(t, newRecordingHistory(), nil)
	service.Start(context.Background())

	service.Close()
	service.Close()
}

// A service that was never started must still close cleanly, or an early
// startup failure would hang the process on the deferred Close.
func TestCloseWithoutStartDoesNotHang(t *testing.T) {
	t.Parallel()

	service := newHistoryService(t, newRecordingHistory(), nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		service.Close()
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close hung on a service that was never started")
	}
}

// Retention is enforced against what a previous run left behind, not only
// against what this one writes.
func TestSamplerPrunesAtStartup(t *testing.T) {
	t.Parallel()

	store := newRecordingHistory()
	service := newHistoryService(t, store, nil)
	service.Start(context.Background())

	select {
	case <-store.written:
	case <-time.After(5 * time.Second):
		t.Fatal("the sampler never ran")
	}
	service.Close()

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.pruned) == 0 {
		t.Error("expected a prune at startup")
	}
}
