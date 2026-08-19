package domain_test

import (
	"strings"
	"testing"
	"time"

	"k8sense/app/domain"
)

// The overview is the one place in K8Sense that makes a judgement rather than
// reporting a fact, so the judgements are what these tests pin down: which
// states count as problems, which do not, and how the problems group.

var overviewNow = time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)

// podFixture builds a pod, failing the test if the spec is invalid.
func podFixture(t *testing.T, spec domain.PodSpec) domain.Pod {
	t.Helper()

	if spec.Name == "" {
		spec.Name = "api-7d9f"
	}
	if spec.Namespace == "" {
		spec.Namespace = "default"
	}
	if spec.ClusterID == "" {
		spec.ClusterID = "dev"
	}
	if spec.CreatedAt.IsZero() {
		spec.CreatedAt = overviewNow.Add(-time.Hour)
	}

	pod, err := domain.NewPod(spec)
	if err != nil {
		t.Fatalf("building pod %q: %v", spec.Name, err)
	}
	return pod
}

// nodeFixture builds a ready node with the given capacity.
func nodeFixture(t *testing.T, name string, cpuMilli, memoryBytes, pods int64) domain.Node {
	t.Helper()

	node, err := domain.NewNode(domain.NodeSpec{
		Name:           name,
		ClusterID:      "dev",
		Ready:          true,
		KubeletVersion: "v1.32.7",
		Capacity:       domain.Capacity{CPUMilli: cpuMilli, MemoryBytes: memoryBytes, Pods: pods},
		Allocatable:    domain.Capacity{CPUMilli: cpuMilli, MemoryBytes: memoryBytes, Pods: pods},
		CreatedAt:      overviewNow.Add(-30 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("building node %q: %v", name, err)
	}
	return node
}

// findingByTitle returns the finding with the given title.
func findingByTitle(findings []domain.Finding, title string) (domain.Finding, bool) {
	for _, finding := range findings {
		if finding.Title == title {
			return finding, true
		}
	}
	return domain.Finding{}, false
}

func TestDiagnosePodStates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		pod          domain.PodSpec
		wantTitle    string // "" means the pod must raise no finding at all
		wantSeverity domain.Severity
	}{
		{
			name: "healthy running pod is not a problem",
			pod: domain.PodSpec{
				Phase:      domain.PodPhaseRunning,
				NodeName:   "node-1",
				Containers: []domain.Container{{Name: "app", Ready: true, State: domain.ContainerStateRunning}},
			},
		},
		{
			name: "completed pod is not a problem",
			pod: domain.PodSpec{
				Phase:      domain.PodPhaseSucceeded,
				NodeName:   "node-1",
				Containers: []domain.Container{{Name: "app", State: domain.ContainerStateTerminated, Reason: "Completed"}},
			},
		},
		{
			name: "crash loop is critical",
			pod: domain.PodSpec{
				Phase:    domain.PodPhaseRunning,
				NodeName: "node-1",
				Containers: []domain.Container{
					{Name: "app", State: domain.ContainerStateWaiting, Reason: "CrashLoopBackOff"},
				},
			},
			wantTitle:    "CrashLoopBackOff",
			wantSeverity: domain.SeverityCritical,
		},
		{
			name: "image pull failure is critical",
			pod: domain.PodSpec{
				Phase:    domain.PodPhasePending,
				NodeName: "node-1",
				Containers: []domain.Container{
					{Name: "app", State: domain.ContainerStateWaiting, Reason: "ImagePullBackOff"},
				},
			},
			wantTitle:    "Image cannot be pulled",
			wantSeverity: domain.SeverityCritical,
		},
		{
			name: "unschedulable pod is critical and keeps the scheduler's message",
			pod: domain.PodSpec{
				Phase:   domain.PodPhasePending,
				Reason:  "Unschedulable",
				Message: "0/6 nodes are available: 6 Insufficient cpu.",
			},
			wantTitle:    "Unschedulable",
			wantSeverity: domain.SeverityCritical,
		},
		{
			// A pod that has only just been created is normal churn during
			// every rollout; reporting it would make the dashboard useless.
			name: "briefly pending pod is not yet a problem",
			pod: domain.PodSpec{
				Phase:     domain.PodPhasePending,
				CreatedAt: overviewNow.Add(-15 * time.Second),
			},
		},
		{
			name: "pod pending past the grace period is a warning",
			pod: domain.PodSpec{
				Phase:     domain.PodPhasePending,
				CreatedAt: overviewNow.Add(-10 * time.Minute),
			},
			wantTitle:    "Pending",
			wantSeverity: domain.SeverityWarning,
		},
		{
			name: "evicted pod is critical",
			pod: domain.PodSpec{
				Phase:    domain.PodPhaseFailed,
				NodeName: "node-1",
				Reason:   "Evicted",
				Message:  "The node was low on resource: memory.",
			},
			wantTitle:    "Evicted",
			wantSeverity: domain.SeverityCritical,
		},
		{
			name: "terminating pod within the grace period is not a problem",
			pod: domain.PodSpec{
				Phase:     domain.PodPhaseTerminating,
				NodeName:  "node-1",
				CreatedAt: overviewNow.Add(-20 * time.Second),
			},
		},
		{
			name: "long-terminating pod is stuck",
			pod: domain.PodSpec{
				Phase:     domain.PodPhaseTerminating,
				NodeName:  "node-1",
				CreatedAt: overviewNow.Add(-time.Hour),
			},
			wantTitle:    "Stuck terminating",
			wantSeverity: domain.SeverityWarning,
		},
		{
			// Running but never passing readiness: no Service sends traffic
			// here, and nothing in the phase says so.
			name: "long-unready pod is a warning",
			pod: domain.PodSpec{
				Phase:      domain.PodPhaseRunning,
				NodeName:   "node-1",
				CreatedAt:  overviewNow.Add(-time.Hour),
				Containers: []domain.Container{{Name: "app", Ready: false, State: domain.ContainerStateRunning}},
			},
			wantTitle:    "Not ready",
			wantSeverity: domain.SeverityWarning,
		},
		{
			name: "unknown phase points at the node",
			pod: domain.PodSpec{
				Phase:    domain.PodPhaseUnknown,
				NodeName: "node-1",
			},
			wantTitle:    "Unknown state",
			wantSeverity: domain.SeverityWarning,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			overview := domain.NewOverview(domain.OverviewInput{
				ClusterID: "dev",
				Pods:      []domain.Pod{podFixture(t, test.pod)},
				Now:       overviewNow,
			})

			if test.wantTitle == "" {
				if len(overview.Findings) != 0 {
					t.Fatalf("expected no findings, got %d: %q",
						len(overview.Findings), overview.Findings[0].Title)
				}
				if overview.Health != domain.HealthHealthy {
					t.Errorf("health = %q, want healthy", overview.Health)
				}
				return
			}

			finding, ok := findingByTitle(overview.Findings, test.wantTitle)
			if !ok {
				t.Fatalf("no finding titled %q; got %v", test.wantTitle, titles(overview.Findings))
			}
			if finding.Severity != test.wantSeverity {
				t.Errorf("severity = %q, want %q", finding.Severity, test.wantSeverity)
			}
			if finding.Advice == "" {
				t.Error("finding carries no advice, which is the half a raw reason string lacks")
			}
		})
	}
}

