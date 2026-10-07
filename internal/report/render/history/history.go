// Package history renders a report.Run as one line of a trend file,
// datamitsu.history/1: what the run covered and, per operation and tool, its
// counts and durations — never a finding or a path. The line is appended to
// the file, never replacing it: the file is the user's, kept wherever they
// name it, and a trend is built from it outside datamitsu.
package history

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/datamitsu/datamitsu/internal/report"
)

// Renderer writes the history line.
type Renderer struct{}

// Name is the format's name in --report.
func (Renderer) Name() string { return "history" }

// Options is empty: the line has no variant to choose.
func (Renderer) Options() []string { return nil }

// OmitsIncompleteTools is false: every tool is counted, with its
// completeness.
func (Renderer) OmitsIncompleteTools() bool { return false }

// ListsNoFindings marks a format of counts: a narrowed run writes it, since
// its selection is part of the line.
func (Renderer) ListsNoFindings() {}

// Appends marks a format whose file collects one line per run.
func (Renderer) Appends() {}

// Render writes the line, with its newline, in one write.
func (Renderer) Render(w io.Writer, run *report.Run, _ map[string]string) error {
	data, err := json.Marshal(report.History(run))
	if err != nil {
		return fmt.Errorf("encode history line: %w", err)
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write history line: %w", err)
	}
	return nil
}

// Decode reads one line the renderer wrote, refusing any other schema.
func Decode(line []byte) (report.HistoryLine, error) {
	var h report.HistoryLine
	if err := json.Unmarshal(line, &h); err != nil {
		return report.HistoryLine{}, fmt.Errorf("decode history line: %w", err)
	}
	if h.Schema != report.HistorySchema {
		return report.HistoryLine{}, fmt.Errorf("unsupported history schema %q (want %q)", h.Schema, report.HistorySchema)
	}
	return h, nil
}
