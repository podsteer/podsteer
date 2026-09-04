package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// requestLine encodes one JSON-RPC request as the transport would carry it.
func requestLine(t *testing.T, id any, method string, params any) []byte {
	t.Helper()

	message := map[string]any{"jsonrpc": "2.0", "method": method}
	if id != nil {
		message["id"] = id
	}
	if params != nil {
		message["params"] = params
	}

	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("encoding request: %v", err)
	}
	return encoded
}

// answer runs one request through the server and returns the response.
func answer(t *testing.T, server *Server, id any, method string, params any) response {
	t.Helper()

	result, replied := server.handle(context.Background(), requestLine(t, id, method, params))
	if !replied {
		t.Fatalf("%s was not answered", method)
	}
	return result
}

func TestInitializeAnswersInTheClientsOwnProtocolVersionWhenItIsOneWeSpeak(t *testing.T) {
	server := newServer(t, newStub(t))

	for _, version := range protocolVersions {
		result := answer(t, server, 1, "initialize", map[string]any{"protocolVersion": version})
		if result.Error != nil {
			t.Fatalf("initialize failed: %s", result.Error.Message)
		}

		handshake, ok := result.Result.(initializeResult)
		if !ok {
			t.Fatalf("initialize returned %T", result.Result)
		}
		if handshake.ProtocolVersion != version {
			t.Errorf("answered in %q, want the client's own %q", handshake.ProtocolVersion, version)
		}
	}
}

// A client asking for something this server does not speak must be told what
// it DOES speak, so it can decide whether to carry on — answering in the
// unknown version would claim conformance to a revision nothing here has seen.
func TestInitializeFallsBackToOurNewestVersionForOneWeDoNotSpeak(t *testing.T) {
	server := newServer(t, newStub(t))

	result := answer(t, server, 1, "initialize", map[string]any{"protocolVersion": "1999-01-01"})
	handshake := result.Result.(initializeResult)

	if handshake.ProtocolVersion != protocolVersions[0] {
		t.Errorf("answered in %q, want our newest %q", handshake.ProtocolVersion, protocolVersions[0])
	}
	if handshake.ServerInfo.Name != "podsteer" {
		t.Errorf("server named %q", handshake.ServerInfo.Name)
	}
	if !strings.Contains(handshake.Instructions, "READ-ONLY") {
		t.Error("the instructions must say the tool set is read-only: an agent that thinks it can act will report that it has")
	}
}

func TestToolsListDeclaresEveryToolReadOnlyAndNotDestructive(t *testing.T) {
	server := newServer(t, newStub(t))

	result := answer(t, server, 1, "tools/list", nil)
	listed := result.Result.(toolList)

	if len(listed.Tools) != len(server.Tools()) {
		t.Fatalf("listed %d tools, built %d", len(listed.Tools), len(server.Tools()))
	}

	for _, tool := range listed.Tools {
		if !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s does not declare readOnlyHint", tool.Name)
		}
		if tool.Annotations.DestructiveHint {
			t.Errorf("%s declares destructiveHint", tool.Name)
		}
		if tool.Description == "" {
			t.Errorf("%s has no description, which is what a model chooses on", tool.Name)
		}
		if tool.InputSchema.Type != "object" || tool.InputSchema.AdditionalProperties {
			t.Errorf("%s must take an object that refuses unknown arguments", tool.Name)
		}
	}
}

func TestToolsCallReturnsTheAnswerAsTextContent(t *testing.T) {
	server := newServer(t, newStub(t))

	result := call(t, server, "list_clusters", nil)
	if result.IsError {
		t.Fatalf("list_clusters reported an error: %s", resultText(t, result))
	}

	var decoded struct {
		Clusters []clusterRow `json:"clusters"`
	}
	if err := json.Unmarshal([]byte(resultText(t, result)), &decoded); err != nil {
		t.Fatalf("the text content must be JSON a model can parse: %v", err)
	}
	if len(decoded.Clusters) != 1 || decoded.Clusters[0].Cluster != "staging" {
		t.Fatalf("got %+v", decoded.Clusters)
	}
}

// An unknown tool is a defect in the CALL, which the agent's runtime should
// correct — a text result saying "no such tool" invites the model to try the
// same name again.
func TestAnUnknownToolIsAProtocolErrorRatherThanAFailedResult(t *testing.T) {
	server := newServer(t, newStub(t))

	result := answer(t, server, 7, "tools/call", map[string]any{"name": "delete_pod", "arguments": map[string]any{}})
	if result.Error == nil {
		t.Fatal("an unknown tool must not succeed")
	}
	if result.Error.Code != rpcInvalidParams {
		t.Errorf("code %d, want %d", result.Error.Code, rpcInvalidParams)
	}
	if !strings.Contains(result.Error.Message, "delete_pod") {
		t.Errorf("the message must name the tool asked for: %q", result.Error.Message)
	}
}

func TestAnUnknownMethodIsMethodNotFound(t *testing.T) {
	server := newServer(t, newStub(t))

	result := answer(t, server, 1, "resources/list", nil)
	if result.Error == nil || result.Error.Code != rpcMethodNotFound {
		t.Fatalf("got %+v, want a method-not-found error — a client must be able to tell an unimplemented capability from an empty one", result.Error)
	}
}

