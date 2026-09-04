package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		factory: factory,
		logger:  slog.New(slog.DiscardHandler),
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
