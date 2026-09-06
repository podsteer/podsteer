package k8s

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	clientgotesting "k8s.io/client-go/testing"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

const testShellImage = "docker.io/cloudresty/dockydeb:v1.2.28-nonroot"

// newClusterShellAdapter is newTestAdapter with the in-cluster shell registry
// initialised — that registry is not part of the zero value, and a nil map
// would panic on the first register rather than fail with a message.
func newClusterShellAdapter(id domain.ClusterID, client *fake.Clientset) *Adapter {
	adapter := newTestAdapter(id, client)
	adapter.clusterShells = clusterShells{byID: make(map[string]domain.ClusterShell)}
	return adapter
}

// TestBuildClusterShellPodSatisfiesRestrictedPodSecurity asserts every field
// Pod Security's `restricted` profile requires of a container, because the
// whole point of this shell — as against the node shell — is that it is
// ADMISSIBLE in the namespaces most likely to enforce it. Drop any one of
// these and the pod is rejected before anything starts.
func TestBuildClusterShellPodSatisfiesRestrictedPodSecurity(t *testing.T) {
	pod := buildClusterShellPod("podsteer-shell-abcde", "shop", testShellImage)

	if pod.Name != "podsteer-shell-abcde" || pod.Namespace != "shop" {
		t.Errorf("pod = %q in %q, want the caller's name and namespace", pod.Name, pod.Namespace)
	}
	if got := pod.Labels[clusterShellManagedByLabel]; got != clusterShellManagedByValue {
		t.Errorf("%s = %q, want %q", clusterShellManagedByLabel, got, clusterShellManagedByValue)
	}
	if got := pod.Labels[clusterShellPurposeLabel]; got != clusterShellPurposeValue {
		t.Errorf("%s = %q, want %q — the purpose label is what keeps a node shell out of this list", clusterShellPurposeLabel, got, clusterShellPurposeValue)
	}

	spec := pod.Spec
	if spec.SecurityContext == nil || spec.SecurityContext.RunAsNonRoot == nil || !*spec.SecurityContext.RunAsNonRoot {
		t.Error("pod securityContext.runAsNonRoot is not true — `restricted` rejects a root container outright")
	}
	if spec.SecurityContext == nil || spec.SecurityContext.SeccompProfile == nil ||
		spec.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Error("pod seccompProfile is not RuntimeDefault, which `restricted` requires")
	}
	if spec.ActiveDeadlineSeconds == nil || *spec.ActiveDeadlineSeconds != clusterShellDeadlineSeconds {
		t.Errorf("activeDeadlineSeconds = %v, want %d (the crash backstop)", spec.ActiveDeadlineSeconds, clusterShellDeadlineSeconds)
	}
	if spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("restartPolicy = %q, want Never", spec.RestartPolicy)
	}
	// The one field this deliberately sets the OPPOSITE way from the node
	// shell: this shell exists so somebody can run kubectl from inside the
	// cluster, with the namespace's own default ServiceAccount.
	if spec.AutomountServiceAccountToken == nil || !*spec.AutomountServiceAccountToken {
		t.Error("automountServiceAccountToken is not true — kubectl from inside the cluster needs the namespace's default account")
	}

	if len(spec.Containers) != 1 {
		t.Fatalf("containers = %d, want exactly one", len(spec.Containers))
	}
	c := spec.Containers[0]
	if c.Name != clusterShellContainerName {
		t.Errorf("container name = %q, want %q", c.Name, clusterShellContainerName)
	}
	if c.Image != testShellImage {
		t.Errorf("image = %q, want the caller's image", c.Image)
	}
	if !c.TTY || !c.Stdin {
		t.Errorf("tty=%v stdin=%v, want both true so the shell can be attached to", c.TTY, c.Stdin)
	}
	sc := c.SecurityContext
	if sc == nil {
		t.Fatal("container securityContext is nil — `restricted` is judged per container")
	}
	if sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot {
		t.Error("container runAsNonRoot is not true")
	}
	if sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation {
		t.Error("allowPrivilegeEscalation is not false, which `restricted` requires")
	}
	if sc.Capabilities == nil || !slices.Contains(sc.Capabilities.Drop, corev1.Capability("ALL")) {
		t.Errorf("capabilities = %+v, want ALL dropped", sc.Capabilities)
	}
	if sc.SeccompProfile == nil || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Error("container seccompProfile is not RuntimeDefault")
	}
}

