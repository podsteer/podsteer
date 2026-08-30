package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// The overview is the one place in PodSteer that makes a judgement rather than
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

// Ephemeral storage is capacity like any other, and the one an operator has
// no reservation protecting: hardly anybody declares it.
func TestCapacityCountsNodeEphemeralStorage(t *testing.T) {
	t.Parallel()

	node, err := domain.NewNode(domain.NodeSpec{
		Name: "node-1", ClusterID: "dev", Ready: true, KubeletVersion: "v1.32.7",
		Capacity: domain.Capacity{
			CPUMilli: 4000, MemoryBytes: 8 << 30, Pods: 110, EphemeralBytes: 200 << 30,
		},
		// Allocatable is lower than capacity, as it always is in reality: the
		// kubelet reserves for itself and holds back the eviction threshold.
		Allocatable: domain.Capacity{
			CPUMilli: 4000, MemoryBytes: 8 << 30, Pods: 110, EphemeralBytes: 180 << 30,
		},
		CreatedAt: overviewNow.Add(-24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("building node: %v", err)
	}

	pod := podFixture(t, domain.PodSpec{
		Name: "writer-1", NodeName: "node-1", Phase: domain.PodPhaseRunning,
		Containers: []domain.Container{{
			Name:     "app",
			State:    domain.ContainerStateRunning,
			Requests: domain.Resources{CPUMilli: 100, EphemeralBytes: 2 << 30},
			Limits:   domain.Resources{EphemeralBytes: 4 << 30},
		}},
	})

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Nodes:     []domain.Node{node},
		Pods:      []domain.Pod{pod},
		Now:       overviewNow,
	})

	ephemeral := overview.Capacity.Ephemeral
	if ephemeral.Capacity != 200<<30 {
		t.Errorf("capacity = %d, want the node's whole scratch disk", ephemeral.Capacity)
	}
	if ephemeral.Allocatable != 180<<30 {
		t.Errorf("allocatable = %d, want what the scheduler may hand out", ephemeral.Allocatable)
	}
	if ephemeral.Requests != 2<<30 {
		t.Errorf("requests = %d, want the pod's declaration", ephemeral.Requests)
	}
	if ephemeral.Limits != 4<<30 {
		t.Errorf("limits = %d, want the pod's ceiling", ephemeral.Limits)
	}
}

// "3 nodes under pressure" is not actionable: disk, memory and PIDs are three
// different jobs, fixed in three different places.
func TestPressureIsReportedPerCondition(t *testing.T) {
	t.Parallel()

	build := func(name string, conditions ...domain.NodeCondition) domain.Node {
		t.Helper()
		node, err := domain.NewNode(domain.NodeSpec{
			Name: name, ClusterID: "dev", Ready: true, KubeletVersion: "v1.32.7",
			Capacity:         domain.Capacity{CPUMilli: 4000, MemoryBytes: 8 << 30, Pods: 110},
			Allocatable:      domain.Capacity{CPUMilli: 4000, MemoryBytes: 8 << 30, Pods: 110},
			ActiveConditions: conditions,
			CreatedAt:        overviewNow.Add(-24 * time.Hour),
		})
		if err != nil {
			t.Fatalf("building node %q: %v", name, err)
		}
		return node
	}

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Nodes: []domain.Node{
			build("node-1", domain.NodeDiskPressure),
			build("node-2", domain.NodeDiskPressure, domain.NodeMemoryPressure),
			build("node-3"),
		},
		Now: overviewNow,
	})

	disk, ok := findingByTitle(overview.Findings, "Nodes out of disk")
	if !ok {
		t.Fatalf("findings = %v, want one naming disk", titles(overview.Findings))
	}
	if disk.Count != 2 {
		t.Errorf("disk pressure count = %d, want both nodes", disk.Count)
	}
	memory, ok := findingByTitle(overview.Findings, "Nodes out of memory")
	if !ok {
		t.Fatalf("findings = %v, want one naming memory", titles(overview.Findings))
	}
	if memory.Count != 1 {
		t.Errorf("memory pressure count = %d, want the one node", memory.Count)
	}

	// The node counted once overall, and once per condition it raises.
	if overview.Nodes.UnderPressure != 2 {
		t.Errorf("underPressure = %d, want the two affected nodes", overview.Nodes.UnderPressure)
	}
	if got := overview.Nodes.Pressure[domain.NodeDiskPressure]; got != 2 {
		t.Errorf("disk pressure nodes = %d, want 2", got)
	}
}

