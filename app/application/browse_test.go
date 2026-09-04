package application_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// fakeResources counts objects without a cluster.
//
// counts is keyed by the kind's plural resource name; a kind absent from it
// returns the failure in failures, or zero. peak records the highest number of
// counts in flight at once, which is what the concurrency limit is about.
type fakeResources struct {
	counts   map[string]int
	failures map[string]error
	// hold, when non-nil, blocks every count until it is closed — so the
	// calls actually overlap and the peak measures something. Without it a
	// fake that returns immediately never has two counts in flight at once,
	// and any assertion about pacing passes whether or not it is paced.
	hold chan struct{}

	// chain and inspectErr shape what InspectTLSSecret answers, for the
	// application-level tests of BrowseService.InspectTLSSecret — no cluster
	// and no real certificate involved at this layer.
	chain      domain.CertificateChain
	inspectErr error

	// summaries and summariesErr shape what ListVulnerabilitySummaries
	// answers. Empty is the ordinary case — most clusters run no scanner —
	// so the zero value is already the realistic one.
	summaries    []domain.VulnerabilitySummary
	summariesErr error
	// graphInput and graphErr shape what ObjectGraphSources answers, and
	// graphRef records the reference it was asked for — which is the half of
	// BrowseService.ObjectGraph worth asserting at this layer, since the
	// catalogue lookup and the cluster-scoped namespace reset both land there.
	graphInput domain.ObjectGraphInput
	graphErr   error
	graphRef   domain.ResourceRef

	mu       sync.Mutex
	inFlight int
	peak     int
	calls    atomic.Int64
}

// flight reports how many counts are in progress right now.
func (f *fakeResources) flight() (current, peak int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inFlight, f.peak
}

func (f *fakeResources) CountResources(_ context.Context, _ domain.ClusterID, kind domain.ResourceKind, _ domain.NamespaceName) (int, error) {
	f.calls.Add(1)

	f.mu.Lock()
	f.inFlight++
	if f.inFlight > f.peak {
		f.peak = f.inFlight
	}
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inFlight--
		f.mu.Unlock()
	}()

	if f.hold != nil {
		<-f.hold
	}

	if err, failed := f.failures[kind.Resource]; failed {
		return 0, err
	}
	return f.counts[kind.Resource], nil
}

func (f *fakeResources) ListTable(context.Context, domain.ClusterID, domain.ResourceKind, domain.NamespaceName, domain.Projection) (domain.ResourceTable, error) {
	return domain.ResourceTable{}, nil
}

func (f *fakeResources) GetManifest(context.Context, domain.ResourceRef, bool) (string, error) {
	return "", nil
}

func (f *fakeResources) RevealSecretKey(context.Context, domain.ClusterID, domain.NamespaceName, string, string) (string, error) {
	return "", nil
}

func (f *fakeResources) ObjectGraphSources(_ context.Context, ref domain.ResourceRef) (domain.ObjectGraphInput, error) {
	f.mu.Lock()
	f.graphRef = ref
	f.mu.Unlock()

	if f.graphErr != nil {
		return domain.ObjectGraphInput{}, f.graphErr
	}
	return f.graphInput, nil
}

// chain is returned by InspectTLSSecret when it is set, so a test can shape
// the answer without a cluster or a certificate.
func (f *fakeResources) InspectTLSSecret(context.Context, domain.ClusterID, domain.NamespaceName, string) (domain.CertificateChain, error) {
	if f.inspectErr != nil {
		return domain.CertificateChain{}, f.inspectErr
	}
	return f.chain, nil
}

func (f *fakeResources) ListVulnerabilitySummaries(context.Context, domain.ClusterID, domain.NamespaceName) ([]domain.VulnerabilitySummary, error) {
	return f.summaries, f.summariesErr
}

// Compile-time proof the fake still matches the port it stands in for.
var _ ports.ResourcePort = (*fakeResources)(nil)

func newBrowseService(t *testing.T, resources *fakeResources) *application.BrowseService {
	t.Helper()

	registry := application.NewRegistry()
	registry.Open(mustCluster(t, "dev", true))

	service, err := application.NewBrowseService(application.BrowseServiceDeps{
		Resources: resources,
		Events:    &fakeEvents{},
		Registry:  registry,
		Catalog:   domain.NewCatalog(),
	})
	if err != nil {
		t.Fatalf("NewBrowseService() error = %v", err)
	}
	return service
}

