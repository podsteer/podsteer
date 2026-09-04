package application

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// WorkloadServiceDeps are the collaborators WorkloadService needs.
type WorkloadServiceDeps struct {
	// Workloads reads pods and controllers. Required.
	Workloads ports.WorkloadPort
	// Metrics supplies pod usage. Required, but allowed to fail.
	Metrics ports.MetricsPort
	// Registry tracks open connections. Required.
	Registry *Registry
	// Logger receives diagnostics. Optional; defaults to slog.Default.
	Logger *slog.Logger
}

// WorkloadService implements the workload-reading use cases.
type WorkloadService struct {
	workloads ports.WorkloadPort
	metrics   ports.MetricsPort
	registry  *Registry
	logger    *slog.Logger
}

// Compile-time proof that the service satisfies its inbound port.
var _ ports.WorkloadService = (*WorkloadService)(nil)

// NewWorkloadService validates deps and returns the service.
func NewWorkloadService(deps WorkloadServiceDeps) (*WorkloadService, error) {
	switch {
	case deps.Workloads == nil:
		return nil, errors.New("application: WorkloadService requires a WorkloadPort")
	case deps.Metrics == nil:
		return nil, errors.New("application: WorkloadService requires a MetricsPort")
	case deps.Registry == nil:
		return nil, errors.New("application: WorkloadService requires a Registry")
	}

	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &WorkloadService{
		workloads: deps.Workloads,
		metrics:   deps.Metrics,
		registry:  deps.Registry,
		logger:    logger.With(slog.String("service", "workload")),
	}, nil
}

// ListPods returns the pods of a connected cluster, enriched with usage.
//
// Sorted by namespace then name so the table is stable between refreshes: the
// API server returns pods in etcd key order, which shifts as objects come and
// go and would make rows jump under the cursor.
func (s *WorkloadService) ListPods(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) ([]domain.Pod, error) {
	if _, err := s.registry.Get(id); err != nil {
		return nil, fmt.Errorf("listing pods: %w", err)
	}

	pods, err := s.workloads.ListPods(ctx, id, namespace)
	if err != nil {
		return nil, fmt.Errorf("listing pods in %q of %q: %w", namespace, id, err)
	}

	pods = s.withPodMetrics(ctx, id, namespace, pods)

	slices.SortStableFunc(pods, func(a, b domain.Pod) int {
		if byNamespace := cmp.Compare(a.Namespace(), b.Namespace()); byNamespace != 0 {
			return byNamespace
		}
		return cmp.Compare(a.Name(), b.Name())
	})

	return pods, nil
}

// ListWorkloads returns controllers of the given kind, sorted by namespace
// then name.
func (s *WorkloadService) ListWorkloads(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName) ([]domain.Workload, error) {
	if _, err := s.registry.Get(id); err != nil {
		return nil, fmt.Errorf("listing %ss: %w", kind, err)
	}

	workloads, err := s.workloads.ListWorkloads(ctx, id, kind, namespace)
	if err != nil {
		return nil, fmt.Errorf("listing %ss in %q of %q: %w", kind, namespace, id, err)
	}

	slices.SortStableFunc(workloads, func(a, b domain.Workload) int {
		if byNamespace := cmp.Compare(a.Namespace(), b.Namespace()); byNamespace != 0 {
			return byNamespace
		}
		return cmp.Compare(a.Name(), b.Name())
	})

	return workloads, nil
}

