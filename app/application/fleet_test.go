package application_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// fakeFleetSource stands in for the per-cluster services a fleet read fans
// out to.
//
// It embeds the two inbound ports it satisfies so only the three reads the
// fleet makes need writing; anything else is a nil-interface panic, which
// is the right outcome for a fleet that started calling something new
// without a test noticing.
type fakeFleetSource struct {
	ports.WorkloadService
	ports.EventService

	mu sync.Mutex

	pods      map[domain.ClusterID][]domain.Pod
	workloads map[domain.ClusterID][]domain.Workload
	events    map[domain.ClusterID][]domain.Event
	// errs fails every read of a cluster; kindErrs fails one workload kind
	// on every cluster, for the partial case.
	errs     map[domain.ClusterID]error
	kindErrs map[domain.WorkloadKind]error
	// gates hold a cluster's reads open until the channel is closed.
	gates map[domain.ClusterID]chan struct{}

	calls       []domain.ClusterID
	kinds       []domain.WorkloadKind
	inFlight    int
	maxInFlight int
	// completed counts reads that have returned — including ones the fleet
	// stopped waiting for, which is what a late-answer test needs to see.
	completed int
}

func (f *fakeFleetSource) enter(id domain.ClusterID) {
	f.mu.Lock()
	f.calls = append(f.calls, id)
	f.inFlight++
	f.maxInFlight = max(f.maxInFlight, f.inFlight)
	gate := f.gates[id]
	f.mu.Unlock()

	if gate != nil {
		<-gate
	}
}

func (f *fakeFleetSource) leave() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inFlight--
	f.completed++
}

func (f *fakeFleetSource) started() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeFleetSource) finished() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.completed
}

// hold replaces the gate a cluster's reads block on. Under the lock, because
// enter reads the map from the fleet's own goroutines.
func (f *fakeFleetSource) hold(id domain.ClusterID, gate chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gates[id] = gate
}

func (f *fakeFleetSource) ListPods(_ context.Context, id domain.ClusterID, _ domain.NamespaceName) ([]domain.Pod, error) {
	f.enter(id)
	defer f.leave()
	if err := f.errs[id]; err != nil {
		return nil, err
	}
	return append([]domain.Pod(nil), f.pods[id]...), nil
}

func (f *fakeFleetSource) ListWorkloads(_ context.Context, id domain.ClusterID, kind domain.WorkloadKind, _ domain.NamespaceName) ([]domain.Workload, error) {
	f.enter(id)
	defer f.leave()

	f.mu.Lock()
	f.kinds = append(f.kinds, kind)
	f.mu.Unlock()

	if err := f.errs[id]; err != nil {
		return nil, err
	}
	if err := f.kindErrs[kind]; err != nil {
		return nil, err
	}
	var out []domain.Workload
	for _, workload := range f.workloads[id] {
		if workload.Kind() == kind {
			out = append(out, workload)
		}
	}
	return out, nil
}

func (f *fakeFleetSource) ListEvents(_ context.Context, id domain.ClusterID, _ domain.NamespaceName) ([]domain.Event, error) {
	f.enter(id)
	defer f.leave()
	if err := f.errs[id]; err != nil {
		return nil, err
	}
	return append([]domain.Event(nil), f.events[id]...), nil
}

// newFleetService wires a fleet service over source with the given clusters
// open, in that order.
func newFleetService(t *testing.T, source *fakeFleetSource, budget time.Duration, open ...string) *application.FleetService {
	t.Helper()

	registry := application.NewRegistry()
	for _, id := range open {
		registry.Open(mustCluster(t, id, false))
	}

	service, err := application.NewFleetService(application.FleetServiceDeps{
		Workloads:  source,
		Events:     source,
		Registry:   registry,
		ReadBudget: budget,
	})
	if err != nil {
		t.Fatalf("NewFleetService() error = %v", err)
	}
	return service
}

func ids(names ...string) []domain.ClusterID {
	out := make([]domain.ClusterID, len(names))
	for i, name := range names {
		out[i] = domain.ClusterID(name)
	}
	return out
}

