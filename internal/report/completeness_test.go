package report

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/tooling"

	zerovalue_time "time"
)

func ran(extraction tooling.Extraction) Invocation {
	return Invocation{
		ID:                 "",
		TaskID:             "",
		Dir:                "",
		Scope:              "",
		Granularity:        "",
		Arity:              "",
		WholeUnit:          false,
		RedactedState:      0,
		ExitCode:           nil,
		Success:            false,
		FailureKind:        "",
		Duration:           0,
		RedactedExtraction: 0,
		Provenance:         "",
		Files:              nil,
		Findings:           nil,
		Step:               0,
		Changes:            nil,
		OutputTail:         "",
		State:              "ran", Coverage: "complete", Extraction: string(extraction),
	}
}

func TestToolReasons(t *testing.T) {
	all := Selection{
		Dir:           "",
		Paths:         nil,
		Tools:         nil,
		FileScoped:    false,
		ExcludedTools: nil,
		Mode:          "all",
	}
	tests := []struct {
		name        string
		sel         Selection
		invocations []Invocation
		want        []Reason
	}{
		{
			name: "parsed clean and with findings", sel: all,
			invocations: []Invocation{ran(tooling.ExtractionParsedClean), ran(tooling.ExtractionParsedFindings)}, want: []Reason{},
		},
		{
			name: "a narrowed selection", sel: Selection{
				Dir:           "",
				Paths:         nil,
				Tools:         nil,
				FileScoped:    false,
				ExcludedTools: nil,
				Mode:          "paths",
			},
			invocations: []Invocation{ran(tooling.ExtractionParsedClean)}, want: []Reason{ReasonNarrowedSelection},
		},
		{
			name: "a task over part of its unit", sel: all,
			invocations: []Invocation{{
				ID:                 "",
				TaskID:             "",
				Dir:                "",
				Scope:              "",
				Granularity:        "",
				Arity:              "",
				WholeUnit:          false,
				RedactedState:      0,
				ExitCode:           nil,
				Success:            false,
				FailureKind:        "",
				Duration:           0,
				RedactedExtraction: 0,
				Provenance:         "",
				Files:              nil,
				Findings:           nil,
				Step:               0,
				Changes:            nil,
				OutputTail:         "",
				State:              "ran", Coverage: "partial", Extraction: "parsed-clean",
			}}, want: []Reason{ReasonPartialUnit},
		},
		{
			name: "no parser", sel: all,
			invocations: []Invocation{ran(tooling.ExtractionNone)}, want: []Reason{ReasonNoExtraction},
		},
		{
			name: "every extraction failure", sel: all,
			invocations: []Invocation{ran(tooling.ExtractionParseFailed), ran(tooling.ExtractionParserUnavailable), ran(tooling.ExtractionTruncated)},
			want:        []Reason{ReasonParseFailed, ReasonParserUnavailable, ReasonTruncated},
		},
		{
			name: "a failed process its parser found nothing in", sel: all,
			invocations: []Invocation{
				{
					ID:                 "",
					TaskID:             "",
					Dir:                "",
					Scope:              "",
					Granularity:        "",
					Arity:              "",
					WholeUnit:          false,
					RedactedState:      0,
					Success:            false,
					FailureKind:        "",
					Duration:           0,
					RedactedExtraction: 0,
					Provenance:         "",
					Files:              nil,
					Findings:           nil,
					Step:               0,
					Changes:            nil,
					OutputTail:         "",
					State:              "ran", Coverage: "complete", Extraction: "parsed-clean", ExitCode: new(2),
				},
				{
					ID:                 "",
					TaskID:             "",
					Dir:                "",
					Scope:              "",
					Granularity:        "",
					Arity:              "",
					WholeUnit:          false,
					RedactedState:      0,
					Success:            false,
					FailureKind:        "",
					Duration:           0,
					RedactedExtraction: 0,
					Provenance:         "",
					Files:              nil,
					Findings:           nil,
					Step:               0,
					Changes:            nil,
					OutputTail:         "",
					State:              "ran", Coverage: "complete", Extraction: "parsed-clean", ExitCode: new(0),
				},
				{
					ID:                 "",
					TaskID:             "",
					Dir:                "",
					Scope:              "",
					Granularity:        "",
					Arity:              "",
					WholeUnit:          false,
					RedactedState:      0,
					Success:            false,
					FailureKind:        "",
					Duration:           0,
					RedactedExtraction: 0,
					Provenance:         "",
					Files:              nil,
					Findings:           nil,
					Step:               0,
					Changes:            nil,
					OutputTail:         "",
					State:              "ran", Coverage: "complete", Extraction: "parsed-findings", ExitCode: new(1),
				},
			},
			want: []Reason{ReasonFailedWithoutFindings},
		},
		{
			want: nil,

			name: "a parsed-clean cache hit", sel: all,
			invocations: []Invocation{{
				ID:                 "",
				TaskID:             "",
				Dir:                "",
				Scope:              "",
				Granularity:        "",
				Arity:              "",
				Coverage:           "",
				WholeUnit:          false,
				RedactedState:      0,
				ExitCode:           nil,
				Success:            false,
				FailureKind:        "",
				Duration:           0,
				RedactedExtraction: 0,
				Provenance:         "",
				Files:              nil,
				Findings:           nil,
				Step:               0,
				Changes:            nil,
				OutputTail:         "",
				State:              "cached", Extraction: "parsed-clean",
			}, {
				ID:                 "",
				TaskID:             "",
				Dir:                "",
				Scope:              "",
				Granularity:        "",
				Arity:              "",
				Coverage:           "",
				WholeUnit:          false,
				RedactedState:      0,
				ExitCode:           nil,
				Success:            false,
				FailureKind:        "",
				Duration:           0,
				RedactedExtraction: 0,
				Provenance:         "",
				Files:              nil,
				Findings:           nil,
				Step:               0,
				Changes:            nil,
				OutputTail:         "",
				State:              "verdict-hit", Extraction: "parsed-clean",
			}},
		},
		{
			name: "a cache hit of a tool without a parser", sel: all,
			invocations: []Invocation{{
				ID:                 "",
				TaskID:             "",
				Dir:                "",
				Scope:              "",
				Granularity:        "",
				Arity:              "",
				Coverage:           "",
				WholeUnit:          false,
				RedactedState:      0,
				ExitCode:           nil,
				Success:            false,
				FailureKind:        "",
				Duration:           0,
				Extraction:         "",
				RedactedExtraction: 0,
				Provenance:         "",
				Files:              nil,
				Findings:           nil,
				Step:               0,
				Changes:            nil,
				OutputTail:         "",
				State:              "cached",
			}}, want: []Reason{ReasonUnparsedCacheHit},
		},
		{
			name: "tasks that did not run to the end", sel: all,
			invocations: []Invocation{{
				ID:                 "",
				TaskID:             "",
				Dir:                "",
				Scope:              "",
				Granularity:        "",
				Arity:              "",
				Coverage:           "",
				WholeUnit:          false,
				RedactedState:      0,
				ExitCode:           nil,
				Success:            false,
				FailureKind:        "",
				Duration:           0,
				Extraction:         "",
				RedactedExtraction: 0,
				Provenance:         "",
				Files:              nil,
				Findings:           nil,
				Step:               0,
				Changes:            nil,
				OutputTail:         "",
				State:              "cancelled",
			}, {
				ID:                 "",
				TaskID:             "",
				Dir:                "",
				Scope:              "",
				Granularity:        "",
				Arity:              "",
				Coverage:           "",
				WholeUnit:          false,
				RedactedState:      0,
				ExitCode:           nil,
				Success:            false,
				FailureKind:        "",
				Duration:           0,
				Extraction:         "",
				RedactedExtraction: 0,
				Provenance:         "",
				Files:              nil,
				Findings:           nil,
				Step:               0,
				Changes:            nil,
				OutputTail:         "",
				State:              "not-started",
			}, {
				ID:                 "",
				TaskID:             "",
				Dir:                "",
				Scope:              "",
				Granularity:        "",
				Arity:              "",
				Coverage:           "",
				WholeUnit:          false,
				RedactedState:      0,
				ExitCode:           nil,
				Success:            false,
				FailureKind:        "",
				Duration:           0,
				Extraction:         "",
				RedactedExtraction: 0,
				Provenance:         "",
				Files:              nil,
				Findings:           nil,
				Step:               0,
				Changes:            nil,
				OutputTail:         "",
				State:              "setup-failed",
			}},
			want: []Reason{ReasonCancelled, ReasonNotStarted, ReasonSetupFailed},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toolReasons(&ToolRun{
				Name:            "",
				App:             AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""},
				Parser:          nil,
				FailOn:          "",
				GateActive:      false,
				Category:        "",
				Complete:        false,
				Incomplete:      nil,
				RedactedReasons: nil,
				mayHoldSecrets:  false,
				Invocations:     tt.invocations,
			}, tt.sel, true)
			if !slices.Equal(got, tt.want) {
				t.Errorf("toolReasons() = %v, want %v", got, tt.want)
			}
		})
	}
}

