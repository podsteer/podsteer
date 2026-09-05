package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// This file records what a cluster looked like over time, so the dashboard can
// show a trend rather than an instant.
//
// The sampler holds to the rule every long-lived goroutine here holds to: one
// owner (this service), one way to stop (Close), and it waits for its work to
// finish before returning. It is not the only one — the watch sweeper, the
// reflectors and their supervisors, the port-forward supervisors, exec and
// attach sessions, local-shell pumps, log streams and file transfers all own
// goroutines — and the rule matters more here than in most of them because
// this one writes files.
//
// NOTHING CANCELS A CONTEXT AT SHUTDOWN. The framework's runtime context is
// never cancelled — the shutdown hook only clears the pointer to it — so Close
// is the whole mechanism, exactly as StopAllPortForwards and StopAllWatches
// are theirs. A goroutine here that waited to be cancelled would never be.

const (
	// pruneInterval is how often expired samples are swept. Hourly is far
	// more often than a day of retention needs, and cheap.
	pruneInterval = time.Hour
	// maxSeriesPoints caps what a single read returns when the caller asks
	// for no limit of its own.
	maxSeriesPoints = 720
)

// HistoryServiceDeps are the collaborators the history needs.
type HistoryServiceDeps struct {
	// History stores samples. Required.
	History ports.HistoryPort
	// Overview produces the assessment each sample is reduced from. Required.
	Overview ports.OverviewService
	// Registry tracks which clusters are open. Required.
	Registry *Registry
	// SettingsPath is the file retention is persisted to. Optional; when
	// empty, retention lives only for the life of the process.
	SettingsPath string
	// Logger receives diagnostics. Optional; defaults to slog.Default.
	Logger *slog.Logger
}

// HistoryService records and serves a cluster's history.
type HistoryService struct {
	history  ports.HistoryPort
	overview ports.OverviewService
	registry *Registry
	settings string
	logger   *slog.Logger

	mu        sync.RWMutex
	retention domain.Retention
	interval  time.Duration

	// reconfigure carries "the cadence changed" to the sampler. Buffered and
	// non-blocking, so changing a setting never waits on the sampler and a
	// burst of changes collapses into one rebuild.
	reconfigure chan struct{}

	// cancel stops the sampler; done closes once it has finished.
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once

	// newTicker builds the sampler's tick source. Unexported and set only by
	// tests: the cadence floor is ten seconds, so a test that waited for real
	// ticks would have to sleep for half a minute to observe two of them.
	// Substituting the source is what lets the cadence itself be asserted
	// rather than assumed.
	newTicker func(time.Duration) (<-chan time.Time, func())
}

// realTicker is the production tick source.
func realTicker(d time.Duration) (<-chan time.Time, func()) {
	ticker := time.NewTicker(d)
	return ticker.C, ticker.Stop
}

var _ ports.HistoryService = (*HistoryService)(nil)

// NewHistoryService validates deps and returns the service.
//
// Retention is loaded here rather than lazily, so the sampler never records a
// single sample on a machine where the operator has turned recording off.
func NewHistoryService(deps HistoryServiceDeps) (*HistoryService, error) {
	switch {
	case deps.History == nil:
		return nil, errors.New("application: HistoryService requires a HistoryPort")
	case deps.Overview == nil:
		return nil, errors.New("application: HistoryService requires an OverviewService")
	case deps.Registry == nil:
		return nil, errors.New("application: HistoryService requires a Registry")
	}

	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	service := &HistoryService{
		history:  deps.History,
		overview: deps.Overview,
		registry: deps.Registry,
		settings: deps.SettingsPath,
		logger:   logger.With(slog.String("service", "history")),
		// The default records a day. Long enough for the trend on the
		// dashboard to be useful across a working day, short enough that
		// nobody discovers PodSteer has been keeping a month of data they
		// never asked for.
		retention:   domain.NewRetention(1),
		interval:    domain.DefaultSamplingInterval,
		done:        make(chan struct{}),
		reconfigure: make(chan struct{}, 1),
		newTicker:   realTicker,
	}
	service.retention, service.interval = service.loadSettings()

	return service, nil
}

// Start begins sampling every open cluster until Close is called.
//
// The context passed here bounds the sampler's whole life; Close cancels it
// and waits. Calling Start twice is a programming error and panics rather than
// silently running two samplers writing the same files.
func (s *HistoryService) Start(ctx context.Context) {
	if s.cancel != nil {
		panic("application: HistoryService.Start called twice")
	}

	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	go s.run(ctx)
}

// Close stops the sampler and waits for it to finish.
//
// Waiting matters: the sampler writes files, and returning before it has
// stopped would let the process exit mid-write. Safe to call more than once,
// and safe to call on a service that was never started.
func (s *HistoryService) Close() {
	s.once.Do(func() {
		if s.cancel == nil {
			close(s.done)
			return
		}
		s.cancel()
		<-s.done
	})
}

