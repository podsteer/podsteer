package wails

import (
	"errors"
	"log/slog"
	"time"

	"k8sense/app/domain"
	"k8sense/app/ports"
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
		Samples:       samples,
		SpanSeconds:   int64(series.Span().Seconds()),
		RetentionDays: retention.Days,
		Recording:     retention.Enabled(),
	}, nil
}

// RetentionSetting is how long K8Sense keeps samples.
type RetentionSetting struct {
	// Days is 0 when nothing is recorded.
	Days int `json:"days"`
	// MaxDays is the ceiling the UI should offer.
	MaxDays int `json:"maxDays"`
}

// GetRetention reports the current retention.
func (h *HistoryAPI) GetRetention() (RetentionSetting, error) {
	ctx, cancel := h.app.requestContext()
	defer cancel()

	retention, err := h.history.Retention(ctx)
	if err != nil {
		return RetentionSetting{}, apiError(h.logger, "GetRetention", err)
	}
	return RetentionSetting{Days: retention.Days, MaxDays: domain.MaxRetentionDays}, nil
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
