package patch

import (
	"bytes"
	"testing"
	zerovalue_time "time"

	"github.com/datamitsu/datamitsu/internal/report"
)

// TestRenderInApplicationOrder: patches are written in the order the steps
// that applied them ran, whatever the order of the tools in the report.
func TestRenderInApplicationOrder(t *testing.T) {
	file := func(path, patch string) report.FileResult {
		return report.FileResult{State: "", Success: false, ExitCode: nil, Path: path, Patch: patch}
	}
	run := &report.Run{
		Schema:      "",
		Fingerprint: "",
		Datamitsu:   report.Producer{Version: "", Configuration: ""},
		StartedAt:   zerovalue_time.Time{},
		EndedAt:     zerovalue_time.Time{},
		Selection:   report.Selection{Mode: "", Dir: "", Paths: nil, Tools: nil, FileScoped: false, ExcludedTools: nil},
		FailFast:    false,
		Complete:    false,
		Incomplete:  nil,
		Exports:     nil,
		CI:          report.CIEnvironment{Vendor: "", SHA: "", Ref: "", BaseRef: "", PRNumber: ""}, Operations: []report.Operation{{
			Success:         false,
			Duration:        0,
			Skipped:         nil,
			Cancelled:       nil,
			ChangesObserved: false,
			ChangesReason:   "",
			ChangesDetail:   "",
			ChangesScope:    nil,
			Changes:         nil,
			Name:            "fix", Ran: true,
			Tools: []report.ToolRun{
				{App: report.AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""}, Parser: nil, FailOn: "", GateActive: false, Category: "", Complete: false, Incomplete: nil, Name: "a-second", Invocations: []report.Invocation{{ID: "", TaskID: "", Dir: "", Scope: "", Granularity: "", Arity: "", Coverage: "", WholeUnit: false, State: "", ExitCode: nil, Success: false, FailureKind: "", Duration: 0, Extraction: "", Provenance: "", Findings: nil, Changes: nil, OutputTail: "", Step: 2, Files: []report.FileResult{file("x.txt", "2\n")}}}},
				{
					App:        report.AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""},
					Parser:     nil,
					FailOn:     "",
					GateActive: false,
					Category:   "",
					Complete:   false,
					Incomplete: nil, Name: "b-first", Invocations: []report.Invocation{
						{ID: "", TaskID: "", Dir: "", Scope: "", Granularity: "", Arity: "", Coverage: "", WholeUnit: false, State: "", ExitCode: nil, Success: false, FailureKind: "", Duration: 0, Extraction: "", Provenance: "", Findings: nil, Changes: nil, OutputTail: "", Step: 1, Files: []report.FileResult{file("y.txt", "1y\n"), file("z.txt", "")}},
						{ID: "", TaskID: "", Dir: "", Scope: "", Granularity: "", Arity: "", Coverage: "", WholeUnit: false, State: "", ExitCode: nil, Success: false, FailureKind: "", Duration: 0, Extraction: "", Provenance: "", Findings: nil, Changes: nil, OutputTail: "", Step: 1, Files: []report.FileResult{file("x.txt", "1x\n")}},
					},
				},
			},
		}},
	}
	var out bytes.Buffer
	if err := (Renderer{}).Render(&out, run, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "1y\n1x\n2\n"; got != want {
		t.Errorf("patch = %q, want %q", got, want)
	}
}
