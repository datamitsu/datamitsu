package cmd

import (
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/runner"
	"github.com/datamitsu/datamitsu/internal/sponsor"

	"github.com/spf13/cobra"
)

var (
	fixExplain       string
	fixFileScoped    bool
	fixSelectedTools string
	fixFailOnSkip    bool
	fixWidenTo       string
	fixRequireCov    string
	fixFailFast      bool
	fixFailOn        string
	fixReports       reportFlags
)

var fixCmd = &cobra.Command{
	Use:   "fix [files...]",
	Short: "Run fix operations on files",
	Long: `Runs configured fix tools on specified files or the entire project.
Tools are executed based on priority and file patterns, with automatic parallelization
for non-overlapping tasks.

Use --explain to see the execution plan without running:
  --explain          Show brief plan (summary mode, default)
  --explain=summary  Show brief plan
  --explain=detailed Show detailed plan with file lists
  --explain=json     Output plan in JSON format`,
	RunE: runFix,
}

func init() {
	fixCmd.Flags().StringVar(&fixExplain, "explain", "",
		"Show execution plan without running (summary|detailed|json)")
	fixCmd.Flags().Lookup("explain").NoOptDefVal = "summary"
	fixCmd.Flags().BoolVar(&fixFileScoped, "file-scoped", false, "Only process git staged files")
	fixCmd.Flags().StringVar(&fixSelectedTools, "tools", "", "Comma-separated list of tools to run (for debugging)")
	fixCmd.Flags().BoolVar(&fixFailOnSkip, "fail-on-skip", false, "Exit non-zero if any tool is skipped because its binary is unavailable for this platform")
	fixCmd.Flags().StringVar(&fixWidenTo, "widen-to", "", "Limit how far work may widen beyond the selection (target|unit|repo)")
	fixCmd.Flags().StringVar(&fixRequireCov, "require-coverage", "", "Exit non-zero unless the run answered completely (unit|repo)")
	addFailFastFlag(fixCmd, &fixFailFast)
	addFailOnFlag(fixCmd, &fixFailOn)
	addReportFlags(fixCmd, &fixReports)
	rootCmd.AddCommand(fixCmd)
}

func runFix(cmd *cobra.Command, args []string) error {
	opts := runner.Options{WidenTo: fixWidenTo, RequireCoverage: fixRequireCov}
	if err := applyFailOn(cmd, fixFailOn, &opts); err != nil {
		return err
	}
	if err := applyFailFast(cmd, fixFailFast, &opts); err != nil {
		return err
	}
	if err := applyReports(fixReports, &opts); err != nil {
		return err
	}
	err := runner.Run(config.OpFix, args, fixExplain, fixFileScoped, fixSelectedTools, fixFailOnSkip,
		opts,
		func() (*config.Config, string, error) {
			cfg, _, _, err := loadConfig()
			return cfg, "", err
		})
	if err == nil && fixExplain == "" {
		sponsor.New(env.GetCachePath()).MaybePrint(false)
	}
	return err
}
