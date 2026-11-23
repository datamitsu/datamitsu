package runner

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	clr "github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/textpos"
	"github.com/datamitsu/datamitsu/internal/tooling"
	"github.com/datamitsu/datamitsu/internal/ui"
)

// The core config-placement check: Datamitsu reads its configuration only at
// the git root, so any of its reserved files below that root is a dead,
// misleading artifact. The check rides the run's one repository walk and
// reports through the same report surface as a tool (pseudo tool run
// "datamitsu", code "config-placement"); it never moves or deletes anything.

const (
	// placementTool is the pseudo tool name the check reports under. Fingerprints
	// namespace by it, so code alone stays unambiguous.
	placementTool = config.BuiltinToolName
	// placementCode is the rule code; full name on tool+code surfaces:
	// datamitsu/config-placement.
	placementCode = "config-placement"
	// placementMessage is every placement finding's message: the violation is
	// the placement, not the file's content, which is never read.
	placementMessage = "Datamitsu config must be located at the repository git root"
)

// reservedDirCollector returns the walk's directory callback and an accessor
// for the directories it saw that carry a reserved name. The walk fans out
// across goroutines, so the collector synchronizes.
func reservedDirCollector() (func(string), func() []string) {
	var mu sync.Mutex
	var dirs []string
	onDir := func(d string) {
		if filepath.Base(d) == config.DatamitsuDirName {
			mu.Lock()
			dirs = append(dirs, d)
			mu.Unlock()
		}
	}
	return onDir, func() []string { return dirs }
}

// checkConfigPlacement turns the violations the walk found below the git root
// into diagnostics, marks those the baseline holds, and gates the rest: the
// core severity contract is always active, so a violation fails the run at
// every failOn setting. The artifacts' content is never read — identity rests
// on the path.
func (sc *sharedContext) checkConfigPlacement(violations []string) {
	if len(violations) == 0 {
		sc.placement = nil
		return
	}
	diags := make([]diagnostic.Diagnostic, 0, len(violations))
	for _, v := range violations {
		diags = append(diags, diagnostic.Diagnostic{
			File: v, Row: 1, Col: 1,
			Severity: diagnostic.SeverityError,
			Source:   placementTool, Code: placementCode,
			Message: placementMessage,
			// Row basis, no line read: the artifact may be a directory, and
			// placement identity must not move when content does.
			Anchor: &diagnostic.Anchor{Basis: report.BasisRow, LineHash: report.RowHash(1), Precision: textpos.Unknown},
		})
	}
	if sc.baselineMatcher != nil {
		task := tooling.Task{ToolName: placementTool, Operation: config.OpLint}
		proc := &tooling.ProcessResult{Diagnostics: diags}
		sc.baselineMatcher.Mark(task, proc)
	}
	for i := range diags {
		diags[i].Reported = !diags[i].Baselined
		diags[i].Gates = diags[i].Reported
	}
	sc.placement = diags
}

// printCoreFindings renders built-in results using the tool diagnostic format.
func printCoreFindings(diags []diagnostic.Diagnostic, nameWidth int, baseDir string) {
	if ui.Muted() {
		return
	}
	shown := make([]diagnostic.Diagnostic, 0, len(diags))
	baselined, changes := 0, 0
	gated, placementOnly := false, true
	for _, d := range diags {
		if d.Baselined {
			baselined++
			continue
		}
		if !d.Reported {
			continue
		}
		gated = gated || d.Gates
		changes += boolInt(d.Severity == diagnostic.SeverityInfo)
		placementOnly = placementOnly && d.Code == placementCode
		shown = append(shown, d)
	}
	if len(shown) == 0 {
		return
	}
	fmt.Println(clr.Faint("┃"))
	status, nameDisplay := clr.Green("✓"), clr.Bold(placementTool)
	if gated {
		status, nameDisplay = clr.Red("✗"), clr.Red(placementTool)
	}
	pad := max(nameWidth-utf8.RuneCountInString(placementTool), 0) + 2
	line := clr.Faint("┃ ") + status + " " + nameDisplay + strings.Repeat(" ", pad) + ui.FormatDurationShort(0)
	coloredLabel := clr.Faint("built-in checks")
	switch {
	case placementOnly:
		coloredLabel = clr.Faint("config placement")
	case gated:
		coloredLabel = clr.Red(fmt.Sprintf("%d finding(s)", len(shown)))
	case changes > 0:
		coloredLabel = clr.Cyan(fmt.Sprintf("%d change(s)", changes))
	}
	line += "  " + coloredLabel
	if baselined > 0 {
		line += "  " + clr.Faint(fmt.Sprintf("· %d baselined", baselined))
	}
	fmt.Println(toolText(line))
	for _, d := range shown {
		if strings.ContainsAny(d.File+d.Message, "\r\n") {
			head, detail, _ := strings.Cut(d.Message, "\n")
			d.Message = head
			text := formatDiagnosticRelativeTo(d, baseDir)
			if detail != "" {
				text += "\n" + detail
			}
			printFramed(clr.Faint, "", text)
			continue
		}
		fmt.Println(toolText(clr.Faint("┃ ") + "    " + formatDiagnosticRelativeTo(d, baseDir)))
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
