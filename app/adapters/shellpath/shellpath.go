// Package shellpath recovers the PATH a desktop application was not given.
//
// THE PROBLEM. A macOS .app launched from Finder, the Dock or `open` inherits
// launchd's environment, not a shell's. On a stock machine `launchctl getenv
// PATH` is empty, so the process gets /usr/bin:/bin:/usr/sbin:/sbin — which
// contains neither Homebrew's /opt/homebrew/bin nor a Google Cloud SDK under
// the home directory. A .desktop launch on Linux has the same shape.
//
// WHY PODSTEER CARES. Kubernetes credential plugins are executables named in
// the kubeconfig and run by client-go through exec.LookPath. Every EKS context
// runs `aws eks get-token`; GKE runs `gke-gcloud-auth-plugin`; AKS runs
// `kubelogin`. None of them are in launchd's PATH, so connecting to a managed
// cluster fails with `executable file not found in $PATH` — from a binary the
// operator can run in their terminal, which makes it look like PodSteer cannot
// reach the cluster rather than cannot find a program.
//
// The same application started with `make run` works, because a terminal
// passes its own environment down. That difference is why this went unnoticed:
// development and a Homebrew install are not the same launch.
//
// WHAT THIS DOES. Runs the operator's login shell once, asks it what PATH it
// would have, and adopts the answer. This is what every GUI application that
// shells out ends up doing; there is no API for "the PATH a terminal would
// have" because the answer is defined by files the shell reads.
package shellpath

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// probeTimeout bounds the shell.
//
// A login shell runs the operator's own startup files, and those can block —
// on a network mount, on a version manager reaching for an update, on
// something waiting for input that will never come. Failing to enrich PATH
// must never stop the window from opening, so this gives up and carries on.
const probeTimeout = 3 * time.Second

// sentinel brackets the value in the shell's output.
//
// NEEDED BECAUSE THE SHELL IS INTERACTIVE. PATH is set in ~/.zshrc far more
// often than in ~/.zprofile, and only an interactive shell reads it — but an
// interactive shell also prints whatever the operator's configuration prints:
// version-manager notices, greetings, a fortune. Taking the last line would
// work until somebody's prompt hook wrote to stdout.
const sentinel = "__PODSTEER_PATH__"

// systemDefault is the PATH a process gets when nothing set one.
//
// Matches launchd's fallback, and _PATH_DEFPATH on Linux.
var systemDefault = map[string]bool{
	"/usr/bin": true, "/bin": true, "/usr/sbin": true, "/sbin": true,
	"/usr/local/bin": true,
}

// Resolve enriches the process PATH from the operator's login shell, and
// reports what it did for the log.
//
// Called once, from the composition root, before anything builds a Kubernetes
// client. Never returns an error: every failure is a reason to keep the PATH
// we already have.
func Resolve(ctx context.Context) string {
	if runtime.GOOS == "windows" {
		// Windows takes PATH from the registry and a GUI process inherits it
		// the same as a console one. There is no shell to ask.
		return "not needed on windows"
	}

	if !looksInherited(os.Getenv("PATH")) {
		return "already inherited from a shell"
	}

	resolved, err := fromLoginShell(ctx)
	if err != nil {
		return fmt.Sprintf("kept the inherited PATH: %v", err)
	}

	added := missingFrom(os.Getenv("PATH"), resolved)
	if len(added) == 0 {
		return "the login shell added nothing"
	}

	if err := os.Setenv("PATH", resolved); err != nil {
		return fmt.Sprintf("kept the inherited PATH: %v", err)
	}
	return fmt.Sprintf("adopted the login shell's PATH, adding %s", strings.Join(added, ", "))
}

// LoginShell names the shell the operator actually uses.
//
// The same $SHELL this package already asks for PATH, exported so the local
// terminal opens the shell somebody configured rather than a shell PodSteer
// chose. Empty when there is nothing to go on, which the caller decides what
// to do about — this package has no opinion about a machine with no $SHELL,
// and inventing one here would put the fallback in two places.
func LoginShell() string {
	return os.Getenv("SHELL")
}

// looksInherited reports whether PATH looks like the bare system default,
// which is the signal that no shell handed it down.
//
// CONSERVATIVE ON PURPOSE. An operator who launched from a terminal, or who
// set PODSTEER_* and a PATH deliberately, must keep exactly what they set —
// overriding it would make the application's behaviour depend on a login shell
// they were not using. So this only fires when every entry is one the system
// would have supplied by itself.
func looksInherited(current string) bool {
	if current == "" {
		return true
	}
	for _, entry := range filepath.SplitList(current) {
		if entry != "" && !systemDefault[entry] {
			return false
		}
	}
	return true
}

// fromLoginShell asks the operator's shell what PATH it would have.
func fromLoginShell(ctx context.Context) (string, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return "", fmt.Errorf("no $SHELL to ask")
	}

	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	// -i as well as -l: PATH lives in ~/.zshrc and ~/.bashrc at least as often
	// as in the profile files, and only an interactive shell reads those.
	cmd := exec.CommandContext(ctx, shell, "-ilc", "printf '%s%s%s' "+sentinel+" \"$PATH\" "+sentinel)

	// Stdin closed rather than inherited, so a startup file that reads from it
	// gets EOF and returns instead of hanging until the timeout.
	cmd.Stdin = nil
	// The shell's own chatter goes nowhere. It is not diagnostic — it is
	// somebody's prompt configuration — and mixing it into our log would be
	// noise on every start.
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("running %s: %w", shell, err)
	}

	value, err := betweenSentinels(out.String())
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s reported an empty PATH", shell)
	}
	return value, nil
}

// betweenSentinels extracts the value the shell was asked to print.
func betweenSentinels(output string) (string, error) {
	start := strings.Index(output, sentinel)
	if start < 0 {
		return "", fmt.Errorf("the shell printed no PATH")
	}
	rest := output[start+len(sentinel):]

	end := strings.Index(rest, sentinel)
	if end < 0 {
		return "", fmt.Errorf("the shell's output was truncated")
	}
	return rest[:end], nil
}

// missingFrom names the directories in resolved that current does not have,
// so the log says what was actually gained rather than printing both PATHs.
func missingFrom(current, resolved string) []string {
	have := make(map[string]bool)
	for _, entry := range filepath.SplitList(current) {
		have[entry] = true
	}

	var added []string
	for _, entry := range filepath.SplitList(resolved) {
		if entry != "" && !have[entry] {
			added = append(added, entry)
			have[entry] = true
		}
	}
	return added
}
