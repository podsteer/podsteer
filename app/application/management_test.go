package application_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"fmt"
	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// newManagementService wires a management service over a fake port.
func newManagementService(t *testing.T, management *fakeManagementPort, registry *application.Registry) *application.ManagementService {
	t.Helper()

	service, err := application.NewManagementService(application.ManagementServiceDeps{
		Management: management,
		Registry:   registry,
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
	service := newManagementService(t, management, application.NewRegistry())

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
	service := newManagementService(t, management, application.NewRegistry())

	_, err := service.TriggerCronJob(context.Background(), "dev", "batch", "nightly")
	if !errors.Is(err, wantErr) {
		t.Errorf("TriggerCronJob() error = %v, want %v", err, wantErr)
	}
}

func TestSuspendWorkloadRejectsAKindThatDoesNotSupportIt(t *testing.T) {
	t.Parallel()

	management := &fakeManagementPort{}
	service := newManagementService(t, management, application.NewRegistry())

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
			service := newManagementService(t, management, application.NewRegistry())

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
	service := newManagementService(t, management, application.NewRegistry())

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
		Registry:   application.NewRegistry(),
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
	service := newManagementService(t, management, application.NewRegistry())

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
	service := newManagementService(t, management, application.NewRegistry())

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
	service := newManagementService(t, management, application.NewRegistry())

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
	service := newManagementService(t, management, application.NewRegistry())

	err := service.SetConfigMapKey(context.Background(), "dev", "app", "settings", "greeting", "hi")
	if !errors.Is(err, wantErr) {
		t.Errorf("SetConfigMapKey() error = %v, want %v", err, wantErr)
	}
}

func TestSetImagePassesArgumentsThrough(t *testing.T) {
	t.Parallel()

	management := &fakeManagementPort{}
	service := newManagementService(t, management, application.NewRegistry())

	err := service.SetImage(context.Background(), "dev", domain.WorkloadDeployment, "web", "api", "app", "nginx:1.25.3", false)
	if err != nil {
		t.Fatalf("SetImage() error = %v", err)
	}

	if !management.setImageCalled {
		t.Fatal("SetImage() never reached the adapter")
	}
	if management.setImageID != "dev" {
		t.Errorf("cluster = %q, want %q", management.setImageID, "dev")
	}
	if management.setImageKind != domain.WorkloadDeployment {
		t.Errorf("kind = %q, want %q", management.setImageKind, domain.WorkloadDeployment)
	}
	if management.setImageNS != "web" {
		t.Errorf("namespace = %q, want %q", management.setImageNS, "web")
	}
	if management.setImageName != "api" {
		t.Errorf("name = %q, want %q", management.setImageName, "api")
	}
	if management.setImageContainer != "app" {
		t.Errorf("container = %q, want %q", management.setImageContainer, "app")
	}
	if management.setImageValue != "nginx:1.25.3" {
		t.Errorf("image = %q, want %q", management.setImageValue, "nginx:1.25.3")
	}
	if management.setImageInit {
		t.Error("initContainer = true, want false")
	}
}

// TestSetImageAcceptsTheThreeSupportedKinds mirrors
// TestSuspendWorkloadAcceptsCronJobsAndJobs: SetImage supports exactly the
// three controller kinds whose pod template sits at spec.template.
func TestSetImageAcceptsTheThreeSupportedKinds(t *testing.T) {
	t.Parallel()

	for _, kind := range []domain.WorkloadKind{domain.WorkloadDeployment, domain.WorkloadStatefulSet, domain.WorkloadDaemonSet} {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()

			management := &fakeManagementPort{}
			service := newManagementService(t, management, application.NewRegistry())

			if err := service.SetImage(context.Background(), "dev", kind, "web", "api", "app", "nginx:1.25", true); err != nil {
				t.Fatalf("SetImage() error = %v", err)
			}
			if !management.setImageCalled {
				t.Fatal("SetImage() never reached the adapter")
			}
			if !management.setImageInit {
				t.Error("initContainer = false, want true — the flag must reach the adapter")
			}
		})
	}
}

