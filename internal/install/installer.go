package install

import (
	"context"
	"github.com/datamitsu/datamitsu/internal/config"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dop251/goja"
)

// Installer handles configuration file installation and patching
type Installer struct {
	rootPath     string
	cwdPath      string
	projectTypes []string
	configs      config.MapOfConfigInit
	vm           *goja.Runtime
	layerMap     *config.InitLayerMap
}

// NewInstaller creates a new configuration installer
func NewInstaller(
	rootPath string,
	cwdPath string,
	projectTypes []string,
	configs config.MapOfConfigInit,
	vm *goja.Runtime,
	layerMap *config.InitLayerMap,
) *Installer {
	return &Installer{
		rootPath:     rootPath,
		cwdPath:      cwdPath,
		projectTypes: projectTypes,
		configs:      configs,
		vm:           vm,
		layerMap:     layerMap,
	}
}

// InstallResult represents the result of a single config file installation
type InstallResult struct {
	ConfigName   string
	FilePath     string
	Action       string // "created", "patched", "replaced"
	DeletedFiles []string
	Scope        string
	LinkTarget   string
	Error        error
}

// InstallAll installs all applicable configuration files
func (i *Installer) InstallAll(ctx context.Context, dryRun bool) ([]InstallResult, error) {
	names := make([]string, 0, len(i.configs))
	for name := range i.configs {
		names = append(names, name)
	}
	sort.Strings(names)

	var results []InstallResult
	for _, name := range names {
		result := i.installConfig(ctx, name, i.configs[name], dryRun)
		results = append(results, result)
	}

	return results, nil
}

// installConfig installs a single configuration file
func (i *Installer) installConfig(ctx context.Context, name string, cfg config.ConfigInit, dryRun bool) InstallResult {
	result := InstallResult{
		ConfigName:   name,
		DeletedFiles: []string{},
		Scope:        cfg.Scope,
	}

	// Check if config applies to current project types
	if !i.isApplicable(cfg) {
		result.Action = "skipped"
		return result
	}

	// Skip git-root scoped configs when not at root
	if cfg.Scope == config.ScopeGitRoot && i.cwdPath != i.rootPath {
		result.Action = "skipped"
		return result
	}

	// Determine target directory
	targetDir := i.cwdPath

	// Use the map key as mainFilename
	mainPath := filepath.Join(targetDir, name)
	result.FilePath = mainPath

	// Delete other known config file variants before reading main file state
	for _, altFilename := range cfg.OtherFileNameList {
		altPath := filepath.Join(targetDir, altFilename)
		if altPath == mainPath {
			continue
		}
		if i.fileExists(altPath) {
			if !dryRun {
				if err := os.Remove(altPath); err != nil {
					result.Error = fmt.Errorf("failed to remove %s: %w", altPath, err)
					return result
				}
			}
			result.DeletedFiles = append(result.DeletedFiles, altPath)
		}
	}

	// If LinkTarget is set, create a symlink instead of writing file content
	if cfg.LinkTarget != "" {
		return i.installSymlink(mainPath, cfg.LinkTarget, dryRun, result)
	}

	// Check if main file exists (after alternatives are cleaned up)
	var existingContent *string
	var originalContent *string
	var existingPath *string
	mainFileExisted := i.fileExists(mainPath)
	if mainFileExisted {
		content, err := os.ReadFile(mainPath)
		if err == nil {
			contentStr := string(content)
			existingContent = &contentStr
			originalContent = &contentStr // Store original content
			existingPath = &mainPath
			result.Action = "patched"
		} else {
			result.Action = "replaced"
		}
	} else {
		result.Action = "created"
	}

	// If deleteOnly is true, only delete files and skip creation
	if cfg.DeleteOnly {
		if len(result.DeletedFiles) > 0 {
			result.Action = "deleted"
		} else {
			result.Action = "skipped"
		}
		return result
	}

	// Use pre-evaluated content from layer history when available, fall back to generateContent.
	// Only use layer content for git-root scoped entries. Project-scoped entries run per-project
	// with different cwdPath/projectTypes, so eagerly-evaluated content (computed once during
	// config loading) would be incorrect for them.
	var newContent string
	var usedLayerContent bool
	if i.layerMap != nil && cfg.Scope == config.ScopeGitRoot {
		if history, hasHistory := (*i.layerMap)[name]; hasHistory {
			if layerContent := config.GetLastGeneratedContent(history); layerContent != nil {
				newContent = *layerContent
				usedLayerContent = true
			}
		}
	}
	if !usedLayerContent {
		var err error
		newContent, err = i.generateContent(ctx, cfg, existingContent, originalContent, existingPath)
		if err != nil {
			result.Error = fmt.Errorf("failed to generate content: %w", err)
			return result
		}
	}

	// Write the file
	if !dryRun {
		// Create all parent directories for the file (handles nested paths like .vscode/settings.json)
		fileDir := filepath.Dir(mainPath)
		if err := os.MkdirAll(fileDir, 0755); err != nil {
			result.Error = fmt.Errorf("failed to create directory: %w", err)
			return result
		}

		if err := os.WriteFile(mainPath, []byte(newContent), 0644); err != nil {
			result.Error = fmt.Errorf("failed to write file: %w", err)
			return result
		}
	}

	return result
}

