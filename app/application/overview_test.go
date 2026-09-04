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
		APIs:      kubernetes,
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
		APIs:      shared,
		Registry:  application.NewRegistry(),
	}

	tests := map[string]func(*application.OverviewServiceDeps){
		"no cluster port":  func(d *application.OverviewServiceDeps) { d.Cluster = nil },
		"no workload port": func(d *application.OverviewServiceDeps) { d.Workloads = nil },
		"no event port":    func(d *application.OverviewServiceDeps) { d.Events = nil },
		"no metrics port":  func(d *application.OverviewServiceDeps) { d.Metrics = nil },
		"no API inspector": func(d *application.OverviewServiceDeps) { d.APIs = nil },
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

// findingByID returns the finding with the given ID.
func findingByID(findings []domain.Finding, id string) (domain.Finding, bool) {
	for _, finding := range findings {
		if finding.ID == id {
			return finding, true
		}
	}
	return domain.Finding{}, false
}

// flowSchemaV1Beta3 is the entry used throughout the tests below:
// flowcontrol.apiserver.k8s.io/v1beta3 FlowSchema — deprecated since 1.29,
// removed at 1.32, replaced by flowcontrol.apiserver.k8s.io/v1.
var flowSchemaV1Beta3 = domain.Deprecation{
	Group: "flowcontrol.apiserver.k8s.io", Version: "v1beta3", Kind: "FlowSchema", Resource: "flowschemas",
	DeprecatedIn: "1.29", RemovedIn: "1.32", ReplacedBy: "flowcontrol.apiserver.k8s.io/v1",
}

// This exercises the whole upgrade-impact wiring end to end — served
// group/versions coming from discovery the way ServedAPIs reports them, and
// writers coming from the same bounded APIWriters scan every candidate goes
// through — rather than only the pure function in the domain package.
func TestOverviewIncludesUpgradeImpactFindings(t *testing.T) {
	t.Parallel()

	kubernetes := &fakeKubernetes{
		version:    domain.ServerVersion{GitVersion: "v1.31.0", Major: "1", Minor: "31"},
		servedAPIs: []domain.APIGroupVersion{{Group: flowSchemaV1Beta3.Group, Version: flowSchemaV1Beta3.Version}},
		apiUsage: map[string]domain.APIUsage{
			flowSchemaV1Beta3.ResourceKind().ID(): {
				Writers: []domain.APIWriter{{Manager: "helm", Name: "my-schema"}},
			},
		},
	}

	registry := application.NewRegistry()
	service, err := application.NewOverviewService(application.OverviewServiceDeps{
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
	registry.Open(mustCluster(t, "dev", true))

	// Default target: the next minor after 1.31 is 1.32, exactly where
	// flowcontrol.apiserver.k8s.io/v1beta3 stops being served.
	overview, err := service.Overview(context.Background(), "dev")
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}

	wantID := "upgrade:" + flowSchemaV1Beta3.ResourceKind().ID()
	finding, ok := findingByID(overview.Findings, wantID)
	if !ok {
		t.Fatalf("findings = %v, want %q", titles(overview.Findings), wantID)
	}
	if finding.Severity != domain.SeverityCritical {
		t.Errorf("severity = %q, want critical", finding.Severity)
	}
	if len(finding.Subjects) != 1 || finding.Subjects[0].Name != "my-schema" {
		t.Errorf("subjects = %v, want the one writer's name", finding.Subjects)
	}
	if overview.Upgrade.TargetMinor != "1.32" {
		t.Errorf("upgrade target = %q, want 1.32", overview.Upgrade.TargetMinor)
	}
	// 2, not 1: the table's v1beta3 entry for this group/version also names
	// PriorityLevelConfiguration, which the served group/version matches
	// too — its usage was never configured, so it comes back as its own
	// "usage not checked" finding alongside the one asserted above.
	if overview.Upgrade.Count != 2 {
		t.Errorf("upgrade count = %d, want 2", overview.Upgrade.Count)
	}

	// OverviewForTarget bypasses the shared cache, so this is a fresh
	// assessment against an explicit target that does not reach the removal
	// (1.31 is where the cluster already is). The group/version has been
	// deprecated since 1.29, which is already behind current, so it is
	// still reported — as an info-level heads-up rather than the critical
	// break above.
	before, err := service.OverviewForTarget(context.Background(), "dev", "1.31")
	if err != nil {
		t.Fatalf("OverviewForTarget() error = %v", err)
	}
	beforeFinding, ok := findingByID(before.Findings, wantID)
	if !ok {
		t.Fatalf("findings = %v, want %q even short of the removal", titles(before.Findings), wantID)
	}
	if beforeFinding.Severity != domain.SeverityInfo {
		t.Errorf("severity = %q, want info: this target does not reach the removal", beforeFinding.Severity)
	}
	if before.Upgrade.TargetMinor != "1.31" {
		t.Errorf("upgrade target = %q, want the explicit 1.31", before.Upgrade.TargetMinor)
	}
}

// A discovery failure must leave the overview unassessed, not assessed and
// clean: TargetMinor == "" is how the UI tells "not assessed" from "assessed,
// found nothing", and conflating the two would show a green upgrade badge on
// a cluster whose served APIs could not even be read.
func TestOverviewWithServedAPIsFailureLeavesUpgradeUnassessed(t *testing.T) {
	t.Parallel()

	kubernetes := &fakeKubernetes{
		version:       domain.ServerVersion{GitVersion: "v1.31.0", Major: "1", Minor: "31"},
		servedAPIsErr: errors.New("discovery unavailable"),
	}

	registry := application.NewRegistry()
	service, err := application.NewOverviewService(application.OverviewServiceDeps{
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
	registry.Open(mustCluster(t, "dev", true))

	overview, err := service.Overview(context.Background(), "dev")
	if err != nil {
		t.Fatalf("Overview() error = %v; a discovery failure must never fail the assessment", err)
	}

	if overview.Upgrade.TargetMinor != "" {
		t.Errorf("upgrade target = %q, want empty: served APIs could not be read", overview.Upgrade.TargetMinor)
	}
	for _, finding := range overview.Findings {
		if finding.Category == domain.CategoryFindingUpgrade {
			t.Errorf("findings = %v, want no Upgrade-category finding when served APIs are unknown", overview.Findings)
		}
	}
}

// A writer scan failing for one candidate must degrade to "usage not
// checked" rather than failing the whole assessment or silently reporting
// zero writers, which would read as "nothing writes through this" when the
// truth is "PodSteer could not find out".
func TestOverviewWithAPIWritersFailureReportsUsageNotChecked(t *testing.T) {
	t.Parallel()

	kubernetes := &fakeKubernetes{
		version:    domain.ServerVersion{GitVersion: "v1.31.0", Major: "1", Minor: "31"},
		servedAPIs: []domain.APIGroupVersion{{Group: flowSchemaV1Beta3.Group, Version: flowSchemaV1Beta3.Version}},
		apiUsageErr: map[string]error{
			flowSchemaV1Beta3.ResourceKind().ID(): errors.New("forbidden"),
		},
	}

	registry := application.NewRegistry()
	service, err := application.NewOverviewService(application.OverviewServiceDeps{
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
	registry.Open(mustCluster(t, "dev", true))

	overview, err := service.Overview(context.Background(), "dev")
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}

	wantID := "upgrade:" + flowSchemaV1Beta3.ResourceKind().ID()
	finding, ok := findingByID(overview.Findings, wantID)
	if !ok {
		t.Fatalf("findings = %v, want %q", titles(overview.Findings), wantID)
	}
	if finding.Severity != domain.SeverityWarning {
		t.Errorf("severity = %q, want warning", finding.Severity)
	}
	if len(finding.Subjects) != 1 || finding.Subjects[0].Detail != "usage not checked" {
		t.Errorf("subjects = %v, want a single 'usage not checked' subject", finding.Subjects)
	}
}

// titles lists finding titles, for a readable assertion failure.
func titles(findings []domain.Finding) []string {
	out := make([]string, len(findings))
	for i, finding := range findings {
		out[i] = finding.Title
	}
	return out
}
