package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// FleetServiceDeps are the collaborators FleetService needs.
type FleetServiceDeps struct {
	// Workloads reads one cluster's pods and controllers. Required.
	//
	// The application service, not the outbound port: a cross-cluster read
	// must be the SAME read the cluster's own tab makes — sorted, enriched
	// with usage, refused for a cluster that has since closed — so the
	// adapter's read cache coalesces the two when they land in the same
	// tick, and so the merged table cannot disagree with the tab beside it.
	Workloads ports.WorkloadService
	// Events reads one cluster's events. Required.
	Events ports.EventService
	// Resources reads one cluster's arbitrary kinds as a table, for the
	// merged view of a kind that has no typed reader. Required.
	//
	// The application service again, for the reason Workloads is: the merged
	// table has to be the SAME read the cluster's own tab makes, or the two
	// disagree about what is in a cluster they are both showing.
	Resources ports.ResourceService
	// Catalog resolves a kind's group and resource to whatever version each
	// cluster serves it at. Required for the table read; see
	// domain.Catalog.LookupByResource for why an id is not portable.
	Catalog *domain.Catalog
	// Registry says which clusters are open, and in what order. Required.
	Registry *Registry
	// ReadBudget is how long one cluster is waited for before it is reported
	// slow and the rest are answered without it. Optional; defaults to
	// fleetReadBudget. Tests shorten it.
	ReadBudget time.Duration
	// Logger receives diagnostics. Optional; defaults to slog.Default.
	Logger *slog.Logger
}

// fleetConcurrency bounds how many clusters are read at once.
//
// Four, the same as apiWriterConcurrency: enough that a handful of tabs
// answer together, few enough that a dozen open clusters do not each start a
// pod list in the same instant on a laptop that is also running everything
// else. A cluster that outlives its budget releases its slot to the next
// one, so the reads still in flight can briefly exceed this by the number of
// slow clusters — bounded, and the alternative is queueing the healthy
// clusters behind the sick one.
const fleetConcurrency = 4

// fleetReadBudget is how long the fan-out waits for any one cluster.
//
// THE OTHERS DO NOT WAIT. A merged table is only worth having if a cluster
// behind a dropped VPN — which fails only when its dial times out, tens of
// seconds later — cannot hold every other cluster's rows hostage. A cluster
// still unanswered at the budget is reported slow, its read is left running
// and whatever it eventually returns is handed to the next read of the same
// thing (see readOne). Shorter than the fastest refresh the application
// offers (5s) so ticks cannot pile up behind it, and long enough that a big
// cluster over a slow link is not called slow for the crime of having five
// thousand pods.
const fleetReadBudget = 4 * time.Second

// FleetService reads across every open cluster at once.
//
// The fleet is the set of clusters currently open — one per tab — and a
// fleet read is the per-cluster read each tab makes, made for several tabs
// in one call and answered per cluster. It exists for the merged Pods,
// Workloads and Events tables, which are what an operator with six clusters
// open otherwise reconstructs by flipping between them.
//
// Two rules, each with a test:
//
//   - EVERY CLUSTER ANSWERS FOR ITSELF. A refused, unreachable or slow
//     cluster is reported as such beside the others' rows; nothing here
//     returns an error because one cluster did. The only error is asking for
//     a cluster that is not open, which is the caller's mistake.
//   - THE SLOWEST CLUSTER DOES NOT SET THE PACE. See fleetReadBudget.
type FleetService struct {
	workloads ports.WorkloadService
	events    ports.EventService
	resources ports.ResourceService
	catalog   *domain.Catalog
	registry  *Registry
	budget    time.Duration
	logger    *slog.Logger

	// late holds what a read that outlived its budget eventually came back
	// with, keyed by cluster, read and namespace, until the next read of the
	// same thing picks it up. See readOne, and sweepLate for what keeps the
	// map from being every question anyone has ever asked.
	late sync.Map
}

// lateAnswerTTL is how old a late answer may be and still be shown.
//
// A late answer exists to make a SLOW cluster show its rows one tick behind
// instead of never. Nothing about it was ever meant to bridge a gap of
// minutes, and it easily could: the key names a namespace and, for a table,
// a kind, so an operator who narrows the filter, opens a different kind, or
// simply leaves the merged view and comes back is asking a question whose
// last answer may be arbitrarily old. Handed that, the table would present
// rows from before lunch as this tick's, marked only "slow".
//
// Two minutes, which is longer than the slowest refresh the application
// offers and therefore never discards an answer a steadily slow cluster is
// about to be asked for again, and short enough that nothing here can pass
// off yesterday's rows as today's.
const lateAnswerTTL = 2 * time.Minute

