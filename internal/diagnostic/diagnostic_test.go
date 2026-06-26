package diagnostic

import (
	"testing"

	"github.com/datamitsu/datamitsu/internal/parsermanager"
)

func TestResolve_FillsDefaultsForAbsentFields(t *testing.T) {
	// Only message present — every other field defaulted.
	d := Resolve(parsermanager.RawDiagnostic{Message: "boom"}, "dotenv_linter")
	if d.Message != "boom" {
		t.Errorf("message = %q", d.Message)
	}
	if d.Row != 1 || d.Col != 1 {
		t.Errorf("row/col = %d/%d, want 1/1", d.Row, d.Col)
	}
	if d.EndRow != 1 || d.EndCol != 1 {
		t.Errorf("end defaults to start, got %d/%d", d.EndRow, d.EndCol)
	}
	if d.Severity != SeverityWarning {
		t.Errorf("severity = %v, want fallback Warning", d.Severity)
	}
	if d.Source != "dotenv_linter" {
		t.Errorf("source = %q, want the tool name", d.Source)
	}
	if d.Code != "" {
		t.Errorf("code = %q, want empty", d.Code)
	}
}

func TestResolve_UsesProvidedFields(t *testing.T) {
	d := Resolve(parsermanager.RawDiagnostic{
		Message:  "x",
		Row:      new(uint32(3)),
		Col:      new(uint32(7)),
		EndRow:   new(uint32(3)),
		EndCol:   new(uint32(9)),
		Severity: new(uint8(SeverityError)),
		Code:     new("DL3008"),
	}, "hadolint")
	if d.Row != 3 || d.Col != 7 || d.EndRow != 3 || d.EndCol != 9 {
		t.Errorf("positions not preserved: %+v", d)
	}
	if d.Severity != SeverityError {
		t.Errorf("severity = %v, want Error", d.Severity)
	}
	if d.Code != "DL3008" {
		t.Errorf("code = %q", d.Code)
	}
}

func TestResolve_EndDefaultsToStartWhenOnlyStartGiven(t *testing.T) {
	d := Resolve(parsermanager.RawDiagnostic{Message: "m", Row: new(uint32(5)), Col: new(uint32(2))}, "t")
	if d.EndRow != 5 || d.EndCol != 2 {
		t.Errorf("end should default to start (5/2), got %d/%d", d.EndRow, d.EndCol)
	}
}

func TestResolve_ParserSourceOverridesToolName(t *testing.T) {
	// cue_fmt sets its own source; it wins over the dispatch tool name.
	d := Resolve(parsermanager.RawDiagnostic{Message: "m", Source: new("cue_fmt")}, "cue")
	if d.Source != "cue_fmt" {
		t.Errorf("source = %q, want parser-provided cue_fmt", d.Source)
	}
}

func TestResolve_OutOfRangeSeverityFallsBack(t *testing.T) {
	d := Resolve(parsermanager.RawDiagnostic{Message: "m", Severity: new(uint8(9))}, "t")
	if d.Severity != SeverityWarning {
		t.Errorf("out-of-range severity should fall back to Warning, got %v", d.Severity)
	}
}

func TestSeverity_String(t *testing.T) {
	cases := map[Severity]string{
		SeverityError: "error", SeverityWarning: "warning",
		SeverityInfo: "info", SeverityHint: "hint", Severity(0): "warning",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("Severity(%d).String() = %q, want %q", s, got, want)
		}
	}
}

func TestResolveAll(t *testing.T) {
	if got := ResolveAll(nil, "t"); got != nil {
		t.Errorf("ResolveAll(nil) = %v, want nil", got)
	}
	out := ResolveAll([]parsermanager.RawDiagnostic{{Message: "a"}, {Message: "b", Row: new(uint32(2))}}, "yamllint")
	if len(out) != 2 || out[0].Message != "a" || out[1].Row != 2 {
		t.Fatalf("unexpected: %+v", out)
	}
	if out[0].Source != "yamllint" {
		t.Errorf("source not propagated: %+v", out[0])
	}
}
