package tooling

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"
)

// fakeParser records its inputs and returns a canned result/error.
type fakeParser struct {
	gotModule, gotParser, gotTool string
	gotStdout, gotStderr          []byte
	gotExit                       int32
	diags                         []diagnostic.Diagnostic
	err                           error
}

func (f *fakeParser) Parse(_ context.Context, module, parser, toolName string, stdout, stderr []byte, exitCode int32) ([]diagnostic.Diagnostic, error) {
	f.gotModule, f.gotParser, f.gotTool, f.gotStdout, f.gotStderr, f.gotExit = module, parser, toolName, stdout, stderr, exitCode
	return f.diags, f.err
}

func parseTask(module, parser string) Task {
	return Task{
		ToolName: "eslint",
		Tool:     config.Tool{Name: "eslint", OutputParser: &config.OutputParser{Module: module, Parser: parser}},
	}
}

func TestParseFileDiagnostics_StampsFileAndAppends(t *testing.T) {
	fp := &fakeParser{diags: []diagnostic.Diagnostic{
		{Message: "a", Row: 1},
		{Message: "b", Row: 2, File: "already.js"}, // pre-set file is kept
	}}
	e := &Executor{parser: fp}
	var result ExecutionResult

	e.parseFileDiagnostics(context.Background(), &result, parseTask("core", "eslint"), "broken.js", []byte("OUT"), []byte("ERR"), 1)

	if fp.gotModule != "core" || fp.gotParser != "eslint" || fp.gotTool != "eslint" ||
		string(fp.gotStdout) != "OUT" || fp.gotExit != 1 {
		t.Fatalf("parser called with unexpected args: %+v", fp)
	}
	if len(result.Diagnostics) != 2 {
		t.Fatalf("got %d diagnostics, want 2", len(result.Diagnostics))
	}
	if result.Diagnostics[0].File != "broken.js" {
		t.Errorf("first diag file = %q, want stamped broken.js", result.Diagnostics[0].File)
	}
	if result.Diagnostics[1].File != "already.js" {
		t.Errorf("parser-provided file overwritten: %q", result.Diagnostics[1].File)
	}
}

func TestParseFileDiagnostics_ParseErrorIsNonFatal(t *testing.T) {
	e := &Executor{parser: &fakeParser{err: errors.New("boom")}}
	var result ExecutionResult
	// Must not panic and must leave Diagnostics empty.
	e.parseFileDiagnostics(context.Background(), &result, parseTask("core", "eslint"), "f.js", nil, nil, 0)
	if len(result.Diagnostics) != 0 {
		t.Errorf("a parse error must yield no diagnostics, got %+v", result.Diagnostics)
	}
}

// TestExecuteBatchChunkParses drives the batch path used by per-project tools
// like eslint (a {files} list + outputParser): the parser must receive the machine
// output (stdout) apart from wrapper noise (stderr), and the resolved diagnostics
// must reach the result so the runner prints them instead of raw JSON.
func TestExecuteBatchChunkParses(t *testing.T) {
	tmpDir := t.TempDir()
	fp := &fakeParser{diags: []diagnostic.Diagnostic{{Message: "prefer replaceAll", Row: 91, File: "src/a.ts"}}}

	appManager := &mockAppManager{
		commands: map[string]*binmanager.CommandInfo{
			// Mimics eslint under a pnpm wrapper: JSON on stdout, warning on stderr.
			"eslint": {
				Type:    "shell",
				Command: "/bin/sh",
				Args:    []string{"-c", `echo 'catalog warning' >&2; echo '[{"filePath":"src/a.ts"}]'; exit 1`},
			},
		},
	}
	executor := NewExecutor(tmpDir, false, false, appManager, nil)
	executor.SetParser(fp)

	batchTrue := true
	task := Task{
		ToolName:  "eslint",
		Operation: config.OpLint,
		Tool:      config.Tool{Name: "eslint", OutputParser: &config.OutputParser{Module: "core", Parser: "eslint"}},
		OpConfig: config.ToolOperation{
			App:   "eslint",
			Scope: config.ToolScopePerProject,
			Batch: &batchTrue,
		},
		ProjectPath: tmpDir,
	}

	result := executor.executeTask(context.Background(), task)
	if result.Success {
		t.Fatalf("expected the failing tool to be reported as failed")
	}
	if got := strings.TrimSpace(string(fp.gotStdout)); got != `[{"filePath":"src/a.ts"}]` {
		t.Errorf("parser stdout = %q, want the JSON document alone", got)
	}
	if got := strings.TrimSpace(string(fp.gotStderr)); got != "catalog warning" {
		t.Errorf("parser stderr = %q, want the wrapper noise kept apart", got)
	}
	if fp.gotExit != 1 {
		t.Errorf("parser exit code = %d, want 1", fp.gotExit)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].File != "src/a.ts" {
		t.Fatalf("Diagnostics = %+v, want the parsed diagnostic", result.Diagnostics)
	}
	// The textual fallback still carries both streams for tools whose parse is empty.
	if !strings.Contains(result.Output, "catalog warning") || !strings.Contains(result.Output, "filePath") {
		t.Errorf("Output = %q, want both streams", result.Output)
	}
}

