package runner

import (
	"context"
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/bundled"
	"github.com/datamitsu/datamitsu/internal/cache"
	clr "github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"
	"github.com/datamitsu/datamitsu/internal/timing"
	"github.com/datamitsu/datamitsu/internal/tooling"
	"github.com/datamitsu/datamitsu/internal/traverser"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
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
	totalTime      int64
	minTime        int64  // Minimum execution time (-1 if not set)
	maxTime        int64  // Maximum execution time (-1 if not set)
	minDir         string // Project directory with minimum time
	maxDir         string // Project directory with maximum time
	firstSeenIndex int    // Order in which tool was first seen (for preserving execution order)
	executions     []executionInstance
}

// executionInstance represents a single execution of a tool
type executionInstance struct {
	result      tooling.ExecutionResult
	relativeDir string
}

// Progress tracking variables
var (
	lastCIProgressPercent int
	progressMu            sync.Mutex
	currentProgress       *mpb.Progress
	currentProgressBar    *mpb.Bar
	currentBarDesc        atomic.Value      // string - accessed without lock to avoid deadlock with mpb
	activeTools           map[string]map[string]bool // Track currently running tools (tool -> set of active dirs)
)

// sharedContext holds state shared across multiple sequential operations
type sharedContext struct {
	cfg           *config.Config
	rootPath      string
	cwdPath       string
	files         []string
	selectedTools []string
	explainLevel  string
	planner       *tooling.Planner
	projectCache  *cache.Cache
	executor      *tooling.Executor
	timings       *timing.Timings
}

