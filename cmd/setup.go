package cmd

import (
	"context"
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	enginetools "github.com/datamitsu/datamitsu/internal/engine/tools"
	"github.com/datamitsu/datamitsu/internal/install"
	"github.com/datamitsu/datamitsu/internal/project"
	"github.com/datamitsu/datamitsu/internal/runner"
	"github.com/datamitsu/datamitsu/internal/traverser"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"
)

var (
	setupDryRun  bool
	setupSkipFix bool
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Setup configuration files",
	Long: `Sets up configuration files for detected project types.
This command aggressively replaces existing configs by deleting all known variants
and creating new ones based on the configuration.`,
	RunE: runSetup,
}

func init() {
	setupCmd.Flags().BoolVar(&setupDryRun, "dry-run", false, "Show what would be done without making changes")
	setupCmd.Flags().BoolVar(&setupSkipFix, "skip-fix", false, "Skip running fix after setup")
	rootCmd.AddCommand(setupCmd)
}

func runSetup(cmd *cobra.Command, args []string) error {
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

	// Load configuration
	cfg, layerMap, vm, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	registry := buildConfigLinksRegistry(cfg.Apps, cfg.Bundles)
	if err := enginetools.RegisterConfigLinksInVM(vm, registry, rootPath); err != nil {
		return fmt.Errorf("failed to register config links in VM: %w", err)
	}

	// Detect project types and locations
	detector := project.NewDetector(rootPath, cfg.ProjectTypes)
	locations, err := detector.DetectAllWithLocations(ctx)
	if err != nil {
		return fmt.Errorf("failed to detect project types: %w", err)
	}

	// Group locations by path to get all types for each location
	locationMap := make(map[string][]string)
	for _, loc := range locations {
		locationMap[loc.Path] = append(locationMap[loc.Path], loc.Type)
	}

	detectedCount := len(locationMap)

	// Ensure rootPath is always in the map so git-root scoped configs can run
	if _, ok := locationMap[rootPath]; !ok {
		locationMap[rootPath] = []string{}
	}

	// Sort paths for deterministic output
	sortedPaths := make([]string, 0, len(locationMap))
	for path := range locationMap {
		sortedPaths = append(sortedPaths, path)
	}
	sort.Strings(sortedPaths)

	if detectedCount == 0 {
		fmt.Println("⚠️  No project types detected")
	} else {
		fmt.Printf("📦 Found %d project location(s)\n", detectedCount)
		for _, path := range sortedPaths {
			types := locationMap[path]
			if len(types) == 0 {
				continue
			}
			relPath, _ := filepath.Rel(rootPath, path)
			if relPath == "" || relPath == "." {
				relPath = "."
			}
			fmt.Printf("   %s: %v\n", relPath, types)
		}
	}
	fmt.Println()

	var allResults []install.InstallResult
	for _, projectPath := range sortedPaths {
		projectTypes := locationMap[projectPath]
		// Create installer for this specific location
		installer := install.NewInstaller(rootPath, projectPath, projectTypes, cfg.Init, vm, layerMap)

		// Install configs
		results, err := installer.InstallAll(ctx, setupDryRun)
		if err != nil {
			return fmt.Errorf("failed to install configs in %s: %w", projectPath, err)
		}

		allResults = append(allResults, results...)
	}

	results := deduplicateGitRootResults(allResults)

	// Print results
	if setupDryRun {
		fmt.Println("🔍 DRY-RUN MODE - No changes will be made")
		fmt.Println()
	}

	var installErrors []error
	for _, result := range results {
		if result.Action == "skipped" {
			continue
		}

		relPath, _ := filepath.Rel(rootPath, result.FilePath)

		switch result.Action {
		case "created":
			fmt.Printf("✨ Created: %s\n", relPath)
		case "patched":
			fmt.Printf("🔧 Patched: %s\n", relPath)
		case "replaced":
			fmt.Printf("🔄 Replaced: %s\n", relPath)
		case "linked":
			if result.LinkTarget != "" {
				linkTargetRel, _ := filepath.Rel(rootPath, filepath.Join(filepath.Dir(result.FilePath), result.LinkTarget))
				fmt.Printf("🔗 Linked: %s -> %s\n", relPath, linkTargetRel)
			} else {
				fmt.Printf("🔗 Linked: %s\n", relPath)
			}
		case "deleted":
			fmt.Printf("🗑️  Deleted configuration files\n")
		}

		// Only show deleted files if they're different from the created/patched file
		if len(result.DeletedFiles) > 0 {
			for _, deleted := range result.DeletedFiles {
				// Skip showing deletion if it's the same file being created/patched
				if deleted == result.FilePath {
					continue
				}
				relDeleted, _ := filepath.Rel(rootPath, deleted)
				fmt.Printf("   ❌ Deleted: %s\n", relDeleted)
			}
		}

		if result.Error != nil {
			fmt.Printf("   ⚠️  Error: %v\n", result.Error)
			installErrors = append(installErrors, fmt.Errorf("%s: %w", result.ConfigName, result.Error))
		}
	}

	if len(installErrors) > 0 {
		return fmt.Errorf("setup completed with %d error(s)", len(installErrors))
	}

	if !setupDryRun {
		fmt.Println()
		fmt.Println("✅ Setup complete")

		// Run fix operation after setup unless skipped
		if !setupSkipFix {
			fmt.Println()
			fmt.Println("🔧 Running fix operation...")
			fmt.Println()

			if err := runner.Run(config.OpFix, []string{}, "", false, "", func() (*config.Config, string, error) {
				cfg, _, _, err := loadConfig()
				return cfg, "", err
			}); err != nil {
				return fmt.Errorf("fix operation failed: %w", err)
			}
		}
	}

	return nil
}

// deduplicateGitRootResults removes duplicate results for git-root scoped configs.
// When multiple project locations produce results for the same FilePath and all
// have Scope="git-root", only one result is kept. If any result in the group has an
// error, the first error result is preferred so errors are never silently dropped.
func deduplicateGitRootResults(results []install.InstallResult) []install.InstallResult {
	type group struct {
		indices      []int
		allGitRoot   bool
	}

	groups := make(map[string]*group)
	var order []string

	for i, r := range results {
		g, exists := groups[r.FilePath]
		if !exists {
			g = &group{allGitRoot: true}
			groups[r.FilePath] = g
			order = append(order, r.FilePath)
		}
		g.indices = append(g.indices, i)
		if r.Scope != config.ScopeGitRoot {
			g.allGitRoot = false
		}
	}

	keep := make(map[int]bool)
	for _, path := range order {
		g := groups[path]
		if g.allGitRoot && len(g.indices) > 1 {
			bestIdx := g.indices[0]
			for _, idx := range g.indices {
				if results[idx].Error != nil {
					bestIdx = idx
					break
				}
			}
			keep[bestIdx] = true
		} else {
			for _, idx := range g.indices {
				keep[idx] = true
			}
		}
	}

	var deduped []install.InstallResult
	for i, r := range results {
		if keep[i] {
			deduped = append(deduped, r)
		}
	}

	return deduped
}

func buildConfigLinksRegistry(apps binmanager.MapOfApps, bundles binmanager.MapOfBundles) map[string]string {
	registry := make(map[string]string)
	for appName, app := range apps {
		for linkName := range app.Links {
			registry[linkName] = appName
		}
	}
	for bundleName, bundle := range bundles {
		if bundle == nil {
			continue
		}
		for linkName := range bundle.Links {
			registry[linkName] = bundleName
		}
	}
	return registry
}