func TestNamespaceInventoryCountsOnlyWhatIsWorthCounting(t *testing.T) {
	t.Parallel()

	resources := &fakeResources{counts: map[string]int{"pods": 29, "configmaps": 7}}
	service := newBrowseService(t, resources)

	inventory, err := service.NamespaceInventory(context.Background(), "dev", "web")
	if err != nil {
		t.Fatalf("NamespaceInventory() error = %v", err)
	}

	if inventory.Total != 36 {
		t.Fatalf("Total = %d, want 36", inventory.Total)
	}
	if len(inventory.Counts) != 2 {
		t.Fatalf("listed %d kinds, want the 2 holding something: %+v", len(inventory.Counts), inventory.Counts)
	}

	// Nodes are cluster-scoped and Events expire; neither is counted, so the
	// number of calls is the number of countable kinds and nothing else.
	countable := len(domain.CountableKinds(domain.NewCatalog().Kinds("dev")))
	if got := int(resources.calls.Load()); got != countable {
		t.Fatalf("made %d counts for %d countable kinds", got, countable)
	}
}

func TestOneRefusedKindDoesNotFailTheInventory(t *testing.T) {
	t.Parallel()

	// The ordinary account: it can list pods and not secrets. An inventory
	// that failed here would leave the panel with nothing, when the honest
	// answer is everything except the secrets.
	resources := &fakeResources{
		counts: map[string]int{"pods": 29},
		failures: map[string]error{
			"secrets": fmt.Errorf("listing secrets: %w", ports.ErrForbidden),
		},
	}
	service := newBrowseService(t, resources)

	inventory, err := service.NamespaceInventory(context.Background(), "dev", "web")
	if err != nil {
		t.Fatalf("NamespaceInventory() error = %v", err)
	}

	if inventory.Unreadable != 1 {
		t.Fatalf("Unreadable = %d, want 1", inventory.Unreadable)
	}
	if inventory.Total != 29 {
		t.Fatalf("Total = %d — a refusal must contribute nothing, not zero", inventory.Total)
	}

	var secrets domain.ResourceCount
	for _, count := range inventory.Counts {
		if count.Kind.Resource == "secrets" {
			secrets = count
		}
	}
	if secrets.Unreadable != "not permitted" {
		t.Fatalf("secrets reported as %q, want %q", secrets.Unreadable, "not permitted")
	}
}

func TestCountingIsPacedRatherThanFiredAllAtOnce(t *testing.T) {
	t.Parallel()

	// Every count blocks until released, so all of them that are allowed to
	// run at once are in flight together and the peak is the fan-out.
	resources := &fakeResources{counts: map[string]int{"pods": 1}, hold: make(chan struct{})}
	service := newBrowseService(t, resources)

	done := make(chan error, 1)
	go func() {
		_, err := service.NamespaceInventory(context.Background(), "dev", "web")
		done <- err
	}()

	// Wait for the fan-out to fill up. Unpaced, this reaches every countable
	// kind at once — around twenty — and the assertion below fails; paced, it
	// settles at the limit and stays there.
	deadline := time.After(5 * time.Second)
	for {
		current, _ := resources.flight()
		if current >= 8 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("only %d counts started; the fan-out never filled", current)
		default:
			time.Sleep(time.Millisecond)
		}
	}
	// Let the ones in flight settle before reading the peak, so a straggler
	// arriving late is counted.
	time.Sleep(20 * time.Millisecond)
	_, peak := resources.flight()

	close(resources.hold)
	if err := <-done; err != nil {
		t.Fatalf("NamespaceInventory() error = %v", err)
	}

	// Twenty simultaneous requests would spend the client's whole burst
	// allowance on counting a panel nobody is waiting on that hard.
	if peak > 8 {
		t.Fatalf("%d counts in flight at once, want no more than 8", peak)
	}
}

func TestCountingEveryNamespaceAtOnceIsRefused(t *testing.T) {
	t.Parallel()

	// "All namespaces" is a query, not a namespace. Counted literally it
	// returns cluster-wide totals under a heading that says otherwise.
	resources := &fakeResources{}
	service := newBrowseService(t, resources)

	if _, err := service.NamespaceInventory(context.Background(), "dev", domain.NamespaceAll); err == nil {
		t.Fatal("counted the contents of every namespace as though it were one")
	}
	if resources.calls.Load() != 0 {
		t.Fatalf("made %d requests before refusing", resources.calls.Load())
	}
}

