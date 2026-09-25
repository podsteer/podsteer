package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// protocolVersions are the Model Context Protocol revisions this server
// speaks, newest first.
//
// Negotiation is "answer in the client's version if it is one of these,
// otherwise answer in ours and let the client decide whether it can carry on"
// — which is what the specification asks for, and is why the list exists
// rather than a single constant. Nothing in the surface below differs between
// these revisions; tools, their schemas and their results are identical.
var protocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// defaultTimeout bounds one tool call when Deps names none.
//
// The same 30 seconds the window gives a bridge call, for the same reason: a
// read that has not answered by then is not going to be useful to whoever is
// waiting, and an agent blocked on a wedged cluster cannot be interrupted by
// somebody closing a pane.
const defaultTimeout = 30 * time.Second

// ClusterReader is the reading half of ports.ClusterService.
//
// NARROWED ON PURPOSE. The full interface also carries AddKubeconfig, which
// writes the operator's kubeconfig file, and SetReadOnly, which changes a
// policy the window owns. Neither belongs to an agent, and the surest way to
// keep them out is for this package to be unable to name them.
//
// Connect is here because nothing can be read from a cluster that is not
// open. It contacts the API server for its version and its kinds, and writes
// nothing to the cluster and nothing to disk.
type ClusterReader interface {
	ListClusters(ctx context.Context) ([]domain.Cluster, error)
	Connect(ctx context.Context, id domain.ClusterID) (domain.Cluster, error)
	Connections(ctx context.Context) ([]domain.Cluster, error)
	ListNamespaces(ctx context.Context, id domain.ClusterID) ([]domain.Namespace, error)
	ListNamespaceSummaries(ctx context.Context, id domain.ClusterID, projection domain.Projection) ([]domain.NamespaceSummary, error)
	ListNodes(ctx context.Context, id domain.ClusterID, projection domain.Projection) ([]domain.Node, error)
}

// KindReader lists what a cluster can show — ports.NavigationService.
type KindReader interface {
	Kinds(ctx context.Context, id domain.ClusterID) ([]domain.ResourceKind, error)
}

// WorkloadReader is the reading half of ports.WorkloadService.
type WorkloadReader interface {
	ListPods(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, projection domain.Projection) ([]domain.Pod, error)
	ListPodsOnNode(ctx context.Context, id domain.ClusterID, nodeName string) ([]domain.Pod, error)
	ListWorkloads(ctx context.Context, id domain.ClusterID, kind domain.WorkloadKind, namespace domain.NamespaceName, projection domain.Projection) ([]domain.Workload, error)
	PodGraph(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName string) (domain.PodGraph, error)
	WorkloadGraph(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, kind domain.WorkloadKind, name string) (domain.PodGraph, error)
}

// EventReader is ports.EventService.
type EventReader interface {
	ListEvents(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, projection domain.Projection) ([]domain.Event, error)
	ListEventsForResource(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, kind, name string) ([]domain.Event, error)
}

// ResourceReader is the reading half of ports.ResourceService.
//
// RevealSecretKey and InspectTLSSecret are deliberately absent, and that
// absence is the redaction guarantee this package makes. Both return key
// material on an explicit, audited, per-key request an operator makes in
// front of the pane doing the asking; there is no equivalent act an agent can
// perform, so the methods are not reachable from here at all. GetManifest is,
// and every call passes revealSecrets false.
type ResourceReader interface {
	ListTable(ctx context.Context, id domain.ClusterID, kindID string, namespace domain.NamespaceName, projection domain.Projection) (domain.ResourceTable, error)
	GetManifest(ctx context.Context, id domain.ClusterID, kindID string, namespace domain.NamespaceName, name string, revealSecrets bool) (string, error)
	ObjectGraph(ctx context.Context, id domain.ClusterID, kindID string, namespace domain.NamespaceName, name string) (domain.PodGraph, error)
	NamespaceInventory(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) (domain.NamespaceInventory, error)
}

// OverviewReader is the cluster assessment — ports.OverviewService.
type OverviewReader interface {
	Overview(ctx context.Context, id domain.ClusterID) (domain.Overview, error)
}

// RBACReader is ports.RBACService, every method of which is already a read.
type RBACReader interface {
	SubjectRules(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName) (domain.SubjectRules, error)
	CanI(ctx context.Context, id domain.ClusterID, request domain.AccessRequest) (domain.AccessDecision, error)
	InspectRole(ctx context.Context, id domain.ClusterID, target domain.RoleTarget) (domain.RoleInspection, error)
}

