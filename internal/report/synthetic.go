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

// outputTail is what a failed process printed last, for the own JSON and an
// agent's output: without ANSI sequences, and "" for a tool whose output is
// withheld (withholdsOutput). A tail cut from a longer output loses the start
// a secret the cut went through could have left (withoutFragment); the rest
// is masked by whoever prints it.
func outputTail(proc tooling.ProcessResult, withhold bool, secrets []string) string {
	failed := (proc.State == tooling.ProcessRan && !proc.Success) || proc.State == tooling.ProcessSetupFailed
	if !failed || withhold || len(proc.OutputTail) == 0 {
		return ""
	}
	text := string(tooling.StripCSI(proc.OutputTail))
	if len(proc.OutputTail) >= tooling.OutputTailBytes {
		text = withoutFragment(text, secrets)
	}
	// The tail was cut at a byte count, possibly inside a character.
	for len(text) > 0 && !utf8.RuneStart(text[0]) {
		text = text[1:]
	}
	return strings.ToValidUTF8(text, "�")
}
