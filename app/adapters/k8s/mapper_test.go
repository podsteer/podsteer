package k8s

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/podsteer/podsteer/app/domain"
)

// These tests cover the anti-corruption layer, which is where a client-go
// upgrade or an unfamiliar cluster is most likely to break something quietly.

func TestMapPodTranslatesIdentityAndStatus(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 8, 17, 8, 0, 0, 0, time.UTC)
	source := &corev1.Pod{
		UID:               "1f2e3d",
		Name:              "api-7d9f",
		Namespace:         "platform",
		Labels:            map[string]string{"app": "api"},
		CreationTimestamp: metav1.NewTime(created),
		Spec: corev1.PodSpec{
			NodeName: "node-1",
			Containers: []corev1.Container{
				{Name: "app", Image: "ghcr.io/acme/api:1.4.0"},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.42.0.7",
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:         "app",
					Image:        "ghcr.io/acme/api@sha256:abc",
					Ready:        true,
					RestartCount: 2,
					State:        corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
				},
			},
		},
	}

	pod, err := mapPod("dev", source)
	if err != nil {
		t.Fatalf("mapPod() error = %v", err)
	}

	if pod.Name() != "api-7d9f" || pod.Namespace() != "platform" || pod.ClusterID() != "dev" {
		t.Errorf("identity = %q/%q on %q, want platform/api-7d9f on dev",
			pod.Namespace(), pod.Name(), pod.ClusterID())
	}
	if pod.Phase() != domain.PodPhaseRunning {
		t.Errorf("Phase() = %q, want %q", pod.Phase(), domain.PodPhaseRunning)
	}
	if pod.NodeName() != "node-1" || pod.PodIP() != "10.42.0.7" {
		t.Errorf("placement = %q/%q, want node-1/10.42.0.7", pod.NodeName(), pod.PodIP())
	}
	if !pod.CreatedAt().Equal(created) {
		t.Errorf("CreatedAt() = %v, want %v", pod.CreatedAt(), created)
	}
	if !pod.IsHealthy() {
		t.Error("IsHealthy() = false, want true for a running pod with all containers ready")
	}

	container := pod.Containers()[0]
	if container.RestartCount != 2 || !container.Ready {
		t.Errorf("container status = ready %v, restarts %d; want ready true, restarts 2",
			container.Ready, container.RestartCount)
	}
	// The resolved digest is what is actually running, and beats the mutable
	// tag from the spec.
	if container.Image != "ghcr.io/acme/api@sha256:abc" {
		t.Errorf("Image = %q, want the digest-resolved reference from status", container.Image)
	}
}

// A pod being deleted keeps reporting Running right until it disappears. This
// substitution is the single most visible correction the mapper makes.
func TestMapPodReportsDeletionAsTerminating(t *testing.T) {
	t.Parallel()

	deleted := metav1.NewTime(time.Now())
	source := &corev1.Pod{
		Name:              "api-7d9f",
		Namespace:         "default",
		DeletionTimestamp: &deleted,
		Status:            corev1.PodStatus{Phase: corev1.PodRunning},
	}

	pod, err := mapPod("dev", source)
	if err != nil {
		t.Fatalf("mapPod() error = %v", err)
	}
	if pod.Phase() != domain.PodPhaseTerminating {
		t.Errorf("Phase() = %q, want %q", pod.Phase(), domain.PodPhaseTerminating)
	}
}

// Between scheduling and the kubelet's first report a container has a spec but
// no status. It must still appear, or the operator watching a stuck pod sees
// an empty container list.
func TestMapContainersIncludesContainersWithoutStatus(t *testing.T) {
	t.Parallel()

	source := &corev1.Pod{
		Name: "api-7d9f", Namespace: "default",
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "app", Image: "nginx:1.27"},
			{Name: "sidecar", Image: "envoy:1.31"},
		}},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  "app",
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}},
			}},
		},
	}

	pod, err := mapPod("dev", source)
	if err != nil {
		t.Fatalf("mapPod() error = %v", err)
	}

	if got := pod.TotalContainers(); got != 2 {
		t.Fatalf("TotalContainers() = %d, want 2", got)
	}

	containers := pod.Containers()
	if containers[0].Reason != "ImagePullBackOff" {
		t.Errorf("containers[0].Reason = %q, want %q", containers[0].Reason, "ImagePullBackOff")
	}
	if containers[1].State != domain.ContainerStateWaiting {
		t.Errorf("containers[1].State = %q, want %q", containers[1].State, domain.ContainerStateWaiting)
	}
	if containers[1].Image != "envoy:1.31" {
		t.Errorf("containers[1].Image = %q, want the spec image", containers[1].Image)
	}

	// The diagnosis must reach the pod level, where the table shows it.
	if got := pod.StatusReason(); got != "ImagePullBackOff" {
		t.Errorf("StatusReason() = %q, want %q", got, "ImagePullBackOff")
	}
}