func clusterPod(t *testing.T, cluster, name string) domain.Pod {
	t.Helper()

	pod, err := domain.NewPod(domain.PodSpec{
		Name:      name,
		Namespace: "default",
		ClusterID: domain.ClusterID(cluster),
		Phase:     domain.PodPhaseRunning,
	})
	if err != nil {
		t.Fatalf("building pod %s: %v", name, err)
	}
	return pod
}

func clusterWorkload(t *testing.T, cluster string, kind domain.WorkloadKind, name string) domain.Workload {
	t.Helper()

	workload, err := domain.NewWorkload(domain.WorkloadSpec{
		Kind:      kind,
		Name:      name,
		Namespace: "default",
		ClusterID: domain.ClusterID(cluster),
	})
	if err != nil {
		t.Fatalf("building workload %s: %v", name, err)
	}
	return workload
}

func clusterEvent(t *testing.T, cluster, name string) domain.Event {
	t.Helper()

	event, err := domain.NewEvent(domain.EventSpec{
		Name:      name,
		Namespace: "default",
		ClusterID: domain.ClusterID(cluster),
		Type:      domain.EventWarning,
		Reason:    "BackOff",
	})
	if err != nil {
		t.Fatalf("building event %s: %v", name, err)
	}
	return event
}

func statuses[T any](reads []domain.ClusterRead[T]) map[domain.ClusterID]domain.ClusterReadStatus {
	out := make(map[domain.ClusterID]domain.ClusterReadStatus, len(reads))
	for _, read := range reads {
		out[read.Cluster] = read.Status
	}
	return out
}

func order[T any](reads []domain.ClusterRead[T]) []domain.ClusterID {
	out := make([]domain.ClusterID, len(reads))
	for i, read := range reads {
		out[i] = read.Cluster
	}
	return out
}

func TestNewFleetServiceRejectsMissingDependencies(t *testing.T) {
	t.Parallel()

	source := &fakeFleetSource{}
	registry := application.NewRegistry()

	cases := []struct {
		name string
		deps application.FleetServiceDeps
	}{
		{"no workloads", application.FleetServiceDeps{Events: source, Registry: registry}},
		{"no events", application.FleetServiceDeps{Workloads: source, Registry: registry}},
		{"no registry", application.FleetServiceDeps{Workloads: source, Events: source}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := application.NewFleetService(tc.deps); err == nil {
				t.Errorf("NewFleetService() succeeded with %s, want an error", tc.name)
			}
		})
	}
}

// TestFleetListPodsFollowsTheRegistrysTabOrder pins that the merged table
// groups clusters the way the tab bar does, whatever order the caller named
// them in, and that naming a cluster twice reads it once.
func TestFleetListPodsFollowsTheRegistrysTabOrder(t *testing.T) {
	t.Parallel()

	source := &fakeFleetSource{pods: map[domain.ClusterID][]domain.Pod{
		"prod":    {clusterPod(t, "prod", "api-0")},
		"staging": {clusterPod(t, "staging", "api-0")},
		"dev":     {clusterPod(t, "dev", "api-0")},
	}}
	service := newFleetService(t, source, 0, "prod", "staging", "dev")

	reads, err := service.ListPods(context.Background(), ids("dev", "prod", "dev", "staging"), domain.NamespaceAll)
	if err != nil {
		t.Fatalf("ListPods() error = %v", err)
	}

	if got, want := order(reads), ids("prod", "staging", "dev"); !slices.Equal(got, want) {
		t.Fatalf("ListPods() order = %v, want the registry's %v", got, want)
	}
	if got := source.started(); got != 3 {
		t.Errorf("reads made = %d, want 3 (one per distinct cluster)", got)
	}
	for _, read := range reads {
		if read.Status != domain.ClusterReadOK {
			t.Errorf("%s status = %s, want ok", read.Cluster, read.Status)
		}
		if len(read.Items) != 1 || read.Items[0].ClusterID() != read.Cluster {
			t.Errorf("%s items = %v, want that cluster's one pod", read.Cluster, read.Items)
		}
	}
}

