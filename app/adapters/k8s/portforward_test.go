package k8s

import (
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/podsteer/podsteer/app/ports"
)

// TestProbeLocalPortReportsAnOccupiedPortAsNotFree is the case the whole
// method exists for: a port already bound by something else must be reported
// unavailable, so the UI can say so before Start is pressed rather than after
// the forward fails.
func TestProbeLocalPortReportsAnOccupiedPortAsNotFree(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupying a port: %v", err)
	}
	defer func() { _ = listener.Close() }()

	port := listener.Addr().(*net.TCPAddr).Port

	adapter := New(Config{}, nil)
	free, err := adapter.ProbeLocalPort(port)
	if err != nil {
		t.Fatalf("ProbeLocalPort(%d) error = %v", port, err)
	}
	if free {
		t.Errorf("ProbeLocalPort(%d) = true, want false — a listener is holding it", port)
	}
}

// TestProbeLocalPortReportsAJustReleasedPortAsFree proves the probe answers
// by actually binding rather than by consulting some stale record: a port
// released a moment ago must read as free again immediately.
func TestProbeLocalPortReportsAJustReleasedPortAsFree(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("finding a port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("releasing the port: %v", err)
	}

	adapter := New(Config{}, nil)
	free, err := adapter.ProbeLocalPort(port)
	if err != nil {
		t.Fatalf("ProbeLocalPort(%d) error = %v", port, err)
	}
	if !free {
		t.Errorf("ProbeLocalPort(%d) = false, want true — nothing holds it any more", port)
	}
}

// TestProbeLocalPortRefusesPortsOutsideTheValidRange guards the boundary
// StartPortForward never has to think about: a caller passing 0 or something
// above 65535 gets told so, wrapping ports.ErrInvalidPort so the frontend can
// classify it as CodeInvalidInput rather than a probe failure.
func TestProbeLocalPortRefusesPortsOutsideTheValidRange(t *testing.T) {
	adapter := New(Config{}, nil)

	for _, port := range []int{0, -1, 65536, 100000} {
		_, err := adapter.ProbeLocalPort(port)
		if err == nil {
			t.Errorf("ProbeLocalPort(%d) error = nil, want a refusal", port)
			continue
		}
		if !errors.Is(err, ports.ErrInvalidPort) {
			t.Errorf("ProbeLocalPort(%d) error = %v, want it to wrap ports.ErrInvalidPort", port, err)
		}
	}
}

// TestFreeLocalPortReturnsAPortNothingIsUsing asserts the port it hands back
// can actually be bound — the whole reason it exists is to offer the UI
// something usable rather than a number.
func TestFreeLocalPortReturnsAPortNothingIsUsing(t *testing.T) {
	adapter := New(Config{}, nil)

	port, err := adapter.FreeLocalPort()
	if err != nil {
		t.Fatalf("FreeLocalPort() error = %v", err)
	}
	if port < 1 || port > 65535 {
		t.Fatalf("FreeLocalPort() = %d, outside the valid TCP range", port)
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("binding the port FreeLocalPort chose: %v", err)
	}
	_ = listener.Close()
}
