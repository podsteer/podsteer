package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	clientgotesting "k8s.io/client-go/testing"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// newTestAdapter returns an Adapter whose client for id is the fake, for
// tests that exercise ManagementPort methods without a real API server.
//
// The zero value is otherwise fine: TriggerCronJob and SuspendWorkload only
// ever reach a.factory and a.reads (through forgetReads), and readCache's own
// zero value is safe to forget from.
func newTestAdapter(id domain.ClusterID, client kubernetes.Interface) *Adapter {
	factory := newClientFactory(Config{})
	factory.clients[id] = &clients{typed: client}
	return &Adapter{
		factory:    factory,
		logger:     slog.New(slog.DiscardHandler),
		nodeShells: nodeShells{byID: make(map[string]domain.NodeShell)},
	}
}

// testCronJob builds a CronJob with a job template rich enough to prove
// TriggerCronJob actually carries it over, rather than a default zero value
// happening to match.
func testCronJob(namespace, name string) *batchv1.CronJob {
	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       types.UID("uid-" + name),
		},
		Spec: batchv1.CronJobSpec{
			Schedule: "*/5 * * * *",
			JobTemplate: batchv1.JobTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      map[string]string{"app": "worker"},
					Annotations: map[string]string{"note": "keep-me"},
				},
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							RestartPolicy: corev1.RestartPolicyOnFailure,
							Containers: []corev1.Container{{
								Name:  "worker",
								Image: "busybox:1.36",
							}},
						},
					},
				},
			},
		},
	}
}

func TestTriggerCronJobCreatesAJobFromTheTemplate(t *testing.T) {
	cronJob := testCronJob("batch", "nightly")
	client := fake.NewSimpleClientset(cronJob)
	adapter := newTestAdapter("dev", client)

	jobName, err := adapter.TriggerCronJob(context.Background(), "dev", domain.NamespaceName("batch"), "nightly")
	if err != nil {
		t.Fatalf("TriggerCronJob() error = %v", err)
	}

	if !strings.HasPrefix(jobName, "nightly-manual-") {
		t.Fatalf("job name = %q, want prefix %q", jobName, "nightly-manual-")
	}

	job, err := client.BatchV1().Jobs("batch").Get(context.Background(), jobName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting created job: %v", err)
	}

	if got := job.Annotations["cronjob.kubernetes.io/instantiate"]; got != "manual" {
		t.Errorf("instantiate annotation = %q, want %q", got, "manual")
	}
	// The job template's OWN annotation must survive alongside the one added.
	if got := job.Annotations["note"]; got != "keep-me" {
		t.Errorf("carried-over annotation = %q, want %q", got, "keep-me")
	}
	if got := job.Labels["app"]; got != "worker" {
		t.Errorf("carried-over label = %q, want %q", got, "worker")
	}

	if len(job.OwnerReferences) != 1 {
		t.Fatalf("owner references = %d, want 1", len(job.OwnerReferences))
	}
	owner := job.OwnerReferences[0]
	switch {
	case owner.APIVersion != "batch/v1":
		t.Errorf("owner APIVersion = %q, want %q", owner.APIVersion, "batch/v1")
	case owner.Kind != "CronJob":
		t.Errorf("owner Kind = %q, want %q", owner.Kind, "CronJob")
	case owner.Name != cronJob.Name:
		t.Errorf("owner Name = %q, want %q", owner.Name, cronJob.Name)
	case owner.UID != cronJob.UID:
		t.Errorf("owner UID = %q, want %q", owner.UID, cronJob.UID)
	case owner.Controller == nil || !*owner.Controller:
		t.Error("owner Controller is not true, so the CronJob controller will not adopt this Job")
	case owner.BlockOwnerDeletion == nil || !*owner.BlockOwnerDeletion:
		t.Error("owner BlockOwnerDeletion is not true")
	}

	if !reflect.DeepEqual(job.Spec, cronJob.Spec.JobTemplate.Spec) {
		t.Errorf("job spec = %+v, want the cronjob's job template spec %+v", job.Spec, cronJob.Spec.JobTemplate.Spec)
	}
}

