package domain

import (
	"fmt"
	"time"
)

// Termination is how a container's previous life ended.
//
// The single most requested thing a pod pane can show, and the one every
// client still makes an operator leave for `kubectl describe`. A restart
// count says a container died seventeen times; it does not say whether the
// kernel killed it for memory, whether a rollout stopped it cleanly, or
// whether the process exited on its own — three problems with nothing in
// common except the number that reports them.
//
// ONLY ONE OF THESE EXISTS PER CONTAINER, ever. Kubernetes keeps
// `lastState.terminated` for the most recent death and nothing before it, so
// a container with seventeen restarts has sixteen terminations that cannot be
// recovered from the API at all. Events would fill the gap and do not: the
// default `--event-ttl` is one hour, and it is not configurable on EKS, GKE
// or AKS.
type Termination struct {
	// ExitCode is the process's exit status. Zero is a clean exit, which for
	// a long-running container is still a death worth explaining.
	ExitCode int32
	// Signal is the signal that killed it, when one did. Zero means none.
	Signal int32
	// Reason is the kubelet's own word for it — "OOMKilled", "Error",
	// "Completed". It is the field that DISAMBIGUATES an exit code, not the
	// code itself; see Diagnosis.
	Reason string
	// Message is the kubelet's detail, usually empty.
	Message string
	// StartedAt and FinishedAt bracket the life that ended. The difference is
	// how long it survived, which separates a container that ran for a week
	// from one that died three seconds in — the same restart count, entirely
	// different faults.
	StartedAt  time.Time
	FinishedAt time.Time
}

// IsZero reports whether no previous termination was recorded.
func (t Termination) IsZero() bool {
	return t.ExitCode == 0 && t.Signal == 0 && t.Reason == "" && t.FinishedAt.IsZero()
}

// Lifetime is how long the container ran before it died, or zero when the
// timestamps are not both present.
func (t Termination) Lifetime() time.Duration {
	if t.StartedAt.IsZero() || t.FinishedAt.IsZero() {
		return 0
	}
	if d := t.FinishedAt.Sub(t.StartedAt); d > 0 {
		return d
	}
	return 0
}

// Diagnosis explains a termination in one sentence an operator can act on.
//
// THE REASON DISAMBIGUATES THE CODE, NOT THE OTHER WAY ROUND. 137 is
// 128+9 — SIGKILL — and every article about it says "OOMKilled". That is
// true only when the kubelet also said OOMKilled: a 137 WITHOUT that reason
// is an external kill, which means the grace period expired after a failed
// liveness probe, or somebody drained the node, or a rollout ran out of
// patience. Those need opposite responses to a memory limit that is too low,
// and conflating them sends people to tune the wrong number.
//
// 143 is 128+15 — SIGTERM — and during a rollout it is not a fault at all.
// Reporting it as one is a false positive that teaches operators to ignore
// the panel it appears in.
func (t Termination) Diagnosis() string {
	switch {
	case t.IsZero():
		return ""

	case t.Reason == "OOMKilled":
		return "The kernel killed this container for exceeding its memory limit. " +
			"Raise the limit if the workload needs the memory, or find what is holding it."

	case t.ExitCode == 137:
		// SIGKILL without the kubelet calling it an OOM. Something outside
		// the container ended it, and the commonest cause by far is a grace
		// period running out.
		return "Something killed this container outright (SIGKILL), and it was not the memory limit. " +
			"Usually a failed liveness probe or a drain whose grace period expired before the process exited."

	case t.ExitCode == 143:
		return "This container was asked to stop and did (SIGTERM). " +
			"During a rollout or a scale-down that is the normal path, not a fault."

	case t.Signal > 0:
		return fmt.Sprintf("Killed by signal %d.", t.Signal)

	case t.ExitCode == 0:
		return "The process exited cleanly on its own. For a container meant to stay up, " +
			"exiting zero is still a stop — check whether its main process is supposed to return."

	default:
		return fmt.Sprintf("The process exited with status %d, so it stopped on its own terms. "+
			"Its logs from before the restart are where the reason will be.", t.ExitCode)
	}
}

// Alarming reports whether a termination is worth colouring.
//
// A clean SIGTERM is how every rolling update stops a container, so a pane
// that flags it is flagging normal operation. OOMKilled and an unexplained
// SIGKILL are not normal, and a non-zero exit is at least worth reading.
func (t Termination) Alarming() bool {
	if t.IsZero() || t.ExitCode == 143 {
		return false
	}
	return t.Reason == "OOMKilled" || t.ExitCode != 0
}
