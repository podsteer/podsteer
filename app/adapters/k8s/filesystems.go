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
	"sync"
	"time"

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

// filesystemCache holds one sweep's result per cluster.
type filesystemCache struct {
	mu      sync.Mutex
	entries map[domain.ClusterID]filesystemEntry
}

type filesystemEntry struct {
	at     time.Time
	result map[string]domain.NodeFilesystems
	// refused is set when the whole sweep was turned away, and is the reason
	// to serve back. See the note on caching refusals in NodeFilesystems.
	refused error
}

// NodeFilesystems returns disk occupancy keyed by node name.
//
// A partial answer is a success: one kubelet behind a broken network path must
// not cost the other forty-nine. Only a sweep in which nothing at all answered
// is an error, and it is reported as ErrMetricsUnavailable because by far the
// most common cause is a role without nodes/proxy.
func (a *Adapter) NodeFilesystems(ctx context.Context, id domain.ClusterID) (map[string]domain.NodeFilesystems, error) {
	op := fmt.Sprintf("reading node filesystems of %q", id)

	if cached, refused, ok := a.filesystems.get(id); ok {
		if refused != nil {
			return nil, refused
		}
		return cached, nil
	}

	set, err := a.factory.clientsFor(id)
	if err != nil {
		return nil, err
	}

	nodes, err := a.nodeNames(ctx, id, set)
	if err != nil {
		return nil, classify(op, err)
	}

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		result  = make(map[string]domain.NodeFilesystems, len(nodes))
		refused error
		gate    = make(chan struct{}, filesystemConcurrency)
	)

	for i := range nodes {
		name := nodes[i]

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
				return
			}
			result[name] = filesystems
		})
	}
	wg.Wait()

	if len(result) == 0 {
		if refused == nil {
			// No nodes to ask. An empty cluster is not a failure, and caching
			// it keeps an idle cluster from sweeping every minute.
			a.filesystems.put(id, result)
			return result, nil
		}
		// Remembered for the same minute a success would be, so a cluster
		// that will not answer is asked once a minute rather than on every
		// assessment. See putRefusal.
		err := fmt.Errorf("%s: %w: %w", op, ports.ErrMetricsUnavailable, refused)
		a.filesystems.putRefusal(id, err)
		return nil, err
	}

	a.filesystems.put(id, result)
	return result, nil
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

// get returns a cached sweep, or a cached refusal, while it is still fresh.
func (c *filesystemCache) get(id domain.ClusterID) (map[string]domain.NodeFilesystems, error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[id]
	if !ok || time.Since(entry.at) > filesystemTTL {
		return nil, nil, false
	}
	return entry.result, entry.refused, true
}

// put stores a sweep.
func (c *filesystemCache) put(id domain.ClusterID, result map[string]domain.NodeFilesystems) {
	c.store(id, filesystemEntry{at: time.Now(), result: result})
}

// putRefusal remembers that nothing answered, and why.
//
// A refusal has to be cached as firmly as a success. Overwhelmingly the cause
// is a role without nodes/proxy, which will still be true a second from now —
// and without this the next assessment fans out to every node again, all of
// them doomed. On a hundred-node cluster that is a hundred pointless requests
// per assessment, and the overview runs more than once a minute.
func (c *filesystemCache) putRefusal(id domain.ClusterID, refused error) {
	c.store(id, filesystemEntry{at: time.Now(), refused: refused})
}

func (c *filesystemCache) store(id domain.ClusterID, entry filesystemEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.entries == nil {
		c.entries = make(map[domain.ClusterID]filesystemEntry, 2)
	}
	c.entries[id] = entry
}

// forget drops a cluster's cached sweep, for when it is disconnected.
func (c *filesystemCache) forget(id domain.ClusterID) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, id)
}
