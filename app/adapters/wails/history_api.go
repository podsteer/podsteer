package wails

import (
	"errors"
	"log/slog"
	"time"

	"podsteer/app/domain"
	"podsteer/app/ports"
)

// Sample is one recorded measurement of a cluster, as the chart plots it.
//
// Timestamps go out as Unix milliseconds rather than formatted strings: this
// is chart data, and every charting library wants a number. Everything else is
// raw too — the frontend scales and formats, because a chart axis needs to
// compare values the backend cannot know the units of in advance.
type Sample struct {
	// At is Unix milliseconds, UTC.
	At int64 `json:"at"`
	// CPU figures are millicores; memory figures are bytes.
	CPUUsage       int64 `json:"cpuUsage"`
	CPURequests    int64 `json:"cpuRequests"`
	CPUAllocatable int64 `json:"cpuAllocatable"`
	MemoryUsage    int64 `json:"memoryUsage"`
	MemoryRequests int64 `json:"memoryRequests"`
	MemoryAlloc    int64 `json:"memoryAllocatable"`
	PodsScheduled  int   `json:"podsScheduled"`
	PodsNotReady   int   `json:"podsNotReady"`
	NodesReady     int   `json:"nodesReady"`
	NodesTotal     int   `json:"nodesTotal"`
	// Measured is false when no metrics API answered at that moment. The
	// usage series must break rather than plot zero, which would read as an
	// idle cluster.
	Measured bool `json:"measured"`
}

// SeriesResult is a cluster's history and an honest account of its extent.
type SeriesResult struct {
	Samples []Sample `json:"samples"`
	// SpanSeconds is how long the returned samples actually cover, which lets
	// the UI say "the last 40 minutes" instead of implying it has everything.
	SpanSeconds int64 `json:"spanSeconds"`
	// RetentionDays is the configured retention; 0 means nothing is recorded.
	RetentionDays int `json:"retentionDays"`
	// IntervalSeconds is how often a sample is taken, which tells the UI how
	// long to wait before a second point can possibly exist.
	IntervalSeconds int `json:"intervalSeconds"`
	// Recording reports whether sampling is on at all.
	Recording bool `json:"recording"`
}

// HistoryAPI exposes the recorded history and its retention setting.
type HistoryAPI struct {
	history ports.HistoryService
	app     *App
	logger  *slog.Logger
}

// NewHistoryAPI returns the bound history API.
func NewHistoryAPI(history ports.HistoryService, app *App, logger *slog.Logger) (*HistoryAPI, error) {
	switch {
	case history == nil:
		return nil, errors.New("wails: HistoryAPI requires a HistoryService")
	case app == nil:
		return nil, errors.New("wails: HistoryAPI requires an App")
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &HistoryAPI{
		history: history,
		app:     app,
		logger:  logger.With(slog.String("api", "history")),
	}, nil
}

// GetSeries returns a cluster's recorded history over the last windowMinutes.
//
// maxPoints bounds what crosses the IPC boundary; the backend averages down to
// it rather than dropping points, so a spike survives as a bump instead of
// disappearing between the samples that happened to be kept.
func (h *HistoryAPI) GetSeries(clusterID string, windowMinutes, maxPoints int) (SeriesResult, error) {
	ctx, cancel := h.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return SeriesResult{}, apiError(h.logger, "GetSeries", err)
	}

	if windowMinutes <= 0 {
		windowMinutes = 60
	}
	window := time.Duration(windowMinutes) * time.Minute

	series, err := h.history.Series(ctx, id, window, maxPoints)
	if err != nil {
		return SeriesResult{}, apiError(h.logger, "GetSeries", err)
	}

	retention, err := h.history.Retention(ctx)
	if err != nil {
		return SeriesResult{}, apiError(h.logger, "GetSeries", err)
	}

	samples := make([]Sample, 0, len(series))
	for _, sample := range series {
		samples = append(samples, Sample{
			At:             sample.At.UnixMilli(),
			CPUUsage:       sample.CPUUsageMilli,
			CPURequests:    sample.CPURequestsMilli,
			CPUAllocatable: sample.CPUAllocatableMilli,
			MemoryUsage:    sample.MemoryUsageBytes,
			MemoryRequests: sample.MemoryRequestsBytes,
			MemoryAlloc:    sample.MemoryAllocBytes,
			PodsScheduled:  sample.PodsScheduled,
			PodsNotReady:   sample.PodsNotReady,
			NodesReady:     sample.NodesReady,
			NodesTotal:     sample.NodesTotal,
			Measured:       sample.Measured,
		})
	}

	return SeriesResult{
		Samples:         samples,
		SpanSeconds:     int64(series.Span().Seconds()),
		RetentionDays:   retention.Days,
		IntervalSeconds: int(h.history.SamplingInterval().Seconds()),
		Recording:       retention.Enabled(),
	}, nil
}

// HistorySettings is what PodSteer records, and how often.
type HistorySettings struct {
	// RetentionDays is 0 when nothing is recorded.
	RetentionDays int `json:"retentionDays"`
	// MaxDays is the ceiling the UI should offer.
	MaxDays int `json:"maxDays"`
	// IntervalSeconds is the sampling cadence.
	IntervalSeconds int `json:"intervalSeconds"`
	// The bounds the cadence is clamped to, so the UI never offers a value
	// the backend would silently change.
	MinIntervalSeconds int `json:"minIntervalSeconds"`
	MaxIntervalSeconds int `json:"maxIntervalSeconds"`
}

// GetSettings reports what is recorded and how often.
func (h *HistoryAPI) GetSettings() (HistorySettings, error) {
	ctx, cancel := h.app.requestContext()
	defer cancel()

	retention, err := h.history.Retention(ctx)
	if err != nil {
		return HistorySettings{}, apiError(h.logger, "GetSettings", err)
	}

	return HistorySettings{
		RetentionDays:      retention.Days,
		MaxDays:            domain.MaxRetentionDays,
		IntervalSeconds:    int(h.history.SamplingInterval().Seconds()),
		MinIntervalSeconds: int(domain.MinSamplingInterval.Seconds()),
		MaxIntervalSeconds: int(domain.MaxSamplingInterval.Seconds()),
	}, nil
}

// SetSamplingInterval changes how often each open cluster is sampled.
//
// Every sample costs a full cluster assessment, so this is a real load control
// on a large cluster and not only a chart-resolution preference. The value is
// clamped to the supported range rather than rejected: an out-of-range cadence
// is a UI bug, and refusing it would leave the operator with no recording at
// all rather than a slightly different one.
func (h *HistoryAPI) SetSamplingInterval(seconds int) error {
	if err := h.history.SetSamplingInterval(time.Duration(seconds) * time.Second); err != nil {
		return apiError(h.logger, "SetSamplingInterval", err)
	}
	return nil
}

// SetRetention changes how long samples are kept.
//
// Zero turns recording off AND discards what has already been recorded, which
// is what an operator means by it. Anything else would leave the previous
// window's data sitting on disk after they asked for none.
func (h *HistoryAPI) SetRetention(days int) error {
	ctx, cancel := h.app.requestContext()
	defer cancel()

	if err := h.history.SetRetention(ctx, domain.NewRetention(days)); err != nil {
		return apiError(h.logger, "SetRetention", err)
	}
	return nil
}
