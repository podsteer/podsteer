package wails

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// stubClusterShellPort errors loudly on every method that would touch a
// cluster, since what the tests here assert is that a refused start never gets
// that far.
type stubClusterShellPort struct{}

var _ ports.ClusterShellPort = (*stubClusterShellPort)(nil)

func (stubClusterShellPort) StartClusterShell(context.Context, domain.ClusterID, domain.NamespaceName, string) (domain.ClusterShell, error) {
	return domain.ClusterShell{}, errors.New("StartClusterShell reached: a refused StartClusterShellSession must never get this far")
}

func (stubClusterShellPort) AdoptClusterShell(context.Context, domain.ClusterID, domain.NamespaceName, string) (domain.ClusterShell, error) {
	return domain.ClusterShell{}, errors.New("AdoptClusterShell reached: a refused AttachClusterShellSession must never get this far")
}

func (stubClusterShellPort) FindClusterShells(context.Context, domain.ClusterID, domain.NamespaceName) ([]domain.ClusterShellCandidate, error) {
	return nil, nil
}
func (stubClusterShellPort) StopClusterShell(string) error            { return nil }
func (stubClusterShellPort) ListClusterShells() []domain.ClusterShell { return nil }
func (stubClusterShellPort) StopAllClusterShells()                    {}

// newClusterShellTerminal wires a TerminalAPI whose management service carries
// the in-cluster shell port, over a registry the caller has already marked.
func newClusterShellTerminal(t *testing.T, registry *application.Registry) *TerminalAPI {
	t.Helper()

	management, err := application.NewManagementService(application.ManagementServiceDeps{
		Management:    stubManagementPort{},
		Registry:      registry,
		ClusterShells: stubClusterShellPort{},
	})
	if err != nil {
		t.Fatalf("NewManagementService() error = %v", err)
	}

	terminal, err := NewTerminalAPI(management, stubNodeShellPort{}, &stubLocalShellPort{}, NewApp(nil, 0), nil)
	if err != nil {
		t.Fatalf("NewTerminalAPI() error = %v", err)
	}
	return terminal
}

// TestStartClusterShellSessionRefusesOnReadOnlyClusterBeforeThePortIsTouched
// is the fast path CLAUDE.md's read-only section promises, for the third pod
// PodSteer can create: creating one is a write, so it is refused
// synchronously, before a pod exists and before a session is allocated.
//
// stubClusterShellPort.StartClusterShell errors loudly if reached at all.
func TestStartClusterShellSessionRefusesOnReadOnlyClusterBeforeThePortIsTouched(t *testing.T) {
	t.Parallel()

	registry := application.NewRegistry()
	registry.SetReadOnly("prod", true)
	terminal := newClusterShellTerminal(t, registry)

	sessionID, err := terminal.StartClusterShellSession("prod", "shop", "docker.io/cloudresty/dockydeb:v1.2.28-nonroot", 80, 24)
	if err == nil {
		t.Fatal("StartClusterShellSession() error = nil, want a read-only refusal")
	}
	if !strings.Contains(err.Error(), "read_only") {
		t.Fatalf("StartClusterShellSession() error = %q, want it classified read_only", err)
	}
	if sessionID != "" {
		t.Fatalf("session id = %q, want empty on refusal", sessionID)
	}

	terminal.mu.Lock()
	live := len(terminal.sessions)
	terminal.mu.Unlock()
	if live != 0 {
		t.Fatalf("live sessions = %d, want 0 — a refused start must never allocate one", live)
	}
}

// TestAttachClusterShellSessionRefusesOnReadOnlyClusterToo covers the reuse
// path. Adopting a pod is signing up to delete it, which is a write.
func TestAttachClusterShellSessionRefusesOnReadOnlyClusterToo(t *testing.T) {
	t.Parallel()

	registry := application.NewRegistry()
	registry.SetReadOnly("prod", true)
	terminal := newClusterShellTerminal(t, registry)

	sessionID, err := terminal.AttachClusterShellSession("prod", "shop", "podsteer-shell-aaaaa", 80, 24)
	if err == nil {
		t.Fatal("AttachClusterShellSession() error = nil, want a read-only refusal")
	}
	if !strings.Contains(err.Error(), "read_only") {
		t.Fatalf("AttachClusterShellSession() error = %q, want it classified read_only", err)
	}
	if sessionID != "" {
		t.Fatalf("session id = %q, want empty on refusal", sessionID)
	}
}

