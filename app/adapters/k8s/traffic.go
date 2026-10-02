package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// QueryInstant evaluates one expression at an instant. See
// ports.TrafficQueryPort.
//
// THE SAME PATH AS QueryRange: proxyQuery's GET through the API server's
// service proxy, its cached refusal, its bounded read and its status
// classification. Only the endpoint (/api/v1/query) and the shape of the
// answer differ.
func (a *Adapter) QueryInstant(
	ctx context.Context,
	id domain.ClusterID,
	backend domain.MetricsBackend,
	expression string,
	at time.Time,
) ([]domain.PromSeries, error) {
	// Checked here as well as where the expression was composed: this is
	// the last place before a request leaves.
	if !domain.WithinQueryURLBudget(expression) {
		return nil, fmt.Errorf("querying %s in %q: %w", backend.Describe(), id, domain.ErrQueryTooLong)
	}

	body, err := a.proxyQuery(ctx, id, backend, "/api/v1/query", map[string]string{
		"query": expression,
		"time":  formatQueryInstant(at),
	})
	if err != nil {
		return nil, err
	}

	decoded, err := decodeQueryResponse(body)
	if err != nil {
		return nil, err
	}

	series := make([]domain.PromSeries, 0, len(decoded.Data.Result))
	for _, result := range decoded.Data.Result {
		entry := domain.PromSeries{Labels: result.Metric}
		// NaN — a histogram_quantile over no requests — is a gap, dropped
		// exactly as decodePoint drops it from a range. +Inf is NOT: it is a
		// histogram_quantile that fell in the +Inf bucket, which says
		// "slower than every bucket", and domain.MapTraffic reports it as
		// such rather than as a percentile the source does not expose.
		if point, ok := decodePoint(result.Value); ok {
			entry.Points = []domain.SeriesPoint{point}
		} else if point, ok := decodePositiveInfinity(result.Value); ok {
			entry.Points = []domain.SeriesPoint{point}
		}
		series = append(series, entry)
	}
	return series, nil
}

// decodePositiveInfinity reads a [unixSeconds, "+Inf"] pair, the one value
// decodePoint drops that an instant traffic query must keep.
func decodePositiveInfinity(pair []json.RawMessage) (domain.SeriesPoint, bool) {
	if len(pair) != 2 {
		return domain.SeriesPoint{}, false
	}
	var seconds float64
	var raw string
	if json.Unmarshal(pair[0], &seconds) != nil || json.Unmarshal(pair[1], &raw) != nil || raw != "+Inf" {
		return domain.SeriesPoint{}, false
	}
	return domain.SeriesPoint{At: time.UnixMilli(int64(seconds * 1000)).UTC(), Value: math.Inf(1)}, true
}
