package k8s

// Node disk occupancy, read from the kubelets themselves.
//
// This is the one measurement PodSteer cannot get from an aggregated API.
// metrics-server serves CPU and memory and nothing else; node capacity and
// allocatable describe what the scheduler may hand out, not what is occupied;
// and the DiskPressure condition only appears once the kubelet has already
// begun evicting. The kubelet's own /stats/summary endpoint has the number,
// reached through the API server's node proxy.
//
// Two consequences shape everything below. It needs the nodes/proxy
// permission, which plenty of clusters do not grant — so failure is ordinary
// and is reported as ErrMetricsUnavailable rather than as a fault. And it is
// one request per node, so a fifty-node cluster is fifty round trips: they run
// bounded and their result is cached, because disks do not fill in ten
// seconds and the overview refreshes that often.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

const (
	// filesystemTTL is how long a sweep's result is reused.
	//
	// Generous on purpose. A disk that fills fast enough for a minute to
	// matter was already going to fill, and the alternative is fifty kubelet
	// requests every ten seconds for a number that moves in hours.
	filesystemTTL = time.Minute

	// filesystemConcurrency bounds the sweep. High enough that a large
	// cluster finishes inside one refresh, low enough not to arrive at the
	// API server as a burst.
	filesystemConcurrency = 8

	// filesystemSweepCap is the most nodes one sweep asks. At or below it a
	// sweep asks every node, once a minute, as it always did; above it the
	// sweep ROLLS — see filesystemBatch.
	filesystemSweepCap = 128

	// filesystemBatch is how many nodes a rolling sweep asks: the ones whose
	// answer is oldest, never-asked first. A thousand-node cluster was a
	// thousand kubelet requests in one burst every minute, through the API
	// server's node proxy; now it is this many every filesystemBatchSpacing,
	// and the overview says how much of the cluster its figure covers and
	// how old the oldest part is (domain.DiskCoverage).
	filesystemBatch = 64

	// filesystemBatchSpacing is how long a rolling sweep's answer is reused
	// before the next batch is asked — the rolling cluster's TTL.
	filesystemBatchSpacing = 15 * time.Second

	// kubeletTimeout bounds one node's answer. The summary endpoint is
	// served from memory, so a kubelet that has not replied in this long is
	// not going to.
	kubeletTimeout = 5 * time.Second
)

// summaryResponse is the fragment of the kubelet's Summary API that matters.
//
// Declared here rather than importing k8s.io/kubelet: that module pulls a
// large dependency tree for one struct, and this is the whole of it. The
// endpoint also carries per-pod statistics, which are deliberately not
// decoded — on a busy node they are the great majority of the payload.
type summaryResponse struct {
	Node struct {
		NodeName string        `json:"nodeName"`
		Fs       *summaryFs    `json:"fs"`
		Runtime  *summaryRtime `json:"runtime"`
		// PSI, present from Kubernetes 1.36 on a cgroup v2 host. Absent
		// everywhere else, which is why every level of this is a pointer:
		// a missing section must read as "not reported", never as zero
		// stall, which would be an all-clear nobody gave.
		CPU    *summaryPSIHolder `json:"cpu"`
		Memory *summaryPSIHolder `json:"memory"`
		IO     *summaryPSIHolder `json:"io"`
	} `json:"node"`
}

type summaryPSIHolder struct {
	PSI *summaryPSI `json:"psi"`
}

type summaryPSI struct {
	// Some: at least one task stalled. The early signal, and the one worth
	// reporting — Full means every task was stalled, by which point the node
	// is in trouble on every other measure too.
	Some *summaryPSIStats `json:"some"`
}

type summaryPSIStats struct {
	// Avg10 is the proportion of the last ten seconds spent stalled, 0-100.
	Avg10 *float64 `json:"avg10"`
}

// stall reads one dimension's ten-second average, and whether it was reported.
func (h *summaryPSIHolder) stall() (float64, bool) {
	if h == nil || h.PSI == nil || h.PSI.Some == nil || h.PSI.Some.Avg10 == nil {
		return 0, false
	}
	return *h.PSI.Some.Avg10, true
}

