package domain

import (
	"strings"
	"testing"
)

func boolOf(value bool) *bool  { return &value }
func intOf(value int64) *int64 { return &value }

// securedPod builds a pod with one container carrying the given security
// context, in the namespace and name given.
func securedPod(t *testing.T, name string, pod PodSecurity, containers ...Container) Pod {
	t.Helper()

	built, err := NewPod(PodSpec{
		Name:       name,
		Namespace:  NamespaceName("shop"),
		ClusterID:  ClusterID("dev"),
		Phase:      PodPhaseRunning,
		NodeName:   "node-1",
		Security:   pod,
		Containers: containers,
	})
	if err != nil {
		t.Fatalf("NewPod() error = %v", err)
	}
	return built
}

func container(name string, security ContainerSecurity) Container {
	return Container{Name: name, Image: "app:1", Security: security}
}

// findingByID returns the finding with that id, or fails.
func findingByID(t *testing.T, findings []Finding, id string) Finding {
	t.Helper()
	for _, finding := range findings {
		if finding.ID == id {
			return finding
		}
	}
	t.Fatalf("no finding %q in %d findings", id, len(findings))
	return Finding{}
}

// TestSecurityFindingsReportOnlyDeliberateActs is the rule that keeps this
// category worth reading.
//
// Every check fires on something an operator WROTE. An unstated field is the
// operator not having said, which is not the same as choosing the unsafe
// thing — and a category that lights up on every pod in the cluster teaches
// everyone to ignore it, which is worse than not having it.
func TestSecurityFindingsReportOnlyDeliberateActs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		security ContainerSecurity
		wantIDs  []string
	}{
		{
			name:     "a container that states nothing is reported for nothing",
			security: ContainerSecurity{},
		},
		{
			name:     "an explicit false is not a finding either",
			security: ContainerSecurity{Privileged: boolOf(false), AllowPrivilegeEscalation: boolOf(false)},
		},
		{
			name:     "privileged is reported",
			security: ContainerSecurity{Privileged: boolOf(true)},
			wantIDs:  []string{"security:privileged"},
		},
		{
			name:     "a written allowPrivilegeEscalation is reported",
			security: ContainerSecurity{AllowPrivilegeEscalation: boolOf(true)},
			wantIDs:  []string{"security:escalation"},
		},
		{
			name:     "a dangerous capability is reported",
			security: ContainerSecurity{AddedCapabilities: []string{"SYS_ADMIN"}},
			wantIDs:  []string{"security:capabilities"},
		},
		{
			name: "an ordinary capability is not",
			// The whole category dies if it fires on this. Binding port 80 is
			// not a security incident.
			security: ContainerSecurity{AddedCapabilities: []string{"NET_BIND_SERVICE", "CHOWN"}},
		},
		{
			name:     "UID 0 is reported",
			security: ContainerSecurity{RunAsUser: intOf(0)},
			wantIDs:  []string{"security:root"},
		},
		{
			name:     "runAsNonRoot false is reported",
			security: ContainerSecurity{RunAsNonRoot: boolOf(false)},
			wantIDs:  []string{"security:root"},
		},
		{
			name: "a non-zero UID is not",
			// The common, correct case. It must be silent.
			security: ContainerSecurity{RunAsUser: intOf(1000), RunAsNonRoot: boolOf(true)},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			pod := securedPod(t, "web-0", PodSecurity{}, container("app", test.security))
			findings := securityFindings([]Pod{pod})

			ids := make([]string, 0, len(findings))
			for _, finding := range findings {
				ids = append(ids, finding.ID)
			}
			if strings.Join(ids, ",") != strings.Join(test.wantIDs, ",") {
				t.Fatalf("findings = %v, want %v", ids, test.wantIDs)
			}
			for _, finding := range findings {
				if finding.Category != CategoryFindingSecurity {
					t.Errorf("%s category = %q, want Security", finding.ID, finding.Category)
				}
			}
		})
	}
}