func TestInspectTLSSecretPassesTheChainThrough(t *testing.T) {
	t.Parallel()

	matches := true
	want := domain.CertificateChain{
		Leaf:       domain.Certificate{Subject: "CN=app.example.com"},
		KeyMatches: &matches,
	}
	resources := &fakeResources{chain: want}
	service := newBrowseService(t, resources)

	got, err := service.InspectTLSSecret(context.Background(), "dev", "web", "app-tls")
	if err != nil {
		t.Fatalf("InspectTLSSecret() error = %v", err)
	}
	if got.Leaf.Subject != want.Leaf.Subject {
		t.Errorf("Leaf.Subject = %q, want %q — the use case must not reshape what the port returned", got.Leaf.Subject, want.Leaf.Subject)
	}
	if got.KeyMatches == nil || *got.KeyMatches != true {
		t.Errorf("KeyMatches = %v, want a pointer to true", got.KeyMatches)
	}
}

func TestInspectTLSSecretRefusesAnEmptyName(t *testing.T) {
	t.Parallel()

	resources := &fakeResources{}
	service := newBrowseService(t, resources)

	if _, err := service.InspectTLSSecret(context.Background(), "dev", "web", ""); err == nil {
		t.Fatal("InspectTLSSecret() with an empty name, want an error")
	}
}

func TestInspectTLSSecretRefusesADisconnectedCluster(t *testing.T) {
	t.Parallel()

	resources := &fakeResources{}
	// A registry with nothing open, unlike newBrowseService's — this Secret
	// belongs to a cluster PodSteer has not connected to.
	registry := application.NewRegistry()
	service, err := application.NewBrowseService(application.BrowseServiceDeps{
		Resources: resources,
		Events:    &fakeEvents{},
		Registry:  registry,
		Catalog:   domain.NewCatalog(),
	})
	if err != nil {
		t.Fatalf("NewBrowseService() error = %v", err)
	}

	if _, err := service.InspectTLSSecret(context.Background(), "dev", "web", "app-tls"); err == nil {
		t.Fatal("InspectTLSSecret() on an unconnected cluster, want an error")
	}
	if got := resources.calls.Load(); got != 0 {
		t.Fatalf("reached the port %d times before checking the connection", got)
	}
}

func TestInspectTLSSecretWrapsThePortsError(t *testing.T) {
	t.Parallel()

	resources := &fakeResources{inspectErr: domain.ErrNotTLSSecret}
	service := newBrowseService(t, resources)

	_, err := service.InspectTLSSecret(context.Background(), "dev", "web", "not-a-tls-secret")
	if err == nil {
		t.Fatal("InspectTLSSecret() error = nil, want the port's refusal to surface")
	}
}

// The catalogue lookup and the cluster-scoped namespace reset both land in
// ObjectGraph, so what it hands the port is worth asserting: a cluster-scoped
// kind queried with a namespace produces a path that 404s, and the drawer
// always has SOME namespace selected.
func TestObjectGraphReadsTheKindTheCatalogueNames(t *testing.T) {
	t.Parallel()

	resources := &fakeResources{}
	service := newBrowseService(t, resources)

	if _, err := service.ObjectGraph(context.Background(), "dev", "core/v1/nodes", "web", "node-1"); err != nil {
		t.Fatalf("ObjectGraph() error = %v", err)
	}

	resources.mu.Lock()
	defer resources.mu.Unlock()

	if resources.graphRef.Kind.Kind != "Node" {
		t.Errorf("read kind %q, want the one the catalogue id names", resources.graphRef.Kind.Kind)
	}
	if !resources.graphRef.Namespace.IsAll() {
		t.Errorf("namespace = %q, want none on a cluster-scoped kind", resources.graphRef.Namespace)
	}
}

// An unknown catalogue id is refused rather than read: a kind nothing serves
// has no object to draw a map around, and guessing at one would issue a read
// against a path the cluster does not have.
func TestObjectGraphRefusesAnUnknownKind(t *testing.T) {
	t.Parallel()

	service := newBrowseService(t, &fakeResources{})

	if _, err := service.ObjectGraph(context.Background(), "dev", "example.com/v1/widgets", "shop", "left"); err == nil {
		t.Fatal("ObjectGraph() error = nil, want a refusal naming the unknown kind")
	}
}

// An empty name is refused before any read. The drawer can be open on a kind
// with no row selected, and a GET of "" is a LIST of the whole collection.
func TestObjectGraphRefusesAnEmptyName(t *testing.T) {
	t.Parallel()

	resources := &fakeResources{}
	service := newBrowseService(t, resources)

	if _, err := service.ObjectGraph(context.Background(), "dev", "core/v1/configmaps", "shop", ""); err == nil {
		t.Fatal("ObjectGraph() error = nil, want a refusal")
	}

	resources.mu.Lock()
	defer resources.mu.Unlock()
	if resources.graphRef.Name != "" {
		t.Error("a read was made for an object with no name")
	}
}
