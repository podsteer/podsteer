package domain

// Whether a status condition is reporting a problem.
//
// EVERY KIND USES THE SAME FIELD AND NOT THE SAME POLARITY, which is the
// thing to get right. `Ready=False` on a pod is a problem; `MemoryPressure=
// True` on a node is a problem; and a client that assumes one rule colours
// every healthy node as a warning and leaves a node genuinely under pressure
// uncoloured. That is exactly what this application did before this file
// existed.
//
// Kubernetes' own convention is that a condition type is named for the state
// it asserts, so True means the name is true — the polarity is a property of
// the TYPE, not of the kind that carries it. That is why this classifies by
// type alone: a custom resource reusing `Degraded` gets the same answer as a
// built-in one, without anybody adding its kind here.
//
// This is a verdict, so it lives here rather than in a Svelte ternary — see
// CLAUDE.md. It is also why it is tested rather than eyeballed: getting a
// polarity backwards is invisible until somebody is looking at the wrong
// colour during an incident.

// ConditionTone is how a condition should be read.
type ConditionTone string

const (
	// ConditionNormal means the condition is reporting what it should, or
	// means nothing either way.
	ConditionNormal ConditionTone = ""
	// ConditionWarning means the condition is reporting a problem.
	ConditionWarning ConditionTone = "warn"
)

// affirmativeIsBad are condition types whose name asserts a problem, so True
// is the bad state and False is the ordinary one.
var affirmativeIsBad = map[string]bool{
	// Node — the kubelet's own alarms.
	"MemoryPressure":     true,
	"DiskPressure":       true,
	"PIDPressure":        true,
	"NetworkUnavailable": true,
	// Workloads.
	"ReplicaFailure": true,
	"Failed":         true,
	"Degraded":       true,
	"Stalled":        true,
	// Namespace, which reports why a deletion is stuck.
	"NamespaceDeletionContentFailure":             true,
	"NamespaceDeletionDiscoveryFailure":           true,
	"NamespaceDeletionGroupVersionParsingFailure": true,
	"NamespaceContentRemaining":                   true,
	"NamespaceFinalizersRemaining":                true,
}

// affirmativeIsGood are condition types whose name asserts a healthy state, so
// False is the problem.
//
// Listed rather than assumed as the default, because an unrecognised type is
// safer left uncoloured than guessed at: a wrong colour on a healthy object is
// worse than no colour, which is the lesson of the bug this replaced.
var affirmativeIsGood = map[string]bool{
	// Pod.
	"Ready":                     true,
	"Initialized":               true,
	"PodScheduled":              true,
	"ContainersReady":           true,
	"PodReadyToStartContainers": true,
	// Workloads.
	"Available":   true,
	"Progressing": true,
	// CustomResourceDefinition, and the convention most operators follow.
	"Established":   true,
	"NamesAccepted": true,
	"Synced":        true,
	"Healthy":       true,
}

// ClassifyCondition says whether a condition is reporting a problem.
//
// A condition whose status is neither True nor False — Kubernetes allows
// Unknown, and means it — is left uncoloured. Unknown is not a problem being
// reported; it is a problem being unreportable, and the row's own text says
// so better than a colour would.
func ClassifyCondition(conditionType, status string) ConditionTone {
	return ClassifyConditionOf(conditionType, status, "")
}

// terminalPhases are the pod phases after which nothing is expected to run.
//
// A POD THAT FINISHED IS NOT A POD THAT FAILED, and forgetting that put two
// warnings on the panel of every healthy completed Job. A Succeeded pod
// carries Ready=False and ContainersReady=False for ever, correctly and by
// design — a container that has exited is not ready — and read by type and
// status alone both classify as problems.
//
// Pod.IsHealthy already special-cases this for exactly the same reason (see
// CLAUDE.md). A panel that always has something to say is one people stop
// reading, which is the cost of getting this wrong.
var terminalPhases = map[string]bool{
	"Succeeded": true,
	"Failed":    true,
}

// readinessTypes are the conditions a terminal pod is entitled to report
// False on. Anything else it says is still worth colouring: a pod that was
// never scheduled and then failed is still reporting why.
var readinessTypes = map[string]bool{
	"Ready":           true,
	"ContainersReady": true,
}

// ClassifyConditionOf is ClassifyCondition with the subject's phase, where
// there is one.
//
// The phase is the only context this takes, and it takes it because the same
// condition on the same kind means different things before and after the pod
// has finished. Everything else stays a property of the type.
func ClassifyConditionOf(conditionType, status, phase string) ConditionTone {
	if terminalPhases[phase] && readinessTypes[conditionType] {
		return ConditionNormal
	}

	switch status {
	case "True":
		if affirmativeIsBad[conditionType] {
			return ConditionWarning
		}
	case "False":
		if affirmativeIsGood[conditionType] {
			return ConditionWarning
		}
	}
	return ConditionNormal
}
