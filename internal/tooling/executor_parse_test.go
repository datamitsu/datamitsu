package tooling

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"
)

// fakeParser records its inputs and returns a canned result/error. The
// declared parser recognizes the output unless unrecognized is set; the
// fallback recognizes nothing unless fallback is set.
type fakeParser struct {
	gotModule, gotParser, gotTool string
	gotStdout, gotStderr          []byte
	gotExit                       int32
	diags                         []diagnostic.Diagnostic
	err                           error
	unrecognized                  bool
	format                        bool
	partial                       bool

	fallback                    *ParseAnswer
	fallbackFails               error
	fallbackStdout, fallbackErr []byte
	fallbackCalls               int
	fellBack, readNothing       []string
}

func (f *fakeParser) Parse(_ context.Context, module, parser, toolName string, stdout, stderr []byte, exitCode int32) (ParseAnswer, error) {
	f.gotModule, f.gotParser, f.gotTool, f.gotStdout, f.gotStderr, f.gotExit = module, parser, toolName, stdout, stderr, exitCode
	return ParseAnswer{
		Diagnostics: f.diags, Recognized: !f.unrecognized, Format: parser, FormatParser: f.format, Partial: f.partial,
	}, f.err
}

func (f *fakeParser) Fallback(_ context.Context, _ string, stdout, stderr []byte, _ int32) (ParseAnswer, error) {
	f.fallbackCalls++
	f.fallbackStdout, f.fallbackErr = stdout, stderr
	if f.fallbackFails != nil {
		return ParseAnswer{}, f.fallbackFails
	}
	if f.fallback == nil {
		return ParseAnswer{}, nil
	}
	answer := *f.fallback
	answer.Diagnostics = append([]diagnostic.Diagnostic(nil), answer.Diagnostics...)
	return answer, nil
}

func (f *fakeParser) FellBack(tool string, declared config.OutputParser, format string) {
	f.fellBack = append(f.fellBack, tool+"/"+declared.Module+"/"+declared.Parser+"->"+format)
}

func (f *fakeParser) Unrecognized(tool string, declared config.OutputParser) {
	f.readNothing = append(f.readNothing, tool+"/"+declared.Module+"/"+declared.Parser)
}

func parseTask(module, parser string) Task {
	return Task{
		ToolName: "eslint",
		Tool:     config.Tool{Name: "eslint", OutputParser: &config.OutputParser{Module: module, Parser: parser}},
	}
}

func TestParseFileDiagnostics_PassesTheOutputThrough(t *testing.T) {
	fp := &fakeParser{diags: []diagnostic.Diagnostic{{Message: "a", Row: 1}}}
	e := &Executor{parser: fp}
	var proc ProcessResult

	e.parseFileDiagnostics(context.Background(), &proc, parseTask("core", "eslint"), t.TempDir(), []byte("OUT"), []byte("ERR"), 1, true)

	if fp.gotModule != "core" || fp.gotParser != "eslint" || fp.gotTool != "eslint" ||
		string(fp.gotStdout) != "OUT" || string(fp.gotStderr) != "ERR" || fp.gotExit != 1 {
		t.Fatalf("parser called with unexpected args: %+v", fp)
	}
	if len(proc.Diagnostics) != 1 {
		t.Fatalf("got %d diagnostics, want 1", len(proc.Diagnostics))
	}
}

