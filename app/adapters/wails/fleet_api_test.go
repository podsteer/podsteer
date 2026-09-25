package wails

import (
	"fmt"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// TestToClusterPodsSaysWhyAClusterHasNoRows pins the wire contract the
// status strip depends on: a refused cluster crosses with its verdict and
// the same operator sentence a single-cluster call would have shown, a
// healthy one with no sentence at all, and Missing is always a list.
func TestToClusterPodsSaysWhyAClusterHasNoRows(t *testing.T) {
	t.Parallel()

	pod, err := domain.NewPod(domain.PodSpec{Name: "web-0", Namespace: "default", ClusterID: "dev"})
	if err != nil {
		t.Fatalf("NewPod() error = %v", err)
	}
	refusal := fmt.Errorf("listing pods: %w", ports.ErrForbidden)
	_, wantReason := classifyError(refusal)

	got := toClusterPods([]domain.ClusterRead[domain.Pod]{
		{Cluster: "dev", Status: domain.ClusterReadOK, Items: []domain.Pod{pod}},
		{Cluster: "prod", Status: domain.ClusterReadForbidden, Err: refusal},
	}, dtoNow)

	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}

	dev := got[0]
	if dev.Cluster != "dev" || dev.Status != "ok" || dev.Reason != "" || len(dev.Pods) != 1 {
		t.Errorf("dev = %+v, want ok with one pod and no reason", dev)
	}
	if dev.Missing == nil {
		t.Error("dev.Missing = nil, want an empty list on the wire")
	}

	prod := got[1]
	if prod.Cluster != "prod" || prod.Status != "forbidden" || len(prod.Pods) != 0 {
		t.Errorf("prod = %+v, want forbidden with no pods", prod)
	}
	if prod.Reason != wantReason {
		t.Errorf("prod.Reason = %q, want the classified sentence %q", prod.Reason, wantReason)
	}
}

func TestToClusterWorkloadsCarriesWhatAPartialReadIsMissing(t *testing.T) {
	t.Parallel()

	got := toClusterWorkloads([]domain.ClusterRead[domain.Workload]{
		{
			Cluster: "dev",
			Status:  domain.ClusterReadPartial,
			Missing: []string{"CronJob", "Job"},
			Err:     fmt.Errorf("CronJob: %w", ports.ErrForbidden),
		},
	}, dtoNow)

	if got[0].Status != "partial" {
		t.Errorf("Status = %q, want partial", got[0].Status)
	}
	if len(got[0].Missing) != 2 || got[0].Missing[0] != "CronJob" {
		t.Errorf("Missing = %v, want [CronJob Job]", got[0].Missing)
	}
	if got[0].Reason == "" {
		t.Error("Reason is empty on a partial read, want the refusal's sentence")
	}
}

func TestFleetArgsRefusesAnInvalidClusterID(t *testing.T) {
	t.Parallel()

	if _, _, err := fleetArgs([]string{"dev", ""}, ""); err == nil {
		t.Error("fleetArgs() accepted an empty cluster id")
	}
	if _, _, err := fleetArgs([]string{"dev"}, "Not A Namespace"); err == nil {
		t.Error("fleetArgs() accepted an invalid namespace")
	}

	ids, ns, err := fleetArgs([]string{"dev", "prod"}, "")
	if err != nil {
		t.Fatalf("fleetArgs() error = %v", err)
	}
	if len(ids) != 2 || ns != domain.NamespaceAll {
		t.Errorf("fleetArgs() = %v, %q, want two ids across all namespaces", ids, ns)
	}
}
