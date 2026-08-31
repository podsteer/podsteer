package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// PodFinding is something worth telling an operator about one pod.
//
// The same shape as an overview Finding and deliberately not the same type:
// these are scoped to a pod somebody is already looking at, so they carry no
// subjects and no kind to navigate to. Merging them would mean every overview
// rule growing a "which pod" case it does not need.
type PodFinding struct {
	Severity Severity
	// Title is the problem, in a few words.
	Title string
	// Detail says what was observed, with the numbers in it.
	Detail string
	// Advice says what to do. A finding without one is an observation, and
	// there are enough of those on screen already.
	Advice string
}

// AssessPod reports what is wrong, or about to be, with one pod.
//
// A PURE FUNCTION of the pod and a clock, like NewOverview, so every rule can
// be argued with in a test rather than only observed against a real cluster.
//
// The rules are chosen on one criterion: they must say something an operator
// could not read off the object themselves. "This pod is Burstable" is on the
// screen already. "This pod is Burstable because the sidecar declares no
// memory limit, which puts the whole pod ahead of Guaranteed pods in the
// eviction queue" is the same fact turned into a decision.
func AssessPod(pod Pod, now time.Time) []PodFinding {
	findings := make([]PodFinding, 0, 4)

	findings = append(findings, stuckTerminatingFinding(pod, now)...)
	findings = append(findings, unschedulableFinding(pod)...)
	findings = append(findings, sandboxFinding(pod)...)
	findings = append(findings, resizeFindings(pod)...)
	findings = append(findings, probeFindings(pod, now)...)
	findings = append(findings, crashLoopFindings(pod)...)
	findings = append(findings, qosFinding(pod)...)
	findings = append(findings, mutableTagFindings(pod)...)
	findings = append(findings, bareFinding(pod)...)

	// Ranked, not left in the order the rules happen to run in. There are
	// enough of these now that a pod which is crash-looping AND running a
	// mutable tag would otherwise lead with the tag, because that rule is
	// further down this function — which is an implementation detail deciding
	// what somebody reads first.
	//
	// Stable within a severity, so the order above still breaks ties: it runs
	// roughly outermost-first, and "nothing will schedule this" belongs above
	// "a probe is misconfigured" on a pod suffering both.
	sort.SliceStable(findings, func(i, j int) bool {
		return findings[i].Severity.rank() > findings[j].Severity.rank()
	})

	return findings
}

// unschedulableFinding surfaces the scheduler's own explanation.
//
// THE CONDITION CARRIES MORE THAN THE EVENT DOES. The scheduler writes the
// full FitError into the PodScheduled condition's message and a TRUNCATED
// copy into the FailedScheduling event — so `kubectl describe`, which prints
// conditions as Type and Status only and shows the event, is reading the
// shorter one. On a cluster with many distinct failure reasons the truncation
// is exactly where the reason you need gets cut off.
func unschedulableFinding(pod Pod) []PodFinding {
	if pod.Phase() != PodPhasePending || pod.IsScheduled() {
		return nil
	}
	message := strings.TrimSpace(pod.Message())
	if message == "" {
		return nil
	}

	return []PodFinding{{
		Severity: SeverityCritical,
		Title:    "Nothing will schedule this pod",
		Detail:   message,
		Advice:   schedulingAdvice(message),
	}}
}

