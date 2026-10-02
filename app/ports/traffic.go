package ports

import (
	"context"
	"errors"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// TrafficQueryPort evaluates one traffic expression at an instant.
//
// A PORT OF ITS OWN beside MetricsQueryPort, for the reason that one is split
// from MetricsPort: it sends PromQL PodSteer composed to a system that logs it
// under the operator's identity, so a composition that must not do that
// cannot name it. The transport is the same — a GET through the API server's
// service proxy, never a POST, refused past domain.MaxQueryURLBytes — and so
// are the sentinels: ErrForbidden, ErrMetricsQueryRejected,
// ErrMetricsQueryTooLarge.
type TrafficQueryPort interface {
	// QueryInstant evaluates expression at one instant and returns one
	// series per result, each with at most one point.
	//
	// The expression comes from domain.TrafficExpressions or
	// domain.TrafficProbes and nowhere else.
	QueryInstant(
		ctx context.Context,
		id domain.ClusterID,
		backend domain.MetricsBackend,
		expression string,
		at time.Time,
	) ([]domain.PromSeries, error)
}

// TrafficUseCase answers the topology's traffic layer.
//
// NOTHING HERE MAY BE CALLED FROM A REFRESH TICK, the rule MetricsQueryUseCase
// states: the layer is read when somebody switches it on, picks a source or a
// window, or presses refresh.
type TrafficUseCase interface {
	// Sources says which traffic sources the cluster's chosen backend holds.
	Sources(ctx context.Context, id domain.ClusterID) (domain.TrafficSources, error)

	// Traffic reads one source over one window, scoped to namespaces (or
	// every namespace when all is set).
	Traffic(
		ctx context.Context,
		id domain.ClusterID,
		namespaces []domain.NamespaceName,
		all bool,
		source domain.TrafficSource,
		window domain.TrafficWindow,
	) (domain.TrafficLayer, error)
}

// ErrMetricsProxyRefused is a monitoring backend (or the mesh in front of it)
// refusing the API server's proxy with its own 403, after the ephemeral
// port-forward fallback could not be made either. Its text says who refused
// and why the forward failed.
var ErrMetricsProxyRefused = errors.New("the monitoring backend refused the API server's proxy")

// ErrMetricsForwardRefused is the account being refused the port-forward
// PodSteer falls back to when a backend refuses the proxy: the `create` verb
// on pods/portforward (or reading the Service and its pods to find one).
var ErrMetricsForwardRefused = errors.New("the account may not open a port-forward to the monitoring backend")
