// Package codequality renders a report.Run as a GitLab Code Quality report: a
// CodeClimate-shaped JSON array that GitLab reads from
// artifacts:reports:codequality, merges across a pipeline's jobs, and compares
// with the target branch's to show what a merge request brings in and what it
// fixes.
//
// Because GitLab compares, a finding missing from the report reads as fixed:
// a tool whose completeness is not established is kept, with the findings it
// has, and named in the completeness companion a job checks before it
// publishes. GitLab requires a repository-relative path, so a finding without
// a file, or outside the repository, cannot be carried: it is left out and
// counted in the companion.
package codequality

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/report/render/common"
)

// Renderer writes a GitLab Code Quality report.
type Renderer struct{}

// Name is the format's name in --report.
func (Renderer) Name() string { return "codequality" }

// Options is empty: the report has no variant to choose.
func (Renderer) Options() []string { return nil }

// OmitsIncompleteTools is false: the report keeps a tool that is not
// complete, so a narrowed run refuses it.
func (Renderer) OmitsIncompleteTools() bool { return false }

// Companion describes the listed operation and counts the findings the report
// could not carry.
func (Renderer) Companion(run *report.Run, _ map[string]string) common.Companion {
	op := common.ListedOperation(run)
	var ops []*report.Operation
	if op != nil {
		ops = append(ops, op)
	}
	_, omitted := issues(op)
	return common.NewCompanion(run, "codequality", ops, omitted)
}

// Render writes the array, indented by two spaces.
func (Renderer) Render(w io.Writer, run *report.Run, _ map[string]string) error {
	list, _ := issues(common.ListedOperation(run))
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(list); err != nil {
		return fmt.Errorf("write codequality: %w", err)
	}
	return nil
}

// Issue is one finding as GitLab reads it.
type Issue struct {
	Type        string   `json:"type"`
	CheckName   string   `json:"check_name"`
	Description string   `json:"description"`
	Categories  []string `json:"categories"`
	Severity    string   `json:"severity"`
	Fingerprint string   `json:"fingerprint"`
	Location    Location `json:"location"`
}

// Location is a repository-relative path with lines, or with positions when
// the report could convert the columns into characters.
type Location struct {
	Path      string     `json:"path"`
	Lines     *Lines     `json:"lines,omitempty"`
	Positions *Positions `json:"positions,omitempty"`
}

// Lines is the 1-based lines a finding spans.
type Lines struct {
	Begin int `json:"begin"`
	End   int `json:"end"`
}

// Positions is where a finding begins and ends; columns count characters, and
// the end column is exclusive as in the report.
type Positions struct {
	Begin Position `json:"begin"`
	End   Position `json:"end"`
}

// Position is a 1-based line and column.
type Position struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

func issues(op *report.Operation) ([]Issue, []common.OmittedFindings) {
	found := common.Findings(op)
	list := make([]Issue, 0, len(found))
	omitted := map[[2]string]int{}
	for _, l := range found {
		f := l.Finding
		switch {
		case f.Location.Path == "":
			omitted[[2]string{l.Tool.Name, common.OmittedNoFile}]++
			continue
		case common.Outside(f.Location.Path):
			omitted[[2]string{l.Tool.Name, common.OmittedOutside}]++
			continue
		}
		category := "Style"
		if f.Kind == "security" || common.Security(l.Tool) {
			category = "Security"
		}
		list = append(list, Issue{
			Type:        "issue",
			CheckName:   common.Rule(f),
			Description: f.Message,
			Categories:  []string{category},
			Severity:    Severity(f.Severity),
			Fingerprint: f.Fingerprint,
			Location:    location(f.Location),
		})
	}
	keys := make([][2]string, 0, len(omitted))
	for k := range omitted {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	counts := make([]common.OmittedFindings, 0, len(keys))
	for _, k := range keys {
		counts = append(counts, common.OmittedFindings{Tool: k[0], Reason: k[1], Count: omitted[k]})
	}
	return list, counts
}

func location(loc report.Location) Location {
	r := common.RegionOf(loc, common.Chars)
	begin, end := max(r.Line, 1), max(r.EndLine, r.Line, 1)
	if r.Col == 0 {
		return Location{Path: loc.Path, Lines: &Lines{Begin: begin, End: end}}
	}
	endCol := r.EndCol
	if endCol == 0 {
		endCol = r.Col
	}
	return Location{Path: loc.Path, Positions: &Positions{
		Begin: Position{Line: begin, Column: r.Col},
		End:   Position{Line: end, Column: endCol},
	}}
}

// Severity is the Code Quality severity of a core severity: an error is
// major, a warning minor, info and hint are info. critical and blocker are
// never written: the core has no level that would mean them.
func Severity(severity string) string {
	switch config.Severity(severity) {
	case config.SeverityError:
		return "major"
	case config.SeverityWarning:
		return "minor"
	case config.SeverityInfo, config.SeverityHint:
		return "info"
	}
	return "info"
}
