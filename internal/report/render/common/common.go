// Package common holds what the interchange formats share: which operation a
// format that lists findings writes, how a finding's path and columns are
// carried, the order findings are listed in, and the completeness companion
// written beside a format whose shape has no place for that claim.
//
// Everything here reads the report model alone — never the checkout — so a
// document rendered offline by `report render` is the one the run wrote.
package common

import (
	"net/url"
	"slices"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/textpos"
)

// ListedOperationName is the operation a format that lists one operation
// writes from a run of operations: lint when the run has one — a check would
// otherwise hold each tool twice, and lint's findings are the state after the
// fix — otherwise fix; "" when there is neither.
func ListedOperationName(operations []string) string {
	for _, op := range []config.OperationType{config.OpLint, config.OpFix} {
		if slices.Contains(operations, string(op)) {
			return string(op)
		}
	}
	return ""
}

// ListedOperation is the operation of run that ListedOperationName picks; nil
// when the run has neither.
func ListedOperation(run *report.Run) *report.Operation {
	names := make([]string, 0, len(run.Operations))
	for _, op := range run.Operations {
		names = append(names, op.Name)
	}
	want := ListedOperationName(names)
	for i := range run.Operations {
		if run.Operations[i].Name == want {
			return &run.Operations[i]
		}
	}
	return nil
}

// Outside reports a path the report left absolute because it lies outside the
// repository, on this system or the one that wrote the report.
func Outside(p string) bool {
	switch {
	case strings.HasPrefix(p, "/"), strings.HasPrefix(p, `\`):
		return true
	case len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/'):
		return true
	}
	return false
}

// RelativeURI is a repository-relative path as a URI reference: each segment
// percent-encoded, and a colon in the first one encoded too, so that it cannot
// read as a scheme.
func RelativeURI(p string) string {
	segments := strings.Split(p, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	segments[0] = strings.ReplaceAll(segments[0], ":", "%3A")
	return strings.Join(segments, "/")
}

// FileURI is an absolute path as a file:// URI: "/a b/c" is
// "file:///a%20b/c" and `C:\a\b` is "file:///C:/a/b".
func FileURI(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	segments := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, s := range segments {
		if i == 0 && len(s) == 2 && s[1] == ':' {
			continue
		}
		segments[i] = url.PathEscape(s)
	}
	return "file:///" + strings.Join(segments, "/")
}

// Region is where a finding points, in the unit a format counts columns in.
// Line and EndLine are 1-based; Col and EndCol are 1-based with EndCol
// exclusive, and zero where the format gets no column: a location whose
// columns could not be converted into the unit, or, for EndCol, a finding
// that is a point.
type Region struct {
	Line, Col       int
	EndLine, EndCol int
}

// Span says what one location looks like in one column unit.
type Span func(loc report.Location) *textpos.Span

// Chars and Bytes pick the code-point and UTF-8 byte spans of a location.
var (
	Chars Span = func(loc report.Location) *textpos.Span { return loc.Chars }
	Bytes Span = func(loc report.Location) *textpos.Span { return loc.Bytes }
)

// RegionOf is where loc points with columns taken from unit's span; the zero
// Region for a location without a row.
func RegionOf(loc report.Location, unit Span) Region {
	if loc.Row <= 0 {
		return Region{}
	}
	r := Region{Line: loc.Row, EndLine: max(loc.EndRow, loc.Row)}
	span := unit(loc)
	if !Precise(loc) || span == nil || span.Start <= 0 {
		return r
	}
	r.Col = span.Start
	if r.EndLine > r.Line || span.End > span.Start {
		r.EndCol = span.End
	}
	return r
}

// Precise reports columns that were converted from a known unit, or from an
// ASCII line where every unit counts alike.
func Precise(loc report.Location) bool {
	return loc.Precision == string(textpos.Exact) || loc.Precision == string(textpos.ASCII)
}

// Synthetic reports the finding that stands for a tool that failed without a
// parsable one.
func Synthetic(f report.Finding) bool { return f.Kind == "synthetic" }

// Located is one finding with the tool run it belongs to.
type Located struct {
	Tool    *report.ToolRun
	Finding report.Finding
}

// Findings lists every finding of op's tools — synthetic ones included — in
// the order every format lists them: by path (those without a path last), row,
// column, tool, source, rule, message and fingerprint.
func Findings(op *report.Operation) []Located {
	if op == nil {
		return nil
	}
	var out []Located
	for i := range op.Tools {
		tr := &op.Tools[i]
		for _, inv := range tr.Invocations {
			for _, f := range inv.Findings {
				out = append(out, Located{Tool: tr, Finding: f})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return Less(out[i].Finding, out[j].Finding) })
	return out
}

// Less orders findings as the formats list them.
func Less(a, b report.Finding) bool {
	pa, pb := a.Location.Path, b.Location.Path
	switch {
	case (pa == "") != (pb == ""):
		return pb == ""
	case pa != pb:
		return pa < pb
	case a.Location.Row != b.Location.Row:
		return a.Location.Row < b.Location.Row
	case a.Location.Col != b.Location.Col:
		return a.Location.Col < b.Location.Col
	case a.Tool != b.Tool:
		return a.Tool < b.Tool
	case a.Source != b.Source:
		return a.Source < b.Source
	case a.Code != b.Code:
		return a.Code < b.Code
	case a.Message != b.Message:
		return a.Message < b.Message
	}
	return a.Fingerprint < b.Fingerprint
}

// SourceOf is the name a finding's source goes by: the source its parser
// named, or the tool.
func SourceOf(f report.Finding) string {
	if f.Source != "" {
		return f.Source
	}
	return f.Tool
}

// Rule is "<source>/<code>", or the source alone for a finding without a rule.
func Rule(f report.Finding) string {
	if f.Code == "" {
		return SourceOf(f)
	}
	return SourceOf(f) + "/" + f.Code
}

// Level is a severity's rank on the core scale, 1 for error to 4 for hint; 0
// for a severity it does not know.
func Level(severity string) uint8 {
	return config.Severity(severity).Level()
}

// Security reports a tool whose parser module puts it in the security
// category.
func Security(tr *report.ToolRun) bool { return tr.Category == "security" }

// File is one document of a report split over a directory.
type File struct {
	Name string
	Data []byte
}
