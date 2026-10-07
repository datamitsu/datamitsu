package tooling

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/cache"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"

	"go.uber.org/zap"
)

func TestPassesOf(t *testing.T) {
	a, b := "/w/a.txt", "/w/b.txt"
	covered := []string{a, b}
	finding := func(file string) []diagnostic.Diagnostic { return []diagnostic.Diagnostic{{File: file, Message: "m"}} }
	cases := []struct {
		name string
		proc ProcessResult
		want []string
	}{
		{"no parser keeps the exit-status rule", ProcessResult{Extraction: ExtractionNone}, covered},
		{"parsed, nothing found", ProcessResult{Extraction: ExtractionParsedClean}, covered},
		{"a finding keeps its file uncached", ProcessResult{Extraction: ExtractionParsedFindings, Diagnostics: finding(a)}, []string{b}},
		{"findings on every file", ProcessResult{Extraction: ExtractionParsedFindings, Diagnostics: append(finding(a), finding(b)...)}, []string{}},
		{"a file-less finding blocks every file", ProcessResult{Extraction: ExtractionParsedFindings, Diagnostics: finding("")}, nil},
		{"a finding outside the process blocks every file", ProcessResult{Extraction: ExtractionParsedFindings, Diagnostics: finding("/elsewhere/c.txt")}, nil},
		{"a parse failure passes nothing", ProcessResult{Extraction: ExtractionParseFailed}, nil},
		{"an unavailable parser passes nothing", ProcessResult{Extraction: ExtractionParserUnavailable}, nil},
		{"a truncated output passes nothing", ProcessResult{Extraction: ExtractionTruncated}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := passesOf(c.proc, covered)
			if !slices.Equal(got, c.want) {
				t.Errorf("passesOf = %v, want %v", got, c.want)
			}
		})
	}
}

func TestVerdictEligible(t *testing.T) {
	cases := []struct {
		name   string
		result ExecutionResult
		want   bool
	}{
		{"parsed clean", ExecutionResult{Processes: []ProcessResult{{State: ProcessRan, Extraction: ExtractionParsedClean}}}, true},
		{"no parser", ExecutionResult{Processes: []ProcessResult{{State: ProcessRan, Extraction: ExtractionNone}}}, true},
		{"a finding of a passing tool", ExecutionResult{
			Processes:   []ProcessResult{{State: ProcessRan, Extraction: ExtractionParsedFindings}},
			Diagnostics: []diagnostic.Diagnostic{{Message: "m", Severity: diagnostic.SeverityHint}},
		}, false},
		{"one unparsed process", ExecutionResult{Processes: []ProcessResult{
			{State: ProcessRan, Extraction: ExtractionParsedClean}, {State: ProcessRan, Extraction: ExtractionParseFailed},
		}}, false},
		{"an unavailable parser", ExecutionResult{Processes: []ProcessResult{{State: ProcessRan, Extraction: ExtractionParserUnavailable}}}, false},
		{"a dry run", ExecutionResult{Processes: []ProcessResult{{State: ProcessNotStarted, Extraction: ExtractionNone}}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := verdictEligible(c.result); got != c.want {
				t.Errorf("verdictEligible = %v, want %v", got, c.want)
			}
		})
	}
}

// fileParser answers every parse with the findings its function gives for the
// command's stdout, which the scripts below set to the file they were handed.
type fileParser struct {
	findings func(stdout string) []diagnostic.Diagnostic
}

func (p fileParser) Parse(_ context.Context, _, _, _ string, stdout, _ []byte, _ int32) ([]diagnostic.Diagnostic, error) {
	return p.findings(strings.TrimSpace(string(stdout))), nil
}

// c1Project is a repository with two files, a cache, and an executor whose
// only app echoes the paths it was handed and exits 0.
func c1Project(t *testing.T, parser DiagnosticParser) (e *Executor, c *cache.Cache, root string, files []string) {
	t.Helper()
	root = t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		files = append(files, path)
	}
	c, err := cache.NewCache(t.TempDir(), root, config.Config{}, nil, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	e = NewExecutor(root, false, false, &mockAppManager{commands: map[string]*binmanager.CommandInfo{
		"lint": {Type: "shell", Command: "/bin/sh", Args: []string{"-c", `echo "$@"`, "lint"}},
	}}, c)
	e.SetParser(parser)
	return e, c, root, files
}

