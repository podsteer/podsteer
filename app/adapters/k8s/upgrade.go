package k8s

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// This file answers the two questions the upgrade-impact findings need, and
// answers them the way CLAUDE.md's deprecation-table section requires:
// served group/versions come from discovery, never the catalogue, and usage
// is read from who last WROTE an object rather than how many objects exist —
// an object does not use an API version, a writer does, and Kubernetes
// stores one copy of an object and serves it through every version the API
// server offers, so a count under a deprecated version equals the count
// under its replacement.

// upgradeWriterPageSize bounds one page of the metadata list APIWriters
// issues, mirroring the table-print path's own paging rather than asking for
// the whole resource in one response.
const upgradeWriterPageSize = 500

// autoupdateSpecAnnotation is what the API server itself sets, to "true", on
// the FlowSchemas and PriorityLevelConfigurations it bootstraps and keeps
// current — flowcontrol's own suggested defaults, not anything an operator
// or a Helm chart wrote. It matters here because the OLD producer's
// managedFields entry survives an upgrade on these objects even after the
// running producer has already moved to writing through the current
// version, so a writer found on one of them is a stale field, not evidence
// of anyone still using the deprecated one.
const autoupdateSpecAnnotation = "apf.kubernetes.io/autoupdate-spec"

// ServedAPIs returns every group/version discovery reports the cluster
// currently serves.
func (a *Adapter) ServedAPIs(ctx context.Context, id domain.ClusterID) ([]domain.APIGroupVersion, error) {
	if entry, ok := a.upgrades.getServed(id); ok {
		return entry.versions, entry.err
	}

	set, err := a.factory.clientsFor(id)
	if err != nil {
		return nil, err
	}

	groups, err := set.discovery.ServerGroups()
	if err != nil {
		wrapped := classify(fmt.Sprintf("discovering served API versions of %q", id), err)

		// A REFUSAL IS CACHED, a transport failure is not — the same rule
		// DiscoverMetricsBackend follows and for the same reason: an
		// account that may not list discovery will never be able to, and
		// retrying on every poll writes a denied request into somebody's
		// audit log forever. A cluster that was merely unreachable comes
		// back, and should be asked again when it does.
		if errors.Is(wrapped, ports.ErrForbidden) || errors.Is(wrapped, ports.ErrUnauthenticated) {
			a.upgrades.putServed(id, nil, wrapped)
			return nil, wrapped
		}
		return nil, wrapped
	}

	versions := make([]domain.APIGroupVersion, 0, len(groups.Groups)*2)
	for _, group := range groups.Groups {
		for _, entry := range group.Versions {
			gv := entry.GroupVersion
			apiGroup, apiVersion, found := strings.Cut(gv, "/")
			if !found {
				// The core group's discovery entries carry no slash at
				// all — "v1", never "core/v1" or "/v1".
				versions = append(versions, domain.APIGroupVersion{Version: gv})
				continue
			}
			versions = append(versions, domain.APIGroupVersion{Group: apiGroup, Version: apiVersion})
		}
	}

	a.upgrades.putServed(id, versions, nil)
	return versions, nil
}

