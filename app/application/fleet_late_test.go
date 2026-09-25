package application

import (
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// TestSweepLateDropsWhatNobodyCameBackFor pins the bound the field's own doc
// claimed and did not keep.
//
// `late` was described as bounded by three reads times the clusters ever
// opened. The key also names a namespace and, for a table, a kind — so it
// actually grew by one entry for every distinct question ever asked, and an
// answer nobody returned for was never removed at all.
func TestSweepLateDropsWhatNobodyCameBackFor(t *testing.T) {
	t.Parallel()

	service := &FleetService{}
	fresh := lateKey("dev", "pods", "shop")
	stale := lateKey("dev", "pods", "billing")

	service.late.Store(fresh, lateAnswer{at: time.Now()})
	service.late.Store(stale, lateAnswer{at: time.Now().Add(-lateAnswerTTL - time.Second)})

	service.sweepLate()

	if _, found := service.late.Load(fresh); !found {
		t.Error("an answer a steadily slow cluster is about to be asked for again was swept")
	}
	if _, found := service.late.Load(stale); found {
		t.Errorf("an answer older than %s is still held, and would be served as this tick's rows", lateAnswerTTL)
	}
}

// TestInvalidateDropsOnlyTheNamedClustersLateAnswers is the disconnect half.
//
// A tab is routinely reconnected because its kubeconfig context now points at
// a different cluster, and the settings path reconnects every open cluster
// without closing a single tab. A late answer stored under the old connection
// would be handed to the first read of the new one and rendered as that
// cluster's rows, marked only "slow" — which is about the tab, not about
// where the rows came from.
func TestInvalidateDropsOnlyTheNamedClustersLateAnswers(t *testing.T) {
	t.Parallel()

	service := &FleetService{}
	gone := []string{
		lateKey("dev", "pods", "shop"),
		lateKey("dev", "table:acme.io/widgets", "billing"),
	}
	kept := lateKey("prod", "pods", "shop")

	for _, key := range append(append([]string{}, gone...), kept) {
		service.late.Store(key, lateAnswer{at: time.Now()})
	}

	service.Invalidate(domain.ClusterID("dev"))

	for _, key := range gone {
		if _, found := service.late.Load(key); found {
			t.Errorf("%q survived the disconnect", key)
		}
	}
	if _, found := service.late.Load(kept); !found {
		t.Error("disconnecting one cluster cost another cluster its late answer")
	}
}
