package k8s

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/podsteer/podsteer/app/domain"
)

// Watching a cluster's pods, so a refresh reads memory instead of the network.
//
// THE WATCH IS AN OPTIMISATION. POLLING IS THE TRUTH. Everything here is built
// around that one sentence, and it is what keeps the design small: a read is
// NEVER blocked on a cache being ready, because the path that answers when the
// cache is not ready is the path that answered before any of this existed.
// There is no sync timeout to tune, no first-refresh stall, and no new failure
// mode — the worst this can do is not help.
//
// That also settles the permissions question, which is the hard part of
// watching anything in a client somebody else's RBAC governs. An account
// scoped to one namespace cannot list pods cluster-wide; an account with a
// list/get Role cannot watch at all. Both are ordinary, both surface as a 403
// on the reflector's first call, and both end in the same place: the store is
// marked degraded, one line is logged, and every read carries on down the path
// it was using anyway. The cost of finding out is one refused request per
// connection.
//
// Pods only, deliberately. They are the great majority of what a poll
// transfers — a five-thousand-pod cluster is megabytes every few seconds,
// while the controller lists are kilobytes and are already coalesced by
// readcache.go. ReplicaSets and Jobs are the plausible second and third, and
// are deliberately NOT here until there is a measurement saying so: a
// ReplicaSet carries a whole pod template, so a namespace with long revision
// histories can rival the pod list, which is not the intuition anybody starts
// with.
//
// Metrics are not watchable at all. metrics.k8s.io serves no watch verb, so
// PodMetrics stays a poll whatever else changes, and that sets the floor on
// what a refresh can ever cost.

// watchState is where one kind's store has got to.
type watchState int32

const (
	// watchStarting means the reflector is running but has not synced, or has
	// not been started yet. Reads go to the network.
	watchStarting watchState = iota
	// watchServing means the store is synced and its watch is healthy.
	watchServing
	// watchDegraded means this cluster will not be watched — refused, or
	// failing — until it is reconnected. Reads go to the network, for good.
	watchDegraded
)

// watchManager holds one watch set per connected cluster.
type watchManager struct {
	// enabled is the whole feature. Off means nothing is ever started, and
	// every read behaves exactly as it did before this file existed.
	enabled bool
	logger  *slog.Logger

	mu   sync.Mutex
	sets map[domain.ClusterID]*watchSet
}

// watchSet is one cluster's watches, and the goroutines behind them.
type watchSet struct {
	cancel context.CancelFunc
	// done closes when every reflector in the set has returned, so a teardown
	// can be waited on rather than hoped about.
	done chan struct{}
	pods *kindWatch
}

// kindWatch is one kind's store and its state.
type kindWatch struct {
	informer cache.SharedIndexInformer
	state    atomic.Int32
	// served and direct are how many reads each path answered.
	//
	// THE NUMBER THAT DECIDES WHETHER TO WATCH ANYTHING ELSE. Reported once
	// per cluster when it is torn down rather than sampled, so it costs two
	// atomic increments and one log line — and it is the evidence for or
	// against extending this to ReplicaSets and Jobs, which is a decision
	// nobody should make from intuition about which objects are large.
	served atomic.Int64
	direct atomic.Int64
}

func (k *kindWatch) get() watchState  { return watchState(k.state.Load()) }
func (k *kindWatch) set(s watchState) { k.state.Store(int32(s)) }

func newWatchManager(enabled bool, logger *slog.Logger) *watchManager {
	return &watchManager{
		enabled: enabled,
		logger:  logger,
		sets:    make(map[domain.ClusterID]*watchSet),
	}
}

