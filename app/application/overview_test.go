package application_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// newOverviewService wires a service around the given fakes.
func newOverviewService(
	t *testing.T,
	kubernetes *fakeKubernetes,
	events *fakeEvents,
) (*application.OverviewService, *application.Registry) {
	t.Helper()

	registry := application.NewRegistry()
	service, err := application.NewOverviewService(application.OverviewServiceDeps{
		Cluster:   kubernetes,
		Workloads: kubernetes,
		Events:    events,
		Metrics:   kubernetes,
		Registry:  registry,
	})
	if err != nil {
		t.Fatalf("NewOverviewService() error = %v", err)
	}
	return service, registry
}

func TestNewOverviewServiceRejectsMissingDependencies(t *testing.T) {
	t.Parallel()

	shared := &fakeKubernetes{}
	full := application.OverviewServiceDeps{
		Cluster:   shared,
		Workloads: shared,
		Events:    &fakeEvents{},
		Metrics:   shared,
		Registry:  application.NewRegistry(),
	}

	tests := map[string]func(*application.OverviewServiceDeps){
		"no cluster port":  func(d *application.OverviewServiceDeps) { d.Cluster = nil },
		"no workload port": func(d *application.OverviewServiceDeps) { d.Workloads = nil },
		"no event port":    func(d *application.OverviewServiceDeps) { d.Events = nil },
		"no metrics port":  func(d *application.OverviewServiceDeps) { d.Metrics = nil },
		"no registry":      func(d *application.OverviewServiceDeps) { d.Registry = nil },
	}

	for name, break_ := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			deps := full
			break_(&deps)
			if _, err := application.NewOverviewService(deps); err == nil {
				t.Error("expected an error for an incompletely wired service")
			}
		})
	}
}

// An unconnected cluster is the one failure the overview does return, because
// there is nothing to assess.
func TestOverviewRequiresAConnectedCluster(t *testing.T) {
	t.Parallel()

	service, _ := newOverviewService(t, &fakeKubernetes{}, &fakeEvents{})

	if _, err := service.Overview(context.Background(), "dev"); err == nil {
		t.Fatal("expected an error for a cluster that is not connected")
	}
}

// The whole point of assessing sources independently: an operator opens this
// screen because something is wrong, so a missing source must not replace the
// assessment with an error page.
func TestOverviewSurvivesUnreadableSources(t *testing.T) {
	t.Parallel()

	kubernetes := &fakeKubernetes{
		version: domain.ServerVersion{GitVersion: "v1.32.7", Major: "1", Minor: "32"},
		pods: []domain.Pod{
			mustPod(t, "default", "api-1"),
		},
		// No podUsage or nodeUsage, so the metrics port reports itself
		// unavailable — the ordinary state of a cluster without
		// metrics-server.
	}
	events := &fakeEvents{err: errors.New("events forbidden")}

	service, registry := newOverviewService(t, kubernetes, events)
	registry.Open(mustCluster(t, "dev", true))

	overview, err := service.Overview(context.Background(), "dev")
	if err != nil {
		t.Fatalf("Overview() error = %v, want the assessment to survive", err)
	}

	if overview.Pods.Total != 1 {
		t.Errorf("pods = %d, want the readable source still counted", overview.Pods.Total)
	}
	if overview.Version.GitVersion != "v1.32.7" {
		t.Errorf("version = %q, want it read", overview.Version.GitVersion)
	}
	if !containsSource(overview.Unavailable, "events") {
		t.Errorf("unavailable = %v, want events named", overview.Unavailable)
	}
	if !containsSource(overview.Unavailable, "metrics") {
		t.Errorf("unavailable = %v, want metrics named", overview.Unavailable)
	}
	// Both metrics calls fail together on such a cluster, and the operator
	// should be told once.
	if count := countSource(overview.Unavailable, "metrics"); count != 1 {
		t.Errorf("metrics named %d times, want 1", count)
	}
}

