package cmd

import (
	"context"
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/managedconfig"
	"github.com/datamitsu/datamitsu/internal/project"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"
	"github.com/datamitsu/datamitsu/internal/traverser"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"
)

var (
	initDryRun            bool
	initAll               bool
	initSkipDownload      bool
	initFailOnDownloadErr bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize project - download binaries and run init commands",
	Long: `Downloads required binaries and runs initialization commands (like npm install, lefthook install, etc.)
By default, downloads only Required binaries with concurrency of 3 (configurable via DATAMITSU_CONCURRENCY env var).`,
	RunE: runInit,
}

func init() {
	initCmd.Flags().BoolVar(&initDryRun, "dry-run", false, "Show what would be done without making changes")
	initCmd.Flags().BoolVar(&initAll, "all", false, "Download all binaries (both required and optional)")
	initCmd.Flags().BoolVar(&initSkipDownload, "skip-download", false, "Skip binary downloads")
	initCmd.Flags().BoolVar(&initFailOnDownloadErr, "fail-on-download-error", false, "Stop init process if any binary download fails")
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Get cwd
	cwdPath, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get cwd: %w", err)
	}

	// Get root path
	rootPath, err := traverser.GetGitRoot(ctx, cwdPath)
	if err != nil {
		return fmt.Errorf("failed to get git root: %w", err)
	}

	if err := checkInitGitRoot(cwdPath, rootPath); err != nil {
		return err
	}

	// Load configuration
	cfg, _, _, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Detect project types
	detector := project.NewDetector(rootPath, cfg.ProjectTypes)
	projectTypes, err := detector.DetectAll(ctx)
	if err != nil {
		return fmt.Errorf("failed to detect project types: %w", err)
	}

	if len(projectTypes) == 0 {
		fmt.Println("⚠️  No project types detected")
	} else {
		fmt.Printf("📦 Detected project types: %v\n", projectTypes)
	}
	fmt.Println()

	if initDryRun {
		fmt.Println("🔍 DRY-RUN MODE - No changes will be made")
		fmt.Println()
	}

	rm := runtimemanager.New(cfg.Runtimes)
	binMgr := binmanager.New(cfg.Apps, cfg.Bundles, rm)

	// Install runtimes and download binaries if not skipped
	if !initSkipDownload && !initDryRun {
		if err := installRequiredRuntimes(rm, cfg, initAll); err != nil {
			return fmt.Errorf("failed to install runtimes: %w", err)
		}

		if err := downloadBinaries(binMgr); err != nil {
			return fmt.Errorf("failed to download binaries: %w", err)
		}
	}

	// Install runtime-managed apps that have links, then set up config links.
	// downloadBinaries only handles binary-type apps, so FNM/UV apps with links
	// must be installed separately before CreateDatamitsuLinks can resolve their
	// install roots.
	if !initSkipDownload && !initDryRun {
		if err := installRuntimeAppsWithLinks(binMgr, cfg, initAll); err != nil {
			return fmt.Errorf("failed to install runtime apps with links: %w", err)
		}
	}

	// Install bundles before setting up config links.
	// Inline-only bundles install regardless of --skip-download.
	// Bundles with external archives respect --skip-download.
	if !initDryRun {
		if err := installBundles(ctx, binMgr, initSkipDownload); err != nil {
			return fmt.Errorf("failed to install bundles: %w", err)
		}
	}

	// Set up config links even when --skip-download is used, so users with
	// already-cached apps can refresh symlinks without re-downloading.
	if err := setupConfigLinks(rootPath, cfg, binMgr, initDryRun); err != nil {
		return fmt.Errorf("failed to set up config links: %w", err)
	}

	// Run init commands
	if err := runInitCommands(ctx, rootPath, cwdPath, projectTypes, cfg, binMgr, initDryRun); err != nil {
		return fmt.Errorf("failed to run init commands: %w", err)
	}

	if !initDryRun {
		fmt.Println()
		fmt.Println("✅ Initialization complete")
	}

	return nil
}

