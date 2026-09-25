package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// sizing.go is the per-workload half of capacity.go's "capacity:waste"
// finding: same arithmetic, but attributed to whoever is actually holding
// the reservation instead of totalled across the cluster.

// replicaSetOwnedPod builds a pod owned by ReplicaSet "api-7d9f", with
// requests/usage/limits set on a single container, aged past sizingSettleTime
// unless overridden.
func replicaSetOwnedPod(t *testing.T, name string, requests, limits domain.Resources, usage domain.Metrics, createdAt time.Time) domain.Pod {
	t.Helper()

	return podFixture(t, domain.PodSpec{
		Name: name, Namespace: "default", NodeName: "node-1",
		Phase:     domain.PodPhaseRunning,
		CreatedAt: createdAt,
		Owners: []domain.OwnerReference{
			{Kind: "ReplicaSet", Name: "api-7d9f", Controller: true},
		},
		Containers: []domain.Container{{
			Name:     "app",
			State:    domain.ContainerStateRunning,
			Requests: requests,
			Limits:   limits,
		}},
	}).WithUsage(usage)
}

// apiWorkloads returns the ReplicaSet/Deployment pair that attributes
// "api-7d9f"-owned pods to Deployment "api".
func apiWorkloads(t *testing.T) []domain.Workload {
	t.Helper()

	deployment, err := domain.NewWorkload(domain.WorkloadSpec{
		Kind: domain.WorkloadDeployment, Name: "api", Namespace: "default",
		ClusterID: "dev", Desired: 3, Ready: 3, Current: 3, Updated: 3,
	})
	if err != nil {
		t.Fatalf("building deployment: %v", err)
	}

	replicaSet, err := domain.NewWorkload(domain.WorkloadSpec{
		Kind: domain.WorkloadReplicaSet, Name: "api-7d9f", Namespace: "default",
		ClusterID: "dev", Desired: 3, Ready: 3,
		Owner: domain.OwnerReference{Kind: "Deployment", Name: "api", Controller: true},
	})
	if err != nil {
		t.Fatalf("building replicaset: %v", err)
	}

	return []domain.Workload{deployment, replicaSet}
}

var settled = overviewNow.Add(-time.Hour)

func TestSizingCPURequestWaste(t *testing.T) {
	t.Parallel()

	wasteful := []domain.Pod{
		replicaSetOwnedPod(t, "api-7d9f-1", domain.Resources{CPUMilli: 700}, domain.Resources{}, domain.NewMetrics(35, 0), settled),
		replicaSetOwnedPod(t, "api-7d9f-2", domain.Resources{CPUMilli: 700}, domain.Resources{}, domain.NewMetrics(35, 0), settled),
		replicaSetOwnedPod(t, "api-7d9f-3", domain.Resources{CPUMilli: 600}, domain.Resources{}, domain.NewMetrics(30, 0), settled),
	}

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev", MetricsMeasured: true, Now: overviewNow,
		Pods: wasteful, Workloads: apiWorkloads(t),
	})

	finding, ok := findingByTitle(overview.Findings, "Workloads reserving far more CPU than they use")
	if !ok {
		t.Fatalf("expected a CPU sizing finding; got %v", titles(overview.Findings))
	}
	if finding.Count != 1 || len(finding.Subjects) != 1 {
		t.Fatalf("finding = %+v, want one workload named", finding)
	}
	subject := finding.Subjects[0]
	if subject.Kind != "Deployment" || subject.Name != "api" || subject.Namespace != "default" {
		t.Errorf("subject = %+v, want the Deployment it resolves to", subject)
	}
	if !strings.Contains(subject.Detail, "3 pods") {
		t.Errorf("detail = %q, want the pod count", subject.Detail)
	}
	if finding.KindID != "apps/v1/deployments" {
		t.Errorf("kindID = %q, want the deployments list", finding.KindID)
	}
	// One workload is "1 workload holds … and uses"; the count is printed once
	// and the verbs agree with it. "1 workloads hold" is the kind of sentence
	// that makes somebody stop trusting the numbers beside it.
	if !strings.HasPrefix(finding.Summary, "1 workload holds ") || !strings.Contains(finding.Summary, " and uses ") {
		t.Errorf("summary = %q, want singular grammar for one workload", finding.Summary)
	}
}

