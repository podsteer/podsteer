package k8s

import (
	"context"
	"errors"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// PodMetrics returns usage keyed by "namespace/name".
//
// Cached, because it is the second half of every pod read and is asked for by
// each of them independently. metrics-server has no watch, so this stays a
// poll whatever else changes.
func (a *Adapter) PodMetrics(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) (map[string]domain.PodUsage, error) {
	// The same reuse the pod list makes, for the other half of the same read.
	// The map is keyed "namespace/name", so narrowing it is a prefix match.
	if !namespace.IsAll() {
		if all, borrowed := borrow[map[string]domain.PodUsage](&a.reads, readKey(id.String(), "podmetrics", "")); borrowed {
			return usageIn(all, namespace), nil
		}
	}

	return cachedRead(&a.reads, readKey(id.String(), "podmetrics", namespace.String()), func() (map[string]domain.PodUsage, error) {
		return a.podMetrics(ctx, id, namespace)
	})
}

// usageIn narrows cluster-wide usage to one namespace, into a map of its own.
func usageIn(usage map[string]domain.PodUsage, namespace domain.NamespaceName) map[string]domain.PodUsage {
	prefix := namespace.String() + "/"
	narrowed := make(map[string]domain.PodUsage, len(usage))
	for key, value := range usage {
		if strings.HasPrefix(key, prefix) {
			narrowed[key] = value
		}
	}
	return narrowed
}

// PodMetrics returns pod usage keyed by "namespace/name".
//
// A pod's usage is the sum of its containers': the metrics API reports per
// container, and the number an operator reads in a list is the total. THE
// BREAKDOWN IS KEPT AS WELL, because it costs nothing — this loop already
// visits every container to add it up — and because the total alone cannot
// answer the question the total provokes, which is which container is doing
// it.
func (a *Adapter) podMetrics(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) (map[string]domain.PodUsage, error) {
	op := fmt.Sprintf("reading pod metrics in %q of %q", namespace, id)

	set, err := a.factory.clientsFor(id)
	if err != nil {
		return nil, err
	}

	list, err := set.metrics.MetricsV1beta1().
		PodMetricses(namespace.String()).
		List(ctx, metav1.ListOptions{ResourceVersion: cachedResourceVersion})
	if err != nil {
		return nil, classifyMetrics(op, err)
	}

	usage := make(map[string]domain.PodUsage, len(list.Items))
	for i := range list.Items {
		item := &list.Items[i]

		var total domain.Metrics
		containers := make(map[string]domain.Metrics, len(item.Containers))
		for _, container := range item.Containers {
			measured := containerUsage(container)
			containers[container.Name] = measured
			total = total.Add(measured)
		}

		usage[item.Namespace+"/"+item.Name] = domain.PodUsage{Total: total, Containers: containers}
	}

	return usage, nil
}

// NodeMetrics returns usage keyed by node name. Cached alongside ListNodes,
// which every caller pairs it with.
func (a *Adapter) NodeMetrics(ctx context.Context, id domain.ClusterID) (map[string]domain.Metrics, error) {
	return cachedRead(&a.reads, readKey(id.String(), "nodemetrics"), func() (map[string]domain.Metrics, error) {
		return a.nodeMetrics(ctx, id)
	})
}

// NodeMetrics returns node usage keyed by node name.
func (a *Adapter) nodeMetrics(ctx context.Context, id domain.ClusterID) (map[string]domain.Metrics, error) {
	op := fmt.Sprintf("reading node metrics of %q", id)

	set, err := a.factory.clientsFor(id)
	if err != nil {
		return nil, err
	}

	list, err := set.metrics.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{ResourceVersion: cachedResourceVersion})
	if err != nil {
		return nil, classifyMetrics(op, err)
	}

	usage := make(map[string]domain.Metrics, len(list.Items))
	for i := range list.Items {
		item := &list.Items[i]
		usage[item.Name] = domain.NewMetrics(
			item.Usage.Cpu().MilliValue(),
			item.Usage.Memory().Value(),
		)
	}

	return usage, nil
}

// containerUsage converts one container's measurement.
func containerUsage(container metricsv1beta1.ContainerMetrics) domain.Metrics {
	return domain.NewMetrics(
		container.Usage.Cpu().MilliValue(),
		container.Usage.Memory().Value(),
	)
}

// classifyMetrics maps a metrics API failure, translating "this cluster has no
// metrics API" into ErrMetricsUnavailable.
//
// The distinction matters because the two failures call for opposite
// responses: a missing metrics API is an ordinary configuration that callers
// should quietly work around, while a timeout talking to a metrics API that
// does exist is a real problem worth reporting. The API server signals the
// former as 404 (no such endpoint) or 503 (the aggregated service has no
// backend), so both are folded into the sentinel.
func classifyMetrics(op string, err error) error {
	if err == nil {
		return nil
	}

	if apierrors.IsNotFound(err) ||
		apierrors.IsServiceUnavailable(err) ||
		errors.Is(err, ports.ErrNotFound) {
		return fmt.Errorf("%s: %w: %w", op, ports.ErrMetricsUnavailable, err)
	}

	return classify(op, err)
}
