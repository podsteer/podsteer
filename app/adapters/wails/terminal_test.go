package wails

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// The package these live in had no tests at all, which is why `go test -race`
// never saw the two most concurrent files in the application. The size queue
// is the piece with a genuine happens-before requirement, so it is the piece
// worth asserting on.

// TestSizeQueueSendAfterCloseDoesNotPanic pins the crash this type exists to
// prevent.
//
// A send on a closed channel panics, and `select` with a `default` does not
// change that — it only avoids blocking. Before the queue owned its own
// closing, Resize read a session out of the map, the exec goroutine finished
// and closed the channel, and the send that followed took the whole desktop
// process down.
func TestSizeQueueSendAfterCloseDoesNotPanic(t *testing.T) {
	t.Parallel()

	q := newTerminalSizeQueue()
	q.close()

	// The assertion is that this returns at all.
	q.send(ports.TerminalSize{Width: 80, Height: 24})
}

// TestSizeQueueCloseIsIdempotent covers the cleanup path running twice.
func TestSizeQueueCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	q := newTerminalSizeQueue()
	q.close()
	q.close()

	if size := q.Next(); size != nil {
		t.Fatalf("Next() after close = %v, want nil", size)
	}
}

// TestSizeQueueConcurrentSendAndClose is the one that needs -race.
//
// It reproduces the real sequence — a window being dragged while the shell
// exits — with enough repetitions that an unsynchronised implementation fails
// reliably rather than occasionally.
func TestSizeQueueConcurrentSendAndClose(t *testing.T) {
	t.Parallel()

	for range 200 {
		q := newTerminalSizeQueue()

		var wg sync.WaitGroup

		// Two senders, because xterm.js emits resizes continuously during a
		// drag rather than one at a time.
		for range 2 {
			wg.Go(func() {
				for range 20 {
					q.send(ports.TerminalSize{Width: 80, Height: 24})
				}
			})
		}

		// The exec goroutine's cleanup, racing them.
		wg.Go(q.close)

		wg.Wait()
	}
}

// TestSizeQueueNextDrainsThenReportsClose checks the reader contract the
// remote shell depends on: sizes come through, and a closed queue ends the
// loop rather than yielding a zero size forever.
func TestSizeQueueNextDrainsThenReportsClose(t *testing.T) {
	t.Parallel()

	q := newTerminalSizeQueue()
	q.send(ports.TerminalSize{Width: 120, Height: 40})

	got := q.Next()
	if got == nil {
		t.Fatal("Next() = nil, want a size")
	}
	if got.Width != 120 || got.Height != 40 {
		t.Fatalf("Next() = %dx%d, want 120x40", got.Width, got.Height)
	}

	q.close()
	if size := q.Next(); size != nil {
		t.Fatalf("Next() after close = %v, want nil", size)
	}
}

// TestSizeQueueDropsWhenFull documents that a backlog is not queued.
//
// Only the latest size matters: an intermediate size from halfway through a
// drag is of no interest by the time the shell reads it.
func TestSizeQueueDropsWhenFull(t *testing.T) {
	t.Parallel()

	q := newTerminalSizeQueue()
	q.send(ports.TerminalSize{Width: 1, Height: 1})
	q.send(ports.TerminalSize{Width: 2, Height: 2})

	got := q.Next()
	if got == nil || got.Width != 1 {
		t.Fatalf("Next() = %v, want the first size", got)
	}
}

// stubManagementPort is a minimal stand-in for ports.ManagementPort, local to
// this package: application_test's own fake is unexported and lives in a
// different package. ExecInPodWithTTY errors loudly if reached at all, since
// the one thing the test below asserts is that a refused session never gets
// there.
type stubManagementPort struct{}

var _ ports.ManagementPort = (*stubManagementPort)(nil)

