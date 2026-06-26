package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
)

// readFrame parses one Content-Length-framed JSON-RPC message body into a map.
func readFrame(t *testing.T, r *bufio.Reader) map[string]json.RawMessage {
	t.Helper()
	n := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read header: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(line, "Content-Length:"); ok {
			n, _ = strconv.Atoi(strings.TrimSpace(v))
		}
	}
	if n < 0 {
		t.Fatal("frame missing Content-Length")
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode body %q: %v", body, err)
	}
	return m
}

// newTestServer builds a Server wired to read nothing and write into buf. The
// planner/binMgr/executor are nil — fine for protocol/lifecycle tests that never
// reach formatting.
func newTestServer(buf *bytes.Buffer) *Server {
	return &Server{conn: newConn(strings.NewReader(""), buf), docs: make(map[string][]byte)}
}

func TestConnFramingRoundTripWithNewlineBody(t *testing.T) {
	var buf bytes.Buffer
	wc := newConn(strings.NewReader(""), &buf)

	// A params body containing newlines must round-trip exactly: the reader must
	// use Content-Length + ReadFull, never a line-based read.
	type note struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  struct {
			Text string `json:"text"`
		} `json:"params"`
	}
	var n note
	n.JSONRPC = "2.0"
	n.Method = "textDocument/didOpen"
	n.Params.Text = "line1\nline2\nline3\n"
	if err := wc.write(n); err != nil {
		t.Fatalf("write: %v", err)
	}

	rc := newConn(bytes.NewReader(buf.Bytes()), io.Discard)
	body, err := rc.readFrame()
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	var m message
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.Method != "textDocument/didOpen" {
		t.Errorf("method = %q", m.Method)
	}
	var p struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if p.Text != "line1\nline2\nline3\n" {
		t.Errorf("body not framed exactly: %q", p.Text)
	}
	if m.isRequest() {
		t.Error("notification should not be a request (no id)")
	}
}

func TestReadFrameRejectsOverCapLength(t *testing.T) {
	// An advertised length beyond the cap is a fatal framing error, not a
	// multi-gigabyte allocation.
	raw := fmt.Sprintf("Content-Length: %d\r\n\r\n", maxMessageBytes+1)
	c := newConn(strings.NewReader(raw), io.Discard)
	if _, err := c.readFrame(); err == nil {
		t.Error("expected error for over-cap Content-Length")
	}
}