// LogReader streams one container's log.
//
// The one method of ManagementService this package uses, and the reason that
// service is not taken whole: everything else on it writes. Reading a log is
// a GET of a subresource, and the bounds a tool puts on it are in
// domain.LogOptions rather than here.
type LogReader interface {
	StreamLogs(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName string, containerName string, opts domain.LogOptions, out chan<- string) error
}

// Deps are the use cases the tools read through.
type Deps struct {
	// Clusters, Kinds, Workloads, Events, Resources, Overview, RBAC and Logs
	// are all required: a tool surface missing one of them would answer some
	// questions and silently not offer others, and an agent cannot tell a
	// capability that was left out from one the cluster does not have.
	Clusters  ClusterReader
	Kinds     KindReader
	Workloads WorkloadReader
	Events    EventReader
	Resources ResourceReader
	Overview  OverviewReader
	RBAC      RBACReader
	Logs      LogReader

	// Version is reported to the client as the server's version.
	Version string
	// Timeout bounds one tool call. Zero means defaultTimeout.
	Timeout time.Duration
	// Now sources the clock the assessments are made against. Nil means
	// time.Now; a test supplies its own so an age is not a moving target.
	Now func() time.Time
	// Logger receives protocol and failure detail. It must write to stderr:
	// stdout is the transport, and one stray line on it is a protocol error
	// the agent reports as a broken server.
	Logger *slog.Logger
}

// Server answers Model Context Protocol requests on one pair of streams.
//
// One request is handled at a time. Nothing here needs concurrency — an agent
// asks a question and waits for the answer — and handling messages in order
// means a tool call cannot observe a cluster being connected by a call the
// client believes has not happened yet.
type Server struct {
	tools   []Tool
	byName  map[string]Tool
	version string
	timeout time.Duration
	logger  *slog.Logger
}

// New returns a server offering the read-only tool set.
func New(deps Deps) (*Server, error) {
	switch {
	case deps.Clusters == nil:
		return nil, errors.New("mcp: Server requires a ClusterReader")
	case deps.Kinds == nil:
		return nil, errors.New("mcp: Server requires a KindReader")
	case deps.Workloads == nil:
		return nil, errors.New("mcp: Server requires a WorkloadReader")
	case deps.Events == nil:
		return nil, errors.New("mcp: Server requires an EventReader")
	case deps.Resources == nil:
		return nil, errors.New("mcp: Server requires a ResourceReader")
	case deps.Overview == nil:
		return nil, errors.New("mcp: Server requires an OverviewReader")
	case deps.RBAC == nil:
		return nil, errors.New("mcp: Server requires an RBACReader")
	case deps.Logs == nil:
		return nil, errors.New("mcp: Server requires a LogReader")
	}

	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Timeout <= 0 {
		deps.Timeout = defaultTimeout
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}

	tools := buildTools(deps)
	byName := make(map[string]Tool, len(tools))
	for _, tool := range tools {
		if _, duplicate := byName[tool.Name]; duplicate {
			return nil, fmt.Errorf("mcp: duplicate tool %q", tool.Name)
		}
		byName[tool.Name] = tool
	}

	return &Server{
		tools:   tools,
		byName:  byName,
		version: deps.Version,
		timeout: deps.Timeout,
		logger:  deps.Logger.With(slog.String("adapter", "mcp")),
	}, nil
}

// Tools returns the offered tools, in the order tools/list reports them.
func (s *Server) Tools() []Tool { return s.tools }