func (stubManagementPort) StreamLogs(context.Context, domain.ClusterID, domain.NamespaceName, string, string, domain.LogOptions, chan<- string) error {
	return nil
}
func (stubManagementPort) DeleteResource(context.Context, domain.ResourceRef) error { return nil }
func (stubManagementPort) ScaleWorkload(context.Context, domain.ClusterID, domain.WorkloadKind, domain.NamespaceName, string, int32) error {
	return nil
}
func (stubManagementPort) RestartRollout(context.Context, domain.ClusterID, domain.WorkloadKind, domain.NamespaceName, string) error {
	return nil
}
func (stubManagementPort) UpdateResource(context.Context, domain.ClusterID, string, bool) (domain.ApplyOutcome, error) {
	return domain.ApplyOutcome{}, nil
}
func (stubManagementPort) ExecInPod(context.Context, domain.ClusterID, domain.NamespaceName, string, string, []string, io.Reader, io.Writer, io.Writer, bool) error {
	return nil
}
func (stubManagementPort) ExecInPodWithTTY(context.Context, domain.ClusterID, domain.NamespaceName, string, string, []string, io.Reader, io.Writer, io.Writer, ports.TerminalSizeQueue) error {
	return errors.New("ExecInPodWithTTY reached: a refused StartSession must never get this far")
}
func (stubManagementPort) AttachToPod(context.Context, domain.ClusterID, domain.NamespaceName, string, string, io.Reader, io.Writer, io.Writer, ports.TerminalSizeQueue) error {
	return errors.New("AttachToPod reached: a refused StartAttachSession must never get this far")
}

// TestStartSessionRefusesOnReadOnlyCluster pins the fast path CLAUDE.md's
// read-only section promises: an interactive shell refuses synchronously,
// before a PTY is allocated or a goroutine started, rather than opening a
// session that fails on its first keystroke.
func TestStartSessionRefusesOnReadOnlyCluster(t *testing.T) {
	t.Parallel()

	registry := application.NewRegistry()
	registry.SetReadOnly("prod", true)

	management, err := application.NewManagementService(application.ManagementServiceDeps{
		Management: stubManagementPort{},
		Registry:   registry,
	})
	if err != nil {
		t.Fatalf("NewManagementService() error = %v", err)
	}

	terminal, err := NewTerminalAPI(management, NewApp(nil, 0), nil)
	if err != nil {
		t.Fatalf("NewTerminalAPI() error = %v", err)
	}

	sessionID, err := terminal.StartSession("prod", "default", "web-0", "app", 80, 24)
	if err == nil {
		t.Fatal("StartSession() error = nil, want a read-only refusal")
	}
	if !strings.Contains(err.Error(), "read_only") {
		t.Fatalf("StartSession() error = %q, want it classified read_only", err)
	}
	if sessionID != "" {
		t.Fatalf("StartSession() session id = %q, want empty on refusal", sessionID)
	}

	terminal.mu.Lock()
	live := len(terminal.sessions)
	terminal.mu.Unlock()
	if live != 0 {
		t.Fatalf("live sessions = %d, want 0 — a refused start must never allocate one", live)
	}
}

// TestStartSessionAllowsOnOrdinaryCluster is the other half: the guard must
// not refuse a cluster nothing marked, and StartSession has to get far enough
// to try opening a stream — asserted by watching stubManagementPort get past
// the read-only gate, since a fully connected session needs a live Wails
// runtime this test does not have.
func TestStartSessionAllowsOnOrdinaryCluster(t *testing.T) {
	t.Parallel()

	registry := application.NewRegistry()
	// Marked, but a different cluster — the guard has to be per-cluster.
	registry.SetReadOnly("prod", true)

	management, err := application.NewManagementService(application.ManagementServiceDeps{
		Management: stubManagementPort{},
		Registry:   registry,
	})
	if err != nil {
		t.Fatalf("NewManagementService() error = %v", err)
	}

	terminal, err := NewTerminalAPI(management, NewApp(nil, 0), nil)
	if err != nil {
		t.Fatalf("NewTerminalAPI() error = %v", err)
	}

	// No Wails runtime is running, so the call fails past the read-only
	// check — at "application is shutting down" — rather than succeeding.
	// That failure is what proves the guard let it through: a read-only
	// refusal never reaches that line at all.
	_, err = terminal.StartSession("staging", "default", "web-0", "app", 80, 24)
	if err == nil {
		t.Fatal("StartSession() error = nil, want a failure reaching for the (absent) Wails runtime")
	}
	if strings.Contains(err.Error(), "read_only") {
		t.Fatalf("StartSession() error = %q, an unmarked cluster must not be refused as read-only", err)
	}
}

