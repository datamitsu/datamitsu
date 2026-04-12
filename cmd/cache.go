package cmd

import (
	"github.com/datamitsu/datamitsu/internal/cache"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/traverser"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var (
	cacheAll    bool
	cacheDryRun bool
)

var cacheCmd = &cobra.Command{
	Use:   "cache",
	Short: "Manage cache",
	Long:  `Manage the cache for linting and fixing operations.`,
}

var cacheClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Clear cache",
	Long: `Clear the cache. By default clears only the current project's cache.
Use --all to clear all project caches.`,
	RunE: runCacheClear,
}

var cachePathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print cache directory path",
	Long:  `Print the absolute path to the cache directory.`,
	RunE:  runCachePath,
}

var cachePathProjectCmd = &cobra.Command{
	Use:   "project",
	Short: "Print current project's cache directory path",
	Long:  `Print the absolute path to the current project's cache directory.`,
	RunE:  runCachePathProject,
}

func init() {
	cacheClearCmd.Flags().BoolVar(&cacheAll, "all", false, "Clear all project caches")
	cacheClearCmd.Flags().BoolVar(&cacheDryRun, "dry-run", false, "Show what would be deleted without deleting")
	cachePathCmd.AddCommand(cachePathProjectCmd)
	cacheCmd.AddCommand(cacheClearCmd)
	cacheCmd.AddCommand(cachePathCmd)
	rootCmd.AddCommand(cacheCmd)
}

// resolveProjectRoot returns the git root if available, or CWD if genuinely
// not inside a git repository. If a .git directory exists but GetGitRoot fails
// (e.g. git binary missing, permission error), the error is surfaced rather
// than silently falling back to CWD with a wrong cache key.
func resolveProjectRoot(cmd *cobra.Command, cwdPath string) (string, error) {
	rootPath, err := traverser.GetGitRoot(cmd.Context(), cwdPath)
	if err == nil {
		return rootPath, nil
	}
	if traverser.HasGitDir(cwdPath) {
		return "", fmt.Errorf("failed to determine git root (a .git directory exists but git command failed): %w", err)
	}
	return cwdPath, nil
}

func runCachePath(cmd *cobra.Command, args []string) error {
	cacheDir := env.GetCachePath()
	fmt.Println(cacheDir)
	return nil
}

func runCachePathProject(cmd *cobra.Command, args []string) error {
	cwdPath, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get cwd: %w", err)
	}

	rootPath, err := resolveProjectRoot(cmd, cwdPath)
	if err != nil {
		return err
	}

	projectCachePath, err := env.GetProjectCachePath(rootPath, "", "")
	if err != nil {
		return fmt.Errorf("failed to get project cache path: %w", err)
	}

	fmt.Println(projectCachePath)
	return nil
}

func runCacheClear(cmd *cobra.Command, args []string) error {
	cacheDir := env.GetCachePath()

	if cacheAll {
		projectsDir := filepath.Join(cacheDir, "projects")
		if cacheDryRun {
			fmt.Printf("Would delete: %s (all project caches)\n", projectsDir)
			return nil
		}

		// Clear all caches (removes entire projects/ directory including lint/fix + tool caches)
		if err := cache.ClearAll(cacheDir); err != nil {
			return fmt.Errorf("failed to clear all project caches: %w", err)
		}

		fmt.Println("✅ Cleared all project caches (lint/fix + tool caches)")
	} else {
		// Clear only current project
		cwdPath, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get cwd: %w", err)
		}

		rootPath, err := resolveProjectRoot(cmd, cwdPath)
		if err != nil {
			return err
		}

		if cacheDryRun {
			projectHash := env.HashProjectPath(rootPath)
			projectDir := filepath.Join(cacheDir, "projects", projectHash)
			fmt.Printf("Would delete: %s (entire project cache directory)\n", projectDir)
			return nil
		}

		// Clear entire project directory (lint/fix + tool caches)
		if err := cache.ClearProject(cacheDir, rootPath); err != nil {
			return fmt.Errorf("failed to clear project cache: %w", err)
		}

		fmt.Printf("✅ Cleared cache for project: %s (lint/fix + tool caches)\n", rootPath)
	}

	return nil
}
