package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// newPodSpec returns a valid spec that individual tests mutate, so each case
// states only what it is actually exercising.
func newPodSpec() domain.PodSpec {
	return domain.PodSpec{
		Name:      "api-7d9f",
		Namespace: "default",
		ClusterID: "kind-dev",
		Phase:     domain.PodPhaseRunning,
	}
}

func TestNewPodValidatesInvariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*domain.PodSpec)
		wantErr error
	}{
		{
			name:   "valid spec",
			mutate: func(*domain.PodSpec) {},
		},
		{
			name:    "blank name",
			mutate:  func(s *domain.PodSpec) { s.Name = "   " },
			wantErr: domain.ErrEmptyPodName,
		},
		{
			name:    "no namespace",
			mutate:  func(s *domain.PodSpec) { s.Namespace = domain.NamespaceAll },
			wantErr: domain.ErrInvalidNamespaceName,
		},
		{
			name:    "no cluster",
			mutate:  func(s *domain.PodSpec) { s.ClusterID = "" },
			wantErr: domain.ErrEmptyClusterID,
		},
		{
			name: "container without a name",
			mutate: func(s *domain.PodSpec) {
				s.Containers = []domain.Container{{Image: "nginx:1.27"}}
			},
			wantErr: domain.ErrEmptyContainerName,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			spec := newPodSpec()
			test.mutate(&spec)

			_, err := domain.NewPod(spec)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("NewPod() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestNewPodTrimsNameAndDefaultsPhase(t *testing.T) {
	t.Parallel()

	spec := newPodSpec()
	spec.Name = "  api-7d9f  "
	spec.Phase = ""

	pod, err := domain.NewPod(spec)
	if err != nil {
		t.Fatalf("NewPod() error = %v", err)
	}
	if got := pod.Name(); got != "api-7d9f" {
		t.Errorf("Name() = %q, want %q", got, "api-7d9f")
	}
	if got := pod.Phase(); got != domain.PodPhaseUnknown {
		t.Errorf("Phase() = %q, want %q", got, domain.PodPhaseUnknown)
	}
}

// A caller must not be able to mutate a pod after constructing it, because
// adapters build pods from slices they reuse while translating a list.
func TestNewPodCopiesContainersAndLabels(t *testing.T) {
	t.Parallel()

	spec := newPodSpec()
	spec.Containers = []domain.Container{{Name: "app", Image: "nginx:1.27"}}
	spec.Labels = map[string]string{"app": "api"}

	pod, err := domain.NewPod(spec)
	if err != nil {
		t.Fatalf("NewPod() error = %v", err)
	}

	spec.Containers[0].Image = "mutated"
	spec.Labels["app"] = "mutated"

	if got := pod.Containers()[0].Image; got != "nginx:1.27" {
		t.Errorf("container image was mutated through the spec: got %q", got)
	}
	if got := pod.Labels()["app"]; got != "api" {
		t.Errorf("label was mutated through the spec: got %q", got)
	}

	// The accessors must hand out copies too.
	pod.Containers()[0].Image = "mutated"
	pod.Labels()["app"] = "mutated"

	if got := pod.Containers()[0].Image; got != "nginx:1.27" {
		t.Errorf("container image was mutated through the accessor: got %q", got)
	}
	if got := pod.Labels()["app"]; got != "api" {
		t.Errorf("label was mutated through the accessor: got %q", got)
	}
}

func TestPodDerivedStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		phase          domain.PodPhase
		containers     []domain.Container
		wantReady      int
		wantRestarts   int32
		wantHealthy    bool
		wantStatusText string
	}{
		{
			name:  "all containers serving",
			phase: domain.PodPhaseRunning,
			containers: []domain.Container{
				{Name: "app", Ready: true, State: domain.ContainerStateRunning},
				{Name: "sidecar", Ready: true, State: domain.ContainerStateRunning},
			},
			wantReady:   2,
			wantHealthy: true,
		},
		{
			// The case a naive client gets wrong: phase says Running, so the
			// pod looks fine, but nothing is actually serving.
			name:  "running but crash looping",
			phase: domain.PodPhaseRunning,
			containers: []domain.Container{
				{Name: "app", Ready: false, RestartCount: 7,
					State: domain.ContainerStateWaiting, Reason: "CrashLoopBackOff"},
			},
			wantReady:      0,
			wantRestarts:   7,
			wantHealthy:    false,
			wantStatusText: "CrashLoopBackOff",
		},
		{
			name:  "completed job is healthy and unremarkable",
			phase: domain.PodPhaseSucceeded,
			containers: []domain.Container{
				{Name: "job", Ready: false, State: domain.ContainerStateTerminated, Reason: "Completed"},
			},
			wantHealthy: true,
		},
		{
			name:  "restarts accumulate across containers",
			phase: domain.PodPhaseRunning,
			containers: []domain.Container{
				{Name: "app", Ready: true, RestartCount: 2, State: domain.ContainerStateRunning},
				{Name: "sidecar", Ready: true, RestartCount: 3, State: domain.ContainerStateRunning},
			},
			wantReady:    2,
			wantRestarts: 5,
			wantHealthy:  true,
		},
		{
			name:        "a pod with no containers is not ready",
			phase:       domain.PodPhaseRunning,
			wantHealthy: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			spec := newPodSpec()
			spec.Phase = test.phase
			spec.Containers = test.containers

			pod, err := domain.NewPod(spec)
			if err != nil {
				t.Fatalf("NewPod() error = %v", err)
			}

			if got := pod.ReadyContainers(); got != test.wantReady {
				t.Errorf("ReadyContainers() = %d, want %d", got, test.wantReady)
			}
			if got := pod.RestartCount(); got != test.wantRestarts {
				t.Errorf("RestartCount() = %d, want %d", got, test.wantRestarts)
			}
			if got := pod.IsHealthy(); got != test.wantHealthy {
				t.Errorf("IsHealthy() = %v, want %v", got, test.wantHealthy)
			}
			if got := pod.StatusReason(); got != test.wantStatusText {
				t.Errorf("StatusReason() = %q, want %q", got, test.wantStatusText)
			}
		})
	}
}