// A document an earlier build wrote called a tool complete although one of
// its processes failed while its parser found nothing; read back, it is not.
func TestRevise(t *testing.T) {
	two := 2
	run := &Run{
		Fingerprint:     "",
		Datamitsu:       Producer{Version: "", Configuration: ""},
		StartedAt:       zerovalue_time.Time{},
		EndedAt:         zerovalue_time.Time{},
		FailFast:        false,
		Incomplete:      nil,
		RedactedReasons: nil,
		Exports:         nil,
		CI:              CIEnvironment{Vendor: "", SHA: "", Ref: "", BaseRef: "", PRNumber: ""},
		Complete:        true, Selection: Selection{
			Dir:           "",
			Paths:         nil,
			Tools:         nil,
			FileScoped:    false,
			ExcludedTools: nil,
			Mode:          modeAll,
		}, Operations: []Operation{{
			Success:         false,
			Duration:        0,
			Skipped:         nil,
			Cancelled:       nil,
			ChangesObserved: false,
			ChangesReason:   "",
			ChangesDetail:   "",
			ChangesScope:    nil,
			Changes:         nil,
			Name:            "lint", Ran: true, Tools: []ToolRun{
				{
					App:             AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""},
					Parser:          nil,
					FailOn:          "",
					GateActive:      false,
					Category:        "",
					RedactedReasons: nil,
					mayHoldSecrets:  false,
					Name:            "a", Complete: true, Incomplete: []Reason{}, Invocations: []Invocation{{
						ID:                 "",
						TaskID:             "",
						Dir:                "",
						Scope:              "",
						Granularity:        "",
						Arity:              "",
						Coverage:           "",
						WholeUnit:          false,
						RedactedState:      0,
						Success:            false,
						FailureKind:        "",
						Duration:           0,
						RedactedExtraction: 0,
						Provenance:         "",
						Files:              nil,
						Findings:           nil,
						Step:               0,
						Changes:            nil,
						OutputTail:         "",
						State:              "ran", Extraction: "parsed-clean", ExitCode: &two,
					}},
				},
				{
					App:             AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""},
					Parser:          nil,
					FailOn:          "",
					GateActive:      false,
					Category:        "",
					RedactedReasons: nil,
					mayHoldSecrets:  false,
					Name:            "b", Complete: true, Incomplete: []Reason{}, Invocations: []Invocation{{
						ID:                 "",
						TaskID:             "",
						Dir:                "",
						Scope:              "",
						Granularity:        "",
						Arity:              "",
						Coverage:           "",
						WholeUnit:          false,
						RedactedState:      0,
						Success:            false,
						FailureKind:        "",
						Duration:           0,
						RedactedExtraction: 0,
						Provenance:         "",
						Files:              nil,
						Findings:           nil,
						Step:               0,
						Changes:            nil,
						OutputTail:         "",
						State:              "ran", Extraction: "parsed-findings", ExitCode: &two,
					}},
				},
			},
		}},
	}
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
	clean := []Invocation{ran(tooling.ExtractionParsedClean)}
	tests := []struct {
		name         string
		run          Run
		wantComplete bool
		wantRun      []Reason
		wantTool     []Reason
	}{
		{
			name: "complete",
			run: Run{
				Fingerprint:     "",
				Datamitsu:       Producer{Version: "", Configuration: ""},
				StartedAt:       zerovalue_time.Time{},
				EndedAt:         zerovalue_time.Time{},
				FailFast:        false,
				Complete:        false,
				Incomplete:      nil,
				RedactedReasons: nil,
				Exports:         nil,
				CI:              CIEnvironment{Vendor: "", SHA: "", Ref: "", BaseRef: "", PRNumber: ""},
				Selection: Selection{
					Dir:           "",
					Paths:         nil,
					Tools:         nil,
					FileScoped:    false,
					ExcludedTools: nil,
					Mode:          "all",
				}, Operations: []Operation{{
					Name:            "",
					Success:         false,
					Duration:        0,
					Skipped:         nil,
					Cancelled:       nil,
					ChangesObserved: false,
					ChangesReason:   "",
					ChangesDetail:   "",
					ChangesScope:    nil,
					Changes:         nil,
					Ran:             true, Tools: []ToolRun{{
						App:             AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""},
						Parser:          nil,
						FailOn:          "",
						GateActive:      false,
						Category:        "",
						Complete:        false,
						Incomplete:      nil,
						RedactedReasons: nil,
						mayHoldSecrets:  false,
						Name:            "a", Invocations: clean,
					}},
				}},
			},
			wantComplete: true, wantRun: []Reason{}, wantTool: []Reason{},
		},
		{
			wantComplete: false,

			name: "a tools filter",
			run: Run{
				Fingerprint:     "",
				Datamitsu:       Producer{Version: "", Configuration: ""},
				StartedAt:       zerovalue_time.Time{},
				EndedAt:         zerovalue_time.Time{},
				FailFast:        false,
				Complete:        false,
				Incomplete:      nil,
				RedactedReasons: nil,
				Exports:         nil,
				CI:              CIEnvironment{Vendor: "", SHA: "", Ref: "", BaseRef: "", PRNumber: ""},
				Selection: Selection{
					Dir:           "",
					Paths:         nil,
					FileScoped:    false,
					ExcludedTools: nil,
					Mode:          "all", Tools: []string{"a"},
				}, Operations: []Operation{{
					Name:            "",
					Success:         false,
					Duration:        0,
					Skipped:         nil,
					Cancelled:       nil,
					ChangesObserved: false,
					ChangesReason:   "",
					ChangesDetail:   "",
					ChangesScope:    nil,
					Changes:         nil,
					Ran:             true, Tools: []ToolRun{{
						App:             AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""},
						Parser:          nil,
						FailOn:          "",
						GateActive:      false,
						Category:        "",
						Complete:        false,
						Incomplete:      nil,
						RedactedReasons: nil,
						mayHoldSecrets:  false,
						Name:            "a", Invocations: clean,
					}},
				}},
			},
			wantRun: []Reason{ReasonToolsFilter}, wantTool: []Reason{},
		},
		{
			wantComplete: false,

			name: "an operation that did not run and a tool that could not narrow",
			run: Run{
				Fingerprint:     "",
				Datamitsu:       Producer{Version: "", Configuration: ""},
				StartedAt:       zerovalue_time.Time{},
				EndedAt:         zerovalue_time.Time{},
				FailFast:        false,
				Complete:        false,
				Incomplete:      nil,
				RedactedReasons: nil,
				Exports:         nil,
				CI:              CIEnvironment{Vendor: "", SHA: "", Ref: "", BaseRef: "", PRNumber: ""},
				Selection: Selection{
					Dir:           "",
					Paths:         nil,
					Tools:         nil,
					FileScoped:    false,
					ExcludedTools: nil,
					Mode:          "all",
				}, Operations: []Operation{
					{
						Name:            "",
						Success:         false,
						Duration:        0,
						Cancelled:       nil,
						ChangesObserved: false,
						ChangesReason:   "",
						ChangesDetail:   "",
						ChangesScope:    nil,
						Changes:         nil,
						Ran:             true, Skipped: []Skip{{
							Detail: "",
							Tool:   "b", Reason: "not-narrowable",
						}, {
							Detail: "",
							Tool:   "c", Reason: "config",
						}}, Tools: []ToolRun{{
							App:             AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""},
							Parser:          nil,
							FailOn:          "",
							GateActive:      false,
							Category:        "",
							Complete:        false,
							Incomplete:      nil,
							RedactedReasons: nil,
							mayHoldSecrets:  false,
							Name:            "a", Invocations: clean,
						}},
					},
					{
						Ran:             false,
						Success:         false,
						Duration:        0,
						Skipped:         nil,
						Cancelled:       nil,
						Tools:           nil,
						ChangesObserved: false,
						ChangesReason:   "",
						ChangesDetail:   "",
						ChangesScope:    nil,
						Changes:         nil,
						Name:            "lint",
					},
				},
			},
			wantRun: []Reason{ReasonNotNarrowable, ReasonOperationSkipped}, wantTool: []Reason{},
		},
		{
			wantComplete: false,
			wantTool:     nil,

			name: "a narrowed run that matched no tool",
			run: Run{
				Fingerprint:     "",
				Datamitsu:       Producer{Version: "", Configuration: ""},
				StartedAt:       zerovalue_time.Time{},
				EndedAt:         zerovalue_time.Time{},
				FailFast:        false,
				Complete:        false,
				Incomplete:      nil,
				RedactedReasons: nil,
				Exports:         nil,
				CI:              CIEnvironment{Vendor: "", SHA: "", Ref: "", BaseRef: "", PRNumber: ""},
				Selection: Selection{
					Dir:           "",
					Paths:         nil,
					Tools:         nil,
					FileScoped:    false,
					ExcludedTools: nil,
					Mode:          "subtree",
				}, Operations: []Operation{{
					Name:            "",
					Success:         false,
					Duration:        0,
					Skipped:         nil,
					Cancelled:       nil,
					Tools:           nil,
					ChangesObserved: false,
					ChangesReason:   "",
					ChangesDetail:   "",
					ChangesScope:    nil,
					Changes:         nil,
					Ran:             true,
				}},
			},
			wantRun: []Reason{ReasonNarrowedSelection},
		},
		{
			wantComplete: false,

			name: "a platform skip",
			run: Run{
				Fingerprint:     "",
				Datamitsu:       Producer{Version: "", Configuration: ""},
				StartedAt:       zerovalue_time.Time{},
				EndedAt:         zerovalue_time.Time{},
				FailFast:        false,
				Complete:        false,
				Incomplete:      nil,
				RedactedReasons: nil,
				Exports:         nil,
				CI:              CIEnvironment{Vendor: "", SHA: "", Ref: "", BaseRef: "", PRNumber: ""},
				Selection: Selection{
					Dir:           "",
					Paths:         nil,
					Tools:         nil,
					FileScoped:    false,
					ExcludedTools: nil,
					Mode:          "all",
				}, Operations: []Operation{{
					Name:            "",
					Success:         false,
					Duration:        0,
					Skipped:         nil,
					Cancelled:       nil,
					ChangesObserved: false,
					ChangesReason:   "",
					ChangesDetail:   "",
					ChangesScope:    nil,
					Changes:         nil,
					Ran:             true, Tools: []ToolRun{{
						App:             AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""},
						Parser:          nil,
						FailOn:          "",
						GateActive:      false,
						Category:        "",
						Complete:        false,
						RedactedReasons: nil,
						Invocations:     nil,
						mayHoldSecrets:  false,
						Name:            "b", Incomplete: []Reason{ReasonPlatformSkip},
					}},
				}},
			},
			wantRun: []Reason{}, wantTool: []Reason{ReasonPlatformSkip},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := tt.run
			judge(&run)
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
		Name:         "",
		ProjectTypes: nil,
		Skip:         false,
		SkipReason:   "",

		OutputParser: &config.OutputParser{Module: "m", Parser: "p"},
		Operations: map[config.OperationType]config.ToolOperation{
			config.OpFix: {
				App:               "",
				Args:              nil,
				Scope:             "",
				Arity:             "",
				Granularity:       "",
				Globs:             nil,
				ExcludeGlobs:      nil,
				Priority:          0,
				Cache:             nil,
				InvalidateOn:      nil,
				Env:               nil,
				InheritEnv:        nil,
				FailOn:            "",
				Input:             "",
				LSP:               nil,
				ManagedConfigRefs: nil,
				Output:            config.ToolOutputStdout,
			},
			config.OpLint: {
				App:               "",
				Args:              nil,
				Scope:             "",
				Arity:             "",
				Granularity:       "",
				Globs:             nil,
				ExcludeGlobs:      nil,
				Priority:          0,
				Cache:             nil,
				InvalidateOn:      nil,
				Env:               nil,
				InheritEnv:        nil,
				FailOn:            "",
				Input:             "",
				LSP:               nil,
				Output:            "",
				ManagedConfigRefs: nil,
			},
		},
	}}
	cleanCache := []Invocation{{
		ID:                 "",
		TaskID:             "",
		Dir:                "",
		Scope:              "",
		Granularity:        "",
		Arity:              "",
		Coverage:           "",
		WholeUnit:          false,
		RedactedState:      0,
		ExitCode:           nil,
		Success:            false,
		FailureKind:        "",
		Duration:           0,
		RedactedExtraction: 0,
		Provenance:         "",
		Files:              nil,
		Findings:           nil,
		Step:               0,
		Changes:            nil,
		OutputTail:         "",
		State:              "cached", Extraction: "parsed-clean",
	}}
	run := Run{
		Fingerprint:     "",
		Datamitsu:       Producer{Version: "", Configuration: ""},
		StartedAt:       zerovalue_time.Time{},
		EndedAt:         zerovalue_time.Time{},
		FailFast:        false,
		Complete:        false,
		Incomplete:      nil,
		RedactedReasons: nil,
		Exports:         nil,
		CI:              CIEnvironment{Vendor: "", SHA: "", Ref: "", BaseRef: "", PRNumber: ""},
		Selection: Selection{
			Dir:           "",
			Paths:         nil,
			Tools:         nil,
			FileScoped:    false,
			ExcludedTools: nil,
			Mode:          "all",
		}, Operations: []Operation{
			{
				Success:         false,
				Duration:        0,
				Skipped:         nil,
				Cancelled:       nil,
				ChangesObserved: false,
				ChangesReason:   "",
				ChangesDetail:   "",
				ChangesScope:    nil,
				Changes:         nil,
				Name:            "fix", Ran: true, Tools: []ToolRun{{
					App:             AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""},
					Parser:          nil,
					FailOn:          "",
					GateActive:      false,
					Category:        "",
					Complete:        false,
					Incomplete:      nil,
					RedactedReasons: nil,
					mayHoldSecrets:  false,
					Name:            "fmt", Invocations: []Invocation{{
						ID:                 "",
						TaskID:             "",
						Dir:                "",
						Scope:              "",
						Granularity:        "",
						Arity:              "",
						Coverage:           "",
						WholeUnit:          false,
						RedactedState:      0,
						ExitCode:           nil,
						Success:            false,
						FailureKind:        "",
						Duration:           0,
						RedactedExtraction: 0,
						Provenance:         "",
						Files:              nil,
						Findings:           nil,
						Step:               0,
						Changes:            nil,
						OutputTail:         "",
						State:              "cached", Extraction: "none",
					}},
				}},
			},
			{
				Success:         false,
				Duration:        0,
				Skipped:         nil,
				Cancelled:       nil,
				ChangesObserved: false,
				ChangesReason:   "",
				ChangesDetail:   "",
				ChangesScope:    nil,
				Changes:         nil,
				Name:            "lint", Ran: true, Tools: []ToolRun{{
					App:             AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""},
					Parser:          nil,
					FailOn:          "",
					GateActive:      false,
					Category:        "",
					Complete:        false,
					Incomplete:      nil,
					RedactedReasons: nil,
					mayHoldSecrets:  false,
					Name:            "fmt", Invocations: cleanCache,
				}},
			},
		},
	}
	judge(&run)
	if fix := run.Operations[0].Tools[0]; !reflect.DeepEqual(fix.Incomplete, []Reason{ReasonUnparsedCacheHit}) {
		t.Errorf("the formatter's fix = %v, want unparsed-cache-hit", fix.Incomplete)
	}
	if lint := run.Operations[1].Tools[0]; !lint.Complete {
		t.Errorf("the formatter's lint = %v, want complete", lint.Incomplete)
	}

	task := perFileTask("fmt::1", "a")
	task.Tool = tools["fmt"]
	task.OpConfig.Output = config.ToolOutputStdout
	result := tooling.ExecutionResult{
		ToolName:         "",
		Output:           "",
		Error:            nil,
		Duration:         0,
		Command:          "",
		ExitCode:         0,
		WorkingDir:       "",
		RelativeDir:      "",
		Scope:            "",
		Batch:            false,
		Cancelled:        false,
		FailureReason:    0,
		StartedAt:        zerovalue_time.Time{},
		EndedAt:          zerovalue_time.Time{},
		FilesNotRun:      0,
		UnparsedFailures: nil,
		CapturedStdout:   "",
		Diagnostics:      nil,
		Processes:        nil,
		ParseFailed:      false,
		FailOn:           "",
		Files:            nil,
		WholeUnit:        false,
		UnitDir:          "",

		TaskID: task.ID, Success: true, Cached: true,
		FileResults: []tooling.FileResult{{
			ProcessID: "",
			ExitCode:  nil,
			Edits:     nil,
			Patch:     "",
			File:      abs("a"), State: tooling.FileCached, Success: true,
		}},
	}
	if inv := NewAccumulator(Options{
		Tools:   nil,
		Apps:    nil,
		FailOn:  "",
		Parsers: nil,
		Secrets: nil,
		Root:    root,
	}).invocations(task, &result, nil, &ToolRun{
		Name:            "",
		App:             AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""},
		Parser:          nil,
		FailOn:          "",
		GateActive:      false,
		Category:        "",
		Complete:        false,
		Incomplete:      nil,
		RedactedReasons: nil,
		Invocations:     nil,
		mayHoldSecrets:  false,
	})[0]; inv.Extraction != "none" {
		t.Errorf("a stdout formatter's cache hit claims extraction %s", inv.Extraction)
	}
}