// TestClusterShellPodIsNothingLikeANodeShell pins the absences. Each of these
// fields is set by buildNodeShellPod, and each would make this pod either
// inadmissible under `restricted` or a different feature entirely.
func TestClusterShellPodIsNothingLikeANodeShell(t *testing.T) {
	spec := buildClusterShellPod("podsteer-shell-abcde", "shop", testShellImage).Spec

	if spec.HostPID || spec.HostNetwork {
		t.Errorf("hostPID=%v hostNetwork=%v, want both false — this is an ORDINARY pod, not a node shell", spec.HostPID, spec.HostNetwork)
	}
	if spec.NodeName != "" {
		t.Errorf("nodeName = %q, want empty — the scheduler places this pod", spec.NodeName)
	}
	if len(spec.Tolerations) != 0 {
		t.Errorf("tolerations = %+v, want none — a blanket toleration would schedule onto nodes taints exist to protect", spec.Tolerations)
	}
	c := spec.Containers[0]
	if c.SecurityContext.Privileged != nil && *c.SecurityContext.Privileged {
		t.Error("privileged is true — an in-cluster shell is a vantage point, not a privilege")
	}
	if slices.Contains(c.Command, "nsenter") {
		t.Errorf("command = %v, want no nsenter — there is no host to enter", c.Command)
	}
}

// TestStartClusterShellCreatesRunsAndRecords covers the success path.
func TestStartClusterShellCreatesRunsAndRecords(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("create", "pods", runningReactor)
	adapter := newClusterShellAdapter("dev", client)

	shell, err := adapter.StartClusterShell(context.Background(), "dev", "shop", testShellImage)
	if err != nil {
		t.Fatalf("StartClusterShell() error = %v", err)
	}
	if !strings.HasPrefix(shell.PodName, clusterShellNamePrefix) {
		t.Errorf("pod name = %q, want the %q prefix", shell.PodName, clusterShellNamePrefix)
	}
	if shell.ContainerName != clusterShellContainerName {
		t.Errorf("container = %q, want %q", shell.ContainerName, clusterShellContainerName)
	}
	if shell.Adopted {
		t.Error("Adopted = true on a pod this session created")
	}

	if _, err := client.CoreV1().Pods("shop").Get(context.Background(), shell.PodName, metav1.GetOptions{}); err != nil {
		t.Fatalf("the in-cluster shell pod was not created: %v", err)
	}
	if live := adapter.ListClusterShells(); len(live) != 1 {
		t.Fatalf("ListClusterShells() = %d, want 1", len(live))
	}
}

// TestStartClusterShellRefusesAllNamespaces is the "there is no answer, so ask"
// rule at the adapter. NewNamespaceName("") is NamespaceAll rather than an
// error, so without this the create would land the pod in whatever the
// kubeconfig's default namespace happens to be.
func TestStartClusterShellRefusesAllNamespaces(t *testing.T) {
	client := fake.NewSimpleClientset()
	adapter := newClusterShellAdapter("dev", client)

	_, err := adapter.StartClusterShell(context.Background(), "dev", domain.NamespaceAll, testShellImage)
	if !errors.Is(err, domain.ErrShellNamespaceRequired) {
		t.Fatalf("StartClusterShell() error = %v, want ErrShellNamespaceRequired", err)
	}

	pods, listErr := client.CoreV1().Pods("").List(context.Background(), metav1.ListOptions{})
	if listErr != nil {
		t.Fatalf("listing pods: %v", listErr)
	}
	if len(pods.Items) != 0 {
		t.Fatalf("pods created = %d, want 0 — a namespace must never be guessed", len(pods.Items))
	}
}

// TestStopClusterShellDeletesThePodAndForgetsIt is the delete-on-session-end
// lifecycle: the terminal session's exit hook calls exactly this.
func TestStopClusterShellDeletesThePodAndForgetsIt(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("create", "pods", runningReactor)
	adapter := newClusterShellAdapter("dev", client)

	shell, err := adapter.StartClusterShell(context.Background(), "dev", "shop", testShellImage)
	if err != nil {
		t.Fatalf("StartClusterShell() error = %v", err)
	}

	if err := adapter.StopClusterShell(shell.ID); err != nil {
		t.Fatalf("StopClusterShell() error = %v", err)
	}

	_, err = client.CoreV1().Pods("shop").Get(context.Background(), shell.PodName, metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("pod get after stop = %v, want NotFound — the pod must be deleted when the session ends", err)
	}
	if live := adapter.ListClusterShells(); len(live) != 0 {
		t.Fatalf("ListClusterShells() = %d, want 0 after stop", len(live))
	}
}