func TestTriggerCronJobTruncatesALongName(t *testing.T) {
	longName := strings.Repeat("a", 60)
	cronJob := testCronJob("batch", longName)
	client := fake.NewSimpleClientset(cronJob)
	adapter := newTestAdapter("dev", client)

	jobName, err := adapter.TriggerCronJob(context.Background(), "dev", domain.NamespaceName("batch"), longName)
	if err != nil {
		t.Fatalf("TriggerCronJob() error = %v", err)
	}

	if len(jobName) > maxObjectNameLength {
		t.Fatalf("job name %q is %d characters, want at most %d", jobName, len(jobName), maxObjectNameLength)
	}

	// "-manual-" (8) + 5 random characters = 13, so the 60-character prefix
	// must have been cut to 50.
	wantPrefix := longName[:50]
	if !strings.HasPrefix(jobName, wantPrefix+"-manual-") {
		t.Errorf("job name = %q, want prefix %q", jobName, wantPrefix+"-manual-")
	}
}

func TestTriggerCronJobOnAMissingCronJobIsNotFound(t *testing.T) {
	client := fake.NewSimpleClientset()
	adapter := newTestAdapter("dev", client)

	_, err := adapter.TriggerCronJob(context.Background(), "dev", domain.NamespaceName("batch"), "missing")
	if !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("TriggerCronJob() error = %v, want %v", err, ports.ErrNotFound)
	}
}

func TestSuspendWorkloadPatchesACronJob(t *testing.T) {
	cronJob := testCronJob("batch", "nightly")
	client := fake.NewSimpleClientset(cronJob)
	adapter := newTestAdapter("dev", client)

	if err := adapter.SuspendWorkload(context.Background(), "dev", domain.WorkloadCronJob, "batch", "nightly", true); err != nil {
		t.Fatalf("SuspendWorkload(true) error = %v", err)
	}

	got, err := client.BatchV1().CronJobs("batch").Get(context.Background(), "nightly", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting cronjob: %v", err)
	}
	if got.Spec.Suspend == nil || !*got.Spec.Suspend {
		t.Fatal("spec.suspend is not true after suspending")
	}

	// true -> false must work too, not just the initial patch.
	if err := adapter.SuspendWorkload(context.Background(), "dev", domain.WorkloadCronJob, "batch", "nightly", false); err != nil {
		t.Fatalf("SuspendWorkload(false) error = %v", err)
	}

	got, err = client.BatchV1().CronJobs("batch").Get(context.Background(), "nightly", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting cronjob: %v", err)
	}
	if got.Spec.Suspend == nil || *got.Spec.Suspend {
		t.Fatal("spec.suspend is not false after resuming")
	}
}

func TestSuspendWorkloadPatchesAJob(t *testing.T) {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "batch-run", Namespace: "batch"},
	}
	client := fake.NewSimpleClientset(job)
	adapter := newTestAdapter("dev", client)

	if err := adapter.SuspendWorkload(context.Background(), "dev", domain.WorkloadJob, "batch", "batch-run", true); err != nil {
		t.Fatalf("SuspendWorkload(true) error = %v", err)
	}

	got, err := client.BatchV1().Jobs("batch").Get(context.Background(), "batch-run", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting job: %v", err)
	}
	if got.Spec.Suspend == nil || !*got.Spec.Suspend {
		t.Fatal("spec.suspend is not true after suspending")
	}

	if err := adapter.SuspendWorkload(context.Background(), "dev", domain.WorkloadJob, "batch", "batch-run", false); err != nil {
		t.Fatalf("SuspendWorkload(false) error = %v", err)
	}

	got, err = client.BatchV1().Jobs("batch").Get(context.Background(), "batch-run", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting job: %v", err)
	}
	if got.Spec.Suspend == nil || *got.Spec.Suspend {
		t.Fatal("spec.suspend is not false after resuming")
	}
}

