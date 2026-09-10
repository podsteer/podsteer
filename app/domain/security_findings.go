package domain

import (
	"fmt"
	"sort"
	"strings"
)

// podsteerPurposeLabel marks a pod PodSteer created itself.
//
// A node shell IS a privileged pod sharing the host's namespaces — that is
// what makes it a node shell — and reporting it would fire a security finding
// every time an operator opened one, about a pod they are looking at, that
// they created seconds ago, and that deletes itself when the pane closes. The
// label is written by the adapter (see nodeshell.go and clustershell.go);
// matching on it here is the domain declining to report its own footprints.
//
// It is deliberately narrow: only PodSteer's own label, never a general
// "trusted namespace" allowance. kube-system is full of pods that genuinely
// need host access and an operator is entitled to see them listed.
const podsteerPurposeLabel = "podsteer.io/purpose"

// securityFindings reports the privileges a workload's own spec takes.
//
// WHAT THIS IS AND IS NOT. Every rule below reads a field an operator wrote
// into a pod spec. Nothing is scored, nothing is weighted into a percentage,
// and nothing depends on an advisory database — which is exactly what makes
// these safe for PodSteer to own where CVE data is not (see security.go, and
// vulnerability.go for the other half). A privileged container will mean the
// same thing in five years as it does today.
//
// THE RULES ARE CHOSEN TO BE RARE. A category that lights up on every pod
// teaches an operator to ignore it, and the evidence on scanner fatigue is
// unambiguous — a report of three thousand findings is a report nobody reads.
// So each rule fires on a DELIBERATE act with no benign default: privileged
// mode, a shared host namespace, a written allowPrivilegeEscalation, a
// dangerous capability, a UID pinned to 0. Unstated fields are never reported,
// because "the operator did not say" is not "the operator chose the unsafe
// thing" — see ContainerSecurity's own comments.
//
// EVERY RULE IS INFO, AND THAT IS NOT TIMIDITY. SeverityWarning marks a
// cluster DEGRADED (see grade), and every real cluster runs privileged CNI,
// CSI and monitoring agents that share host namespaces because that is what
// those agents are for. Warning severity would therefore paint every cluster
// in the world permanently yellow, for having a network plugin — a verdict
// that is false, that cannot be acted on, and that would teach operators the
// grade means nothing.
//
// These are POSTURE, not incidents. The overview's verdict answers "is
// something wrong right now"; a privileged DaemonSet that has run since the
// cluster was built is not that. It belongs in the notes, where it is legible
// without crying wolf, and its proper home is a security view that ranks it
// against everything else — not a health grade with two states.
//
// DELIBERATELY ABSENT: anything reading pod.Spec.Volumes. hostPath mounts and
// a mounted docker.sock are exactly the kind of finding that belongs here, and
// they cannot be computed correctly today — the watch store strips volumes, so
// the rule would be right on some clusters and silently blank on others. That
// is a worse failure than not having the rule. Restoring it is a deliberate
// decision to stop stripping, with its own reasoning, not a line to add here.
func securityFindings(pods []Pod) []Finding {
	privileged := newSecuritySubjects()
	hostNamespaces := newSecuritySubjects()
	escalation := newSecuritySubjects()
	capabilities := newSecuritySubjects()
	root := newSecuritySubjects()

	for _, pod := range pods {
		if pod.Labels()[podsteerPurposeLabel] != "" {
			continue
		}

		if shared := pod.Security().SharesHostNamespace(); len(shared) > 0 {
			noun := "namespace"
			if len(shared) > 1 {
				noun = "namespaces"
			}
			hostNamespaces.add(pod, fmt.Sprintf("shares the node's %s %s",
				strings.Join(shared, ", "), noun))
		}

		for _, container := range pod.Containers() {
			security := container.Security
			switch {
			case security.IsPrivileged():
				privileged.add(pod, container.Name+" runs privileged")
			// ELSE-IF, not a second rule: a privileged container already has
			// every capability and every escalation there is, so also
			// reporting it for allowPrivilegeEscalation would be two findings
			// about one fact and would put the same pod in two rows.
			case security.AllowsEscalation():
				escalation.add(pod, container.Name+" may escalate privileges")
			}

			if found := security.DangerousCapabilities(); len(found) > 0 && !security.IsPrivileged() {
				capabilities.add(pod, fmt.Sprintf("%s adds %s",
					container.Name, strings.Join(found, ", ")))
			}
			if security.RunsAsRoot() {
				root.add(pod, container.Name+" runs as UID 0")
			}
		}
	}

	findings := make([]Finding, 0, 5)

	findings = privileged.finish(findings, Finding{
		ID:       "security:privileged",
		Summary:  "%s running with privileged containers",
		Severity: SeverityInfo,
		Title:    "Privileged containers",
		Advice: "A privileged container has the node's full capability set and can reach its " +
			"devices, so a compromise of the process is a compromise of the node. Most workloads " +
			"that ask for it need one specific capability instead; the ones that genuinely need " +
			"privileged mode are CNI, CSI and monitoring agents.",
	})

	findings = hostNamespaces.finish(findings, Finding{
		ID:       "security:hostnamespace",
		Summary:  "%s sharing a namespace with the node they run on",
		Severity: SeverityInfo,
		Title:    "Pods sharing a host namespace",
		Advice: "Sharing the node's network exposes every port the pod binds on the node itself " +
			"and bypasses NetworkPolicy. Sharing its PID namespace lets the pod see and signal " +
			"every process on the node, including other tenants'. None of the three has a benign " +
			"default, so each one here was asked for.",
	})

	findings = capabilities.finish(findings, Finding{
		ID:       "security:capabilities",
		Summary:  "%s whose containers add capabilities beyond the default set",
		Severity: SeverityInfo,
		Title:    "Containers adding dangerous capabilities",
		Advice: "These are the additions that change what a compromise reaches — SYS_ADMIN is a " +
			"synonym for root, SYS_MODULE makes it the kernel, SYS_PTRACE reads other processes' " +
			"memory. Ordinary additions such as NET_BIND_SERVICE are not reported. Drop what is " +
			"not needed, and prefer one narrow capability to privileged mode.",
	})

	findings = escalation.finish(findings, Finding{
		ID:       "security:escalation",
		Summary:  "%s that state allowPrivilegeEscalation: true",
		Severity: SeverityInfo,
		Title:    "Containers allowed to escalate privileges",
		Advice: "allowPrivilegeEscalation was written as true here, which lets a process gain more " +
			"privileges than its parent — through a setuid binary, typically. Only containers " +
			"that state it are listed: an unset field is the operator not having said, and is " +
			"not reported as a choice they made.",
	})

	findings = root.finish(findings, Finding{
		ID:       "security:root",
		Summary:  "%s whose spec pins a container to root",
		Severity: SeverityInfo,
		Title:    "Containers pinned to root",
		Advice: "The spec states UID 0, or states outright that a non-root user is not required. " +
			"Containers that simply do not say are NOT listed — the user then comes from the " +
			"image, and PodSteer does not read images. Setting runAsNonRoot with a UID is what " +
			"makes the answer visible in the manifest rather than buried in a layer.",
	})

	return findings
}

