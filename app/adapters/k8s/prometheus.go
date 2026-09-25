package k8s

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// Discovery of a monitoring stack already running in the cluster.
//
// TWO LIST CALLS, AND A RANKED LIST OUT OF THEM. A cluster genuinely holds
// several services that could be the answer — a kube-prometheus-stack install
// returns the operator's service, the alertmanager and sometimes an exporter
// under one label — so discovery produces an ORDER rather than a verdict.
// DiscoverMetricsBackend takes the head of that order, which is what every
// caller wanted anyway; ListMetricsBackends hands over the whole of it, so an
// operator can be offered the ones PodSteer did not pick.

// backendNameLabels are the app.kubernetes.io/name values worth asking about.
//
// MATCHED ON THE LABEL RATHER THAN THE NAME, for VictoriaMetrics especially:
// its operator renames every Service under `useLegacyNaming` (a VMSingle
// called `foo` becomes a Service called `foo`, with no prefix at all) while
// leaving the labels exactly as they are, and its Helm charts prefix the
// release name onto everything. The label is the only part that holds still.
//
// Prometheus keeps its NAME test as well, below, because that is what it has
// always used and because its label is worn by an alertmanager and a
// node-exporter too.
var backendNameLabels = []string{
	// Prometheus, from every recent chart including kube-prometheus-stack.
	"prometheus",
	// VictoriaMetrics through its operator: the single-node binary, and the
	// select component of a cluster. Verified against the operator's
	// SelectorLabels for VMSingle and VMCluster.
	"vmsingle",
	"vmselect",
	// VictoriaMetrics through its own Helm charts, which set the CHART name
	// here and put the component in app.kubernetes.io/component — so these
	// two also match vminsert and vmstorage, and the component is what tells
	// them apart. See classifyBackend.
	"victoria-metrics-single",
	"victoria-metrics-cluster",
}

// backendSelectors are the label selectors worth asking about.
//
// SELECTORS RATHER THAN LISTING EVERY SERVICE. A cluster can hold thousands
// of services and PodSteer would be reading all of them to find one; the API
// server can filter this for us. The first is a set-based selector over every
// standard app label above — ONE request for every product rather than one
// per product, which matters now that a candidate LIST means none of them can
// short-circuit the rest. The second is the older convention still on
// long-lived Prometheus installations, which has no set-based equivalent
// because the KEY differs rather than the value.
var backendSelectors = []string{
	"app.kubernetes.io/name in (" + strings.Join(backendNameLabels, ",") + ")",
	"app=prometheus",
}

// prometheusServiceNames are the names to accept for Prometheus, most
// specific first.
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

// prometheusPortNames are the port names a Prometheus query endpoint uses.
var prometheusPortNames = []string{"web", "http-web", "http"}

// prometheusPortNumbers is the number to fall back to when nothing is named.
var prometheusPortNumbers = []int32{9090}

// victoriaPortNames is the port name VictoriaMetrics uses, and it is the same
// one on every shape of it: the operator builds every Service with a port
// literally called "http", and both charts do too.
var victoriaPortNames = []string{"http"}

// victoriaPortNumbers are the numbers to fall back to when a Service named
// nothing, most likely first.
//
//   - 8429 is the operator's default for a VMSingle.
//   - 8428 is the binary's own default, which the operator ALSO publishes on
//     a VMSingle as an alias port, and which victoria-metrics-single and
//     victoria-metrics-k8s-stack both pin.
//   - 8481 is the select component, in the operator and in
//     victoria-metrics-cluster alike.
var victoriaPortNumbers = []int32{8429, 8428, 8481}

// victoriaSelectPrefix is where a VictoriaMetrics CLUSTER mounts the
// Prometheus query API: per tenant, under an account id.
//
// TENANT ZERO, and no other. Nothing in a Service, its labels or its ports
// records which account ids hold data, so any other number would be a guess
// PodSteer could not check — and a wrong tenant answers 200 with no series at
// all, which reads as a cluster that was idle. Zero is what every
// single-tenant install uses.
const victoriaSelectPrefix = "/select/0/prometheus"

