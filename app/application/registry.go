package application

import (
	"fmt"
	"slices"
	"sync"

	"podsteer/app/domain"
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
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{connected: make(map[domain.ClusterID]domain.Cluster)}
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
	r.order = slices.DeleteFunc(r.order, func(candidate domain.ClusterID) bool {
		return candidate == id
	})
	return true
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
