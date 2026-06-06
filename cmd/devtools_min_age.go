package cmd

import (
	"fmt"

	"github.com/datamitsu/datamitsu/internal/runtimeconfig"

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

// minAgeBanner returns a human-readable description of the effective minimum
// release age for the status output of pull-* commands. A value <= 0 means age
// filtering is disabled.
func minAgeBanner(minAge int) string {
	if minAge <= 0 {
		return "disabled"
	}
	return fmt.Sprintf("%d minutes", minAge)
}

// noReleaseOldEnoughErr builds the hard error returned when no release/version
// for name satisfies the minAge cutoff and there is nothing safe to fall back
// to (e.g. a brand-new app). It points the user at the --min-age 0 bypass.
func noReleaseOldEnoughErr(name string, minAge int) error {
	return fmt.Errorf(
		"no release for %s is at least %d minutes old; use --min-age 0 to bypass age filtering",
		name, minAge,
	)
}