// installSymlink creates a symlink at mainPath pointing to linkTarget (relative to mainPath's directory).
// The resolved target must stay within the repository root directory.
func (i *Installer) installSymlink(mainPath string, linkTarget string, dryRun bool, result InstallResult) InstallResult {
	if filepath.IsAbs(linkTarget) {
		result.Error = fmt.Errorf("linkTarget must be a relative path, got %q", linkTarget)
		return result
	}

	relTarget := filepath.Clean(linkTarget)

	// Validate that the resolved target stays within the repository root
	resolvedTarget := filepath.Clean(filepath.Join(filepath.Dir(mainPath), relTarget))
	relToRoot, err := filepath.Rel(i.rootPath, resolvedTarget)
	if err != nil || relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) {
		result.Error = fmt.Errorf("linkTarget %q resolves to path outside repository root", linkTarget)
		return result
	}

	// Resolve the real root path (following symlinks) for boundary checks
	realRoot := i.rootPath
	if r, rootErr := filepath.EvalSymlinks(i.rootPath); rootErr == nil {
		realRoot = r
	}

	// Check symlink-resolved paths to prevent in-repo symlinks that escape
	if realTarget, evalErr := filepath.EvalSymlinks(resolvedTarget); evalErr == nil {
		realRel, relErr := filepath.Rel(realRoot, realTarget)
		if relErr != nil || realRel == ".." || strings.HasPrefix(realRel, ".."+string(filepath.Separator)) {
			result.Error = fmt.Errorf("linkTarget %q resolves to path outside repository root", linkTarget)
			return result
		}
	} else {
		// Target doesn't fully resolve (e.g., leaf file doesn't exist).
		// First check if resolvedTarget itself is a broken symlink — if so,
		// we cannot verify where it points and must reject it.
		if info, statErr := os.Lstat(resolvedTarget); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			result.Error = fmt.Errorf("linkTarget %q traverses unresolvable symlink at %q", linkTarget, resolvedTarget)
			return result
		}
		// Walk up to find the longest existing ancestor and verify it stays within root.
		dir := resolvedTarget
		for dir != filepath.Dir(dir) {
			dir = filepath.Dir(dir)
			if realDir, dirErr := filepath.EvalSymlinks(dir); dirErr == nil {
				dirRel, relErr := filepath.Rel(realRoot, realDir)
				if relErr != nil || dirRel == ".." || strings.HasPrefix(dirRel, ".."+string(filepath.Separator)) {
					result.Error = fmt.Errorf("linkTarget %q resolves to path outside repository root", linkTarget)
					return result
				}
				break
			} else {
				// EvalSymlinks failed — if this path is itself a symlink (broken),
				// reject because we cannot verify where it points
				if info, statErr := os.Lstat(dir); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
					result.Error = fmt.Errorf("linkTarget %q traverses unresolvable symlink at %q", linkTarget, dir)
					return result
				}
			}
		}
	}

	result.Action = "linked"
	result.LinkTarget = relTarget

	if dryRun {
		return result
	}

	// Remove existing file/symlink at mainPath
	if _, err := os.Lstat(mainPath); err == nil {
		if err := os.Remove(mainPath); err != nil {
			result.Error = fmt.Errorf("failed to remove existing file at %s: %w", mainPath, err)
			return result
		}
	}

	if err := os.MkdirAll(filepath.Dir(mainPath), 0755); err != nil {
		result.Error = fmt.Errorf("failed to create directory: %w", err)
		return result
	}

	if err := os.Symlink(relTarget, mainPath); err != nil {
		result.Error = fmt.Errorf("failed to create symlink: %w", err)
		return result
	}

	return result
}

// isApplicable checks if the config applies to the current project types
func (i *Installer) isApplicable(cfg config.ConfigInit) bool {
	// If no project types specified, applies to all
	if len(cfg.ProjectTypes) == 0 {
		return true
	}

	// Check if any project type matches
	for _, cfgType := range cfg.ProjectTypes {
		for _, detectedType := range i.projectTypes {
			if cfgType == detectedType {
				return true
			}
		}
	}

	return false
}

// generateContent calls the JavaScript content function
func (i *Installer) generateContent(ctx context.Context, cfg config.ConfigInit, existingContent, originalContent, existingPath *string) (string, error) {
	// Content field should be a goja.Value representing a function
	contentValue, ok := cfg.Content.(goja.Value)
	if !ok {
		return "", fmt.Errorf("content is not a goja.Value")
	}

	// Get the callable
	contentFunc, ok := goja.AssertFunction(contentValue)
	if !ok {
		return "", fmt.Errorf("content is not a callable function")
	}

	// Prepare context object
	contextObj := i.vm.NewObject()
	_ = contextObj.Set("projectTypes", i.projectTypes)
	_ = contextObj.Set("rootPath", i.rootPath)
	_ = contextObj.Set("cwdPath", i.cwdPath)
	_ = contextObj.Set("isRoot", i.rootPath == i.cwdPath)

	datamitsuAbsDir := filepath.Join(i.rootPath, ".datamitsu")
	datamitsuRelDir := ".datamitsu"
	if relDir, err := filepath.Rel(i.cwdPath, datamitsuAbsDir); err == nil {
		datamitsuRelDir = relDir
	}
	_ = contextObj.Set("datamitsuDir", datamitsuRelDir)

	if existingContent != nil {
		_ = contextObj.Set("existingContent", *existingContent)
	}
	if originalContent != nil {
		_ = contextObj.Set("originalContent", *originalContent)
	}
	if existingPath != nil {
		_ = contextObj.Set("existingPath", *existingPath)
	}

	result, err := contentFunc(goja.Undefined(), contextObj)
	if err != nil {
		return "", fmt.Errorf("failed to call content function: %w", err)
	}

	if result == nil || goja.IsUndefined(result) || goja.IsNull(result) {
		return "", fmt.Errorf("content function returned nil/undefined")
	}

	return result.String(), nil
}

// fileExists checks if a file exists
func (i *Installer) fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