func TestSizingCPURequestWasteExcludesEfficientTinyAndYoung(t *testing.T) {
	t.Parallel()

	efficient, err := domain.NewWorkload(domain.WorkloadSpec{
		Kind: domain.WorkloadDeployment, Name: "trim", Namespace: "default",
		ClusterID: "dev", Desired: 1, Ready: 1,
	})
	if err != nil {
		t.Fatalf("building workload: %v", err)
	}
	trimReplicaSet, err := domain.NewWorkload(domain.WorkloadSpec{
		Kind: domain.WorkloadReplicaSet, Name: "trim-abc", Namespace: "default",
		ClusterID: "dev", Desired: 1, Ready: 1,
		Owner: domain.OwnerReference{Kind: "Deployment", Name: "trim", Controller: true},
	})
	if err != nil {
		t.Fatalf("building workload: %v", err)
	}

	efficientPod := podFixture(t, domain.PodSpec{
		Name: "trim-abc-1", Namespace: "default", NodeName: "node-1",
		Phase: domain.PodPhaseRunning, CreatedAt: settled,
		Owners: []domain.OwnerReference{{Kind: "ReplicaSet", Name: "trim-abc", Controller: true}},
		Containers: []domain.Container{{
			Name: "app", State: domain.ContainerStateRunning,
			Requests: domain.Resources{CPUMilli: 200},
		}},
	}).WithUsage(domain.NewMetrics(150, 0)) // 75% — not waste

	tinyPod := replicaSetOwnedPod(t, "api-7d9f-tiny", domain.Resources{CPUMilli: 100},
		domain.Resources{}, domain.NewMetrics(5, 0), settled) // 5% ratio, but under the floor

	youngPod := replicaSetOwnedPod(t, "api-7d9f-young", domain.Resources{CPUMilli: 2000},
		domain.Resources{}, domain.NewMetrics(50, 0), overviewNow.Add(-2*time.Minute)) // wasteful but not settled

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev", MetricsMeasured: true, Now: overviewNow,
		Pods:      []domain.Pod{efficientPod, tinyPod, youngPod},
		Workloads: append(apiWorkloads(t), efficient, trimReplicaSet),
	})

	if _, ok := findingByTitle(overview.Findings, "Workloads reserving far more CPU than they use"); ok {
		t.Errorf("no workload should have cleared both the ratio and the floor; got %v", titles(overview.Findings))
	}
}

func TestSizingCPURequestWasteOrdersByUnusedDescending(t *testing.T) {
	t.Parallel()

	small, err := domain.NewWorkload(domain.WorkloadSpec{
		Kind: domain.WorkloadDeployment, Name: "small", Namespace: "default",
		ClusterID: "dev", Desired: 1, Ready: 1,
	})
	if err != nil {
		t.Fatalf("building workload: %v", err)
	}
	smallReplicaSet, err := domain.NewWorkload(domain.WorkloadSpec{
		Kind: domain.WorkloadReplicaSet, Name: "small-abc", Namespace: "default",
		ClusterID: "dev", Desired: 1, Ready: 1,
		Owner: domain.OwnerReference{Kind: "Deployment", Name: "small", Controller: true},
	})
	if err != nil {
		t.Fatalf("building workload: %v", err)
	}

	// api: 2000m requested, 100m used — 1900m unused.
	big := replicaSetOwnedPod(t, "api-7d9f-1", domain.Resources{CPUMilli: 2000},
		domain.Resources{}, domain.NewMetrics(100, 0), settled)

	// small: 1000m requested, 50m used — 950m unused, still over the floor.
	littlePod := podFixture(t, domain.PodSpec{
		Name: "small-abc-1", Namespace: "default", NodeName: "node-1",
		Phase: domain.PodPhaseRunning, CreatedAt: settled,
		Owners: []domain.OwnerReference{{Kind: "ReplicaSet", Name: "small-abc", Controller: true}},
		Containers: []domain.Container{{
			Name: "app", State: domain.ContainerStateRunning,
			Requests: domain.Resources{CPUMilli: 1000},
		}},
	}).WithUsage(domain.NewMetrics(50, 0))

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev", MetricsMeasured: true, Now: overviewNow,
		Pods:      []domain.Pod{big, littlePod},
		Workloads: append(apiWorkloads(t), small, smallReplicaSet),
	})

	finding, ok := findingByTitle(overview.Findings, "Workloads reserving far more CPU than they use")
	if !ok {
		t.Fatalf("expected a CPU sizing finding; got %v", titles(overview.Findings))
	}
	if len(finding.Subjects) != 2 {
		t.Fatalf("finding = %+v, want both workloads named", finding)
	}
	if finding.Subjects[0].Name != "api" || finding.Subjects[1].Name != "small" {
		t.Errorf("order = %v, want the larger reclaim first", finding.Subjects)
	}
}

