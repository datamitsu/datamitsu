// Package runner plans and executes lint/format operations across discovered
// projects, tracking progress and emitting CI-friendly output.
package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/bundled"
	"github.com/datamitsu/datamitsu/internal/cache"
	clr "github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/ocibundle"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"
	"github.com/datamitsu/datamitsu/internal/term"
	"github.com/datamitsu/datamitsu/internal/timing"
	"github.com/datamitsu/datamitsu/internal/tooling"
	"github.com/datamitsu/datamitsu/internal/traverser"
	"github.com/datamitsu/datamitsu/internal/ui"

	"go.uber.org/zap"
)

var log = logger.Logger.With(zap.Namespace("runner"))

// toolExecutionGroup groups all executions of a single tool
type toolExecutionGroup struct {
	toolName       string
	scope          config.ToolScope // Tool scope (repository, per-project, per-file)
	totalRuns      int
	succeededRuns  int
	failedRuns     int
	totalTime      int64     // Sum of per-run durations (serial CPU time; shown only in detailed view)
	wallTime       int64     // Wall-clock span across this tool's runs: max(end) − min(start)
	minTime        int64     // Minimum execution time (-1 if not set)
	maxTime        int64     // Maximum execution time (-1 if not set)
	minDir         string    // Project directory with minimum time
	maxDir         string    // Project directory with maximum time
	wallStart      time.Time // Earliest run start across this tool's runs (zero if none timed)
	wallEnd        time.Time // Latest run end across this tool's runs
	firstSeenIndex int       // Order in which tool was first seen (for preserving execution order)
	executions     []executionInstance
}

// executionInstance represents a single execution of a tool
type executionInstance struct {
	result      tooling.ExecutionResult
	relativeDir string
}

// Progress tracking variables
var (
	progressMu  sync.Mutex
	currentTask *ui.Task                   // shared file-processing task for the active operation
	activeTools map[string]map[string]bool // currently running tools (tool -> set of active dirs)
)

// toolPlanner is the planning surface used by runSingleOperation (satisfied by *tooling.Planner).
type toolPlanner interface {
	Plan(ctx context.Context, operation config.OperationType, files []string, selectedTools []string) (*tooling.ExecutionPlan, error)
	GetDetectedProjectTypes() []string
	GetTimings() *timing.Timings
}

// planExecutor is the execution surface used by runSingleOperation (satisfied by *tooling.Executor).
type planExecutor interface {
	SetResultCallback(cb tooling.ResultCallback)
	SetTaskStartCallback(cb tooling.TaskStartCallback)
	SetFileProgressCallback(cb tooling.FileProgressCallback)
	SetParser(parser tooling.DiagnosticParser)
	Execute(ctx context.Context, plan *tooling.ExecutionPlan) ([]tooling.GroupExecutionResult, error)
}

// toolEnsurer pre-installs every tool a plan needs before parallel execution
// (satisfied by *binmanager.BinManager). Installing up front closes the
// check-then-download race that surfaces under per-file parallelism.
type toolEnsurer interface {
	EnsureTools(ctx context.Context, names []string) error
}

// sharedContext holds state shared across multiple sequential operations
type sharedContext struct {
	cfg           *config.Config
	rootPath      string
	cwdPath       string
	files         []string
	selectedTools []string
	explainLevel  string
	planner       toolPlanner
	projectCache  *cache.Cache
	executor      planExecutor
	binMgr        toolEnsurer
	timings       *timing.Timings
	// nameWidth is the widest configured tool name, computed once so every
	// operation's result block (fix, lint, …) aligns on the same columns.
	nameWidth int
	// failOnSkip makes the run exit non-zero when a tool was skipped because its
	// binary is unavailable for this host (intentional config skips never fail).
	failOnSkip bool
	// platformSkipped collects the names of tools skipped for an unsupported
	// platform across all operations (deduped), to drive failOnSkip after the run.
	platformSkipped map[string]struct{}
}

