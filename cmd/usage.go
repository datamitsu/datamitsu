package cmd

import (
	"github.com/datamitsu/datamitsu/internal/exitcode"

	"github.com/spf13/cobra"
)

// flagUsageError makes a flag cobra could not parse a usage error.
func flagUsageError(_ *cobra.Command, err error) error {
	return exitcode.UsageError{Err: err}
}

// usageArgs makes a positional-argument check's refusal a usage error.
func usageArgs(check cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := check(cmd, args); err != nil {
			return exitcode.UsageError{Err: err}
		}
		return nil
	}
}