func TestSetImageRejectsAKindThatDoesNotSupportIt(t *testing.T) {
	t.Parallel()

	management := &fakeManagementPort{}
	service := newManagementService(t, management, application.NewRegistry())

	err := service.SetImage(context.Background(), "dev", domain.WorkloadCronJob, "batch", "nightly", "worker", "busybox:1.36", false)
	if !errors.Is(err, domain.ErrUnsupportedWorkloadKind) {
		t.Errorf("SetImage() error = %v, want %v", err, domain.ErrUnsupportedWorkloadKind)
	}

	// The whole point of validating here rather than in the adapter: an
	// unsupported kind must never cost a round trip to the cluster.
	if management.setImageCalled {
		t.Error("SetImage() reached the adapter for an unsupported kind")
	}
}

func TestSetImageRejectsAnEmptyContainerBeforeTheAdapter(t *testing.T) {
	t.Parallel()

	management := &fakeManagementPort{}
	service := newManagementService(t, management, application.NewRegistry())

	err := service.SetImage(context.Background(), "dev", domain.WorkloadDeployment, "web", "api", "", "nginx:1.25", false)
	if err == nil {
		t.Error("SetImage() with an empty container succeeded, want an error")
	}
	if management.setImageCalled {
		t.Error("SetImage() reached the adapter for an empty container")
	}
}

func TestSetImageRejectsAnInvalidImageReferenceBeforeTheAdapter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		image string
	}{
		{"empty", ""},
		{"whitespace", "ngi nx:1.25"},
		{"empty tag", "nginx:"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			management := &fakeManagementPort{}
			service := newManagementService(t, management, application.NewRegistry())

			err := service.SetImage(context.Background(), "dev", domain.WorkloadDeployment, "web", "api", "app", tt.image, false)
			if tt.image == "" {
				// Refused as an empty image, checked before ValidImageReference.
				if err == nil {
					t.Error("SetImage() with an empty image succeeded, want an error")
				}
			} else if !errors.Is(err, domain.ErrInvalidImageReference) {
				t.Errorf("SetImage() error = %v, want %v", err, domain.ErrInvalidImageReference)
			}
			if management.setImageCalled {
				t.Error("SetImage() reached the adapter for an invalid image")
			}
		})
	}
}

func TestSetImagePropagatesTheAdapterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	management := &fakeManagementPort{setImageErr: wantErr}
	service := newManagementService(t, management, application.NewRegistry())

	err := service.SetImage(context.Background(), "dev", domain.WorkloadDeployment, "web", "api", "app", "nginx:1.25", false)
	if !errors.Is(err, wantErr) {
		t.Errorf("SetImage() error = %v, want %v", err, wantErr)
	}
}

// TestSetImageRefusesWhenReadOnly is SetImage's share of the property
// TestManagementServiceRefusesEveryWriteWhenReadOnly asserts for the older
// write methods: the guard must run BEFORE the adapter is ever reached, not
// merely produce the right error.
func TestSetImageRefusesWhenReadOnly(t *testing.T) {
	t.Parallel()

	const id domain.ClusterID = "prod"

	registry := application.NewRegistry()
	registry.SetReadOnly(id, true)

	management := &fakeManagementPort{}
	service := newManagementService(t, management, registry)

	err := service.SetImage(context.Background(), id, domain.WorkloadDeployment, "web", "api", "app", "nginx:1.25", false)
	if !errors.Is(err, ports.ErrReadOnly) {
		t.Errorf("SetImage() error = %v, want %v", err, ports.ErrReadOnly)
	}
	if management.setImageCalled {
		t.Error("SetImage() reached the adapter on a read-only cluster")
	}
}