func TestSuspendWorkloadRefusesAnUnsupportedKind(t *testing.T) {
	client := fake.NewSimpleClientset()
	adapter := newTestAdapter("dev", client)

	err := adapter.SuspendWorkload(context.Background(), "dev", domain.WorkloadDeployment, "web", "api", true)
	if err == nil {
		t.Error("SuspendWorkload() with an unsupported kind succeeded, want an error")
	}
}

// lastPatchData returns the `data` map of the most recent patch action's
// body, so a test can assert on the shape of what was actually sent rather
// than only on the state the fake ended up in.
func lastPatchData(t *testing.T, client *fake.Clientset) map[string]any {
	t.Helper()

	actions := client.Actions()
	for i := len(actions) - 1; i >= 0; i-- {
		patch, ok := actions[i].(clientgotesting.PatchAction)
		if !ok {
			continue
		}

		var body map[string]any
		if err := json.Unmarshal(patch.GetPatch(), &body); err != nil {
			t.Fatalf("decoding patch body: %v", err)
		}
		data, _ := body["data"].(map[string]any)
		return data
	}

	t.Fatal("no patch action was recorded")
	return nil
}

func TestSetSecretKeyPatchesExactlyOneKeyAndPreservesTheRest(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "app"},
		Data:       map[string][]byte{"existing": []byte("keep-me")},
	}
	client := fake.NewSimpleClientset(secret)
	adapter := newTestAdapter("dev", client)

	value := []byte("s3cr3t! with \x00 bytes and 🔑")
	err := adapter.SetSecretKey(context.Background(), "dev", "app", "creds", "password", value)
	if err != nil {
		t.Fatalf("SetSecretKey() error = %v", err)
	}

	// The patch itself named exactly the one key being written — not the
	// whole object, and not a key that happened not to change.
	data := lastPatchData(t, client)
	if len(data) != 1 {
		t.Fatalf("patch data = %v, want exactly one key", data)
	}
	if _, ok := data["password"]; !ok {
		t.Fatalf("patch data = %v, want it to name %q", data, "password")
	}

	// Read back through the fake: corev1.Secret.Data is decoded by
	// client-go, so an exact byte match proves json.Marshal's base64
	// encoding of the []byte round-tripped correctly — the wire format
	// RevealSecretKey relies on client-go to decode on the way out.
	got, err := client.CoreV1().Secrets("app").Get(context.Background(), "creds", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting secret: %v", err)
	}
	if !reflect.DeepEqual(got.Data["password"], value) {
		t.Errorf("Data[password] = %v, want %v", got.Data["password"], value)
	}
	// The key nobody touched must still be there, unchanged.
	if string(got.Data["existing"]) != "keep-me" {
		t.Errorf("Data[existing] = %q, want %q — SetSecretKey must not disturb other keys",
			got.Data["existing"], "keep-me")
	}
}

func TestSetSecretKeyRefusesAnInvalidKey(t *testing.T) {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "app"}}
	client := fake.NewSimpleClientset(secret)
	adapter := newTestAdapter("dev", client)

	err := adapter.SetSecretKey(context.Background(), "dev", "app", "creds", "not a valid key!", []byte("x"))
	if !errors.Is(err, domain.ErrInvalidKey) {
		t.Errorf("SetSecretKey() error = %v, want %v", err, domain.ErrInvalidKey)
	}

	// Refused before any request reached the cluster.
	if len(client.Actions()) != 0 {
		t.Errorf("SetSecretKey() with an invalid key made %d requests, want 0", len(client.Actions()))
	}
}

func TestSetSecretKeyOnAMissingSecretIsNotFound(t *testing.T) {
	client := fake.NewSimpleClientset()
	adapter := newTestAdapter("dev", client)

	err := adapter.SetSecretKey(context.Background(), "dev", "app", "missing", "password", []byte("x"))
	if !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("SetSecretKey() error = %v, want %v", err, ports.ErrNotFound)
	}
}

