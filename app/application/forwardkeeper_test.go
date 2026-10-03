package application_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
)

type keeperSettingsFake struct {
	mu    sync.Mutex
	value domain.Settings
}

func newKeeperSettingsFake() *keeperSettingsFake {
	return &keeperSettingsFake{value: domain.DefaultSettings()}
}

func (f *keeperSettingsFake) Load(context.Context) (domain.Settings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.value.Clone(), nil
}

func (f *keeperSettingsFake) Update(_ context.Context, mutate func(*domain.Settings) error) (domain.Settings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	next := f.value.Clone()
	if err := mutate(&next); err != nil {
		return domain.Settings{}, err
	}
	if err := next.Validate(); err != nil {
		return domain.Settings{}, err
	}
	next.Normalise()
	f.value = next
	return next.Clone(), nil
}

type keeperForwardsFake struct {
	mu       sync.Mutex
	live     []domain.Forward
	started  []domain.Forward
	failWith error
	// entered and block make PodForwardTarget slow on demand.
	entered chan struct{}
	block   chan struct{}
}

func (f *keeperForwardsFake) StartPortForward(_ context.Context, id domain.ClusterID, ns domain.NamespaceName, pod, podUID string, localPort, remotePort int, portName, protocol string, selector map[string]string) (domain.Forward, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return domain.Forward{}, f.failWith
	}
	forward := domain.Forward{
		ID: strconv.Itoa(len(f.live) + 1), ClusterID: id, Namespace: ns, Pod: pod,
		LocalPort: localPort, RemotePort: remotePort, Selector: selector,
		Target: domain.ForwardTarget{Kind: domain.ForwardToPod, Name: pod},
	}
	f.live = append(f.live, forward)
	f.started = append(f.started, forward)
	return forward, nil
}

func (f *keeperForwardsFake) ServiceForwardTarget(_ context.Context, _ domain.ClusterID, _ domain.NamespaceName, service, port string) (domain.ServiceForwardTarget, error) {
	return domain.ServiceForwardTarget{Pod: service + "-abc", ContainerPort: 5432, Selector: map[string]string{"app": service}}, nil
}

func (f *keeperForwardsFake) PodForwardTarget(_ context.Context, _ domain.ClusterID, _ domain.NamespaceName, pod string, port int) (domain.ServiceForwardTarget, error) {
	f.mu.Lock()
	entered, block := f.entered, f.block
	f.mu.Unlock()
	if entered != nil {
		entered <- struct{}{}
		<-block
	}
	return domain.ServiceForwardTarget{Pod: pod, ContainerPort: port}, nil
}

func (f *keeperForwardsFake) SetForwardTarget(id string, target domain.ForwardTarget) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.live {
		if f.live[i].ID == id {
			f.live[i].Target = target
		}
	}
	return nil
}

func (f *keeperForwardsFake) ListPortForwards() []domain.Forward {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.live)
}

