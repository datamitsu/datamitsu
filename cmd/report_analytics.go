package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/datamitsu/datamitsu/internal/cienv"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/exitcode"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/report/diff"
	"github.com/datamitsu/datamitsu/internal/report/render"
	"github.com/datamitsu/datamitsu/internal/runner"

	"github.com/spf13/cobra"
)

var reportBaselineOutput string

var reportBaselineCmd = &cobra.Command{
	Use:   "baseline <run.json>",
	Short: "Write the baseline of a run's own JSON report",
	Long: `Write the fingerprints of every finding a run reported as a baseline,
datamitsu.baseline/1, which lint and check take with --baseline: a later run
then gates only on findings the baseline does not hold.

A baseline is made on purpose, never automatically; make it again when the
findings it holds are fixed, so it stops hiding them. A run that was not
complete gives a baseline with fewer fingerprints, which only suppresses fewer
findings: it is written, with a warning, and its source says why.

  datamitsu lint --report json=out/run.json
  datamitsu report baseline out/run.json --output .datamitsu-baseline.json`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: runReportBaseline,
}

var (
	reportDiffFormat string
	reportDiffOutput string
)

var reportDiffCmd = &cobra.Command{
	Use:   "diff <before.json> <after.json>",
	Short: "Compare the findings of two runs' own JSON reports",
	Long: `Compare the findings of two runs by fingerprint, tool by tool: new (in the
second run only), unchanged, moved (same fingerprint, another row), fixed (in
the first run only, where the second run's tool is complete over the whole
repository) and unknown (in the first run only, where it is not — with the
tool's reasons). A tool one run does not hold is unobserved, with every finding
it reported. Each report contributes the operation a report lists: lint, or fix
for a fix-only run.

  datamitsu report diff main.json branch.json
  datamitsu report diff main.json branch.json --format markdown`,
	Args: usageArgs(cobra.ExactArgs(2)),
	RunE: runReportDiff,
}

func init() {
	reportBaselineCmd.Flags().StringVar(&reportBaselineOutput, "output", render.Stdout, "Where to write the baseline; - is stdout")
	reportDiffCmd.Flags().StringVar(&reportDiffFormat, "format", diff.FormatJSON,
		"The format to write: "+strings.Join(diff.Formats(), " or "))
	reportDiffCmd.Flags().StringVar(&reportDiffOutput, "output", render.Stdout, "Where to write it; - is stdout")
	reportCmd.AddCommand(reportBaselineCmd, reportDiffCmd)
}

func runReportBaseline(cmd *cobra.Command, args []string) error {
	run, err := readReport(args[0])
	if err != nil {
		return err
	}
	if run.Fingerprint != report.FingerprintVersion {
		return exitcode.UsageErrorf("report baseline: %s holds fingerprints of version %q, and this build computes %q",
			args[0], run.Fingerprint, report.FingerprintVersion)
	}
	if !run.Complete {
		logger.Logger.Warn(fmt.Sprintf("report baseline: the run of %s is incomplete (%s): the baseline holds only "+
			"the fingerprints it found, and suppresses no finding it missed", args[0], reasonList(run)))
	}
	created := time.Now().UTC()
	if t, ok := env.SourceDateEpoch(); ok {
		created = t
	}
	data, err := json.MarshalIndent(report.NewBaseline(run, created), "", "  ")
	if err != nil {
		return fmt.Errorf("encode baseline: %w", err)
	}
	return writeOutput(cmd.OutOrStdout(), "baseline", "json", reportBaselineOutput, append(data, '\n'))
}

func runReportDiff(cmd *cobra.Command, args []string) error {
	if !slices.Contains(diff.Formats(), reportDiffFormat) {
		return exitcode.UsageErrorf("invalid --format: %q (must be %s)", reportDiffFormat, strings.Join(diff.Formats(), " or "))
	}
	before, err := readReport(args[0])
	if err != nil {
		return err
	}
	after, err := readReport(args[1])
	if err != nil {
		return err
	}
	if err := diff.Comparable(before, after); err != nil {
		return exitcode.UsageErrorf("report diff: %w", err)
	}
	var out bytes.Buffer
	if err := diff.Write(&out, diff.Diff(before, after), reportDiffFormat); err != nil {
		return err
	}
	return writeOutput(cmd.OutOrStdout(), "diff", reportDiffFormat, reportDiffOutput, out.Bytes())
}

// writeOutput writes a document of format to stdout or, atomically, to path;
// a file that could not be written is an artifact asked for and not written
// (exit 5). On stdout in a CI that reads commands anywhere in a line, the
// document spells their openings so that none runs (runner.CommandGuard).
func writeOutput(stdout io.Writer, what, format, path string, data []byte) error {
	if path == render.Stdout {
		stdoutOwned = true
		ci, _ := cienv.Current()
		if guard := runner.CommandGuard("", ci.Vendor); guard != nil {
			data = guard(format, data)
		}
		if _, err := stdout.Write(data); err != nil {
			return exitcode.ExportError{Err: fmt.Errorf("%s: stdout: %w", what, err)}
		}
		return nil
	}
	if err := render.WriteFile(path, data); err != nil {
		return exitcode.ExportError{Err: fmt.Errorf("%s: %s: %w", what, path, err)}
	}
	return nil
}

// reasonList names why a run is incomplete: its own reasons and its tools'.
func reasonList(run *report.Run) string {
	return reasonNames(report.IncompleteReasons(run))
}

func reasonNames(reasons []report.Reason) string {
	if len(reasons) == 0 {
		return "completeness not established"
	}
	out := make([]string, len(reasons))
	for i, r := range reasons {
		out[i] = string(r)
	}
	return strings.Join(out, ", ")
}