// TestParseFileDiagnostics_Paths pins the path contract: every file a
// diagnostic names is absolute and cleaned, and a file-less diagnostic is
// attributed only when its process was handed exactly one file.
func TestParseFileDiagnostics_Paths(t *testing.T) {
	dir := t.TempDir()
	abs := func(rel string) string { return filepath.Join(dir, rel) }
	cases := []struct {
		name     string
		files    []string
		reported string
		want     string
	}{
		{"per-file run stamps its file", []string{abs("broken.js")}, "", abs("broken.js")},
		{"one-file batch stamps its file", []string{abs("only.md")}, "", abs("only.md")},
		{"two-file batch leaves it file-less", []string{abs("a.md"), abs("b.md")}, "", ""},
		{"a process given no file leaves it file-less", nil, "", ""},
		{"a relative path resolves against the working directory", nil, "src/x.ts", abs("src/x.ts")},
		{"an absolute path is kept, cleaned", nil, abs("src") + "/../src/a.ts", abs("src/a.ts")},
		{"a dot-relative path names the same file", []string{abs("x")}, "./x", abs("x")},
		{"a reported path wins over the stamp", []string{abs("a.md")}, "b.md", abs("b.md")},
		{"climbing out stays outside", nil, "../elsewhere.ts", filepath.Join(filepath.Dir(dir), "elsewhere.ts")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := &Executor{parser: &fakeParser{diags: []diagnostic.Diagnostic{{Message: "m", File: c.reported}}}}
			proc := ProcessResult{Files: c.files}
			e.parseFileDiagnostics(context.Background(), &proc, parseTask("core", "tsc"), dir, nil, nil, 1, true)
			if len(proc.Diagnostics) != 1 {
				t.Fatalf("got %d diagnostics, want 1", len(proc.Diagnostics))
			}
			if got := proc.Diagnostics[0].File; got != c.want {
				t.Errorf("File = %q, want %q", got, c.want)
			}
		})
	}
}

// TestParseFileDiagnostics_Extraction pins the outcome each parse answer
// records: only a parser that ran without error says anything about findings.
func TestParseFileDiagnostics_Extraction(t *testing.T) {
	unavailable := &ParserUnavailableError{Err: errors.New("module did not load")}
	cases := []struct {
		name      string
		parser    *fakeParser
		want      Extraction
		wantError string
	}{
		{"no finding", &fakeParser{}, ExtractionParsedClean, ""},
		{"a finding", &fakeParser{diags: []diagnostic.Diagnostic{{Message: "m"}}}, ExtractionParsedFindings, ""},
		{"a parse error", &fakeParser{err: errors.New("boom")}, ExtractionParseFailed, "boom"},
		{"an unavailable parser", &fakeParser{err: fmt.Errorf("parse: %w", unavailable)}, ExtractionParserUnavailable, "parse: module did not load"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := &Executor{parser: c.parser}
			proc := ProcessResult{Files: []string{"/w/f.js"}, Extraction: ExtractionNone}
			e.parseFileDiagnostics(context.Background(), &proc, parseTask("core", "eslint"), "/w", nil, nil, 0, true)
			if proc.Extraction != c.want {
				t.Errorf("Extraction = %q, want %q", proc.Extraction, c.want)
			}
			if proc.ParseError != c.wantError {
				t.Errorf("ParseError = %q, want %q", proc.ParseError, c.wantError)
			}
			if c.parser.err != nil && len(proc.Diagnostics) != 0 {
				t.Errorf("a failed parse must yield no diagnostics, got %+v", proc.Diagnostics)
			}
		})
	}
}