func initSharedContext(
	args []string,
	explainMode string,
	fileScoped bool,
	selectedToolsFlag string,
	failOnSkip bool,
	loadConfigFunc func() (*config.Config, string, error),
) (*sharedContext, error) {
	ctx := context.Background()
	sc := &sharedContext{
		timings:         timing.New(),
		failOnSkip:      failOnSkip,
		platformSkipped: make(map[string]struct{}),
	}

	// Parse selected tools flag
	if selectedToolsFlag != "" {
		parts := strings.Split(selectedToolsFlag, ",")
		seen := make(map[string]bool)
		for _, tool := range parts {
			tool = strings.TrimSpace(tool)
			if tool != "" && !seen[tool] {
				seen[tool] = true
				sc.selectedTools = append(sc.selectedTools, tool)
			}
		}
	}

	// Get cwd
	var err error
	sc.cwdPath, err = os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get cwd: %w", err)
	}

	// Get root path
	sc.rootPath, err = traverser.GetGitRoot(ctx, sc.cwdPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get git root: %w", err)
	}

	// Load configuration
	func() {
		defer sc.timings.Start("Load configuration")()
		sc.cfg, _, err = loadConfigFunc()
	}()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	// Validate and normalize explain mode
	if explainMode != "" {
		switch strings.ToLower(explainMode) {
		case "summary", "s":
			sc.explainLevel = "summary"
		case "detailed", "detail", "d":
			sc.explainLevel = "detailed"
		case "json", "j":
			sc.explainLevel = "json"
		default:
			return nil, fmt.Errorf("invalid --explain value: %s (must be summary, detailed, or json)", explainMode)
		}
	}

	// Determine files to process
	sc.files = args
	if fileScoped {
		stagedFiles, err := getStagedFiles(ctx, sc.rootPath)
		if err != nil {
			return nil, fmt.Errorf("failed to get staged files: %w", err)
		}
		sc.files = stagedFiles
	}

	// Normalize all file paths to absolute paths to prevent filepath.Rel errors in cache.
	sc.files = normalizeFilePaths(sc.files, sc.cwdPath)

	log.Debug("files", zap.Strings("list", sc.files))

	if len(sc.files) == 0 && !fileScoped {
		log.Debug("no files specified, running whole-project tools only")
	}

	// Create planner
	planner := tooling.NewPlanner(sc.rootPath, sc.cwdPath, nil, sc.cfg.Tools, sc.cfg.ProjectTypes, sc.cfg.IgnoreRules)
	sc.planner = planner

	// Create cache
	cacheDir := env.GetCachePath()
	projectCache, err := createCache(cacheDir, sc.rootPath, *sc.cfg, sc.selectedTools)
	if err != nil {
		log.Warn("failed to create cache, continuing without caching", zap.Error(err))
	}
	sc.projectCache = projectCache

	// Create executor
	rm := runtimemanager.New(sc.cfg.Runtimes)
	binMgr := binmanager.New(sc.cfg.Apps, sc.cfg.Bundles, rm)
	sc.binMgr = binMgr
	// Let the planner mark tools whose binary is unavailable for this host as
	// skipped (reported, not fatal) rather than letting EnsureTools hard-fail —
	// and so they appear in --explain, which never reaches the install step.
	planner.SetPlatformChecker(binMgr)
	sc.executor = tooling.NewExecutor(sc.rootPath, false, true, binMgr, sc.projectCache)
	// Wire output-parsing only when parsers are declared; otherwise the executor
	// never parses (tools without an outputParser are unaffected).
	if len(sc.cfg.Parsers) > 0 {
		sc.executor.SetParser(newDiagnosticParser(sc.cfg.Parsers))
	}

	// All configured tools are known here, so the result column width is fixed
	// once and shared across every operation (so fix and lint blocks align).
	for name := range sc.cfg.Tools {
		if n := utf8.RuneCountInString(name); n > sc.nameWidth {
			sc.nameWidth = n
		}
	}

	return sc, nil
}

func (sc *sharedContext) shutdown() {
	if sc.projectCache != nil {
		sc.projectCache.Shutdown()
	}
}

