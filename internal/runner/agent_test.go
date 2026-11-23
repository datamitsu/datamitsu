package runner

import (
	"strings"
	"testing"
	zerovalue_time "time"

	zerovalue_cienv "github.com/datamitsu/datamitsu/internal/cienv"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

func TestAgentInvocation(t *testing.T) {
	one, two := 1, 2
	tool := report.ToolRun{
		RedactedReasons: nil,
		App:             report.AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""}, Parser: nil, Category: "", Complete: false, Incomplete: nil, Invocations: nil, Name: "tsc", FailOn: "error", GateActive: true,
	}
	tests := []struct {
		name string
		inv  report.Invocation
		want string
	}{
		{
			name: "a finding whose message spans lines is one record",
			inv: report.Invocation{
				RedactedState:      0,
				RedactedExtraction: 0,

				ID:          "",
				TaskID:      "",
				Dir:         "",
				Scope:       "",
				Granularity: "",
				Arity:       "",
				Coverage:    "",
				WholeUnit:   false,
				Success:     false,
				FailureKind: "",
				Duration:    0,
				Extraction:  "",
				Provenance:  "",
				Files:       nil,
				Step:        0,
				Changes:     nil,
				OutputTail:  "", State: "ran", ExitCode: &one, Findings: []report.Finding{{
					Fingerprint:      "",
					FingerprintBasis: "",
					RuleURL:          "",
					Gates:            false,
					Baselined:        false,
					Provenance:       "",
					Tool:             "tsc", Source: "tsc", Code: "TS2322", Severity: "error", Reported: true, Shown: true, Kind: "issue",
					Message: "first\r\nsecond\nthird", Location: report.Location{EndRow: 0, EndCol: 0, Unit: "", Chars: nil, Bytes: nil, Utf16: nil, Precision: "", Path: "src/a.ts", Row: 3, Col: 7},
				}},
			},
			want: "src/a.ts:3:7: error tsc(TS2322): first\\nsecond\\nthird\n",
		},
		{
			name: "a path with a line break is one record too",
			inv: report.Invocation{
				RedactedState:      0,
				RedactedExtraction: 0,

				ID:          "",
				TaskID:      "",
				Dir:         "",
				Scope:       "",
				Granularity: "",
				Arity:       "",
				Coverage:    "",
				WholeUnit:   false,
				Success:     false,
				FailureKind: "",
				Duration:    0,
				Extraction:  "",
				Provenance:  "",
				Files:       nil,
				Step:        0,
				Changes:     nil,
				OutputTail:  "", State: "ran", ExitCode: &one, Findings: []report.Finding{{
					Fingerprint:      "",
					FingerprintBasis: "",
					Code:             "",
					RuleURL:          "",
					Gates:            false,
					Baselined:        false,
					Provenance:       "",
					Tool:             "tsc", Source: "tsc", Severity: "error", Reported: true, Shown: true, Kind: "issue",
					Message: "m", Location: report.Location{EndRow: 0, Col: 0, EndCol: 0, Unit: "", Chars: nil, Bytes: nil, Utf16: nil, Precision: "", Path: "odd\nname.ts", Row: 1},
				}},
			},
			want: "odd\\nname.ts:1: error tsc: m\n",
		},
		{
			name: "a failure without findings names its directory and ends with the tool's output",
			inv: report.Invocation{
				RedactedState:      0,
				RedactedExtraction: 0,

				ID:          "",
				TaskID:      "",
				Scope:       "",
				Granularity: "",
				Arity:       "",
				Coverage:    "",
				WholeUnit:   false,
				Success:     false,
				Duration:    0,
				Extraction:  "",
				Provenance:  "",
				Files:       nil,
				Step:        0,
				Changes:     nil,
				Dir:         "packages/api", State: "ran", ExitCode: &two, FailureKind: "exit",
				OutputTail: "noise\n\nerror TS5083: Cannot read file\n",
				Findings: []report.Finding{{
					Fingerprint:      "",
					FingerprintBasis: "",
					Code:             "",
					RuleURL:          "",
					Reported:         false,
					Gates:            false,
					Shown:            false,
					Baselined:        false,
					Location:         report.Location{Path: "", Row: 0, EndRow: 0, Col: 0, EndCol: 0, Unit: "", Chars: nil, Bytes: nil, Utf16: nil, Precision: ""},
					Provenance:       "",
					Tool:             "tsc", Source: "tsc", Severity: "error", Kind: "synthetic",
					Message: "tsc exited 2 without parsable findings",
				}},
			},
			want: "tsc [packages/api] exited 2 without parsable findings\n  │  noise\n  │  error TS5083: Cannot read file\n",
		},
		{
			name: "a process that could not start, after others of its task ran",
			inv: report.Invocation{
				RedactedState:      0,
				RedactedExtraction: 0,

				ID:          "",
				TaskID:      "",
				Dir:         "",
				Scope:       "",
				Granularity: "",
				Arity:       "",
				Coverage:    "",
				WholeUnit:   false,
				ExitCode:    nil,
				Success:     false,
				Duration:    0,
				Extraction:  "",
				Provenance:  "",
				Findings:    nil,
				Step:        0,
				Changes:     nil,
				State:       "setup-failed", FailureKind: "setup", Files: []report.FileResult{{
					RedactedState: 0,
					State:         "", Success: false, ExitCode: nil, Patch: "", Path: "b.ts",
				}},
				OutputTail: "stdin: permission denied",
			},
			want: "b.ts: tsc failed before it ran\n  │  stdin: permission denied\n",
		},
		{
			name: "a tool that failed with exit 0 and no finding",
			inv: report.Invocation{
				RedactedState:      0,
				RedactedExtraction: 0,
				ID:                 "", TaskID: "", Dir: "", Scope: "", Granularity: "", Arity: "", Coverage: "", WholeUnit: false, Success: false, Duration: 0, Extraction: "", Provenance: "", Files: nil, Findings: nil, Step: 0, Changes: nil, OutputTail: "", State: "ran", ExitCode: new(0), FailureKind: "exit",
			},
			want: "tsc failed without parsable findings\n",
		},
		{
			want: "",
			name: "a passed invocation prints nothing",
			inv: report.Invocation{
				RedactedState:      0,
				RedactedExtraction: 0,
				ID:                 "", TaskID: "", Dir: "", Scope: "", Granularity: "", Arity: "", Coverage: "", WholeUnit: false, FailureKind: "", Duration: 0, Extraction: "", Provenance: "", Files: nil, Findings: nil, Step: 0, Changes: nil, OutputTail: "", State: "ran", ExitCode: new(0), Success: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			var shown, hidden levelCounts
			baselined := 0
			agentInvocation(&b, tool, tt.inv, &shown, &hidden, &baselined)
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
	plan := &tooling.ExecutionPlan{
		ConfigName: "",
		FailOn:     "",
		Groups:     nil, Skipped: []tooling.SkippedTool{
			{Operation: "", ToolName: "trufflehog", Reason: tooling.SkipReasonConfig, Detail: "needs " + secret},
			{Operation: "", ToolName: "odd", Reason: tooling.SkipReasonConfig, Detail: "two\nlines"},
			{Operation: "", ToolName: "native", Reason: tooling.SkipReasonUnsupportedPlatform, Detail: "windows/arm64"},
		},
	}
	acc := report.NewAccumulator(report.Options{Root: "", Tools: nil, Apps: nil, FailOn: "", Parsers: nil, Secrets: nil})
	sc := &sharedContext{
		preparedTools:        false,
		parserPrewarmed:      false,
		bundledFixFindings:   nil,
		bundledFixChanges:    nil,
		bundledLintFindings:  nil,
		bundledLintErr:       nil,
		completedBundledLint: false,
		cfg:                  nil, rootPath: "", cwdPath: "", selection: tooling.Selection{Mode: 0, Dir: "", Paths: nil}, opts: Options{WidenTo: "", RequireCoverage: "", FailFast: nil, FailOn: "", Reports: nil, AllowPartial: false, AllDiagnostics: false, Annotations: "", Output: "", Baseline: nil}, files: nil, selectedTools: nil, explainLevel: "", planner: nil, projectCache: nil, executor: nil, binMgr: nil, timings: nil, parserMgr: nil, parseProblems: nil, ignoredFailOn: nil, nameWidth: 0, failOnSkip: false, platformSkipped: nil, narrowed: nil, failFast: false, afterFailedFix: false, summaries: nil, fileScoped: false, report: nil, annotator: nil, baselineMatcher: nil, placement: nil, startedAt: zerovalue_time.Time{}, ci: zerovalue_cienv.Info{Vendor: "", IsCI: false, IsPR: false, SHA: "", Ref: "", BaseRef: "", PRNumber: ""}, ciRuntime: zerovalue_cienv.Runtime{EventName: "", Workspace: "", StepSummaryPath: "", EventPath: ""}, annotations: annotationState{mode: "", export: "", why: "", recorded: false, regionOpen: false, executed: false}, secrets: &report.Secrets{},
	}
	sc.secrets.Add([]string{"DEPLOY_TOKEN=" + secret})
	out := captureStdout(t, func() {
		sc.printAgentOperation(agentOperation{cause: "", summary: opSummary{
			incomplete: false,
			unstarted:  false,
			op:         "", tools: 0, runs: 0, failed: 0, skipped: 0, cancelled: 0, partial: 0, durationMs: 0,
		}, note: "", op: config.OpLint, record: acc.BeginOperation("lint", plan, nil)})
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
	op := report.Operation{
		Name:            "",
		Ran:             false,
		Success:         false,
		Duration:        0,
		Skipped:         nil,
		Cancelled:       nil,
		ChangesObserved: false,
		ChangesReason:   "",
		ChangesDetail:   "",
		ChangesScope:    nil,
		Changes:         nil, Tools: []report.ToolRun{{
			RedactedReasons: nil,

			App:        report.AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""},
			Parser:     nil,
			FailOn:     "",
			GateActive: false,
			Category:   "",
			Complete:   false,
			Incomplete: nil, Name: "t", Invocations: []report.Invocation{{
				RedactedState:      0,
				RedactedExtraction: 0,

				ID:          "",
				TaskID:      "",
				Dir:         "",
				Scope:       "",
				Granularity: "",
				Arity:       "",
				Coverage:    "",
				WholeUnit:   false,
				Success:     false,
				Duration:    0,
				Extraction:  "",
				Provenance:  "",
				Files:       nil,
				Step:        0,
				Changes:     nil,
				State:       "ran", ExitCode: &code, FailureKind: "exit", OutputTail: "token: " + secret + "\n",
				Findings: []report.Finding{{Fingerprint: "", FingerprintBasis: "", Code: "", RuleURL: "", Reported: false, Gates: false, Shown: false, Baselined: false, Location: report.Location{Path: "", Row: 0, EndRow: 0, Col: 0, EndCol: 0, Unit: "", Chars: nil, Bytes: nil, Utf16: nil, Precision: ""}, Provenance: "", Tool: "t", Source: "t", Kind: "synthetic", Severity: "error", Message: "t exited 1 without parsable findings"}},
			}},
		}},
	}
	report.MaskAll(&op, []string{secret})
	var b strings.Builder
	var shown, hidden levelCounts
	baselined := 0
	agentInvocation(&b, op.Tools[0], op.Tools[0].Invocations[0], &shown, &hidden, &baselined)
	if strings.Contains(b.String(), "half-of-it") || !strings.Contains(b.String(), "token: ***") {
		t.Errorf("records =\n%s\nwant the secret masked whole", b.String())
	}
}
