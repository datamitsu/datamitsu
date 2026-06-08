package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/datamitsu/datamitsu/internal/dockerfile"

	"github.com/spf13/cobra"
)

var splitConfigOutput string

var splitConfigCmd = &cobra.Command{
	Use:   "split-config",
	Short: "Write one minimal per-stage config slice per app/runtime for layered builds",
	Long: `Write one minimal config slice per app and per runtime into a directory.

Each slice is a self-contained config that defines exactly one stage's target —
a single binary, a single runtime, or a single runtime-managed app plus the
runtime it installs under. A generated Dockerfile (see "devtools dockerfile")
runs this in a build stage and has every other stage load only its own slice, so
editing one app changes only that app's slice and invalidates only that app's
build cache instead of the entire image.

The config is read from the same sources as every other command (--config /
--before-config / auto-discovery).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runSplitConfig(commandContext(cmd))
	},
}

func init() {
	splitConfigCmd.Flags().StringVarP(&splitConfigOutput, "output", "o", "", "Output directory for the config slices (required)")
	_ = splitConfigCmd.MarkFlagRequired("output")
	devtoolsCmd.AddCommand(splitConfigCmd)
}

func runSplitConfig(ctx context.Context) error {
	cfg, _, _, err := loadConfigWithPaths(ctx, BeforeConfigPaths, NoAutoConfig, ConfigPaths)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if err := os.MkdirAll(splitConfigOutput, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	plan := dockerfile.BuildPlan(cfg.Apps, cfg.Runtimes)
	slices := dockerfile.BuildSlices(plan, cfg.Apps, cfg.Runtimes)
	for _, slice := range slices {
		text, renderErr := dockerfile.RenderSlice(slice.Config)
		if renderErr != nil {
			return fmt.Errorf("render slice %s: %w", slice.StageName, renderErr)
		}
		dst := filepath.Join(splitConfigOutput, slice.FileName)
		if err := writeFileAtomic(dst, []byte(text)); err != nil {
			return fmt.Errorf("write %s: %w", dst, err)
		}
	}

	_, _ = fmt.Fprintf(os.Stdout, "Wrote %d config slices to %s\n", len(slices), splitConfigOutput)
	return nil
}