// writeOnlyNameLabels are the app.kubernetes.io/name values of components
// that INGEST rather than answer.
//
// THE WRITE PATH IS NEVER A CANDIDATE. vmagent scrapes and forwards, vminsert
// accepts writes and shards them, vmstorage holds them — none of the three
// serves `/api/v1/query_range`, so proxying a query at one is a request that
// can only fail, and doing it against somebody's ingest path is a request
// worth being sure never leaves.
//
// The selectors above cannot match them: an operator-built vmagent carries
// `app.kubernetes.io/name=vmagent`, and the chart-built vminsert and
// vmstorage carry the chart name with a component that classifyBackend
// refuses. This list is defence in depth against a selector widened later,
// and it is asserted by a test rather than trusted.
var writeOnlyNameLabels = []string{"vmagent", "vminsert", "vmstorage"}

// Product ranks, lower being better.
//
// PROMETHEUS FIRST, and the reason is not that it is better: it is the one
// this build has always pointed at, so a cluster running both must keep
// answering the way it answered before VictoriaMetrics was recognised.
// Between the two VictoriaMetrics shapes, the single-node one goes first
// because it serves the query API at the root — no prefix, no tenant, nothing
// PodSteer had to assume.
const (
	rankPrometheus = iota
	rankVictoriaSingle
	rankVictoriaSelect
)

// candidate is one discovered Service and where it sorts.
type candidate struct {
	backend domain.MetricsBackend
	// product is the rank of the monitoring product, and name the rank of the
	// service name within it. Compared in that order.
	product int
	name    int
}

// backendCache holds one discovery result per cluster.
//
// Discovery answers a question whose value moves in DAYS — a monitoring stack
// is installed once — so re-running it per refresh would be two list calls
// every ten seconds for an answer that has not changed since the cluster was
// built.
//
// IT HOLDS THE WHOLE RANKED LIST, not the pick. The pick is the head of the
// list, so caching only that would mean a second round of list calls the
// moment somebody was offered the alternatives — for an answer the first
// round already had in its hands.
type backendCache struct {
	mu      sync.Mutex
	entries map[domain.ClusterID]backendEntry
}

type backendEntry struct {
	at      time.Time
	results []domain.MetricsBackend
}

// backendCacheTTL is how long a discovery result stands.
const backendCacheTTL = 30 * time.Minute

// DiscoverMetricsBackend looks for a monitoring system already in the cluster.
//
// A MISS IS NOT AN ERROR. Most clusters have no monitoring stack, or have one
// this account cannot list services to find, and both are ordinary — the
// caller gets a zero MetricsBackend and carries on with the in-memory
// samples. Only a genuine failure to ask is returned as one.
//
// It is the top of ListMetricsBackends' ranking, which is what it always
// returned: the single best guess at which service answers PromQL.
func (a *Adapter) DiscoverMetricsBackend(
	ctx context.Context,
	id domain.ClusterID,
) (domain.MetricsBackend, error) {
	backends, err := a.ListMetricsBackends(ctx, id)
	if err != nil || len(backends) == 0 {
		return domain.MetricsBackend{}, err
	}
	return backends[0], nil
}

// ListMetricsBackends returns every monitoring service that could answer
// PromQL, best first.
//
// A LIST BECAUSE THE CHOICE IS NOT PODSTEER'S TO MAKE ALONE. Ranking picks a
// default and a default is worth having, but a cluster with two monitoring
// stacks — or one Thanos querier beside the Prometheus it fronts — has a
// right answer PodSteer cannot know. The operator picks from what was found,
// never from a URL they typed; see domain.PreferredBackend.
//
// NOTHING HERE QUERIES ANYTHING. It lists services and reads their labels and
// ports. A service listing establishes that an object by that name exists —
// not that it is running, not that it scrapes this cluster, and not that it
// has retained anything — which is the same claim the note built on this has
// always made.
func (a *Adapter) ListMetricsBackends(
	ctx context.Context,
	id domain.ClusterID,
) ([]domain.MetricsBackend, error) {
	if cached, ok := a.backends.get(id); ok {
		return cached, nil
	}

	set, err := a.factory.clientsFor(id)
	if err != nil {
		return nil, err
	}

	var found []candidate
	for _, selector := range backendSelectors {
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
				a.backends.put(id, nil)
				return nil, nil
			}
			return nil, wrapped
		}

		for _, service := range services.Items {
			if matched, ok := classifyBackend(service); ok {
				found = append(found, matched)
			}
		}
	}

	backends := rankBackends(found)
	a.backends.put(id, backends)
	return backends, nil
}