// The measurement no other part of Kubernetes carries: how full a node's disk
// is BEFORE the kubelet says DiskPressure and starts evicting.
func TestFilesystemFindingsWarnBeforeTheKubeletReacts(t *testing.T) {
	t.Parallel()

	withDisk := func(name string, usedPercent float64) domain.Node {
		t.Helper()
		node, err := domain.NewNode(domain.NodeSpec{
			Name: name, ClusterID: "dev", Ready: true, KubeletVersion: "v1.32.7",
			Capacity:    domain.Capacity{CPUMilli: 4000, MemoryBytes: 8 << 30, Pods: 110},
			Allocatable: domain.Capacity{CPUMilli: 4000, MemoryBytes: 8 << 30, Pods: 110},
			CreatedAt:   overviewNow.Add(-24 * time.Hour),
		})
		if err != nil {
			t.Fatalf("building node %q: %v", name, err)
		}
		const size = 100 << 30
		return node.WithFilesystems(domain.NodeFilesystems{
			Nodefs:   domain.Filesystem{CapacityBytes: size, UsedBytes: int64(size * usedPercent / 100)},
			Measured: true,
		})
	}

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Nodes: []domain.Node{
			withDisk("quiet", 40),
			withDisk("filling", 84),
			withDisk("nearly-full", 93),
		},
		Now: overviewNow,
	})

	warning, ok := findingByTitle(overview.Findings, "Node disks filling")
	if !ok {
		t.Fatalf("findings = %v, want one for the filling disk", titles(overview.Findings))
	}
	if warning.Count != 1 || warning.Subjects[0].Name != "filling" {
		t.Errorf("warning names %+v, want only the node at 84%%", warning.Subjects)
	}

	critical, ok := findingByTitle(overview.Findings, "Node disks nearly full")
	if !ok {
		t.Fatalf("findings = %v, want one for the nearly full disk", titles(overview.Findings))
	}
	if critical.Severity != domain.SeverityCritical {
		t.Errorf("severity = %q, want critical", critical.Severity)
	}
	if critical.Count != 1 || critical.Subjects[0].Name != "nearly-full" {
		t.Errorf("critical names %+v, want only the node at 93%%", critical.Subjects)
	}

	// Occupancy also fills in the capacity dimension the API server cannot.
	if !overview.Capacity.Ephemeral.Measured {
		t.Error("ephemeral usage is unmeasured although kubelets reported it")
	}
}

// A node nobody could measure must not be reported as an empty disk.
func TestFilesystemFindingsIgnoreUnmeasuredNodes(t *testing.T) {
	t.Parallel()

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Nodes:     []domain.Node{nodeFixture(t, "node-1", 4000, 8<<30, 110)},
		Now:       overviewNow,
	})

	for _, title := range []string{"Node disks filling", "Node disks nearly full"} {
		if _, ok := findingByTitle(overview.Findings, title); ok {
			t.Errorf("finding %q was raised for a node no kubelet answered for", title)
		}
	}
	if overview.Capacity.Ephemeral.Measured {
		t.Error("ephemeral usage claims to be measured with no kubelet data")
	}
}

// The kubelet evicts on whichever filesystem crosses first, so the fuller one
// is the one worth reporting.
func TestFilesystemFindingsUseTheFullerOfTheTwoDisks(t *testing.T) {
	t.Parallel()

	node := nodeFixture(t, "split", 4000, 8<<30, 110).
		WithFilesystems(domain.NodeFilesystems{
			Nodefs:   domain.Filesystem{CapacityBytes: 100 << 30, UsedBytes: 20 << 30},
			Imagefs:  domain.Filesystem{CapacityBytes: 100 << 30, UsedBytes: 95 << 30},
			Measured: true,
		})

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Nodes:     []domain.Node{node},
		Now:       overviewNow,
	})

	finding, ok := findingByTitle(overview.Findings, "Node disks nearly full")
	if !ok {
		t.Fatalf("findings = %v, want the image filesystem reported", titles(overview.Findings))
	}
	if !strings.Contains(finding.Subjects[0].Detail, "95%") {
		t.Errorf("detail = %q, want the fuller filesystem's figure", finding.Subjects[0].Detail)
	}
}

