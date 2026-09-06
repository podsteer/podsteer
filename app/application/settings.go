package application

import (
	"context"
	"errors"
	"log/slog"
	"slices"

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
	// Logger receives diagnostics. Optional; defaults to slog.Default.
	Logger *slog.Logger
}

// SettingsService is the use-case surface for the backend-owned settings.
type SettingsService struct {
	settings   ports.SettingsPort
	kubeconfig ports.KubeconfigPort
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
