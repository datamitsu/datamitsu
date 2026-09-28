package tooling

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"
)

func findingsAt(levels ...diagnostic.Severity) []diagnostic.Diagnostic {
	out := make([]diagnostic.Diagnostic, 0, len(levels))
	for _, level := range levels {
		out = append(out, diagnostic.Diagnostic{Message: "m", Severity: level})
	}
	return out
}

func TestThresholdGate(t *testing.T) {
	cases := []struct {
		name       string
		own        config.Severity
		global     config.Severity
		contract   bool
		extraction Extraction
		findings   []diagnostic.Diagnostic
		wantFailed bool
		wantReason string
		reported   []bool
		gates      []bool
		ignored    bool
	}{
		{
			name: "an error at the default threshold", contract: true, findings: findingsAt(diagnostic.SeverityError),
			wantFailed: true, wantReason: "1 finding at or above failOn=error", reported: []bool{true}, gates: []bool{true},
		},
		{
			name: "a warning at the default threshold", contract: true, findings: findingsAt(diagnostic.SeverityWarning),
			reported: []bool{false}, gates: []bool{false},
		},
		{
			name: "a baselined error", contract: true,
			findings: []diagnostic.Diagnostic{
				{Message: "old", Severity: diagnostic.SeverityError, Baselined: true},
				{Message: "new", Severity: diagnostic.SeverityError},
			},
			wantFailed: true, wantReason: "1 finding at or above failOn=error",
			reported: []bool{false, true}, gates: []bool{false, true},
		},
		{
			name: "only baselined errors", contract: true,
			findings: []diagnostic.Diagnostic{{Message: "old", Severity: diagnostic.SeverityError, Baselined: true}},
			reported: []bool{false}, gates: []bool{false},
		},
		{
			name: "failOn warning", own: config.SeverityWarning, contract: true,
			findings:   findingsAt(diagnostic.SeverityWarning, diagnostic.SeverityInfo, diagnostic.SeverityError),
			wantFailed: true, wantReason: "2 findings at or above failOn=warning",
			reported: []bool{true, false, true}, gates: []bool{true, false, true},
		},
		{
			name: "the global value raises an operation", global: config.SeverityWarning, contract: true,
			findings: findingsAt(diagnostic.SeverityWarning), wantFailed: true,
			wantReason: "1 finding at or above failOn=warning", reported: []bool{true}, gates: []bool{true},
		},
		{
			name: "the global value does not lower an operation", own: config.SeverityHint, global: config.SeverityWarning,
			contract: true, findings: findingsAt(diagnostic.SeverityHint), wantFailed: true,
			wantReason: "1 finding at or above failOn=hint", reported: []bool{true}, gates: []bool{true},
		},
		{
			name: "a module before the contract", own: config.SeverityWarning, findings: findingsAt(diagnostic.SeverityError),
			reported: []bool{true}, gates: []bool{false}, ignored: true,
		},
		{
			name: "a module before the contract at the default threshold", findings: findingsAt(diagnostic.SeverityError),
			reported: []bool{true}, gates: []bool{false},
		},
		{
			name: "output nobody parsed", own: config.SeverityWarning, contract: true, extraction: ExtractionParserUnavailable,
		},
		{
			name: "the findings kept from truncated output", contract: true, extraction: ExtractionTruncated,
			findings: findingsAt(diagnostic.SeverityError), wantFailed: true,
			wantReason: "1 finding at or above failOn=error", reported: []bool{true}, gates: []bool{true},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var ignored []string
			asked := 0
			gate := ThresholdGate(c.global, func(module string) bool {
				asked++
				if module != "core" {
					t.Errorf("contract asked about module %q", module)
				}
				return c.contract
			}, func(tool string) { ignored = append(ignored, tool) })
			task := Task{
				ToolName: "tool",
				Tool:     config.Tool{Name: "tool", OutputParser: &config.OutputParser{Module: "core", Parser: "tool"}},
				OpConfig: config.ToolOperation{FailOn: c.own},
			}
			extraction := c.extraction
			if extraction == "" {
				extraction = ExtractionParsedFindings
			}
			proc := ProcessResult{Extraction: extraction, Diagnostics: slices.Clone(c.findings)}
			if extraction == ExtractionParsedFindings || extraction == ExtractionTruncated {
				proc.ParserModule = "core"
			}
			decision := gate(task, &proc)
			if decision.Failed != c.wantFailed || decision.Reason != c.wantReason {
				t.Errorf("decision = %+v, want failed %v with %q", decision, c.wantFailed, c.wantReason)
			}
			for i, d := range proc.Diagnostics {
				if d.Reported != c.reported[i] || d.Gates != c.gates[i] {
					t.Errorf("finding %d: reported %v gates %v, want %v %v", i, d.Reported, d.Gates, c.reported[i], c.gates[i])
				}
				if d.Gates != slices.Contains(decision.Gating, i) {
					t.Errorf("finding %d: gates %v but Gating = %v", i, d.Gates, decision.Gating)
				}
			}
			parsed := extraction == ExtractionParsedFindings || extraction == ExtractionTruncated
			if proc.GateActive != (c.contract && parsed) || proc.FailOn != config.EffectiveFailOn(task.OpConfig, c.global) {
				t.Errorf("GateActive %v FailOn %q", proc.GateActive, proc.FailOn)
			}
			// A module that did not parse was never described; asking would
			// load it again.
			if want := map[bool]int{true: 1, false: 0}[parsed]; asked != want {
				t.Errorf("contract asked %d times, want %d", asked, want)
			}
			if (len(ignored) > 0) != c.ignored {
				t.Errorf("ignored = %v, want a report: %v", ignored, c.ignored)
			}
		})
	}
}