// claimFixture builds a claim in the given phase, created `ago` in the past.
func claimFixture(t *testing.T, name string, phase domain.ClaimPhase, ago time.Duration, bytes int64) domain.PersistentVolumeClaim {
	t.Helper()

	claim, err := domain.NewPersistentVolumeClaim(domain.PersistentVolumeClaimSpec{
		Name: name, Namespace: "default", ClusterID: "dev", Phase: phase,
		StorageClass: "gp3", RequestedBytes: bytes,
		CreatedAt: overviewNow.Add(-ago),
	})
	if err != nil {
		t.Fatalf("building claim %q: %v", name, err)
	}
	return claim
}

// volumeFixture builds a volume in the given phase.
func volumeFixture(t *testing.T, name string, phase domain.VolumePhase, policy string, bytes int64) domain.PersistentVolume {
	t.Helper()

	volume, err := domain.NewPersistentVolume(domain.PersistentVolumeSpec{
		Name: name, ClusterID: "dev", Phase: phase, StorageClass: "gp3",
		CapacityBytes: bytes, ReclaimPolicy: policy,
		CreatedAt: overviewNow.Add(-90 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("building volume %q: %v", name, err)
	}
	return volume
}

// Binding takes time, so a claim provisioning right now is not a fault. This
// is the difference between a useful finding and one people learn to ignore.
func TestPendingClaimsAreOnlyReportedOnceTheyAreStuck(t *testing.T) {
	t.Parallel()

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Claims: []domain.PersistentVolumeClaim{
			claimFixture(t, "provisioning", domain.ClaimPending, 20*time.Second, 8<<30),
			claimFixture(t, "stuck", domain.ClaimPending, time.Hour, 8<<30),
			claimFixture(t, "working", domain.ClaimBound, 24*time.Hour, 8<<30),
		},
		Now: overviewNow,
	})

	finding, ok := findingByTitle(overview.Findings, "Claims not bound")
	if !ok {
		t.Fatalf("findings = %v, want one for the stuck claim", titles(overview.Findings))
	}
	if finding.Count != 1 || finding.Subjects[0].Name != "stuck" {
		t.Errorf("subjects = %+v, want only the claim that has waited an hour", finding.Subjects)
	}
	if !strings.Contains(finding.Subjects[0].Detail, "gp3") {
		t.Errorf("detail = %q, want the storage class it is waiting on", finding.Subjects[0].Detail)
	}
}

// A Lost claim is data the workload will not get back by restarting.
func TestLostClaimsAreCritical(t *testing.T) {
	t.Parallel()

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Claims:    []domain.PersistentVolumeClaim{claimFixture(t, "gone", domain.ClaimLost, time.Hour, 8<<30)},
		Now:       overviewNow,
	})

	finding, ok := findingByTitle(overview.Findings, "Claims whose volume is gone")
	if !ok {
		t.Fatalf("findings = %v, want one for the lost claim", titles(overview.Findings))
	}
	if finding.Severity != domain.SeverityCritical {
		t.Errorf("severity = %q, want critical", finding.Severity)
	}
}

// Released volumes that will never be reclaimed are storage still being paid
// for, and nothing else in a Kubernetes client points at them.
func TestReleasedVolumesAreReportedOnlyWhenNothingWillReclaimThem(t *testing.T) {
	t.Parallel()

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Volumes: []domain.PersistentVolume{
			volumeFixture(t, "kept", domain.VolumeReleased, "Retain", 100<<30),
			// Delete means the provisioner is already removing it: reporting
			// that would be reporting normal cleanup as a problem.
			volumeFixture(t, "going", domain.VolumeReleased, "Delete", 50<<30),
			volumeFixture(t, "in-use", domain.VolumeBound, "Delete", 20<<30),
		},
		Now: overviewNow,
	})

	finding, ok := findingByTitle(overview.Findings, "Storage nothing is using")
	if !ok {
		t.Fatalf("findings = %v, want one for the retained volume", titles(overview.Findings))
	}
	if finding.Count != 1 || finding.Subjects[0].Name != "kept" {
		t.Errorf("subjects = %+v, want only the Retain volume", finding.Subjects)
	}
	// Information, not a fault: keeping the data may well be deliberate.
	if finding.Severity != domain.SeverityInfo {
		t.Errorf("severity = %q, want info", finding.Severity)
	}
	if overview.Storage.OrphanedBytes != 100<<30 {
		t.Errorf("orphaned = %d, want only the retained volume's size", overview.Storage.OrphanedBytes)
	}
}