// rankBackends sorts the candidates and drops the duplicates.
//
// BOTH SELECTORS CAN MATCH ONE SERVICE — a chart that sets the standard label
// and the old `app` one alike — so the same service arrives twice and would
// otherwise be offered to the operator twice.
func rankBackends(found []candidate) []domain.MetricsBackend {
	slices.SortFunc(found, func(a, b candidate) int {
		if diff := cmp.Compare(a.product, b.product); diff != 0 {
			return diff
		}
		if diff := cmp.Compare(a.name, b.name); diff != 0 {
			return diff
		}
		// TOTAL, so the order does not depend on which selector answered
		// first or on the order the API server happened to list in. A picker
		// whose rows move between openings is a picker nobody trusts.
		if diff := cmp.Compare(a.backend.Namespace, b.backend.Namespace); diff != 0 {
			return diff
		}
		return cmp.Compare(a.backend.Service, b.backend.Service)
	})

	seen := make(map[string]struct{}, len(found))
	backends := make([]domain.MetricsBackend, 0, len(found))
	for _, entry := range found {
		key := string(entry.backend.Namespace) + "/" + entry.backend.Service
		if _, already := seen[key]; already {
			continue
		}
		seen[key] = struct{}{}
		backends = append(backends, entry.backend)
	}

	if len(backends) == 0 {
		// nil rather than an empty slice, so "nothing was found" and "nothing
		// has been looked for" are the same value in the cache.
		return nil
	}
	return backends
}

// classifyBackend decides whether one service is a query endpoint, and which.
//
// The whole of the product knowledge is here rather than spread across the
// selectors, so it can be argued with in a test against a service value
// instead of against a cluster.
func classifyBackend(service corev1.Service) (candidate, bool) {
	// An ExternalName service carries no endpoints for the API server to
	// proxy to, so it would resolve and then fail at query time.
	if service.Spec.Type == corev1.ServiceTypeExternalName {
		return candidate{}, false
	}

	name := service.Labels["app.kubernetes.io/name"]
	component := service.Labels["app.kubernetes.io/component"]

	// THE WRITE PATH, REFUSED FIRST. See writeOnlyNameLabels.
	if slices.Contains(writeOnlyNameLabels, name) {
		return candidate{}, false
	}

	switch name {
	case "vmsingle":
		// The operator's single-node component. It serves the Prometheus
		// query API at the root, so no prefix.
		return victoriaCandidate(service, rankVictoriaSingle, "")
	case "vmselect":
		// The operator's cluster read component.
		return victoriaCandidate(service, rankVictoriaSelect, victoriaSelectPrefix)
	case "victoria-metrics-single":
		// The chart sets the CHART name here and the component below. Only
		// the server answers; anything else under this chart is not a query
		// endpoint.
		if component != "server" {
			return candidate{}, false
		}
		return victoriaCandidate(service, rankVictoriaSingle, "")
	case "victoria-metrics-cluster":
		// THE COMPONENT IS THE WHOLE TEST HERE. This chart puts the same
		// app.kubernetes.io/name on vmselect, vminsert AND vmstorage, so
		// accepting the label alone would make the ingest path a candidate —
		// and vmstorage even publishes a port literally named "vmselect",
		// which is exactly the sort of thing a looser test would fall for.
		if component != "vmselect" {
			return candidate{}, false
		}
		return victoriaCandidate(service, rankVictoriaSelect, victoriaSelectPrefix)
	default:
		return prometheusCandidate(service)
	}
}

// prometheusCandidate accepts a service whose NAME says it answers PromQL.
//
// Ranked by name rather than taking the first match, because a
// kube-prometheus-stack installation returns several services carrying the
// same label — the operator's headless service, the alertmanager, sometimes a
// node-exporter — and only one of them answers PromQL.
func prometheusCandidate(service corev1.Service) (candidate, bool) {
	rank := nameRank(service.Name)
	if rank < 0 {
		return candidate{}, false
	}
	port, ok := servicePort(service, prometheusPortNames, prometheusPortNumbers)
	if !ok {
		return candidate{}, false
	}

	return candidate{
		backend: domain.MetricsBackend{
			Kind:      domain.MetricsBackendPrometheus,
			Namespace: domain.NamespaceName(service.Namespace),
			Service:   service.Name,
			Port:      port,
		},
		product: rankPrometheus,
		name:    rank,
	}, true
}

