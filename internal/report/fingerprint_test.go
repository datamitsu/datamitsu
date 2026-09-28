package report

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/parsermanager"
	"github.com/datamitsu/datamitsu/internal/textpos"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

// The golden vectors were computed outside Go (Python's hashlib) from the
// input the fingerprint is defined by: a change to any of them is a change to
// every alert a code-scanning service holds.
func TestFingerprintVectors(t *testing.T) {
	const eLine = "b2146372fa238a8d0bf45dccd5495f17c43986ebf866a127dd68221f5d7c20af"
	if got := LineHash([]byte("const café = 1;  ")); got != eLine {
		t.Errorf("LineHash of a line with é and trailing blanks = %s, want %s", got, eLine)
	}
	const twoLine = "316f8b8bd816d7c5c2c35d699eaeae5f99307e5f6d6ef31e83df1e16ee9da1ea"
	tests := []struct {
		name                          string
		tool, code, relPath, lineHash string
		ordinal                       int
		want                          string
	}{
		{
			"a line with é", "eslint", "no-unused-vars", "src/a.js", eLine, 0,
			"782d56fb252d612d013fd522fde6a5a8d4493f0dbe4113f391291cedde45da5c",
		},
		{
			"the first of two findings on one line", "eslint", "eqeqeq", "src/b.js", twoLine, 0,
			"8d08ad593059cec87a6c97ce43655d04b1bee8102810cd99952dd606b26bb618",
		},
		{
			"the second of two findings on one line", "eslint", "eqeqeq", "src/b.js", twoLine, 1,
			"dad1b7e3bcb9d02d57c352b1de167cfb3bb7286b26ab6fca66812abd15559155",
		},
		{
			"a deleted file", "hadolint", "DL3006", "gone/Dockerfile", RowHash(12), 0,
			"243067f037e64756174f040f8142559b790e4173d35cbdf7ca3c5fb64ccd66cf",
		},
		{
			"a finding without a file", "golangci-lint", "", "", "", 0,
			"3389a70737766fc5b3754e0cf170e1a6e0c6c9e0cf1755ad727010293798ad27",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Fingerprint(tt.tool, tt.code, tt.relPath, tt.lineHash, tt.ordinal); got != tt.want {
				t.Errorf("Fingerprint() = %s, want %s", got, tt.want)
			}
		})
	}
	if twoLine != LineHash([]byte("let x = y == z;")) {
		t.Error("the two-findings vector was computed from another line")
	}
}

// annotated anchors one process's findings against files under a temporary
// root and returns them.
func annotated(t *testing.T, root string, tool string, unit string, ds ...diagnostic.Diagnostic) []diagnostic.Diagnostic {
	t.Helper()
	facts := func(string, string) (parsermanager.ParserFacts, bool) {
		return parsermanager.ParserFacts{Tool: parsermanager.ToolCapability{ColumnUnit: unit}}, true
	}
	task := tooling.Task{ToolName: tool, Tool: config.Tool{OutputParser: &config.OutputParser{Module: "m", Parser: tool}}}
	proc := tooling.ProcessResult{Diagnostics: ds, ParserModule: "m"}
	NewAnnotator(root, facts).Annotate(task, &proc)
	return proc.Diagnostics
}

// The fallback's findings are not counted in the declared parser's unit: a
// format carries whatever unit the tool that printed it counts in.
func TestTheFallbacksColumnsHaveNoUnit(t *testing.T) {
	root := t.TempDir()
	path := writeFile(t, root, "src/a.js", "const café = 1;\n")
	facts := func(string, string) (parsermanager.ParserFacts, bool) {
		return parsermanager.ParserFacts{Tool: parsermanager.ToolCapability{ColumnUnit: "utf-16"}}, true
	}
	task := tooling.Task{ToolName: "eslint", Tool: config.Tool{OutputParser: &config.OutputParser{Module: "m", Parser: "eslint"}}}
	proc := tooling.ProcessResult{
		ParserModule: tooling.EmbeddedParserModule, Provenance: "fallback:gcc",
		Diagnostics: []diagnostic.Diagnostic{{File: path, Row: 1, EndRow: 1, Col: 7, EndCol: 11, Message: "m"}},
	}
	NewAnnotator(root, facts).Annotate(task, &proc)
	if got := proc.Diagnostics[0].Anchor; got.Precision != textpos.Unknown || got.Chars != nil {
		t.Errorf("anchor = %+v, want no columns of an unknown unit on a non-ASCII line", got)
	}
}

func writeFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The fingerprint of a finding on a line with é is the golden vector, whether
// the path is spelled with this platform's separator or not.
func TestAnnotateMatchesTheVectors(t *testing.T) {
	root := t.TempDir()
	path := writeFile(t, root, "src/a.js", "// header\nconst café = 1;  \n")
	got := annotated(t, root, "eslint", "utf-16", diagnostic.Diagnostic{
		File: path, Row: 2, EndRow: 2, Col: 7, EndCol: 11, Code: "no-unused-vars", Source: "eslint", Message: "unused",
	})[0]
	const want = "782d56fb252d612d013fd522fde6a5a8d4493f0dbe4113f391291cedde45da5c"
	if got.Anchor.Fingerprint != want || got.Anchor.Basis != BasisLine {
		t.Errorf("anchor = %+v, want fingerprint %s on the line", got.Anchor, want)
	}
	if *got.Anchor.Chars != (textpos.Span{Start: 7, End: 11}) || *got.Anchor.Bytes != (textpos.Span{Start: 7, End: 12}) ||
		got.Anchor.Precision != textpos.Exact {
		t.Errorf("spans = %v %v %s, want café in code points and bytes", *got.Anchor.Chars, *got.Anchor.Bytes, got.Anchor.Precision)
	}
}

// A line inserted above a finding moves its row, not its fingerprint.
func TestFingerprintSurvivesALineAbove(t *testing.T) {
	root := t.TempDir()
	finding := diagnostic.Diagnostic{Row: 2, EndRow: 2, Col: 1, EndCol: 2, Code: "C1", Source: "t", Message: "m"}

	finding.File = writeFile(t, root, "a.go", "package a\nvar bad = 1\n")
	before := annotated(t, root, "t", "utf-8", finding)[0].Anchor.Fingerprint

	finding.File = writeFile(t, root, "a.go", "package a\n\n// added\nvar bad = 1\n")
	finding.Row, finding.EndRow = 4, 4
	after := annotated(t, root, "t", "utf-8", finding)[0].Anchor.Fingerprint
	if before != after {
		t.Errorf("fingerprint moved with its row: %s, then %s", before, after)
	}
}

// Two equal findings on one line are two fingerprints, and so are the same
// rule on the same line from two tools.
func TestFingerprintsAreDistinct(t *testing.T) {
	root := t.TempDir()
	path := writeFile(t, root, "src/b.js", "let x = y == z;\n")
	one := diagnostic.Diagnostic{File: path, Row: 1, EndRow: 1, Col: 11, EndCol: 13, Code: "eqeqeq", Source: "eslint", Message: "Expected ==="}
	two := one
	two.Col, two.EndCol = 7, 8

	got := annotated(t, root, "eslint", "utf-16", one, two)
	if got[0].Anchor.Fingerprint == got[1].Anchor.Fingerprint {
		t.Fatal("two findings on one line share a fingerprint")
	}
	// Ordinals follow the column, not the order the tool printed them in.
	if got[1].Anchor.Fingerprint != "8d08ad593059cec87a6c97ce43655d04b1bee8102810cd99952dd606b26bb618" ||
		got[0].Anchor.Fingerprint != "dad1b7e3bcb9d02d57c352b1de167cfb3bb7286b26ab6fca66812abd15559155" {
		t.Errorf("fingerprints = %s, %s; want the golden vectors by column", got[0].Anchor.Fingerprint, got[1].Anchor.Fingerprint)
	}

	other := annotated(t, root, "oxlint", "utf-16", one)[0].Anchor.Fingerprint
	eslint := annotated(t, root, "eslint", "utf-16", one)[0].Anchor.Fingerprint
	if other == eslint {
		t.Error("two tools reporting one rule on one line share a fingerprint")
	}
}