// The summary is what the card reads, and only bound volumes are storage the
// cluster is actually providing.
func TestStorageSummaryCountsProvisionedAndPending(t *testing.T) {
	t.Parallel()

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Volumes: []domain.PersistentVolume{
			volumeFixture(t, "one", domain.VolumeBound, "Delete", 100<<30),
			volumeFixture(t, "two", domain.VolumeBound, "Delete", 20<<30),
			volumeFixture(t, "spare", domain.VolumeAvailable, "Delete", 500<<30),
		},
		Claims: []domain.PersistentVolumeClaim{
			claimFixture(t, "bound", domain.ClaimBound, time.Hour, 100<<30),
			claimFixture(t, "waiting", domain.ClaimPending, time.Hour, 8<<30),
		},
		Now: overviewNow,
	})

	storage := overview.Storage
	if storage.ProvisionedBytes != 120<<30 {
		t.Errorf("provisioned = %d, want only the bound volumes", storage.ProvisionedBytes)
	}
	if storage.UnboundBytes != 8<<30 {
		t.Errorf("unbound = %d, want what the pending claim asked for", storage.UnboundBytes)
	}
	if storage.Claims[domain.ClaimBound] != 1 || storage.Claims[domain.ClaimPending] != 1 {
		t.Errorf("claims = %v, want one of each", storage.Claims)
	}
	if len(storage.Classes) != 1 || storage.Classes[0].Volumes != 2 {
		t.Errorf("classes = %+v, want gp3 with the two bound volumes", storage.Classes)
	}
}

// Two rankings, because the pod holding the most CPU and the pod holding the
// most memory are usually different pods.
func TestTopConsumersRankEachDimensionSeparately(t *testing.T) {
	t.Parallel()

	usage := func(name string, cpuMilli, memoryBytes int64) domain.Pod {
		t.Helper()
		pod := podFixture(t, domain.PodSpec{
			Name: name, NodeName: "node-1", Phase: domain.PodPhaseRunning,
			Containers: []domain.Container{{
				Name: "app", State: domain.ContainerStateRunning,
				Requests: domain.Resources{CPUMilli: 100, MemoryBytes: 1 << 30},
			}},
		})
		return pod.WithUsage(domain.NewMetrics(cpuMilli, memoryBytes))
	}

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Pods: []domain.Pod{
			usage("cpu-hog", 4000, 1<<30),
			usage("memory-hog", 100, 40<<30),
			usage("quiet", 10, 1<<28),
		},
		MetricsMeasured: true,
		Now:             overviewNow,
	})

	consumers := overview.Consumers
	if !consumers.Measured {
		t.Fatal("consumers report themselves unmeasured although metrics answered")
	}
	if consumers.ByCPU[0].Name != "cpu-hog" {
		t.Errorf("top by CPU = %q, want cpu-hog", consumers.ByCPU[0].Name)
	}
	if consumers.ByMemory[0].Name != "memory-hog" {
		t.Errorf("top by memory = %q, want memory-hog", consumers.ByMemory[0].Name)
	}

	// Usage against the reservation is the half that says whether a pod is
	// merely busy or sized wrong: 4000m used against 100m requested is 4000%.
	if got := consumers.ByCPU[0].CPUOfRequest(); got != 4000 {
		t.Errorf("CPU of request = %v, want 4000", got)
	}
}

// Without metrics the lists must be empty AND say so: an empty list that looks
// measured reads as "nothing is using anything".
func TestTopConsumersAreEmptyWithoutMetrics(t *testing.T) {
	t.Parallel()

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Pods: []domain.Pod{podFixture(t, domain.PodSpec{
			Name: "app-1", NodeName: "node-1", Phase: domain.PodPhaseRunning,
			Containers: []domain.Container{{Name: "app", State: domain.ContainerStateRunning}},
		})},
		Now: overviewNow,
	})

	if overview.Consumers.Measured {
		t.Error("consumers claim to be measured with no metrics")
	}
	if len(overview.Consumers.ByCPU) != 0 || len(overview.Consumers.ByMemory) != 0 {
		t.Errorf("consumers = %+v, want nothing", overview.Consumers)
	}
}

// A pod with no reservation is the interesting case, not a gap to hide.
func TestConsumerWithoutRequestsReportsNoShare(t *testing.T) {
	t.Parallel()

	consumer := domain.Consumer{CPUMilli: 500, MemoryBytes: 1 << 30}
	if got := consumer.CPUOfRequest(); got != -1 {
		t.Errorf("CPU of request = %v, want -1 for a pod that reserved nothing", got)
	}
	if got := consumer.MemoryOfRequest(); got != -1 {
		t.Errorf("memory of request = %v, want -1", got)
	}
}