// schedulingAdvice maps the scheduler's vocabulary onto the field to change.
//
// The message names which plugin rejected each node, and each plugin
// corresponds to one thing in the manifest. Translating it saves the step
// everybody does by hand, and gets it right for the reasons that look alike:
// "didn't match node selector" and "untolerated taint" both mean "no node
// wanted you" and need opposite edits.
func schedulingAdvice(message string) string {
	switch {
	case strings.Contains(message, "untolerated taint"):
		return "The nodes are tainted against this pod. Add a matching toleration, or schedule it " +
			"somewhere without the taint."
	case strings.Contains(message, "Insufficient cpu"), strings.Contains(message, "Insufficient memory"):
		return "No node has enough unreserved capacity for what this pod REQUESTS — which is not the " +
			"same as enough free capacity. Lower the requests or add a node."
	// BEFORE the plain node-affinity case: "volume node affinity conflict"
	// contains "node affinity", and matched by the wrong branch it sends
	// somebody to check nodeSelector labels for a problem that is actually a
	// disk in the wrong zone. Order is load-bearing here.
	case strings.Contains(message, "volume node affinity conflict"):
		return "The pod's volume is pinned to a zone the candidate nodes are not in. A volume cannot " +
			"move; the pod has to go to it."
	case strings.Contains(message, "node affinity"), strings.Contains(message, "node selector"):
		return "No node carries the labels this pod insists on. Check its nodeSelector and affinity " +
			"against the labels the nodes actually have."
	case strings.Contains(message, "didn't match pod anti-affinity"):
		return "Its own anti-affinity is keeping it away from the nodes running its siblings. There " +
			"may be fewer distinct nodes than replicas."
	default:
		return "The scheduler's reason is above, verbatim. It names the plugin that rejected each node."
	}
}

// probeFindings catches probe configuration that will cause the restarts
// somebody is already seeing, or is about to.
//
// The arithmetic is the whole point and nothing else does it. A liveness
// probe starts killing at initialDelaySeconds + failureThreshold ×
// periodSeconds. Compare that against how long the container ACTUALLY takes
// to start — which the pod records — and a boot loop is predictable from a
// pod that is currently healthy.
func probeFindings(pod Pod, now time.Time) []PodFinding {
	findings := make([]PodFinding, 0, 2)

	for _, container := range pod.Containers() {
		probe := container.Liveness
		if probe.IsZero() {
			continue
		}

		budget := probe.KillsAfter()

		// The observed startup: how long this container has been running.
		// Only meaningful once, at the start of a life — a container up for
		// three days tells us nothing about how long it took to boot.
		if !container.StartedAt.IsZero() && container.Startup.IsZero() {
			startup := now.Sub(container.StartedAt)
			if startup > 0 && startup < budget && float64(startup) > 0.8*float64(budget) {
				findings = append(findings, PodFinding{
					Severity: SeverityWarning,
					Title:    "Liveness probe is close to killing " + container.Name,
					Detail: fmt.Sprintf(
						"It starts killing %s after startup, and this container took %s to come up.",
						budget.Round(time.Second), startup.Round(time.Second)),
					Advice: "Add a startupProbe. It gates the liveness probe until the container is " +
						"actually up, which is what initialDelaySeconds is usually being used to " +
						"approximate — and it does not have to be guessed.",
				})
			}
		}

		// THE TRAP THAT TURNS A SLOW DEPENDENCY INTO A RESTART LOOP. When
		// liveness and readiness check the same endpoint, and that endpoint
		// reports on a dependency — a database, a queue — then the dependency
		// going away does not merely take the pod out of the Service, which
		// is what readiness is for and is harmless. It also fails liveness,
		// so the kubelet restarts a process that was working, repeatedly,
		// while the thing that is actually broken is somewhere else entirely.
		if probe.Target != "" && probe.Target == container.Readiness.Target {
			findings = append(findings, PodFinding{
				Severity: SeverityWarning,
				Title:    "Liveness and readiness check the same thing on " + container.Name,
				Detail:   "Both probes target " + probe.Target + ".",
				Advice: "They are meant to answer different questions: readiness asks whether to send " +
					"traffic, liveness asks whether to restart. Pointed at one endpoint, anything that " +
					"fails the first also restarts the container — so a dependency going down " +
					"restarts a process that was fine. Give liveness a check of the process itself.",
			})
		}

		if probe.TimeoutSeconds >= probe.PeriodSeconds && probe.PeriodSeconds > 0 {
			findings = append(findings, PodFinding{
				Severity: SeverityWarning,
				Title:    "Liveness probe on " + container.Name + " can overlap itself",
				Detail: fmt.Sprintf("timeout=%ds is not shorter than period=%ds.",
					probe.TimeoutSeconds, probe.PeriodSeconds),
				Advice: "A probe that has not answered before the next one is due makes the failure " +
					"threshold mean something other than it appears to. Set the timeout below the period.",
			})
		}
	}

	return findings
}