func installRequiredRuntimes(rm *runtimemanager.RuntimeManager, cfg *config.Config, includeAll bool) error {
	runtimeNames := runtimemanager.CollectRequiredRuntimes(cfg.Apps, cfg.Runtimes, includeAll)
	if len(runtimeNames) == 0 {
		return nil
	}

	fmt.Println("📦 Installing runtimes...")
	fmt.Println()

	concurrency := env.GetConcurrency()

	stats, err := rm.InstallRuntimes(runtimeNames, concurrency)
	if err != nil {
		return err
	}

	total := len(stats.Downloaded) + len(stats.AlreadyCached) + len(stats.Skipped) + len(stats.Failed)
	if total == 0 {
		return nil
	}

	fmt.Println()
	fmt.Println("📊 Runtime Installation Summary:")
	fmt.Println()

	if len(stats.Downloaded) > 0 {
		fmt.Printf("  ✅ Downloaded (%d):\n", len(stats.Downloaded))
		for _, name := range stats.Downloaded {
			fmt.Printf("     • %s\n", name)
		}
		fmt.Println()
	}

	if len(stats.AlreadyCached) > 0 {
		fmt.Printf("  ♻️  Already installed (%d):\n", len(stats.AlreadyCached))
		for _, name := range stats.AlreadyCached {
			fmt.Printf("     • %s\n", name)
		}
		fmt.Println()
	}

	if len(stats.Skipped) > 0 {
		fmt.Printf("  ⏭️  Skipped - platform incompatible (%d):\n", len(stats.Skipped))
		for _, name := range stats.Skipped {
			fmt.Printf("     • %s\n", name)
		}
		fmt.Println()
	}

	if len(stats.Failed) > 0 {
		fmt.Printf("  ❌ Failed (%d):\n", len(stats.Failed))
		for _, result := range stats.Failed {
			fmt.Printf("     • %s: %v\n", result.Name, result.Error)
		}
		fmt.Println()
	}

	if initFailOnDownloadErr && len(stats.Failed) > 0 {
		return fmt.Errorf("failed to download %d runtime(s)", len(stats.Failed))
	}

	return nil
}

func downloadBinaries(binMgr *binmanager.BinManager) error {
	concurrency := env.GetConcurrency()

	stats, err := binMgr.InstallWithConcurrency(initAll, concurrency, initFailOnDownloadErr)

	if err != nil {
		return err
	}

	total := len(stats.Downloaded) + len(stats.AlreadyCached) + len(stats.Skipped) + len(stats.Failed)
	if total == 0 {
		return nil
	}

	fmt.Println()
	fmt.Println("📊 Installation Summary:")
	fmt.Println()

	if len(stats.Downloaded) > 0 {
		fmt.Printf("  ✅ Downloaded (%d):\n", len(stats.Downloaded))
		for _, name := range stats.Downloaded {
			fmt.Printf("     • %s\n", name)
		}
		fmt.Println()
	}

	if len(stats.AlreadyCached) > 0 {
		fmt.Printf("  ♻️  Already installed (%d):\n", len(stats.AlreadyCached))
		for _, name := range stats.AlreadyCached {
			fmt.Printf("     • %s\n", name)
		}
		fmt.Println()
	}

	if len(stats.Skipped) > 0 {
		fmt.Printf("  ⏭️  Skipped - platform incompatible (%d):\n", len(stats.Skipped))
		for _, name := range stats.Skipped {
			fmt.Printf("     • %s\n", name)
		}
		fmt.Println()
	}

	if len(stats.Failed) > 0 {
		fmt.Printf("  ❌ Failed (%d):\n", len(stats.Failed))
		for _, result := range stats.Failed {
			fmt.Printf("     • %s: %v\n", result.Name, result.Error)
		}
		fmt.Println()
	}

	return nil
}

