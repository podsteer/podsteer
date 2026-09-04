package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
)

// newManagementService wires a management service over a fake port.
func newManagementService(t *testing.T, management *fakeManagementPort) *application.ManagementService {
	t.Helper()

	service, err := application.NewManagementService(application.ManagementServiceDeps{
		Management: management,
	})
	if err != nil {
		t.Fatalf("NewManagementService() error = %v", err)
	}
	return service
}

func TestNewManagementServiceRejectsMissingDependencies(t *testing.T) {
	t.Parallel()

	if _, err := application.NewManagementService(application.ManagementServiceDeps{}); err == nil {
		t.Error("NewManagementService() succeeded without a ManagementPort, want an error")
	}
}

func TestTriggerCronJobPassesArgumentsThroughAndReturnsTheJobName(t *testing.T) {
	t.Parallel()

	management := &fakeManagementPort{triggerJobName: "nightly-manual-ab12c"}
	service := newManagementService(t, management)

	jobName, err := service.TriggerCronJob(context.Background(), "dev", "batch", "nightly")
	if err != nil {
		t.Fatalf("TriggerCronJob() error = %v", err)
	}
	if jobName != "nightly-manual-ab12c" {
		t.Errorf("TriggerCronJob() = %q, want %q", jobName, "nightly-manual-ab12c")
	}

	if management.triggeredID != "dev" {
		t.Errorf("triggered cluster = %q, want %q", management.triggeredID, "dev")
	}
	if management.triggeredNS != "batch" {
		t.Errorf("triggered namespace = %q, want %q", management.triggeredNS, "batch")
	}
	if management.triggeredName != "nightly" {
		t.Errorf("triggered name = %q, want %q", management.triggeredName, "nightly")
	}
}

func TestTriggerCronJobPropagatesTheAdapterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	management := &fakeManagementPort{triggerErr: wantErr}
	service := newManagementService(t, management)

	_, err := service.TriggerCronJob(context.Background(), "dev", "batch", "nightly")
	if !errors.Is(err, wantErr) {
		t.Errorf("TriggerCronJob() error = %v, want %v", err, wantErr)
	}
}

func TestSuspendWorkloadRejectsAKindThatDoesNotSupportIt(t *testing.T) {
	t.Parallel()

	management := &fakeManagementPort{}
	service := newManagementService(t, management)

	err := service.SuspendWorkload(context.Background(), "dev", domain.WorkloadDeployment, "web", "api", true)
	if !errors.Is(err, domain.ErrUnsupportedWorkloadKind) {
		t.Errorf("SuspendWorkload() error = %v, want %v", err, domain.ErrUnsupportedWorkloadKind)
	}

	// The whole point of validating here rather than in the adapter: an
	// unsupported kind must never cost a round trip to the cluster.
	if management.suspendCalled {
		t.Error("SuspendWorkload() reached the adapter for an unsupported kind")
	}
}

func TestSuspendWorkloadAcceptsCronJobsAndJobs(t *testing.T) {
	t.Parallel()

	for _, kind := range []domain.WorkloadKind{domain.WorkloadCronJob, domain.WorkloadJob} {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()

			management := &fakeManagementPort{}
			service := newManagementService(t, management)

			if err := service.SuspendWorkload(context.Background(), "dev", kind, "batch", "nightly", true); err != nil {
				t.Fatalf("SuspendWorkload() error = %v", err)
			}

			if !management.suspendCalled {
				t.Fatal("SuspendWorkload() never reached the adapter")
			}
			if management.suspendedID != "dev" {
				t.Errorf("suspended cluster = %q, want %q", management.suspendedID, "dev")
			}
			if management.suspendedKind != kind {
				t.Errorf("suspended kind = %q, want %q", management.suspendedKind, kind)
			}
			if management.suspendedNS != "batch" {
				t.Errorf("suspended namespace = %q, want %q", management.suspendedNS, "batch")
			}
			if management.suspendedName != "nightly" {
				t.Errorf("suspended name = %q, want %q", management.suspendedName, "nightly")
			}
			if !management.suspendedValue {
				t.Error("suspended value = false, want true")
			}
		})
	}
}

func TestSuspendWorkloadPropagatesTheAdapterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	management := &fakeManagementPort{suspendErr: wantErr}
	service := newManagementService(t, management)

	err := service.SuspendWorkload(context.Background(), "dev", domain.WorkloadCronJob, "batch", "nightly", false)
	if !errors.Is(err, wantErr) {
		t.Errorf("SuspendWorkload() error = %v, want %v", err, wantErr)
	}
}