// TestSetImagePassesWhenNotReadOnly is the other half: a cluster that is NOT
// marked read-only — including when a DIFFERENT cluster is — must still let
// SetImage through, mirroring TestManagementServicePassesEveryWriteWhenNotReadOnly.
func TestSetImagePassesWhenNotReadOnly(t *testing.T) {
	t.Parallel()

	const id domain.ClusterID = "staging"

	registry := application.NewRegistry()
	registry.SetReadOnly("prod", true)

	management := &fakeManagementPort{}
	service := newManagementService(t, management, registry)

	if err := service.SetImage(context.Background(), id, domain.WorkloadDeployment, "web", "api", "app", "nginx:1.25", false); err != nil {
		t.Fatalf("SetImage() error = %v", err)
	}
	if !management.setImageCalled {
		t.Fatal("SetImage() never reached the adapter")
	}
}

func TestRollbackWorkloadPassesArgumentsThrough(t *testing.T) {
	t.Parallel()

	management := &fakeManagementPort{rollbackOutcome: domain.RollbackOutcome{ToRevision: 3, DryRun: false}}
	service := newManagementService(t, management, application.NewRegistry())

	outcome, err := service.RollbackWorkload(context.Background(), "dev", domain.WorkloadDeployment, "web", "api", 3, false)
	if err != nil {
		t.Fatalf("RollbackWorkload() error = %v", err)
	}
	if outcome.ToRevision != 3 {
		t.Errorf("outcome.ToRevision = %d, want 3", outcome.ToRevision)
	}

	if !management.rollbackCalled {
		t.Fatal("RollbackWorkload() never reached the adapter")
	}
	if management.rollbackID != "dev" {
		t.Errorf("cluster = %q, want %q", management.rollbackID, "dev")
	}
	if management.rollbackKind != domain.WorkloadDeployment {
		t.Errorf("kind = %q, want %q", management.rollbackKind, domain.WorkloadDeployment)
	}
	if management.rollbackNS != "web" {
		t.Errorf("namespace = %q, want %q", management.rollbackNS, "web")
	}
	if management.rollbackName != "api" {
		t.Errorf("name = %q, want %q", management.rollbackName, "api")
	}
	if management.rollbackToRevision != 3 {
		t.Errorf("toRevision = %d, want 3", management.rollbackToRevision)
	}
	if management.rollbackDryRun {
		t.Error("dryRun = true, want false")
	}
}

// TestRollbackWorkloadAcceptsTheThreeSupportedKinds mirrors
// TestSetImageAcceptsTheThreeSupportedKinds: a rollback supports exactly the
// three controller kinds that carry a rollout history.
func TestRollbackWorkloadAcceptsTheThreeSupportedKinds(t *testing.T) {
	t.Parallel()

	for _, kind := range []domain.WorkloadKind{domain.WorkloadDeployment, domain.WorkloadStatefulSet, domain.WorkloadDaemonSet} {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()

			management := &fakeManagementPort{}
			service := newManagementService(t, management, application.NewRegistry())

			if _, err := service.RollbackWorkload(context.Background(), "dev", kind, "web", "api", 1, false); err != nil {
				t.Fatalf("RollbackWorkload() error = %v", err)
			}
			if !management.rollbackCalled {
				t.Fatal("RollbackWorkload() never reached the adapter")
			}
		})
	}
}

func TestRollbackWorkloadRejectsAKindThatDoesNotSupportIt(t *testing.T) {
	t.Parallel()

	management := &fakeManagementPort{}
	service := newManagementService(t, management, application.NewRegistry())

	_, err := service.RollbackWorkload(context.Background(), "dev", domain.WorkloadCronJob, "batch", "nightly", 1, false)
	if !errors.Is(err, domain.ErrUnsupportedWorkloadKind) {
		t.Errorf("RollbackWorkload() error = %v, want %v", err, domain.ErrUnsupportedWorkloadKind)
	}

	// The whole point of validating here rather than in the adapter: an
	// unsupported kind must never cost a round trip to the cluster.
	if management.rollbackCalled {
		t.Error("RollbackWorkload() reached the adapter for an unsupported kind")
	}
}

