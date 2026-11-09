// Package install writes and patches managed configuration files into a
// project, creating, replacing or symlinking each config entry.
package install

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/config"

	"github.com/dop251/goja"
)

// Installer handles configuration file installation and patching
type Installer struct {
	rootPath          string
	cwdPath           string
	projectTypes      []string
	projectLocations  []config.ProjectLocation // repo-wide detected {type, path}; exposed to content()
	selectedTools     []string                 // nil/empty = no --tools filter (install all applicable)
	repoProjectTypes  []string                 // every type detected anywhere in the repository
	configs           config.MapOfManagedConfigs
	vm                *goja.Runtime
	layerMap          *config.ManagedConfigLayerMap
	force             bool
	createMissingOnly bool
}

// NewInstaller creates a new configuration installer. selectedTools scopes
// installation to configs associated with those tools (via ManagedConfig.Tools);
// pass nil to install every applicable config.
func NewInstaller(
	rootPath string,
	cwdPath string,
	projectTypes []string,
	selectedTools []string,
	configs config.MapOfManagedConfigs,
	vm *goja.Runtime,
	layerMap *config.ManagedConfigLayerMap,
) *Installer {
	return &Installer{
		rootPath:      rootPath,
		cwdPath:       cwdPath,
		projectTypes:  projectTypes,
		selectedTools: selectedTools,
		configs:       configs,
		vm:            vm,
		layerMap:      layerMap,
	}
}

// SetForce discards existing file settings and permits explicit replacement or
// removal of customized managed configs. Configuration-layer overrides remain.
func (i *Installer) SetForce(force bool) {
	i.force = force
}

// SetCreateMissingOnly switches the installer to the `config reconcile
// --create-missing` mode: it creates only managed config files that are
// missing and never touches an existing file — no replacement, patching,
// deletion, or otherFileNameList cleanup. An existing regular file is reported
// as "skipped-existing"; a path occupied by anything else is an error.
func (i *Installer) SetCreateMissingOnly(only bool) {
	i.createMissingOnly = only
}

// SetProjectLocations sets the repo-wide detected project locations (git-root
// relative {type, path}) exposed to content() functions as
// context.projectLocations. Optional; defaults to none. Used by reconciliation to let
// configs (e.g. dependabot) build per-ecosystem output dynamically.
func (i *Installer) SetProjectLocations(locations []config.ProjectLocation) {
	i.projectLocations = locations
}

// SetRepoProjectTypes sets every project type detected anywhere in the
// repository. An ejectable git-root entry applies when any of them matches:
// it is written once at the root for the whole repository, so a Docker project
// in docker/ is what makes droast.toml apply, and the root's own types would
// leave an ejected file unwritten while its tool still reads it.
func (i *Installer) SetRepoProjectTypes(types []string) {
	i.repoProjectTypes = types
}

// InstallResult represents the result of a single config file installation
type InstallResult struct { //nolint:revive // exported: name kept explicit; install.InstallResult reads clearer than install.Result at its cross-package call sites
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

	results := make([]InstallResult, 0, len(names))
	for _, name := range names {
		result := i.installConfig(ctx, name, i.configs[name], dryRun)
		results = append(results, result)
	}

	return results, nil
}

