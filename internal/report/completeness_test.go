package report

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

func ran(extraction tooling.Extraction) Invocation {
	return Invocation{State: "ran", Coverage: "complete", Extraction: string(extraction)}
}

func TestToolReasons(t *testing.T) {
	all := Selection{Mode: "all"}
	tests := []struct {
		name        string
		sel         Selection
		parsed      bool
		invocations []Invocation
		want        []Reason
	}{
		{
			name: "parsed clean and with findings", sel: all, parsed: true,
			invocations: []Invocation{ran(tooling.ExtractionParsedClean), ran(tooling.ExtractionParsedFindings)}, want: []Reason{},
		},
		{
			name: "a narrowed selection", sel: Selection{Mode: "paths"}, parsed: true,
			invocations: []Invocation{ran(tooling.ExtractionParsedClean)}, want: []Reason{ReasonNarrowedSelection},
		},
		{
			name: "a task over part of its unit", sel: all, parsed: true,
			invocations: []Invocation{{State: "ran", Coverage: "partial", Extraction: "parsed-clean"}}, want: []Reason{ReasonPartialUnit},
		},
		{
			name: "no parser", sel: all,
			invocations: []Invocation{ran(tooling.ExtractionNone)}, want: []Reason{ReasonNoExtraction},
		},
		{
			name: "every extraction failure", sel: all, parsed: true,
			invocations: []Invocation{ran(tooling.ExtractionParseFailed), ran(tooling.ExtractionParserUnavailable), ran(tooling.ExtractionTruncated)},
			want:        []Reason{ReasonParseFailed, ReasonParserUnavailable, ReasonTruncated},
		},
		{
			name: "a failed process its parser found nothing in", sel: all, parsed: true,
			invocations: []Invocation{
				{State: "ran", Coverage: "complete", Extraction: "parsed-clean", ExitCode: new(2)},
				{State: "ran", Coverage: "complete", Extraction: "parsed-clean", ExitCode: new(0)},
				{State: "ran", Coverage: "complete", Extraction: "parsed-findings", ExitCode: new(1)},
			},
			want: []Reason{ReasonFailedWithoutFindings},
		},
		{
			name: "a cache hit of a parsed tool replays a parsed-clean pass", sel: all, parsed: true,
			invocations: []Invocation{{State: "cached"}, {State: "verdict-hit"}}, want: []Reason{},
		},
		{
			name: "a cache hit of a tool without a parser", sel: all,
			invocations: []Invocation{{State: "cached"}}, want: []Reason{ReasonUnparsedCacheHit},
		},
		{
			name: "tasks that did not run to the end", sel: all, parsed: true,
			invocations: []Invocation{{State: "cancelled"}, {State: "not-started"}, {State: "setup-failed"}},
			want:        []Reason{ReasonCancelled, ReasonNotStarted, ReasonSetupFailed},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toolReasons(&ToolRun{Invocations: tt.invocations}, tt.sel, tt.parsed)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("toolReasons() = %v, want %v", got, tt.want)
			}
		})
	}
}

// A document an earlier build wrote called a tool complete although one of
// its processes failed while its parser found nothing; read back, it is not.
func TestRevise(t *testing.T) {
	two := 2
	run := &Run{Complete: true, Operations: []Operation{{Name: "lint", Ran: true, Tools: []ToolRun{
		{Name: "a", Complete: true, Incomplete: []Reason{}, Invocations: []Invocation{{State: "ran", Extraction: "parsed-clean", ExitCode: &two}}},
		{Name: "b", Complete: true, Incomplete: []Reason{}, Invocations: []Invocation{{State: "ran", Extraction: "parsed-findings", ExitCode: &two}}},
	}}}}
	Revise(run)
	a, b := run.Operations[0].Tools[0], run.Operations[0].Tools[1]
	if run.Complete || a.Complete || !reflect.DeepEqual(a.Incomplete, []Reason{ReasonFailedWithoutFindings}) || !b.Complete {
		t.Errorf("revised run = %+v", run)
	}
	before := fmt.Sprint(run)
	Revise(run)
	if fmt.Sprint(run) != before {
		t.Error("revising twice changed the run again")
	}
}

// A run is complete only when every tool run is, every operation ran and
// nothing was left out at run level; --tools leaves the listed tools' own runs
// complete.
func TestJudge(t *testing.T) {
	tools := config.MapOfTools{"a": {OutputParser: &config.OutputParser{}}, "b": {}}
	clean := []Invocation{ran(tooling.ExtractionParsedClean)}
	tests := []struct {
		name         string
		run          Run
		wantComplete bool
		wantRun      []Reason
		wantTool     []Reason
	}{
		{
			name:         "complete",
			run:          Run{Selection: Selection{Mode: "all"}, Operations: []Operation{{Ran: true, Tools: []ToolRun{{Name: "a", Invocations: clean}}}}},
			wantComplete: true, wantRun: []Reason{}, wantTool: []Reason{},
		},
		{
			name:    "a tools filter",
			run:     Run{Selection: Selection{Mode: "all", Tools: []string{"a"}}, Operations: []Operation{{Ran: true, Tools: []ToolRun{{Name: "a", Invocations: clean}}}}},
			wantRun: []Reason{ReasonToolsFilter}, wantTool: []Reason{},
		},
		{
			name: "an operation that did not run and a tool that could not narrow",
			run: Run{Selection: Selection{Mode: "all"}, Operations: []Operation{
				{Ran: true, Skipped: []Skip{{Tool: "b", Reason: "not-narrowable"}, {Tool: "c", Reason: "config"}}, Tools: []ToolRun{{Name: "a", Invocations: clean}}},
				{Name: "lint"},
			}},
			wantRun: []Reason{ReasonNotNarrowable, ReasonOperationSkipped}, wantTool: []Reason{},
		},
		{
			name:    "a narrowed run that matched no tool",
			run:     Run{Selection: Selection{Mode: "subtree"}, Operations: []Operation{{Ran: true}}},
			wantRun: []Reason{ReasonNarrowedSelection},
		},
		{
			name:    "a platform skip",
			run:     Run{Selection: Selection{Mode: "all"}, Operations: []Operation{{Ran: true, Tools: []ToolRun{{Name: "b", Incomplete: []Reason{ReasonPlatformSkip}}}}}},
			wantRun: []Reason{}, wantTool: []Reason{ReasonPlatformSkip},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := tt.run
			judge(&run, tools)
			if run.Complete != tt.wantComplete || !reflect.DeepEqual(run.Incomplete, tt.wantRun) {
				t.Errorf("run complete = %v %v, want %v %v", run.Complete, run.Incomplete, tt.wantComplete, tt.wantRun)
			}
			if tt.wantTool != nil {
				tr := run.Operations[0].Tools[0]
				if !reflect.DeepEqual(tr.Incomplete, tt.wantTool) || tr.Complete != (len(tt.wantTool) == 0) {
					t.Errorf("tool complete = %v %v, want %v", tr.Complete, tr.Incomplete, tt.wantTool)
				}
			}
		})
	}
}