// A cluster total cannot distinguish an evenly loaded cluster from one where
// half the nodes are full — which is the case that explains a pod refusing to
// schedule on a cluster reading 50% requested.
func TestNodeLoadsRankTheBusiestFirst(t *testing.T) {
	t.Parallel()

	pod := func(name, node string, cpuMilli int64) domain.Pod {
		t.Helper()
		return podFixture(t, domain.PodSpec{
			Name: name, NodeName: node, Phase: domain.PodPhaseRunning,
			Containers: []domain.Container{{
				Name: "app", State: domain.ContainerStateRunning,
				Requests: domain.Resources{CPUMilli: cpuMilli},
			}},
		})
	}

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Nodes: []domain.Node{
			nodeFixture(t, "aaa-idle", 4000, 8<<30, 110),
			nodeFixture(t, "zzz-busy", 4000, 8<<30, 110),
		},
		Pods: []domain.Pod{
			pod("small", "aaa-idle", 200),
			pod("large", "zzz-busy", 3600),
		},
		Now: overviewNow,
	})

	loads := overview.NodeLoads
	if len(loads) != 2 {
		t.Fatalf("loads = %d, want one per node", len(loads))
	}
	// Busiest first, not alphabetical: sorting by name would bury the node
	// about to refuse work wherever the alphabet put it.
	if loads[0].Name != "zzz-busy" {
		t.Errorf("first = %q, want the busiest node", loads[0].Name)
	}
	if got := loads[0].CPUPercent; got < 89 || got > 91 {
		t.Errorf("busiest CPU = %v%%, want about 90", got)
	}
	if loads[0].Pods != 1 {
		t.Errorf("pods = %d, want the one scheduled on it", loads[0].Pods)
	}

	// Unmeasured disk is -1, never 0: a row of zeroes would read as empty
	// disks rather than as nobody having been asked.
	if loads[0].DiskPercent != -1 {
		t.Errorf("disk = %v, want -1 for a node no kubelet answered for", loads[0].DiskPercent)
	}
}

// A tainted node advertises its slots like any other and will never accept a
// pod that does not tolerate it. Counting them as headroom is what makes a
// cluster look like it has room no ordinary workload can reach.
func TestPodCapacityExcludesSlotsOnTaintedNodes(t *testing.T) {
	t.Parallel()

	node := func(name string, blocking int) domain.Node {
		t.Helper()
		built, err := domain.NewNode(domain.NodeSpec{
			Name: name, ClusterID: "dev", Ready: true, KubeletVersion: "v1.32.7",
			Capacity:       domain.Capacity{CPUMilli: 4000, MemoryBytes: 8 << 30, Pods: 110},
			Allocatable:    domain.Capacity{CPUMilli: 4000, MemoryBytes: 8 << 30, Pods: 110},
			Taints:         blocking,
			BlockingTaints: blocking,
			CreatedAt:      overviewNow.Add(-24 * time.Hour),
		})
		if err != nil {
			t.Fatalf("building node %q: %v", name, err)
		}
		return built
	}

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Nodes: []domain.Node{
			node("worker-1", 0),
			node("worker-2", 0),
			node("control-plane-1", 1),
		},
		Now: overviewNow,
	})

	pods := overview.Capacity.Pods
	if pods.Capacity != 220 {
		t.Errorf("capacity = %d, want the two untainted nodes' slots", pods.Capacity)
	}
	if pods.Reserved != 110 {
		t.Errorf("reserved = %d, want the tainted node's slots", pods.Reserved)
	}
	if pods.ReservedNodes != 1 {
		t.Errorf("reserved nodes = %d, want 1", pods.ReservedNodes)
	}
}

// PreferNoSchedule refuses nothing — the scheduler ignores it when nowhere
// else will do — so a node carrying only that is not reserved.
func TestPodCapacityCountsNodesWithSoftTaints(t *testing.T) {
	t.Parallel()

	soft, err := domain.NewNode(domain.NodeSpec{
		Name: "soft", ClusterID: "dev", Ready: true, KubeletVersion: "v1.32.7",
		Capacity:    domain.Capacity{CPUMilli: 4000, MemoryBytes: 8 << 30, Pods: 110},
		Allocatable: domain.Capacity{CPUMilli: 4000, MemoryBytes: 8 << 30, Pods: 110},
		// One taint, none of them blocking.
		Taints:    1,
		CreatedAt: overviewNow.Add(-24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("building node: %v", err)
	}

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev", Nodes: []domain.Node{soft}, Now: overviewNow,
	})

	if got := overview.Capacity.Pods.Capacity; got != 110 {
		t.Errorf("capacity = %d, want the node counted", got)
	}
	if got := overview.Capacity.Pods.Reserved; got != 0 {
		t.Errorf("reserved = %d, want none", got)
	}
}