// pods returns the watched pods when the store is serving them.
//
// The second return is the whole contract: false means "ask the cluster", and
// it is false far more often than not — before the first sync, on a cluster
// whose account may not watch, and always when the feature is off.
func (m *watchManager) pods(id domain.ClusterID) ([]*corev1.Pod, bool) {
	if !m.enabled {
		return nil, false
	}

	m.mu.Lock()
	set, running := m.sets[id]
	m.mu.Unlock()

	if !running || set.pods.get() != watchServing {
		if running {
			set.pods.direct.Add(1)
		}
		return nil, false
	}
	set.pods.served.Add(1)

	objects := set.pods.informer.GetStore().List()
	pods := make([]*corev1.Pod, 0, len(objects))
	for _, object := range objects {
		if pod, ok := object.(*corev1.Pod); ok {
			pods = append(pods, pod)
		}
	}
	return pods, true
}

// ensure starts watching a cluster's pods if nothing is watching them yet.
//
// NEVER BLOCKS AND NEVER FAILS THE CALLER. It is called from the read path,
// which is about to answer from the network regardless; whether the store ever
// becomes useful is decided in the background.
func (m *watchManager) ensure(id domain.ClusterID, client func() (kubernetes.Interface, error)) {
	if !m.enabled || id.IsZero() {
		return
	}

	m.mu.Lock()
	if _, running := m.sets[id]; running {
		m.mu.Unlock()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	set := &watchSet{cancel: cancel, done: make(chan struct{}), pods: &kindWatch{}}
	m.sets[id] = set
	m.mu.Unlock()

	// Outside the lock, deliberately: building a client waits on the PATH
	// probe and takes the factory's own write lock, and holding this mutex
	// across that would serialise every other cluster behind the first one to
	// connect.
	go m.run(ctx, id, set, client)
}

func (m *watchManager) run(ctx context.Context, id domain.ClusterID, set *watchSet, client func() (kubernetes.Interface, error)) {
	defer close(set.done)

	api, err := client()
	if err != nil {
		set.pods.set(watchDegraded)
		m.logger.DebugContext(ctx, "not watching pods; no client",
			slog.String("cluster", id.String()), slog.String("error", err.Error()))
		return
	}

	informer := cache.NewSharedIndexInformer(
		&cache.ListWatch{
			// The context-carrying forms: the reflector passes its own, which
			// is how a cancelled set stops its in-flight request rather than
			// waiting for it to finish against a cluster nobody is watching.
			ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
				return api.CoreV1().Pods(metav1.NamespaceAll).List(ctx, options)
			},
			WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
				return api.CoreV1().Pods(metav1.NamespaceAll).Watch(ctx, options)
			},
		},
		&corev1.Pod{},
		0, // No resync: the store is a mirror, and a periodic full replay of
		// it would cost exactly what watching is here to avoid.
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)

	// STRIPPED ON THE WAY IN. A pod as the API server sends it is mostly
	// fields nothing here reads — managed-field bookkeeping above all, then
	// the last-applied annotation, which between them are usually the
	// majority of the bytes. What survives is what mapPod reads, and there is
	// a test asserting exactly that: see watch_test.go.
	if err := informer.SetTransform(stripPod); err != nil {
		set.pods.set(watchDegraded)
		return
	}

	// A REFUSAL IS A DECISION, NOT AN INCIDENT. An account that may not list
	// pods cluster-wide, or may list but not watch, is ordinary — it is told
	// once, at debug, and the store is condemned so no read ever waits on it
	// again. Anything else is left to the reflector's own backoff.
	_ = informer.SetWatchErrorHandler(func(_ *cache.Reflector, err error) {
		if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
			set.pods.set(watchDegraded)
			cancelIf(ctx, set)
			m.logger.DebugContext(ctx, "not watching pods; not permitted",
				slog.String("cluster", id.String()))
			return
		}
		// Transient. The store may have gone stale behind a watch that is no
		// longer delivering, so it stops serving until it has resynced.
		if set.pods.get() == watchServing {
			set.pods.set(watchStarting)
		}
	})

	// PUBLISHED BEFORE ANYTHING CAN FLIP THE STATE. A reader takes the
	// informer only after seeing `serving`, so the atomic store below is the
	// happens-before edge between the two; the other order is a read of a nil
	// informer under -race.
	set.pods.informer = informer

	go func() {
		// Flipped to serving only after a sync AND only if nothing has
		// condemned it in the meantime — a store that listed successfully and
		// was then refused its watch must never answer.
		if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
			return
		}
		if set.pods.get() == watchStarting {
			set.pods.set(watchServing)
			m.logger.DebugContext(ctx, "watching pods",
				slog.String("cluster", id.String()))
		}
	}()

	informer.Run(ctx.Done())
}

