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

// DefaultChangeWindow is how long changes to one cluster are gathered before
// they are announced as one. A rollout is dozens of pod events in a second;
// the page needs to hear "it changed" once.
const DefaultChangeWindow = time.Second

// DefaultTopologyInterest is how long a drawn scope keeps hearing changes
// after it was last drawn. Long, because the badge is the point of a page
// left open; bounded, because a page nobody is looking at should not keep a
// cluster's changes flowing to the webview for ever.
const DefaultTopologyInterest = time.Hour

// TopologyServiceDeps are the collaborators of the topology use case.
type TopologyServiceDeps struct {
	// Topology reads the sources. Required.
	Topology ports.TopologyPort
	// Registry tracks open connections. Required.
	Registry *Registry
	// Window is the coalescing window; zero means DefaultChangeWindow.
	Window time.Duration
	// Interest is how long a drawn scope hears changes; zero means
	// DefaultTopologyInterest.
	Interest time.Duration
	// Logger receives diagnostics. Optional.
	Logger *slog.Logger
}

// TopologyService draws namespace topologies and announces their changes.
//
// It is also the adapter's ports.ChangeSink: the watch stores and every
// write call Changed, and the feed behind it turns that stream into one
// coalesced announcement per cluster per window — only for the clusters and
// namespaces somebody has drawn, and only while somebody is subscribed.
// NOTHING IS REDRAWN HERE: the announcement is "it changed", and the page
// decides whether to say so or to redraw.
type TopologyService struct {
	topology ports.TopologyPort
	registry *Registry
	feed     *ChangeFeed
	logger   *slog.Logger

	// last is each cluster's most recently drawn scope and its nodes, so the
	// traffic layer attaches its endpoints to the boxes actually on screen
	// rather than to a second read that may differ by a pod.
	mu   sync.Mutex
	last map[domain.ClusterID]drawnNodes
}

type drawnNodes struct {
	scope domain.TopologyScope
	nodes []domain.TopologyNode
}

// Compile-time proof of the ports this satisfies.
var (
	_ ports.TopologyService = (*TopologyService)(nil)
	_ ports.ChangeSink      = (*TopologyService)(nil)
)