// Compile-time proof that the service satisfies its inbound port, and that a
// disconnect can reach it.
var (
	_ ports.FleetService = (*FleetService)(nil)
	_ ClusterInvalidator = (*FleetService)(nil)
)

// Invalidate drops what this service holds for one cluster, for a disconnect.
//
// IT JOINS THE Invalidators LIST FOR THE OVERVIEW'S REASON, sharpened by what
// a late answer is. A tab is routinely reconnected because its kubeconfig
// context now points at a different cluster — and the settings path
// reconnects every open cluster at once, without closing a single tab. A late
// answer stored under the old connection would then be handed to the first
// read of the new one and rendered as that cluster's rows. Nothing on screen
// would say otherwise: the row is labelled with the cluster and marked
// "slow", both of which are about the tab, not about which cluster the rows
// were read from.
func (s *FleetService) Invalidate(id domain.ClusterID) {
	prefix := string(id) + "|"
	s.late.Range(func(key, _ any) bool {
		if name, ok := key.(string); ok && strings.HasPrefix(name, prefix) {
			s.late.Delete(name)
		}
		return true
	})
}

// sweepLate drops late answers nothing is going to ask for again.
//
// THE BOUND THE FIELD'S OWN DOC CLAIMED AND DID NOT KEEP. `late` was
// described as bounded by three reads times the clusters ever opened, but the
// key also names a NAMESPACE and, for a table, a KIND — so it grew by one
// entry for every distinct question ever asked, and an answer nobody came
// back for was never removed at all. An operator moving between namespaces on
// an intermittently slow cluster accumulated them for the life of the
// process.
//
// Once per fan-out rather than on a timer: a fan-out is the only thing that
// writes the map, so there is no moment it can grow unobserved, and a map
// this size costs less to walk than a goroutine costs to own.
func (s *FleetService) sweepLate() {
	s.late.Range(func(key, value any) bool {
		if answer, ok := value.(lateAnswer); ok && time.Since(answer.at) > lateAnswerTTL {
			s.late.Delete(key)
		}
		return true
	})
}

// NewFleetService validates deps and returns the service.
func NewFleetService(deps FleetServiceDeps) (*FleetService, error) {
	switch {
	case deps.Workloads == nil:
		return nil, errors.New("application: FleetService requires a WorkloadService")
	case deps.Events == nil:
		return nil, errors.New("application: FleetService requires an EventService")
	case deps.Resources == nil:
		return nil, errors.New("application: FleetService requires a ResourceService")
	case deps.Catalog == nil:
		return nil, errors.New("application: FleetService requires a Catalog")
	case deps.Registry == nil:
		return nil, errors.New("application: FleetService requires a Registry")
	}

	budget := deps.ReadBudget
	if budget <= 0 {
		budget = fleetReadBudget
	}

	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &FleetService{
		workloads: deps.Workloads,
		events:    deps.Events,
		resources: deps.Resources,
		catalog:   deps.Catalog,
		registry:  deps.Registry,
		budget:    budget,
		logger:    logger.With(slog.String("service", "fleet")),
	}, nil
}

// FLEET READS CARRY NO ANNOTATION PROJECTION. Custom columns are configured
// per KIND (see domain.Projection and the navigator's own lists), and the
// merged tables are not a kind — they hold three kinds' rows side by side
// under one id. Reading with the zero projection also keeps a fleet read
// sharing the read cache's answer with the tab's own poll, which a
// projected read would be keyed apart from. Labels ride every row already,
// so `label:` works here exactly as it does on a single cluster.
// ListPods lists pods in the given namespace of each cluster.
func (s *FleetService) ListPods(ctx context.Context, ids []domain.ClusterID, namespace domain.NamespaceName) ([]domain.ClusterRead[domain.Pod], error) {
	return fanOut(ctx, s, "pods", namespace, ids, func(ctx context.Context, id domain.ClusterID) ([]domain.Pod, []string, error) {
		pods, err := s.workloads.ListPods(ctx, id, namespace, domain.Projection{})
		return pods, nil, err
	})
}

