package cmd

import (
	"github.com/datamitsu/datamitsu/internal/exitcode"

	"github.com/spf13/cobra"
)

func flagUsageError(_ *cobra.Command, err error) error {
	return exitcode.UsageError{Err: err}
}

// validateFlagConstraints runs cobra's required-flag and flag-group checks
// ahead of cobra, which would return their errors without a code. It is the
// root's persistent pre-run hook; lsp and source declare their own, which
// replaces it for their subtrees, and declare no such constraints.
func validateFlagConstraints(cmd *cobra.Command, _ []string) error {
	if err := cmd.ValidateRequiredFlags(); err != nil {
		return exitcode.UsageError{Err: err}
	}
	if err := cmd.ValidateFlagGroups(); err != nil {
		return exitcode.UsageError{Err: err}
	}
	return nil
}

func usageArgs(check cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := check(cmd, args); err != nil {
			return exitcode.UsageError{Err: err}
		}
		return nil
	}
}
