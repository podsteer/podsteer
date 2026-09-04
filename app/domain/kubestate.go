package domain

import "fmt"

// KubeStateMetrics is a kube-state-metrics installation found in a cluster.
//
// It sits beside MetricsBackend and is the same kind of value for the same
// reason: something already running that PodSteer noticed, named so an
// operator can find it, and nothing more. kube-state-metrics turns the API
// server's objects into Prometheus gauges — how many replicas a Deployment
// wants against how many it has, why a pod is not ready, when a Job last
// completed — which is where a great many of the panels in somebody's Grafana
// actually come from. Knowing it is installed answers "why does Grafana have
// this and PodSteer's own screens get it from somewhere else".
//
// THERE IS DELIBERATELY NO ProxyTarget HERE, unlike MetricsBackend. That
// method exists because a proxied PromQL query is a thing somebody may one
// day ask for; kube-state-metrics is a scrape endpoint, and PodSteer reading
// it would be PodSteer collecting metrics rather than pointing at the system
// that already does. Adding one is the first step of exactly the creep the
// "does not query it" rule exists to prevent, so the value carries only what
// a person needs to go and look.
type KubeStateMetrics struct {
	// Namespace and Service locate it, for somebody who wants to look.
	Namespace NamespaceName
	Service   string
	// Port is the metrics port, named or numeric as the Service declares it,
	// or empty when the Service names none this build recognises. Empty is
	// not a failure to find it — see the adapter's pickKubeStateMetrics.
	Port string
}

// Found reports whether anything was discovered.
func (k KubeStateMetrics) Found() bool { return k.Service != "" }

// Describe names it for the interface, e.g. "kube-state-metrics in monitoring".
func (k KubeStateMetrics) Describe() string {
	if !k.Found() {
		return ""
	}
	return fmt.Sprintf("kube-state-metrics in %s", k.Namespace)
}