// TestParseFileDiagnostics_Limits pins the parse caps: a parser reads at most
// the limit of each stream, keeps at most the limit of findings, and a process
// over either is truncated with what was kept.
func TestParseFileDiagnostics_Limits(t *testing.T) {
	three := []diagnostic.Diagnostic{{Message: "a"}, {Message: "b"}, {Message: "c"}}
	cases := []struct {
		name         string
		limits       ParseLimits
		stdout       string
		stderr       string
		diags        []diagnostic.Diagnostic
		wantStdout   string
		wantStderr   string
		wantFindings int
		want         Extraction
	}{
		{"within both", ParseLimits{InputBytes: 8, Findings: 3}, "12345678", "abc", three, "12345678", "abc", 3, ExtractionParsedFindings},
		{"stdout cut", ParseLimits{InputBytes: 4, Findings: 3}, "12345678", "", nil, "1234", "", 0, ExtractionTruncated},
		{"stderr cut", ParseLimits{InputBytes: 2, Findings: 3}, "", "abc", three, "", "ab", 3, ExtractionTruncated},
		{"findings dropped", ParseLimits{InputBytes: 8, Findings: 2}, "x", "", three, "x", "", 2, ExtractionTruncated},
		{"unset limits are the defaults", ParseLimits{}, "x", "y", three, "x", "y", 3, ExtractionParsedFindings},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fp := &fakeParser{diags: c.diags}
			e := &Executor{parser: fp}
			e.SetParseLimits(c.limits)
			var proc ProcessResult
			e.parseFileDiagnostics(context.Background(), &proc, parseTask("core", "eslint"), "/w", []byte(c.stdout), []byte(c.stderr), 1, true)
			if string(fp.gotStdout) != c.wantStdout || string(fp.gotStderr) != c.wantStderr {
				t.Errorf("parser read %q/%q, want %q/%q", fp.gotStdout, fp.gotStderr, c.wantStdout, c.wantStderr)
			}
			if len(proc.Diagnostics) != c.wantFindings || proc.Extraction != c.want {
				t.Errorf("got %d findings, %q; want %d, %q", len(proc.Diagnostics), proc.Extraction, c.wantFindings, c.want)
			}
		})
	}
}

// TestExecutionResultParseFailed: a task whose every process was parsed says
// so; one process whose output could not be parsed makes an empty
// Diagnostics unbelievable, and the task says that instead.
func TestExecutionResultParseFailed(t *testing.T) {
	for _, c := range []struct {
		outcomes []Extraction
		want     bool
	}{
		{[]Extraction{ExtractionParsedClean, ExtractionParsedFindings, ExtractionNone}, false},
		{[]Extraction{ExtractionParsedClean, ExtractionParseFailed}, true},
		{[]Extraction{ExtractionParserUnavailable}, true},
	} {
		var r ExecutionResult
		for _, o := range c.outcomes {
			r.addProcess(ProcessResult{Extraction: o})
		}
		if r.ParseFailed != c.want {
			t.Errorf("outcomes %v: ParseFailed = %v, want %v", c.outcomes, r.ParseFailed, c.want)
		}
		if len(r.Processes) != len(c.outcomes) {
			t.Errorf("outcomes %v: %d processes recorded", c.outcomes, len(r.Processes))
		}
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
	task := Task{
		ToolName:  "eslint",
		Operation: config.OpLint,
		Tool:      config.Tool{Name: "eslint", OutputParser: &config.OutputParser{Module: "core", Parser: "eslint"}},
		OpConfig: config.ToolOperation{
			App:   "eslint",
			Scope: config.ToolScopePerProject,
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
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].File != filepath.Join(tmpDir, "src", "a.ts") {
		t.Fatalf("Diagnostics = %+v, want the parsed diagnostic", result.Diagnostics)
	}
	// The textual fallback still carries both streams for tools whose parse is empty.
	if !strings.Contains(result.Output, "catalog warning") || !strings.Contains(result.Output, "filePath") {
		t.Errorf("Output = %q, want both streams", result.Output)
	}
}

// A tool that colours its output into a pipe: the parser reads both streams
// without the escapes, and the frame keeps what the tool printed.
func TestParserReadsOutputWithoutANSI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the tool is an sh script")
	}
	tmpDir := t.TempDir()
	fp := &fakeParser{}
	executor := NewExecutor(tmpDir, false, false, &mockAppManager{commands: map[string]*binmanager.CommandInfo{
		"yamllint": shellApp(`printf 'a.yaml:3:1: [\033[31merror\033[0m] x\n'; printf '\033[33m[note]\033[0m\n' >&2; exit 1`),
	}}, nil)
	executor.SetParser(fp)
	task := parseTask("core", "yamllint")
	task.ToolName, task.Operation = "yamllint", config.OpLint
	task.OpConfig = config.ToolOperation{App: "yamllint", Scope: config.ToolScopePerProject}
	task.ProjectPath = tmpDir

	result := executor.executeTask(context.Background(), task)
	if got := string(fp.gotStdout); got != "a.yaml:3:1: [error] x\n" {
		t.Errorf("parser stdout = %q, want it without escapes", got)
	}
	if got := string(fp.gotStderr); got != "[note]\n" {
		t.Errorf("parser stderr = %q, want it without escapes", got)
	}
	if !strings.Contains(result.Output, "\x1b[31merror\x1b[0m") || !strings.Contains(result.Output, "\x1b[33m[note]") {
		t.Errorf("Output = %q, want the raw streams kept for the frame", result.Output)
	}
}

