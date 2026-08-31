package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

var assessNow = time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)

func findingTitled(findings []domain.PodFinding, substring string) (domain.PodFinding, bool) {
	for _, finding := range findings {
		if strings.Contains(finding.Title, substring) {
			return finding, true
		}
	}
	return domain.PodFinding{}, false
}

func TestProbeKillsAfterCountsTheWholeFailureBudget(t *testing.T) {
	t.Parallel()

	// The number everybody is surprised by. A 30s delay with the DEFAULTS is
	// not a thirty-second grace: the probe waits 30s, then has to fail three
	// times ten seconds apart, so the container has a full minute.
	probe := domain.Probe{InitialDelaySeconds: 30}
	if got := probe.KillsAfter(); got != 60*time.Second {
		t.Errorf("KillsAfter() = %v, want 60s — delay plus threshold × period at their defaults", got)
	}

	explicit := domain.Probe{InitialDelaySeconds: 5, PeriodSeconds: 5, FailureThreshold: 2}
	if got := explicit.KillsAfter(); got != 15*time.Second {
		t.Errorf("KillsAfter() = %v, want 15s", got)
	}

	if got := (domain.Probe{}).KillsAfter(); got != 0 {
		t.Errorf("KillsAfter() with no probe = %v, want 0", got)
	}
}

func TestAssessPodPredictsALivenessProbeAboutToKillASlowStart(t *testing.T) {
	t.Parallel()

	// Budget is 10 + 3×10 = 40s. The container took 36s to come up, which is
	// 90% of it — healthy right now, one slow start from a boot loop.
	spec := newPodSpec()
	spec.NodeName = "node-1"
	spec.Containers = []domain.Container{{
		Name:      "app",
		State:     domain.ContainerStateRunning,
		StartedAt: assessNow.Add(-36 * time.Second),
		Liveness:  domain.Probe{InitialDelaySeconds: 10},
	}}

	pod, err := domain.NewPod(spec)
	if err != nil {
		t.Fatalf("NewPod() error = %v", err)
	}

	finding, ok := findingTitled(domain.AssessPod(pod, assessNow), "close to killing")
	if !ok {
		t.Fatal("a container starting inside 90% of its liveness budget was not flagged")
	}
	if !strings.Contains(finding.Advice, "startupProbe") {
		t.Errorf("advice = %q, want it to name the actual fix", finding.Advice)
	}

	// A container with a startupProbe is already protected — the liveness
	// probe does not run until it passes — so the same timings are fine.
	spec.Containers[0].Startup = domain.Probe{PeriodSeconds: 5, FailureThreshold: 30}
	guarded, _ := domain.NewPod(spec)
	if _, ok := findingTitled(domain.AssessPod(guarded, assessNow), "close to killing"); ok {
		t.Error("a container with a startupProbe must not be flagged for a slow start")
	}
}

func TestAssessPodTranslatesTheSchedulersOwnWords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		message  string
		wantSaid string
	}{
		{
			name:     "taints",
			message:  "0/3 nodes are available: 3 node(s) had untolerated taint {dedicated: gpu}.",
			wantSaid: "toleration",
		},
		{
			name:     "capacity is about requests, not free memory",
			message:  "0/3 nodes are available: 3 Insufficient cpu.",
			wantSaid: "REQUESTS",
		},
		{
			name:     "labels",
			message:  "0/3 nodes are available: 3 node(s) didn't match Pod's node affinity/selector.",
			wantSaid: "labels",
		},
		{
			name:     "a volume cannot move",
			message:  "0/3 nodes are available: 3 node(s) had volume node affinity conflict.",
			wantSaid: "pinned to a zone",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			spec := newPodSpec()
			spec.Phase = domain.PodPhasePending
			spec.Message = test.message

			pod, err := domain.NewPod(spec)
			if err != nil {
				t.Fatalf("NewPod() error = %v", err)
			}

			finding, ok := findingTitled(domain.AssessPod(pod, assessNow), "Nothing will schedule")
			if !ok {
				t.Fatalf("an unschedulable pod was not flagged; message = %q", test.message)
			}
			// The scheduler's own text is preserved verbatim: it is the
			// longer, untruncated copy that `kubectl describe` does not show.
			if finding.Detail != test.message {
				t.Errorf("detail = %q, want the scheduler's message unaltered", finding.Detail)
			}
			if !strings.Contains(finding.Advice, test.wantSaid) {
				t.Errorf("advice = %q, want it to mention %q", finding.Advice, test.wantSaid)
			}
		})
	}
}

