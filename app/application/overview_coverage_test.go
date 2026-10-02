package application_test

import (
	"context"
	"testing"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
)

// coveringMetrics is the fake cluster plus the adapter's FilesystemCoverage.
type coveringMetrics struct {
	*fakeKubernetes
	coverage domain.DiskCoverage
}

func (c coveringMetrics) FilesystemCoverage(domain.ClusterID) (domain.DiskCoverage, bool) {
	return c.coverage, true
}

// TestOverviewSaysHowMuchOfTheClusterTheDiskFigureCovers pins that a rolling
// sweep's coverage reaches the assessment: on a cluster asked a batch at a
// time, "the fullest disk" is the fullest of what has answered.
func TestOverviewSaysHowMuchOfTheClusterTheDiskFigureCovers(t *testing.T) {
	t.Parallel()

	kubernetes := &fakeKubernetes{}
	want := domain.DiskCoverage{Asked: 300, Answered: 192, OldestSeconds: 45, Rolling: true}

	registry := application.NewRegistry()
	registry.Open(mustCluster(t, "dev", true))
	service, err := application.NewOverviewService(application.OverviewServiceDeps{
		Cluster:   kubernetes,
		Workloads: kubernetes,
		Events:    &fakeEvents{},
		Metrics:   coveringMetrics{fakeKubernetes: kubernetes, coverage: want},
		APIs:      kubernetes,
		Registry:  registry,
	})
	if err != nil {
		t.Fatalf("NewOverviewService() error = %v", err)
	}

	overview, err := service.Overview(context.Background(), "dev")
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if got := overview.Nodes.Disks.Coverage; got != want {
		t.Fatalf("coverage = %+v, want %+v", got, want)
	}
}
