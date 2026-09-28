package patch

import (
	"bytes"
	"testing"

	"github.com/datamitsu/datamitsu/internal/report"
)

// TestRenderInApplicationOrder: patches are written in the order the steps
// that applied them ran, whatever the order of the tools in the report.
func TestRenderInApplicationOrder(t *testing.T) {
	file := func(path, patch string) report.FileResult { return report.FileResult{Path: path, Patch: patch} }
	run := &report.Run{Operations: []report.Operation{{
		Name: "fix", Ran: true,
		Tools: []report.ToolRun{
			{Name: "a-second", Invocations: []report.Invocation{{Step: 2, Files: []report.FileResult{file("x.txt", "2\n")}}}},
			{Name: "b-first", Invocations: []report.Invocation{
				{Step: 1, Files: []report.FileResult{file("y.txt", "1y\n"), file("z.txt", "")}},
				{Step: 1, Files: []report.FileResult{file("x.txt", "1x\n")}},
			}},
		},
	}}}
	var out bytes.Buffer
	if err := (Renderer{}).Render(&out, run, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "1y\n1x\n2\n"; got != want {
		t.Errorf("patch = %q, want %q", got, want)
	}
}
