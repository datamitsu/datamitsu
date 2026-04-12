package managedconfig

import (
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// InstallRootResolver resolves install root paths for apps.
type InstallRootResolver interface {
	GetInstallRoot(appName string) (string, error)
}

// validateLinkPath ensures a link target path stays within installRoot.
// It rejects absolute paths, parent traversal, and symlinks that escape.
func validateLinkPath(path, installRoot string) error {
	if path == "" {
		return fmt.Errorf("link path must not be empty")
	}

	cleaned := filepath.Clean(path)

	if filepath.IsAbs(cleaned) {
		return fmt.Errorf("link path must be relative: %s", path)
	}

	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("link path escapes install directory: %s", path)
	}

	absPath := filepath.Join(installRoot, cleaned)
	resolved, err := filepath.EvalSymlinks(absPath)
	if err == nil {
		resolvedRoot, rootErr := filepath.EvalSymlinks(installRoot)
		if rootErr != nil {
			resolvedRoot = installRoot
		}
		if !strings.HasPrefix(resolved, resolvedRoot+string(filepath.Separator)) && resolved != resolvedRoot {
			return fmt.Errorf("link path escapes install directory: %s", path)
		}
	}

	return nil
}

// verifySymlink checks that linkPath is a symlink pointing to expectedTarget
// and that the target file exists.
func verifySymlink(linkPath, expectedTarget string) error {
	info, err := os.Lstat(linkPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("symlink does not exist: %s", linkPath)
		}
		return fmt.Errorf("failed to stat symlink: %w", err)
	}

	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("path exists but is not a symlink: %s", linkPath)
	}

	actualTarget, err := os.Readlink(linkPath)
	if err != nil {
		return fmt.Errorf("failed to read symlink: %w", err)
	}

	if actualTarget != expectedTarget {
		return fmt.Errorf("symlink points to wrong target: got %q, expected %q", actualTarget, expectedTarget)
	}

	if _, err := os.Stat(linkPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("symlink target does not exist: %s -> %s", linkPath, expectedTarget)
		}
		return fmt.Errorf("failed to stat symlink target: %w", err)
	}

	return nil
}