// ListWorkloads lists every kind in domain.FleetWorkloadKinds in the given
// namespace of each cluster.
func (s *FleetService) ListWorkloads(ctx context.Context, ids []domain.ClusterID, namespace domain.NamespaceName) ([]domain.ClusterRead[domain.Workload], error) {
	return fanOut(ctx, s, "workloads", namespace, ids, func(ctx context.Context, id domain.ClusterID) ([]domain.Workload, []string, error) {
		return s.readWorkloads(ctx, id, namespace)
	})
}

// ListEvents lists events in the given namespace of each cluster.
func (s *FleetService) ListEvents(ctx context.Context, ids []domain.ClusterID, namespace domain.NamespaceName) ([]domain.ClusterRead[domain.Event], error) {
	return fanOut(ctx, s, "events", namespace, ids, func(ctx context.Context, id domain.ClusterID) ([]domain.Event, []string, error) {
		events, err := s.events.ListEvents(ctx, id, namespace, domain.Projection{})
		return events, nil, err
	})
}

// ListTable lists ONE ARBITRARY KIND across every open cluster.
//
// The kind is named by its GROUP AND RESOURCE rather than by a kind id,
// because an id carries a version and a version is per-cluster: a CRD served
// at v1alpha1 on one cluster and v1 on another is the same kind, and matching
// the whole id would report the second as not having it. See
// domain.Catalog.LookupByResource.
//
// A cluster that does not serve the kind at all answers UNSERVED, which is an
// ordinary answer and not a failure — a CRD lives on the clusters that need
// it. Reported as failed it would read as an outage; reported as ok with no
// rows it would say "none here", which is a different claim.
//
// ONE TABLE PER CLUSTER, CARRIED AS A ONE-ELEMENT SLICE. The fan-out is
// generic over items and hands back `Items []T`; a table is one item, and
// making the ITEM the whole table rather than its rows is what keeps a
// cluster's columns attached to its own rows — including when a slow
// cluster's answer arrives late and is handed to the next read, where rows
// separated from their columns would be cells nothing could position.
func (s *FleetService) ListTable(ctx context.Context, ids []domain.ClusterID, group, resource string, namespace domain.NamespaceName) ([]domain.ClusterRead[domain.ResourceTable], error) {
	name := "table:" + group + "/" + resource

	return fanOut(ctx, s, name, namespace, ids, func(ctx context.Context, id domain.ClusterID) ([]domain.ResourceTable, []string, error) {
		kind, err := s.catalog.LookupByResource(id, group, resource)
		if err != nil {
			// Not wrapped in anything the classifier reads: this cluster is
			// answered UNSERVED by the caller below, and there is nothing
			// here for an operator to fix.
			return nil, nil, errKindUnserved
		}

		table, err := s.resources.ListTable(ctx, id, kind.ID(), namespace, domain.Projection{})
		if err != nil {
			return nil, nil, err
		}
		return []domain.ResourceTable{table}, nil, nil
	})
}

// errKindUnserved marks the one failure that is not one: this cluster simply
// does not have the kind. Package-private and never wrapped in an operator
// message, because classifyRead turns it into a verdict rather than a
// sentence.
var errKindUnserved = errors.New("this cluster does not serve that kind")

// readWorkloads reads one cluster's controllers, one list per kind.
//
// The kinds are read together rather than in turn — five round trips in a
// row would make every cluster look slow — and each fails on its own. An
// account that may list Deployments and not CronJobs is ordinary, and its
// Deployments are still worth showing: the answer is partial and names what
// is missing. Only when nothing answered is the cluster reported failed,
// with the first refusal standing for all of them — they are the same
// refusal or the same outage, and five copies of it say nothing more.
func (s *FleetService) readWorkloads(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) ([]domain.Workload, []string, error) {
	kinds := domain.FleetWorkloadKinds()
	results := make([][]domain.Workload, len(kinds))
	errs := make([]error, len(kinds))

	var wg sync.WaitGroup
	for i, kind := range kinds {
		wg.Go(func() {
			results[i], errs[i] = s.workloads.ListWorkloads(ctx, id, kind, namespace, domain.Projection{})
		})
	}
	wg.Wait()

	var (
		items    []domain.Workload
		missing  []string
		failures []error
	)
	for i, kind := range kinds {
		if errs[i] != nil {
			missing = append(missing, string(kind))
			failures = append(failures, fmt.Errorf("%s: %w", kind, errs[i]))
			continue
		}
		items = append(items, results[i]...)
	}

	switch {
	case len(failures) == len(kinds):
		return nil, nil, failures[0]
	case len(failures) > 0:
		return items, missing, errors.Join(failures...)
	default:
		return items, nil, nil
	}
}

