package cmd

import (
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/runner"
	"github.com/datamitsu/datamitsu/internal/sponsor"

	"github.com/spf13/cobra"
)

var (
	lintExplain       string
	lintFileScoped    bool
	lintSelectedTools string
)

var lintCmd = &cobra.Command{
	Use:   "lint [files...]",
	Short: "Run lint operations on files",
	Long: `Runs configured lint tools on specified files or the entire project.
Tools are executed based on priority and file patterns, with automatic parallelization
for non-overlapping tasks.

Use --explain to see the execution plan without running:
  --explain          Show brief plan (summary mode, default)
  --explain=summary  Show brief plan
  --explain=detailed Show detailed plan with file lists
  --explain=json     Output plan in JSON format`,
	RunE: runLint,
}

func init() {
	lintCmd.Flags().StringVar(&lintExplain, "explain", "",
		"Show execution plan without running (summary|detailed|json)")
	lintCmd.Flags().Lookup("explain").NoOptDefVal = "summary"
	lintCmd.Flags().BoolVar(&lintFileScoped, "file-scoped", false, "Only process git staged files")
	lintCmd.Flags().StringVar(&lintSelectedTools, "tools", "", "Comma-separated list of tools to run (for debugging)")
	rootCmd.AddCommand(lintCmd)
}

func runLint(cmd *cobra.Command, args []string) error {
	err := runner.Run(config.OpLint, args, lintExplain, lintFileScoped, lintSelectedTools, func() (*config.Config, string, error) {
		cfg, _, _, err := loadConfig()
		return cfg, "", err
	})
	if err == nil && lintExplain == "" {
		sponsor.New(env.GetCachePath()).MaybePrint(false)
	}
	return err
}
