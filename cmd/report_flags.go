package cmd

import (
	"slices"

	"github.com/datamitsu/datamitsu/internal/exitcode"
	"github.com/datamitsu/datamitsu/internal/report/render"
	"github.com/datamitsu/datamitsu/internal/runner"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"

	"github.com/spf13/cobra"
)

const reportUsage = "Write a report once the run ends, failed or not: <format>=<path>, or <format>=- for stdout " +
	"(repeatable; formats: json; also via DATAMITSU_REPORT)"

// reportFlags are the report flags of fix, lint and check.
type reportFlags struct {
	reports []string
}

func addReportFlags(cmd *cobra.Command, flags *reportFlags) {
	// StringArray, not StringSlice: a report's options may hold commas.
	cmd.Flags().StringArrayVar(&flags.reports, "report", nil, reportUsage)
}

// applyReports resolves the reports of a run from the --report flags and
// DATAMITSU_REPORT as the effective runtime configuration holds it. A value
// that cannot be read is a usage error, flag or variable alike: a report asked
// for and silently not written is what a pipeline cannot notice.
func applyReports(flags reportFlags, opts *runner.Options) error {
	eff, err := runtimeconfig.Get()
	if err != nil {
		eff = runtimeconfig.Compute()
	}
	specs, err := render.ParseSpecs(flags.reports, eff.Report)
	if err != nil {
		return exitcode.UsageError{Err: err}
	}
	opts.Reports = specs
	// A report on stdout owns it: human output goes, and stderr carries the
	// JSON-L events instead.
	if slices.ContainsFunc(specs, render.Spec.Stdout) {
		setJSONLStderr(true)
	}
	return nil
}