// Serve reads requests from in and writes answers to out until the stream
// ends or ctx is cancelled.
//
// Returns nil on a clean end of input, which is how this process normally
// exits: the agent closes the pipe when it is finished with it.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	answers := newWriter(out)

	// READ ON ITS OWN GOROUTINE, because a read of a pipe cannot be
	// interrupted: a loop calling next() directly would sit inside that read
	// until the agent sent something, which for an operator who started this
	// by hand and pressed Ctrl-C means a process that ignores the signal —
	// signal.NotifyContext has already taken the default handler away. The
	// goroutine is left blocked when that happens and dies with the process;
	// there is nothing else it holds.
	type incoming struct {
		line []byte
		err  error
	}
	lines := make(chan incoming)
	go func() {
		messages := newReader(in)
		for {
			line, err := messages.next()
			select {
			case lines <- incoming{line: line, err: err}:
				if err != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		var message incoming
		select {
		case <-ctx.Done():
			return nil
		case message = <-lines:
		}

		if errors.Is(message.err, io.EOF) {
			return nil
		}
		if message.err != nil {
			// A read failure that is not EOF means the transport itself is
			// broken — an over-long line, a closed pipe mid-message — and
			// there is nothing left to answer on.
			return fmt.Errorf("reading request: %w", message.err)
		}

		answer, reply := s.handle(ctx, message.line)
		if !reply {
			continue
		}
		if err := answers.write(answer); err != nil {
			return err
		}
	}
}

// handle answers one raw message, reporting whether an answer must be sent.
func (s *Server) handle(ctx context.Context, line []byte) (response, bool) {
	var message request
	if err := json.Unmarshal(line, &message); err != nil {
		// The id is unknown, so this cannot be matched to a call — it is
		// still sent, because silence looks identical to a hung server.
		return newError(nil, rpcParseError, "invalid JSON: %v", err), true
	}

	if message.JSONRPC != "2.0" || message.Method == "" {
		if message.isNotification() {
			return response{}, false
		}
		return newError(message.ID, rpcInvalidRequest, "not a JSON-RPC 2.0 request"), true
	}

	result, err := s.dispatch(ctx, message)

	// A notification is answered with nothing at all, including when it
	// failed: the peer is not waiting, and an unsolicited response is a
	// protocol violation rather than helpful extra detail.
	if message.isNotification() {
		if err != nil {
			s.logger.Debug("notification failed",
				slog.String("method", message.Method), slog.String("error", err.Error()))
		}
		return response{}, false
	}

	if err != nil {
		var protocol *protocolError
		if errors.As(err, &protocol) {
			return newError(message.ID, protocol.code, "%s", protocol.message), true
		}
		s.logger.Error("request failed",
			slog.String("method", message.Method), slog.String("error", err.Error()))
		return newError(message.ID, rpcInternalError, "%s", err.Error()), true
	}

	return newResult(message.ID, result), true
}

// dispatch routes one method.
//
// The set is deliberately small: this server offers tools and nothing else —
// no resources, no prompts, no sampling, no completion — so a method outside
// it is method-not-found rather than a stub returning an empty list. A client
// must be able to tell "not implemented" from "implemented and empty".
func (s *Server) dispatch(ctx context.Context, message request) (any, error) {
	switch message.Method {
	case "initialize":
		return s.initialize(message.Params)
	case "notifications/initialized", "notifications/cancelled":
		return nil, nil
	case "ping":
		return struct{}{}, nil
	case "tools/list":
		return s.listTools(), nil
	case "tools/call":
		return s.callTool(ctx, message.Params)
	default:
		return nil, &protocolError{code: rpcMethodNotFound, message: fmt.Sprintf("unknown method %q", message.Method)}
	}
}

// protocolError is a failure of the message rather than of the answer.
type protocolError struct {
	code    int
	message string
}

func (e *protocolError) Error() string { return e.message }

// initializeParams is the half of the client's handshake this server reads.
type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
}

