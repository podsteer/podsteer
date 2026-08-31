package domain

import (
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
