package tooling

import (
	"context"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/datamitsuignore"
	"github.com/datamitsu/datamitsu/internal/project"
	"github.com/datamitsu/datamitsu/internal/timing"
	"github.com/datamitsu/datamitsu/internal/traverser"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// ToolNotFoundError is returned when selected tools are not found
type ToolNotFoundError struct {
	NotFound  []string
	Available []string
}

func (e *ToolNotFoundError) Error() string {
	return fmt.Sprintf(
		"tools not found: %s\navailable tools: %s",
		strings.Join(e.NotFound, ", "),
		strings.Join(e.Available, ", "),
	)
}

// Planner creates execution plans for tools
type Planner struct {
	rootPath           string
	cwdPath            string
	detectedTypes      []string // Detected project type names
	tools              config.MapOfTools
	projectTypesConfig config.MapOfProjectTypes // Project type definitions

	// Extra ignore rules from config (Config.IgnoreRules)
	extraIgnoreRules []string

	// Cache fields for performance optimization
	cachedFiles      []string                  // All files in repo (cached)
	cachedProjects   []project.ProjectLocation // All project locations (cached)
	cacheInitialized bool                      // Whether cache has been populated

	// .datamitsuignore matcher for disabling tools per file
	ignoreMatcher *datamitsuignore.Matcher

	// Timings for performance measurement
	timings *timing.Timings
}

// NewPlanner creates a new tool execution planner
func NewPlanner(
	rootPath string,
	cwdPath string,
	detectedTypes []string,
	tools config.MapOfTools,
	projectTypesConfig config.MapOfProjectTypes,
	extraIgnoreRules []string,
) *Planner {
	return &Planner{
		rootPath:           filepath.Clean(rootPath),
		cwdPath:            filepath.Clean(cwdPath),
		detectedTypes:      detectedTypes,
		tools:              tools,
		projectTypesConfig: projectTypesConfig,
		extraIgnoreRules:   extraIgnoreRules,
		timings:            timing.New(),
	}
}

// GetTimings returns the timing measurements for this planner
func (p *Planner) GetTimings() *timing.Timings {
	return p.timings
}

// GetDetectedProjectTypes returns unique project type names from cached projects
// Must be called after initializeCache
func (p *Planner) GetDetectedProjectTypes() []string {
	if !p.cacheInitialized {
		return p.detectedTypes // Fallback to original detected types
	}

	// Extract unique type names from cached projects
	typeSet := make(map[string]bool)
	for _, loc := range p.cachedProjects {
		typeSet[loc.Type] = true
	}

	// Convert to slice
	types := make([]string, 0, len(typeSet))
	for typeName := range typeSet {
		types = append(types, typeName)
	}

	return types
}

// initializeCache performs expensive one-time operations:
// - Scans all files in repository (respecting .gitignore)
// - Detects all project locations
// This is called once before planning begins
func (p *Planner) initializeCache(ctx context.Context) error {
	if p.cacheInitialized {
		return nil
	}

	// Track timing with children for parallel operations
	cacheTimings := p.timings.StartWithChildren("Cache initialization")
	defer cacheTimings.End()

	// Create detector once
	detector := project.NewDetector(p.rootPath, p.projectTypesConfig)

	// Use errgroup for parallel execution
	g, gctx := errgroup.WithContext(ctx)

	// Goroutine 1: Scan all files
	g.Go(func() error {
		defer cacheTimings.StartChild("Scan files")()
		files, err := traverser.FindFilesFromPath(gctx, p.rootPath, p.rootPath)
		if err != nil {
			return fmt.Errorf("failed to scan files: %w", err)
		}
		p.cachedFiles = files
		return nil
	})

	// Goroutine 2: Detect all projects
	g.Go(func() error {
		defer cacheTimings.StartChild("Detect projects")()
		locations, err := detector.DetectAllWithLocations(gctx)
		if err != nil {
			return fmt.Errorf("failed to detect projects: %w", err)
		}
		p.cachedProjects = locations
		return nil
	})

	// Wait for both to complete
	if err := g.Wait(); err != nil {
		return err
	}

	// Build .datamitsuignore matcher from scanned files
	func() {
		defer cacheTimings.StartChild("Build datamitsuignore matcher")()
		p.ignoreMatcher = p.buildIgnoreMatcher()
	}()

	p.cacheInitialized = true
	return nil
}

// buildIgnoreMatcher scans cached files for .datamitsuignore entries
// and builds a Matcher. Config-defined ignore rules (extraIgnoreRules)
// are added as root-level rules.
func (p *Planner) buildIgnoreMatcher() *datamitsuignore.Matcher {
	m := datamitsuignore.NewMatcher()

	// Built-in: never run tools on the managed symlinks directory.
	if err := m.AddFile("", ".datamitsu/**: *"); err != nil {
		log.Warn("failed to add built-in .datamitsu ignore rule", zap.Error(err))
	}

	if len(p.extraIgnoreRules) > 0 {
		if err := m.AddFile("", strings.Join(p.extraIgnoreRules, "\n")); err != nil {
			log.Warn("failed to parse config-defined ignore rules",
				zap.Error(err),
			)
		}
	}

	const filename = ".datamitsuignore"

	for _, f := range p.cachedFiles {
		if filepath.Base(f) != filename {
			continue
		}
		content, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		relDir, err := filepath.Rel(p.rootPath, filepath.Dir(f))
		if err != nil {
			continue
		}
		if relDir == "." {
			relDir = ""
		}
		if err := m.AddFile(relDir, string(content)); err != nil {
			log.Warn("failed to parse .datamitsuignore",
				zap.String("file", f),
				zap.Error(err),
			)
		}
	}

	return m
}

// Plan creates an execution plan for the given operation and files
func (p *Planner) Plan(ctx context.Context, operation config.OperationType, files []string, selectedTools []string) (*ExecutionPlan, error) {
	// Initialize cache once before planning
	if err := p.initializeCache(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize cache: %w", err)
	}

	// Collect all applicable tasks (now uses cached data)
	var tasks []Task
	func() {
		defer p.timings.Start("Collect tasks")()
		tasks = p.collectTasks(operation, files)
	}()

	// Filter by selectedTools if specified
	if len(selectedTools) > 0 {
		var filterErr error
		func() {
			defer p.timings.Start("Filter by selected tools")()
			filteredTasks, err := p.filterTasksBySelectedTools(tasks, selectedTools)
			if err != nil {
				filterErr = err
				return
			}
			tasks = filteredTasks
		}()
		if filterErr != nil {
			return nil, filterErr
		}
	}

	// Group by priority and detect overlaps
	var groups []TaskGroup
	func() {
		defer p.timings.Start("Group by priority")()
		groups = p.groupByPriority(tasks)
	}()

	return &ExecutionPlan{Groups: groups}, nil
}

// collectTasks collects all tasks for the given operation
func (p *Planner) collectTasks(operation config.OperationType, files []string) []Task {
	var tasks []Task

	toolNames := make([]string, 0, len(p.tools))
	for name := range p.tools {
		toolNames = append(toolNames, name)
	}
	sort.Strings(toolNames)

	for _, toolName := range toolNames {
		tool := p.tools[toolName]
		// Check if tool applies to current project types
		if !p.isApplicableTool(tool) {
			continue
		}

		// Check if tool supports this operation
		opConfig, hasOp := tool.Operations[operation]
		if !hasOp {
			continue
		}

		task := Task{
			ToolName:  toolName,
			Tool:      tool,
			Operation: operation,
			OpConfig:  opConfig,
		}

		// Match files and create tasks based on scope
		switch opConfig.Scope {
		case config.ToolScopeRepository:
			// Repository scope: only run when cwd is the git root.
			if p.cwdPath != p.rootPath {
				continue
			}
			// Skip when globs are configured but no files match (consistent with per-project behavior).
			var matchedFiles []string
			if len(opConfig.Globs) > 0 {
				if len(files) == 0 {
					matchedFiles = p.findFilesByGlobs(opConfig.Globs)
				} else {
					matchedFiles = p.filterFilesByGlobs(files, opConfig.Globs)
				}
			}
			if len(matchedFiles) > 0 || len(opConfig.Globs) == 0 {
				task.Files = matchedFiles
				task.ProjectPath = p.rootPath
				tasks = append(tasks, task)
			}

		case config.ToolScopePerProject:
			// Per-project scope: run for each detected project in its directory
			var matchedFiles []string
			if len(opConfig.Globs) > 0 {
				if len(files) == 0 {
					matchedFiles = p.findFilesByGlobs(opConfig.Globs)
				} else {
					matchedFiles = p.filterFilesByGlobs(files, opConfig.Globs)
				}
				matchedFiles = p.filterFilesToCwd(matchedFiles)
			}

			if len(matchedFiles) > 0 || len(opConfig.Globs) == 0 {
				projectTasks := p.createPerProjectTasksWithFiles(task, matchedFiles)
				tasks = append(tasks, projectTasks...)
			}

		case config.ToolScopePerFile:
			// Per-file scope: run for each file in its directory
			var matchedFiles []string
			if len(files) == 0 {
				matchedFiles = p.findFilesByGlobs(opConfig.Globs)
			} else {
				matchedFiles = p.filterFilesByGlobs(files, opConfig.Globs)
			}
			matchedFiles = p.filterFilesToCwd(matchedFiles)

			for _, file := range matchedFiles {
				if p.isToolDisabledForFile(toolName, file) {
					continue
				}
				fileTask := task
				fileTask.Files = []string{file}
				fileTask.ProjectPath = filepath.Dir(file)
				tasks = append(tasks, fileTask)
			}

		default:
			// Default to per-project for safety
			var matchedFiles []string
			if len(opConfig.Globs) > 0 {
				if len(files) == 0 {
					matchedFiles = p.findFilesByGlobs(opConfig.Globs)
				} else {
					matchedFiles = p.filterFilesByGlobs(files, opConfig.Globs)
				}
				matchedFiles = p.filterFilesToCwd(matchedFiles)
			}

			if len(matchedFiles) > 0 || len(opConfig.Globs) == 0 {
				projectTasks := p.createPerProjectTasksWithFiles(task, matchedFiles)
				tasks = append(tasks, projectTasks...)
			}
		}
	}

	return tasks
}

// isUnderCwd reports whether path is inside (or equal to) p.cwdPath.
// When cwdPath == rootPath, it returns true unconditionally.
func (p *Planner) isUnderCwd(path string) bool {
	if p.cwdPath == p.rootPath {
		return true
	}

	rel, err := filepath.Rel(p.cwdPath, filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// filterFilesToCwd returns only those files that are under p.cwdPath.
// No-op when cwdPath == rootPath.
func (p *Planner) filterFilesToCwd(files []string) []string {
	if p.cwdPath == p.rootPath {
		return files
	}
	var out []string
	for _, f := range files {
		if p.isUnderCwd(f) {
			out = append(out, f)
		}
	}
	return out
}

// filterProjectLocationsToCwd returns only those project locations whose Path
// is under p.cwdPath.  No-op when cwdPath == rootPath.
func (p *Planner) filterProjectLocationsToCwd(locs []project.ProjectLocation) []project.ProjectLocation {
	if p.cwdPath == p.rootPath {
		return locs
	}
	var out []project.ProjectLocation
	for _, loc := range locs {
		if p.isUnderCwd(loc.Path) {
			out = append(out, loc)
		}
	}
	return out
}

// isToolDisabledForFile checks if the tool is disabled for the given absolute file path
// using the .datamitsuignore matcher.
func (p *Planner) isToolDisabledForFile(toolName string, absFilePath string) bool {
	if p.ignoreMatcher == nil {
		return false
	}
	relPath, err := filepath.Rel(p.rootPath, absFilePath)
	if err != nil {
		return false
	}
	return p.ignoreMatcher.IsDisabled(toolName, relPath)
}

// isToolDisabledForProject checks if the tool is disabled for an entire project
// directory using the .datamitsuignore matcher.
func (p *Planner) isToolDisabledForProject(toolName string, absProjectDir string) bool {
	if p.ignoreMatcher == nil {
		return false
	}
	relDir, err := filepath.Rel(p.rootPath, absProjectDir)
	if err != nil {
		return false
	}
	if relDir == "." {
		relDir = ""
	}
	return p.ignoreMatcher.IsProjectDisabled(toolName, relDir)
}

// groupByPriority groups tasks by priority and detects overlaps within each priority level
func (p *Planner) groupByPriority(tasks []Task) []TaskGroup {
	// Group tasks by priority
	priorityMap := make(map[int][]Task)
	for _, task := range tasks {
		priority := task.OpConfig.Priority
		priorityMap[priority] = append(priorityMap[priority], task)
	}

	// Get sorted priority levels
	var priorities []int
	for priority := range priorityMap {
		priorities = append(priorities, priority)
	}
	sort.Ints(priorities)

	// Create task groups
	var groups []TaskGroup
	for _, priority := range priorities {
		tasks := priorityMap[priority]

		// Within the same priority, we could further split by overlaps
		// For now, we keep all tasks with same priority in one group
		// The executor will handle sequential vs parallel execution
		groups = append(groups, TaskGroup{
			Priority: priority,
			Tasks:    tasks,
		})
	}

	return groups
}

// isApplicableTool checks if a tool applies to current project types
func (p *Planner) isApplicableTool(tool config.Tool) bool {
	// If tool has no project type restrictions, it applies
	if len(tool.ProjectTypes) == 0 {
		return true
	}

	// Get detected types from cache or fallback
	detectedTypes := p.GetDetectedProjectTypes()

	// Check if any project type matches
	for _, toolType := range tool.ProjectTypes {
		for _, detectedType := range detectedTypes {
			if toolType == detectedType {
				return true
			}
		}
	}

	return false
}

// filterTasksBySelectedTools filters tasks to only include selected tools.
// Validates tool names against the full config (not just the current operation's tasks)
// so that tools missing one operation type don't cause errors in RunSequential.
func (p *Planner) filterTasksBySelectedTools(tasks []Task, selectedTools []string) ([]Task, error) {
	// Validate against full config to allow tools that exist but lack the current operation
	var notFound []string
	for _, name := range selectedTools {
		if _, exists := p.tools[name]; !exists {
			notFound = append(notFound, name)
		}
	}

	if len(notFound) > 0 {
		var availableList []string
		for name := range p.tools {
			availableList = append(availableList, name)
		}
		sort.Strings(availableList)

		return nil, &ToolNotFoundError{
			NotFound:  notFound,
			Available: availableList,
		}
	}

	// Filter to only selected tools
	selected := make(map[string]bool)
	for _, name := range selectedTools {
		selected[name] = true
	}

	var filtered []Task
	for _, task := range tasks {
		if selected[task.ToolName] {
			filtered = append(filtered, task)
		}
	}

	return filtered, nil
}

// groupFilesByProject groups files by their containing project
// Each file belongs to its NEAREST parent project (not all ancestor projects)
// This ensures files are processed exactly once
func (p *Planner) groupFilesByProject(files []string, projectLocations []project.ProjectLocation) map[string][]string {
	result := make(map[string][]string)

	// Sort projects by path length (longest first)
	// This ensures we check more specific (deeper) projects first
	sortedProjects := make([]project.ProjectLocation, len(projectLocations))
	copy(sortedProjects, projectLocations)
	sort.Slice(sortedProjects, func(i, j int) bool {
		return len(sortedProjects[i].Path) > len(sortedProjects[j].Path)
	})

	for _, file := range files {
		// Find the NEAREST parent project (deepest match)
		var belongsTo string

		for _, loc := range sortedProjects {
			// Check if file is under this project directory
			relPath, err := filepath.Rel(loc.Path, file)
			if err != nil {
				continue
			}

			// File is under this project if relPath doesn't escape via parent traversal
			if relPath != ".." && !strings.HasPrefix(relPath, ".."+string(filepath.Separator)) && relPath != "." {
				// This is the nearest parent (because we sorted by depth)
				belongsTo = loc.Path
				break
			}
		}

		// If no project found, use root path
		if belongsTo == "" {
			belongsTo = p.rootPath
		}

		result[belongsTo] = append(result[belongsTo], file)
	}

	return result
}

// createPerProjectTasksWithFiles creates tasks per project, grouping files by project
// Now uses cached project locations instead of detecting every time
func (p *Planner) createPerProjectTasksWithFiles(baseTask Task, files []string) []Task {
	// Use cached projects instead of detecting again
	var locations []project.ProjectLocation

	if p.cacheInitialized {
		locations = p.cachedProjects
	} else {
		// Fallback to old behavior if cache not initialized
		ctx := context.Background()
		detector := project.NewDetector(p.rootPath, p.projectTypesConfig)
		locs, err := detector.DetectAllWithLocations(ctx)
		if err != nil {
			baseTask.Files = files
			return []Task{baseTask}
		}
		locations = locs
	}

	// Filter locations by tool's projectTypes
	var filteredLocations []project.ProjectLocation
	if len(baseTask.Tool.ProjectTypes) == 0 {
		// No restriction - use all locations
		filteredLocations = locations
	} else {
		// Filter by tool's projectTypes
		for _, loc := range locations {
			for _, toolType := range baseTask.Tool.ProjectTypes {
				if loc.Type == toolType {
					filteredLocations = append(filteredLocations, loc)
					break
				}
			}
		}
	}

	// Restrict to cwd subtree (no-op when cwdPath == rootPath)
	filteredLocations = p.filterProjectLocationsToCwd(filteredLocations)

	// If no matching projects found after filtering
	if len(filteredLocations) == 0 {
		if p.cwdPath != p.rootPath {
			return nil
		}
		baseTask.Files = files
		return []Task{baseTask}
	}

	// When no files are provided (no globs configured), run once per project without a file list
	if len(files) == 0 {
		seenPaths := make(map[string]bool)
		var tasks []Task
		for _, loc := range filteredLocations {
			if seenPaths[loc.Path] {
				continue
			}
			seenPaths[loc.Path] = true
			if p.isToolDisabledForProject(baseTask.ToolName, loc.Path) {
				continue
			}
			task := baseTask
			task.ProjectPath = loc.Path
			tasks = append(tasks, task)
		}
		return tasks
	}

	// Group files by project
	filesByProject := p.groupFilesByProject(files, filteredLocations)

	// Create deduplicated list of project paths
	seenPaths := make(map[string]bool)
	var tasks []Task

	for projectPath, projectFiles := range filesByProject {
		if seenPaths[projectPath] {
			continue
		}
		seenPaths[projectPath] = true

		if len(projectFiles) == 0 {
			continue
		}

		if !p.isUnderCwd(projectPath) {
			continue
		}

		if p.isToolDisabledForProject(baseTask.ToolName, projectPath) {
			continue
		}

		// Create task for this project
		task := baseTask
		task.ProjectPath = projectPath
		task.Files = projectFiles
		tasks = append(tasks, task)
	}

	// If no tasks created, return single task with all files
	if len(tasks) == 0 {
		if p.cwdPath != p.rootPath {
			return nil
		}
		baseTask.Files = files
		return []Task{baseTask}
	}

	return tasks
}

// findFilesByGlobs finds all files in the repository matching the given glob patterns
// Now uses cached file list instead of scanning every time
func (p *Planner) findFilesByGlobs(globs []string) []string {
	// Use cached files instead of scanning again
	if !p.cacheInitialized {
		// Fallback to old behavior if cache not initialized
		allFiles, err := traverser.FindFilesFromPath(context.Background(), p.rootPath, p.rootPath)
		if err != nil {
			return []string{}
		}
		return p.filterFilesByGlobs(allFiles, globs)
	}

	// Filter cached files by globs
	return p.filterFilesByGlobs(p.cachedFiles, globs)
}

// filterFilesByGlobs filters files that match any of the given glob patterns
func (p *Planner) filterFilesByGlobs(files []string, globs []string) []string {
	var matched []string

	for _, file := range files {
		// Make path relative to root for glob matching
		relPath, err := filepath.Rel(p.rootPath, file)
		if err != nil {
			relPath = file
		}

		for _, glob := range globs {
			match, err := doublestar.Match(glob, relPath)
			if err == nil && match {
				matched = append(matched, file)
				break
			}
		}
	}

	return matched
}

// HasOverlap checks if two tasks have overlapping file sets
func HasOverlap(task1, task2 Task) bool {
	// Repository-scoped tasks always overlap with everything (they operate on the entire repository,
	// including files in any project subdirectory). This check must precede the different-path guard.
	if task1.OpConfig.Scope == config.ToolScopeRepository || task2.OpConfig.Scope == config.ToolScopeRepository {
		return true
	}

	// Tasks from different projects never overlap (they work on different file sets)
	if task1.ProjectPath != "" && task2.ProjectPath != "" && task1.ProjectPath != task2.ProjectPath {
		return false
	}

	// Per-file tasks with different files never overlap (each processes exactly one file)
	if task1.OpConfig.Scope == config.ToolScopePerFile && task2.OpConfig.Scope == config.ToolScopePerFile {
		if len(task1.Files) == 1 && len(task2.Files) == 1 && task1.Files[0] != task2.Files[0] {
			return false
		}
	}

	// Check if glob patterns overlap
	return globsOverlap(task1.OpConfig.Globs, task2.OpConfig.Globs)
}

// globsOverlap checks if two sets of glob patterns have any overlap.
// Returns true (assumes overlap) unless the patterns can be proven disjoint
// by having no shared file extensions.
func globsOverlap(globs1, globs2 []string) bool {
	if len(globs1) == 0 || len(globs2) == 0 {
		return true
	}

	exts1 := extractGlobExtensions(globs1)
	exts2 := extractGlobExtensions(globs2)

	// If we couldn't extract extensions from all patterns, assume overlap
	if exts1 == nil || exts2 == nil {
		return true
	}

	for ext1 := range exts1 {
		for ext2 := range exts2 {
			if ext1 == ext2 || strings.HasSuffix(ext1, ext2) || strings.HasSuffix(ext2, ext1) {
				return true
			}
		}
	}
	return false
}

// extractGlobExtensions extracts file extensions from glob patterns.
// Returns nil if any pattern cannot be reduced to a set of extensions
// (e.g., patterns without extensions like "Makefile" or "src/**").
func extractGlobExtensions(globs []string) map[string]bool {
	exts := make(map[string]bool)
	for _, g := range globs {
		patternExts := parseGlobExtensions(g)
		if patternExts == nil {
			return nil
		}
		for _, ext := range patternExts {
			exts[ext] = true
		}
	}
	return exts
}

// parseGlobExtensions extracts file extensions from a single glob pattern.
// Handles patterns like "*.go", "**/*.{ts,tsx}", "**/*.js".
// Returns nil if the pattern cannot be reduced to extensions.
func parseGlobExtensions(pattern string) []string {
	// Find the last segment after the final path separator
	lastSlash := -1
	for i := len(pattern) - 1; i >= 0; i-- {
		if pattern[i] == '/' {
			lastSlash = i
			break
		}
	}
	filename := pattern[lastSlash+1:]

	// Must start with "*." to be an extension pattern
	if len(filename) < 3 || filename[0] != '*' || filename[1] != '.' {
		return nil
	}
	extPart := filename[2:]

	// Handle brace expansion like {ts,tsx,js}
	if len(extPart) > 2 && extPart[0] == '{' && extPart[len(extPart)-1] == '}' {
		inner := extPart[1 : len(extPart)-1]
		var exts []string
		start := 0
		for i := 0; i <= len(inner); i++ {
			if i == len(inner) || inner[i] == ',' {
				ext := inner[start:i]
				if ext == "" {
					return nil
				}
				exts = append(exts, "."+ext)
				start = i + 1
			}
		}
		return exts
	}

	// Reject extensions containing wildcards or braces
	for _, c := range extPart {
		if c == '*' || c == '?' || c == '{' || c == '}' || c == '[' {
			return nil
		}
	}

	return []string{"." + extPart}
}
