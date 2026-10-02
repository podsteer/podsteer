package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
	"github.com/podsteer/podsteer/app/safego"
)

// This file keeps port-forwards across a restart, for the ones the operator
// opted in.
//
// A KEPT FORWARD IS A DEFINITION, NOT A PROCESS. What reaches the settings
// file is the cluster context, the namespace, the pod or Service, two port
// numbers — see domain.KeptForward — and nothing else: no credential, no
// selector, nothing the cluster returned. On the next launch the definition is
// rebuilt into an ordinary forward through the same adapter calls a click
// makes, so a restored forward has exactly the behaviour of one started by
// hand, supervisor included.
//
// IT NEVER CONNECTS A CLUSTER. Opening a cluster is the operator's act — a tab
// — and a forward is no reason to reach a cluster they did not open. So at
// launch nothing is restored; each kept forward is listed as PAUSED, and is
// restored when its cluster is connected by the ordinary route (Connect calls
// ClusterConnected). A restore that cannot proceed — the port is taken, the pod
// is gone, the Service no longer selects anything — leaves the forward listed
// with the reason, for the operator to retry or forget.
//
// One owner, one way to stop: restores run on goroutines this service owns,
// and Close waits for them. A start racing Close is refused rather than
// registering a forward nobody will stop.

// ErrForwardNotRunning reports a Keep or Stop aimed at a forward that is not
// in the live registry — a normal race with a supervisor or another click.
var ErrForwardNotRunning = errors.New("that port-forward is not running")

// restoreTimeout bounds one restored forward. The adapter already gives a dial
// ten seconds; this covers resolving the target before it.
const restoreTimeout = 25 * time.Second

// KeptForwardState says why a kept forward is not running.
type KeptForwardState string

const (
	// KeptPaused means the cluster is not connected, so nothing was tried.
	KeptPaused KeptForwardState = "paused"
	// KeptRestoring means the cluster is connected and the restore is under way.
	KeptRestoring KeptForwardState = "restoring"
	// KeptFailed means the restore was tried and refused; Reason says why.
	KeptFailed KeptForwardState = "failed"
)

// PausedForward is a kept forward that is not running right now.
type PausedForward struct {
	ClusterID domain.ClusterID
	Kept      domain.KeptForward
	State     KeptForwardState
	// Reason is the failure, for KeptFailed only.
	Reason string
}

// KeeperSettings is the part of the settings store the keeper uses, defined
// here at the consumer.
type KeeperSettings interface {
	Load(ctx context.Context) (domain.Settings, error)
	Update(ctx context.Context, mutate func(*domain.Settings) error) (domain.Settings, error)
}

// KeeperForwards is the part of the port-forward transport the keeper uses.
type KeeperForwards interface {
	StartPortForward(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, pod, podUID string, localPort, remotePort int, portName, protocol string, selector map[string]string) (domain.Forward, error)
	ServiceForwardTarget(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, service, wantedPort string) (domain.ServiceForwardTarget, error)
	PodForwardTarget(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, pod string, remotePort int) (domain.ServiceForwardTarget, error)
	SetForwardTarget(id string, target domain.ForwardTarget) error
	ListPortForwards() []domain.Forward
}

// ForwardKeeperDeps are the keeper's collaborators.
type ForwardKeeperDeps struct {
	// Settings holds the definitions. Required.
	Settings KeeperSettings
	// Forwards starts the forwards. Required.
	Forwards KeeperForwards
	// Registry says which clusters are open. Required.
	Registry *Registry
	// Logger receives diagnostics. Optional.
	Logger *slog.Logger
}

type keptKey struct {
	cluster   domain.ClusterID
	localPort int
}

// ForwardKeeper persists opted-in forwards and restores them.
type ForwardKeeper struct {
	settings KeeperSettings
	forwards KeeperForwards
	registry *Registry
	logger   *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	wait   sync.WaitGroup

	mu        sync.Mutex
	closed    bool
	failures  map[keptKey]string
	restoring map[domain.ClusterID]bool
}