func TestAssessPodNamesTheContainerThatCostGuaranteed(t *testing.T) {
	t.Parallel()

	// "QoS: Burstable" is already on screen. WHICH container caused it is not.
	spec := newPodSpec()
	spec.NodeName = "node-1"
	spec.QoSClass = domain.QoSBurstable
	spec.Containers = []domain.Container{
		{
			Name:     "app",
			Requests: domain.Resources{MemoryBytes: 256 << 20},
			Limits:   domain.Resources{MemoryBytes: 256 << 20},
		},
		{Name: "sidecar", Requests: domain.Resources{MemoryBytes: 64 << 20}},
	}

	pod, err := domain.NewPod(spec)
	if err != nil {
		t.Fatalf("NewPod() error = %v", err)
	}

	finding, ok := findingTitled(domain.AssessPod(pod, assessNow), "Burstable")
	if !ok {
		t.Fatal("a Burstable pod was not explained")
	}
	if !strings.Contains(finding.Detail, "sidecar") {
		t.Errorf("detail = %q, want the offending container named", finding.Detail)
	}
	if strings.Contains(finding.Detail, "app") {
		t.Errorf("detail = %q, must not blame the container that is correctly sized", finding.Detail)
	}
}

func TestAssessPodFlagsATagThatCanMove(t *testing.T) {
	t.Parallel()

	spec := newPodSpec()
	spec.NodeName = "node-1"
	spec.Containers = []domain.Container{
		// A digest is what makes a reference immutable. A specific-LOOKING
		// tag is still a pointer somebody can repoint.
		{Name: "pinned", Image: "registry/app@sha256:abc123"},
		{Name: "loose", Image: "registry/app:v1.2.3"},
	}

	pod, err := domain.NewPod(spec)
	if err != nil {
		t.Fatalf("NewPod() error = %v", err)
	}

	finding, ok := findingTitled(domain.AssessPod(pod, assessNow), "tag that can move")
	if !ok {
		t.Fatal("an image referenced by tag was not flagged")
	}
	if !strings.Contains(finding.Detail, "loose") || strings.Contains(finding.Detail, "pinned") {
		t.Errorf("detail = %q, want only the unpinned container named", finding.Detail)
	}
}

func TestAssessPodStaysQuietOnAHealthyPod(t *testing.T) {
	t.Parallel()

	// Everything right: owned, Guaranteed, digest-pinned, no probe traps.
	spec := newPodSpec()
	spec.NodeName = "node-1"
	spec.QoSClass = domain.QoSGuaranteed
	spec.Owners = []domain.OwnerReference{
		{Kind: "ReplicaSet", Name: "app-7d9f", Controller: true},
	}
	spec.Containers = []domain.Container{{
		Name:     "app",
		Image:    "registry/app@sha256:abc123",
		State:    domain.ContainerStateRunning,
		Requests: domain.Resources{MemoryBytes: 256 << 20},
		Limits:   domain.Resources{MemoryBytes: 256 << 20},
	}}

	pod, err := domain.NewPod(spec)
	if err != nil {
		t.Fatalf("NewPod() error = %v", err)
	}

	if findings := domain.AssessPod(pod, assessNow); len(findings) != 0 {
		t.Errorf("a correctly configured pod produced %d findings, want none: %+v", len(findings), findings)
	}
}

func TestAssessPodNamesWhatIsHoldingADeletionOpen(t *testing.T) {
	t.Parallel()

	spec := newPodSpec()
	spec.NodeName = "node-1"
	spec.DeletedAt = assessNow.Add(-3 * time.Hour)
	spec.Finalizers = []string{"kubernetes.io/pvc-protection", "example.com/drain"}

	pod, err := domain.NewPod(spec)
	if err != nil {
		t.Fatalf("NewPod() error = %v", err)
	}

	finding, ok := findingTitled(domain.AssessPod(pod, assessNow), "held open")
	if !ok {
		t.Fatal("a pod stuck terminating for three hours was not flagged")
	}
	// The field kubectl describe never prints, which is the whole point.
	if !strings.Contains(finding.Detail, "example.com/drain") {
		t.Errorf("detail = %q, want the finalizers named", finding.Detail)
	}
	if !strings.Contains(finding.Advice, "will not help") {
		t.Errorf("advice = %q, want it to say deleting again is not the fix", finding.Advice)
	}
}

