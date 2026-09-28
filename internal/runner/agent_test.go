package runner

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/tooling"
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
				Tool: "tsc", Source: "tsc", Code: "TS2322", Severity: "error", Reported: true, Shown: true, Kind: "issue",
				Message: "first\r\nsecond\nthird", Location: report.Location{Path: "src/a.ts", Row: 3, Col: 7},
			}}},
			want: "src/a.ts:3:7: error tsc(TS2322): first\\nsecond\\nthird\n",
		},
		{
			name: "a path with a line break is one record too",
			inv: report.Invocation{State: "ran", ExitCode: &one, Findings: []report.Finding{{
				Tool: "tsc", Source: "tsc", Severity: "error", Reported: true, Shown: true, Kind: "issue",
				Message: "m", Location: report.Location{Path: "odd\nname.ts", Row: 1},
			}}},
			want: "odd\\nname.ts:1: error tsc: m\n",
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

// Every record is one line, whatever it names, and every part of it is masked:
// a skip reason can be a configuration's text, which may quote a secret.
func TestPrintAgentOperationRecordsAreOneMaskedLineEach(t *testing.T) {
	const secret = "super-secret-value"
	plan := &tooling.ExecutionPlan{Skipped: []tooling.SkippedTool{
		{ToolName: "trufflehog", Reason: tooling.SkipReasonConfig, Detail: "needs " + secret},
		{ToolName: "odd", Reason: tooling.SkipReasonConfig, Detail: "two\nlines"},
		{ToolName: "native", Reason: tooling.SkipReasonUnsupportedPlatform, Detail: "windows/arm64"},
	}}
	acc := report.NewAccumulator(report.Options{})
	sc := &sharedContext{secrets: &report.Secrets{}}
	sc.secrets.Add([]string{"DEPLOY_TOKEN=" + secret})
	out := captureStdout(t, func() {
		sc.printAgentOperation(agentOperation{op: config.OpLint, record: acc.BeginOperation("lint", plan, nil)})
	})
	want := "native: skipped (no binary for windows/arm64)\n" +
		"odd: skipped (two\\nlines)\n" +
		"trufflehog: skipped (needs ***)\n" +
		"lint: 0 tools · 0 runs · 0 failed · 0 errors 0 warnings · 0 hidden\n"
	if out != want {
		t.Errorf("records =\n%s\nwant\n%s", out, want)
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