func initSharedContext(
	args []string,
	explainMode string,
	fileScoped bool,
	selectedToolsFlag string,
	loadConfigFunc func() (*config.Config, string, error),
) (*sharedContext, error) {
	ctx := context.Background()
	sc := &sharedContext{
		timings: timing.New(),
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

	if len(sc.files) == 0 && !fileScoped && sc.explainLevel != "json" {
		fmt.Println("ℹ️  No files specified, running whole-project tools only")
	}

	// Create planner
	sc.planner = tooling.NewPlanner(sc.rootPath, sc.cwdPath, nil, sc.cfg.Tools, sc.cfg.ProjectTypes, sc.cfg.IgnoreRules)

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
	sc.executor = tooling.NewExecutor(sc.rootPath, false, true, binMgr, sc.projectCache)

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
	if len(projectTypes) == 0 {
		if sc.explainLevel == "json" {
			// In JSON mode, output empty plan even when no project types detected
			output := formatExecutionPlan(plan, sc.rootPath, sc.cwdPath, operation, sc.explainLevel)
			fmt.Println(output)
		} else {
			fmt.Println("⚠️  No project types detected")
		}
		return nil
	}

	if sc.explainLevel != "json" {
		fmt.Printf("📦 Detected project types: %v\n", projectTypes)
	}

	if len(plan.Groups) == 0 {
		if sc.explainLevel == "json" {
			// In JSON mode, output empty plan even when no applicable tools
			output := formatExecutionPlan(plan, sc.rootPath, sc.cwdPath, operation, sc.explainLevel)
			fmt.Println(output)
		} else {
			fmt.Println("ℹ️  No applicable tools found")
		}
		return nil
	}

	// Show matched tools
	toolNames := plan.GetToolNames()
	if len(toolNames) > 0 && sc.explainLevel != "json" {
		fmt.Printf("🔧 Matched tools: %s\n", strings.Join(toolNames, ", "))
	}

	// Show plan and exit if explain mode is enabled
	if sc.explainLevel != "" {
		output := formatExecutionPlan(plan, sc.rootPath, sc.cwdPath, operation, sc.explainLevel)
		fmt.Println(output)
		return nil
	}

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
				totalFileProcessing += 1
			}
		}
	}

	// Track progress
	progressTracker := make(map[string]*toolExecutionGroup)
	completedFileProcessing := 0
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

	// Create progress bar for non-CI environments
	if !env.IsCI() && totalFileProcessing > 0 {
		currentProgress = mpb.New(mpb.WithWidth(60))
		currentBarDesc.Store("Starting...")
		currentProgressBar = currentProgress.AddBar(int64(totalFileProcessing),
			mpb.PrependDecorators(
				decor.Any(func(s decor.Statistics) string {
					// Read currentBarDesc without lock to avoid deadlock
					// mpb may call this from its own goroutine while we hold progressMu
					if desc := currentBarDesc.Load(); desc != nil {
						return desc.(string)
					}
					return ""
				}, decor.WC{W: 40, C: decor.DSyncWidthR}),
			),
			mpb.AppendDecorators(
				decor.CountersNoUnit(" %d / %d", decor.WCSyncSpace),
			),
		)
	}

	// Ensure cleanup on exit
	progressFinalized := false
	finalizeProgress := func() {
		if progressFinalized {
			return
		}
		progressFinalized = true

		if !env.IsCI() && currentProgress != nil {
			// Finalize any incomplete progress bars before waiting
			if currentProgressBar != nil {
				progressMu.Lock()
				completed := completedFileProcessing
				progressMu.Unlock()
				if completed < totalFileProcessing {
					currentProgressBar.SetCurrent(int64(totalFileProcessing))
					currentProgressBar.SetTotal(int64(totalFileProcessing), true)
				}
			}
			currentProgress.Wait()
		}
		// Reset progress state for next operation
		progressMu.Lock()
		currentProgress = nil
		currentProgressBar = nil
		lastCIProgressPercent = 0
		progressMu.Unlock()
	}
	defer func() {
		finalizeProgress()
	}()

	// Set up task start callback
	sc.executor.SetTaskStartCallback(func(toolName string, relativeDir string) {
		progressMu.Lock()
		if activeTools[toolName] == nil {
			activeTools[toolName] = make(map[string]bool)
		}
		activeTools[toolName][relativeDir] = true

		if !env.IsCI() && currentProgressBar != nil {
			currentBarDesc.Store(formatToolWithDir(toolName, relativeDir))
		}
		progressMu.Unlock()

		if env.IsCI() {
			dirInfo := ""
			if relativeDir != "" {
				dirInfo = fmt.Sprintf(" in %s", relativeDir)
			}
			fmt.Printf("⏳ Starting %s%s\n", toolName, dirInfo)
		}
	})

	// Set up file progress callback
	sc.executor.SetFileProgressCallback(func(toolName string, fileIndex, totalFiles int, success bool) {
		status := "✅"
		if !success {
			status = "❌"
		}

		progressMu.Lock()
		completedFileProcessing++
		currentCompleted := completedFileProcessing
		bar := currentProgressBar
		if !env.IsCI() && bar != nil {
			dir := activeToolDir(toolName)
			if dir != "" {
				currentBarDesc.Store(fmt.Sprintf("%s %s (%s) [%d/%d]", status, toolName, dir, fileIndex, totalFiles))
			} else {
				currentBarDesc.Store(fmt.Sprintf("%s %s [%d/%d]", status, toolName, fileIndex, totalFiles))
			}
		}
		progressMu.Unlock()

		if env.IsCI() {
			updateCIProgress(currentCompleted, totalFileProcessing, status, toolName)
		} else if bar != nil {
			bar.Increment()
		}
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

			// Do not update progress bar description here.
			// FileProgressCallback is the single source of truth for bar updates.
			progressMu.Unlock()
		}
	})

	// Execute plan
	if !env.IsCI() && currentProgressBar == nil {
		fmt.Println()
	}
	fmt.Printf("🚀 Running %s operation...\n", operation)
	if env.IsCI() {
		fmt.Println()
	}
	results, execErr := sc.executor.Execute(ctx, plan)
	// Finalize progress before printing any summaries/errors to avoid interleaved output.
	finalizeProgress()

	// Print cache statistics if cache is available
	if sc.projectCache != nil {
		stats := sc.projectCache.GetStats()
		if stats.Hits > 0 || stats.Misses > 0 {
			fmt.Println()
			fmt.Printf("📊 Cache: %d cached, %d checked", stats.Hits, stats.Misses)
			if stats.Hits+stats.Misses > 0 {
				percentage := float64(stats.Hits) / float64(stats.Hits+stats.Misses) * 100
				fmt.Printf(" (%.1f%%)\n", percentage)
			} else {
				fmt.Println()
			}
		}
	}

	// Calculate total wall-clock and CPU time
	hasFailures := execErr != nil
	var totalWallClockTime int64
	var totalCPUTime int64

	for _, groupResult := range results {
		totalWallClockTime += groupResult.WallClockDuration
		if !groupResult.Success {
			hasFailures = true
		}

		for _, taskResult := range groupResult.Results {
			totalCPUTime += taskResult.Duration
		}
	}

	// Progress bar will be finalized in defer

	// Always print grouped results (even partial results from fail-fast)
	if len(results) > 0 {
		toolGroups := groupResultsByTool(results)
		printGroupedResults(toolGroups)
		printOverallSummary(toolGroups, totalWallClockTime, totalCPUTime)
	}

	if hasFailures {
		return fmt.Errorf("operation failed")
	}

	fmt.Println()
	fmt.Println("✅ Operation complete")
	return nil
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
	loadConfigFunc func() (*config.Config, string, error),
) error {
	sc, err := initSharedContext(args, explainMode, fileScoped, selectedToolsFlag, loadConfigFunc)
	if err != nil {
		return err
	}
	defer func() {
		sc.timings.Print()
		sc.planner.GetTimings().Print()
		sc.shutdown()
	}()

	hasFix := slices.Contains(operations, config.OpFix)

	if hasFix && sc.explainLevel == "" {
		if err := bundled.RunFix(sc.rootPath); err != nil {
			return err
		}
	}
	if lintErr := bundled.RunLint(sc.rootPath, sc.cfg.Tools); lintErr != nil {
		if slices.Contains(operations, config.OpLint) {
			return lintErr
		}
		log.Warn("bundled lint error (non-lint mode, continuing)", zap.Error(lintErr))
	}

	ctx := context.Background()
	for _, op := range operations {
		if err := runSingleOperation(ctx, sc, op); err != nil {
			return err
		}
	}

	return nil
}