// A finding must name the object it is about, or it cannot be acted on.
func TestUnschedulableFindingCarriesSchedulerMessage(t *testing.T) {
	t.Parallel()

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Pods: []domain.Pod{podFixture(t, domain.PodSpec{
			Name:    "queue-worker-5",
			Phase:   domain.PodPhasePending,
			Reason:  "Unschedulable",
			Message: "0/6 nodes are available: 6 Insufficient cpu.",
		})},
		Now: overviewNow,
	})

	finding, ok := findingByTitle(overview.Findings, "Unschedulable")
	if !ok {
		t.Fatal("expected an Unschedulable finding")
	}
	if len(finding.Subjects) != 1 || finding.Subjects[0].Name != "queue-worker-5" {
		t.Fatalf("subjects = %+v, want the pod named", finding.Subjects)
	}
	if !strings.Contains(finding.Subjects[0].Detail, "Insufficient cpu") {
		t.Errorf("detail = %q, want the scheduler's own message", finding.Subjects[0].Detail)
	}
}

// The grouping rule is the reason this dashboard is readable during an
// incident: one broken deployment is one line, not twelve.
func TestPodFindingsGroupByController(t *testing.T) {
	t.Parallel()

	pods := make([]domain.Pod, 0, 12)
	for i := range 12 {
		pods = append(pods, podFixture(t, domain.PodSpec{
			Name:     "api-7d9f-" + string(rune('a'+i)),
			NodeName: "node-1",
			Phase:    domain.PodPhaseRunning,
			Owners: []domain.OwnerReference{
				{Kind: "ReplicaSet", Name: "api-7d9f", Controller: true},
			},
			Containers: []domain.Container{
				{Name: "app", State: domain.ContainerStateWaiting, Reason: "CrashLoopBackOff"},
			},
		}))
	}

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Pods:      pods,
		Now:       overviewNow,
	})

	finding, ok := findingByTitle(overview.Findings, "CrashLoopBackOff")
	if !ok {
		t.Fatal("expected a CrashLoopBackOff finding")
	}
	if finding.Count != 12 {
		t.Errorf("count = %d, want 12", finding.Count)
	}
	if got := len(overview.Findings); got != 1 {
		t.Errorf("findings = %d, want the twelve pods collapsed into 1", got)
	}
	if !strings.Contains(finding.Summary, "api-7d9f") {
		t.Errorf("summary = %q, want the controller named", finding.Summary)
	}
}

