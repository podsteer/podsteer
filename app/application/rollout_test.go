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
	"github.com/podsteer/podsteer/app/ports"
)

// The Argo Rollouts controls are ordinary writes and are held to the ordinary
// rules: refused on a read-only cluster BEFORE the port is reached, and one
// audit line naming the cluster, the namespace and the object. The refusal
// property itself is asserted alongside every other write in
// TestManagementServiceRefusesEveryWriteWhenReadOnly; what is here is the
// half that test cannot see — that the arguments arrive intact and that the
// log line exists.

func TestPromoteRolloutPassesArgumentsThroughAndAuditsThem(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	management := &fakeManagementPort{}
	service := newManagementServiceWithLogger(t, management, slog.New(slog.NewTextHandler(&logs, nil)))

	namespace, err := domain.NewNamespaceName("shop")
	if err != nil {
		t.Fatalf("building namespace: %v", err)
	}

	if err := service.PromoteRollout(context.Background(), "dev", namespace, "checkout"); err != nil {
		t.Fatalf("PromoteRollout() error = %v", err)
	}

	if !management.promoteCalled {
		t.Fatal("PromoteRollout() never reached the port")
	}
	if management.rolloutID != "dev" || management.rolloutNamespace != namespace || management.rolloutName != "checkout" {
		t.Errorf("port saw %q/%q/%q, want dev/shop/checkout",
			management.rolloutID, management.rolloutNamespace, management.rolloutName)
	}

	// A write leaves a line naming what it acted on. The cluster, the
	// namespace and the name are all in it, because "a rollout was promoted"
	// with none of them is not an audit trail.
	line := logs.String()
	for _, want := range []string{"promoting rollout", "dev", "shop", "checkout"} {
		if !strings.Contains(line, want) {
			t.Errorf("audit line %q does not name %q", line, want)
		}
	}
}

func TestAbortRolloutPassesArgumentsThroughAndAuditsThem(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	management := &fakeManagementPort{}
	service := newManagementServiceWithLogger(t, management, slog.New(slog.NewTextHandler(&logs, nil)))

	namespace, err := domain.NewNamespaceName("shop")
	if err != nil {
		t.Fatalf("building namespace: %v", err)
	}

	if err := service.AbortRollout(context.Background(), "dev", namespace, "checkout"); err != nil {
		t.Fatalf("AbortRollout() error = %v", err)
	}

	if !management.abortCalled {
		t.Fatal("AbortRollout() never reached the port")
	}
	if !strings.Contains(logs.String(), "aborting rollout") {
		t.Errorf("audit line %q does not record the abort", logs.String())
	}
}

func TestRolloutControlsRefuseOnAReadOnlyClusterWithoutReachingThePort(t *testing.T) {
	t.Parallel()

	const id domain.ClusterID = "prod"

	registry := application.NewRegistry()
	registry.SetReadOnly(id, true)

	management := &fakeManagementPort{}
	service := newManagementService(t, management, registry)

	namespace, err := domain.NewNamespaceName("shop")
	if err != nil {
		t.Fatalf("building namespace: %v", err)
	}

	if err := service.PromoteRollout(context.Background(), id, namespace, "checkout"); !errors.Is(err, ports.ErrReadOnly) {
		t.Errorf("PromoteRollout() error = %v, want ErrReadOnly", err)
	}
	if err := service.AbortRollout(context.Background(), id, namespace, "checkout"); !errors.Is(err, ports.ErrReadOnly) {
		t.Errorf("AbortRollout() error = %v, want ErrReadOnly", err)
	}

	// THE PART THAT MATTERS. A guard that returned the right error after the
	// patch had already gone would pass every assertion above.
	if management.promoteCalled || management.abortCalled {
		t.Error("a refused rollout control still reached the adapter")
	}
}

func TestPromoteRolloutReportsTheAdaptersFailure(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("the controller rejected it")
	management := &fakeManagementPort{rolloutErr: wantErr}
	service := newManagementService(t, management, application.NewRegistry())

	namespace, err := domain.NewNamespaceName("shop")
	if err != nil {
		t.Fatalf("building namespace: %v", err)
	}

	if err := service.PromoteRollout(context.Background(), "dev", namespace, "checkout"); !errors.Is(err, wantErr) {
		t.Errorf("PromoteRollout() error = %v, want %v", err, wantErr)
	}
}
