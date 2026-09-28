// Package github turns a run's findings into GitHub Actions workflow commands
// — `::error file=…,line=…,title=…::message` — which GitHub shows as
// annotations in the Checks tab and on the lines a pull request changed.
//
// GitHub keeps the first ten annotations of each type a step prints, so which
// ten is the whole design: Select takes what the terminal shows of the whole
// run, deduplicated, and orders each type so that the files a change touched
// come first and every other file gets one annotation before any gets a
// second. What does not fit is counted in a notice that takes the first
// notice slot.
package github

import (
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/textpos"
)

// Annotation types, the GitHub side of the core's levels: info and hint are
// both notices.
const (
	LevelError   = "error"
	LevelWarning = "warning"
	LevelNotice  = "notice"
)

// Budget is how many annotations of one type GitHub keeps from a step: the
// first ten it reads.
const Budget = 10

// MessageLimit is the length at which GitHub cuts a message, in characters;
// cutting at as many bytes never exceeds it.
const MessageLimit = 4096

// Annotation is one finding as a workflow command.
type Annotation struct {
	Level string
	// File is relative to the repository root with "/"; empty for a finding
	// that names no file, or one outside the repository.
	File string
	// Line and EndLine are 1-based; EndLine is set only when the finding
	// spans lines. Col and EndCol are code points on a one-line finding whose
	// columns could be converted, EndCol exclusive; zero otherwise.
	Line, EndLine int
	Col, EndCol   int
	Source, Code  string
	Message       string
	Fingerprint   string

	// severity is the finding's level, which a duplicate is weighed by.
	severity string
}

// Title is how GitHub heads the annotation: "<source>(<code>)", or the source
// alone.
func (a Annotation) Title() string {
	if a.Code == "" {
		return a.Source
	}
	return a.Source + "(" + a.Code + ")"
}

// Command is the workflow command that prints a as an annotation.
func (a Annotation) Command() string {
	var props []string
	if a.File != "" {
		props = append(props, "file="+EscapeProperty(a.File))
		if a.Line > 0 {
			props = append(props, fmt.Sprintf("line=%d", a.Line))
			if a.Col > 0 {
				props = append(props, fmt.Sprintf("col=%d", a.Col))
				if a.EndCol > a.Col {
					props = append(props, fmt.Sprintf("endColumn=%d", a.EndCol))
				}
			}
			if a.EndLine > a.Line {
				props = append(props, fmt.Sprintf("endLine=%d", a.EndLine))
			}
		}
	}
	if title := a.Title(); title != "" {
		props = append(props, "title="+EscapeProperty(title))
	}
	return "::" + a.Level + " " + strings.Join(props, ",") + "::" + EscapeData(cut(a.Message, MessageLimit))
}

// EscapeData escapes a command's message as the runner unescapes it.
func EscapeData(s string) string {
	return dataEscaper.Replace(s)
}

// EscapeProperty escapes a command's property value as the runner unescapes
// it: the message escapes, and the separators of the property list.
func EscapeProperty(s string) string {
	return propertyEscaper.Replace(s)
}

var (
	dataEscaper     = strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A")
	propertyEscaper = strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C")
)

// cut shortens s to at most n bytes without splitting a character.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	i := n
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i]
}

// Candidates is everything a run can annotate: what the terminal shows of
// every invocation (report.Visible) and the synthetic findings that stand for
// a failure without one, each finding once however many invocations reported
// it — the strongest of its duplicates.
func Candidates(run *report.Run) []Annotation {
	var out []Annotation
	at := map[string]int{}
	add := func(f report.Finding) {
		a := fromFinding(f)
		if f.Fingerprint == "" {
			out = append(out, a)
			return
		}
		if i, seen := at[f.Fingerprint]; seen {
			if levelRank(f.Severity) < levelRank(out[i].severity) {
				out[i] = a
			}
			return
		}
		at[f.Fingerprint] = len(out)
		out = append(out, a)
	}
	for _, op := range run.Operations {
		for _, tr := range op.Tools {
			for _, inv := range tr.Invocations {
				shown, _ := report.Visible(tr, inv)
				for _, f := range shown {
					add(f)
				}
				if f, ok := report.Synthetic(inv); ok {
					add(f)
				}
			}
		}
	}
	return out
}

func levelRank(severity string) uint8 {
	if l := config.Severity(severity).Level(); l > 0 {
		return l
	}
	return 255
}

func fromFinding(f report.Finding) Annotation {
	a := Annotation{
		Level:       levelOf(f.Severity),
		Source:      f.Source,
		Code:        f.Code,
		Message:     f.Message,
		Fingerprint: f.Fingerprint,
		severity:    f.Severity,
	}
	if a.Source == "" {
		a.Source = f.Tool
	}
	loc := f.Location
	if loc.Path == "" || isAbsolute(loc.Path) {
		return a
	}
	a.File, a.Line = loc.Path, loc.Row
	if loc.EndRow > loc.Row {
		a.EndLine = loc.EndRow
		return a
	}
	precise := loc.Precision == string(textpos.Exact) || loc.Precision == string(textpos.ASCII)
	if precise && loc.Chars != nil && loc.Chars.Start > 0 {
		a.Col = loc.Chars.Start
		if loc.Chars.End > loc.Chars.Start {
			a.EndCol = loc.Chars.End
		}
	}
	return a
}

