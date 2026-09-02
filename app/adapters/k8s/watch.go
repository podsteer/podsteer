package k8s

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
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

// idleAfter is how long a set survives with nothing reading it, and
// sweepEvery is how often that is checked.
//
// Five minutes is well past any refresh interval the application offers, so
// an ordinary session never trips it — it is aimed at the tab left open on a
// cluster nobody is looking at, and at "manual only", where the gap between
// button presses is the whole point.
const (
	idleAfter  = 5 * time.Minute
	sweepEvery = time.Minute
)

// watchSpec is everything needed to watch one kind.
type watchSpec struct {
	kind watchKind
	// object is the zero value of the type stored, for the informer.
	object runtime.Object
	// source builds the list and watch calls.
	source func(ctx context.Context, api kubernetes.Interface) *cache.ListWatch
	// transform removes what nothing here reads. Every one has a contract
	// test against its mapper; see watch_test.go.
	transform cache.TransformFunc
}

// watchedKinds is what a cluster is watched for, and it is deliberately short.
//
// Pods are the great majority of what a poll transfers. ReplicaSets and Jobs
// earn their place for a second reason: they are the intermediates a
// controller's usage is attributed through, so the Deployment and CronJob
// pages read them on every refresh — and a ReplicaSet carries a whole pod
// template, so a namespace with long revision histories can rival the pod
// list, which is not the intuition anybody starts with.
//
// Nothing else. Deployments, StatefulSets, DaemonSets, CronJobs, namespaces
// and nodes are small lists already coalesced by readcache.go; an informer
// each would buy hundreds of kilobytes a tick for four more state machines.
// Events are high-churn and would hold that churn in memory. Metrics cannot
// be watched at all — metrics.k8s.io serves no watch verb — which sets the
// floor on what a refresh can ever cost.
var watchedKinds = []watchSpec{
	{
		kind:   watchPods,
		object: &corev1.Pod{},
		source: func(ctx context.Context, api kubernetes.Interface) *cache.ListWatch {
			return &cache.ListWatch{
				// The context-carrying forms: the reflector passes its own,
				// which is how a cancelled set stops its in-flight request
				// rather than waiting for it to finish against a cluster
				// nobody is watching.
				ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
					return api.CoreV1().Pods(metav1.NamespaceAll).List(ctx, options)
				},
				WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
					return api.CoreV1().Pods(metav1.NamespaceAll).Watch(ctx, options)
				},
			}
		},
		transform: stripPod,
	},
	{
		kind:   watchReplicaSets,
		object: &appsv1.ReplicaSet{},
		source: func(ctx context.Context, api kubernetes.Interface) *cache.ListWatch {
			return &cache.ListWatch{
				ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
					return api.AppsV1().ReplicaSets(metav1.NamespaceAll).List(ctx, options)
				},
				WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
					return api.AppsV1().ReplicaSets(metav1.NamespaceAll).Watch(ctx, options)
				},
			}
		},
		transform: stripReplicaSet,
	},
	{
		kind:   watchJobs,
		object: &batchv1.Job{},
		source: func(ctx context.Context, api kubernetes.Interface) *cache.ListWatch {
			return &cache.ListWatch{
				ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
					return api.BatchV1().Jobs(metav1.NamespaceAll).List(ctx, options)
				},
				WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
					return api.BatchV1().Jobs(metav1.NamespaceAll).Watch(ctx, options)
				},
			}
		},
		transform: stripJob,
	},
}

// watchManager holds one watch set per connected cluster.
type watchManager struct {
	// enabled is the whole feature. Off means nothing is ever started, and
	// every read behaves exactly as it did before this file existed.
	enabled bool
	logger  *slog.Logger

	// idleAfter and sweepEvery are set once, at construction. See
	// newWatchManager.
	idleAfter  time.Duration
	sweepEvery time.Duration

	stopped chan struct{}
	wait    sync.WaitGroup

	mu     sync.Mutex
	closed bool
	sets   map[domain.ClusterID]*watchSet
}

// watchKind names one thing a cluster is watched for.
type watchKind string

const (
	watchPods        watchKind = "pods"
	watchReplicaSets watchKind = "replicasets"
	watchJobs        watchKind = "jobs"
)