// Scheduled says a slot is taken and nothing about the workload in it.
func TestPodCapacityCountsHealthySeparatelyFromScheduled(t *testing.T) {
	t.Parallel()

	working := podFixture(t, domain.PodSpec{
		Name: "working", NodeName: "node-1", Phase: domain.PodPhaseRunning,
		Containers: []domain.Container{{Name: "app", State: domain.ContainerStateRunning, Ready: true}},
	})
	broken := podFixture(t, domain.PodSpec{
		Name: "broken", NodeName: "node-1", Phase: domain.PodPhaseRunning,
		Containers: []domain.Container{
			{Name: "app", State: domain.ContainerStateWaiting, Reason: "CrashLoopBackOff"},
		},
	})

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Nodes:     []domain.Node{nodeFixture(t, "node-1", 4000, 8<<30, 110)},
		Pods:      []domain.Pod{working, broken},
		Now:       overviewNow,
	})

	pods := overview.Capacity.Pods
	if pods.Scheduled != 2 {
		t.Errorf("scheduled = %d, want both pods occupying slots", pods.Scheduled)
	}
	if pods.Healthy != 1 {
		t.Errorf("healthy = %d, want only the working pod", pods.Healthy)
	}
	if got := pods.HealthyPercent(); got != 50 {
		t.Errorf("healthy percent = %v, want half of what is scheduled", got)
	}
}

