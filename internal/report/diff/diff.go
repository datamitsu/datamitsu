// Package diff compares the findings of two runs by fingerprint, tool by tool:
// what is new in the second, what stayed, what moved to another row, what was
// fixed — and what cannot be called fixed because the second run did not look
// everywhere the first did. It reads two own reports and nothing else.
package diff

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/report/render/common"
	"github.com/datamitsu/datamitsu/internal/report/render/markdown"
)

// Schema names the shape of a diff document.
const Schema = "datamitsu.diff/1"

// Tool statuses: a tool both runs hold is compared; one only a single run
// holds is unobserved in the other.
const (
	StatusCompared   = "compared"
	StatusBeforeOnly = "before-only"
	StatusAfterOnly  = "after-only"
)

// Result is the difference between two runs.
type Result struct {
	Schema string `json:"schema"`
	// Fingerprint is the version of the fingerprints both runs hold.
	Fingerprint string `json:"fingerprint"`
	Before      Side   `json:"before"`
	After       Side   `json:"after"`
	Summary     Counts `json:"summary"`
	Tools       []Tool `json:"tools"`
}

// Side is one of the two runs: when it started, the operation whose findings
// were compared, what it covered and whether it was complete.
type Side struct {
	StartedAt  time.Time       `json:"startedAt"`
	Operation  string          `json:"operation"`
	Selection  string          `json:"selection"`
	Complete   bool            `json:"complete"`
	Incomplete []report.Reason `json:"incomplete"`
}

// Counts are how many findings fell in each class.
type Counts struct {
	New        int `json:"new"`
	Fixed      int `json:"fixed"`
	Unchanged  int `json:"unchanged"`
	Moved      int `json:"moved"`
	Unknown    int `json:"unknown"`
	Unobserved int `json:"unobserved"`
}

// Tool is one tool's findings, classed.
type Tool struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	// Complete and Incomplete are the tool's completeness in the second run —
	// in the first for a tool only it holds: what decides whether a finding
	// that disappeared is fixed.
	Complete   bool            `json:"complete"`
	Incomplete []report.Reason `json:"incomplete"`
	Counts     Counts          `json:"counts"`
	// New are in the second run only; Unchanged in both, on the same row;
	// Moved in both, on another row; Fixed in the first only, where the second
	// looked everywhere; Unknown in the first only, where it did not;
	// Unobserved every finding of a tool one run does not hold.
	New        []Entry `json:"new"`
	Fixed      []Entry `json:"fixed"`
	Unchanged  []Entry `json:"unchanged"`
	Moved      []Entry `json:"moved"`
	Unknown    []Entry `json:"unknown"`
	Unobserved []Entry `json:"unobserved"`
}

// Entry is one finding: as the second run reported it, or as the first did
// for one the second does not hold.
type Entry struct {
	Fingerprint string `json:"fingerprint"`
	Severity    string `json:"severity"`
	Source      string `json:"source"`
	Code        string `json:"code,omitempty"`
	Message     string `json:"message"`
	Path        string `json:"path"`
	Row         int    `json:"row"`
	// BeforeRow is the row of a moved finding in the first run.
	BeforeRow int `json:"beforeRow,omitempty"`
}

