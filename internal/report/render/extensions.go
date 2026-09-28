package render

import (
	"fmt"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/report/render/common"
)

// What a format may add to a Renderer. A format implements only what its
// shape calls for; the writer, the runner and `report render` ask for each.

// DirRenderer is a format a path ending in "/" splits over several files in
// that directory.
type DirRenderer interface {
	Renderer
	// RenderFiles returns the documents of run, named as they are written
	// into the directory.
	RenderFiles(run *report.Run, options map[string]string) ([]File, error)
	// Owns reports a file name the format writes into a directory, so that
	// what an earlier run wrote there and this one did not is removed: a
	// reader of the directory would take it for part of this run.
	Owns(name string) bool
}

// File is one document of a directory target.
type File = common.File

// OptionChecker is a format whose option values are checked when the report
// is asked for, before anything runs.
type OptionChecker interface {
	CheckOptions(options map[string]string) error
}

// Companioned is a format whose shape has no place to say how complete it
// is: a completeness companion is written beside its file.
type Companioned interface {
	Companion(run *report.Run, options map[string]string) common.Companion
}

// Omitter is a format that leaves out a tool whose completeness is not
// established, or one it cannot hold.
type Omitter interface {
	Omitted(run *report.Run, options map[string]string) []report.OmittedTool
}

// Capped is a format that holds at most a number of tools in one file; a
// DirRenderer splits a run with more.
type Capped interface {
	ToolsPerFile() int
	// WrittenTools is how many tools the format writes of run.
	WrittenTools(run *report.Run, options map[string]string) int
}

// OneFileCap is how many tools the one file spec writes can hold; ok is
// false for a format without a limit, and for a directory target, which
// splits.
func OneFileCap(spec Spec) (limit int, ok bool) {
	r, known := Lookup(spec.Format)
	if !known || spec.Dir() {
		return 0, false
	}
	c, capped := r.(Capped)
	if !capped {
		return 0, false
	}
	return c.ToolsPerFile(), true
}

// CheckCapacity refuses a report that would have to hold more tools in one
// file than its format can: tools is how many it would hold.
func CheckCapacity(spec Spec, tools int) error {
	limit, ok := OneFileCap(spec)
	if !ok || tools <= limit {
		return nil
	}
	where := "one file"
	if spec.Stdout() {
		where = "stdout"
	}
	return fmt.Errorf("report %s would hold %d tools in %s, more than the %d it can: "+
		"name a directory, %s=<dir>/, to split it into files of at most %d",
		spec.Format, tools, where, limit, spec.Format, limit)
}

// Describe records in run's exports what each report beside its status is:
// the companion written beside it, and the tools it leaves out.
func Describe(run *report.Run, specs []Spec) {
	for _, spec := range specs {
		r, ok := Lookup(spec.Format)
		if !ok {
			continue
		}
		for i := range run.Exports {
			e := &run.Exports[i]
			if e.Format != spec.Format || e.Path != spec.Path {
				continue
			}
			if _, companioned := r.(Companioned); companioned && !spec.Stdout() {
				e.Companion = common.CompanionPath(spec.Path)
			}
			if o, omits := r.(Omitter); omits {
				e.Omitted = o.Omitted(run, spec.Options)
			}
		}
	}
}

// Notes are the warnings a run's reports call for, one line each: a tool
// whose completeness is not established — named once, with the reports that
// leave it out and those that flag it — a tool a format cannot hold, and the
// findings a format could not carry.
func Notes(run *report.Run, specs []Spec) []string {
	type key struct{ op, tool string }
	type note struct {
		reasons         []string
		omittedBy       []string
		flaggedBy       []string
		unflaggedBy     []string
		tooManyResultIn []string
	}
	notes := map[key]*note{}
	at := func(op, tool string) *note {
		k := key{op, tool}
		if notes[k] == nil {
			notes[k] = &note{}
		}
		return notes[k]
	}
	var findings []string
	for _, spec := range specs {
		r, ok := Lookup(spec.Format)
		if !ok {
			continue
		}
		if o, omits := r.(Omitter); omits {
			for _, t := range o.Omitted(run, spec.Options) {
				n := at(t.Operation, t.Tool)
				if len(t.Reasons) == 1 && t.Reasons[0] == common.ReasonTooManyResults {
					n.tooManyResultIn = append(n.tooManyResultIn, spec.Format)
					continue
				}
				n.reasons = t.Reasons
				n.omittedBy = append(n.omittedBy, spec.Format)
			}
		}
		if c, companioned := r.(Companioned); companioned {
			companion := c.Companion(run, spec.Options)
			for _, t := range companion.IncompleteTools() {
				n := at(t.Operation, t.Name)
				n.reasons = reasonStrings(t.Incomplete)
				if spec.Stdout() {
					n.unflaggedBy = append(n.unflaggedBy, spec.Format)
				} else {
					n.flaggedBy = append(n.flaggedBy, spec.Format)
				}
			}
			if line := omittedFindings(spec.Format, companion.Omitted); line != "" {
				findings = append(findings, line)
			}
		}
	}
	keys := make([]key, 0, len(notes))
	for k := range notes {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].op != keys[j].op {
			return keys[i].op < keys[j].op
		}
		return keys[i].tool < keys[j].tool
	})
	var out []string
	for _, k := range keys {
		n := notes[k]
		if len(n.omittedBy) > 0 || len(n.flaggedBy) > 0 || len(n.unflaggedBy) > 0 {
			var what []string
			if len(n.omittedBy) > 0 {
				what = append(what, "left out of "+strings.Join(n.omittedBy, ", "))
			}
			if len(n.flaggedBy) > 0 {
				what = append(what, "flagged in the completeness companion of "+strings.Join(n.flaggedBy, ", "))
			}
			if len(n.unflaggedBy) > 0 {
				what = append(what, "listed as if complete by "+strings.Join(n.unflaggedBy, ", ")+" on stdout, which has no companion")
			}
			reasons := "completeness not established"
			if len(n.reasons) > 0 {
				reasons = strings.Join(n.reasons, ", ")
			}
			out = append(out, fmt.Sprintf("report: %s tool %s is incomplete (%s): %s", k.op, k.tool, reasons, strings.Join(what, "; ")))
		}
		if len(n.tooManyResultIn) > 0 {
			out = append(out, fmt.Sprintf("report: %s tool %s has more results than one run of %s can hold: left out of it",
				k.op, k.tool, strings.Join(n.tooManyResultIn, ", ")))
		}
	}
	return append(out, findings...)
}

func reasonStrings(reasons []report.Reason) []string {
	out := make([]string, len(reasons))
	for i, r := range reasons {
		out[i] = string(r)
	}
	return out
}

func omittedFindings(format string, omitted []common.OmittedFindings) string {
	counts := map[string]int{}
	total := 0
	for _, o := range omitted {
		counts[o.Reason] += o.Count
		total += o.Count
	}
	if total == 0 {
		return ""
	}
	var parts []string
	if n := counts[common.OmittedNoFile]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d without a file", n))
	}
	if n := counts[common.OmittedOutside]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d outside the repository", n))
	}
	noun := "findings"
	if total == 1 {
		noun = "finding"
	}
	return fmt.Sprintf("report: %s left out %d %s it cannot place (%s); its completeness companion counts them",
		format, total, noun, strings.Join(parts, ", "))
}