// clusterReader reads one cluster's share of a fleet read.
//
// err is whatever failed, whole. missing is set only for a PARTIAL answer —
// some of what was asked arrived and the rest did not — and names the rest;
// a reader that got nothing returns err alone, so a total refusal is
// reported by its cause rather than as partial.
type clusterReader[T any] func(ctx context.Context, id domain.ClusterID) (items []T, missing []string, err error)

// fanOut reads every requested cluster and answers per cluster, in tab order.
//
// name says which read this is and namespace what it is scoped to; together
// with the cluster they name the question a late answer belongs to — see
// lateKey. A package function rather than a method because Go has no
// generic methods; the service is the receiver in all but syntax.
func fanOut[T any](ctx context.Context, s *FleetService, name string, namespace domain.NamespaceName, ids []domain.ClusterID, read clusterReader[T]) ([]domain.ClusterRead[T], error) {
	targets, err := s.targets(ids)
	if err != nil {
		return nil, err
	}

	s.sweepLate()

	results := make([]domain.ClusterRead[T], len(targets))

	// Bounded, not one goroutine per cluster outright: see fleetConcurrency.
	// No task returns an error — a cluster's failure is its own row's
	// verdict, never the call's — so the group's error is always nil and
	// Wait is only the barrier.
	var group errgroup.Group
	group.SetLimit(fleetConcurrency)
	for i, id := range targets {
		group.Go(func() error {
			results[i] = readOne(ctx, s, name, namespace, id, read)
			return nil
		})
	}
	_ = group.Wait()

	return results, nil
}

// targets resolves the requested clusters to the registry's own order.
//
// Tab order, whatever order the caller listed them in, so the merged table
// groups clusters the way the tab bar does. A cluster that is not open is
// refused for the whole call rather than reported as a failed row: the
// caller asked for something that does not exist, which is a bug or a race
// with a closing tab, and either way the next tick asks with the right set.
// One snapshot of the registry decides both questions, so a cluster closing
// between two looks cannot be accepted by one and dropped by the other.
func (s *FleetService) targets(ids []domain.ClusterID) ([]domain.ClusterID, error) {
	wanted := make(map[domain.ClusterID]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}

	ordered := make([]domain.ClusterID, 0, len(wanted))
	for _, cluster := range s.registry.All() {
		if wanted[cluster.ID()] {
			ordered = append(ordered, cluster.ID())
		}
	}

	if len(ordered) != len(wanted) {
		for _, id := range ids {
			if !slices.Contains(ordered, id) {
				return nil, fmt.Errorf("fleet: cluster %q: %w", id, domain.ErrClusterNotConnected)
			}
		}
	}
	return ordered, nil
}

// lateAnswer is what a read that outlived its budget came back with.
//
// items is []T for whichever T the read produces; the key it is stored
// under names the read, so the assertion on the way out cannot meet a
// different type.
type lateAnswer struct {
	items   any
	missing []string
	err     error
	// at is when the read finally answered, which is what decides whether it
	// is still worth showing. See lateAnswerTTL.
	at time.Time
}

// lateKey names the question a late answer belongs to: which cluster, which
// read, and which namespace it was scoped to.
//
// THE NAMESPACE IS PART OF THE QUESTION. Keyed by cluster and read alone, an
// operator who narrowed the filter while a cluster was slow would be handed
// that cluster's late rows for the OLD namespace and see them rendered under
// the new one — the right cluster's wrong answer, which is worse than none.
// The same "<cluster>|<rest>" shape the adapter's read cache keys by, and
// for the same reason it is sound: a ClusterID may not contain the
// separator, and a namespace is a DNS label.
func lateKey(id domain.ClusterID, name string, namespace domain.NamespaceName) string {
	return string(id) + "|" + name + "|" + namespace.String()
}

