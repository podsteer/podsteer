package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// JSON-RPC 2.0 error codes, as the Model Context Protocol uses them.
//
// The split between these and a tool FAILURE is load-bearing and is the one
// part of the protocol worth stating here: a code below means the message
// itself was wrong — unparseable, or naming a tool that does not exist, or
// carrying arguments that do not fit the schema — and the agent's runtime
// deals with it. Anything that went wrong while ANSWERING a well-formed call
// is a successful result carrying isError, so the model reads the refusal and
// can act on it. Reporting a 403 as an internal error would hide it inside the
// agent's plumbing.
const (
	rpcParseError     = -32700
	rpcInvalidRequest = -32600
	rpcMethodNotFound = -32601
	rpcInvalidParams  = -32602
	rpcInternalError  = -32603
)

// maxMessageBytes bounds one incoming line.
//
// The peer is a process on this machine that the operator started, so this is
// not a defence against an attacker; it is a defence against a stream that is
// not JSON-RPC at all — a binary file piped in by mistake — turning into an
// allocation the size of the file.
const maxMessageBytes = 4 << 20

// request is one incoming JSON-RPC message.
//
// ID is raw because JSON-RPC allows a string or a number and the value is
// echoed rather than interpreted; decoding it into a Go type would force a
// choice the protocol deliberately leaves open. Absent means the message is a
// NOTIFICATION, which must never be answered — a response to one is a
// protocol violation, not merely noise.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// isNotification reports whether the message expects no answer.
func (r request) isNotification() bool { return len(r.ID) == 0 }

// response is one outgoing JSON-RPC message.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is the error member of a response.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// newResult builds a success response for id.
func newResult(id json.RawMessage, result any) response {
	return response{JSONRPC: "2.0", ID: idOrNull(id), Result: result}
}

// newError builds a failure response for id.
func newError(id json.RawMessage, code int, format string, args ...any) response {
	return response{
		JSONRPC: "2.0",
		ID:      idOrNull(id),
		Error:   &rpcError{Code: code, Message: fmt.Sprintf(format, args...)},
	}
}

// idOrNull renders a missing id as JSON null.
//
// A parse failure is the case: the id could not be read, and JSON-RPC says
// the response carries null rather than omitting the member, so the peer can
// still match it to the fact that SOMETHING it sent was unusable.
func idOrNull(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

// reader reads newline-delimited JSON-RPC messages.
//
// The stdio transport frames one message per line and forbids embedded
// newlines, so a line is a message. Reading lines rather than streaming
// through a json.Decoder is what makes a malformed message survivable: the
// decoder would be left mid-value with no way to resynchronise, whereas a bad
// line can be answered with a parse error and the next one read normally.
type reader struct {
	scanner *bufio.Scanner
}

// newReader wraps in for line-delimited reading.
func newReader(in io.Reader) *reader {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), maxMessageBytes)
	return &reader{scanner: scanner}
}

// next returns the next non-empty line, or io.EOF when the stream ends.
func (r *reader) next() ([]byte, error) {
	for r.scanner.Scan() {
		line := r.scanner.Bytes()
		if len(trimSpace(line)) == 0 {
			continue
		}
		// The scanner reuses its buffer, so the caller gets a copy: the line
		// outlives this call by the length of one request handler.
		return append([]byte(nil), line...), nil
	}
	if err := r.scanner.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

// trimSpace drops leading and trailing ASCII whitespace.
func trimSpace(line []byte) []byte {
	start := 0
	for start < len(line) && isSpace(line[start]) {
		start++
	}
	end := len(line)
	for end > start && isSpace(line[end-1]) {
		end--
	}
	return line[start:end]
}

// isSpace reports whether b is JSON insignificant whitespace.
func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\r' || b == '\n' }

// writer writes newline-delimited JSON-RPC messages.
type writer struct {
	out *bufio.Writer
}

// newWriter wraps out for line-delimited writing.
func newWriter(out io.Writer) *writer { return &writer{out: bufio.NewWriter(out)} }

// write encodes one message and flushes it.
//
// FLUSHED EVERY TIME, because the peer is waiting on this answer before it
// sends anything else: a buffered response is a hung agent, and there is no
// second message coming along to push it out.
func (w *writer) write(message response) error {
	encoded, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encoding response: %w", err)
	}
	if _, err := w.out.Write(encoded); err != nil {
		return fmt.Errorf("writing response: %w", err)
	}
	if err := w.out.WriteByte('\n'); err != nil {
		return fmt.Errorf("writing response: %w", err)
	}
	return w.out.Flush()
}