func TestSizingMemoryRequestWaste(t *testing.T) {
	t.Parallel()

	pods := []domain.Pod{
		replicaSetOwnedPod(t, "api-7d9f-1", domain.Resources{MemoryBytes: 1 << 30},
			domain.Resources{}, domain.NewMetrics(0, 64<<20), settled),
		replicaSetOwnedPod(t, "api-7d9f-2", domain.Resources{MemoryBytes: 1 << 30},
			domain.Resources{}, domain.NewMetrics(0, 64<<20), settled),
	}

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev", MetricsMeasured: true, Now: overviewNow,
		Pods: pods, Workloads: apiWorkloads(t),
	})

	finding, ok := findingByTitle(overview.Findings, "Workloads reserving far more memory than they use")
	if !ok {
		t.Fatalf("expected a memory sizing finding; got %v", titles(overview.Findings))
	}
	if finding.Count != 1 || finding.Subjects[0].Name != "api" {
		t.Fatalf("finding = %+v, want the Deployment named", finding)
	}
}

func TestSizingMemoryRequestWasteExcludedByFloor(t *testing.T) {
	t.Parallel()

	// Ratio is well under wasteRatio but the absolute reclaim (16Mi) is below
	// memoryWasteFloorBytes.
	pod := replicaSetOwnedPod(t, "api-7d9f-1", domain.Resources{MemoryBytes: 20 << 20},
		domain.Resources{}, domain.NewMetrics(0, 1<<20), settled)

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev", MetricsMeasured: true, Now: overviewNow,
		Pods: []domain.Pod{pod}, Workloads: apiWorkloads(t),
	})

	if _, ok := findingByTitle(overview.Findings, "Workloads reserving far more memory than they use"); ok {
		t.Error("a reclaim below the floor should not be reported however lopsided the ratio")
	}
}

func TestSizingCPULimitThrottled(t *testing.T) {
	t.Parallel()

	throttledContainer := func(name string, limitMilli, usageMilli int64) domain.Pod {
		return podFixture(t, domain.PodSpec{
			Name: name, Namespace: "default", NodeName: "node-1",
			Phase: domain.PodPhaseRunning, CreatedAt: settled,
			Containers: []domain.Container{{
				Name: "app", State: domain.ContainerStateRunning,
				Limits: domain.Resources{CPUMilli: limitMilli},
			}},
		}).WithUsage(domain.NewMetrics(usageMilli, 0))
	}

	partialLimitPod := podFixture(t, domain.PodSpec{
		Name: "partial", Namespace: "default", NodeName: "node-1",
		Phase: domain.PodPhaseRunning, CreatedAt: settled,
		Containers: []domain.Container{
			{Name: "app", State: domain.ContainerStateRunning, Limits: domain.Resources{CPUMilli: 100}},
			{Name: "sidecar", State: domain.ContainerStateRunning},
		},
	}).WithUsage(domain.NewMetrics(99, 0))

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev", MetricsMeasured: true, Now: overviewNow,
		Pods: []domain.Pod{
			throttledContainer("hot", 100, 95),     // 95% — reported
			throttledContainer("exactly", 100, 90), // 90% — the boundary is inclusive
			throttledContainer("cool", 100, 60),    // 60% — not news
			partialLimitPod,                        // one container has no limit
		},
	})

	finding, ok := findingByTitle(overview.Findings, "Pods throttled at their CPU limit")
	if !ok {
		t.Fatalf("expected a CPU limit finding; got %v", titles(overview.Findings))
	}
	if finding.Count != 2 {
		t.Errorf("count = %d, want the two at or above the line", finding.Count)
	}
	if !strings.Contains(finding.Summary, "95%") {
		t.Errorf("summary = %q, want the worst percentage named", finding.Summary)
	}

	names := make(map[string]bool, len(finding.Subjects))
	for _, subject := range finding.Subjects {
		names[subject.Name] = true
	}
	if names["cool"] {
		t.Error("a pod at 60%% of its CPU limit was reported as throttled")
	}
	if names["partial"] {
		t.Error("a pod with one container missing a CPU limit was reported")
	}
}