// NewTopologyService builds the use case.
func NewTopologyService(deps TopologyServiceDeps) (*TopologyService, error) {
	switch {
	case deps.Topology == nil:
		return nil, errors.New("application: TopologyService requires a TopologyPort")
	case deps.Registry == nil:
		return nil, errors.New("application: TopologyService requires a Registry")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &TopologyService{
		topology: deps.Topology,
		registry: deps.Registry,
		feed:     NewChangeFeed(deps.Window, deps.Interest, deps.Registry.IsOpen),
		logger:   logger.With(slog.String("service", "topology")),
		last:     make(map[domain.ClusterID]drawnNodes),
	}, nil
}

// Topology draws one scope of an open cluster, and registers interest in it.
func (s *TopologyService) Topology(ctx context.Context, id domain.ClusterID, scope domain.TopologyScope) (domain.TopologyGraph, error) {
	if _, err := s.registry.Get(id); err != nil {
		return domain.TopologyGraph{}, fmt.Errorf("drawing a topology: %w", err)
	}
	if !scope.All && len(scope.Namespaces) == 0 {
		return domain.TopologyGraph{}, fmt.Errorf("drawing a topology: %w", domain.ErrEmptyTopologyScope)
	}

	graph, err := s.draw(ctx, id, scope)
	if err != nil {
		return domain.TopologyGraph{}, err
	}
	s.mu.Lock()
	s.last[id] = drawnNodes{scope: scope, nodes: graph.Nodes}
	s.mu.Unlock()
	s.feed.Watch(id, scope)
	return graph, nil
}

func (s *TopologyService) draw(ctx context.Context, id domain.ClusterID, scope domain.TopologyScope) (domain.TopologyGraph, error) {
	input, err := s.topology.TopologySources(ctx, id, scope)
	if err != nil {
		return domain.TopologyGraph{}, fmt.Errorf("reading the topology of %s: %w", id, err)
	}
	return domain.NewTopologyGraph(input), nil
}

// TopologyNodes returns the nodes of a scope: those of the graph last drawn
// for it when that is the same scope, otherwise a fresh read that registers
// no interest. For the traffic layer, which attaches observed endpoints to
// these boxes. The slice is shared; callers must not modify it.
func (s *TopologyService) TopologyNodes(ctx context.Context, id domain.ClusterID, namespaces []domain.NamespaceName, all bool) ([]domain.TopologyNode, error) {
	if _, err := s.registry.Get(id); err != nil {
		return nil, fmt.Errorf("reading topology nodes: %w", err)
	}
	raw := make([]string, 0, len(namespaces))
	for _, ns := range namespaces {
		raw = append(raw, ns.String())
	}
	scope, err := domain.NewTopologyScope(raw, all)
	if err != nil {
		return nil, fmt.Errorf("reading topology nodes: %w", err)
	}

	s.mu.Lock()
	last, found := s.last[id]
	s.mu.Unlock()
	if found && last.scope.All == scope.All && slices.Equal(last.scope.Namespaces, scope.Namespaces) {
		return last.nodes, nil
	}

	graph, err := s.draw(ctx, id, scope)
	if err != nil {
		return nil, err
	}
	return graph.Nodes, nil
}

// Subscribe receives coalesced changes in drawn scopes until cancel.
func (s *TopologyService) Subscribe(fn func(domain.ClusterChange)) (cancel func()) {
	return s.feed.Subscribe(fn)
}

// Release forgets a cluster's drawn scope.
func (s *TopologyService) Release(id domain.ClusterID) {
	s.feed.Release(id)
	s.mu.Lock()
	delete(s.last, id)
	s.mu.Unlock()
}

// Changed is the ports.ChangeSink the adapter calls. It never blocks.
func (s *TopologyService) Changed(id domain.ClusterID, namespace domain.NamespaceName) {
	s.feed.Changed(id, namespace)
}

// Close stops every pending announcement and waits for any in flight. For
// shutdown, beside the other owners OnShutdown names.
func (s *TopologyService) Close() { s.feed.Close() }

// ChangeFeed coalesces "something changed" into one announcement per
// cluster per window.
//
// CHANGED NEVER BLOCKS. It runs on a reflector's delivery goroutine and on
// the write path, so it takes one short lock, drops what nobody asked about
// — no subscriber, a cluster nobody drew, a namespace outside the drawn
// scope — and arms at most one timer per cluster. The timer's goroutine is
// the only one this owns; Close stops the timers and waits for any that
// already fired.
type ChangeFeed struct {
	window   time.Duration
	interest time.Duration
	isOpen   func(domain.ClusterID) bool
	now      func() time.Time

	mu       sync.Mutex
	closed   bool
	subs     map[int]func(domain.ClusterChange)
	nextSub  int
	drawn    map[domain.ClusterID]drawnScope
	pending  map[domain.ClusterID]*pendingChange
	drawings uint64
	inFlight sync.WaitGroup
}

type drawnScope struct {
	scope domain.TopologyScope
	at    time.Time
	// drawing numbers each Watch, so a flush that found its cluster closed
	// forgets only the drawing it read — not one made after a reopen.
	drawing uint64
}

type pendingChange struct {
	namespaces map[domain.NamespaceName]bool
	anywhere   bool
	timer      *time.Timer
}

// NewChangeFeed builds a feed. isOpen, when given, drops announcements for a
// cluster that has since been closed.
func NewChangeFeed(window, interest time.Duration, isOpen func(domain.ClusterID) bool) *ChangeFeed {
	if window <= 0 {
		window = DefaultChangeWindow
	}
	if interest <= 0 {
		interest = DefaultTopologyInterest
	}
	return &ChangeFeed{
		window:   window,
		interest: interest,
		isOpen:   isOpen,
		now:      time.Now,
		subs:     make(map[int]func(domain.ClusterChange)),
		drawn:    make(map[domain.ClusterID]drawnScope),
		pending:  make(map[domain.ClusterID]*pendingChange),
	}
}

// Watch records that a cluster's scope was drawn. The latest drawing wins:
// one topology page per cluster tab.
func (f *ChangeFeed) Watch(id domain.ClusterID, scope domain.TopologyScope) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	f.drawings++
	f.drawn[id] = drawnScope{scope: scope, at: f.now(), drawing: f.drawings}
}

// Release forgets a cluster's drawn scope and anything pending for it.
func (f *ChangeFeed) Release(id domain.ClusterID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.drawn, id)
	if p := f.pending[id]; p != nil && p.timer.Stop() {
		f.inFlight.Done()
	}
	delete(f.pending, id)
}

