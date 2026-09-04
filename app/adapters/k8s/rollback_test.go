package k8s

import (
	"context"
	"errors"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clientgotesting "k8s.io/client-go/testing"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// testDeploymentObj is a minimal single-container Deployment fixture for the
// rollback tests below.
//
// Deliberately ONE container, unlike management_test.go's testDeployment
// (which also carries a "sidecar" and an init container to prove SetImage
// leaves them alone): rollbackDeployment's patch is a STRATEGIC merge of
// spec.template, and a strategic merge patch merges spec.template.spec.
// containers BY NAME rather than replacing the list wholesale — the same
// container-list merge SetImage's own doc comment describes — so a target
// revision with fewer containers than the live template would not remove
// the ones missing from it. A single container sidesteps that here; the
// merge-by-name behaviour itself is what SetImage's tests already cover.
func testDeploymentObj(namespace, name string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", Image: "unset"}},
				},
			},
		},
	}
}

// testStatefulSetObj and testDaemonSetObj are minimal fixtures for the
// rollback tests below: the object itself only needs to exist and be
// gettable, since RollbackWorkload reads the target revision's history from
// ControllerRevisions, not from this object's own spec.
func testStatefulSetObj(namespace, name string) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", Image: "unset"}},
				},
			},
		},
	}
}

func testDaemonSetObj(namespace, name string) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: appsv1.DaemonSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", Image: "unset"}},
				},
			},
		},
	}
}

func TestRollbackDeploymentPatchesTemplateFromTheRightReplicaSet(t *testing.T) {
	client := fake.NewSimpleClientset(
		testDeploymentObj("web", "api"),
		testReplicaSet("web", "api-111", "api", 1, 1, "myapp/web:1.0.0"),
		testReplicaSet("web", "api-222", "api", 2, 1, "myapp/web:2.0.0"),
	)
	adapter := newTestAdapter("dev", client)

	outcome, err := adapter.RollbackWorkload(context.Background(), "dev", domain.WorkloadDeployment, "web", "api", 1, false)
	if err != nil {
		t.Fatalf("RollbackWorkload() error = %v", err)
	}
	if outcome.ToRevision != 1 {
		t.Errorf("outcome.ToRevision = %d, want 1", outcome.ToRevision)
	}
	if outcome.DryRun {
		t.Error("outcome.DryRun = true, want false — this was a real rollback")
	}

	got, err := client.AppsV1().Deployments("web").Get(context.Background(), "api", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting deployment: %v", err)
	}
	containers := got.Spec.Template.Spec.Containers
	if len(containers) != 1 || containers[0].Image != "myapp/web:1.0.0" {
		t.Errorf("deployment template containers = %v, want the revision-1 image", containers)
	}
}

func TestRollbackDeploymentRefusesCurrentRevision(t *testing.T) {
	client := fake.NewSimpleClientset(
		testDeploymentObj("web", "api"),
		testReplicaSet("web", "api-111", "api", 1, 1, "myapp/web:1.0.0"),
		testReplicaSet("web", "api-222", "api", 2, 1, "myapp/web:2.0.0"),
	)
	adapter := newTestAdapter("dev", client)

	// Revision 2 carries the highest number, so it is the current one — see
	// markCurrent's own doc comment for why that is the rule.
	_, err := adapter.RollbackWorkload(context.Background(), "dev", domain.WorkloadDeployment, "web", "api", 2, false)
	if !errors.Is(err, domain.ErrInvalidRevision) {
		t.Fatalf("RollbackWorkload(toRevision=2) error = %v, want it to wrap domain.ErrInvalidRevision", err)
	}

	// Refused before anything was touched.
	got, getErr := client.AppsV1().Deployments("web").Get(context.Background(), "api", metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("getting deployment: %v", getErr)
	}
	if containers := got.Spec.Template.Spec.Containers; len(containers) != 1 || containers[0].Image != "unset" {
		t.Errorf("deployment template = %v, want the original (unset) template — the refusal must not have written anything", containers)
	}
}

