package application_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// newManagementService builds a ManagementService wired to a fresh fake port
// and registry, or fails the test.
func newManagementService(t *testing.T, port *fakeManagementPort, registry *application.Registry) *application.ManagementService {
	t.Helper()

	service, err := application.NewManagementService(application.ManagementServiceDeps{
		Management: port,
		Registry:   registry,
	})
	if err != nil {
		t.Fatalf("NewManagementService() error = %v", err)
	}
	return service
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
			return service.UpdateResource(ctx, id, "kind: Pod")
		}},
		{"ExecInPod", func() error {
			var stdout, stderr bytes.Buffer
			return service.ExecInPod(ctx, id, ns, "web-0", "app", []string{"true"}, nil, &stdout, &stderr, false)
		}},
		{"ExecInPodWithTTY", func() error {
			var stdout, stderr bytes.Buffer
			return service.ExecInPodWithTTY(ctx, id, ns, "web-0", "app", []string{"/bin/sh"}, nil, &stdout, &stderr, nil)
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
			return service.UpdateResource(ctx, id, "kind: Pod")
		}, "UpdateResource"},
		{"ExecInPod", func() error {
			var stdout, stderr bytes.Buffer
			return service.ExecInPod(ctx, id, ns, "web-0", "app", []string{"true"}, nil, &stdout, &stderr, false)
		}, "ExecInPod"},
		{"ExecInPodWithTTY", func() error {
			var stdout, stderr bytes.Buffer
			return service.ExecInPodWithTTY(ctx, id, ns, "web-0", "app", []string{"/bin/sh"}, nil, &stdout, &stderr, nil)
		}, "ExecInPodWithTTY"},
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
