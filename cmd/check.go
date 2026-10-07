package cmd

import (
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/runner"
	"github.com/datamitsu/datamitsu/internal/sponsor"

	"github.com/spf13/cobra"
)

var (
	checkExplain       string
	checkFileScoped    bool
	checkSelectedTools string
	checkFailOnSkip    bool
	checkWidenTo       string
	checkRequireCov    string
	checkFailFast      bool
	checkFailOn        string
	checkReports       reportFlags
	checkBaseline      string
)

var checkCmd = &cobra.Command{
	Use:   "check [files...]",
	Short: "Run fix then lint operations on files",
	Long: `Runs fix followed by lint on specified files or the entire project,
reusing shared context (config, file listing, caches) for efficiency.
If fix fails, lint is skipped, unless --fail-fast=false runs it anyway.

Use --explain to see the execution plan without running:
  --explain          Show brief plan (summary mode, default)
  --explain=summary  Show brief plan
  --explain=detailed Show detailed plan with file lists
  --explain=json     Output plan in JSON format`,
	RunE: runCheck,
}

func init() {
	checkCmd.Flags().StringVar(&checkExplain, "explain", "",
		"Show execution plan without running (summary|detailed|json)")
	checkCmd.Flags().Lookup("explain").NoOptDefVal = "summary"
	checkCmd.Flags().BoolVar(&checkFileScoped, "file-scoped", false, "Only process git staged files")
	checkCmd.Flags().StringVar(&checkSelectedTools, "tools", "", "Comma-separated list of tools to run (for debugging)")
	checkCmd.Flags().StringVar(&checkWidenTo, "widen-to", "", "Limit how far work may widen beyond the selection (target|unit|repo)")
	checkCmd.Flags().StringVar(&checkRequireCov, "require-coverage", "", "Exit non-zero unless the run answered completely (unit|repo)")
	checkCmd.Flags().BoolVar(&checkFailOnSkip, "fail-on-skip", false, "Exit non-zero if any tool is skipped because its binary is unavailable for this platform")
	addFailFastFlag(checkCmd, &checkFailFast)
	addFailOnFlag(checkCmd, &checkFailOn)
	addReportFlags(checkCmd, &checkReports)
	addBaselineFlag(checkCmd, &checkBaseline)
	rootCmd.AddCommand(checkCmd)
}

func runCheck(cmd *cobra.Command, args []string) error {
	opts := runner.Options{WidenTo: checkWidenTo, RequireCoverage: checkRequireCov}
	if err := applyFailOn(cmd, checkFailOn, &opts); err != nil {
		return err
	}
	if err := applyFailFast(cmd, checkFailFast, &opts); err != nil {
		return err
	}
	if err := checkParseLimits(); err != nil {
		return err
	}
	if err := applyReports(cmd, checkReports, &opts); err != nil {
		return err
	}
	if err := applyAnnotations(cmd, checkReports, checkExplain, &opts); err != nil {
		return err
	}
	if err := applyOutput(cmd, checkReports, &opts); err != nil {
		return err
	}
	if err := applyBaseline(checkBaseline, &opts); err != nil {
		return err
	}
	err := runner.RunSequential(
		[]config.OperationType{config.OpFix, config.OpLint},
		args, checkExplain, checkFileScoped, checkSelectedTools, checkFailOnSkip,
		opts,
		func() (*config.Config, string, error) {
			cfg, _, _, err := loadConfig()
			return cfg, "", err
		},
	)
	if err == nil && checkExplain == "" {
		sponsor.New(env.GetCachePath()).MaybePrint(opts.Output == runner.OutputAgent)
	}
	return err
}