func TestSetConfigMapKeyWritesTextAndPreservesTheRest(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "settings", Namespace: "app"},
		Data:       map[string]string{"existing": "keep-me"},
	}
	client := fake.NewSimpleClientset(cm)
	adapter := newTestAdapter("dev", client)

	err := adapter.SetConfigMapKey(context.Background(), "dev", "app", "settings", "greeting", "hello world")
	if err != nil {
		t.Fatalf("SetConfigMapKey() error = %v", err)
	}

	data := lastPatchData(t, client)
	if len(data) != 1 {
		t.Fatalf("patch data = %v, want exactly one key", data)
	}
	if got := data["greeting"]; got != "hello world" {
		t.Fatalf("patch data[greeting] = %v, want %q", got, "hello world")
	}

	got, err := client.CoreV1().ConfigMaps("app").Get(context.Background(), "settings", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting configmap: %v", err)
	}
	if got.Data["greeting"] != "hello world" {
		t.Errorf("Data[greeting] = %q, want %q", got.Data["greeting"], "hello world")
	}
	if got.Data["existing"] != "keep-me" {
		t.Errorf("Data[existing] = %q, want %q — SetConfigMapKey must not disturb other keys",
			got.Data["existing"], "keep-me")
	}
}

func TestSetConfigMapKeyRefusesAKeyThatHoldsBinaryData(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "settings", Namespace: "app"},
		BinaryData: map[string][]byte{"blob": {0x01, 0x02, 0x03}},
	}
	client := fake.NewSimpleClientset(cm)
	adapter := newTestAdapter("dev", client)

	err := adapter.SetConfigMapKey(context.Background(), "dev", "app", "settings", "blob", "oops")
	if !errors.Is(err, domain.ErrInvalidKey) {
		t.Errorf("SetConfigMapKey() error = %v, want %v", err, domain.ErrInvalidKey)
	}

	// A text write must never have reached the cluster: writing it would
	// have left the binary entry in place and added a second, text one
	// under the same name in `data`, which is not what refusing means.
	got, getErr := client.CoreV1().ConfigMaps("app").Get(context.Background(), "settings", metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("getting configmap: %v", getErr)
	}
	if _, inData := got.Data["blob"]; inData {
		t.Error("blob appeared in Data after a refused write — the binaryData key was moved rather than left alone")
	}
	if !reflect.DeepEqual(got.BinaryData["blob"], []byte{0x01, 0x02, 0x03}) {
		t.Errorf("BinaryData[blob] = %v, want it untouched", got.BinaryData["blob"])
	}
}

func TestSetConfigMapKeyRefusesAnInvalidKey(t *testing.T) {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "settings", Namespace: "app"}}
	client := fake.NewSimpleClientset(cm)
	adapter := newTestAdapter("dev", client)

	err := adapter.SetConfigMapKey(context.Background(), "dev", "app", "settings", "", "value")
	if !errors.Is(err, domain.ErrInvalidKey) {
		t.Errorf("SetConfigMapKey() error = %v, want %v", err, domain.ErrInvalidKey)
	}

	// Refused before even the Get that the binaryData check needs.
	if len(client.Actions()) != 0 {
		t.Errorf("SetConfigMapKey() with an invalid key made %d requests, want 0", len(client.Actions()))
	}
}

func TestSetConfigMapKeyOnAMissingConfigMapIsNotFound(t *testing.T) {
	client := fake.NewSimpleClientset()
	adapter := newTestAdapter("dev", client)

	err := adapter.SetConfigMapKey(context.Background(), "dev", "app", "missing", "greeting", "hi")
	if !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("SetConfigMapKey() error = %v, want %v", err, ports.ErrNotFound)
	}
}

