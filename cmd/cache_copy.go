package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/datamitsu/datamitsu/internal/cache"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/exitcode"

	"github.com/spf13/cobra"
)

var (
	cacheCopySource     string
	cacheCopyDryRun     bool
	cacheCopyToolCaches bool
)

var cacheCopyCmd = &cobra.Command{
	Use:   "copy",
	Short: "Copy cache from another worktree",
	Long: `Copy the lint/fix results and verdicts of another project root into this
one as an independent deep copy (no links). Destination is the current project.
The source-mode farm and evaluated configs are rebuilt on the next run.
Tool caches are not copied unless --tool-caches is given: their contents belong
to the tools and are not guaranteed to be portable between roots.
Works when the source has no cache yet: prints a note and exits 0.`,
	Args: usageArgs(cobra.NoArgs),
	RunE: runCacheCopy,
}

func init() {
	cacheCopyCmd.Flags().StringVar(&cacheCopySource, "source", "",
		"Project root (for example the worktree to copy from) (required)")
	_ = cacheCopyCmd.MarkFlagRequired("source")
	cacheCopyCmd.Flags().BoolVar(&cacheCopyDryRun, "dry-run", false,
		"Show what would be copied without copying")
	cacheCopyCmd.Flags().BoolVar(&cacheCopyToolCaches, "tool-caches", false,
		"Also copy the tools' own cache directories (best effort; the contents are tool-owned and not guaranteed portable between roots)")
	cacheCmd.AddCommand(cacheCopyCmd)
}

func runCacheCopy(cmd *cobra.Command, args []string) error {
	// An empty or missing source directory is a caller mistake, not a missing
	// cache: the source must name a place a cache could live before "nothing
	// to copy" is an answer about that place.
	if strings.TrimSpace(cacheCopySource) == "" {
		return exitcode.UsageErrorf("--source must not be empty")
	}
	source, err := filepath.Abs(cacheCopySource)
	if err != nil {
		return fmt.Errorf("failed to resolve --source: %w", err)
	}
	source = filepath.Clean(source)
	info, statErr := os.Stat(source)
	switch {
	case os.IsNotExist(statErr):
		return exitcode.UsageErrorf("--source %q does not exist", cacheCopySource)
	case statErr != nil:
		// A path that exists but cannot be inspected is the filesystem's
		// failure, not the caller's; usage errors are reserved for arguments
		// no invocation could act on.
		return fmt.Errorf("failed to inspect --source: %w", statErr)
	case !info.IsDir():
		return exitcode.UsageErrorf("--source %q is not a directory", cacheCopySource)
	}

	srcRoot, err := resolveProjectRoot(cmd, source)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get cwd: %w", err)
	}
	dstRoot, err := resolveProjectRoot(cmd, cwd)
	if err != nil {
		return err
	}

	report, err := cache.CopyProject(env.GetCachePath(), srcRoot, dstRoot, cache.CopyOptions{
		DryRun:     cacheCopyDryRun,
		ToolCaches: cacheCopyToolCaches,
	})
	switch {
	case errors.Is(err, cache.ErrSameProject):
		fmt.Println("Source and destination are the same project — nothing to copy.")
		return nil
	case errors.Is(err, cache.ErrNoSourceCache):
		fmt.Printf("No datamitsu cache found for %s — nothing to copy.\n", srcRoot)
		return nil
	case err != nil:
		return fmt.Errorf("failed to copy cache: %w", err)
	}

	if cacheCopyDryRun {
		fmt.Printf("Would copy cache from %s to %s\n", srcRoot, dstRoot)
	} else {
		fmt.Printf("✅ Copied cache from %s to %s\n", srcRoot, dstRoot)
	}
	switch report.Toolstate {
	case cache.ToolstateCopied:
		fmt.Printf("lint/fix results: %d file entries and %d verdicts imported\n",
			report.ImportedEntries, report.ImportedVerdicts)
	case cache.ToolstateMerged:
		fmt.Printf("lint/fix results: %d file entries and %d verdicts merged into the destination's toolstate\n",
			report.ImportedEntries, report.ImportedVerdicts)
	case cache.ToolstateKeptIncompatible:
		fmt.Println("lint/fix results: not copied — the destination's toolstate belongs to a different configuration, version or tool selection")
	case cache.ToolstateSkippedInvalid:
		fmt.Println("lint/fix results: not copied — the source toolstate is unreadable")
	case cache.ToolstateAbsent:
		fmt.Println("lint/fix results: none in the source")
	}
	if cacheCopyToolCaches {
		fmt.Printf("tool caches: %d files (%s), %d already present, %d skipped\n",
			report.ToolCacheFiles, humanBytes(report.ToolCacheBytes),
			report.SkippedExisting, report.SkippedSpecial)
	}
	for _, w := range report.Warnings {
		fmt.Printf("⚠️  %s\n", w)
	}
	fmt.Println("evaluated configs and the source-mode farm rebuild on the next run")
	return nil
}

// humanBytes renders a byte count the way cache reports quote copy volume.
func humanBytes(n int64) string {
	const kiB, miB = 1 << 10, 1 << 20
	switch {
	case n >= miB:
		return fmt.Sprintf("%.1f MiB", float64(n)/miB)
	case n >= kiB:
		return fmt.Sprintf("%.1f KiB", float64(n)/kiB)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