type summaryFs struct {
	CapacityBytes  *int64 `json:"capacityBytes"`
	UsedBytes      *int64 `json:"usedBytes"`
	AvailableBytes *int64 `json:"availableBytes"`
}

type summaryRtime struct {
	ImageFs *summaryFs `json:"imageFs"`
}

// toFilesystem converts one reported filesystem.
//
// Used is preferred to capacity-minus-available when both are present: on a
// filesystem with reserved blocks the two disagree, and used is what the
// kubelet's own eviction logic works from.
func (f *summaryFs) toFilesystem() domain.Filesystem {
	if f == nil || f.CapacityBytes == nil {
		return domain.Filesystem{}
	}

	filesystem := domain.Filesystem{CapacityBytes: *f.CapacityBytes}
	switch {
	case f.UsedBytes != nil:
		filesystem.UsedBytes = *f.UsedBytes
	case f.AvailableBytes != nil:
		filesystem.UsedBytes = *f.CapacityBytes - *f.AvailableBytes
	}
	return filesystem
}

// filesystemCache holds one sweep's result per cluster, and the sweep in
// flight for each.
//
// THE CACHE ALONE DID NOT STOP A SECOND SWEEP. It is written when a sweep
// FINISHES, and a sweep is a kubelet request per node — seconds on a large
// cluster. Every assessment that arrived meanwhile (the overview, the
// navigator's background assessment, a second tab on the same cluster, the
// history sampler) found the cache empty and started its own, so a
// two-hundred-node cluster could be asked for two hundred summaries three or
// four times over in the same few seconds. `inflight` is the singleflight in
// front of it: the first caller leads the sweep, everyone after it waits for
// that one.
//
// Deliberately NOT routed through readcache.go's cachedRead: that cache's
// window is shorter than a tick and it never reuses a failure, where this
// one holds an answer for a minute and holds a refusal just as firmly.
type filesystemCache struct {
	mu       sync.Mutex
	entries  map[domain.ClusterID]filesystemEntry
	inflight map[domain.ClusterID]*sweepCall
}

// sweepOutcome is what one sweep came back with, and whether it is worth
// holding for filesystemTTL.
//
// A REFUSAL IS HELD AS FIRMLY AS A SUCCESS. Overwhelmingly the cause is a
// role without nodes/proxy, which will still be true a second from now — and
// without this the next assessment fans out to every node again, all of them
// doomed. On a hundred-node cluster that is a hundred pointless requests per
// assessment, and the overview runs more than once a minute.
//
// A transient failure is not held — the cluster's client could not be built,
// the node list failed: it is handed to the callers who waited for it and
// then forgotten, so the next assessment tries again.
type sweepOutcome struct {
	result   map[string]domain.NodeFilesystems
	err      error
	remember bool
	// entry is what to store when remember is set: the answer, per node, and
	// how long it stands.
	entry filesystemEntry
}

// sweepCall is one sweep in flight, and what it came back with once done is
// closed.
type sweepCall struct {
	done   chan struct{}
	result map[string]domain.NodeFilesystems
	err    error
}

// do answers from the cache, or joins the sweep already running, or leads a
// new one.
//
// THE LEADER'S SWEEP IS DETACHED FROM ITS CALLER, for the reason readcache.go
// gives at detach: the first caller is not the only caller, and its
// cancellation must not become everyone else's. The deadline is kept. Every
// caller — the leader included — waits on its OWN context, so one that gives
// up leaves at once and a wedged kubelet cannot pin anybody.
func (c *filesystemCache) do(
	ctx context.Context,
	id domain.ClusterID,
	sweep func(context.Context, filesystemEntry) sweepOutcome,
) (map[string]domain.NodeFilesystems, error) {
	entry, cached, call, leader := c.claim(id)
	if cached {
		if entry.refused != nil {
			return nil, entry.refused
		}
		return entry.result, nil
	}
	if leader {
		sweepCtx, release := detach(ctx)
		go func() {
			defer release()
			// The previous entry, stale or not: a rolling sweep picks its
			// batch by how old each node's answer in it is.
			c.finish(id, call, sweep(sweepCtx, entry))
		}()
	}

	if err := wait(ctx, call.done); err != nil {
		return nil, err
	}
	return call.result, call.err
}