// NewForwardKeeper validates deps and returns the keeper.
func NewForwardKeeper(deps ForwardKeeperDeps) (*ForwardKeeper, error) {
	switch {
	case deps.Settings == nil:
		return nil, errors.New("application: ForwardKeeper requires settings")
	case deps.Forwards == nil:
		return nil, errors.New("application: ForwardKeeper requires a port-forward transport")
	case deps.Registry == nil:
		return nil, errors.New("application: ForwardKeeper requires a Registry")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &ForwardKeeper{
		settings:  deps.Settings,
		forwards:  deps.Forwards,
		registry:  deps.Registry,
		logger:    logger.With(slog.String("service", "forwardkeeper")),
		ctx:       ctx,
		cancel:    cancel,
		failures:  make(map[keptKey]string),
		restoring: make(map[domain.ClusterID]bool),
	}, nil
}

// Keep turns "keep across restarts" on or off for a live forward.
func (k *ForwardKeeper) Keep(ctx context.Context, forwardID string, keep bool) error {
	forward, ok := k.live(forwardID)
	if !ok {
		return fmt.Errorf("keeping forward %q: %w", forwardID, ErrForwardNotRunning)
	}

	if !keep {
		return k.remove(ctx, forward.ClusterID, forward.LocalPort)
	}

	kept := domain.KeptForward{
		Namespace:  forward.Namespace,
		Target:     forward.Target,
		RemotePort: forward.RemotePort,
		LocalPort:  forward.LocalPort,
	}
	if kept.Target.Name == "" {
		// A forward the adapter did not label: fall back to the pod it is on,
		// which is what a pod forward is.
		kept.Target = domain.ForwardTarget{Kind: domain.ForwardToPod, Name: forward.Pod}
	}
	if kept.Target.Kind == domain.ForwardToService {
		// A Service is restored from its own port, not from the container
		// port of whichever pod answered today.
		kept.RemotePort = 0
	}
	if err := kept.Validate(); err != nil {
		return fmt.Errorf("keeping forward %q: %w", forwardID, err)
	}

	_, err := k.settings.Update(ctx, func(settings *domain.Settings) error {
		cluster := settings.Cluster(forward.ClusterID)
		cluster.KeptForwards = replaceKept(cluster.KeptForwards, kept)
		settings.Clusters[string(forward.ClusterID)] = cluster
		return nil
	})
	if err != nil {
		return fmt.Errorf("keeping forward %q: %w", forwardID, err)
	}
	return nil
}

// replaceKept returns list with kept in it, replacing any entry on the same
// local port: a machine has one of each.
func replaceKept(list []domain.KeptForward, kept domain.KeptForward) []domain.KeptForward {
	out := make([]domain.KeptForward, 0, len(list)+1)
	for _, existing := range list {
		if existing.LocalPort != kept.LocalPort {
			out = append(out, existing)
		}
	}
	return append(out, kept)
}

// ForgetLive drops the definition for a live forward the operator is about to
// stop: stopping is a decision, and a stopped forward must not come back.
// A forward that was never kept, or a settings file that cannot be written,
// is not an error here — the stop must go ahead regardless.
func (k *ForwardKeeper) ForgetLive(ctx context.Context, forwardID string) {
	forward, ok := k.live(forwardID)
	if !ok {
		return
	}
	if err := k.remove(ctx, forward.ClusterID, forward.LocalPort); err != nil &&
		!errors.Is(err, ports.ErrSettingsReadOnly) && !errors.Is(err, ports.ErrSettingsFromFuture) {
		k.logger.WarnContext(ctx, "could not forget a stopped forward",
			slog.String("cluster", forward.ClusterID.String()), slog.String("error", err.Error()))
	}
}

// ForgetAllLive is ForgetLive for every running forward, for "Stop all".
func (k *ForwardKeeper) ForgetAllLive(ctx context.Context) {
	for _, forward := range k.forwards.ListPortForwards() {
		k.ForgetLive(ctx, forward.ID)
	}
}

// Forget removes a kept definition by cluster and local port, for a paused
// forward the operator no longer wants.
func (k *ForwardKeeper) Forget(ctx context.Context, id domain.ClusterID, localPort int) error {
	return k.remove(ctx, id, localPort)
}

func (k *ForwardKeeper) remove(ctx context.Context, id domain.ClusterID, localPort int) error {
	settings, err := k.settings.Load(ctx)
	if err != nil {
		return fmt.Errorf("forgetting a kept forward: %w", err)
	}
	// Nothing to write for a forward that was never kept, so a stop does not
	// rewrite the file for every forward ever started.
	if !slices.ContainsFunc(settings.Cluster(id).KeptForwards, func(f domain.KeptForward) bool {
		return f.LocalPort == localPort
	}) {
		return nil
	}

	if _, err := k.settings.Update(ctx, func(settings *domain.Settings) error {
		cluster := settings.Cluster(id)
		cluster.KeptForwards = slices.DeleteFunc(slices.Clone(cluster.KeptForwards), func(f domain.KeptForward) bool {
			return f.LocalPort == localPort
		})
		settings.Clusters[string(id)] = cluster
		return nil
	}); err != nil {
		return fmt.Errorf("forgetting a kept forward: %w", err)
	}

	k.mu.Lock()
	delete(k.failures, keptKey{id, localPort})
	k.mu.Unlock()
	return nil
}

// KeptPorts reports which live forwards are kept, as cluster and local port.
func (k *ForwardKeeper) KeptPorts(ctx context.Context) (map[domain.ClusterID]map[int]bool, error) {
	settings, err := k.settings.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading kept forwards: %w", err)
	}
	out := make(map[domain.ClusterID]map[int]bool)
	for id, cluster := range settings.Clusters {
		for _, kept := range cluster.KeptForwards {
			cid := domain.ClusterID(id)
			if out[cid] == nil {
				out[cid] = make(map[int]bool)
			}
			out[cid][kept.LocalPort] = true
		}
	}
	return out, nil
}