func TestRollbackDeploymentMissingRevisionIsNotFound(t *testing.T) {
	client := fake.NewSimpleClientset(
		testDeploymentObj("web", "api"),
		testReplicaSet("web", "api-111", "api", 1, 1, "myapp/web:1.0.0"),
	)
	adapter := newTestAdapter("dev", client)

	_, err := adapter.RollbackWorkload(context.Background(), "dev", domain.WorkloadDeployment, "web", "api", 99, false)
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("RollbackWorkload(toRevision=99) error = %v, want it to wrap ports.ErrNotFound", err)
	}
}

func TestRollbackDeploymentDryRunPersistsNothing(t *testing.T) {
	client := fake.NewSimpleClientset(
		testDeploymentObj("web", "api"),
		testReplicaSet("web", "api-111", "api", 1, 1, "myapp/web:1.0.0"),
		testReplicaSet("web", "api-222", "api", 2, 1, "myapp/web:2.0.0"),
	)

	// The fake tracker has no notion of dry run — unlike a real API server it
	// applies whatever a Patch reactor hands back — so this does what the API
	// server's admission chain does for a dry-run request: report the
	// options it received, then hand back the object WITHOUT letting the
	// default reactor (which would call the tracker) run. Mirrors
	// TestUpdateResourceDryRunSendsTheOptionAndStoresNothing in apply_test.go.
	var sawDryRun []string
	client.PrependReactor("patch", "deployments", func(action clientgotesting.Action) (bool, runtime.Object, error) {
		patch := action.(clientgotesting.PatchActionImpl)
		sawDryRun = patch.PatchOptions.DryRun
		current, err := client.Tracker().Get(action.GetResource(), action.GetNamespace(), patch.GetName())
		return true, current, err
	})

	adapter := newTestAdapter("dev", client)

	outcome, err := adapter.RollbackWorkload(context.Background(), "dev", domain.WorkloadDeployment, "web", "api", 1, true)
	if err != nil {
		t.Fatalf("RollbackWorkload(dryRun=true) error = %v", err)
	}
	if !outcome.DryRun {
		t.Error("outcome.DryRun = false, want true")
	}
	if len(sawDryRun) != 1 || sawDryRun[0] != metav1.DryRunAll {
		t.Errorf("patch options DryRun = %v, want [%q]", sawDryRun, metav1.DryRunAll)
	}

	got, err := client.AppsV1().Deployments("web").Get(context.Background(), "api", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting deployment: %v", err)
	}
	if containers := got.Spec.Template.Spec.Containers; len(containers) != 1 || containers[0].Image != "unset" {
		t.Errorf("deployment template = %v, want it untouched — a dry run must persist nothing", containers)
	}
}

func TestRollbackDeploymentSetsChangeCauseOnlyWhenAlreadyPresent(t *testing.T) {
	withCause := testDeploymentObj("web", "recorded")
	withCause.Annotations = map[string]string{changeCauseAnnotation: "kubectl set image deployment/recorded app=myapp/web:2.0.0"}

	client := fake.NewSimpleClientset(
		withCause,
		testDeploymentObj("web", "unrecorded"),
		testReplicaSet("web", "recorded-111", "recorded", 1, 1, "myapp/web:1.0.0"),
		testReplicaSet("web", "recorded-222", "recorded", 2, 1, "myapp/web:2.0.0"),
		testReplicaSet("web", "unrecorded-111", "unrecorded", 1, 1, "myapp/web:1.0.0"),
		testReplicaSet("web", "unrecorded-222", "unrecorded", 2, 1, "myapp/web:2.0.0"),
	)
	adapter := newTestAdapter("dev", client)

	if _, err := adapter.RollbackWorkload(context.Background(), "dev", domain.WorkloadDeployment, "web", "recorded", 1, false); err != nil {
		t.Fatalf("RollbackWorkload(recorded) error = %v", err)
	}
	got, err := client.AppsV1().Deployments("web").Get(context.Background(), "recorded", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting deployment: %v", err)
	}
	if want := "rollback to revision 1"; got.Annotations[changeCauseAnnotation] != want {
		t.Errorf("change-cause annotation = %q, want %q — the deployment already used the convention",
			got.Annotations[changeCauseAnnotation], want)
	}

	if _, err := adapter.RollbackWorkload(context.Background(), "dev", domain.WorkloadDeployment, "web", "unrecorded", 1, false); err != nil {
		t.Fatalf("RollbackWorkload(unrecorded) error = %v", err)
	}
	got, err = client.AppsV1().Deployments("web").Get(context.Background(), "unrecorded", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting deployment: %v", err)
	}
	if _, has := got.Annotations[changeCauseAnnotation]; has {
		t.Errorf("change-cause annotation = %q, want it absent — this deployment never used the convention",
			got.Annotations[changeCauseAnnotation])
	}
}