// claim decides, under ONE lock, what a caller does: answer from a fresh
// cache entry, follow the sweep in flight, or lead a new one.
//
// One decision rather than a cache read followed by a claim, because between
// two separate locks a sweep can finish: the caller misses the cache, then
// finds nothing in flight, and leads a second sweep a moment after the first
// one's answer was stored.
func (c *filesystemCache) claim(id domain.ClusterID) (entry filesystemEntry, cached bool, call *sweepCall, leader bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[id]
	if ok && time.Since(entry.at) <= entry.lifetime() {
		return entry, true, nil, false
	}
	if call, ok := c.inflight[id]; ok {
		return filesystemEntry{}, false, call, false
	}
	if c.inflight == nil {
		c.inflight = make(map[domain.ClusterID]*sweepCall, 2)
	}
	call = &sweepCall{done: make(chan struct{})}
	c.inflight[id] = call
	return entry, false, call, true
}

// finish stores a sweep's answer when it is worth keeping, and publishes it
// to everyone waiting on it.
//
// STORED ONLY IF THIS SWEEP IS STILL THE CLUSTER'S. A disconnect (forget)
// lets go of the sweep in flight; one that finishes afterwards was read over
// the connection that was dropped, and writing it into the cache would hand
// the reconnected cluster — possibly a different cluster behind the same
// context name — the old one's disks for a minute.
func (c *filesystemCache) finish(id domain.ClusterID, call *sweepCall, outcome sweepOutcome) {
	c.mu.Lock()
	if c.inflight[id] == call {
		delete(c.inflight, id)
		if outcome.remember {
			c.storeLocked(id, outcome.entry)
		}
	}
	c.mu.Unlock()

	call.result, call.err = outcome.result, outcome.err
	close(call.done)
}

type filesystemEntry struct {
	at     time.Time
	result map[string]domain.NodeFilesystems
	// refused is set when the whole sweep was turned away, and is the reason
	// to serve back. See the note on caching refusals in NodeFilesystems.
	refused error

	// answeredAt is when each node in result last answered — what the
	// coverage's age reads, and what decays a carried answer.
	answeredAt map[string]time.Time
	// askedAt is when each node was last ASKED, answer or not — what a
	// rolling sweep picks its next batch by. Separate from answeredAt, or a
	// node that never answers is never "asked" and is picked again on every
	// batch: with a batch's worth of dead kubelets, every batch was the dead
	// ones and no healthy node was ever refreshed again.
	askedAt map[string]time.Time
	// nodes is how many nodes the cluster had at the sweep.
	nodes int
	// ttl is how long this entry stands: filesystemTTL, or
	// filesystemBatchSpacing for a rolling cluster.
	ttl time.Duration
}

// lifetime is how long the entry is reused; a zero ttl is the minute every
// entry had before sweeps could roll.
func (e filesystemEntry) lifetime() time.Duration {
	if e.ttl > 0 {
		return e.ttl
	}
	return filesystemTTL
}

// rolling reports whether the cluster is too large to sweep whole.
func (e filesystemEntry) rolling() bool { return e.nodes > filesystemSweepCap }

// coverage says how much of the cluster an entry's answer covers.
func (e filesystemEntry) coverage(now time.Time) domain.DiskCoverage {
	coverage := domain.DiskCoverage{Asked: e.nodes, Answered: len(e.result), Rolling: e.rolling()}
	for _, at := range e.answeredAt {
		if age := int64(now.Sub(at).Seconds()); age > coverage.OldestSeconds {
			coverage.OldestSeconds = age
		}
	}
	return coverage
}