func TestMalformedInputIsAnsweredWithAParseErrorAndTheLoopCarriesOn(t *testing.T) {
	server := newServer(t, newStub(t))

	in := strings.Join([]string{
		"{not json at all",
		string(requestLine(t, 2, "ping", nil)),
	}, "\n") + "\n"

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(in), &out); err != nil {
		t.Fatalf("Serve returned %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d answers, want one per request:\n%s", len(lines), out.String())
	}

	var parseFailure response
	if err := json.Unmarshal([]byte(lines[0]), &parseFailure); err != nil {
		t.Fatalf("decoding the first answer: %v", err)
	}
	if parseFailure.Error == nil || parseFailure.Error.Code != rpcParseError {
		t.Errorf("first answer was %+v, want a parse error", parseFailure)
	}
	if string(parseFailure.ID) != "null" {
		t.Errorf("an unparseable message has no id to echo, so the answer carries null; got %s", parseFailure.ID)
	}

	// The whole point: a bad line does not end the session.
	var pong response
	if err := json.Unmarshal([]byte(lines[1]), &pong); err != nil {
		t.Fatalf("decoding the second answer: %v", err)
	}
	if pong.Error != nil {
		t.Errorf("the request after a malformed one failed: %+v", pong.Error)
	}
}

// A response to a notification is a protocol violation, not merely noise, so
// this holds even when the notification names something unknown.
func TestANotificationIsNeverAnswered(t *testing.T) {
	server := newServer(t, newStub(t))

	for _, method := range []string{"notifications/initialized", "notifications/cancelled", "notifications/something/new"} {
		if _, replied := server.handle(context.Background(), requestLine(t, nil, method, nil)); replied {
			t.Errorf("%s was answered", method)
		}
	}
}

func TestServeAnswersEachRequestOnItsOwnLineAndReturnsAtEndOfInput(t *testing.T) {
	server := newServer(t, newStub(t))

	in := strings.Join([]string{
		string(requestLine(t, 1, "initialize", map[string]any{"protocolVersion": protocolVersions[0]})),
		string(requestLine(t, nil, "notifications/initialized", nil)),
		string(requestLine(t, 2, "tools/list", nil)),
	}, "\n") + "\n"

	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(in), &out); err != nil {
		t.Fatalf("Serve returned %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d answers, want 2 (the notification is not answered):\n%s", len(lines), out.String())
	}
	for _, line := range lines {
		var message response
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			t.Fatalf("every line must be one complete JSON-RPC message: %v", err)
		}
		if message.JSONRPC != "2.0" {
			t.Errorf("answer carried jsonrpc %q", message.JSONRPC)
		}
	}
}

// Arguments that do not fit the schema are a defect in the call, so they are
// reported as invalid params rather than as a tool result the model reads as
// an answer about the cluster.
func TestArgumentsThatDoNotFitTheSchemaAreAnInvalidParamsError(t *testing.T) {
	server := newServer(t, newStub(t))

	cases := map[string]map[string]any{
		"missing required": {},
		"unknown argument": {"cluster": "staging", "namesapce": "shop"},
		"wrong type":       {"cluster": 42},
		"empty string":     {"cluster": "   "},
	}

	for name, arguments := range cases {
		t.Run(name, func(t *testing.T) {
			result := answer(t, server, 1, "tools/call",
				map[string]any{"name": "list_pods", "arguments": arguments})
			if result.Error == nil {
				t.Fatalf("accepted %v", arguments)
			}
			if result.Error.Code != rpcInvalidParams {
				t.Errorf("code %d, want %d", result.Error.Code, rpcInvalidParams)
			}
		})
	}
}

// An operator who started this by hand and pressed Ctrl-C must get their
// terminal back: signal.NotifyContext has taken the default handler away, and
// a read of a pipe cannot be interrupted, so the cancellation has to be
// noticed beside the read rather than after it.
func TestServeReturnsOnCancellationEvenWhileWaitingForAMessage(t *testing.T) {
	server := newServer(t, newStub(t))

	// A pipe with no writer: the read blocks until this test's context ends.
	blocked, writer := io.Pipe()
	//nolint:errcheck // closing a pipe nothing wrote to has no recoverable failure
	defer writer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, blocked, io.Discard) }()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned %v, want nil on cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after its context was cancelled")
	}
}

// A client that fills in the defaults it was given must not have its call
// rejected for obeying them.
func TestNoSchemaAdvertisesADefaultItsOwnBoundsWouldRefuse(t *testing.T) {
	server := newServer(t, newStub(t))

	for _, tool := range server.Tools() {
		for name, property := range tool.Schema.Properties {
			fallback, declared := property.Default.(int64)
			if !declared {
				continue
			}
			if property.Minimum != nil && fallback < *property.Minimum {
				t.Errorf("%s.%s defaults to %d, below its own minimum %d", tool.Name, name, fallback, *property.Minimum)
			}
			if property.Maximum != nil && fallback > *property.Maximum {
				t.Errorf("%s.%s defaults to %d, above its own maximum %d", tool.Name, name, fallback, *property.Maximum)
			}
		}
	}
}

func TestPingIsAnsweredWithAnEmptyResult(t *testing.T) {
	server := newServer(t, newStub(t))

	result := answer(t, server, 3, "ping", nil)
	if result.Error != nil {
		t.Fatalf("ping failed: %s", result.Error.Message)
	}
	encoded, err := json.Marshal(result.Result)
	if err != nil {
		t.Fatalf("encoding ping result: %v", err)
	}
	if string(encoded) != "{}" {
		t.Errorf("ping answered %s, want {}", encoded)
	}
}