// installConfig installs a single configuration file
func (i *Installer) installConfig(ctx context.Context, name string, cfg config.ManagedConfig, dryRun bool) InstallResult {
	result := InstallResult{
		ConfigName:   name,
		DeletedFiles: []string{},
		Scope:        cfg.Scope,
	}

	if !i.applies(cfg) {
		result.Action = "skipped"
		return result
	}

	// Honor the --tools filter: skip configs not associated with a selected tool.
	if !i.isToolSelected(cfg) {
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

	// Create-missing mode: nothing below this point may mutate an existing
	// file, so it diverges before any cleanup, removal, or patching can run.
	if i.createMissingOnly {
		return i.createMissing(ctx, name, cfg, mainPath, dryRun, result)
	}

	if cfg.Ejectable && !i.force {
		if conflicts := i.ejectConflicts(name, cfg); len(conflicts) > 0 {
			result.Error = conflicts[0]
			return result
		}
	}

	if cfg.Placement == config.PlacementInternal {
		return i.internalize(name, cfg, dryRun, result)
	}

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
	if i.force && i.layerMap != nil {
		if history := (*i.layerMap)[name]; history != nil && len(history.SourceLayers) > 0 {
			cc := config.ContentContext{
				RootPath: i.rootPath, CwdPath: i.cwdPath, Placement: config.PlacementRepo,
				OutputPath: mainPath, ProjectTypes: i.projectTypes, ProjectLocations: i.projectLocations,
			}
			if cfg.Ejectable {
				cc.ProjectTypes, cc.ProjectLocations = nil, nil
			}
			rendered, err := config.RenderManagedConfigWithContext(history.SourceLayers, name, cc)
			if err != nil {
				result.Error = err
				return result
			}
			if rendered == nil {
				result.Action = "skipped"
				return result
			}
			newContent, usedLayerContent = *rendered, true
		}
	}
	if !usedLayerContent && i.layerMap != nil && cfg.Scope == config.ScopeGitRoot {
		if history, hasHistory := (*i.layerMap)[name]; hasHistory {
			if layerContent := config.GetLastGeneratedContent(history); layerContent != nil {
				newContent = *layerContent
				usedLayerContent = true
			}
		}
	}
	if !usedLayerContent {
		var skip bool
		var err error
		newContent, skip, err = i.generateContent(ctx, cfg, mainPath, existingContent, originalContent, existingPath)
		if err != nil {
			result.Error = fmt.Errorf("failed to generate content: %w", err)
			return result
		}
		// content() returning undefined/null is an explicit opt-out (e.g. a
		// "manage only if the file already exists" generator): leave the file
		// untouched rather than writing it. Mirrors the eager-eval skip.
		if skip {
			result.Action = "skipped"
			return result
		}
	}

	// Write the file
	if !dryRun {
		// Create all parent directories for the file (handles nested paths like .vscode/settings.json)
		fileDir := filepath.Dir(mainPath)
		if err := os.MkdirAll(fileDir, 0o755); err != nil {
			result.Error = fmt.Errorf("failed to create directory: %w", err)
			return result
		}

		if err := os.WriteFile(mainPath, []byte(newContent), 0o644); err != nil {
			result.Error = fmt.Errorf("failed to write file: %w", err)
			return result
		}
	}

	return result
}

// createMissing implements the `config reconcile --create-missing` safety
// contract for a single config entry: a missing managed file is created, and
// every existing file is left byte-for-byte untouched — no replacement,
// patching, deletion, or otherFileNameList cleanup. An existing regular file
// (or a symlink to one) is reported as "skipped-existing"; a path occupied by
// anything else is a collision error, never worked around or removed.
func (i *Installer) createMissing(ctx context.Context, name string, cfg config.ManagedConfig, mainPath string, dryRun bool, result InstallResult) InstallResult {
	// deleteOnly entries have nothing to create.
	if cfg.DeleteOnly {
		result.Action = "skipped"
		return result
	}
	// Internal placement lives in .datamitsu/configs/ and is written by init;
	// this mode would otherwise have nothing to do but the repository-copy
	// removal normal reconcile performs — which is forbidden here.
	if cfg.Placement == config.PlacementInternal {
		result.Action = "skipped"
		return result
	}

	if _, err := os.Lstat(mainPath); err == nil {
		return i.classifyExisting(mainPath, result)
	} else if !errors.Is(err, fs.ErrNotExist) {
		result.Error = fmt.Errorf("failed to inspect %s: %w", mainPath, err)
		return result
	}

	if cfg.LinkTarget != "" {
		return i.installSymlink(mainPath, cfg.LinkTarget, dryRun, result)
	}

	// Only git-root scoped entries carry eagerly evaluated layer content; every
	// project-scoped entry renders here with no existing content, exactly like
	// today's create path. A found-but-empty layer render is a valid file, not
	// an opt-out, so "found" is tracked separately from the content.
	newContent := ""
	usedLayerContent := false
	if i.layerMap != nil && cfg.Scope == config.ScopeGitRoot {
		if history, hasHistory := (*i.layerMap)[name]; hasHistory {
			if layerContent := config.GetLastGeneratedContent(history); layerContent != nil {
				newContent = *layerContent
				usedLayerContent = true
			}
		}
	}
	if !usedLayerContent {
		var skip bool
		var err error
		newContent, skip, err = i.generateContent(ctx, cfg, mainPath, nil, nil, nil)
		if err != nil {
			result.Error = fmt.Errorf("failed to generate content: %w", err)
			return result
		}
		// content() returning undefined/null is an explicit opt-out: leave
		// nothing behind.
		if skip {
			result.Action = "skipped"
			return result
		}
	}

	if !dryRun {
		if err := os.MkdirAll(filepath.Dir(mainPath), 0o755); err != nil {
			result.Error = fmt.Errorf("failed to create directory: %w", err)
			return result
		}
		// Exclusive create: a file that appeared between the check above and
		// this write must not be truncated — it is someone's existing file.
		file, err := os.OpenFile(mainPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if errors.Is(err, os.ErrExist) {
			return i.classifyExisting(mainPath, result)
		}
		if err != nil {
			result.Error = fmt.Errorf("failed to write file: %w", err)
			return result
		}
		_, writeErr := file.WriteString(newContent)
		closeErr := file.Close()
		if writeErr != nil {
			result.Error = fmt.Errorf("failed to write file: %w", writeErr)
			return result
		}
		if closeErr != nil {
			result.Error = fmt.Errorf("failed to write file: %w", closeErr)
			return result
		}
	}
	result.Action = "created"
	return result
}

// classifyExisting reports a path that appeared under create-missing between
// the absence check and the write: a regular file (or a symlink to one) is
// "skipped-existing", anything else is a collision.
func (i *Installer) classifyExisting(mainPath string, result InstallResult) InstallResult {
	if resolved, statErr := os.Stat(mainPath); statErr == nil && resolved.Mode().IsRegular() {
		result.Action = "skipped-existing"
		return result
	}
	result.Action = "blocked"
	result.Error = fmt.Errorf("%s exists and is not a regular file; --create-missing never replaces or removes it", mainPath)
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
			realDir, dirErr := filepath.EvalSymlinks(dir)
			if dirErr == nil {
				dirRel, relErr := filepath.Rel(realRoot, realDir)
				if relErr != nil || dirRel == ".." || strings.HasPrefix(dirRel, ".."+string(filepath.Separator)) {
					result.Error = fmt.Errorf("linkTarget %q resolves to path outside repository root", linkTarget)
					return result
				}
				break
			}
			// EvalSymlinks failed — if this path is itself a symlink (broken),
			// reject because we cannot verify where it points
			if info, statErr := os.Lstat(dir); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
				result.Error = fmt.Errorf("linkTarget %q traverses unresolvable symlink at %q", linkTarget, dir)
				return result
			}
		}
	}

	result.Action = "linked"
	result.LinkTarget = relTarget

	if dryRun {
		return result
	}

	// Nested managed links (e.g. .config/tool.yml) need their parents, in both
	// the exclusive create-missing path and the replacing one.
	if err := os.MkdirAll(filepath.Dir(mainPath), 0o755); err != nil {
		result.Error = fmt.Errorf("failed to create directory: %w", err)
		return result
	}

	// Remove existing file/symlink at mainPath. Create-missing never removes:
	// its exclusive symlink creation below reports a path that appeared as
	// skipped-existing or blocked instead.
	if i.createMissingOnly {
		if err := os.Symlink(relTarget, mainPath); err != nil {
			if errors.Is(err, os.ErrExist) {
				return i.classifyExisting(mainPath, result)
			}
			result.Error = fmt.Errorf("failed to create symlink at %s: %w", mainPath, err)
			return result
		}
		result.LinkTarget = relTarget
		return result
	}
	if _, err := os.Lstat(mainPath); err == nil {
		if err := os.Remove(mainPath); err != nil {
			result.Error = fmt.Errorf("failed to remove existing file at %s: %w", mainPath, err)
			return result
		}
	}

	if err := os.Symlink(relTarget, mainPath); err != nil {
		result.Error = fmt.Errorf("failed to create symlink: %w", err)
		return result
	}

	return result
}

