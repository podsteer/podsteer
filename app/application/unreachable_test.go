package application_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// goneCluster answers every read with ErrUnreachable, as the k8s adapter does
// when the transport fails — a dropped VPN, a sleeping laptop, a closed
// port-forward.
//
// It counts assessments rather than calls: what these tests care about is how
// many times the service went back to the cluster, not how many reads each
// attempt made.
type goneCluster struct {
	assessments atomic.Int64
	// recoverAfter, when positive, makes the Nth assessment and everything
	// after it succeed — the blip that retrying is meant to ride out.
	recoverAfter int64
}

func (g *goneCluster) fail() error {
	return errors.New("dial tcp 10.0.0.1:6443: connect: no route to host: " + ports.ErrUnreachable.Error())
}

// err reports the failure for the current attempt, counting version reads as
// the start of an assessment because it is the first thing assess() runs.
func (g *goneCluster) err(counts bool) error {
	if counts {
		g.assessments.Add(1)
	}
	if g.recoverAfter > 0 && g.assessments.Load() >= g.recoverAfter {
		return nil
	}
	return wrapUnreachable(g.fail())
}

func wrapUnreachable(cause error) error {
	return errors.Join(ports.ErrUnreachable, cause)
}

func (g *goneCluster) ServerVersion(context.Context, domain.ClusterID) (domain.ServerVersion, error) {
	if err := g.err(true); err != nil {
		return domain.ServerVersion{}, err
	}
	return domain.ServerVersion{}, nil
}

func (g *goneCluster) ListNamespaces(context.Context, domain.ClusterID) ([]domain.Namespace, error) {
	return nil, g.err(false)
}

func (g *goneCluster) ListNodes(context.Context, domain.ClusterID) ([]domain.Node, error) {
	return nil, g.err(false)
}

func (g *goneCluster) ListPersistentVolumes(context.Context, domain.ClusterID) ([]domain.PersistentVolume, error) {
	return nil, g.err(false)
}

func (g *goneCluster) ListPersistentVolumeClaims(context.Context, domain.ClusterID, domain.NamespaceName) ([]domain.PersistentVolumeClaim, error) {
	return nil, g.err(false)
}

func (g *goneCluster) DiscoverCustomKinds(context.Context, domain.ClusterID) ([]domain.ResourceKind, error) {
	return nil, g.err(false)
}

func (g *goneCluster) ListPods(context.Context, domain.ClusterID, domain.NamespaceName) ([]domain.Pod, error) {
	return nil, g.err(false)
}

func (g *goneCluster) ListWorkloads(context.Context, domain.ClusterID, domain.WorkloadKind, domain.NamespaceName) ([]domain.Workload, error) {
	return nil, g.err(false)
}

func (g *goneCluster) ListPodsForWorkload(context.Context, domain.ClusterID, domain.NamespaceName, domain.WorkloadKind, string) ([]domain.Pod, error) {
	return nil, g.err(false)
}

func (g *goneCluster) ListEvents(context.Context, domain.ClusterID, domain.NamespaceName) ([]domain.Event, error) {
	return nil, g.err(false)
}

func (g *goneCluster) ListEventsForResource(context.Context, domain.ClusterID, domain.NamespaceName, string, string) ([]domain.Event, error) {
	return nil, g.err(false)
}

func (g *goneCluster) PodMetrics(context.Context, domain.ClusterID, domain.NamespaceName) (map[string]domain.PodUsage, error) {
	return nil, g.err(false)
}

func (g *goneCluster) NodeMetrics(context.Context, domain.ClusterID) (map[string]domain.Metrics, error) {
	return nil, g.err(false)
}

func (g *goneCluster) PodGraphSources(context.Context, domain.ClusterID, domain.NamespaceName, string) (domain.GraphInput, error) {
	return domain.GraphInput{}, g.err(false)
}

func (g *goneCluster) ListPodsOnNode(context.Context, domain.ClusterID, string) ([]domain.Pod, error) {
	return nil, g.err(false)
}

