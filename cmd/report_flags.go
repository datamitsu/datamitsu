package cmd

import (
	"fmt"
	"slices"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/exitcode"
	"github.com/datamitsu/datamitsu/internal/report/render"
	"github.com/datamitsu/datamitsu/internal/runner"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"

	"github.com/spf13/cobra"
)

const (
	reportUsage = "Write a report once the run ends, failed or not: <format>=<path>, or <format>=- for stdout " +
		"(repeatable; formats: json; turns fail-fast off; also via DATAMITSU_REPORT)"
	allowPartialUsage = "Write a report that lists findings for a narrowed run (named files, a subdirectory, " +
		"--tools, --file-scoped) instead of refusing it; the report keeps every reason it is incomplete " +
		"(also via DATAMITSU_ALLOW_PARTIAL)"
	eventsUsage = "Which findings --log-format jsonl carries as diagnostic events: diagnostics=reported " +
		"(at or above failOn, the default) or diagnostics=all (also via DATAMITSU_EVENTS)"
)

// Values of --events and DATAMITSU_EVENTS.
const (
	eventsReported = "diagnostics=reported"
	eventsAll      = "diagnostics=all"
)

// reportFlags are the report flags of fix, lint and check.
type reportFlags struct {
	reports      []string
	allowPartial bool
	events       string
}

func addReportFlags(cmd *cobra.Command, flags *reportFlags) {
	// StringArray, not StringSlice: a report's options may hold commas.
	cmd.Flags().StringArrayVar(&flags.reports, "report", nil, reportUsage)
	cmd.Flags().BoolVar(&flags.allowPartial, "allow-partial", false, allowPartialUsage)
	cmd.Flags().StringVar(&flags.events, "events", "", eventsUsage)
}

// failFastWithReport refuses a run that is asked both to stop at the first
// failure and to list what it found: the list could not be complete. source
// names where fail-fast was asked for.
func failFastWithReport(source string) error {
	return exitcode.UsageErrorf("--report cannot be combined with %s: "+
		"a run that stops at the first failing tool cannot list every finding", source)
}

// applyReports resolves the reports of a run from the --report flags and
// DATAMITSU_REPORT as the effective runtime configuration holds it, and
// --allow-partial from its flag and DATAMITSU_ALLOW_PARTIAL. A value that
// cannot be read is a usage error, flag or variable alike: a report asked for
// and silently not written is what a pipeline cannot notice.
//
// A report turns fail-fast off: a run that stops at the first failure cannot
// report a complete list. Fail-fast asked for explicitly — the flag, or
// DATAMITSU_FAIL_FAST — together with a report is a usage error. It runs after
// applyFailFast, whose flag it reads from opts.
func applyReports(cmd *cobra.Command, flags reportFlags, opts *runner.Options) error {
	eff, err := runtimeconfig.Get()
	if err != nil {
		eff = runtimeconfig.Compute()
	}
	specs, err := render.ParseSpecs(flags.reports, eff.Report)
	if err != nil {
		return exitcode.UsageError{Err: err}
	}
	allowPartial, err := resolveAllowPartial(cmd, flags.allowPartial, eff.AllowPartial)
	if err != nil {
		return err
	}
	opts.Reports, opts.AllowPartial = specs, allowPartial
	if opts.AllDiagnostics, err = resolveEvents(cmd, flags.events, eff.Events); err != nil {
		return err
	}
	if len(specs) == 0 {
		return nil
	}

	switch {
	case opts.FailFast != nil && *opts.FailFast:
		return failFastWithReport("--fail-fast=true")
	case opts.FailFast == nil && eff.FailFastSource == runtimeconfig.FailFastSourceEnv && eff.FailFast:
		return failFastWithReport("DATAMITSU_FAIL_FAST=true")
	}
	opts.FailFast = new(false)

	// A report on stdout owns it: human output goes, and stderr carries the
	// JSON-L events instead.
	if slices.ContainsFunc(specs, render.Spec.Stdout) {
		stdoutOwned = true
		setJSONLStderr(true)
	}
	return nil
}

// resolveEvents reads --events, or DATAMITSU_EVENTS when the flag is not
// given; both are checked either way, since a mistyped value would silently
// narrow what a stream consumer sees.
func resolveEvents(cmd *cobra.Command, flag, fromEnv string) (bool, error) {
	all := false
	for _, v := range []struct {
		raw, source string
		use         bool
	}{
		{fromEnv, "DATAMITSU_EVENTS", fromEnv != ""},
		{flag, "--events", cmd.Flags().Changed("events")},
	} {
		if !v.use {
			continue
		}
		switch v.raw {
		case eventsAll:
			all = true
		case eventsReported:
			all = false
		default:
			return false, exitcode.UsageErrorf("invalid %s value: %q (must be %s or %s)", v.source, v.raw, eventsReported, eventsAll)
		}
	}
	return all, nil
}

// resolveAllowPartial takes the flag when given, otherwise the variable, which
// is checked either way.
func resolveAllowPartial(cmd *cobra.Command, flag bool, fromEnv string) (bool, error) {
	value := false
	if fromEnv != "" {
		v, ok := env.ParseBool(fromEnv)
		if !ok {
			return false, exitcode.UsageError{Err: fmt.Errorf(
				"invalid DATAMITSU_ALLOW_PARTIAL value: %q (must be true, false, 1 or 0)", fromEnv)}
		}
		value = v
	}
	if cmd.Flags().Changed("allow-partial") {
		value = flag
	}
	return value, nil
}