// Pods of different controllers are different problems even when the reason
// matches — they are fixed by different people.
func TestPodFindingsSeparateDifferentControllers(t *testing.T) {
	t.Parallel()

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Pods: []domain.Pod{
			podFixture(t, domain.PodSpec{
				Name:     "api-1",
				NodeName: "node-1",
				Owners:   []domain.OwnerReference{{Kind: "ReplicaSet", Name: "api", Controller: true}},
				Containers: []domain.Container{
					{Name: "app", State: domain.ContainerStateWaiting, Reason: "CrashLoopBackOff"},
				},
			}),
			podFixture(t, domain.PodSpec{
				Name:     "worker-1",
				NodeName: "node-1",
				Owners:   []domain.OwnerReference{{Kind: "ReplicaSet", Name: "worker", Controller: true}},
				Containers: []domain.Container{
					{Name: "app", State: domain.ContainerStateWaiting, Reason: "CrashLoopBackOff"},
				},
			}),
		},
		Now: overviewNow,
	})

	if got := len(overview.Findings); got != 2 {
		t.Fatalf("findings = %d (%v), want one per controller", got, titles(overview.Findings))
	}
}

// Requests, not usage, decide what can be scheduled. This is the calculation
// the whole capacity section exists to make honest.
func TestCapacityCountsOnlyPodsOccupyingNodes(t *testing.T) {
	t.Parallel()

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Nodes:     []domain.Node{nodeFixture(t, "node-1", 4000, 8<<30, 110)},
		Pods: []domain.Pod{
			podFixture(t, domain.PodSpec{
				Name: "running", NodeName: "node-1", Phase: domain.PodPhaseRunning,
				Containers: []domain.Container{{
					Name:     "app",
					Ready:    true,
					State:    domain.ContainerStateRunning,
					Requests: domain.Resources{CPUMilli: 500, MemoryBytes: 1 << 30},
					Limits:   domain.Resources{CPUMilli: 1000, MemoryBytes: 2 << 30},
				}},
			}),
			// A finished Job pod still exists as an object but reserves
			// nothing. Counting it is the classic way to produce a cluster
			// utilisation figure that is quietly wrong.
			podFixture(t, domain.PodSpec{
				Name: "finished", NodeName: "node-1", Phase: domain.PodPhaseSucceeded,
				Containers: []domain.Container{{
					Name:     "app",
					State:    domain.ContainerStateTerminated,
					Reason:   "Completed",
					Requests: domain.Resources{CPUMilli: 2000, MemoryBytes: 4 << 30},
				}},
			}),
		},
		Now: overviewNow,
	})

	if got := overview.Capacity.CPU.Requests; got != 500 {
		t.Errorf("cpu requests = %dm, want 500m — the completed pod must not count", got)
	}
	if got := overview.Capacity.Pods.Scheduled; got != 1 {
		t.Errorf("scheduled pods = %d, want 1", got)
	}
	if got := overview.Capacity.CPU.Schedulable(); got != 3500 {
		t.Errorf("schedulable cpu = %dm, want 3500m", got)
	}
}

