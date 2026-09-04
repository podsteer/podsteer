package wails

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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
	// workloads reads the pods a drain plan is built from. Borrowed here
	// rather than duplicated: PlanDrain needs DrainCandidates, which is a
	// WorkloadService read (see ports.WorkloadService.DrainCandidates), and
	// domain.PlanDrain itself is a pure function called directly — the same
	// pattern BrowseAPI.ClassifyConditions uses for a domain rule that
	// reaches no cluster.
	workloads ports.WorkloadService
	app       *App
	logger    *slog.Logger

	// Active log streams, keyed by stream ID, with their cancel function.
	streamsMu sync.Mutex
	streams   map[string]context.CancelFunc
}

// NewManagementAPI returns a new management API.
func NewManagementAPI(management *application.ManagementService, forwards ports.PortForwardPort, workloads ports.WorkloadService, app *App, logger *slog.Logger) (*ManagementAPI, error) {
	switch {
	case management == nil:
		return nil, errors.New("wails: ManagementAPI requires a ManagementService")
	case forwards == nil:
		return nil, errors.New("wails: ManagementAPI requires a PortForwardPort")
	case workloads == nil:
		return nil, errors.New("wails: ManagementAPI requires a WorkloadService")
	case app == nil:
		return nil, errors.New("wails: ManagementAPI requires an App")
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &ManagementAPI{
		management: management,
		forwards:   forwards,
		workloads:  workloads,
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
//
// sinceSeconds and limitBytes of 0 mean unset, matching domain.LogOptions —
// see its doc comment for what each parameter does. timestamps is always
// sent true by the frontend today: it decides whether to DISPLAY a
// timestamp at render time rather than by re-opening the stream, so the
// parameter exists here for the same reason it exists on domain.LogOptions,
// not because any caller currently varies it.
func (m *ManagementAPI) StreamLogs(clusterID, namespace, podName, containerName string, follow bool, tailLines int, sinceSeconds int, previous bool, timestamps bool, limitBytes int) (string, error) {
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

	opts := domain.LogOptions{
		Follow:       follow,
		TailLines:    int64(tailLines),
		SinceSeconds: int64(sinceSeconds),
		Previous:     previous,
		Timestamps:   timestamps,
		LimitBytes:   int64(limitBytes),
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
			err := m.management.StreamLogs(ctx, id, ns, podName, containerName, opts, out)
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
		return apiError(m.logger, "DeleteResource", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return apiError(m.logger, "DeleteResource", err)
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

	if err := m.management.DeleteResource(ctx, ref); err != nil {
		return apiError(m.logger, "DeleteResource", err)
	}
	return nil
}

// ScaleWorkload sets the replica count for a workload.
func (m *ManagementAPI) ScaleWorkload(clusterID, kind, namespace, name string, replicas int) error {
	ctx, cancel := m.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return apiError(m.logger, "ScaleWorkload", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return apiError(m.logger, "ScaleWorkload", err)
	}

	if err := m.management.ScaleWorkload(ctx, id, domain.WorkloadKind(kind), ns, name, int32(replicas)); err != nil {
		return apiError(m.logger, "ScaleWorkload", err)
	}
	return nil
}

// RestartRollout triggers a rolling restart of a Deployment or StatefulSet.
func (m *ManagementAPI) RestartRollout(clusterID, kind, namespace, name string) error {
	ctx, cancel := m.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return apiError(m.logger, "RestartRollout", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return apiError(m.logger, "RestartRollout", err)
	}

	if err := m.management.RestartRollout(ctx, id, domain.WorkloadKind(kind), ns, name); err != nil {
		return apiError(m.logger, "RestartRollout", err)
	}
	return nil
}

// TriggerCronJob creates a Job from a CronJob's template right now, outside
// its schedule, and returns the created Job's name.
func (m *ManagementAPI) TriggerCronJob(clusterID, namespace, name string) (string, error) {
	ctx, cancel := m.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return "", apiError(m.logger, "TriggerCronJob", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return "", apiError(m.logger, "TriggerCronJob", err)
	}

	jobName, err := m.management.TriggerCronJob(ctx, id, ns, name)
	if err != nil {
		return "", apiError(m.logger, "TriggerCronJob", err)
	}

	return jobName, nil
}

// SuspendWorkload sets or clears suspend on a CronJob or a Job.
func (m *ManagementAPI) SuspendWorkload(clusterID, kind, namespace, name string, suspend bool) error {
	ctx, cancel := m.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return apiError(m.logger, "SuspendWorkload", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return apiError(m.logger, "SuspendWorkload", err)
	}

	if err := m.management.SuspendWorkload(ctx, id, domain.WorkloadKind(kind), ns, name, suspend); err != nil {
		return apiError(m.logger, "SuspendWorkload", err)
	}

	return nil
}

// SetImage sets one container's (or, when initContainer is true, one init
// container's) image on a Deployment, StatefulSet or DaemonSet.
func (m *ManagementAPI) SetImage(clusterID, kind, namespace, name, container, image string, initContainer bool) error {
	ctx, cancel := m.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return apiError(m.logger, "SetImage", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return apiError(m.logger, "SetImage", err)
	}

	if err := m.management.SetImage(ctx, id, domain.WorkloadKind(kind), ns, name, container, image, initContainer); err != nil {
		return apiError(m.logger, "SetImage", err)
	}

	return nil
}

// RollbackWorkload rolls a Deployment, StatefulSet or DaemonSet back to a
// previously recorded revision, the way `kubectl rollout undo
// --to-revision` does. dryRun asks the API server to validate the request
// without persisting anything — RollbackDialog's Preview button — and is
// allowed on a read-only cluster for the same reason ValidateResource is.
func (m *ManagementAPI) RollbackWorkload(clusterID, kind, namespace, name string, toRevision int, dryRun bool) (RollbackOutcomeDTO, error) {
	ctx, cancel := m.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return RollbackOutcomeDTO{}, apiError(m.logger, "RollbackWorkload", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return RollbackOutcomeDTO{}, apiError(m.logger, "RollbackWorkload", err)
	}

	outcome, err := m.management.RollbackWorkload(ctx, id, domain.WorkloadKind(kind), ns, name, int64(toRevision), dryRun)
	if err != nil {
		return RollbackOutcomeDTO{}, apiError(m.logger, "RollbackWorkload", err)
	}

	return toRollbackOutcome(outcome), nil
}

// SetSecretKey writes one key of one Secret.
//
// value is the operator's typed, decoded text — not base64 — converted to
// bytes here rather than asking the frontend to encode anything, mirroring
// how RevealSecretKey hands back the decoded string in the other direction.
//
// Bound as its own narrow method for the same reason RevealSecretKey is: it
// is only ever called from a deliberate Save on a key the operator has
// already revealed, which is what keeps each entry in a cluster's audit log
// interpretable. See web/src/lib/components/ContainerDetail.svelte and
// CLAUDE.md's "Secrets are read on request, never on render".
func (m *ManagementAPI) SetSecretKey(clusterID, namespace, name, key, value string) error {
	ctx, cancel := m.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return apiError(m.logger, "SetSecretKey", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return apiError(m.logger, "SetSecretKey", err)
	}

	if err := m.management.SetSecretKey(ctx, id, ns, name, key, []byte(value)); err != nil {
		return apiError(m.logger, "SetSecretKey", err)
	}

	return nil
}

// SetConfigMapKey writes one key of one ConfigMap.
func (m *ManagementAPI) SetConfigMapKey(clusterID, namespace, name, key, value string) error {
	ctx, cancel := m.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return apiError(m.logger, "SetConfigMapKey", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return apiError(m.logger, "SetConfigMapKey", err)
	}

	if err := m.management.SetConfigMapKey(ctx, id, ns, name, key, value); err != nil {
		return apiError(m.logger, "SetConfigMapKey", err)
	}

	return nil
}

// UpdateResource applies a YAML manifest of any kind to the cluster — the
// generic path through the dynamic client, not a fixed set of typed kinds.
// See ports.ManagementPort.UpdateResource for the full contract: a manifest
// carrying metadata.resourceVersion is sent as a PUT the server optimistic-
// locks against (a stale version comes back as the "conflict" error code);
// one without it is created, replacing an existing object of the same name.
func (m *ManagementAPI) UpdateResource(clusterID, manifest string) (ApplyOutcomeDTO, error) {
	ctx, cancel := m.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return ApplyOutcomeDTO{}, apiError(m.logger, "UpdateResource", err)
	}

	outcome, err := m.management.UpdateResource(ctx, id, manifest, false)
	if err != nil {
		return ApplyOutcomeDTO{}, apiError(m.logger, "UpdateResource", err)
	}
	return toApplyOutcome(outcome), nil
}

// ValidateResource is UpdateResource's dry run: the manifest is sent through
// the same generic apply path with DryRun=All, so the API server runs every
// admission check (schema validation, webhooks) without persisting anything.
// Allowed on a read-only cluster — see ManagementService.UpdateResource's own
// comment on why — which is what lets an operator check a manifest is
// well-formed before asking someone with write access to apply it for real.
func (m *ManagementAPI) ValidateResource(clusterID, manifest string) (ApplyOutcomeDTO, error) {
	ctx, cancel := m.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return ApplyOutcomeDTO{}, apiError(m.logger, "ValidateResource", err)
	}

	outcome, err := m.management.UpdateResource(ctx, id, manifest, true)
	if err != nil {
		return ApplyOutcomeDTO{}, apiError(m.logger, "ValidateResource", err)
	}
	return toApplyOutcome(outcome), nil
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
		return "", apiError(m.logger, "ExecInPod", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return "", apiError(m.logger, "ExecInPod", err)
	}

	var stdout, stderr bytes.Buffer
	err = m.management.ExecInPod(ctx, id, ns, podName, containerName, command, nil, &stdout, &stderr, false)
	if err != nil {
		return "", apiError(m.logger, "ExecInPod", err)
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
	// Scheme is "http" or "https", guessed from the container port's name —
	// see domain.SchemeForPort. Carried separately from Address, which is
	// meant for display against "localhost", so the frontend can build the
	// 127.0.0.1 form OpenURL needs without re-parsing a string built for
	// something else.
	Scheme string `json:"scheme"`
	// Reconnecting reports that the pod behind this forward went away and a
	// replacement is being sought. The local port stays bound throughout, so
	// whatever is pointed at it keeps its address and simply stalls.
	Reconnecting bool `json:"reconnecting"`
}

func toPortForward(forward domain.Forward) PortForward {
	return PortForward{
		ID:           forward.ID,
		ClusterID:    forward.ClusterID.String(),
		Namespace:    forward.Namespace.String(),
		Pod:          forward.Pod,
		LocalPort:    forward.LocalPort,
		RemotePort:   forward.RemotePort,
		Address:      forward.Address(),
		Scheme:       forward.Scheme,
		Reconnecting: forward.Reconnecting,
	}
}

// StartPortForward opens a local port onto a container port.
//
// localPort may be zero, in which case the operating system chooses and the
// returned forward carries what it chose. That is the honest default: asking
// somebody to pick a free port is asking them to guess, and a collision is
// reported as a failure to start rather than silently moved somewhere else.
func (m *ManagementAPI) StartPortForward(clusterID, namespace, pod, podUID string, localPort, remotePort int, portName, protocol string, selector map[string]string) (PortForward, error) {
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

	forward, err := m.forwards.StartPortForward(ctx, id, ns, pod, podUID, localPort, remotePort, portName, protocol, selector)
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

// CordonNode marks a node schedulable or unschedulable.
func (m *ManagementAPI) CordonNode(clusterID, name string, cordon bool) error {
	ctx, cancel := m.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return apiError(m.logger, "CordonNode", err)
	}

	if err := m.management.CordonNode(ctx, id, name, cordon); err != nil {
		return apiError(m.logger, "CordonNode", err)
	}

	return nil
}

// EvictPod evicts one pod through the eviction subresource, which a
// PodDisruptionBudget may refuse — see ports.ErrDisruptionBudget.
func (m *ManagementAPI) EvictPod(clusterID, namespace, name string, gracePeriodSeconds int) error {
	ctx, cancel := m.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return apiError(m.logger, "EvictPod", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return apiError(m.logger, "EvictPod", err)
	}

	if err := m.management.EvictPod(ctx, id, ns, name, gracePeriodSeconds); err != nil {
		return apiError(m.logger, "EvictPod", err)
	}

	return nil
}

// PlanDrain previews what draining name would do, without touching the
// cluster — the candidates a drain of this node would consider, run through
// the same domain.PlanDrain a real drain uses, so the preview and the drain
// it precedes can never disagree.
func (m *ManagementAPI) PlanDrain(clusterID, name string, force, deleteEmptyDirData bool) (DrainPlanDTO, error) {
	ctx, cancel := m.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return DrainPlanDTO{}, apiError(m.logger, "PlanDrain", err)
	}

	candidates, err := m.workloads.DrainCandidates(ctx, id, name)
	if err != nil {
		return DrainPlanDTO{}, apiError(m.logger, "PlanDrain", err)
	}

	opts := domain.DrainOptions{Force: force, DeleteEmptyDirData: deleteEmptyDirData}
	return toDrainPlan(domain.PlanDrain(candidates, opts)), nil
}

// DrainNode cordons a node and evicts every pod the drain plan allows.
//
// timeoutSeconds of zero or less means the adapter's own default. A negative
// gracePeriodSeconds means "use each pod's own
// terminationGracePeriodSeconds", the same convention `kubectl drain
// --grace-period` uses.
func (m *ManagementAPI) DrainNode(clusterID, name string, force, deleteEmptyDirData bool, gracePeriodSeconds, timeoutSeconds int) (DrainReportDTO, error) {
	ctx, cancel := m.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return DrainReportDTO{}, apiError(m.logger, "DrainNode", err)
	}

	opts := domain.DrainOptions{
		Force:              force,
		DeleteEmptyDirData: deleteEmptyDirData,
		GracePeriodSeconds: gracePeriodSeconds,
		Timeout:            time.Duration(timeoutSeconds) * time.Second,
	}

	report, err := m.management.DrainNode(ctx, id, name, opts)
	if err != nil {
		// Wails rejects a promise with an error string alone — there is no
		// channel back for a value beside it — so an ErrDrainRefused here
		// loses the report's own Refused list. That is an acceptable gap
		// rather than a design flaw: the confirm button is disabled while
		// PlanDrain's preview is not runnable, so reaching here with a
		// refusal at all means the node changed underneath an operator
		// between opening the dialog and confirming it, which the error
		// message alone is enough to explain.
		return DrainReportDTO{}, apiError(m.logger, "DrainNode", err)
	}

	return toDrainReport(report), nil
}

// StopAllPortForwards closes every running forward, across every cluster,
// and waits for each local port to be released before returning.
//
// Previously reachable only from OnShutdown, where nothing was waiting on the
// answer. The "Stop all" control in the forwards panel needs the same
// guarantee StopPortForward already makes for one forward — that the port is
// free before the call returns — which the underlying registry teardown
// already provides; this only exposes it.
func (m *ManagementAPI) StopAllPortForwards() error {
	m.forwards.StopAllPortForwards()
	return nil
}

// ProbeLocalPort reports whether a TCP port on THIS machine — never the
// cluster — is free to bind.
//
// Placed beside the other port-forward calls rather than on SystemAPI: it
// exists purely to serve the local-port picker in the forward-start UI, the
// transport it is asking about is the same forwards.PortForwardPort this
// struct already holds, and splitting "local port" concerns across two bound
// APIs over a single feature would cost the frontend two imports for one
// idea. It never touches a cluster.
func (m *ManagementAPI) ProbeLocalPort(port int) (bool, error) {
	free, err := m.forwards.ProbeLocalPort(port)
	if err != nil {
		return false, apiError(m.logger, "ProbeLocalPort", err)
	}
	return free, nil
}

// FreeLocalPort asks the operating system for a local TCP port nothing is
// using, so the "Pick a free port" control can offer one instead of asking
// the operator to guess.
func (m *ManagementAPI) FreeLocalPort() (int, error) {
	port, err := m.forwards.FreeLocalPort()
	if err != nil {
		return 0, apiError(m.logger, "FreeLocalPort", err)
	}
	return port, nil
}

// --- Bulk actions -----------------------------------------------------------

// parseBulkAction narrows the frontend's action string to a domain.BulkAction,
// refusing anything else as invalid input rather than planning nothing.
func parseBulkAction(action string) (domain.BulkAction, error) {
	switch parsed := domain.BulkAction(action); parsed {
	case domain.BulkActionDelete, domain.BulkActionRestart, domain.BulkActionScale,
		domain.BulkActionCordon, domain.BulkActionUncordon:
		return parsed, nil
	default:
		return "", fmt.Errorf("%w: %q", errInvalidBulkAction, action)
	}
}

// PlanBulk previews what a bulk action would do to items, without touching
// the cluster — the same domain.PlanBulk the Bulk* methods below run, so the
// review dialog and the run it precedes can never disagree. The cluster's
// read-only flag is read here so a read-only cluster's preview says so on
// every line, before the operator confirms into a refusal.
//
// replicas is read for the scale action only.
func (m *ManagementAPI) PlanBulk(clusterID, action string, items []BulkItemDTO, replicas int) (BulkPlanDTO, error) {
	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return BulkPlanDTO{}, apiError(m.logger, "PlanBulk", err)
	}

	parsed, err := parseBulkAction(action)
	if err != nil {
		return BulkPlanDTO{}, apiError(m.logger, "PlanBulk", err)
	}

	candidates, err := toBulkCandidates(id, items)
	if err != nil {
		return BulkPlanDTO{}, apiError(m.logger, "PlanBulk", err)
	}

	opts := domain.BulkOptions{
		Action:   parsed,
		Replicas: int32(replicas),
		ReadOnly: m.management.ReadOnly(id),
	}
	return toBulkPlan(domain.PlanBulk(candidates, opts)), nil
}

// bulkRun is one of ManagementService's Bulk methods, bound to whatever the
// action needs beyond the candidates.
type bulkRun func(ctx context.Context, id domain.ClusterID, candidates []domain.BulkCandidate) ([]application.BulkResult, error)

// bulk is the shape every bulk method shares: parse the cluster, convert the
// rows, run, and classify each outcome. The returned error covers only what
// stopped the whole action — an invalid argument, a read-only cluster; a
// per-object failure is a result, never a rejected promise, because one
// forbidden delete must not hide the forty-nine that succeeded.
func (m *ManagementAPI) bulk(op, clusterID string, items []BulkItemDTO, run bulkRun) ([]BulkResultDTO, error) {
	ctx, cancel := m.app.requestContextFor(len(items))
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return nil, apiError(m.logger, op, err)
	}

	candidates, err := toBulkCandidates(id, items)
	if err != nil {
		return nil, apiError(m.logger, op, err)
	}

	results, err := run(ctx, id, candidates)
	if err != nil {
		return nil, apiError(m.logger, op, err)
	}

	return toBulkResults(results), nil
}