// TestStopClusterShellIsIdempotent covers the session ending and an explicit
// stop both reaching the same shell.
func TestStopClusterShellIsIdempotent(t *testing.T) {
	adapter := newClusterShellAdapter("dev", fake.NewSimpleClientset())
	if err := adapter.StopClusterShell("nope"); err != nil {
		t.Fatalf("StopClusterShell() on an unknown id = %v, want nil", err)
	}
}

// TestStopAllClusterShellsDeletesEveryPod is the shutdown sweep: OnShutdown
// calls this beside StopAllNodeShells, and nothing PodSteer created may be
// left running in somebody's namespace.
func TestStopAllClusterShellsDeletesEveryPod(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("create", "pods", runningReactor)
	adapter := newClusterShellAdapter("dev", client)

	first, err := adapter.StartClusterShell(context.Background(), "dev", "shop", testShellImage)
	if err != nil {
		t.Fatalf("first StartClusterShell() error = %v", err)
	}
	second, err := adapter.StartClusterShell(context.Background(), "dev", "shop", testShellImage)
	if err != nil {
		t.Fatalf("second StartClusterShell() error = %v", err)
	}

	adapter.StopAllClusterShells()

	if live := adapter.ListClusterShells(); len(live) != 0 {
		t.Fatalf("ListClusterShells() = %d, want 0 after StopAll", len(live))
	}
	for _, shell := range []domain.ClusterShell{first, second} {
		_, err := client.CoreV1().Pods("shop").Get(context.Background(), shell.PodName, metav1.GetOptions{})
		if !apierrors.IsNotFound(err) {
			t.Errorf("pod %q get after StopAll = %v, want NotFound", shell.PodName, err)
		}
	}
}

// TestStopAllClusterShellsClosesTheRegistry is the node shell's sweep race,
// restated here because the registry is a second one: a start beginning after
// the sweep is refused and its pod deleted, rather than landing in a map
// nothing reads again.
func TestStopAllClusterShellsClosesTheRegistry(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("create", "pods", runningReactor)
	adapter := newClusterShellAdapter("dev", client)

	adapter.StopAllClusterShells()

	_, err := adapter.StartClusterShell(context.Background(), "dev", "shop", testShellImage)
	if !errors.Is(err, errClusterShellsClosed) {
		t.Fatalf("StartClusterShell() after StopAllClusterShells error = %v, want errClusterShellsClosed", err)
	}

	pods, listErr := client.CoreV1().Pods("shop").List(context.Background(), metav1.ListOptions{})
	if listErr != nil {
		t.Fatalf("listing pods: %v", listErr)
	}
	if len(pods.Items) != 0 {
		t.Fatalf("pods left after a refused start = %d, want 0", len(pods.Items))
	}
}

// TestStartClusterShellDeletesThePodWhenItNeverRuns proves the wait's failure
// path does not leak.
func TestStartClusterShellDeletesThePodWhenItNeverRuns(t *testing.T) {
	client := fake.NewSimpleClientset()
	adapter := newClusterShellAdapter("dev", client)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	if _, err := adapter.StartClusterShell(ctx, "dev", "shop", testShellImage); err == nil {
		t.Fatal("StartClusterShell() error = nil, want a wait failure for a pod that never runs")
	}

	pods, listErr := client.CoreV1().Pods("shop").List(context.Background(), metav1.ListOptions{})
	if listErr != nil {
		t.Fatalf("listing pods: %v", listErr)
	}
	if len(pods.Items) != 0 {
		t.Fatalf("pods left after a failed start = %d, want 0", len(pods.Items))
	}
}

// existingShellPod builds a pod carrying PodSteer's in-cluster-shell labels, as
// one left in a namespace by an earlier session would look.
func existingShellPod(namespace, name string, phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				clusterShellManagedByLabel: clusterShellManagedByValue,
				clusterShellPurposeLabel:   clusterShellPurposeValue,
			},
		},
		Spec:   corev1.PodSpec{Containers: []corev1.Container{{Name: clusterShellContainerName, Image: testShellImage}}},
		Status: corev1.PodStatus{Phase: phase},
	}
}