// forgetDrawing releases a cluster only if its scope is still the drawing
// the caller read.
func (f *ChangeFeed) forgetDrawing(id domain.ClusterID, drawing uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if current, found := f.drawn[id]; found && current.drawing == drawing {
		delete(f.drawn, id)
	}
}

// Subscribe adds a listener; cancel removes it and is safe to call twice.
func (f *ChangeFeed) Subscribe(fn func(domain.ClusterChange)) (cancel func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := f.nextSub
	f.nextSub++
	f.subs[key] = fn
	return func() {
		f.mu.Lock()
		delete(f.subs, key)
		f.mu.Unlock()
	}
}

// Changed notes a change. A namespace of NamespaceAll means somewhere in the
// cluster.
func (f *ChangeFeed) Changed(id domain.ClusterID, namespace domain.NamespaceName) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed || len(f.subs) == 0 {
		return
	}
	drawn, found := f.drawn[id]
	if !found {
		return
	}
	if f.now().Sub(drawn.at) > f.interest {
		delete(f.drawn, id)
		return
	}
	if !namespace.IsAll() && !drawn.scope.Includes(namespace) {
		return
	}

	p := f.pending[id]
	if p == nil {
		p = &pendingChange{namespaces: make(map[domain.NamespaceName]bool)}
		f.pending[id] = p
		f.inFlight.Add(1)
		p.timer = time.AfterFunc(f.window, func() {
			defer f.inFlight.Done()
			f.flush(id, p)
		})
	}
	if namespace.IsAll() {
		p.anywhere = true
	} else {
		p.namespaces[namespace] = true
	}
}

// flush announces what gathered for one cluster.
func (f *ChangeFeed) flush(id domain.ClusterID, p *pendingChange) {
	defer safego.Recover("topology change feed " + id.String())

	f.mu.Lock()
	if f.closed || f.pending[id] != p {
		f.mu.Unlock()
		return
	}
	delete(f.pending, id)
	drawn, found := f.drawn[id]
	subs := make([]func(domain.ClusterChange), 0, len(f.subs))
	for _, fn := range f.subs {
		subs = append(subs, fn)
	}
	change := domain.ClusterChange{ClusterID: id}
	switch {
	case p.anywhere && !drawn.scope.All:
		change.Namespaces = slices.Clone(drawn.scope.Namespaces)
	case !p.anywhere:
		for ns := range p.namespaces {
			change.Namespaces = append(change.Namespaces, ns)
		}
		slices.Sort(change.Namespaces)
	}
	f.mu.Unlock()

	if !found || len(subs) == 0 {
		return
	}
	if f.isOpen != nil && !f.isOpen(id) {
		// The lock is not held here, so the cluster may have been reopened
		// and drawn again since it was read: forget only this drawing.
		f.forgetDrawing(id, drawn.drawing)
		return
	}
	for _, fn := range subs {
		safego.Run("topology change subscriber", func() { fn(change) })
	}
}

// Close stops the feed: pending announcements are dropped, ones already
// firing are waited for, and Changed does nothing afterwards.
func (f *ChangeFeed) Close() {
	f.mu.Lock()
	f.closed = true
	for id, p := range f.pending {
		if p.timer.Stop() {
			f.inFlight.Done()
		}
		delete(f.pending, id)
	}
	f.mu.Unlock()
	f.inFlight.Wait()
}

// TrafficNodes is TopologyNodes in the shape the traffic layer reads: what
// lets an observed endpoint carry the id of the box it belongs to. It makes
// the topology service the traffic service's TrafficNodeReader.
func (s *TopologyService) TrafficNodes(ctx context.Context, id domain.ClusterID, namespaces []domain.NamespaceName, all bool) ([]domain.TrafficNodeRef, error) {
	nodes, err := s.TopologyNodes(ctx, id, namespaces, all)
	if err != nil {
		return nil, err
	}
	refs := make([]domain.TrafficNodeRef, 0, len(nodes))
	for _, node := range nodes {
		// A backend-folded pod set has no name of its own to match.
		if node.Name == "" {
			continue
		}
		refs = append(refs, domain.TrafficNodeRef{
			ID: node.ID, APIKind: node.APIKind, Name: node.Name, Namespace: node.Namespace,
		})
	}
	return refs, nil
}

var _ TrafficNodeReader = (*TopologyService)(nil)