// A cordoned node's capacity is not available to anything, so counting it
// would overstate the headroom in exactly the situation — a drain — where
// headroom matters most.
func TestCapacityExcludesCordonedNodePodSlots(t *testing.T) {
	t.Parallel()

	cordoned, err := domain.NewNode(domain.NodeSpec{
		Name: "node-2", ClusterID: "dev", Ready: true, Unschedulable: true,
		Allocatable: domain.Capacity{CPUMilli: 4000, MemoryBytes: 8 << 30, Pods: 110},
	})
	if err != nil {
		t.Fatalf("building node: %v", err)
	}

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Nodes:     []domain.Node{nodeFixture(t, "node-1", 4000, 8<<30, 110), cordoned},
		Now:       overviewNow,
	})

	if got := overview.Capacity.Pods.Capacity; got != 110 {
		t.Errorf("pod capacity = %d, want 110 — the cordoned node contributes none", got)
	}
	if _, ok := findingByTitle(overview.Findings, "Nodes cordoned"); !ok {
		t.Error("expected the cordoned node to be reported")
	}
}

func TestEfficiencyReportsUsageAgainstRequests(t *testing.T) {
	t.Parallel()

	// PodUsage, not Usage: the node figure includes the kubelet, the runtime
	// and the OS, none of which requested anything — dividing it by pod
	// requests reports clusters as more than 100% "efficient".
	usage := domain.ResourceUsage{
		Allocatable: 4000, Requests: 2000, Usage: 900, PodUsage: 500, Measured: true,
	}
	if got := usage.Efficiency(); got != 25 {
		t.Errorf("efficiency = %.0f%%, want 25%% of what the pods reserved", got)
	}

	unmeasured := domain.ResourceUsage{Allocatable: 4000, Requests: 2000}
	if got := unmeasured.Efficiency(); got != -1 {
		t.Errorf("efficiency = %.0f, want -1 when nothing was measured", got)
	}
}

// A cluster that cannot schedule is a warning even when every graph is calm,
// which is the failure mode a usage-only dashboard cannot show.
func TestCapacityWarnsWhenRequestsFillTheCluster(t *testing.T) {
	t.Parallel()

	pods := make([]domain.Pod, 0, 4)
	for i := range 4 {
		pods = append(pods, podFixture(t, domain.PodSpec{
			Name: "greedy-" + string(rune('a'+i)), NodeName: "node-1", Phase: domain.PodPhaseRunning,
			Containers: []domain.Container{{
				Name:     "app",
				Ready:    true,
				State:    domain.ContainerStateRunning,
				Requests: domain.Resources{CPUMilli: 950, MemoryBytes: 1 << 30},
			}},
		}))
	}

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Nodes:     []domain.Node{nodeFixture(t, "node-1", 4000, 16<<30, 110)},
		Pods:      pods,
		Now:       overviewNow,
	})

	finding, ok := findingByTitle(overview.Findings, "CPU headroom nearly gone")
	if !ok {
		t.Fatalf("expected a CPU headroom finding; got %v", titles(overview.Findings))
	}
	if finding.Severity != domain.SeverityWarning {
		t.Errorf("severity = %q, want warning", finding.Severity)
	}
	if overview.Health != domain.HealthDegraded {
		t.Errorf("health = %q, want degraded", overview.Health)
	}
}