// testDeployment builds a Deployment with two containers and one init
// container, rich enough to prove SetImage patches exactly the one named and
// leaves every other container — including the init container — untouched.
func testDeployment(namespace, name string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{Name: "migrate", Image: "myapp/migrate:1.0.0"},
					},
					Containers: []corev1.Container{
						{Name: "app", Image: "myapp/web:1.0.0"},
						{Name: "sidecar", Image: "myapp/proxy:1.0.0"},
					},
				},
			},
		},
	}
}

func TestSetImagePatchesOnlyTheNamedContainer(t *testing.T) {
	client := fake.NewSimpleClientset(testDeployment("web", "api"))
	adapter := newTestAdapter("dev", client)

	err := adapter.SetImage(context.Background(), "dev", domain.WorkloadDeployment, "web", "api", "app", "myapp/web:2.0.0", false)
	if err != nil {
		t.Fatalf("SetImage() error = %v", err)
	}

	got, err := client.AppsV1().Deployments("web").Get(context.Background(), "api", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting deployment: %v", err)
	}

	containers := got.Spec.Template.Spec.Containers
	if len(containers) != 2 {
		t.Fatalf("containers = %v, want 2 — SetImage must not add or remove containers", containers)
	}
	for _, c := range containers {
		switch c.Name {
		case "app":
			if c.Image != "myapp/web:2.0.0" {
				t.Errorf("container %q image = %q, want %q", c.Name, c.Image, "myapp/web:2.0.0")
			}
		case "sidecar":
			if c.Image != "myapp/proxy:1.0.0" {
				t.Errorf("container %q image = %q, want it untouched (%q)", c.Name, c.Image, "myapp/proxy:1.0.0")
			}
		default:
			t.Errorf("unexpected container %q", c.Name)
		}
	}

	// The init container is a different list entirely — a merge patch on
	// `containers` must never touch it.
	initContainers := got.Spec.Template.Spec.InitContainers
	if len(initContainers) != 1 || initContainers[0].Image != "myapp/migrate:1.0.0" {
		t.Errorf("initContainers = %v, want untouched", initContainers)
	}
}

func TestSetImageOnAnInitContainerPatchesInitContainersInstead(t *testing.T) {
	client := fake.NewSimpleClientset(testDeployment("web", "api"))
	adapter := newTestAdapter("dev", client)

	err := adapter.SetImage(context.Background(), "dev", domain.WorkloadDeployment, "web", "api", "migrate", "myapp/migrate:2.0.0", true)
	if err != nil {
		t.Fatalf("SetImage() error = %v", err)
	}

	got, err := client.AppsV1().Deployments("web").Get(context.Background(), "api", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting deployment: %v", err)
	}

	initContainers := got.Spec.Template.Spec.InitContainers
	if len(initContainers) != 1 || initContainers[0].Image != "myapp/migrate:2.0.0" {
		t.Fatalf("initContainers = %v, want migrate patched to myapp/migrate:2.0.0", initContainers)
	}

	// The regular containers are a different list — untouched by the
	// initContainer flag routing the patch elsewhere.
	containers := got.Spec.Template.Spec.Containers
	for _, c := range containers {
		if c.Name == "app" && c.Image != "myapp/web:1.0.0" {
			t.Errorf("container %q image = %q, want it untouched (%q)", c.Name, c.Image, "myapp/web:1.0.0")
		}
	}
}

