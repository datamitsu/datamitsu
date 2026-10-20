// Package teamcity turns a run's findings into TeamCity service messages: an
// inspectionType per rule and an inspection per finding, which the build's
// Inspections tab lists, and a buildProblem for a tool that failed without a
// finding.
//
// TeamCity keeps every inspection, so there is no budget and no selection.
// It reads service messages anywhere in a line; the runner suspends that for
// the results block (DisableServiceMessages … EnableServiceMessages) and
// breaks the prefix in every line of tool output it prints (Neutralize), so a
// tool can neither report a problem nor turn the reading back on.
package teamcity

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/digest"
	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/report/render/github"
)

// MessagePrefix starts every service message.
const MessagePrefix = "##teamcity["

// DisableServiceMessages and EnableServiceMessages suspend and resume the
// reading of service messages for the rest of the build step.
const (
	DisableServiceMessages = MessagePrefix + "disableServiceMessages]"
	EnableServiceMessages  = MessagePrefix + "enableServiceMessages]"
)

// identityLimit is the longest identity a buildProblem takes.
const identityLimit = 60

// Severity is the inspection severity of a finding level; TeamCity has one for
// each.
func Severity(level string) string {
	switch config.Severity(level) {
	case config.SeverityError:
		return "ERROR"
	case config.SeverityWarning:
		return "WARNING"
	case config.SeverityInfo:
		return "INFO"
	case config.SeverityHint:
	}
	return "WEAK WARNING"
}

// Messages is what Print writes: the service messages of a run, and how many
// findings without a file — which an inspection cannot hold — it left out.
type Messages struct {
	Lines   []string
	Omitted int
}

// Build turns the candidates of a run (github.Candidates: what the terminal
// shows, each finding once, and the synthetic findings) into service
// messages: every inspectionType first, once per tool and rule, then the
// inspections, file by file, then a buildProblem per synthetic finding.
func Build(candidates []github.Annotation) Messages {
	var m Messages
	var inspections, problems []string
	declared := map[string]bool{}
	urls := map[string]string{}
	for _, a := range candidates {
		if id := typeID(a); a.RuleURL != "" && urls[id] == "" {
			urls[id] = a.RuleURL
		}
	}
	for _, a := range sorted(candidates) {
		if a.Synthetic {
			problems = append(problems, MessagePrefix+fmt.Sprintf("buildProblem description='%s' identity='%s']",
				Escape(a.Message), Identity(a.Tool)))
			continue
		}
		if a.File == "" {
			m.Omitted++
			continue
		}
		id := typeID(a)
		if !declared[id] {
			declared[id] = true
			m.Lines = append(m.Lines, inspectionType(a, id, urls[id]))
		}
		props := fmt.Sprintf("typeId='%s' message='%s' file='%s'", Escape(id), Escape(a.Message), Escape(a.File))
		if a.Line > 0 {
			props += " line='" + strconv.Itoa(a.Line) + "'"
		}
		inspections = append(inspections, MessagePrefix+"inspection "+props+" SEVERITY='"+Severity(a.Severity)+"']")
	}
	m.Lines = append(m.Lines, inspections...)
	m.Lines = append(m.Lines, problems...)
	return m
}

// sorted orders findings by file — those without one last — then line,
// column, source, code and fingerprint.
func sorted(list []github.Annotation) []github.Annotation {
	out := append([]github.Annotation{}, list...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case (a.File == "") != (b.File == ""):
			return b.File == ""
		case a.File != b.File:
			return a.File < b.File
		case a.Line != b.Line:
			return a.Line < b.Line
		case a.Col != b.Col:
			return a.Col < b.Col
		case a.Source != b.Source:
			return a.Source < b.Source
		case a.Code != b.Code:
			return a.Code < b.Code
		}
		return a.Fingerprint < b.Fingerprint
	})
	return out
}

// typeID names a rule: the tool, then the rule's code, or unknown.
func typeID(a github.Annotation) string {
	code := a.Code
	if code == "" {
		code = "unknown"
	}
	return toolOf(a) + "/" + code
}

func toolOf(a github.Annotation) string {
	if a.Tool != "" {
		return a.Tool
	}
	return a.Source
}

func inspectionType(a github.Annotation, id, url string) string {
	name := a.Code
	if name == "" {
		name = toolOf(a)
	}
	description := url
	if description == "" {
		description = a.Title()
	}
	return MessagePrefix + fmt.Sprintf("inspectionType id='%s' name='%s' description='%s' category='%s']",
		Escape(id), Escape(name), Escape(description), Escape(toolOf(a)))
}

// Identity is the identity of a tool's build problem: its name in Java
// identifier characters, at most 60 of them. A name that had to change gets
// the start of its SHA-256, so two tools never share one — TeamCity stores it
// to recognise the problem in the next build.
func Identity(tool string) string {
	var b strings.Builder
	for i, r := range tool {
		switch {
		case r == '_' || r == '$' || unicode.IsLetter(r) || (i > 0 && unicode.IsDigit(r)):
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	id := b.String()
	if id == tool && len(id) <= identityLimit && id != "" {
		return id
	}
	suffix := "_" + digest.SHA256Of([]byte(tool)).Hex()[:8]
	runes := []rune(id)
	if len(runes) > identityLimit-len(suffix) {
		runes = runes[:identityLimit-len(suffix)]
	}
	return string(runes) + suffix
}

// Escape escapes a service message value: "|" first, then the quote, the
// brackets and the line breaks by their letters, and every other control
// character — and the Unicode line separators — as |0xNNNN.
func Escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '|':
			b.WriteString("||")
		case '\'':
			b.WriteString("|'")
		case '\n':
			b.WriteString("|n")
		case '\r':
			b.WriteString("|r")
		case '[':
			b.WriteString("|[")
		case ']':
			b.WriteString("|]")
		default:
			if r < 0x20 || (r >= 0x7f && r <= 0x9f) || r == 0x2028 || r == 0x2029 {
				fmt.Fprintf(&b, "|0x%04X", r)
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Neutralize breaks every service message in a line of tool text —
// "##teamcity[" becomes "##teamcity [" — so that TeamCity, which finds the
// prefix anywhere in a line, reads none of them, not even one that turns the
// reading back on.
func Neutralize(line string) string {
	return strings.ReplaceAll(line, MessagePrefix, "##teamcity [")
}

// Print writes m, one message per line, then — when findings without a file
// were left out — one plain line saying how many.
func Print(w io.Writer, m Messages) error {
	var b strings.Builder
	for _, line := range m.Lines {
		b.WriteString(line + "\n")
	}
	if m.Omitted > 0 {
		noun := "findings"
		if m.Omitted == 1 {
			noun = "finding"
		}
		fmt.Fprintf(&b, "datamitsu: %d %s without a file not reported as inspections\n", m.Omitted, noun)
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("print service messages: %w", err)
	}
	return nil
}

// Candidates is github.Candidates: TeamCity takes every level.
func Candidates(run *report.Run) []github.Annotation {
	return github.Candidates(run)
}
