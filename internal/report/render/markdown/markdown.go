// Package markdown renders a report.Run as Markdown for a person: what the run
// covered and whether it is complete, a table of each operation's tools, the
// findings the terminal would show — grouped by level, then by file — with the
// rest counted, and what was skipped, stopped or left incomplete. It is also
// what a GitHub step summary holds, where the page has a size limit: Write
// stops before it and says how many findings it cut.
//
// It carries no captured output: a tool that failed without findings is its
// structured message.
package markdown

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/report"
)

// Renderer writes the Markdown document.
type Renderer struct{}

// Name is the format's name in --report.
func (Renderer) Name() string { return "markdown" }

// Options is empty: the document has no variant to choose.
func (Renderer) Options() []string { return nil }

// OmitsIncompleteTools is false: the document lists every tool, with what
// kept it from being complete.
func (Renderer) OmitsIncompleteTools() bool { return false }

// Render writes the whole document.
func (Renderer) Render(w io.Writer, run *report.Run, _ map[string]string) error {
	_, err := Write(w, run, 0)
	return err
}

// Write writes the document, at most budget bytes of it when budget is not
// zero: the part that fits, then a footer saying how many findings did not.
// It returns how many were cut; ErrNoRoom, and nothing written, when not even
// the footer fits.
func Write(w io.Writer, run *report.Run, budget int) (cut int, err error) {
	parts := document(run)
	total, size := 0, 0
	for _, p := range parts {
		total += p.findings
		size += len(p.text)
	}
	if budget <= 0 || size <= budget {
		budget = 0
	}
	// The footer of the deepest cut is the longest one.
	reserve := len(footer(total, run))
	if budget > 0 && reserve > budget {
		return total, ErrNoRoom
	}
	var b strings.Builder
	for i, p := range parts {
		if budget > 0 && b.Len()+len(p.text) > budget-reserve {
			for _, rest := range parts[i:] {
				cut += rest.findings
			}
			b.WriteString(footer(cut, run))
			break
		}
		b.WriteString(p.text)
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return cut, fmt.Errorf("write markdown: %w", err)
	}
	return cut, nil
}

// ErrNoRoom is a budget too small for even the note that says so.
var ErrNoRoom = errors.New("no room left on the page")

func footer(cut int, run *report.Run) string {
	return fmt.Sprintf("\n_%s cut: the page ran out of room.%s_\n", count(cut, "finding", "findings"), wholeList(run))
}

// wholeList names where the findings a cut document leaves out can be read:
// the run's own JSON documents, written to a file, which hold every finding.
func wholeList(run *report.Run) string {
	var paths []string
	for _, e := range run.Exports {
		if e.Format == "json" && e.Status == report.ExportWritten && e.Path != "-" {
			paths = append(paths, code(e.Path))
		}
	}
	if len(paths) == 0 {
		return ""
	}
	return " Every finding is in " + strings.Join(paths, " and ") + "."
}

// part is a piece of the document; findings counts the findings it lists, so
// a cut document knows how many it left out.
type part struct {
	text     string
	findings int
}

func document(run *report.Run) []part {
	var parts []part
	add := func(format string, args ...any) {
		parts = append(parts, part{text: fmt.Sprintf(format, args...)})
	}
	add("## datamitsu · %s\n\n", escape(run.Datamitsu.Configuration))
	add("- **Selection:** %s\n", selection(run.Selection))
	if run.Complete {
		add("- **Complete:** yes\n")
	} else {
		reasons := make([]string, 0, len(run.Incomplete))
		for _, r := range run.Incomplete {
			reasons = append(reasons, string(r))
		}
		detail := "some tools did not cover everything"
		if len(reasons) > 0 {
			detail = strings.Join(reasons, ", ")
		}
		add("- **Complete:** no — %s\n", detail)
	}
	for _, op := range run.Operations {
		parts = append(parts, operation(op)...)
	}
	return parts
}

func selection(sel report.Selection) string {
	var s string
	switch sel.Mode {
	case "all":
		s = "the whole repository"
	case "subtree":
		s = "the subdirectory " + code(sel.Dir)
	case "paths":
		s = count(len(sel.Paths), "file", "files") + " named"
	case "empty":
		s = "nothing staged"
	default:
		s = escape(sel.Mode)
	}
	if sel.FileScoped {
		s += ", staged files only"
	}
	if len(sel.Tools) > 0 {
		s += ", tools " + codes(sel.Tools)
	}
	return s
}