// cancelIf stops a set's reflectors once its watch has been refused, so a
// condemned store is not left retrying a request that will never be allowed.
func cancelIf(ctx context.Context, set *watchSet) {
	if ctx.Err() == nil {
		set.cancel()
	}
}

// forget stops watching one cluster and waits for its goroutines to exit.
//
// A SET IS NEVER REUSED. A reconnect builds a fresh one against the fresh
// client, so a reflector from before the disconnect cannot write into the
// store a later read will answer from.
func (m *watchManager) forget(id domain.ClusterID) {
	m.mu.Lock()
	set, running := m.sets[id]
	delete(m.sets, id)
	m.mu.Unlock()

	if !running {
		return
	}
	m.report(id, set)
	set.cancel()
	<-set.done
}

// report says how much the watch actually saved, once, on the way out.
func (m *watchManager) report(id domain.ClusterID, set *watchSet) {
	served, direct := set.pods.served.Load(), set.pods.direct.Load()
	if served+direct == 0 {
		return
	}
	m.logger.Debug("pod reads while connected",
		slog.String("cluster", id.String()),
		slog.Int64("fromWatch", served),
		slog.Int64("fromCluster", direct))
}

// stopAll tears every watch down, for shutdown.
func (m *watchManager) stopAll() {
	m.mu.Lock()
	sets := make([]*watchSet, 0, len(m.sets))
	for id, set := range m.sets {
		m.report(id, set)
		sets = append(sets, set)
		delete(m.sets, id)
	}
	m.mu.Unlock()

	for _, set := range sets {
		set.cancel()
		<-set.done
	}
}

// stripPod removes what nothing here reads, before a pod is stored.
//
// THE CONTRACT IS mapPod, AND IT IS TESTED. Everything removed below is a
// field mapPod never touches; the day somebody extends mapPod to read one of
// them, watch_test.go fails rather than the interface quietly losing a column
// on clusters where the watch happens to be serving.
//
// The pod spec's env, volumes and scheduling constraints go: the
// detail pane reads those from the object's own manifest, fetched on demand,
// not from the list. Container resources stay — the meters are computed from
// them on every row.
func stripPod(object any) (any, error) {
	pod, ok := object.(*corev1.Pod)
	if !ok {
		// A DeletedFinalStateUnknown tombstone, or a kind this does not
		// handle. Passed through untouched rather than dropped.
		return object, nil
	}

	trimmed := pod.DeepCopy()
	trimmed.ManagedFields = nil
	delete(trimmed.Annotations, corev1.LastAppliedConfigAnnotation)

	trimmed.Spec.Volumes = nil
	trimmed.Spec.Affinity = nil
	trimmed.Spec.Tolerations = nil
	trimmed.Spec.TopologySpreadConstraints = nil
	trimmed.Spec.NodeSelector = nil
	stripContainers(trimmed.Spec.Containers)
	stripContainers(trimmed.Spec.InitContainers)

	return trimmed, nil
}

func stripContainers(containers []corev1.Container) {
	for index := range containers {
		container := &containers[index]
		container.Env = nil
		container.EnvFrom = nil
		container.Command = nil
		container.Args = nil
		container.VolumeMounts = nil
		container.Lifecycle = nil
		// PROBES STAY. The contract test caught this: mapPod reads all three
		// and the pod list shows them, so stripping them blanked a column on
		// exactly the clusters where the watch happened to be serving — and
		// left it correct everywhere else, which is the worst way to find
		// out. That test is what this whole approach rests on.
	}
}
