package application_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// fakeClusterShellPort records what reached it and errors loudly on the two
// methods the read-only tests assert are never reached.
//
// Recorded rather than counted, the discipline fakeManagementPort already
// follows: a refused write must be shown never to have reached the port at
// all, not merely to have returned an error.
type fakeClusterShellPort struct {
	mu sync.Mutex
	// calls records every method that reached the fake, in order.
	calls []string

	started    domain.ClusterShell
	startErr   error
	adopted    domain.ClusterShell
	adoptErr   error
	candidates []domain.ClusterShellCandidate
	findErr    error
	stopped    []string
	stopAllRan bool
}

var _ ports.ClusterShellPort = (*fakeClusterShellPort)(nil)

func (f *fakeClusterShellPort) record(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
}

func (f *fakeClusterShellPort) reached() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeClusterShellPort) StartClusterShell(_ context.Context, id domain.ClusterID, ns domain.NamespaceName, image string) (domain.ClusterShell, error) {
	f.record("StartClusterShell")
	if f.startErr != nil {
		return domain.ClusterShell{}, f.startErr
	}
	shell := f.started
	shell.ClusterID, shell.Namespace, shell.Image = id, ns, image
	return shell, nil
}

func (f *fakeClusterShellPort) AdoptClusterShell(_ context.Context, id domain.ClusterID, ns domain.NamespaceName, podName string) (domain.ClusterShell, error) {
	f.record("AdoptClusterShell")
	if f.adoptErr != nil {
		return domain.ClusterShell{}, f.adoptErr
	}
	shell := f.adopted
	shell.ClusterID, shell.Namespace, shell.PodName, shell.Adopted = id, ns, podName, true
	return shell, nil
}

func (f *fakeClusterShellPort) FindClusterShells(context.Context, domain.ClusterID, domain.NamespaceName) ([]domain.ClusterShellCandidate, error) {
	f.record("FindClusterShells")
	return f.candidates, f.findErr
}

func (f *fakeClusterShellPort) StopClusterShell(id string) error {
	f.record("StopClusterShell")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, id)
	return nil
}

func (f *fakeClusterShellPort) ListClusterShells() []domain.ClusterShell { return nil }

func (f *fakeClusterShellPort) StopAllClusterShells() {
	f.record("StopAllClusterShells")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopAllRan = true
}

// newShellService wires a management service over both fakes, optionally with
// its own logger so the audit line can be read back.
func newShellService(t *testing.T, shells *fakeClusterShellPort, registry *application.Registry, logger *slog.Logger) *application.ManagementService {
	t.Helper()

	service, err := application.NewManagementService(application.ManagementServiceDeps{
		Management:    &fakeManagementPort{},
		Registry:      registry,
		Logger:        logger,
		ClusterShells: shells,
	})
	if err != nil {
		t.Fatalf("NewManagementService() error = %v", err)
	}
	return service
}

// TestStartClusterShellRefusesAReadOnlyClusterBeforeThePortIsTouched is the
// fencing rule: creating a pod is a write, so it is refused BEFORE anything
// reaches the cluster.
//
// Asserted by what the port RECORDED rather than by the returned error alone —
// a guard that refuses after the pod exists is not a guard, and only the call
// list can tell the two apart.
func TestStartClusterShellRefusesAReadOnlyClusterBeforeThePortIsTouched(t *testing.T) {
	t.Parallel()

	registry := application.NewRegistry()
	registry.SetReadOnly("prod", true)

	shells := &fakeClusterShellPort{}
	service := newShellService(t, shells, registry, nil)

	_, err := service.StartClusterShell(context.Background(), "prod", "shop", "docker.io/cloudresty/dockydeb:v1.2.28-nonroot")
	if !errors.Is(err, ports.ErrReadOnly) {
		t.Fatalf("StartClusterShell() error = %v, want ErrReadOnly", err)
	}
	if reached := shells.reached(); len(reached) != 0 {
		t.Fatalf("the port was reached with %v — a refused write must never touch the cluster", reached)
	}
}

// TestAdoptClusterShellIsRefusedOnAReadOnlyClusterToo covers the reuse path.
// Adoption creates nothing and signs up to DELETE a pod when the session ends,
// which is a write like any other.
func TestAdoptClusterShellIsRefusedOnAReadOnlyClusterToo(t *testing.T) {
	t.Parallel()

	registry := application.NewRegistry()
	registry.SetReadOnly("prod", true)

	shells := &fakeClusterShellPort{}
	service := newShellService(t, shells, registry, nil)

	_, err := service.AdoptClusterShell(context.Background(), "prod", "shop", "podsteer-shell-aaaaa")
	if !errors.Is(err, ports.ErrReadOnly) {
		t.Fatalf("AdoptClusterShell() error = %v, want ErrReadOnly", err)
	}
	if reached := shells.reached(); len(reached) != 0 {
		t.Fatalf("the port was reached with %v — adoption puts a pod on the delete hook and is fenced like a write", reached)
	}
}

