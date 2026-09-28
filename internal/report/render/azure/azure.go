// Package azure turns a run's findings into Azure Pipelines logging commands —
// `##vso[task.logissue type=error;sourcepath=…;linenumber=…]message` — which
// the pipeline shows as the errors and warnings of the task.
//
// The agent keeps ten issues of each type a task logs, and has no type below
// warning, so Select takes the errors and the warnings of what the terminal
// shows, orders them as the GitHub renderer does — touched files first, then
// one finding of every file before a second of any — and keeps ten of each.
// The agent finds `##vso[` anywhere in a line, not only at its start: Neutralize
// breaks the prefix in every line of tool output the runner prints.
package azure

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/report/render/github"
)

// Issue types: the agent knows these two; info and hint have no issue.
const (
	TypeError   = "error"
	TypeWarning = "warning"
)

// Budget is how many issues of one type the agent keeps from a task.
const Budget = 10

// commandPrefix starts every logging command; the agent looks for it
// anywhere in a line.
const commandPrefix = "##vso["

// Candidates is everything a run can log as an issue: the errors and warnings
// of github.Candidates — what the terminal shows, each finding once, and the
// synthetic findings of tools that failed without one.
func Candidates(run *report.Run) []github.Annotation {
	var out []github.Annotation
	for _, a := range github.Candidates(run) {
		if typeOf(a) != "" {
			out = append(out, a)
		}
	}
	return out
}

// typeOf is the issue type of a, "" for a level the agent has no type for.
func typeOf(a github.Annotation) string {
	switch a.Level {
	case github.LevelError:
		return TypeError
	case github.LevelWarning:
		return TypeWarning
	}
	return ""
}

// Overflows reports whether some type has more candidates than the agent
// keeps: only then does their order decide what is shown.
func Overflows(candidates []github.Annotation) bool {
	count := map[string]int{}
	for _, a := range candidates {
		count[a.Level]++
		if count[a.Level] > Budget {
			return true
		}
	}
	return false
}

// Selection is what a run logs: the issues that fit, errors then warnings, and
// how many candidates did not fit.
type Selection struct {
	Issues     []github.Annotation
	Candidates int
	Omitted    int
}

// Select orders the candidates of each type as github.Order does and keeps
// the first Budget of each.
func Select(candidates []github.Annotation, touched map[string]bool) Selection {
	buckets := map[string][]github.Annotation{}
	for _, a := range candidates {
		buckets[a.Level] = append(buckets[a.Level], a)
	}
	sel := Selection{Candidates: len(candidates)}
	for _, level := range []string{github.LevelError, github.LevelWarning} {
		list := github.Order(buckets[level], touched)
		kept := list[:min(len(list), Budget)]
		sel.Issues = append(sel.Issues, kept...)
		sel.Omitted += len(list) - len(kept)
	}
	return sel
}

// Command is the logging command that logs a as an issue: its file relative to
// the repository root, its line and — on a one-line finding whose columns could
// be converted — its column in characters, and its rule as source(code).
func Command(a github.Annotation) string {
	props := []string{"type=" + typeOf(a)}
	if a.File != "" {
		props = append(props, "sourcepath="+Escape(a.File))
		if a.Line > 0 {
			props = append(props, "linenumber="+strconv.Itoa(a.Line))
			if a.Col > 0 {
				props = append(props, "columnnumber="+strconv.Itoa(a.Col))
			}
		}
	}
	if title := a.Title(); title != "" {
		props = append(props, "code="+Escape(title))
	}
	return commandPrefix + "task.logissue " + strings.Join(props, ";") + "]" + Escape(Neutralize(a.Message))
}

// Escape escapes a property value or a message as the agent unescapes them:
// "%" first, so that no escape it writes is read twice.
func Escape(s string) string {
	return escaper.Replace(s)
}

var escaper = strings.NewReplacer("%", "%AZP25", ";", "%3B", "\r", "%0D", "\n", "%0A", "]", "%5D")

// Neutralize breaks every logging command in a line of tool text — "##vso["
// becomes "##vso [" — so that the agent, which finds the prefix anywhere in a
// line, runs none of them.
func Neutralize(line string) string {
	return strings.ReplaceAll(line, commandPrefix, "##vso [")
}

// Print writes sel as logging commands, one per line, then — when anything did
// not fit — one plain line saying how many were left out and where they can be
// read.
func Print(w io.Writer, sel Selection, rest []string) error {
	var b strings.Builder
	for _, a := range sel.Issues {
		b.WriteString(Command(a) + "\n")
	}
	if sel.Omitted > 0 {
		noun := "findings"
		if sel.Omitted == 1 {
			noun = "finding"
		}
		where := "not logged as issues"
		if len(rest) > 0 {
			where = "in " + strings.Join(rest, " and in ")
		}
		fmt.Fprintf(&b, "datamitsu: %d more %s %s\n", sel.Omitted, noun, Neutralize(where))
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("print logging commands: %w", err)
	}
	return nil
}
