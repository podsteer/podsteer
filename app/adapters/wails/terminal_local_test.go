package wails

// Tests for the LOCAL terminal and the coding-agent bridge.
//
// The one that matters most is the read-only exemption. Every other Start
// method in terminal.go refuses synchronously on a cluster the operator marked
// read-only, and it would be an easy and entirely wrong instinct to add the
// same check here for consistency. It is not there on purpose: that guard is
// about PodSteer's own writes, and a shell somebody opened on their own
// machine with their own credentials is not something this application can or
// should police. A test is the only thing that stops it being "fixed".

import (
	"strings"
	"testing"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
)

// readOnlyTerminal builds a TerminalAPI whose only cluster is marked
// read-only, returning the API and the local-shell stub behind it.
func readOnlyTerminal(t *testing.T) (*TerminalAPI, *stubLocalShellPort) {
	t.Helper()

	registry := application.NewRegistry()
	registry.SetReadOnly("prod", true)

	management, err := application.NewManagementService(application.ManagementServiceDeps{
		Management: stubManagementPort{},
		Registry:   registry,
	})
	if err != nil {
		t.Fatalf("NewManagementService() error = %v", err)
	}

	shells := &stubLocalShellPort{}
	terminal, err := NewTerminalAPI(management, stubNodeShellPort{}, shells, NewApp(nil, 0), nil)
	if err != nil {
		t.Fatalf("NewTerminalAPI() error = %v", err)
	}
	return terminal, shells
}

// TestStartLocalSessionIsNotGovernedByTheReadOnlyGuard is the whole point of
// this file. The cluster tab is marked read-only and the local shell opens
// anyway, because the guard governs what PodSteer writes to a cluster and this
// writes to none.
func TestStartLocalSessionIsNotGovernedByTheReadOnlyGuard(t *testing.T) {
	t.Parallel()

	terminal, shells := readOnlyTerminal(t)

	sessionID, err := terminal.StartLocalSession("prod", 80, 24)
	if err != nil {
		t.Fatalf("StartLocalSession() error = %v, want a local shell to open on a read-only cluster", err)
	}
	if sessionID == "" {
		t.Fatal("StartLocalSession() returned no session id")
	}

	starts := shells.starts()
	if len(starts) != 1 {
		t.Fatalf("local shells started = %d, want 1", len(starts))
	}
	if starts[0].Context != "prod" {
		t.Fatalf("context = %q, want the open tab's context", starts[0].Context)
	}
	if starts[0].Cols != 80 || starts[0].Rows != 24 {
		t.Fatalf("size = %dx%d, want 80x24 — the shell draws its prompt at the size it starts with",
			starts[0].Cols, starts[0].Rows)
	}
}

// TestStartAgentSessionIsNotGovernedByTheReadOnlyGuardEither pins the same for
// the bridge, which launches a process with exactly the same access.
func TestStartAgentSessionIsNotGovernedByTheReadOnlyGuardEither(t *testing.T) {
	t.Parallel()

	terminal, shells := readOnlyTerminal(t)

	if _, err := terminal.StartAgentSession("prod", "claude", "Pod", "shop", "web-0", true, 80, 24); err != nil {
		t.Fatalf("StartAgentSession() error = %v, want it to open on a read-only cluster", err)
	}
	if len(shells.starts()) != 1 {
		t.Fatalf("agent sessions started = %d, want 1", len(shells.starts()))
	}
}

// TestStartAgentSessionCarriesTheReadOnlyRequestAndTheOpenObject pins what the
// launcher's defaults actually reach the process as: the marker the operator
// chose, and the object they had open so the first prompt can name it.
func TestStartAgentSessionCarriesTheReadOnlyRequestAndTheOpenObject(t *testing.T) {
	t.Parallel()

	terminal, shells := readOnlyTerminal(t)

	if _, err := terminal.StartAgentSession("prod", "codex", "Deployment", "shop", "web", true, 100, 40); err != nil {
		t.Fatalf("StartAgentSession() error = %v", err)
	}

	spec := shells.starts()[0]
	if spec.Agent != "codex" {
		t.Errorf("agent = %q, want codex", spec.Agent)
	}
	if !spec.ReadOnly {
		t.Error("ReadOnly = false, want the read-only default to reach the process")
	}
	want := domain.TerminalSubject{Kind: "Deployment", Namespace: "shop", Name: "web"}
	if spec.Subject != want {
		t.Errorf("subject = %+v, want %+v", spec.Subject, want)
	}
}