// Run executes a single tool operation (fix, lint, etc.)
func Run(
	operation config.OperationType,
	args []string,
	explainMode string,
	fileScoped bool,
	selectedToolsFlag string,
	loadConfigFunc func() (*config.Config, string, error),
) error {
	return RunSequential(
		[]config.OperationType{operation},
		args, explainMode, fileScoped, selectedToolsFlag, loadConfigFunc,
	)
}

// formatToolWithDir formats a tool name with optional directory context
func formatToolWithDir(toolName, relativeDir string) string {
	if relativeDir != "" {
		return fmt.Sprintf("⏳ %s (%s)", toolName, relativeDir)
	}
	return fmt.Sprintf("⏳ %s", toolName)
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

	// Sort by first seen index to preserve execution order
	var groups []toolExecutionGroup
	for _, group := range toolMap {
		groups = append(groups, *group)
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].firstSeenIndex < groups[j].firstSeenIndex
	})

	return groups
}

// printGroupedResults prints execution results grouped by tool
func printGroupedResults(toolGroups []toolExecutionGroup) {
	fmt.Println()

	for _, group := range toolGroups {
		avgTime := group.totalTime
		if group.totalRuns > 0 {
			avgTime = group.totalTime / int64(group.totalRuns)
		}

		runText := "run"
		if group.totalRuns > 1 {
			runText = "runs"
		}

		status := "✅"
		if group.failedRuns > 0 {
			status = "❌"
		}

		// Print tool summary line with scope and min/max
		scopeInfo := ""
		if group.scope != "" {
			scopeInfo = fmt.Sprintf(" %s", clr.Faint("["+string(group.scope)+"]"))
		}
		toolDisplay := clr.Bold(group.toolName)
		if group.failedRuns > 0 {
			toolDisplay = clr.Red(group.toolName)
		}
		fmt.Printf("%s %s%s (%d %s, %s, avg: %s",
			status,
			toolDisplay,
			scopeInfo,
			group.totalRuns,
			runText,
			formatDuration(group.totalTime),
			formatDuration(avgTime))

		// Add min/max if there are multiple runs
		if group.totalRuns > 1 && group.minTime >= 0 && group.maxTime >= 0 {
			minDirInfo := ""
			if group.minDir != "" {
				minDirInfo = fmt.Sprintf(" [%s]", group.minDir)
			}
			maxDirInfo := ""
			if group.maxDir != "" {
				maxDirInfo = fmt.Sprintf(" [%s]", group.maxDir)
			}
			fmt.Printf(", min: %s%s, max: %s%s",
				formatDuration(group.minTime), minDirInfo,
				formatDuration(group.maxTime), maxDirInfo)
		}

		fmt.Printf(")")

		if group.failedRuns > 0 {
			fmt.Printf(" - %s", clr.Red(fmt.Sprintf("%d failed", group.failedRuns)))
		}
		fmt.Println()

		// Show failed runs details
		if group.failedRuns > 0 {
			fmt.Println()
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

// printFailedExecution prints details of a failed execution in a bordered format
// showing all context needed to interpret error output in monorepo setups
func printFailedExecution(runNum int, exec executionInstance) {
	result := exec.result

	// Build header with tool name and scope
	header := fmt.Sprintf("─ %s", clr.Red(result.ToolName))
	if result.Scope != "" {
		header += fmt.Sprintf(" %s", clr.Faint("["+string(result.Scope)+"]"))
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
	fmt.Printf("  %s  %s %s\n", border("│"), label("Exit code:"), clr.Red(fmt.Sprintf("%d", result.ExitCode)))
	fmt.Printf("  %s  %s %s\n", border("│"), label("Duration: "), formatDuration(result.Duration))

	// Tool output
	if result.Output != "" {
		fmt.Printf("  %s\n", border("│"))
		lines := strings.Split(strings.TrimRight(result.Output, "\n"), "\n")
		for _, line := range lines {
			fmt.Printf("  %s  %s\n", border("│"), line)
		}
	} else if result.Error != nil {
		fmt.Printf("  %s\n", border("│"))
		fmt.Printf("  %s  %s\n", border("│"), result.Error.Error())
	}

	fmt.Printf("  %s%s\n", border("└"), border(strings.Repeat("─", 57)))
	fmt.Println()
}

// printOverallSummary prints the final summary
func printOverallSummary(toolGroups []toolExecutionGroup, wallClockTime, cpuTime int64) {
	totalTools := len(toolGroups)
	successfulTools := 0
	failedTools := 0
	totalExecutions := 0

	for _, group := range toolGroups {
		totalExecutions += group.totalRuns
		if group.failedRuns == 0 {
			successfulTools++
		} else {
			failedTools++
		}
	}

	separator := clr.Faint("─────────────────────────────────────────────────────────────")
	fmt.Println()
	fmt.Println(separator)
	fmt.Printf("📊 %s %d tools", clr.Bold("Summary:"), totalTools)
	if failedTools > 0 {
		fmt.Printf(" (%s, %s)", clr.Green(fmt.Sprintf("%d succeeded", successfulTools)), clr.Red(fmt.Sprintf("%d failed", failedTools)))
	}
	fmt.Printf(", %d runs, %s", totalExecutions, formatDuration(wallClockTime))
	if cpuTime != wallClockTime {
		fmt.Printf(" (CPU: %s)", formatDuration(cpuTime))
	}
	fmt.Println()
	fmt.Println(separator)
}

// updateCIProgress prints simple progress for CI environments
func updateCIProgress(completed, total int, status, toolName string) {
	progressMu.Lock()
	defer progressMu.Unlock()

	percent := 0
	if total > 0 {
		percent = (completed * 100) / total
	}

	// Print progress every 25% to avoid spam
	if percent >= lastCIProgressPercent+25 || completed == total {
		if completed < total {
			fmt.Printf("  %s %s -- %d/%d (%d%%)\n",
				status, toolName, completed, total, percent)
		} else {
			fmt.Printf("  Progress: %d/%d (%d%%)\n", completed, total, percent)
		}
		lastCIProgressPercent = percent
	}

	// Print completion message
	if completed == total {
		fmt.Println("✅ All tasks completed!")
		fmt.Println()
	}
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