// WorkloadConsumption sums what each controller in a list is using.
//
// SEPARATE FROM ListWorkloads, and called alongside it rather than inside it.
// The controllers themselves are one cheap read; this is the namespace's pods
// and their metrics, and on a cluster where the metrics API is missing or the
// account cannot list pods the list must still render. Folding it in would
// make a column cost the page.
//
// A pod is attributed by its controlling ownerReference — see
// domain.WorkloadConsumption. Two kinds do not own their pods directly, so
// their intermediates are read as the missing hop: a Deployment's ReplicaSets
// and a CronJob's Jobs. Both are small objects beside the pod list already
// being fetched.
func (s *WorkloadService) WorkloadConsumption(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName) (map[string]domain.AggregateUsage, error) {
	if _, err := s.registry.Get(id); err != nil {
		return nil, fmt.Errorf("reading %s usage: %w", kind, err)
	}

	workloads, err := s.workloads.ListWorkloads(ctx, id, kind, namespace)
	if err != nil {
		return nil, fmt.Errorf("listing %ss in %q of %q: %w", kind, namespace, id, err)
	}

	pods, err := s.workloads.ListPods(ctx, id, namespace)
	if err != nil {
		return nil, fmt.Errorf("listing pods in %q of %q: %w", namespace, id, err)
	}
	pods, measured := podsWithUsage(ctx, s.metrics, s.logger, id, namespace, pods)

	var intermediates []domain.Workload
	if hop, needed := intermediateKind(kind); needed {
		// FAILING RATHER THAN REPORTING IDLE. Without the hop every
		// controller of this kind reads as running nothing — which is also
		// the ordinary state of a CronJob between runs and a Deployment
		// scaled to zero, so the two would be indistinguishable. An error
		// puts a dash and a reason on screen; a silent zero is a lie.
		found, err := s.workloads.ListWorkloads(ctx, id, hop, namespace)
		if err != nil {
			return nil, fmt.Errorf("listing %ss in %q of %q: %w", hop, namespace, id, err)
		}
		intermediates = found
	}

	return domain.WorkloadConsumption(workloads, pods, intermediates, measured), nil
}

// intermediateKind names the object that stands between a controller and its
// pods, for the two kinds that do not own theirs directly.
func intermediateKind(kind domain.WorkloadKind) (domain.WorkloadKind, bool) {
	switch kind {
	case domain.WorkloadDeployment:
		return domain.WorkloadReplicaSet, true
	case domain.WorkloadCronJob:
		return domain.WorkloadJob, true
	default:
		return "", false
	}
}

// WorkloadUsage sums what one controller's pods are consuming.
//
// THROUGH THE SAME FUNCTION THE LIST USES, deliberately. It used to have its
// own path — ListPodsForWorkload, which resolves ownership inside the adapter
// — and the two rules disagreed: the meter in the row and the chart in the
// panel were computed differently and would show different numbers for the
// same controller. Sharing the rule is what makes them agree by construction
// rather than by two people remembering to change both.
//
// It costs listing the controllers of this kind as well as the pods, which is
// the cheap half of a read this was doing anyway.
func (s *WorkloadService) WorkloadUsage(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, kind domain.WorkloadKind, name string) (domain.AggregateUsage, error) {
	consumption, err := s.WorkloadConsumption(ctx, id, kind, namespace)
	if err != nil {
		return domain.AggregateUsage{}, err
	}

	// Absent means the controller is gone, or is not in the namespace being
	// browsed. A zero reading is the honest answer either way: there are no
	// pods to count.
	return consumption[namespace.String()+"/"+name], nil
}

// withPodMetrics attaches usage to pods, degrading silently when the cluster
// serves no metrics API. See ClusterService.withNodeMetrics for why silently.
func (s *WorkloadService) withPodMetrics(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, pods []domain.Pod) []domain.Pod {
	enriched, _ := podsWithUsage(ctx, s.metrics, s.logger, id, namespace, pods)
	return enriched
}