// securitySubjects collects one rule's affected pods, bounded and deduplicated.
//
// COUNTED BY POD, NOT BY CONTAINER, which is what makes the number match what
// an operator would go and fix: a two-container pod that is privileged twice
// is one pod to change. The detail line still names each container, so the
// finding says which.
type securitySubjects struct {
	subjects []Subject
	details  map[string][]string
	order    []string
	count    int
}

func newSecuritySubjects() *securitySubjects {
	return &securitySubjects{details: make(map[string][]string)}
}

func (s *securitySubjects) add(pod Pod, detail string) {
	key := pod.Namespace().String() + "/" + pod.Name()
	if _, seen := s.details[key]; !seen {
		s.order = append(s.order, key)
		s.count++
	}
	s.details[key] = append(s.details[key], detail)
}

// finish appends the rule's finding to findings, or nothing when it found
// nothing. The template carries everything but the parts derived from the
// subjects themselves.
func (s *securitySubjects) finish(findings []Finding, template Finding) []Finding {
	if s.count == 0 {
		return findings
	}

	// Sorted so two reads of the same cluster produce the same finding, which
	// is what lets the alert diff tell a new one from a familiar one.
	sort.Strings(s.order)

	for _, key := range s.order {
		if len(s.subjects) >= maxSubjects {
			break
		}
		namespace, name, _ := strings.Cut(key, "/")
		s.subjects = append(s.subjects, Subject{
			Kind:      "Pod",
			Namespace: NamespaceName(namespace),
			Name:      name,
			Detail:    strings.Join(s.details[key], "; "),
		})
	}

	template.Category = CategoryFindingSecurity
	// The template's Summary is a format string with one verb, so each rule
	// owns its own wording while the count stays derived from what was found.
	template.Summary = fmt.Sprintf(template.Summary, plural(s.count, "pod", "pods"))
	template.Subjects = s.subjects
	template.Count = s.count
	template.KindID = podKindID
	return append(findings, template)
}
