package domain

import (
	"errors"
	"fmt"
	"strconv"
)

// Forwarding to a Service, which Kubernetes does not do.
//
// THE API SERVER FORWARDS TO A POD AND ONLY TO A POD. `kubectl port-forward
// service/x` is a client-side convenience: kubectl reads the Service, picks
// one of the pods behind it, translates the service port to that pod's
// container port, and forwards to the pod. What follows is the same
// translation, written down so it can be argued with — because every step of
// it has a case that is easy to get quietly wrong:
//
//   - A Service port can be named or numbered, and the operator may have
//     typed either.
//   - `targetPort` may be absent, which does NOT mean zero: Kubernetes
//     defaults it to the service port.
//   - `targetPort` may be a NAME, which only means something against a
//     particular pod's containers — two pods behind one Service can, in
//     principle, resolve the same name to different numbers.
//
// PodSteer's version differs from kubectl's in one way, and it is the reason
// this is worth having rather than a shell alias: the forward keeps the
// Service's SELECTOR, so when the pod it landed on goes away the existing
// supervisor finds another one behind the same Service and rebinds the same
// local port. kubectl drops.

var (
	// ErrServiceHasNoSelector means the Service selects no pods — an
	// ExternalName, a headless Service with hand-managed Endpoints, or one
	// whose selector is simply absent.
	ErrServiceHasNoSelector = errors.New("this Service selects no pods to forward to")

	// ErrServicePortNotFound means the Service has no port by that name or
	// number.
	ErrServicePortNotFound = errors.New("the Service has no such port")

	// ErrServicePortAmbiguous means no port was named and the Service has
	// more than one, so PodSteer will not choose on the operator's behalf.
	ErrServicePortAmbiguous = errors.New("this Service has several ports; name the one to forward")

	// ErrTargetPortUnresolved means the Service's targetPort is a name that
	// the pod behind it does not declare.
	ErrTargetPortUnresolved = errors.New("no container declares the port this Service targets")

	// ErrNoReadyEndpoint means the Service selects pods but none of them is
	// ready to receive traffic.
	ErrNoReadyEndpoint = errors.New("no ready pod is behind this Service")
)

// ServicePort is one entry of a Service's spec.ports, in the shape this
// package can reason about.
type ServicePort struct {
	// Name is the port's name, which is empty on a single-port Service.
	Name string
	// Port is the port the Service listens on.
	Port int
	// TargetPort is what it forwards to: a number as a string, a container
	// port NAME, or empty — which means the service port itself.
	TargetPort string
	// Protocol is TCP, UDP or SCTP as the Service declares it.
	Protocol string
}

// ContainerPort is one entry of a container's ports, for resolving a name.
type ContainerPort struct {
	Name string
	Port int
}

// SelectServicePort picks the port the operator asked for.
//
// An empty `wanted` is answered only when there is no ambiguity to resolve: a
// Service with one port has an obvious answer, and a Service with three does
// not. GUESSING WOULD BE WORSE THAN ASKING — forwarding to the wrong port of a
// multi-port Service produces a connection that establishes and then behaves
// like a broken application, which is a long way to travel before finding out.
func SelectServicePort(ports []ServicePort, wanted string) (ServicePort, error) {
	if len(ports) == 0 {
		return ServicePort{}, ErrServicePortNotFound
	}

	if wanted == "" {
		if len(ports) == 1 {
			return ports[0], nil
		}
		return ServicePort{}, ErrServicePortAmbiguous
	}

	// A number matches the service port; a word matches its name. Both are
	// tried, in that order, because a Service may legitimately name a port
	// "8080" and the number is what somebody typing it meant.
	if number, err := strconv.Atoi(wanted); err == nil {
		for _, port := range ports {
			if port.Port == number {
				return port, nil
			}
		}
	}
	for _, port := range ports {
		if port.Name == wanted {
			return port, nil
		}
	}
	return ServicePort{}, fmt.Errorf("%w: %q", ErrServicePortNotFound, wanted)
}

// ResolveTargetPort turns a Service port into the pod port to forward to.
//
// The three cases are Kubernetes' own, and the middle one is the trap:
//
//   - a NUMBER is used as it stands;
//   - EMPTY defaults to the service port, which is not zero;
//   - a NAME is resolved against this pod's containers, and a pod that does
//     not declare it is an error rather than a guess.
func ResolveTargetPort(port ServicePort, containers []ContainerPort) (int, error) {
	if port.TargetPort == "" {
		return port.Port, nil
	}
	if number, err := strconv.Atoi(port.TargetPort); err == nil {
		return number, nil
	}
	for _, container := range containers {
		if container.Name == port.TargetPort {
			return container.Port, nil
		}
	}
	return 0, fmt.Errorf("%w: %q", ErrTargetPortUnresolved, port.TargetPort)
}

// ServiceForwardTarget is everything a forward needs, resolved from a Service.
type ServiceForwardTarget struct {
	// Pod and PodUID are the pod this forward will land on FIRST. It is not
	// pinned: Selector below is what lets the supervisor move to another pod
	// behind the same Service when this one goes away.
	Pod    string
	PodUID string
	// ContainerPort is the resolved pod port.
	ContainerPort int
	// ServicePort is what the Service listens on, kept for the label the
	// interface shows — the operator asked for the Service's port and should
	// see it, even though the connection lands on the container's.
	ServicePort int
	// PortName is the Service port's name, which decides the scheme guess.
	PortName string
	// Protocol is the Service's, refused upstream when it is not TCP.
	Protocol string
	// Selector is the Service's own, and the reason this outlives its pod.
	Selector map[string]string
}
