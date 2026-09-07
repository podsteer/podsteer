package wails

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// blockingClusterService answers Connect only when its context ends, standing
// in for a cluster behind a link that drops packets rather than refusing them:
// the case where an operator is left watching a control they cannot take back.
//
// It embeds the interface rather than implementing forty methods; anything
// else this test touches would panic, loudly, which is the right outcome for a
// test that reached somewhere it was not meant to.
type blockingClusterService struct {
	ports.ClusterService

	started chan domain.ClusterID
}

func (b *blockingClusterService) Connect(ctx context.Context, id domain.ClusterID) (domain.Cluster, error) {
	b.started <- id
	<-ctx.Done()
	return domain.Cluster{}, ctx.Err()
}

func newBlockingClusterAPI(t *testing.T) (*ClusterAPI, *blockingClusterService) {
	t.Helper()

	service := &blockingClusterService{started: make(chan domain.ClusterID, 4)}
	api, err := NewClusterAPI(service, NewApp(nil, time.Minute), nil)
	if err != nil {
		t.Fatalf("NewClusterAPI() error = %v", err)
	}
	return api, service
}

// TestCancelConnectStopsAnAttemptThatIsStillInTheAir is the control the picker
// gained.
//
// Without it the only way out of a connect to a cluster that answers neither
// yes nor no is to wait out the whole request timeout — a minute of a control
// that does nothing, on an application whose other clusters are fine.
func TestCancelConnectStopsAnAttemptThatIsStillInTheAir(t *testing.T) {
	t.Parallel()

	api, service := newBlockingClusterAPI(t)

	done := make(chan error, 1)
	go func() {
		_, err := api.Connect("dev")
		done <- err
	}()

	<-service.started

	if err := api.CancelConnect("dev"); err != nil {
		t.Fatalf("CancelConnect() error = %v", err)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Connect() returned nil after being cancelled")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Connect() did not return after CancelConnect — the attempt was not stopped")
	}
}

// TestOneCancelLeavesTheOtherAttemptsAlone is the whole point of keying the
// attempts by cluster.
//
// An operator opening five clusters and stopping the two that are down must
// keep the three that are not, or the control is a disconnect-everything
// button wearing a different label.
func TestOneCancelLeavesTheOtherAttemptsAlone(t *testing.T) {
	t.Parallel()

	api, service := newBlockingClusterAPI(t)

	stopped := make(chan error, 1)
	go func() {
		_, err := api.Connect("down")
		stopped <- err
	}()
	kept := make(chan error, 1)
	go func() {
		_, err := api.Connect("fine")
		kept <- err
	}()

	// Both are in the air before anything is cancelled.
	<-service.started
	<-service.started

	if err := api.CancelConnect("down"); err != nil {
		t.Fatalf("CancelConnect() error = %v", err)
	}

	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("the cancelled attempt did not return")
	}

	select {
	case err := <-kept:
		t.Fatalf("the other attempt returned too (%v) — one cancel stopped both", err)
	case <-time.After(250 * time.Millisecond):
		// Still connecting, which is the assertion.
	}
}

// TestCancelConnectWithNothingInFlightIsNotAnError covers the race the UI
// should not have to handle: the attempt finished between the operator
// pressing the control and this call arriving.
func TestCancelConnectWithNothingInFlightIsNotAnError(t *testing.T) {
	t.Parallel()

	api, _ := newBlockingClusterAPI(t)

	if err := api.CancelConnect("never-started"); err != nil {
		t.Fatalf("CancelConnect() error = %v, want nil — there was nothing to stop", err)
	}
}

// TestCancelConnectRejectsAnEmptyID keeps the boundary check every other
// method here makes.
func TestCancelConnectRejectsAnEmptyID(t *testing.T) {
	t.Parallel()

	api, _ := newBlockingClusterAPI(t)

	err := api.CancelConnect("")
	if err == nil {
		t.Fatal("CancelConnect(\"\") error = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), string(CodeInvalidInput)) {
		t.Fatalf("error = %q, want it classified as invalid input", err)
	}
}
