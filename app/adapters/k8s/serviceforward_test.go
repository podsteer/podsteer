package k8s

import (
	"context"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/podsteer/podsteer/app/domain"
)

// TestServicePortsFlattensTargetPort covers the trap in the intstr type: an
// UNSET targetPort renders as "0", which is not a port and is not what
// Kubernetes does with it — it defaults an absent targetPort to the service
// port. Left as "0", every Service that omits targetPort would forward to
// port zero and fail with a message about nothing anybody could act on.
func TestServicePortsFlattensTargetPort(t *testing.T) {
	service := &corev1.Service{
		Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{
			{Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080), Protocol: corev1.ProtocolTCP},
			{Name: "named", Port: 443, TargetPort: intstr.FromString("https")},
			{Name: "unset", Port: 5432},
		}},
	}

	ports := servicePorts(service)
	if len(ports) != 3 {
		t.Fatalf("got %d ports, want 3", len(ports))
	}
	if ports[0].TargetPort != "8080" {
		t.Errorf("numeric targetPort = %q, want \"8080\"", ports[0].TargetPort)
	}
	if ports[1].TargetPort != "https" {
		t.Errorf("named targetPort = %q, want \"https\"", ports[1].TargetPort)
	}
	if ports[2].TargetPort != "" {
		t.Errorf("unset targetPort = %q, want empty — the domain defaults it to the service port", ports[2].TargetPort)
	}

	// And the domain does default it, which is the half this flattening exists for.
	resolved, err := domain.ResolveTargetPort(ports[2], nil)
	if err != nil {
		t.Fatalf("resolving an unset targetPort: %v", err)
	}
	if resolved != 5432 {
		t.Errorf("resolved = %d, want 5432 (the service port)", resolved)
	}
}

// TestContainerPortsIncludesInitContainers asserts a sidecar declared as a
// restartable init container can answer a named targetPort. Leaving them out
// would refuse a name that does resolve — a service mesh's port, typically.
func TestContainerPortsIncludesInitContainers(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{
		InitContainers: []corev1.Container{{
			Name:  "proxy",
			Ports: []corev1.ContainerPort{{Name: "mesh", ContainerPort: 15001}},
		}},
		Containers: []corev1.Container{{
			Name: "app",
			Ports: []corev1.ContainerPort{
				{Name: "http", ContainerPort: 8080},
				{ContainerPort: 9090}, // unnamed: nothing can ask for it by name
			},
		}},
	}}

	names := map[string]int{}
	for _, port := range containerPorts(pod) {
		names[port.Name] = port.Port
	}

	if names["mesh"] != 15001 {
		t.Errorf("mesh = %d, want 15001 — an init container's ports must be resolvable", names["mesh"])
	}
	if names["http"] != 8080 {
		t.Errorf("http = %d, want 8080", names["http"])
	}
	if len(names) != 2 {
		t.Errorf("got %d named ports, want 2 — an unnamed port has no name to resolve", len(names))
	}
}

func pod(name string, phase corev1.PodPhase, ready bool) *corev1.Pod {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "web", Labels: map[string]string{"app": "api"}},
		Status: corev1.PodStatus{
			Phase:      phase,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: status}},
		},
	}
}

func TestReadyPodFor(t *testing.T) {
	selector := map[string]string{"app": "api"}

	t.Run("picks a ready pod over a running one that is not", func(t *testing.T) {
		client := fake.NewClientset(
			pod("api-0", corev1.PodRunning, false),
			pod("api-1", corev1.PodRunning, true),
		)

		found, err := readyPodFor(context.Background(), client, "web", selector)
		if err != nil {
			t.Fatalf("readyPodFor: %v", err)
		}
		if found.Name != "api-1" {
			t.Errorf("chose %q, want api-1 — the Service sends traffic only to the ready one", found.Name)
		}
	})

	t.Run("refuses a pod that is running and failing its readiness probe", func(t *testing.T) {
		// THE FAILURE THIS PREVENTS: forwarding to a pod the Service itself
		// excludes produces a connection that refuses while the Service works,
		// which reads as the application being broken.
		client := fake.NewClientset(pod("api-0", corev1.PodRunning, false))

		_, err := readyPodFor(context.Background(), client, "web", selector)
		if !errors.Is(err, domain.ErrNoReadyEndpoint) {
			t.Fatalf("err = %v, want ErrNoReadyEndpoint", err)
		}
		if !strings.Contains(err.Error(), "none of them ready") {
			t.Errorf("err = %q, want it to say the pods are up but unready", err)
		}
	})

	t.Run("says something different when the selector matches nothing", func(t *testing.T) {
		// Same sentinel, different sentence, because they send somebody to
		// different places: an unready pod is an application problem, and no
		// pod at all is a selector that names nothing.
		client := fake.NewClientset(pod("other-0", corev1.PodRunning, true))

		_, err := readyPodFor(context.Background(), client, "web", map[string]string{"app": "missing"})
		if !errors.Is(err, domain.ErrNoReadyEndpoint) {
			t.Fatalf("err = %v, want ErrNoReadyEndpoint", err)
		}
		if !strings.Contains(err.Error(), "matches its selector") {
			t.Errorf("err = %q, want it to say nothing matches the selector", err)
		}
	})

	t.Run("ignores a pod that is terminating", func(t *testing.T) {
		// A pod with a deletion timestamp is already out of the endpoints;
		// landing a forward on it buys a connection that dies within seconds.
		going := pod("api-0", corev1.PodRunning, true)
		now := metav1.Now()
		going.DeletionTimestamp = &now
		going.Finalizers = []string{"podsteer.test/keep"}

		client := fake.NewClientset(going, pod("api-1", corev1.PodRunning, true))

		found, err := readyPodFor(context.Background(), client, "web", selector)
		if err != nil {
			t.Fatalf("readyPodFor: %v", err)
		}
		if found.Name != "api-1" {
			t.Errorf("chose %q, want api-1 — api-0 is terminating", found.Name)
		}
	})

	t.Run("ignores a pod that has succeeded or failed", func(t *testing.T) {
		client := fake.NewClientset(
			pod("api-0", corev1.PodSucceeded, true),
			pod("api-1", corev1.PodFailed, true),
		)

		_, err := readyPodFor(context.Background(), client, "web", selector)
		if !errors.Is(err, domain.ErrNoReadyEndpoint) {
			t.Fatalf("err = %v, want ErrNoReadyEndpoint", err)
		}
		if !strings.Contains(err.Error(), "matches its selector") {
			t.Errorf("err = %q, want the no-running-pod sentence", err)
		}
	})
}
