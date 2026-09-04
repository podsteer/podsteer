package k8s

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/podsteer/podsteer/app/domain"
)

func int32Ptr(v int32) *int32 { return &v }

// testReplicaSet builds a ReplicaSet owned by a Deployment, annotated with a
// revision number the way the deployment controller stamps it.
func testReplicaSet(namespace, name, deploymentName string, revision int64, replicas int32, image string) *appsv1.ReplicaSet {
	return &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         namespace,
			CreationTimestamp: metav1.NewTime(time.Date(2024, 1, int(revision), 0, 0, 0, 0, time.UTC)),
			Annotations: map[string]string{
				deploymentRevisionAnnotation: strconv.FormatInt(revision, 10),
			},
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "Deployment", Name: deploymentName, Controller: boolPtr(true)},
			},
		},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: int32Ptr(replicas),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", Image: image}},
				},
			},
		},
	}
}

// controllerRevisionData builds the raw patch a StatefulSet's or DaemonSet's
// shared history controller writes: a strategic-merge-patch-shaped JSON
// document carrying spec.template.
func controllerRevisionData(image string) []byte {
	return []byte(`{"spec":{"template":{"spec":{"containers":[{"name":"app","image":"` + image + `"}]}}}}`)
}

// testControllerRevision builds a ControllerRevision owned by name of kind
// ownerKind ("StatefulSet" or "DaemonSet").
func testControllerRevision(namespace, name, ownerKind, ownerName string, revision int64, image string) *appsv1.ControllerRevision {
	return &appsv1.ControllerRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         namespace,
			CreationTimestamp: metav1.NewTime(time.Date(2024, 1, int(revision), 0, 0, 0, 0, time.UTC)),
			OwnerReferences: []metav1.OwnerReference{
				{Kind: ownerKind, Name: ownerName, Controller: boolPtr(true)},
			},
		},
		Data:     runtime.RawExtension{Raw: controllerRevisionData(image)},
		Revision: revision,
	}
}

func TestRolloutHistoryForDeployment_OrdersNewestFirstAndMarksCurrent(t *testing.T) {
	client := fake.NewSimpleClientset(
		testReplicaSet("web", "api-111", "api", 1, 2, "myapp/web:1.0.0"),
		testReplicaSet("web", "api-222", "api", 2, 3, "myapp/web:2.0.0"),
	)
	adapter := newTestAdapter("dev", client)

	revisions, err := adapter.RolloutHistory(context.Background(), "dev", domain.WorkloadDeployment, "web", "api")
	if err != nil {
		t.Fatalf("RolloutHistory() error = %v", err)
	}
	if len(revisions) != 2 {
		t.Fatalf("len(revisions) = %d, want 2", len(revisions))
	}

	// Newest first.
	if revisions[0].Number != 2 || revisions[1].Number != 1 {
		t.Fatalf("revision order = [%d, %d], want [2, 1]", revisions[0].Number, revisions[1].Number)
	}

	if !revisions[0].Current {
		t.Error("revisions[0].Current = false, want true — it carries the highest revision number")
	}
	if revisions[1].Current {
		t.Error("revisions[1].Current = true, want false")
	}

	if revisions[0].Name != "api-222" {
		t.Errorf("revisions[0].Name = %q, want %q", revisions[0].Name, "api-222")
	}
	if revisions[0].Replicas != 3 {
		t.Errorf("revisions[0].Replicas = %d, want 3", revisions[0].Replicas)
	}
	if len(revisions[0].Images) != 1 || revisions[0].Images[0] != "myapp/web:2.0.0" {
		t.Errorf("revisions[0].Images = %v, want [myapp/web:2.0.0]", revisions[0].Images)
	}
	if !strings.Contains(revisions[0].TemplateYAML, "myapp/web:2.0.0") {
		t.Errorf("revisions[0].TemplateYAML = %q, want it to carry the image", revisions[0].TemplateYAML)
	}
	// The template comes from the ReplicaSet's OWN manifest, never the watch
	// store — see CLAUDE.md. This adapter method never touches the watch
	// store at all, so the assertion above (the image is present in full)
	// is the behavioural proof of that.
}