// runSingleOperation executes one operation (fix, lint, etc.) using a pre-initialized shared context
func runSingleOperation(ctx context.Context, sc *sharedContext, operation config.OperationType) error {
	// Create execution plan
	plan, err := sc.planner.Plan(ctx, operation, sc.files, sc.selectedTools)
	if err != nil {
		return fmt.Errorf("failed to create execution plan: %w", err)
	}

	// Get detected project types from planner cache
	projectTypes := sc.planner.GetDetectedProjectTypes()

	// Explain mode (json/summary/detailed): print the formatted plan, which now
	// lists skipped tools too, and stop. Nothing runs, so explain never records
	// skips for --fail-on-skip.
	if sc.explainLevel != "" {
		output := formatExecutionPlan(plan, sc.rootPath, sc.cwdPath, operation, sc.explainLevel)
		fmt.Println(output)
		return nil
	}

	// Nothing to run (no project types, or no applicable tasks). Still surface
	// any explicit skips — recording unsupported-platform ones for --fail-on-skip
	// — instead of leaving them invisible.
	if len(projectTypes) == 0 || len(plan.Groups) == 0 {
		if len(plan.Skipped) > 0 {
			renderSkipOnlyBlock(string(operation), plan.Skipped, sc.nameWidth)
			sc.recordSkips(plan.Skipped)
			return nil
		}
		if len(projectTypes) == 0 {
			fmt.Println("⚠️  No project types detected")
		} else {
			fmt.Println("ℹ️  No applicable tools found")
		}
		return nil
	}

	// Open the operation block: bold bracket header + dimmed project types. The
	// matched-tool list is omitted — the per-tool results below cover it.
	shortTypes := make([]string, len(projectTypes))
	for i, pt := range projectTypes {
		shortTypes[i] = shortProjectType(pt)
	}
	fmt.Println()
	fmt.Println(phaseTop(string(operation)))
	fmt.Println(clr.Faint("┃ " + strings.Join(shortTypes, " · ")))

	// Calculate total file processing count for progress bar
	totalFileProcessing := 0
	for _, group := range plan.Groups {
		for _, task := range group.Tasks {
			// Determine batch mode
			batch := task.OpConfig.Batch
			if batch == nil {
				defaultBatch := task.OpConfig.Scope != config.ToolScopePerFile
				batch = &defaultBatch
			}

			if !*batch && len(task.Files) > 0 {
				// Per-file mode: count each file
				totalFileProcessing += len(task.Files)
			} else {
				// Batch mode or whole-project mode (no files): count as 1 unit
				totalFileProcessing++
			}
		}
	}

	// Track progress
	progressTracker := make(map[string]*toolExecutionGroup)
	activeTools = make(map[string]map[string]bool) // Initialize active tools tracker (tool -> set of active dirs)

	// Initialize tracker with all expected tools
	toolOrder := 0
	for _, group := range plan.Groups {
		for _, task := range group.Tasks {
			if _, exists := progressTracker[task.ToolName]; !exists {
				progressTracker[task.ToolName] = &toolExecutionGroup{
					toolName:       task.ToolName,
					executions:     []executionInstance{},
					firstSeenIndex: toolOrder,
					minTime:        -1, // -1 means not set yet
					maxTime:        -1,
				}
				toolOrder++
			}
		}
	}

	// Activate the process-wide display for this operation. Binary/runtime
	// downloads during pre-install and the file-processing task all render into
	// ONE shared container, so nothing fights over the terminal. Interactive
	// terminals get animated bars; CI/pipes get throttled append-only lines.
	disp := ui.New(term.DetectMode())
	restore := ui.Activate(disp)

	// Ensure cleanup on exit. Completing the task and closing the display (which
	// flushes and tears down the shared bar container) BEFORE any summaries are
	// printed keeps result output free of progress artifacts.
	progressFinalized := false
	finalizeProgress := func() {
		if progressFinalized {
			return
		}
		progressFinalized = true

		progressMu.Lock()
		t := currentTask
		currentTask = nil
		progressMu.Unlock()
		if t != nil {
			t.Complete()
		}
		disp.Close()
		restore()
	}
	defer finalizeProgress()

	// ensureTask lazily creates the file-processing task on first activity, so a
	// "0 / N" bar never lingers during the install phase (downloads render as
	// their own bars meanwhile). Caller must hold progressMu.
	ensureTask := func() *ui.Task {
		if currentTask == nil && totalFileProcessing > 0 {
			currentTask = disp.Task("Starting...", int64(totalFileProcessing))
		}
		return currentTask
	}

	// Set up task start callback
	sc.executor.SetTaskStartCallback(func(toolName string, relativeDir string) {
		progressMu.Lock()
		if activeTools[toolName] == nil {
			activeTools[toolName] = make(map[string]bool)
		}
		activeTools[toolName][relativeDir] = true
		t := ensureTask()
		progressMu.Unlock()

		t.SetLabel(formatToolWithDir(toolName, relativeDir))
	})

	// Set up file progress callback
	sc.executor.SetFileProgressCallback(func(toolName string, fileIndex, totalFiles int, success bool) {
		status := "✓"
		if !success {
			status = "✗"
		}

		progressMu.Lock()
		t := ensureTask()
		dir := activeToolDir(toolName)
		progressMu.Unlock()

		if dir != "" {
			t.SetLabel(fmt.Sprintf("%s %s (%s) [%d/%d]", status, toolName, dir, fileIndex, totalFiles))
		} else {
			t.SetLabel(fmt.Sprintf("%s %s [%d/%d]", status, toolName, fileIndex, totalFiles))
		}
		t.Increment()
	})

	// Set up progress tracking callback
	sc.executor.SetResultCallback(func(result tooling.ExecutionResult) {
		if group, exists := progressTracker[result.ToolName]; exists {
			group.totalRuns++
			if result.Success {
				group.succeededRuns++
			} else {
				group.failedRuns++
			}

			progressMu.Lock()
			if dirs, ok := activeTools[result.ToolName]; ok {
				delete(dirs, result.RelativeDir)
				if len(dirs) == 0 {
					delete(activeTools, result.ToolName)
				}
			}
			progressMu.Unlock()
		}
	})

	// Pre-install every tool the plan needs once, before parallel per-file
	// execution. This closes the check-then-download install race (multiple
	// per-file tasks installing the same binary concurrently). Explain/dry-run
	// modes return earlier and never reach this point, so no installs happen
	// during planning-only runs.
	if sc.binMgr != nil {
		// Seed the store from the declared OCI bundle first (demand-driven:
		// only the layers of the planned tools), so EnsureTools' stat checks
		// hit seeded content instead of downloading.
		if err := ocibundle.AutoSeed(ctx, sc.cfg, plan.GetAppNames(), nil); err != nil {
			return fmt.Errorf("failed to seed store from oci bundle: %w", err)
		}
		if err := sc.binMgr.EnsureTools(ctx, plan.GetAppNames()); err != nil {
			return fmt.Errorf("failed to pre-install tools: %w", err)
		}
	}

	results, execErr := sc.executor.Execute(ctx, plan)
	// Finalize progress before printing any summaries/errors to avoid interleaved output.
	finalizeProgress()

	// Cache hit/miss feeds the footer.
	cacheHits, cacheMisses := 0, 0
	if sc.projectCache != nil {
		stats := sc.projectCache.GetStats()
		cacheHits, cacheMisses = int(stats.Hits), int(stats.Misses)
	}

	// Calculate total wall-clock time and failure state.
	hasFailures := execErr != nil
	var totalWallClockTime int64
	for _, groupResult := range results {
		totalWallClockTime += groupResult.WallClockDuration
		if !groupResult.Success {
			hasFailures = true
		}
	}

	// Close the operation block: per-tool body lines, skipped-tool lines, then the
	// summary footer (the footer doubles as the "complete" marker, so no separate
	// line is printed).
	toolGroups := groupResultsByTool(results)
	if len(toolGroups) > 0 || len(plan.Skipped) > 0 {
		printGroupedResults(toolGroups, sc.nameWidth, env.IsTimingsEnabled())
		printSkippedTools(plan.Skipped, sc.nameWidth)
		printOperationFooter(toolGroups, totalWallClockTime, cacheHits, cacheMisses, len(plan.Skipped))
	}
	sc.recordSkips(plan.Skipped)

	if hasFailures {
		return errors.New("operation failed")
	}

	return nil
}