// batchFor picks the nodes a sweep asks: every node up to filesystemSweepCap,
// and above it the filesystemBatch asked longest ago — never-asked first,
// then by when, then by name so the order is stable. By when they were
// ASKED, not answered: see askedAt.
func batchFor(nodes []string, previous filesystemEntry) []string {
	if len(nodes) <= filesystemSweepCap {
		return nodes
	}
	batch := slices.Clone(nodes)
	slices.SortStableFunc(batch, func(a, b string) int {
		atA, okA := previous.askedAt[a]
		atB, okB := previous.askedAt[b]
		switch {
		case !okA && !okB:
			return strings.Compare(a, b)
		case !okA:
			return -1
		case !okB:
			return 1
		}
		if order := atA.Compare(atB); order != 0 {
			return order
		}
		return strings.Compare(a, b)
	})
	return batch[:filesystemBatch]
}

// carriedAnswerMaxAge is how old a rolling cluster's carried answer may be:
// four full rotations through its nodes, and never under five minutes. A
// node asked on every rotation and never answering since is not "the disk
// was fine a while ago" any more — past this its last answer is dropped and
// the figure decays towards unknown rather than presenting it as current.
func carriedAnswerMaxAge(nodes int) time.Duration {
	rotations := (nodes + filesystemBatch - 1) / filesystemBatch
	return max(4*time.Duration(rotations)*filesystemBatchSpacing, 5*time.Minute)
}

// mergeSweep folds one sweep's answers into what the previous one knew.
//
// A cluster swept whole is answered by THIS sweep alone, as it always was: a
// node that stopped answering drops out. A rolling cluster keeps every node's
// last answer until that node is asked again — the batch asked now is only a
// slice of it — drops nodes the cluster no longer has, and drops answers
// older than carriedAnswerMaxAge. Every node in batch is stamped asked,
// whether or not it answered.
func mergeSweep(previous filesystemEntry, nodes, batch []string, answers map[string]domain.NodeFilesystems, now time.Time) filesystemEntry {
	entry := filesystemEntry{
		at:         now,
		nodes:      len(nodes),
		result:     make(map[string]domain.NodeFilesystems, len(nodes)),
		answeredAt: make(map[string]time.Time, len(nodes)),
		askedAt:    make(map[string]time.Time, len(nodes)),
		ttl:        filesystemTTL,
	}
	if len(nodes) > filesystemSweepCap {
		entry.ttl = filesystemBatchSpacing
		maxAge := carriedAnswerMaxAge(len(nodes))
		present := make(map[string]bool, len(nodes))
		for _, name := range nodes {
			present[name] = true
		}
		for name, at := range previous.askedAt {
			if present[name] {
				entry.askedAt[name] = at
			}
		}
		for name, filesystems := range previous.result {
			answered := previous.answeredAt[name]
			if present[name] && now.Sub(answered) <= maxAge {
				entry.result[name] = filesystems
				entry.answeredAt[name] = answered
			}
		}
	}
	for _, name := range batch {
		entry.askedAt[name] = now
	}
	for name, filesystems := range answers {
		entry.result[name] = filesystems
		entry.answeredAt[name] = now
	}
	return entry
}

// sweepRefused decides whether a sweep is a refusal: nothing answered, and
// either nothing is left to show or every node asked was DENIED. See
// sweepFilesystems.
func sweepRefused(answered, kept, asked, denied int, failed bool) bool {
	if answered > 0 || !failed {
		return false
	}
	return kept == 0 || (asked > 0 && denied == asked)
}

// FilesystemCoverage says how much of a cluster the last disk figures cover:
// how many nodes it has, how many answered, how old the oldest answer is,
// and whether the sweep is rolling. False before any sweep has answered.
func (a *Adapter) FilesystemCoverage(id domain.ClusterID) (domain.DiskCoverage, bool) {
	a.filesystems.mu.Lock()
	defer a.filesystems.mu.Unlock()

	entry, ok := a.filesystems.entries[id]
	if !ok || entry.refused != nil {
		return domain.DiskCoverage{}, false
	}
	return entry.coverage(time.Now()), true
}