func operation(op report.Operation) []part {
	var parts []part
	add := func(findings int, format string, args ...any) {
		parts = append(parts, part{text: fmt.Sprintf(format, args...), findings: findings})
	}
	add(0, "\n### %s\n\n", escape(op.Name))
	if !op.Ran {
		add(0, "_Did not run._\n")
		return parts
	}
	if len(op.Tools) > 0 {
		add(0, "| Tool | Status | Runs | Cached | Errors | Warnings | Info | Hints |\n| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |\n")
		for _, tr := range op.Tools {
			levels := levelsOf(tr)
			add(0, "| %s | %s | %d | %d | %d | %d | %d | %d |\n", cell(tr.Name), status(tr), runs(tr), cached(tr),
				levels[0], levels[1], levels[2], levels[3])
		}
	}

	shown, hidden, baselined := findings(op)
	if len(shown) > 0 {
		add(0, "\n#### Findings\n")
	}
	for _, group := range byLevel(shown) {
		add(0, "\n##### %s\n", levelTitle(group.level))
		for _, file := range group.files {
			heading := "No file"
			if file.path != "" {
				heading = code(file.path)
			}
			add(0, "\n%s\n\n", heading)
			for _, f := range file.findings {
				add(1, "- %s\n", line(f))
			}
		}
	}
	if hidden.total() > 0 {
		add(0, "\n_%s below the threshold not listed._\n", hidden.String())
	}
	if baselined > 0 {
		add(0, "\n_%s held by the baseline not listed._\n", count(baselined, "finding", "findings"))
	}

	if len(op.Skipped) > 0 || len(op.Cancelled) > 0 {
		add(0, "\n#### Skipped and stopped\n\n")
		for _, s := range op.Skipped {
			text := escape(s.Reason)
			if s.Detail != "" {
				text += ": " + escape(s.Detail)
			}
			add(0, "- %s skipped (%s)\n", code(s.Tool), text)
		}
		for _, c := range op.Cancelled {
			state := "not started"
			if c.Started {
				state = "cancelled"
			}
			add(0, "- %s %s (%s)\n", label(c.Tool, c.Dir), state, escape(c.Cause))
		}
	}

	if text := changedFiles(op); text != "" {
		add(0, "%s", text)
	}

	var incomplete []report.ToolRun
	for _, tr := range op.Tools {
		if !tr.Complete {
			incomplete = append(incomplete, tr)
		}
	}
	if len(incomplete) > 0 {
		add(0, "\n#### Incomplete\n\n")
		for _, tr := range incomplete {
			reasons := make([]string, 0, len(tr.Incomplete))
			for _, r := range tr.Incomplete {
				reasons = append(reasons, string(r))
			}
			add(0, "- %s: %s\n", code(tr.Name), escape(strings.Join(reasons, ", ")))
		}
	}
	return parts
}

// changedFiles is the section of a fix operation listing the files it
// changed, or saying why they were not observed; "" for an operation that
// observes none.
func changedFiles(op report.Operation) string {
	if op.Name != "fix" || op.ChangesReason == report.ChangesNoFixTask {
		return ""
	}
	changes := op.AllChanges()
	var b strings.Builder
	b.WriteString("\n#### Changed files\n\n")
	if !op.ChangesObserved {
		why := op.ChangesReason
		if op.ChangesDetail != "" {
			why += ": " + op.ChangesDetail
		}
		fmt.Fprintf(&b, "_Not observed: %s._\n", escape(why))
		if len(changes) == 0 {
			return b.String()
		}
		b.WriteString("\n")
	} else if len(changes) == 0 {
		b.WriteString("_None._\n")
		return b.String()
	}
	for _, c := range changes[:min(len(changes), changedFilesListed)] {
		fmt.Fprintf(&b, "- %s %s\n", code(c.Path), escape(c.Kind))
	}
	if more := len(changes) - changedFilesListed; more > 0 {
		fmt.Fprintf(&b, "- _and %s more; the JSON report lists every one_\n", count(more, "file", "files"))
	}
	return b.String()
}

// changedFilesListed is how many changed files the section names, so that a
// fix that rewrote a whole repository does not push the findings off a step
// summary.
const changedFilesListed = 50

func status(tr report.ToolRun) string {
	if len(tr.Invocations) == 0 {
		return "⊘ skipped"
	}
	failed, stopped := false, false
	for _, inv := range tr.Invocations {
		switch inv.FailureKind {
		case "exit", "threshold", "setup":
			failed = true
		case "cancelled":
			stopped = true
		}
		if inv.State == "not-started" {
			stopped = true
		}
	}
	switch {
	case failed:
		return "✗ failed"
	case stopped:
		return "⊘ stopped"
	}
	return "✓ passed"
}

func runs(tr report.ToolRun) int {
	n := 0
	for _, inv := range tr.Invocations {
		if inv.State == "ran" {
			n++
		}
	}
	return n
}

// cached counts the files a cache answered for.
func cached(tr report.ToolRun) int {
	n := 0
	for _, inv := range tr.Invocations {
		if inv.State == "cached" || inv.State == "verdict-hit" {
			n += len(inv.Files)
		}
	}
	return n
}

// levels counts findings per level, error first.
type levels [4]int

func (l *levels) String() string {
	names := [4][2]string{{"error", "errors"}, {"warning", "warnings"}, {"info", "info"}, {"hint", "hints"}}
	var parts []string
	for i, n := range l {
		if n > 0 {
			parts = append(parts, count(n, names[i][0], names[i][1]))
		}
	}
	return strings.Join(parts, ", ")
}

func (l *levels) add(severity string) {
	if lvl := config.Severity(severity).Level(); lvl >= 1 && lvl <= 4 {
		l[lvl-1]++
	}
}