// qosFinding names the container responsible for the pod's QoS class.
//
// "QoS: Burstable" is on the screen already. Which container caused it, and
// what it costs under pressure, is not — and the second is the part that
// decides whether anybody acts.
func qosFinding(pod Pod) []PodFinding {
	if pod.QoSClass() == QoSGuaranteed || !pod.OccupiesNode() {
		return nil
	}

	culprits := make([]string, 0, 2)
	for _, container := range pod.Containers() {
		if container.Limits.MemoryBytes <= 0 || container.Requests.MemoryBytes != container.Limits.MemoryBytes {
			culprits = append(culprits, container.Name)
		}
	}
	if len(culprits) == 0 {
		return nil
	}

	if pod.QoSClass() == QoSBestEffort {
		return []PodFinding{{
			Severity: SeverityWarning,
			Title:    "First in the queue to be evicted",
			Detail: "This pod is BestEffort: it reserves nothing, so the scheduler cannot hold " +
				"anything for it and the kubelet evicts it before anything else when a node runs short.",
			Advice: "Declare requests on " + strings.Join(culprits, ", ") + ", even approximate ones. " +
				"A request is what makes the guarantee exist.",
		}}
	}

	return []PodFinding{{
		Severity: SeverityInfo,
		Title:    "Burstable, not Guaranteed",
		Detail: fmt.Sprintf("%s %s a memory request equal to its limit, which is what Guaranteed requires.",
			strings.Join(culprits, ", "), agrees(len(culprits), "does not have", "do not have")),
		Advice: "Under node memory pressure the kubelet evicts Burstable pods before Guaranteed ones, " +
			"and ranks by how far usage exceeds requests. Matching request to limit moves this pod " +
			"to the back of that queue.",
	}}
}

// mutableTagFindings reports images that cannot be reproduced.
//
// A tag is a pointer somebody can move. `:latest` today and `:latest`
// tomorrow are not the same bytes, so a pod that restarts can come back as
// different code with nothing recording that it changed — and a rollback to
// "the same tag" rolls back to nothing at all.
func mutableTagFindings(pod Pod) []PodFinding {
	loose := make([]string, 0, 2)
	for _, container := range pod.Containers() {
		if hasMutableTag(container.Image) {
			loose = append(loose, container.Name)
		}
	}
	if len(loose) == 0 {
		return nil
	}

	return []PodFinding{{
		Severity: SeverityInfo,
		Title:    "Running a tag that can move",
		Detail: fmt.Sprintf("%s %s an image by a tag rather than a digest.",
			strings.Join(loose, ", "), agrees(len(loose), "names", "name")),
		Advice: "A tag is a pointer somebody can repoint. If this pod restarts it may come back as " +
			"different code with nothing recording the change, and rolling back to the same tag rolls " +
			"back to nothing. Pin a digest for anything you need to reproduce.",
	}}
}

// hasMutableTag reports whether an image reference is pinned to a digest.
//
// The digest is what makes a reference immutable, so anything without one is
// mutable however specific the tag looks — "v1.2.3" is as repointable as
// "latest", it is only less likely to be repointed.
func hasMutableTag(image string) bool {
	if image == "" {
		return false
	}
	return !strings.Contains(image, "@sha256:")
}

// bareFinding reports a pod nothing will recreate.
func bareFinding(pod Pod) []PodFinding {
	if pod.Controller().Name != "" || !pod.OccupiesNode() {
		return nil
	}

	return []PodFinding{{
		Severity: SeverityWarning,
		Title:    "Nothing will recreate this pod",
		Detail:   "It has no controlling owner, so it was created directly rather than by a workload.",
		Advice: "If its node is drained or it is evicted, it is gone and nothing notices. Anything " +
			"meant to stay running wants a Deployment, StatefulSet or DaemonSet behind it.",
	}}
}

