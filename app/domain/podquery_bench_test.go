package domain_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/adapters/collation"
	"github.com/podsteer/podsteer/app/domain"
)

// synthPods returns n pods named pod-000… in list order, spread over twenty
// namespaces, one in twenty crash-looping — enough variety that a search, a
// chip and a sort each have real work to do.
func synthPods(tb testing.TB, n int) []domain.Pod {
	tb.Helper()

	created := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	pods := make([]domain.Pod, 0, n)
	for i := range n {
		container := domain.Container{Name: "app", Ready: true, State: domain.ContainerStateRunning}
		if i%20 == 7 {
			container = domain.Container{Name: "app", State: domain.ContainerStateWaiting, Reason: "CrashLoopBackOff", RestartCount: 9}
		}
		pod, err := domain.NewPod(domain.PodSpec{
			Name:       fmt.Sprintf("pod-%03d", i),
			Namespace:  domain.NamespaceName(fmt.Sprintf("team-%02d", i%20)),
			ClusterID:  "kind-scale",
			Phase:      domain.PodPhaseRunning,
			NodeName:   fmt.Sprintf("worker-%03d", i%100),
			PodIP:      fmt.Sprintf("10.%d.%d.%d", i/65536%256, i/256%256, i%256),
			Containers: []domain.Container{container},
			Labels:     map[string]string{"app": fmt.Sprintf("service-%03d", i%250), "pod-template-hash": "7d9f8c6b5"},
			Owners:     []domain.OwnerReference{{Kind: "ReplicaSet", Name: fmt.Sprintf("service-%03d-7d9f8c6b5", i%250), Controller: true}},
			Usage:      domain.NewMetrics(int64(10+i%900), int64(64<<20+(i%400)<<20)),
			CreatedAt:  created.Add(-time.Duration(i) * time.Minute),
		})
		if err != nil {
			tb.Fatalf("NewPod() error = %v", err)
		}
		pods = append(pods, pod)
	}
	return pods
}

// The heaviest page query the table makes: a search that keeps most rows, a
// chip, and a text sort — collation is the expensive comparison.
var heavyQuery = domain.PodQuery{
	Text:       "team",
	SortColumn: "node",
	Limit:      50,
}

// podQueryBudget is how long a 10k-pod page query may take. Generous on
// purpose — this runs on CI machines of every speed — and still a fraction
// of the shortest refresh interval the application offers.
const podQueryBudget = 25 * time.Millisecond

func TestQueryPods10kWithinBudget(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("a timing budget means nothing under -short or the race detector")
	}

	pods := synthPods(t, 10_000)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	// The best of a few runs: the budget is about the work, not about
	// whatever else the machine was doing during one of them.
	best := time.Hour
	for range 5 {
		start := time.Now()
		page := domain.QueryPods(pods, heavyQuery, now, collation.Key)
		best = min(best, time.Since(start))
		if page.Matched != 10_000 {
			t.Fatalf("matched %d, want 10000", page.Matched)
		}
	}
	if best > podQueryBudget {
		t.Fatalf("a 10k-pod page query took %s, over the %s budget", best, podQueryBudget)
	}
}

func BenchmarkQueryPods10k(b *testing.B) {
	pods := synthPods(b, 10_000)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	b.ReportAllocs()
	for b.Loop() {
		_ = domain.QueryPods(pods, heavyQuery, now, collation.Key)
	}
}