func TestRollbackWorkloadRejectsANonPositiveRevisionBeforeTheAdapter(t *testing.T) {
	t.Parallel()

	for _, revision := range []int64{0, -1} {
		t.Run(fmt.Sprintf("revision=%d", revision), func(t *testing.T) {
			t.Parallel()

			management := &fakeManagementPort{}
			service := newManagementService(t, management, application.NewRegistry())

			_, err := service.RollbackWorkload(context.Background(), "dev", domain.WorkloadDeployment, "web", "api", revision, false)
			if !errors.Is(err, domain.ErrInvalidRevision) {
				t.Errorf("RollbackWorkload() error = %v, want %v", err, domain.ErrInvalidRevision)
			}
			if management.rollbackCalled {
				t.Error("RollbackWorkload() reached the adapter for a non-positive revision")
			}
		})
	}
}

func TestRollbackWorkloadPropagatesTheAdapterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	management := &fakeManagementPort{rollbackErr: wantErr}
	service := newManagementService(t, management, application.NewRegistry())

	_, err := service.RollbackWorkload(context.Background(), "dev", domain.WorkloadDeployment, "web", "api", 1, false)
	if !errors.Is(err, wantErr) {
		t.Errorf("RollbackWorkload() error = %v, want %v", err, wantErr)
	}
}

// TestRollbackWorkloadRefusesWhenReadOnly is RollbackWorkload's share of the
// property TestManagementServiceRefusesEveryWriteWhenReadOnly asserts for
// the older write methods — a REAL rollback, dryRun false.
func TestRollbackWorkloadRefusesWhenReadOnly(t *testing.T) {
	t.Parallel()

	const id domain.ClusterID = "prod"

	registry := application.NewRegistry()
	registry.SetReadOnly(id, true)

	management := &fakeManagementPort{}
	service := newManagementService(t, management, registry)

	_, err := service.RollbackWorkload(context.Background(), id, domain.WorkloadDeployment, "web", "api", 1, false)
	if !errors.Is(err, ports.ErrReadOnly) {
		t.Errorf("RollbackWorkload() error = %v, want %v", err, ports.ErrReadOnly)
	}
	if management.rollbackCalled {
		t.Error("RollbackWorkload() reached the adapter on a read-only cluster")
	}
}

// TestRollbackWorkloadAllowsDryRunOnAReadOnlyCluster mirrors
// TestManagementServiceUpdateResourceAllowsDryRunOnAReadOnlyCluster: a
// Preview asks the API server to validate and persists nothing, so it is
// exactly as safe against a read-only cluster as any other read.
func TestRollbackWorkloadAllowsDryRunOnAReadOnlyCluster(t *testing.T) {
	t.Parallel()

	const id domain.ClusterID = "prod"

	registry := application.NewRegistry()
	registry.SetReadOnly(id, true)

	management := &fakeManagementPort{}
	service := newManagementService(t, management, registry)

	if _, err := service.RollbackWorkload(context.Background(), id, domain.WorkloadDeployment, "web", "api", 1, true); err != nil {
		t.Fatalf("RollbackWorkload(dryRun=true) on a read-only cluster error = %v, want nil", err)
	}
	if !management.rollbackCalled {
		t.Fatal("RollbackWorkload(dryRun=true) never reached the adapter — a dry run must not be refused by the read-only guard")
	}
}

// TestRollbackWorkloadPassesWhenNotReadOnly mirrors TestSetImagePassesWhenNotReadOnly.
func TestRollbackWorkloadPassesWhenNotReadOnly(t *testing.T) {
	t.Parallel()

	const id domain.ClusterID = "staging"

	registry := application.NewRegistry()
	registry.SetReadOnly("prod", true)

	management := &fakeManagementPort{}
	service := newManagementService(t, management, registry)

	if _, err := service.RollbackWorkload(context.Background(), id, domain.WorkloadDeployment, "web", "api", 1, false); err != nil {
		t.Fatalf("RollbackWorkload() error = %v", err)
	}
	if !management.rollbackCalled {
		t.Fatal("RollbackWorkload() never reached the adapter")
	}
}

