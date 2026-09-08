package localshell

import (
	"fmt"
	"os"
	"strings"

	"github.com/podsteer/podsteer/app/domain"
)

// Environment variable names this package sets on a local shell.
const (
	// EnvKubeconfig is the one variable kubectl, helm, kubectx and everything
	// else in that family agree on. It is set to the SAME files PodSteer
	// itself reads, in the same order, so a command typed in this shell sees
	// exactly the clusters the application does.
	EnvKubeconfig = "KUBECONFIG"
	// EnvContext names the cluster tab the shell was opened beside.
	//
	// INFORMATIONAL ONLY. No Kubernetes tool reads it — there is no such
	// thing as a context environment variable, which is why selecting the
	// context takes a file (see kubecontext.go) and not a variable here. It
	// exists so a prompt theme, a shell function or the operator's own alias
	// can pick the context up if they want it, and so the agent prompt has
	// one place to quote.
	EnvContext = "PODSTEER_CONTEXT"
	// EnvAgent names the coding agent this session launched, empty for a
	// plain shell.
	EnvAgent = "PODSTEER_AGENT"
	// EnvAgentReadOnly is the read-only MARKER an agent session carries.
	//
	// A marker, not a mechanism. It cannot stop anything: the credentials are
	// the operator's and an agent that ignores it can still do whatever the
	// kubeconfig allows. It is set so a wrapper, a hook or the agent's own
	// configuration can act on it, and the same request is stated in words in
	// the first prompt, which is what an agent actually reads.
	EnvAgentReadOnly = "PODSTEER_AGENT_READ_ONLY"
)

// terminalEnv is what xterm.js on the other end actually emulates.
//
// Set rather than inherited: a desktop launch has no TERM at all, and a shell
// with an empty TERM falls back to a dumb terminal — no colour, no line
// editing, no full-screen programs, which is most of the point of a PTY.
var terminalEnv = map[string]string{
	"TERM":      "xterm-256color",
	"COLORTERM": "truecolor",
}

// BuildEnv assembles the environment for one local shell.
//
// base is the PodSteer process's own environment, which by this point already
// carries the login shell's PATH — see the shellpath package for why a desktop
// launch does not have one. Everything in it is kept: this is the operator's
// machine and their environment, and dropping a variable they rely on would
// make a shell opened here behave differently to the one in their terminal.
//
// files is the precedence list to publish as KUBECONFIG, with the session's
// context overlay already at the front when there is one — this function does
// not decide that, StartLocalShell does, because the overlay has a lifetime
// and this is a pure function of its arguments.
//
// Exactly four things are overridden, and each replaces rather than appends,
// so a value already present cannot shadow the one this sets.
func BuildEnv(base []string, files []string, spec domain.LocalShellSpec) []string {
	overrides := map[string]string{}
	for name, value := range terminalEnv {
		overrides[name] = value
	}

	// An empty list means no kubeconfig was resolvable at all, in which case
	// leaving whatever the operator had beats setting KUBECONFIG to nothing
	// and hiding the clusters their own shell can see.
	if len(files) > 0 {
		overrides[EnvKubeconfig] = strings.Join(files, string(os.PathListSeparator))
	}
	overrides[EnvContext] = spec.Context
	overrides[EnvAgent] = spec.Agent

	// Present only when asked for. An unset variable and one set to "0" are
	// the same intent, and a wrapper checking for presence should not see a
	// marker on a session that did not ask for one.
	if spec.Agent != "" && spec.ReadOnly {
		overrides[EnvAgentReadOnly] = "1"
	}

	out := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			// Not a NAME=VALUE pair. Carried through rather than dropped —
			// it is not this function's business to tidy the environment.
			out = append(out, entry)
			continue
		}
		if _, replaced := overrides[name]; replaced {
			continue
		}
		// A stale marker inherited by PodSteer's own process must not leak
		// into a session that did not ask for one.
		if name == EnvAgentReadOnly {
			continue
		}
		out = append(out, entry)
	}

	// Sorted by name so the result is comparable in a test and stable in a
	// log; the environment is a set, and the order of these four carries no
	// meaning.
	for _, name := range sortedKeys(overrides) {
		out = append(out, name+"="+overrides[name])
	}
	return out
}

