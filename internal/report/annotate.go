package report

import (
	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/textpos"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

// Annotator anchors the findings of each parsed process while their files are
// on disk: the runner calls it from the gate hook, right after parsing and
// before the threshold decides, so that anything judging a finding by its
// fingerprint can do so before the gate does. It reads each file with findings
// once for as long as the file is unchanged.
type Annotator struct {
	root  string
	lines *textpos.Reader
	facts ParserFacts
}

// NewAnnotator anchors findings relative to root; facts names each parser's
// column unit, and may be nil.
func NewAnnotator(root string, facts ParserFacts) *Annotator {
	return &Annotator{root: root, lines: textpos.NewReader(), facts: facts}
}

// Annotate sets the Anchor of every finding of proc: its fingerprint within
// the process, what the fingerprint rests on, and its columns in every unit.
func (a *Annotator) Annotate(task tooling.Task, proc *tooling.ProcessResult) {
	if len(proc.Diagnostics) == 0 {
		return
	}
	unit := a.columnUnit(task)
	in := make([]fingerprintInput, len(proc.Diagnostics))
	for i := range proc.Diagnostics {
		d := &proc.Diagnostics[i]
		d.Anchor = a.anchor(*d, unit)
		in[i] = inputOf(task.ToolName, RelPath(a.root, d.File), *d)
	}
	for i, fp := range fingerprints(in) {
		proc.Diagnostics[i].Anchor.Fingerprint = fp
	}
}

func (a *Annotator) columnUnit(task tooling.Task) textpos.Unit {
	p := task.Tool.OutputParser
	if p == nil || a.facts == nil {
		return ""
	}
	facts, ok := a.facts(p.Module, p.Parser)
	if !ok {
		return ""
	}
	return textpos.Unit(facts.Tool.ColumnUnit)
}

func (a *Annotator) anchor(d diagnostic.Diagnostic, unit textpos.Unit) *diagnostic.Anchor {
	if d.File == "" {
		return &diagnostic.Anchor{Basis: BasisNone, Precision: textpos.Unknown}
	}
	start, err := a.lines.LineAt(d.File, d.Row)
	if err != nil {
		return &diagnostic.Anchor{Basis: BasisRow, LineHash: RowHash(d.Row), Precision: textpos.Unknown}
	}
	anchor := &diagnostic.Anchor{Basis: BasisLine, LineHash: LineHash(start), Precision: textpos.Unknown}
	end := start
	if d.EndRow != d.Row {
		if end, err = a.lines.LineAt(d.File, d.EndRow); err != nil {
			return anchor
		}
	}
	chars, bytes, utf16, precision := textpos.Spans(start, end, d.Col, d.EndCol, unit)
	anchor.Precision = precision
	if precision != textpos.Unknown {
		anchor.Chars, anchor.Bytes, anchor.UTF16 = &chars, &bytes, &utf16
	}
	return anchor
}

// inputOf is what a finding's fingerprint is computed from. A finding that was
// never anchored rests on its row, or on nothing when it names no file.
func inputOf(tool, relPath string, d diagnostic.Diagnostic) fingerprintInput {
	in := fingerprintInput{
		tool: tool, code: d.Code, relPath: relPath,
		row: d.Row, col: d.Col, source: d.Source, message: d.Message,
	}
	switch {
	case d.Anchor != nil:
		in.lineHash = d.Anchor.LineHash
	case d.File != "":
		in.lineHash = RowHash(d.Row)
	}
	return in
}
