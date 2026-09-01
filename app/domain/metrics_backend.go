package domain

import "fmt"

// MetricsBackend is a monitoring system found running in a cluster.
//
// Discovered rather than configured, because the point is to notice what is
// already there. A cluster with kube-prometheus-stack installed has a
// continuous history of exactly the figures PodSteer charts from a few
// minutes of its own samples — and asking somebody to type in a URL for
// something the cluster can already be asked about is work nobody should do.
type MetricsBackend struct {
	// Kind is "prometheus", or empty when nothing was found.
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

// Describe names it for the interface, e.g. "Prometheus in monitoring".
func (b MetricsBackend) Describe() string {
	if !b.Found() {
		return ""
	}
	return fmt.Sprintf("Prometheus in %s", b.Namespace)
}