func newTestKeeper(t *testing.T) (*application.ForwardKeeper, *keeperSettingsFake, *keeperForwardsFake, *application.Registry) {
	t.Helper()
	settings, forwards, registry := newKeeperSettingsFake(), &keeperForwardsFake{}, application.NewRegistry()
	keeper, err := application.NewForwardKeeper(application.ForwardKeeperDeps{Settings: settings, Forwards: forwards, Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(keeper.Close)
	return keeper, settings, forwards, registry
}

func TestKeepPersistsADefinitionAndForgetRemovesIt(t *testing.T) {
	ctx := t.Context()
	tests := []struct {
		name   string
		target domain.ForwardTarget
		remote int
		want   domain.KeptForward
	}{
		{
			name:   "a pod keeps its container port",
			target: domain.ForwardTarget{Kind: domain.ForwardToPod, Name: "api-0"},
			remote: 8080,
			want: domain.KeptForward{Namespace: "web", RemotePort: 8080, LocalPort: 18080,
				Target: domain.ForwardTarget{Kind: domain.ForwardToPod, Name: "api-0"}},
		},
		{
			name:   "a Service keeps its own port, not the container port",
			target: domain.ForwardTarget{Kind: domain.ForwardToService, Name: "pg", ServicePort: "postgres"},
			remote: 5432,
			want: domain.KeptForward{Namespace: "web", LocalPort: 18080,
				Target: domain.ForwardTarget{Kind: domain.ForwardToService, Name: "pg", ServicePort: "postgres"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keeper, settings, forwards, _ := newTestKeeper(t)
			forwards.live = []domain.Forward{{
				ID: "1", ClusterID: "prod", Namespace: "web", Pod: "x", LocalPort: 18080,
				RemotePort: tt.remote, Target: tt.target,
			}}

			if err := keeper.Keep(ctx, "1", true); err != nil {
				t.Fatalf("Keep() error = %v", err)
			}
			got, _ := settings.Load(ctx)
			if kept := got.Cluster("prod").KeptForwards; len(kept) != 1 || kept[0] != tt.want {
				t.Fatalf("kept = %+v, want [%+v]", kept, tt.want)
			}

			if err := keeper.Keep(ctx, "1", false); err != nil {
				t.Fatalf("un-keep error = %v", err)
			}
			got, _ = settings.Load(ctx)
			if len(got.Cluster("prod").KeptForwards) != 0 {
				t.Fatalf("still kept after un-keep: %+v", got.Cluster("prod").KeptForwards)
			}
			if _, present := got.Clusters["prod"]; present {
				t.Fatal("an empty cluster stanza was left behind")
			}
		})
	}
}

func TestKeepRefusesAForwardThatIsNotRunning(t *testing.T) {
	keeper, _, _, _ := newTestKeeper(t)
	if err := keeper.Keep(t.Context(), "nope", true); !errors.Is(err, application.ErrForwardNotRunning) {
		t.Fatalf("Keep() error = %v, want application.ErrForwardNotRunning", err)
	}
}

func TestStoppingAForwardForgetsIt(t *testing.T) {
	ctx := t.Context()
	keeper, settings, forwards, _ := newTestKeeper(t)
	forwards.live = []domain.Forward{{ID: "1", ClusterID: "prod", Namespace: "web", Pod: "x", LocalPort: 18080, RemotePort: 80,
		Target: domain.ForwardTarget{Kind: domain.ForwardToPod, Name: "x"}}}
	if err := keeper.Keep(ctx, "1", true); err != nil {
		t.Fatal(err)
	}

	keeper.ForgetLive(ctx, "1")

	got, _ := settings.Load(ctx)
	if len(got.Cluster("prod").KeptForwards) != 0 {
		t.Fatalf("a stopped forward is still kept: %+v", got.Cluster("prod").KeptForwards)
	}
}

func TestRestoreNeverConnectsAndReportsWhy(t *testing.T) {
	ctx := t.Context()
	keptPod := domain.KeptForward{Namespace: "web", RemotePort: 8080, LocalPort: 18080,
		Target: domain.ForwardTarget{Kind: domain.ForwardToPod, Name: "api-0"}}
	keptSvc := domain.KeptForward{Namespace: "data", LocalPort: 15432,
		Target: domain.ForwardTarget{Kind: domain.ForwardToService, Name: "pg", ServicePort: "postgres"}}

	tests := []struct {
		name      string
		open      bool
		failWith  error
		wantState application.KeptForwardState
		wantLive  int
	}{
		{"cluster not connected is paused, nothing started", false, nil, application.KeptPaused, 0},
		{"connected clusters are restored", true, nil, "", 2},
		{"a refused restore is listed with the reason", true, errors.New("address already in use"), application.KeptFailed, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keeper, settings, forwards, registry := newTestKeeper(t)
			forwards.failWith = tt.failWith
			if _, err := settings.Update(ctx, func(s *domain.Settings) error {
				c := s.Cluster("prod")
				c.KeptForwards = []domain.KeptForward{keptPod, keptSvc}
				s.Clusters["prod"] = c
				return nil
			}); err != nil {
				t.Fatal(err)
			}

			if tt.open {
				cluster := mustCluster(t, "prod", false)
				registry.Open(cluster)
				keeper.Restore(ctx, "prod")
			}

			if got := len(forwards.ListPortForwards()); got != tt.wantLive {
				t.Fatalf("started %d forwards, want %d", got, tt.wantLive)
			}
			paused, err := keeper.Paused(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantState == "" {
				if len(paused) != 0 {
					t.Fatalf("paused = %+v, want none", paused)
				}
				// Service forward was rebuilt from its Service, with the label.
				for _, f := range forwards.ListPortForwards() {
					if f.LocalPort == 15432 && f.Target.Kind != domain.ForwardToService {
						t.Errorf("restored Service forward lost its target: %+v", f.Target)
					}
				}
				return
			}
			if len(paused) != 2 {
				t.Fatalf("paused = %+v, want both listed", paused)
			}
			for _, p := range paused {
				if p.State != tt.wantState {
					t.Errorf("state = %q, want %q", p.State, tt.wantState)
				}
				if tt.wantState == application.KeptFailed && p.Reason == "" {
					t.Error("a failed restore carries no reason")
				}
			}
		})
	}
}

func TestResumeRefusesAnUnconnectedCluster(t *testing.T) {
	keeper, _, _, _ := newTestKeeper(t)
	if err := keeper.Resume(t.Context(), "prod", 1); !errors.Is(err, domain.ErrClusterNotConnected) {
		t.Fatalf("Resume() error = %v, want ErrClusterNotConnected", err)
	}
}

func TestClusterConnectedAfterCloseRestoresNothing(t *testing.T) {
	ctx := t.Context()
	keeper, settings, forwards, registry := newTestKeeper(t)
	_, _ = settings.Update(ctx, func(s *domain.Settings) error {
		c := s.Cluster("prod")
		c.KeptForwards = []domain.KeptForward{{Namespace: "web", RemotePort: 80, LocalPort: 18080,
			Target: domain.ForwardTarget{Kind: domain.ForwardToPod, Name: "x"}}}
		s.Clusters["prod"] = c
		return nil
	})
	cluster := mustCluster(t, "prod", false)
	registry.Open(cluster)

	keeper.Close()
	keeper.ClusterConnected("prod")
	keeper.Close() // waits; a second Close is safe

	if n := len(forwards.ListPortForwards()); n != 0 {
		t.Fatalf("a restore started %d forwards after Close", n)
	}
}

// A restore in flight across a Disconnect must start nothing: it would put a
// forward behind the sweep and rebuild a client for a closed tab. A reconnect
// straight after must restore again.
func TestDisconnectDuringARestoreStartsNothingAndAReconnectRestores(t *testing.T) {
	ctx := t.Context()
	keeper, settings, forwards, registry := newTestKeeper(t)
	_, _ = settings.Update(ctx, func(s *domain.Settings) error {
		c := s.Cluster("prod")
		c.KeptForwards = []domain.KeptForward{{Namespace: "web", RemotePort: 80, LocalPort: 18080,
			Target: domain.ForwardTarget{Kind: domain.ForwardToPod, Name: "x"}}}
		s.Clusters["prod"] = c
		return nil
	})
	registry.Open(mustCluster(t, "prod", false))

	forwards.entered, forwards.block = make(chan struct{}, 1), make(chan struct{})
	keeper.ClusterConnected("prod")
	<-forwards.entered // the restore is now inside its slow target lookup

	registry.Close("prod") // what Disconnect does first
	done := make(chan struct{})
	go func() { keeper.ClusterDisconnected("prod"); close(done) }()
	close(forwards.block)
	<-done

	if n := len(forwards.ListPortForwards()); n != 0 {
		t.Fatalf("%d forwards started across a disconnect, want 0", n)
	}

	forwards.mu.Lock()
	forwards.entered, forwards.block = nil, nil
	forwards.mu.Unlock()
	registry.Open(mustCluster(t, "prod", false))
	keeper.ClusterConnected("prod")

	deadline := time.Now().Add(5 * time.Second)
	for len(forwards.ListPortForwards()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := len(forwards.ListPortForwards()); n != 1 {
		t.Fatalf("%d forwards after reconnecting, want 1", n)
	}
}
