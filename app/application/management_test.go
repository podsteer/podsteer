package application_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
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

// newManagementServiceWithLogger is newManagementService with a caller-owned
// logger, for the tests that must inspect what was actually written — every
// other test here only cares that a call succeeded or failed.
func newManagementServiceWithLogger(t *testing.T, management *fakeManagementPort, logger *slog.Logger) *application.ManagementService {
	t.Helper()

	service, err := application.NewManagementService(application.ManagementServiceDeps{
		Management: management,
		Logger:     logger,
	})
	if err != nil {
		t.Fatalf("NewManagementService() error = %v", err)
	}
	return service
}

func TestSetSecretKeyPassesArgumentsThroughAndNeverLogsTheValue(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	management := &fakeManagementPort{}
	service := newManagementServiceWithLogger(t, management, logger)

	value := []byte("s3cr3t-value-that-must-never-appear-in-a-log-line")
	if err := service.SetSecretKey(context.Background(), "dev", "app", "creds", "password", value); err != nil {
		t.Fatalf("SetSecretKey() error = %v", err)
	}

	if !management.setSecretKeyCalled {
		t.Fatal("SetSecretKey() never reached the adapter")
	}
	if management.setSecretID != "dev" {
		t.Errorf("cluster = %q, want %q", management.setSecretID, "dev")
	}
	if management.setSecretNS != "app" {
		t.Errorf("namespace = %q, want %q", management.setSecretNS, "app")
	}
	if management.setSecretName != "creds" {
		t.Errorf("name = %q, want %q", management.setSecretName, "creds")
	}
	if management.setSecretKeyName != "password" {
		t.Errorf("key = %q, want %q", management.setSecretKeyName, "password")
	}
	if !bytes.Equal(management.setSecretValue, value) {
		t.Errorf("value = %q, want %q", management.setSecretValue, value)
	}

	logged := logs.String()
	if !strings.Contains(logged, "password") {
		t.Error("log output does not mention the key, want the audit line to name it")
	}
	if strings.Contains(logged, string(value)) {
		t.Error("log output contains the secret VALUE — the audit line must never carry it")
	}
}

func TestSetSecretKeyRefusesAnInvalidKeyBeforeTheAdapter(t *testing.T) {
	t.Parallel()

	management := &fakeManagementPort{}
	service := newManagementService(t, management)

	err := service.SetSecretKey(context.Background(), "dev", "app", "creds", "not a valid key!", []byte("x"))
	if !errors.Is(err, domain.ErrInvalidKey) {
		t.Errorf("SetSecretKey() error = %v, want %v", err, domain.ErrInvalidKey)
	}
	if management.setSecretKeyCalled {
		t.Error("SetSecretKey() reached the adapter for an invalid key")
	}
}

func TestSetSecretKeyPropagatesTheAdapterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	management := &fakeManagementPort{setSecretKeyErr: wantErr}
	service := newManagementService(t, management)

	err := service.SetSecretKey(context.Background(), "dev", "app", "creds", "password", []byte("x"))
	if !errors.Is(err, wantErr) {
		t.Errorf("SetSecretKey() error = %v, want %v", err, wantErr)
	}
}

func TestSetConfigMapKeyPassesArgumentsThroughAndNeverLogsTheValue(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	management := &fakeManagementPort{}
	service := newManagementServiceWithLogger(t, management, logger)

	const value = "the-configmap-value-must-not-appear-in-a-log-line"
	if err := service.SetConfigMapKey(context.Background(), "dev", "app", "settings", "greeting", value); err != nil {
		t.Fatalf("SetConfigMapKey() error = %v", err)
	}

	if !management.setConfigMapKeyCalled {
		t.Fatal("SetConfigMapKey() never reached the adapter")
	}
	if management.setConfigMapID != "dev" {
		t.Errorf("cluster = %q, want %q", management.setConfigMapID, "dev")
	}
	if management.setConfigMapNS != "app" {
		t.Errorf("namespace = %q, want %q", management.setConfigMapNS, "app")
	}
	if management.setConfigMapName != "settings" {
		t.Errorf("name = %q, want %q", management.setConfigMapName, "settings")
	}
	if management.setConfigMapKeyName != "greeting" {
		t.Errorf("key = %q, want %q", management.setConfigMapKeyName, "greeting")
	}
	if management.setConfigMapValue != value {
		t.Errorf("value = %q, want %q", management.setConfigMapValue, value)
	}

	logged := logs.String()
	if !strings.Contains(logged, "greeting") {
		t.Error("log output does not mention the key, want the audit line to name it")
	}
	if strings.Contains(logged, value) {
		t.Error("log output contains the configmap VALUE — the audit line must never carry it")
	}
}

func TestSetConfigMapKeyRefusesAnInvalidKeyBeforeTheAdapter(t *testing.T) {
	t.Parallel()

	management := &fakeManagementPort{}
	service := newManagementService(t, management)

	err := service.SetConfigMapKey(context.Background(), "dev", "app", "settings", "", "value")
	if !errors.Is(err, domain.ErrInvalidKey) {
		t.Errorf("SetConfigMapKey() error = %v, want %v", err, domain.ErrInvalidKey)
	}
	if management.setConfigMapKeyCalled {
		t.Error("SetConfigMapKey() reached the adapter for an invalid key")
	}
}

func TestSetConfigMapKeyPropagatesTheAdapterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	management := &fakeManagementPort{setConfigMapKeyErr: wantErr}
	service := newManagementService(t, management)

	err := service.SetConfigMapKey(context.Background(), "dev", "app", "settings", "greeting", "hi")
	if !errors.Is(err, wantErr) {
		t.Errorf("SetConfigMapKey() error = %v, want %v", err, wantErr)
	}
}