// A degraded deployment whose pods already explain themselves must not be
// reported twice at two levels of detail.
func TestWorkloadFindingSuppressedWhenPodsExplainIt(t *testing.T) {
	t.Parallel()

	deployment, err := domain.NewWorkload(domain.WorkloadSpec{
		Kind: domain.WorkloadDeployment, Name: "api", Namespace: "default",
		ClusterID: "dev", Desired: 3, Ready: 0, Current: 3, Updated: 3,
		CreatedAt: overviewNow.Add(-24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("building workload: %v", err)
	}

	// The pod is owned by the ReplicaSet, not the Deployment — which is the
	// whole reason the ownership has to be resolved rather than assumed.
	replicaSet, err := domain.NewWorkload(domain.WorkloadSpec{
		Kind: domain.WorkloadReplicaSet, Name: "api-7d9f8b4c9", Namespace: "default",
		ClusterID: "dev", Desired: 3, Ready: 0,
		Owner: domain.OwnerReference{Kind: "Deployment", Name: "api", Controller: true},
	})
	if err != nil {
		t.Fatalf("building replicaset: %v", err)
	}

	crashing := podFixture(t, domain.PodSpec{
		Name: "api-7d9f8b4c9-x2k4l", NodeName: "node-1", Phase: domain.PodPhaseRunning,
		Owners: []domain.OwnerReference{{Kind: "ReplicaSet", Name: "api-7d9f8b4c9", Controller: true}},
		Containers: []domain.Container{
			{Name: "app", State: domain.ContainerStateWaiting, Reason: "CrashLoopBackOff"},
		},
	})

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Pods:      []domain.Pod{crashing},
		Workloads: []domain.Workload{deployment, replicaSet},
		Now:       overviewNow,
	})

	if _, ok := findingByTitle(overview.Findings, "Deployment not at desired scale"); ok {
		t.Error("the deployment finding duplicates the CrashLoopBackOff one and should be suppressed")
	}
	finding, ok := findingByTitle(overview.Findings, "CrashLoopBackOff")
	if !ok {
		t.Fatal("the specific finding must survive")
	}
	// The generated ReplicaSet hash is not what anybody calls the workload.
	if !strings.Contains(finding.Summary, "default/api") || strings.Contains(finding.Summary, "7d9f8b4c9") {
		t.Errorf("summary = %q, want the deployment named rather than the replicaset hash", finding.Summary)
	}
}

// An undegraded workload that owns no failing pods must still be reported when
// it is short of replicas — the suppression above must not swallow everything.
func TestWorkloadFindingReportsUnexplainedDegradation(t *testing.T) {
	t.Parallel()

	statefulSet, err := domain.NewWorkload(domain.WorkloadSpec{
		Kind: domain.WorkloadStatefulSet, Name: "postgres", Namespace: "data",
		ClusterID: "dev", Desired: 3, Ready: 1, Current: 3, Updated: 3,
		CreatedAt: overviewNow.Add(-72 * time.Hour),
	})
	if err != nil {
		t.Fatalf("building workload: %v", err)
	}

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Workloads: []domain.Workload{statefulSet},
		Now:       overviewNow,
	})

	finding, ok := findingByTitle(overview.Findings, "StatefulSet not at desired scale")
	if !ok {
		t.Fatalf("expected a StatefulSet finding; got %v", titles(overview.Findings))
	}
	if finding.Count != 1 || len(finding.Subjects) != 1 {
		t.Fatalf("finding = %+v, want the one statefulset named", finding)
	}
	if finding.Subjects[0].Detail != "1/3 ready" {
		t.Errorf("detail = %q, want the replica shortfall", finding.Subjects[0].Detail)
	}
	if finding.KindID != "apps/v1/statefulsets" {
		t.Errorf("kindID = %q, want the statefulsets list", finding.KindID)
	}
}

// A scaled-to-zero deployment is a decision, not a fault.
func TestWorkloadFindingIgnoresScaledToZero(t *testing.T) {
	t.Parallel()

	idle, err := domain.NewWorkload(domain.WorkloadSpec{
		Kind: domain.WorkloadDeployment, Name: "batch", Namespace: "default",
		ClusterID: "dev", Desired: 0, Ready: 0,
	})
	if err != nil {
		t.Fatalf("building workload: %v", err)
	}

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Workloads: []domain.Workload{idle},
		Now:       overviewNow,
	})

	if len(overview.Findings) != 0 {
		t.Errorf("findings = %v, want none", titles(overview.Findings))
	}
	if overview.Health != domain.HealthHealthy {
		t.Errorf("health = %q, want healthy", overview.Health)
	}
}

// Events already explained by a pod finding are dropped, so a crash-looping
// pod does not appear under both "CrashLoopBackOff" and "BackOff".
func TestEventFindingsSkipAlreadyExplainedObjects(t *testing.T) {
	t.Parallel()

	event, err := domain.NewEvent(domain.EventSpec{
		Name: "api-1.17c", Namespace: "default", ClusterID: "dev",
		Type: domain.EventWarning, Reason: "BackOff",
		Message:      "Back-off restarting failed container",
		InvolvedKind: "Pod", InvolvedName: "api-1", Count: 9,
		FirstSeen: overviewNow.Add(-10 * time.Minute), LastSeen: overviewNow.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("building event: %v", err)
	}

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Pods: []domain.Pod{podFixture(t, domain.PodSpec{
			Name: "api-1", NodeName: "node-1", Phase: domain.PodPhaseRunning,
			Containers: []domain.Container{
				{Name: "app", State: domain.ContainerStateWaiting, Reason: "CrashLoopBackOff"},
			},
		})},
		Events: []domain.Event{event},
		Now:    overviewNow,
	})

	if _, ok := findingByTitle(overview.Findings, "BackOff"); ok {
		t.Error("the BackOff event duplicates the pod finding and should be suppressed")
	}
}

