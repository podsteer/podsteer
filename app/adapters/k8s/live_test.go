package k8s

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// The watch, against a real cluster, on purpose.
//
// SKIPPED UNLESS ASKED FOR, because it needs a reachable cluster and the rest
// of this package deliberately does not. It is here rather than deleted
// because the fake clientset cannot exercise the path production takes:
// client-go streams the initial list through the watch by default, and the
// fake does not implement that protocol — so every other test in watch_test.go
// pins the fallback and none of them proves the store ever syncs for real.
//
//	PODSTEER_LIVE_TEST=1 go test ./app/adapters/k8s/ -run LiveWatch -v
//
// It reads and compares; it writes nothing.
func TestLiveWatchAgainstTheCurrentContext(t *testing.T) {
	if os.Getenv("PODSTEER_LIVE_TEST") == "" {
		t.Skip("set PODSTEER_LIVE_TEST=1")
	}

	adapter := New(Config{LiveWatch: true}, nil)
	defer adapter.StopAllWatches()

	clusters, err := adapter.Clusters(context.Background())
	if err != nil || len(clusters) == 0 {
		t.Fatalf("Clusters() = %d, %v", len(clusters), err)
	}

	var id domain.ClusterID
	for _, cluster := range clusters {
		if cluster.IsCurrent() {
			id = cluster.ID()
		}
	}
	if id.IsZero() {
		t.Fatal("no current context")
	}

	ctx := context.Background()
	first, err := adapter.ListPods(ctx, id, domain.NamespaceAll)
	if err != nil {
		t.Fatalf("ListPods() error = %v", err)
	}
	fmt.Printf("FIRST(from cluster)=%d\n", len(first))

	// Give the reflector a moment to sync, then read again — this one should
	// come from the store.
	deadline := time.After(30 * time.Second)
	for {
		if _, serving := adapter.watches.pods(id); serving {
			break
		}
		select {
		case <-deadline:
			t.Fatal("the store never started serving")
		default:
			time.Sleep(200 * time.Millisecond)
		}
	}

	// Past the read cache's window, so this is a genuine second read.
	time.Sleep(readTTL + 100*time.Millisecond)
	second, err := adapter.ListPods(ctx, id, domain.NamespaceAll)
	if err != nil {
		t.Fatalf("ListPods() from store error = %v", err)
	}
	fmt.Printf("SECOND(from store)=%d\n", len(second))

	if len(second) != len(first) {
		t.Fatalf("store has %d pods, the cluster listed %d", len(second), len(first))
	}
	if len(second) > 0 {
		fmt.Printf("SAMPLE=%s/%s phase=%s containers=%d\n",
			second[0].Namespace(), second[0].Name(), second[0].Phase(), second[0].TotalContainers())
	}
}
