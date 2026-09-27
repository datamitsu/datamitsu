package cmd

import (
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/exitcode"
	"github.com/datamitsu/datamitsu/internal/runner"

	"github.com/spf13/cobra"
)

const failOnUsage = "Fail on findings at this level or above in every operation (error|warning|info|hint); " +
	"raises an operation's failOn, never lowers it (also via DATAMITSU_FAIL_ON)"

func addFailOnFlag(cmd *cobra.Command, value *string) {
	cmd.Flags().StringVar(value, "fail-on", "", failOnUsage)
}

// applyFailOn sets the runner's global failOn raise: the flag when given,
// otherwise DATAMITSU_FAIL_ON. Both are checked whether used or not, because a
// mistyped threshold read as "no raise" would turn a CI gate off silently.
func applyFailOn(cmd *cobra.Command, value string, opts *runner.Options) error {
	if raw := env.FailOn(); raw != "" {
		if _, err := config.ParseSeverity(raw); err != nil {
			return exitcode.UsageErrorf("invalid DATAMITSU_FAIL_ON value: %q (must be %s)", raw, config.SeverityChoices())
		}
		opts.FailOn = raw
	}
	if cmd.Flags().Changed("fail-on") {
		if _, err := config.ParseSeverity(value); err != nil {
			return exitcode.UsageErrorf("invalid --fail-on value: %q (must be %s)", value, config.SeverityChoices())
		}
		opts.FailOn = value
	}
	return nil
}
