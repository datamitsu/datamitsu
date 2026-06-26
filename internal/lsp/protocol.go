// Package lsp implements a minimal, formatting-only Language Server Protocol
// server over stdio. It speaks just enough LSP to register as a document
// formatter — initialize/initialized/shutdown/exit, didOpen/didChange/didClose
// document tracking, and textDocument/formatting — and delegates the actual
// formatting to datamitsu's existing fix tools (stdin->stdout) plus the in-core
// line diff. It deliberately implements NO diagnostics and loads NO WASM parsers.
//
// The transport is hand-rolled Content-Length-framed JSON-RPC 2.0 (no external
// dependency). stdout carries ONLY framed JSON-RPC; all human/status output is
// diverted to stderr by the cmd layer (the process runs in JSON-L quiet mode).
package lsp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// JSON-RPC / LSP error codes used by this server.
const (
	codeParseError     = -32700 // body was not valid JSON
	codeInvalidRequest = -32600 // valid JSON but not a valid request (e.g. after shutdown)
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeRequestFailed  = -32803 // LSP-specific: a request failed but the server is healthy
	codeServerNotReady = -32002 // ServerNotInitialized
)

// maxMessageBytes caps an advertised Content-Length so a bogus or out-of-sync
// header can't trigger a multi-gigabyte allocation. No legitimate LSP message — even a
// whole-file didChange — approaches this; over-cap is treated as a fatal framing
// error since the stream can no longer be trusted.
const maxMessageBytes = 256 << 20 // 256 MiB

// textDocumentSyncFull tells the client to send the whole document text on every
// change (the simplest sync mode that still gives us the live, unsaved buffer).
const textDocumentSyncFull = 1

// message is an inbound JSON-RPC message. A request carries ID + Method; a
// notification carries Method with no ID; a response (never received here)
// carries ID + Result/Error.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// isRequest reports whether the message expects a response (has an id).
func (m *message) isRequest() bool { return len(m.ID) > 0 }

type successResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"` // always present (may be the literal "null")
}

type errorResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Error   responseError   `json:"error"`
}

type responseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// conn frames JSON-RPC messages over a reader/writer pair (stdin/stdout).
type conn struct {
	r   *bufio.Reader
	w   io.Writer
	wmu sync.Mutex // serializes writes; reads are single-threaded by the server loop
}

func newConn(r io.Reader, w io.Writer) *conn {
	return &conn{r: bufio.NewReader(r), w: w}
}

// readFrame returns the next framed message body, or io.EOF when the stream
// closes. Every error it returns is FATAL (the stream can no longer be trusted to
// be in sync): EOF, a header it cannot parse, an over-cap length, or a short body
// read. A body that is read in full but contains invalid JSON is NOT this layer's
// concern — it is decoded by the caller, which can recover with a Parse Error
// response because exactly Content-Length bytes were consumed and the stream stays
// framed.
func (c *conn) readFrame() ([]byte, error) {
	contentLength := -1
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return nil, err //nolint:wrapcheck // io.EOF must pass through unwrapped so the loop can detect stream close
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // blank line terminates the header block
		}
		if v, ok := strings.CutPrefix(line, "Content-Length:"); ok {
			n, convErr := strconv.Atoi(strings.TrimSpace(v))
			if convErr != nil {
				return nil, fmt.Errorf("invalid Content-Length %q: %w", v, convErr)
			}
			contentLength = n
		}
		// Other headers (e.g. Content-Type) are ignored.
	}
	if contentLength < 0 {
		return nil, errors.New("message header missing Content-Length")
	}
	if contentLength > maxMessageBytes {
		return nil, fmt.Errorf("message length %d exceeds limit %d", contentLength, maxMessageBytes)
	}

	// Read EXACTLY contentLength bytes — a bufio ReadString on the body would
	// break framing on any payload containing a newline.
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(c.r, body); err != nil {
		return nil, fmt.Errorf("read message body: %w", err)
	}
	return body, nil
}

// reply writes a successful response. result==nil serializes as JSON null
// (correct for the shutdown reply); a value serializes normally.
func (c *conn) reply(id json.RawMessage, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	return c.write(successResponse{JSONRPC: "2.0", ID: id, Result: raw})
}

// replyError writes an error response for a request id.
func (c *conn) replyError(id json.RawMessage, code int, msg string) error {
	return c.write(errorResponse{JSONRPC: "2.0", ID: id, Error: responseError{Code: code, Message: msg}})
}

func (c *conn) write(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if _, err := c.w.Write(body); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	return nil
}

// --- LSP types (only the subset this server needs) -------------------------

// Position is a zero-based line/character offset. Per LSP, character is in UTF-16
// code units — but every edit this server emits sits on a line boundary
// (character==0), where bytes, runes and UTF-16 units coincide, so no encoding
// math is ever performed. See convert.go.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is a half-open [Start, End) span.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// TextEdit replaces Range with NewText.
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

type textDocumentIdentifier struct {
	URI string `json:"uri"`
}

type textDocumentItem struct {
	URI  string `json:"uri"`
	Text string `json:"text"`
}

type didOpenParams struct {
	TextDocument textDocumentItem `json:"textDocument"`
}

type didCloseParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
}

type contentChange struct {
	Text string `json:"text"` // Full-sync: the entire new document text
}

type didChangeParams struct {
	TextDocument   textDocumentIdentifier `json:"textDocument"`
	ContentChanges []contentChange        `json:"contentChanges"`
}

type formattingParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
}

type initializeResult struct {
	Capabilities serverCapabilities `json:"capabilities"`
	ServerInfo   serverInfo         `json:"serverInfo"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type serverCapabilities struct {
	PositionEncoding           string                  `json:"positionEncoding"`
	TextDocumentSync           textDocumentSyncOptions `json:"textDocumentSync"`
	DocumentFormattingProvider bool                    `json:"documentFormattingProvider"`
}

type textDocumentSyncOptions struct {
	OpenClose bool `json:"openClose"`
	Change    int  `json:"change"`
}