// agrees picks the verb form for a count.
//
// Not `plural`, which prepends the number: these sentences already name the
// containers, and "app, sidecar 2 do not have a memory request" is not a
// sentence. This chooses the agreement and nothing else.
func agrees(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}

// stuckDeletionAfter is how long a deletion may take before it is a problem.
//
// The default grace period is thirty seconds, and a pod that has honoured it
// is gone before anybody opens a pane to look. Two minutes is comfortably
// past a long but legitimate shutdown while still catching the case this rule
// exists for, where the answer is minutes-to-forever rather than a slow
// SIGTERM.
const stuckDeletionAfter = 2 * time.Minute

// stuckTerminatingFinding names what is holding a deletion open.
//
// THE CHEAPEST HIGH-VALUE FINDING AVAILABLE, because `kubectl describe`
// prints "Terminating (lasts 30s)" and then never prints the finalizers — so
// the one field that explains why thirty seconds became three hours is absent
// from the baseline everybody falls back to. It is right there in the object.
//
// A finalizer is a controller saying "not until I have cleaned up". When that
// controller is gone, or wedged, or never existed, the pod stays forever and
// no amount of deleting it again helps, because the delete already happened —
// what is outstanding is somebody else's promise to finish.
func stuckTerminatingFinding(pod Pod, now time.Time) []PodFinding {
	if !pod.Terminating() {
		return nil
	}

	elapsed := pod.DeletingFor(now)
	if elapsed < stuckDeletionAfter {
		return nil
	}

	holders := pod.Finalizers()
	if len(holders) == 0 {
		// Deleted, past its grace period, and nothing registered against it.
		// The pod object outliving its own deletion with no finalizer is a
		// kubelet that cannot confirm the containers are gone — commonly a
		// node that stopped reporting.
		return []PodFinding{{
			Severity: SeverityWarning,
			Title:    "Deleting, with nothing holding it",
			Detail: fmt.Sprintf("Deletion was requested %s ago and no finalizer is registered.",
				roundDuration(elapsed)),
			Advice: "Nothing is waiting to clean up, so the kubelet has not confirmed the containers " +
				"are gone. Check whether its node is still reporting — a pod on an unreachable node " +
				"stays like this until the node returns or is removed.",
		}}
	}

	return []PodFinding{{
		Severity: SeverityWarning,
		Title:    "Deletion is being held open",
		Detail: fmt.Sprintf("Requested %s ago, and still registered: %s.",
			roundDuration(elapsed), strings.Join(holders, ", ")),
		Advice: "A finalizer is a controller saying it is not finished cleaning up. Deleting the pod " +
			"again will not help — the delete already happened, and what is outstanding is that " +
			"promise. Find whether the controller behind that name is still running; removing the " +
			"finalizer by hand abandons whatever it was going to tidy.",
	}}
}

// roundDuration renders an elapsed time at a resolution worth reading.
func roundDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return d.Round(time.Second).String()
	case d < time.Hour:
		return d.Round(time.Minute).String()
	default:
		return d.Round(time.Hour).String()
	}
}

// sandboxFinding reports a pod stuck before its containers were ever started.
//
// PodReadyToStartContainers covers the sandbox, the network and the volumes —
// everything that has to exist before an image is even pulled. When it is
// False the problem is CNI or volume attachment, and the container's own
// state says "Waiting" with a reason about the image, which sends people to
// investigate a registry that is working perfectly.
//
// That distinction saves the wrong investigation entirely, and no client
// surfaces the condition at all.
func sandboxFinding(pod Pod) []PodFinding {
	condition, ok := pod.Condition("PodReadyToStartContainers")
	if !ok || condition.True() {
		return nil
	}
	// A pod that has not been scheduled has nowhere to build a sandbox, and
	// the unschedulable finding already explains that better.
	if !pod.IsScheduled() {
		return nil
	}

	detail := "The sandbox, its network or its volumes are not ready yet."
	if condition.Message != "" {
		detail = condition.Message
	}

	return []PodFinding{{
		Severity: SeverityWarning,
		Title:    "Stuck before the containers could start",
		Detail:   detail,
		Advice: "This is below the containers: the network plugin has not attached the pod, or a " +
			"volume has not mounted. Whatever the container states say about images is downstream of " +
			"it — check the CNI and the volume attachments on this node rather than the registry.",
	}}
}