func TestSetImageOnAStatefulSetAndDaemonSet(t *testing.T) {
	tests := []struct {
		kind domain.WorkloadKind
	}{
		{domain.WorkloadStatefulSet},
		{domain.WorkloadDaemonSet},
	}

	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			var client *fake.Clientset
			switch tt.kind {
			case domain.WorkloadStatefulSet:
				client = fake.NewSimpleClientset(&appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "data"},
					Spec: appsv1.StatefulSetSpec{
						Template: corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{{Name: "db", Image: "postgres:15"}},
							},
						},
					},
				})
			case domain.WorkloadDaemonSet:
				client = fake.NewSimpleClientset(&appsv1.DaemonSet{
					ObjectMeta: metav1.ObjectMeta{Name: "agent", Namespace: "data"},
					Spec: appsv1.DaemonSetSpec{
						Template: corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{{Name: "db", Image: "postgres:15"}},
							},
						},
					},
				})
			}
			adapter := newTestAdapter("dev", client)

			name := map[domain.WorkloadKind]string{domain.WorkloadStatefulSet: "db", domain.WorkloadDaemonSet: "agent"}[tt.kind]
			err := adapter.SetImage(context.Background(), "dev", tt.kind, "data", name, "db", "postgres:16", false)
			if err != nil {
				t.Fatalf("SetImage() error = %v", err)
			}

			var image string
			switch tt.kind {
			case domain.WorkloadStatefulSet:
				got, err := client.AppsV1().StatefulSets("data").Get(context.Background(), "db", metav1.GetOptions{})
				if err != nil {
					t.Fatalf("getting statefulset: %v", err)
				}
				image = got.Spec.Template.Spec.Containers[0].Image
			case domain.WorkloadDaemonSet:
				got, err := client.AppsV1().DaemonSets("data").Get(context.Background(), "agent", metav1.GetOptions{})
				if err != nil {
					t.Fatalf("getting daemonset: %v", err)
				}
				image = got.Spec.Template.Spec.Containers[0].Image
			}
			if image != "postgres:16" {
				t.Errorf("image = %q, want %q", image, "postgres:16")
			}
		})
	}
}

func TestSetImageRefusesAnUnsupportedKind(t *testing.T) {
	client := fake.NewSimpleClientset()
	adapter := newTestAdapter("dev", client)

	err := adapter.SetImage(context.Background(), "dev", domain.WorkloadCronJob, "batch", "nightly", "worker", "busybox:1.36", false)
	if err == nil {
		t.Error("SetImage() with an unsupported kind succeeded, want an error")
	}

	// Refused before any request reached the cluster — the same defence in
	// depth SuspendWorkload's own unsupported-kind test asserts.
	if len(client.Actions()) != 0 {
		t.Errorf("SetImage() with an unsupported kind made %d requests, want 0", len(client.Actions()))
	}
}

func TestSetImageOnAMissingWorkloadIsNotFound(t *testing.T) {
	client := fake.NewSimpleClientset()
	adapter := newTestAdapter("dev", client)

	err := adapter.SetImage(context.Background(), "dev", domain.WorkloadDeployment, "web", "missing", "app", "nginx:1.25", false)
	if !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("SetImage() error = %v, want %v", err, ports.ErrNotFound)
	}
}

// TestStreamLogsSendsTheRequestedOptions asserts every domain.LogOptions
// field reaches the corev1.PodLogOptions the request actually carries — the
// translation podLogOptions does, exercised through the real StreamLogs path
// rather than called directly, so a future refactor that stops using it is
// still caught.
func TestStreamLogsSendsTheRequestedOptions(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web-0", Namespace: "app"}}
	client := fake.NewSimpleClientset(pod)
	adapter := newTestAdapter("dev", client)

	var captured *corev1.PodLogOptions
	client.PrependReactor("get", "pods", func(action clientgotesting.Action) (bool, runtime.Object, error) {
		generic, ok := action.(clientgotesting.GenericAction)
		if !ok || action.GetSubresource() != "log" {
			return false, nil, nil
		}
		captured, _ = generic.GetValue().(*corev1.PodLogOptions)
		return false, nil, nil
	})

	opts := domain.LogOptions{
		Follow:       true,
		TailLines:    50,
		SinceSeconds: 300,
		Previous:     true,
		Timestamps:   true,
		LimitBytes:   1024,
	}

	out := make(chan string, 8)
	if err := adapter.StreamLogs(context.Background(), "dev", "app", "web-0", "app", opts, out); err != nil {
		t.Fatalf("StreamLogs() error = %v", err)
	}
	//nolint:revive // draining is the whole point — the lines themselves are
	// covered by TestStreamLogsDeliversTheStreamedLines.
	for range out {
	}

	if captured == nil {
		t.Fatal("no PodLogOptions reached the request")
	}
	if captured.Container != "app" {
		t.Errorf("Container = %q, want %q", captured.Container, "app")
	}
	if !captured.Follow {
		t.Error("Follow = false, want true")
	}
	if captured.TailLines == nil || *captured.TailLines != 50 {
		t.Errorf("TailLines = %v, want 50", captured.TailLines)
	}
	if captured.SinceSeconds == nil || *captured.SinceSeconds != 300 {
		t.Errorf("SinceSeconds = %v, want 300", captured.SinceSeconds)
	}
	if !captured.Previous {
		t.Error("Previous = false, want true")
	}
	if !captured.Timestamps {
		t.Error("Timestamps = false, want true")
	}
	if captured.LimitBytes == nil || *captured.LimitBytes != 1024 {
		t.Errorf("LimitBytes = %v, want 1024", captured.LimitBytes)
	}
}

