package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

func TestNewNamespaceName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		want    domain.NamespaceName
		wantErr error
	}{
		{name: "simple", raw: "default", want: "default"},
		{name: "with hyphens", raw: "kube-system", want: "kube-system"},
		{name: "digits", raw: "team-42", want: "team-42"},
		// A blank name is the cross-namespace query, not a validation failure.
		{name: "blank means all", raw: "", want: domain.NamespaceAll},
		{name: "whitespace means all", raw: "   ", want: domain.NamespaceAll},
		{name: "uppercase", raw: "Default", wantErr: domain.ErrInvalidNamespaceName},
		{name: "leading hyphen", raw: "-system", wantErr: domain.ErrInvalidNamespaceName},
		{name: "trailing hyphen", raw: "system-", wantErr: domain.ErrInvalidNamespaceName},
		{name: "underscore", raw: "kube_system", wantErr: domain.ErrInvalidNamespaceName},
		{name: "too long", raw: strings.Repeat("a", 64), wantErr: domain.ErrInvalidNamespaceName},
		{name: "at the limit", raw: strings.Repeat("a", 63), want: domain.NamespaceName(strings.Repeat("a", 63))},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := domain.NewNamespaceName(test.raw)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("NewNamespaceName(%q) error = %v, want %v", test.raw, err, test.wantErr)
			}
			if test.wantErr == nil && got != test.want {
				t.Errorf("NewNamespaceName(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

func TestNamespaceNameHelpers(t *testing.T) {
	t.Parallel()

	if !domain.NamespaceAll.IsAll() {
		t.Error("NamespaceAll.IsAll() = false, want true")
	}
	if domain.NamespaceAll.String() != "" {
		t.Error("NamespaceAll must render as the empty string the list APIs expect")
	}
	if got := domain.NamespaceAll.OrDefault(); got != domain.NamespaceDefault {
		t.Errorf("OrDefault() = %q, want %q", got, domain.NamespaceDefault)
	}
	if got := domain.NamespaceName("platform").OrDefault(); got != "platform" {
		t.Errorf("OrDefault() = %q, want %q", got, "platform")
	}
}

// A namespace object always has a concrete name; only a *query* may be blank.
func TestNewNamespaceRejectsBlankName(t *testing.T) {
	t.Parallel()

	if _, err := domain.NewNamespace("", domain.NamespacePhaseActive, time.Time{}); !errors.Is(err, domain.ErrInvalidNamespaceName) {
		t.Errorf("NewNamespace(\"\") error = %v, want %v", err, domain.ErrInvalidNamespaceName)
	}
}

func TestNewNamespacePhaseFallsBackToUnknown(t *testing.T) {
	t.Parallel()

	tests := map[string]domain.NamespacePhase{
		"Active":      domain.NamespacePhaseActive,
		"Terminating": domain.NamespacePhaseTerminating,
		"  Active  ":  domain.NamespacePhaseActive,
		"":            domain.NamespacePhaseUnknown,
		"Draining":    domain.NamespacePhaseUnknown,
	}

	for raw, want := range tests {
		if got := domain.NewNamespacePhase(raw); got != want {
			t.Errorf("NewNamespacePhase(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestEveryNamespaceGetsARowIncludingTheEmptyOnes(t *testing.T) {
	// "This namespace is empty" is one of the more useful things a namespace
	// list can say, and building the rows from the pods instead of from the
	// namespaces silently drops it.
	namespaces := []domain.Namespace{
		mustNamespace(t, "web", domain.NamespacePhaseActive),
		mustNamespace(t, "abandoned", domain.NamespacePhaseActive),
	}

	summaries := domain.NewNamespaceSummaries(namespaces, []domain.Pod{
		mustSummaryPod(t, "web", "api-1", domain.PodPhaseRunning),
	}, true)

	if len(summaries) != 2 {
		t.Fatalf("summarised %d namespaces, want 2", len(summaries))
	}
	// Alphabetical: a namespace list is read by looking one up.
	if summaries[0].Namespace.Name() != "abandoned" {
		t.Fatalf("led with %q, want abandoned", summaries[0].Namespace.Name())
	}
	if !summaries[0].IsEmpty() {
		t.Fatalf("abandoned reported %d pods, want none", summaries[0].Pods())
	}
	if summaries[1].Pods() != 1 {
		t.Fatalf("web reported %d pods, want 1", summaries[1].Pods())
	}
}

func TestCompletedPodsAreCountedButReserveNothing(t *testing.T) {
	// THE TWO HALVES THIS GETS RIGHT SEPARATELY. A Succeeded pod is still in
	// the pod list, so the count has to include it or the row disagrees with
	// the page it links to — and it has given its reservation back, so
	// including it in the requests would report capacity nobody can reclaim.
	namespaces := []domain.Namespace{mustNamespace(t, "batch", domain.NamespacePhaseActive)}

	summaries := domain.NewNamespaceSummaries(namespaces, []domain.Pod{
		mustSummaryPod(t, "batch", "nightly-1", domain.PodPhaseSucceeded),
		mustSummaryPod(t, "batch", "nightly-2", domain.PodPhaseRunning),
	}, true)

	if summaries[0].Usage.Pods != 2 {
		t.Fatalf("counted %d pods, want both", summaries[0].Usage.Pods)
	}
	if summaries[0].Usage.Requests.CPUMilli != 100 {
		t.Fatalf("CPURequests = %d, want only the running pod's 100", summaries[0].Usage.Requests.CPUMilli)
	}
}

func TestAPodInAnInvisibleNamespaceIsNotInvented(t *testing.T) {
	// An account may list pods across the cluster and not list namespaces.
	// Inventing a row would put a namespace on screen that nothing else in
	// the application knows about.
	namespaces := []domain.Namespace{mustNamespace(t, "web", domain.NamespacePhaseActive)}

	summaries := domain.NewNamespaceSummaries(namespaces, []domain.Pod{
		mustSummaryPod(t, "web", "api-1", domain.PodPhaseRunning),
		mustSummaryPod(t, "kube-system", "coredns-1", domain.PodPhaseRunning),
	}, true)

	if len(summaries) != 1 {
		t.Fatalf("summarised %d namespaces, want only the visible one", len(summaries))
	}
	if summaries[0].Usage.Pods != 1 {
		t.Fatalf("counted %d pods, want only the one in a known namespace", summaries[0].Pods())
	}
}

// mustNamespace builds a namespace or fails the test.
func mustNamespace(t *testing.T, name string, phase domain.NamespacePhase) domain.Namespace {
	t.Helper()

	namespace, err := domain.NewNamespace(name, phase, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("building namespace %q: %v", name, err)
	}
	return namespace
}

// mustSummaryPod builds a pod reserving 100m of CPU, or fails the test.
func mustSummaryPod(t *testing.T, namespace, name string, phase domain.PodPhase) domain.Pod {
	t.Helper()

	pod, err := domain.NewPod(domain.PodSpec{
		Name:      name,
		Namespace: domain.NamespaceName(namespace),
		ClusterID: "dev",
		Phase:     phase,
		// Scheduled, because a pod only occupies a node once it is placed on
		// one — which is half of what decides whether its requests count.
		NodeName: "node-1",
		Containers: []domain.Container{
			{Name: "app", Requests: domain.Resources{CPUMilli: 100}},
		},
	})
	if err != nil {
		t.Fatalf("building pod %q: %v", name, err)
	}
	return pod
}