// podsWithUsage attaches measured usage to pods and reports whether the
// cluster answered at all.
//
// Shared by the use cases that need pods with figures on them. The BOOLEAN is
// the reason it is not simply a slice: zero usage on a cluster with no metrics
// API is the absence of a measurement, and a caller that cannot tell the two
// apart reports an unmeasured namespace as an idle one.
func podsWithUsage(
	ctx context.Context,
	metrics ports.MetricsPort,
	logger *slog.Logger,
	id domain.ClusterID,
	namespace domain.NamespaceName,
	pods []domain.Pod,
) ([]domain.Pod, bool) {
	// No early return on an empty slice, deliberately. The answer is not only
	// the enriched pods but whether the cluster serves metrics AT ALL, and an
	// empty namespace on a metered cluster must not report the same thing as
	// a cluster with no metrics-server — that distinction is what stops the
	// panel telling somebody to install one.
	usage, err := metrics.PodMetrics(ctx, id, namespace)
	if err != nil {
		if !errors.Is(err, ports.ErrMetricsUnavailable) {
			logger.WarnContext(ctx, "pod metrics unavailable",
				slog.String("cluster", id.String()),
				slog.String("error", err.Error()))
		}
		return pods, false
	}

	enriched := make([]domain.Pod, 0, len(pods))
	for _, pod := range pods {
		if measured, ok := usage[pod.Namespace().String()+"/"+pod.Name()]; ok {
			pod = pod.WithPodUsage(measured)
		}
		enriched = append(enriched, pod)
	}

	return enriched, true
}

// ListApplications groups a cluster's workloads by the application they
// belong to.
//
// FROM THE READS ALREADY BEING MADE. Every controller kind and the pod list
// are polled anyway, and they are coalesced by the adapter's read cache — so
// on a refresh where anything else has asked for them this costs nothing new,
// and where it has not it costs the same reads the workload pages make.
//
// Workloads and pods only, deliberately. The recommended labels are carried
// by everything a chart deploys — Services, Ingresses, ConfigMaps — but
// listing every kind in the cluster to count them would turn one page into a
// dozen reads. What an application IS, to somebody looking at this list, is
// the things that run; the panel for one member reaches the rest.
func (s *WorkloadService) ListApplications(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) (domain.ApplicationInventory, error) {
	if _, err := s.registry.Get(id); err != nil {
		return domain.ApplicationInventory{}, fmt.Errorf("listing applications: %w", err)
	}

	objects := make([]domain.ApplicationObject, 0)

	pods, err := s.workloads.ListPods(ctx, id, namespace)
	if err != nil {
		return domain.ApplicationInventory{}, fmt.Errorf(
			"listing pods in %q of %q: %w", namespace, id, err)
	}
	// With their measurements, so an application can be metered the way a
	// namespace and a controller are. The read is coalesced with whatever
	// else this tick asked for.
	pods, measured := podsWithUsage(ctx, s.metrics, s.logger, id, namespace, pods)
	for _, pod := range pods {
		objects = append(objects, domain.ApplicationObject{
			Kind:      "Pod",
			Namespace: pod.Namespace(),
			Labels:    pod.Labels(),
		})
	}

	for _, kind := range domain.WorkloadKinds() {
		workloads, err := s.workloads.ListWorkloads(ctx, id, kind, namespace)
		if err != nil {
			// One kind an account may not list must not empty the page: an
			// application is still found through its other members.
			//
			// The count it reports is then SHORT AND DOES NOT SAY SO, which
			// is a known gap rather than an intended design — the inventory
			// carries no field for "a kind was skipped", so a reader cannot
			// tell an application with no CronJobs from an account that may
			// not list them. `Unlabelled` exists for the analogous case and
			// this one wants the same treatment.
			s.logger.DebugContext(ctx, "a kind was not counted towards applications",
				slog.String("cluster", id.String()),
				slog.String("kind", string(kind)),
				slog.String("error", err.Error()))
			continue
		}
		for _, workload := range workloads {
			objects = append(objects, domain.ApplicationObject{
				Kind:      string(workload.Kind()),
				Namespace: workload.Namespace(),
				Labels:    workload.Labels(),
			})
		}
	}

	return domain.NewApplicationInventory(objects, pods, measured), nil
}

