package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

func container() domain.ContainerResize {
	return domain.ContainerResize{
		Name:          "app",
		CPURequest:    "500m",
		CPULimit:      "1",
		MemoryRequest: "512Mi",
		MemoryLimit:   "1Gi",
	}
}

func TestPlanResizeSendsOnlyWhatWasTyped(t *testing.T) {
	t.Parallel()

	plan, err := domain.PlanResize(container(), domain.ResizeRequest{
		Container:  "app",
		CPURequest: "750m",
	})
	if err != nil {
		t.Fatalf("PlanResize() error = %v", err)
	}

	if plan.CPURequest != "750m" {
		t.Errorf("CPURequest = %q, want the typed one", plan.CPURequest)
	}
	// EMPTY MEANS LEAVE IT ALONE, which is not the same as clearing it. A
	// plan that carried the current figures forward would rewrite three
	// fields nobody touched, and record every one of them in managedFields.
	if plan.CPULimit != "" || plan.MemoryRequest != "" || plan.MemoryLimit != "" {
		t.Errorf("plan = %+v, want the untouched figures left empty", plan)
	}
}

func TestPlanResizeRefusesAContainerThatIsNotThere(t *testing.T) {
	t.Parallel()

	_, err := domain.PlanResize(container(), domain.ResizeRequest{Container: "sidecar", CPURequest: "1"})
	if !errors.Is(err, domain.ErrResizeContainerNotFound) {
		t.Fatalf("err = %v, want ErrResizeContainerNotFound", err)
	}
}

func TestPlanResizeRefusesAQuantityThatIsNotOne(t *testing.T) {
	t.Parallel()

	_, err := domain.PlanResize(container(), domain.ResizeRequest{Container: "app", MemoryRequest: "1 gigabyte"})
	if !errors.Is(err, domain.ErrResizeQuantityInvalid) {
		t.Fatalf("err = %v, want ErrResizeQuantityInvalid", err)
	}
	// The message names WHICH field, because four of them are on screen.
	if !strings.Contains(err.Error(), "memory request") {
		t.Errorf("err = %q, want it to name the field", err)
	}
}

func TestPlanResizeRefusesALimitBelowItsRequest(t *testing.T) {
	t.Parallel()

	// The limit typed, the request left alone: the comparison is against what
	// WILL be set, not against what was typed, or this is invisible.
	_, err := domain.PlanResize(container(), domain.ResizeRequest{Container: "app", MemoryLimit: "256Mi"})
	if !errors.Is(err, domain.ErrResizeLimitBelowRequest) {
		t.Fatalf("err = %v, want ErrResizeLimitBelowRequest", err)
	}

	// And the other way round: lowering a request below an untouched limit is
	// perfectly ordinary.
	if _, err := domain.PlanResize(container(), domain.ResizeRequest{Container: "app", MemoryRequest: "256Mi"}); err != nil {
		t.Fatalf("PlanResize() error = %v, want a lower request accepted", err)
	}
}

func TestPlanResizeRefusesAChangeThatChangesNothing(t *testing.T) {
	t.Parallel()

	_, err := domain.PlanResize(container(), domain.ResizeRequest{Container: "app", CPURequest: "500m"})
	if !errors.Is(err, domain.ErrResizeNoChange) {
		t.Fatalf("err = %v, want ErrResizeNoChange", err)
	}
}

func TestPlanResizeComparesQuantitiesRatherThanText(t *testing.T) {
	t.Parallel()

	// "1024Mi" and "1Gi" are the same amount written two ways. Sent as a
	// change it writes a no-op the API server records; refused as no change
	// when the operator meant something else would be worse still — so the
	// comparison is on the VALUE.
	_, err := domain.PlanResize(container(), domain.ResizeRequest{Container: "app", MemoryLimit: "1024Mi"})
	if !errors.Is(err, domain.ErrResizeNoChange) {
		t.Fatalf("err = %v, want 1024Mi to equal 1Gi", err)
	}

	if _, err := domain.PlanResize(container(), domain.ResizeRequest{Container: "app", MemoryLimit: "2048Mi"}); err != nil {
		t.Fatalf("PlanResize() error = %v, want a real change accepted", err)
	}
}

func TestPlanResizeWarnsOnlyForTheResourceItChanges(t *testing.T) {
	t.Parallel()

	// A container that restarts for memory is NOT restarted by a CPU change,
	// and warning about it anyway teaches people to ignore the warning.
	restarts := container()
	restarts.RestartsForMemory = true

	cpu, err := domain.PlanResize(restarts, domain.ResizeRequest{Container: "app", CPURequest: "750m"})
	if err != nil {
		t.Fatalf("PlanResize() error = %v", err)
	}
	if cpu.Restarts {
		t.Error("a CPU change was reported as restarting a container whose MEMORY policy restarts")
	}

	memory, err := domain.PlanResize(restarts, domain.ResizeRequest{Container: "app", MemoryRequest: "1Gi"})
	if err != nil {
		t.Fatalf("PlanResize() error = %v", err)
	}
	if !memory.Restarts || memory.RestartReason != "memory" {
		t.Errorf("plan = %+v, want a restart named for memory", memory)
	}
}

func TestPlanResizeSaysNothingAboutRestartsWhenThePolicyDoesNot(t *testing.T) {
	t.Parallel()

	// The default is NotRequired for both, which is what makes in-place
	// resize worth having: the cgroup changes under the running process.
	plan, err := domain.PlanResize(container(), domain.ResizeRequest{Container: "app", MemoryRequest: "1Gi"})
	if err != nil {
		t.Fatalf("PlanResize() error = %v", err)
	}
	if plan.Restarts || plan.RestartReason != "" {
		t.Errorf("plan = %+v, want no restart claimed", plan)
	}
}

func TestPlanResizeAcceptsAFigureOnAContainerThatDeclaresNone(t *testing.T) {
	t.Parallel()

	// A BestEffort container has no requests at all; setting one is a change.
	bare := domain.ContainerResize{Name: "app"}

	plan, err := domain.PlanResize(bare, domain.ResizeRequest{Container: "app", CPURequest: "100m"})
	if err != nil {
		t.Fatalf("PlanResize() error = %v", err)
	}
	if plan.CPURequest != "100m" {
		t.Errorf("CPURequest = %q, want the typed one", plan.CPURequest)
	}
}