// TestStartAgentSessionWithoutReadOnlyCarriesNoRequest is the other half: an
// operator who turned the default off must not have a read-only marker set on
// their behalf, since the marker's whole meaning is that somebody asked.
func TestStartAgentSessionWithoutReadOnlyCarriesNoRequest(t *testing.T) {
	t.Parallel()

	terminal, shells := readOnlyTerminal(t)

	if _, err := terminal.StartAgentSession("prod", "claude", "", "", "", false, 80, 24); err != nil {
		t.Fatalf("StartAgentSession() error = %v", err)
	}
	if shells.starts()[0].ReadOnly {
		t.Fatal("ReadOnly = true, want it off when the operator turned the default off")
	}
}

// TestStartAgentSessionRefusesWithNoAgentNamed keeps an empty identifier from
// reaching the manager, where it would silently mean "open a plain shell" —
// a launcher bug that would look like the agent starting and doing nothing.
func TestStartAgentSessionRefusesWithNoAgentNamed(t *testing.T) {
	t.Parallel()

	terminal, shells := readOnlyTerminal(t)

	if _, err := terminal.StartAgentSession("prod", "", "", "", "", true, 80, 24); err == nil {
		t.Fatal("StartAgentSession() error = nil, want a refusal when no agent is named")
	}
	if len(shells.starts()) != 0 {
		t.Fatal("a refused agent session still started a process")
	}
}

// TestDetectAgentsReportsWhatTheAdapterFound pins the pass-through, including
// the path — a machine can easily hold two of the same agent, and the launcher
// shows which one it would run.
func TestDetectAgentsReportsWhatTheAdapterFound(t *testing.T) {
	t.Parallel()

	terminal, shells := readOnlyTerminal(t)
	shells.agents = []domain.CodingAgent{
		{ID: "claude", Label: "Claude Code", Path: "/opt/homebrew/bin/claude"},
		{ID: "gemini", Label: "Gemini CLI", Path: "/usr/local/bin/gemini"},
	}

	got := terminal.DetectAgents()
	if len(got) != 2 {
		t.Fatalf("DetectAgents() = %v, want two", got)
	}
	if got[0].ID != "claude" || got[0].Path != "/opt/homebrew/bin/claude" {
		t.Errorf("DetectAgents()[0] = %+v, want the Claude Code entry with its path", got[0])
	}
	if got[1].Label != "Gemini CLI" {
		t.Errorf("DetectAgents()[1] = %+v, want the Gemini CLI entry", got[1])
	}
}

// TestDetectAgentsOnAMachineWithNoneIsAnEmptyList pins that having no agent is
// an ordinary answer rather than an error — nobody is obliged to have one, and
// PodSteer never offers to install one.
func TestDetectAgentsOnAMachineWithNoneIsAnEmptyList(t *testing.T) {
	t.Parallel()

	terminal, _ := readOnlyTerminal(t)

	if got := terminal.DetectAgents(); len(got) != 0 {
		t.Fatalf("DetectAgents() = %v, want none", got)
	}
}

// TestLocalShellSupportedIsReportedRatherThanGuessed covers the platform
// question the interface asks before offering the control, so an operator
// without the feature reads a sentence instead of pressing something that
// fails.
func TestLocalShellSupportedIsReportedRatherThanGuessed(t *testing.T) {
	t.Parallel()

	terminal, _ := readOnlyTerminal(t)

	got := terminal.LocalShellSupported()
	if !got.Supported {
		t.Fatalf("LocalShellSupported() = %+v, want the stub's answer", got)
	}
	if got.Reason != "" {
		t.Fatalf("reason = %q, want empty when the feature is available", got.Reason)
	}
}