func TestAssessPodDistinguishesAnUnconfirmedDeletionFromAHeldOne(t *testing.T) {
	t.Parallel()

	// Deleted, past its grace, and nothing registered. Not a finalizer
	// problem: the kubelet has not confirmed the containers are gone, which
	// usually means its node stopped reporting. Opposite investigation.
	spec := newPodSpec()
	spec.NodeName = "node-1"
	spec.DeletedAt = assessNow.Add(-10 * time.Minute)

	pod, err := domain.NewPod(spec)
	if err != nil {
		t.Fatalf("NewPod() error = %v", err)
	}

	finding, ok := findingTitled(domain.AssessPod(pod, assessNow), "nothing holding it")
	if !ok {
		t.Fatal("a deletion with no finalizer was not distinguished")
	}
	if !strings.Contains(finding.Advice, "node") {
		t.Errorf("advice = %q, want it to point at the node", finding.Advice)
	}
}

func TestAssessPodIsPatientWithANormalDeletion(t *testing.T) {
	t.Parallel()

	// The default grace period is thirty seconds. A pod shutting down now is
	// not a problem, and flagging every terminating pod would make the whole
	// section noise during any rollout.
	spec := newPodSpec()
	spec.NodeName = "node-1"
	spec.DeletedAt = assessNow.Add(-5 * time.Second)
	spec.Finalizers = []string{"kubernetes.io/pvc-protection"}

	pod, err := domain.NewPod(spec)
	if err != nil {
		t.Fatalf("NewPod() error = %v", err)
	}

	if _, ok := findingTitled(domain.AssessPod(pod, assessNow), "held open"); ok {
		t.Error("a pod five seconds into a normal shutdown was reported as stuck")
	}
}

func TestAssessPodSeparatesASandboxProblemFromAnImageProblem(t *testing.T) {
	t.Parallel()

	// The container says "Waiting" with an image reason, which sends people
	// to check a registry that is working perfectly. The real problem is
	// below the containers: the CNI has not attached the pod.
	spec := newPodSpec()
	spec.NodeName = "node-1"
	spec.Conditions = []domain.PodCondition{
		{Type: "PodReadyToStartContainers", Status: "False", Message: "network is not ready: cni plugin not initialized"},
	}
	spec.Containers = []domain.Container{
		{Name: "app", State: domain.ContainerStateWaiting, Reason: "ImagePullBackOff"},
	}

	pod, err := domain.NewPod(spec)
	if err != nil {
		t.Fatalf("NewPod() error = %v", err)
	}

	finding, ok := findingTitled(domain.AssessPod(pod, assessNow), "before the containers")
	if !ok {
		t.Fatal("a pod blocked below its containers was not distinguished")
	}
	if !strings.Contains(finding.Advice, "registry") {
		t.Errorf("advice = %q, want it to say the registry is not the problem", finding.Advice)
	}
}

func TestAssessPodTellsInfeasibleResizesFromDeferredOnes(t *testing.T) {
	t.Parallel()

	// The two need OPPOSITE responses: one means change the request, the
	// other means wait. Reporting them the same way is worse than silence.
	tests := []struct {
		name     string
		reason   string
		wantSaid string
		want     domain.Severity
	}{
		{"infeasible", "Infeasible", "will not help", domain.SeverityWarning},
		{"deferred", "Deferred", "frees up", domain.SeverityInfo},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			spec := newPodSpec()
			spec.NodeName = "node-1"
			spec.Conditions = []domain.PodCondition{
				{Type: "PodResizePending", Status: "False", Reason: test.reason},
			}

			pod, err := domain.NewPod(spec)
			if err != nil {
				t.Fatalf("NewPod() error = %v", err)
			}

			finding, ok := findingTitled(domain.AssessPod(pod, assessNow), "has not been applied")
			if !ok {
				t.Fatalf("a pending resize (%s) was not reported", test.reason)
			}
			if !strings.Contains(finding.Advice, test.wantSaid) {
				t.Errorf("advice = %q, want it to mention %q", finding.Advice, test.wantSaid)
			}
			if finding.Severity != test.want {
				t.Errorf("severity = %q, want %q", finding.Severity, test.want)
			}
		})
	}
}