// Stale events are history, not the current state of the cluster.
func TestEventFindingsIgnoreOldEvents(t *testing.T) {
	t.Parallel()

	event, err := domain.NewEvent(domain.EventSpec{
		Name: "old.17c", Namespace: "default", ClusterID: "dev",
		Type: domain.EventWarning, Reason: "FailedMount", Message: "timed out waiting",
		InvolvedKind: "Pod", InvolvedName: "gone-1", Count: 1,
		FirstSeen: overviewNow.Add(-3 * time.Hour), LastSeen: overviewNow.Add(-2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("building event: %v", err)
	}

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Events:    []domain.Event{event},
		Now:       overviewNow,
	})

	if len(overview.Findings) != 0 {
		t.Errorf("findings = %v, want none for a two-hour-old event", titles(overview.Findings))
	}
}

// Critical findings must sort above warnings, and bigger problems above
// smaller ones of the same severity — the list is read top-down under
// pressure.
func TestFindingsRankBySeverityThenExtent(t *testing.T) {
	t.Parallel()

	pods := []domain.Pod{
		podFixture(t, domain.PodSpec{
			Name: "slow-1", NodeName: "node-1", Phase: domain.PodPhaseRunning,
			CreatedAt:  overviewNow.Add(-time.Hour),
			Containers: []domain.Container{{Name: "app", State: domain.ContainerStateRunning}},
		}),
		podFixture(t, domain.PodSpec{
			Name: "broken-1", NodeName: "node-1", Phase: domain.PodPhaseRunning,
			Containers: []domain.Container{
				{Name: "app", State: domain.ContainerStateWaiting, Reason: "CrashLoopBackOff"},
			},
		}),
	}

	overview := domain.NewOverview(domain.OverviewInput{ClusterID: "dev", Pods: pods, Now: overviewNow})

	if len(overview.Findings) < 2 {
		t.Fatalf("findings = %v, want at least two", titles(overview.Findings))
	}
	if overview.Findings[0].Severity != domain.SeverityCritical {
		t.Errorf("first finding = %q (%s), want the critical one first",
			overview.Findings[0].Title, overview.Findings[0].Severity)
	}
	if overview.Health != domain.HealthCritical {
		t.Errorf("health = %q, want critical", overview.Health)
	}
}

// Without metrics the assessment must still be produced — a cluster with no
// metrics-server is ordinary, not broken.
func TestOverviewDegradesWithoutMetrics(t *testing.T) {
	t.Parallel()

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID:   "dev",
		Nodes:       []domain.Node{nodeFixture(t, "node-1", 4000, 8<<30, 110)},
		Unavailable: []string{"metrics"},
		Now:         overviewNow,
	})

	if overview.Capacity.CPU.Measured {
		t.Error("CPU must report itself unmeasured")
	}
	if overview.Capacity.CPU.Allocatable != 4000 {
		t.Errorf("allocatable = %d, want the node capacity regardless of metrics", overview.Capacity.CPU.Allocatable)
	}
	if len(overview.Unavailable) != 1 || overview.Unavailable[0] != "metrics" {
		t.Errorf("unavailable = %v, want metrics named", overview.Unavailable)
	}
}

func TestRestartHotspotsRankByRestartCount(t *testing.T) {
	t.Parallel()

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Pods: []domain.Pod{
			podFixture(t, domain.PodSpec{
				Name: "quiet", NodeName: "node-1", Phase: domain.PodPhaseRunning,
				Containers: []domain.Container{{Name: "app", Ready: true, State: domain.ContainerStateRunning}},
			}),
			podFixture(t, domain.PodSpec{
				Name: "noisy", NodeName: "node-1", Phase: domain.PodPhaseRunning,
				Containers: []domain.Container{
					{Name: "app", Ready: true, State: domain.ContainerStateRunning, RestartCount: 47},
				},
			}),
		},
		Now: overviewNow,
	})

	if len(overview.Restarts) != 1 {
		t.Fatalf("hotspots = %d, want only the pod that has restarted", len(overview.Restarts))
	}
	if overview.Restarts[0].Name != "noisy" || overview.Restarts[0].Restarts != 47 {
		t.Errorf("hotspot = %+v, want noisy with 47 restarts", overview.Restarts[0])
	}
}