// Diff compares the findings of before and after, both own reports with
// fingerprints of one version. Each run contributes the operation a report
// lists — lint, or fix for a fix-only run. A finding the second run does not
// hold is fixed only when its tool is complete there and the run covered the
// whole repository; otherwise it is unknown, with the tool's reasons.
func Diff(before, after *report.Run) Result {
	res := Result{
		Schema:      Schema,
		Fingerprint: report.FingerprintVersion,
		Before:      side(before),
		After:       side(after),
		Tools:       []Tool{},
	}
	b, a := toolsOf(before), toolsOf(after)
	names := map[string]bool{}
	for name := range b {
		names[name] = true
	}
	for name := range a {
		names[name] = true
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	lookedEverywhere := after.Selection.Mode == "all"
	for _, name := range sorted {
		bt, inBefore := b[name]
		at, inAfter := a[name]
		var t Tool
		switch {
		case !inAfter:
			t = unobserved(name, StatusBeforeOnly, bt)
		case !inBefore:
			t = unobserved(name, StatusAfterOnly, at)
		default:
			t = compare(name, bt, at, lookedEverywhere)
		}
		res.Summary.add(t.Counts)
		res.Tools = append(res.Tools, t)
	}
	return res
}

func side(run *report.Run) Side {
	s := Side{
		StartedAt: run.StartedAt, Selection: run.Selection.Mode, Complete: run.Complete,
		Incomplete: append([]report.Reason{}, run.Incomplete...),
	}
	if op := common.ListedOperation(run); op != nil {
		s.Operation = op.Name
	}
	return s
}

// toolFindings is one tool of a run: its completeness and its findings by
// fingerprint.
type toolFindings struct {
	run      report.ToolRun
	findings map[string]report.Finding
}

func toolsOf(run *report.Run) map[string]toolFindings {
	out := map[string]toolFindings{}
	op := common.ListedOperation(run)
	if op == nil {
		return out
	}
	for _, tr := range op.Tools {
		tf := toolFindings{run: tr, findings: map[string]report.Finding{}}
		for _, inv := range tr.Invocations {
			for _, f := range inv.Findings {
				if f.Kind == "synthetic" || f.Fingerprint == "" {
					continue
				}
				if _, seen := tf.findings[f.Fingerprint]; !seen {
					tf.findings[f.Fingerprint] = f
				}
			}
		}
		out[tr.Name] = tf
	}
	return out
}

func unobserved(name, status string, tf toolFindings) Tool {
	t := newTool(name, status, tf.run)
	for _, f := range tf.findings {
		t.Unobserved = append(t.Unobserved, entry(f))
	}
	t.finish()
	return t
}

func compare(name string, before, after toolFindings, lookedEverywhere bool) Tool {
	t := newTool(name, StatusCompared, after.run)
	for fp, f := range after.findings {
		prev, held := before.findings[fp]
		switch {
		case !held:
			t.New = append(t.New, entry(f))
		case prev.Location.Row == f.Location.Row:
			t.Unchanged = append(t.Unchanged, entry(f))
		default:
			e := entry(f)
			e.BeforeRow = prev.Location.Row
			t.Moved = append(t.Moved, e)
		}
	}
	fixed := lookedEverywhere && after.run.Complete
	for fp, f := range before.findings {
		if _, held := after.findings[fp]; held {
			continue
		}
		if fixed {
			t.Fixed = append(t.Fixed, entry(f))
		} else {
			t.Unknown = append(t.Unknown, entry(f))
		}
	}
	t.finish()
	return t
}

func newTool(name, status string, tr report.ToolRun) Tool {
	return Tool{
		Name: name, Status: status, Complete: tr.Complete, Incomplete: append([]report.Reason{}, tr.Incomplete...),
		New: []Entry{}, Fixed: []Entry{}, Unchanged: []Entry{}, Moved: []Entry{}, Unknown: []Entry{}, Unobserved: []Entry{},
	}
}

func entry(f report.Finding) Entry {
	source := f.Source
	if source == "" {
		source = f.Tool
	}
	return Entry{
		Fingerprint: f.Fingerprint, Severity: f.Severity, Source: source, Code: f.Code, Message: f.Message,
		Path: f.Location.Path, Row: f.Location.Row,
	}
}

// finish sorts every list and counts it.
func (t *Tool) finish() {
	for _, list := range []*[]Entry{&t.New, &t.Fixed, &t.Unchanged, &t.Moved, &t.Unknown, &t.Unobserved} {
		sortEntries(*list)
	}
	t.Counts = Counts{
		New: len(t.New), Fixed: len(t.Fixed), Unchanged: len(t.Unchanged),
		Moved: len(t.Moved), Unknown: len(t.Unknown), Unobserved: len(t.Unobserved),
	}
}

func (c *Counts) add(o Counts) {
	c.New += o.New
	c.Fixed += o.Fixed
	c.Unchanged += o.Unchanged
	c.Moved += o.Moved
	c.Unknown += o.Unknown
	c.Unobserved += o.Unobserved
}

func sortEntries(list []Entry) {
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		switch {
		case a.Path != b.Path:
			return a.Path < b.Path
		case a.Row != b.Row:
			return a.Row < b.Row
		default:
			return a.Fingerprint < b.Fingerprint
		}
	})
}

