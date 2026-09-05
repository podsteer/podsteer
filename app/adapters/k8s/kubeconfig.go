package k8s

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// Clusters returns every cluster described by the local kubeconfig.
//
// The file is re-read on every call rather than cached. It is a few kilobytes,
// this runs only when the cluster picker opens, and kubeconfigs are edited
// under a running client all the time — `kubectl config use-context`, a cloud
// CLI adding a freshly provisioned cluster, a token refresh rewriting the
// file. A cache here would show the operator a stale list.
func (a *Adapter) Clusters(ctx context.Context) ([]domain.Cluster, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	raw, err := a.factory.rawConfig()
	if err != nil {
		return nil, err
	}

	clusters := make([]domain.Cluster, 0, len(raw.Contexts))
	skipped := 0
	for name, kubeContext := range raw.Contexts {
		cluster, err := a.toCluster(name, kubeContext, raw)
		if err != nil {
			// A kubeconfig accumulates dead entries — a context pointing at a
			// cluster block that was removed, an endpoint that is no longer a
			// URL. Dropping the whole file over one of them would be the worst
			// possible failure mode, so skip and keep going.
			a.logger.WarnContext(ctx, "skipping unusable kubeconfig context",
				slog.String("context", name),
				slog.String("error", err.Error()))
			skipped++
			continue
		}
		clusters = append(clusters, cluster)
	}

	// Only fail when there was something to read and none of it was usable.
	// A kubeconfig with no contexts at all is an empty list, not an error:
	// that is simply a machine which has not been pointed at a cluster yet.
	if len(clusters) == 0 && skipped > 0 {
		return nil, fmt.Errorf("reading kubeconfig: %w: all %d contexts are unusable",
			ports.ErrKubeconfigUnavailable, skipped)
	}

	return clusters, nil
}

// Cluster returns the single cluster with the given id.
func (a *Adapter) Cluster(ctx context.Context, id domain.ClusterID) (domain.Cluster, error) {
	if id.IsZero() {
		return domain.Cluster{}, domain.ErrEmptyClusterID
	}
	if err := ctx.Err(); err != nil {
		return domain.Cluster{}, err
	}

	raw, err := a.factory.rawConfig()
	if err != nil {
		return domain.Cluster{}, err
	}

	kubeContext, ok := raw.Contexts[id.String()]
	if !ok {
		return domain.Cluster{}, fmt.Errorf("kubeconfig context %q: %w", id, domain.ErrClusterNotFound)
	}

	cluster, err := a.toCluster(id.String(), kubeContext, raw)
	if err != nil {
		return domain.Cluster{}, fmt.Errorf("kubeconfig context %q: %w", id, err)
	}

	return cluster, nil
}

// toCluster translates one kubeconfig context into a domain Cluster.
//
// A context is a triple of references — into the clusters, users and (loosely)
// namespaces sections — so the endpoint has to be resolved through the cluster
// block it names rather than read off the context itself.
func (a *Adapter) toCluster(name string, kubeContext *clientcmdapi.Context, raw clientcmdapi.Config) (domain.Cluster, error) {
	if kubeContext == nil {
		return domain.Cluster{}, fmt.Errorf("context %q is empty", name)
	}

	id, err := domain.NewClusterID(name)
	if err != nil {
		return domain.Cluster{}, err
	}

	entry, ok := raw.Clusters[kubeContext.Cluster]
	if !ok || entry == nil {
		return domain.Cluster{}, fmt.Errorf("context %q references unknown cluster %q",
			name, kubeContext.Cluster)
	}

	endpoint, err := domain.NewServerEndpoint(entry.Server)
	if err != nil {
		return domain.Cluster{}, err
	}

	// An unusable namespace in the context is not worth discarding the whole
	// cluster over: fall back to "no namespace pinned" and let the operator
	// pick one in the UI.
	namespace, err := domain.NewNamespaceName(kubeContext.Namespace)
	if err != nil {
		a.logger.Warn("ignoring invalid namespace in kubeconfig context",
			slog.String("context", name),
			slog.String("namespace", kubeContext.Namespace),
			slog.String("error", err.Error()))
		namespace = domain.NamespaceAll
	}

	return domain.NewCluster(domain.ClusterSpec{
		ID:               id,
		Server:           endpoint,
		DefaultNamespace: namespace,
		AuthInfo:         kubeContext.AuthInfo,
		IsCurrent:        name == raw.CurrentContext,
		// client-go stamps every context with the file it was read from when
		// it merges a precedence list, and that stamp is the ONLY record of
		// which of several files won a duplicated context name. Carried into
		// the domain so the picker can show it; it is a path on this machine,
		// which is the same class of thing the settings file already holds.
		Source: domain.KubeconfigLocation(kubeContext.LocationOfOrigin),
	})
}

