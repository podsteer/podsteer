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
	} `json:"node"`
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
}

// NodeFilesystems returns disk occupancy keyed by node name.
//
// A partial answer is a success: one kubelet behind a broken network path must
// not cost the other forty-nine. Only a sweep in which nothing at all answered
// is an error, and it is reported as ErrMetricsUnavailable because by far the
// most common cause is a role without nodes/proxy.
func (a *Adapter) NodeFilesystems(ctx context.Context, id domain.ClusterID) (map[string]domain.NodeFilesystems, error) {
	op := fmt.Sprintf("reading node filesystems of %q", id)

	if cached, ok := a.filesystems.get(id); ok {
		return cached, nil
	}

	set, err := a.factory.clientsFor(id)
	if err != nil {
		return nil, err
	}

	nodes, err := set.typed.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, classify(op, err)
	}

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		result  = make(map[string]domain.NodeFilesystems, len(nodes.Items))
		refused error
		gate    = make(chan struct{}, filesystemConcurrency)
	)

	for i := range nodes.Items {
		name := nodes.Items[i].Name
		wg.Add(1)

		go func() {
			defer wg.Done()
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
		}()
	}
	wg.Wait()

	if len(result) == 0 {
		if refused == nil {
			// No nodes to ask. An empty cluster is not a failure, and caching
			// it keeps an idle cluster from sweeping every minute.
			a.filesystems.put(id, result)
			return result, nil
		}
		return nil, fmt.Errorf("%s: %w: %w", op, ports.ErrMetricsUnavailable, refused)
	}

	a.filesystems.put(id, result)
	return result, nil
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

	return domain.NodeFilesystems{Nodefs: nodefs, Imagefs: imagefs, Measured: true}, nil
}

// get returns a cached sweep while it is still fresh.
func (c *filesystemCache) get(id domain.ClusterID) (map[string]domain.NodeFilesystems, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[id]
	if !ok || time.Since(entry.at) > filesystemTTL {
		return nil, false
	}
	return entry.result, true
}

// put stores a sweep.
func (c *filesystemCache) put(id domain.ClusterID, result map[string]domain.NodeFilesystems) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.entries == nil {
		c.entries = make(map[domain.ClusterID]filesystemEntry, 2)
	}
	c.entries[id] = filesystemEntry{at: time.Now(), result: result}
}

// forget drops a cluster's cached sweep, for when it is disconnected.
func (c *filesystemCache) forget(id domain.ClusterID) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, id)
}
