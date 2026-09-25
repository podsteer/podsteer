package k8s

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/podsteer/podsteer/app/domain"
)

// What one refresh costs against a real cluster, measured rather than guessed.
//
// SKIPPED UNLESS ASKED FOR. It reads and prints; it changes nothing. It exists
// because every claim about what this application costs is otherwise an
// argument, and the argument is settled by a number.
//
//	PODSTEER_LIVE_TEST=1 go test ./app/adapters/k8s/ -run LiveTickCost -v
//
// On a 199-pod, 18-node cluster with 65 custom kinds, at the time it was
// written:
//
//	ListPods(all) cold          379ms   the network read
//	ListPods(all) cached          0ms   the read cache, within its window
//	ListPods(all) from store      2ms   the watch, once synced
//	PodMetrics(all)              59ms   polled, and always will be
//	ListWorkloads ReplicaSet      0ms   from the store
//	DiscoverCustomKinds          84ms   once per connect
//
// The 379ms against 2ms is the whole case for watching pods, and the 59ms is
// the floor underneath it: metrics.k8s.io serves no watch verb.
func TestLiveTickCost(t *testing.T) {
	if os.Getenv("PODSTEER_LIVE_TEST") == "" {
		t.Skip("set PODSTEER_LIVE_TEST=1")
	}

	adapter := New(Config{LiveWatch: true}, nil)
	defer adapter.StopAllWatches()

	clusters, err := adapter.Clusters(context.Background())
	if err != nil {
		t.Fatalf("Clusters(): %v", err)
	}
	var id domain.ClusterID
	for _, cluster := range clusters {
		if cluster.IsCurrent() {
			id = cluster.ID()
		}
	}

	ctx := context.Background()
	measure := func(label string, fn func() int) {
		start := time.Now()
		n := fn()
		fmt.Printf("%-28s %6dms  n=%d\n", label, time.Since(start).Milliseconds(), n)
	}

	measure("ListPods(all) cold", func() int {
		pods, _ := adapter.ListPods(ctx, id, domain.NamespaceAll, domain.Projection{})
		return len(pods)
	})
	measure("ListPods(all) cached", func() int {
		pods, _ := adapter.ListPods(ctx, id, domain.NamespaceAll, domain.Projection{})
		return len(pods)
	})
	measure("PodMetrics(all)", func() int {
		usage, _ := adapter.PodMetrics(ctx, id, domain.NamespaceAll)
		return len(usage)
	})
	measure("ListNodes", func() int {
		nodes, _ := adapter.ListNodes(ctx, id, domain.Projection{})
		return len(nodes)
	})
	for _, kind := range domain.WorkloadKinds() {
		measure("ListWorkloads "+string(kind), func() int {
			workloads, _ := adapter.ListWorkloads(ctx, id, kind, domain.NamespaceAll, domain.Projection{})
			return len(workloads)
		})
	}
	measure("DiscoverCustomKinds", func() int {
		kinds, _ := adapter.DiscoverCustomKinds(ctx, id)
		return len(kinds)
	})

	// And once the watch is serving, which is the steady state.
	deadline := time.After(30 * time.Second)
	for {
		if _, ok := watched[*corev1.Pod](adapter.watches, id, watchPods); ok {
			break
		}
		select {
		case <-deadline:
			t.Fatal("store never served")
		default:
			time.Sleep(200 * time.Millisecond)
		}
	}
	time.Sleep(readTTL + 100*time.Millisecond)
	measure("ListPods(all) from store", func() int {
		pods, _ := adapter.ListPods(ctx, id, domain.NamespaceAll, domain.Projection{})
		return len(pods)
	})
}
