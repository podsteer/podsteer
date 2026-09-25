package application_test

// Tests for the use-case layer over the backend settings.
//
// The service is thin, so what is worth asserting is the JUDGEMENT in it —
// what a gesture MEANS. Adding a source already listed is not an error;
// removing one that is not there succeeds; a move past either end is clamped.
// Each of those is a decision about the operator's intent, and each would
// otherwise surface as an error dialog for a state that already matches what
// they asked for.

import (
	"context"
	"errors"
	"testing"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// newSettingsService wires the service around an in-memory store.
func newSettingsService(t *testing.T) (*application.SettingsService, *memorySettings) {
	t.Helper()

	store := newMemorySettings()
	service, err := application.NewSettingsService(application.SettingsServiceDeps{
		Settings:   settingsPortOver(store),
		Kubeconfig: &fakeKubeconfig{},
	})
	if err != nil {
		t.Fatalf("NewSettingsService() error = %v", err)
	}
	return service, store
}

// settingsPortOver gives memorySettings the State method the port needs.
type settingsPort struct {
	*memorySettings
	state domain.SettingsState
}

func settingsPortOver(store *memorySettings) ports.SettingsPort {
	return &settingsPort{memorySettings: store, state: domain.SettingsState{Path: "/tmp/settings.json"}}
}

func (s *settingsPort) State() domain.SettingsState { return s.state }

func TestNewSettingsServiceRejectsMissingDependencies(t *testing.T) {
	t.Parallel()

	full := application.SettingsServiceDeps{
		Settings:   settingsPortOver(newMemorySettings()),
		Kubeconfig: &fakeKubeconfig{},
	}

	tests := map[string]func(*application.SettingsServiceDeps){
		"no settings port":   func(d *application.SettingsServiceDeps) { d.Settings = nil },
		"no kubeconfig port": func(d *application.SettingsServiceDeps) { d.Kubeconfig = nil },
	}

	for name, breakIt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			deps := full
			breakIt(&deps)
			if _, err := application.NewSettingsService(deps); err == nil {
				t.Error("NewSettingsService() accepted incomplete dependencies")
			}
		})
	}
}

func TestAddingASourceAppendsItAfterWhatIsAlreadyThere(t *testing.T) {
	t.Parallel()

	service, store := newSettingsService(t)
	ctx := context.Background()

	for _, path := range []string{"/one.yaml", "/two.yaml"} {
		if err := service.AddKubeconfigSource(ctx, domain.KubeconfigSource{
			Path: path, Kind: domain.SourceFile,
		}); err != nil {
			t.Fatalf("AddKubeconfigSource(%q) error = %v", path, err)
		}
	}

	settings, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(settings.Kubeconfig.Sources) != 2 {
		t.Fatalf("sources = %+v, want 2", settings.Kubeconfig.Sources)
	}
	// APPENDED, never prepended: precedence is the reason order matters, and a
	// new source has to start where it can shadow nothing.
	if settings.Kubeconfig.Sources[0].Path != "/one.yaml" {
		t.Errorf("sources = %+v, want the first one still first", settings.Kubeconfig.Sources)
	}
}

// Asking for a path to be a source when it already is matches what the
// operator wanted, so it succeeds and changes nothing.
func TestAddingASourceTwiceIsNotAnError(t *testing.T) {
	t.Parallel()

	service, store := newSettingsService(t)
	ctx := context.Background()
	source := domain.KubeconfigSource{Path: "/one.yaml", Kind: domain.SourceFile}

	if err := service.AddKubeconfigSource(ctx, source); err != nil {
		t.Fatalf("AddKubeconfigSource() error = %v", err)
	}
	if err := service.AddKubeconfigSource(ctx, source); err != nil {
		t.Fatalf("adding the same source again returned %v", err)
	}

	settings, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(settings.Kubeconfig.Sources) != 1 {
		t.Errorf("sources = %+v, want one", settings.Kubeconfig.Sources)
	}
}

// Removing one that is not there leaves the list as asked, which is what the
// caller wanted; an error would only ever be a message about a row somebody
// had already deleted in another window.
func TestRemovingASourceThatIsNotThereSucceeds(t *testing.T) {
	t.Parallel()

	service, _ := newSettingsService(t)
	if err := service.RemoveKubeconfigSource(context.Background(), "/never-added.yaml"); err != nil {
		t.Errorf("RemoveKubeconfigSource() error = %v", err)
	}
}

