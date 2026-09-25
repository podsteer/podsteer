package k8s

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/podsteer/podsteer/app/config"
	"github.com/podsteer/podsteer/app/domain"
)

// Every store, against a real cluster, on purpose.
//
// SKIPPED UNLESS ASKED FOR, because it needs a reachable cluster and the rest
// of this package deliberately does not. It is here rather than deleted
// because the fake clientset cannot exercise the path production takes:
// client-go streams the initial list through the watch by default, and the
// fake does not implement that protocol — so every other test in
// watch_test.go pins the fallback and none of them proves a store ever syncs.
//
//	PODSTEER_LIVE_TEST=1 go test ./app/adapters/k8s/ -run LiveEveryStore -v
//
// It reads and compares; it writes nothing.
func TestLiveEveryStoreAgrees(t *testing.T) {
	if os.Getenv("PODSTEER_LIVE_TEST") == "" {
		t.Skip("set PODSTEER_LIVE_TEST=1")
	}

	loaded, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	adapter := New(Config{LiveWatch: loaded.Kubernetes.LiveWatch}, nil)
	defer adapter.StopAllWatches()

	clusters, err := adapter.Clusters(context.Background())
	if err != nil {
		t.Fatalf("Clusters() error = %v", err)
	}
	var id domain.ClusterID
	for _, cluster := range clusters {
		if cluster.IsCurrent() {
			id = cluster.ID()
		}
	}

	ctx := context.Background()
	pods, _ := adapter.ListPods(ctx, id, domain.NamespaceAll, domain.Projection{})
	sets, _ := adapter.ListWorkloads(ctx, id, domain.WorkloadReplicaSet, domain.NamespaceAll, domain.Projection{})
	jobs, _ := adapter.ListWorkloads(ctx, id, domain.WorkloadJob, domain.NamespaceAll, domain.Projection{})
	fmt.Printf("CLUSTER pods=%d replicasets=%d jobs=%d\n", len(pods), len(sets), len(jobs))

	deadline := time.After(45 * time.Second)
	for {
		_, p := watched[*corev1.Pod](adapter.watches, id, watchPods)
		_, r := watched[*appsv1.ReplicaSet](adapter.watches, id, watchReplicaSets)
		_, j := watched[*batchv1.Job](adapter.watches, id, watchJobs)
		if p && r && j {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("stores never all served (pods=%v replicasets=%v jobs=%v)", p, r, j)
		default:
			time.Sleep(200 * time.Millisecond)
		}
	}

	time.Sleep(readTTL + 100*time.Millisecond)
	storedPods, _ := adapter.ListPods(ctx, id, domain.NamespaceAll, domain.Projection{})
	storedSets, _ := adapter.ListWorkloads(ctx, id, domain.WorkloadReplicaSet, domain.NamespaceAll, domain.Projection{})
	storedJobs, _ := adapter.ListWorkloads(ctx, id, domain.WorkloadJob, domain.NamespaceAll, domain.Projection{})
	fmt.Printf("STORE   pods=%d replicasets=%d jobs=%d\n", len(storedPods), len(storedSets), len(storedJobs))

	if len(storedPods) != len(pods) || len(storedSets) != len(sets) || len(storedJobs) != len(jobs) {
		t.Fatal("a store disagrees with the cluster")
	}
	if len(storedSets) > 0 {
		fmt.Printf("SAMPLE rs=%s/%s ready=%d images=%v selector=%v\n",
			storedSets[0].Namespace(), storedSets[0].Name(),
			storedSets[0].Ready(), storedSets[0].Images(), storedSets[0].Selector())
	}

	// And a namespace-scoped read, which is what a controller page makes.
	scoped, err := adapter.ListWorkloads(ctx, id, domain.WorkloadReplicaSet, "kube-system", domain.Projection{})
	if err != nil {
		t.Fatalf("scoped ListWorkloads() error = %v", err)
	}
	fmt.Printf("SCOPED kube-system replicasets=%d\n", len(scoped))
}