func TestCordonNodePassesArgumentsThrough(t *testing.T) {
	t.Parallel()

	management := &fakeManagementPort{}
	service := newManagementService(t, management, application.NewRegistry())

	if err := service.CordonNode(context.Background(), "dev", "node-1", true); err != nil {
		t.Fatalf("CordonNode() error = %v", err)
	}

	if !management.cordonCalled {
		t.Fatal("CordonNode() never reached the adapter")
	}
	if management.cordonedID != "dev" {
		t.Errorf("cordoned cluster = %q, want %q", management.cordonedID, "dev")
	}
	if management.cordonedName != "node-1" {
		t.Errorf("cordoned name = %q, want %q", management.cordonedName, "node-1")
	}
	if !management.cordonedValue {
		t.Error("cordoned value = false, want true")
	}
}
func TestCordonNodePropagatesTheAdapterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	management := &fakeManagementPort{cordonErr: wantErr}
	service := newManagementService(t, management, application.NewRegistry())

	err := service.CordonNode(context.Background(), "dev", "node-1", false)
	if !errors.Is(err, wantErr) {
		t.Errorf("CordonNode() error = %v, want %v", err, wantErr)
	}
}
func TestEvictPodPassesArgumentsThrough(t *testing.T) {
	t.Parallel()

	management := &fakeManagementPort{}
	service := newManagementService(t, management, application.NewRegistry())

	if err := service.EvictPod(context.Background(), "dev", "batch", "worker-1", 30); err != nil {
		t.Fatalf("EvictPod() error = %v", err)
	}

	if !management.evictCalled {
		t.Fatal("EvictPod() never reached the adapter")
	}
	if management.evictedID != "dev" {
		t.Errorf("evicted cluster = %q, want %q", management.evictedID, "dev")
	}
	if management.evictedNS != "batch" {
		t.Errorf("evicted namespace = %q, want %q", management.evictedNS, "batch")
	}
	if management.evictedName != "worker-1" {
		t.Errorf("evicted name = %q, want %q", management.evictedName, "worker-1")
	}
	if management.evictedGrace != 30 {
		t.Errorf("evicted grace period = %d, want 30", management.evictedGrace)
	}
}
func TestEvictPodPropagatesTheAdapterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	management := &fakeManagementPort{evictErr: wantErr}
	service := newManagementService(t, management, application.NewRegistry())

	err := service.EvictPod(context.Background(), "dev", "batch", "worker-1", -1)
	if !errors.Is(err, wantErr) {
		t.Errorf("EvictPod() error = %v, want %v", err, wantErr)
	}
}
func TestDrainNodePassesArgumentsThroughAndReturnsTheReport(t *testing.T) {
	t.Parallel()

	wantReport := domain.DrainReport{Cordoned: true, Evicted: []domain.Pod{mustPod(t, "batch", "worker-1")}}
	opts := domain.DrainOptions{Force: true, DeleteEmptyDirData: true, GracePeriodSeconds: 5}
	management := &fakeManagementPort{drainReport: wantReport}
	service := newManagementService(t, management, application.NewRegistry())

	report, err := service.DrainNode(context.Background(), "dev", "node-1", opts)
	if err != nil {
		t.Fatalf("DrainNode() error = %v", err)
	}
	if len(report.Evicted) != 1 || report.Evicted[0].Name() != "worker-1" {
		t.Errorf("DrainNode() report = %+v, want the fake's report", report)
	}
	if !report.Cordoned {
		t.Error("report.Cordoned = false, want true")
	}

	if !management.drainCalled {
		t.Fatal("DrainNode() never reached the adapter")
	}
	if management.drainedID != "dev" {
		t.Errorf("drained cluster = %q, want %q", management.drainedID, "dev")
	}
	if management.drainedName != "node-1" {
		t.Errorf("drained name = %q, want %q", management.drainedName, "node-1")
	}
	if management.drainedOpts != opts {
		t.Errorf("drained opts = %+v, want %+v", management.drainedOpts, opts)
	}
}