// A pod that is up now but has restarted forty times is invisible in every
// list K8Sense shows — this is the only place it surfaces.
func TestRestartFindingReportsHealthyButFlappingPods(t *testing.T) {
	t.Parallel()

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Pods: []domain.Pod{podFixture(t, domain.PodSpec{
			Name: "api-1", NodeName: "node-1", Phase: domain.PodPhaseRunning,
			Containers: []domain.Container{
				{Name: "app", Ready: true, State: domain.ContainerStateRunning, RestartCount: 41},
			},
		})},
		Now: overviewNow,
	})

	finding, ok := findingByTitle(overview.Findings, "Pods restarting repeatedly")
	if !ok {
		t.Fatalf("expected a restart finding; got %v", titles(overview.Findings))
	}
	if finding.Subjects[0].Detail != "41 restarts" {
		t.Errorf("detail = %q, want the restart count", finding.Subjects[0].Detail)
	}
}

// A crash-looping pod is already reported with its real reason; counting it
// again as "restarting" would name the same pod twice.
func TestRestartFindingSkipsAlreadyBrokenPods(t *testing.T) {
	t.Parallel()

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Pods: []domain.Pod{podFixture(t, domain.PodSpec{
			Name: "api-1", NodeName: "node-1", Phase: domain.PodPhaseRunning,
			Containers: []domain.Container{
				{Name: "app", State: domain.ContainerStateWaiting, Reason: "CrashLoopBackOff", RestartCount: 41},
			},
		})},
		Now: overviewNow,
	})

	if _, ok := findingByTitle(overview.Findings, "Pods restarting repeatedly"); ok {
		t.Error("the crash-looping pod is already reported by its reason")
	}
}

// A Job that is still running is in progress; one that gave up is a problem.
// Reporting both as "0 of 1 completions" hides the second behind the first.
func TestJobFindingsSeparateRunningFromFailed(t *testing.T) {
	t.Parallel()

	running, err := domain.NewWorkload(domain.WorkloadSpec{
		Kind: domain.WorkloadJob, Name: "nightly-export", Namespace: "batch",
		ClusterID: "dev", Desired: 1, Ready: 0, Available: 1,
	})
	if err != nil {
		t.Fatalf("building job: %v", err)
	}

	failed, err := domain.NewWorkload(domain.WorkloadSpec{
		Kind: domain.WorkloadJob, Name: "migrate-schema", Namespace: "batch",
		ClusterID: "dev", Desired: 1, Ready: 0, Available: 0, Failed: 6,
		CreatedAt: overviewNow.Add(-3 * time.Hour),
	})
	if err != nil {
		t.Fatalf("building job: %v", err)
	}

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Workloads: []domain.Workload{running, failed},
		Now:       overviewNow,
	})

	finding, ok := findingByTitle(overview.Findings, "Jobs failed")
	if !ok {
		t.Fatalf("expected a failed-job finding; got %v", titles(overview.Findings))
	}
	if finding.Count != 1 || finding.Subjects[0].Name != "migrate-schema" {
		t.Errorf("finding = %+v, want only the job that gave up", finding)
	}
	if finding.Subjects[0].Detail != "6 failed pods" {
		t.Errorf("detail = %q, want the failed pod count", finding.Subjects[0].Detail)
	}

	// And the running one must not be reported as short of replicas.
	if _, ok := findingByTitle(overview.Findings, "Job not at desired scale"); ok {
		t.Error("a Job in progress is not a controller short of replicas")
	}

	// The summary tells the same story.
	var jobs domain.WorkloadKindSummary
	for _, summary := range overview.Workloads {
		if summary.Kind == domain.WorkloadJob {
			jobs = summary
		}
	}
	if jobs.Rolling != 1 || jobs.Degraded != 1 {
		t.Errorf("job summary = %+v, want 1 in progress and 1 failed", jobs)
	}
}

// titles renders finding titles for failure messages.
func titles(findings []domain.Finding) []string {
	names := make([]string, 0, len(findings))
	for _, finding := range findings {
		names = append(names, finding.Title)
	}
	return names
}
