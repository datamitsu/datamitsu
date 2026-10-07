// Package diagnostic defines datamitsu's finalized diagnostic contract and the
// "defaults-in-core" boundary: a WASM parser extracts only what a tool actually
// emitted (a nullable parsermanager.RawDiagnostic), and this package fills the
// gaps to produce a complete Diagnostic. Keeping every judgement call here — not
// in the parsers — means the parsers stay tiny and dumb and all policy lives in
// one reviewable place (analysis.md §1, the none-ls/efm intersection).
package diagnostic

import (
	"path/filepath"

	"github.com/datamitsu/datamitsu/internal/parsermanager"
)

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

// Diagnostic is the core's finalized diagnostic. It is produced from a parser's
// nullable RawDiagnostic by Resolve — never constructed by a parser — and every
// consumer after the terminal (an editor, a report, a CI annotation) relies on
// one contract for its positions and path, whatever the tool printed:
//   - Row and Col are 1-based. The future LSP layer converts to 0-based at its
//     boundary.
//   - EndRow and EndCol are 1-based and EndCol is exclusive: the span stops
//     before the column it names. A span with no extent is a point, EndRow ==
//     Row and EndCol == Col.
//   - File is absolute and cleaned once the executor has resolved it against
//     the working directory of the process that reported it (AbsPath).
//   - Severity is the level the tool printed, when it printed one. A finding
//     without a level is an error when the process that reported it failed
//     and a warning when it passed: for a tool with no level vocabulary the
//     exit code is the only statement of seriousness it makes. A printed
//     level is never changed by the exit code.
//
// The core cannot tell a 0-based positive column from a 1-based one, or an
// inclusive end from an exclusive one; those are corrected in the parser that
// reports them.
type Diagnostic struct {
	// File is the path the diagnostic belongs to. Most tool formats drop the
	// filename, so the executor stamps the one file a process was given;
	// formats that do name one per diagnostic (eslint's filePath) report it
	// through the parser, which is what makes batch runs over many files
	// attributable. Empty when neither source knows it.
	File     string   `json:"file,omitempty"`
	Row      int      `json:"row"`      // 1-based start line
	Col      int      `json:"col"`      // 1-based start column
	EndRow   int      `json:"endRow"`   // 1-based end line
	EndCol   int      `json:"endCol"`   // 1-based end column, exclusive
	Severity Severity `json:"severity"` // resolved 1–4
	Message  string   `json:"message"`  // the one always-present field
	Source   string   `json:"source"`   // originating tool (e.g. "hadolint")
	Code     string   `json:"code,omitempty"`
	URL      string   `json:"url,omitempty"` // the rule's documentation, where the tool prints it
	// Reported marks a finding at or above its operation's effective failOn:
	// what the terminal shows. The failOn gate sets it; without one it stays
	// false.
	Reported bool `json:"reported,omitempty"`
	// Gates marks a reported finding whose process the gate was active for —
	// what fails a run on its own. A process parsed by a module that predates
	// the severity contract has none.
	Gates bool `json:"gates,omitempty"`
}

// Resolve fills the core's defaults over a parser's nullable RawDiagnostic. source
// is the tool name the parser ran for; a Source the parser set itself (e.g.
// cue_fmt) takes precedence. failed says whether the process that printed the
// finding exited non-zero. Defaults:
//   - a missing or 0 row/col → 1 (several parsers emit 0 for "no position");
//   - an end column without an end row → on the start row, the span most
//     tools mean by a column range;
//   - an end without a column, or one before the start, → a point at the
//     start: a column the tool did not print would be invented;
//   - a 0 end row/col → 1, before that comparison;
//   - a missing or out-of-range severity → error when failed, warning when
//     not (see Diagnostic).
//
// The file is left as the parser reported it: only the executor knows the
// working directory a relative path is relative to.
func Resolve(raw parsermanager.RawDiagnostic, source string, failed bool) Diagnostic {
	row := position(raw.Row, 1)
	col := position(raw.Col, 1)
	endRow, endCol := row, col
	if raw.EndCol != nil {
		endRow = position(raw.EndRow, row)
		endCol = position(raw.EndCol, col)
		if endRow < row || (endRow == row && endCol < col) {
			endRow, endCol = row, col
		}
	}
	d := Diagnostic{
		Row:      row,
		Col:      col,
		EndRow:   endRow,
		EndCol:   endCol,
		Severity: levelWithout(failed),
		Message:  raw.Message,
		Source:   source,
	}
	if raw.Severity != nil {
		if s := Severity(*raw.Severity); s >= SeverityError && s <= SeverityHint {
			d.Severity = s
		}
	}
	if raw.URL != nil {
		d.URL = *raw.URL
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

// AbsPath is the path contract of Diagnostic.File: a path a tool reported
// relative to the working directory of its process is joined onto it, and an
// absolute one is cleaned, so "./x", "x" and "<dir>/x" name one file. An empty
// path stays empty.
func AbsPath(file, workingDir string) string {
	switch {
	case file == "":
		return ""
	case filepath.IsAbs(file):
		return filepath.Clean(file)
	default:
		return filepath.Join(workingDir, file)
	}
}

// levelWithout is the level of a finding its tool printed none for.
func levelWithout(failed bool) Severity {
	if failed {
		return SeverityError
	}
	return SeverityWarning
}

// ResolveAll resolves a parser's whole output for one process of a tool.
func ResolveAll(raws []parsermanager.RawDiagnostic, source string, failed bool) []Diagnostic {
	if len(raws) == 0 {
		return nil
	}
	out := make([]Diagnostic, 0, len(raws))
	for _, raw := range raws {
		out = append(out, Resolve(raw, source, failed))
	}
	return out
}

func position(p *uint32, def int) int {
	switch {
	case p == nil:
		return def
	case *p == 0:
		return 1
	default:
		return int(*p)
	}
}