func TestSizingMemoryOverRequest(t *testing.T) {
	t.Parallel()

	overRequestPod := func(name string, requestBytes, usageBytes int64) domain.Pod {
		return podFixture(t, domain.PodSpec{
			Name: name, Namespace: "default", NodeName: "node-1",
			Phase: domain.PodPhaseRunning, CreatedAt: settled,
			Containers: []domain.Container{{
				Name: "app", State: domain.ContainerStateRunning,
				Requests: domain.Resources{MemoryBytes: requestBytes},
			}},
		}).WithUsage(domain.NewMetrics(0, usageBytes))
	}

	tests := []struct {
		name     string
		pod      domain.Pod
		reported bool
	}{
		{
			name:     "2x with 200Mi over",
			pod:      overRequestPod("double", 200<<20, 400<<20),
			reported: true,
		},
		{
			name:     "1.2x is not well over",
			pod:      overRequestPod("mild", 500<<20, 600<<20),
			reported: false,
		},
		{
			name:     "1.6x but only 10Mi over the floor",
			pod:      overRequestPod("tiny", 16<<20, (16<<20)*16/10),
			reported: false,
		},
		{
			name: "no request declared",
			pod: podFixture(t, domain.PodSpec{
				Name: "unrequested", Namespace: "default", NodeName: "node-1",
				Phase: domain.PodPhaseRunning, CreatedAt: settled,
				Containers: []domain.Container{{Name: "app", State: domain.ContainerStateRunning}},
			}).WithUsage(domain.NewMetrics(0, 500<<20)),
			reported: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			overview := domain.NewOverview(domain.OverviewInput{
				ClusterID: "dev", MetricsMeasured: true, Now: overviewNow,
				Pods: []domain.Pod{test.pod},
			})

			finding, ok := findingByTitle(overview.Findings, "Pods using more memory than they reserved")
			if ok != test.reported {
				t.Errorf("reported = %v, want %v (finding = %+v)", ok, test.reported, finding)
			}
		})
	}
}

func TestSizingMemoryOverRequestNamesTheWorst(t *testing.T) {
	t.Parallel()

	worse := podFixture(t, domain.PodSpec{
		Name: "worst", Namespace: "default", NodeName: "node-1",
		Phase: domain.PodPhaseRunning, CreatedAt: settled,
		Containers: []domain.Container{{
			Name: "app", State: domain.ContainerStateRunning,
			Requests: domain.Resources{MemoryBytes: 256 << 20},
		}},
	}).WithUsage(domain.NewMetrics(0, 612<<20))

	milder := podFixture(t, domain.PodSpec{
		Name: "milder", Namespace: "default", NodeName: "node-1",
		Phase: domain.PodPhaseRunning, CreatedAt: settled,
		Containers: []domain.Container{{
			Name: "app", State: domain.ContainerStateRunning,
			Requests: domain.Resources{MemoryBytes: 200 << 20},
		}},
	}).WithUsage(domain.NewMetrics(0, 350<<20))

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev", MetricsMeasured: true, Now: overviewNow,
		Pods: []domain.Pod{worse, milder},
	})

	finding, ok := findingByTitle(overview.Findings, "Pods using more memory than they reserved")
	if !ok {
		t.Fatalf("expected a memory-over-request finding; got %v", titles(overview.Findings))
	}
	if finding.Count != 2 {
		t.Errorf("count = %d, want both pods", finding.Count)
	}
	if !strings.Contains(finding.Summary, "2.4×") {
		t.Errorf("summary = %q, want the worst ratio named", finding.Summary)
	}

	for _, subject := range finding.Subjects {
		if subject.Name == "worst" && subject.Detail != "uses 612.0MiB against a 256.0MiB request" {
			t.Errorf("detail = %q, want the usage and request named", subject.Detail)
		}
	}
}