func TestNewPodPhaseFallsBackToUnknown(t *testing.T) {
	t.Parallel()

	tests := map[string]domain.PodPhase{
		"Running":        domain.PodPhaseRunning,
		"Pending":        domain.PodPhasePending,
		"Succeeded":      domain.PodPhaseSucceeded,
		"Failed":         domain.PodPhaseFailed,
		"Terminating":    domain.PodPhaseTerminating,
		"  Running  ":    domain.PodPhaseRunning,
		"":               domain.PodPhaseUnknown,
		"SomeFuturePhas": domain.PodPhaseUnknown,
	}

	for raw, want := range tests {
		if got := domain.NewPodPhase(raw); got != want {
			t.Errorf("NewPodPhase(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestPodAgeUsesInjectedReferenceTime(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	spec := newPodSpec()
	spec.CreatedAt = created

	pod, err := domain.NewPod(spec)
	if err != nil {
		t.Fatalf("NewPod() error = %v", err)
	}

	if got := pod.Age(created.Add(90 * time.Minute)); got != 90*time.Minute {
		t.Errorf("Age() = %v, want %v", got, 90*time.Minute)
	}

	// An object with no creation timestamp has no meaningful age, and must not
	// report the time since the zero date.
	unset, err := domain.NewPod(newPodSpec())
	if err != nil {
		t.Fatalf("NewPod() error = %v", err)
	}
	if got := unset.Age(created); got != 0 {
		t.Errorf("Age() with no timestamp = %v, want 0", got)
	}
}

func TestPodPercentagesMeasureUsageAgainstRequests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		containers []domain.Container
		usage      domain.Metrics
		wantCPU    float64
		wantMemory float64
	}{
		{
			name: "usage as a share of what was reserved",
			containers: []domain.Container{
				{Name: "app", Requests: domain.Resources{CPUMilli: 200, MemoryBytes: 512 << 20}},
			},
			usage:      domain.NewMetrics(50, 128<<20),
			wantCPU:    25,
			wantMemory: 25,
		},
		{
			// Requests are summed across containers, so a sidecar's
			// reservation counts towards the pod's denominator. Dividing by
			// the first container's request alone would overstate every
			// multi-container pod, which on a service-mesh cluster is all of
			// them.
			name: "requests are summed across containers",
			containers: []domain.Container{
				{Name: "app", Requests: domain.Resources{CPUMilli: 300, MemoryBytes: 256 << 20}},
				{Name: "proxy", Requests: domain.Resources{CPUMilli: 100, MemoryBytes: 256 << 20}},
			},
			usage:      domain.NewMetrics(200, 256<<20),
			wantCPU:    50,
			wantMemory: 50,
		},
		{
			// A REQUEST IS NOT A CEILING. A Burstable pod is entitled to climb
			// above what it reserved, so the figure must be allowed past 100
			// rather than being clamped into looking comfortable.
			name: "bursting above the reservation exceeds 100",
			containers: []domain.Container{
				{Name: "app", Requests: domain.Resources{CPUMilli: 100, MemoryBytes: 100 << 20}},
			},
			usage:      domain.NewMetrics(350, 200<<20),
			wantCPU:    350,
			wantMemory: 200,
		},
		{
			// BestEffort: nothing was reserved, so there is no denominator.
			// Zero here means "no proportion exists", and callers are expected
			// to test the request rather than read this as idleness.
			name: "no requests declared yields zero",
			containers: []domain.Container{
				{Name: "app"},
			},
			usage:      domain.NewMetrics(50, 128<<20),
			wantCPU:    0,
			wantMemory: 0,
		},
		{
			// The other zero: a reservation exists but nothing measured the
			// pod. Reporting a proportion here would invent one.
			name: "unmeasured usage yields zero",
			containers: []domain.Container{
				{Name: "app", Requests: domain.Resources{CPUMilli: 200, MemoryBytes: 512 << 20}},
			},
			usage:      domain.Metrics{},
			wantCPU:    0,
			wantMemory: 0,
		},
		{
			// A measured zero is a real reading and must survive: an idle pod
			// that did reserve capacity is at 0% of it, which is worth seeing.
			name: "measured zero is a real zero",
			containers: []domain.Container{
				{Name: "app", Requests: domain.Resources{CPUMilli: 200, MemoryBytes: 512 << 20}},
			},
			usage:      domain.NewMetrics(0, 0),
			wantCPU:    0,
			wantMemory: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			spec := newPodSpec()
			spec.Containers = test.containers

			pod, err := domain.NewPod(spec)
			if err != nil {
				t.Fatalf("NewPod() error = %v", err)
			}
			pod = pod.WithUsage(test.usage)

			if got := pod.CPUPercent(); got != test.wantCPU {
				t.Errorf("CPUPercent() = %v, want %v", got, test.wantCPU)
			}
			if got := pod.MemoryPercent(); got != test.wantMemory {
				t.Errorf("MemoryPercent() = %v, want %v", got, test.wantMemory)
			}
		})
	}
}