func TestMapContainerState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		state      corev1.ContainerState
		wantState  domain.ContainerState
		wantReason string
	}{
		{
			name:      "running",
			state:     corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			wantState: domain.ContainerStateRunning,
		},
		{
			name: "terminated with a reason",
			state: corev1.ContainerState{
				Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled"},
			},
			wantState:  domain.ContainerStateTerminated,
			wantReason: "OOMKilled",
		},
		{
			name: "waiting with a reason",
			state: corev1.ContainerState{
				Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
			},
			wantState:  domain.ContainerStateWaiting,
			wantReason: "CrashLoopBackOff",
		},
		{
			name:      "nothing reported",
			state:     corev1.ContainerState{},
			wantState: domain.ContainerStateUnknown,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			state, reason := mapContainerState(test.state)
			if state != test.wantState {
				t.Errorf("state = %q, want %q", state, test.wantState)
			}
			if reason != test.wantReason {
				t.Errorf("reason = %q, want %q", reason, test.wantReason)
			}
		})
	}
}

func TestMapNamespace(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	namespace, err := mapNamespace(&corev1.Namespace{
		Name: "argocd", CreationTimestamp: metav1.NewTime(created),
		Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	})
	if err != nil {
		t.Fatalf("mapNamespace() error = %v", err)
	}

	if namespace.Name() != "argocd" {
		t.Errorf("Name() = %q, want %q", namespace.Name(), "argocd")
	}
	if !namespace.IsActive() {
		t.Error("IsActive() = false, want true")
	}
	if !namespace.CreatedAt().Equal(created) {
		t.Errorf("CreatedAt() = %v, want %v", namespace.CreatedAt(), created)
	}
}

func TestMapServerVersionHandlesNil(t *testing.T) {
	t.Parallel()

	if got := mapServerVersion(nil); !got.IsZero() {
		t.Errorf("mapServerVersion(nil) = %+v, want the zero value", got)
	}
}

// An event that exists happened at least once, whatever the API left unset.
//
// Kubernetes has two generations of this field and a real cluster carries
// both: events.k8s.io sets `series.count` when something repeats and NOTHING
// when it happens once, while the older form sets `count`. Reading the legacy
// field alone reported "0" against events that had just fired.
func TestMapEventCountsBothGenerations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		event corev1.Event
		want  int32
	}{
		{
			name: "modern, fired once, nothing set",
			event: corev1.Event{
				EventTime: metav1.NewMicroTime(time.Now()),
			},
			want: 1,
		},
		{
			name: "modern, repeating",
			event: corev1.Event{
				Series:    &corev1.EventSeries{Count: 46},
				EventTime: metav1.NewMicroTime(time.Now()),
			},
			want: 46,
		},
		{
			name:  "legacy count",
			event: corev1.Event{Count: 9},
			want:  9,
		},
		{
			name: "series wins over the legacy field it supersedes",
			event: corev1.Event{
				Count:  3,
				Series: &corev1.EventSeries{Count: 12},
			},
			want: 12,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			event := test.event
			event.Name = "probe.17c"
			event.Namespace = "default"
			event.Type = string(domain.EventWarning)
			event.Reason = "Probing"
			event.InvolvedObject = corev1.ObjectReference{Kind: "Pod", Name: "app-1"}

			mapped, err := mapEvent("dev", &event)
			if err != nil {
				t.Fatalf("mapEvent() error = %v", err)
			}
			if got := mapped.Count(); got != test.want {
				t.Errorf("count = %d, want %d", got, test.want)
			}
		})
	}
}

func TestKubernetesOwnGroupsAreNotCustomResources(t *testing.T) {
	// THE DUPLICATION THIS GUARDS, and it was on screen. The rule was "ends
	// with k8s.io", and `apps`, `batch`, `autoscaling` and `policy` carry no
	// suffix — so every Deployment, Job, HorizontalPodAutoscaler and
	// PodDisruptionBudget was discovered a second time and listed again under
	// Custom Resources, beside the catalog entry that already had it.
	for _, group := range []string{"apps", "batch", "autoscaling", "policy", "extensions"} {
		if !isKubernetesGroup(group) {
			t.Fatalf("%q was treated as a custom resource group", group)
		}
	}

	for _, group := range []string{"rbac.authorization.k8s.io", "storage.k8s.io", "node.k8s.io"} {
		if !isKubernetesGroup(group) {
			t.Fatalf("%q was treated as a custom resource group", group)
		}
	}
}

func TestOperatorInstalledGroupsAreCustomResources(t *testing.T) {
	for _, group := range []string{"argoproj.io", "cert-manager.io", "monitoring.coreos.com"} {
		if isKubernetesGroup(group) {
			t.Fatalf("%q was hidden as though Kubernetes published it", group)
		}
	}
}

func TestAdoptedGroupsSurviveTheSuffixRule(t *testing.T) {
	// Gateway API is the declared successor to Ingress and VolumeSnapshots
	// are how anybody backs up a PVC. Both ship as CRDs under a k8s.io group,
	// so the suffix rule was true about their names and false about their
	// availability — a cluster only has them because somebody installed them.
	for _, group := range []string{"gateway.networking.k8s.io", "snapshot.storage.k8s.io"} {
		if isKubernetesGroup(group) {
			t.Fatalf("%q was hidden, though it is only present when installed", group)
		}
	}
}