func TestAssessPodCatchesLivenessAndReadinessSharingAnEndpoint(t *testing.T) {
	t.Parallel()

	// The trap: when both probes check the same endpoint and that endpoint
	// reports on a dependency, the dependency going down does not merely take
	// the pod out of the Service — it restarts a process that was working.
	spec := newPodSpec()
	spec.NodeName = "node-1"
	spec.Containers = []domain.Container{{
		Name:      "app",
		State:     domain.ContainerStateRunning,
		Liveness:  domain.Probe{Target: "http :8080/health", PeriodSeconds: 10, TimeoutSeconds: 1},
		Readiness: domain.Probe{Target: "http :8080/health", PeriodSeconds: 5, TimeoutSeconds: 1},
	}}

	pod, err := domain.NewPod(spec)
	if err != nil {
		t.Fatalf("NewPod() error = %v", err)
	}

	finding, ok := findingTitled(domain.AssessPod(pod, assessNow), "same thing")
	if !ok {
		t.Fatal("two probes on one endpoint were not flagged")
	}
	if !strings.Contains(finding.Advice, "different questions") {
		t.Errorf("advice = %q, want it to explain what each probe is for", finding.Advice)
	}
}

func TestAssessPodAllowsProbesOnDifferentEndpoints(t *testing.T) {
	t.Parallel()

	// Different endpoints is the correct configuration and must be silent —
	// the timings differing is not what matters.
	spec := newPodSpec()
	spec.NodeName = "node-1"
	spec.Containers = []domain.Container{{
		Name:      "app",
		State:     domain.ContainerStateRunning,
		Liveness:  domain.Probe{Target: "http :8080/live", PeriodSeconds: 10, TimeoutSeconds: 1},
		Readiness: domain.Probe{Target: "http :8080/ready", PeriodSeconds: 5, TimeoutSeconds: 1},
	}}

	pod, err := domain.NewPod(spec)
	if err != nil {
		t.Fatalf("NewPod() error = %v", err)
	}

	if _, ok := findingTitled(domain.AssessPod(pod, assessNow), "same thing"); ok {
		t.Error("correctly separated probes were flagged")
	}
}

func TestAssessPodExplainsTheCrashLoopWaitWithoutInventingACountdown(t *testing.T) {
	t.Parallel()

	spec := newPodSpec()
	spec.NodeName = "node-1"
	spec.Containers = []domain.Container{
		{Name: "app", State: domain.ContainerStateWaiting, Reason: "CrashLoopBackOff"},
	}

	pod, err := domain.NewPod(spec)
	if err != nil {
		t.Fatalf("NewPod() error = %v", err)
	}

	finding, ok := findingTitled(domain.AssessPod(pod, assessNow), "Backing off")
	if !ok {
		t.Fatal("a crash-looping container was not explained")
	}
	// The two consequences nobody is told, both of which change what somebody
	// does next: the wait grows to five minutes, and it resets after ten
	// minutes of clean running.
	if !strings.Contains(finding.Advice, "five minutes") {
		t.Errorf("advice = %q, want the cap named", finding.Advice)
	}
	if !strings.Contains(finding.Advice, "ten minutes") {
		t.Errorf("advice = %q, want the reset rule named", finding.Advice)
	}
}

func TestAssessPodLeadsWithTheWorstThing(t *testing.T) {
	t.Parallel()

	// A pod that is crash-looping AND running a mutable tag AND bare. Without
	// ranking, the order the rules happen to run in decides what somebody
	// reads first, which is an implementation detail making a product
	// decision.
	spec := newPodSpec()
	spec.NodeName = "node-1"
	spec.Containers = []domain.Container{{
		Name:   "app",
		Image:  "registry/app:latest",
		State:  domain.ContainerStateWaiting,
		Reason: "CrashLoopBackOff",
	}}

	pod, err := domain.NewPod(spec)
	if err != nil {
		t.Fatalf("NewPod() error = %v", err)
	}

	findings := domain.AssessPod(pod, assessNow)
	if len(findings) < 2 {
		t.Fatalf("expected several findings, got %d", len(findings))
	}
	if findings[0].Severity != domain.SeverityCritical {
		t.Errorf("first finding = %q (%s), want the critical one first",
			findings[0].Title, findings[0].Severity)
	}

	// And severity must never increase as the list goes down.
	for i := 1; i < len(findings); i++ {
		if findings[i-1].Severity == domain.SeverityInfo && findings[i].Severity != domain.SeverityInfo {
			t.Errorf("finding %d (%s) outranks the one above it (%s)",
				i, findings[i].Severity, findings[i-1].Severity)
		}
	}
}
