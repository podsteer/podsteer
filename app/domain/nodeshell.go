package domain

// NodeShell is one live node shell — a privileged pod pinned to a node whose
// process runs a login shell in the node's own host namespaces, the way
// `kubectl node-shell` and Lens do.
//
// Identified and tracked like a Forward, and for the same reason: the pod is
// a resource PodSteer created and is on the hook to remove. Every leak in the
// clients that offer this comes from the record of the pod and the thing that
// deletes it parting company — so the two are created together and torn down
// together, the pod is deleted when the terminal session ends or PodSteer
// closes, and this appears in the activity list exactly as a port-forward
// does, with a stop control, so an operator can always see and end what is
// still running on a node.
type NodeShell struct {
	// ID is stable for the life of the node shell.
	ID string
	// ClusterID, Namespace and PodName say where the pod is, so it can be
	// deleted against the cluster it was created on — the cluster is part of
	// the identity for the same reason it is on a Forward: two clusters run
	// identically named namespaces, and deleting the wrong one is worse than
	// leaking.
	ClusterID ClusterID
	Namespace NamespaceName
	PodName   string
	// NodeName is the node the shell runs on, pinned by the pod's nodeName.
	NodeName string
	// Image is the pod's image, kept for display in the activity list.
	Image string
	// ContainerName is the pod's single container, which the terminal session
	// attaches to. Carried here so the layer opening the session need not know
	// how the pod was built.
	ContainerName string
}