// run is the sampler loop. It is the only goroutine this service starts.
func (s *HistoryService) run(ctx context.Context) {
	defer close(s.done)

	sampleTicks, stopSamples := s.newTicker(s.SamplingInterval())
	// Through a closure, because the sampler is rebuilt on reconfigure and a
	// bare `defer stopSamples()` would capture the FIRST one — leaving
	// whichever ticker was live at shutdown unstopped.
	defer func() { stopSamples() }()

	prunes := time.NewTicker(pruneInterval)
	defer prunes.Stop()

	// Prune once at startup: retention has to be enforced against what a
	// previous run left behind, not only against what this one writes.
	s.prune(ctx)

	// One sample immediately, so a chart has a point to draw within seconds
	// of the application opening rather than after the first half minute.
	s.sampleAll(ctx)

	for {
		select {
		case <-ctx.Done():
			s.logger.Debug("sampler stopped")
			return
		case <-sampleTicks:
			s.sampleAll(ctx)
		case <-prunes.C:
			s.prune(ctx)
		case <-s.reconfigure:
			// Rebuild rather than Reset: the new cadence should start from
			// now, so shortening it takes effect immediately instead of after
			// the old, longer tick has elapsed.
			stopSamples()
			sampleTicks, stopSamples = s.newTicker(s.SamplingInterval())
		}
	}
}

// sampleAll records one sample per open cluster.
//
// Failures are logged and dropped: a cluster that went unreachable between
// ticks must not stop the others being recorded, and a missing sample is a gap
// in a chart rather than something to interrupt anyone over.
// sampleTimeout bounds one cluster's assessment.
//
// Generous, because a large cluster over a slow link is not a fault — but
// finite, because the loop is sequential and one wedged cluster must not cost
// the others their history.
const sampleTimeout = 30 * time.Second

func (s *HistoryService) sampleAll(ctx context.Context) {
	if !s.Enabled() {
		return
	}

	interval := s.SamplingInterval()

	for _, cluster := range s.registry.All() {
		if err := ctx.Err(); err != nil {
			return
		}

		overview, err := s.assess(ctx, cluster.ID(), interval)
		if err != nil {
			s.logger.Debug("skipping sample",
				slog.String("cluster", cluster.ID().String()),
				slog.String("error", err.Error()))
			continue
		}

		sample := domain.NewSampleFromOverview(overview)
		if err := s.history.Append(ctx, cluster.ID(), sample); err != nil {
			s.logger.Warn("recording sample failed",
				slog.String("cluster", cluster.ID().String()),
				slog.String("error", err.Error()))
		}
	}
}

// assess reads one cluster's overview, bounded in time and willing to reuse
// a recent one.
//
// The deadline is the important half. The REST config deliberately sets no
// client timeout — per-request deadlines belong on the context, attached by
// whichever inbound adapter made the call — and the sampler is an inbound
// driver that was attaching none. So the exact case that motivated a request
// timeout in the first place, a half-open tunnel that accepts the connection
// and then says nothing, blocked one Overview call indefinitely, which
// blocked the sequential loop over every OTHER cluster, which stopped both
// sampling and pruning for all of them — silently, because a missing sample
// is by design just a gap in a chart.
//
// maxAge lets the sampler reuse an assessment the dashboard has just made. A
// sample taken on a thirty-second timer is no less true for describing a
// moment a few seconds earlier, and the alternative is reading the whole
// cluster twice on overlapping timers.
func (s *HistoryService) assess(
	ctx context.Context,
	id domain.ClusterID,
	maxAge time.Duration,
) (domain.Overview, error) {
	ctx, cancel := context.WithTimeout(ctx, sampleTimeout)
	defer cancel()

	if reusable, ok := s.overview.(interface {
		OverviewWithin(context.Context, domain.ClusterID, time.Duration) (domain.Overview, error)
	}); ok {
		return reusable.OverviewWithin(ctx, id, maxAge)
	}
	return s.overview.Overview(ctx, id)
}

// prune enforces the retention window.
func (s *HistoryService) prune(ctx context.Context) {
	retention := s.current()
	if err := s.history.Prune(ctx, retention.Cutoff(time.Now())); err != nil {
		s.logger.Warn("pruning history failed", slog.String("error", err.Error()))
	}
}

// Enabled reports whether anything is being recorded.
func (s *HistoryService) Enabled() bool { return s.current().Enabled() }

func (s *HistoryService) current() domain.Retention {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.retention
}

// SamplingInterval reports how often each open cluster is sampled.
func (s *HistoryService) SamplingInterval() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.interval
}

