package k8s

import (
	"context"
	"os"
	"slices"

	"k8s.io/client-go/tools/clientcmd"

	"github.com/podsteer/podsteer/app/domain"
)

// This file answers one question for the settings pane: what is actually in
// the kubeconfig loading list, and where did each part of it come from.
//
// It is a REPORT, derived on every call from the environment plus the stored
// sources, and it writes nothing. It exists because the composition is not
// visible from any one place otherwise — the default chain is client-go's, the
// directory is an environment variable's, and the sources are the settings
// file's — and an operator who adds a folder and does not see their cluster
// has no way to find out which of those three swallowed it.

// KubeconfigSources reports the composed loading list, in precedence order.
//
// The order here IS the precedence: the explicit or default chain, then
// whatever PODSTEER_KUBECONFIG_DIR names, then the operator's own sources in
// list order. client-go keeps the first definition of a context name, so an
// entry's contexts are shadowed by any identically named context in an entry
// ABOVE it — which is what lets the pane say which file won.
//
// A path that does not exist is reported missing and kept. A folder synced
// from a password manager or a cloud drive is routinely absent for the first
// minute after a login, and a list that quietly dropped it would be a setting
// that disappears when the machine is slow.
func (a *Adapter) KubeconfigSources(ctx context.Context) ([]domain.KubeconfigEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return a.factory.kubeconfigSources(), nil
}

// kubeconfigSources builds the report.
func (f *clientFactory) kubeconfigSources() []domain.KubeconfigEntry {
	entries := make([]domain.KubeconfigEntry, 0, 4)

	// The explicit override, or the default chain — $KUBECONFIG (itself
	// possibly a path list) or ~/.kube/config. One entry per file, because
	// that is one entry per thing that can define a context.
	for _, path := range f.defaultChain() {
		entries = append(entries, f.fileEntry(path, domain.OriginDefault))
	}

	if dir := f.cfg.KubeconfigDir; dir != "" {
		entries = append(entries, f.directoryEntry(dir, domain.OriginEnvironment))
	}

	if f.cfg.Sources != nil {
		for _, source := range f.cfg.Sources() {
			if source.Kind == domain.SourceDirectory {
				entries = append(entries, f.directoryEntry(source.Path, domain.OriginSettings))
				continue
			}
			entries = append(entries, f.fileEntry(source.Path, domain.OriginSettings))
		}
	}

	return entries
}

// defaultChain returns the files the explicit override or the default
// resolution contributes, before anything is appended to them.
func (f *clientFactory) defaultChain() []string {
	if f.cfg.KubeconfigPath != "" {
		return []string{f.cfg.KubeconfigPath}
	}
	return clientcmd.NewDefaultClientConfigLoadingRules().Precedence
}

// fileEntry describes one kubeconfig file.
func (f *clientFactory) fileEntry(path string, origin domain.KubeconfigOrigin) domain.KubeconfigEntry {
	entry := domain.KubeconfigEntry{
		Path:    path,
		Kind:    domain.SourceFile,
		Origin:  origin,
		Missing: !exists(path),
	}
	if entry.Missing {
		return entry
	}
	entry.Files = []string{path}
	entry.Contexts = contextsIn(path)
	return entry
}

// directoryEntry describes one folder of kubeconfig files.
//
// Scanned by kubeconfigFilesIn, the same function the loading rules use, so
// the files listed here are exactly the files that will be read — not a second
// answer to the same question that could drift from the first.
func (f *clientFactory) directoryEntry(path string, origin domain.KubeconfigOrigin) domain.KubeconfigEntry {
	entry := domain.KubeconfigEntry{
		Path:    path,
		Kind:    domain.SourceDirectory,
		Origin:  origin,
		Missing: !exists(path),
	}
	if entry.Missing {
		return entry
	}

	entry.Files = f.kubeconfigFilesIn(path)
	for _, file := range entry.Files {
		entry.Contexts = append(entry.Contexts, contextsIn(file)...)
	}
	return entry
}

// exists reports whether anything is at path right now.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// contextsIn returns the context names one kubeconfig file defines.
//
// A file that will not parse contributes none rather than failing the report:
// the loading rules already skip it, and the pane's job here is to say what a
// source contributed — which, for an unparsable file, is nothing.
func contextsIn(path string) []string {
	config, err := clientcmd.LoadFromFile(path)
	if err != nil || config == nil {
		return nil
	}

	names := make([]string, 0, len(config.Contexts))
	for name := range config.Contexts {
		names = append(names, name)
	}
	// Sorted so the pane's rows do not reshuffle between reads: Go's map
	// iteration order is deliberately random, and a list that reorders itself
	// every few seconds is unreadable.
	slices.Sort(names)
	return names
}