func TestRunRecoversFromInvalidJSONFrame(t *testing.T) {
	// A frame with invalid JSON must NOT kill the server: it replies Parse Error
	// and the following valid shutdown/exit is still processed.
	var out bytes.Buffer
	in := frameStr(`{x}`) + // invalid JSON body
		frameStr(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`) +
		frameStr(`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`) +
		frameStr(`{"jsonrpc":"2.0","method":"exit"}`)
	s := &Server{conn: newConn(strings.NewReader(in), &out), docs: make(map[string][]byte)}

	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("Run returned fatal error on a recoverable bad frame: %v", err)
	}
	if !s.shutdownReceived {
		t.Error("shutdown after a bad frame was not processed — server died on the bad frame")
	}
	if s.ExitCode() != 0 {
		t.Errorf("exit code = %d, want 0", s.ExitCode())
	}
	frames := allFrames(t, out.Bytes())
	// First reply must be a Parse Error with id:null.
	var perr struct {
		ID    json.RawMessage `json:"id"`
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(frames[0], &perr); err != nil {
		t.Fatalf("decode first reply: %v", err)
	}
	if perr.Error.Code != codeParseError {
		t.Errorf("first reply code = %d, want %d (parse error)", perr.Error.Code, codeParseError)
	}
	if string(perr.ID) != "null" {
		t.Errorf("parse-error id = %s, want null", perr.ID)
	}
}

func TestRequestAfterShutdownIsInvalidRequest(t *testing.T) {
	var buf bytes.Buffer
	s := newTestServer(&buf)
	s.initialized = true
	s.shutdownReceived = true
	ctx := context.Background()

	s.handle(ctx, msg(t, "7", "textDocument/formatting", formattingParams{
		TextDocument: textDocumentIdentifier{URI: "file:///tmp/x.txt"},
	}))
	frame := readFrame(t, bufio.NewReader(bytes.NewReader(buf.Bytes())))
	var respErr struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(frame["error"], &respErr); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if respErr.Code != codeInvalidRequest {
		t.Errorf("post-shutdown request code = %d, want %d (InvalidRequest)", respErr.Code, codeInvalidRequest)
	}
}

// frameStr wraps body in Content-Length framing for driving Run in tests.
func frameStr(body string) string {
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
}

// allFrames decodes every framed message body in data.
func allFrames(t *testing.T, data []byte) []json.RawMessage {
	t.Helper()
	c := newConn(bytes.NewReader(data), io.Discard)
	var out []json.RawMessage
	for {
		body, err := c.readFrame()
		if err != nil {
			return out
		}
		out = append(out, body)
	}
}

func msg(t *testing.T, id, method string, params any) *message {
	t.Helper()
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal params: %v", err)
		}
		raw = b
	}
	m := &message{JSONRPC: "2.0", Method: method, Params: raw}
	if id != "" {
		m.ID = json.RawMessage(id)
	}
	return m
}

func TestLifecycleInitializeShutdownExit(t *testing.T) {
	var buf bytes.Buffer
	s := newTestServer(&buf)
	ctx := context.Background()

	if stop := s.handle(ctx, msg(t, "1", "initialize", map[string]any{})); stop {
		t.Fatal("initialize should not stop the loop")
	}
	if !s.initialized {
		t.Error("server should be initialized")
	}
	if stop := s.handle(ctx, msg(t, "2", "shutdown", nil)); stop {
		t.Fatal("shutdown should not stop the loop")
	}
	if !s.shutdownReceived {
		t.Error("shutdownReceived should be set")
	}
	if stop := s.handle(ctx, msg(t, "", "exit", nil)); !stop {
		t.Fatal("exit should stop the loop")
	}
	if s.ExitCode() != 0 {
		t.Errorf("exit code = %d, want 0 (shutdown preceded exit)", s.ExitCode())
	}

	rd := bufio.NewReader(bytes.NewReader(buf.Bytes()))
	// First frame: initialize result with documentFormattingProvider.
	init := readFrame(t, rd)
	var res initializeResult
	if err := json.Unmarshal(init["result"], &res); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if !res.Capabilities.DocumentFormattingProvider {
		t.Error("initialize must advertise documentFormattingProvider")
	}
	if res.Capabilities.TextDocumentSync.Change != textDocumentSyncFull {
		t.Errorf("textDocumentSync.change = %d, want %d (Full)", res.Capabilities.TextDocumentSync.Change, textDocumentSyncFull)
	}
	// Second frame: shutdown reply, result null.
	sh := readFrame(t, rd)
	if string(sh["result"]) != "null" {
		t.Errorf("shutdown result = %s, want null", sh["result"])
	}
}

func TestExitBeforeShutdownIsExitCode1(t *testing.T) {
	s := newTestServer(&bytes.Buffer{})
	s.initialized = true
	if stop := s.handle(context.Background(), msg(t, "", "exit", nil)); !stop {
		t.Fatal("exit should stop the loop")
	}
	if s.ExitCode() != 1 {
		t.Errorf("exit code = %d, want 1 (exit without prior shutdown)", s.ExitCode())
	}
}

func TestDocStoreOpenChangeClose(t *testing.T) {
	s := newTestServer(&bytes.Buffer{})
	s.initialized = true
	ctx := context.Background()
	uri := "file:///tmp/x.txt"

	s.handle(ctx, msg(t, "", "textDocument/didOpen", didOpenParams{
		TextDocument: textDocumentItem{URI: uri, Text: "v1\n"},
	}))
	if string(s.docs[uri]) != "v1\n" {
		t.Errorf("after didOpen docs[uri] = %q", s.docs[uri])
	}

	s.handle(ctx, msg(t, "", "textDocument/didChange", didChangeParams{
		TextDocument:   textDocumentIdentifier{URI: uri},
		ContentChanges: []contentChange{{Text: "v2\n"}},
	}))
	if string(s.docs[uri]) != "v2\n" {
		t.Errorf("after didChange docs[uri] = %q, want v2", s.docs[uri])
	}

	s.handle(ctx, msg(t, "", "textDocument/didClose", didCloseParams{
		TextDocument: textDocumentIdentifier{URI: uri},
	}))
	if _, ok := s.docs[uri]; ok {
		t.Error("after didClose the doc should be gone")
	}
}

func TestFormattingBeforeInitializeIsRejected(t *testing.T) {
	var buf bytes.Buffer
	s := newTestServer(&buf)
	ctx := context.Background()

	if stop := s.handle(ctx, msg(t, "9", "textDocument/formatting", formattingParams{
		TextDocument: textDocumentIdentifier{URI: "file:///tmp/x.txt"},
	})); stop {
		t.Fatal("formatting should not stop the loop")
	}
	frame := readFrame(t, bufio.NewReader(bytes.NewReader(buf.Bytes())))
	if _, ok := frame["error"]; !ok {
		t.Errorf("expected an error response before initialize, got %v", frame)
	}
}

func TestUnknownRequestReturnsMethodNotFound(t *testing.T) {
	var buf bytes.Buffer
	s := newTestServer(&buf)
	s.initialized = true
	s.handle(context.Background(), msg(t, "5", "textDocument/rangeFormatting", map[string]any{}))
	frame := readFrame(t, bufio.NewReader(bytes.NewReader(buf.Bytes())))
	var respErr struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(frame["error"], &respErr); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if respErr.Code != codeMethodNotFound {
		t.Errorf("error code = %d, want %d", respErr.Code, codeMethodNotFound)
	}
}

func TestURIToPath(t *testing.T) {
	got, err := uriToPath("file:///home/u/a%20b.go")
	if err != nil {
		t.Fatalf("uriToPath: %v", err)
	}
	if got != "/home/u/a b.go" {
		t.Errorf("path = %q, want /home/u/a b.go (percent-decoded)", got)
	}
	if _, err := uriToPath("http://example.com/x"); err == nil {
		t.Error("expected error for non-file scheme")
	}
}