// TestStopSessionEndsALocalShell pins that a local session is reachable
// through the same registry as every cluster one: the pane closing calls
// StopSession with a session id and knows nothing about which kind it holds.
func TestStopSessionEndsALocalShell(t *testing.T) {
	t.Parallel()

	terminal, shells := readOnlyTerminal(t)

	sessionID, err := terminal.StartLocalSession("prod", 80, 24)
	if err != nil {
		t.Fatalf("StartLocalSession() error = %v", err)
	}
	if err := terminal.StopSession(sessionID); err != nil {
		t.Fatalf("StopSession() error = %v", err)
	}

	shells.mu.Lock()
	stopped := append([]string(nil), shells.stopped...)
	shells.mu.Unlock()
	if len(stopped) != 1 || stopped[0] != "shell-1" {
		t.Fatalf("stopped = %v, want the local shell to have been ended", stopped)
	}
}

// TestResizeReachesALocalShellsTerminal pins the branch a local session takes
// through Resize: it has no size queue, so a resize that fell through to one
// would panic on a nil pointer or, worse, silently do nothing.
func TestResizeReachesALocalShellsTerminal(t *testing.T) {
	t.Parallel()

	terminal, _ := readOnlyTerminal(t)

	sessionID, err := terminal.StartLocalSession("prod", 80, 24)
	if err != nil {
		t.Fatalf("StartLocalSession() error = %v", err)
	}
	if err := terminal.Resize(sessionID, 132, 43); err != nil {
		t.Fatalf("Resize() error = %v", err)
	}
}

// TestWriteReachesALocalShell is the keystroke path, through the same bound
// method a container session uses.
func TestWriteReachesALocalShell(t *testing.T) {
	t.Parallel()

	terminal, _ := readOnlyTerminal(t)

	sessionID, err := terminal.StartLocalSession("prod", 80, 24)
	if err != nil {
		t.Fatalf("StartLocalSession() error = %v", err)
	}
	if err := terminal.Write(sessionID, "ls\n"); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
}

// TestStartLocalSessionReportsAFailedStart covers a shell that could not be
// started at all — the Windows case, and a machine with no shell to run. The
// session must not be left in the registry for a pane that never opened.
func TestStartLocalSessionReportsAFailedStart(t *testing.T) {
	t.Parallel()

	registry := application.NewRegistry()
	management, err := application.NewManagementService(application.ManagementServiceDeps{
		Management: stubManagementPort{},
		Registry:   registry,
	})
	if err != nil {
		t.Fatalf("NewManagementService() error = %v", err)
	}

	shells := &stubLocalShellPort{startErr: errRefusedLocalShell}
	terminal, err := NewTerminalAPI(management, stubNodeShellPort{}, shells, NewApp(nil, 0), nil)
	if err != nil {
		t.Fatalf("NewTerminalAPI() error = %v", err)
	}

	sessionID, err := terminal.StartLocalSession("prod", 80, 24)
	if err == nil {
		t.Fatal("StartLocalSession() error = nil, want the refusal reported")
	}
	// Classified rather than quoted: errors.go deliberately does not pass an
	// unrecognised message through to the frontend, and the sentence a
	// platform without a pseudo-terminal deserves reaches the pane through
	// LocalShellSupported instead, before the control is ever offered.
	if !strings.Contains(err.Error(), "internal") {
		t.Fatalf("StartLocalSession() error = %q, want it classified rather than raw", err)
	}
	if sessionID != "" {
		t.Fatalf("session id = %q, want empty on a failed start", sessionID)
	}

	terminal.mu.Lock()
	live := len(terminal.sessions)
	terminal.mu.Unlock()
	if live != 0 {
		t.Fatalf("live sessions = %d, want 0 — a failed start must leave nothing registered", live)
	}
}
