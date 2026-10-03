package k8s

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

func nodeNamesN(n int) []string {
	names := make([]string, n)
	for i := range n {
		names[i] = fmt.Sprintf("node-%04d", i)
	}
	return names
}

func answersFor(names []string) map[string]domain.NodeFilesystems {
	out := make(map[string]domain.NodeFilesystems, len(names))
	for _, name := range names {
		out[name] = domain.NodeFilesystems{Measured: true}
	}
	return out
}

func TestASmallClusterIsSweptWholeEveryMinute(t *testing.T) {
	t.Parallel()

	nodes := nodeNamesN(filesystemSweepCap)
	if got := batchFor(nodes, filesystemEntry{}); len(got) != len(nodes) {
		t.Fatalf("asked %d of %d nodes, want every one", len(got), len(nodes))
	}

	now := time.Now()
	previous := mergeSweep(filesystemEntry{}, nodes, nodes, answersFor(nodes), now)
	// A node that stops answering drops out, as it always did.
	entry := mergeSweep(previous, nodes, nodes, answersFor(nodes[1:]), now.Add(time.Minute))
	if len(entry.result) != len(nodes)-1 || entry.lifetime() != filesystemTTL || entry.rolling() {
		t.Fatalf("whole sweep: %d answers, ttl %v, rolling %v", len(entry.result), entry.lifetime(), entry.rolling())
	}
}

// TestALargeClusterRollsThroughEveryNode pins the round-robin: each sweep
// asks the oldest batch, never-asked first, so every node is asked within
// ceil(N/batch) sweeps and none twice before all have been once.
func TestALargeClusterRollsThroughEveryNode(t *testing.T) {
	t.Parallel()

	nodes := nodeNamesN(300)
	start := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	var entry filesystemEntry
	asked := map[string]int{}

	sweeps := (len(nodes) + filesystemBatch - 1) / filesystemBatch
	for i := range sweeps {
		batch := batchFor(nodes, entry)
		if len(batch) != filesystemBatch {
			t.Fatalf("sweep %d asked %d nodes, want %d", i, len(batch), filesystemBatch)
		}
		for _, name := range batch {
			asked[name]++
		}
		entry = mergeSweep(entry, nodes, batch, answersFor(batch), start.Add(time.Duration(i)*filesystemBatchSpacing))
	}

	if len(asked) != len(nodes) {
		t.Fatalf("%d of %d nodes asked after %d sweeps, want all", len(asked), len(nodes), sweeps)
	}
	if len(entry.result) != len(nodes) || entry.lifetime() != filesystemBatchSpacing {
		t.Fatalf("rolling entry holds %d answers with ttl %v", len(entry.result), entry.lifetime())
	}

	// The next batch is the oldest answers: what is left of the first
	// sweep's — the last sweep wrapped round and re-asked its first 20.
	wrapped := sweeps*filesystemBatch - len(nodes)
	next := batchFor(nodes, entry)
	if !slices.Equal(next, nodes[wrapped:wrapped+filesystemBatch]) {
		t.Fatalf("next batch starts %v, want the oldest answers first", next[:3])
	}

	now := start.Add(time.Duration(sweeps-1) * filesystemBatchSpacing)
	coverage := entry.coverage(now)
	want := domain.DiskCoverage{Asked: 300, Answered: 300, OldestSeconds: int64((sweeps - 1) * int(filesystemBatchSpacing.Seconds())), Rolling: true}
	if coverage != want {
		t.Fatalf("coverage = %+v, want %+v", coverage, want)
	}
}

func TestARollingSweepForgetsNodesTheClusterNoLongerHas(t *testing.T) {
	t.Parallel()

	nodes := nodeNamesN(200)
	now := time.Now()
	entry := mergeSweep(filesystemEntry{}, nodes, nodes, answersFor(nodes), now)

	shrunk := nodes[:150]
	entry = mergeSweep(entry, shrunk, shrunk[:10], answersFor(shrunk[:10]), now.Add(time.Minute))
	if len(entry.result) != 150 {
		t.Fatalf("%d answers kept, want the 150 nodes still in the cluster", len(entry.result))
	}
	if coverage := entry.coverage(now.Add(time.Minute)); coverage.Asked != 150 || coverage.Answered != 150 {
		t.Fatalf("coverage = %+v", coverage)
	}
}

// TestDeadKubeletsDoNotStarveTheRotation pins the fix for a batch's worth of
// silent nodes: they are stamped asked like any other, so the rotation moves
// on to the healthy nodes instead of asking the dead ones on every batch.
func TestDeadKubeletsDoNotStarveTheRotation(t *testing.T) {
	t.Parallel()

	nodes := nodeNamesN(400)
	dead := map[string]bool{}
	for _, name := range nodes[:2*filesystemBatch] {
		dead[name] = true
	}
	start := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	var entry filesystemEntry
	asked := map[string]bool{}
	sweeps := (len(nodes) + filesystemBatch - 1) / filesystemBatch
	for i := range sweeps {
		batch := batchFor(nodes, entry)
		answers := map[string]domain.NodeFilesystems{}
		for _, name := range batch {
			asked[name] = true
			if !dead[name] {
				answers[name] = domain.NodeFilesystems{Measured: true}
			}
		}
		entry = mergeSweep(entry, nodes, batch, answers, start.Add(time.Duration(i)*filesystemBatchSpacing))
	}

	if len(asked) != len(nodes) {
		t.Fatalf("%d of %d nodes asked: dead kubelets held the rotation", len(asked), len(nodes))
	}
	if got, want := len(entry.result), len(nodes)-len(dead); got != want {
		t.Fatalf("%d answers, want every healthy node's %d", got, want)
	}
}

// TestCarriedAnswersDecay pins that a node silent for several rotations
// stops being presented as current.
func TestCarriedAnswersDecay(t *testing.T) {
	t.Parallel()

	nodes := nodeNamesN(200)
	start := time.Now()
	entry := mergeSweep(filesystemEntry{}, nodes, nodes[:filesystemBatch], answersFor(nodes[:filesystemBatch]), start)

	later := start.Add(carriedAnswerMaxAge(len(nodes)) + time.Second)
	entry = mergeSweep(entry, nodes, nodes[filesystemBatch:2*filesystemBatch], nil, later)
	if len(entry.result) != 0 {
		t.Fatalf("%d answers older than the bound still carried", len(entry.result))
	}
}

func TestARollingClusterCanStillBeRefused(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                          string
		answered, kept, asked, denied int
		failed, want                  bool
	}{
		{"an answer is never a refusal", 1, 300, 64, 63, true, false},
		{"nothing asked failed", 0, 300, 64, 0, false, false},
		{"silent kubelets, answers carried", 0, 300, 64, 0, true, false},
		{"every node denied, answers carried", 0, 300, 64, 64, true, true},
		{"nothing to show at all", 0, 0, 64, 0, true, true},
	}
	for _, tt := range tests {
		if got := sweepRefused(tt.answered, tt.kept, tt.asked, tt.denied, tt.failed); got != tt.want {
			t.Errorf("%s: refused = %v, want %v", tt.name, got, tt.want)
		}
	}
}