// recordSkips accumulates unsupported-platform skips (deduped by tool name) so
// --fail-on-skip can fail the run afterwards. Intentional config skips are not
// recorded — they never fail the run.
func (sc *sharedContext) recordSkips(skipped []tooling.SkippedTool) {
	for _, s := range skipped {
		if s.Reason == tooling.SkipReasonUnsupportedPlatform {
			sc.platformSkipped[s.ToolName] = struct{}{}
		}
	}
}

// printSkippedTools renders faint "┃ ⊘ name   skipped (reason)" body lines,
// aligned to nameWidth like the per-tool result rows. No-op for an empty list.
func printSkippedTools(skipped []tooling.SkippedTool, nameWidth int) {
	for _, s := range skipped {
		pad := max(nameWidth-utf8.RuneCountInString(s.ToolName), 0) + 2
		line := clr.Faint("┃ ⊘ ") + clr.Faint(s.ToolName) +
			strings.Repeat(" ", pad) + clr.Faint("skipped ("+s.ReasonText()+")")
		fmt.Println(line)
	}
}

// renderSkipOnlyBlock prints a minimal operation block containing only skipped
// tools, used when planning produced skips but nothing runnable.
func renderSkipOnlyBlock(operation string, skipped []tooling.SkippedTool, nameWidth int) {
	fmt.Println()
	fmt.Println(phaseTop(operation))
	fmt.Println(clr.Faint("┃"))
	printSkippedTools(skipped, nameWidth)
	printOperationFooter(nil, 0, 0, 0, len(skipped))
}

// shortProjectType trims the redundant "-package"/"-project" suffix from a
// detected project type for the compact header line (e.g. "golang-package" →
// "golang").
func shortProjectType(s string) string {
	s = strings.TrimSuffix(s, "-package")
	s = strings.TrimSuffix(s, "-project")
	return s
}

// RunSequential runs multiple operations in sequence, reusing shared context
// (config, git root, file listing, planner, cache, executor).
// If any operation fails, subsequent operations are skipped.
func RunSequential(
	operations []config.OperationType,
	args []string,
	explainMode string,
	fileScoped bool,
	selectedToolsFlag string,
	failOnSkip bool,
	loadConfigFunc func() (*config.Config, string, error),
) error {
	return runSequential(operations, args, explainMode, fileScoped, selectedToolsFlag, loadConfigFunc, true, failOnSkip)
}