func TestFleetListPodsRefusesAClusterThatIsNotOpen(t *testing.T) {
	t.Parallel()

	source := &fakeFleetSource{}
	service := newFleetService(t, source, 0, "dev")

	_, err := service.ListPods(context.Background(), ids("dev", "closed"), domain.NamespaceAll)
	if !errors.Is(err, domain.ErrClusterNotConnected) {
		t.Fatalf("ListPods() error = %v, want %v", err, domain.ErrClusterNotConnected)
	}
	if got := source.started(); got != 0 {
		t.Errorf("reads made = %d, want none — the call was refused whole", got)
	}
}

func TestFleetListPodsWithNoClustersReadsNothing(t *testing.T) {
	t.Parallel()

	source := &fakeFleetSource{}
	service := newFleetService(t, source, 0, "dev")

	reads, err := service.ListPods(context.Background(), nil, domain.NamespaceAll)
	if err != nil {
		t.Fatalf("ListPods() error = %v", err)
	}
	if len(reads) != 0 || source.started() != 0 {
		t.Errorf("ListPods(nil) = %v after %d reads, want nothing", reads, source.started())
	}
}

// TestFleetSurfacesEachClustersFailureOnItsOwn is the rule the feature
// exists for: one cluster refusing, one gone and one healthy is three
// verdicts and a table, not an error.
func TestFleetSurfacesEachClustersFailureOnItsOwn(t *testing.T) {
	t.Parallel()

	unreachable := fmt.Errorf("dial: %w", ports.ErrUnreachable)
	forbidden := fmt.Errorf("list pods: %w", ports.ErrForbidden)

	source := &fakeFleetSource{
		pods:   map[domain.ClusterID][]domain.Pod{"dev": {clusterPod(t, "dev", "web-0")}},
		events: map[domain.ClusterID][]domain.Event{"dev": {clusterEvent(t, "dev", "web-0.1")}},
		workloads: map[domain.ClusterID][]domain.Workload{
			"dev": {clusterWorkload(t, "dev", domain.WorkloadDeployment, "web")},
		},
		errs: map[domain.ClusterID]error{
			"prod":    forbidden,
			"staging": unreachable,
			"broken":  errors.New("something else"),
		},
	}
	service := newFleetService(t, source, 0, "prod", "staging", "dev", "broken")
	all := ids("prod", "staging", "dev", "broken")
	want := map[domain.ClusterID]domain.ClusterReadStatus{
		"prod":    domain.ClusterReadForbidden,
		"staging": domain.ClusterReadUnreachable,
		"dev":     domain.ClusterReadOK,
		"broken":  domain.ClusterReadFailed,
	}

	t.Run("pods", func(t *testing.T) {
		t.Parallel()
		reads, err := service.ListPods(context.Background(), all, domain.NamespaceAll)
		if err != nil {
			t.Fatalf("ListPods() error = %v", err)
		}
		assertVerdicts(t, reads, want)
		if len(reads[2].Items) != 1 {
			t.Errorf("dev items = %d, want the one pod that answered", len(reads[2].Items))
		}
	})

	t.Run("workloads", func(t *testing.T) {
		t.Parallel()
		reads, err := service.ListWorkloads(context.Background(), all, domain.NamespaceAll)
		if err != nil {
			t.Fatalf("ListWorkloads() error = %v", err)
		}
		assertVerdicts(t, reads, want)
		if len(reads[2].Items) != 1 {
			t.Errorf("dev items = %d, want the one deployment that answered", len(reads[2].Items))
		}
		// Every kind refused is one refusal, not a partial answer.
		if len(reads[0].Missing) != 0 {
			t.Errorf("prod missing = %v, want none on a total refusal", reads[0].Missing)
		}
	})

	t.Run("events", func(t *testing.T) {
		t.Parallel()
		reads, err := service.ListEvents(context.Background(), all, domain.NamespaceAll)
		if err != nil {
			t.Fatalf("ListEvents() error = %v", err)
		}
		assertVerdicts(t, reads, want)
		if len(reads[2].Items) != 1 {
			t.Errorf("dev items = %d, want the one event that answered", len(reads[2].Items))
		}
	})
}