func c1Task(op config.OperationType, scope config.ToolScope, args []string, files []string, root string) Task {
	return Task{
		ToolName:    "lint",
		Tool:        config.Tool{Name: "lint", OutputParser: &config.OutputParser{Module: "core", Parser: "lint"}},
		Operation:   op,
		OpConfig:    config.ToolOperation{App: "lint", Scope: scope, Args: args},
		Files:       files,
		ProjectPath: root,
	}
}

func cachedFiles(t *testing.T, c *cache.Cache, op cache.Operation, files []string) []string {
	t.Helper()
	var out []string
	for _, f := range files {
		if !c.Check(f, "lint", op, observe(f), true) {
			out = append(out, filepath.Base(f))
		}
	}
	return out
}

// TestPerFileLintPassFollowsTheParse: a tool that exits 0 on a finding is a
// success, and still records no pass for the file the finding names — the next
// run must see the finding again.
func TestPerFileLintPassFollowsTheParse(t *testing.T) {
	e, c, root, files := c1Project(t, fileParser{findings: func(stdout string) []diagnostic.Diagnostic {
		if filepath.Base(stdout) == "a.txt" {
			return []diagnostic.Diagnostic{{Message: "info-level finding", Severity: diagnostic.SeverityInfo}}
		}
		return nil
	}})
	result := e.executeTask(context.Background(), c1Task(config.OpLint, config.ToolScopePerFile, []string{"{file}"}, files, root))
	if !result.Success {
		t.Fatalf("an exit-0 tool failed: %v", result.Error)
	}
	if got := cachedFiles(t, c, cache.OperationLint, files); !slices.Equal(got, []string{"b.txt"}) {
		t.Errorf("cached = %v, want only the file without findings", got)
	}
}