// isApplicable checks if the config applies to the current project types
func (i *Installer) isApplicable(cfg config.ManagedConfig) bool {
	// If no project types specified, applies to all
	if len(cfg.ProjectTypes) == 0 {
		return true
	}

	// Check if any project type matches
	for _, cfgType := range cfg.ProjectTypes {
		if slices.Contains(i.projectTypes, cfgType) {
			return true
		}
	}

	return false
}

// isToolSelected reports whether a config should run under the active --tools
// filter. With no filter (selectedTools empty) every config passes. With a
// filter, only configs whose Tools intersect the selected set pass; configs
// with no Tools (unassociated infra) are always skipped.
func (i *Installer) isToolSelected(cfg config.ManagedConfig) bool {
	if len(i.selectedTools) == 0 {
		return true
	}
	for _, t := range cfg.Tools {
		if slices.Contains(i.selectedTools, t) {
			return true
		}
	}
	return false
}

// generateContent calls the JavaScript content function.
//
// ctx is currently unused here (goja runs the content function under a watchdog
// timeout, not a context), but it is threaded through the InstallAll ->
// installConfig -> generateContent chain so the public install API stays
// cancellation-ready; do not drop it.
//
// The bool return reports whether content() opted out (returned undefined/null),
// in which case the caller skips the file rather than writing it.
//
//nolint:unparam // ctx reserved for cancellable JS execution; keeps the install chain uniform
func (i *Installer) generateContent(ctx context.Context, cfg config.ManagedConfig, outputPath string, existingContent, originalContent, existingPath *string) (string, bool, error) {
	// Content field should be a goja.Value representing a function
	contentValue, ok := cfg.Content.(goja.Value)
	if !ok {
		return "", false, errors.New("content is not a goja.Value")
	}

	// Get the callable
	contentFunc, ok := goja.AssertFunction(contentValue)
	if !ok {
		return "", false, errors.New("content is not a callable function")
	}

	cc := config.ContentContext{
		RootPath:         i.rootPath,
		CwdPath:          i.cwdPath,
		ProjectTypes:     i.projectTypes,
		ProjectLocations: i.projectLocations,
		Placement:        config.PlacementRepo,
		OutputPath:       outputPath,
		ExistingContent:  existingContent,
		OriginalContent:  originalContent,
		ExistingPath:     existingPath,
	}
	if i.force {
		cc.OriginalContent, cc.ExistingContent = nil, nil
		cc.ExistingPath = nil
	}
	if cfg.Ejectable {
		cc.ProjectTypes, cc.ProjectLocations = nil, nil
	}
	contextObj := config.NewContentContextObject(i.vm, cc)

	result, err := contentFunc(goja.Undefined(), contextObj)
	if err != nil {
		return "", false, fmt.Errorf("failed to call content function: %w", err)
	}

	// undefined/null is an explicit opt-out, not an error: the generator chose
	// not to produce this file (e.g. "manage only if it already exists").
	if result == nil || goja.IsUndefined(result) || goja.IsNull(result) {
		return "", true, nil
	}

	return result.String(), false, nil
}

// fileExists checks if a file exists
func (i *Installer) fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