func TestRolloutHistoryForDeployment_ReadsChangeCause(t *testing.T) {
	rs := testReplicaSet("web", "api-111", "api", 1, 1, "myapp/web:1.0.0")
	rs.Annotations[changeCauseAnnotation] = "kubectl set image deployment/api app=myapp/web:1.0.0"
	client := fake.NewSimpleClientset(rs)
	adapter := newTestAdapter("dev", client)

	revisions, err := adapter.RolloutHistory(context.Background(), "dev", domain.WorkloadDeployment, "web", "api")
	if err != nil {
		t.Fatalf("RolloutHistory() error = %v", err)
	}
	if len(revisions) != 1 {
		t.Fatalf("len(revisions) = %d, want 1", len(revisions))
	}
	if revisions[0].ChangeCause != "kubectl set image deployment/api app=myapp/web:1.0.0" {
		t.Errorf("ChangeCause = %q, want the annotation's value", revisions[0].ChangeCause)
	}
}

func TestRolloutHistoryForDeployment_SkipsReplicaSetsNotOwnedOrUnannotated(t *testing.T) {
	unowned := testReplicaSet("web", "other-111", "other-deployment", 1, 1, "myapp/other:1.0.0")
	unannotated := testReplicaSet("web", "api-333", "api", 3, 1, "myapp/web:3.0.0")
	delete(unannotated.Annotations, deploymentRevisionAnnotation)

	client := fake.NewSimpleClientset(
		testReplicaSet("web", "api-111", "api", 1, 1, "myapp/web:1.0.0"),
		unowned,
		unannotated,
	)
	adapter := newTestAdapter("dev", client)

	revisions, err := adapter.RolloutHistory(context.Background(), "dev", domain.WorkloadDeployment, "web", "api")
	if err != nil {
		t.Fatalf("RolloutHistory() error = %v", err)
	}
	if len(revisions) != 1 {
		t.Fatalf("len(revisions) = %d, want 1 — an unowned or unannotated ReplicaSet must not appear", len(revisions))
	}
	if revisions[0].Name != "api-111" {
		t.Errorf("revisions[0].Name = %q, want %q", revisions[0].Name, "api-111")
	}
}

func TestRolloutHistoryForStatefulSet_ReadsControllerRevisions(t *testing.T) {
	client := fake.NewSimpleClientset(
		testControllerRevision("data", "db-aaaa", "StatefulSet", "db", 1, "postgres:13"),
		testControllerRevision("data", "db-bbbb", "StatefulSet", "db", 2, "postgres:14"),
		// A revision belonging to an unrelated StatefulSet must not leak in.
		testControllerRevision("data", "other-cccc", "StatefulSet", "cache", 5, "redis:7"),
	)
	adapter := newTestAdapter("dev", client)

	revisions, err := adapter.RolloutHistory(context.Background(), "dev", domain.WorkloadStatefulSet, "data", "db")
	if err != nil {
		t.Fatalf("RolloutHistory() error = %v", err)
	}
	if len(revisions) != 2 {
		t.Fatalf("len(revisions) = %d, want 2", len(revisions))
	}
	if revisions[0].Number != 2 || revisions[1].Number != 1 {
		t.Fatalf("revision order = [%d, %d], want [2, 1]", revisions[0].Number, revisions[1].Number)
	}
	if !revisions[0].Current || revisions[1].Current {
		t.Errorf("Current = [%v, %v], want [true, false]", revisions[0].Current, revisions[1].Current)
	}
	// A StatefulSet/DaemonSet revision carries no replica count of its own —
	// it is a stored patch, not a scaled object.
	if revisions[0].Replicas != 0 {
		t.Errorf("revisions[0].Replicas = %d, want 0", revisions[0].Replicas)
	}
	if len(revisions[0].Images) != 1 || revisions[0].Images[0] != "postgres:14" {
		t.Errorf("revisions[0].Images = %v, want [postgres:14]", revisions[0].Images)
	}
	if !strings.Contains(revisions[0].TemplateYAML, "postgres:14") {
		t.Errorf("revisions[0].TemplateYAML = %q, want it to carry the image", revisions[0].TemplateYAML)
	}
}

func TestRolloutHistoryForDaemonSet_ReadsControllerRevisions(t *testing.T) {
	client := fake.NewSimpleClientset(
		testControllerRevision("kube-system", "agent-aaaa", "DaemonSet", "agent", 1, "fluentd:1.14"),
		testControllerRevision("kube-system", "agent-bbbb", "DaemonSet", "agent", 2, "fluentd:1.15"),
	)
	adapter := newTestAdapter("dev", client)

	revisions, err := adapter.RolloutHistory(context.Background(), "dev", domain.WorkloadDaemonSet, "kube-system", "agent")
	if err != nil {
		t.Fatalf("RolloutHistory() error = %v", err)
	}
	if len(revisions) != 2 {
		t.Fatalf("len(revisions) = %d, want 2", len(revisions))
	}
	if !revisions[0].Current {
		t.Error("revisions[0].Current = false, want true")
	}
}