// watchSet is one cluster's watches, and the goroutines behind them.
type watchSet struct {
	cancel context.CancelFunc
	// done closes when every reflector in the set has returned, so a teardown
	// can be waited on rather than hoped about.
	done  chan struct{}
	kinds map[watchKind]*kindWatch
	// lastRead is when something last asked this set anything, in unix nanos.
	//
	// A WATCH NOBODY IS READING IS A CONNECTION NOBODY ASKED FOR. Somebody
	// who set refresh to "manual only" chose to stop talking to their
	// cluster, and a stream left open between button presses is not that —
	// so a set that has answered nothing for a while is torn down, and the
	// next read starts a fresh one exactly as the first read did.
	lastRead atomic.Int64
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

// newWatchManager builds the manager and starts its sweeper.
//
// The two durations are parameters rather than the constants read directly,
// so a test can reap in milliseconds instead of minutes — and they are passed
// IN rather than set afterwards, because the sweeper reads them and starts
// here. Setting them on the returned value is a data race, which is how this
// signature came to have four arguments.
func newWatchManager(enabled bool, logger *slog.Logger, idle, sweep time.Duration) *watchManager {
	manager := &watchManager{
		enabled:    enabled,
		logger:     logger,
		sets:       make(map[domain.ClusterID]*watchSet),
		idleAfter:  idle,
		sweepEvery: sweep,
		stopped:    make(chan struct{}),
	}
	if enabled {
		manager.wait.Add(1)
		go manager.sweep()
	}
	return manager
}

// watched returns one kind's objects when its store is serving them.
//
// The second return is the whole contract: false means "ask the cluster", and
// it is false far more often than not — before the first sync, on a cluster
// whose account may not watch, and always when the feature is off.
func watched[T any](m *watchManager, id domain.ClusterID, kind watchKind) ([]T, bool) {
	if !m.enabled {
		return nil, false
	}

	m.mu.Lock()
	set, running := m.sets[id]
	m.mu.Unlock()

	if !running {
		return nil, false
	}
	set.lastRead.Store(time.Now().UnixNano())

	store := set.kinds[kind]
	if store == nil || store.get() != watchServing {
		if store != nil {
			store.direct.Add(1)
		}
		return nil, false
	}
	store.served.Add(1)

	objects := store.informer.GetStore().List()
	typed := make([]T, 0, len(objects))
	for _, object := range objects {
		if item, ok := object.(T); ok {
			typed = append(typed, item)
		}
	}
	return typed, true
}

// ensure starts watching a cluster if nothing is watching it yet.
//
// NEVER BLOCKS AND NEVER FAILS THE CALLER. It is called from the read path,
// which is about to answer from the network regardless; whether the stores
// ever become useful is decided in the background.
func (m *watchManager) ensure(id domain.ClusterID, client func() (kubernetes.Interface, error)) {
	if !m.enabled || id.IsZero() {
		return
	}

	m.mu.Lock()
	if set, running := m.sets[id]; running {
		set.lastRead.Store(time.Now().UnixNano())
		m.mu.Unlock()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	set := &watchSet{
		cancel: cancel,
		done:   make(chan struct{}),
		kinds:  make(map[watchKind]*kindWatch, len(watchedKinds)),
	}
	for _, spec := range watchedKinds {
		set.kinds[spec.kind] = &kindWatch{}
	}
	set.lastRead.Store(time.Now().UnixNano())
	m.sets[id] = set
	m.mu.Unlock()

	// Outside the lock, deliberately: building a client waits on the PATH
	// probe and takes the factory's own write lock, and holding this mutex
	// across that would serialise every other cluster behind the first one to
	// connect.
	go m.start(ctx, id, set, client)
}

// start builds one client and a reflector per kind behind it.
func (m *watchManager) start(ctx context.Context, id domain.ClusterID, set *watchSet, client func() (kubernetes.Interface, error)) {
	defer close(set.done)

	api, err := client()
	if err != nil {
		for _, store := range set.kinds {
			store.set(watchDegraded)
		}
		m.logger.DebugContext(ctx, "not watching; no client",
			slog.String("cluster", id.String()), slog.String("error", err.Error()))
		return
	}

	var running sync.WaitGroup
	for _, spec := range watchedKinds {
		running.Add(1)
		go func() {
			defer running.Done()
			m.run(ctx, id, set.kinds[spec.kind], spec, api)
		}()
	}
	running.Wait()
}

// run drives one kind's reflector until the set is cancelled.
func (m *watchManager) run(ctx context.Context, id domain.ClusterID, store *kindWatch, spec watchSpec, api kubernetes.Interface) {
	informer := cache.NewSharedIndexInformer(
		spec.source(ctx, api), spec.object, 0, // No resync: the store is a
		// mirror, and a periodic full replay of it would cost exactly what
		// watching is here to avoid.
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)

	// STRIPPED ON THE WAY IN. An object as the API server sends it is mostly
	// fields nothing here reads — managed-field bookkeeping above all, then
	// the last-applied annotation, which between them are usually the
	// majority of the bytes. What survives is what the mapper reads, and
	// there is a test per kind asserting exactly that: see watch_test.go.
	if err := informer.SetTransform(spec.transform); err != nil {
		store.set(watchDegraded)
		return
	}

	// A REFUSAL IS A DECISION, NOT AN INCIDENT. An account that may not list
	// this kind cluster-wide, or may list but not watch, is ordinary — it is
	// told once, at debug, and the store is condemned so no read ever waits
	// on it again. Anything else is left to the reflector's own backoff.
	_ = informer.SetWatchErrorHandler(func(_ *cache.Reflector, err error) {
		if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
			store.set(watchDegraded)
			m.logger.DebugContext(ctx, "not watching; not permitted",
				slog.String("cluster", id.String()), slog.String("kind", string(spec.kind)))
			return
		}
		// Transient. The store may have gone stale behind a watch that is no
		// longer delivering, so it stops serving until it has resynced.
		if store.get() == watchServing {
			store.set(watchStarting)
		}
	})

	// PUBLISHED BEFORE ANYTHING CAN FLIP THE STATE. A reader takes the
	// informer only after seeing `serving`, so the atomic store below is the
	// happens-before edge between the two; the other order is a read of a nil
	// informer under -race.
	store.informer = informer

	go func() {
		// Flipped to serving only after a sync AND only if nothing has
		// condemned it in the meantime — a store that listed successfully and
		// was then refused its watch must never answer.
		if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
			return
		}
		if store.get() == watchStarting {
			store.set(watchServing)
			m.logger.DebugContext(ctx, "watching",
				slog.String("cluster", id.String()), slog.String("kind", string(spec.kind)))
		}
	}()

	informer.Run(ctx.Done())
}

