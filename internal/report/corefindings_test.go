package report

import (
	"slices"
	"testing"

	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/textpos"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

// placementDiag is a config-placement diagnostic as the runner builds it: the
// file's path and row 1, gated, resting on its row (no line is read — the
// artifact may be a directory).
func placementDiag(rel string) diagnostic.Diagnostic {
	return diagnostic.Diagnostic{
		File: abs(rel), Row: 1, Col: 1,
		Severity: diagnostic.SeverityError,
		Source:   "datamitsu", Code: "config-placement",
		Message:  "Datamitsu config must be located at the repository git root",
		Reported: true, Gates: true,
		Anchor: &diagnostic.Anchor{Basis: BasisRow, LineHash: RowHash(1), Precision: textpos.Unknown},
	}
}

// AddCoreFindings folds a core-built-in check into a pseudo tool run that the
// report holds complete: it ran, its output was read into findings, and it
// failed on its threshold.
func TestAddCoreFindings(t *testing.T) {
	task := perFileTask("hadolint::1", "a/Dockerfile")
	plan := &tooling.ExecutionPlan{Groups: []tooling.TaskGroup{{Tasks: []tooling.Task{task}}}}
	pass := tooling.ExecutionResult{
		ToolName: "hadolint", TaskID: task.ID, Success: true,
		Processes: []tooling.ProcessResult{{
			ID: task.ID + "#1", Files: task.Files, State: tooling.ProcessRan,
			ExitCode: new(int), Success: true, Extraction: tooling.ExtractionParsedClean,
		}},
	}
	acc := NewAccumulator(testOptions())
	op := acc.BeginOperation("lint", plan, nil)
	op.AddTask(pass)

	got := op.AddCoreFindings("datamitsu", []diagnostic.Diagnostic{
		placementDiag("sub/datamitsu.config.js"),
		placementDiag("sub/.datamitsu"),
	})
	if len(got) != 2 || got[0].TaskID != "builtin" {
		t.Fatalf("AddCoreFindings = %+v, want two builtin tool findings", got)
	}

	run := acc.Build(BuildInfo{Selection: Selection{Mode: "all"}})
	var tr *ToolRun
	for i := range run.Operations[0].Tools {
		if run.Operations[0].Tools[i].Name == "datamitsu" {
			tr = &run.Operations[0].Tools[i]
		}
	}
	if tr == nil {
		t.Fatal("the report holds no datamitsu tool run")
	}
	if !tr.Complete || len(tr.Incomplete) != 0 {
		t.Errorf("datamitsu tool run incomplete: %v", tr.Incomplete)
	}
	if !run.Complete {
		t.Errorf("a run whose only findings are core ones should be complete; reasons: %v", run.Incomplete)
	}

	inv := tr.Invocations[0]
	if inv.State != string(tooling.ProcessRan) || inv.Extraction != string(tooling.ExtractionParsedFindings) ||
		inv.Coverage != string(tooling.CoverageComplete) || inv.ExitCode == nil || *inv.ExitCode != 1 ||
		inv.Success || inv.FailureKind != FailureThreshold {
		t.Errorf("invocation = %+v, want ran/parsed-findings/complete, exit 1, threshold failure", inv)
	}

	// Ordinals distinguish findings on one line of one file; these are two
	// distinct paths, so each rests on ordinal 0. Identity is path-based.
	fingerprints := make([]string, 0, len(inv.Findings))
	for _, f := range inv.Findings {
		fingerprints = append(fingerprints, f.Fingerprint)
		if f.Kind != kindIssue || f.Provenance != provenanceCore || f.Severity != "error" ||
			!f.Reported || !f.Gates {
			t.Errorf("finding = %+v, want a gated core issue", f)
		}
	}
	want := []string{
		Fingerprint("datamitsu", "config-placement", "sub/datamitsu.config.js", "row:1", 0),
		Fingerprint("datamitsu", "config-placement", "sub/.datamitsu", "row:1", 0),
	}
	if !slices.Equal(fingerprints, want) {
		t.Errorf("fingerprints = %v, want %v", fingerprints, want)
	}
	if got[0].Finding.Fingerprint != want[0] && got[1].Finding.Fingerprint != want[0] {
		t.Errorf("the event findings %v are not the report's", []string{got[0].Finding.Fingerprint, got[1].Finding.Fingerprint})
	}
}

// A core check's findings are returned once; a later build of the operation
// holds them again, not twice.
func TestAddCoreFindingsIdempotentBuilds(t *testing.T) {
	acc := NewAccumulator(testOptions())
	op := acc.BeginOperation("lint", nil, nil)
	op.AddCoreFindings("datamitsu", []diagnostic.Diagnostic{placementDiag("sub/.datamitsuignore")})

	first := op.Operation()
	if n := len(first.Tools); n != 1 {
		t.Fatalf("tools = %d, want the one pseudo tool run", n)
	}
	second := op.Operation()
	if n := len(second.Tools); n != 1 {
		t.Fatalf("tools after rebuild = %d, want still one", n)
	}
}

// What the terminal printed is what visible consumers show: a reported core
// finding has Shown set (agent output, annotations and Markdown read
// report.Visible, which keys on Shown), a baselined one does not and fails
// nothing.
func TestAddCoreFindingsVisibility(t *testing.T) {
	reported := placementDiag("sub/datamitsu.config.js")
	baselined := placementDiag("sub/.datamitsuignore")
	baselined.Reported, baselined.Gates, baselined.Baselined = false, false, true

	acc := NewAccumulator(testOptions())
	op := acc.BeginOperation("lint", nil, nil)
	got := op.AddCoreFindings("datamitsu", []diagnostic.Diagnostic{reported, baselined})
	if len(got) != 2 {
		t.Fatalf("AddCoreFindings = %d findings, want 2", len(got))
	}
	if !got[0].Finding.Shown || got[1].Finding.Shown {
		t.Errorf("shown = %v, %v; want the reported finding shown and the baselined one hidden",
			got[0].Finding.Shown, got[1].Finding.Shown)
	}

	run := acc.Build(BuildInfo{Selection: Selection{Mode: "all"}})
	tr := run.Operations[0].Tools[0]
	inv := tr.Invocations[0]
	if inv.ExitCode == nil || *inv.ExitCode != 1 || inv.Success || inv.FailureKind != FailureThreshold {
		t.Errorf("invocation = %+v, want a threshold failure while one finding gates", inv)
	}
	if !tr.Complete {
		t.Errorf("incomplete = %v, want complete with a failed-but-findings invocation", tr.Incomplete)
	}
}

// A run narrowed below the whole repository narrows the pseudo tool run with
// every other tool's.
func TestAddCoreFindingsNarrowed(t *testing.T) {
	acc := NewAccumulator(testOptions())
	op := acc.BeginOperation("lint", nil, nil)
	op.AddCoreFindings("datamitsu", []diagnostic.Diagnostic{placementDiag("sub/.datamitsuignore")})

	run := acc.Build(BuildInfo{Selection: Selection{Mode: "dir"}})
	tr := run.Operations[0].Tools[0]
	if tr.Complete || !slices.Contains(tr.Incomplete, ReasonNarrowedSelection) {
		t.Errorf("incomplete = %v, want %v", tr.Incomplete, ReasonNarrowedSelection)
	}
}