// TestDrainNodeReturnsTheReportEvenWhenRefused proves the report is not
// discarded just because the call also failed — an ErrDrainRefused still
// cordoned the node, and the caller needs to see what was refused and why.
func TestDrainNodeReturnsTheReportEvenWhenRefused(t *testing.T) {
	t.Parallel()

	wantReport := domain.DrainReport{
		Cordoned: true,
		Refused:  []domain.DrainRefusal{{Pod: mustPod(t, "batch", "standalone"), Reason: domain.DrainReasonBarePod}},
	}
	management := &fakeManagementPort{
		drainReport: wantReport,
		drainErr:    fmt.Errorf("draining node %q: %w", "node-1", ports.ErrDrainRefused),
	}
	service := newManagementService(t, management, application.NewRegistry())

	report, err := service.DrainNode(context.Background(), "dev", "node-1", domain.DrainOptions{})
	if !errors.Is(err, ports.ErrDrainRefused) {
		t.Errorf("DrainNode() error = %v, want %v", err, ports.ErrDrainRefused)
	}
	if !report.Cordoned {
		t.Error("report.Cordoned = false, want true even on refusal")
	}
	if len(report.Refused) != 1 {
		t.Errorf("report.Refused = %+v, want exactly 1", report.Refused)
	}
}

// mustResourceRef builds a minimal ResourceRef naming id, or fails the test.
func mustResourceRef(t *testing.T, id domain.ClusterID) domain.ResourceRef {
	t.Helper()
	ns, err := domain.NewNamespaceName("default")
	if err != nil {
		t.Fatalf("building namespace: %v", err)
	}
	return domain.ResourceRef{
		ClusterID: id,
		Kind:      domain.ResourceKind{Group: "", Version: "v1", Kind: "Pod"},
		Namespace: ns,
		Name:      "example",
	}
}

// TestManagementServiceRefusesEveryWriteWhenReadOnly is the property the
// whole feature exists for: every mutating method on ManagementService
// returns ports.ErrReadOnly, and — the part that actually matters — none of
// them reach the port. A check that ran AFTER the write, or that logged the
// refusal without stopping it, would pass a test asserting only the returned
// error.
func TestManagementServiceRefusesEveryWriteWhenReadOnly(t *testing.T) {
	t.Parallel()

	const id domain.ClusterID = "prod"
	ns, err := domain.NewNamespaceName("default")
	if err != nil {
		t.Fatalf("building namespace: %v", err)
	}

	registry := application.NewRegistry()
	registry.SetReadOnly(id, true)

	port := &fakeManagementPort{}
	service := newManagementService(t, port, registry)

	ctx := context.Background()
	cases := []struct {
		name string
		call func() error
	}{
		{"DeleteResource", func() error {
			return service.DeleteResource(ctx, mustResourceRef(t, id))
		}},
		{"ScaleWorkload", func() error {
			return service.ScaleWorkload(ctx, id, domain.WorkloadKind("Deployment"), ns, "web", 3)
		}},
		{"RestartRollout", func() error {
			return service.RestartRollout(ctx, id, domain.WorkloadKind("Deployment"), ns, "web")
		}},
		{"UpdateResource", func() error {
			_, err := service.UpdateResource(ctx, id, "kind: Pod", false)
			return err
		}},
		{"ExecInPod", func() error {
			var stdout, stderr bytes.Buffer
			return service.ExecInPod(ctx, id, ns, "web-0", "app", []string{"true"}, nil, &stdout, &stderr, false)
		}},
		{"ExecInPodWithTTY", func() error {
			var stdout, stderr bytes.Buffer
			return service.ExecInPodWithTTY(ctx, id, ns, "web-0", "app", []string{"/bin/sh"}, nil, &stdout, &stderr, nil)
		}},
		{"AttachToPod", func() error {
			var stdout, stderr bytes.Buffer
			return service.AttachToPod(ctx, id, ns, "web-0", "app", nil, &stdout, &stderr, nil)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if !errors.Is(err, ports.ErrReadOnly) {
				t.Fatalf("%s() error = %v, want wrapping ports.ErrReadOnly", tc.name, err)
			}
		})
	}

	// THE PART A "does it return the right error" test would miss: the port
	// underneath never saw any of them.
	if calls := port.recordedCalls(); len(calls) != 0 {
		t.Fatalf("port recorded calls %v, want none — a refused write must never reach the adapter", calls)
	}
}

