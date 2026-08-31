package k8s

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	"github.com/podsteer/podsteer/app/domain"
)

// forwardReadyTimeout bounds how long a forward may take to come up.
//
// A forward that has not bound its local port is not a forward, and leaving
// one "connecting" indefinitely is the failure Headlamp and Lens both ship:
// an RBAC denial surfaces as a spinner that never resolves, and the operator
// cannot tell a slow cluster from a permission they do not have.
const forwardReadyTimeout = 10 * time.Second

// forwarder is one running port-forward and the means to stop it.
//
// ONE OWNER PER FORWARD, which is the whole design. Every leak in the
// competing clients is the same shape: the UI forgets a forward without
// telling the goroutine, so the entry disappears from the list while the
// local port stays bound and the connection stays open. Here the map entry
// and the goroutine are created together and removed together, and stopping
// waits for the goroutine to actually finish before reporting the port free.
type forwarder struct {
	forward domain.Forward
	stop    chan struct{}
	done    chan struct{}
}

// portForwards holds the live forwards for one adapter.
type portForwards struct {
	mu     sync.Mutex
	byID   map[string]*forwarder
	nextID int
}

// StartPortForward opens a local port onto a container port.
//
// Returns the forward with its LOCAL PORT FILLED IN, including when the
// operating system chose it: a caller that asked for port 0 still has to be
// able to tell somebody where to point their browser.
func (a *Adapter) StartPortForward(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, pod, podUID string, localPort, remotePort int, portName, protocol string) (domain.Forward, error) {
	op := fmt.Sprintf("forwarding %s/%s:%d in %q", namespace, pod, remotePort, id)

	// Refused here rather than filtered in the UI, because it is a fact about
	// the transport and not a presentation choice: Kubernetes port-forward
	// carries TCP only. A UDP forward would appear to establish and then drop
	// every packet, which is a worse outcome than being told no.
	if !forwardableProtocol(protocol) {
		return domain.Forward{}, fmt.Errorf("%s: %w", op, errNotTCP)
	}

	set, err := a.factory.clientsFor(id)
	if err != nil {
		return domain.Forward{}, err
	}
	config, err := a.factory.restConfig(id)
	if err != nil {
		return domain.Forward{}, fmt.Errorf("%s: %w", op, err)
	}

	transport, upgrader, err := spdy.RoundTripperFor(config)
	if err != nil {
		return domain.Forward{}, fmt.Errorf("%s: %w", op, err)
	}

	request := set.typed.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(namespace.String()).
		Name(pod).
		SubResource("portforward")

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", request.URL())

	stop := make(chan struct{})
	ready := make(chan struct{})

	// Discarded rather than surfaced. client-go writes "Forwarding from
	// 127.0.0.1:8080 -> 8080" and per-connection chatter to these, none of
	// which belongs in an application log — and the port is read back from
	// the forwarder itself below rather than parsed out of that text, which
	// is what Lens does and what breaks when the wording changes.
	forwarderImpl, err := portforward.New(
		dialer,
		[]string{fmt.Sprintf("%d:%d", localPort, remotePort)},
		stop, ready,
		discardWriter{}, discardWriter{},
	)
	if err != nil {
		return domain.Forward{}, fmt.Errorf("%s: %w", op, err)
	}

	done := make(chan struct{})
	failed := make(chan error, 1)

	go func() {
		defer close(done)
		if err := forwarderImpl.ForwardPorts(); err != nil {
			failed <- err
		}
	}()

	select {
	case <-ready:
	case err := <-failed:
		// The goroutine has already returned; nothing to signal, and the
		// local port was never bound.
		return domain.Forward{}, classify(op, err)
	case <-time.After(forwardReadyTimeout):
		// TORN DOWN, NOT ABANDONED. Left running, ForwardPorts can succeed a
		// moment later and bind a local port nothing is managing — which is
		// precisely the leak that produces "the port is in use and nothing
		// is using it".
		close(stop)
		<-done
		return domain.Forward{}, fmt.Errorf("%s: %w", op, errForwardTimeout)
	case <-ctx.Done():
		close(stop)
		<-done
		return domain.Forward{}, ctx.Err()
	}

	// The port actually bound, which is the only truthful answer when the
	// caller asked for zero and let the operating system choose.
	bound, err := forwarderImpl.GetPorts()
	if err != nil || len(bound) == 0 {
		close(stop)
		<-done
		return domain.Forward{}, fmt.Errorf("%s: could not determine the local port", op)
	}

	a.forwards.mu.Lock()
	a.forwards.nextID++
	forward := domain.Forward{
		ID:         strconv.Itoa(a.forwards.nextID),
		ClusterID:  id,
		Namespace:  namespace,
		Pod:        pod,
		PodUID:     podUID,
		LocalPort:  int(bound[0].Local),
		RemotePort: remotePort,
		Scheme:     domain.SchemeForPort(portName),
	}
	a.forwards.byID[forward.ID] = &forwarder{forward: forward, stop: stop, done: done}
	a.forwards.mu.Unlock()

	return forward, nil
}

// StopPortForward closes one forward and waits for its port to be released.
//
// WAITS, rather than signalling and returning. A caller that immediately
// starts a new forward on the same local port would otherwise race the old
// one's teardown and fail with "address already in use" on a port it just
// released itself.
func (a *Adapter) StopPortForward(id string) error {
	a.forwards.mu.Lock()
	entry, ok := a.forwards.byID[id]
	if ok {
		delete(a.forwards.byID, id)
	}
	a.forwards.mu.Unlock()

	if !ok {
		return nil
	}

	close(entry.stop)
	<-entry.done
	return nil
}

// ListPortForwards returns what is forwarded right now.
func (a *Adapter) ListPortForwards() []domain.Forward {
	a.forwards.mu.Lock()
	defer a.forwards.mu.Unlock()

	out := make([]domain.Forward, 0, len(a.forwards.byID))
	for _, entry := range a.forwards.byID {
		out = append(out, entry.forward)
	}
	return out
}

// StopAllPortForwards tears down every forward, for shutdown.
func (a *Adapter) StopAllPortForwards() {
	a.forwards.mu.Lock()
	entries := make([]*forwarder, 0, len(a.forwards.byID))
	for id, entry := range a.forwards.byID {
		entries = append(entries, entry)
		delete(a.forwards.byID, id)
	}
	a.forwards.mu.Unlock()

	for _, entry := range entries {
		close(entry.stop)
		<-entry.done
	}
}

// errForwardTimeout reports a forward that never became ready.
var errForwardTimeout = errors.New("the forward did not start within ten seconds")

// errNotTCP reports a port Kubernetes cannot forward.
var errNotTCP = errors.New("only TCP ports can be forwarded")

// discardWriter swallows client-go's forwarding chatter.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// FreeLocalPort asks the operating system for a port nothing is using.
//
// Offered so the UI can SHOW the port it is about to use rather than
// discovering it afterwards. There is an unavoidable race between this and
// binding it, which is why the forward reports the port it actually bound
// rather than trusting this one.
func FreeLocalPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = listener.Close() }()

	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, errors.New("could not read the chosen port")
	}
	return address.Port, nil
}

// forwardableProtocol reports whether a container port can be forwarded.
//
// TCP ONLY. Kubernetes port-forward does not carry UDP, and a button that
// offers it produces a forward that appears to work and drops every packet.
func forwardableProtocol(protocol string) bool {
	return protocol == "" || strings.EqualFold(protocol, "TCP")
}