func TestRollbackStatefulSetAppliesTheControllerRevisionPatch(t *testing.T) {
	client := fake.NewSimpleClientset(
		testStatefulSetObj("data", "db"),
		testControllerRevision("data", "db-aaaa", "StatefulSet", "db", 1, "postgres:13"),
		testControllerRevision("data", "db-bbbb", "StatefulSet", "db", 2, "postgres:14"),
	)
	adapter := newTestAdapter("dev", client)

	outcome, err := adapter.RollbackWorkload(context.Background(), "dev", domain.WorkloadStatefulSet, "data", "db", 1, false)
	if err != nil {
		t.Fatalf("RollbackWorkload() error = %v", err)
	}
	if outcome.ToRevision != 1 {
		t.Errorf("outcome.ToRevision = %d, want 1", outcome.ToRevision)
	}

	got, err := client.AppsV1().StatefulSets("data").Get(context.Background(), "db", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting statefulset: %v", err)
	}
	containers := got.Spec.Template.Spec.Containers
	if len(containers) != 1 || containers[0].Image != "postgres:13" {
		t.Errorf("statefulset template containers = %v, want the revision-1 image", containers)
	}
}

func TestRollbackStatefulSetRefusesCurrentRevision(t *testing.T) {
	client := fake.NewSimpleClientset(
		testStatefulSetObj("data", "db"),
		testControllerRevision("data", "db-aaaa", "StatefulSet", "db", 1, "postgres:13"),
		testControllerRevision("data", "db-bbbb", "StatefulSet", "db", 2, "postgres:14"),
	)
	adapter := newTestAdapter("dev", client)

	_, err := adapter.RollbackWorkload(context.Background(), "dev", domain.WorkloadStatefulSet, "data", "db", 2, false)
	if !errors.Is(err, domain.ErrInvalidRevision) {
		t.Fatalf("RollbackWorkload(toRevision=2) error = %v, want it to wrap domain.ErrInvalidRevision", err)
	}
}

func TestRollbackDaemonSetAppliesTheControllerRevisionPatch(t *testing.T) {
	client := fake.NewSimpleClientset(
		testDaemonSetObj("kube-system", "agent"),
		testControllerRevision("kube-system", "agent-aaaa", "DaemonSet", "agent", 1, "fluentd:1.14"),
		testControllerRevision("kube-system", "agent-bbbb", "DaemonSet", "agent", 2, "fluentd:1.15"),
	)
	adapter := newTestAdapter("dev", client)

	if _, err := adapter.RollbackWorkload(context.Background(), "dev", domain.WorkloadDaemonSet, "kube-system", "agent", 1, false); err != nil {
		t.Fatalf("RollbackWorkload() error = %v", err)
	}

	got, err := client.AppsV1().DaemonSets("kube-system").Get(context.Background(), "agent", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting daemonset: %v", err)
	}
	containers := got.Spec.Template.Spec.Containers
	if len(containers) != 1 || containers[0].Image != "fluentd:1.14" {
		t.Errorf("daemonset template containers = %v, want the revision-1 image", containers)
	}
}

func TestRollbackControllerRevisionMissingRevisionIsNotFound(t *testing.T) {
	client := fake.NewSimpleClientset(
		testStatefulSetObj("data", "db"),
		testControllerRevision("data", "db-aaaa", "StatefulSet", "db", 1, "postgres:13"),
	)
	adapter := newTestAdapter("dev", client)

	_, err := adapter.RollbackWorkload(context.Background(), "dev", domain.WorkloadStatefulSet, "data", "db", 99, false)
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("RollbackWorkload(toRevision=99) error = %v, want it to wrap ports.ErrNotFound", err)
	}
}