// CreateDatamitsuLinks creates symlinks in {gitRoot}/.datamitsu/ pointing to
// files in app and bundle install directories. Returns the list of created link names.
// All apps/bundles with links must be installed — uninstalled sources cause an immediate error.
// After atomic directory swap, every symlink is verified for existence, correct
// target, and target file validity. Verification failure is a hard error.
func CreateDatamitsuLinks(gitRoot string, apps binmanager.MapOfApps, resolver InstallRootResolver, bundles binmanager.MapOfBundles, bundleResolver InstallRootResolver, dryRun bool) ([]string, error) {
	type linkEntry struct {
		sourceName   string
		linkName     string
		relativePath string
		resolver     InstallRootResolver
	}

	var entries []linkEntry
	for appName, app := range apps {
		if len(app.Links) == 0 {
			continue
		}
		for linkName, relativePath := range app.Links {
			entries = append(entries, linkEntry{
				sourceName:   appName,
				linkName:     linkName,
				relativePath: relativePath,
				resolver:     resolver,
			})
		}
	}

	for bundleName, bundle := range bundles {
		if bundle == nil || len(bundle.Links) == 0 {
			continue
		}
		for linkName, relativePath := range bundle.Links {
			entries = append(entries, linkEntry{
				sourceName:   bundleName,
				linkName:     linkName,
				relativePath: relativePath,
				resolver:     bundleResolver,
			})
		}
	}

	if len(entries) == 0 {
		return nil, nil
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].linkName < entries[j].linkName
	})

	if dryRun {
		for _, e := range entries {
			cleanedLinkName := filepath.Clean(e.linkName)
			if cleanedLinkName == ".gitignore" || cleanedLinkName == "datamitsu.config.d.ts" {
				return nil, fmt.Errorf("link name %q is reserved for internal use", e.linkName)
			}
			if filepath.IsAbs(cleanedLinkName) || cleanedLinkName == ".." || strings.HasPrefix(cleanedLinkName, ".."+string(filepath.Separator)) {
				return nil, fmt.Errorf("invalid link name %q: path traversal", e.linkName)
			}

			installRoot, err := e.resolver.GetInstallRoot(e.sourceName)
			if err != nil {
				return nil, fmt.Errorf("cannot create link %q: source %q is not installed", e.linkName, e.sourceName)
			}

			if err := validateLinkPath(e.relativePath, installRoot); err != nil {
				return nil, fmt.Errorf("invalid link %q for source %q: %w", e.linkName, e.sourceName, err)
			}

			target := filepath.Join(installRoot, e.relativePath)
			if _, err := os.Stat(target); err != nil {
				return nil, fmt.Errorf("link target %q for source %q does not exist: %w", e.relativePath, e.sourceName, err)
			}
		}
		fmt.Println("Would create .datamitsu/ symlinks:")
		for _, e := range entries {
			fmt.Printf("  %s -> %s (from %q)\n", e.linkName, e.relativePath, e.sourceName)
		}
		return nil, nil
	}

	datamitsuDir := filepath.Join(gitRoot, ".datamitsu")

	if err := os.MkdirAll(gitRoot, 0755); err != nil {
		return nil, fmt.Errorf("failed to create git root directory: %w", err)
	}

	// Build symlinks in a temp directory first, then atomically swap via rename.
	// This avoids leaving .datamitsu missing or partially built on error.
	tmpDir, err := os.MkdirTemp(gitRoot, ".datamitsu-tmp-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory for .datamitsu: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	if err := createDatamitsuGitignore(tmpDir); err != nil {
		return nil, fmt.Errorf("failed to create .gitignore in .datamitsu: %w", err)
	}

	if err := writeTypeDefinitions(tmpDir); err != nil {
		return nil, fmt.Errorf("failed to write type definitions: %w", err)
	}

	var createdLinks []string
	linkTargets := make(map[string]string)
	for _, e := range entries {
		cleanedLinkName := filepath.Clean(e.linkName)
		if cleanedLinkName == ".gitignore" || cleanedLinkName == "datamitsu.config.d.ts" {
			return nil, fmt.Errorf("link name %q is reserved for internal use", e.linkName)
		}
		if filepath.IsAbs(cleanedLinkName) || cleanedLinkName == ".." || strings.HasPrefix(cleanedLinkName, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("invalid link name %q: path traversal", e.linkName)
		}

		installRoot, err := e.resolver.GetInstallRoot(e.sourceName)
		if err != nil {
			return nil, fmt.Errorf("cannot create link %q: source %q is not installed", e.linkName, e.sourceName)
		}

		if err := validateLinkPath(e.relativePath, installRoot); err != nil {
			return nil, fmt.Errorf("invalid link %q for source %q: %w", e.linkName, e.sourceName, err)
		}

		target := filepath.Join(installRoot, e.relativePath)
		if _, err := os.Stat(target); err != nil {
			return nil, fmt.Errorf("link target %q for source %q does not exist: %w", e.relativePath, e.sourceName, err)
		}
		linkPath := filepath.Join(tmpDir, cleanedLinkName)

		if err := os.MkdirAll(filepath.Dir(linkPath), 0755); err != nil {
			return nil, fmt.Errorf("failed to create parent directory for link %s: %w", e.linkName, err)
		}

		relTarget, err := filepath.Rel(filepath.Dir(linkPath), target)
		if err != nil {
			return nil, fmt.Errorf("failed to compute relative symlink target for %s: %w", e.linkName, err)
		}

		if err := os.Symlink(relTarget, linkPath); err != nil {
			return nil, fmt.Errorf("failed to create symlink %s -> %s: %w", e.linkName, relTarget, err)
		}
		createdLinks = append(createdLinks, e.linkName)
		linkTargets[e.linkName] = relTarget
	}

	backupDir := datamitsuDir + ".bak"

	hasExisting := false
	if _, err := os.Lstat(datamitsuDir); err == nil {
		_ = os.RemoveAll(backupDir)
		if err := os.Rename(datamitsuDir, backupDir); err != nil {
			return nil, fmt.Errorf("failed to back up existing .datamitsu directory: %w", err)
		}
		hasExisting = true
	}

	if err := os.Rename(tmpDir, datamitsuDir); err != nil {
		if hasExisting {
			if restoreErr := os.Rename(backupDir, datamitsuDir); restoreErr != nil {
				return nil, fmt.Errorf("failed to rename temp directory to .datamitsu: %w (also failed to restore backup: %v)", err, restoreErr)
			}
		}
		return nil, fmt.Errorf("failed to rename temp directory to .datamitsu: %w", err)
	}

	for _, linkName := range createdLinks {
		linkPath := filepath.Join(datamitsuDir, linkName)
		expectedTarget := linkTargets[linkName]
		if err := verifySymlink(linkPath, expectedTarget); err != nil {
			cleanupErr := os.RemoveAll(datamitsuDir)
			var restoreErr error
			if hasExisting {
				restoreErr = os.Rename(backupDir, datamitsuDir)
			}
			if cleanupErr != nil || restoreErr != nil {
				return nil, fmt.Errorf("link verification failed for %q: %w (also failed to restore previous state: remove=%v, restore=%v)", linkName, err, cleanupErr, restoreErr)
			}
			return nil, fmt.Errorf("link verification failed for %q: %w", linkName, err)
		}
	}

	_ = os.RemoveAll(backupDir)

	sort.Strings(createdLinks)
	return createdLinks, nil
}

