package cmd

import (
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/exitcode"
	"github.com/datamitsu/datamitsu/internal/runner"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"

	"github.com/spf13/cobra"
)

const failFastUsage = "Stop at the first failing tool; --fail-fast=false runs everything to the end (also via DATAMITSU_FAIL_FAST)"

// addFailFastFlag binds --fail-fast. A bool flag parses both the bare
// --fail-fast and --fail-fast=false.
func addFailFastFlag(cmd *cobra.Command, value *bool) {
	cmd.Flags().BoolVar(value, "fail-fast", runtimeconfig.FailFast, failFastUsage)
}

// applyFailFast sets the runner's FailFast option to the flag's value when the
// flag was given, and otherwise leaves it nil so the runner defers to
// DATAMITSU_FAIL_FAST. The variable is checked here because its getter falls
// back to the default on a value it does not accept.
func applyFailFast(cmd *cobra.Command, value bool, opts *runner.Options) error {
	if cmd.Flags().Changed("fail-fast") {
		opts.FailFast = &value
		return nil
	}
	if err := env.CheckFailFast(); err != nil {
		return exitcode.UsageError{Err: err}
	}
	return nil
}
