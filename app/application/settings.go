package application

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// This file is the use-case layer over the settings the Go process owns.
//
// It is thin on purpose. Every method is one read-modify-write handed to the
// store, which does the locking, the validation and the atomic write; the only
// judgement here is what a change MEANS — that adding a source already listed
// is not an error, that removing one that is not there succeeds, and that a
// move is clamped to the ends of the list rather than refused. Those are
// decisions about the operator's intent, which is exactly what belongs in a
// use case and nowhere else.

// SettingsServiceDeps are the collaborators the settings service needs.
type SettingsServiceDeps struct {
	// Settings is the store. Required.
	Settings ports.SettingsPort
	// Kubeconfig reports the composed loading list. Required: the pane's
	// whole point is showing what the sources actually contributed, and only
	// the thing that performs the merge can say.
	Kubeconfig ports.KubeconfigPort
	// Reconnect releases every cached client, so a transport-level setting
	// takes effect on the clusters already open rather than only on the ones
	// opened afterwards. Optional; without it a proxy change applies to new
	// connections only.
	//
	// A FUNCTION RATHER THAN THE Invalidators SLICE, because this service has
	// no business knowing which clusters are open — the composition root
	// does, and it already holds both the registry and the invalidators.
	Reconnect func()
	// Logger receives diagnostics. Optional; defaults to slog.Default.
	Logger *slog.Logger
}

// SettingsService is the use-case surface for the backend-owned settings.
type SettingsService struct {
	settings   ports.SettingsPort
	kubeconfig ports.KubeconfigPort
	reconnect  func()
	logger     *slog.Logger
}

var _ ports.SettingsService = (*SettingsService)(nil)

// NewSettingsService validates deps and returns the service.
func NewSettingsService(deps SettingsServiceDeps) (*SettingsService, error) {
	switch {
	case deps.Settings == nil:
		return nil, errors.New("application: SettingsService requires a SettingsPort")
	case deps.Kubeconfig == nil:
		return nil, errors.New("application: SettingsService requires a KubeconfigPort")
	}

	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &SettingsService{
		settings:   deps.Settings,
		kubeconfig: deps.Kubeconfig,
		reconnect:  deps.Reconnect,
		logger:     logger.With(slog.String("service", "settings")),
	}, nil
}

// State reports where the settings live and whether they can be written.
func (s *SettingsService) State(context.Context) (domain.SettingsState, error) {
	return s.settings.State(), nil
}

// KubeconfigSources reports the composed loading list, in precedence order.
func (s *SettingsService) KubeconfigSources(ctx context.Context) ([]domain.KubeconfigEntry, error) {
	return s.kubeconfig.KubeconfigSources(ctx)
}

// AddKubeconfigSource appends a file or folder to the operator's own list.
//
// APPENDED, never inserted at the front. Precedence is the whole reason order
// matters here, and the environment's entries come first by construction — see
// the adapter — so a new source starts where it can shadow nothing.
func (s *SettingsService) AddKubeconfigSource(
	ctx context.Context,
	source domain.KubeconfigSource,
) error {
	_, err := s.settings.Update(ctx, func(settings *domain.Settings) error {
		// Adding one already listed is not an error: the operator asked for
		// that path to be a source, and it is. Refusing would mean an error
		// dialog for a state that already matches what they wanted.
		for _, existing := range settings.Kubeconfig.Sources {
			if existing.Path == source.Path {
				return nil
			}
		}
		settings.Kubeconfig.Sources = append(settings.Kubeconfig.Sources, source)
		return nil
	})
	if err != nil {
		return err
	}

	s.logger.Info("kubeconfig source added",
		slog.String("path", source.Path), slog.String("kind", string(source.Kind)))
	return nil
}

// RemoveKubeconfigSource drops the source with the given path.
//
// Removing one that is not there succeeds. The caller asked for a list without
// that path and gets one; a not-found error would only ever surface as a
// message about a row somebody had already deleted in another window.
func (s *SettingsService) RemoveKubeconfigSource(ctx context.Context, path string) error {
	_, err := s.settings.Update(ctx, func(settings *domain.Settings) error {
		settings.Kubeconfig.Sources = slices.DeleteFunc(
			settings.Kubeconfig.Sources,
			func(source domain.KubeconfigSource) bool { return source.Path == path },
		)
		return nil
	})
	if err != nil {
		return err
	}

	s.logger.Info("kubeconfig source removed", slog.String("path", path))
	return nil
}

// MoveKubeconfigSource shifts a source by delta places, clamped to the ends.
//
// Clamped rather than refused: the control is a pair of arrows, and the top
// row's "up" is a press that should do nothing rather than raise an error.
func (s *SettingsService) MoveKubeconfigSource(ctx context.Context, path string, delta int) error {
	_, err := s.settings.Update(ctx, func(settings *domain.Settings) error {
		sources := settings.Kubeconfig.Sources
		from := slices.IndexFunc(sources, func(source domain.KubeconfigSource) bool {
			return source.Path == path
		})
		if from < 0 || delta == 0 {
			return nil
		}

		to := min(max(from+delta, 0), len(sources)-1)
		if to == from {
			return nil
		}

		moved := sources[from]
		sources = slices.Delete(sources, from, from+1)
		settings.Kubeconfig.Sources = slices.Insert(sources, to, moved)
		return nil
	})
	if err != nil {
		return err
	}

	s.logger.Info("kubeconfig source moved",
		slog.String("path", path), slog.Int("by", delta))
	return nil
}