// Formats a diff is written in.
const (
	FormatJSON     = "json"
	FormatMarkdown = "markdown"
)

// Formats lists the formats Write takes.
func Formats() []string { return []string{FormatJSON, FormatMarkdown} }

// Write writes res in format.
func Write(w io.Writer, res Result, format string) error {
	switch format {
	case FormatJSON:
		data, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return fmt.Errorf("encode diff: %w", err)
		}
		if _, err := w.Write(append(data, '\n')); err != nil {
			return fmt.Errorf("write diff: %w", err)
		}
		return nil
	case FormatMarkdown:
		if _, err := io.WriteString(w, markdownOf(res)); err != nil {
			return fmt.Errorf("write diff: %w", err)
		}
		return nil
	}
	return fmt.Errorf("unknown diff format %q (must be %s)", format, strings.Join(Formats(), " or "))
}

// markdownOf is the diff for a person: the counts per tool, then the new,
// fixed, moved, unknown and unobserved findings; unchanged ones are counted.
func markdownOf(res Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## datamitsu diff · %s → %s\n\n", sideText(res.Before), sideText(res.After))
	s := res.Summary
	fmt.Fprintf(&b, "%d new · %d fixed · %d unchanged · %d moved · %d unknown · %d unobserved\n",
		s.New, s.Fixed, s.Unchanged, s.Moved, s.Unknown, s.Unobserved)
	if len(res.Tools) == 0 {
		return b.String()
	}
	b.WriteString("\n| Tool | New | Fixed | Unchanged | Moved | Unknown | Unobserved |\n" +
		"| --- | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, t := range res.Tools {
		c := t.Counts
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d | %d |\n", strings.ReplaceAll(markdown.Code(t.Name), "|", `\|`),
			c.New, c.Fixed, c.Unchanged, c.Moved, c.Unknown, c.Unobserved)
	}
	for _, class := range []struct {
		title string
		list  func(Tool) []Entry
	}{
		{"New", func(t Tool) []Entry { return t.New }},
		{"Fixed", func(t Tool) []Entry { return t.Fixed }},
		{"Moved", func(t Tool) []Entry { return t.Moved }},
		{"Unknown", func(t Tool) []Entry { return t.Unknown }},
		{"Unobserved", func(t Tool) []Entry { return t.Unobserved }},
	} {
		var lines []string
		for _, t := range res.Tools {
			for _, e := range class.list(t) {
				lines = append(lines, "- "+entryText(t, e, class.title))
			}
		}
		if len(lines) > 0 {
			fmt.Fprintf(&b, "\n### %s\n\n%s\n", class.title, strings.Join(lines, "\n"))
		}
	}
	return b.String()
}

func sideText(s Side) string {
	text := s.StartedAt.UTC().Format(time.RFC3339) + " " + s.Operation
	if !s.Complete {
		text += " (incomplete)"
	}
	return text
}

func entryText(t Tool, e Entry, class string) string {
	where := "no file"
	if e.Path != "" {
		where = fmt.Sprintf("%s:%d", e.Path, e.Row)
	}
	rule := e.Source
	if e.Code != "" {
		rule += "(" + e.Code + ")"
	}
	text := fmt.Sprintf("%s — %s %s: %s", markdown.Code(where), markdown.Code(rule), e.Severity, markdown.Escape(e.Message))
	switch {
	case class == "Moved":
		text += fmt.Sprintf(" _(was row %d)_", e.BeforeRow)
	case class == "Unknown" && len(t.Incomplete) > 0:
		text += " _(" + markdown.Escape(reasons(t.Incomplete)) + ")_"
	case class == "Unobserved":
		text += " _(" + t.Status + ")_"
	}
	return text
}

func reasons(rs []report.Reason) string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = string(r)
	}
	return strings.Join(out, ", ")
}
