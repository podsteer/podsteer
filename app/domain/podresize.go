package domain

import (
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"
)

// Changing a running pod's CPU and memory without recreating it.
//
// IN-PLACE RESIZE IS A WRITE TO A SUBRESOURCE, `pods/resize`, and it is the
// one write in this application whose EFFECT ON THE THING IN FRONT OF THE
// OPERATOR depends on a field they have probably never read. A container
// declares a `resizePolicy` per resource: `NotRequired` means the kubelet
// changes the cgroup under the running process, and `RestartContainer` means
// it kills and restarts the container to apply the change. Memory defaults to
// NotRequired, so most memory resizes are live — but a container that asked
// for RestartContainer gets one, and a dialog that did not say so would have
// restarted somebody's database to change a number.
//
// PodSteer already DIAGNOSES what happened to a resize — see resizeFindings,
// which tells Infeasible from Deferred because they need opposite responses.
// This is the other half: making one.
//
// WHAT THIS DOES NOT DO. It does not wait for the resize to be applied, and
// it does not report that it was: the kubelet may apply it immediately, defer
// it until the node has room, or refuse it as infeasible, and which of those
// happened is a CONDITION on the pod that the assessment already reads. A
// write that claimed success would be claiming the kubelet's answer before it
// gave one.

var (
	// ErrResizeNoChange means the requested figures are the ones already set.
	ErrResizeNoChange = errors.New("that is what the container already asks for")

	// ErrResizeContainerNotFound means the pod has no such container.
	ErrResizeContainerNotFound = errors.New("this pod has no container by that name")

	// ErrResizeQuantityInvalid means a figure is not a Kubernetes quantity.
	ErrResizeQuantityInvalid = errors.New("not a quantity Kubernetes understands")

	// ErrResizeLimitBelowRequest means the limit is smaller than the request,
	// which the API server refuses — and refusing it here says which of the
	// two fields to change, where the API server's message names a path.
	ErrResizeLimitBelowRequest = errors.New("a limit cannot be below its request")
)

// ResizeField is one of the four figures a resize can set.
type ResizeField struct {
	// Value is the quantity as typed, e.g. "500m" or "1Gi". Empty means
	// LEAVE THIS ONE ALONE — which is not the same as clearing it, and is why
	// this is a string rather than a *resource.Quantity: "" and "0" are
	// different requests, and one of them means "no opinion".
	Value string
}

// ResizeRequest is what an operator asked for, before it is checked.
type ResizeRequest struct {
	Container     string
	CPURequest    string
	CPULimit      string
	MemoryRequest string
	MemoryLimit   string
}

// ContainerResize describes a container as it stands, for planning a change.
type ContainerResize struct {
	Name string
	// The four figures as they are now, formatted as Kubernetes writes them.
	// Empty means the container declares none.
	CPURequest    string
	CPULimit      string
	MemoryRequest string
	MemoryLimit   string
	// RestartsForCPU and RestartsForMemory report the container's own
	// resizePolicy for each resource: true when it says RestartContainer.
	RestartsForCPU    bool
	RestartsForMemory bool
}

// ResizePlan is a checked resize: what will be sent, and what it will do.
type ResizePlan struct {
	Container string
	// The four figures to send. An empty one is not sent at all, leaving
	// whatever the container already declares.
	CPURequest    string
	CPULimit      string
	MemoryRequest string
	MemoryLimit   string
	// Restarts reports that applying this will restart the container,
	// because the container's own resizePolicy says so for a resource this
	// plan actually changes. THE PLAN SAYS IT; the dialog shows it; nobody
	// finds out afterwards.
	Restarts bool
	// RestartReason names which resource forces the restart, for the sentence
	// the dialog shows. Empty when Restarts is false.
	RestartReason string
}

