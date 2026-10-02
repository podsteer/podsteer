package wails

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// TrafficAPI is the topology's traffic layer, bound to the frontend.
//
// LIKE MetricsQueryAPI, NO PARAMETER IS AN EXPRESSION OR A URL: a source and
// a window are each one of a fixed set, validated here, and namespaces are
// DNS-1123 labels before they go anywhere near a matcher. Nothing here is
// called on a tick; the page asks when the layer is switched on.
type TrafficAPI struct {
	traffic ports.TrafficUseCase
	app     *App
	logger  *slog.Logger
}

// NewTrafficAPI wires the API.
func NewTrafficAPI(traffic ports.TrafficUseCase, app *App, logger *slog.Logger) (*TrafficAPI, error) {
	switch {
	case traffic == nil:
		return nil, errors.New("wails: TrafficAPI requires a TrafficUseCase")
	case app == nil:
		return nil, errors.New("wails: TrafficAPI requires an App")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &TrafficAPI{traffic: traffic, app: app, logger: logger.With(slog.String("api", "traffic"))}, nil
}

// trafficProbeRequests is how many requests Sources may make: one probe per
// source.
const trafficProbeRequests = 5

// trafficLayerRequests is the most requests one Traffic call may make: the
// probes when they are not cached, then one per expression.
const trafficLayerRequests = trafficProbeRequests + 7

// Sources says which traffic sources the cluster's monitoring backend holds.
func (t *TrafficAPI) Sources(clusterID string) (TrafficSources, error) {
	ctx, cancel := t.app.requestContextFor(trafficProbeRequests)
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return TrafficSources{}, apiError(t.logger, "Sources", err)
	}

	sources, err := t.traffic.Sources(ctx, id)
	if err != nil {
		return TrafficSources{}, apiError(t.logger, "Sources", err)
	}
	return toTrafficSources(sources), nil
}

// Traffic reads one source over one window for the namespaces drawn.
func (t *TrafficAPI) Traffic(clusterID string, namespaces []string, all bool, source, window string) (TrafficLayer, error) {
	ctx, cancel := t.app.requestContextFor(trafficLayerRequests)
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return TrafficLayer{}, apiError(t.logger, "Traffic", err)
	}
	src, err := domain.ParseTrafficSource(source)
	if err != nil {
		return TrafficLayer{}, apiError(t.logger, "Traffic", err)
	}
	win, err := domain.ParseTrafficWindow(window)
	if err != nil {
		return TrafficLayer{}, apiError(t.logger, "Traffic", err)
	}

	scope := make([]domain.NamespaceName, 0, len(namespaces))
	if !all {
		for _, raw := range namespaces {
			name, err := domain.NewNamespaceName(raw)
			if err != nil {
				return TrafficLayer{}, apiError(t.logger, "Traffic", err)
			}
			if name == domain.NamespaceAll {
				continue
			}
			scope = append(scope, name)
		}
		if len(scope) == 0 {
			return TrafficLayer{}, apiError(t.logger, "Traffic",
				fmt.Errorf("%w: no namespace to draw traffic for", domain.ErrInvalidNamespaceName))
		}
	}

	layer, err := t.traffic.Traffic(ctx, id, scope, all, src, win)
	if err != nil {
		return TrafficLayer{}, apiError(t.logger, "Traffic", err)
	}
	return toTrafficLayer(layer), nil
}
