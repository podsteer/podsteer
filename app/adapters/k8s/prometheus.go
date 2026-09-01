package k8s

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// prometheusSelectors are the label selectors worth asking about, in order.
//
// SELECTORS RATHER THAN LISTING EVERY SERVICE. A cluster can hold thousands
// of services and PodSteer would be reading all of them to find one; the API
// server can filter this for us. These two cover what is actually deployed:
// the first is the standard app label every recent Helm chart sets, including
// kube-prometheus-stack and the Prometheus community chart, and the second is
// the older convention still on long-lived installations.
var prometheusSelectors = []string{
	"app.kubernetes.io/name=prometheus",
	"app=prometheus",
}

// prometheusServiceNames are the names to accept when a label match is
// ambiguous or absent, most specific first.
//
// `prometheus-operated` is what the Prometheus Operator creates and is the
// strongest signal; the rest are what the common charts name their query
// endpoint. Anything else is not guessed at — a service called "metrics" may
// be anything, and a wrong guess here would have PodSteer proxying queries at
// somebody's application.
var prometheusServiceNames = []string{
	"prometheus-operated",
	"prometheus-server",
	"prometheus-k8s",
	"prometheus",
}

// prometheusPortNames are the port names a query endpoint uses.
var prometheusPortNames = []string{"web", "http-web", "http"}

// backendCache holds one discovery result per cluster.
//
// Discovery answers a question whose value moves in DAYS — a monitoring stack
// is installed once — so re-running it per refresh would be two list calls
// every ten seconds for an answer that has not changed since the cluster was
// built.
type backendCache struct {
	mu      sync.Mutex
	entries map[domain.ClusterID]backendEntry
}

type backendEntry struct {
	at     time.Time
	result domain.MetricsBackend
}

// backendCacheTTL is how long a discovery result stands.
const backendCacheTTL = 30 * time.Minute

// DiscoverMetricsBackend looks for a monitoring system already in the cluster.
//
// A MISS IS NOT AN ERROR. Most clusters have no Prometheus, or have one this
// account cannot list services to find, and both are ordinary — the caller
// gets a zero MetricsBackend and carries on with the in-memory samples. Only
// a genuine failure to ask is returned as one.
func (a *Adapter) DiscoverMetricsBackend(ctx context.Context, id domain.ClusterID) (domain.MetricsBackend, error) {
	if cached, ok := a.backends.get(id); ok {
		return cached, nil
	}

	set, err := a.factory.clientsFor(id)
	if err != nil {
		return domain.MetricsBackend{}, err
	}

	var found domain.MetricsBackend
	for _, selector := range prometheusSelectors {
		services, err := set.typed.CoreV1().Services(metav1.NamespaceAll).
			List(ctx, metav1.ListOptions{LabelSelector: selector, Limit: 50})
		if err != nil {
			wrapped := classify("discovering a metrics backend", err)

			// A REFUSAL IS CACHED, a transport failure is not. An account
			// that may not list services across namespaces will never be
			// able to, and retrying on every ten-second poll writes two
			// denied requests into somebody's audit log forever for a feature
			// that is an offer rather than a requirement. A cluster that was
			// merely unreachable, on the other hand, comes back — and should
			// be asked again when it does rather than after half an hour.
			if errors.Is(wrapped, ports.ErrForbidden) || errors.Is(wrapped, ports.ErrUnauthenticated) {
				a.backends.put(id, domain.MetricsBackend{})
				return domain.MetricsBackend{}, nil
			}
			return domain.MetricsBackend{}, wrapped
		}

		if backend, ok := pickPrometheus(services.Items); ok {
			found = backend
			break
		}
	}

	a.backends.put(id, found)
	return found, nil
}

// pickPrometheus chooses the most likely query endpoint from a candidate set.
//
// Ranked by NAME rather than taking the first match, because a
// kube-prometheus-stack installation returns several services carrying the
// same label — the operator's headless service, the alertmanager, sometimes a
// node-exporter — and only one of them answers PromQL.
func pickPrometheus(services []corev1.Service) (domain.MetricsBackend, bool) {
	best := -1
	var chosen corev1.Service

	for _, service := range services {
		rank := nameRank(service.Name)
		if rank < 0 {
			continue
		}
		// ExternalName services carry no endpoints to proxy to.
		if service.Spec.Type == corev1.ServiceTypeExternalName {
			continue
		}
		if best == -1 || rank < best {
			best = rank
			chosen = service
		}
	}

	if best == -1 {
		return domain.MetricsBackend{}, false
	}

	port, ok := queryPort(chosen)
	if !ok {
		return domain.MetricsBackend{}, false
	}

	return domain.MetricsBackend{
		Kind:      "prometheus",
		Namespace: domain.NamespaceName(chosen.Namespace),
		Service:   chosen.Name,
		Port:      port,
	}, true
}

// nameRank scores a service name, lower being better, or -1 for no match.
func nameRank(name string) int {
	for index, candidate := range prometheusServiceNames {
		if name == candidate || strings.HasSuffix(name, "-"+candidate) {
			return index
		}
	}
	return -1
}

// queryPort finds the port that answers PromQL.
//
// By NAME first and 9090 second. A named port survives a chart that moves the
// number, and the number catches a bare deployment that named nothing —
// taking the first port regardless would proxy at whatever the chart happened
// to list first, which on the operator's service is a gRPC port.
func queryPort(service corev1.Service) (string, bool) {
	for _, wanted := range prometheusPortNames {
		for _, port := range service.Spec.Ports {
			if port.Name == wanted {
				return port.Name, true
			}
		}
	}
	for _, port := range service.Spec.Ports {
		if port.Port == 9090 {
			if port.Name != "" {
				return port.Name, true
			}
			return "9090", true
		}
	}
	return "", false
}

func (c *backendCache) get(id domain.ClusterID) (domain.MetricsBackend, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[id]
	if !ok || time.Since(entry.at) > backendCacheTTL {
		return domain.MetricsBackend{}, false
	}
	return entry.result, true
}

func (c *backendCache) put(id domain.ClusterID, result domain.MetricsBackend) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.entries == nil {
		c.entries = make(map[domain.ClusterID]backendEntry)
	}
	c.entries[id] = backendEntry{at: time.Now(), result: result}
}