// readOne reads one cluster within the budget, and settles what to say when
// it does not answer in time.
//
// A read still running at the budget is NOT cancelled. It carries on, on a
// context freed from the caller's cancellation but not its deadline — the
// bridge call this arrived on returns and cancels the moment the fan-out
// does, and a read cancelled with it could never become an answer — and
// whatever it eventually returns is kept for the next read of the same
// cluster, kind and namespace. That is what turns "slow" into something
// true: a cluster that is merely slow shows its rows one tick late instead
// of never, and one that is actually down is reported unreachable once its
// dial has timed out instead of slow forever. The goroutine exits when the
// read returns, which the request deadline bounds.
func readOne[T any](ctx context.Context, s *FleetService, name string, namespace domain.NamespaceName, id domain.ClusterID, read clusterReader[T]) domain.ClusterRead[T] {
	key := lateKey(id, name, namespace)

	type answer struct {
		items   []T
		missing []string
		err     error
	}
	// Buffered: a read that finishes after nobody is waiting must be able
	// to put its answer down and leave.
	done := make(chan answer, 1)

	readCtx, release := outlive(ctx)
	go func() {
		defer release()
		items, missing, err := read(readCtx, id)
		done <- answer{items: items, missing: missing, err: err}
	}()

	timer := time.NewTimer(s.budget)
	defer timer.Stop()

	select {
	case a := <-done:
		// In time. Anything an earlier, slower read left behind is older
		// than this and must not be handed to anyone.
		s.late.Delete(key)
		return settle(s, id, name, a.items, a.missing, a.err)
	case <-timer.C:
	}

	// Over budget. Whatever this read comes back with goes to the next one.
	go func() {
		a := <-done
		s.late.Store(key, lateAnswer{items: a.items, missing: a.missing, err: a.err, at: time.Now()})
	}()

	// And this one answers with whatever the read before it left, if
	// anything: a late success is still a slow cluster's, and says so.
	//
	// UNLESS IT HAS GONE OFF. The key names a namespace and, for a table, a
	// kind, so the previous answer to this exact question may be from before
	// the operator changed the filter and went to lunch. Presented here it
	// would be this tick's rows in every respect the interface shows — see
	// lateAnswerTTL.
	if previous, found := s.late.LoadAndDelete(key); found {
		late := previous.(lateAnswer)
		if time.Since(late.at) <= lateAnswerTTL {
			items, _ := late.items.([]T)
			verdict := settle(s, id, name, items, late.missing, late.err)
			if verdict.Status == domain.ClusterReadOK {
				verdict.Status = domain.ClusterReadSlow
			}
			return verdict
		}
	}

	s.logger.Debug("fleet read over budget",
		slog.String("cluster", string(id)),
		slog.String("read", name),
		slog.Duration("budget", s.budget))

	return domain.ClusterRead[T]{Cluster: id, Status: domain.ClusterReadSlow}
}

// settle turns what a cluster came back with into its verdict.
func settle[T any](s *FleetService, id domain.ClusterID, name string, items []T, missing []string, err error) domain.ClusterRead[T] {
	verdict := domain.ClusterRead[T]{Cluster: id, Items: items, Missing: missing, Err: err}

	switch {
	case err == nil:
		verdict.Status = domain.ClusterReadOK
		return verdict
	case len(missing) > 0:
		verdict.Status = domain.ClusterReadPartial
	default:
		verdict.Status = classifyRead(err)
	}

	s.logger.Debug("fleet read degraded",
		slog.String("cluster", string(id)),
		slog.String("read", name),
		slog.String("status", string(verdict.Status)),
		slog.String("error", err.Error()))

	return verdict
}

// classifyRead maps a failed read onto what the operator needs to know.
//
// The same split metricsStatusFor makes, for the same reason: forbidden and
// unreachable call for opposite actions. A deadline that passed counts as
// unreachable — the cluster did not answer in the time the request allowed,
// which is what an operator behind a dead tunnel sees — where a
// cancellation is nobody's fault and simply failed.
func classifyRead(err error) domain.ClusterReadStatus {
	switch {
	case errors.Is(err, errKindUnserved):
		return domain.ClusterReadUnserved
	case errors.Is(err, ports.ErrForbidden):
		return domain.ClusterReadForbidden
	case errors.Is(err, ports.ErrUnreachable), errors.Is(err, context.DeadlineExceeded):
		return domain.ClusterReadUnreachable
	default:
		return domain.ClusterReadFailed
	}
}

// outlive frees ctx from its cancellation while keeping its deadline.
//
// The adapter's read cache draws the same distinction, and for the same
// reason: how long an answer is worth waiting for is a property of the
// request; that one caller has stopped waiting is not.
func outlive(ctx context.Context) (context.Context, context.CancelFunc) {
	free := context.WithoutCancel(ctx)
	deadline, ok := ctx.Deadline()
	if !ok {
		return context.WithCancel(free)
	}
	return context.WithDeadline(free, deadline)
}
