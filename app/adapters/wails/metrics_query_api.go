package wails

import (
	"errors"
	"log/slog"
	"time"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// The frontend's view of a monitoring backend's answer.
//
// A SEPARATE API FROM HistoryAPI, and that separation is the point rather
// than tidiness: HistoryAPI serves PodSteer's OWN samples, derived from the
// overview, and this serves somebody else's measurement taken at somebody
// else's interval through somebody else's recording rules. Two calls means no
// component can accidentally treat one as a continuation of the other, which
// is the rule ADR 7 exists to protect.

// BackendPoint is one instant of a backend series.
//
// Unix milliseconds and a float, the same shape Sample uses, because it is
// chart data and every charting library wants numbers.
type BackendPoint struct {
	At    int64   `json:"at"`
	Value float64 `json:"value"`
}

// BackendSeries is one labelled series a backend answered with.
type BackendSeries struct {
	// Label is the series' own name — the node for a node-scoped expression,
	// empty for a cluster-scoped one, which returns exactly one series.
	Label  string         `json:"label"`
	Points []BackendPoint `json:"points"`
}

// SeriesProvenance travels with every backend series and is what keeps it
// from ever being drawn as PodSteer's own.
//
// THE TWO MUST NEVER MERGE INTO ONE LINE. One is somebody else's measurement
// and the other is ours, and splicing them — or using one to fill a gap in
// the other — is the recorded mistake ADR 1 refused for kubelet readings.
// This struct is what a component reads to label which is which.
type SeriesProvenance struct {
	// Origin is "backend" here and "sampled" for PodSteer's own series, which
	// the frontend supplies for its half.
	Origin string `json:"origin"`
	// Source names the service that answered — "Prometheus in monitoring".
	Source string `json:"source"`
	// Verification is "verified", "fleet", "mismatch" or "unverifiable": how
	// far PodSteer could check that this backend's series are about THIS
	// cluster. Shown beside the source, because a number narrowed by a filter
	// PodSteer composed is a different claim from one that needed no filter.
	Verification string `json:"verification"`
	// Filtered reports that the expression was narrowed to this cluster's
	// node names.
	Filtered bool `json:"filtered"`
}

// BackendSeriesResult is everything the chart needs to draw a backend's
// answer, or to say why it is not drawing one.
type BackendSeriesResult struct {
	// Status is one of: not-enabled, nothing-discovered, forbidden,
	// unreachable, rejected, too-large, unverified, answered, answered-empty.
	//
	// ANSWERED AND ANSWERED-EMPTY ARE SEPARATE, and that distinction is why
	// this is a status rather than a boolean: a Prometheus that scrapes
	// application endpoints and not kubelets returns HTTP 200 and nothing,
	// and collapsed into "answered" that is a blank chart under a green
	// label.
	Status string `json:"status"`
	// Message is the one line the panel shows. For "rejected" it is the
	// backend's own words, carried across the way a rejected manifest carries
	// the API server's.
	Message string `json:"message"`
	// Provenance says whose measurement this is and how far it was checked.
	Provenance SeriesProvenance `json:"provenance"`
	// Series are the answers. A cluster-scoped expression returns one.
	Series []BackendSeries `json:"series"`
	// Expression is the PromQL that was sent, so an operator reading their
	// own backend's query log can match it to what they pressed. There is no
	// query box — this is shown, never typed.
	Expression string `json:"expression"`
	// SpanSeconds is what the returned points actually cover, which is not
	// what was asked for: a range over seven days against a Prometheus
	// retaining one returns one day and no error.
	SpanSeconds int64 `json:"spanSeconds"`
	// StepSeconds is the resolution the range was evaluated at.
	StepSeconds int64 `json:"stepSeconds"`
	// Unit is "cores", "bytes" or "pods", so the chart formats a backend's
	// numbers the same way it formats PodSteer's own.
	Unit string `json:"unit"`
}

// MetricsQueryAPI exposes the monitoring-backend read to the frontend.
type MetricsQueryAPI struct {
	queries ports.MetricsQueryUseCase
	app     *App
	logger  *slog.Logger
}

// NewMetricsQueryAPI returns the bound API.
func NewMetricsQueryAPI(queries ports.MetricsQueryUseCase, app *App, logger *slog.Logger) (*MetricsQueryAPI, error) {
	switch {
	case queries == nil:
		return nil, errors.New("wails: MetricsQueryAPI requires a MetricsQueryUseCase")
	case app == nil:
		return nil, errors.New("wails: MetricsQueryAPI requires an App")
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &MetricsQueryAPI{
		queries: queries,
		app:     app,
		logger:  logger.With(slog.String("api", "metricsquery")),
	}, nil
}

// GetSeries asks the cluster's monitoring backend for one metric over a
// window.
//
// NEVER CALLED FROM A REFRESH TICK. The rule lives with the caller — see
// web/src/stores/backendTrend.svelte.ts, whose test counts calls across
// several driven refreshes — because a tick is this application deciding on
// its own to put PromQL onto somebody's production Prometheus, and a chart
// opening is somebody looking at something.
//
// There is NO EXPRESSION PARAMETER, and there never will be one: the metric
// and the scope select from a fixed table in the domain, so no string an
// operator wrote can reach a backend.
func (m *MetricsQueryAPI) GetSeries(clusterID, metric, scope string, windowMinutes int) (BackendSeriesResult, error) {
	ctx, cancel := m.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return BackendSeriesResult{}, apiError(m.logger, "GetSeries", err)
	}

	if windowMinutes <= 0 {
		windowMinutes = 60
	}
	if scope == "" {
		scope = string(domain.ScopeCluster)
	}

	result, err := m.queries.Series(ctx, id,
		domain.MetricID(metric), domain.MetricScope(scope),
		time.Duration(windowMinutes)*time.Minute)
	if err != nil {
		return BackendSeriesResult{}, apiError(m.logger, "GetSeries", err)
	}

	return toBackendSeriesResult(result, domain.MetricID(metric), domain.MetricScope(scope)), nil
}

func toBackendSeriesResult(
	result domain.BackendSeriesResult,
	metric domain.MetricID,
	scope domain.MetricScope,
) BackendSeriesResult {
	// Never null on the wire: both readers of this field test its length on
	// every render, and a nil slice marshals as `null`.
	series := make([]BackendSeries, 0, len(result.Series))
	for _, answered := range result.Series {
		points := make([]BackendPoint, 0, len(answered.Points))
		for _, point := range answered.Points {
			points = append(points, BackendPoint{At: point.At.UnixMilli(), Value: point.Value})
		}
		series = append(series, BackendSeries{
			Label:  answered.Labels[domain.NodeProbeLabel],
			Points: points,
		})
	}

	unit := ""
	if byScope, known := domain.Expressions[metric]; known {
		unit = byScope[scope].Unit
	}

	return BackendSeriesResult{
		Status:  string(result.Status),
		Message: result.Message,
		Provenance: SeriesProvenance{
			Origin:       string(result.Provenance.Origin),
			Source:       result.Provenance.Source,
			Verification: string(result.Provenance.Verification),
			Filtered:     result.Provenance.Filtered,
		},
		Series:      series,
		Expression:  result.Expression,
		SpanSeconds: int64(result.Span().Seconds()),
		StepSeconds: int64(result.Step.Seconds()),
		Unit:        unit,
	}
}
