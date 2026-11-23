package runner

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/textpos"
)

func anchorCoreFindings(findings []diagnostic.Diagnostic) error {
	for i := range findings {
		d := &findings[i]
		if d.File == "" || d.Row < 1 {
			continue
		}
		content, err := os.ReadFile(d.File)
		if err != nil {
			return fmt.Errorf("reading bundled finding path %q: %w", d.File, err)
		}
		lines := bytes.Split(content, []byte{'\n'})
		if d.Row > len(lines) {
			d.Anchor = &diagnostic.Anchor{Basis: report.BasisRow, LineHash: report.RowHash(d.Row), Precision: textpos.Unknown}
			continue
		}
		d.Anchor = &diagnostic.Anchor{Basis: report.BasisLine, LineHash: report.LineHash(lines[d.Row-1]), Precision: textpos.Unknown}
	}
	return nil
}

func coreFixFinding(root, relative string) diagnostic.Diagnostic {
	return diagnostic.Diagnostic{
		File: filepath.Join(root, filepath.FromSlash(relative)), Row: 1, Col: 1, EndRow: 1, EndCol: 1,
		Severity: diagnostic.SeverityInfo, Source: placementTool, Code: "datamitsuignore-normalized",
		Message: "normalized .datamitsuignore formatting", Reported: true,
		Anchor: &diagnostic.Anchor{Basis: report.BasisRow, LineHash: report.RowHash(1), Precision: textpos.Unknown},
	}
}

func coreFindingFailure(findings []diagnostic.Diagnostic) error {
	if len(findings) == 0 {
		return nil
	}
	placementOnly := true
	for _, finding := range findings {
		placementOnly = placementOnly && finding.Code == placementCode
	}
	if placementOnly {
		return fmt.Errorf("datamitsu: %d config placement violation(s) below the git root", len(findings))
	}
	return fmt.Errorf("datamitsu: %d built-in finding(s)", len(findings))
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}
