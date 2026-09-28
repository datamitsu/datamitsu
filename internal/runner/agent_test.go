package runner

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/report"
)

func TestAgentInvocation(t *testing.T) {
	one, two := 1, 2
	tool := report.ToolRun{Name: "tsc", FailOn: "error", GateActive: true}
	tests := []struct {
		name string
		inv  report.Invocation
		want string
	}{
		{
			name: "a finding whose message spans lines is one record",
			inv: report.Invocation{State: "ran", ExitCode: &one, Findings: []report.Finding{{
				Tool: "tsc", Source: "tsc", Code: "TS2322", Severity: "error", Reported: true, Kind: "issue",
				Message: "first\r\nsecond\nthird", Location: report.Location{Path: "src/a.ts", Row: 3, Col: 7},
			}}},
			want: "src/a.ts:3:7: error tsc(TS2322): first\\nsecond\\nthird\n",
		},
		{
			name: "a failure without findings names its directory and ends with the tool's output",
			inv: report.Invocation{
				Dir: "packages/api", State: "ran", ExitCode: &two, FailureKind: "exit",
				OutputTail: "noise\n\nerror TS5083: Cannot read file\n",
				Findings: []report.Finding{{
					Tool: "tsc", Source: "tsc", Severity: "error", Kind: "synthetic",
					Message: "tsc exited 2 without parsable findings",
				}},
			},
			want: "tsc [packages/api] exited 2 without parsable findings\n  │  noise\n  │  error TS5083: Cannot read file\n",
		},
		{
			name: "a process that could not start, after others of its task ran",
			inv: report.Invocation{
				State: "setup-failed", FailureKind: "setup", Files: []report.FileResult{{Path: "b.ts"}},
				OutputTail: "stdin: permission denied",
			},
			want: "b.ts: tsc failed before it ran\n  │  stdin: permission denied\n",
		},
		{
			name: "a tool that failed with exit 0 and no finding",
			inv:  report.Invocation{State: "ran", ExitCode: new(0), FailureKind: "exit"},
			want: "tsc failed without parsable findings\n",
		},
		{
			name: "a passed invocation prints nothing",
			inv:  report.Invocation{State: "ran", ExitCode: new(0), Success: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			var shown, hidden levelCounts
			agentInvocation(&b, tool, tt.inv, &shown, &hidden)
			if b.String() != tt.want {
				t.Errorf("records =\n%q\nwant\n%q", b.String(), tt.want)
			}
		})
	}
}

// The tail is masked with the whole operation, before it is split into lines:
// a secret that spans lines is still one value to mask.
func TestAgentTailIsMaskedBeforeItIsSplit(t *testing.T) {
	code := 1
	secret := "first-half-of-it\nsecond-half-of-it"
	op := report.Operation{Tools: []report.ToolRun{{Name: "t", Invocations: []report.Invocation{{
		State: "ran", ExitCode: &code, FailureKind: "exit", OutputTail: "token: " + secret + "\n",
		Findings: []report.Finding{{Tool: "t", Source: "t", Kind: "synthetic", Severity: "error", Message: "t exited 1 without parsable findings"}},
	}}}}}
	report.MaskAll(&op, []string{secret})
	var b strings.Builder
	var shown, hidden levelCounts
	agentInvocation(&b, op.Tools[0], op.Tools[0].Invocations[0], &shown, &hidden)
	if strings.Contains(b.String(), "half-of-it") || !strings.Contains(b.String(), "token: ***") {
		t.Errorf("records =\n%s\nwant the secret masked whole", b.String())
	}
}
