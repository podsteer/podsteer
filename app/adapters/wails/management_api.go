package wails

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// generateStreamID creates a unique identifier for a log stream.
func generateStreamID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ManagementAPI exposes management operations to the frontend.
type ManagementAPI struct {
	management *application.ManagementService
	// forwards is the port-forward transport, used directly rather than
	// through a service: there is no policy to apply between the UI's "open
	// this port" and the adapter's, and inserting a layer that only forwards
	// arguments would be a place for the record and the goroutine to drift.
	forwards ports.PortForwardPort
	app      *App
	logger   *slog.Logger

	// Active log streams, keyed by stream ID, with their cancel function.
	streamsMu sync.Mutex
	streams   map[string]context.CancelFunc
}

// NewManagementAPI returns a new management API.
func NewManagementAPI(management *application.ManagementService, forwards ports.PortForwardPort, app *App, logger *slog.Logger) (*ManagementAPI, error) {
	switch {
	case management == nil:
		return nil, errors.New("wails: ManagementAPI requires a ManagementService")
	case forwards == nil:
		return nil, errors.New("wails: ManagementAPI requires a PortForwardPort")
	case app == nil:
		return nil, errors.New("wails: ManagementAPI requires an App")
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &ManagementAPI{
		management: management,
		forwards:   forwards,
		app:        app,
		logger:     logger.With(slog.String("api", "management")),
		streams:    make(map[string]context.CancelFunc),
	}, nil
}

// StreamLogs streams pod logs to the frontend via events.
//
// The frontend must subscribe to "log:lines" events to receive log lines.
// Each event payload is a LogLinesEvent carrying a BATCH of lines — see the
// batcher below for why they are not sent one at a time.
// The stream ends when the pod terminates, the context is cancelled, or an
// error occurs. A "log:end" event is emitted when the stream closes.
func (m *ManagementAPI) StreamLogs(clusterID, namespace, podName, containerName string, follow bool, tailLines int) (string, error) {
	// Through the accessor, not the field — see runtimeContext. Reading
	// app.ctx bare races OnShutdown's write of nil, and WithCancel(nil)
	// panics rather than failing.
	parent, ok := m.app.runtimeContext()
	if !ok {
		return "", errors.New("application is shutting down")
	}
	ctx, cancel := context.WithCancel(parent)

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		cancel()
		return "", err
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		cancel()
		return "", err
	}

	// Generate a stream ID so the frontend can cancel if needed.
	streamID := generateStreamID()

	// Track the cancel function so StopLogStream can kill it.
	m.streamsMu.Lock()
	m.streams[streamID] = cancel
	m.streamsMu.Unlock()

	// Start streaming in a goroutine.
	go func() {
		defer func() {
			cancel()
			m.streamsMu.Lock()
			delete(m.streams, streamID)
			m.streamsMu.Unlock()
		}()

		out := make(chan string, 100)
		// Buffered, and written exactly once, so the writer never blocks on a
		// receiver that has gone.
		failure := make(chan error, 1)

		// Start the stream.
		go func() {
			err := m.management.StreamLogs(ctx, id, ns, podName, containerName, follow, int64(tailLines), out)
			if err != nil && ctx.Err() == nil {
				m.logger.ErrorContext(ctx, "log stream error",
					slog.String("cluster", clusterID),
					slog.String("pod", podName),
					slog.String("error", err.Error()))
			}
			failure <- err
		}()

		// Forward log lines to the frontend in batches.
		//
		// One emit per line is what made a large tail unusable: asking for
		// 5000 lines put 5000 separate messages across the bridge as fast as
		// the channel would drain, and the frontend had to wake for each one.
		// Batching bounds that by TIME as well as by size, so a quiet pod
		// still delivers promptly and a noisy one cannot flood anything.
		forward := newLogBatcher(m.app, streamID)
		ticker := time.NewTicker(logFlushInterval)
		defer ticker.Stop()

	drain:
		for {
			select {
			case line, ok := <-out:
				if !ok {
					break drain
				}
				forward.add(line)

			case <-ticker.C:
				forward.flush()
			}
		}
		forward.flush()

		// The adapter closes `out` as it returns, so its error is either here
		// already or a moment away. Cancellation is not a failure — it is the
		// operator having stopped following, or the window closing — so it
		// ends the stream without a reason.
		reason := ""
		if err := <-failure; err != nil && ctx.Err() == nil {
			reason = err.Error()
		}

		// Signal end of stream, with why if it was not a clean one.
		m.app.emit("log:end", LogEndEvent{
			StreamID: streamID,
			Reason:   reason,
		})
	}()

	return streamID, nil
}