// TestStreamLogsLeavesZeroFieldsUnset pins the "0 means unset" convention
// domain.LogOptions documents on TailLines, SinceSeconds and LimitBytes: each
// is a pointer field on corev1.PodLogOptions specifically because a PRESENT
// zero means something to the API server ("the last 0 lines"), so a caller
// that never set them must leave the pointers nil rather than pointing at a
// zero.
func TestStreamLogsLeavesZeroFieldsUnset(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web-0", Namespace: "app"}}
	client := fake.NewSimpleClientset(pod)
	adapter := newTestAdapter("dev", client)

	var captured *corev1.PodLogOptions
	client.PrependReactor("get", "pods", func(action clientgotesting.Action) (bool, runtime.Object, error) {
		generic, ok := action.(clientgotesting.GenericAction)
		if !ok || action.GetSubresource() != "log" {
			return false, nil, nil
		}
		captured, _ = generic.GetValue().(*corev1.PodLogOptions)
		return false, nil, nil
	})

	out := make(chan string, 8)
	if err := adapter.StreamLogs(context.Background(), "dev", "app", "web-0", "app", domain.LogOptions{Timestamps: true}, out); err != nil {
		t.Fatalf("StreamLogs() error = %v", err)
	}
	for range out {
	}

	if captured == nil {
		t.Fatal("no PodLogOptions reached the request")
	}
	if captured.TailLines != nil {
		t.Errorf("TailLines = %v, want nil", *captured.TailLines)
	}
	if captured.SinceSeconds != nil {
		t.Errorf("SinceSeconds = %v, want nil", *captured.SinceSeconds)
	}
	if captured.LimitBytes != nil {
		t.Errorf("LimitBytes = %v, want nil", *captured.LimitBytes)
	}
	if captured.Previous {
		t.Error("Previous = true, want false")
	}
}

// TestStreamLogsDeliversTheStreamedLines is a smoke test that the channel
// carries what the API server sent, one line per receive, and is closed when
// the stream ends — the contract every caller of StreamLogs relies on.
func TestStreamLogsDeliversTheStreamedLines(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web-0", Namespace: "app"}}
	client := fake.NewSimpleClientset(pod)
	adapter := newTestAdapter("dev", client)

	client.PrependReactor("get", "pods", func(action clientgotesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "log" {
			return false, nil, nil
		}
		return true, &runtime.Unknown{Raw: []byte("line one\nline two\n")}, nil
	})

	out := make(chan string, 8)
	if err := adapter.StreamLogs(context.Background(), "dev", "app", "web-0", "app", domain.LogOptions{Timestamps: true}, out); err != nil {
		t.Fatalf("StreamLogs() error = %v", err)
	}

	var lines []string
	for line := range out {
		lines = append(lines, line)
	}

	want := []string{"line one", "line two"}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("lines = %v, want %v", lines, want)
	}
}