// Paused lists every kept forward that is not running, with why.
func (k *ForwardKeeper) Paused(ctx context.Context) ([]PausedForward, error) {
	settings, err := k.settings.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading kept forwards: %w", err)
	}

	running := make(map[keptKey]struct{})
	for _, forward := range k.forwards.ListPortForwards() {
		running[keptKey{forward.ClusterID, forward.LocalPort}] = struct{}{}
	}

	k.mu.Lock()
	defer k.mu.Unlock()

	var out []PausedForward
	for _, contextName := range sortedKeys(settings.Clusters) {
		id := domain.ClusterID(contextName)
		for _, kept := range settings.Clusters[contextName].KeptForwards {
			key := keptKey{id, kept.LocalPort}
			if _, up := running[key]; up {
				continue
			}
			paused := PausedForward{ClusterID: id, Kept: kept, State: KeptPaused}
			// A cluster that is not open is paused whatever happened last
			// time: the reason a restore failed belongs to a connection that
			// no longer exists.
			if k.registry.IsOpen(id) {
				if reason, failed := k.failures[key]; failed {
					paused.State, paused.Reason = KeptFailed, reason
				} else if k.restoring[id] {
					paused.State = KeptRestoring
				}
			}
			out = append(out, paused)
		}
	}
	return out, nil
}

func sortedKeys(m map[string]domain.ClusterSettings) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// ClusterConnected restores the cluster's kept forwards, in the background.
//
// Called by the cluster service after a successful Connect, so a restore only
// ever follows the operator opening the cluster.
func (k *ForwardKeeper) ClusterConnected(id domain.ClusterID) {
	k.mu.Lock()
	if k.closed || k.restoring[id] {
		k.mu.Unlock()
		return
	}
	k.restoring[id] = true
	k.wait.Add(1)
	k.mu.Unlock()

	go func() {
		defer k.wait.Done()
		defer safego.Recover("forward restore " + id.String())
		defer func() {
			k.mu.Lock()
			delete(k.restoring, id)
			k.mu.Unlock()
		}()
		k.Restore(k.ctx, id)
	}()
}