// TestStartAttachSessionRefusesOnReadOnlyCluster is StartSession's own
// read-only test, mirrored for the attach path: attaching can type into the
// container's process as freely as an interactive shell can, so it gets the
// identical synchronous refusal — before a PTY is allocated or a goroutine
// started — rather than opening a session that fails on its first keystroke.
func TestStartAttachSessionRefusesOnReadOnlyCluster(t *testing.T) {
	t.Parallel()

	registry := application.NewRegistry()
	registry.SetReadOnly("prod", true)

	management, err := application.NewManagementService(application.ManagementServiceDeps{
		Management: stubManagementPort{},
		Registry:   registry,
	})
	if err != nil {
		t.Fatalf("NewManagementService() error = %v", err)
	}

	terminal, err := NewTerminalAPI(management, NewApp(nil, 0), nil)
	if err != nil {
		t.Fatalf("NewTerminalAPI() error = %v", err)
	}

	sessionID, err := terminal.StartAttachSession("prod", "default", "web-0", "app", 80, 24)
	if err == nil {
		t.Fatal("StartAttachSession() error = nil, want a read-only refusal")
	}
	if !strings.Contains(err.Error(), "read_only") {
		t.Fatalf("StartAttachSession() error = %q, want it classified read_only", err)
	}
	if sessionID != "" {
		t.Fatalf("StartAttachSession() session id = %q, want empty on refusal", sessionID)
	}

	terminal.mu.Lock()
	live := len(terminal.sessions)
	terminal.mu.Unlock()
	if live != 0 {
		t.Fatalf("live sessions = %d, want 0 — a refused start must never allocate one", live)
	}
}

// TestStartAttachSessionAllowsOnOrdinaryCluster is the other half, mirroring
// TestStartSessionAllowsOnOrdinaryCluster: the guard must not refuse a
// cluster nothing marked, so the call has to get far enough to try opening a
// stream — asserted the same way, by watching it fail past the read-only
// gate rather than at it, since a fully connected session needs a live Wails
// runtime this test does not have.
func TestStartAttachSessionAllowsOnOrdinaryCluster(t *testing.T) {
	t.Parallel()

	registry := application.NewRegistry()
	// Marked, but a different cluster — the guard has to be per-cluster.
	registry.SetReadOnly("prod", true)

	management, err := application.NewManagementService(application.ManagementServiceDeps{
		Management: stubManagementPort{},
		Registry:   registry,
	})
	if err != nil {
		t.Fatalf("NewManagementService() error = %v", err)
	}

	terminal, err := NewTerminalAPI(management, NewApp(nil, 0), nil)
	if err != nil {
		t.Fatalf("NewTerminalAPI() error = %v", err)
	}

	_, err = terminal.StartAttachSession("staging", "default", "web-0", "app", 80, 24)
	if err == nil {
		t.Fatal("StartAttachSession() error = nil, want a failure reaching for the (absent) Wails runtime")
	}
	if strings.Contains(err.Error(), "read_only") {
		t.Fatalf("StartAttachSession() error = %q, an unmarked cluster must not be refused as read-only", err)
	}
}

// The write operations added after this stub was written. Every one is a
// no-op: the terminal tests only need a ManagementPort that compiles and that
// answers ReadOnly through the service, never one that performs a write.
func (stubManagementPort) TriggerCronJob(context.Context, domain.ClusterID, domain.NamespaceName, string) (string, error) {
	return "", nil
}
func (stubManagementPort) SuspendWorkload(context.Context, domain.ClusterID, domain.WorkloadKind, domain.NamespaceName, string, bool) error {
	return nil
}
func (stubManagementPort) SetSecretKey(context.Context, domain.ClusterID, domain.NamespaceName, string, string, []byte) error {
	return nil
}
func (stubManagementPort) SetConfigMapKey(context.Context, domain.ClusterID, domain.NamespaceName, string, string, string) error {
	return nil
}
func (stubManagementPort) CordonNode(context.Context, domain.ClusterID, string, bool) error {
	return nil
}
func (stubManagementPort) EvictPod(context.Context, domain.ClusterID, domain.NamespaceName, string, int) error {
	return nil
}
func (stubManagementPort) DrainNode(context.Context, domain.ClusterID, string, domain.DrainOptions) (domain.DrainReport, error) {
	return domain.DrainReport{}, nil
}
func (stubManagementPort) SetImage(context.Context, domain.ClusterID, domain.WorkloadKind, domain.NamespaceName, string, string, string, bool) error {
	return nil
}
func (stubManagementPort) RollbackWorkload(context.Context, domain.ClusterID, domain.WorkloadKind, domain.NamespaceName, string, int64, bool) (domain.RollbackOutcome, error) {
	return domain.RollbackOutcome{}, nil
}