// CreateDatamitsuTypeDefinitions creates the .datamitsu/ directory with only
// .gitignore and datamitsu.config.d.ts (no symlinks). Used when no app/bundle
// links are configured but type definitions should still be available for IDE
// autocomplete in config files.
func CreateDatamitsuTypeDefinitions(gitRoot string, dryRun bool) error {
	if dryRun {
		fmt.Println("Would create .datamitsu/ directory with type definitions only (no links configured)")
		return nil
	}

	datamitsuDir := filepath.Join(gitRoot, ".datamitsu")

	if err := os.MkdirAll(gitRoot, 0755); err != nil {
		return fmt.Errorf("failed to create git root directory: %w", err)
	}

	tmpDir, err := os.MkdirTemp(gitRoot, ".datamitsu-tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory for .datamitsu: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	if err := createDatamitsuGitignore(tmpDir); err != nil {
		return fmt.Errorf("failed to create .gitignore in .datamitsu: %w", err)
	}

	if err := writeTypeDefinitions(tmpDir); err != nil {
		return fmt.Errorf("failed to write type definitions: %w", err)
	}

	// Atomically swap using backup-and-restore pattern (same as CreateDatamitsuLinks)
	backupDir := datamitsuDir + ".bak"

	hasExisting := false
	if _, err := os.Lstat(datamitsuDir); err == nil {
		_ = os.RemoveAll(backupDir)
		if err := os.Rename(datamitsuDir, backupDir); err != nil {
			return fmt.Errorf("failed to back up existing .datamitsu directory: %w", err)
		}
		hasExisting = true
	}

	if err := os.Rename(tmpDir, datamitsuDir); err != nil {
		if hasExisting {
			if restoreErr := os.Rename(backupDir, datamitsuDir); restoreErr != nil {
				return fmt.Errorf("failed to rename temp directory to .datamitsu: %w (also failed to restore backup: %v)", err, restoreErr)
			}
		}
		return fmt.Errorf("failed to rename temp directory to .datamitsu: %w", err)
	}

	_ = os.RemoveAll(backupDir)

	return nil
}

func createDatamitsuGitignore(dir string) error {
	return os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*\n"), 0644)
}

// writeTypeDefinitions writes the embedded TypeScript type definitions
// to {dir}/datamitsu.config.d.ts so that IDEs can provide autocomplete
// when editing datamitsu configuration files.
func writeTypeDefinitions(dir string) error {
	content := config.GetDefaultConfigDTS()
	return os.WriteFile(filepath.Join(dir, "datamitsu.config.d.ts"), []byte(content), 0644)
}