func TestJoinStreams(t *testing.T) {
	cases := []struct {
		out, err, want string
	}{
		{"out", "", "out"},
		{"", "err", "err"},
		{"out", "err", "out\nerr"},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := string(joinStreams([]byte(c.out), []byte(c.err))); got != c.want {
			t.Errorf("joinStreams(%q,%q) = %q, want %q", c.out, c.err, got, c.want)
		}
	}
}

// TestExecuteBatchChunksParallelMergesDiagnostics pins the chunk aggregation:
// when a long file list is split across commands, the diagnostics of every chunk
// must reach the result. Without the merge the run reports only some of them —
// and, since the runner prefers diagnostics over raw output, the rest vanish.
func TestExecuteBatchChunksParallelMergesDiagnostics(t *testing.T) {
	tmpDir := t.TempDir()
	// Force a chunk per file: the base command alone already fills the budget.
	t.Setenv("DATAMITSU_MAX_CMD_LENGTH", "1")

	files := make([]string, 3)
	for i := range files {
		files[i] = filepath.Join(tmpDir, fmt.Sprintf("f%d.js", i))
		if err := os.WriteFile(files[i], []byte("x\n"), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}

	// One diagnostic per invocation, labelled with the file the chunk carried, so
	// a dropped chunk is visible in the result rather than merely a smaller count.
	fp := &perCallParser{}
	appManager := &mockAppManager{
		commands: map[string]*binmanager.CommandInfo{
			"eslint": {Type: "shell", Command: "/bin/sh", Args: []string{"-c", `echo "$@"; exit 1`, "sh"}},
		},
	}
	executor := NewExecutor(tmpDir, false, false, appManager, nil)
	executor.SetParser(fp)

	batchTrue := true
	result := executor.executeTask(context.Background(), Task{
		ToolName:  "eslint",
		Operation: config.OpLint,
		Tool:      config.Tool{Name: "eslint", OutputParser: &config.OutputParser{Module: "core", Parser: "eslint"}},
		OpConfig: config.ToolOperation{
			App:   "eslint",
			Scope: config.ToolScopePerProject,
			Batch: &batchTrue,
			Args:  []string{"{files}"},
		},
		Files:       files,
		ProjectPath: tmpDir,
	})

	if got := fp.calls.Load(); got != 3 {
		t.Fatalf("parser called %d times, want one per chunk (3)", got)
	}
	if len(result.Diagnostics) != 3 {
		t.Fatalf("got %d diagnostics, want every chunk's: %+v", len(result.Diagnostics), result.Diagnostics)
	}
	seen := map[string]bool{}
	for _, d := range result.Diagnostics {
		seen[d.Message] = true
	}
	for i := range files {
		if want := fmt.Sprintf("f%d.js", i); !seen[want] {
			t.Errorf("diagnostic for %s missing: %+v", want, result.Diagnostics)
		}
	}
}

// TestExecuteBatchRunsOnceWhenArgsIgnoreFiles covers the tools whose args never
// mention the files (tsc reads tsconfig.json). Chunking them re-runs one
// identical command, which since batch output is parsed would also report every
// diagnostic once per chunk.
func TestExecuteBatchRunsOnceWhenArgsIgnoreFiles(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DATAMITSU_MAX_CMD_LENGTH", "1") // would chunk per file if it chunked

	files := make([]string, 4)
	for i := range files {
		files[i] = filepath.Join(tmpDir, fmt.Sprintf("f%d.ts", i))
		if err := os.WriteFile(files[i], []byte("x\n"), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}

	fp := &perCallParser{}
	appManager := &mockAppManager{
		commands: map[string]*binmanager.CommandInfo{
			"tsc": {Type: "shell", Command: "/bin/sh", Args: []string{"-c", "echo whole-project; exit 1"}},
		},
	}
	executor := NewExecutor(tmpDir, false, false, appManager, nil)
	executor.SetParser(fp)

	batchTrue := true
	result := executor.executeTask(context.Background(), Task{
		ToolName:  "tsc",
		Operation: config.OpLint,
		Tool:      config.Tool{Name: "tsc", OutputParser: &config.OutputParser{Module: "core", Parser: "tsc"}},
		OpConfig: config.ToolOperation{
			App:   "tsc",
			Scope: config.ToolScopePerProject,
			Batch: &batchTrue,
			Args:  []string{"--noEmit"}, // no {files}
		},
		Files:       files,
		ProjectPath: tmpDir,
	})

	if got := fp.calls.Load(); got != 1 {
		t.Errorf("tool ran %d times, want 1 — its args ignore the file list", got)
	}
	if len(result.Diagnostics) != 1 {
		t.Errorf("got %d diagnostics, want 1 (no duplication): %+v", len(result.Diagnostics), result.Diagnostics)
	}
}

// perCallParser returns one diagnostic per call, echoing the tool's stdout as
// the message so each invocation is distinguishable in the merged result.
type perCallParser struct{ calls atomic.Int32 }

func (p *perCallParser) Parse(_ context.Context, _, _, _ string, stdout, _ []byte, _ int32) ([]diagnostic.Diagnostic, error) {
	p.calls.Add(1)
	return []diagnostic.Diagnostic{{Message: strings.TrimSpace(string(stdout)), File: "x.js"}}, nil
}
