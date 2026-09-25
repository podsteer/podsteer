package localshell

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

// lookupOf returns a LookupFunc that finds exactly the named binaries.
//
// The fake answers in whatever order it is asked, which is the point: nothing
// about the PATH may decide the order agents are offered in.
func lookupOf(present ...string) LookupFunc {
	set := make(map[string]bool, len(present))
	for _, name := range present {
		set[name] = true
	}
	return func(name string) (string, error) {
		if set[name] {
			return "/opt/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
}

// ids reduces a detection result to the identifiers, in order.
func ids(agents []domain.CodingAgent) []string {
	out := make([]string, 0, len(agents))
	for _, agent := range agents {
		out = append(out, agent.ID)
	}
	return out
}

// TestDetectAgentsFindsNothingOnAMachineWithNone is the ordinary answer for
// most machines, and it must be an empty list rather than an error: nobody is
// obliged to have a coding agent, and PodSteer never offers to install one.
func TestDetectAgentsFindsNothingOnAMachineWithNone(t *testing.T) {
	t.Parallel()

	if got := DetectAgents(lookupOf()); len(got) != 0 {
		t.Fatalf("DetectAgents() = %v, want none", ids(got))
	}
}

// TestDetectAgentsReportsWhatIsThere covers the single-agent machine, and pins
// that the path found is carried through — a machine can have two, and which
// one this is matters.
func TestDetectAgentsReportsWhatIsThere(t *testing.T) {
	t.Parallel()

	got := DetectAgents(lookupOf("gemini"))
	if len(got) != 1 {
		t.Fatalf("DetectAgents() = %v, want exactly one", ids(got))
	}
	if got[0].ID != "gemini" || got[0].Label != "Gemini CLI" {
		t.Fatalf("DetectAgents()[0] = %+v, want the Gemini CLI entry", got[0])
	}
	if got[0].Path != "/opt/bin/gemini" {
		t.Fatalf("path = %q, want where it was actually found", got[0].Path)
	}
}

// TestDetectAgentsKeepsTheFixedPreferenceOrder is the one that matters on a
// machine with several. The order must come from the table and nowhere else,
// so the same machine offers the same default every time it is asked — not
// whichever the PATH happened to reach first.
func TestDetectAgentsKeepsTheFixedPreferenceOrder(t *testing.T) {
	t.Parallel()

	// Named in deliberately the wrong order, since the lookup is asked in the
	// table's order and the caller's spelling must not reach the result.
	got := ids(DetectAgents(lookupOf("copilot", "gemini", "codex", "claude")))
	want := []string{"claude", "codex", "gemini", "copilot"}

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("DetectAgents() = %v, want %v", got, want)
	}
}

// TestDetectAgentsSkipsALookupThatAnswersEmpty covers a lookup that reports no
// error and no path — a shape exec.LookPath does not produce but a wrapper
// might, and one that would otherwise offer an agent with nothing to run.
func TestDetectAgentsSkipsALookupThatAnswersEmpty(t *testing.T) {
	t.Parallel()

	blank := func(string) (string, error) { return "", nil }
	if got := DetectAgents(blank); len(got) != 0 {
		t.Fatalf("DetectAgents() = %v, want none", ids(got))
	}
}

// TestAgentArgsUsesEachAgentsOwnPromptFlag pins the one per-agent difference
// in the feature. Two of these take the prompt positionally and two behind a
// flag, and getting it wrong means the agent starts and does not read it.
func TestAgentArgsUsesEachAgentsOwnPromptFlag(t *testing.T) {
	t.Parallel()

	cases := []struct {
		id   string
		want []string
	}{
		{"claude", []string{"hello"}},
		{"codex", []string{"hello"}},
		{"gemini", []string{"-i", "hello"}},
		{"copilot", []string{"-p", "hello"}},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			t.Parallel()

			got, err := AgentArgs(tc.id, "hello")
			if err != nil {
				t.Fatalf("AgentArgs(%q) error = %v", tc.id, err)
			}
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Fatalf("AgentArgs(%q) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}

// TestAgentArgsRefusesAnUnknownAgent keeps the frontend from naming something
// the table has never heard of, which would otherwise be launched with a
// prompt shaped for a different CLI.
func TestAgentArgsRefusesAnUnknownAgent(t *testing.T) {
	t.Parallel()

	if _, err := AgentArgs("something-else", "hello"); err == nil {
		t.Fatal("AgentArgs() error = nil, want a refusal for an agent not in the table")
	}
}

// TestAgentArgsPassesNoPromptWhenThereIsNone covers the launch with nothing to
// say: the agent starts bare rather than with an empty argument, which some
// CLIs read as a prompt of no words.
func TestAgentArgsPassesNoPromptWhenThereIsNone(t *testing.T) {
	t.Parallel()

	got, err := AgentArgs("claude", "")
	if err != nil {
		t.Fatalf("AgentArgs() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("AgentArgs() = %v, want no arguments", got)
	}
}

// TestAgentPromptNamesTheClusterAndTheOpenObject is the first of the prompt's
// three duties: an agent that does not know what it is pointed at either asks
// or guesses, and guessing against a real cluster is the bad case.
func TestAgentPromptNamesTheClusterAndTheOpenObject(t *testing.T) {
	t.Parallel()

	prompt := AgentPrompt(domain.LocalShellSpec{
		Context: "prod-eu",
		Subject: domain.TerminalSubject{Kind: "Deployment", Namespace: "shop", Name: "web"},
	})

	for _, want := range []string{"prod-eu", "Deployment", "web", "shop"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt does not mention %q:\n%s", want, prompt)
		}
	}
}

// TestAgentPromptOmitsTheObjectWhenNothingIsOpen keeps the prompt from
// asserting an object that does not exist — a sentence naming an empty kind
// is worse than no sentence.
func TestAgentPromptOmitsTheObjectWhenNothingIsOpen(t *testing.T) {
	t.Parallel()

	prompt := AgentPrompt(domain.LocalShellSpec{Context: "prod-eu"})

	if strings.Contains(prompt, "The object I have open") {
		t.Fatalf("prompt claims an open object with none:\n%s", prompt)
	}
}

// TestAgentPromptStatesTheAccessItHas is the second duty, and the one that is
// about honesty rather than convenience: the agent is holding the operator's
// own credentials against a real cluster and is told so plainly.
func TestAgentPromptStatesTheAccessItHas(t *testing.T) {
	t.Parallel()

	prompt := AgentPrompt(domain.LocalShellSpec{Context: "prod-eu"})

	if !strings.Contains(prompt, "KUBECONFIG") {
		t.Errorf("prompt does not mention the kubeconfig it was given:\n%s", prompt)
	}
	if !strings.Contains(prompt, "--context prod-eu") {
		t.Errorf("prompt does not tell the agent how to target the open cluster:\n%s", prompt)
	}
}

// TestAgentPromptAsksForReadOnlyWhenThatIsTheDefault is the third duty. It is
// a REQUEST and reads like one: the credentials are the operator's and nothing
// here can narrow them, so a prompt claiming a restriction would be false.
func TestAgentPromptAsksForReadOnlyWhenThatIsTheDefault(t *testing.T) {
	t.Parallel()

	asked := AgentPrompt(domain.LocalShellSpec{Context: "prod-eu", ReadOnly: true})
	if !strings.Contains(asked, "read-only") {
		t.Errorf("read-only prompt does not ask for read-only kubectl:\n%s", asked)
	}
	if !strings.Contains(asked, "unless I say otherwise") {
		t.Errorf("read-only prompt does not leave the operator a way out:\n%s", asked)
	}

	open := AgentPrompt(domain.LocalShellSpec{Context: "prod-eu"})
	if strings.Contains(open, "Please keep to read-only") {
		t.Errorf("a session that did not ask for read-only carries the request anyway:\n%s", open)
	}
}

// TestUnknownAgentIsNotStarted pins the manager's own refusal, which is where
// an agent that has been uninstalled since detection lands. It must name the
// agent and never attempt to obtain it.
func TestUnknownAgentIsNotStarted(t *testing.T) {
	t.Parallel()

	manager := New(Config{Lookup: lookupOf()}, nil)
	_, _, err := manager.command(domain.LocalShellSpec{Agent: "claude"})

	if err == nil {
		t.Fatal("command() error = nil, want a refusal for an agent that is not on PATH")
	}
	if !strings.Contains(err.Error(), "claude") {
		t.Fatalf("command() error = %v, want it to name the agent", err)
	}
	if errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("command() error = %v, want a sentence rather than the raw lookup failure", err)
	}
}