// recordingClusterShellPort answers a start and records every stop, so the
// session-end test can assert the pod was actually removed rather than merely
// that a hook existed.
type recordingClusterShellPort struct {
	stubClusterShellPort

	mu      sync.Mutex
	stopped []string
}

func (r *recordingClusterShellPort) StopClusterShell(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = append(r.stopped, id)
	return nil
}

func (r *recordingClusterShellPort) stops() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.stopped...)
}

// attachingManagementPort answers AttachToPod immediately, standing in for a
// shell the operator closed. Everything else is the loud stub's.
type attachingManagementPort struct{ stubManagementPort }

func (attachingManagementPort) AttachToPod(context.Context, domain.ClusterID, domain.NamespaceName, string, string, io.Reader, io.Writer, io.Writer, ports.TerminalSizeQueue) error {
	return nil
}

// TestAnEndingSessionDeletesTheInClusterShellPod is the delete-on-session-end
// half of the lifecycle, driven through the real wiring rather than by calling
// the adapter directly.
//
// It calls attachClusterShell — which is what both StartClusterShellSession
// and AttachClusterShellSession end in — and lets the attach return, which is
// what a closed shell looks like. The session's own defer must then reach
// StopClusterShell, or a pod outlives the terminal that made it useful and is
// left until its one-hour deadline reaps it.
//
// It cannot go through StartClusterShellSession itself: that reads the
// framework's runtime context, which only exists once a real Wails application
// is attached, and no test can supply one.
func TestAnEndingSessionDeletesTheInClusterShellPod(t *testing.T) {
	t.Parallel()

	shells := &recordingClusterShellPort{}
	management, err := application.NewManagementService(application.ManagementServiceDeps{
		Management:    attachingManagementPort{},
		Registry:      application.NewRegistry(),
		ClusterShells: shells,
	})
	if err != nil {
		t.Fatalf("NewManagementService() error = %v", err)
	}

	terminal, err := NewTerminalAPI(management, stubNodeShellPort{}, &stubLocalShellPort{}, NewApp(nil, 0), nil)
	if err != nil {
		t.Fatalf("NewTerminalAPI() error = %v", err)
	}

	shell := domain.ClusterShell{
		ID: "7", ClusterID: "dev", Namespace: "shop",
		PodName: "podsteer-shell-aaaaa", ContainerName: "shell",
	}
	sessionID, err := terminal.attachClusterShell(context.Background(), shell, 80, 24)
	if err != nil {
		t.Fatalf("attachClusterShell() error = %v", err)
	}
	if sessionID == "" {
		t.Fatal("attachClusterShell() returned no session id")
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if stops := shells.stops(); len(stops) > 0 {
			if stops[0] != "7" {
				t.Fatalf("stopped %q, want the shell this session held", stops[0])
			}
			// And the session is forgotten too, or Write and Resize would keep
			// answering for a pod that is gone.
			terminal.mu.Lock()
			live := len(terminal.sessions)
			terminal.mu.Unlock()
			if live != 0 {
				t.Fatalf("live sessions = %d after the session ended, want 0", live)
			}
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("the session ended and its pod was never deleted")
}

// TestClusterShellSessionRefusesAllNamespacesRatherThanGuessing pins the rule
// at the boundary the frontend calls, as well as in ManagementService.
//
// NewNamespaceName("") is NamespaceAll, not an error, so without this check an
// empty argument reaches the create as "every namespace" and lands the pod in
// whatever the kubeconfig's default is.
func TestClusterShellSessionRefusesAllNamespacesRatherThanGuessing(t *testing.T) {
	t.Parallel()

	terminal := newClusterShellTerminal(t, application.NewRegistry())

	_, err := terminal.StartClusterShellSession("dev", "", "docker.io/cloudresty/dockydeb:v1.2.28-nonroot", 80, 24)
	if err == nil {
		t.Fatal("StartClusterShellSession() error = nil, want a refusal for \"all namespaces\"")
	}
	if !strings.Contains(err.Error(), "invalid_input") {
		t.Fatalf("error = %q, want it classified invalid_input", err)
	}
	if !strings.Contains(err.Error(), "all namespaces") {
		t.Fatalf("error = %q, want it to say why \"all namespaces\" is not an answer", err)
	}
}