// How long a line may wait for company before being sent on its own.
//
// Short enough to read as live — a log arriving 50ms late is indistinguishable
// from one arriving at once — and long enough that a pod emitting thousands of
// lines a second is delivered in tens of messages rather than thousands.
const logFlushInterval = 50 * time.Millisecond

// The most lines to hold before sending regardless of the interval.
//
// A bound on memory and on the size of a single message, for the case the
// interval alone would not catch: a backlog drains far faster than the ticker
// fires.
const logBatchSize = 500

// logBatcher coalesces log lines into "log:lines" events.
//
// Not safe for concurrent use: it is owned by the single goroutine forwarding
// one stream, which is the only thing that touches it.
type logBatcher struct {
	app      *App
	streamID string
	pending  []string
}

func newLogBatcher(app *App, streamID string) *logBatcher {
	return &logBatcher{
		app:      app,
		streamID: streamID,
		pending:  make([]string, 0, logBatchSize),
	}
}

func (b *logBatcher) add(line string) {
	b.pending = append(b.pending, line)
	if len(b.pending) >= logBatchSize {
		b.flush()
	}
}

func (b *logBatcher) flush() {
	if len(b.pending) == 0 {
		return
	}

	// A fresh slice rather than a reslice of the same array: the batch just
	// handed over is serialised by the runtime on its own schedule, and
	// appending into the array behind it would rewrite lines already in
	// flight.
	batch := b.pending
	b.pending = make([]string, 0, logBatchSize)

	b.app.emit("log:lines", LogLinesEvent{
		StreamID: b.streamID,
		Lines:    batch,
	})
}

// StopLogStream cancels a log stream.
//
// The streamID is returned by StreamLogs. Calling this on an already-stopped
// stream is a no-op.
func (m *ManagementAPI) StopLogStream(streamID string) error {
	m.streamsMu.Lock()
	cancel, ok := m.streams[streamID]
	if ok {
		delete(m.streams, streamID)
	}
	m.streamsMu.Unlock()

	if ok {
		cancel()
		m.logger.Debug("log stream cancelled", slog.String("streamID", streamID))
	}
	return nil
}

// DeleteResource deletes a single resource.
func (m *ManagementAPI) DeleteResource(clusterID, kindGroup, kindVersion, kindKind, namespace, name string) error {
	ctx, cancel := m.app.requestContext()
	defer cancel()

	cluster, err := domain.NewClusterID(clusterID)
	if err != nil {
		return err
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return err
	}

	kind := domain.ResourceKind{
		Group:   kindGroup,
		Version: kindVersion,
		Kind:    kindKind,
	}
	ref := domain.ResourceRef{
		ClusterID: cluster,
		Kind:      kind,
		Namespace: ns,
		Name:      name,
	}

	return m.management.DeleteResource(ctx, ref)
}

// ScaleWorkload sets the replica count for a workload.
func (m *ManagementAPI) ScaleWorkload(clusterID, kind, namespace, name string, replicas int) error {
	ctx, cancel := m.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return err
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return err
	}

	return m.management.ScaleWorkload(ctx, id, domain.WorkloadKind(kind), ns, name, int32(replicas))
}

