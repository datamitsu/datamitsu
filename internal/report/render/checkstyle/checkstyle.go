// Package checkstyle renders a report.Run as Checkstyle XML, which Jenkins'
// Warnings Next Generation plugin and many other consumers read and compare
// with a reference build to tell new findings from fixed ones.
//
// One <file> per file, one <error> per finding of the listed operation, every
// level; a finding without a file, the synthetic finding of a tool that failed
// without a parsable one included, goes under <file name="">. Because a
// consumer compares builds, the report is refused for a narrowed run, and an
// incomplete tool is kept and named in the completeness companion.
package checkstyle

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/report/render/common"
)

// Renderer writes Checkstyle XML.
type Renderer struct{}

// Name is the format's name in --report.
func (Renderer) Name() string { return "checkstyle" }

// Options is empty: the document has no variant to choose.
func (Renderer) Options() []string { return nil }

// OmitsIncompleteTools is false: the document keeps a tool that is not
// complete, so a narrowed run refuses it.
func (Renderer) OmitsIncompleteTools() bool { return false }

// Companion describes the listed operation.
func (Renderer) Companion(run *report.Run, _ map[string]string) common.Companion {
	var ops []*report.Operation
	if op := common.ListedOperation(run); op != nil {
		ops = append(ops, op)
	}
	return common.NewCompanion(run, "checkstyle", ops, nil)
}

// Render writes every finding of the listed operation, one <file> per file.
func (Renderer) Render(w io.Writer, run *report.Run, _ map[string]string) error {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	fmt.Fprintf(&b, `<checkstyle version="%s">`+"\n", common.XMLAttr("datamitsu-"+run.Datamitsu.Version))
	open := false
	current := ""
	for _, l := range common.Findings(common.ListedOperation(run)) {
		f := l.Finding
		if !open || f.Location.Path != current {
			if open {
				b.WriteString("  </file>\n")
			}
			current, open = f.Location.Path, true
			fmt.Fprintf(&b, `  <file name="%s">`+"\n", common.XMLAttr(current))
		}
		b.WriteString("    <error")
		r := common.RegionOf(f.Location, common.Chars)
		if r.Line > 0 {
			b.WriteString(` line="` + strconv.Itoa(r.Line) + `"`)
		}
		if r.Col > 0 {
			b.WriteString(` column="` + strconv.Itoa(r.Col) + `"`)
		}
		fmt.Fprintf(&b, ` severity="%s" message="%s" source="%s"/>`+"\n",
			Severity(f.Severity), common.XMLAttr(f.Message), common.XMLAttr(common.Rule(f)))
	}
	if open {
		b.WriteString("  </file>\n")
	}
	b.WriteString("</checkstyle>\n")
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write checkstyle: %w", err)
	}
	return nil
}

// Severity is the Checkstyle severity of a core severity: info and hint are
// both info.
func Severity(severity string) string {
	switch config.Severity(severity) {
	case config.SeverityError:
		return "error"
	case config.SeverityWarning:
		return "warning"
	case config.SeverityInfo, config.SeverityHint:
		return "info"
	}
	return "info"
}