// The overview reads controllers of every kind, including the ReplicaSets it
// never displays — they are what resolves a pod's owner to its Deployment.
func TestOverviewReadsEveryControllerKind(t *testing.T) {
	t.Parallel()

	kubernetes := &fakeKubernetes{}
	service, registry := newOverviewService(t, kubernetes, &fakeEvents{})
	registry.Open(mustCluster(t, "dev", true))

	if _, err := service.Overview(context.Background(), "dev"); err != nil {
		t.Fatalf("Overview() error = %v", err)
	}

	want := []domain.WorkloadKind{
		domain.WorkloadDeployment,
		domain.WorkloadStatefulSet,
		domain.WorkloadDaemonSet,
		domain.WorkloadReplicaSet,
		domain.WorkloadJob,
		domain.WorkloadCronJob,
	}
	requested := kubernetes.requestedKinds()
	for _, kind := range want {
		if !requested[kind] {
			t.Errorf("kind %q was never requested; without it a pod cannot be traced to its workload", kind)
		}
	}
}

var _ ports.OverviewService = (*application.OverviewService)(nil)

func containsSource(sources []string, want string) bool {
	return countSource(sources, want) > 0
}

func countSource(sources []string, want string) int {
	count := 0
	for _, source := range sources {
		if source == want {
			count++
		}
	}
	return count
}

// "No metrics" is not one situation, and the two causes need opposite advice.
//
// Telling somebody to install metrics-server when it is already running and
// merely unreadable sends them to argue with an administrator about software
// that is working perfectly well. The adapter already distinguishes these; this
// is the check that the distinction survives into something the UI can render.
func TestMetricsStatusExplainsWhyUsageIsAbsent(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		err  error
		want domain.MetricsStatus
	}{
		{
			name: "no metrics-server installed",
			err:  fmt.Errorf("listing node metrics: %w", ports.ErrMetricsUnavailable),
			want: domain.MetricsNotInstalled,
		},
		{
			name: "metrics API exists but RBAC forbids it",
			err:  fmt.Errorf("listing node metrics: %w", ports.ErrForbidden),
			want: domain.MetricsForbidden,
		},
		{
			name: "cluster unreachable",
			err:  fmt.Errorf("listing node metrics: %w", ports.ErrUnreachable),
			want: domain.MetricsFailed,
		},
		{
			name: "anything else",
			err:  errors.New("decoding response"),
			want: domain.MetricsFailed,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			kubernetes := &fakeKubernetes{
				version:    domain.ServerVersion{Major: "1", Minor: "32"},
				metricsErr: testCase.err,
			}
			service, registry := newOverviewService(t, kubernetes, &fakeEvents{})
			registry.Open(mustCluster(t, "dev", true))

			overview, err := service.Overview(context.Background(), "dev")
			if err != nil {
				t.Fatalf("Overview() error = %v; a metrics failure must never fail the assessment", err)
			}

			if overview.Metrics != testCase.want {
				t.Errorf("metrics status = %q, want %q", overview.Metrics, testCase.want)
			}
		})
	}
}

// A cluster that does serve metrics must not be told anything about them: a
// notice shown on every healthy cluster is one nobody reads on the cluster
// where it matters.
func TestMeasuredMetricsCarryNoExplanation(t *testing.T) {
	t.Parallel()

	kubernetes := &fakeKubernetes{
		version:   domain.ServerVersion{Major: "1", Minor: "32"},
		nodeUsage: map[string]domain.Metrics{"node-1": {CPUMilli: 100, MemoryBytes: 1 << 30}},
	}
	service, registry := newOverviewService(t, kubernetes, &fakeEvents{})
	registry.Open(mustCluster(t, "dev", true))

	overview, err := service.Overview(context.Background(), "dev")
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}

	if overview.Metrics != domain.MetricsMeasuredOK {
		t.Errorf("metrics status = %q, want measured", overview.Metrics)
	}
}