func TestPodLimitPercentagesMeasureUsageAgainstLimits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		containers []domain.Container
		usage      domain.Metrics
		wantCPU    float64
		wantMemory float64
	}{
		{
			name: "usage as a share of the ceiling",
			containers: []domain.Container{
				{Name: "app", Limits: domain.Resources{CPUMilli: 500, MemoryBytes: 512 << 20}},
			},
			usage:      domain.NewMetrics(250, 384<<20),
			wantCPU:    50,
			wantMemory: 75,
		},
		{
			// The common real shape: a memory limit set, CPU left unbounded
			// on purpose. The two must be independent, or a pod with one of
			// them would report a ratio against a ceiling it does not have.
			name: "memory limited, CPU unbounded",
			containers: []domain.Container{
				{Name: "app", Limits: domain.Resources{MemoryBytes: 200 << 20}},
			},
			usage:      domain.NewMetrics(300, 180<<20),
			wantCPU:    0,
			wantMemory: 90,
		},
		{
			name: "limits are summed across containers",
			containers: []domain.Container{
				{Name: "app", Limits: domain.Resources{CPUMilli: 400, MemoryBytes: 300 << 20}},
				{Name: "proxy", Limits: domain.Resources{CPUMilli: 100, MemoryBytes: 100 << 20}},
			},
			usage:      domain.NewMetrics(250, 200<<20),
			wantCPU:    50,
			wantMemory: 50,
		},
		{
			name: "unmeasured usage yields zero",
			containers: []domain.Container{
				{Name: "app", Limits: domain.Resources{CPUMilli: 500, MemoryBytes: 512 << 20}},
			},
			usage:      domain.Metrics{},
			wantCPU:    0,
			wantMemory: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			spec := newPodSpec()
			spec.Containers = test.containers

			pod, err := domain.NewPod(spec)
			if err != nil {
				t.Fatalf("NewPod() error = %v", err)
			}
			pod = pod.WithUsage(test.usage)

			if got := pod.CPULimitPercent(); got != test.wantCPU {
				t.Errorf("CPULimitPercent() = %v, want %v", got, test.wantCPU)
			}
			if got := pod.MemoryLimitPercent(); got != test.wantMemory {
				t.Errorf("MemoryLimitPercent() = %v, want %v", got, test.wantMemory)
			}
		})
	}
}