// TestPrivilegedIsReportedOnceRatherThanThreeTimes pins the else-if.
//
// A privileged container already has every capability and every escalation
// there is. Reporting it under all three rules would put one pod in three
// rows and treble the apparent size of the problem.
func TestPrivilegedIsReportedOnceRatherThanThreeTimes(t *testing.T) {
	t.Parallel()

	pod := securedPod(t, "agent-0", PodSecurity{}, container("app", ContainerSecurity{
		Privileged:               boolOf(true),
		AllowPrivilegeEscalation: boolOf(true),
		AddedCapabilities:        []string{"SYS_ADMIN"},
	}))

	findings := securityFindings([]Pod{pod})

	if len(findings) != 1 || findings[0].ID != "security:privileged" {
		ids := make([]string, 0, len(findings))
		for _, finding := range findings {
			ids = append(ids, finding.ID)
		}
		t.Fatalf("findings = %v, want only security:privileged", ids)
	}
}

// TestHostNamespacesAreNamedIndividually — three different exposures, and an
// operator reading the row needs to know which.
func TestHostNamespacesAreNamedIndividually(t *testing.T) {
	t.Parallel()

	pod := securedPod(t, "cni-0", PodSecurity{HostNetwork: true, HostPID: true},
		container("app", ContainerSecurity{}))

	finding := findingByID(t, securityFindings([]Pod{pod}), "security:hostnamespace")

	if len(finding.Subjects) != 1 {
		t.Fatalf("subjects = %d, want 1", len(finding.Subjects))
	}
	detail := finding.Subjects[0].Detail
	if !strings.Contains(detail, "network") || !strings.Contains(detail, "PID") {
		t.Errorf("detail = %q, want it to name both namespaces", detail)
	}
	if strings.Contains(detail, "IPC") {
		t.Errorf("detail = %q, names a namespace the pod does not share", detail)
	}
	if !strings.Contains(detail, "namespaces") {
		t.Errorf("detail = %q, want the plural for two", detail)
	}
}

// TestSecurityFindingsCountPodsNotContainers — the number has to match what an
// operator would go and change.
func TestSecurityFindingsCountPodsNotContainers(t *testing.T) {
	t.Parallel()

	pod := securedPod(t, "web-0", PodSecurity{},
		container("app", ContainerSecurity{Privileged: boolOf(true)}),
		container("sidecar", ContainerSecurity{Privileged: boolOf(true)}))

	finding := findingByID(t, securityFindings([]Pod{pod}), "security:privileged")

	if finding.Count != 1 {
		t.Errorf("Count = %d, want 1 — it is one pod to fix", finding.Count)
	}
	if len(finding.Subjects) != 1 {
		t.Fatalf("subjects = %d, want 1", len(finding.Subjects))
	}
	detail := finding.Subjects[0].Detail
	if !strings.Contains(detail, "app") || !strings.Contains(detail, "sidecar") {
		t.Errorf("detail = %q, want both containers named", detail)
	}
}

// TestPodSteersOwnPodsAreNotReported — a node shell IS a privileged pod
// sharing the host's namespaces; that is what makes it a node shell.
//
// Without this the category fires every time an operator opens one, about a
// pod they created seconds ago and are looking at, which deletes itself when
// they close the pane.
func TestPodSteersOwnPodsAreNotReported(t *testing.T) {
	t.Parallel()

	shell, err := NewPod(PodSpec{
		Name:       "podsteer-node-shell-abc",
		Namespace:  NamespaceName("kube-system"),
		ClusterID:  ClusterID("dev"),
		Phase:      PodPhaseRunning,
		NodeName:   "node-1",
		Labels:     map[string]string{"podsteer.io/purpose": "node-shell"},
		Security:   PodSecurity{HostNetwork: true, HostPID: true, HostIPC: true},
		Containers: []Container{container("shell", ContainerSecurity{Privileged: boolOf(true)})},
	})
	if err != nil {
		t.Fatalf("NewPod() error = %v", err)
	}

	if findings := securityFindings([]Pod{shell}); len(findings) != 0 {
		t.Fatalf("PodSteer reported %d findings about its own pod", len(findings))
	}
}

