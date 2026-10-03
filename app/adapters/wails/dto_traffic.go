package wails

import "github.com/podsteer/podsteer/app/domain"

// The traffic layer's wire shapes. Their JSON names are the ones in
// web/src/lib/topology/contract.ts, written ahead of this file so the
// interface could be built at the same time; dto_traffic_test.go holds the
// two to each other.

// TrafficSourceStatus says whether one source's metrics were found.
type TrafficSourceStatus struct {
	// Source is istio, linkerd, beyla, caretta or hubble.
	Source    string `json:"source"`
	Available bool   `json:"available"`
	// Detail is what was found, or what would be needed.
	Detail string `json:"detail"`
}

// TrafficSources answers which traffic sources the chosen backend holds.
type TrafficSources struct {
	// Backend is the Prometheus the queries go to, as the metrics query
	// feature names it.
	Backend string                `json:"backend"`
	Sources []TrafficSourceStatus `json:"sources"`
	// Status is the metrics query backend status: answered, not-enabled,
	// forbidden…
	Status  string `json:"status"`
	Message string `json:"message"`
}

// TrafficEndpoint is one end of an observed edge.
type TrafficEndpoint struct {
	Namespace string `json:"namespace"`
	Workload  string `json:"workload"`
	Service   string `json:"service"`
	External  string `json:"external"`
	Unknown   bool   `json:"unknown"`
	// NodeID is the topology node this endpoint maps to, '' when unmapped.
	NodeID string `json:"nodeId"`
}

// TrafficEdge is the traffic observed between two endpoints over a window.
type TrafficEdge struct {
	Source         TrafficEndpoint `json:"source"`
	Dest           TrafficEndpoint `json:"dest"`
	Protocol       string          `json:"protocol"`
	RequestsPerSec float64         `json:"requestsPerSec"`
	ErrorsPerSec   float64         `json:"errorsPerSec"`
	BytesPerSec    float64         `json:"bytesPerSec"`
	Connections    float64         `json:"connections"`
	// P50, P95 and P99 are milliseconds; -1 when the source does not expose
	// latency.
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
	// LatencyBeyondBuckets is true when a percentile fell in the histogram's
	// +Inf bucket — slower than its largest bound. That percentile is -1, and
	// this is what tells it apart from "not exposed".
	LatencyBeyondBuckets bool `json:"latencyBeyondBuckets"`
}

// TrafficLayer is one source's traffic over one window.
type TrafficLayer struct {
	Source   string            `json:"source"`
	Window   string            `json:"window"`
	Edges    []TrafficEdge     `json:"edges"`
	Unmapped []TrafficEndpoint `json:"unmapped"`
	Status   string            `json:"status"`
	Message  string            `json:"message"`
	// Provenance is which backend answered, as the metrics query feature
	// reports it.
	Provenance SeriesProvenance `json:"provenance"`
	// Expressions are the PromQL that was sent, shown to the operator.
	Expressions []string `json:"expressions"`
}

func toTrafficSources(sources domain.TrafficSources) TrafficSources {
	out := TrafficSources{
		Backend: sources.Backend,
		Sources: make([]TrafficSourceStatus, 0, len(sources.Sources)),
		Status:  string(sources.Status),
		Message: sources.Message,
	}
	for _, source := range sources.Sources {
		out.Sources = append(out.Sources, TrafficSourceStatus{
			Source:    string(source.Source),
			Available: source.Available,
			Detail:    source.Detail,
		})
	}
	return out
}

func toTrafficEndpoint(endpoint domain.TrafficEndpoint) TrafficEndpoint {
	return TrafficEndpoint{
		Namespace: endpoint.Namespace,
		Workload:  endpoint.Workload,
		Service:   endpoint.Service,
		External:  endpoint.External,
		Unknown:   endpoint.Unknown,
		NodeID:    endpoint.NodeID,
	}
}

// toTrafficLayer converts, with every slice non-nil so the interface never
// meets a null where the contract promises an array.
func toTrafficLayer(layer domain.TrafficLayer) TrafficLayer {
	out := TrafficLayer{
		Source:   string(layer.Source),
		Window:   string(layer.Window),
		Edges:    make([]TrafficEdge, 0, len(layer.Edges)),
		Unmapped: make([]TrafficEndpoint, 0, len(layer.Unmapped)),
		Status:   string(layer.Status),
		Message:  layer.Message,
		Provenance: SeriesProvenance{
			Origin:       string(layer.Provenance.Origin),
			Source:       layer.Provenance.Source,
			Verification: string(layer.Provenance.Verification),
			Filtered:     layer.Provenance.Filtered,
		},
		Expressions: append(make([]string, 0, len(layer.Expressions)), layer.Expressions...),
	}
	for _, edge := range layer.Edges {
		out.Edges = append(out.Edges, TrafficEdge{
			Source:         toTrafficEndpoint(edge.Source),
			Dest:           toTrafficEndpoint(edge.Dest),
			Protocol:       edge.Protocol,
			RequestsPerSec: edge.RequestsPerSec,
			ErrorsPerSec:   edge.ErrorsPerSec,
			BytesPerSec:    edge.BytesPerSec,
			Connections:    edge.Connections,
			P50:            edge.P50,
			P95:            edge.P95,
			P99:            edge.P99,

			LatencyBeyondBuckets: edge.LatencyBeyondBuckets,
		})
	}
	for _, endpoint := range layer.Unmapped {
		out.Unmapped = append(out.Unmapped, toTrafficEndpoint(endpoint))
	}
	return out
}
