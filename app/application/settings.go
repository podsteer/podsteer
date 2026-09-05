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
