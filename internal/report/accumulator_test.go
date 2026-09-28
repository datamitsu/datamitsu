package report

import (
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/parsermanager"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

var root = filepath.FromSlash("/repo")

func abs(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

func perFileTask(id string, files ...string) tooling.Task {
	absFiles := make([]string, len(files))
	for i, f := range files {
		absFiles[i] = abs(f)
	}
	return tooling.Task{
		ID: id, ToolName: "hadolint",
		Tool:     config.Tool{OutputParser: &config.OutputParser{Module: "core", Parser: "hadolint"}},
		OpConfig: config.ToolOperation{App: "hadolint", Args: []string{"{file}"}, Scope: config.ToolScopePerFile},
		Files:    absFiles, Coverage: tooling.CoverageComplete,
	}
}

func testOptions() Options {
	return Options{
		Root: root,
		Tools: config.MapOfTools{
			"hadolint": {
				OutputParser: &config.OutputParser{Module: "core", Parser: "hadolint"},
				Operations:   map[config.OperationType]config.ToolOperation{config.OpLint: {App: "hadolint", FailOn: config.SeverityWarning}},
			},
		},
		Apps:   binmanager.MapOfApps{"hadolint": {Binary: &binmanager.AppConfigBinary{Version: "2.12.0"}}},
		FailOn: config.SeverityError,
		Parsers: func(module, parser string) (parsermanager.ParserFacts, bool) {
			return parsermanager.ParserFacts{
				Version: "0.3.0", Schema: 2, Contract: true,
				Tool: parsermanager.ToolCapability{Name: parser, ColumnUnit: "utf-32", Category: "security"},
			}, module == "core"
		},
	}
}

// A per-file task is one invocation per process, plus one for the files the
// per-file cache answered; a task the run never reached is one not-started
// stand-in over its planned files.
func TestInvocationsOfATask(t *testing.T) {
	task := perFileTask("hadolint::1", "b/Dockerfile", "a/Dockerfile", "c/Dockerfile")
	unreached := perFileTask("hadolint:x:2", "x/Dockerfile")
	plan := &tooling.ExecutionPlan{Groups: []tooling.TaskGroup{{Tasks: []tooling.Task{task, unreached}}}}
	result := tooling.ExecutionResult{
		ToolName: "hadolint", TaskID: task.ID, Success: false,
		Processes: []tooling.ProcessResult{
			{
				ID: "hadolint::1#1", Files: []string{abs("b/Dockerfile")}, State: tooling.ProcessRan, ExitCode: new(1),
				Extraction: tooling.ExtractionParsedFindings, DurationMs: 12,
				Diagnostics: []diagnostic.Diagnostic{{
					File: abs("b/Dockerfile"), Row: 3, EndRow: 3, Col: 1, EndCol: 5,
					Severity: diagnostic.SeverityError, Message: "m", Source: "hadolint", Code: "DL3000",
					URL: "https://example.test/DL3000", Reported: true, Gates: true,
				}},
			},
			{ID: "hadolint::1#2", Files: []string{abs("c/Dockerfile")}, State: tooling.ProcessNotStarted, Extraction: tooling.ExtractionNone},
		},
		Files: []string{abs("b/Dockerfile"), abs("a/Dockerfile"), abs("c/Dockerfile")},
		FileResults: []tooling.FileResult{
			{File: abs("b/Dockerfile"), State: tooling.FileRan, ProcessID: "hadolint::1#1", ExitCode: new(1)},
			{File: abs("a/Dockerfile"), State: tooling.FileCached, Success: true},
			{File: abs("c/Dockerfile"), State: tooling.FileNotStarted},
		},
	}

	acc := NewAccumulator(testOptions())
	op := acc.BeginOperation("lint", plan, func(tooling.Task) string { return "x" })
	op.AddTask(result)
	op.Stopped(Cancel{TaskID: unreached.ID, Tool: "hadolint", Dir: "x", Cause: "fail-fast"})
	op.End(false, 40)
	acc.NotRun("fix")
	run := acc.Build(BuildInfo{Version: "1.0.0", Configuration: "c", StartedAt: time.Unix(0, 0), EndedAt: time.Unix(1, 0), Selection: Selection{Mode: "all"}})

	if len(run.Operations) != 2 || run.Operations[1].Name != "fix" || run.Operations[1].Ran {
		t.Fatalf("operations = %+v, want lint then a fix that did not run", run.Operations)
	}
	tools := run.Operations[0].Tools
	if len(tools) != 1 {
		t.Fatalf("tools = %+v, want hadolint alone", tools)
	}
	tr := tools[0]
	wantTool := ToolRun{
		Name: "hadolint", App: AppRef{Name: "hadolint", Kind: "binary", Version: "2.12.0"},
		Parser: &ParserRef{Module: "core", Parser: "hadolint", Version: "0.3.0", Schema: 2, ColumnUnit: "utf-32"},
		FailOn: "warning", GateActive: true, Category: "security", Incomplete: []Reason{ReasonNotStarted}, mayHoldSecrets: true,
	}
	tr.Invocations = nil
	if !reflect.DeepEqual(tr, wantTool) {
		t.Errorf("tool run = %+v, want %+v", tr, wantTool)
	}

	got := make([]string, 0, len(tools[0].Invocations))
	for _, inv := range tools[0].Invocations {
		got = append(got, inv.ID+" "+inv.State+" "+inv.FailureKind+" "+inv.Extraction)
	}
	want := []string{
		"hadolint::1#1 ran exit parsed-findings",
		"hadolint::1#2 not-started  none",
		"hadolint::1#cached cached  parsed-clean",
		"hadolint:x:2#0 not-started  none",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("invocations =\n%q\nwant\n%q", got, want)
	}
	first := tools[0].Invocations[0]
	if len(first.Files) != 1 || first.Files[0].Path != "b/Dockerfile" || *first.Files[0].ExitCode != 1 {
		t.Errorf("files of the process = %+v, want b/Dockerfile exited 1", first.Files)
	}
	// Never anchored, so it rests on its row.
	wantFinding := Finding{
		Fingerprint:      Fingerprint("hadolint", "DL3000", "b/Dockerfile", "row:3", 0),
		FingerprintBasis: "row",
		Tool:             "hadolint", Source: "hadolint", Code: "DL3000", RuleURL: "https://example.test/DL3000",
		Severity: "error", Reported: true, Gates: true, Kind: "security", Message: "m", Provenance: "parser",
		Location: Location{Path: "b/Dockerfile", Row: 3, EndRow: 3, Col: 1, EndCol: 5, Unit: "utf-32", Precision: "unknown"},
		lineHash: "row:3",
	}
	if len(first.Findings) != 1 || !reflect.DeepEqual(first.Findings[0], wantFinding) {
		t.Errorf("findings = %+v, want %+v", first.Findings, wantFinding)
	}
	if unrun := tools[0].Invocations[3]; unrun.Dir != "x" || len(unrun.Files) != 1 || unrun.Files[0].State != "not-started" {
		t.Errorf("unreached task = %+v, want its planned file not started in x", unrun)
	}
	if c := run.Operations[0].Cancelled; len(c) != 1 || c[0].TaskID != unreached.ID {
		t.Errorf("cancelled = %+v, want the unreached task", c)
	}
}

// A unit whose verdict held, a task whose command could not be resolved and
// one a cancellation stopped before it started each stand in as one
// invocation.
func TestStandInInvocations(t *testing.T) {
	tests := []struct {
		name      string
		result    tooling.ExecutionResult
		wantID    string
		wantState string
		wantKind  string
		success   bool
	}{
		{
			name:   "verdict hit",
			result: tooling.ExecutionResult{Success: true, Cached: true, FileResults: []tooling.FileResult{{File: abs("a"), State: tooling.FileVerdictHit, Success: true}}},
			wantID: "t::1#verdict", wantState: "verdict-hit", success: true,
		},
		{
			name:   "setup failed",
			result: tooling.ExecutionResult{Success: false, StartedAt: time.Unix(1, 0), FileResults: []tooling.FileResult{{File: abs("a"), State: tooling.FileSetupFailed}}},
			wantID: "t::1#0", wantState: "setup-failed", wantKind: "setup",
		},
		{
			name:   "cancelled before it started",
			result: tooling.ExecutionResult{Success: false, Cancelled: true, FileResults: []tooling.FileResult{{File: abs("a"), State: tooling.FileNotStarted}}},
			wantID: "t::1#0", wantState: "not-started",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := perFileTask("t::1", "a")
			tt.result.TaskID = task.ID
			acc := NewAccumulator(Options{Root: root})
			invocations := acc.invocations(task, &tt.result, nil, &ToolRun{})
			if len(invocations) != 1 {
				t.Fatalf("invocations = %+v, want one", invocations)
			}
			inv := invocations[0]
			if inv.ID != tt.wantID || inv.State != tt.wantState || inv.FailureKind != tt.wantKind || inv.Success != tt.success {
				t.Errorf("invocation = %+v, want %s %s %q success=%v", inv, tt.wantID, tt.wantState, tt.wantKind, tt.success)
			}
			if len(inv.Files) != 1 || inv.Files[0].Path != "a" {
				t.Errorf("files = %+v, want a", inv.Files)
			}
		})
	}
}

// A process given no path answers for every file no cache answered.
func TestWholeUnitProcessClaimsTheUnit(t *testing.T) {
	task := tooling.Task{ID: "tsc:pkg:1", ToolName: "tsc", OpConfig: config.ToolOperation{Scope: config.ToolScopePerProject}}
	result := tooling.ExecutionResult{
		TaskID: task.ID, Success: true, RelativeDir: "pkg", WholeUnit: true,
		Processes: []tooling.ProcessResult{{ID: "tsc:pkg:1#1", State: tooling.ProcessRan, ExitCode: new(0), Success: true, Extraction: tooling.ExtractionNone}},
		FileResults: []tooling.FileResult{
			{File: abs("pkg/b.ts"), State: tooling.FileRan, Success: true, ProcessID: "tsc:pkg:1#1", ExitCode: new(0)},
			{File: abs("pkg/a.ts"), State: tooling.FileRan, Success: true, ProcessID: "tsc:pkg:1#1", ExitCode: new(0)},
		},
	}
	invocations := NewAccumulator(Options{Root: root}).invocations(task, &result, nil, &ToolRun{})
	if len(invocations) != 1 || !invocations[0].WholeUnit || invocations[0].Dir != "pkg" {
		t.Fatalf("invocations = %+v, want the one whole-unit process in pkg", invocations)
	}
	if got := []string{invocations[0].Files[0].Path, invocations[0].Files[1].Path}; !reflect.DeepEqual(got, []string{"pkg/a.ts", "pkg/b.ts"}) {
		t.Errorf("files = %v, want both, sorted", got)
	}
}

// A finding two processes of one tool both reported is listed once, by the
// first, and ordinals count across the tool's processes: the fingerprints do
// not depend on how its work was split.
func TestSettleFindings(t *testing.T) {
	finding := func(col int) Finding {
		return Finding{
			Tool: "eslint", Code: "eqeqeq", Source: "eslint", Message: "m", lineHash: "h",
			Location: Location{Path: "a.js", Row: 5, Col: col},
		}
	}
	tr := &ToolRun{Invocations: []Invocation{
		{ID: "eslint::1#1", Findings: []Finding{finding(7)}},
		{ID: "eslint::1#2", Findings: []Finding{finding(3), finding(7)}},
	}}
	settleFindings(tr)
	first, second := tr.Invocations[0].Findings, tr.Invocations[1].Findings
	if len(first) != 1 || len(second) != 1 || second[0].Location.Col != 3 {
		t.Fatalf("findings = %+v, %+v; want the duplicate dropped from the second process", first, second)
	}
	if first[0].Fingerprint != Fingerprint("eslint", "eqeqeq", "a.js", "h", 1) ||
		second[0].Fingerprint != Fingerprint("eslint", "eqeqeq", "a.js", "h", 0) {
		t.Errorf("fingerprints = %s, %s; want ordinals by column across both processes", first[0].Fingerprint, second[0].Fingerprint)
	}
}

// A tool's findings are handed out once its last planned task arrives — or by
// Flush when the run stopped before it — with the fingerprints the report
// holds, and never twice.
func TestFindingsOfAFinishedTool(t *testing.T) {
	finding := func(file string, col int) diagnostic.Diagnostic {
		return diagnostic.Diagnostic{File: abs(file), Row: 1, EndRow: 1, Col: col, EndCol: col, Code: "C", Source: "hadolint", Message: "m", Severity: diagnostic.SeverityError}
	}
	a1, a2, b1 := perFileTask("hadolint::1", "a"), perFileTask("hadolint::2", "a"), perFileTask("other::3", "b")
	b1.ToolName = "other"
	plan := &tooling.ExecutionPlan{Groups: []tooling.TaskGroup{{Tasks: []tooling.Task{a1, a2, b1}}}}
	result := func(task tooling.Task, d diagnostic.Diagnostic) tooling.ExecutionResult {
		return tooling.ExecutionResult{
			ToolName: task.ToolName, TaskID: task.ID, Success: false,
			Processes: []tooling.ProcessResult{{
				ID: task.ID + "#1", Files: task.Files, State: tooling.ProcessRan, ExitCode: new(1),
				Extraction: tooling.ExtractionParsedFindings, Diagnostics: []diagnostic.Diagnostic{d},
			}},
		}
	}

	acc := NewAccumulator(Options{Root: root})
	op := acc.BeginOperation("lint", plan, nil)
	if got := op.AddTask(result(a1, finding("a", 5))); got != nil {
		t.Fatalf("findings before hadolint finished = %+v", got)
	}
	if got := op.AddTask(result(b1, finding("b", 1))); len(got) != 1 || got[0].TaskID != b1.ID {
		t.Fatalf("findings of other = %+v, want its one finding", got)
	}
	got := op.AddTask(result(a2, finding("a", 2)))
	if len(got) != 2 || got[0].TaskID != a1.ID || got[1].TaskID != a2.ID {
		t.Fatalf("findings of hadolint = %+v, want both tasks'", got)
	}
	if rest := op.Flush(); rest != nil {
		t.Errorf("Flush after every tool finished = %+v", rest)
	}
	// Ordinals count across the two tasks: column 2 is the first.
	if got[1].Finding.Fingerprint != Fingerprint("hadolint", "C", "a", "row:1", 0) ||
		got[0].Finding.Fingerprint != Fingerprint("hadolint", "C", "a", "row:1", 1) {
		t.Errorf("fingerprints = %s, %s", got[0].Finding.Fingerprint, got[1].Finding.Fingerprint)
	}
	run := acc.Build(BuildInfo{Selection: Selection{Mode: "all"}})
	for _, tr := range run.Operations[0].Tools {
		for _, inv := range tr.Invocations {
			for _, f := range inv.Findings {
				if tr.Name == "hadolint" && f.Fingerprint != got[0].Finding.Fingerprint && f.Fingerprint != got[1].Finding.Fingerprint {
					t.Errorf("the report's fingerprint %s is not an event's", f.Fingerprint)
				}
			}
		}
	}

	stopped := NewAccumulator(Options{Root: root}).BeginOperation("lint", plan, nil)
	stopped.AddTask(result(a1, finding("a", 5)))
	if rest := stopped.Flush(); len(rest) != 1 || rest[0].TaskID != a1.ID {
		t.Errorf("Flush = %+v, want the finding of the tool the run stopped", rest)
	}
}

// A message a structured format carried with escaped colour codes reaches the
// report without them.
func TestFindingMessageIsPlainText(t *testing.T) {
	f := NewAccumulator(Options{Root: root}).finding("t", diagnostic.Diagnostic{Message: "\x1b[31merror\x1b[0m: bad"}, &ToolRun{})
	if f.Message != "error: bad" {
		t.Errorf("message = %q, want it without ANSI sequences", f.Message)
	}
}

// Duplicates from two processes are listed once, where the strongest was
// reported: the error a failed process reported is not lost behind the warning
// a passing one reported first, nor moved onto the passing one.
func TestSettleFindingsKeepsTheStrongest(t *testing.T) {
	finding := func(severity string, reported, gates bool) Finding {
		return Finding{
			Tool: "t", Code: "C", Source: "t", Message: "m", lineHash: "h", Severity: severity,
			Reported: reported, Gates: gates, Location: Location{Path: "a", Row: 1, Col: 1},
		}
	}
	tr := &ToolRun{Invocations: []Invocation{
		{ID: "t::1#1", Findings: []Finding{finding("warning", false, false)}},
		{ID: "t::1#2", Findings: []Finding{finding("error", true, true)}},
	}}
	settleFindings(tr)
	if len(tr.Invocations[0].Findings) != 0 {
		t.Fatalf("the weaker duplicate was kept: %+v", tr.Invocations[0].Findings)
	}
	if got := tr.Invocations[1].Findings; len(got) != 1 || got[0].Severity != "error" || !got[0].Gates {
		t.Errorf("kept findings of the failed process = %+v, want its gating error", got)
	}

	// Of equals, the first is kept.
	tr = &ToolRun{Invocations: []Invocation{
		{ID: "t::1#1", Findings: []Finding{finding("error", true, true)}},
		{ID: "t::1#2", Findings: []Finding{finding("error", true, true)}},
	}}
	settleFindings(tr)
	if len(tr.Invocations[0].Findings) != 1 || len(tr.Invocations[1].Findings) != 0 {
		t.Errorf("findings = %+v, %+v; want the first of two equals", tr.Invocations[0].Findings, tr.Invocations[1].Findings)
	}
}

func TestRelPath(t *testing.T) {
	tests := []struct {
		name, path, want string
	}{
		{"inside", abs("src/a.go"), "src/a.go"},
		{"root", root, "."},
		{"empty", "", ""},
		{"outside", filepath.FromSlash("/elsewhere/a.go"), filepath.FromSlash("/elsewhere/a.go")},
		{"sibling with a shared prefix", filepath.FromSlash("/repository/a.go"), filepath.FromSlash("/repository/a.go")},
	}
	if runtime.GOOS == "windows" {
		tests = append(tests, struct{ name, path, want string }{"backslashes", root + `\src\a.go`, "src/a.go"})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RelPath(root, tt.path); got != tt.want {
				t.Errorf("RelPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestInvocationOrder(t *testing.T) {
	invocations := []Invocation{
		{ID: "a::10#1"}, {ID: "a::2#cached"}, {ID: "a::2#10"}, {ID: "a::2#2"}, {ID: "a::2#0"}, {ID: "a::2#verdict"},
	}
	sortInvocations(invocations)
	got := make([]string, 0, len(invocations))
	for _, inv := range invocations {
		got = append(got, inv.ID)
	}
	want := []string{"a::2#0", "a::2#2", "a::2#10", "a::2#cached", "a::2#verdict", "a::10#1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// Nothing is recorded through a nil record: a run that writes no report holds
// one.
func TestNilOperationRecord(t *testing.T) {
	var op *OperationRecord
	op.AddTask(tooling.ExecutionResult{})
	op.Stopped(Cancel{})
	op.End(true, 1)
}