func (l *levels) total() int { return l[0] + l[1] + l[2] + l[3] }

func levelsOf(tr report.ToolRun) levels {
	var l levels
	for _, inv := range tr.Invocations {
		for _, f := range inv.Findings {
			if f.Kind != "synthetic" {
				l.add(f.Severity)
			}
		}
	}
	return l
}

// findings is what the terminal shows of an operation (report.Visible), with
// the synthetic finding of a tool that failed without one, and a count of the
// rest: below the threshold, and held by the run's baseline.
func findings(op report.Operation) (shown []report.Finding, hidden levels, baselined int) {
	for _, tr := range op.Tools {
		for _, inv := range tr.Invocations {
			s, h := report.Visible(inv)
			shown = append(shown, s...)
			for _, f := range h {
				if f.Baselined {
					baselined++
				} else {
					hidden.add(f.Severity)
				}
			}
			if f, ok := report.Synthetic(inv); ok {
				shown = append(shown, f)
			}
		}
	}
	return shown, hidden, baselined
}

type fileGroup struct {
	path     string
	findings []report.Finding
}

type levelGroup struct {
	level string
	files []fileGroup
}

// byLevel groups findings by level, most severe first, then by file, the
// findings without a file last.
func byLevel(fs []report.Finding) []levelGroup {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		la, lb := config.Severity(a.Severity).Level(), config.Severity(b.Severity).Level()
		switch {
		case la != lb:
			return la < lb
		case (a.Location.Path == "") != (b.Location.Path == ""):
			return b.Location.Path == ""
		case a.Location.Path != b.Location.Path:
			return a.Location.Path < b.Location.Path
		case a.Location.Row != b.Location.Row:
			return a.Location.Row < b.Location.Row
		case a.Location.Col != b.Location.Col:
			return a.Location.Col < b.Location.Col
		case a.Source != b.Source:
			return a.Source < b.Source
		case a.Code != b.Code:
			return a.Code < b.Code
		}
		return a.Message < b.Message
	})
	var out []levelGroup
	for _, f := range fs {
		level := string(config.Severity(f.Severity))
		if len(out) == 0 || out[len(out)-1].level != level {
			out = append(out, levelGroup{level: level})
		}
		g := &out[len(out)-1]
		if len(g.files) == 0 || g.files[len(g.files)-1].path != f.Location.Path {
			g.files = append(g.files, fileGroup{path: f.Location.Path})
		}
		fg := &g.files[len(g.files)-1]
		fg.findings = append(fg.findings, f)
	}
	return out
}

func levelTitle(level string) string {
	switch level {
	case "error":
		return "Errors"
	case "warning":
		return "Warnings"
	case "info":
		return "Info"
	case "hint":
		return "Hints"
	}
	return escape(level)
}

// line is one finding: where, which rule, what. Columns are code points, and
// only where the report could convert them.
func line(f report.Finding) string {
	title := f.Source
	if title == "" {
		title = f.Tool
	}
	if f.Code != "" {
		title += "(" + f.Code + ")"
	}
	text := code(title) + ": " + escape(f.Message)
	if f.Baselined {
		text += " _(baselined)_"
	}
	loc := f.Location
	if loc.Path == "" {
		return text
	}
	where := fmt.Sprintf("%s:%d", loc.Path, loc.Row)
	precise := loc.Precision == "exact" || loc.Precision == "ascii"
	if precise && loc.Chars != nil && loc.Chars.Start > 0 && (loc.EndRow == 0 || loc.EndRow == loc.Row) {
		where += fmt.Sprintf(":%d", loc.Chars.Start)
	}
	return code(where) + " — " + text
}

func label(tool, dir string) string {
	if dir == "" {
		return code(tool)
	}
	return code(tool) + " in " + code(dir)
}

func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// code is s as inline code, fenced by more backticks than it holds in a row.
func code(s string) string {
	s = oneLine(s)
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", longest+1)
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		return fence + " " + s + " " + fence
	}
	return fence + s + fence
}

// cell is s as inline code in a table cell, where a pipe ends the cell even
// inside code unless it is escaped.
func cell(s string) string {
	return strings.ReplaceAll(code(s), "|", `\|`)
}

func codes(names []string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = code(n)
	}
	return strings.Join(out, ", ")
}

// Escape is tool text as the document writes it: inert Markdown on one line.
func Escape(s string) string { return escape(s) }

// Code is s as the document writes a path or a name: inline code on one line.
func Code(s string) string { return code(s) }

// escape makes tool text inert Markdown on one line: no emphasis, link,
// heading, table cell or HTML it could open.
func escape(s string) string {
	return escaper.Replace(oneLine(s))
}

var escaper = strings.NewReplacer(
	"&", "&amp;", `\`, `\\`, "`", "\\`", "*", `\*`, "_", `\_`, "[", `\[`, "]", `\]`,
	"<", "&lt;", ">", "&gt;", "#", `\#`, "|", `\|`, "~", `\~`, "!", `\!`,
)

func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' }), " ")
}