func assertVerdicts[T any](t *testing.T, reads []domain.ClusterRead[T], want map[domain.ClusterID]domain.ClusterReadStatus) {
	t.Helper()

	got := statuses(reads)
	for cluster, status := range want {
		if got[cluster] != status {
			t.Errorf("%s status = %s, want %s", cluster, got[cluster], status)
		}
	}
	for _, read := range reads {
		if read.Status == domain.ClusterReadOK && read.Err != nil {
			t.Errorf("%s ok but carries error %v", read.Cluster, read.Err)
		}
		if read.Status != domain.ClusterReadOK && read.Err == nil {
			t.Errorf("%s %s but carries no error to say why", read.Cluster, read.Status)
		}
	}
}

// TestFleetListWorkloadsReportsAPartialClusterWithWhatIsMissing covers the
// account that may list Deployments and not CronJobs: its Deployments show,
// and the strip says what does not.
func TestFleetListWorkloadsReportsAPartialClusterWithWhatIsMissing(t *testing.T) {
	t.Parallel()

	source := &fakeFleetSource{
		workloads: map[domain.ClusterID][]domain.Workload{
			"dev": {
				clusterWorkload(t, "dev", domain.WorkloadDeployment, "web"),
				clusterWorkload(t, "dev", domain.WorkloadStatefulSet, "db"),
			},
		},
		kindErrs: map[domain.WorkloadKind]error{
			domain.WorkloadCronJob: fmt.Errorf("list cronjobs: %w", ports.ErrForbidden),
		},
	}
	service := newFleetService(t, source, 0, "dev")

	reads, err := service.ListWorkloads(context.Background(), ids("dev"), domain.NamespaceAll)
	if err != nil {
		t.Fatalf("ListWorkloads() error = %v", err)
	}

	read := reads[0]
	if read.Status != domain.ClusterReadPartial {
		t.Fatalf("status = %s, want partial", read.Status)
	}
	if !slices.Equal(read.Missing, []string{"CronJob"}) {
		t.Errorf("missing = %v, want [CronJob]", read.Missing)
	}
	if len(read.Items) != 2 {
		t.Errorf("items = %d, want the two controllers that answered", len(read.Items))
	}
	if !errors.Is(read.Err, ports.ErrForbidden) {
		t.Errorf("err = %v, want it to wrap %v so the adapter can say why", read.Err, ports.ErrForbidden)
	}
}

// TestFleetListWorkloadsReadsEveryKindButReplicaSet pins the read set to
// the domain's list — one request per cluster per kind, and no
// intermediates.
func TestFleetListWorkloadsReadsEveryKindButReplicaSet(t *testing.T) {
	t.Parallel()

	source := &fakeFleetSource{}
	service := newFleetService(t, source, 0, "dev")

	if _, err := service.ListWorkloads(context.Background(), ids("dev"), domain.NamespaceAll); err != nil {
		t.Fatalf("ListWorkloads() error = %v", err)
	}

	want := domain.FleetWorkloadKinds()
	got := slices.Clone(source.kinds)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("kinds read = %v, want %v", got, want)
	}
	if slices.Contains(got, domain.WorkloadReplicaSet) {
		t.Error("ReplicaSets were read; a merged table must not carry a Deployment's revisions")
	}
}

// TestFleetBoundsHowManyClustersAreReadAtOnce holds six clusters' reads
// open and asserts only four were started, then lets them go and asserts
// all six were read.
func TestFleetBoundsHowManyClustersAreReadAtOnce(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	clusters := []string{"a", "b", "c", "d", "e", "f"}
	source := &fakeFleetSource{gates: map[domain.ClusterID]chan struct{}{}}
	for _, id := range clusters {
		source.gates[domain.ClusterID(id)] = gate
	}
	// A budget long enough that the fan-out is still waiting when the
	// assertions run; the gate, not the clock, ends the test.
	service := newFleetService(t, source, time.Minute, clusters...)

	type outcome struct {
		reads []domain.ClusterRead[domain.Pod]
		err   error
	}
	result := make(chan outcome, 1)
	go func() {
		reads, err := service.ListPods(context.Background(), ids(clusters...), domain.NamespaceAll)
		result <- outcome{reads, err}
	}()

	waitFor(t, func() bool { return source.started() >= 4 })
	// Give a fifth read every chance to start if the limit were missing.
	time.Sleep(50 * time.Millisecond)
	if got := source.started(); got != 4 {
		t.Fatalf("reads in flight = %d, want exactly 4 while the first four are held", got)
	}

	close(gate)
	out := <-result
	if out.err != nil {
		t.Fatalf("ListPods() error = %v", out.err)
	}
	if got := source.started(); got != 6 {
		t.Errorf("reads made = %d, want all 6 once released", got)
	}
	if source.maxInFlight > 4 {
		t.Errorf("max in flight = %d, want at most 4", source.maxInFlight)
	}
	if len(out.reads) != 6 {
		t.Errorf("reads returned = %d, want 6", len(out.reads))
	}
}

