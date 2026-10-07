// Package json renders a report.Run as datamitsu's own document,
// datamitsu.report/1: the model itself, indented by two spaces. It is the one
// format that carries everything the model holds.
package json

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/datamitsu/datamitsu/internal/report"
)

// Renderer writes the own JSON document.
type Renderer struct{}

// Name is the format's name in --report.
func (Renderer) Name() string { return "json" }

// Options is empty: the document has no variant to choose.
func (Renderer) Options() []string { return nil }

// OmitsIncompleteTools is false: the document lists every tool, with its
// completeness, instead of leaving one out.
func (Renderer) OmitsIncompleteTools() bool { return false }

// Render writes the document indented by two spaces, with a final newline.
func (Renderer) Render(w io.Writer, run *report.Run, _ map[string]string) error {
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	data = append(data, '\n')
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

// Decode reads a document the renderer wrote, refusing any other schema.
func Decode(r io.Reader) (*report.Run, error) {
	var run report.Run
	dec := json.NewDecoder(r)
	if err := dec.Decode(&run); err != nil {
		return nil, fmt.Errorf("decode report: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode report: data after the document")
	}
	if run.Schema != report.SchemaVersion {
		return nil, fmt.Errorf("unsupported report schema %q (want %q)", run.Schema, report.SchemaVersion)
	}
	return &run, nil
}