// victoriaCandidate accepts a VictoriaMetrics service the labels have already
// identified.
//
// NO NAME TEST, deliberately, and it is the difference from Prometheus above.
// The label has already said which component this is, and the name is exactly
// the part VictoriaMetrics does not hold still: `useLegacyNaming` strips the
// operator's prefix entirely, and both charts prepend a release name. A name
// test would refuse installations that are perfectly ordinary.
func victoriaCandidate(service corev1.Service, product int, prefix string) (candidate, bool) {
	port, ok := servicePort(service, victoriaPortNames, victoriaPortNumbers)
	if !ok {
		return candidate{}, false
	}

	return candidate{
		backend: domain.MetricsBackend{
			Kind:      domain.MetricsBackendVictoriaMetrics,
			Namespace: domain.NamespaceName(service.Namespace),
			Service:   service.Name,
			Port:      port,
			Prefix:    prefix,
		},
		product: product,
		// One name rank for every VictoriaMetrics service: the label decided
		// which component this is, so there is nothing left for a name to
		// break a tie on. The namespace and name do that, totally, in
		// rankBackends.
		name: 0,
	}, true
}

// nameRank scores a Prometheus service name, lower being better, or -1 for no
// match.
func nameRank(name string) int {
	for index, candidate := range prometheusServiceNames {
		if name == candidate || strings.HasSuffix(name, "-"+candidate) {
			return index
		}
	}
	return -1
}

// servicePort finds the port that answers the query API.
//
// By NAME first and by NUMBER second. A named port survives a chart that
// moves the number, and the numbers catch a bare deployment that named
// nothing — taking the first port regardless would proxy HTTP at whatever the
// chart happened to list first, which on the Prometheus operator's service is
// a gRPC port.
func servicePort(service corev1.Service, names []string, numbers []int32) (string, bool) {
	for _, wanted := range names {
		for _, port := range service.Spec.Ports {
			if port.Name == wanted {
				return port.Name, true
			}
		}
	}
	for _, wanted := range numbers {
		for _, port := range service.Spec.Ports {
			if port.Port != wanted {
				continue
			}
			if port.Name != "" {
				return port.Name, true
			}
			return strconv.Itoa(int(wanted)), true
		}
	}
	return "", false
}

func (c *backendCache) get(id domain.ClusterID) ([]domain.MetricsBackend, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[id]
	if !ok || time.Since(entry.at) > backendCacheTTL {
		return nil, false
	}
	// A COPY. The slice is handed to callers that may hold it while the next
	// one reads, and a shared backing array is how one caller's sort becomes
	// another's ranking.
	return slices.Clone(entry.results), true
}

// forget drops one cluster's discovery result, for a disconnect.
//
// THE LAST PER-CLUSTER CACHE THAT DID NOT DO THIS, and it holds the answer
// with the longest life of any of them: thirty minutes, because a monitoring
// stack is installed once. That is exactly what made it wrong to keep. A tab
// is routinely reconnected because its kubeconfig context now points at a
// DIFFERENT cluster, and the discovery result is a Service coordinate —
// namespace, name, port. Carried across, PodSteer went on proxying PromQL to
// a service address it had never looked for in the cluster it was now talking
// to: a graph either empty for half an hour with no explanation, or, where a
// stack of the same shape happens to exist, one drawn from a Prometheus this
// connection never discovered.
//
// The refusal cache beside it (queryRefusals) was already dropped here. This
// is the other half of the same question: whether monitoring is reachable,
// and where it is.
func (c *backendCache) forget(id domain.ClusterID) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, id)
}

func (c *backendCache) put(id domain.ClusterID, results []domain.MetricsBackend) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.entries == nil {
		c.entries = make(map[domain.ClusterID]backendEntry)
	}
	c.entries[id] = backendEntry{at: time.Now(), results: slices.Clone(results)}
}