// TestFleetReportsASlowClusterWithoutWaitingForIt is the second rule: the
// healthy cluster's rows come back at the budget with the slow one marked
// slow, not after the slow one's dial has timed out.
func TestFleetReportsASlowClusterWithoutWaitingForIt(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	t.Cleanup(func() { close(gate) })

	source := &fakeFleetSource{
		pods:  map[domain.ClusterID][]domain.Pod{"fast": {clusterPod(t, "fast", "web-0")}},
		gates: map[domain.ClusterID]chan struct{}{"slow": gate},
	}
	service := newFleetService(t, source, 50*time.Millisecond, "fast", "slow")

	started := time.Now()
	reads, err := service.ListPods(context.Background(), ids("fast", "slow"), domain.NamespaceAll)
	if err != nil {
		t.Fatalf("ListPods() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("ListPods() took %s, want it back at the budget", elapsed)
	}

	got := statuses(reads)
	if got["fast"] != domain.ClusterReadOK {
		t.Errorf("fast status = %s, want ok", got["fast"])
	}
	if got["slow"] != domain.ClusterReadSlow {
		t.Errorf("slow status = %s, want slow", got["slow"])
	}
	if len(reads[0].Items) != 1 {
		t.Errorf("fast items = %d, want its pod regardless of the slow cluster", len(reads[0].Items))
	}
	if reads[1].Err != nil {
		t.Errorf("slow err = %v, want none — nothing has failed yet", reads[1].Err)
	}
}

// TestFleetHandsALateAnswerToTheNextRead is what makes "slow" true rather
// than permanent: a read left running past its budget is not discarded, and
// the next read of the same cluster that also runs over reports what it
// came back with — rows for a cluster that is merely slow, and the failure
// for one that is actually down.
func TestFleetHandsALateAnswerToTheNextRead(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		pods       []domain.Pod
		err        error
		wantStatus domain.ClusterReadStatus
		wantItems  int
	}{
		{
			name:       "late rows arrive as slow",
			pods:       []domain.Pod{clusterPod(t, "slow", "web-0")},
			wantStatus: domain.ClusterReadSlow,
			wantItems:  1,
		},
		{
			name:       "late failure is reported as what it was",
			err:        fmt.Errorf("dial: %w", ports.ErrUnreachable),
			wantStatus: domain.ClusterReadUnreachable,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			first := make(chan struct{})
			source := &fakeFleetSource{
				pods:  map[domain.ClusterID][]domain.Pod{"slow": tc.pods},
				errs:  map[domain.ClusterID]error{},
				gates: map[domain.ClusterID]chan struct{}{"slow": first},
			}
			if tc.err != nil {
				source.errs["slow"] = tc.err
			}
			service := newFleetService(t, source, 30*time.Millisecond, "slow")

			reads, err := service.ListPods(context.Background(), ids("slow"), domain.NamespaceAll)
			if err != nil {
				t.Fatalf("first ListPods() error = %v", err)
			}
			if reads[0].Status != domain.ClusterReadSlow || len(reads[0].Items) != 0 {
				t.Fatalf("first read = %s with %d items, want slow and empty", reads[0].Status, len(reads[0].Items))
			}

			// Let the first read finish late, and hold every read after it
			// so the next call runs over its budget too.
			rest := make(chan struct{})
			t.Cleanup(func() { close(rest) })
			source.hold("slow", rest)
			close(first)

			// The late answer lands a moment after the gate opens; ask
			// until it has.
			var late domain.ClusterRead[domain.Pod]
			waitFor(t, func() bool {
				reads, err := service.ListPods(context.Background(), ids("slow"), domain.NamespaceAll)
				if err != nil {
					t.Fatalf("ListPods() error = %v", err)
				}
				late = reads[0]
				return late.Status != domain.ClusterReadSlow || len(late.Items) > 0
			})

			if late.Status != tc.wantStatus {
				t.Errorf("status = %s, want %s", late.Status, tc.wantStatus)
			}
			if len(late.Items) != tc.wantItems {
				t.Errorf("items = %d, want %d", len(late.Items), tc.wantItems)
			}
			if tc.err != nil && !errors.Is(late.Err, ports.ErrUnreachable) {
				t.Errorf("err = %v, want the late read's %v", late.Err, ports.ErrUnreachable)
			}
		})
	}
}

