// Package rdjsonl renders a report.Run as reviewdog's rdjsonl — one Diagnostic
// JSON object per line — which `reviewdog -f rdjsonl` turns into review
// comments on any forge it reports to.
//
// Columns are UTF-8 bytes with an exclusive end, as reviewdog's rdf format
// counts them (proto/rdf/reviewdog.proto), taken from the report's bytes span
// and left out where it could not be converted. A finding without a file is
// a line without a location. Suggestions are never written.
package rdjsonl

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/report/render/common"
)

// Renderer writes rdjsonl.
type Renderer struct{}

// Name is the format's name in --report.
func (Renderer) Name() string { return "rdjsonl" }

// Options is empty: the stream has no variant to choose.
func (Renderer) Options() []string { return nil }

// OmitsIncompleteTools is false: the stream keeps a tool that is not
// complete, so a narrowed run refuses it, as it does every format that lists
// findings.
func (Renderer) OmitsIncompleteTools() bool { return false }

// Companion describes the listed operation.
func (Renderer) Companion(run *report.Run, _ map[string]string) common.Companion {
	var ops []*report.Operation
	if op := common.ListedOperation(run); op != nil {
		ops = append(ops, op)
	}
	return common.NewCompanion(run, "rdjsonl", ops, nil)
}

// Diagnostic is one line of the stream.
type Diagnostic struct {
	Message  string    `json:"message"`
	Location *Location `json:"location,omitempty"`
	Severity string    `json:"severity"`
	Source   Source    `json:"source"`
	Code     *Code     `json:"code,omitempty"`
}

// Location is a path and, where the finding has one, a range.
type Location struct {
	Path  string `json:"path"`
	Range *Range `json:"range,omitempty"`
}

// Range is where a finding starts and, unless it is a point, ends.
type Range struct {
	Start Position  `json:"start"`
	End   *Position `json:"end,omitempty"`
}

// Position is a 1-based line and a 1-based column in UTF-8 bytes; zero
// columns are left out.
type Position struct {
	Line   int `json:"line"`
	Column int `json:"column,omitempty"`
}

// Source is the tool that reported a finding and where to read about it.
type Source struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

// Code is a finding's rule and its documentation.
type Code struct {
	Value string `json:"value"`
	URL   string `json:"url,omitempty"`
}

// Render writes one line per finding of the listed operation.
func (Renderer) Render(w io.Writer, run *report.Run, _ map[string]string) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, l := range common.Findings(common.ListedOperation(run)) {
		if err := enc.Encode(diagnostic(l)); err != nil {
			return fmt.Errorf("write rdjsonl: %w", err)
		}
	}
	return nil
}

func diagnostic(l common.Located) Diagnostic {
	f := l.Finding
	d := Diagnostic{
		Message:  f.Message,
		Severity: Severity(f.Severity),
		Source:   Source{Name: common.SourceOf(f), URL: l.Tool.App.OfficialURL},
	}
	if f.Code != "" {
		d.Code = &Code{Value: f.Code, URL: f.RuleURL}
	}
	if f.Location.Path == "" {
		return d
	}
	d.Location = &Location{Path: f.Location.Path}
	if r := common.RegionOf(f.Location, common.Bytes); r.Line > 0 {
		d.Location.Range = &Range{Start: Position{Line: r.Line, Column: r.Col}}
		if r.EndLine > r.Line || r.EndCol > 0 {
			d.Location.Range.End = &Position{Line: r.EndLine, Column: r.EndCol}
		}
	}
	return d
}

// Severity is the rdf severity of a core severity: info and hint are both
// INFO.
func Severity(severity string) string {
	switch config.Severity(severity) {
	case config.SeverityError:
		return "ERROR"
	case config.SeverityWarning:
		return "WARNING"
	case config.SeverityInfo, config.SeverityHint:
		return "INFO"
	}
	return "INFO"
}