// RunContinuation runs a single operation as a continuation of another command's
// output (e.g. setup's post-fix). It reuses the banner already shown by the
// caller instead of printing a second one.
func RunContinuation(
	operation config.OperationType,
	args []string,
	explainMode string,
	fileScoped bool,
	selectedToolsFlag string,
	loadConfigFunc func() (*config.Config, string, error),
) error {
	// Continuations (e.g. setup's post-fix) never harden on skips.
	return runSequential([]config.OperationType{operation}, args, explainMode, fileScoped, selectedToolsFlag, loadConfigFunc, false, false)
}

func runSequential(
	operations []config.OperationType,
	args []string,
	explainMode string,
	fileScoped bool,
	selectedToolsFlag string,
	loadConfigFunc func() (*config.Config, string, error),
	showBanner bool,
	failOnSkip bool,
) error {
	sc, err := initSharedContext(args, explainMode, fileScoped, selectedToolsFlag, failOnSkip, loadConfigFunc)
	if err != nil {
		return err
	}
	defer func() {
		sc.timings.Print()
		sc.planner.GetTimings().Print()
		sc.shutdown()
	}()

	// Branded banner once at the top (skipped in explain/json so that output
	// stays clean/machine-readable, and when running as a continuation).
	if showBanner && sc.explainLevel == "" {
		ui.Current().Banner(ldflags.PackageName, ldflags.Version)
	}

	ctx := context.Background()

	hasFix := slices.Contains(operations, config.OpFix)

	if hasFix && sc.explainLevel == "" {
		if err := bundled.RunFix(ctx, sc.rootPath); err != nil {
			return err
		}
	}
	if lintErr := bundled.RunLint(ctx, sc.rootPath, sc.cfg.Tools); lintErr != nil {
		if slices.Contains(operations, config.OpLint) {
			return lintErr
		}
		log.Warn("bundled lint error (non-lint mode, continuing)", zap.Error(lintErr))
	}
	for _, op := range operations {
		if err := runSingleOperation(ctx, sc, op); err != nil {
			return err
		}
	}

	// --fail-on-skip: only unsupported-platform skips are treated as failures;
	// intentional config skips (skip: true) never fail the run.
	if err := sc.skipFailure(); err != nil {
		return err
	}

	return nil
}

// skipFailure returns a non-nil error when --fail-on-skip is set and at least one
// tool was skipped because its binary is unavailable for this host. Intentional
// config skips are never recorded, so they never trigger this.
func (sc *sharedContext) skipFailure() error {
	if !sc.failOnSkip || len(sc.platformSkipped) == 0 {
		return nil
	}
	names := make([]string, 0, len(sc.platformSkipped))
	for n := range sc.platformSkipped {
		names = append(names, n)
	}
	sort.Strings(names)
	return fmt.Errorf("--fail-on-skip: %d tool(s) have no binary for this host: %s",
		len(names), strings.Join(names, ", "))
}

// Run executes a single tool operation (fix, lint, etc.)
func Run(
	operation config.OperationType,
	args []string,
	explainMode string,
	fileScoped bool,
	selectedToolsFlag string,
	failOnSkip bool,
	loadConfigFunc func() (*config.Config, string, error),
) error {
	return RunSequential(
		[]config.OperationType{operation},
		args, explainMode, fileScoped, selectedToolsFlag, failOnSkip, loadConfigFunc,
	)
}

// formatToolWithDir formats a tool name with optional directory context
func formatToolWithDir(toolName, relativeDir string) string {
	if relativeDir != "" {
		return fmt.Sprintf("⏳ %s (%s)", toolName, relativeDir)
	}
	return "⏳ " + toolName
}

// activeToolDir returns any active directory for a tool (for progress display).
// Must be called while holding progressMu.
func activeToolDir(toolName string) string {
	if dirs, ok := activeTools[toolName]; ok {
		for dir := range dirs {
			return dir
		}
	}
	return ""
}

