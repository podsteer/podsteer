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

// kubeStateSelectors are the label selectors worth asking about, in order.
//
// The same trade prometheus.go makes next door: a cluster can hold thousands
// of services and the API server can filter for us. The first is the standard
// app label the kube-state-metrics chart and kube-prometheus-stack both set;
// the second is the older `k8s-app` convention, which is what the addon
// manifests in several distributions still carry.
var kubeStateSelectors = []string{
	"app.kubernetes.io/name=kube-state-metrics",
	"k8s-app=kube-state-metrics",
}

// kubeStateName is the service name every distribution of it uses, bare or
// with a Helm release prefixed.
const kubeStateName = "kube-state-metrics"

// kubeStatePortNames are the port names its metrics endpoint uses, most
// specific first. `telemetry` is deliberately absent: that port serves
// kube-state-metrics' own process metrics rather than the cluster's objects,
// so a service that exposed only telemetry is not the endpoint anybody means.
var kubeStatePortNames = []string{"http-metrics", "metrics", "http"}

// kubeStateCache holds one discovery result per cluster.
//
// Same question, same cadence and therefore the same TTL as backendCache: an
// operator installs kube-state-metrics once and it is there for the life of
// the cluster, so re-running two list calls for it on every ten-second poll
// would be paying for an answer that has not changed since the cluster was
// built.
type kubeStateCache struct {
	mu      sync.Mutex
	entries map[domain.ClusterID]kubeStateEntry
}

type kubeStateEntry struct {
	at     time.Time
	result domain.KubeStateMetrics
}

// DiscoverKubeStateMetrics looks for kube-state-metrics already in the cluster.
//
// A MISS IS NOT AN ERROR, exactly as it is not one for DiscoverMetricsBackend.
// Most clusters do not run kube-state-metrics, and plenty of accounts cannot
// list services across namespaces to find out; both are ordinary, both return
// a zero KubeStateMetrics, and neither is a degraded cluster. Only a genuine
// failure to ask comes back as an error.
//
// NOTHING HERE EVER SCRAPES IT. A service listing establishes that an object
// by that name and those labels exists — not that it is running, not that it
// is scraped by anything, and not that any series it produces has been
// retained. The note built on this claims only the first, which is the same
// discipline the Prometheus note keeps.
func (a *Adapter) DiscoverKubeStateMetrics(ctx context.Context, id domain.ClusterID) (domain.KubeStateMetrics, error) {
	if cached, ok := a.kubeState.get(id); ok {
		return cached, nil
	}

	set, err := a.factory.clientsFor(id)
	if err != nil {
		return domain.KubeStateMetrics{}, err
	}

	var found domain.KubeStateMetrics
	for _, selector := range kubeStateSelectors {
		services, err := set.typed.CoreV1().Services(metav1.NamespaceAll).
			List(ctx, metav1.ListOptions{LabelSelector: selector, Limit: 50})
		if err != nil {
			wrapped := classify("discovering kube-state-metrics", err)

			// A REFUSAL IS CACHED, a transport failure is not — the rule
			// DiscoverMetricsBackend states and for its reasons. An account
			// that may not list services cluster-wide never will be able to,
			// and retrying every poll writes two denied requests into
			// somebody's audit log forever for a note that is an offer rather
			// than a requirement. A cluster that was merely unreachable comes
			// back, and should be asked again when it does rather than in
			// half an hour.
			if errors.Is(wrapped, ports.ErrForbidden) || errors.Is(wrapped, ports.ErrUnauthenticated) {
				a.kubeState.put(id, domain.KubeStateMetrics{})
				return domain.KubeStateMetrics{}, nil
			}
			return domain.KubeStateMetrics{}, wrapped
		}

		if state, ok := pickKubeStateMetrics(services.Items); ok {
			found = state
			break
		}
	}

	a.kubeState.put(id, found)
	return found, nil
}

