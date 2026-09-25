package k8s

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	clientgotesting "k8s.io/client-go/testing"
)

const testDigest = "sha256:3333333333333333333333333333333333333333333333333333333333333333"

func imagePod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web-0", Namespace: "shop"},
		Spec: corev1.PodSpec{
			NodeName:         "node-1",
			Containers:       []corev1.Container{{Name: "app", Image: "ghcr.io/team/app:v1", ImagePullPolicy: corev1.PullIfNotPresent}},
			InitContainers:   []corev1.Container{{Name: "setup", Image: "busybox:1.37"}},
			ImagePullSecrets: []corev1.LocalObjectReference{{Name: "ghcr-pull"}},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:    "app",
				Image:   "ghcr.io/team/app:v1",
				ImageID: "ghcr.io/team/app@" + testDigest,
			}},
			InitContainerStatuses: []corev1.ContainerStatus{{
				Name:  "setup",
				Image: "docker.io/library/busybox:1.37",
			}},
		},
	}
}

func imageNode() *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
		Status: corev1.NodeStatus{Images: []corev1.ContainerImage{
			{Names: []string{"ghcr.io/team/app@" + testDigest, "ghcr.io/team/app:v1"}, SizeBytes: 41_000_000},
		}},
	}
}

func TestImageFactsGathersThePodsViewAndTheNodesImageList(t *testing.T) {
	client := fake.NewSimpleClientset(imagePod(), imageNode())
	adapter := newTestAdapter("dev", client)

	facts, err := adapter.ImageFacts(context.Background(), "dev", "shop", "web-0", "app")
	if err != nil {
		t.Fatalf("ImageFacts() error = %v", err)
	}

	if facts.DeclaredRef != "ghcr.io/team/app:v1" {
		t.Errorf("declared = %q", facts.DeclaredRef)
	}
	if facts.ImageID != "ghcr.io/team/app@"+testDigest {
		t.Errorf("imageID = %q", facts.ImageID)
	}
	if facts.PullPolicy != string(corev1.PullIfNotPresent) {
		t.Errorf("pull policy = %q", facts.PullPolicy)
	}
	if len(facts.PullSecrets) != 1 || facts.PullSecrets[0] != "ghcr-pull" {
		t.Errorf("pull secrets = %v, want the NAME and nothing more", facts.PullSecrets)
	}
	if facts.NodeName != "node-1" || len(facts.NodeImages) != 1 {
		t.Errorf("node = %q with %d images, want node-1 with one", facts.NodeName, len(facts.NodeImages))
	}
	if facts.NodeUnreadable != "" {
		t.Errorf("node unreadable = %q, want nothing", facts.NodeUnreadable)
	}
}

// An init container's image is the one nothing else on screen describes, and
// an init container that cannot pull is exactly why somebody opens this.
func TestImageFactsFindsAnInitContainer(t *testing.T) {
	client := fake.NewSimpleClientset(imagePod(), imageNode())
	adapter := newTestAdapter("dev", client)

	facts, err := adapter.ImageFacts(context.Background(), "dev", "shop", "web-0", "setup")
	if err != nil {
		t.Fatalf("ImageFacts() error = %v", err)
	}
	if facts.DeclaredRef != "busybox:1.37" {
		t.Errorf("declared = %q, want the init container's image", facts.DeclaredRef)
	}
	if facts.ResolvedRef != "docker.io/library/busybox:1.37" {
		t.Errorf("resolved = %q, want the init container's status", facts.ResolvedRef)
	}
}

// A REFUSAL IS NOT AN ABSENCE, and it is not a failed call either: plenty of
// accounts read a pod in their namespace and cannot get a cluster-scoped
// node, and the digest and the references are still worth showing.
func TestImageFactsSurvivesANodeItMayNotRead(t *testing.T) {
	client := fake.NewSimpleClientset(imagePod())
	client.PrependReactor("get", "nodes", func(clientgotesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(
			schema.GroupResource{Resource: "nodes"}, "node-1", nil)
	})
	adapter := newTestAdapter("dev", client)

	facts, err := adapter.ImageFacts(context.Background(), "dev", "shop", "web-0", "app")
	if err != nil {
		t.Fatalf("ImageFacts() error = %v, want the refusal carried inside the answer", err)
	}
	if facts.NodeUnreadable == "" {
		t.Fatal("the refusal has to be stated, not swallowed")
	}
	if !strings.Contains(facts.NodeUnreadable, "node-1") {
		t.Errorf("the reason should name the node, got %q", facts.NodeUnreadable)
	}
	if len(facts.NodeImages) != 0 {
		t.Error("a refused node must contribute no images")
	}
	if facts.ImageID == "" {
		t.Error("the half that answered must still be reported")
	}
}

// An unscheduled pod has no node to ask, and asking one anyway would be a
// request that could only fail.
func TestImageFactsAsksNoNodeForAnUnscheduledPod(t *testing.T) {
	pod := imagePod()
	pod.Spec.NodeName = ""
	client := fake.NewSimpleClientset(pod)

	asked := false
	client.PrependReactor("get", "nodes", func(clientgotesting.Action) (bool, runtime.Object, error) {
		asked = true
		return false, nil, nil
	})
	adapter := newTestAdapter("dev", client)

	facts, err := adapter.ImageFacts(context.Background(), "dev", "shop", "web-0", "app")
	if err != nil {
		t.Fatalf("ImageFacts() error = %v", err)
	}
	if asked {
		t.Error("an unscheduled pod has no node; nothing should have been asked")
	}
	if facts.NodeName != "" {
		t.Errorf("node = %q, want none", facts.NodeName)
	}
}

// TWO GETS, AND NOTHING ELSE. No registry read, no pull Secret read — the
// second of those is what the Secrets doctrine forbids on render, and this is
// the test that keeps it true as the file grows.
func TestImageFactsReadsNoSecretAndCostsTwoRequests(t *testing.T) {
	client := fake.NewSimpleClientset(imagePod(), imageNode())
	adapter := newTestAdapter("dev", client)

	if _, err := adapter.ImageFacts(context.Background(), "dev", "shop", "web-0", "app"); err != nil {
		t.Fatalf("ImageFacts() error = %v", err)
	}

	var pods, nodes, others int
	for _, action := range client.Actions() {
		switch {
		case action.GetResource().Resource == "pods":
			pods++
		case action.GetResource().Resource == "nodes":
			nodes++
		default:
			others++
			if action.GetResource().Resource == "secrets" {
				t.Fatalf("ImageFacts read a Secret: %v", action)
			}
		}
	}
	if pods != 1 || nodes != 1 || others != 0 {
		t.Errorf("requests = %d pods, %d nodes, %d other; want exactly one of each and nothing else",
			pods, nodes, others)
	}
}