// TestFindClusterShellsQuotesThePhaseAndSelectsOnlyOurs is the reuse listing.
//
// It reports the phase VERBATIM rather than a verdict — the decision about
// which of them may be offered belongs to domain.PlanClusterShellReuse — and
// it must not pick up a node-shell pod, which carries the same managed-by
// label and a different purpose.
func TestFindClusterShellsQuotesThePhaseAndSelectsOnlyOurs(t *testing.T) {
	nodeShell := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "podsteer-node-shell-zzzzz",
			Namespace: "shop",
			Labels: map[string]string{
				nodeShellManagedByLabel: nodeShellManagedByValue,
				nodeShellPurposeLabel:   nodeShellPurposeValue,
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	client := fake.NewSimpleClientset(
		existingShellPod("shop", "podsteer-shell-aaaaa", corev1.PodRunning),
		existingShellPod("shop", "podsteer-shell-bbbbb", corev1.PodFailed),
		nodeShell,
	)
	// The fake clientset does not apply a label selector, so the selector this
	// sends is asserted directly rather than through what comes back.
	var sentSelector string
	client.PrependReactor("list", "pods", func(action clientgotesting.Action) (bool, runtime.Object, error) {
		if list, ok := action.(clientgotesting.ListAction); ok {
			sentSelector = list.GetListRestrictions().Labels.String()
		}
		return false, nil, nil
	})
	adapter := newClusterShellAdapter("dev", client)

	candidates, err := adapter.FindClusterShells(context.Background(), "dev", "shop")
	if err != nil {
		t.Fatalf("FindClusterShells() error = %v", err)
	}

	if !strings.Contains(sentSelector, clusterShellPurposeLabel+"="+clusterShellPurposeValue) {
		t.Errorf("label selector = %q, want it to narrow to the cluster-shell purpose so a node shell is never offered", sentSelector)
	}

	phases := map[string]domain.PodPhase{}
	for _, candidate := range candidates {
		phases[candidate.PodName] = candidate.Phase
	}
	if phases["podsteer-shell-aaaaa"] != domain.PodPhaseRunning {
		t.Errorf("running pod's phase = %q, want %q verbatim", phases["podsteer-shell-aaaaa"], domain.PodPhaseRunning)
	}
	if phases["podsteer-shell-bbbbb"] != domain.PodPhaseFailed {
		t.Errorf("failed pod's phase = %q, want %q verbatim — an exited pod is reported, never dropped", phases["podsteer-shell-bbbbb"], domain.PodPhaseFailed)
	}
}

// TestFindClusterShellsExcludesWhatThisProcessAlreadyOwns keeps two panes off
// one pod. A shell this process is holding is already in the activity list;
// offering it again would give two sessions the same pod and then race them to
// delete it.
func TestFindClusterShellsExcludesWhatThisProcessAlreadyOwns(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("create", "pods", runningReactor)
	adapter := newClusterShellAdapter("dev", client)

	mine, err := adapter.StartClusterShell(context.Background(), "dev", "shop", testShellImage)
	if err != nil {
		t.Fatalf("StartClusterShell() error = %v", err)
	}

	candidates, err := adapter.FindClusterShells(context.Background(), "dev", "shop")
	if err != nil {
		t.Fatalf("FindClusterShells() error = %v", err)
	}
	for _, candidate := range candidates {
		if candidate.PodName == mine.PodName {
			t.Fatalf("pod %q is held by this process and was still offered for reuse", candidate.PodName)
		}
	}
}

// TestAdoptClusterShellTakesOverARunningPodAndOwnsItsDeletion is the reuse
// path's other half: adopting registers the pod so it is deleted on session
// end exactly as a created one is. A pod nobody owns is a pod nobody deletes.
func TestAdoptClusterShellTakesOverARunningPodAndOwnsItsDeletion(t *testing.T) {
	client := fake.NewSimpleClientset(existingShellPod("shop", "podsteer-shell-aaaaa", corev1.PodRunning))
	adapter := newClusterShellAdapter("dev", client)

	shell, err := adapter.AdoptClusterShell(context.Background(), "dev", "shop", "podsteer-shell-aaaaa")
	if err != nil {
		t.Fatalf("AdoptClusterShell() error = %v", err)
	}
	if !shell.Adopted {
		t.Error("Adopted = false on a pod taken over rather than created")
	}
	if shell.Image != testShellImage {
		t.Errorf("image = %q, want it read off the pod that was adopted", shell.Image)
	}

	if err := adapter.StopClusterShell(shell.ID); err != nil {
		t.Fatalf("StopClusterShell() error = %v", err)
	}
	_, err = client.CoreV1().Pods("shop").Get(context.Background(), "podsteer-shell-aaaaa", metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("pod get after stop = %v, want NotFound — an adopted pod is on the same hook as a created one", err)
	}
}

// TestAdoptClusterShellRefusesAPodThatIsNotRunningOrNotOurs covers both
// refusals, and the second is the structural one: adoption signs up to DELETE
// a pod, so it must never be reachable for an arbitrary name.
func TestAdoptClusterShellRefusesAPodThatIsNotRunningOrNotOurs(t *testing.T) {
	stranger := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "somebodys-workload", Namespace: "shop"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	client := fake.NewSimpleClientset(
		existingShellPod("shop", "podsteer-shell-exited", corev1.PodSucceeded),
		stranger,
	)
	adapter := newClusterShellAdapter("dev", client)

	for _, name := range []string{"podsteer-shell-exited", "somebodys-workload"} {
		_, err := adapter.AdoptClusterShell(context.Background(), "dev", "shop", name)
		if !errors.Is(err, ports.ErrClusterShellNotReusable) {
			t.Errorf("AdoptClusterShell(%q) error = %v, want ErrClusterShellNotReusable", name, err)
		}
		if live := adapter.ListClusterShells(); len(live) != 0 {
			t.Fatalf("ListClusterShells() = %d after a refused adoption, want 0 — a refused pod must not be put on the delete hook", len(live))
		}
	}

	// And the stranger is still there: a refused adoption deletes nothing.
	if _, err := client.CoreV1().Pods("shop").Get(context.Background(), "somebodys-workload", metav1.GetOptions{}); err != nil {
		t.Fatalf("somebody else's pod after a refused adoption: %v", err)
	}
}

// TestAnAdmissionRefusalCarriesTheApiServersOwnWords is the message contract.
//
// Pod Security answers a create with HTTP 403, which classify would otherwise
// report as "your account is not allowed to perform this operation" — false,
// and it discards the one sentence naming the field to change.
func TestAnAdmissionRefusalCarriesTheApiServersOwnWords(t *testing.T) {
	const message = `pods "podsteer-shell-abcde" is forbidden: violates PodSecurity "restricted:latest": ` +
		`allowPrivilegeEscalation != false (container "shell" must set securityContext.allowPrivilegeEscalation=false)`

	refusal := apierrors.NewForbidden(
		schema.GroupResource{Resource: "pods"}, "podsteer-shell-abcde", errors.New(message))

	client := fake.NewSimpleClientset()
	client.PrependReactor("create", "pods", func(clientgotesting.Action) (bool, runtime.Object, error) {
		return true, nil, refusal
	})
	adapter := newClusterShellAdapter("dev", client)

	_, err := adapter.StartClusterShell(context.Background(), "dev", "shop", testShellImage)
	if !errors.Is(err, ports.ErrPodRejectedByAdmission) {
		t.Fatalf("StartClusterShell() error = %v, want ErrPodRejectedByAdmission", err)
	}
	if errors.Is(err, ports.ErrForbidden) {
		t.Error("an admission refusal is also classified ErrForbidden, which would report it as an RBAC denial")
	}
	if !strings.Contains(err.Error(), "securityContext.allowPrivilegeEscalation=false") {
		t.Fatalf("error = %q, want the API server's own words verbatim — they name the field to change", err)
	}
}

// TestAnRbacDenialOnTheSameCreateStaysForbidden is the other side of that
// distinction: a 403 that is genuinely authorisation must keep the sentence
// that sends somebody to their administrator.
func TestAnRbacDenialOnTheSameCreateStaysForbidden(t *testing.T) {
	refusal := apierrors.NewForbidden(
		schema.GroupResource{Resource: "pods"}, "podsteer-shell-abcde",
		errors.New(`User "alice" cannot create resource "pods" in API group "" in the namespace "shop"`))

	client := fake.NewSimpleClientset()
	client.PrependReactor("create", "pods", func(clientgotesting.Action) (bool, runtime.Object, error) {
		return true, nil, refusal
	})
	adapter := newClusterShellAdapter("dev", client)

	_, err := adapter.StartClusterShell(context.Background(), "dev", "shop", testShellImage)
	if !errors.Is(err, ports.ErrForbidden) {
		t.Fatalf("StartClusterShell() error = %v, want ErrForbidden for an RBAC denial", err)
	}
	if errors.Is(err, ports.ErrPodRejectedByAdmission) {
		t.Error("an RBAC denial was classified as an admission refusal")
	}
}
