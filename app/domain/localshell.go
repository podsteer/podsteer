package domain

import "time"

// LocalShell is a shell running on the OPERATOR'S OWN MACHINE, opened beside
// the cluster tab that was in front.
//
// Everything else this application can start is a process in the cluster: an
// exec, an attach, a node shell. This one is not — it is the operator's login
// shell, on their laptop, with their credentials, and PodSteer's only
// contribution is the environment it inherits. That difference is why the
// read-only guard does not apply to it (see LocalShellSpec.Context) and why
// nothing here is ever installed: whatever `kubectl` or `helm` means on that
// machine is what runs.
//
// Shaped like NodeShell and Forward for the same reason both of those are:
// PodSteer started a process, so the record of it and the thing that kills it
// are created and destroyed together, and the live set is listable so the
// activity surface can show it and shutdown can end it.
type LocalShell struct {
	// ID identifies the session for Write, Resize and Stop.
	ID string
	// Context is the kubeconfig context the environment was built for — the
	// cluster whose tab was open. Never written to the kubeconfig; see
	// ContextNotice.
	Context string
	// Agent is the coding agent this session launched, empty for a plain
	// shell.
	Agent string
	// Command is what was actually executed, for the activity list. A shell
	// path, or the agent binary's path.
	Command string
	// Started is when the process was started.
	Started time.Time
}

// LocalShellSpec describes a local shell to open.
type LocalShellSpec struct {
	// Context is the kubeconfig context of the cluster tab in front. It is
	// exported to the shell and named in the notice, and it is NOT written
	// anywhere — the operator's current-context is left exactly as it was.
	Context string
	// Cols and Rows size the pseudo-terminal at start, before the shell draws
	// its first prompt. A shell started at the wrong size wraps its prompt.
	Cols, Rows uint16
	// Agent, when set, is the ID of a coding agent to run instead of the
	// login shell. See CodingAgent.
	Agent string
	// ReadOnly asks the agent to keep to read-only kubectl unless told
	// otherwise. It is a REQUEST, not a guard: the credentials are the
	// operator's and nothing here can restrict them. Ignored for a plain
	// shell.
	ReadOnly bool
	// Subject is the object the operator had open when they launched an
	// agent, named in its first prompt. Zero when nothing was open.
	Subject TerminalSubject
}

// TerminalSubject names the object a terminal was opened beside, in the plain
// strings the interface already holds.
//
// Deliberately not ResourceRef: that carries a ResourceKind with a group and a
// version, which is machinery for addressing an object through the API. This
// is a label for a sentence in a prompt, and the kind an operator reads on
// screen ("Deployment") is what belongs in it.
type TerminalSubject struct {
	Kind      string
	Namespace string
	Name      string
}

// IsZero reports whether nothing was open.
func (s TerminalSubject) IsZero() bool { return s.Name == "" || s.Kind == "" }

// CodingAgent is a coding agent CLI found on the operator's PATH.
//
// FOUND, NEVER INSTALLED. PodSteer looks for the binary on the PATH it adopted
// at startup and offers what is there; a machine without one simply has no
// agent to offer. Nothing is downloaded, bundled or suggested for
// installation, and nothing is sent anywhere — launching one is a local
// process start, which is what keeps this consistent with the no-account,
// no-telemetry commitment.
type CodingAgent struct {
	// ID is the binary's name, and the stable identifier the frontend passes
	// back to launch it.
	ID string
	// Label is what the operator reads.
	Label string
	// Path is where the binary was found, for the activity list and the log.
	Path string
}