// PreviewMerge reports what adding raw would change, without touching the file.
func (a *Adapter) PreviewMerge(ctx context.Context, raw string) (domain.KubeconfigMerge, error) {
	if err := ctx.Err(); err != nil {
		return domain.KubeconfigMerge{}, err
	}
	merge, _, _, err := a.planMerge(raw)
	return merge, err
}

// Merge adds raw to the kubeconfig and reports what changed.
//
// The whole method is about not destroying a file that holds credentials:
//
//   - The incoming text is parsed and the plan computed BEFORE anything is
//     opened for writing, so a paste that is not a kubeconfig cannot get as
//     far as the file.
//   - A conflict is refused. The plan says which names collide; replacing a
//     working context's credentials on a paste is not a recoverable mistake.
//   - The destination has its symlinks resolved. `~/.kube` pointing into
//     Documents or a dotfile repository is common, and writing through the
//     link rather than over it is the difference between updating a file and
//     replacing somebody's symlink with a regular file.
//   - The existing file is copied to a sibling backup first.
//   - The new content is written to a temporary file in the SAME directory,
//     synced, and renamed over the target. Rename within a directory is
//     atomic, so an interrupted write leaves the old file intact rather than
//     a half-written one; a temporary file elsewhere could not be renamed
//     across a filesystem boundary.
//   - The file's mode is preserved, defaulting to 0600. A kubeconfig that
//     becomes world-readable because a tool rewrote it is a real incident.
func (a *Adapter) Merge(ctx context.Context, raw string) (domain.KubeconfigMerge, error) {
	if err := ctx.Err(); err != nil {
		return domain.KubeconfigMerge{}, err
	}

	merge, incoming, existing, err := a.planMerge(raw)
	if err != nil {
		return domain.KubeconfigMerge{}, err
	}
	if merge.HasConflicts() {
		return merge, fmt.Errorf("adding to kubeconfig: %w: %s",
			ports.ErrKubeconfigConflict, strings.Join(merge.Conflicts, ", "))
	}

	maps.Copy(existing.Clusters, incoming.Clusters)
	maps.Copy(existing.AuthInfos, incoming.AuthInfos)
	maps.Copy(existing.Contexts, incoming.Contexts)
	// The current context is left alone. Adding a cluster is not a request to
	// switch to it, and kubectl in another terminal should not change target
	// because somebody pasted a config here.

	encoded, err := clientcmd.Write(*existing)
	if err != nil {
		return domain.KubeconfigMerge{}, fmt.Errorf("encoding kubeconfig: %w", err)
	}
	if err := writeKubeconfig(merge.Path, encoded); err != nil {
		return domain.KubeconfigMerge{}, err
	}

	a.logger.InfoContext(ctx, "added contexts to kubeconfig",
		slog.String("path", merge.Path),
		slog.Any("contexts", merge.Added))

	return merge, nil
}