// PlanResize checks a request against the container as it stands.
//
// The rules are all local and all refusals, in the order that answers the
// operator soonest: a container that is not there, a quantity that is not
// one, a limit below its request, and a request that changes nothing.
func PlanResize(container ContainerResize, request ResizeRequest) (ResizePlan, error) {
	if container.Name == "" || container.Name != request.Container {
		return ResizePlan{}, fmt.Errorf("%w: %q", ErrResizeContainerNotFound, request.Container)
	}

	plan := ResizePlan{
		Container:     container.Name,
		CPURequest:    request.CPURequest,
		CPULimit:      request.CPULimit,
		MemoryRequest: request.MemoryRequest,
		MemoryLimit:   request.MemoryLimit,
	}

	for name, value := range map[string]string{
		"CPU request":    plan.CPURequest,
		"CPU limit":      plan.CPULimit,
		"memory request": plan.MemoryRequest,
		"memory limit":   plan.MemoryLimit,
	} {
		if value == "" {
			continue
		}
		if _, err := resource.ParseQuantity(value); err != nil {
			return ResizePlan{}, fmt.Errorf("%w: the %s %q", ErrResizeQuantityInvalid, name, value)
		}
	}

	// Compared against what WILL be set rather than what was typed: leaving a
	// limit alone while lowering its request below it is fine, and lowering a
	// limit below a request the operator did not touch is not.
	if err := checkPair("CPU", effective(plan.CPURequest, container.CPURequest), effective(plan.CPULimit, container.CPULimit)); err != nil {
		return ResizePlan{}, err
	}
	if err := checkPair("memory", effective(plan.MemoryRequest, container.MemoryRequest), effective(plan.MemoryLimit, container.MemoryLimit)); err != nil {
		return ResizePlan{}, err
	}

	cpuChanged := changed(plan.CPURequest, container.CPURequest) || changed(plan.CPULimit, container.CPULimit)
	memoryChanged := changed(plan.MemoryRequest, container.MemoryRequest) ||
		changed(plan.MemoryLimit, container.MemoryLimit)

	if !cpuChanged && !memoryChanged {
		return ResizePlan{}, ErrResizeNoChange
	}

	// ONLY A RESOURCE THIS PLAN ACTUALLY CHANGES CAN FORCE A RESTART. A
	// container whose memory policy is RestartContainer is not restarted by a
	// CPU change, and warning about it would teach the operator to ignore the
	// warning.
	switch {
	case memoryChanged && container.RestartsForMemory:
		plan.Restarts = true
		plan.RestartReason = "memory"
	case cpuChanged && container.RestartsForCPU:
		plan.Restarts = true
		plan.RestartReason = "CPU"
	}

	return plan, nil
}

// effective is what a figure will be after the plan: the new one when there
// is one, otherwise what the container already declares.
func effective(wanted, current string) string {
	if wanted != "" {
		return wanted
	}
	return current
}

// changed reports whether a wanted figure differs from the current one.
//
// COMPARED AS QUANTITIES, NOT AS STRINGS: "1024Mi" and "1Gi" are the same
// amount of memory written two ways, and refusing the second as "no change"
// or sending it as a change would both be wrong — the first surprises
// somebody who typed a real edit, the second writes a no-op the API server
// records in managedFields.
func changed(wanted, current string) bool {
	if wanted == "" {
		return false
	}
	if current == "" {
		return true
	}

	left, errLeft := resource.ParseQuantity(wanted)
	right, errRight := resource.ParseQuantity(current)
	if errLeft != nil || errRight != nil {
		return wanted != current
	}
	return left.Cmp(right) != 0
}

// checkPair refuses a limit below its request, whichever of the two the
// operator actually typed.
func checkPair(resourceName, request, limit string) error {
	if request == "" || limit == "" {
		return nil
	}

	left, errLeft := resource.ParseQuantity(request)
	right, errRight := resource.ParseQuantity(limit)
	if errLeft != nil || errRight != nil {
		return nil
	}
	if right.Cmp(left) < 0 {
		return fmt.Errorf("%w: the %s limit %s is below the request %s", ErrResizeLimitBelowRequest, resourceName, limit, request)
	}
	return nil
}