// Cluster reports one cluster's per-cluster switches, defaults included.
//
// A READ THAT CANNOT FAIL TO ANSWER. domain.Settings.Cluster is total, so a
// cluster nobody has configured reports the defaults rather than an absence
// the caller would have to interpret. The error return is the store's, not
// this decision's.
func (s *SettingsService) Cluster(
	ctx context.Context,
	id domain.ClusterID,
) (domain.ClusterSettings, error) {
	settings, err := s.settings.Load(ctx)
	if err != nil {
		return domain.ClusterSettings{}, err
	}
	return settings.Cluster(id), nil
}

// Proxy reports the proxy in force.
func (s *SettingsService) Proxy(ctx context.Context) (domain.ProxySettings, error) {
	settings, err := s.settings.Load(ctx)
	if err != nil {
		return domain.ProxySettings{}, err
	}
	return settings.Proxy, nil
}

// SetProxy records the proxy PodSteer's own outbound calls go through, and
// rebuilds every open cluster's client so the change takes effect now.
//
// THE REBUILD IS THE HALF THAT IS EASY TO FORGET. A client-go client captures
// its transport when it is built, so a proxy written without releasing the
// cached clients would apply to clusters opened afterwards and to nothing the
// operator is currently looking at — which reads exactly like the setting not
// working, and is worse than that: some tabs on the new route and some on the
// old, with nothing on screen saying which.
//
// REFUSED RATHER THAN NORMALISED, on Validate's side of the line: a bad value
// arriving here came from the interface, and writing it would persist a bug in
// the interface. A URL carrying credentials is refused outright — see
// ErrSettingsProxyCredential — because a proxy password in a settings file is
// a credential PodSteer would have put on disk, which nothing else in this
// application does.
func (s *SettingsService) SetProxy(ctx context.Context, proxy domain.ProxySettings) error {
	if _, err := proxy.Dialer(); err != nil {
		return err
	}

	if _, err := s.settings.Update(ctx, func(settings *domain.Settings) error {
		settings.Proxy = proxy
		return nil
	}); err != nil {
		return err
	}

	s.logger.InfoContext(ctx, "proxy changed",
		slog.String("mode", string(proxy.Mode)),
		// The URL is a host somebody typed, not a credential — the write path
		// refuses one carrying userinfo — and knowing which proxy is in force
		// is the first question when a cluster stops answering.
		slog.String("url", proxy.URL),
		slog.Bool("has_exceptions", strings.TrimSpace(proxy.NoProxy) != ""))

	if s.reconnect != nil {
		s.reconnect()
	}
	return nil
}

// SetMetricsQuery records ADR 7's per-cluster value: whether a discovered
// monitoring backend may be queried, which one answers, and on what terms.
//
// NOTHING HERE SENDS A QUERY. This writes the switch; MetricsQueryService
// reads it before discovery runs, before the node list, and before anything
// reaches the network — so a cluster left off makes no request at all rather
// than one whose answer is discarded, which metricsquery_test.go asserts by
// counting requests.
//
// A value equal to the defaults leaves no stanza behind: the store's Normalise
// drops an entry that says nothing, so turning this back off removes the
// cluster's entry — and with it any monitoring Service name it held — rather
// than leaving a section behind that names a context for no reason.
func (s *SettingsService) SetMetricsQuery(
	ctx context.Context,
	id domain.ClusterID,
	query domain.MetricsQuerySettings,
) error {
	if id == "" {
		return errors.New("application: SetMetricsQuery needs a cluster")
	}

	_, err := s.settings.Update(ctx, func(settings *domain.Settings) error {
		if settings.Clusters == nil {
			settings.Clusters = map[string]domain.ClusterSettings{}
		}
		// READ-MODIFY-WRITE OF THE WHOLE ENTRY, so that setting the metrics
		// query cannot clear a node-history opt-in the history service wrote
		// into the same entry. Both features share this section by design;
		// each must write only its own half.
		entry := settings.Cluster(id)
		entry.MetricsQuery = query
		settings.Clusters[string(id)] = entry
		return nil
	})
	if err != nil {
		return err
	}

	// THE MODE AND THE POLICY, NEVER THE PICK. The preferred backend is the
	// one object name this feature holds, and a log line is a second place it
	// would land — in a terminal, in a support bundle — for no diagnostic
	// value the mode does not already carry.
	s.logger.Info("metrics query settings changed",
		slog.String("cluster", string(id)),
		slog.String("mode", string(query.Mode)),
		slog.String("fleetPolicy", string(query.Fleet)),
		slog.Bool("backendChosen", !query.Preferred.IsZero()))
	return nil
}