// initializeResult is the handshake answer.
type initializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    serverCapabilities `json:"capabilities"`
	ServerInfo      serverInfo         `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
}

type serverCapabilities struct {
	Tools toolsCapability `json:"tools"`
}

// toolsCapability declares a fixed tool set.
//
// listChanged is false and will stay false: the tools are compiled in, so
// there is no event that could change them mid-session, and claiming
// otherwise would have clients subscribe to a notification that never comes.
type toolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

// instructions tells the model what this server is and what it will not do.
//
// The read-only sentence is here as well as on every tool because an agent
// that believes it can act will announce that it has, and an operator reading
// "I have restarted the deployment" from a tool set that cannot restart
// anything has been told something false about their cluster.
const instructions = "PodSteer reads the Kubernetes clusters in this machine's kubeconfig, " +
	"with the operator's own credentials and permissions. Every tool is READ-ONLY: " +
	"nothing here can delete, scale, restart, apply, exec or port-forward, and there is no " +
	"tool that reveals a Secret's values. Start with list_clusters, then pass that cluster " +
	"name to the other tools. A refusal (RBAC) is reported as a refusal, never as an empty result."

// initialize answers the handshake.
func (s *Server) initialize(params json.RawMessage) (any, error) {
	var requested initializeParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &requested); err != nil {
			return nil, &protocolError{code: rpcInvalidParams, message: fmt.Sprintf("invalid initialize params: %v", err)}
		}
	}

	return initializeResult{
		ProtocolVersion: negotiate(requested.ProtocolVersion),
		Capabilities:    serverCapabilities{Tools: toolsCapability{ListChanged: false}},
		ServerInfo:      serverInfo{Name: "podsteer", Title: "PodSteer", Version: s.version},
		Instructions:    instructions,
	}, nil
}

// negotiate picks the version to answer in.
//
// The client's, when this server speaks it; otherwise the newest this server
// speaks, which the specification says to send so the client can decide
// whether to continue rather than being told nothing at all.
func negotiate(requested string) string {
	for _, supported := range protocolVersions {
		if requested == supported {
			return supported
		}
	}
	return protocolVersions[0]
}

// listedTool is one entry of tools/list.
type listedTool struct {
	Name        string      `json:"name"`
	Title       string      `json:"title,omitempty"`
	Description string      `json:"description"`
	InputSchema Schema      `json:"inputSchema"`
	Annotations annotations `json:"annotations"`
}

// annotations are the behavioural hints a client shows before running a tool.
//
// Identical on every tool, and constructed here rather than declared per tool
// for exactly that reason: there is no read-only flag to get wrong on a new
// tool, because the package has no way to express a tool that writes. See the
// package comment.
type annotations struct {
	ReadOnlyHint    bool `json:"readOnlyHint"`
	DestructiveHint bool `json:"destructiveHint"`
	IdempotentHint  bool `json:"idempotentHint"`
	OpenWorldHint   bool `json:"openWorldHint"`
}

// readOnlyAnnotations is what every tool declares.
//
// openWorldHint is true because the answers come from a cluster that changes
// underneath the reader: two identical calls a minute apart may legitimately
// differ, and a client caching one as settled fact would be wrong.
func readOnlyAnnotations() annotations {
	return annotations{
		ReadOnlyHint:    true,
		DestructiveHint: false,
		IdempotentHint:  true,
		OpenWorldHint:   true,
	}
}

// toolList is the tools/list result.
type toolList struct {
	Tools []listedTool `json:"tools"`
}

// listTools renders the offered tools.
func (s *Server) listTools() toolList {
	listed := make([]listedTool, 0, len(s.tools))
	for _, tool := range s.tools {
		listed = append(listed, listedTool{
			Name:        tool.Name,
			Title:       tool.Title,
			Description: tool.Description,
			InputSchema: tool.Schema,
			Annotations: readOnlyAnnotations(),
		})
	}
	return toolList{Tools: listed}
}

// callParams is the tools/call request.
type callParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// content is one piece of a tool result.
type content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// callResult is the tools/call answer.
type callResult struct {
	Content []content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

// callTool validates and runs one tool.
func (s *Server) callTool(ctx context.Context, params json.RawMessage) (any, error) {
	var call callParams
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, &protocolError{code: rpcInvalidParams, message: fmt.Sprintf("invalid tools/call params: %v", err)}
	}

	tool, known := s.byName[call.Name]
	if !known {
		// A protocol error rather than a failed result: the model asked for
		// something that does not exist, which its runtime should correct,
		// and a text answer saying "no such tool" invites it to try again
		// with the same name.
		return nil, &protocolError{code: rpcInvalidParams, message: fmt.Sprintf("unknown tool %q", call.Name)}
	}

	if err := validate(tool.Schema, call.Arguments); err != nil {
		return nil, &protocolError{code: rpcInvalidParams, message: fmt.Sprintf("%s: %v", tool.Name, err)}
	}

	// The call gets its own deadline. The context the transport loop carries
	// is cancelled only when the process is stopping, so without this a
	// wedged cluster read would hold the one message loop open indefinitely
	// and the agent would see a server that answers nothing at all.
	callCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	result, err := tool.Call(callCtx, Arguments(call.Arguments))
	if err != nil {
		var invalid *invalidArgumentError
		if errors.As(err, &invalid) {
			return nil, &protocolError{code: rpcInvalidParams, message: fmt.Sprintf("%s: %v", tool.Name, err)}
		}
		s.logger.Warn("tool failed",
			slog.String("tool", tool.Name), slog.String("error", err.Error()))
		return toolFailure(err), nil
	}

	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding %s result: %w", tool.Name, err)
	}

	return callResult{Content: []content{{Type: "text", Text: string(encoded)}}}, nil
}
