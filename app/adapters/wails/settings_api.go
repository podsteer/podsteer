package wails

import (
	"errors"
	"log/slog"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// This file is the frontend's view of the settings the GO PROCESS owns — not
// the ones the interface keeps for itself in the webview's own storage, and
// not the exported settings document either.
//
// The surface is deliberately narrow, and its narrowness is the safeguard.
// There is no "write these settings" method: each call changes one thing, so
// there is no shape here that could carry an object name or a credential into
// a file PodSteer writes. See SECURITY.md.

// SettingsState is where the settings live and whether they can be saved.
type SettingsState struct {
	// Path is the settings file, whether or not it exists yet.
	Path string `json:"path"`
	// Writable reports that a change made now would reach the disk. When it
	// is false, Notice says why in one sentence.
	Writable bool `json:"writable"`
	// Notice is the one line the pane shows when anything is not ordinary:
	// the file is from a newer PodSteer, could not be read, or held values
	// that fell back to their defaults. Empty when there is nothing to say.
	//
	// COMPOSED IN GO rather than assembled from flags in the interface,
	// because the sentence is the whole of what an operator can act on and
	// splitting it across two layers is how it ends up saying two things.
	Notice string `json:"notice"`
}

// KubeconfigSource is one entry of the composed loading list.
type KubeconfigSource struct {
	// Path is the file or folder.
	Path string `json:"path"`
	// Kind is "file" or "directory".
	Kind string `json:"kind"`
	// Origin is "default", "environment" or "settings". Only a settings entry
	// may be removed or reordered; the other two are shown as read-only rows,
	// because nothing in this application can change an environment variable
	// or the operator's own $KUBECONFIG.
	Origin string `json:"origin"`
	// Editable is Origin == "settings", carried explicitly so the interface
	// never has to know which origins those are.
	Editable bool `json:"editable"`
	// Missing reports that nothing is at Path right now. The row stays.
	Missing bool `json:"missing"`
	// Files are the kubeconfig files this entry contributed.
	Files []string `json:"files"`
	// Contexts are the context names defined in those files.
	Contexts []string `json:"contexts"`
	// ShadowedBy maps a context name this entry defines to the path of the
	// entry that actually won it — empty for every context this entry
	// provides.
	//
	// COMPUTED HERE, once, rather than in the interface: it is a statement
	// about client-go's merge (the first file's definition of a name wins),
	// and that rule belongs beside the code that composes the precedence
	// rather than in a component that would have to re-derive it.
	ShadowedBy map[string]string `json:"shadowedBy"`
}

// SettingsAPI exposes the backend-owned settings to the frontend.
type SettingsAPI struct {
	settings ports.SettingsService
	app      *App
	logger   *slog.Logger
}

// NewSettingsAPI returns the bound settings API.
func NewSettingsAPI(settings ports.SettingsService, app *App, logger *slog.Logger) (*SettingsAPI, error) {
	switch {
	case settings == nil:
		return nil, errors.New("wails: SettingsAPI requires a SettingsService")
	case app == nil:
		return nil, errors.New("wails: SettingsAPI requires an App")
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &SettingsAPI{
		settings: settings,
		app:      app,
		logger:   logger.With(slog.String("api", "settings")),
	}, nil
}

// GetState reports where the settings live and whether they can be saved.
func (s *SettingsAPI) GetState() (SettingsState, error) {
	ctx, cancel := s.app.requestContext()
	defer cancel()

	state, err := s.settings.State(ctx)
	if err != nil {
		return SettingsState{}, apiError(s.logger, "GetState", err)
	}

	return SettingsState{
		Path:     state.Path,
		Writable: state.IsWritable(),
		Notice:   settingsNotice(state),
	}, nil
}

// settingsNotice composes the one line the pane shows.
//
// Ordered by what an operator most needs to act on: a refusal to save first,
// then a file that was set aside, then values that were repaired. Only one
// line is ever produced — a pane that stacks three warnings about the same
// file gets read as broken rather than as informative.
func settingsNotice(state domain.SettingsState) string {
	switch {
	case state.FromFuture:
		return "This settings file was written by a newer version of PodSteer. " +
			"PodSteer is using what it understands and will not save over it, so changes made here will not persist. " +
			"Upgrade PodSteer, or move the file aside to start again from the defaults."
	case state.ReadOnly && state.Path == "":
		return "PodSteer could not find a configuration directory on this machine, so settings are not saved between launches."
	case state.ReadOnly:
		return "This PodSteer is not saving settings."
	case state.Unreadable:
		return "The settings file could not be read, so the defaults are in use. " +
			"It will be renamed with an .invalid suffix rather than overwritten the first time a setting is saved."
	case state.Repaired > 0:
		return "Some settings held values PodSteer could not use and fell back to their defaults."
	default:
		return ""
	}
}

// GetKubeconfigSources reports the composed loading list, in precedence order.
func (s *SettingsAPI) GetKubeconfigSources() ([]KubeconfigSource, error) {
	ctx, cancel := s.app.requestContext()
	defer cancel()

	entries, err := s.settings.KubeconfigSources(ctx)
	if err != nil {
		return nil, apiError(s.logger, "GetKubeconfigSources", err)
	}
	return toKubeconfigSources(entries), nil
}

// toKubeconfigSources converts the report and works out what is shadowed.
//
// The result is always non-nil so it marshals to [] rather than null.
func toKubeconfigSources(entries []domain.KubeconfigEntry) []KubeconfigSource {
	// winner maps a context name to the path of the FIRST entry that defined
	// it, which is the one client-go's merge keeps.
	winner := make(map[string]string)

	out := make([]KubeconfigSource, 0, len(entries))
	for _, entry := range entries {
		source := KubeconfigSource{
			Path:       entry.Path,
			Kind:       string(entry.Kind),
			Origin:     string(entry.Origin),
			Editable:   entry.Origin.IsEditable(),
			Missing:    entry.Missing,
			Files:      nonNil(entry.Files),
			Contexts:   nonNil(entry.Contexts),
			ShadowedBy: map[string]string{},
		}

		for _, name := range entry.Contexts {
			if won, taken := winner[name]; taken {
				source.ShadowedBy[name] = won
				continue
			}
			winner[name] = entry.Path
		}

		out = append(out, source)
	}
	return out
}

// nonNil returns an empty slice rather than nil, so the bindings' `T[] | null`
// never reaches a view that would have to check.
func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// AddKubeconfigFile adds a single kubeconfig file to the operator's sources.
func (s *SettingsAPI) AddKubeconfigFile(path string) error {
	return s.add(path, domain.SourceFile, "AddKubeconfigFile")
}

// AddKubeconfigFolder adds a folder of kubeconfig files.
//
// Scanned by the same code PODSTEER_KUBECONFIG_DIR is, so what it contributes
// is decided the same way: dotfiles, subdirectories and anything that does not
// parse as a kubeconfig are skipped.
func (s *SettingsAPI) AddKubeconfigFolder(path string) error {
	return s.add(path, domain.SourceDirectory, "AddKubeconfigFolder")
}

func (s *SettingsAPI) add(path string, kind domain.SourceKind, op string) error {
	ctx, cancel := s.app.requestContext()
	defer cancel()

	source := domain.KubeconfigSource{Path: path, Kind: kind}
	if err := s.settings.AddKubeconfigSource(ctx, source); err != nil {
		return apiError(s.logger, op, err)
	}
	return nil
}

// RemoveKubeconfigSource drops one of the operator's own sources.
//
// It removes an ENTRY, never a file: nothing on disk is touched, and the
// kubeconfig it named is exactly as it was.
func (s *SettingsAPI) RemoveKubeconfigSource(path string) error {
	ctx, cancel := s.app.requestContext()
	defer cancel()

	if err := s.settings.RemoveKubeconfigSource(ctx, path); err != nil {
		return apiError(s.logger, "RemoveKubeconfigSource", err)
	}
	return nil
}

// MoveKubeconfigSource shifts one of the operator's sources by delta places.
//
// Order is precedence, so this decides which of two sources defining the same
// context name wins. A move past either end is clamped rather than refused.
func (s *SettingsAPI) MoveKubeconfigSource(path string, delta int) error {
	ctx, cancel := s.app.requestContext()
	defer cancel()

	if err := s.settings.MoveKubeconfigSource(ctx, path, delta); err != nil {
		return apiError(s.logger, "MoveKubeconfigSource", err)
	}
	return nil
}