// TestManagementServiceUpdateResourceAllowsDryRunOnAReadOnlyCluster is the
// one deliberate exception to the property above: a dry run persists
// nothing, so it must reach the port even on a cluster marked read-only —
// otherwise "Validate" would be the one control OrganiseDialog's read-only
// toggle disables despite changing nothing, which is not what the toggle is
// for. A REAL apply on the same cluster must still be refused, which the
// second half of this test checks so the exception cannot regress into "dry
// run just skips the check for everyone".
func TestManagementServiceUpdateResourceAllowsDryRunOnAReadOnlyCluster(t *testing.T) {
	t.Parallel()

	const id domain.ClusterID = "prod"

	registry := application.NewRegistry()
	registry.SetReadOnly(id, true)

	port := &fakeManagementPort{}
	service := newManagementService(t, port, registry)

	ctx := context.Background()

	if _, err := service.UpdateResource(ctx, id, "kind: Pod", true); err != nil {
		t.Fatalf("UpdateResource(dryRun=true) on a read-only cluster error = %v, want nil", err)
	}
	if calls := port.recordedCalls(); len(calls) != 1 || calls[0] != "UpdateResource" {
		t.Fatalf("port recorded calls %v, want exactly one UpdateResource — a dry run must reach the port", calls)
	}

	if _, err := service.UpdateResource(ctx, id, "kind: Pod", false); !errors.Is(err, ports.ErrReadOnly) {
		t.Fatalf("UpdateResource(dryRun=false) on a read-only cluster error = %v, want wrapping ports.ErrReadOnly", err)
	}
	// Still exactly one recorded call: the real apply above must have been
	// refused before it ever reached the port.
	if calls := port.recordedCalls(); len(calls) != 1 {
		t.Fatalf("port recorded calls %v, want still exactly one — the real apply must never reach the adapter", calls)
	}
}

// TestManagementServicePassesEveryWriteWhenNotReadOnly is the other half:
// the guard must refuse ONLY a cluster actually marked, never as a side
// effect of existing. Every method below must reach the port when nothing
// marked the cluster.
func TestManagementServicePassesEveryWriteWhenNotReadOnly(t *testing.T) {
	t.Parallel()

	const id domain.ClusterID = "staging"
	ns, err := domain.NewNamespaceName("default")
	if err != nil {
		t.Fatalf("building namespace: %v", err)
	}

	registry := application.NewRegistry()
	// Deliberately not marked — and another cluster IS marked, so the guard
	// is proven to be per-cluster rather than a global switch that happens to
	// default off.
	registry.SetReadOnly("prod", true)

	port := &fakeManagementPort{}
	service := newManagementService(t, port, registry)

	ctx := context.Background()
	cases := []struct {
		name string
		call func() error
		want string
	}{
		{"DeleteResource", func() error {
			return service.DeleteResource(ctx, mustResourceRef(t, id))
		}, "DeleteResource"},
		{"ScaleWorkload", func() error {
			return service.ScaleWorkload(ctx, id, domain.WorkloadKind("Deployment"), ns, "web", 3)
		}, "ScaleWorkload"},
		{"RestartRollout", func() error {
			return service.RestartRollout(ctx, id, domain.WorkloadKind("Deployment"), ns, "web")
		}, "RestartRollout"},
		{"UpdateResource", func() error {
			_, err := service.UpdateResource(ctx, id, "kind: Pod", false)
			return err
		}, "UpdateResource"},
		{"ExecInPod", func() error {
			var stdout, stderr bytes.Buffer
			return service.ExecInPod(ctx, id, ns, "web-0", "app", []string{"true"}, nil, &stdout, &stderr, false)
		}, "ExecInPod"},
		{"ExecInPodWithTTY", func() error {
			var stdout, stderr bytes.Buffer
			return service.ExecInPodWithTTY(ctx, id, ns, "web-0", "app", []string{"/bin/sh"}, nil, &stdout, &stderr, nil)
		}, "ExecInPodWithTTY"},
		{"AttachToPod", func() error {
			var stdout, stderr bytes.Buffer
			return service.AttachToPod(ctx, id, ns, "web-0", "app", nil, &stdout, &stderr, nil)
		}, "AttachToPod"},
	}

	var want []string
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err != nil {
				t.Fatalf("%s() error = %v, want nil", tc.name, err)
			}
		})
		want = append(want, tc.want)
	}

	got := port.recordedCalls()
	if len(got) != len(want) {
		t.Fatalf("port recorded calls %v, want %v", got, want)
	}
	for i, name := range want {
		if got[i] != name {
			t.Fatalf("call %d = %q, want %q", i, got[i], name)
		}
	}
}