// groupResultsByTool groups execution results by tool name
func groupResultsByTool(groupResults []tooling.GroupExecutionResult) []toolExecutionGroup {
	toolMap := make(map[string]*toolExecutionGroup)
	var toolOrder int

	for _, groupResult := range groupResults {
		for _, result := range groupResult.Results {
			// Skip cancelled tasks (noise from fail-fast cancellation)
			if result.Cancelled || result.FailureReason == tooling.FailureReasonCancelled {
				continue
			}
			if _, exists := toolMap[result.ToolName]; !exists {
				toolMap[result.ToolName] = &toolExecutionGroup{
					toolName:       result.ToolName,
					scope:          result.Scope,
					executions:     []executionInstance{},
					minTime:        -1,
					maxTime:        -1,
					firstSeenIndex: toolOrder,
				}
				toolOrder++
			}

			group := toolMap[result.ToolName]
			group.totalRuns++
			group.totalTime += result.Duration

			// Track the wall-clock window (earliest start, latest end) so parallel
			// runs report real elapsed time instead of the summed serial total.
			if !result.StartedAt.IsZero() {
				if group.wallStart.IsZero() || result.StartedAt.Before(group.wallStart) {
					group.wallStart = result.StartedAt
				}
				if result.EndedAt.After(group.wallEnd) {
					group.wallEnd = result.EndedAt
				}
			}

			// Track min/max execution times
			if group.minTime == -1 || result.Duration < group.minTime {
				group.minTime = result.Duration
				group.minDir = result.RelativeDir
			}
			if group.maxTime == -1 || result.Duration > group.maxTime {
				group.maxTime = result.Duration
				group.maxDir = result.RelativeDir
			}

			if result.Success {
				group.succeededRuns++
			} else {
				group.failedRuns++
			}

			group.executions = append(group.executions, executionInstance{
				result:      result,
				relativeDir: result.RelativeDir,
			})
		}
	}

	// Resolve each tool's wall-clock span. Fall back to the summed duration when
	// runs carry no timestamps (e.g. dry-run paths) so the column is never empty.
	for _, group := range toolMap {
		if !group.wallStart.IsZero() && group.wallEnd.After(group.wallStart) {
			group.wallTime = group.wallEnd.Sub(group.wallStart).Milliseconds()
		} else {
			group.wallTime = group.totalTime
		}
	}

	// Sort by first seen index to preserve execution order
	groups := make([]toolExecutionGroup, 0, len(toolMap))
	for _, group := range toolMap {
		groups = append(groups, *group)
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].firstSeenIndex < groups[j].firstSeenIndex
	})

	return groups
}

// printGroupedResults prints per-tool results as bracketed body lines (┃). Each
// line is compact by default — status, name, total time, run count — with the
// detailed timings (scope, avg, min/max) appended only when `detailed` is set
// (DATAMITSU_TIMINGS). Failed tools show a red ✗ and a bordered detail box.
func printGroupedResults(toolGroups []toolExecutionGroup, nameWidth int, detailed bool) {
	fmt.Println(clr.Faint("┃"))

	// Slowest tool in this run anchors the duration heatmap (by wall-clock).
	var maxMs int64
	for _, group := range toolGroups {
		if group.wallTime > maxMs {
			maxMs = group.wallTime
		}
	}

	for _, group := range toolGroups {
		status := clr.Green("✓")
		nameDisplay := clr.Bold(group.toolName)
		if group.failedRuns > 0 {
			status = clr.Red("✗")
			nameDisplay = clr.Red(group.toolName)
		}

		// Align the duration column to nameWidth (the widest tool name across the
		// whole run, computed once) + a 2-space gap, so every operation block —
		// fix and lint alike — uses the same columns.
		pad := max(nameWidth-utf8.RuneCountInString(group.toolName), 0) + 2

		// Reserve a fixed-width slot for the duration so anything after it (the run
		// count) stays in a stable column instead of floating with the duration
		// width. Pad only when something follows, to avoid trailing whitespace.
		durStr := ui.FormatDurationShort(group.wallTime)
		if group.totalRuns > 1 || group.failedRuns > 0 || detailed {
			durStr = fmt.Sprintf("%-*s", durationColWidth, durStr)
		}
		line := clr.Faint("┃ ") + status + " " + nameDisplay + strings.Repeat(" ", pad) + heatDuration(group.wallTime, maxMs, durStr)
		if group.totalRuns > 1 {
			line += " " + clr.Faint(fmt.Sprintf("×%d", group.totalRuns))
		}
		if group.failedRuns > 0 {
			line += "  " + clr.Red(fmt.Sprintf("(%d failed)", group.failedRuns))
		}
		if detailed {
			line += "  " + clr.Faint(toolDetail(group))
		}
		fmt.Println(line)

		// Show failed runs details
		if group.failedRuns > 0 {
			runNum := 0
			for _, exec := range group.executions {
				if !exec.result.Success {
					runNum++
					printFailedExecution(runNum, exec)
				}
			}
		}
	}
}

// heatFloorMs is the duration below which a tool is always shown "cool" (faint):
// trivial and cached runs never draw attention, only genuinely slow tools warm
// up. This avoids false alarms on fast runs (e.g. "all 5ms, one 10ms").
const heatFloorMs = 250

// heatPalette is an xterm-256 ramp from warm (yellow) to hot (red); the slowest
// tool above the floor is reddest.
var heatPalette = []int{220, 214, 208, 202, 196}