func installBundles(ctx context.Context, binMgr *binmanager.BinManager, skipDownload bool) error {
	stats, err := binMgr.InstallBundles(ctx, skipDownload)

	total := len(stats.Installed) + len(stats.AlreadyCached) + len(stats.Skipped) + len(stats.Failed)
	if total == 0 {
		return nil
	}

	fmt.Println()
	fmt.Println("📦 Bundle Installation:")
	fmt.Println()

	if len(stats.Installed) > 0 {
		fmt.Printf("  ✅ Installed (%d):\n", len(stats.Installed))
		for _, name := range stats.Installed {
			fmt.Printf("     • %s\n", name)
		}
		fmt.Println()
	}

	if len(stats.AlreadyCached) > 0 {
		fmt.Printf("  ♻️  Already installed (%d):\n", len(stats.AlreadyCached))
		for _, name := range stats.AlreadyCached {
			fmt.Printf("     • %s\n", name)
		}
		fmt.Println()
	}

	if len(stats.Skipped) > 0 {
		fmt.Printf("  ⏭️  Skipped - has external archives (%d):\n", len(stats.Skipped))
		for _, name := range stats.Skipped {
			fmt.Printf("     • %s\n", name)
		}
		fmt.Println()
	}

	if len(stats.Failed) > 0 {
		fmt.Printf("  ❌ Failed (%d):\n", len(stats.Failed))
		for _, result := range stats.Failed {
			fmt.Printf("     • %s: %v\n", result.Name, result.Error)
		}
		fmt.Println()
	}

	if err != nil {
		return err
	}

	return nil
}

func runInitCommands(ctx context.Context, rootPath, cwdPath string, projectTypes []string, cfg *config.Config, binMgr *binmanager.BinManager, dryRun bool) error {
	initNames := make([]string, 0, len(cfg.InitCommands))
	for name := range cfg.InitCommands {
		initNames = append(initNames, name)
	}
	sort.Strings(initNames)

	for _, name := range initNames {
		initCmd := cfg.InitCommands[name]

		// Check if command applies to current project types
		if !isApplicableInitCommand(initCmd, projectTypes) {
			continue
		}

		// Check 'when' condition
		if initCmd.When != "" {
			whenPath := filepath.Join(rootPath, initCmd.When)
			if _, err := os.Stat(whenPath); os.IsNotExist(err) {
				fmt.Printf("⏭️  Skipping %s: %s not found\n", name, initCmd.When)
				continue
			}
		}

		fmt.Printf("🔧 Running: %s", name)
		if initCmd.Description != "" {
			fmt.Printf(" (%s)", initCmd.Description)
		}
		fmt.Println()

		if dryRun {
			fmt.Printf("   [DRY-RUN] %s %v\n", initCmd.Command, initCmd.Args)
			continue
		}

		// Execute the command
		if err := binMgr.Exec(initCmd.Command, initCmd.Args); err != nil {
			return fmt.Errorf("failed to run %s: %w", name, err)
		}
	}

	return nil
}

// commandInfoGetter abstracts GetCommandInfo for testability.
type commandInfoGetter interface {
	GetCommandInfo(appName string) (*binmanager.CommandInfo, error)
}

func installRuntimeAppsWithLinks(binMgr *binmanager.BinManager, cfg *config.Config, installAll bool) error {
	var appsToInstall []string
	if installAll {
		appsToInstall = filterAppsForSmartInit(cfg.Apps, allAppNames(cfg.Apps))
	} else {
		referencedApps := scanReferencedApps(cfg)
		appsToInstall = filterAppsForSmartInit(cfg.Apps, referencedApps)

		// Also include any runtime-managed app that has Links defined,
		// even if not directly referenced by tool operations. Apps with
		// Links may only be referenced via tools.Config.linkPath() in
		// ConfigInit sections, which scanReferencedApps does not inspect.
		linkApps := allRuntimeAppsWithLinks(cfg.Apps)
		appsToInstall = mergeUnique(appsToInstall, linkApps)
	}
	return installSmartInitApps(binMgr, appsToInstall)
}

// allAppNames returns all app names from the config (for --all mode).
func allAppNames(apps binmanager.MapOfApps) []string {
	result := make([]string, 0, len(apps))
	for name := range apps {
		result = append(result, name)
	}
	return result
}

