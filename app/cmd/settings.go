package cmd

import (
	"context"
	"log/slog"
	"path/filepath"

	historystore "github.com/podsteer/podsteer/app/adapters/history"
	settingsstore "github.com/podsteer/podsteer/app/adapters/settings"
	"github.com/podsteer/podsteer/app/domain"
)

// This file is the one place either composition root builds the settings
// store, so that the window and `podsteer mcp` cannot end up reading two
// different files or disagreeing about what gets adopted.
//
// The ONE difference between them is the read-only flag, and it is the
// argument rather than a branch inside, so the difference is visible at both
// call sites.

// openSettings builds the backend settings store.
//
// NEVER FATAL. A machine whose configuration directory cannot be located gets
// a store over the defaults and a warning; refusing to start a Kubernetes
// client because a settings file could not be found would be an absurd trade.
// The store itself is equally forgiving about the file — see its Open.
func openSettings(readOnly bool, logger *slog.Logger) *settingsstore.Store {
	path, err := settingsstore.DefaultPath()
	if err != nil {
		logger.Warn("settings will not be saved", slog.String("error", err.Error()))
		return nil
	}

	// THE ONE PLACE THAT KNOWS BOTH PATHS. The store resolves its own location
	// from the user configuration directory and knows nothing about where the
	// history lives; the pre-0.3 `history.json` sat beside the history
	// directory. Joining those two facts is a composition concern, which is
	// why the legacy path is passed in rather than derived inside the store —
	// see the settings package comment for why that dependency must not run
	// the other way.
	adoptFrom := ""
	if historyDir, err := historystore.DefaultDir(); err == nil {
		adoptFrom = filepath.Join(filepath.Dir(historyDir), "history.json")
	}

	store, err := settingsstore.Open(settingsstore.Options{
		Path:      path,
		ReadOnly:  readOnly,
		AdoptFrom: adoptFrom,
		Logger:    logger,
	})
	if err != nil {
		logger.Warn("settings will not be saved", slog.String("error", err.Error()))
		return nil
	}
	return store
}

// kubeconfigSources adapts the store to the callback the Kubernetes adapter
// takes.
//
// A FUNCTION RATHER THAN A SNAPSHOT, so a source added in Settings is in the
// loading precedence for the next resolution without a restart — the same
// property the kubeconfig directory already has. A nil store yields nil, which
// the adapter reads as "no in-app sources".
func kubeconfigSources(store *settingsstore.Store) func() []domain.KubeconfigSource {
	if store == nil {
		return nil
	}
	return func() []domain.KubeconfigSource {
		settings, err := store.Load(context.Background())
		if err != nil {
			return nil
		}
		return settings.Kubeconfig.Sources
	}
}
