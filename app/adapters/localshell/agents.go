package localshell

import (
	"fmt"
	"strings"

	"github.com/podsteer/podsteer/app/domain"
)

// knownAgent describes one coding agent CLI PodSteer knows how to start.
type knownAgent struct {
	// id is the binary's name on the PATH, and the identifier the frontend
	// hands back.
	id string
	// label is what the operator reads.
	label string
	// promptFlag is how this CLI takes its opening prompt, empty when the
	// prompt is a plain positional argument.
	//
	// THE ONE PLACE THIS IS WRITTEN DOWN. These are the only per-agent
	// difference in the whole feature, and a CLI that changes its flag is
	// fixed here rather than in a launcher.
	promptFlag string
}

// knownAgents is the fixed preference order.
//
// FIXED, NOT RANKED. The order decides which agent is offered first when a
// machine has several, and it must not depend on which was found first, on
// map iteration, or on anything about the operator's PATH — the same machine
// has to offer the same default every time it is asked. It is not a judgement
// about the agents: it is a list that has to be in some order and stays in
// this one so the answer is reproducible.
var knownAgents = []knownAgent{
	{id: "claude", label: "Claude Code"},
	{id: "codex", label: "Codex"},
	{id: "gemini", label: "Gemini CLI", promptFlag: "-i"},
	{id: "copilot", label: "Copilot CLI", promptFlag: "-p"},
}

// LookupFunc finds an executable on the PATH, matching exec.LookPath.
//
// Injected so detection can be tested without installing four coding agents
// on whatever machine runs the suite.
type LookupFunc func(name string) (string, error)

// DetectAgents reports which coding agents are on the adopted PATH, in the
// fixed preference order above.
//
// The PATH it searches is the process's, which by this point is the login
// shell's — the same adoption that makes credential plugins findable makes
// agents findable, and a Homebrew-installed agent is invisible without it.
func DetectAgents(look LookupFunc) []domain.CodingAgent {
	found := make([]domain.CodingAgent, 0, len(knownAgents))
	for _, agent := range knownAgents {
		path, err := look(agent.id)
		if err != nil || path == "" {
			continue
		}
		found = append(found, domain.CodingAgent{ID: agent.id, Label: agent.label, Path: path})
	}
	return found
}

// agentByID returns the known agent with this id.
func agentByID(id string) (knownAgent, bool) {
	for _, agent := range knownAgents {
		if agent.id == id {
			return agent, true
		}
	}
	return knownAgent{}, false
}

// AgentArgs builds the argument list for one agent's opening prompt.
func AgentArgs(id, prompt string) ([]string, error) {
	agent, ok := agentByID(id)
	if !ok {
		return nil, fmt.Errorf("unknown coding agent %q", id)
	}
	if prompt == "" {
		return nil, nil
	}
	if agent.promptFlag == "" {
		return []string{prompt}, nil
	}
	return []string{agent.promptFlag, prompt}, nil
}

// AgentPrompt composes the opening prompt.
//
// THREE SENTENCES, EACH LOAD-BEARING. The first says which cluster and which
// object, so the agent does not start by asking or, worse, guessing. The
// second states plainly what access it has, because an agent that does not
// know it is holding real credentials against a real cluster is the dangerous
// case. The third asks for read-only kubectl when that was the default, and
// says the operator may lift it — a request the operator can override is
// honest; one presented as a restriction would not be, since nothing here
// enforces it.
//
// Composed in Go rather than in the interface because it is the sentence that
// tells an agent what it is pointed at, and it should read identically
// wherever a session is launched.
func AgentPrompt(spec domain.LocalShellSpec) string {
	var b strings.Builder

	b.WriteString("I am working in PodSteer, a Kubernetes desktop client, against the cluster ")
	if spec.Context != "" {
		fmt.Fprintf(&b, "whose kubeconfig context is %q", spec.Context)
	} else {
		b.WriteString("my kubeconfig currently selects")
	}
	if !spec.Subject.IsZero() {
		fmt.Fprintf(&b, ". The object I have open is the %s %q", spec.Subject.Kind, spec.Subject.Name)
		if spec.Subject.Namespace != "" {
			fmt.Fprintf(&b, " in namespace %q", spec.Subject.Namespace)
		}
	}
	b.WriteString(".\n\n")

	b.WriteString("KUBECONFIG is already set in this shell and you have whatever access it grants — ")
	b.WriteString("the same credentials I use, with no additional restriction of any kind.")
	if spec.Context != "" {
		fmt.Fprintf(&b, " current-context is NOT set to this cluster, so pass --context %s on every command.", spec.Context)
	}
	b.WriteString("\n\n")

	if spec.ReadOnly {
		b.WriteString("Please keep to read-only kubectl — get, describe, logs, events — ")
		b.WriteString("and ask me before anything that writes, deletes or scales, unless I say otherwise.")
	} else {
		b.WriteString("I have not asked you to stay read-only, so writes are allowed; ")
		b.WriteString("tell me what you are about to change before you change it.")
	}
	return b.String()
}