// isAbsolute reports a path the report left absolute because it lies outside
// the repository, on this system or the one that wrote the report.
func isAbsolute(p string) bool {
	switch {
	case strings.HasPrefix(p, "/"), strings.HasPrefix(p, `\`):
		return true
	case len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/'):
		return true
	}
	return false
}

func levelOf(severity string) string {
	switch severity {
	case string(config.SeverityError):
		return LevelError
	case string(config.SeverityWarning):
		return LevelWarning
	}
	return LevelNotice
}

// Overflows reports whether some type has more candidates than GitHub keeps:
// only then does their order decide what is shown.
func Overflows(candidates []Annotation) bool {
	count := map[string]int{}
	for _, a := range candidates {
		count[a.Level]++
		if count[a.Level] > Budget {
			return true
		}
	}
	return false
}

// Selection is what a run prints: the annotations of each type that fit, in
// the order they are printed, and how many candidates did not fit.
type Selection struct {
	Annotations []Annotation
	// Candidates counts every finding that could be annotated.
	Candidates int
	// Omitted counts the candidates left out; when it is not zero a notice
	// saying so takes the first notice slot.
	Omitted int
}

// Select orders the candidates of each type — touched files first, then one
// finding of every other file before a second of any, then the findings
// without a file — and keeps the first Budget of each; the notice type keeps
// one fewer when anything was left out, for the notice that says so.
func Select(candidates []Annotation, touched map[string]bool) Selection {
	buckets := map[string][]Annotation{}
	for _, a := range candidates {
		buckets[a.Level] = append(buckets[a.Level], a)
	}
	sel := Selection{Candidates: len(candidates)}
	errors := order(buckets[LevelError], touched)
	warnings := order(buckets[LevelWarning], touched)
	notices := order(buckets[LevelNotice], touched)
	noticeBudget := Budget
	if len(errors) > Budget || len(warnings) > Budget || len(notices) > Budget {
		noticeBudget--
	}
	for _, bucket := range []struct {
		list   []Annotation
		budget int
	}{{errors, Budget}, {warnings, Budget}, {notices, noticeBudget}} {
		kept := bucket.list[:min(len(bucket.list), bucket.budget)]
		sel.Annotations = append(sel.Annotations, kept...)
		sel.Omitted += len(bucket.list) - len(kept)
	}
	return sel
}

// order is one type's candidates in print order.
func order(list []Annotation, touched map[string]bool) []Annotation {
	byFile := map[string][]Annotation{}
	var noFile []Annotation
	for _, a := range list {
		if a.File == "" {
			noFile = append(noFile, a)
			continue
		}
		byFile[a.File] = append(byFile[a.File], a)
	}
	var near, far []string
	for file, found := range byFile {
		sort.Slice(found, func(i, j int) bool { return less(found[i], found[j]) })
		if touched[file] {
			near = append(near, file)
		} else {
			far = append(far, file)
		}
	}
	slices.Sort(near)
	slices.Sort(far)
	sort.Slice(noFile, func(i, j int) bool { return less(noFile[i], noFile[j]) })
	out := make([]Annotation, 0, len(list))
	out = append(out, roundRobin(near, byFile)...)
	out = append(out, roundRobin(far, byFile)...)
	return append(out, noFile...)
}

// roundRobin takes the first finding of every file, then the second of every
// file that has one, and so on.
func roundRobin(files []string, byFile map[string][]Annotation) []Annotation {
	var out []Annotation
	for round := 0; ; round++ {
		took := false
		for _, file := range files {
			if found := byFile[file]; round < len(found) {
				out = append(out, found[round])
				took = true
			}
		}
		if !took {
			return out
		}
	}
}

func less(a, b Annotation) bool {
	switch {
	case a.Line != b.Line:
		return a.Line < b.Line
	case a.Col != b.Col:
		return a.Col < b.Col
	case a.Source != b.Source:
		return a.Source < b.Source
	case a.Code != b.Code:
		return a.Code < b.Code
	default:
		return a.Fingerprint < b.Fingerprint
	}
}

// Print writes sel as workflow commands, one per line. rest names where the
// findings it left out can be read — "the step summary", a report's path —
// for the notice that counts them.
func Print(w io.Writer, sel Selection, rest []string) error {
	var b strings.Builder
	notice := sel.Omitted > 0
	for _, a := range sel.Annotations {
		if notice && a.Level == LevelNotice {
			b.WriteString(omittedNotice(sel.Omitted, rest) + "\n")
			notice = false
		}
		b.WriteString(a.Command() + "\n")
	}
	if notice {
		b.WriteString(omittedNotice(sel.Omitted, rest) + "\n")
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("print annotations: %w", err)
	}
	return nil
}

func omittedNotice(n int, rest []string) string {
	noun := "findings"
	if n == 1 {
		noun = "finding"
	}
	where := "not annotated"
	if len(rest) > 0 {
		where = "in " + strings.Join(rest, " and in ")
	}
	return "::" + LevelNotice + "::" + EscapeData(fmt.Sprintf("datamitsu: %d more %s %s", n, noun, where))
}
