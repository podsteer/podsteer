package application

import (
	"fmt"
	"sync"

	"k8sense/app/domain"
)

// Session holds the cluster the operator is currently working in.
//
// This is the one piece of mutable state K8Sense keeps between calls, and it
// exists because a desktop client is modal in a way a server is not: the
// operator picks a cluster once and then issues dozens of queries against it.
// Making every call carry the cluster id would push that selection into the
// frontend and let the two drift apart.
//
// Session lives in the application layer rather than the domain because "which
// cluster is on screen" is a property of this running program, not of the
// modelled world.
//
// It is safe for concurrent use.
type Session struct {
	mu     sync.RWMutex
	active domain.Cluster
}

// NewSession returns an empty session with no active cluster.
func NewSession() *Session {
	return &Session{}
}

// Activate makes cluster the active one, replacing any previous selection.
func (s *Session) Activate(cluster domain.Cluster) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = cluster
}

// Active returns the active cluster.
//
// It fails with an error wrapping domain.ErrNoActiveCluster when no cluster
// has been connected, which is the normal state at startup rather than a bug.
func (s *Session) Active() (domain.Cluster, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.active.IsZero() {
		return domain.Cluster{}, fmt.Errorf("session: %w", domain.ErrNoActiveCluster)
	}
	return s.active, nil
}

// HasActive reports whether a cluster is currently selected.
func (s *Session) HasActive() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return !s.active.IsZero()
}

// Clear drops the active cluster selection.
func (s *Session) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = domain.Cluster{}
}
