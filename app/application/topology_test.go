package application_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
)

// fakeTopologyPort answers TopologySources with a fixed input.
type fakeTopologyPort struct {
	mu     sync.Mutex
	input  domain.TopologyInput
	err    error
	scopes []domain.TopologyScope
}

func (f *fakeTopologyPort) TopologySources(_ context.Context, _ domain.ClusterID, scope domain.TopologyScope) (domain.TopologyInput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scopes = append(f.scopes, scope)
	in := f.input
	in.Scope = scope
	return in, f.err
}

// changes collects announcements.
type changes struct {
	mu  sync.Mutex
	got []domain.ClusterChange
}

func (c *changes) add(change domain.ClusterChange) {
	c.mu.Lock()
	c.got = append(c.got, change)
	c.mu.Unlock()
}

func (c *changes) snapshot() []domain.ClusterChange {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.got)
}

const testWindow = 20 * time.Millisecond

// settle waits long enough for one window to fire.
func settle() { time.Sleep(4 * testWindow) }

func topologyService(t *testing.T, port *fakeTopologyPort) (*application.TopologyService, *application.Registry) {
	t.Helper()
	registry := application.NewRegistry()
	registry.Open(mustCluster(t, "dev", true))
	service, err := application.NewTopologyService(application.TopologyServiceDeps{
		Topology: port, Registry: registry, Window: testWindow,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	return service, registry
}

func mustScope(t *testing.T, namespaces ...string) domain.TopologyScope {
	t.Helper()
	scope, err := domain.NewTopologyScope(namespaces, len(namespaces) == 0)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func TestNewTopologyServiceRequiresItsPorts(t *testing.T) {
	if _, err := application.NewTopologyService(application.TopologyServiceDeps{Registry: application.NewRegistry()}); err == nil {
		t.Error("no TopologyPort accepted")
	}
	if _, err := application.NewTopologyService(application.TopologyServiceDeps{Topology: &fakeTopologyPort{}}); err == nil {
		t.Error("no Registry accepted")
	}
}

func TestTopologyDrawsAnOpenClusterOnly(t *testing.T) {
	port := &fakeTopologyPort{}
	service, _ := topologyService(t, port)

	if _, err := service.Topology(context.Background(), "prod", mustScope(t, "shop")); !errors.Is(err, domain.ErrClusterNotConnected) {
		t.Errorf("err = %v, want ErrClusterNotConnected", err)
	}
	if _, err := service.Topology(context.Background(), "dev", domain.TopologyScope{}); !errors.Is(err, domain.ErrEmptyTopologyScope) {
		t.Errorf("err = %v, want ErrEmptyTopologyScope", err)
	}

	graph, err := service.Topology(context.Background(), "dev", mustScope(t, "shop"))
	if err != nil {
		t.Fatal(err)
	}
	if graph.Bounded == "" || graph.Counts == nil {
		t.Errorf("graph = %+v", graph)
	}

	port.err = errors.New("no client")
	if _, err := service.Topology(context.Background(), "dev", mustScope(t, "shop")); err == nil {
		t.Error("a client failure is an error")
	}
}

func TestChangesAreCoalescedPerClusterAndScoped(t *testing.T) {
	service, _ := topologyService(t, &fakeTopologyPort{})
	heard := &changes{}
	cancel := service.Subscribe(heard.add)
	defer cancel()

	// Nothing drawn yet: nothing is announced.
	service.Changed("dev", "shop")
	settle()
	if got := heard.snapshot(); len(got) != 0 {
		t.Fatalf("an undrawn cluster announced %v", got)
	}

	if _, err := service.Topology(context.Background(), "dev", mustScope(t, "shop", "batch")); err != nil {
		t.Fatal(err)
	}
	for range 50 {
		service.Changed("dev", "shop")
	}
	service.Changed("dev", "batch")
	service.Changed("dev", "elsewhere") // outside the scope
	settle()

	got := heard.snapshot()
	if len(got) != 1 {
		t.Fatalf("heard %d announcements, want one: %v", len(got), got)
	}
	if want := []domain.NamespaceName{"batch", "shop"}; got[0].ClusterID != "dev" || !slices.Equal(got[0].Namespaces, want) {
		t.Errorf("change = %+v", got[0])
	}

	// Only outside the scope: nothing.
	service.Changed("dev", "elsewhere")
	settle()
	if n := len(heard.snapshot()); n != 1 {
		t.Errorf("an out-of-scope change was announced (%d)", n)
	}

	// A write says nothing about where: the drawn scope is named.
	service.Changed("dev", domain.NamespaceAll)
	settle()
	got = heard.snapshot()
	if len(got) != 2 || !slices.Equal(got[1].Namespaces, []domain.NamespaceName{"batch", "shop"}) {
		t.Errorf("anywhere change = %+v", got)
	}
}

func TestAnAllScopeAnnouncesNamespacesItSaw(t *testing.T) {
	service, _ := topologyService(t, &fakeTopologyPort{})
	heard := &changes{}
	defer service.Subscribe(heard.add)()

	if _, err := service.Topology(context.Background(), "dev", mustScope(t)); err != nil {
		t.Fatal(err)
	}
	service.Changed("dev", "anything")
	settle()
	service.Changed("dev", domain.NamespaceAll)
	settle()

	got := heard.snapshot()
	if len(got) != 2 || !slices.Equal(got[0].Namespaces, []domain.NamespaceName{"anything"}) || got[1].Namespaces != nil {
		t.Errorf("heard %+v", got)
	}
}

func TestNoSubscriberNoClosedClusterNoReleasedScope(t *testing.T) {
	service, registry := topologyService(t, &fakeTopologyPort{})
	if _, err := service.Topology(context.Background(), "dev", mustScope(t, "shop")); err != nil {
		t.Fatal(err)
	}

	// Nobody listening: dropped without arming anything.
	service.Changed("dev", "shop")
	heard := &changes{}
	cancel := service.Subscribe(heard.add)
	settle()
	if n := len(heard.snapshot()); n != 0 {
		t.Errorf("a change before any subscriber was announced (%d)", n)
	}

	service.Release("dev")
	service.Changed("dev", "shop")
	settle()
	if n := len(heard.snapshot()); n != 0 {
		t.Errorf("a released scope announced (%d)", n)
	}

	if _, err := service.Topology(context.Background(), "dev", mustScope(t, "shop")); err != nil {
		t.Fatal(err)
	}
	registry.Close("dev")
	service.Changed("dev", "shop")
	settle()
	if n := len(heard.snapshot()); n != 0 {
		t.Errorf("a closed cluster announced (%d)", n)
	}

	cancel()
	cancel() // twice is fine
}

func TestInterestExpires(t *testing.T) {
	registry := application.NewRegistry()
	registry.Open(mustCluster(t, "dev", true))
	service, err := application.NewTopologyService(application.TopologyServiceDeps{
		Topology: &fakeTopologyPort{}, Registry: registry, Window: testWindow, Interest: 30 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	heard := &changes{}
	defer service.Subscribe(heard.add)()

	if _, err := service.Topology(context.Background(), "dev", mustScope(t, "shop")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	service.Changed("dev", "shop")
	settle()
	if n := len(heard.snapshot()); n != 0 {
		t.Errorf("an expired scope announced (%d)", n)
	}
}

// TestChangeFeedRace drives every entry point at once, for -race: Changed is
// called from reflector goroutines and the write path while the page draws,
// subscribes, releases and the application shuts down.
func TestChangeFeedRace(t *testing.T) {
	feed := application.NewChangeFeed(time.Millisecond, time.Hour, func(domain.ClusterID) bool { return true })
	scope := mustScope(t, "a", "b")

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for j := range 500 {
				ns := domain.NamespaceName([]string{"a", "b", "c", ""}[(i+j)%4])
				feed.Changed(domain.ClusterID([]string{"dev", "prod"}[j%2]), ns)
			}
		})
	}
	wg.Go(func() {
		for range 100 {
			cancel := feed.Subscribe(func(domain.ClusterChange) {})
			feed.Watch("dev", scope)
			feed.Watch("prod", scope)
			time.Sleep(50 * time.Microsecond)
			feed.Release("prod")
			cancel()
		}
	})
	keep := feed.Subscribe(func(domain.ClusterChange) {})
	wg.Wait()
	keep()
	feed.Close()
	feed.Changed("dev", "a") // after Close: nothing, and no panic
}