// planMerge parses raw and works out what merging it would do.
//
// Returns the plan, the parsed incoming config and the existing one, so Merge
// and PreviewMerge cannot disagree about what would happen.
func (a *Adapter) planMerge(raw string) (
	domain.KubeconfigMerge, *clientcmdapi.Config, *clientcmdapi.Config, error,
) {
	if strings.TrimSpace(raw) == "" {
		return domain.KubeconfigMerge{}, nil, nil, fmt.Errorf(
			"reading pasted kubeconfig: %w: it is empty", ports.ErrKubeconfigInvalid)
	}

	incoming, err := clientcmd.Load([]byte(raw))
	if err != nil {
		return domain.KubeconfigMerge{}, nil, nil, fmt.Errorf(
			"reading pasted kubeconfig: %w: %w", ports.ErrKubeconfigInvalid, err)
	}
	if len(incoming.Contexts) == 0 {
		return domain.KubeconfigMerge{}, nil, nil, fmt.Errorf(
			"reading pasted kubeconfig: %w: it defines no contexts", ports.ErrKubeconfigInvalid)
	}

	path, err := a.writableKubeconfigPath()
	if err != nil {
		return domain.KubeconfigMerge{}, nil, nil, err
	}

	// Loaded from the destination alone, because that is the only file
	// anything below is about to WRITE to: the plan's Clusters/AuthInfos/
	// Contexts maps are copied onto this value and nothing else, never onto
	// a synthetic merge that could smuggle a directory file's entries into
	// the one file PodSteer is allowed to touch.
	existing, err := clientcmd.LoadFromFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			if hint := kubeconfigPermissionHint(path); hint != "" {
				return domain.KubeconfigMerge{}, nil, nil, fmt.Errorf(
					"reading kubeconfig: %w: %s", ports.ErrKubeconfigUnavailable, hint)
			}
			return domain.KubeconfigMerge{}, nil, nil, fmt.Errorf(
				"reading kubeconfig: %w: %w", ports.ErrKubeconfigUnavailable, err)
		}
		// A machine with no kubeconfig yet is a machine this can create one
		// for, which is the most useful moment for this feature to work.
		existing = clientcmdapi.NewConfig()
	}

	// The CONFLICT CHECK, though, is against the merged view Clusters()
	// itself reads — existing's contexts plus whatever PODSTEER_KUBECONFIG_DIR
	// contributes — because a name is exactly as unusable for a new context
	// when a directory file already defines it as when the explicit file
	// does: PodSteer would either refuse to add it or (worse) add it here
	// while the operator's own file elsewhere still wins the read, which is a
	// confusing way to discover a name was never free. Best-effort: a
	// directory that fails to read at this moment falls back to the
	// explicit file alone rather than blocking the add altogether — the same
	// leniency kubeconfigDirFiles already shows an unreadable directory.
	taken := existing.Contexts
	if merged, err := a.factory.rawConfig(); err == nil {
		taken = merged.Contexts
	}

	added := make([]string, 0, len(incoming.Contexts))
	conflicts := make([]string, 0)
	for name := range incoming.Contexts {
		if _, ok := taken[name]; ok {
			conflicts = append(conflicts, name)
		} else {
			added = append(added, name)
		}
	}

	return domain.NewKubeconfigMerge(added, conflicts, path), incoming, existing, nil
}

// writableKubeconfigPath returns the file a merge should be written to, with
// its symlinks resolved.
//
// The first entry of the precedence list, which is what kubectl itself writes
// to when $KUBECONFIG names several files.
func (a *Adapter) writableKubeconfigPath() (string, error) {
	path := a.factory.kubeconfigPath(a.factory.clientConfig(""))
	if path == "" {
		return "", fmt.Errorf("locating the kubeconfig: %w", ports.ErrKubeconfigUnavailable)
	}

	// EvalSymlinks fails on a path that does not exist yet, which is the
	// ordinary case on a machine with no kubeconfig — the unresolved path is
	// then correct by definition.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved, nil
	}
	return path, nil
}

// writeKubeconfig replaces path's contents atomically, keeping a backup.
func writeKubeconfig(path string, content []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("preparing %s: %w", dir, err)
	}

	mode := fs.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()

		// Best effort: a kubeconfig that cannot be backed up is still worth
		// adding to, and refusing would be the more surprising behaviour.
		if previous, err := os.ReadFile(path); err == nil {
			_ = os.WriteFile(path+".podsteer.bak", previous, mode)
		}
	}

	temp, err := os.CreateTemp(dir, ".kubeconfig-*.tmp")
	if err != nil {
		if hint := kubeconfigPermissionHint(path); hint != "" {
			return fmt.Errorf("writing kubeconfig: %w: %s", ports.ErrKubeconfigUnavailable, hint)
		}
		return fmt.Errorf("writing kubeconfig: %w", err)
	}
	name := temp.Name()
	defer func() { _ = os.Remove(name) }() // A no-op once the rename succeeds.

	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return fmt.Errorf("securing %s: %w", name, err)
	}
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return fmt.Errorf("writing %s: %w", name, err)
	}
	// Synced before the rename: the rename is atomic with respect to the
	// directory, but the CONTENT still has to have reached the disk or a crash
	// could leave the new name pointing at an empty file.
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("syncing %s: %w", name, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", name, err)
	}

	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}