// A formatter whose stdout is the file's new content declares a parser for its
// lint operation only: a cache hit of its fix replays an exit code, never a
// parsed-clean pass.
func TestJudgeCachedStdoutFormatter(t *testing.T) {
	tools := config.MapOfTools{"fmt": {
		OutputParser: &config.OutputParser{Module: "m", Parser: "p"},
		Operations: map[config.OperationType]config.ToolOperation{
			config.OpFix:  {Output: config.ToolOutputStdout},
			config.OpLint: {},
		},
	}}
	cached := []Invocation{{State: "cached"}}
	run := Run{Selection: Selection{Mode: "all"}, Operations: []Operation{
		{Name: "fix", Ran: true, Tools: []ToolRun{{Name: "fmt", Invocations: cached}}},
		{Name: "lint", Ran: true, Tools: []ToolRun{{Name: "fmt", Invocations: cached}}},
	}}
	judge(&run, tools)
	if fix := run.Operations[0].Tools[0]; !reflect.DeepEqual(fix.Incomplete, []Reason{ReasonUnparsedCacheHit}) {
		t.Errorf("the formatter's fix = %v, want unparsed-cache-hit", fix.Incomplete)
	}
	if lint := run.Operations[1].Tools[0]; !lint.Complete {
		t.Errorf("the formatter's lint = %v, want complete", lint.Incomplete)
	}

	task := perFileTask("fmt::1", "a")
	task.OpConfig.Output = config.ToolOutputStdout
	result := tooling.ExecutionResult{
		TaskID: task.ID, Success: true, Cached: true,
		FileResults: []tooling.FileResult{{File: abs("a"), State: tooling.FileCached, Success: true}},
	}
	if inv := NewAccumulator(Options{Root: root}).invocations(task, &result, nil, &ToolRun{})[0]; inv.Extraction != "none" {
		t.Errorf("a stdout formatter's cache hit claims extraction %s", inv.Extraction)
	}
}

func TestExcludedTools(t *testing.T) {
	tools := config.MapOfTools{
		"a":     {Operations: map[config.OperationType]config.ToolOperation{config.OpLint: {}}},
		"b":     {Operations: map[config.OperationType]config.ToolOperation{config.OpLint: {}}},
		"fixer": {Operations: map[config.OperationType]config.ToolOperation{config.OpFix: {}}},
	}
	if got := excludedTools(tools, []string{"lint"}, []string{"a"}); !reflect.DeepEqual(got, []string{"b"}) {
		t.Errorf("excludedTools(lint, a) = %v, want [b]: a fix-only tool was never asked for", got)
	}
	if got := excludedTools(tools, []string{"fix", "lint"}, []string{"a"}); !reflect.DeepEqual(got, []string{"b", "fixer"}) {
		t.Errorf("excludedTools(fix+lint, a) = %v, want [b fixer]", got)
	}
	if got := excludedTools(tools, []string{"lint"}, nil); got != nil {
		t.Errorf("excludedTools without a filter = %v, want none", got)
	}
}

// A tool with no binary for this host is listed as a tool run that did not
// complete, never left out.
func TestPlatformSkippedToolIsListed(t *testing.T) {
	plan := &tooling.ExecutionPlan{Skipped: []tooling.SkippedTool{
		{ToolName: "hadolint", Reason: tooling.SkipReasonUnsupportedPlatform, Detail: "windows/arm64"},
		{ToolName: "off", Reason: tooling.SkipReasonConfig},
	}}
	acc := NewAccumulator(testOptions())
	acc.BeginOperation("lint", plan, nil).End(true, 0)
	run := acc.Build(BuildInfo{Selection: Selection{Mode: "all"}})
	tools := run.Operations[0].Tools
	if len(tools) != 1 || tools[0].Name != "hadolint" || tools[0].Complete ||
		!reflect.DeepEqual(tools[0].Incomplete, []Reason{ReasonPlatformSkip}) {
		t.Fatalf("tools = %+v, want hadolint alone, incomplete for platform-skip", tools)
	}
	if run.Complete {
		t.Error("a run with a platform skip is complete")
	}
	if len(run.Operations[0].Skipped) != 2 {
		t.Errorf("skipped = %+v, want both skips listed", run.Operations[0].Skipped)
	}
}
