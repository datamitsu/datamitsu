package cmd

import (
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
	"fmt"

	"github.com/spf13/cobra"
)

// minAgeFlagDefault is the sentinel meaning "use the global effective minimum
// release age". A value of 0 disables age filtering; a positive value is custom.
const minAgeFlagDefault = -1

// addMinAgeFlag registers the shared --min-age flag on cmd and returns a pointer
// to the bound value. The default (-1) resolves to the global effective minimum
// release age via resolveMinAge.
func addMinAgeFlag(cmd *cobra.Command) *int {
	return cmd.Flags().Int("min-age", minAgeFlagDefault, minAgeDescription())
}

// resolveMinAge resolves the --min-age flag value against the effective runtime
// config. The sentinel default (-1, or any negative) resolves to
// eff.MinimumReleaseAgeMinutes; any value >= 0 is returned as-is (0 disables
// filtering, positive is a custom cutoff). This reads from runtimeconfig.Effective,
// not from env directly, so it shares the single source of truth for effective values.
func resolveMinAge(flagValue int, eff runtimeconfig.Effective) int {
	if flagValue < 0 {
		return eff.MinimumReleaseAgeMinutes
	}
	return flagValue
}

// minAgeDescription returns the help text for the --min-age flag.
func minAgeDescription() string {
	return fmt.Sprintf(
		"Minimum release age in minutes before a version is eligible "+
			"(-1 = use global default of %d, 0 = disable age filtering, positive = custom)",
		runtimeconfig.MinimumReleaseAgeMinutes,
	)
}
