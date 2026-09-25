package application

import (
	"fmt"
	"slices"
	"sync"

	"github.com/podsteer/podsteer/app/domain"
)

// Registry tracks which clusters PodSteer currently has open.
//
// This replaces the single "active cluster" the application used to hold. A
// desktop client showing one tab per cluster is genuinely multi-cluster: the
// operator flips between production and staging mid-incident, and each tab has
// to keep its own connection alive. A single active cluster would make every
// tab switch a reconnect.
//
// Connection order is preserved so the tab bar does not reshuffle itself when
// the list is re-read — tabs that move under the cursor are the fastest way to
// make somebody act on the wrong cluster.
//
// It is safe for concurrent use.
type Registry struct {
	mu        sync.RWMutex
	order     []domain.ClusterID
	connected map[domain.ClusterID]domain.Cluster

	// readOnly holds the clusters currently marked read-only. Absence means
	// false, the same "stored as the exception" shape the frontend's own
	// organisation store uses — a registry that only ever opens a handful of
	// its clusters this way should not grow an entry for every one that never
	// was.
	readOnly map[domain.ClusterID]bool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		connected: make(map[domain.ClusterID]domain.Cluster),
		readOnly:  make(map[domain.ClusterID]bool),
	}
}

// Open records a cluster as connected, or refreshes one already open.
//
// Reconnecting keeps the cluster's position in the tab order: an operator who
// reconnects a cluster whose token expired expects the tab to stay where it is.
func (r *Registry) Open(cluster domain.Cluster) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.connected[cluster.ID()]; !exists {
		r.order = append(r.order, cluster.ID())
	}
	r.connected[cluster.ID()] = cluster
}

// Close drops a connection, reporting whether it was open.
func (r *Registry) Close(id domain.ClusterID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.connected[id]; !exists {
		return false
	}

	delete(r.connected, id)
	// The read-only mark is cleared with the connection, not carried across
	// it: a reconnect starts from "not read-only" and waits for the frontend
	// to tell it otherwise, which it does right after Connect succeeds. A
	// flag that survived a disconnect could otherwise outlive the group
	// setting it came from, and a cluster reopened after the operator
	// switched it off would still refuse every write.
	delete(r.readOnly, id)
	r.order = slices.DeleteFunc(r.order, func(candidate domain.ClusterID) bool {
		return candidate == id
	})
	return true
}

// SetReadOnly marks id read-only, or lifts the mark.
//
// This is the write side of a client-set policy — see ErrReadOnly for why
// checking it is a guard, not a permission. It is recorded independently of
// whether id is currently connected, so a call racing a Disconnect cannot
// leave the registry in a state neither caller intended; Close clears it
// regardless.
func (r *Registry) SetReadOnly(id domain.ClusterID, readOnly bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if readOnly {
		r.readOnly[id] = true
	} else {
		delete(r.readOnly, id)
	}
}

// ReadOnly reports whether id is currently marked read-only. A cluster never
// marked, or marked and since disconnected, reports false.
func (r *Registry) ReadOnly(id domain.ClusterID) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.readOnly[id]
}

// Get returns a connected cluster.
//
// It fails with an error wrapping domain.ErrClusterNotConnected when the
// cluster is not open — which is what the frontend gets if it asks for a tab
// that has since been closed, a normal race rather than a bug.
func (r *Registry) Get(id domain.ClusterID) (domain.Cluster, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cluster, exists := r.connected[id]
	if !exists {
		return domain.Cluster{}, fmt.Errorf("cluster %q: %w", id, domain.ErrClusterNotConnected)
	}
	return cluster, nil
}

// IsOpen reports whether a cluster is connected.
func (r *Registry) IsOpen(id domain.ClusterID) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.connected[id]
	return exists
}

// All returns the connected clusters in connection order.
func (r *Registry) All() []domain.Cluster {
	r.mu.RLock()
	defer r.mu.RUnlock()

	clusters := make([]domain.Cluster, 0, len(r.order))
	for _, id := range r.order {
		if cluster, exists := r.connected[id]; exists {
			clusters = append(clusters, cluster)
		}
	}
	return clusters
}

// Len returns how many clusters are connected.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.connected)
}
