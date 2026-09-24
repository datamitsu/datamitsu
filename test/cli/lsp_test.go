package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
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
	stderrEvents(t, res.Stderr)
}

// stderrEvents decodes a JSON-L stderr and fails on any line that is not a JSON
// object carrying type and op_id: stderr is the typed event stream and nothing
// else, --verbose included, so an editor can parse every line.
func stderrEvents(t *testing.T, stderr string) []map[string]any {
	t.Helper()
	var events []map[string]any
	for line := range strings.SplitSeq(stderr, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e map[string]any
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Errorf("stderr line is not JSON: %q", line)
			continue
		}
		if typ, ok := e["type"].(string); !ok || typ == "" {
			t.Errorf("stderr event without a type: %q", line)
		}
		if id, ok := e["op_id"].(string); !ok || id == "" {
			t.Errorf("stderr event without an op_id: %q", line)
		}
		events = append(events, e)
	}
	return events
}

// lspPolicyConfigJS has two per-file fix tools on *.txt; the author vetoes one
// from the editor with lsp: false.
const lspPolicyConfigJS = `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = () => ({
  apps: {
    "append-a": { shell: { name: "sh", args: ["-c", "printf a >> \"$1\"", "append-a"] } },
    "append-b": { shell: { name: "sh", args: ["-c", "printf b >> \"$1\"", "append-b"] } },
  },
  runtimes: {},
  managedConfigs: {},
  tools: {
    kept: {
      name: "kept",
      operations: { fix: { app: "append-a", args: ["{file}"], scope: "per-file", globs: ["**/*.txt"] } },
    },
    vetoed: {
      name: "vetoed",
      operations: { fix: { app: "append-b", args: ["{file}"], scope: "per-file", globs: ["**/*.txt"], lsp: false } },
    },
  },
});
globalThis.getMinVersion = () => "0.0.0";
`

// TestLspSessionPolicy locks the editor-facing policy contract: initialize
// echoes the effective policy under capabilities.experimental.datamitsu with
// rejected options left out, an operation declaring lsp: false never runs on
// save even when the editor opts it in, and every stderr line is a typed event.
func TestLspSessionPolicy(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := p.WriteFile("policy.config.js", lspPolicyConfigJS)
	file := p.WriteFile("note.txt", "x")
	uri := "file://" + file

	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"initializationOptions":` +
		`{"format":{"widenTo":"target","timeoutMs":0,"tools":{"kept":true,"vetoed":true,"nonesuch":true}}}}}`
	open := fmt.Sprintf(
		`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":%q,"text":"x"}}}`, uri)
	format := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":2,"method":"textDocument/formatting","params":{"textDocument":{"uri":%q}}}`, uri)

	session := frame(initialize) +
		frame(`{"jsonrpc":"2.0","method":"initialized","params":{}}`) +
		frame(open) +
		frame(format) +
		frame(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`) +
		frame(`{"jsonrpc":"2.0","method":"exit"}`)

	res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, Stdin: session},
		"lsp", "--no-auto-config", "--config", cfg)
	if res.ExitCode != 0 {
		t.Fatalf("lsp exited %d\n--- stdout ---\n%s\n--- stderr ---\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}

	byID := map[string]map[string]json.RawMessage{}
	for _, f := range parseAllFrames(t, []byte(res.Stdout)) {
		byID[strings.TrimSpace(string(f["id"]))] = f
	}

	var initRes struct {
		Capabilities struct {
			Experimental struct {
				Datamitsu struct {
					Format json.RawMessage `json:"format"`
				} `json:"datamitsu"`
			} `json:"experimental"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(byID["1"]["result"], &initRes); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	wantEcho := `{"widenTo":"target","timeoutMs":0,"tools":{"kept":true,"vetoed":true}}`
	if got := string(initRes.Capabilities.Experimental.Datamitsu.Format); got != wantEcho {
		t.Errorf("capabilities.experimental.datamitsu.format =\n  %s\nwant\n  %s", got, wantEcho)
	}

	// The buffer matched disk, so the fix happens in place and no edits return.
	if got := strings.TrimSpace(string(byID["2"]["result"])); got != "[]" {
		t.Errorf("formatting result = %s, want []", got)
	}
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "xa" {
		t.Errorf("note.txt = %q, want %q: kept runs, the lsp: false operation never does", got, "xa")
	}

	var sawUnknownTool, sawVeto, sawDone bool
	for _, e := range stderrEvents(t, res.Stderr) {
		msg, _ := e["msg"].(string)
		switch e["type"] {
		case "log":
			if e["level"] == "warn" && strings.Contains(msg, "format.tools.nonesuch: unknown tool") {
				sawUnknownTool = true
			}
			if e["level"] == "info" && strings.Contains(msg, "vetoed (lsp: false in config)") {
				sawVeto = true
			}
		case "done":
			if e["op"] == "format" {
				sawDone = true
				if e["runs"] != float64(1) || e["skipped"] != float64(1) || e["success"] != true {
					t.Errorf("format done = %v, want runs 1, skipped 1, success", e)
				}
			}
		}
	}
	if !sawUnknownTool {
		t.Errorf("no warning for the unknown tool in initializationOptions:\n%s", res.Stderr)
	}
	if !sawVeto {
		t.Errorf("no notice naming the vetoed operation:\n%s", res.Stderr)
	}
	if !sawDone {
		t.Errorf("no done event closing the format request:\n%s", res.Stderr)
	}
}

