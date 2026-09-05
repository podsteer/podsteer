package application_test

import (
	"context"
	"testing"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
)

// TestReconnectingInTheFreshnessWindowIsNotServedThePreviousConnection is the
// stale-assessment race.
//
// The overview forgets a cluster's assessment only when a READ finds it
// unregistered, and Disconnect never produces such a read. So a tab closed
// and reopened inside overviewFreshness is handed the assessment made for the
// connection before it — and CLAUDE.md says re-pointing a kubeconfig context
// at a different cluster is routine, which is what makes this more than a
// stale number: the history sampler, whose freshness window is its own
// thirty-second interval, writes one sample of the OLD cluster into the NEW
// cluster's history file.
//
// The API server's version stands in for "a different cluster" because it is
// the cheapest fact on the assessment that could not have changed on its own.
func TestReconnectingInTheFreshnessWindowIsNotServedThePreviousConnection(t *testing.T) {
	kubernetes := &fakeKubernetes{
		version: domain.ServerVersion{GitVersion: "v1.30.0", Major: "1", Minor: "30"},
	}

	registry := application.NewRegistry()
	overview, err := application.NewOverviewService(application.OverviewServiceDeps{
		Cluster:   kubernetes,
		Workloads: kubernetes,
		Events:    &fakeEvents{},
		Metrics:   kubernetes,
		APIs:      kubernetes,
		Registry:  registry,
	})
	if err != nil {
		t.Fatalf("NewOverviewService() error = %v", err)
	}

	clusters, err := application.NewClusterService(application.ClusterServiceDeps{
		Kubeconfig: &fakeKubeconfig{},
		Cluster:    kubernetes,
		Workloads:  kubernetes,
		Metrics:    kubernetes,
		Events:     &recordingPublisher{},
		Registry:   registry,
		Catalog:    domain.NewCatalog(),
		// The whole subject of the test: the overview is released by the same
		// hook the adapter's caches are, rather than by a read that never
		// happens.
		Invalidator: application.Invalidators{overview},
	})
	if err != nil {
		t.Fatalf("NewClusterService() error = %v", err)
	}

	ctx := context.Background()
	registry.Open(mustCluster(t, "dev", true))

	first, err := overview.Overview(ctx, "dev")
	if err != nil {
		t.Fatalf("first Overview() error = %v", err)
	}
	if first.Version.GitVersion != "v1.30.0" {
		t.Fatalf("first assessment version = %q, want v1.30.0", first.Version.GitVersion)
	}

	// The tab closes.
	if err := clusters.Disconnect(ctx, "dev"); err != nil {
		t.Fatalf("Disconnect() error = %v", err)
	}

	// The context now resolves to a different cluster — a rewritten
	// kubeconfig, a rotated environment, a colleague's file.
	kubernetes.version = domain.ServerVersion{GitVersion: "v1.31.0", Major: "1", Minor: "31"}

	// And the tab is reopened at once, well inside overviewFreshness.
	registry.Open(mustCluster(t, "dev", true))

	second, err := overview.Overview(ctx, "dev")
	if err != nil {
		t.Fatalf("second Overview() error = %v", err)
	}
	if second.Version.GitVersion != "v1.31.0" {
		t.Fatalf("second assessment version = %q, want v1.31.0 — the reconnect was served the PREVIOUS connection's assessment", second.Version.GitVersion)
	}
}

// TestInvalidatorsFanOutInOrder pins the composite the composition root wires:
// one disconnect has more than one thing to release, and a nil member must not
// cost the caller a branch of its own.
func TestInvalidatorsFanOutInOrder(t *testing.T) {
	var called []string
	first := invalidatorFunc(func(domain.ClusterID) { called = append(called, "first") })
	second := invalidatorFunc(func(domain.ClusterID) { called = append(called, "second") })

	application.Invalidators{first, nil, second}.Invalidate("dev")

	if len(called) != 2 || called[0] != "first" || called[1] != "second" {
		t.Fatalf("invalidations = %v, want [first second]", called)
	}
}

// invalidatorFunc adapts a function to application.ClusterInvalidator.
type invalidatorFunc func(domain.ClusterID)

func (f invalidatorFunc) Invalidate(id domain.ClusterID) { f(id) }