func TestRemovingASourceDropsOnlyThatOne(t *testing.T) {
	t.Parallel()

	service, store := newSettingsService(t)
	ctx := context.Background()

	for _, path := range []string{"/one.yaml", "/two.yaml", "/three.yaml"} {
		if err := service.AddKubeconfigSource(ctx, domain.KubeconfigSource{
			Path: path, Kind: domain.SourceFile,
		}); err != nil {
			t.Fatalf("AddKubeconfigSource(%q) error = %v", path, err)
		}
	}

	if err := service.RemoveKubeconfigSource(ctx, "/two.yaml"); err != nil {
		t.Fatalf("RemoveKubeconfigSource() error = %v", err)
	}

	settings, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := pathsOf(settings.Kubeconfig.Sources); len(got) != 2 || got[0] != "/one.yaml" || got[1] != "/three.yaml" {
		t.Errorf("sources = %v, want the other two in order", got)
	}
}

func TestMovingASourceChangesItsPrecedence(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		path  string
		delta int
		want  []string
	}{
		"up one":                {path: "/three.yaml", delta: -1, want: []string{"/one.yaml", "/three.yaml", "/two.yaml"}},
		"down one":              {path: "/one.yaml", delta: 1, want: []string{"/two.yaml", "/one.yaml", "/three.yaml"}},
		"to the top":            {path: "/three.yaml", delta: -2, want: []string{"/three.yaml", "/one.yaml", "/two.yaml"}},
		"past the top, clamped": {path: "/two.yaml", delta: -9, want: []string{"/two.yaml", "/one.yaml", "/three.yaml"}},
		"past the end, clamped": {path: "/one.yaml", delta: 9, want: []string{"/two.yaml", "/three.yaml", "/one.yaml"}},
		"nowhere":               {path: "/one.yaml", delta: 0, want: []string{"/one.yaml", "/two.yaml", "/three.yaml"}},
		"a path not in the list": {
			path: "/absent.yaml", delta: 1,
			want: []string{"/one.yaml", "/two.yaml", "/three.yaml"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			service, store := newSettingsService(t)
			ctx := context.Background()
			for _, path := range []string{"/one.yaml", "/two.yaml", "/three.yaml"} {
				if err := service.AddKubeconfigSource(ctx, domain.KubeconfigSource{
					Path: path, Kind: domain.SourceFile,
				}); err != nil {
					t.Fatalf("AddKubeconfigSource(%q) error = %v", path, err)
				}
			}

			// CLAMPED RATHER THAN REFUSED: the control is a pair of arrows,
			// and the top row's "up" is a press that should do nothing rather
			// than raise an error.
			if err := service.MoveKubeconfigSource(ctx, tc.path, tc.delta); err != nil {
				t.Fatalf("MoveKubeconfigSource() error = %v", err)
			}

			settings, err := store.Load(ctx)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			got := pathsOf(settings.Kubeconfig.Sources)
			if len(got) != len(tc.want) {
				t.Fatalf("sources = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("sources = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// A refusal from the store reaches the caller rather than being swallowed:
// unlike a retention change, adding a source has no effect at all if it was
// not stored, so the pane must be able to say so.
func TestARefusedStoreSurfacesItsRefusal(t *testing.T) {
	t.Parallel()

	service, err := application.NewSettingsService(application.SettingsServiceDeps{
		Settings:   refusingSettingsPort{},
		Kubeconfig: &fakeKubeconfig{},
	})
	if err != nil {
		t.Fatalf("NewSettingsService() error = %v", err)
	}

	err = service.AddKubeconfigSource(context.Background(), domain.KubeconfigSource{
		Path: "/one.yaml", Kind: domain.SourceFile,
	})
	if !errors.Is(err, ports.ErrSettingsFromFuture) {
		t.Errorf("AddKubeconfigSource() error = %v, want ErrSettingsFromFuture", err)
	}
}

// refusingSettingsPort stands in for a store over a file from a newer
// PodSteer.
type refusingSettingsPort struct{}

func (refusingSettingsPort) Load(context.Context) (domain.Settings, error) {
	return domain.DefaultSettings(), nil
}

func (refusingSettingsPort) Update(context.Context, func(*domain.Settings) error) (domain.Settings, error) {
	return domain.Settings{}, ports.ErrSettingsFromFuture
}

func (refusingSettingsPort) State() domain.SettingsState {
	return domain.SettingsState{FromFuture: true, Version: 99}
}

func TestStateAndSourcesAreForwardedFromTheirOwners(t *testing.T) {
	t.Parallel()

	kubeconfig := &fakeKubeconfig{
		sources: []domain.KubeconfigEntry{
			{Path: "/home/op/.kube/config", Kind: domain.SourceFile, Origin: domain.OriginDefault},
		},
	}
	service, err := application.NewSettingsService(application.SettingsServiceDeps{
		Settings:   settingsPortOver(newMemorySettings()),
		Kubeconfig: kubeconfig,
	})
	if err != nil {
		t.Fatalf("NewSettingsService() error = %v", err)
	}

	ctx := context.Background()

	state, err := service.State(ctx)
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	if state.Path != "/tmp/settings.json" || !state.IsWritable() {
		t.Errorf("state = %+v, want the store's own", state)
	}

	// The composed list comes from the thing that performs the merge, never
	// from the settings: only it can say which file contributed which context.
	entries, err := service.KubeconfigSources(ctx)
	if err != nil {
		t.Fatalf("KubeconfigSources() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Origin != domain.OriginDefault {
		t.Errorf("entries = %+v, want the kubeconfig port's own", entries)
	}
}

func pathsOf(sources []domain.KubeconfigSource) []string {
	out := make([]string, 0, len(sources))
	for _, source := range sources {
		out = append(out, source.Path)
	}
	return out
}

// --- per-cluster switches ---------------------------------------------------

func TestClusterReportsTheDefaultsRatherThanAnAbsence(t *testing.T) {
	t.Parallel()

	// The judgement worth asserting here is that "no entry" is answered as a
	// VALUE. A caller handed an absence would have to decide what it means,
	// and for a setting governing whether PromQL reaches somebody's
	// production Prometheus that decision must be made once, in the domain.
	service, _ := newSettingsService(t)

	got, err := service.Cluster(context.Background(), "never-opened")
	if err != nil {
		t.Fatalf("Cluster() error = %v", err)
	}
	if got != domain.DefaultClusterSettings() {
		t.Fatalf("Cluster() = %+v, want the defaults", got)
	}
}

func TestSetMetricsQueryStoresTheWholeValue(t *testing.T) {
	t.Parallel()

	service, store := newSettingsService(t)

	want := domain.MetricsQuerySettings{
		Mode:      domain.MetricsQueryManual,
		Preferred: domain.PreferredBackend{Namespace: "monitoring", Service: "prometheus-operated"},
		Fleet:     domain.FleetRefuse,
	}
	if err := service.SetMetricsQuery(context.Background(), "prod", want); err != nil {
		t.Fatalf("SetMetricsQuery() error = %v", err)
	}

	settings, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := settings.Cluster("prod").MetricsQuery; got != want {
		t.Fatalf("stored %+v, want %+v", got, want)
	}
}

func TestSetMetricsQueryLeavesTheNodeHistoryOptInAlone(t *testing.T) {
	t.Parallel()

	// TWO FEATURES SHARE THIS SECTION, and each must write only its own half.
	// Writing the whole entry from the metrics-query value would silently
	// turn node recording off for a cluster whose operator changed something
	// else entirely — and node history is the one whose off has a side effect
	// (the recorded history is erased), which is why its setter lives on the
	// history service and not here.
	service, store := newSettingsService(t)

	if _, err := store.Update(context.Background(), func(settings *domain.Settings) error {
		settings.Clusters["prod"] = domain.ClusterSettings{NodeHistory: true}
		return nil
	}); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	err := service.SetMetricsQuery(context.Background(), "prod", domain.MetricsQuerySettings{
		Mode:  domain.MetricsQueryAuto,
		Fleet: domain.FleetFilter,
	})
	if err != nil {
		t.Fatalf("SetMetricsQuery() error = %v", err)
	}

	settings, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	cluster := settings.Cluster("prod")
	if !cluster.NodeHistory {
		t.Error("the node history opt-in was cleared by a metrics query write")
	}
	if cluster.MetricsQuery.Mode != domain.MetricsQueryAuto {
		t.Errorf("mode = %q, want auto", cluster.MetricsQuery.Mode)
	}
}

func TestTurningTheQueryBackOffRemovesTheEntryAndItsBackendName(t *testing.T) {
	t.Parallel()

	// The disclosed exception is narrow in TIME as well as in scope: an
	// operator who chose a backend and then turned querying off leaves no
	// entry behind, so the monitoring Service's name is gone from the file
	// rather than lingering under a mode that never reads it.
	service, store := newSettingsService(t)

	if err := service.SetMetricsQuery(context.Background(), "prod", domain.MetricsQuerySettings{
		Mode:      domain.MetricsQueryAuto,
		Preferred: domain.PreferredBackend{Namespace: "monitoring", Service: "prometheus-operated"},
		Fleet:     domain.FleetFilter,
	}); err != nil {
		t.Fatalf("SetMetricsQuery() error = %v", err)
	}

	if err := service.SetMetricsQuery(context.Background(), "prod",
		domain.DefaultMetricsQuerySettings()); err != nil {
		t.Fatalf("SetMetricsQuery() error = %v", err)
	}

	settings, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if _, kept := settings.Clusters["prod"]; kept {
		t.Fatalf("the entry survived being turned back off: %+v", settings.Clusters["prod"])
	}
}

func TestSetMetricsQueryNeedsACluster(t *testing.T) {
	t.Parallel()

	// A blank context name would key an entry nothing could ever read back,
	// and it would put an empty string into the file's cluster map.
	service, _ := newSettingsService(t)

	err := service.SetMetricsQuery(context.Background(), "", domain.DefaultMetricsQuerySettings())
	if err == nil {
		t.Fatal("SetMetricsQuery() error = nil, want a refusal")
	}
}
