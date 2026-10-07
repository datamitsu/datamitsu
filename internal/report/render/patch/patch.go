// Package patch renders the unified diffs a fix applied — those of the
// formatters that write their result on stdout, which the core writes into the
// file itself and so sees before and after — as one patch, in the order they
// were applied: a file two formatters changed one after the other has two
// hunks, in that order. A tool that writes its files itself leaves no patch;
// its files are in the report's changes.
package patch

import (
	"fmt"
	"io"
	"sort"

	"github.com/datamitsu/datamitsu/internal/report"
)

// Name is the format's name in --report.
const Name = "patch"

// Renderer writes the patches.
type Renderer struct{}

// Name is the format's name in --report.
func (Renderer) Name() string { return Name }

// Options is empty: the patch has no variant to choose.
func (Renderer) Options() []string { return nil }

// OmitsIncompleteTools is false: every patch the run captured is written.
func (Renderer) OmitsIncompleteTools() bool { return false }

// ListsNoFindings marks a format of changes, not findings: a narrowed run
// writes it.
func (Renderer) ListsNoFindings() {}

// Render writes every patch of every operation, in the order the steps that
// applied them ran; within a step, whose invocations changed disjoint files,
// by tool, then task, then file.
func (Renderer) Render(w io.Writer, run *report.Run, _ map[string]string) error {
	for _, op := range run.Operations {
		var invocations []report.Invocation
		for _, tr := range op.Tools {
			invocations = append(invocations, tr.Invocations...)
		}
		sort.SliceStable(invocations, func(i, j int) bool { return invocations[i].Step < invocations[j].Step })
		for _, inv := range invocations {
			for _, f := range inv.Files {
				if f.Patch == "" {
					continue
				}
				if _, err := io.WriteString(w, f.Patch); err != nil {
					return fmt.Errorf("write patch: %w", err)
				}
			}
		}
	}
	return nil
}