// gatedProject runs "tool" over files per file, or in one batch, with a parser
// that gives each file the findings levels names for it, and the failOn gate
// with contract.
func gatedProject(t *testing.T, failFast, contract bool, script string, levels map[string][]diagnostic.Severity, names ...string) (*Executor, string, []string) {
	t.Helper()
	e, _, root, files := resultsProject(t, failFast, script, names...)
	e.SetParser(fileParser{findings: func(stdout string) []diagnostic.Diagnostic {
		var out []diagnostic.Diagnostic
		for path := range strings.FieldsSeq(stdout) {
			for _, level := range levels[filepath.Base(path)] {
				out = append(out, diagnostic.Diagnostic{Message: "m", Severity: level, File: path})
			}
		}
		return out
	}})
	e.SetGate(ThresholdGate("", func(string) bool { return contract }, nil))
	return e, root, files
}

func gatedTask(root string, files []string, failOn config.Severity) Task {
	task := loopTask(root, files)
	task.Tool = config.Tool{Name: "tool", OutputParser: &config.OutputParser{Module: "core", Parser: "tool"}}
	task.OpConfig.FailOn = failOn
	return task
}

func fileSuccess(result ExecutionResult) []bool {
	out := make([]bool, 0, len(result.FileResults))
	for _, fr := range result.FileResults {
		out = append(out, fr.Success)
	}
	return out
}

// TestGateFailsAProcessThatExitedZero follows the gate through the executor: a
// finding at or above the threshold fails a process the tool passed, one below
// it does not, a non-zero exit fails at any threshold, and a module before the
// contract leaves the exit code alone to decide.
// TestThresholdGateAsksTheModuleThatParsed: findings the embedded fallback
// read gate by its contract, whatever module the tool declares, or none — and
// so do the fallback's findings under a declared parser the run could not use.
func TestThresholdGateAsksTheModuleThatParsed(t *testing.T) {
	for _, extraction := range []Extraction{ExtractionParsedFindings, ExtractionParserUnavailable} {
		var asked []string
		gate := ThresholdGate("", func(module string) bool {
			asked = append(asked, module)
			return module == EmbeddedParserModule
		}, nil)
		task := Task{ToolName: "ruff", Tool: config.Tool{Name: "ruff"}}
		proc := ProcessResult{
			Extraction: extraction, Provenance: "fallback:sarif", ParserModule: EmbeddedParserModule,
			Diagnostics: findingsAt(diagnostic.SeverityError),
		}
		decision := gate(task, &proc)
		if !decision.Failed || !proc.GateActive || !slices.Equal(asked, []string{EmbeddedParserModule}) {
			t.Errorf("%s: decision %+v, active %v, asked %v; want the embedded module's contract to gate",
				extraction, decision, proc.GateActive, asked)
		}
	}
}