// noisyConfigJS logs while it evaluates. On a JSON-L stream a config's console
// output is a debug log line, so --verbose makes it a log event.
const noisyConfigJS = `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = () => {
  console.log("hello from config");
  return { apps: {}, runtimes: {}, managedConfigs: {}, tools: {} };
};
globalThis.getMinVersion = () => "0.0.0";
`

// Log lines join a JSON-L stream as log events, in the language server and under
// --log-format=jsonl alike, so --verbose or a warning never puts a line into the
// stream that its consumer cannot parse.
func TestLogLinesAreEventsOnJSONLStream(t *testing.T) {
	lspSession := frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`) +
		frame(`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`) +
		frame(`{"jsonrpc":"2.0","method":"exit"}`)

	tests := []struct {
		name  string
		args  []string
		stdin string
	}{
		{name: "lsp --verbose", args: []string{"lsp", "--verbose"}, stdin: lspSession},
		{name: "--log-format=jsonl --verbose", args: []string{"config", "show", "--log-format=jsonl", "--verbose"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := clitest.NewProject(t)
			cfg := p.WriteFile("noisy.config.js", noisyConfigJS)
			args := append(append([]string{}, tt.args...), "--no-auto-config", "--config", cfg)

			res := clitest.Run(t, clitest.RunOptions{Dir: p.Dir, Stdin: tt.stdin}, args...)
			if res.ExitCode != 0 {
				t.Fatalf("exit %d\n--- stderr ---\n%s", res.ExitCode, res.Stderr)
			}

			var logged bool
			for _, e := range stderrEvents(t, res.Stderr) {
				msg, _ := e["msg"].(string)
				if e["type"] == "log" && e["level"] == "debug" && strings.HasPrefix(msg, "hello from config ") {
					logged = true
				}
			}
			if !logged {
				t.Errorf("the config's console output is not a debug log event:\n%s", res.Stderr)
			}
		})
	}
}

// The startup-timing and trace reports are opt-in diagnostics an editor can pass
// down to the language server through its environment. On its stderr each is one
// log event, never lines of text.
func TestDiagnosticReportsAreEventsInLsp(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := p.WriteFile("noisy.config.js", noisyConfigJS)
	session := frame(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`) +
		frame(`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`) +
		frame(`{"jsonrpc":"2.0","method":"exit"}`)

	res := clitest.Run(t, clitest.RunOptions{
		Dir:   p.Dir,
		Stdin: session,
		Env: []string{
			"DATAMITSU_STARTUP_TIMINGS=1",
			"DATAMITSU_TRACE=1",
			"DATAMITSU_TRACE_DIR=" + t.TempDir(),
		},
	}, "lsp", "--no-auto-config", "--config", cfg)
	if res.ExitCode != 0 {
		t.Fatalf("lsp exited %d\n--- stderr ---\n%s", res.ExitCode, res.Stderr)
	}

	reports := map[string]bool{"Startup phases": false, "trace": false}
	for _, e := range stderrEvents(t, res.Stderr) {
		msg, _ := e["msg"].(string)
		for heading := range reports {
			if e["type"] == "log" && e["level"] == "info" && strings.HasPrefix(msg, "⏱  "+heading) {
				reports[heading] = true
			}
		}
	}
	for heading, seen := range reports {
		if !seen {
			t.Errorf("no info log event carries the %q report:\n%s", heading, res.Stderr)
		}
	}
}
