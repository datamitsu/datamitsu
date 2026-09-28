package common

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/datamitsu/datamitsu/internal/report"
)

// CompanionSchema names the shape of a completeness companion.
const CompanionSchema = "datamitsu.completeness/1"

// CompanionSuffix is what a companion's path adds to its report's.
const CompanionSuffix = ".completeness.json"

// CompanionPath is where the companion of the report at path is written.
func CompanionPath(path string) string { return path + CompanionSuffix }

// Companion says how complete a report is, for a format — a CodeClimate array,
// a Checkstyle tree, an rdjsonl stream, a JUnit document — whose shape has no
// place to say so: read alone, such a report cannot tell a clean run from a
// tool that did not cover everything. A job reads Complete before it publishes
// the report.
type Companion struct {
	Schema string `json:"schema"`
	// Format is the report the companion describes.
	Format string `json:"format"`
	// Complete is true when every tool the report lists is complete and the
	// run left nothing out that the report covers.
	Complete bool `json:"complete"`
	// Incomplete are the run-level reasons that apply to the report.
	Incomplete []report.Reason `json:"incomplete"`
	// Tools are the tool runs the report was written from.
	Tools []CompanionTool `json:"tools"`
	// Omitted counts the findings the format could not carry, by tool and
	// reason.
	Omitted []OmittedFindings `json:"omitted,omitempty"`
	// Exports are the reports the run was asked for.
	Exports []report.Export `json:"exports"`
}

// CompanionTool is one tool run a report was written from.
type CompanionTool struct {
	Operation  string          `json:"operation"`
	Name       string          `json:"name"`
	Complete   bool            `json:"complete"`
	Incomplete []report.Reason `json:"incomplete"`
}

// OmittedFindings counts the findings of one tool a format left out for one
// reason.
type OmittedFindings struct {
	Tool string `json:"tool"`
	// Reason is no-file, for a finding that names no file, or
	// outside-repository.
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// Reasons a format leaves a finding out.
const (
	OmittedNoFile  = "no-file"
	OmittedOutside = "outside-repository"
)

// ReasonTooManyResults is why a format left out a complete tool it cannot
// hold: more results than one of its runs takes.
const ReasonTooManyResults = "too-many-results"

// NewCompanion describes a report of format written from ops of run. A
// run-level reason applies unless it is only that an operation the report
// does not hold did not run.
func NewCompanion(run *report.Run, format string, ops []*report.Operation, omitted []OmittedFindings) Companion {
	c := Companion{
		Schema:     CompanionSchema,
		Format:     format,
		Incomplete: []report.Reason{},
		Tools:      []CompanionTool{},
		Omitted:    omitted,
		Exports:    append([]report.Export{}, run.Exports...),
	}
	for _, r := range run.Incomplete {
		if r != report.ReasonOperationSkipped {
			c.Incomplete = append(c.Incomplete, r)
		}
	}
	complete := true
	for _, op := range ops {
		if !op.Ran && !slices.Contains(c.Incomplete, report.ReasonOperationSkipped) {
			c.Incomplete = append(c.Incomplete, report.ReasonOperationSkipped)
		}
		for _, tr := range op.Tools {
			c.Tools = append(c.Tools, CompanionTool{
				Operation:  op.Name,
				Name:       tr.Name,
				Complete:   tr.Complete,
				Incomplete: append([]report.Reason{}, tr.Incomplete...),
			})
			complete = complete && tr.Complete
		}
	}
	// A document read back without its run-level fields holds no evidence
	// that the run covered everything.
	if run.Selection.Mode == "" && !slices.Contains(c.Incomplete, report.ReasonNarrowedSelection) {
		c.Incomplete = append(c.Incomplete, report.ReasonNarrowedSelection)
	}
	slices.Sort(c.Incomplete)
	c.Complete = complete && len(c.Incomplete) == 0 && len(ops) > 0
	return c
}

// Write writes the companion indented by two spaces, with a final newline.
func (c Companion) Write(w io.Writer) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode completeness: %w", err)
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write completeness: %w", err)
	}
	return nil
}

// IncompleteTools are the tool runs of ops whose completeness is not
// established, which a companion flags.
func (c Companion) IncompleteTools() []CompanionTool {
	var out []CompanionTool
	for _, t := range c.Tools {
		if !t.Complete {
			out = append(out, t)
		}
	}
	return out
}