func TestExcludedTools(t *testing.T) {
	tools := config.MapOfTools{
		"a": {
			Name:         "",
			ProjectTypes: nil,
			Skip:         false,
			SkipReason:   "",
			OutputParser: nil,
			Operations: map[config.OperationType]config.ToolOperation{config.OpLint: {
				App:               "",
				Args:              nil,
				Scope:             "",
				Arity:             "",
				Granularity:       "",
				Globs:             nil,
				ExcludeGlobs:      nil,
				Priority:          0,
				Cache:             nil,
				InvalidateOn:      nil,
				Env:               nil,
				InheritEnv:        nil,
				FailOn:            "",
				Input:             "",
				LSP:               nil,
				Output:            "",
				ManagedConfigRefs: nil,
			}},
		},
		"b": {
			Name:         "",
			ProjectTypes: nil,
			Skip:         false,
			SkipReason:   "",
			OutputParser: nil,
			Operations: map[config.OperationType]config.ToolOperation{config.OpLint: {
				App:               "",
				Args:              nil,
				Scope:             "",
				Arity:             "",
				Granularity:       "",
				Globs:             nil,
				ExcludeGlobs:      nil,
				Priority:          0,
				Cache:             nil,
				InvalidateOn:      nil,
				Env:               nil,
				InheritEnv:        nil,
				FailOn:            "",
				Input:             "",
				LSP:               nil,
				Output:            "",
				ManagedConfigRefs: nil,
			}},
		},
		"fixer": {
			Name:         "",
			ProjectTypes: nil,
			Skip:         false,
			SkipReason:   "",
			OutputParser: nil,
			Operations: map[config.OperationType]config.ToolOperation{config.OpFix: {
				App:               "",
				Args:              nil,
				Scope:             "",
				Arity:             "",
				Granularity:       "",
				Globs:             nil,
				ExcludeGlobs:      nil,
				Priority:          0,
				Cache:             nil,
				InvalidateOn:      nil,
				Env:               nil,
				InheritEnv:        nil,
				FailOn:            "",
				Input:             "",
				LSP:               nil,
				Output:            "",
				ManagedConfigRefs: nil,
			}},
		},
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
	plan := &tooling.ExecutionPlan{
		ConfigName: "",
		FailOn:     "",
		Groups:     nil,
		Skipped: []tooling.SkippedTool{
			{
				Operation: "",
				ToolName:  "hadolint", Reason: tooling.SkipReasonUnsupportedPlatform, Detail: "windows/arm64",
			},
			{
				Operation: "",
				Detail:    "",
				ToolName:  "off", Reason: tooling.SkipReasonConfig,
			},
		},
	}
	acc := NewAccumulator(testOptions())
	acc.BeginOperation("lint", plan, nil).End(true, 0)
	run := acc.Build(BuildInfo{
		Version:       "",
		Configuration: "",
		StartedAt:     zerovalue_time.Time{},
		EndedAt:       zerovalue_time.Time{},
		FailFast:      false,
		Exports:       nil,
		CI:            CIEnvironment{Vendor: "", SHA: "", Ref: "", BaseRef: "", PRNumber: ""},
		Selection: Selection{
			Dir:           "",
			Paths:         nil,
			Tools:         nil,
			FileScoped:    false,
			ExcludedTools: nil,
			Mode:          "all",
		},
	})
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