// TestExecuteBatchStampsASingleFile drives a list-taking tool whose parser
// names no file: handed one file, the process's findings are about it; handed
// two, nothing says which.
func TestExecuteBatchStampsASingleFile(t *testing.T) {
	for _, n := range []int{1, 2} {
		t.Run(fmt.Sprintf("%d files", n), func(t *testing.T) {
			tmpDir := t.TempDir()
			files := make([]string, n)
			for i := range files {
				files[i] = filepath.Join(tmpDir, fmt.Sprintf("f%d.md", i))
				if err := os.WriteFile(files[i], []byte("x\n"), 0o600); err != nil {
					t.Fatalf("write fixture: %v", err)
				}
			}
			appManager := &mockAppManager{commands: map[string]*binmanager.CommandInfo{
				"vale": {Type: "shell", Command: "/bin/sh", Args: []string{"-c", "exit 1"}},
			}}
			executor := NewExecutor(tmpDir, false, false, appManager, nil)
			executor.SetParser(&fakeParser{diags: []diagnostic.Diagnostic{{Message: "m"}}})
			result := executor.executeTask(context.Background(), Task{
				ToolName:    "vale",
				Operation:   config.OpLint,
				Tool:        config.Tool{Name: "vale", OutputParser: &config.OutputParser{Module: "core", Parser: "vale"}},
				OpConfig:    config.ToolOperation{App: "vale", Scope: config.ToolScopeRepository, Args: []string{"{files}"}},
				Files:       files,
				ProjectPath: tmpDir,
			})
			if len(result.Diagnostics) != 1 {
				t.Fatalf("Diagnostics = %+v, want one", result.Diagnostics)
			}
			want := ""
			if n == 1 {
				want = files[0]
			}
			if got := result.Diagnostics[0].File; got != want {
				t.Errorf("File = %q, want %q", got, want)
			}
		})
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
	result := executor.executeTask(context.Background(), Task{
		ToolName:  "eslint",
		Operation: config.OpLint,
		Tool:      config.Tool{Name: "eslint", OutputParser: &config.OutputParser{Module: "core", Parser: "eslint"}},
		OpConfig: config.ToolOperation{
			App:   "eslint",
			Scope: config.ToolScopePerProject,
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
	result := executor.executeTask(context.Background(), Task{
		ToolName:  "tsc",
		Operation: config.OpLint,
		Tool:      config.Tool{Name: "tsc", OutputParser: &config.OutputParser{Module: "core", Parser: "tsc"}},
		OpConfig: config.ToolOperation{
			App:   "tsc",
			Scope: config.ToolScopePerProject,
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

func (p *perCallParser) Parse(_ context.Context, _, parser, _ string, stdout, _ []byte, _ int32) (ParseAnswer, error) {
	p.calls.Add(1)
	return ParseAnswer{
		Diagnostics: []diagnostic.Diagnostic{{Message: strings.TrimSpace(string(stdout)), File: "x.js"}},
		Recognized:  true,
		Format:      parser,
	}, nil
}

func (p *perCallParser) Fallback(context.Context, string, []byte, []byte, int32) (ParseAnswer, error) {
	return ParseAnswer{}, nil
}

func (p *perCallParser) FellBack(string, config.OutputParser, string) {}

func (p *perCallParser) Unrecognized(string, config.OutputParser) {}