// APIWriters scans up to limit objects of kind and reports who last wrote
// each one through kind's own API version, per its
// metadata.managedFields — never a count of objects, which cannot
// distinguish a deprecated version from its replacement.
func (a *Adapter) APIWriters(
	ctx context.Context,
	id domain.ClusterID,
	kind domain.ResourceKind,
	limit int,
) (domain.APIUsage, error) {
	if entry, ok := a.upgrades.getWriters(id, kind); ok {
		return entry.usage, entry.err
	}

	set, err := a.factory.clientsFor(id)
	if err != nil {
		return domain.APIUsage{}, err
	}

	gvr := schema.GroupVersionResource{Group: kind.Group, Version: kind.Version, Resource: kind.Resource}
	// No .Namespace() call: this lists every namespace at once for a
	// namespaced kind, which is what a cluster-wide writer scan needs, and
	// works unchanged for a cluster-scoped kind, which has no namespace to
	// narrow to in the first place.
	client := set.meta.Resource(gvr)

	var (
		usage         domain.APIUsage
		seen          = make(map[[3]string]bool)
		continueToken string
	)

	for usage.Scanned < limit {
		remaining := int64(limit - usage.Scanned)
		pageSize := remaining
		if pageSize > upgradeWriterPageSize {
			pageSize = upgradeWriterPageSize
		}

		list, err := client.List(ctx, metav1.ListOptions{Limit: pageSize, Continue: continueToken})
		if err != nil {
			op := fmt.Sprintf("listing %s to check API writers in %q", kind.Resource, id)
			wrapped := classify(op, err)
			if errors.Is(wrapped, ports.ErrForbidden) || errors.Is(wrapped, ports.ErrUnauthenticated) {
				a.upgrades.putWriters(id, kind, domain.APIUsage{}, wrapped)
			}
			return domain.APIUsage{}, wrapped
		}

		items := list.Items
		// A page holding more items than the remaining budget is what makes
		// truncation observable against a fake client that ignores Limit
		// and Continue and returns everything in one page: a real server
		// would instead leave a non-empty Continue token, handled below.
		if int64(len(items)) > pageSize {
			usage.Truncated = true
			items = items[:pageSize]
		}

		for _, item := range items {
			usage.Scanned++
			for _, field := range item.ManagedFields {
				if field.APIVersion != kind.GroupVersion() {
					continue
				}
				key := [3]string{field.Manager, item.Namespace, item.Name}
				if seen[key] {
					continue
				}
				seen[key] = true
				usage.Writers = append(usage.Writers, domain.APIWriter{
					Manager:     field.Manager,
					Namespace:   domain.NamespaceName(item.Namespace),
					Name:        item.Name,
					SelfManaged: item.Annotations[autoupdateSpecAnnotation] == "true",
				})
			}
		}

		continueToken = list.Continue
		if continueToken == "" {
			break
		}
	}
	if continueToken != "" {
		usage.Truncated = true
	}

	a.upgrades.putWriters(id, kind, usage, nil)
	return usage, nil
}

// upgradeCache holds discovery's served group/versions and the writer scans
// found for them, per cluster.
//
// upgradeCacheTTL is five minutes: served versions change only on a
// control-plane upgrade, and writers change only when somebody applies
// something, so a 5-second poll re-listing FlowSchemas every tick would pay
// for an answer that has not moved since the last one.
type upgradeCache struct {
	mu      sync.Mutex
	served  map[domain.ClusterID]servedEntry
	writers map[string]writersEntry
}

const upgradeCacheTTL = 5 * time.Minute

type servedEntry struct {
	at       time.Time
	versions []domain.APIGroupVersion
	err      error
}

type writersEntry struct {
	at    time.Time
	usage domain.APIUsage
	err   error
}

// writersKey combines the cluster and the kind, since writers are cached per
// deprecated group/version rather than once per cluster.
func writersKey(id domain.ClusterID, kind domain.ResourceKind) string {
	return id.String() + "|" + kind.ID()
}

func (c *upgradeCache) getServed(id domain.ClusterID) (servedEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.served[id]
	if !ok || time.Since(entry.at) > upgradeCacheTTL {
		return servedEntry{}, false
	}
	return entry, true
}

func (c *upgradeCache) putServed(id domain.ClusterID, versions []domain.APIGroupVersion, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.served == nil {
		c.served = make(map[domain.ClusterID]servedEntry)
	}
	c.served[id] = servedEntry{at: time.Now(), versions: versions, err: err}
}

func (c *upgradeCache) getWriters(id domain.ClusterID, kind domain.ResourceKind) (writersEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.writers[writersKey(id, kind)]
	if !ok || time.Since(entry.at) > upgradeCacheTTL {
		return writersEntry{}, false
	}
	return entry, true
}

func (c *upgradeCache) putWriters(id domain.ClusterID, kind domain.ResourceKind, usage domain.APIUsage, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.writers == nil {
		c.writers = make(map[string]writersEntry)
	}
	c.writers[writersKey(id, kind)] = writersEntry{at: time.Now(), usage: usage, err: err}
}

// forget drops both maps' entries for id, on disconnect — see
// Adapter.Invalidate.
func (c *upgradeCache) forget(id domain.ClusterID) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.served, id)
	prefix := id.String() + "|"
	for key := range c.writers {
		if strings.HasPrefix(key, prefix) {
			delete(c.writers, key)
		}
	}
}