func TestSizingFindingsStayQuietWithoutGrounds(t *testing.T) {
	t.Parallel()

	unmeasuredMetrics := domain.OverviewInput{
		ClusterID: "dev", MetricsMeasured: false, Now: overviewNow,
		Pods: []domain.Pod{
			replicaSetOwnedPod(t, "api-7d9f-1", domain.Resources{CPUMilli: 2000},
				domain.Resources{}, domain.NewMetrics(0, 0), settled),
		},
	}

	unmeasuredPod := domain.OverviewInput{
		ClusterID: "dev", MetricsMeasured: true, Now: overviewNow,
		Pods: []domain.Pod{
			podFixture(t, domain.PodSpec{
				Name: "unmeasured", Namespace: "default", NodeName: "node-1",
				Phase: domain.PodPhaseRunning, CreatedAt: settled,
				Containers: []domain.Container{{
					Name: "app", State: domain.ContainerStateRunning,
					Requests: domain.Resources{CPUMilli: 2000, MemoryBytes: 1 << 30},
					Limits:   domain.Resources{CPUMilli: 2000},
				}},
			}),
		},
	}

	succeededPod := podFixture(t, domain.PodSpec{
		Name: "finished", Namespace: "default", NodeName: "node-1",
		Phase: domain.PodPhaseSucceeded, CreatedAt: settled,
		Containers: []domain.Container{{
			Name: "app", State: domain.ContainerStateTerminated,
			Requests: domain.Resources{CPUMilli: 2000, MemoryBytes: 1 << 30},
			Limits:   domain.Resources{CPUMilli: 2000},
		}},
	}).WithUsage(domain.NewMetrics(10, 1<<20))
	succeeded := domain.OverviewInput{
		ClusterID: "dev", MetricsMeasured: true, Now: overviewNow,
		Pods: []domain.Pod{succeededPod},
	}

	titlesToCheck := []string{
		"Workloads reserving far more CPU than they use",
		"Workloads reserving far more memory than they use",
		"Pods throttled at their CPU limit",
		"Pods using more memory than they reserved",
	}

	for name, input := range map[string]domain.OverviewInput{
		"MetricsMeasured false": unmeasuredMetrics,
		"pod usage unmeasured":  unmeasuredPod,
		"succeeded pod":         succeeded,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			overview := domain.NewOverview(input)
			for _, title := range titlesToCheck {
				if _, ok := findingByTitle(overview.Findings, title); ok {
					t.Errorf("%s: %q reported with no grounds; findings = %v", name, title, titles(overview.Findings))
				}
			}
		})
	}
}

func TestSizingAttributesBarePodToItself(t *testing.T) {
	t.Parallel()

	bare := podFixture(t, domain.PodSpec{
		Name: "standalone", Namespace: "default", NodeName: "node-1",
		Phase: domain.PodPhaseRunning, CreatedAt: settled,
		Containers: []domain.Container{{
			Name: "app", State: domain.ContainerStateRunning,
			Requests: domain.Resources{CPUMilli: 2000},
		}},
	}).WithUsage(domain.NewMetrics(50, 0))

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev", MetricsMeasured: true, Now: overviewNow,
		Pods: []domain.Pod{bare},
	})

	finding, ok := findingByTitle(overview.Findings, "Workloads reserving far more CPU than they use")
	if !ok {
		t.Fatalf("expected a CPU sizing finding; got %v", titles(overview.Findings))
	}
	if len(finding.Subjects) != 1 || finding.Subjects[0].Kind != "Pod" || finding.Subjects[0].Name != "standalone" {
		t.Errorf("subject = %+v, want the bare pod named as its own subject", finding.Subjects)
	}
	if finding.KindID != "core/v1/pods" {
		t.Errorf("kindID = %q, want the pods list", finding.KindID)
	}
}

// A correctly sized workload produces none of the four findings — the same
// discipline as pod_assessment.go's "a correctly configured pod produces no
// findings".
func TestSizingCorrectlySizedWorkloadProducesNoFindings(t *testing.T) {
	t.Parallel()

	pods := []domain.Pod{
		replicaSetOwnedPod(t, "api-7d9f-1",
			domain.Resources{CPUMilli: 500, MemoryBytes: 512 << 20},
			domain.Resources{CPUMilli: 1000, MemoryBytes: 1 << 30},
			domain.NewMetrics(300, 300<<20), // 60% of both requests, well under both limits
			settled),
		replicaSetOwnedPod(t, "api-7d9f-2",
			domain.Resources{CPUMilli: 500, MemoryBytes: 512 << 20},
			domain.Resources{CPUMilli: 1000, MemoryBytes: 1 << 30},
			domain.NewMetrics(300, 300<<20),
			settled),
	}

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev", MetricsMeasured: true, Now: overviewNow,
		Pods: pods, Workloads: apiWorkloads(t),
	})

	titlesToCheck := []string{
		"Workloads reserving far more CPU than they use",
		"Workloads reserving far more memory than they use",
		"Pods throttled at their CPU limit",
		"Pods using more memory than they reserved",
	}
	for _, title := range titlesToCheck {
		if _, ok := findingByTitle(overview.Findings, title); ok {
			t.Errorf("a correctly sized workload should produce no sizing findings; got %q", title)
		}
	}
}
