package domain

// DebugContainerSpec describes an ephemeral debug container to add to a pod,
// the way `kubectl debug -it POD --image=… --target=CONTAINER` does.
//
// It is a value object, not an entity: an ephemeral container has no life of
// its own to track once it is added, because Kubernetes will not remove one.
// It stays in the pod's spec until the pod is deleted — the dialog that offers
// this says so, because it is the cluster's behaviour and not something
// PodSteer could undo if it wanted to.
type DebugContainerSpec struct {
	// Image is the debugger image, e.g. busybox:1.37. The application layer
	// checks it with ValidImageReference before any request leaves the
	// process, mirroring SetImage.
	Image string
	// TargetContainer, when set, becomes the ephemeral container's
	// targetContainerName — it shares that container's process namespace, so
	// the debugger can see and signal the target's processes. Empty shares
	// only the pod's namespaces, which is the default when nothing is
	// targeted.
	TargetContainer string
	// Command is what the container runs, e.g. ["sh"]. Kept separate from the
	// audit trail on purpose: the cluster, namespace, pod, target and image
	// are logged, never a command transcript.
	Command []string
	// TTY and Stdin allocate a pseudo-terminal and keep standard input open,
	// which an interactive debug shell needs. Both are set true by the caller
	// that opens a terminal into the container.
	TTY   bool
	Stdin bool
}