func TestGateFailsAProcessThatExitedZero(t *testing.T) {
	const echoAndPass = `echo "$1"`
	const failBad = `echo "$1"; case "$1" in *bad*) exit 1;; esac`
	cases := []struct {
		name       string
		script     string
		contract   bool
		op         config.OperationType
		failOn     config.Severity
		levels     map[string][]diagnostic.Severity
		wantOK     bool
		wantReason FailureReason
		wantFiles  []bool
	}{
		{
			name: "an error-level finding", script: echoAndPass, contract: true,
			levels:     map[string][]diagnostic.Severity{"a.txt": {diagnostic.SeverityError}},
			wantReason: FailureReasonThreshold, wantFiles: []bool{false},
		},
		{
			name: "a warning with failOn warning", script: echoAndPass, contract: true, failOn: config.SeverityWarning,
			levels:     map[string][]diagnostic.Severity{"a.txt": {diagnostic.SeverityWarning}},
			wantReason: FailureReasonThreshold, wantFiles: []bool{false},
		},
		{
			name: "a warning at the default threshold", script: echoAndPass, contract: true,
			levels: map[string][]diagnostic.Severity{"a.txt": {diagnostic.SeverityWarning}},
			wantOK: true, wantFiles: []bool{true},
		},
		{
			name: "a failing tool without findings", script: `exit 1`, contract: true, failOn: config.SeverityHint,
			wantReason: FailureReasonIndependent, wantFiles: []bool{false},
		},
		{
			name: "a fix that leaves an error behind", script: echoAndPass, contract: true, op: config.OpFix,
			levels:     map[string][]diagnostic.Severity{"a.txt": {diagnostic.SeverityError}},
			wantReason: FailureReasonThreshold, wantFiles: []bool{false},
		},
		{
			name: "a module before the contract", script: echoAndPass, failOn: config.SeverityWarning,
			levels: map[string][]diagnostic.Severity{"a.txt": {diagnostic.SeverityError}},
			wantOK: true, wantFiles: []bool{true},
		},
		{
			name: "a failing tool whose findings do not reach the threshold", script: failBad, contract: true,
			levels:     map[string][]diagnostic.Severity{"bad.txt": {diagnostic.SeverityInfo}},
			wantReason: FailureReasonIndependent, wantFiles: []bool{false},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			name := "a.txt"
			if c.script == failBad || c.script == `exit 1` {
				name = "bad.txt"
			}
			e, root, files := gatedProject(t, false, c.contract, c.script, c.levels, name)
			task := gatedTask(root, files, c.failOn)
			if c.op != "" {
				task.Operation = c.op
			}
			result := e.executeTask(context.Background(), task)
			if result.Success != c.wantOK {
				t.Fatalf("Success = %v, want %v (%v)", result.Success, c.wantOK, result.Error)
			}
			if !c.wantOK && result.FailureReason != c.wantReason {
				t.Errorf("FailureReason = %v, want %v", result.FailureReason, c.wantReason)
			}
			if got := fileSuccess(result); !slices.Equal(got, c.wantFiles) {
				t.Errorf("file success = %v, want %v", got, c.wantFiles)
			}
			proc := result.Processes[0]
			if proc.ThresholdFailed != (c.wantReason == FailureReasonThreshold && !c.wantOK) {
				t.Errorf("ThresholdFailed = %v", proc.ThresholdFailed)
			}
			if c.wantReason == FailureReasonThreshold && !strings.Contains(result.Error.Error(), "at or above failOn=") {
				t.Errorf("Error = %v, want the threshold named", result.Error)
			}
		})
	}
}

// TestGateStopsAPerFileLoop: under fail-fast a file the threshold failed stops
// the per-file loop as a non-zero exit does.
func TestGateStopsAPerFileLoop(t *testing.T) {
	e, root, files := gatedProject(t, true, true, `echo "$1"`,
		map[string][]diagnostic.Severity{"a.txt": {diagnostic.SeverityError}}, "a.txt", "b.txt")
	result := e.executeTask(context.Background(), gatedTask(root, files, ""))
	if result.Success || result.FilesNotRun != 1 {
		t.Fatalf("Success = %v, FilesNotRun = %d; want the loop stopped after a.txt", result.Success, result.FilesNotRun)
	}
	if got := fileStates(result); !slices.Equal(got, []FileState{FileRan, FileNotStarted}) {
		t.Errorf("file states = %v", got)
	}
}

// TestAThresholdFailureKeepsItsExitCode: a file the threshold failed exited
// 0, and a later file the run then cancelled does not lend it its exit code.
func TestAThresholdFailureKeepsItsExitCode(t *testing.T) {
	e, root, files := gatedProject(t, false, true, `case "$1" in *a.txt) echo "$1" ;; *) sleep 5 ;; esac`,
		map[string][]diagnostic.Severity{"a.txt": {diagnostic.SeverityError}}, "a.txt", "b.txt")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gate := e.gate
	e.SetGate(func(task Task, proc *ProcessResult) GateDecision {
		decision := gate(task, proc)
		if decision.Failed {
			time.AfterFunc(200*time.Millisecond, cancel)
		}
		return decision
	})
	result := e.executeTask(ctx, gatedTask(root, files, ""))
	if result.Success || result.FailureReason != FailureReasonThreshold || result.ExitCode != 0 {
		t.Fatalf("Success = %v, FailureReason = %v, ExitCode = %d; want the threshold failure of a.txt, exit 0",
			result.Success, result.FailureReason, result.ExitCode)
	}
	if !strings.Contains(result.Command, "a.txt") {
		t.Errorf("Command = %q, want a.txt's", result.Command)
	}
}

// TestGateFailsOnlyTheGatedFilesOfABatch: one batch process answers for many
// files; the threshold fails the files its gating findings name, not the rest.
func TestGateFailsOnlyTheGatedFilesOfABatch(t *testing.T) {
	e, root, files := gatedProject(t, false, true, `echo "$@"`,
		map[string][]diagnostic.Severity{"a.txt": {diagnostic.SeverityError}, "b.txt": {diagnostic.SeverityHint}},
		"a.txt", "b.txt", "c.txt")
	task := gatedTask(root, files, "")
	task.OpConfig.Scope = config.ToolScopeRepository
	task.OpConfig.Args = []string{"{files}"}
	result := e.executeTask(context.Background(), task)
	if result.Success || result.FailureReason != FailureReasonThreshold {
		t.Fatalf("Success = %v, FailureReason = %v; want a threshold failure", result.Success, result.FailureReason)
	}
	if got := fileSuccess(result); !slices.Equal(got, []bool{false, true, true}) {
		t.Errorf("file success = %v, want only a.txt failed", got)
	}
	if result.FailOn != config.SeverityError {
		t.Errorf("FailOn = %q, want the effective threshold", result.FailOn)
	}
}
