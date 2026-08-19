package tooling

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/datamitsuignore"
	"github.com/datamitsu/datamitsu/internal/project"
	"github.com/datamitsu/datamitsu/internal/timing"
	"github.com/datamitsu/datamitsu/internal/traverser"

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

// PlatformChecker reports whether an app's backing binary is available for the
// current host. It lets the planner mark platform-unsupported tools as skipped
// at plan time (so they surface in --explain and never reach install), without
// the planner resolving binaries itself. Satisfied by *binmanager.BinManager.
type PlatformChecker interface {
	// BinaryAvailable returns (true, "") when the app can run on this host, and
	// (false, detail) when its binary has no build for the current os/arch/libc
	// (detail is the host string). Non-binary apps always return available.
	BinaryAvailable(app string) (available bool, detail string)
}

// Planner creates execution plans for tools
type Planner struct {
	rootPath           string
	cwdPath            string
	detectedTypes      []string // Detected project type names
	tools              config.MapOfTools
	projectTypesConfig config.MapOfProjectTypes // Project type definitions

	// platformChecker, when set, marks tools whose binary is unavailable for the
	// current host as skipped instead of planning them. Optional (nil disables
	// the check), injected via SetPlatformChecker.
	platformChecker PlatformChecker

	// Extra ignore rules from config (Config.IgnoreRules)
	extraIgnoreRules []string

	// Cache fields for performance optimization
	cachedFiles      []string                  // All files in repo (cached)
	cachedProjects   []project.ProjectLocation // All project locations (cached)
	cacheInitialized bool                      // Whether cache has been populated

	// .datamitsuignore matcher for disabling tools per file
	ignoreMatcher *datamitsuignore.Matcher

	// execution is the run-shaping policy from config; nil means defaults.
	execution *config.Execution
	// widenOverride is a one-off --widen-to; it overrides config in either
	// direction (see config.Execution.ResolveWidenTo).
	widenOverride config.WidenTo

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

// SetWidenPolicy wires the run's widening policy. Left unset, every operation
// takes config.DefaultWidenTo.
func (p *Planner) SetWidenPolicy(exec *config.Execution, override config.WidenTo) {
	p.execution = exec
	p.widenOverride = override
}

// SetPlatformChecker injects the host-availability checker used to skip
// platform-unsupported tools. Wired from the runner after the BinManager exists;
// left nil in contexts (e.g. some tests) where the check is not needed.
func (p *Planner) SetPlatformChecker(c PlatformChecker) {
	p.platformChecker = c
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

// Plan creates an execution plan for the given operation and files
func (p *Planner) Plan(ctx context.Context, operation config.OperationType, sel Selection, selectedTools []string) (*ExecutionPlan, error) {
	// Initialize cache once before planning
	if err := p.initializeCache(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize cache: %w", err)
	}

	// Collect all applicable tasks (now uses cached data)
	var tasks []Task
	var skipped []SkippedTool
	func() {
		defer p.timings.Start("Collect tasks")()
		tasks, skipped = p.collectTasks(ctx, operation, sel)
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
			skipped = filterSkippedBySelectedTools(skipped, selectedTools)
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

	return &ExecutionPlan{Groups: groups, Skipped: skipped}, nil
}

// collectTasks collects all tasks (and explicitly-skipped tools) for the operation.
func (p *Planner) collectTasks(ctx context.Context, operation config.OperationType, sel Selection) ([]Task, []SkippedTool) {
	// An empty selection is a selection: it targets nothing, so nothing runs.
	if sel.Mode == SelectionEmpty {
		return nil, nil
	}

	var tasks []Task
	var skipped []SkippedTool

	widenTo := p.execution.ResolveWidenTo(operation, p.widenOverride)

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

		// Explicit config skip: report it, never plan it. Checked after
		// applicability/operation so a skip:true tool irrelevant to this repo or
		// operation does not add noise.
		if tool.Skip {
			skipped = append(skipped, SkippedTool{
				ToolName:  toolName,
				Operation: operation,
				Reason:    SkipReasonConfig,
				Detail:    tool.SkipReason,
			})
			continue
		}

		// Platform skip: the backing binary has no build for this host. Mark it
		// skipped instead of letting install hard-fail later.
		if p.platformChecker != nil && opConfig.App != "" {
			if ok, detail := p.platformChecker.BinaryAvailable(opConfig.App); !ok {
				skipped = append(skipped, SkippedTool{
					ToolName:  toolName,
					Operation: operation,
					Reason:    SkipReasonUnsupportedPlatform,
					Detail:    detail,
				})
				continue
			}
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
			// A repository-scoped operation used to be dropped outright when cwd
			// was not the git root — silently, without even a skip entry. What
			// actually matters is whether its verdict survives being narrowed:
			// one that takes a file list does, one that answers a whole-repository
			// question does not.
			repoWide := config.InferGranularity(opConfig) != config.GranularityFile
			if repoWide && p.cwdPath != p.rootPath && widenTo != config.WidenToRepo {
				skipped = append(skipped, SkippedTool{
					ToolName:  toolName,
					Operation: operation,
					Reason:    SkipReasonNotNarrowable,
				})
				continue
			}
			// Respect .datamitsuignore: skip when this tool is disabled for the
			// repository root. Only short-circuit when there are no globs to
			// enumerate (e.g. knip) — then there are no files to filter and the
			// project-level probe is the only signal. With globs, per-file ignore
			// filtering below handles disabling and, unlike the root-only project
			// probe, respects subdir re-enables via inversion (e.g.
			// "**/*: t" + "!config/**/*: t").
			if len(opConfig.Globs) == 0 && p.isToolDisabledForProject(toolName, p.rootPath) {
				continue
			}
			// Permitting the run has to widen its input too. A whole-repository
			// verdict is computed over the whole repository, so once the policy
			// allows it the selection that narrowed it no longer applies —
			// otherwise --widen-to=repo un-skips the tool and then drops it for
			// matching none of the named paths, which is the silent no-op the
			// skip entry existed to expose.
			matchSel := sel
			if repoWide && widenTo == config.WidenToRepo {
				matchSel = Selection{Mode: SelectionAll}
			}
			// Skip when globs are configured but no files match (consistent with per-project behavior).
			matchedFiles := p.matchFiles(ctx, matchSel, opConfig)
			// Narrowable from a subdirectory: restrict the batch to what was asked
			// for. ProjectPath below stays the git root, so the process still starts
			// there and {root}-anchored config paths keep resolving.
			if config.InferGranularity(opConfig) == config.GranularityFile {
				matchedFiles = p.selectionFilterToCwd(sel, matchedFiles)
			}
			// Respect file-specific .datamitsuignore rules (e.g. "**/foo.toml: oxfmt"):
			// prune individual files from the batch even though the tool runs once.
			matchedFiles = p.filterFilesByIgnore(toolName, matchedFiles)
			if len(matchedFiles) > 0 || len(opConfig.Globs) == 0 {
				task.Files = matchedFiles
				task.ProjectPath = p.rootPath
				p.attachUnit(&task, p.rootPath)
				tasks = append(tasks, task)
			}

		case config.ToolScopePerProject:
			// Per-project scope: run for each detected project in its directory
			matchedFiles := p.filterFilesByIgnore(toolName, p.selectionFilterToCwd(sel, p.matchFiles(ctx, sel, opConfig)))

			if len(matchedFiles) > 0 || len(opConfig.Globs) == 0 {
				unitTasks, narrowedAway := p.collectUnitTasks(ctx, task, sel, matchedFiles, widenTo)
				if narrowedAway {
					skipped = append(skipped, SkippedTool{
						ToolName:  toolName,
						Operation: operation,
						Reason:    SkipReasonNotNarrowable,
					})
					continue
				}
				tasks = append(tasks, unitTasks...)
			}

		case config.ToolScopePerFile:
			// Per-file scope: run for each file in its directory
			matchedFiles := p.selectionFilterToCwd(sel, p.matchFiles(ctx, sel, opConfig))

			for _, file := range matchedFiles {
				if p.isToolDisabledForFile(toolName, file) {
					continue
				}
				fileTask := task
				fileTask.Files = []string{file}
				fileTask.ProjectPath = filepath.Dir(file)
				p.attachUnit(&fileTask, fileTask.ProjectPath)
				tasks = append(tasks, fileTask)
			}

		default:
			// Default to per-project for safety
			matchedFiles := p.filterFilesByIgnore(toolName, p.selectionFilterToCwd(sel, p.matchFiles(ctx, sel, opConfig)))

			if len(matchedFiles) > 0 || len(opConfig.Globs) == 0 {
				unitTasks, narrowedAway := p.collectUnitTasks(ctx, task, sel, matchedFiles, widenTo)
				if narrowedAway {
					skipped = append(skipped, SkippedTool{
						ToolName:  toolName,
						Operation: operation,
						Reason:    SkipReasonNotNarrowable,
					})
					continue
				}
				tasks = append(tasks, unitTasks...)
			}
		}
	}

	return tasks, skipped
}

// matchFiles resolves an operation's glob-matched file set for the selection.
// Named paths are filtered down to the operation's globs; every other mode
// sweeps the cached repository walk. Scope-specific cwd and .datamitsuignore
// filtering is applied by the caller, which is where the scopes genuinely differ.
func (p *Planner) matchFiles(ctx context.Context, sel Selection, op config.ToolOperation) []string {
	if len(op.Globs) == 0 {
		return nil
	}
	var matched []string
	if named := sel.Files(); len(named) > 0 {
		matched = p.filterFilesByGlobs(named, op.Globs)
	} else {
		matched = p.findFilesByGlobs(ctx, op.Globs)
	}
	return p.excludeFilesByGlobs(matched, op.ExcludeGlobs)
}

// filterSkippedBySelectedTools keeps only skipped entries whose tool is in the
// --tools selection, mirroring filterTasksBySelectedTools for the skip list.
func filterSkippedBySelectedTools(skipped []SkippedTool, selectedTools []string) []SkippedTool {
	selected := make(map[string]bool, len(selectedTools))
	for _, name := range selectedTools {
		selected[name] = true
	}
	filtered := make([]SkippedTool, 0, len(skipped))
	for _, s := range skipped {
		if selected[s.ToolName] {
			filtered = append(filtered, s)
		}
	}
	return filtered
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

// selectionFilterToCwd restricts files to the cwd subtree, except when the user
// named paths explicitly. `cd services/api && dm fix ../web/x.ts` used to drop
// the named file and report nothing: cwd is where you happen to stand, but a
// path on the command line is a decision.
func (p *Planner) selectionFilterToCwd(sel Selection, files []string) []string {
	if sel.Mode == SelectionPaths {
		return files
	}
	return p.filterFilesToCwd(files)
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

// selectionPermitsUnit reports whether processing the whole of unitDir stays
// inside what widenTo allows for this selection.
//
// It only matters for an operation with no path in argv: that one reads its
// whole unit whatever file list it was given, so planning it in a unit the
// selection does not reach widens the run past the declared policy — and these
// tools fix in place, which means rewriting files nobody named.
func (p *Planner) selectionPermitsUnit(sel Selection, unitDir string, widenTo config.WidenTo) bool {
	if widenTo == config.WidenToRepo {
		return true
	}
	switch sel.Mode {
	case SelectionAll:
		return true
	case SelectionEmpty:
		return false
	case SelectionSubtree:
		if p.contains(sel.Dir, unitDir) {
			return true
		}
		// Standing below a unit root still means "this unit", but only once the
		// policy allows widening to it.
		return widenTo == config.WidenToUnit && p.contains(unitDir, sel.Dir)
	case SelectionPaths:
		// Widening to the unit holding a named path is exactly what "unit" is.
		if widenTo == config.WidenToUnit {
			for _, path := range sel.Paths {
				if p.contains(unitDir, path) {
					return true
				}
			}
		}
		// Under "target" a whole unit is more than what was named, so the tool is
		// reported rather than run.
		return false
	}
	return false
}

// collectUnitTasks plans the per-project tasks for one operation, dropping the
// units the widening policy does not reach.
func (p *Planner) collectUnitTasks(
	ctx context.Context, task Task, sel Selection, matchedFiles []string, widenTo config.WidenTo,
) (kept []Task, narrowedAway bool) {
	projectTasks := p.createPerProjectTasksWithFiles(ctx, task, matchedFiles)
	for i := range projectTasks {
		p.attachUnit(&projectTasks[i], projectTasks[i].ProjectPath)
	}
	if config.ArgsReferenceFiles(task.OpConfig.Args) {
		// argv carries the paths, so the tool touches those and nothing else.
		return projectTasks, false
	}
	for _, projectTask := range projectTasks {
		if p.selectionPermitsUnit(sel, projectTask.ProjectPath, widenTo) {
			kept = append(kept, projectTask)
			continue
		}
		narrowedAway = true
	}
	// Only a tool left with nothing to do has failed to answer: dropping the
	// units the user did not ask about is the policy working, not a skip.
	return kept, narrowedAway && len(kept) == 0
}

// contains reports whether dir is an ancestor of (or equal to) path.
func (p *Planner) contains(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// filterProjectLocationsToSubtree keeps the locations under cwd plus the nearest
// one containing it.
func (p *Planner) filterProjectLocationsToSubtree(locs []project.ProjectLocation) []project.ProjectLocation {
	if p.cwdPath == p.rootPath {
		return locs
	}

	out := p.filterProjectLocationsToCwd(locs)
	if len(out) > 0 {
		return out
	}

	// Nothing below: fall back to the deepest project containing cwd.
	var nearest *project.ProjectLocation
	for i, loc := range locs {
		if !p.contains(loc.Path, p.cwdPath) {
			continue
		}
		if nearest == nil || len(loc.Path) > len(nearest.Path) {
			nearest = &locs[i]
		}
	}
	if nearest != nil {
		return []project.ProjectLocation{*nearest}
	}
	return nil
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

// filterFilesByIgnore drops files for which the tool is disabled by a
// file-specific .datamitsuignore rule. It is applied to repository- and
// per-project-scoped tools so that file-granular rules (e.g. "**/foo.toml: oxfmt")
// prune individual files from the batch, mirroring the per-file scope behavior.
func (p *Planner) filterFilesByIgnore(toolName string, files []string) []string {
	if p.ignoreMatcher == nil {
		return files
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		if !p.isToolDisabledForFile(toolName, f) {
			out = append(out, f)
		}
	}
	return out
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
	priorities := make([]int, 0, len(priorityMap))
	for priority := range priorityMap {
		priorities = append(priorities, priority)
	}
	sort.Ints(priorities)

	// Create task groups
	groups := make([]TaskGroup, 0, len(priorities))
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
		if slices.Contains(detectedTypes, toolType) {
			return true
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
		availableList := make([]string, 0, len(p.tools))
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
func (p *Planner) createPerProjectTasksWithFiles(ctx context.Context, baseTask Task, files []string) []Task {
	// Use cached projects instead of detecting again
	var locations []project.ProjectLocation

	if p.cacheInitialized {
		locations = p.cachedProjects
	} else {
		// Fallback to old behavior if cache not initialized
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
			if slices.Contains(baseTask.Tool.ProjectTypes, loc.Type) {
				filteredLocations = append(filteredLocations, loc)
			}
		}
	}

	// Restrict to cwd subtree (no-op when cwdPath == rootPath), keeping the
	// project that CONTAINS cwd as well as those under it. Descendants alone is
	// the wrong reading of "the app I am standing in": from services/api/src
	// there are no projects below, so the run planned nothing and reported
	// nothing — the same silent emptiness this work exists to remove.
	filteredLocations = p.filterProjectLocationsToSubtree(filteredLocations)

	// If no matching projects found after filtering
	if len(filteredLocations) == 0 {
		if p.cwdPath != p.rootPath {
			return nil
		}
		// Don't resurrect a tool that .datamitsuignore disabled for the root.
		if p.isToolDisabledForProject(baseTask.ToolName, p.rootPath) {
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

		// Under cwd, or containing it: standing below a package root is still
		// standing in that package.
		if !p.isUnderCwd(projectPath) && !p.contains(projectPath, p.cwdPath) {
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
		// Don't resurrect a tool that .datamitsuignore disabled for the root.
		if p.isToolDisabledForProject(baseTask.ToolName, p.rootPath) {
			return nil
		}
		baseTask.Files = files
		return []Task{baseTask}
	}

	return tasks
}

// findFilesByGlobs finds all files in the repository matching the given glob patterns
// Now uses cached file list instead of scanning every time
func (p *Planner) findFilesByGlobs(ctx context.Context, globs []string) []string {
	// Use cached files instead of scanning again
	if !p.cacheInitialized {
		// Fallback to old behavior if cache not initialized
		allFiles, err := traverser.FindFilesFromPath(ctx, p.rootPath, p.rootPath)
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

// excludeFilesByGlobs removes files matching any of the given exclude glob patterns.
// Nil or empty excludeGlobs is a no-op and returns the input slice unchanged.
func (p *Planner) excludeFilesByGlobs(files []string, excludeGlobs []string) []string {
	if len(excludeGlobs) == 0 {
		return files
	}

	kept := make([]string, 0, len(files))
	for _, file := range files {
		relPath, err := filepath.Rel(p.rootPath, file)
		if err != nil {
			relPath = file
		}

		excluded := false
		for _, glob := range excludeGlobs {
			match, err := doublestar.Match(glob, relPath)
			if err == nil && match {
				excluded = true
				break
			}
		}
		if !excluded {
			kept = append(kept, file)
		}
	}

	return kept
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

	// Check if glob patterns overlap.
	// Note: ExcludeGlobs is intentionally not considered here. Two tools whose
	// Globs match the same files are treated as overlapping even if their
	// ExcludeGlobs differ — this is conservative and avoids missing real
	// conflicts when exclusions evaluate to overlapping concrete file sets.
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
		return fmt.Errorf("failed to initialize cache: %w", err)
	}

	// Build .datamitsuignore matcher from scanned files
	var ignoreErr error
	func() {
		defer cacheTimings.StartChild("Build datamitsuignore matcher")()
		p.ignoreMatcher, ignoreErr = p.buildIgnoreMatcher()
	}()
	if ignoreErr != nil {
		return fmt.Errorf("failed to build .datamitsuignore matcher: %w", ignoreErr)
	}

	p.cacheInitialized = true
	return nil
}

// buildIgnoreMatcher scans cached files for .datamitsuignore entries and builds
// a Matcher. Config-defined ignore rules (extraIgnoreRules) are added as
// root-level rules.
//
// A malformed user .datamitsuignore is a hard error rather than a warning: a
// parse failure drops every rule in that file, which would silently let tools
// run on paths the user meant to exclude (e.g. a formatter rewriting a file).
// Unknown tool names are warned (the intended tool simply never gets disabled).
//
// File discovery mirrors internal/bundled's lint/fix: both consume the same
// gitignore-aware traversal (cachedFiles here, traverser.FindFilesFromPath
// there), so the set of files validated and the set applied stay identical.
func (p *Planner) buildIgnoreMatcher() (*datamitsuignore.Matcher, error) {
	m := datamitsuignore.NewMatcher()

	// Built-in: never run tools on the managed symlinks directory.
	if err := m.AddFile("", ".datamitsu/**: *"); err != nil {
		log.Warn("failed to add built-in .datamitsu ignore rule", zap.Error(err))
	}

	// Config-defined rules are already validated at config load; warn (not fail)
	// here to avoid double-reporting the same parse error.
	if len(p.extraIgnoreRules) > 0 {
		if err := m.AddFile("", strings.Join(p.extraIgnoreRules, "\n")); err != nil {
			log.Warn("failed to parse config-defined ignore rules", zap.Error(err))
		}
	}

	known := make(map[string]bool, len(p.tools))
	for name := range p.tools {
		known[name] = true
	}

	const filename = ".datamitsuignore"

	for _, f := range p.cachedFiles {
		if filepath.Base(f) != filename {
			continue
		}
		content, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", f, err)
		}
		rules, err := datamitsuignore.Parse(string(content))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		for _, tool := range datamitsuignore.UnknownTools(rules, known) {
			log.Warn("unknown tool in .datamitsuignore",
				zap.String("file", f),
				zap.String("tool", tool),
			)
		}
		relDir, err := filepath.Rel(p.rootPath, filepath.Dir(f))
		if err != nil {
			return nil, fmt.Errorf("resolving path for %s: %w", f, err)
		}
		if relDir == "." {
			relDir = ""
		}
		m.AddRules(relDir, rules)
	}

	return m, nil
}