// NodeFilesystems returns disk occupancy keyed by node name.
//
// A partial answer is a success: one kubelet behind a broken network path must
// not cost the other forty-nine. Only a sweep in which nothing at all answered
// is an error, and it is reported as ErrMetricsUnavailable because by far the
// most common cause is a role without nodes/proxy.
func (a *Adapter) NodeFilesystems(ctx context.Context, id domain.ClusterID) (map[string]domain.NodeFilesystems, error) {
	return a.filesystems.do(ctx, id, func(ctx context.Context, previous filesystemEntry) sweepOutcome {
		return a.sweepFilesystems(ctx, id, previous)
	})
}

// sweepFilesystems asks every kubelet once.
//
// Only ever run as the leader of a sweep — see filesystemCache.do — so the
// whole fan-out happens once per cluster however many callers wanted it. It
// says whether its answer is worth keeping rather than storing it itself, so
// a sweep overtaken by a disconnect can be told not to.
func (a *Adapter) sweepFilesystems(ctx context.Context, id domain.ClusterID, previous filesystemEntry) sweepOutcome {
	op := fmt.Sprintf("reading node filesystems of %q", id)

	set, err := a.factory.clientsFor(id)
	if err != nil {
		return sweepOutcome{err: err}
	}

	nodes, err := a.nodeNames(ctx, id, set)
	if err != nil {
		return sweepOutcome{err: classify(op, err)}
	}
	batch := batchFor(nodes, previous)

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		result  = make(map[string]domain.NodeFilesystems, len(batch))
		refused error
		// denied counts the nodes the API server REFUSED (403/401), as
		// opposed to a kubelet that did not answer.
		denied int
		gate   = make(chan struct{}, filesystemConcurrency)
	)

	for i := range batch {
		name := batch[i]

		wg.Go(func() {
			gate <- struct{}{}
			defer func() { <-gate }()

			filesystems, err := a.nodeSummary(ctx, set, name)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				// Kept so a sweep that produced nothing can say WHY, rather
				// than reporting an empty map as a cluster with no disks.
				if refused == nil {
					refused = err
				}
				if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
					denied++
				}
				return
			}
			result[name] = filesystems
		})
	}
	wg.Wait()

	entry := mergeSweep(previous, nodes, batch, result, time.Now())

	// A REFUSAL, whatever was carried. Nothing answered and either there is
	// nothing to show, or every node asked was DENIED rather than merely
	// silent — nodes/proxy revoked. A rolling cluster used to re-store its
	// carried answers stamped as new on every refused batch, so it could
	// never become refused once one node had answered. Silent kubelets are
	// not a refusal: the other batches still answer, and their carried
	// answers decay (carriedAnswerMaxAge).
	if sweepRefused(len(result), len(entry.result), len(batch), denied, refused != nil) {
		// Remembered for the same minute a success would be, so a cluster
		// that will not answer is asked once a minute rather than on every
		// assessment. See sweepOutcome. Where it was asked is kept, so the
		// rotation resumes rather than restarting at the same batch.
		err := fmt.Errorf("%s: %w: %w", op, ports.ErrMetricsUnavailable, refused)
		return sweepOutcome{err: err, remember: true, entry: filesystemEntry{
			at: entry.at, refused: err, nodes: len(nodes), askedAt: entry.askedAt, ttl: filesystemTTL,
		}}
	}

	// An empty cluster lands here too, which is not a failure: caching it
	// keeps an idle cluster from sweeping every minute.
	return sweepOutcome{result: entry.result, remember: true, entry: entry}
}

// nodeNames lists the nodes to sweep, reusing the overview's list when it is
// still fresh.
//
// The sweep used to LIST nodes itself, moments after the assessment that
// triggered it had listed the very same nodes — a second full node LIST per
// assessment, for a set that changes on the timescale of somebody adding a
// machine. The cache is written by the node read that precedes it, and is
// held to the same one-minute window as the sweep itself, so a node joining
// is picked up on the next minute rather than the next assessment.
func (a *Adapter) nodeNames(ctx context.Context, id domain.ClusterID, set *clients) ([]string, error) {
	if names, ok := a.nodeList.get(id); ok {
		return names, nil
	}

	list, err := set.typed.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		// Served from the API server's watch cache rather than a quorum read
		// from etcd. A sweep of disk usage does not need consensus on the
		// exact membership of the node set.
		ResourceVersion: "0",
	})
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(list.Items))
	for i := range list.Items {
		names = append(names, list.Items[i].Name)
	}
	a.nodeList.put(id, names)
	return names, nil
}

