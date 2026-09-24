package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

	lsp := startLsp(t, p.Dir, nil, "--no-auto-config", "--config", cfg)
	lsp.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	initRes := lsp.await("1")
	lsp.send(`{"jsonrpc":"2.0","method":"initialized","params":{}}`)
	lsp.send(fmt.Sprintf(
		`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":%q,"text":"hello world\n"}}}`, uri))
	lsp.send(fmt.Sprintf(
		`{"jsonrpc":"2.0","id":2,"method":"textDocument/formatting","params":{"textDocument":{"uri":%q}}}`, uri))
	fmtRes := lsp.await("2")
	res := lsp.finish()

	if res.ExitCode != 0 {
		t.Fatalf("lsp exited %d\n--- stdout ---\n%s\n--- stderr ---\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	// Stdout must be exactly the framed responses (3 requests => 3 responses),
	// nothing else.
	if frames := parseAllFrames(t, []byte(res.Stdout)); len(frames) != 3 {
		t.Fatalf("got %d response frames, want 3: %s", len(frames), res.Stdout)
	}

	// initialize: capabilities.documentFormattingProvider == true, and the root
	// the server took from its launch directory, since initialize named none.
	var capWrap struct {
		Capabilities struct {
			DocumentFormattingProvider bool `json:"documentFormattingProvider"`
			Experimental               struct {
				Datamitsu struct {
					Root string `json:"root"`
				} `json:"datamitsu"`
			} `json:"experimental"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(initRes["result"], &capWrap); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if !capWrap.Capabilities.DocumentFormattingProvider {
		t.Error("initialize must advertise documentFormattingProvider")
	}
	if got := capWrap.Capabilities.Experimental.Datamitsu.Root; got != p.Dir {
		t.Errorf("capabilities.experimental.datamitsu.root = %q, want the launch directory's root %q", got, p.Dir)
	}

	// formatting: a file with no configured formatter yields [] (not null).
	if got := strings.TrimSpace(string(fmtRes["result"])); got != "[]" {
		t.Errorf("formatting result = %s, want [] (no applicable formatter)", got)
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

	lsp := startLsp(t, p.Dir, nil, "--no-auto-config", "--config", cfg)
	lsp.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"initializationOptions":` +
		`{"format":{"widenTo":"target","timeoutMs":0,"tools":{"kept":true,"vetoed":true,"nonesuch":true}}}}}`)
	initRes := lsp.await("1")
	lsp.send(`{"jsonrpc":"2.0","method":"initialized","params":{}}`)
	lsp.send(fmt.Sprintf(
		`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":%q,"text":"x"}}}`, uri))
	lsp.send(fmt.Sprintf(
		`{"jsonrpc":"2.0","id":2,"method":"textDocument/formatting","params":{"textDocument":{"uri":%q}}}`, uri))
	fmtRes := lsp.await("2")
	res := lsp.finish()
	if res.ExitCode != 0 {
		t.Fatalf("lsp exited %d\n--- stdout ---\n%s\n--- stderr ---\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}

	var echo struct {
		Capabilities struct {
			Experimental struct {
				Datamitsu struct {
					Format json.RawMessage `json:"format"`
				} `json:"datamitsu"`
			} `json:"experimental"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(initRes["result"], &echo); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	wantEcho := `{"widenTo":"target","timeoutMs":0,"tools":{"kept":true,"vetoed":true}}`
	if got := string(echo.Capabilities.Experimental.Datamitsu.Format); got != wantEcho {
		t.Errorf("capabilities.experimental.datamitsu.format =\n  %s\nwant\n  %s", got, wantEcho)
	}

	// The buffer matched disk, so the fix happens in place and no edits return.
	if got := strings.TrimSpace(string(fmtRes["result"])); got != "[]" {
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

// appendConfigJS is a config with one per-file fix on *.txt that appends
// letter to the file.
func appendConfigJS(letter string) string {
	return `globalThis.getBeforeConfigs = () => [];
globalThis.getConfig = () => ({
  apps: { "append": { shell: { name: "sh", args: ["-c", "printf ` + letter + ` >> \"$1\"", "append"] } } },
  runtimes: {},
  managedConfigs: {},
  tools: {
    append: { name: "append", operations: { fix: { app: "append", args: ["{file}"], scope: "per-file", globs: ["**/*.txt"] } } },
  },
});
globalThis.getMinVersion = () => "0.0.0";
`
}

func lspServedRoot(t *testing.T, initRes map[string]json.RawMessage) string {
	t.Helper()
	var echo struct {
		Capabilities struct {
			Experimental struct {
				Datamitsu struct {
					Root string `json:"root"`
				} `json:"datamitsu"`
			} `json:"experimental"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(initRes["result"], &echo); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	return echo.Capabilities.Experimental.Datamitsu.Root
}

// The served repository comes from initialize, not from where the server was
// started: an editor may start it anywhere. A relative --config still names the
// file it named in the launch directory, a folder resolves to its repository's
// root, and a second folder in another repository is named as not served, as
// is a document from it.
func TestLspServesTheRootInitializeNames(t *testing.T) {
	p := clitest.NewProject(t)
	file := p.WriteFile("pkg/note.txt", "x")
	other := clitest.NewProject(t)
	foreign := other.WriteFile("foreign.txt", "x")

	launch := t.TempDir() // not a git repository
	if err := os.WriteFile(filepath.Join(launch, "lsp.config.js"), []byte(appendConfigJS("a")), 0o644); err != nil {
		t.Fatal(err)
	}

	lsp := startLsp(t, launch, nil, "--no-auto-config", "--config", "lsp.config.js")
	lsp.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"workspaceFolders":[`+
		`{"uri":%q,"name":"pkg"},{"uri":%q,"name":"other"}]}}`, "file://"+filepath.Join(p.Dir, "pkg"), "file://"+other.Dir))
	initRes := lsp.await("1")
	for _, doc := range []struct{ id, path string }{{"2", file}, {"3", foreign}} {
		lsp.send(fmt.Sprintf(
			`{"jsonrpc":"2.0","id":%s,"method":"textDocument/formatting","params":{"textDocument":{"uri":%q}}}`, doc.id, "file://"+doc.path))
		if got := strings.TrimSpace(string(lsp.await(doc.id)["result"])); got != "[]" {
			t.Errorf("formatting %s = %s, want []", doc.path, got)
		}
	}
	res := lsp.finish()
	if res.ExitCode != 0 {
		t.Fatalf("lsp exited %d\n--- stderr ---\n%s", res.ExitCode, res.Stderr)
	}

	if got := lspServedRoot(t, initRes); got != p.Dir {
		t.Errorf("served root = %q, want the first folder's repository %q", got, p.Dir)
	}
	for path, want := range map[string]string{file: "xa", foreign: "x"} {
		if got, _ := os.ReadFile(path); string(got) != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}

	var unserved, outside int
	for _, e := range stderrEvents(t, res.Stderr) {
		msg, _ := e["msg"].(string)
		if e["type"] != "log" || e["level"] != "info" {
			continue
		}
		if strings.Contains(msg, "workspace folder "+other.Dir+" is not formatted") {
			unserved++
		}
		if strings.Contains(msg, "format "+foreign+": outside "+p.Dir) {
			outside++
		}
	}
	if unserved != 1 || outside != 1 {
		t.Errorf("notices: %d for the unserved folder, %d for the foreign document; want one each:\n%s",
			unserved, outside, res.Stderr)
	}
}

// A configuration edited while the server runs is loaded again before the next
// format, with no restart. One that fails to load keeps the previous one
// formatting, and is reported once, not on every save.
func TestLspReloadsTheConfiguration(t *testing.T) {
	p := clitest.NewProject(t)
	cfg := p.WriteFile("datamitsu.config.js", appendConfigJS("a"))
	file := p.WriteFile("note.txt", "")
	uri := "file://" + file

	lsp := startLsp(t, p.Dir, nil)
	lsp.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	lsp.await("1")
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The document is not open, so each format reads it from disk; new content
	// each time keeps the execution cache from skipping the tool.
	format := func(id, content, want string) {
		t.Helper()
		write(file, content)
		lsp.send(fmt.Sprintf(
			`{"jsonrpc":"2.0","id":%s,"method":"textDocument/formatting","params":{"textDocument":{"uri":%q}}}`, id, uri))
		if got := strings.TrimSpace(string(lsp.await(id)["result"])); got != "[]" {
			t.Errorf("formatting (id %s) = %s, want []", id, got)
		}
		if got, _ := os.ReadFile(file); string(got) != want {
			t.Errorf("after format %s note.txt = %q, want %q", id, got, want)
		}
	}

	format("2", "w", "wa")
	write(cfg, appendConfigJS("b"))
	format("3", "x", "xb") // reloaded
	write(cfg, "globalThis.getConfig = () => ({")
	format("4", "y", "yb") // the broken edit keeps the previous configuration
	format("5", "z", "zb") // and is not reported again
	res := lsp.finish()
	if res.ExitCode != 0 {
		t.Fatalf("lsp exited %d\n--- stderr ---\n%s", res.ExitCode, res.Stderr)
	}

	var reloaded, failed int
	for _, e := range stderrEvents(t, res.Stderr) {
		msg, _ := e["msg"].(string)
		switch {
		case e["type"] == "log" && e["level"] == "info" && msg == "configuration reloaded":
			reloaded++
		case e["type"] == "log" && e["level"] == "warn" && strings.Contains(msg, "failed to load"):
			failed++
		}
	}
	if reloaded != 1 || failed != 1 {
		t.Errorf("%d reload notices and %d failure warnings, want one each:\n%s", reloaded, failed, res.Stderr)
	}
}

// A shared configuration the project declares with getBeforeConfigs, missing
// until an install puts it in node_modules, is picked up by the first format
// after it appears — though the install left pnpm-lock.yaml as it was.
func TestLspLoadsABeforeConfigInstalledAfterStart(t *testing.T) {
	p := clitest.NewProject(t)
	p.WriteFile("pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
	p.WriteFile("datamitsu.config.js", `globalThis.getBeforeConfigs = () => [{ path: "./node_modules/wrapper/base.js" }];
globalThis.getConfig = (input) => input;
globalThis.getMinVersion = () => "0.0.0";
`)
	file := p.WriteFile("note.txt", "")
	uri := "file://" + file

	lsp := startLsp(t, p.Dir, nil)
	lsp.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	lsp.await("1")
	format := func(id, content, want string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		lsp.send(fmt.Sprintf(
			`{"jsonrpc":"2.0","id":%s,"method":"textDocument/formatting","params":{"textDocument":{"uri":%q}}}`, id, uri))
		if got := strings.TrimSpace(string(lsp.await(id)["result"])); got != "[]" {
			t.Errorf("formatting (id %s) = %s, want []", id, got)
		}
		if got, _ := os.ReadFile(file); string(got) != want {
			t.Errorf("after format %s note.txt = %q, want %q", id, got, want)
		}
	}

	format("2", "x", "x") // nothing loaded yet
	p.WriteFile("node_modules/wrapper/base.js", appendConfigJS("a"))
	format("3", "y", "ya")
	res := lsp.finish()
	if res.ExitCode != 0 {
		t.Fatalf("lsp exited %d\n--- stderr ---\n%s", res.ExitCode, res.Stderr)
	}

	var missing, reloaded bool
	for _, e := range stderrEvents(t, res.Stderr) {
		msg, _ := e["msg"].(string)
		switch {
		case e["type"] == "log" && e["level"] == "error" && strings.Contains(msg, "node_modules/wrapper/base.js"):
			missing = true
		case e["type"] == "log" && e["level"] == "info" && msg == "configuration reloaded":
			reloaded = true
		}
	}
	if !missing || !reloaded {
		t.Errorf("missing-config error seen: %v, reload notice seen: %v\n%s", missing, reloaded, res.Stderr)
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