// A file that is gone rests on the row; a finding without a file on nothing.
func TestFingerprintFallbacks(t *testing.T) {
	root := t.TempDir()
	gone := diagnostic.Diagnostic{File: filepath.Join(root, "gone", "Dockerfile"), Row: 12, EndRow: 12, Col: 1, EndCol: 1, Code: "DL3006", Source: "hadolint"}
	got := annotated(t, root, "hadolint", "", gone)[0].Anchor
	if got.Basis != BasisRow || got.Fingerprint != "243067f037e64756174f040f8142559b790e4173d35cbdf7ca3c5fb64ccd66cf" ||
		got.Precision != textpos.Unknown || got.Chars != nil {
		t.Errorf("deleted file anchor = %+v, want the row vector and no spans", got)
	}

	withoutFile := annotated(t, root, "golangci-lint", "utf-8", diagnostic.Diagnostic{Row: 1, EndRow: 1, Col: 1, EndCol: 1, Source: "golangci-lint", Message: "typecheck"})[0].Anchor
	if withoutFile.Basis != BasisNone || withoutFile.Fingerprint != "3389a70737766fc5b3754e0cf170e1a6e0c6c9e0cf1755ad727010293798ad27" {
		t.Errorf("file-less anchor = %+v, want the file-less vector", withoutFile)
	}
}

// A file outside the repository is never read, even when it could be: it
// rests on its row.
func TestAnnotateOutsideTheRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	path := writeFile(t, outside, "shared.go", "package shared\n")
	got := annotated(t, root, "t", "utf-8", diagnostic.Diagnostic{File: path, Row: 1, EndRow: 1, Col: 1, EndCol: 2})[0].Anchor
	if got.Basis != BasisRow || got.LineHash != RowHash(1) || got.Chars != nil {
		t.Errorf("anchor of a file outside the root = %+v, want the row", got)
	}
}

// The fingerprint a process's findings have in the gate hook is the one the
// report settles on when one process reports a line's findings.
func TestGateFingerprintIsTheReports(t *testing.T) {
	root := t.TempDir()
	path := writeFile(t, root, "src/b.js", "let x = y == z;\n")
	one := diagnostic.Diagnostic{File: path, Row: 1, EndRow: 1, Col: 11, EndCol: 13, Code: "eqeqeq", Source: "eslint", Message: "m", Severity: diagnostic.SeverityError}
	two := one
	two.Col, two.EndCol = 7, 8
	ds := annotated(t, root, "eslint", "utf-16", one, two)

	acc := NewAccumulator(Options{Root: root})
	tr := &ToolRun{Name: "eslint", Invocations: []Invocation{{ID: "eslint::1#1"}}}
	for _, d := range ds {
		tr.Invocations[0].Findings = append(tr.Invocations[0].Findings, acc.finding("eslint", d, tr))
	}
	settleFindings(tr)
	for i, f := range tr.Invocations[0].Findings {
		if f.Fingerprint != ds[i].Anchor.Fingerprint {
			t.Errorf("finding %d: report %s, gate %s", i, f.Fingerprint, ds[i].Anchor.Fingerprint)
		}
	}
}

// A finding over several lines converts its end column on its last line; one
// whose last line is missing keeps its fingerprint and loses its spans.
func TestAnnotateAcrossLines(t *testing.T) {
	root := t.TempDir()
	path := writeFile(t, root, "c.txt", "ab\né😀z\n")
	got := annotated(t, root, "t", "utf-32", diagnostic.Diagnostic{File: path, Row: 1, EndRow: 2, Col: 2, EndCol: 3})[0].Anchor
	if *got.UTF16 != (textpos.Span{Start: 2, End: 4}) {
		t.Errorf("UTF-16 span = %v, want the end after the emoji", *got.UTF16)
	}
	missing := annotated(t, root, "t", "utf-32", diagnostic.Diagnostic{File: path, Row: 1, EndRow: 9, Col: 1, EndCol: 2})[0].Anchor
	if missing.Basis != BasisLine || missing.Chars != nil || missing.Precision != textpos.Unknown {
		t.Errorf("anchor with a missing last line = %+v, want the line basis and no spans", missing)
	}
	if len(missing.Fingerprint) != 64 {
		t.Errorf("fingerprint = %q, want 64 hex characters", missing.Fingerprint)
	}
}