// nodeNameCache holds the node names of each cluster for the sweep's window.
type nodeNameCache struct {
	mu      sync.Mutex
	entries map[domain.ClusterID]nodeNameEntry
}

type nodeNameEntry struct {
	at    time.Time
	names []string
}

func (c *nodeNameCache) get(id domain.ClusterID) ([]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[id]
	if !ok || time.Since(entry.at) > filesystemTTL {
		return nil, false
	}
	return entry.names, true
}

func (c *nodeNameCache) put(id domain.ClusterID, names []string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.entries == nil {
		c.entries = make(map[domain.ClusterID]nodeNameEntry, 2)
	}
	c.entries[id] = nodeNameEntry{at: time.Now(), names: names}
}

func (c *nodeNameCache) forget(id domain.ClusterID) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, id)
}

// nodeSummary reads and decodes one kubelet's statistics.
func (a *Adapter) nodeSummary(
	ctx context.Context,
	set *clients,
	name string,
) (domain.NodeFilesystems, error) {
	ctx, cancel := context.WithTimeout(ctx, kubeletTimeout)
	defer cancel()

	raw, err := set.typed.CoreV1().RESTClient().Get().
		Resource("nodes").
		Name(name).
		SubResource("proxy").
		Suffix("stats", "summary").
		DoRaw(ctx)
	if err != nil {
		return domain.NodeFilesystems{}, err
	}

	var summary summaryResponse
	if err := json.Unmarshal(raw, &summary); err != nil {
		return domain.NodeFilesystems{}, fmt.Errorf("decoding the summary of node %q: %w", name, err)
	}

	nodefs := summary.Node.Fs.toFilesystem()
	var imagefs domain.Filesystem
	if summary.Node.Runtime != nil {
		imagefs = summary.Node.Runtime.ImageFs.toFilesystem()
	}

	// A kubelet that answered but reported no sizes has told us nothing, and
	// saying "0% full" on its behalf would be worse than saying nothing.
	if nodefs.CapacityBytes == 0 && imagefs.CapacityBytes == 0 {
		return domain.NodeFilesystems{}, fmt.Errorf("node %q reported no filesystem sizes", name)
	}

	// Pressure is optional in a way the filesystems are not: a kubelet older
	// than 1.36, or a cgroup v1 host, reports none of it and is not faulty.
	// Any one dimension being present is enough to call it measured.
	var pressure domain.Pressure
	cpu, hasCPU := summary.Node.CPU.stall()
	memory, hasMemory := summary.Node.Memory.stall()
	io, hasIO := summary.Node.IO.stall()
	if hasCPU || hasMemory || hasIO {
		pressure = domain.Pressure{CPU: cpu, Memory: memory, IO: io, Measured: true}
	}

	return domain.NodeFilesystems{
		Pressure: pressure,
		Nodefs:   nodefs,
		Imagefs:  imagefs,
		Measured: true,
	}, nil
}

// storeLocked records a sweep's answer. The caller holds mu.
func (c *filesystemCache) storeLocked(id domain.ClusterID, entry filesystemEntry) {
	if c.entries == nil {
		c.entries = make(map[domain.ClusterID]filesystemEntry, 2)
	}
	c.entries[id] = entry
}

// forget drops a cluster's cached sweep, for when it is disconnected.
//
// The sweep in flight is let go too, so the first read of a reconnected
// cluster leads a sweep of its own rather than joining one started against
// the connection that has just been dropped. Callers already waiting on that
// one still get its answer; nobody new does.
func (c *filesystemCache) forget(id domain.ClusterID) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, id)
	delete(c.inflight, id)
}
