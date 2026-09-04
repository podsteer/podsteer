package domain_test

import (
	"slices"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

// TestFleetWorkloadKindsOmitReplicaSets pins the one exclusion: a merged
// workload table shows controllers, not the intermediates a Deployment keeps
// behind it, or every Deployment would appear once per revision retained.
func TestFleetWorkloadKindsOmitReplicaSets(t *testing.T) {
	t.Parallel()

	kinds := domain.FleetWorkloadKinds()

	if slices.Contains(kinds, domain.WorkloadReplicaSet) {
		t.Fatalf("FleetWorkloadKinds() = %v, must not include ReplicaSet", kinds)
	}
	for _, kind := range domain.WorkloadKinds() {
		if kind == domain.WorkloadReplicaSet {
			continue
		}
		if !slices.Contains(kinds, kind) {
			t.Errorf("FleetWorkloadKinds() = %v, missing %s", kinds, kind)
		}
	}
}