// TestFleetNeverHandsALateAnswerToAnotherNamespace pins that a late answer
// belongs to the question that asked it — cluster, kind AND namespace. An
// operator who narrows the filter while a cluster is slow must not be shown
// that cluster's late rows for the namespace they just left, rendered under
// the one they chose.
func TestFleetNeverHandsALateAnswerToAnotherNamespace(t *testing.T) {
	t.Parallel()

	const (
		namespaceA = domain.NamespaceName("shop")
		namespaceB = domain.NamespaceName("billing")
	)

	first := make(chan struct{})
	source := &fakeFleetSource{
		pods:  map[domain.ClusterID][]domain.Pod{"slow": {clusterPod(t, "slow", "web-0")}},
		gates: map[domain.ClusterID]chan struct{}{"slow": first},
	}
	service := newFleetService(t, source, 30*time.Millisecond, "slow")

	// Namespace A, over budget: slow and empty, read still running.
	reads, err := service.ListPods(context.Background(), ids("slow"), namespaceA)
	if err != nil {
		t.Fatalf("ListPods(%s) error = %v", namespaceA, err)
	}
	if reads[0].Status != domain.ClusterReadSlow || len(reads[0].Items) != 0 {
		t.Fatalf("first read of %s = %s with %d items, want slow and empty", namespaceA, reads[0].Status, len(reads[0].Items))
	}

	// Let that read finish late, hold everything after it, and wait until
	// its answer has actually come back before asking anything else — the
	// point is what happens once a late answer for A EXISTS.
	rest := make(chan struct{})
	t.Cleanup(func() { close(rest) })
	source.hold("slow", rest)
	close(first)
	waitFor(t, func() bool { return source.finished() >= 1 })
	// The fleet stores the answer a moment after the read returns.
	time.Sleep(20 * time.Millisecond)

	// Namespace B, over budget: nothing was ever asked for B, so nothing
	// may be handed over — not A's rows.
	reads, err = service.ListPods(context.Background(), ids("slow"), namespaceB)
	if err != nil {
		t.Fatalf("ListPods(%s) error = %v", namespaceB, err)
	}
	if reads[0].Status != domain.ClusterReadSlow {
		t.Errorf("read of %s status = %s, want slow", namespaceB, reads[0].Status)
	}
	if len(reads[0].Items) != 0 {
		t.Errorf("read of %s carried %d items, want none — those are %s's rows", namespaceB, len(reads[0].Items), namespaceA)
	}

	// Namespace A again, over budget: its own late rows, untouched by B's read.
	var late domain.ClusterRead[domain.Pod]
	waitFor(t, func() bool {
		reads, err := service.ListPods(context.Background(), ids("slow"), namespaceA)
		if err != nil {
			t.Fatalf("ListPods(%s) error = %v", namespaceA, err)
		}
		late = reads[0]
		return len(late.Items) > 0
	})
	if late.Status != domain.ClusterReadSlow || len(late.Items) != 1 {
		t.Errorf("late read of %s = %s with %d items, want slow with its one pod", namespaceA, late.Status, len(late.Items))
	}
}

// waitFor polls condition until it holds or the test's patience runs out.
func waitFor(t *testing.T, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