// BulkDelete deletes every selected object the plan allows, and reports each
// outcome.
func (m *ManagementAPI) BulkDelete(clusterID string, items []BulkItemDTO) ([]BulkResultDTO, error) {
	return m.bulk("BulkDelete", clusterID, items, m.management.BulkDelete)
}

// BulkRestart triggers a rolling restart of every selected Deployment,
// StatefulSet and DaemonSet, and reports each outcome.
func (m *ManagementAPI) BulkRestart(clusterID string, items []BulkItemDTO) ([]BulkResultDTO, error) {
	return m.bulk("BulkRestart", clusterID, items, m.management.BulkRestart)
}

// BulkScale sets replicas on every selected Deployment, StatefulSet and
// ReplicaSet, and reports each outcome.
func (m *ManagementAPI) BulkScale(clusterID string, items []BulkItemDTO, replicas int) ([]BulkResultDTO, error) {
	return m.bulk("BulkScale", clusterID, items,
		func(ctx context.Context, id domain.ClusterID, candidates []domain.BulkCandidate) ([]application.BulkResult, error) {
			return m.management.BulkScale(ctx, id, candidates, int32(replicas))
		})
}

// BulkCordon marks every selected node unschedulable (cordon true) or
// schedulable again (cordon false), and reports each outcome.
func (m *ManagementAPI) BulkCordon(clusterID string, items []BulkItemDTO, cordon bool) ([]BulkResultDTO, error) {
	return m.bulk("BulkCordon", clusterID, items,
		func(ctx context.Context, id domain.ClusterID, candidates []domain.BulkCandidate) ([]application.BulkResult, error) {
			return m.management.BulkCordon(ctx, id, candidates, cordon)
		})
}