// Restore starts every kept forward of one cluster that is not already
// running, recording why any could not be.
func (k *ForwardKeeper) Restore(ctx context.Context, id domain.ClusterID) {
	settings, err := k.settings.Load(ctx)
	if err != nil {
		k.logger.WarnContext(ctx, "could not read kept forwards", slog.String("error", err.Error()))
		return
	}

	for _, kept := range settings.Cluster(id).KeptForwards {
		if ctx.Err() != nil {
			return
		}
		if _, ok := k.liveOn(id, kept.LocalPort); ok {
			continue
		}
		if err := k.start(ctx, id, kept); err != nil {
			k.logger.WarnContext(ctx, "could not restore a kept forward",
				slog.String("cluster", id.String()),
				slog.Int("local", kept.LocalPort),
				slog.String("error", err.Error()))
		}
	}
}

// Resume retries one kept forward, for the operator pressing the button on a
// failed one.
func (k *ForwardKeeper) Resume(ctx context.Context, id domain.ClusterID, localPort int) error {
	if !k.registry.IsOpen(id) {
		return fmt.Errorf("resuming a kept forward in %q: %w", id, domain.ErrClusterNotConnected)
	}
	settings, err := k.settings.Load(ctx)
	if err != nil {
		return fmt.Errorf("resuming a kept forward: %w", err)
	}
	for _, kept := range settings.Cluster(id).KeptForwards {
		if kept.LocalPort == localPort {
			return k.start(ctx, id, kept)
		}
	}
	return fmt.Errorf("resuming a kept forward: no forward is kept on local port %d", localPort)
}

// start rebuilds one forward from its definition and records the outcome.
func (k *ForwardKeeper) start(ctx context.Context, id domain.ClusterID, kept domain.KeptForward) (err error) {
	key := keptKey{id, kept.LocalPort}
	defer func() {
		k.mu.Lock()
		defer k.mu.Unlock()
		if err != nil {
			k.failures[key] = err.Error()
			return
		}
		delete(k.failures, key)
	}()

	ctx, cancel := context.WithTimeout(ctx, restoreTimeout)
	defer cancel()

	var target domain.ServiceForwardTarget
	switch kept.Target.Kind {
	case domain.ForwardToService:
		target, err = k.forwards.ServiceForwardTarget(ctx, id, kept.Namespace, kept.Target.Name, kept.Target.ServicePort)
	default:
		target, err = k.forwards.PodForwardTarget(ctx, id, kept.Namespace, kept.Target.Name, kept.RemotePort)
	}
	if err != nil {
		return fmt.Errorf("restoring %s/%s: %w", kept.Namespace, kept.Target.Name, err)
	}

	forward, err := k.forwards.StartPortForward(ctx, id, kept.Namespace, target.Pod, target.PodUID,
		kept.LocalPort, target.ContainerPort, target.PortName, target.Protocol, target.Selector)
	if err != nil {
		return fmt.Errorf("restoring %s/%s on local port %d: %w", kept.Namespace, kept.Target.Name, kept.LocalPort, err)
	}
	if err := k.forwards.SetForwardTarget(forward.ID, kept.Target); err != nil {
		k.logger.WarnContext(ctx, "restored forward could not be labelled", slog.String("error", err.Error()))
	}
	return nil
}

func (k *ForwardKeeper) live(forwardID string) (domain.Forward, bool) {
	for _, forward := range k.forwards.ListPortForwards() {
		if forward.ID == forwardID {
			return forward, true
		}
	}
	return domain.Forward{}, false
}

func (k *ForwardKeeper) liveOn(id domain.ClusterID, localPort int) (domain.Forward, bool) {
	for _, forward := range k.forwards.ListPortForwards() {
		if forward.ClusterID == id && forward.LocalPort == localPort {
			return forward, true
		}
	}
	return domain.Forward{}, false
}

// Close stops accepting restores and waits for the ones under way.
func (k *ForwardKeeper) Close() {
	k.mu.Lock()
	k.closed = true
	k.mu.Unlock()
	k.cancel()
	k.wait.Wait()
}
