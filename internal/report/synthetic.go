package report

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/datamitsu/datamitsu/internal/textpos"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

// syntheticFinding stands for a process that ran, exited non-zero and left no
// finding, so a report that lists findings does not read the failure as
// clean. Its message is structured and carries none of the tool's output: for a
// security tool that output may be the secret it found. A process that was
// cancelled, never started or failed only on its threshold gets none — its
// state or its findings already say why.
func syntheticFinding(tool string, proc tooling.ProcessResult, category string) (Finding, bool) {
	if proc.State != tooling.ProcessRan || proc.ExitCode == nil || *proc.ExitCode == 0 || len(proc.Diagnostics) > 0 {
		return Finding{}, false
	}
	msg := fmt.Sprintf("%s exited %d without parsable findings", tool, *proc.ExitCode)
	if category == categorySecurity {
		msg = fmt.Sprintf("%s failed (exit %d); output withheld for a security tool", tool, *proc.ExitCode)
	}
	return Finding{
		FingerprintBasis: BasisNone,
		Tool:             tool,
		Source:           tool,
		Severity:         "error",
		Gates:            true,
		Kind:             kindSynthetic,
		Message:          msg,
		Location:         Location{Precision: string(textpos.Unknown)},
		Provenance:       provenanceSynthetic,
	}, true
}

// outputTail is what a failed invocation printed last, for the own JSON. A
// security tool's output is withheld: it may hold what the tool found.
func outputTail(proc tooling.ProcessResult, category string) string {
	failed := (proc.State == tooling.ProcessRan && !proc.Success) || proc.State == tooling.ProcessSetupFailed
	if !failed || category == categorySecurity || len(proc.OutputTail) == 0 {
		return ""
	}
	tail := tooling.StripCSI(proc.OutputTail)
	// The tail was cut at a byte count, possibly inside a character.
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}
	return strings.ToValidUTF8(string(tail), "�")
}