// TestManagementServiceStreamLogsIgnoresReadOnly pins the one deliberate
// exception: a log stream changes nothing about the cluster, so a read-only
// mark must never touch it. This is the case CLAUDE.md calls out by name —
// "log streaming and port-forwarding stay allowed".
func TestManagementServiceStreamLogsIgnoresReadOnly(t *testing.T) {
	t.Parallel()

	const id domain.ClusterID = "prod"
	ns, err := domain.NewNamespaceName("default")
	if err != nil {
		t.Fatalf("building namespace: %v", err)
	}

	registry := application.NewRegistry()
	registry.SetReadOnly(id, true)

	port := &fakeManagementPort{}
	service := newManagementService(t, port, registry)

	out := make(chan string, 1)
	close(out)
	if err := service.StreamLogs(context.Background(), id, ns, "web-0", "app", false, 10, out); err != nil {
		t.Fatalf("StreamLogs() error = %v, want nil on a read-only cluster", err)
	}
	if calls := port.recordedCalls(); len(calls) != 1 || calls[0] != "StreamLogs" {
		t.Fatalf("port recorded calls %v, want [StreamLogs]", calls)
	}
}

// TestManagementServiceReadOnlyReflectsTheRegistry asserts the convenience
// accessor TerminalAPI relies on to fail fast is not a second, independent
// source of truth — it is a straight read of the same registry every write
// checks.
func TestManagementServiceReadOnlyReflectsTheRegistry(t *testing.T) {
	t.Parallel()

	const id domain.ClusterID = "prod"
	registry := application.NewRegistry()
	service := newManagementService(t, &fakeManagementPort{}, registry)

	if service.ReadOnly(id) {
		t.Fatal("ReadOnly() = true before anything marked the cluster")
	}

	registry.SetReadOnly(id, true)
	if !service.ReadOnly(id) {
		t.Fatal("ReadOnly() = false after the registry marked the cluster read-only")
	}

	registry.SetReadOnly(id, false)
	if service.ReadOnly(id) {
		t.Fatal("ReadOnly() = true after the registry lifted the mark")
	}
}

// TestNewManagementServiceRequiresARegistry guards the constructor: a
// ManagementService built without one would silently nil-panic on the first
// write, on ANY cluster, rather than refuse to construct.
func TestNewManagementServiceRequiresARegistry(t *testing.T) {
	t.Parallel()

	_, err := application.NewManagementService(application.ManagementServiceDeps{
		Management: &fakeManagementPort{},
	})
	if err == nil {
		t.Fatal("NewManagementService() error = nil, want a complaint about the missing Registry")
	}
}