// Free slots must never go negative, and the shares must agree with them.
func TestPodCapacityFreeIsFlooredAtZero(t *testing.T) {
	t.Parallel()

	full := domain.PodCapacity{Scheduled: 120, Capacity: 110}
	if got := full.Free(); got != 0 {
		t.Errorf("free = %d, want none on a cluster past its own cap", got)
	}
	if got := full.FreePercent(); got != 0 {
		t.Errorf("free percent = %v, want 0", got)
	}

	waiting := domain.PodCapacity{Scheduled: 90, Capacity: 110, Unschedulable: 10}
	if got := waiting.WaitingPercent(); got != 10 {
		t.Errorf("waiting percent = %v, want a tenth of everything wanting to run", got)
	}
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
		Allocatable: 4000, Requests: 2000, Usage: 900, PodUsage: 500,
		Measured: true, PodMeasured: true,
	}
	if got := usage.Efficiency(); got != 25 {
		t.Errorf("efficiency = %.0f%%, want 25%% of what the pods reserved", got)
	}

	unmeasured := domain.ResourceUsage{Allocatable: 4000, Requests: 2000}
	if got := unmeasured.Efficiency(); got != -1 {
		t.Errorf("efficiency = %.0f, want -1 when nothing was measured", got)
	}

	// Measured at the node and nowhere else, which is exactly ephemeral
	// storage: PodUsage is zero because nothing reports it, not because the
	// pods are using nothing. Dividing by it announced a cluster wasting
	// 100% of its disk reservation.
	nodeOnly := domain.ResourceUsage{
		Allocatable: 4000, Requests: 2000, Usage: 900, Measured: true,
	}
	if got := nodeOnly.Efficiency(); got != -1 {
		t.Errorf("efficiency = %.0f, want -1 when only the nodes were measured", got)
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

// Two identical events on the same object are one line, not two.
//
// This is what a repeated warning actually looks like — the same message from
// the same pod, minutes apart — and listing it twice told the operator nothing
// the count above it had not. It also has to be true for the list to render at
// all: the frontend keys the rows by the object they name.
func TestEventFindingsListRepeatsOfOneMessageOnce(t *testing.T) {
	t.Parallel()

	newEvent := func(name, message string, ago time.Duration) domain.Event {
		t.Helper()
		event, err := domain.NewEvent(domain.EventSpec{
			Name: name, Namespace: "default", ClusterID: "dev",
			Type: domain.EventWarning, Reason: "FailedDeclare", Message: message,
			InvolvedKind: "Pod", InvolvedName: "queue-1", Count: 1,
			FirstSeen: overviewNow.Add(-ago), LastSeen: overviewNow.Add(-ago),
		})
		if err != nil {
			t.Fatalf("building event: %v", err)
		}
		return event
	}

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID: "dev",
		Events: []domain.Event{
			newEvent("q.1", "failed to declare queue", 8*time.Minute),
			newEvent("q.2", "failed to declare queue", 2*time.Minute),
			newEvent("q.3", "failed to declare exchange", time.Minute),
		},
		Now: overviewNow,
	})

	finding, ok := findingByTitle(overview.Findings, "FailedDeclare")
	if !ok {
		t.Fatalf("findings = %v, want FailedDeclare", titles(overview.Findings))
	}
	// Three events, of which two say the same thing: the extent is still 3.
	if finding.Count != 3 {
		t.Errorf("count = %d, want 3 — the count states how often it happened", finding.Count)
	}
	if len(finding.Subjects) != 2 {
		t.Fatalf("subjects = %+v, want the two distinct messages", finding.Subjects)
	}
	// Nothing was withheld, so nothing must claim to have been: an
	// "and 1 more" here expands to an empty list.
	if finding.Truncated() {
		t.Error("Truncated() is true although only duplicates were collapsed")
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
// list PodSteer shows — this is the only place it surfaces.
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

// A cluster nobody could read is not a healthy cluster.
//
// This is the bug that motivated HealthUnknown: with the VPN disconnected every
// source failed, so there were no findings, and an empty findings slice graded
// Healthy. The dashboard showed a green tick reading "No problems found" over
// 0 nodes, 0 pods and every figure zero — telling the operator the opposite of
// the truth at the exact moment it mattered.
func TestUnreadableLoadBearingSourcesAreNotHealthy(t *testing.T) {
	t.Parallel()

	for _, unavailable := range [][]string{
		{"nodes"},
		{"pods"},
		{"nodes", "pods"},
		{"version", "nodes", "pods", "events", "metrics", "namespaces"},
	} {
		overview := domain.NewOverview(domain.OverviewInput{
			ClusterID:   domain.ClusterID("test"),
			Unavailable: unavailable,
			Now:         time.Now(),
		})

		if overview.Health == domain.HealthHealthy {
			t.Errorf("unavailable %v graded healthy; nothing was read to justify it", unavailable)
		}
		if overview.Health != domain.HealthUnknown {
			t.Errorf("unavailable %v graded %q, want unknown", unavailable, overview.Health)
		}
	}
}

// Sources whose absence the assessment genuinely survives must NOT cost the
// verdict. A cluster with no metrics-server is completely assessable, and
// degrading rather than failing on it is the entire point of Unavailable —
// this is the check that the new rule did not swallow that.
func TestDegradableSourcesStillGradeHealthy(t *testing.T) {
	t.Parallel()

	for _, unavailable := range [][]string{
		{"metrics"},
		{"events"},
		{"volumes", "claims"},
		{"metrics", "events", "namespaces", "version"},
	} {
		overview := domain.NewOverview(domain.OverviewInput{
			ClusterID:   domain.ClusterID("test"),
			Unavailable: unavailable,
			Now:         time.Now(),
		})

		if overview.Health != domain.HealthHealthy {
			t.Errorf("unavailable %v graded %q; these are survivable and should stay healthy",
				unavailable, overview.Health)
		}
	}
}

// Ignorance about one thing does not erase knowledge of another: a real finding
// still outranks the unknown, so an unready node is reported as such even when
// pods could not be listed.
func TestAFindingOutranksTheUnknown(t *testing.T) {
	t.Parallel()

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID:   domain.ClusterID("test"),
		Unavailable: []string{"pods"},
		Nodes:       []domain.Node{unreadyNode(t)},
		Now:         time.Now(),
	})

	if overview.Health == domain.HealthUnknown {
		t.Error("a real finding was demoted to unknown; the assessment did see something")
	}
	if len(overview.Findings) == 0 {
		t.Fatal("expected the unready node to raise a finding")
	}
}