// RestartRollout triggers a rolling restart of a Deployment or StatefulSet.
func (m *ManagementAPI) RestartRollout(clusterID, kind, namespace, name string) error {
	ctx, cancel := m.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return err
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return err
	}

	return m.management.RestartRollout(ctx, id, domain.WorkloadKind(kind), ns, name)
}

// UpdateResource applies a YAML manifest to the cluster.
func (m *ManagementAPI) UpdateResource(clusterID, manifest string) error {
	ctx, cancel := m.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return err
	}

	return m.management.UpdateResource(ctx, id, manifest)
}

// ExecInPod executes a command in a pod container.
//
// This is a simplified version that runs a command and returns the output.
// For interactive terminal sessions, use the WebSocket-based exec endpoint.
func (m *ManagementAPI) ExecInPod(clusterID, namespace, podName, containerName string, command []string) (string, error) {
	ctx, cancel := m.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return "", err
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return "", err
	}

	var stdout, stderr bytes.Buffer
	err = m.management.ExecInPod(ctx, id, ns, podName, containerName, command, nil, &stdout, &stderr, false)
	if err != nil {
		return "", err
	}

	return stdout.String(), nil
}

// PortForward is one live forward, as the UI shows it.
type PortForward struct {
	ID         string `json:"id"`
	ClusterID  string `json:"clusterId"`
	Namespace  string `json:"namespace"`
	Pod        string `json:"pod"`
	LocalPort  int    `json:"localPort"`
	RemotePort int    `json:"remotePort"`
	// Address is where to point a browser, scheme included.
	Address string `json:"address"`
}

func toPortForward(forward domain.Forward) PortForward {
	return PortForward{
		ID:         forward.ID,
		ClusterID:  forward.ClusterID.String(),
		Namespace:  forward.Namespace.String(),
		Pod:        forward.Pod,
		LocalPort:  forward.LocalPort,
		RemotePort: forward.RemotePort,
		Address:    forward.Address(),
	}
}

// StartPortForward opens a local port onto a container port.
//
// localPort may be zero, in which case the operating system chooses and the
// returned forward carries what it chose. That is the honest default: asking
// somebody to pick a free port is asking them to guess, and a collision is
// reported as a failure to start rather than silently moved somewhere else.
func (m *ManagementAPI) StartPortForward(clusterID, namespace, pod, podUID string, localPort, remotePort int, portName, protocol string) (PortForward, error) {
	ctx, cancel := m.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return PortForward{}, apiError(m.logger, "StartPortForward", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return PortForward{}, apiError(m.logger, "StartPortForward", err)
	}

	forward, err := m.forwards.StartPortForward(ctx, id, ns, pod, podUID, localPort, remotePort, portName, protocol)
	if err != nil {
		return PortForward{}, apiError(m.logger, "StartPortForward", err)
	}

	m.logger.Info("port forward started",
		slog.String("cluster", clusterID),
		slog.String("pod", pod),
		slog.Int("local", forward.LocalPort),
		slog.Int("remote", remotePort))

	return toPortForward(forward), nil
}

// StopPortForward closes one forward.
func (m *ManagementAPI) StopPortForward(forwardID string) error {
	if err := m.forwards.StopPortForward(forwardID); err != nil {
		return apiError(m.logger, "StopPortForward", err)
	}
	return nil
}

// ListPortForwards reports what is forwarded right now.
//
// The list is the live registry, not a record of intent. A forward appears
// here because a goroutine is holding its socket — which is the difference
// between this and the clients where a forward shows as active after its
// connection died, and the stop button does nothing because there is nothing
// left to stop.
func (m *ManagementAPI) ListPortForwards() ([]PortForward, error) {
	forwards := m.forwards.ListPortForwards()

	out := make([]PortForward, 0, len(forwards))
	for _, forward := range forwards {
		out = append(out, toPortForward(forward))
	}
	return out, nil
}
