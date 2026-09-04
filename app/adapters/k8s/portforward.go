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
	"github.com/podsteer/podsteer/app/ports"
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
	// mu guards forward, which the supervisor rewrites as it reconnects
	// while the UI reads it.
	mu      sync.Mutex
	forward domain.Forward
	// stop ends the SUPERVISOR, for good. Each individual attempt has its own
	// inner stop channel, so a pod dying tears down one attempt without
	// ending the forward.
	stop chan struct{}
	done chan struct{}
}

func (f *forwarder) snapshot() domain.Forward {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.forward
}

func (f *forwarder) update(mutate func(*domain.Forward)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	mutate(&f.forward)
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
func (a *Adapter) StartPortForward(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, pod, podUID string, localPort, remotePort int, portName, protocol string, selector map[string]string) (domain.Forward, error) {
	op := fmt.Sprintf("forwarding %s/%s:%d in %q", namespace, pod, remotePort, id)

	// Refused here rather than filtered in the UI, because it is a fact about
	// the transport and not a presentation choice: Kubernetes port-forward
	// carries TCP only. A UDP forward would appear to establish and then drop
	// every packet, which is a worse outcome than being told no.
	if !forwardableProtocol(protocol) {
		return domain.Forward{}, fmt.Errorf("%s: %w", op, errNotTCP)
	}

	established, local, first, err := a.dialForward(id, namespace, pod, localPort, remotePort)
	if err != nil {
		return domain.Forward{}, err
	}
	if !established {
		return domain.Forward{}, fmt.Errorf("%s: %w", op, errForwardTimeout)
	}

	a.forwards.mu.Lock()
	a.forwards.nextID++
	forward := domain.Forward{
		ID:         strconv.Itoa(a.forwards.nextID),
		ClusterID:  id,
		Namespace:  namespace,
		Pod:        pod,
		PodUID:     podUID,
		LocalPort:  local,
		RemotePort: remotePort,
		Scheme:     domain.SchemeForPort(portName),
		Selector:   selector,
	}

	// The SUPERVISOR owns the forward from here, and its stop channel is what
	// StopPortForward closes. The first attempt's channels are handed to it;
	// every later attempt makes its own.
	supervisorStop := make(chan struct{})
	supervisorDone := make(chan struct{})

	entry := &forwarder{forward: forward, stop: supervisorStop, done: supervisorDone}
	a.forwards.byID[forward.ID] = entry
	a.forwards.mu.Unlock()

	go a.superviseForward(entry, first, portName)

	return forward, nil
}

// attempt is one established connection, and the channels that end it.
type attempt struct {
	stop   chan struct{}
	done   chan struct{}
	failed chan error
}

// reconnectBackoff is how long to wait between attempts to find a replacement.
//
// Not exponential. This is not protecting a struggling server — it is one
// LIST against the API server per attempt, and the thing being waited for is
// a scheduler placing a replacement pod, which takes seconds. A backoff that
// grew would mostly add delay after the pod was already back.
const reconnectBackoff = 3 * time.Second

// reconnectWindow is how long to keep looking before giving up.
//
// Past this, a replacement is not coming: the workload has been scaled to
// zero, or deleted, or rolled to a revision whose pods no longer match this
// selector. Holding a local port bound forever for a pod that will never
// return is the leak this whole design exists to avoid.
const reconnectWindow = 2 * time.Minute

// superviseForward keeps a forward alive across the death of its pod.
//
// THE MOST-REQUESTED BEHAVIOUR IN THE CATEGORY AND THE ONE NOBODY SHIPS.
// `kubectl port-forward` binds one pod and does not recover; the upstream
// issue asking for it was closed as not planned in 2019, and every client
// that shells out to kubectl inherits that. So a rollout, an eviction or a
// crash silently kills the forward and whatever was pointed at it starts
// refusing connections, with the UI still showing it as active.
//
// THE LOCAL PORT IS HELD ACROSS THE GAP. That is the entire point: a browser
// tab, a database client or a curl loop keeps its address and simply stalls
// while the replacement is found, rather than having to be re-pointed at a
// new port somebody has to go and read.
func (a *Adapter) superviseForward(entry *forwarder, current attempt, portName string) {
	defer close(entry.done)

	for {
		select {
		case <-entry.stop:
			// Deliberate stop. End the running attempt and wait for it, so
			// StopPortForward can promise the local port is free.
			close(current.stop)
			<-current.done
			return

		case <-current.done:
			// The attempt ended on its own: the pod went away. Not an error
			// to report — it is the case this exists for.
		case err := <-current.failed:
			_ = err
			<-current.done
		}

		forward := entry.snapshot()
		entry.update(func(f *domain.Forward) { f.Reconnecting = true })

		next, ok := a.reconnect(entry, forward, portName)
		if !ok {
			// Nothing came back within the window. The registry entry is
			// removed so the UI stops showing a forward that is not one, and
			// the local port is long since released with the failed attempt.
			a.forwards.mu.Lock()
			delete(a.forwards.byID, forward.ID)
			a.forwards.mu.Unlock()
			return
		}

		current = next
		entry.update(func(f *domain.Forward) { f.Reconnecting = false })
	}
}

// reconnect looks for a replacement pod and rebinds the SAME local port.
func (a *Adapter) reconnect(entry *forwarder, forward domain.Forward, portName string) (attempt, bool) {
	deadline := time.Now().Add(reconnectWindow)

	for time.Now().Before(deadline) {
		select {
		case <-entry.stop:
			return attempt{}, false
		case <-time.After(reconnectBackoff):
		}

		replacement, err := a.findReplacementPod(forward)
		if err != nil || replacement == "" {
			continue
		}

		established, local, next, err := a.dialForward(
			forward.ClusterID, forward.Namespace, replacement,
			forward.LocalPort, forward.RemotePort,
		)
		if err != nil || !established {
			continue
		}

		entry.update(func(f *domain.Forward) {
			f.Pod = replacement
			f.LocalPort = local
		})
		return next, true
	}

	return attempt{}, false
}

// findReplacementPod returns a running pod matching the forward's selector.
//
// Matched on the pod's OWN labels, which for a ReplicaSet's pods include
// pod-template-hash — so a replacement is a sibling of the same revision, not
// a pod of whatever rolled out since. Silently moving a forward onto
// different code would be worse than not reconnecting at all.
func (a *Adapter) findReplacementPod(forward domain.Forward) (string, error) {
	if len(forward.Selector) == 0 {
		return "", nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pods, err := a.ListPods(ctx, forward.ClusterID, forward.Namespace)
	if err != nil {
		return "", err
	}

	for _, pod := range pods {
		if pod.Name() == forward.Pod || !pod.IsReady() || !pod.OccupiesNode() {
			continue
		}
		if matchesSelector(pod.Labels(), forward.Selector) {
			return pod.Name(), nil
		}
	}
	return "", nil
}

// matchesSelector reports whether labels carry every pair the selector names.
func matchesSelector(labels, selector map[string]string) bool {
	for key, want := range selector {
		if labels[key] != want {
			return false
		}
	}
	return true
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
		out = append(out, entry.snapshot())
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
func (a *Adapter) FreeLocalPort() (int, error) {
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

// ProbeLocalPort reports whether a TCP port on this machine — never the
// cluster, which is the mistake the name invites — is free to bind.
//
// BINDING IS THE ANSWER, not a heuristic about the ephemeral range: a stale
// process, a container runtime's proxy or a port Docker Desktop leaked all
// show as bound to nothing a process list would name, and only actually
// trying to listen catches them. The listener is closed immediately, so the
// probe itself never holds the port anybody was asking about — and, exactly
// as with FreeLocalPort, there is a race between this answer and whatever the
// operator does next: this exists to catch a collision before Start is
// pressed, not to reserve anything.
func (a *Adapter) ProbeLocalPort(port int) (bool, error) {
	if port < 1 || port > 65535 {
		return false, fmt.Errorf("probing local port %d: %w", port, ports.ErrInvalidPort)
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		// Not free — and deliberately not surfaced as a call failure. Whether
		// the refusal was "already in use" or "permission denied" (a port
		// below 1024 without privilege), the practical answer an operator
		// needs is the same one: they cannot bind here.
		return false, nil
	}
	_ = listener.Close()
	return true, nil
}

// forwardableProtocol reports whether a container port can be forwarded.
//
// TCP ONLY. Kubernetes port-forward does not carry UDP, and a button that
// offers it produces a forward that appears to work and drops every packet.
func forwardableProtocol(protocol string) bool {
	return protocol == "" || strings.EqualFold(protocol, "TCP")
}

// dialForward opens one connection and returns once its local port is bound.
//
// Shared by the first attempt and every reconnect, so the timeout, the
// teardown-on-timeout and the read-back of the bound port are written once.
// Returns established=false with no error when the ten seconds elapsed, which
// the caller reports differently depending on whether it was the first
// attempt or a retry.
func (a *Adapter) dialForward(id domain.ClusterID, namespace domain.NamespaceName, pod string, localPort, remotePort int) (bool, int, attempt, error) {
	op := fmt.Sprintf("forwarding %s/%s:%d in %q", namespace, pod, remotePort, id)

	set, err := a.factory.clientsFor(id)
	if err != nil {
		return false, 0, attempt{}, err
	}
	config, err := a.factory.restConfig(id)
	if err != nil {
		return false, 0, attempt{}, fmt.Errorf("%s: %w", op, err)
	}

	transport, upgrader, err := spdy.RoundTripperFor(config)
	if err != nil {
		return false, 0, attempt{}, fmt.Errorf("%s: %w", op, err)
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
		return false, 0, attempt{}, fmt.Errorf("%s: %w", op, err)
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
		<-done
		return false, 0, attempt{}, classify(op, err)
	case <-time.After(forwardReadyTimeout):
		// TORN DOWN, NOT ABANDONED. Left running, ForwardPorts can succeed a
		// moment later and bind a local port nothing is managing — which is
		// precisely the leak that produces "the port is in use and nothing
		// is using it".
		close(stop)
		<-done
		return false, 0, attempt{}, nil
	}

	bound, err := forwarderImpl.GetPorts()
	if err != nil || len(bound) == 0 {
		close(stop)
		<-done
		return false, 0, attempt{}, fmt.Errorf("%s: could not determine the local port", op)
	}

	return true, int(bound[0].Local), attempt{stop: stop, done: done, failed: failed}, nil
}