// pickKubeStateMetrics chooses one installation from a candidate set.
//
// Ranked rather than first-match, the same as pickPrometheus, because a
// cluster can genuinely hold two: a distribution's own addon in kube-system
// and a kube-prometheus-stack release in monitoring. Either would be a true
// answer, so what matters is that the answer is the SAME one on every read —
// a note that named a different namespace each refresh would read as the
// cluster changing.
//
// TWO DELIBERATE DIVERGENCES FROM pickPrometheus, both because nothing here
// is ever queried:
//
//   - An unrecognised port does NOT disqualify a service. Prometheus needs
//     the right port because the wrong one means proxying PromQL at a gRPC
//     listener; this needs no port at all, so a service whose port somebody
//     renamed is still kube-state-metrics and is still worth naming. The
//     port is reported when it can be recognised and left empty when it
//     cannot.
//   - An exact name outranks a Helm-prefixed one, where nameRank scores both
//     the same. Prometheus has four candidate names to rank against and this
//     has one, so a list of one would rank nothing and leave the two-install
//     case decided by whatever order the API server happened to return.
func pickKubeStateMetrics(services []corev1.Service) (domain.KubeStateMetrics, bool) {
	best := -1
	var chosen corev1.Service

	for _, service := range services {
		rank := kubeStateRank(service.Name)
		if rank < 0 {
			continue
		}
		// An ExternalName service is a pointer at something OUTSIDE this
		// cluster. Saying "kube-state-metrics is running in monitoring" about
		// one would be a false statement about where it is, which is the only
		// thing the note claims.
		if service.Spec.Type == corev1.ServiceTypeExternalName {
			continue
		}
		if best == -1 || rank < best || (rank == best && earlierService(service, chosen)) {
			best = rank
			chosen = service
		}
	}

	if best == -1 {
		return domain.KubeStateMetrics{}, false
	}

	return domain.KubeStateMetrics{
		Namespace: domain.NamespaceName(chosen.Namespace),
		Service:   chosen.Name,
		Port:      kubeStatePort(chosen),
	}, true
}

// kubeStateRank scores a service name, lower being better, or -1 for no match.
func kubeStateRank(name string) int {
	switch {
	case name == kubeStateName:
		return 0
	case strings.HasSuffix(name, "-"+kubeStateName):
		return 1
	default:
		return -1
	}
}

// earlierService breaks a rank tie deterministically, by namespace then name.
//
// Not cosmetic: two equally ranked installations decided by list order would
// make the note name one namespace on one refresh and the other on the next,
// and there is no ordering guarantee on a LIST to lean on instead.
func earlierService(candidate, incumbent corev1.Service) bool {
	if candidate.Namespace != incumbent.Namespace {
		return candidate.Namespace < incumbent.Namespace
	}
	return candidate.Name < incumbent.Name
}

// kubeStatePort finds the port serving the cluster's object metrics, or "".
//
// By NAME first and 8080 second, as queryPort does for Prometheus, and for
// the same reason: a named port survives a chart that moves the number, and
// the number catches a bare deployment that named nothing. Unlike queryPort
// this returns "" rather than refusing the service — see pickKubeStateMetrics.
func kubeStatePort(service corev1.Service) string {
	for _, wanted := range kubeStatePortNames {
		for _, port := range service.Spec.Ports {
			if port.Name == wanted {
				return port.Name
			}
		}
	}
	for _, port := range service.Spec.Ports {
		if port.Port == 8080 {
			if port.Name != "" {
				return port.Name
			}
			return "8080"
		}
	}
	return ""
}

func (c *kubeStateCache) get(id domain.ClusterID) (domain.KubeStateMetrics, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[id]
	if !ok || time.Since(entry.at) > backendCacheTTL {
		return domain.KubeStateMetrics{}, false
	}
	return entry.result, true
}

func (c *kubeStateCache) put(id domain.ClusterID, result domain.KubeStateMetrics) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.entries == nil {
		c.entries = make(map[domain.ClusterID]kubeStateEntry)
	}
	c.entries[id] = kubeStateEntry{at: time.Now(), result: result}
}

// forget drops one cluster's answer, for a disconnect.
//
// A half-hour answer carried across a reconnect would describe the cluster
// that tab held BEFORE it was closed — and a tab is routinely reconnected
// because a kubeconfig context now points somewhere else. Only the cluster
// named is dropped: closing one tab must not cost every other tab its answer.
func (c *kubeStateCache) forget(id domain.ClusterID) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, id)
}
