package domain

import (
	"errors"
	"fmt"
	"strings"
)

// Forward is one live port-forward.
//
// Identified by its LOCAL PORT, because that is the thing that is actually
// scarce and the thing a collision is about. Two forwards to the same pod
// port from two different local ports are two forwards; two forwards claiming
// one local port cannot both exist.
type Forward struct {
	// ID is stable for the life of the forward.
	ID string
	// ClusterID, Namespace and Pod say what is on the far end. The pod's UID
	// is kept as well, so a forward can be recognised as belonging to a pod
	// that has since been replaced by one of the same name.
	ClusterID ClusterID
	Namespace NamespaceName
	Pod       string
	PodUID    string
	// LocalPort is the port on this machine. Never zero once running: a
	// forward that let the operating system choose has had the chosen port
	// read back into it, because a forward whose address nobody can state is
	// not usable.
	LocalPort int
	// RemotePort is the container port on the far end.
	RemotePort int
	// Scheme is what a browser should be sent to, guessed from the port's
	// name. Only ever "http" or "https".
	Scheme string
	// Selector is the pod's own labels, used to find a replacement when the
	// pod behind this forward goes away.
	//
	// The pod's labels rather than its controller's, deliberately: a
	// ReplicaSet's pods carry pod-template-hash, so selecting on them finds
	// siblings of the SAME REVISION. Reconnecting to a pod of a different
	// revision would silently move a forward onto different code, which is
	// the sort of helpfulness nobody asked for.
	Selector map[string]string
	// Reconnecting reports that the pod died and a replacement is being
	// sought. The local port stays bound throughout, so whatever is pointed
	// at it keeps its address.
	Reconnecting bool
	// Lost reports that the reconnect window ran out: nothing matching the
	// selector came back, and the local port has been released. The forward
	// stays listed rather than vanishing, because a row that disappears after
	// two minutes of a network outage is indistinguishable from one the
	// operator stopped. It keeps looking, slowly, and comes back by itself
	// when the cluster does; Stop dismisses it.
	Lost bool
	// Target is what the operator asked to be forwarded to, as against the pod
	// it happens to be landed on today. It is what a kept forward is rebuilt
	// from after a restart.
	Target ForwardTarget
}

// ForwardTargetKind says what a forward was asked to point at.
type ForwardTargetKind string

const (
	// ForwardToPod is a forward onto one pod's container port.
	ForwardToPod ForwardTargetKind = "pod"
	// ForwardToService is a forward onto a Service's port, which PodSteer
	// resolves to a pod behind it.
	ForwardToService ForwardTargetKind = "service"
)

// IsValid reports whether the kind is one this build understands.
func (k ForwardTargetKind) IsValid() bool {
	return k == ForwardToPod || k == ForwardToService
}

// ForwardTarget is the operator's request, kept apart from where it landed.
//
// A POD NAME CHANGES WITH EVERY ROLLOUT and a Service's does not, which is why
// a kept forward is most durable when it points at a Service; a pod forward
// whose pod is gone by the next launch is reported as paused with the reason.
type ForwardTarget struct {
	Kind ForwardTargetKind
	// Name is the pod or the Service.
	Name string
	// ServicePort is the Service port as the operator named it — a number or
	// a name. Empty for a pod.
	ServicePort string
	// PortName is the port's name, which decides the scheme guess.
	PortName string
}

// KeptForward is the definition of a forward the operator asked to keep
// across restarts. It is what reaches the settings file.
//
// DEFINITION ONLY, NEVER STATE AND NEVER A SECRET: a context, a namespace, a
// pod or Service name, two port numbers. No credential, no selector, no
// address beyond a loopback port, and nothing the cluster returned.
type KeptForward struct {
	Namespace NamespaceName
	Target    ForwardTarget
	// RemotePort is the container port for a pod forward.
	RemotePort int
	// LocalPort is the port on this machine, as bound — never zero, so a
	// restored forward gives back the address whatever was pointed at it
	// already has.
	LocalPort int
}

// Validate reports whether the definition is usable.
func (k KeptForward) Validate() error {
	if strings.TrimSpace(string(k.Namespace)) == "" || strings.TrimSpace(k.Target.Name) == "" {
		return errors.New("a kept forward needs a namespace and a target")
	}
	if !k.Target.Kind.IsValid() {
		return fmt.Errorf("a kept forward has an unknown target kind %q", k.Target.Kind)
	}
	if k.LocalPort < 1 || k.LocalPort > 65535 {
		return fmt.Errorf("a kept forward has local port %d", k.LocalPort)
	}
	if k.Target.Kind == ForwardToPod && (k.RemotePort < 1 || k.RemotePort > 65535) {
		return fmt.Errorf("a kept forward has remote port %d", k.RemotePort)
	}
	return nil
}

// Address is where to point a browser.
func (f Forward) Address() string {
	return fmt.Sprintf("%s://localhost:%d", f.Scheme, f.LocalPort)
}

// SchemeForPort guesses the protocol from a container port's NAME.
//
// The name is the only hint Kubernetes offers — the port number tells you
// nothing, since anything can listen anywhere — and it is a convention people
// follow closely enough to be worth using: a port named "https" is https.
// Everything else is http, because being wrong about that costs one redirect
// and being wrong the other way costs a confusing TLS error.
func SchemeForPort(name string) string {
	if strings.EqualFold(name, "https") {
		return "https"
	}
	return "http"
}

// ForwardKey identifies a forward for collision purposes.
//
// The CLUSTER IS IN THE KEY, not just the pod name. Two clusters commonly run
// identically named pods in identically named namespaces — that is what a
// staging environment IS — and a cache keyed without the cluster returns one
// cluster's forward for another's request. Headlamp has an open bug that is
// exactly this.
func ForwardKey(id ClusterID, namespace NamespaceName, pod string, remotePort int) string {
	return fmt.Sprintf("%s|%s|%s|%d", id, namespace, pod, remotePort)
}