// sweep tears down sets nobody has read for a while.
func (m *watchManager) sweep() {
	defer m.wait.Done()

	ticker := time.NewTicker(m.sweepEvery)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopped:
			return
		case <-ticker.C:
			m.reapIdle()
		}
	}
}

func (m *watchManager) reapIdle() {
	cutoff := time.Now().Add(-m.idleAfter).UnixNano()

	m.mu.Lock()
	idle := make(map[domain.ClusterID]*watchSet)
	for id, set := range m.sets {
		if set.lastRead.Load() < cutoff {
			idle[id] = set
			delete(m.sets, id)
		}
	}
	m.mu.Unlock()

	for id, set := range idle {
		m.report(id, set)
		m.logger.Debug("stopped watching; nothing was reading it",
			slog.String("cluster", id.String()))
		set.cancel()
		<-set.done
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

// report says how much the watches actually saved, once, on the way out.
func (m *watchManager) report(id domain.ClusterID, set *watchSet) {
	for kind, store := range set.kinds {
		served, direct := store.served.Load(), store.direct.Load()
		if served+direct == 0 {
			continue
		}
		m.logger.Debug("reads while connected",
			slog.String("cluster", id.String()),
			slog.String("kind", string(kind)),
			slog.Int64("fromWatch", served),
			slog.Int64("fromCluster", direct))
	}
}

// stopAll tears every watch down, for shutdown. Safe to call twice.
func (m *watchManager) stopAll() {
	m.mu.Lock()
	sets := make(map[domain.ClusterID]*watchSet, len(m.sets))
	for id, set := range m.sets {
		sets[id] = set
		delete(m.sets, id)
	}
	stopping := !m.closed
	m.closed = true
	m.mu.Unlock()

	if stopping {
		close(m.stopped)
	}
	for id, set := range sets {
		m.report(id, set)
		set.cancel()
		<-set.done
	}
	m.wait.Wait()
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

// stripReplicaSet and stripJob keep only what their mappers read.
//
// THE POD TEMPLATE IS THE POINT. A ReplicaSet carries a whole one — every
// container's environment, volumes, probes and resources — and the mapper
// reads exactly one field out of it: the images. A namespace with ten
// revisions of a large Deployment therefore holds ten copies of a spec
// nothing displays, which is why these two are worth watching at all and why
// they are worth stripping hard.
//
// The selector survives, because attribution is not the only thing that reads
// it, and so do the replica counts, which are the columns.
func stripReplicaSet(object any) (any, error) {
	set, ok := object.(*appsv1.ReplicaSet)
	if !ok {
		return object, nil
	}

	trimmed := set.DeepCopy()
	stripMeta(&trimmed.ObjectMeta)
	stripTemplate(&trimmed.Spec.Template)
	return trimmed, nil
}

func stripJob(object any) (any, error) {
	job, ok := object.(*batchv1.Job)
	if !ok {
		return object, nil
	}

	trimmed := job.DeepCopy()
	stripMeta(&trimmed.ObjectMeta)
	stripTemplate(&trimmed.Spec.Template)
	return trimmed, nil
}

// stripMeta removes the bookkeeping every object carries and nothing reads.
func stripMeta(meta *metav1.ObjectMeta) {
	meta.ManagedFields = nil
	delete(meta.Annotations, corev1.LastAppliedConfigAnnotation)
}

// stripTemplate reduces a pod template to the images its mapper reads.
//
// Everything else in a template is display material the detail pane fetches
// from the object's own manifest on demand. Init container images count, so
// the containers themselves stay — emptied of all but their image.
func stripTemplate(template *corev1.PodTemplateSpec) {
	template.ObjectMeta = metav1.ObjectMeta{Labels: template.Labels}
	template.Spec = corev1.PodSpec{
		Containers:     imagesOnly(template.Spec.Containers),
		InitContainers: imagesOnly(template.Spec.InitContainers),
	}
}

func imagesOnly(containers []corev1.Container) []corev1.Container {
	if len(containers) == 0 {
		return nil
	}
	bare := make([]corev1.Container, 0, len(containers))
	for _, container := range containers {
		bare = append(bare, corev1.Container{Name: container.Name, Image: container.Image})
	}
	return bare
}