// TestFindClusterShellsIsNotGovernedByTheReadOnlyGuard states the other half
// of the rule, and it matters as much: listing pods by label changes nothing,
// and a guard about PodSteer's writes has no business refusing a read. Without
// this, a read-only cluster could never be told it already has a shell.
func TestFindClusterShellsIsNotGovernedByTheReadOnlyGuard(t *testing.T) {
	t.Parallel()

	registry := application.NewRegistry()
	registry.SetReadOnly("prod", true)

	shells := &fakeClusterShellPort{candidates: []domain.ClusterShellCandidate{
		{PodName: "podsteer-shell-aaaaa", Phase: domain.PodPhaseRunning},
		{PodName: "podsteer-shell-bbbbb", Phase: domain.PodPhaseFailed},
	}}
	service := newShellService(t, shells, registry, nil)

	plan, err := service.FindClusterShells(context.Background(), "prod", "shop")
	if err != nil {
		t.Fatalf("FindClusterShells() error = %v, want a read to succeed on a read-only cluster", err)
	}
	if len(plan.Reusable) != 1 || plan.Reusable[0].PodName != "podsteer-shell-aaaaa" {
		t.Fatalf("reusable = %+v, want only the Running pod", plan.Reusable)
	}
	if len(plan.Other) != 1 {
		t.Fatalf("other = %+v, want the exited pod reported rather than dropped", plan.Other)
	}
}

// TestStartClusterShellRefusesAllNamespacesRatherThanGuessing is the rule that
// the tab being on "All namespaces" has no answer.
//
// NewNamespaceName("") is NamespaceAll rather than an error, so nothing else
// in this application would object — the create would simply land the pod in
// whatever the kubeconfig's default namespace is. Falling back to a system
// namespace, which is where the node shell's own setting points, is the other
// wrong answer.
func TestStartClusterShellRefusesAllNamespacesRatherThanGuessing(t *testing.T) {
	t.Parallel()

	shells := &fakeClusterShellPort{}
	service := newShellService(t, shells, application.NewRegistry(), nil)

	_, err := service.StartClusterShell(context.Background(), "dev", domain.NamespaceAll, "docker.io/cloudresty/dockydeb:v1.2.28-nonroot")
	if !errors.Is(err, domain.ErrShellNamespaceRequired) {
		t.Fatalf("StartClusterShell() error = %v, want ErrShellNamespaceRequired", err)
	}
	if reached := shells.reached(); len(reached) != 0 {
		t.Fatalf("the port was reached with %v — a namespace is asked for, never guessed", reached)
	}
}

// TestStartClusterShellLeavesOneAuditLineNamingClusterNamespaceAndPod is the
// audit contract. The line names what PodSteer put in the cluster, and nothing
// about what the shell then did — the bytes flowing through the terminal are
// the operator's session, exactly as a file transfer's line never carries a
// file's contents.
func TestStartClusterShellLeavesOneAuditLineNamingClusterNamespaceAndPod(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	shells := &fakeClusterShellPort{started: domain.ClusterShell{ID: "1", PodName: "podsteer-shell-aaaaa"}}
	service := newShellService(t, shells, application.NewRegistry(), logger)

	if _, err := service.StartClusterShell(context.Background(), "dev", "shop", "docker.io/cloudresty/dockydeb:v1.2.28-nonroot"); err != nil {
		t.Fatalf("StartClusterShell() error = %v", err)
	}

	logged := buf.String()
	for _, want := range []string{"started in-cluster shell", `cluster=dev`, `namespace=shop`, `pod=podsteer-shell-aaaaa`} {
		if !strings.Contains(logged, want) {
			t.Errorf("audit line %q does not contain %q", logged, want)
		}
	}
	if count := strings.Count(logged, "started in-cluster shell"); count != 1 {
		t.Errorf("audit lines = %d, want exactly one", count)
	}
}

// TestStopClusterShellIsNotGovernedByTheReadOnlyGuard is the deliberate
// exception among the write-shaped methods here.
//
// Stopping REMOVES something PodSteer put in the cluster, and it is what a
// terminal session's exit calls. Refusing it because the cluster was marked
// read-only after the pod was created would strand that pod until its
// deadline — a guard against PodSteer's own writes turning into the reason one
// of them is left behind.
func TestStopClusterShellIsNotGovernedByTheReadOnlyGuard(t *testing.T) {
	t.Parallel()

	registry := application.NewRegistry()
	registry.SetReadOnly("prod", true)

	shells := &fakeClusterShellPort{}
	service := newShellService(t, shells, registry, nil)

	if err := service.StopClusterShell("1"); err != nil {
		t.Fatalf("StopClusterShell() error = %v, want the delete to go through", err)
	}
	service.StopAllClusterShells()

	if len(shells.stopped) != 1 || shells.stopped[0] != "1" {
		t.Errorf("stopped = %v, want the one shell", shells.stopped)
	}
	if !shells.stopAllRan {
		t.Error("StopAllClusterShells did not reach the port — the shutdown sweep must not be refusable")
	}
}

// TestClusterShellMethodsRefuseWhenNoPortIsWired covers the composition that
// never offers this — `podsteer mcp` — where the refusal must name the absent
// capability rather than panic on a nil interface.
func TestClusterShellMethodsRefuseWhenNoPortIsWired(t *testing.T) {
	t.Parallel()

	service, err := application.NewManagementService(application.ManagementServiceDeps{
		Management: &fakeManagementPort{},
		Registry:   application.NewRegistry(),
	})
	if err != nil {
		t.Fatalf("NewManagementService() error = %v", err)
	}

	if _, err := service.StartClusterShell(context.Background(), "dev", "shop", "img:1"); err == nil {
		t.Error("StartClusterShell() succeeded with no ClusterShellPort wired")
	}
	if _, err := service.FindClusterShells(context.Background(), "dev", "shop"); err == nil {
		t.Error("FindClusterShells() succeeded with no ClusterShellPort wired")
	}
	if shells := service.ListClusterShells(); shells != nil {
		t.Errorf("ListClusterShells() = %v, want nil", shells)
	}
	// Must not panic.
	service.StopAllClusterShells()
}
