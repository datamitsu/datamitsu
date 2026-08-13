// Package diagnostic defines datamitsu's finalized diagnostic contract and the
// "defaults-in-core" boundary: a WASM parser extracts only what a tool actually
// emitted (a nullable parsermanager.RawDiagnostic), and this package fills the
// gaps to produce a complete Diagnostic. Keeping every judgement call here — not
// in the parsers — means the parsers stay tiny and dumb and all policy lives in
// one reviewable place (analysis.md §1, the none-ls/efm intersection).
package diagnostic

import "github.com/datamitsu/datamitsu/internal/parsermanager"

// Severity is the normalized 1–4 scale shared by parsers, this contract, and LSP
// DiagnosticSeverity (1=Error … 4=Hint), so values map 1:1 across the boundary.
type Severity uint8

// Severity levels on the normalized 1–4 scale (== LSP DiagnosticSeverity).
const (
	SeverityError   Severity = 1
	SeverityWarning Severity = 2
	SeverityInfo    Severity = 3
	SeverityHint    Severity = 4
)

// fallbackSeverity is used when a tool reports no level. none-ls/efm both escalate
// to error; datamitsu deliberately defaults to Warning instead — an un-leveled
// finding should not silently gate a build at error severity. Flip this one
// constant to change the policy.
const fallbackSeverity = SeverityWarning

// String is the lowercase human/label form ("error", "warning", …).
func (s Severity) String() string {
	switch s {
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	case SeverityInfo:
		return "info"
	case SeverityHint:
		return "hint"
	default:
		return "warning"
	}
}

// Diagnostic is the core's finalized diagnostic: every position is present and
// **1-based** (matching tool output and the CLI; the future LSP layer converts to
// 0-based at its boundary), and severity is resolved. It is produced from a
// parser's nullable RawDiagnostic by Resolve — never constructed by a parser.
type Diagnostic struct {
	// File is the path the diagnostic belongs to. Most tool formats drop the
	// filename, so the executor stamps the file it linted; formats that do name
	// one per diagnostic (eslint's filePath) report it through the parser, which
	// is what makes batch runs over many files attributable. Empty when neither
	// source knows it.
	File     string   `json:"file,omitempty"`
	Row      int      `json:"row"`      // 1-based start line
	Col      int      `json:"col"`      // 1-based start column
	EndRow   int      `json:"endRow"`   // 1-based end line
	EndCol   int      `json:"endCol"`   // 1-based end column
	Severity Severity `json:"severity"` // resolved 1–4
	Message  string   `json:"message"`  // the one always-present field
	Source   string   `json:"source"`   // originating tool (e.g. "hadolint")
	Code     string   `json:"code,omitempty"`
}

// Resolve fills the core's defaults over a parser's nullable RawDiagnostic. source
// is the tool name the parser ran for; a Source the parser set itself (e.g.
// cue_fmt) takes precedence. Defaults follow the none-ls/efm intersection:
//   - missing row/col → 1 (both projects coerce 0 → 1);
//   - missing end_row → row, missing end_col → col (a point span);
//   - missing/out-of-range severity → fallbackSeverity.
func Resolve(raw parsermanager.RawDiagnostic, source string) Diagnostic {
	row := derefU32(raw.Row, 1)
	col := derefU32(raw.Col, 1)
	d := Diagnostic{
		Row:      row,
		Col:      col,
		EndRow:   derefU32(raw.EndRow, row),
		EndCol:   derefU32(raw.EndCol, col),
		Severity: fallbackSeverity,
		Message:  raw.Message,
		Source:   source,
	}
	if raw.Severity != nil {
		if s := Severity(*raw.Severity); s >= SeverityError && s <= SeverityHint {
			d.Severity = s
		}
	}
	if raw.Source != nil && *raw.Source != "" {
		d.Source = *raw.Source
	}
	if raw.Code != nil {
		d.Code = *raw.Code
	}
	if raw.File != nil {
		d.File = *raw.File
	}
	return d
}

// ResolveAll resolves a parser's whole output for one tool.
func ResolveAll(raws []parsermanager.RawDiagnostic, source string) []Diagnostic {
	if len(raws) == 0 {
		return nil
	}
	out := make([]Diagnostic, 0, len(raws))
	for _, raw := range raws {
		out = append(out, Resolve(raw, source))
	}
	return out
}

// derefU32 returns the pointed-to value as an int, or def when nil.
func derefU32(p *uint32, def int) int {
	if p == nil {
		return def
	}
	return int(*p)
}