func (g *goneCluster) NodeFilesystems(context.Context, domain.ClusterID) (map[string]domain.NodeFilesystems, error) {
	return nil, g.err(false)
}

func (g *goneCluster) DiscoverMetricsBackend(context.Context, domain.ClusterID) (domain.MetricsBackend, error) {
	return domain.MetricsBackend{}, g.err(false)
}

func goneService(t *testing.T, cluster *goneCluster) *application.OverviewService {
	t.Helper()

	registry := application.NewRegistry()
	service, err := application.NewOverviewService(application.OverviewServiceDeps{
		Cluster:   cluster,
		Workloads: cluster,
		Events:    cluster,
		Metrics:   cluster,
		Registry:  registry,
	})
	if err != nil {
		t.Fatalf("NewOverviewService() error = %v", err)
	}

	// The cluster was connected and then went away, which is the situation
	// being tested — not one that was never reachable.
	registry.Open(mustCluster(t, "dev", true))
	return service
}

// THE BUG THIS FIXES. With the VPN disconnected every read failed, the
// assessment degraded around all of them at once, and the dashboard rendered a
// green "No problems found" over a cluster it could not reach.
//
// An assessment that read nothing is not an assessment. It must be an error.
func TestAnUnreachableClusterIsAnErrorNotAnEmptyOverview(t *testing.T) {
	t.Parallel()

	service := goneService(t, &goneCluster{})

	overview, err := service.Overview(context.Background(), domain.ClusterID("dev"))
	if err == nil {
		t.Fatalf("unreachable cluster returned no error; health = %q, findings = %d",
			overview.Health, len(overview.Findings))
	}
	if !errors.Is(err, ports.ErrUnreachable) {
		t.Errorf("error = %v, want one wrapping ErrUnreachable so the UI can say the cluster is gone", err)
	}
}

// Three attempts before giving up: the failure being guarded against is a blip
// — a wifi handover, a VPN renegotiating — and one dropped packet should not
// empty the dashboard.
func TestUnreachableIsRetriedBeforeGivingUp(t *testing.T) {
	t.Parallel()

	cluster := &goneCluster{}
	service := goneService(t, cluster)

	if _, err := service.Overview(context.Background(), domain.ClusterID("dev")); err == nil {
		t.Fatal("expected an error")
	}

	if got := cluster.assessments.Load(); got != 3 {
		t.Errorf("assessed %d times, want 3 before giving up", got)
	}
}

// ... and a cluster that comes back within those attempts is simply assessed,
// with no error and no flicker in the UI.
func TestAClusterThatComesBackIsAssessedNormally(t *testing.T) {
	t.Parallel()

	cluster := &goneCluster{recoverAfter: 2}
	service := goneService(t, cluster)

	if _, err := service.Overview(context.Background(), domain.ClusterID("dev")); err != nil {
		t.Fatalf("cluster recovered on the second attempt but the assessment failed: %v", err)
	}
	if got := cluster.assessments.Load(); got != 2 {
		t.Errorf("assessed %d times, want 2 — it should stop as soon as the cluster answers", got)
	}
}

// The retry budget must stay well inside a refresh interval; the point is to
// ride out a blip, not to wait out an outage.
func TestGivingUpIsFast(t *testing.T) {
	t.Parallel()

	service := goneService(t, &goneCluster{})

	start := time.Now()
	if _, err := service.Overview(context.Background(), domain.ClusterID("dev")); err == nil {
		t.Fatal("expected an error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("gave up after %s; the operator is waiting on this refresh", elapsed)
	}
}

// A cancelled request is the operator navigating away, not an outage, and must
// not be retried into a multi-second wait.
func TestCancellationIsNotRetried(t *testing.T) {
	t.Parallel()

	service := goneService(t, &goneCluster{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	if _, err := service.Overview(ctx, domain.ClusterID("dev")); err == nil {
		t.Fatal("expected an error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("a cancelled request took %s; it should abandon immediately", elapsed)
	}
}