// unreadyNode builds one node that is not Ready, for tests that need a finding
// to exist without caring how it arose.
func unreadyNode(t *testing.T) domain.Node {
	t.Helper()

	node, err := domain.NewNode(domain.NodeSpec{
		Name: "node-1", ClusterID: "dev", Ready: false, KubeletVersion: "v1.32.7",
		Capacity:    domain.Capacity{CPUMilli: 4000, MemoryBytes: 8 << 30, Pods: 110},
		Allocatable: domain.Capacity{CPUMilli: 4000, MemoryBytes: 8 << 30, Pods: 110},
		CreatedAt:   time.Now().Add(-24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("building node: %v", err)
	}
	return node
}

// nearLimitPod builds a running pod with a memory limit and a measurement
// against it, which is the only shape memoryLimitFindings looks at.
func nearLimitPod(t *testing.T, name string, limitBytes, usedBytes int64) domain.Pod {
	t.Helper()

	pod := podFixture(t, domain.PodSpec{
		Name: name, Namespace: "default", NodeName: "node-1",
		Phase:     domain.PodPhaseRunning,
		CreatedAt: overviewNow.Add(-time.Hour),
		Containers: []domain.Container{{
			Name:   "app",
			State:  domain.ContainerStateRunning,
			Limits: domain.Resources{MemoryBytes: limitBytes},
		}},
	})
	return pod.WithUsage(domain.NewMetrics(10, usedBytes))
}

func TestMemoryLimitFindingWarnsBeforeTheKernelKills(t *testing.T) {
	t.Parallel()

	const limit = 100 << 20

	overview := domain.NewOverview(domain.OverviewInput{
		ClusterID:       "dev",
		MetricsMeasured: true,
		Now:             overviewNow,
		Pods: []domain.Pod{
			nearLimitPod(t, "leaky", limit, 96<<20),   // 96% — reported
			nearLimitPod(t, "hottest", limit, 99<<20), // 99% — reported, and the worst
			nearLimitPod(t, "roomy", limit, 40<<20),   // 40% — not news
			nearLimitPod(t, "exactly", limit, 90<<20), // 90% — the boundary is inclusive
		},
	})

	finding, ok := findingByTitle(overview.Findings, "Pods near their memory limit")
	if !ok {
		t.Fatalf("expected a memory-limit finding; got %v", titles(overview.Findings))
	}
	if finding.Count != 3 {
		t.Errorf("count = %d, want the three at or above the line", finding.Count)
	}
	if finding.Severity != domain.SeverityWarning {
		t.Errorf("severity = %q, want warning: nothing has died yet", finding.Severity)
	}
	// The worst offender has to reach the summary — the whole point is to say
	// how close the closest one is.
	if !strings.Contains(finding.Summary, "99%") {
		t.Errorf("summary = %q, want the worst percentage named", finding.Summary)
	}

	names := make(map[string]bool, len(finding.Subjects))
	for _, subject := range finding.Subjects {
		names[subject.Name] = true
	}
	if names["roomy"] {
		t.Error("a pod at 40%% of its limit was reported as near it")
	}
}

func TestMemoryLimitFindingStaysQuietWithoutGrounds(t *testing.T) {
	t.Parallel()

	const limit = 100 << 20

	tests := []struct {
		name  string
		input domain.OverviewInput
	}{
		{
			// Without metrics every usage is zero, so every ratio is 0% —
			// which must read as "nothing measured", not "nothing wrong".
			name: "no metrics source",
			input: domain.OverviewInput{
				MetricsMeasured: false,
				Pods:            []domain.Pod{nearLimitPod(t, "leaky", limit, 99<<20)},
			},
		},
		{
			// No ceiling declared means no proximity to one. This is the
			// majority of real pods and must never be inferred into a finding.
			name: "no limit declared",
			input: domain.OverviewInput{
				MetricsMeasured: true,
				Pods: []domain.Pod{func() domain.Pod {
					pod := podFixture(t, domain.PodSpec{
						Name: "unbounded", Namespace: "default", NodeName: "node-1",
						Phase:      domain.PodPhaseRunning,
						Containers: []domain.Container{{Name: "app", State: domain.ContainerStateRunning}},
					})
					return pod.WithUsage(domain.NewMetrics(10, 4<<30))
				}()},
			},
		},
		{
			// A Succeeded pod's containers are gone. Its last measurement
			// predicts nothing, and the kernel will not be killing it.
			name: "terminal pod",
			input: domain.OverviewInput{
				MetricsMeasured: true,
				Pods: []domain.Pod{func() domain.Pod {
					pod := podFixture(t, domain.PodSpec{
						Name: "finished", Namespace: "batch", NodeName: "node-1",
						Phase: domain.PodPhaseSucceeded,
						Containers: []domain.Container{{
							Name:   "app",
							Limits: domain.Resources{MemoryBytes: limit},
						}},
					})
					return pod.WithUsage(domain.NewMetrics(10, 99<<20))
				}()},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			input := test.input
			input.ClusterID = "dev"
			input.Now = overviewNow

			if _, ok := findingByTitle(domain.NewOverview(input).Findings, "Pods near their memory limit"); ok {
				t.Error("reported a pod as near its memory limit on no evidence")
			}
		})
	}
}
