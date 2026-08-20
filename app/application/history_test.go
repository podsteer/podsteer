package application_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"podsteer/app/application"
	"podsteer/app/domain"
	"podsteer/app/ports"
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

// newHistoryService wires a service around the fakes, with one cluster open.
func newHistoryService(t *testing.T, store ports.HistoryPort, settingsPath string) *application.HistoryService {
	t.Helper()

	registry := application.NewRegistry()
	registry.Open(mustCluster(t, "dev", true))

	service, err := application.NewHistoryService(application.HistoryServiceDeps{
		History:      store,
		Overview:     stubOverview{},
		Registry:     registry,
		SettingsPath: settingsPath,
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
	service := newHistoryService(t, store, "")
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

	store := newRecordingHistory()
	service := newHistoryService(t, store, "")
	if err := service.SetRetention(context.Background(), domain.NewRetention(0)); err != nil {
		t.Fatalf("SetRetention() error = %v", err)
	}

	service.Start(context.Background())

	select {
	case <-store.written:
		t.Fatal("a sample was written while recording was turned off")
	case <-time.After(250 * time.Millisecond):
		// Nothing written, which is the point.
	}

	service.Close()
	if store.count() != 0 {
		t.Errorf("appended = %d samples, want 0", store.count())
	}
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

			service := newHistoryService(t, newRecordingHistory(), "")
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

	service := newHistoryService(t, newRecordingHistory(), "")
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

// Both settings survive a restart, and the file carries them together.
func TestSettingsPersistAcrossRestart(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "history.json")

	first := newHistoryService(t, newRecordingHistory(), path)
	if err := first.SetRetention(context.Background(), domain.NewRetention(7)); err != nil {
		t.Fatalf("SetRetention() error = %v", err)
	}
	if err := first.SetSamplingInterval(5 * time.Minute); err != nil {
		t.Fatalf("SetSamplingInterval() error = %v", err)
	}

	second := newHistoryService(t, newRecordingHistory(), path)

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

// Closing must be safe however it is reached: the composition root defers it
// AND the window's shutdown hook calls it, so it runs twice on every exit.
func TestCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	service := newHistoryService(t, newRecordingHistory(), "")
	service.Start(context.Background())

	service.Close()
	service.Close()
}

// A service that was never started must still close cleanly, or an early
// startup failure would hang the process on the deferred Close.
func TestCloseWithoutStartDoesNotHang(t *testing.T) {
	t.Parallel()

	service := newHistoryService(t, newRecordingHistory(), "")

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
	service := newHistoryService(t, store, "")
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
