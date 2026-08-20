package wails

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
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
	app        *App
	logger     *slog.Logger

	// Active log streams, keyed by stream ID, with their cancel function.
	streamsMu sync.Mutex
	streams   map[string]context.CancelFunc
}

// NewManagementAPI returns a new management API.
func NewManagementAPI(management *application.ManagementService, app *App, logger *slog.Logger) (*ManagementAPI, error) {
	switch {
	case management == nil:
		return nil, errors.New("wails: ManagementAPI requires a ManagementService")
	case app == nil:
		return nil, errors.New("wails: ManagementAPI requires an App")
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &ManagementAPI{
		management: management,
		app:        app,
		logger:     logger.With(slog.String("api", "management")),
		streams:    make(map[string]context.CancelFunc),
	}, nil
}

// StreamLogs streams pod logs to the frontend via events.
//
// The frontend must subscribe to "log:line" events to receive log lines.
// Each event payload is a LogLineEvent with the line text.
// The stream ends when the pod terminates, the context is cancelled, or an
// error occurs. A "log:end" event is emitted when the stream closes.
func (m *ManagementAPI) StreamLogs(clusterID, namespace, podName, containerName string, follow bool, tailLines int) (string, error) {
	ctx, cancel := context.WithCancel(m.app.ctx)

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

		// Start the stream.
		go func() {
			err := m.management.StreamLogs(ctx, id, ns, podName, containerName, follow, int64(tailLines), out)
			if err != nil && ctx.Err() == nil {
				m.logger.ErrorContext(ctx, "log stream error",
					slog.String("cluster", clusterID),
					slog.String("pod", podName),
					slog.String("error", err.Error()))
			}
		}()

		// Forward log lines to the frontend.
		for line := range out {
			m.app.emit("log:line", LogLineEvent{
				StreamID: streamID,
				Line:     line,
			})
		}

		// Signal end of stream.
		m.app.emit("log:end", LogEndEvent{
			StreamID: streamID,
		})
	}()

	return streamID, nil
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
