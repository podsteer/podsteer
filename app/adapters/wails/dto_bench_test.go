package wails

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// The pod list is the largest thing that crosses the bridge, and these pin
// what it costs. The synthetic pods are shaped like a real namespace's rather
// than like a minimal fixture — labels a Deployment stamps, two containers
// with requests and limits, a controller, measured usage — because a budget
// measured on a pod with nothing on it says nothing about a cluster.

// synthDomainPods returns n pods spread over twenty namespaces and a hundred
// nodes, roughly one in twenty unhealthy in one of the ways the status chips
// select on.
func synthDomainPods(tb testing.TB, n int) []domain.Pod {
	tb.Helper()

	created := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	pods := make([]domain.Pod, 0, n)
	for i := range n {
		namespace := domain.NamespaceName(fmt.Sprintf("team-%02d", i%20))
		app := fmt.Sprintf("service-%03d", i%250)
		phase := domain.PodPhaseRunning
		ready := true
		state := domain.ContainerStateRunning
		reason := ""
		var last domain.Termination
		switch i % 20 {
		case 7:
			ready, state, reason = false, domain.ContainerStateWaiting, "CrashLoopBackOff"
			last = domain.Termination{ExitCode: 137, Reason: "OOMKilled", FinishedAt: created}
		case 13:
			phase, ready, state, reason = domain.PodPhasePending, false, domain.ContainerStateWaiting, "ImagePullBackOff"
		}

		containers := []domain.Container{
			{
				Name:            "app",
				Image:           "registry.example.com/" + app + ":1.4.2",
				Ready:           ready,
				RestartCount:    int32(i % 4),
				State:           state,
				Reason:          reason,
				Requests:        domain.Resources{CPUMilli: 250, MemoryBytes: 256 << 20},
				Limits:          domain.Resources{CPUMilli: 1000, MemoryBytes: 512 << 20},
				Started:         ready,
				LastTermination: last,
			},
			{
				Name:     "istio-proxy",
				Image:    "docker.io/istio/proxyv2:1.23.0",
				Ready:    true,
				State:    domain.ContainerStateRunning,
				Requests: domain.Resources{CPUMilli: 100, MemoryBytes: 128 << 20},
				Limits:   domain.Resources{CPUMilli: 2000, MemoryBytes: 1 << 30},
				Started:  true,
			},
		}

		pod, err := domain.NewPod(domain.PodSpec{
			UID:        fmt.Sprintf("7c9e6679-7425-40de-944b-%012d", i),
			Name:       fmt.Sprintf("%s-7d9f8c6b5-%05d", app, i),
			Namespace:  namespace,
			ClusterID:  "kind-scale",
			Phase:      phase,
			NodeName:   fmt.Sprintf("worker-%03d", i%100),
			PodIP:      fmt.Sprintf("10.%d.%d.%d", i/65536%256, i/256%256, i%256),
			Containers: containers,
			Labels: map[string]string{
				"app.kubernetes.io/name":     app,
				"app.kubernetes.io/instance": app + "-prod",
				"app.kubernetes.io/version":  "1.4.2",
				"pod-template-hash":          "7d9f8c6b5",
				"team":                       string(namespace),
			},
			Owners:    []domain.OwnerReference{{Kind: "ReplicaSet", Name: app + "-7d9f8c6b5", Controller: true}},
			QoSClass:  domain.QoSBurstable,
			Usage:     domain.NewMetrics(int64(10+i%900), int64(64<<20+(i%400)<<20)),
			CreatedAt: created.Add(-time.Duration(i) * time.Minute),
		})
		if err != nil {
			tb.Fatalf("NewPod() error = %v", err)
		}
		pods = append(pods, pod)
	}
	return pods
}

// podsPerPage is the page a PodPage carries at most by default — the
// table's own default page size, so the budget is measured on what an
// operator actually receives per tick.
const podsPerPage = 50

// The bridge budget for one page of the pod list. Per pod is the sharper of
// the two: it is what catches a field that grows without bound — an
// annotation map shipped wholesale, a findings list that repeats — while the
// page figure is what a tick actually costs.
//
// The per-pod figure is the page budget shared out over a full page, not a
// round number: a realistic pod measures about 2.4 KB (two containers, two
// info findings whose advice is a paragraph each), so the 2 KB first guessed
// at would have failed on the pod this test is built from.
const (
	podPageBudgetBytes = 160 << 10
	podBudgetBytes     = podPageBudgetBytes / podsPerPage
)

func TestPodPayloadBudget(t *testing.T) {
	t.Parallel()

	// The page as it crosses the bridge: domain.QueryPods over a list ten
	// times the page, rows and counts and all.
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	page := domain.QueryPods(synthDomainPods(t, 10*podsPerPage), domain.PodQuery{Limit: podsPerPage}, now, nil)
	if len(page.Rows) != podsPerPage {
		t.Fatalf("page has %d rows, want %d", len(page.Rows), podsPerPage)
	}

	encoded, err := json.Marshal(toPodPage(page, now))
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	if len(encoded) > podPageBudgetBytes {
		t.Errorf("a %d-pod page is %d bytes, over the %d budget", podsPerPage, len(encoded), podPageBudgetBytes)
	}
	if perPod := len(encoded) / podsPerPage; perPod > podBudgetBytes {
		t.Errorf("one pod is %d bytes on the wire, over the %d budget", perPod, podBudgetBytes)
	}
}

// TestPodListPayloadSize records what the WHOLE list costs — what every tick
// of the pod table crossed before it was paged in Go, and what ListPods
// still returns to its other callers — beside what one page costs now.
// Logged rather than asserted: the budget is TestPodPayloadBudget's. Run
// with -v to read it.
func TestPodListPayloadSize(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	for _, n := range []int{5_000, 10_000} {
		encoded, err := json.Marshal(toPods(synthDomainPods(t, n), now))
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		t.Logf("whole list, %d pods: %d bytes (%.1f MB)", n, len(encoded), float64(len(encoded))/(1<<20))

		page, err := json.Marshal(toPodPage(domain.QueryPods(synthDomainPods(t, n), domain.PodQuery{Limit: podsPerPage}, now, nil), now))
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		t.Logf("one %d-row page of %d pods: %d bytes (%.1f KB)", podsPerPage, n, len(page), float64(len(page))/(1<<10))
	}
}

func BenchmarkToPods10k(b *testing.B) {
	pods := synthDomainPods(b, 10_000)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	b.ReportAllocs()
	for b.Loop() {
		_ = toPods(pods, now)
	}
}
