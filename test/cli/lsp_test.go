package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// frame wraps a JSON-RPC body in LSP Content-Length framing.
func frame(body string) string {
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
}

// parseAllFrames decodes every Content-Length-framed message in data and asserts
// that NOTHING outside the frames is present — the hard LSP contract that stdout
// carries only framed JSON-RPC. A stray byte (a leaked log/human line) makes a
// header parse fail or leaves a non-zero remainder, failing the test.
func parseAllFrames(t *testing.T, data []byte) []map[string]json.RawMessage {
	t.Helper()
	var frames []map[string]json.RawMessage
	i := 0
	for i < len(data) {
		rel := bytes.Index(data[i:], []byte("\r\n\r\n"))
		if rel < 0 {
			t.Fatalf("stdout has non-frame bytes at offset %d: %q", i, data[i:])
		}
		header := string(data[i : i+rel])
		n := -1
		for line := range strings.SplitSeq(header, "\r\n") {
			if v, ok := strings.CutPrefix(line, "Content-Length:"); ok {
				parsed, err := strconv.Atoi(strings.TrimSpace(v))
				if err != nil {
					t.Fatalf("bad Content-Length %q: %v", v, err)
				}
				n = parsed
			}
		}
		if n < 0 {
			t.Fatalf("frame header missing Content-Length: %q", header)
		}
		bodyStart := i + rel + 4
		if bodyStart+n > len(data) {
			t.Fatalf("frame body truncated: need %d bytes from %d, have %d", n, bodyStart, len(data))
		}
		body := data[bodyStart : bodyStart+n]
		var m map[string]json.RawMessage
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatalf("frame body is not JSON: %q: %v", body, err)
		}
		frames = append(frames, m)
		i = bodyStart + n
	}
	return frames
}

// TestLspFormattingSession drives a full LSP session (initialize, didOpen,
// formatting, shutdown, exit) against an empty-tools config and locks the
// contract: stdout is ONLY framed JSON-RPC, initialize advertises
// documentFormattingProvider, a file with no applicable formatter returns the
// empty array (not null), and the process exits cleanly. Real formatter edits are
// covered by the FormatContent unit test and the manual dog-food.
func TestLspFormattingSession(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := clitest.WriteMinimalConfig(p)
	file := p.WriteFile("sample.txt", "hello world\n")
	uri := "file://" + file

	open := fmt.Sprintf(
		`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":%q,"text":"hello world\n"}}}`, uri)
	format := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":2,"method":"textDocument/formatting","params":{"textDocument":{"uri":%q}}}`, uri)

	session := frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`) +
		frame(`{"jsonrpc":"2.0","method":"initialized","params":{}}`) +
		frame(open) +
		frame(format) +
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`) +
		frame(`{"jsonrpc":"2.0","method":"exit"}`)

	res := clitest.Run(t, clitest.RunOptions{
		Dir:   p.Dir,
		Stdin: session,
	}, "lsp", "--no-auto-config", "--config", cfg)

	if res.ExitCode != 0 {
		t.Fatalf("lsp exited %d\n--- stdout ---\n%s\n--- stderr ---\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}

	// Stdout must be exactly the framed responses (3 requests => 3 responses),
	// nothing else.
	frames := parseAllFrames(t, []byte(res.Stdout))
	if len(frames) != 3 {
		t.Fatalf("got %d response frames, want 3: %s", len(frames), res.Stdout)
	}

	byID := map[string]map[string]json.RawMessage{}
	for _, f := range frames {
		byID[strings.TrimSpace(string(f["id"]))] = f
	}

	// initialize: capabilities.documentFormattingProvider == true.
	initRes := byID["1"]
	if initRes == nil {
		t.Fatal("missing response for initialize (id 1)")
	}
	var capWrap struct {
		Capabilities struct {
			DocumentFormattingProvider bool `json:"documentFormattingProvider"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(initRes["result"], &capWrap); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if !capWrap.Capabilities.DocumentFormattingProvider {
		t.Error("initialize must advertise documentFormattingProvider")
	}

	// formatting: a file with no configured formatter yields [] (not null).
	fmtRes := byID["2"]
	if fmtRes == nil {
		t.Fatal("missing response for formatting (id 2)")
	}
	if got := strings.TrimSpace(string(fmtRes["result"])); got != "[]" {
		t.Errorf("formatting result = %s, want [] (no applicable formatter)", got)
	}

	// shutdown: result null.
	shRes := byID["3"]
	if shRes == nil {
		t.Fatal("missing response for shutdown (id 3)")
	}
	if got := strings.TrimSpace(string(shRes["result"])); got != "null" {
		t.Errorf("shutdown result = %s, want null", got)
	}

	// No LSP framing must ever appear on stderr.
	if strings.Contains(res.Stderr, "Content-Length:") {
		t.Errorf("LSP framing leaked onto stderr:\n%s", res.Stderr)
	}
}