// resizeFindings reports an in-place resource change that did not take.
//
// In-place vertical scaling went stable in 1.35 and brought a new, silent
// failure: the spec says four CPUs, the container is running on one, and
// nothing in any existing UI shows the divergence. The two conditions need
// OPPOSITE responses — Infeasible means this node can never satisfy it and
// the request must change; Deferred means it might, once something frees up.
func resizeFindings(pod Pod) []PodFinding {
	if pending, ok := pod.Condition("PodResizePending"); ok && !pending.True() {
		infeasible := pending.Reason == "Infeasible"

		advice := "The node may be able to satisfy this later, once something frees up. It will be " +
			"applied without a restart when that happens."
		if infeasible {
			advice = "This node can never satisfy the new size, so waiting will not help. Lower the " +
				"request, or move the pod to a node that can."
		}

		detail := fmt.Sprintf("A resize is pending (%s).", strings.ToLower(pending.Reason))
		if pending.Message != "" {
			detail = pending.Message
		}

		return []PodFinding{{
			Severity: severityIf(infeasible, SeverityWarning, SeverityInfo),
			Title:    "Resource change has not been applied",
			Detail:   detail,
			Advice:   advice,
		}}
	}

	if progress, ok := pod.Condition("PodResizeInProgress"); ok && progress.Reason == "Error" {
		detail := "A resize was accepted but failed while being applied."
		if progress.Message != "" {
			detail = progress.Message
		}

		return []PodFinding{{
			Severity: SeverityWarning,
			Title:    "Resource change failed while applying",
			Detail:   detail,
			Advice: "The pod is running on neither the old size nor the new one reliably. Check the " +
				"kubelet on this node; the spec and the container's actual limits have diverged.",
		}}
	}

	return nil
}

// severityIf picks between two severities.
func severityIf(condition bool, whenTrue, whenFalse Severity) Severity {
	if condition {
		return whenTrue
	}
	return whenFalse
}

// crashLoopFindings explains the wait an operator is looking at.
//
// CrashLoopBackOff is the most-seen and least-understood status in
// Kubernetes. It is not an error state — nothing is broken about the backoff
// itself — it is the kubelet deliberately waiting before trying again, and
// the wait DOUBLES: ten seconds, twenty, forty, up to a five-minute cap. Two
// consequences nobody is told, and both change what somebody does next.
//
// First, a pod that has been crash-looping for an hour is being retried every
// five minutes, so a fix will appear to do nothing for up to five minutes
// after it lands. People conclude the fix did not work and change something
// else. Second, the counter resets only after the container has run cleanly
// for ten minutes — restarting the pod resets it immediately, which is the
// legitimate reason to do that and is otherwise cargo-culted.
//
// The exact time of the next attempt is NOT computed. It depends on how many
// consecutive failures preceded the current one, which the API does not
// report, and inventing a countdown that was wrong would be worse than
// explaining the rule that governs it.
func crashLoopFindings(pod Pod) []PodFinding {
	looping := make([]string, 0, 2)
	for _, container := range pod.Containers() {
		if container.State == ContainerStateWaiting && container.Reason == "CrashLoopBackOff" {
			looping = append(looping, container.Name)
		}
	}
	if len(looping) == 0 {
		return nil
	}

	return []PodFinding{{
		Severity: SeverityCritical,
		Title:    "Backing off before trying again",
		Detail: fmt.Sprintf("%s %s crash-looping, so the kubelet is waiting between attempts.",
			strings.Join(looping, ", "), agrees(len(looping), "is", "are")),
		Advice: "The wait doubles each time — ten seconds, twenty, forty, up to five minutes — so a " +
			"fix can take that long to be tried and will look like it did nothing. The counter only " +
			"resets after ten minutes of clean running, which is why deleting the pod after a fix " +
			"gets you an answer sooner. The previous exit above says why it is dying.",
	}}
}