// heatDuration colors the already-formatted duration text by how slow the tool
// is relative to the slowest in this run, normalized over the notable range
// [heatFloorMs, maxMs]. Sub-floor (and trivial) runs stay faint.
func heatDuration(ms, maxMs int64, text string) string {
	if ms < heatFloorMs || maxMs <= heatFloorMs {
		return clr.Faint(text)
	}
	ratio := float64(ms-heatFloorMs) / float64(maxMs-heatFloorMs)
	idx := int(ratio * float64(len(heatPalette)))
	idx = max(min(idx, len(heatPalette)-1), 0)
	return clr.Color256(heatPalette[idx])(text)
}

// toolDetail renders the verbose per-tool timing detail (scope, avg, min/max),
// shown only in detailed mode.
func toolDetail(group toolExecutionGroup) string {
	avg := group.totalTime
	if group.totalRuns > 0 {
		avg = group.totalTime / int64(group.totalRuns)
	}
	parts := make([]string, 0, 4)
	if group.scope != "" {
		parts = append(parts, "["+string(group.scope)+"]")
	}
	parts = append(parts, "avg "+formatDuration(avg))
	if group.totalRuns > 1 && group.minTime >= 0 && group.maxTime >= 0 {
		parts = append(parts, "min "+formatDuration(group.minTime), "max "+formatDuration(group.maxTime))
		// The headline column is wall-clock; surface the summed serial time so the
		// parallelism speedup (cpu ≫ wall) is visible.
		parts = append(parts, "cpu "+formatDuration(group.totalTime))
	}
	return strings.Join(parts, " · ")
}

// formatDiagnostic renders one parsed diagnostic as
// "file:row:col severity message [code]", the severity colored by level.
func formatDiagnostic(d diagnostic.Diagnostic) string {
	loc := fmt.Sprintf("%d:%d", d.Row, d.Col)
	if d.File != "" {
		loc = d.File + ":" + loc
	}
	line := fmt.Sprintf("%s %s %s", clr.Faint(loc), severityColor(d.Severity)(d.Severity.String()), d.Message)
	if d.Code != "" {
		line += " " + clr.Faint("["+d.Code+"]")
	}
	return line
}

// severityColor maps a severity to its display color.
func severityColor(s diagnostic.Severity) func(a ...any) string {
	switch s {
	case diagnostic.SeverityError:
		return clr.Red
	case diagnostic.SeverityWarning:
		return clr.Yellow
	case diagnostic.SeverityInfo:
		return clr.Cyan
	case diagnostic.SeverityHint:
		return clr.Faint
	default:
		return clr.Faint
	}
}

// printFailedExecution prints details of a failed execution in a bordered format
// showing all context needed to interpret error output in monorepo setups
func printFailedExecution(runNum int, exec executionInstance) {
	result := exec.result

	// Build header with tool name and scope
	header := "─ " + clr.Red(result.ToolName)
	if result.Scope != "" {
		header += " " + clr.Faint("["+string(result.Scope)+"]")
	}
	header += fmt.Sprintf(" (run #%d) ", runNum)

	border := clr.Red
	label := clr.Faint

	fmt.Printf("  %s%s%s\n", border("┌"), header, border(strings.Repeat("─", 20)))

	// Directory context for interpreting relative paths in tool output
	if exec.relativeDir != "" {
		fmt.Printf("  %s  %s %s\n", border("│"), label("Dir:      "), exec.relativeDir)
	}
	if result.WorkingDir != "" {
		fmt.Printf("  %s  %s %s\n", border("│"), label("Cwd:      "), result.WorkingDir)
	}

	// Command details
	if result.Command != "" {
		fmt.Printf("  %s  %s %s\n", border("│"), label("Command:  "), result.Command)
	}

	// Exit info
	fmt.Printf("  %s  %s %s\n", border("│"), label("Exit code:"), clr.Red(strconv.Itoa(result.ExitCode)))
	fmt.Printf("  %s  %s %s\n", border("│"), label("Duration: "), formatDuration(result.Duration))

	switch {
	// Parsed diagnostics, when the tool has an outputParser, are clearer than the
	// raw output (often JSON) and take its place.
	case len(result.Diagnostics) > 0:
		fmt.Printf("  %s\n", border("│"))
		for _, d := range result.Diagnostics {
			fmt.Printf("  %s  %s\n", border("│"), formatDiagnostic(d))
		}
	case result.Output != "":
		fmt.Printf("  %s\n", border("│"))
		lines := strings.SplitSeq(strings.TrimRight(result.Output, "\n"), "\n")
		for line := range lines {
			fmt.Printf("  %s  %s\n", border("│"), line)
		}
	case result.Error != nil:
		fmt.Printf("  %s\n", border("│"))
		fmt.Printf("  %s  %s\n", border("│"), result.Error.Error())
	}

	fmt.Printf("  %s%s\n", border("└"), border(strings.Repeat("─", 57)))
	fmt.Println()
}

