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
	rootCmd.AddCommand(fixCmd)
}

func runFix(cmd *cobra.Command, args []string) error {
	err := runner.Run(config.OpFix, args, fixExplain, fixFileScoped, fixSelectedTools, func() (*config.Config, string, error) {
		cfg, _, _, err := loadConfig()
		return cfg, "", err
	})
	if err == nil && fixExplain == "" {
		sponsor.New(env.GetCachePath()).MaybePrint(false)
	}
	return err
}