// sortedKeys returns the map's keys in a fixed order.
func sortedKeys(m map[string]string) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	// Insertion sort: four entries, and a sort package call here would be
	// noise beside the thing it orders.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return names
}

// ContextNotice is the one line printed into the shell before its first
// prompt, saying which context PodSteer selected for it — or, when the overlay
// could not be written, which one it would have.
//
// HOW THE CONTEXT IS SELECTED, AND WHY THAT IS NOT A KUBECONFIG WRITE. kubectl
// takes a context from `current-context` in the MERGED kubeconfig or from an
// explicit --context flag, and from nothing else: no environment variable
// carries one, whatever the shape of KUBECONTEXT suggests. That merge is the
// way in. PodSteer writes a kubeconfig containing nothing but
// `current-context` and puts it FIRST in the KUBECONFIG list — see
// kubecontext.go — so the merged document's current-context is the open tab's
// while every cluster, user and context in it still comes from the operator's
// own files. Their kubeconfig is opened for reading and never for writing.
//
// The three options this decision originally enumerated are still refused, and
// the overlay costs none of what they cost:
//
//   - Writing current-context in the operator's own kubeconfig. Refused
//     outright — see the kubeconfig section of CLAUDE.md. A shell opened in
//     this window must not change what kubectl targets in the terminal next
//     to it. The overlay changes nothing outside this session.
//   - Writing a per-session kubeconfig for the shell to point at. That was
//     refused as a copy of somebody's CREDENTIALS on disk, and it still is.
//     The overlay is not one: it has no clusters and no users in it, so there
//     is nothing in the file to leak.
//   - Injecting a shell alias. Every shell reads its aliases out of a file,
//     and the two ways in — bash's --rcfile and zsh's ZDOTDIR — REPLACE the
//     operator's own startup files rather than adding to them, so the shell
//     would lose their prompt, their functions and their aliases in exchange
//     for one of ours. The overlay adds to a list; it substitutes for nothing.
//
// pinned says whether the overlay is actually in place. It is false only when
// writing it failed — a temp directory that will not take a file — and the
// session opens anyway with the behaviour it had before the overlay existed,
// because a shell is worth more than a pin. The notice then says so rather
// than claiming a context that is not selected, which is the whole reason this
// takes a parameter instead of assuming.
//
// WHY use-context IS MENTIONED. `kubectl config use-context` writes to the
// FIRST file in KUBECONFIG, which is now the overlay, so inside this shell it
// appears to work and is gone with the terminal. That is the outcome we want —
// far better than it editing their real kubeconfig — but it is surprising
// enough that discovering it by accident would read as a bug.
//
// One line, because a wall of text above somebody's prompt is something they
// will want gone by the second session.
func ContextNotice(context string, pinned bool) string {
	if context == "" {
		return ""
	}
	if !pinned {
		return fmt.Sprintf(
			"PodSteer: KUBECONFIG is set for this shell and the open tab is context %q, "+
				"but selecting it for this session failed — pass --context %s. "+
				"Your own kubeconfig is untouched.",
			context, context)
	}
	return fmt.Sprintf(
		"PodSteer: this shell is already set to context %q, so kubectl needs no flag. "+
			"Your own kubeconfig is untouched; the selection lives in a PodSteer file "+
			"for this session only, so `kubectl config use-context` here lasts only as "+
			"long as this terminal.",
		context)
}

// UnsupportedNotice explains a machine that cannot open a local shell.
//
// Said in the terminal rather than hidden behind a disabled control, because
// "this control does nothing" is a worse answer than a sentence saying why.
//
// IT NAMES A WINDOWS VERSION NOW, not Windows. A local shell needs a
// pseudo-terminal, and Windows has had one — ConPTY — since Windows 10 1809;
// PodSteer uses it. Only an older Windows reaches this sentence, which is why
// it says which and what to do about it rather than describing a limitation
// of the build.
const UnsupportedNotice = "PodSteer: a local shell needs a pseudo-console, which Windows " +
	"has had since Windows 10 version 1809 — this machine is older than that. Open your own " +
	"terminal instead; nothing here is required to make kubectl work there."