// PodGraph returns the dependency chain around one pod.
//
// Thin, and correctly so: the reading is the adapter's and the rules are the
// domain's. What is left here is the registry check every use case does and
// the join between the two.
func (s *WorkloadService) PodGraph(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName string) (domain.PodGraph, error) {
	if _, err := s.registry.Get(id); err != nil {
		return domain.PodGraph{}, fmt.Errorf("mapping pod dependencies: %w", err)
	}

	input, err := s.workloads.PodGraphSources(ctx, id, namespace, podName)
	if err != nil {
		return domain.PodGraph{}, fmt.Errorf("reading dependencies for %s/%s in %q: %w",
			namespace, podName, id, err)
	}
	return domain.NewPodGraph(input), nil
}

// WorkloadGraph returns the dependency chain around one workload.
func (s *WorkloadService) WorkloadGraph(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, kind domain.WorkloadKind, name string) (domain.PodGraph, error) {
	if _, err := s.registry.Get(id); err != nil {
		return domain.PodGraph{}, fmt.Errorf("mapping workload dependencies: %w", err)
	}

	input, err := s.workloads.WorkloadGraphSources(ctx, id, namespace, kind, name)
	if err != nil {
		return domain.PodGraph{}, fmt.Errorf("reading dependencies for %s/%s in %q: %w",
			kind, name, namespace, err)
	}
	return domain.NewWorkloadGraph(input), nil
}

// ListPodsOnNode returns the pods the scheduler has placed on one node.
//
// NOT ENRICHED WITH METRICS, unlike the workload listing. Usage for these pods
// is already on the node's own list row, which is where somebody opened this
// from; fetching PodMetrics for every namespace to decorate a list of names
// would be a second cluster-wide read for a column the panel does not show.
func (s *WorkloadService) ListPodsOnNode(ctx context.Context, id domain.ClusterID, nodeName string) ([]domain.Pod, error) {
	if _, err := s.registry.Get(id); err != nil {
		return nil, fmt.Errorf("listing pods on node: %w", err)
	}

	pods, err := s.workloads.ListPodsOnNode(ctx, id, nodeName)
	if err != nil {
		return nil, fmt.Errorf("listing pods on node %q of %q: %w", nodeName, id, err)
	}
	return pods, nil
}

// DrainCandidates returns the pods on a node with the extra facts a drain
// plan needs.
//
// Lives here beside ListPodsOnNode rather than on ManagementService: it is a
// read, and ManagementAPI's PlanDrain preview borrows it through this
// service — via the same registry check as every other read here — so a
// preview asked for after a cluster disconnects fails the same ordinary way
// a pod list would, rather than reaching an adapter with no client to use.
func (s *WorkloadService) DrainCandidates(ctx context.Context, id domain.ClusterID, nodeName string) ([]domain.DrainCandidate, error) {
	if _, err := s.registry.Get(id); err != nil {
		return nil, fmt.Errorf("listing drain candidates: %w", err)
	}

	candidates, err := s.workloads.DrainCandidates(ctx, id, nodeName)
	if err != nil {
		return nil, fmt.Errorf("listing drain candidates on node %q of %q: %w", nodeName, id, err)
	}
	return candidates, nil
}

// ListPodsForWorkload returns all pods owned by a specific workload.
func (s *WorkloadService) ListPodsForWorkload(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, kind domain.WorkloadKind, name string) ([]domain.Pod, error) {
	if _, err := s.registry.Get(id); err != nil {
		return nil, fmt.Errorf("listing pods for workload: %w", err)
	}

	pods, err := s.workloads.ListPodsForWorkload(ctx, id, namespace, kind, name)
	if err != nil {
		return nil, fmt.Errorf("listing pods for %s/%s in %q of %q: %w", kind, name, namespace, id, err)
	}

	// Enrich with metrics
	pods = s.withPodMetrics(ctx, id, namespace, pods)

	return pods, nil
}