// scanReferencedApps collects unique app names from all tool operations.
func scanReferencedApps(cfg *config.Config) []string {
	seen := make(map[string]bool)
	for _, tool := range cfg.Tools {
		for _, op := range tool.Operations {
			if op.App != "" {
				seen[op.App] = true
			}
		}
	}

	result := make([]string, 0, len(seen))
	for name := range seen {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

// filterAppsForSmartInit returns runtime-managed app names that are both
// referenced by tools and have Links defined.
func filterAppsForSmartInit(apps binmanager.MapOfApps, referencedApps []string) []string {
	refSet := make(map[string]bool, len(referencedApps))
	for _, name := range referencedApps {
		refSet[name] = true
	}

	var result []string
	for name, app := range apps {
		if !refSet[name] {
			continue
		}
		if len(app.Links) == 0 {
			continue
		}
		if app.Binary != nil || app.Shell != nil || app.Jvm != nil {
			continue
		}
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

// allRuntimeAppsWithLinks returns names of all runtime-managed (UV/FNM) apps
// that have Links defined. These apps provide config files for symlinking
// and should always be installed during init.
func allRuntimeAppsWithLinks(apps binmanager.MapOfApps) []string {
	var result []string
	for name, app := range apps {
		if len(app.Links) == 0 {
			continue
		}
		if app.Binary != nil || app.Shell != nil || app.Jvm != nil {
			continue
		}
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

// mergeUnique merges two sorted string slices and returns a sorted, deduplicated result.
func mergeUnique(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	for _, s := range a {
		seen[s] = true
	}
	for _, s := range b {
		seen[s] = true
	}
	result := make([]string, 0, len(seen))
	for s := range seen {
		result = append(result, s)
	}
	sort.Strings(result)
	return result
}

// installSmartInitApps installs the given list of runtime-managed apps.
func installSmartInitApps(getter commandInfoGetter, appsToInstall []string) error {
	sort.Strings(appsToInstall)
	for _, name := range appsToInstall {
		fmt.Printf("📦 Installing %s (referenced by tools, has config links)...\n", name)
		if _, err := getter.GetCommandInfo(name); err != nil {
			return fmt.Errorf("failed to install %s: %w", name, err)
		}
	}
	return nil
}

// bundleRootResolver adapts BinManager.GetBundleRoot to the InstallRootResolver interface.
type bundleRootResolver struct {
	bm *binmanager.BinManager
}

func (r *bundleRootResolver) GetInstallRoot(name string) (string, error) {
	return r.bm.GetBundleRoot(name)
}

func setupConfigLinks(rootPath string, cfg *config.Config, binMgr *binmanager.BinManager, dryRun bool) error {
	if !hasAnyLinks(cfg.Apps, cfg.Bundles) {
		// Even without links, create .datamitsu/ with type definitions
		// so that /// <reference path=".datamitsu/datamitsu.config.d.ts" /> works
		return managedconfig.CreateDatamitsuTypeDefinitions(rootPath, dryRun)
	}

	fmt.Println()
	fmt.Println("🔗 Setting up config links...")

	var bundleResolver managedconfig.InstallRootResolver
	if len(cfg.Bundles) > 0 {
		bundleResolver = &bundleRootResolver{bm: binMgr}
	}

	createdLinks, err := managedconfig.CreateDatamitsuLinks(rootPath, cfg.Apps, binMgr, cfg.Bundles, bundleResolver, dryRun)
	if err != nil {
		return err
	}

	if !dryRun {
		for _, linkName := range createdLinks {
			fmt.Printf("  ✅ .datamitsu/%s\n", linkName)
		}
	}

	return nil
}

func hasAnyLinks(apps binmanager.MapOfApps, bundles binmanager.MapOfBundles) bool {
	for _, app := range apps {
		if len(app.Links) > 0 {
			return true
		}
	}
	for _, bundle := range bundles {
		if bundle != nil && len(bundle.Links) > 0 {
			return true
		}
	}
	return false
}


func checkInitGitRoot(cwdPath, rootPath string) error {
	resolvedCwd, errCwd := filepath.EvalSymlinks(cwdPath)
	resolvedRoot, errRoot := filepath.EvalSymlinks(rootPath)
	if errCwd != nil || errRoot != nil {
		resolvedCwd = filepath.Clean(cwdPath)
		resolvedRoot = filepath.Clean(rootPath)
	}
	if resolvedCwd != resolvedRoot {
		return fmt.Errorf("init must be run from git root: currently in %s, git root is %s", cwdPath, rootPath)
	}
	return nil
}

func isApplicableInitCommand(initCmd config.InitCommand, projectTypes []string) bool {
	// If no project types specified, applies to all
	if len(initCmd.ProjectTypes) == 0 {
		return true
	}

	// Check if any project type matches
	for _, cmdType := range initCmd.ProjectTypes {
		for _, detectedType := range projectTypes {
			if cmdType == detectedType {
				return true
			}
		}
	}

	return false
}
