package localshell

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

// valueOf reads one variable out of an assembled environment.
func valueOf(env []string, name string) (string, bool) {
	for _, entry := range env {
		if key, value, ok := strings.Cut(entry, "="); ok && key == name {
			return value, true
		}
	}
	return "", false
}

// countOf reports how many times a variable appears.
//
// Worth asserting on its own: a duplicate is not a cosmetic problem. Which of
// two identical names wins is decided by whoever reads the environment, so an
// appended KUBECONFIG beside an inherited one is a shell that may see either
// answer.
func countOf(env []string, name string) int {
	n := 0
	for _, entry := range env {
		if key, _, ok := strings.Cut(entry, "="); ok && key == name {
			n++
		}
	}
	return n
}

// TestBuildEnvSetsKubeconfigToTheResolvedPrecedenceList is the point of the
// whole feature: a shell opened here reads the same clusters PodSteer does,
// including every file the kubeconfig directory contributed, in the same
// order.
func TestBuildEnvSetsKubeconfigToTheResolvedPrecedenceList(t *testing.T) {
	t.Parallel()

	files := []string{"/home/kim/.kube/config", "/home/kim/.kube/configs/a.yaml", "/home/kim/.kube/configs/b.yaml"}
	env := BuildEnv(nil, files, domain.LocalShellSpec{Context: "staging"})

	want := strings.Join(files, string(os.PathListSeparator))
	got, ok := valueOf(env, EnvKubeconfig)
	if !ok {
		t.Fatalf("KUBECONFIG missing from %v", env)
	}
	if got != want {
		t.Fatalf("KUBECONFIG = %q, want %q — the directory's files must ride along, in precedence order", got, want)
	}
}

// TestBuildEnvKeepsTheOperatorsOwnEnvironment guards the rule that this is the
// operator's machine: anything they had set is still set. Dropping a variable
// would make a shell opened here behave unlike the one in their terminal, for
// reasons nothing on screen could explain.
func TestBuildEnvKeepsTheOperatorsOwnEnvironment(t *testing.T) {
	t.Parallel()

	base := []string{"HOME=/home/kim", "EDITOR=hx", "AWS_PROFILE=prod", "MALFORMED"}
	env := BuildEnv(base, nil, domain.LocalShellSpec{Context: "staging"})

	for _, entry := range base {
		if !slices.Contains(env, entry) {
			t.Errorf("%q was dropped; the operator's own environment must survive", entry)
		}
	}
}

// TestBuildEnvReplacesRatherThanShadowsAnInheritedKubeconfig pins that an
// override is exactly one entry. PodSteer's own process may well have been
// launched with KUBECONFIG already set, and appending a second one leaves
// which of them a shell obeys up to the reader.
func TestBuildEnvReplacesRatherThanShadowsAnInheritedKubeconfig(t *testing.T) {
	t.Parallel()

	base := []string{"KUBECONFIG=/somewhere/else", "TERM=dumb", "PODSTEER_CONTEXT=stale"}
	env := BuildEnv(base, []string{"/home/kim/.kube/config"}, domain.LocalShellSpec{Context: "staging"})

	for _, name := range []string{EnvKubeconfig, EnvContext, "TERM"} {
		if n := countOf(env, name); n != 1 {
			t.Errorf("%s appears %d times, want exactly 1", name, n)
		}
	}
	if got, _ := valueOf(env, EnvKubeconfig); got != "/home/kim/.kube/config" {
		t.Errorf("KUBECONFIG = %q, want the resolved list rather than the inherited one", got)
	}
	if got, _ := valueOf(env, EnvContext); got != "staging" {
		t.Errorf("PODSTEER_CONTEXT = %q, want staging", got)
	}
	if got, _ := valueOf(env, "TERM"); got != "xterm-256color" {
		t.Errorf("TERM = %q, want xterm-256color — a dumb terminal has no full-screen programs", got)
	}
}

// TestBuildEnvLeavesKubeconfigAloneWhenNothingResolved covers the machine with
// no kubeconfig at all. Setting KUBECONFIG to an empty string there would hide
// whatever the operator's own shell can see, which is worse than saying
// nothing.
func TestBuildEnvLeavesKubeconfigAloneWhenNothingResolved(t *testing.T) {
	t.Parallel()

	env := BuildEnv([]string{"KUBECONFIG=/theirs.yaml"}, nil, domain.LocalShellSpec{Context: "staging"})

	got, ok := valueOf(env, EnvKubeconfig)
	if !ok || got != "/theirs.yaml" {
		t.Fatalf("KUBECONFIG = %q (present=%v), want the inherited value untouched", got, ok)
	}
}

// TestBuildEnvSetsTheReadOnlyMarkerOnlyForAnAgentThatAskedForIt pins the
// marker's meaning. Present means "this session asked to stay read-only";
// absent means nothing was asked. A plain shell never carries it, because
// nobody asked a shell anything.
func TestBuildEnvSetsTheReadOnlyMarkerOnlyForAnAgentThatAskedForIt(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		spec domain.LocalShellSpec
		want string
	}{
		{"a read-only agent", domain.LocalShellSpec{Agent: "claude", ReadOnly: true}, "1"},
		{"an agent that was not asked", domain.LocalShellSpec{Agent: "claude"}, ""},
		{"a plain shell", domain.LocalShellSpec{ReadOnly: true}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := BuildEnv(nil, nil, tc.spec)
			got, present := valueOf(env, EnvAgentReadOnly)
			if tc.want == "" && present {
				t.Fatalf("%s = %q, want it absent", EnvAgentReadOnly, got)
			}
			if tc.want != "" && got != tc.want {
				t.Fatalf("%s = %q (present=%v), want %q", EnvAgentReadOnly, got, present, tc.want)
			}
		})
	}
}

// TestBuildEnvDropsAnInheritedReadOnlyMarker covers the case that makes the
// marker a lie: PodSteer itself started from a shell that had one set, and a
// session that did not ask for read-only inherits the claim that it did.
func TestBuildEnvDropsAnInheritedReadOnlyMarker(t *testing.T) {
	t.Parallel()

	env := BuildEnv([]string{EnvAgentReadOnly + "=1"}, nil, domain.LocalShellSpec{Agent: "claude"})

	if got, present := valueOf(env, EnvAgentReadOnly); present {
		t.Fatalf("%s = %q, want it absent — this session did not ask for it", EnvAgentReadOnly, got)
	}
}

// TestContextNoticeNamesTheContextAndStaysOneLine is the honest half of the
// pinning decision: PodSteer cannot set a context without writing a file, so
// it says which one it would have used and leaves the kubeconfig alone.
func TestContextNoticeNamesTheContextAndStaysOneLine(t *testing.T) {
	t.Parallel()

	notice := ContextNotice("staging")

	if !strings.Contains(notice, "staging") {
		t.Errorf("notice = %q, want it to name the context", notice)
	}
	if !strings.Contains(notice, "--context") {
		t.Errorf("notice = %q, want it to say how to target that context", notice)
	}
	if strings.Contains(notice, "\n") {
		t.Errorf("notice = %q, want a single line above somebody's prompt", notice)
	}
}

// TestContextNoticeIsEmptyWithoutAContext covers a shell opened with no
// cluster tab in front. There is nothing true to say, so nothing is printed.
func TestContextNoticeIsEmptyWithoutAContext(t *testing.T) {
	t.Parallel()

	if notice := ContextNotice(""); notice != "" {
		t.Fatalf("notice = %q, want empty when no context is open", notice)
	}
}