// SetSamplingInterval changes how often each open cluster is sampled.
//
// Takes effect at once rather than after the current tick: an operator who
// slows sampling down to reduce load on an API server means now, and one who
// speeds it up is usually watching something happen.
func (s *HistoryService) SetSamplingInterval(interval time.Duration) error {
	interval = domain.NewSamplingInterval(interval)

	s.mu.Lock()
	s.interval = interval
	retention := s.retention
	s.mu.Unlock()

	s.saveSettings(retention, interval)

	// Non-blocking: a full buffer already means "rebuild pending".
	select {
	case s.reconfigure <- struct{}{}:
	default:
	}

	s.logger.Info("sampling interval changed", slog.Duration("interval", interval))
	return nil
}

// Series returns a cluster's samples over the given window.
func (s *HistoryService) Series(
	ctx context.Context,
	id domain.ClusterID,
	window time.Duration,
	maxPoints int,
) (domain.Series, error) {
	if _, err := s.registry.Get(id); err != nil {
		return nil, err
	}

	retention := s.current()
	if !retention.Enabled() {
		return nil, nil
	}

	// Never serve beyond the retention window, whatever the caller asks for:
	// the operator's setting is a statement about what PodSteer keeps, and it
	// would be a poor one if a wider query still returned older data that
	// pruning had not swept yet.
	cutoff := time.Now().UTC().Add(-window)
	if floor := retention.Cutoff(time.Now()); floor.After(cutoff) {
		cutoff = floor
	}

	series, err := s.history.Series(ctx, id, cutoff)
	if err != nil {
		return nil, err
	}

	if maxPoints <= 0 {
		maxPoints = maxSeriesPoints
	}
	return series.Downsample(maxPoints), nil
}

// Retention reports how long samples are kept.
func (s *HistoryService) Retention(context.Context) (domain.Retention, error) {
	return s.current(), nil
}

// SetRetention changes how long samples are kept and enforces it at once.
//
// Enforcing immediately is the whole contract: an operator who reduces
// retention — or turns recording off — is telling PodSteer to stop holding
// data it already has, not merely to stop adding to it.
func (s *HistoryService) SetRetention(ctx context.Context, retention domain.Retention) error {
	retention = domain.NewRetention(retention.Days)

	s.mu.Lock()
	s.retention = retention
	interval := s.interval
	s.mu.Unlock()

	s.saveSettings(retention, interval)

	if err := s.history.Prune(ctx, retention.Cutoff(time.Now())); err != nil {
		return fmt.Errorf("applying retention: %w", err)
	}

	s.logger.Info("retention changed", slog.Int("days", retention.Days))
	return nil
}

// --- retention persistence ------------------------------------------------
//
// Kept on the Go side rather than with the UI preferences in localStorage,
// because it governs what gets written to disk. A setting that says "record
// nothing" has to be honoured by the process doing the recording, even on the
// run where the window never opens.

type persistedSettings struct {
	RetentionDays   int `json:"retentionDays"`
	IntervalSeconds int `json:"intervalSeconds"`
}

// loadSettings reads the persisted retention and cadence, falling back to the
// service's defaults for anything missing or unreadable.
func (s *HistoryService) loadSettings() (domain.Retention, time.Duration) {
	if s.settings == "" {
		return s.retention, s.interval
	}

	raw, err := os.ReadFile(s.settings)
	if err != nil {
		// No file yet is the ordinary first run.
		if !errors.Is(err, os.ErrNotExist) {
			s.logger.Warn("reading history settings failed", slog.String("error", err.Error()))
		}
		return s.retention, s.interval
	}

	var stored persistedSettings
	if err := json.Unmarshal(raw, &stored); err != nil {
		s.logger.Warn("history settings are unreadable, using the defaults",
			slog.String("error", err.Error()))
		return s.retention, s.interval
	}

	// A file written before the cadence was configurable has no interval;
	// NewSamplingInterval turns that zero into the default.
	return domain.NewRetention(stored.RetentionDays),
		domain.NewSamplingInterval(time.Duration(stored.IntervalSeconds) * time.Second)
}

func (s *HistoryService) saveSettings(retention domain.Retention, interval time.Duration) {
	if s.settings == "" {
		return
	}

	if err := os.MkdirAll(filepath.Dir(s.settings), 0o700); err != nil {
		s.logger.Warn("saving history settings failed", slog.String("error", err.Error()))
		return
	}

	raw, err := json.Marshal(persistedSettings{
		RetentionDays:   retention.Days,
		IntervalSeconds: int(interval.Seconds()),
	})
	if err != nil {
		return
	}
	if err := os.WriteFile(s.settings, raw, 0o600); err != nil {
		s.logger.Warn("saving history settings failed", slog.String("error", err.Error()))
	}
}
