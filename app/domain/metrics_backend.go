package domain

import "fmt"

// The monitoring products PodSteer recognises, as MetricsBackend.Kind.
//
// A PRODUCT, NOT A DEPLOYMENT SHAPE. VictoriaMetrics answers PromQL from a
// single-node binary and from a cluster's select component alike; what differs
// between them is where the query API is mounted, and that is carried by
// Prefix rather than by a second kind. A consumer that needs to say what it
// found reads Kind; one that needs to reach it reads Prefix and ProxyTarget.
const (
	// MetricsBackendPrometheus is Prometheus itself, and anything serving its
	// API at the same paths under the same name.
	MetricsBackendPrometheus = "prometheus"

	// MetricsBackendVictoriaMetrics is VictoriaMetrics, which serves a
	// compatible query API and is what a fair number of clusters run instead.
	MetricsBackendVictoriaMetrics = "victoriametrics"
)

// metricsBackendProducts names each kind for a person.
var metricsBackendProducts = map[string]string{
	MetricsBackendPrometheus:      "Prometheus",
	MetricsBackendVictoriaMetrics: "VictoriaMetrics",
}

// MetricsBackend is a monitoring system found running in a cluster.
//
// Discovered rather than configured, because the point is to notice what is
// already there. A cluster with kube-prometheus-stack installed has a
// continuous history of exactly the figures PodSteer charts from a few
// minutes of its own samples — and asking somebody to type in a URL for
// something the cluster can already be asked about is work nobody should do.
//
// A TYPED URL IS REFUSED, and that is a decision rather than an omission: one
// would be a new outbound destination, a new credential at rest and a second
// network path to explain, where a discovered Service is reached through the
// API server's own proxy on the kubeconfig credential already open for that
// tab. See ADR 7.
type MetricsBackend struct {
	// Kind is one of the MetricsBackend* constants above, or empty when
	// nothing was found.
	Kind string
	// Namespace and Service locate it. Reached through the API server's
	// service proxy rather than directly, so it needs no network route from
	// the operator's machine and no second credential — the same kubeconfig
	// that reads pods reads this.
	Namespace NamespaceName
	Service   string
	// Port is the service port to proxy to, named or numeric as the Service
	// declares it.
	Port string
	// Prefix is the path the Prometheus query API is mounted under, with a
	// leading slash and no trailing one. EMPTY for Prometheus and for a
	// single-node VictoriaMetrics, both of which serve `/api/v1/query_range`
	// at the root.
	//
	// It is not empty for a VictoriaMetrics CLUSTER, whose select component
	// mounts the query API per tenant at `/select/<accountID>/prometheus`.
	// Asking that component for `/api/v1/query_range` gets an error about the
	// URL format rather than an answer, so a discovery that reported the
	// service without the prefix would have reported something unusable.
	//
	// TENANT ZERO ONLY. An account id is a number the cluster's operator
	// chose and nothing in a Service, its labels or its ports records which
	// ones hold data — so any other tenant would be a guess PodSteer could
	// not check, and a wrong guess answers 200 with no series, which reads as
	// an idle cluster. Zero is the default every single-tenant install uses.
	Prefix string
}

// Found reports whether anything was discovered.
func (b MetricsBackend) Found() bool { return b.Kind != "" }

// ProxyTarget renders the "service:port" segment the API server's proxy
// subresource expects.
func (b MetricsBackend) ProxyTarget() string {
	if b.Port == "" {
		return b.Service
	}
	return fmt.Sprintf("%s:%s", b.Service, b.Port)
}

// Product names the monitoring system for a person, e.g. "VictoriaMetrics".
//
// A kind this build does not recognise renders as itself rather than as
// nothing: a value that reached here is a value something produced, and
// showing it is more useful than a blank where a product name should be.
func (b MetricsBackend) Product() string {
	if !b.Found() {
		return ""
	}
	if name, known := metricsBackendProducts[b.Kind]; known {
		return name
	}
	return b.Kind
}

// Describe names it for the interface, e.g. "VictoriaMetrics in monitoring".
//
// IT SAYS WHICH PRODUCT, and that is not cosmetic: an operator told
// "Prometheus in monitoring" about a VictoriaMetrics install would go looking
// for a Prometheus that is not there, and the sentence would be PodSteer
// claiming something about their cluster that is false.
func (b MetricsBackend) Describe() string {
	if !b.Found() {
		return ""
	}
	return fmt.Sprintf("%s in %s", b.Product(), b.Namespace)
}