// TestSomebodyElsesPrivilegedPodIsStillReported is the other half of the
// exclusion above: it is narrow, and it is not a trusted-namespace allowance.
func TestSomebodyElsesPrivilegedPodIsStillReported(t *testing.T) {
	t.Parallel()

	agent, err := NewPod(PodSpec{
		Name:       "cni-plugin-xyz",
		Namespace:  NamespaceName("kube-system"),
		ClusterID:  ClusterID("dev"),
		Phase:      PodPhaseRunning,
		NodeName:   "node-1",
		Labels:     map[string]string{"k8s-app": "cni"},
		Containers: []Container{container("agent", ContainerSecurity{Privileged: boolOf(true)})},
	})
	if err != nil {
		t.Fatalf("NewPod() error = %v", err)
	}

	if findings := securityFindings([]Pod{agent}); len(findings) != 1 {
		t.Fatalf("findings = %d, want 1 — kube-system is not an exemption", len(findings))
	}
}

// TestSecurityFindingsAreStableAcrossReads — the alert diff compares one
// assessment to the last, so a finding whose subjects reshuffle between reads
// would be announced as new every tick.
func TestSecurityFindingsAreStableAcrossReads(t *testing.T) {
	t.Parallel()

	pods := []Pod{
		securedPod(t, "zulu", PodSecurity{}, container("app", ContainerSecurity{Privileged: boolOf(true)})),
		securedPod(t, "alpha", PodSecurity{}, container("app", ContainerSecurity{Privileged: boolOf(true)})),
		securedPod(t, "mike", PodSecurity{}, container("app", ContainerSecurity{Privileged: boolOf(true)})),
	}

	first := findingByID(t, securityFindings(pods), "security:privileged")
	// The same pods, read back in a different order — which is what a map
	// iteration upstream produces.
	reversed := []Pod{pods[2], pods[0], pods[1]}
	second := findingByID(t, securityFindings(reversed), "security:privileged")

	if len(first.Subjects) != 3 {
		t.Fatalf("subjects = %d, want 3", len(first.Subjects))
	}
	for at := range first.Subjects {
		if first.Subjects[at].Name != second.Subjects[at].Name {
			t.Fatalf("subject %d = %q then %q — the order is not stable",
				at, first.Subjects[at].Name, second.Subjects[at].Name)
		}
	}
}

// TestSecurityFindingsNeverDegradeTheClusterVerdict is the guard on the
// severity choice, and it matters more than any individual rule.
//
// SeverityWarning marks a cluster DEGRADED. Every real cluster runs privileged
// CNI, CSI and monitoring agents that share host namespaces — that is what
// those agents are for — so a warning here would paint every cluster in the
// world permanently yellow for having a network plugin. The grade would then
// mean nothing, which costs more than these findings are worth.
func TestSecurityFindingsNeverDegradeTheClusterVerdict(t *testing.T) {
	t.Parallel()

	// One pod taking every privilege there is: the worst case this file can
	// produce.
	worst := securedPod(t, "cni-0",
		PodSecurity{HostNetwork: true, HostPID: true, HostIPC: true},
		container("agent", ContainerSecurity{
			Privileged:               boolOf(true),
			AllowPrivilegeEscalation: boolOf(true),
			RunAsUser:                intOf(0),
			AddedCapabilities:        []string{"SYS_ADMIN", "NET_ADMIN"},
		}))

	findings := securityFindings([]Pod{worst})
	if len(findings) == 0 {
		t.Fatal("the worst case produced no findings at all")
	}

	for _, finding := range findings {
		if finding.Severity != SeverityInfo {
			t.Errorf("%s severity = %q — anything above info degrades every cluster that runs a CNI",
				finding.ID, finding.Severity)
		}
	}

	if got := grade(findings, nil); got != HealthHealthy {
		t.Fatalf("grade = %q, want healthy — a cluster is not degraded for having a network plugin", got)
	}
}
