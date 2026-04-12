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
)

var checkCmd = &cobra.Command{
	Use:   "check [files...]",
	Short: "Run fix then lint operations on files",
	Long: `Runs fix followed by lint on specified files or the entire project,
reusing shared context (config, file listing, caches) for efficiency.
If fix fails, lint is skipped.

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
	rootCmd.AddCommand(checkCmd)
}

func runCheck(cmd *cobra.Command, args []string) error {
	err := runner.RunSequential(
		[]config.OperationType{config.OpFix, config.OpLint},
		args, checkExplain, checkFileScoped, checkSelectedTools,
		func() (*config.Config, string, error) {
			cfg, _, _, err := loadConfig()
			return cfg, "", err
		},
	)
	if err == nil && checkExplain == "" {
		sponsor.New(env.GetCachePath()).MaybePrint(false)
	}
	return err
}