func TestBatchLintPassAttribution(t *testing.T) {
	cases := []struct {
		name     string
		reported func(root string) string
		want     []string
	}{
		{"a finding on one file", func(root string) string { return filepath.Join(root, "a.txt") }, []string{"b.txt"}},
		{"the same file spelled ./", func(string) string { return "./a.txt" }, []string{"b.txt"}},
		{"a path outside the process", func(string) string { return "/elsewhere/c.txt" }, nil},
		{"no file at all", func(string) string { return "" }, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var root string
			e, c, r, files := c1Project(t, fileParser{findings: func(string) []diagnostic.Diagnostic {
				return []diagnostic.Diagnostic{{Message: "m", File: tc.reported(root)}}
			}})
			root = r
			task := c1Task(config.OpLint, config.ToolScopeRepository, []string{"{files}"}, files, root)
			if result := e.executeTask(context.Background(), task); !result.Success {
				t.Fatalf("an exit-0 tool failed: %v", result.Error)
			}
			if got := cachedFiles(t, c, cache.OperationLint, files); !slices.Equal(got, tc.want) {
				t.Errorf("cached = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestFixPassFollowsTheParse: a fix pass means nothing to report too — the
// failOn gate judges what a fixer leaves behind — so the file a finding names
// records none, and the other one does.
func TestFixPassFollowsTheParse(t *testing.T) {
	e, c, root, files := c1Project(t, fileParser{findings: func(stdout string) []diagnostic.Diagnostic {
		if filepath.Base(stdout) == "a.txt" {
			return []diagnostic.Diagnostic{{Message: "1 problem left", Severity: diagnostic.SeverityHint}}
		}
		return nil
	}})
	if result := e.executeTask(context.Background(), c1Task(config.OpFix, config.ToolScopePerFile, []string{"{file}"}, files, root)); !result.Success {
		t.Fatalf("the fix failed: %v", result.Error)
	}
	if got := cachedFiles(t, c, cache.OperationFix, files); !slices.Equal(got, []string{"b.txt"}) {
		t.Errorf("cached = %v, want only the file without findings", got)
	}
}

// TestUnitVerdictNeedsNothingToReport: an exit-0 unit task with a finding, or
// with output that could not be parsed, records no verdict; a clean parse does.
func TestUnitVerdictNeedsNothingToReport(t *testing.T) {
	cases := []struct {
		name   string
		parser DiagnosticParser
		want   bool
	}{
		{"clean", fileParser{findings: func(string) []diagnostic.Diagnostic { return nil }}, true},
		{"a hint", fileParser{findings: func(string) []diagnostic.Diagnostic {
			return []diagnostic.Diagnostic{{Message: "m", Severity: diagnostic.SeverityHint}}
		}}, false},
		{"unparsed", &fakeParser{err: &ParserUnavailableError{Err: os.ErrNotExist}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, _, root, files := c1Project(t, tc.parser)
			task := c1Task(config.OpLint, config.ToolScopePerProject, []string{"--check"}, files, root)
			task.UnitMembers = files
			task.Coverage = CoverageComplete
			if result := e.executeTask(context.Background(), task); !result.Success {
				t.Fatalf("an exit-0 tool failed: %v", result.Error)
			}
			key, snap, _, ok := e.verdictKeys(task)
			if !ok {
				t.Fatal("the verdict cache does not apply to the task")
			}
			if hit := !e.cache.ShouldRunVerdict(key, snap.hash(), e.verdictTTL()); hit != tc.want {
				t.Errorf("verdict recorded = %v, want %v", hit, tc.want)
			}
		})
	}
}

// TestUnitVerdictNamesItsParserModule: a verdict the parser module decided is
// not replayed once the configuration pins another module.
func TestUnitVerdictNamesItsParserModule(t *testing.T) {
	e, _, root, files := c1Project(t, fileParser{findings: func(string) []diagnostic.Diagnostic { return nil }})
	e.SetParserModules(config.MapOfParsers{"core": {Hash: strings.Repeat("a", 64)}})
	task := c1Task(config.OpLint, config.ToolScopePerProject, []string{"--check"}, files, root)
	task.UnitMembers = files
	task.Coverage = CoverageComplete
	if result := e.executeTask(context.Background(), task); !result.Success {
		t.Fatalf("the tool failed: %v", result.Error)
	}
	hit := func() bool {
		key, snap, _, _ := e.verdictKeys(task)
		return !e.cache.ShouldRunVerdict(key, snap.hash(), e.verdictTTL())
	}
	if !hit() {
		t.Fatal("the clean run recorded no verdict")
	}
	e.SetParserModules(config.MapOfParsers{"core": {Hash: strings.Repeat("b", 64)}})
	if hit() {
		t.Error("a verdict recorded with one parser module was replayed for another")
	}
}

// TestMissingParserRecordsNoLintPass: a tool whose declared parser this
// executor was not given had its output read by nobody, so neither a per-file
// pass nor a verdict may say it found nothing.
func TestMissingParserRecordsNoLintPass(t *testing.T) {
	e, c, root, files := c1Project(t, nil)

	perFile := c1Task(config.OpLint, config.ToolScopePerFile, []string{"{file}"}, files, root)
	result := e.executeTask(context.Background(), perFile)
	if !result.Success || !result.ParseFailed {
		t.Fatalf("Success = %v, ParseFailed = %v", result.Success, result.ParseFailed)
	}
	if proc := result.Processes[0]; proc.Extraction != ExtractionParserUnavailable {
		t.Errorf("Extraction = %s, want parser-unavailable", proc.Extraction)
	}
	if got := cachedFiles(t, c, cache.OperationLint, files); len(got) != 0 {
		t.Errorf("cached = %v, want nothing", got)
	}

	unit := c1Task(config.OpLint, config.ToolScopePerProject, []string{"--check"}, files, root)
	unit.UnitMembers = files
	unit.Coverage = CoverageComplete
	e.executeTask(context.Background(), unit)
	key, snap, _, _ := e.verdictKeys(unit)
	if !e.cache.ShouldRunVerdict(key, snap.hash(), e.verdictTTL()) {
		t.Error("a verdict was recorded for output nobody parsed")
	}
}

// TestChunkPassesSurviveAFailingChunk: each chunk answers for its own files,
// so a failure in one chunk does not cost the others their passes.
func TestChunkPassesSurviveAFailingChunk(t *testing.T) {
	t.Setenv("DATAMITSU_MAX_CMD_LENGTH", "1")
	e, c, root, files := resultsProject(t, false, `case "$1" in *bad*) exit 1;; esac`, "good.txt", "bad.txt")
	task := loopTask(root, files)
	task.OpConfig.Scope = config.ToolScopeRepository
	task.OpConfig.Args = []string{"{files}"}

	if result := e.executeTask(context.Background(), task); result.Success {
		t.Fatal("the task passed")
	}
	if !c.Check(files[1], "tool", cache.OperationLint, observe(files[1]), true) {
		t.Error("the failing chunk's file was cached")
	}
	if c.Check(files[0], "tool", cache.OperationLint, observe(files[0]), true) {
		t.Error("the passing chunk's file lost its pass")
	}
}
