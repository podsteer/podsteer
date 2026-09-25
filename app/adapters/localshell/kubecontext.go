package localshell

import (
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

// overlayDirPrefix names the per-session directory the overlay lives in, so a
// stray one left by a crash is recognisable as PodSteer's rather than as
// somebody's stray kubeconfig.
const overlayDirPrefix = "podsteer-kubecontext-"

// overlayFileName is what the overlay is called inside that directory.
//
// `config`, because the one thing an operator will do with this path is cat it
// after seeing it in KUBECONFIG, and a file called config in a directory
// called podsteer-kubecontext-… explains itself.
const overlayFileName = "config"

// contextOverlay is the ENTIRE kubeconfig PodSteer writes for a local shell.
//
// THREE FIELDS, AND THE ABSENT ONES ARE THE POINT. There are no clusters, no
// users, no contexts and no credentials in it — only the name of a context
// defined in the operator's own files, which sit after this one in KUBECONFIG.
// client-go merges the list and keeps the FIRST definition of anything, so a
// `current-context` here wins over the one in their kubeconfig while every
// cluster, user and context still comes from theirs: the context resolves in
// full, namespace included, and their file is never opened for writing.
//
// This is the fourth option the pinning decision originally missed. It costs
// none of what the three refused ones cost — their kubeconfig is untouched, no
// credential is copied anywhere, and their startup files are not replaced —
// because nothing in this document is a secret. It names a context; it does
// not describe one.
type contextOverlay struct {
	// JSON tags rather than YAML ones: sigs.k8s.io/yaml marshals through
	// encoding/json, which is also how client-go reads a kubeconfig back, so
	// these are the same field names at both ends.
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	// CurrentContext is quoted by the marshaller, which is why this is not a
	// fmt.Sprintf: a context name is whatever a cloud provider generated, and
	// an arn: or a colon-bearing GKE name would end a hand-built YAML line in
	// the middle and produce a file kubectl refuses to parse at all.
	CurrentContext string `json:"current-context"`
}

// writeContextOverlay writes the overlay selecting kubeContext and returns the
// directory holding it and the path to hand to KUBECONFIG.
//
// The directory is the unit that gets removed, not the file: os.MkdirTemp
// makes it 0700, so the overlay is unreadable to other accounts on the machine
// before it exists rather than for the instant between creating it and
// chmod-ing it, and one RemoveAll at the end of a session leaves nothing —
// including anything a shell may have written beside it, which is exactly what
// `kubectl config use-context` in that shell does.
func writeContextOverlay(kubeContext string) (dir, path string, err error) {
	dir, err = os.MkdirTemp("", overlayDirPrefix)
	if err != nil {
		return "", "", fmt.Errorf("making a directory for the context overlay: %w", err)
	}

	body, err := yaml.Marshal(contextOverlay{
		APIVersion:     "v1",
		Kind:           "Config",
		CurrentContext: kubeContext,
	})
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", "", fmt.Errorf("rendering the context overlay: %w", err)
	}

	path = filepath.Join(dir, overlayFileName)
	// 0600 as well as the 0700 directory. Belt and braces rather than a live
	// hole — the file holds no secret — but every other file this application
	// writes is 0600 and a reader who finds one that is not will reasonably
	// wonder what makes it different.
	if err := os.WriteFile(path, body, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", fmt.Errorf("writing the context overlay: %w", err)
	}
	return dir, path, nil
}

// removeContextOverlay deletes one session's overlay directory.
//
// CALLED WHEREVER THE SESSION RECORD IS DROPPED, and nowhere else, because the
// rule the port-forward and node-shell registries follow applies here too: the
// record and the thing it names are created and destroyed together. A file
// left in the temp directory after its shell has gone is the same class of
// leak as a goroutine nobody stops — nothing breaks today, and by the hundredth
// session there are a hundred of them.
//
// Best effort, and it says so in the log rather than failing anything: the
// caller is either retiring a session or shutting the application down, and
// neither has anywhere to return an error to.
func removeContextOverlay(dir string) error {
	if dir == "" {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("removing the context overlay: %w", err)
	}
	return nil
}
