package diagnostic

import (
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/parsermanager"
)

func TestResolve_PositionContract(t *testing.T) {
	u := func(v uint32) *uint32 { return &v }
	cases := []struct {
		name                     string
		raw                      parsermanager.RawDiagnostic
		row, col, endRow, endCol int
	}{
		{"nil positions", parsermanager.RawDiagnostic{}, 1, 1, 1, 1},
		{"0 row", parsermanager.RawDiagnostic{Row: u(0), Col: u(4)}, 1, 4, 1, 4},
		{"0 col", parsermanager.RawDiagnostic{Row: u(3), Col: u(0)}, 3, 1, 3, 1},
		{"missing end", parsermanager.RawDiagnostic{Row: u(3), Col: u(5)}, 3, 5, 3, 5},
		{"end kept", parsermanager.RawDiagnostic{Row: u(3), Col: u(5), EndRow: u(4), EndCol: u(2)}, 3, 5, 4, 2},
		{"end column on the start row", parsermanager.RawDiagnostic{Row: u(3), Col: u(5), EndCol: u(9)}, 3, 5, 3, 9},
		{"end row before start", parsermanager.RawDiagnostic{Row: u(3), Col: u(5), EndRow: u(2), EndCol: u(9)}, 3, 5, 3, 5},
		{"end column before start", parsermanager.RawDiagnostic{Row: u(3), Col: u(5), EndRow: u(3), EndCol: u(4)}, 3, 5, 3, 5},
		{"end column only, before start", parsermanager.RawDiagnostic{Row: u(3), Col: u(5), EndCol: u(2)}, 3, 5, 3, 5},
		{"end row only", parsermanager.RawDiagnostic{Row: u(3), Col: u(5), EndRow: u(4)}, 3, 5, 3, 5},
		{"0 end", parsermanager.RawDiagnostic{Row: u(1), Col: u(1), EndRow: u(0), EndCol: u(0)}, 1, 1, 1, 1},
		{"0 start with an end", parsermanager.RawDiagnostic{Row: u(0), Col: u(0), EndRow: u(1), EndCol: u(3)}, 1, 1, 1, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.raw.Message = "m"
			d := Resolve(c.raw, "t")
			if d.Row != c.row || d.Col != c.col || d.EndRow != c.endRow || d.EndCol != c.endCol {
				t.Errorf("got %d:%d-%d:%d, want %d:%d-%d:%d",
					d.Row, d.Col, d.EndRow, d.EndCol, c.row, c.col, c.endRow, c.endCol)
			}
		})
	}
}

func TestAbsPath(t *testing.T) {
	root := string(filepath.Separator) + "repo"
	dir := filepath.Join(root, "pkg")
	cases := []struct {
		name, file, want string
	}{
		{"relative joins the working directory", "src/a.ts", filepath.Join(dir, "src", "a.ts")},
		{"dot-relative is cleaned", "./a.ts", filepath.Join(dir, "a.ts")},
		{"climbing out stays climbed out", "../b/a.ts", filepath.Join(root, "b", "a.ts")},
		{"absolute is cleaned", filepath.Join(dir, "src") + string(filepath.Separator) + ".." + string(filepath.Separator) + "a.ts", filepath.Join(dir, "a.ts")},
		{"empty stays empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := AbsPath(c.file, dir); got != c.want {
				t.Errorf("AbsPath(%q, %q) = %q, want %q", c.file, dir, got, c.want)
			}
		})
	}
}

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

// A batch parser (eslint) names the file per diagnostic; the core must keep it,
// since the executor has no single file to stamp on a many-file run.
func TestResolve_CarriesParserReportedFile(t *testing.T) {
	d := Resolve(parsermanager.RawDiagnostic{Message: "m", File: new("src/a.ts")}, "eslint")
	if d.File != "src/a.ts" {
		t.Errorf("file = %q, want the parser-reported path", d.File)
	}
	// Absent stays empty so the executor's own stamping still applies.
	if got := Resolve(parsermanager.RawDiagnostic{Message: "m"}, "eslint"); got.File != "" {
		t.Errorf("file = %q, want empty when the parser reported none", got.File)
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
