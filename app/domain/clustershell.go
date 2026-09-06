package domain

import "errors"

// ErrShellNamespaceRequired is raised when an in-cluster shell is asked for
// without a namespace.
//
// IT EXISTS BECAUSE THE EMPTY STRING IS NOT AN ERROR ANYWHERE ELSE HERE.
// NewNamespaceName("") returns NamespaceAll, which every LIST in this
// application reads as "every namespace" — perfectly sensible for a read, and
// meaningless for a create: a pod lives in exactly one namespace, and handing
// the empty string to the client would create it in whatever the kubeconfig's
// default happens to be. That is a pod appearing somewhere nobody asked for,
// under a name that composed perfectly well. So "all namespaces" is refused
// and the operator is asked, rather than a namespace being guessed for them.
var ErrShellNamespaceRequired = errors.New("an in-cluster shell needs one namespace, and \"all namespaces\" is not one")

// ClusterShell is one live in-cluster shell — an ordinary pod PodSteer created
// in a namespace, running a shell its terminal session attaches to.
//
// THE THIRD THING PODSTEER CAN OPEN INSIDE A CLUSTER, and it is neither of
// the other two. It is not the ephemeral debug container, which is injected
// into somebody else's pod and shares that pod's namespaces; and it is not the
// node shell, which is privileged, enters a node's host namespaces with
// nsenter, and is the most powerful thing this application can do. This is an
// ordinary, unprivileged pod, admissible under Pod Security's `restricted`
// profile, for somebody who wants kubectl, dig and curl FROM INSIDE the
// cluster's network — a vantage point, not a privilege.
//
// Tracked exactly like NodeShell, and for the identical reason: the pod is a
// resource PodSteer created and is on the hook to remove, so the record of it
// and the thing that deletes it are created together and torn down together,
// the pod is deleted when the terminal session ends or PodSteer closes, and
// the live set is listable so the activity surface can show it with a stop
// control.
type ClusterShell struct {
	// ID is stable for the life of the shell.
	ID string
	// ClusterID, Namespace and PodName say where the pod is, so it can be
	// deleted against the cluster it was created on — the cluster is part of
	// the identity for the same reason it is on a NodeShell: two clusters run
	// identically named namespaces, and deleting the wrong one is worse than
	// leaking.
	ClusterID ClusterID
	Namespace NamespaceName
	PodName   string
	// Image is the pod's image, kept for display in the activity list.
	Image string
	// ContainerName is the pod's single container, which the terminal session
	// attaches to. Carried here so the layer opening the session need not know
	// how the pod was built.
	ContainerName string
	// Adopted reports that this record was taken over an EXISTING pod rather
	// than one this session created — the reuse path. It changes nothing about
	// the lifecycle (an adopted pod is deleted on session end exactly as a
	// created one is); it is here so the activity list and the log can say
	// which happened, because "PodSteer created a pod" and "PodSteer took
	// responsibility for one it found" are different sentences about a
	// cluster.
	Adopted bool
}

// ClusterShellCandidate is a pod PodSteer created for an in-cluster shell,
// found in a namespace before another one is created.
//
// PHASE IS QUOTED, NOT JUDGED. The candidate carries the pod's phase exactly
// as the API server reported it, and the decision about what may be offered is
// PlanClusterShellReuse's — see that function for why only a running pod is an
// offer.
type ClusterShellCandidate struct {
	PodName string
	Image   string
	// Phase is the pod's status.phase as the API server reported it —
	// PodPhaseRunning, PodPhasePending, PodPhaseSucceeded, PodPhaseFailed,
	// PodPhaseUnknown, or "" when the kubelet has not reported one yet.
	//
	// The PodPhase the pod list already uses rather than a plain string:
	// PlanClusterShellReuse compares it against PodPhaseRunning, and two
	// spellings of "Running" in one repository is one too many.
	// PodPhaseTerminating never appears here — that one is the mapper's own
	// substitution for a deleting pod, and this reads status.phase directly.
	Phase PodPhase
}

// ClusterShellReuse splits the pods PodSteer created in a namespace into the
// ones that may be offered and the ones that may only be reported.
type ClusterShellReuse struct {
	// Reusable are pods in a Running phase. Attaching to one of these lands
	// the operator on a shell.
	Reusable []ClusterShellCandidate
	// Other are pods PodSteer created that are in any other phase. They are
	// worth SAYING — a namespace accumulating them is the explanation for
	// pods somebody is looking at and cannot account for — but they are not
	// offers.
	Other []ClusterShellCandidate
}

// PlanClusterShellReuse decides which of PodSteer's own shell pods in a
// namespace may be offered for reuse.
//
// ONLY RUNNING, AND THAT IS THE WHOLE RULE. A pod that has exited — Succeeded
// because its shell was closed from inside, Failed because it hit its
// activeDeadlineSeconds backstop, Pending because it cannot pull its image —
// cannot be attached to. Offering one would hand somebody an attach that fails
// for a reason the offer gave them no way to see, which is worse than not
// offering: they asked for a shell and got an error about a pod they never
// chose to create.
//
// A pod in some other state is still REPORTED, in Other, because it answers a
// question the operator will otherwise have to ask the cluster: why does this
// namespace have five podsteer-shell-* pods in it. Reporting is not offering.
func PlanClusterShellReuse(candidates []ClusterShellCandidate) ClusterShellReuse {
	plan := ClusterShellReuse{}
	for _, candidate := range candidates {
		if candidate.Phase == PodPhaseRunning {
			plan.Reusable = append(plan.Reusable, candidate)
			continue
		}
		plan.Other = append(plan.Other, candidate)
	}
	return plan
}