// durationColWidth reserves a fixed slot for the per-tool duration (covers
// values like "11.35s"/"120ms"/"1m05s") so the run count after it never floats.
const durationColWidth = 7

// phaseTop renders the opening bracket rule for an operation.
func phaseTop(operation string) string {
	return ui.RuleLine("┏", operation, clr.Bold(operation))
}

// printOperationFooter renders the closing bracket rule that summarizes the
// operation (tool/run counts, wall-clock time, failures, skips and cache hit rate).
func printOperationFooter(toolGroups []toolExecutionGroup, wallClockTime int64, cacheHits, cacheMisses, skipped int) {
	totalTools := len(toolGroups)
	totalRuns := 0
	failedTools := 0
	for _, group := range toolGroups {
		totalRuns += group.totalRuns
		if group.failedRuns > 0 {
			failedTools++
		}
	}

	dur := ui.FormatDurationShort(wallClockTime)
	plain := fmt.Sprintf("%d tools · %d runs · done in %s", totalTools, totalRuns, dur)
	colored := clr.Bold(fmt.Sprintf("%d tools", totalTools)) + fmt.Sprintf(" · %d runs · done in %s", totalRuns, dur)
	if failedTools > 0 {
		plain += fmt.Sprintf(" · %d failed", failedTools)
		colored += " · " + clr.Red(fmt.Sprintf("%d failed", failedTools))
	}
	if skipped > 0 {
		skipText := fmt.Sprintf(" · %d skipped", skipped)
		plain += skipText
		colored += clr.Faint(skipText)
	}
	if cacheHits+cacheMisses > 0 {
		pct := float64(cacheHits) / float64(cacheHits+cacheMisses) * 100
		cacheText := fmt.Sprintf(" · cache %.0f%%", pct)
		plain += cacheText
		colored += clr.Faint(cacheText)
	}

	fmt.Println(ui.RuleLine("┗", plain, colored))
}

func normalizeFilePaths(files []string, cwdPath string) []string {
	for i, file := range files {
		if !filepath.IsAbs(file) {
			files[i] = filepath.Join(cwdPath, file)
		}
	}
	return files
}

func formatDuration(ms int64) string {
	if ms < 100 {
		return fmt.Sprintf("%dms", ms)
	}

	seconds := float64(ms) / 1000.0
	if seconds < 60 {
		return fmt.Sprintf("%.2fs (%dms)", seconds, ms)
	}

	minutes := int(seconds / 60)
	remainingSeconds := seconds - float64(minutes*60)
	return fmt.Sprintf("%dm%.2fs (%dms)", minutes, remainingSeconds, ms)
}

func formatExecutionPlan(
	plan *tooling.ExecutionPlan,
	rootPath, cwdPath string,
	operation config.OperationType,
	explainLevel string,
) string {
	var formatter tooling.PlanFormatter

	switch explainLevel {
	case "summary":
		formatter = tooling.NewSummaryFormatter()
	case "detailed":
		formatter = tooling.NewDetailedFormatter()
	case "json":
		formatter = tooling.NewJSONFormatter()
	default:
		formatter = tooling.NewSummaryFormatter()
	}

	return formatter.Format(plan, rootPath, cwdPath, operation)
}

func getStagedFiles(ctx context.Context, rootPath string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--cached", "--name-only", "--diff-filter=ACMR")
	cmd.Dir = rootPath

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get staged files: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var files []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		absPath := filepath.Join(rootPath, line)
		files = append(files, absPath)
	}

	return filterSymlinkPaths(files), nil
}

func createCache(cacheDir string, projectPath string, cfg config.Config, selectedTools []string) (*cache.Cache, error) {
	invalidateOnFiles := make(map[string][]string)

	for toolName, tool := range cfg.Tools {
		var files []string

		for _, op := range tool.Operations {
			if op.InvalidateOn != nil {
				files = append(files, op.InvalidateOn...)
			}
		}

		if len(files) > 0 {
			fileSet := make(map[string]bool)
			var uniqueFiles []string
			for _, file := range files {
				if !fileSet[file] {
					fileSet[file] = true
					uniqueFiles = append(uniqueFiles, file)
				}
			}
			invalidateOnFiles[toolName] = uniqueFiles
		}
	}

	return cache.NewCache(cacheDir, projectPath, cfg, invalidateOnFiles, selectedTools, logger.Logger)
}
