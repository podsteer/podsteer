package application

import (
	"fmt"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

func usagePod(t *testing.T, cluster, name string, cpu int64, measured bool) domain.Pod {
	t.Helper()
	usage := domain.Metrics{}
	if measured {
		usage = domain.NewMetrics(cpu, 64<<20)
	}
	pod, err := domain.NewPod(domain.PodSpec{Name: name, Namespace: "shop", ClusterID: domain.ClusterID(cluster), Usage: usage})
	if err != nil {
		t.Fatalf("NewPod() error = %v", err)
	}
	return pod
}

func TestPodUsageRingKeepsEveryListedPod(t *testing.T) {
	t.Parallel()

	var ring podUsageRing
	start := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	key := podUsageKey{cluster: "prod", namespace: "shop", name: "web-1"}

	for i := range domain.UsageRingCapacity + 50 {
		recordAll(&ring, []domain.Pod{
			usagePod(t, "prod", "web-1", int64(i), true),
			usagePod(t, "prod", "idle", 0, false),
		}, start.Add(time.Duration(i)*10*time.Second))
	}

	got := ring.since(key, start.Add(time.Duration(domain.UsageRingCapacity+50)*10*time.Second))
	if len(got) != domain.UsageRingCapacity {
		t.Fatalf("kept %d points, want the ring's %d", len(got), domain.UsageRingCapacity)
	}
	if got[len(got)-1].CPUMilli != int64(domain.UsageRingCapacity+49) {
		t.Fatalf("last point = %+v, want the newest", got[len(got)-1])
	}
	if ring.size() != 1 {
		t.Fatalf("%d pods kept, want 1 — an unmeasured pod has no series", ring.size())
	}
}

func TestPodUsageRingCollapsesOneTicksReads(t *testing.T) {
	t.Parallel()

	var ring podUsageRing
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	recordAll(&ring, []domain.Pod{usagePod(t, "prod", "web-1", 100, true)}, now)
	recordAll(&ring, []domain.Pod{usagePod(t, "prod", "web-1", 120, true)}, now.Add(time.Second))

	got := ring.since(podUsageKey{cluster: "prod", namespace: "shop", name: "web-1"}, now.Add(time.Second))
	if len(got) != 1 || got[0].CPUMilli != 120 {
		t.Fatalf("got %+v, want one point, the later reading", got)
	}
}

func TestPodUsageRingKeepsClustersApartAndForgetsOne(t *testing.T) {
	t.Parallel()

	var ring podUsageRing
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	recordAll(&ring, []domain.Pod{usagePod(t, "prod", "web-1", 100, true), usagePod(t, "staging", "web-1", 5, true)}, now)

	prod := ring.since(podUsageKey{cluster: "prod", namespace: "shop", name: "web-1"}, now)
	if len(prod) != 1 || prod[0].CPUMilli != 100 {
		t.Fatalf("prod's series = %+v: another cluster's same-named pod leaked in", prod)
	}

	ring.forget("prod")
	if got := ring.since(podUsageKey{cluster: "prod", namespace: "shop", name: "web-1"}, now); len(got) != 0 {
		t.Fatalf("a forgotten cluster still has %d points", len(got))
	}
	if ring.size() != 1 {
		t.Fatalf("forgetting prod took staging with it")
	}
}

func TestPodUsageRingSweepsPodsNothingMeasures(t *testing.T) {
	t.Parallel()

	var ring podUsageRing
	start := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	recordAll(&ring, []domain.Pod{usagePod(t, "prod", "gone", 1, true)}, start)

	later := start.Add(domain.UsageRingMaxAge + time.Minute)
	pods := make([]domain.Pod, 0, 1000)
	for i := range 1000 {
		pods = append(pods, usagePod(t, "prod", fmt.Sprintf("web-%d", i), 1, true))
	}
	// The sweep is by time: the first read past sweepUsageEvery drops it.
	recordAll(&ring, pods, later)
	if got := ring.since(podUsageKey{cluster: "prod", namespace: "shop", name: "gone"}, later); len(got) != 0 {
		t.Fatal("a pod gone for over an hour is still served")
	}
	if ring.size() != 1000 {
		t.Fatalf("%d pods kept, want the 1000 still listed", ring.size())
	}
}

// recordAll files pods of any cluster at their cluster's current generation.
func recordAll(r *podUsageRing, pods []domain.Pod, now time.Time) {
	byCluster := map[domain.ClusterID][]domain.Pod{}
	for _, pod := range pods {
		byCluster[pod.ClusterID()] = append(byCluster[pod.ClusterID()], pod)
	}
	for id, share := range byCluster {
		r.record(id, r.generationOf(id), share, now)
	}
}

// TestPodUsageRingRefusesAReadFromBeforeAForget pins the generation: a list
// read that began before a disconnect cannot file the old cluster's pods
// after it.
func TestPodUsageRingRefusesAReadFromBeforeAForget(t *testing.T) {
	t.Parallel()

	var ring podUsageRing
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	began := ring.generationOf("prod")
	ring.forget("prod")
	ring.record("prod", began, []domain.Pod{usagePod(t, "prod", "web-1", 100, true)}, now)

	if ring.size() != 0 {
		t.Fatal("a read from before the forget was filed")
	}
}
