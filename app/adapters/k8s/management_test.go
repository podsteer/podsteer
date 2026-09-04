package k8s

import (
	"context"
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
