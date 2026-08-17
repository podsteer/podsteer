package domain_test

import (
	"errors"
	"testing"
	"time"

	"k8sense/app/domain"
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
