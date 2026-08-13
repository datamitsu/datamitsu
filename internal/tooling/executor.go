// Package tooling plans and executes managed tool operations: it groups tasks
// by priority, runs them through a bounded worker pool, and classifies their
// results for fail-fast reporting.
package tooling

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/cache"
	clr "github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/textdiff"

	"go.uber.org/zap"
)

var log = logger.Logger.With(zap.Namespace("cmd"))

// errCancelled is a sentinel error used when tasks are cancelled due to fail-fast context cancellation.
var errCancelled = errors.New("cancelled")

// Executor executes tool tasks
type Executor struct {
	rootPath             string
	dryRun               bool
	failFast             bool
	appManager           AppManager           // Interface to get binary paths
	resultCallback       ResultCallback       // Optional callback for real-time results
	taskStartCallback    TaskStartCallback    // Optional callback when task starts
	fileProgressCallback FileProgressCallback // Optional callback for per-file progress
	cache                *cache.Cache         // Cache for storing execution results
	parser               DiagnosticParser     // Optional: parses tool output into diagnostics
}

// AppManager interface for getting application command information
type AppManager interface {
	GetBinaryPath(ctx context.Context, appName string) (string, error)
	GetCommandInfo(ctx context.Context, appName string) (*binmanager.CommandInfo, error)
}

// DiagnosticParser turns a tool's raw output into resolved diagnostics. It is
// injected via SetParser; when nil (the default) the executor never parses, so
// tools without an outputParser are entirely unaffected. The concrete
// implementation (in the runner) loads the WASM module and applies the
// defaults-in-core resolution.
type DiagnosticParser interface {
	// Parse loads the WASM module named by `module` (a parsers config entry),
	// dispatches its `parser` (the key inside the module), and labels the resulting
	// diagnostics with `toolName` as their source.
	Parse(ctx context.Context, module, parser, toolName string, stdout, stderr []byte, exitCode int32) ([]diagnostic.Diagnostic, error)
}

// ResultCallback is called when a task completes
type ResultCallback func(result ExecutionResult)

// TaskStartCallback is called when a task starts executing
type TaskStartCallback func(toolName string, relativeDir string)

// FileProgressCallback is called after each file is processed
type FileProgressCallback func(toolName string, fileIndex, totalFiles int, success bool)

// NewExecutor creates a new tool executor
func NewExecutor(
	rootPath string,
	dryRun bool,
	failFast bool,
	appManager AppManager,
	cache *cache.Cache,
) *Executor {
	return &Executor{
		rootPath:   rootPath,
		dryRun:     dryRun,
		failFast:   failFast,
		appManager: appManager,
		cache:      cache,
	}
}

// SetResultCallback sets a callback to be called when each task completes
func (e *Executor) SetResultCallback(callback ResultCallback) {
	e.resultCallback = callback
}

// SetTaskStartCallback sets a callback to be called when each task starts
func (e *Executor) SetTaskStartCallback(callback TaskStartCallback) {
	e.taskStartCallback = callback
}

// SetFileProgressCallback sets a callback to be called after each file is processed
func (e *Executor) SetFileProgressCallback(callback FileProgressCallback) {
	e.fileProgressCallback = callback
}

// SetParser wires the diagnostic parser used for tools that declare an
// outputParser. Without it, tool output is never parsed.
func (e *Executor) SetParser(parser DiagnosticParser) {
	e.parser = parser
}

// Execute runs an execution plan
func (e *Executor) Execute(ctx context.Context, plan *ExecutionPlan) ([]GroupExecutionResult, error) {
	log.Debug("starting execution plan", zap.Int("groupCount", len(plan.Groups)))
	var results []GroupExecutionResult

	// Create a cancellable context for fail-fast propagation
	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	for _, group := range plan.Groups {
		// Check if already cancelled before starting next group
		if execCtx.Err() != nil {
			log.Debug("skipping group due to cancellation", zap.Int("priority", group.Priority))
			break
		}

		log.Debug("executing group", zap.Int("priority", group.Priority), zap.Int("taskCount", len(group.Tasks)))
		groupResult := e.executeGroup(execCtx, group, cancel)
		results = append(results, groupResult)

		log.Debug("group execution completed",
			zap.Int("priority", group.Priority),
			zap.Bool("success", groupResult.Success),
			zap.Int("resultCount", len(groupResult.Results)))

		// Fail-fast: stop on first group failure
		if e.failFast && !groupResult.Success {
			log.Debug("fail-fast triggered", zap.Int("priority", group.Priority))
			cancel()
			// Collect failed tool errors, skip cancelled tasks (noise from fail-fast)
			var errorMessages []string
			for _, r := range groupResult.Results {
				if !r.Success && !errors.Is(r.Error, errCancelled) && r.FailureReason != FailureReasonCancelled {
					// Create identifier with directory for clarity in monorepos
					toolIdentifier := r.ToolName
					if r.RelativeDir != "" {
						toolIdentifier = fmt.Sprintf("%s [%s]", r.ToolName, r.RelativeDir)
					}

					var msg strings.Builder
					msg.WriteString(toolIdentifier)
					msg.WriteString(":")

					// Include error message if present
					if r.Error != nil {
						msg.WriteString(" ")
						msg.WriteString(r.Error.Error())
					}

					// Include full stdout/stderr output if present
					if r.Output != "" {
						if r.Error != nil {
							msg.WriteString("\n")
						} else {
							msg.WriteString(" ")
						}
						msg.WriteString(r.Output)
					}

					// If no error and no output, just mark as failed
					if r.Error == nil && r.Output == "" {
						msg.WriteString(" execution failed")
					}

					errorMessages = append(errorMessages, msg.String())
				}
			}
			if len(errorMessages) > 0 {
				return results, fmt.Errorf("execution failed:\n%s", strings.Join(errorMessages, "\n"))
			}
			// This should not happen, but just in case
			return results, fmt.Errorf("group with priority %d failed (no failed tools found)", group.Priority)
		}
	}

	log.Debug("execution plan completed", zap.Int("totalGroups", len(results)))
	return results, nil
}

// executeGroup executes a task group
func (e *Executor) executeGroup(ctx context.Context, group TaskGroup, cancel context.CancelFunc) GroupExecutionResult {
	startTime := time.Now()
	log.Debug("executeGroup start", zap.Int("priority", group.Priority), zap.Int("tasks", len(group.Tasks)))
	result := GroupExecutionResult{
		Priority: group.Priority,
		Success:  true,
	}

	// Detect overlaps within the group to determine parallelization strategy
	parallelGroups := e.detectParallelGroups(group.Tasks)
	log.Debug("parallel groups detected", zap.Int("parallelGroupCount", len(parallelGroups)))

	// Execute each parallel group sequentially
	for i, parallelTasks := range parallelGroups {
		// Check if context is cancelled before starting next parallel group
		if ctx.Err() != nil {
			log.Debug("skipping parallel group due to cancellation", zap.Int("groupIndex", i))
			break
		}

		log.Debug("processing parallel group", zap.Int("groupIndex", i), zap.Int("taskCount", len(parallelTasks)))
		if len(parallelTasks) == 1 {
			// Single task - execute directly
			log.Debug("executing single task", zap.String("toolName", parallelTasks[0].ToolName))
			taskResult := e.executeTask(ctx, parallelTasks[0])
			result.Results = append(result.Results, taskResult)

			// Call callback if set
			if e.resultCallback != nil {
				e.resultCallback(taskResult)
			}

			if !taskResult.Success {
				result.Success = false
				if e.failFast {
					cancel()
					return result
				}
			}
		} else {
			// Multiple non-overlapping tasks - execute in parallel
			log.Debug("executing tasks in parallel", zap.Int("parallelTaskCount", len(parallelTasks)))
			taskResults := e.executeTasksParallel(ctx, parallelTasks, cancel)
			result.Results = append(result.Results, taskResults...)
			log.Debug("parallel execution completed", zap.Int("resultCount", len(taskResults)))

			// Call callback for each result if set
			if e.resultCallback != nil {
				for _, tr := range taskResults {
					e.resultCallback(tr)
				}
			}

			for _, tr := range taskResults {
				if !tr.Success {
					result.Success = false
					if e.failFast {
						cancel()
						return result
					}
				}
			}
		}
	}

	result.WallClockDuration = time.Since(startTime).Milliseconds()
	return result
}

// detectParallelGroups detects which tasks can run in parallel
func (e *Executor) detectParallelGroups(tasks []Task) [][]Task {
	log.Debug("detectParallelGroups start", zap.Int("taskCount", len(tasks)))
	var groups [][]Task
	used := make(map[int]bool)

	for i, task1 := range tasks {
		if used[i] {
			continue
		}

		// Start a new parallel group with this task
		parallelGroup := []Task{task1}
		used[i] = true

		// Find other tasks that don't overlap with any task in this group
		for j, task2 := range tasks {
			if used[j] {
				continue
			}

			hasOverlapWithGroup := false
			for _, groupTask := range parallelGroup {
				if HasOverlap(groupTask, task2) {
					log.Debug("overlap detected",
						zap.String("task1", groupTask.ToolName),
						zap.String("task2", task2.ToolName))
					hasOverlapWithGroup = true
					break
				}
			}

			if !hasOverlapWithGroup {
				log.Debug("adding task to parallel group",
					zap.String("toolName", task2.ToolName),
					zap.Int("groupSize", len(parallelGroup)))
				parallelGroup = append(parallelGroup, task2)
				used[j] = true
			}
		}

		log.Debug("parallel group formed", zap.Int("groupIndex", len(groups)), zap.Int("groupSize", len(parallelGroup)))
		groups = append(groups, parallelGroup)
	}

	log.Debug("detectParallelGroups completed", zap.Int("parallelGroupCount", len(groups)))
	return groups
}

// executeTasksParallel executes multiple tasks in parallel with worker pool limiting.
// cancel is called on first failure when failFast is enabled, so that sibling tasks
// waiting for the semaphore (or running via exec.CommandContext) are stopped promptly.
func (e *Executor) executeTasksParallel(ctx context.Context, tasks []Task, cancel context.CancelFunc) []ExecutionResult {
	maxWorkers := env.GetMaxParallelWorkers()
	log.Debug("executeTasksParallel start",
		zap.Int("taskCount", len(tasks)),
		zap.Int("maxWorkers", maxWorkers))

	results := make([]ExecutionResult, len(tasks))
	var wg sync.WaitGroup

	// Shuffle tasks to improve load balancing with random distribution.
	// This helps avoid worst-case scenario where slow tasks all start late,
	// leaving workers idle. On average, slow tasks will be distributed
	// throughout the execution, keeping workers busy.
	taskIndices := make([]int, len(tasks))
	for i := range taskIndices {
		taskIndices[i] = i
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec // G404: non-cryptographic use (task shuffling for load balancing); math/rand is intentional
	rng.Shuffle(len(taskIndices), func(i, j int) {
		taskIndices[i], taskIndices[j] = taskIndices[j], taskIndices[i]
	})

	// Create a semaphore to limit concurrent workers
	semaphore := make(chan struct{}, maxWorkers)

	for _, origIdx := range taskIndices {
		task := tasks[origIdx]
		i := origIdx
		wg.Add(1)
		log.Debug("spawning parallel task", zap.Int("index", i), zap.String("toolName", task.ToolName))
		go func(idx int, t Task) {
			defer wg.Done()

			// Check if context is cancelled before acquiring semaphore
			select {
			case <-ctx.Done():
				log.Debug("parallel task skipped due to cancellation",
					zap.Int("index", idx), zap.String("toolName", t.ToolName))
				results[idx] = ExecutionResult{
					ToolName:      t.ToolName,
					Success:       false,
					Error:         errCancelled,
					Cancelled:     true,
					FailureReason: FailureReasonCancelled,
				}
				return
			case semaphore <- struct{}{}:
				// Acquired semaphore slot
			}
			defer func() { <-semaphore }() // Release slot when done

			// Check again after acquiring semaphore in case context was cancelled while waiting
			if ctx.Err() != nil {
				log.Debug("parallel task skipped after semaphore due to cancellation",
					zap.Int("index", idx), zap.String("toolName", t.ToolName))
				results[idx] = ExecutionResult{
					ToolName:      t.ToolName,
					Success:       false,
					Error:         errCancelled,
					Cancelled:     true,
					FailureReason: FailureReasonCancelled,
				}
				return
			}

			log.Debug("parallel task started", zap.Int("index", idx), zap.String("toolName", t.ToolName))
			results[idx] = e.executeTask(ctx, t)
			log.Debug("parallel task completed",
				zap.Int("index", idx),
				zap.String("toolName", t.ToolName),
				zap.Bool("success", results[idx].Success))

			if e.failFast && !results[idx].Success {
				log.Debug("fail-fast: cancelling sibling parallel tasks",
					zap.Int("index", idx), zap.String("toolName", t.ToolName))
				cancel()
			}
		}(i, task)
	}

	wg.Wait()
	log.Debug("executeTasksParallel completed", zap.Int("taskCount", len(tasks)))
	return results
}

// executeTask executes a single task
func (e *Executor) executeTask(ctx context.Context, task Task) ExecutionResult {
	startTime := time.Now()
	log.Debug("executeTask start",
		zap.String("toolName", task.ToolName),
		zap.String("app", task.OpConfig.App),
		zap.Int("fileCount", len(task.Files)))

	// Determine working directory early for callback and result population
	workingDir := e.getWorkingDir(task)
	relativeDir := e.getRelativeDir(workingDir)

	// Call task start callback if set
	if e.taskStartCallback != nil {
		e.taskStartCallback(task.ToolName, relativeDir)
	}

	result := ExecutionResult{
		ToolName: task.ToolName,
		Success:  true,
	}

	// Get command info
	cmdInfo, err := e.appManager.GetCommandInfo(ctx, task.OpConfig.App)
	if err != nil {
		log.Debug("failed to get command info",
			zap.String("app", task.OpConfig.App),
			zap.String("workingDir", workingDir),
			zap.String("relativeDir", relativeDir),
			zap.Error(err))

		result.Success = false
		result.Error = fmt.Errorf("failed to get command info: %w", err)
		result.recordTiming(startTime)
		result.WorkingDir = workingDir
		result.RelativeDir = relativeDir
		result.FailureReason = FailureReasonIndependent

		// Call file progress callback even on error to maintain progress tracking
		if e.fileProgressCallback != nil {
			// Determine if this would be batch mode
			batch := task.OpConfig.Batch
			if batch == nil {
				defaultBatch := task.OpConfig.Scope != config.ToolScopePerFile
				batch = &defaultBatch
			}

			if *batch || len(task.Files) == 0 {
				// Batch mode: count as 1 unit
				e.fileProgressCallback(task.ToolName, 1, 1, false)
			} else {
				// Per-file mode: count each file
				for i := range task.Files {
					e.fileProgressCallback(task.ToolName, i+1, len(task.Files), false)
				}
			}
		}
		return result
	}

	log.Debug("working directory determined",
		zap.String("workingDir", workingDir),
		zap.String("relativeDir", relativeDir),
		zap.String("scope", string(task.OpConfig.Scope)))

	// Execute based on scope and batch mode
	// For per-file scope, the planner already created individual tasks per file
	// For per-project and repository scopes, we execute in batch or per-file based on batch flag
	batch := task.OpConfig.Batch
	if batch == nil {
		// Default batch behavior based on scope
		defaultBatch := task.OpConfig.Scope != config.ToolScopePerFile
		batch = &defaultBatch
	}

	if *batch || len(task.Files) == 0 {
		// Batch execution (all files at once) or whole-project mode
		result = e.executeBatch(ctx, task, cmdInfo, workingDir, startTime)
	} else {
		// Per-file execution
		result = e.executePerFile(ctx, task, cmdInfo, workingDir, startTime)
	}

	// Classify unclassified failures as independent (tool failed on its own)
	if !result.Success && result.FailureReason == FailureReasonNone {
		result.FailureReason = FailureReasonIndependent
	}

	log.Debug("executeTask completed",
		zap.String("toolName", task.ToolName),
		zap.Bool("success", result.Success),
		zap.Int64("durationMs", result.Duration))

	return result
}

// buildCommand creates an exec.Cmd from CommandInfo and arguments.
// Environment merge order: OS env -> app env (cmdInfo.Env) -> toolOpEnv (ToolOperation.Env).
func (e *Executor) buildCommand(ctx context.Context, cmdInfo *binmanager.CommandInfo, args []string, workingDir string, toolOpEnv map[string]string) *exec.Cmd {
	var cmd *exec.Cmd

	switch cmdInfo.Type {
	case "shell", "uv", "node", "jvm":
		allArgs := make([]string, 0, len(cmdInfo.Args)+len(args))
		allArgs = append(allArgs, cmdInfo.Args...)
		allArgs = append(allArgs, args...)
		cmd = exec.CommandContext(ctx, cmdInfo.Command, allArgs...) //nolint:gosec // G204: command path comes from the trusted managed store (binmanager.CommandInfo) and args come from validated config
	default:
		cmd = exec.CommandContext(ctx, cmdInfo.Command, args...) //nolint:gosec // G204: command path comes from the trusted managed store (binmanager.CommandInfo) and args come from validated config
	}

	cmd.Dir = workingDir

	// Apply environment with proper merge order:
	// OS env -> color hints -> app env -> tool operation env
	colorHints := clr.ChildEnvHints()
	if len(cmdInfo.Env) > 0 || len(toolOpEnv) > 0 || len(colorHints) > 0 {
		cmd.Env = mergeEnvLayers(cmd.Environ(), colorHints, cmdInfo.Env, toolOpEnv)
	}

	log.Debug("buildCommand", zap.Int("countOfArgs", len(cmd.Args)), zap.String("dir", cmd.Dir), zap.String("path", cmd.Path), zap.Strings("args", cmd.Args))

	return cmd
}

// mergeEnvLayers merges environment variable layers with later layers overriding earlier ones.
// Order: base (OS env) -> layers[0] (app env) -> layers[1] (tool operation env) -> ...
func mergeEnvLayers(base []string, layers ...map[string]string) []string {
	extra := 0
	for _, layer := range layers {
		extra += len(layer)
	}
	env := make([]string, 0, len(base)+extra)
	env = append(env, base...)

	keyToIdx := make(map[string]int, len(env))
	for i, e := range env {
		if j := strings.IndexByte(e, '='); j > 0 {
			keyToIdx[e[:j]] = i
		}
	}

	for _, layer := range layers {
		for key, value := range layer {
			envVar := fmt.Sprintf("%s=%s", key, value)
			if idx, ok := keyToIdx[key]; ok {
				env[idx] = envVar
			} else {
				keyToIdx[key] = len(env)
				env = append(env, envVar)
			}
		}
	}

	return env
}

// formatCommandString formats a command for display (dry-run, logging)
func (e *Executor) formatCommandString(cmdInfo *binmanager.CommandInfo, args []string) string {
	switch cmdInfo.Type {
	case "shell", "uv", "node", "jvm":
		allArgs := make([]string, 0, len(cmdInfo.Args)+len(args))
		allArgs = append(allArgs, cmdInfo.Args...)
		allArgs = append(allArgs, args...)
		return fmt.Sprintf("%s %s", cmdInfo.Command, strings.Join(allArgs, " "))
	default:
		return fmt.Sprintf("%s %s", cmdInfo.Command, strings.Join(args, " "))
	}
}

// getRelativeDir returns the working directory relative to git root
func (e *Executor) getRelativeDir(workingDir string) string {
	relPath, err := filepath.Rel(e.rootPath, workingDir)
	if err != nil || relPath == "." {
		return ""
	}
	return relPath
}

// filterFilesByCache filters files based on cache, returns files that need to be processed
func (e *Executor) filterFilesByCache(task Task) []string {
	if e.cache == nil {
		return task.Files
	}

	// Check if caching is enabled for this tool (default: true)
	toolCacheEnabled := true
	if task.OpConfig.Cache != nil {
		toolCacheEnabled = *task.OpConfig.Cache
	}

	// Convert operation type
	var cacheOp cache.Operation
	if task.Operation == config.OpLint {
		cacheOp = cache.OperationLint
	} else {
		cacheOp = cache.OperationFix
	}

	var filesToProcess []string
	for _, file := range task.Files {
		if e.cache.ShouldRun(file, task.ToolName, cacheOp, toolCacheEnabled) {
			filesToProcess = append(filesToProcess, file)
		}
	}

	return filesToProcess
}

// updateCacheAfterSuccess updates cache after successful tool execution
func (e *Executor) updateCacheAfterSuccess(task Task, files []string) {
	if e.cache == nil {
		return
	}

	// Check if caching is enabled for this tool (default: true)
	toolCacheEnabled := true
	if task.OpConfig.Cache != nil {
		toolCacheEnabled = *task.OpConfig.Cache
	}

	for _, file := range files {
		var err error
		if task.Operation == config.OpLint {
			err = e.cache.AfterLint(file, task.ToolName, toolCacheEnabled)
		} else {
			err = e.cache.AfterFix(file, task.ToolName, toolCacheEnabled)
		}

		if err != nil {
			log.Warn("failed to update cache",
				zap.String("file", file),
				zap.String("tool", task.ToolName),
				zap.Error(err))
		}
	}

	// Mark cache as dirty (async save)
	e.cache.MarkDirty()
}

// executePerFile executes a tool once per file
// joinStreams concatenates a tool's captured stdout and stderr for the textual
// fallback display (the parser sees them apart).
func joinStreams(stdout, stderr []byte) []byte {
	switch {
	case len(stderr) == 0:
		return stdout
	case len(stdout) == 0:
		return stderr
	default:
		out := make([]byte, 0, len(stdout)+1+len(stderr))
		out = append(out, stdout...)
		out = append(out, '\n')
		return append(out, stderr...)
	}
}

// parseFileDiagnostics runs the tool's declared parser over one invocation's
// captured output and appends the resolved diagnostics to result. Diagnostics the
// parser left without a file are stamped with `file`; batch callers pass "" and
// rely on the parser reporting a path per diagnostic (eslint's JSON does).
// A parse failure is logged, not fatal — the tool's own pass/fail is unaffected.
func (e *Executor) parseFileDiagnostics(ctx context.Context, result *ExecutionResult, task Task, file string, stdout, stderr []byte, exitCode int) {
	op := task.Tool.OutputParser
	//nolint:gosec // G115: a process exit code is small; the int32 cast is intentional.
	diags, err := e.parser.Parse(ctx, op.Module, op.Parser, task.ToolName, stdout, stderr, int32(exitCode))
	if err != nil {
		log.Warn("output parser failed",
			zap.String("tool", task.ToolName),
			zap.String("module", op.Module),
			zap.String("parser", op.Parser),
			zap.String("file", file),
			zap.Error(err))
		return
	}
	for i := range diags {
		if diags[i].File == "" {
			diags[i].File = file
		}
	}
	result.Diagnostics = append(result.Diagnostics, diags...)
}

func (e *Executor) executePerFile(ctx context.Context, task Task, cmdInfo *binmanager.CommandInfo, workingDir string, startTime time.Time) ExecutionResult {
	log.Debug("executePerFile start", zap.String("toolName", task.ToolName), zap.Int("fileCount", len(task.Files)))

	// Filter files by cache
	filesToProcess := e.filterFilesByCache(task)
	cachedCount := len(task.Files) - len(filesToProcess)

	if cachedCount > 0 {
		log.Debug("cache hits", zap.Int("count", cachedCount), zap.String("tool", task.ToolName))
	}

	result := ExecutionResult{
		ToolName:    task.ToolName,
		Success:     true,
		WorkingDir:  workingDir,
		RelativeDir: e.getRelativeDir(workingDir),
		ExitCode:    0,
		Scope:       task.OpConfig.Scope,
		Batch:       false,
	}

	// If all files are cached, return success immediately
	if len(filesToProcess) == 0 {
		result.recordTiming(startTime)
		// Even when all cached, call progress callback for each file
		for i := range task.Files {
			if e.fileProgressCallback != nil {
				e.fileProgressCallback(task.ToolName, i+1, len(task.Files), true)
			}
		}
		return result
	}

	// Report progress for cached files using total file count for consistent display
	totalFiles := len(task.Files)
	if e.fileProgressCallback != nil && cachedCount > 0 {
		for i := range cachedCount {
			e.fileProgressCallback(task.ToolName, i+1, totalFiles, true)
		}
	}

	var outputs []string
	var lastExitCode int
	var processedFiles []string

	for i, file := range filesToProcess {
		// Check if context is cancelled before processing next file
		if ctx.Err() != nil {
			log.Debug("per-file execution cancelled, skipping remaining files",
				zap.String("toolName", task.ToolName),
				zap.Int("remainingFiles", len(filesToProcess)-i))
			result.Success = false
			result.Error = fmt.Errorf("%w: %d files remaining", errCancelled, len(filesToProcess)-i)
			result.Cancelled = true
			result.FailureReason = FailureReasonCancelled
			break
		}

		args := e.replacePlaceholders(task.OpConfig.Args, file, task.Files, task.ProjectPath, task.ToolName)
		cmdString := e.formatCommandString(cmdInfo, args)

		if e.dryRun {
			log.Debug("dry-run mode", zap.String("file", file), zap.Strings("args", args))
			outputs = append(outputs, "[DRY-RUN] "+cmdString)
			processedFiles = append(processedFiles, file)

			// Call progress callback for dry-run files (offset by cached count)
			if e.fileProgressCallback != nil {
				e.fileProgressCallback(task.ToolName, cachedCount+i+1, totalFiles, true)
			}
			continue
		}

		// Store the command for reporting (use last command in per-file mode)
		result.Command = cmdString

		log.Debug("executing per-file command",
			zap.Int("fileIndex", i),
			zap.String("file", file),
			zap.Strings("args", args))

		opEnv := e.replaceEnvPlaceholders(task.OpConfig.Env, task.ProjectPath, task.ToolName)
		cmd := e.buildCommand(ctx, cmdInfo, args, workingDir, opEnv)

		stdinContent, stdinErr := stdinForOperation(task.OpConfig, file)
		if stdinErr != nil {
			result.Success = false
			result.Error = fmt.Errorf("failed to prepare stdin for file %s: %w", file, stdinErr)
			if e.fileProgressCallback != nil {
				e.fileProgressCallback(task.ToolName, cachedCount+i+1, totalFiles, false)
			}
			if e.failFast {
				break
			}
			continue
		}

		formatMode := task.OpConfig.Output == config.ToolOutputStdout
		// A formatter's stdout is the formatted file content, not diagnostics, so it
		// must never be fed to the parser. Validation already limits output:stdout to
		// the fix op, but a tool could still pair it with a tool-level outputParser;
		// !formatMode keeps the parser off the formatted text in that case.
		parseMode := e.parser != nil && task.Tool.OutputParser != nil && !formatMode
		// Both formatting and parsing need stdout and stderr kept apart.
		separate := formatMode || parseMode
		stdoutBytes, stderrBytes, err := e.runCommandIO(cmd, stdinContent, separate)

		var output []byte
		switch {
		case formatMode:
			// stdout is the candidate formatted content; surface it separately
			// and report only stderr (diagnostics) as the operation output.
			result.CapturedStdout = string(stdoutBytes)
			output = stderrBytes
		case parseMode:
			// Both streams were captured apart for the parser; for the textual
			// fallback display keep them both.
			output = joinStreams(stdoutBytes, stderrBytes)
		default:
			output = stdoutBytes
		}
		outputs = append(outputs, string(output))

		exitCode := getExitCode(err)
		lastExitCode = exitCode

		// Parse this file's output into structured diagnostics when the tool
		// declares an outputParser. Per-file mode means each invocation lints one
		// file, so the diagnostics belong to it.
		if parseMode {
			e.parseFileDiagnostics(ctx, &result, task, file, stdoutBytes, stderrBytes, exitCode)
		}

		// Formatting pipeline (diff-in-core): in stdout-output mode a successful
		// run yields the full new file text on stdout. Treat the original file as
		// "before", the captured stdout as "after", compute the minimal line-based
		// diff and apply it. No change → no edits → the file is left untouched
		// (mtime preserved). No WASM parser is involved — formatting is text→text.
		if err == nil && formatMode {
			original := stdinContent // already the file content in stdin mode
			if original == nil {
				var readErr error
				original, readErr = os.ReadFile(file)
				if readErr != nil {
					err = fmt.Errorf("read original content for %s: %w", file, readErr)
				}
			}
			if err == nil && len(stdoutBytes) == 0 && len(original) > 0 {
				// A stdout-mode formatter that exits 0 but emits nothing is
				// misbehaving (e.g. it formats in place and writes only to
				// stderr). Treat it as an error rather than letting the empty
				// candidate truncate the file to zero bytes.
				err = fmt.Errorf("formatter produced empty stdout for non-empty file %s", file)
			}
			if err == nil {
				edits, fmtErr := applyStdoutFormat(file, original, stdoutBytes)
				if fmtErr != nil {
					err = fmtErr
				} else {
					result.FormatEdits = edits
				}
			}
		}

		// Output command output in debug mode
		if len(output) > 0 {
			log.Debug("command output",
				zap.String("tool", task.ToolName),
				zap.String("file", file),
				zap.String("output", string(output)))
		}

		fileSuccess := err == nil
		if err != nil {
			log.Debug("per-file execution failed",
				zap.String("file", file),
				zap.Int("exitCode", exitCode),
				zap.Error(err))
			result.Success = false
			result.ExitCode = exitCode
			result.Error = fmt.Errorf("failed to execute for file %s (exit code %d): %w", file, exitCode, err)
			if ctx.Err() != nil {
				result.Cancelled = true
				result.FailureReason = FailureReasonCancelled
				if e.fileProgressCallback != nil {
					e.fileProgressCallback(task.ToolName, cachedCount+i+1, totalFiles, fileSuccess)
				}
				break
			}
			if e.failFast {
				log.Debug("fail-fast triggered in per-file execution")
				// Call progress callback before breaking (offset by cached count)
				if e.fileProgressCallback != nil {
					e.fileProgressCallback(task.ToolName, cachedCount+i+1, totalFiles, fileSuccess)
				}
				break
			}
		} else {
			log.Debug("per-file execution succeeded", zap.String("file", file))
			processedFiles = append(processedFiles, file)
		}

		// Call progress callback after processing each file (offset by cached count)
		if e.fileProgressCallback != nil {
			e.fileProgressCallback(task.ToolName, cachedCount+i+1, totalFiles, fileSuccess)
		}
	}

	// Update cache for successfully processed files
	if len(processedFiles) > 0 {
		e.updateCacheAfterSuccess(task, processedFiles)
	}

	// If all succeeded, use exit code 0
	if result.Success {
		result.ExitCode = 0
	} else if result.ExitCode == 0 {
		// If marked as failed but no exit code set, use last exit code
		result.ExitCode = lastExitCode
	}

	result.Output = strings.Join(outputs, "\n")
	result.recordTiming(startTime)
	log.Debug("executePerFile completed",
		zap.String("toolName", task.ToolName),
		zap.Bool("success", result.Success),
		zap.Int("exitCode", result.ExitCode),
		zap.Int64("durationMs", result.Duration))
	return result
}

// executeBatch executes a tool with all files at once, chunking if necessary
func (e *Executor) executeBatch(ctx context.Context, task Task, cmdInfo *binmanager.CommandInfo, workingDir string, startTime time.Time) ExecutionResult {
	log.Debug("executeBatch start", zap.String("toolName", task.ToolName), zap.Int("fileCount", len(task.Files)))

	// Filter files by cache
	filesToProcess := e.filterFilesByCache(task)
	cachedCount := len(task.Files) - len(filesToProcess)

	if cachedCount > 0 {
		log.Debug("cache hits", zap.Int("count", cachedCount), zap.String("tool", task.ToolName))
	}

	result := ExecutionResult{
		ToolName:    task.ToolName,
		Success:     true,
		Scope:       task.OpConfig.Scope,
		Batch:       true,
		WorkingDir:  workingDir,
		RelativeDir: e.getRelativeDir(workingDir),
	}

	// If files were specified but all are cached, return success immediately.
	// Do not skip when task.Files is nil (whole-project mode with no globs).
	if len(task.Files) > 0 && len(filesToProcess) == 0 {
		result.recordTiming(startTime)
		// Call file progress callback for batch mode (counts as 1 unit of work)
		if e.fileProgressCallback != nil {
			e.fileProgressCallback(task.ToolName, 1, 1, true)
		}
		return result
	}

	// Convert files to relative paths relative to workingDir
	relativeFiles := e.makeRelativePaths(filesToProcess, workingDir)

	// Whole-project mode: no files to chunk, execute once with no file args
	if len(relativeFiles) == 0 {
		chunkResult := e.executeBatchChunk(ctx, task, cmdInfo, workingDir, nil, startTime)
		if e.fileProgressCallback != nil {
			e.fileProgressCallback(task.ToolName, 1, 1, chunkResult.Success)
		}
		return chunkResult
	}

	// A batch tool whose args never mention the files runs the same command no
	// matter how the list is split (tsc reads tsconfig.json, not argv). Chunking
	// it re-runs one identical command N times: N× the work, and — now that batch
	// output is parsed — every diagnostic reported N times. Run it once.
	if !argsReferenceFiles(task.OpConfig.Args) {
		log.Debug("batch args do not reference files; running once",
			zap.String("toolName", task.ToolName), zap.Int("fileCount", len(relativeFiles)))
		chunkResult := e.executeBatchChunk(ctx, task, cmdInfo, workingDir, nil, startTime)
		if e.fileProgressCallback != nil {
			e.fileProgressCallback(task.ToolName, 1, 1, chunkResult.Success)
		}
		if chunkResult.Success {
			e.updateCacheAfterSuccess(task, filesToProcess)
		}
		return chunkResult
	}

	// Split files into chunks based on command line length limits
	chunks := e.chunkFilesByCommandLength(relativeFiles, task.OpConfig.Args, cmdInfo)
	log.Debug("files split into chunks", zap.Int("chunkCount", len(chunks)))

	// If only one chunk, execute sequentially (no need for parallelization)
	if len(chunks) == 1 {
		chunkResult := e.executeBatchChunk(ctx, task, cmdInfo, workingDir, chunks[0], startTime)
		if e.fileProgressCallback != nil {
			e.fileProgressCallback(task.ToolName, 1, 1, chunkResult.Success)
		}
		// Update cache on success
		if chunkResult.Success {
			e.updateCacheAfterSuccess(task, filesToProcess)
		}
		return chunkResult
	}

	// Multiple chunks - execute in parallel
	result = e.executeBatchChunksParallel(ctx, task, cmdInfo, workingDir, chunks, startTime)
	if e.fileProgressCallback != nil {
		e.fileProgressCallback(task.ToolName, 1, 1, result.Success)
	}
	// Update cache on success
	if result.Success {
		e.updateCacheAfterSuccess(task, filesToProcess)
	}
	return result
}

// argsReferenceFiles reports whether an operation's args place the matched files
// on the command line, via {files} or {file} (standalone or embedded). When they
// do not, the file list only decides *whether* the tool runs, not what it is
// given — so splitting it into chunks changes nothing but the number of runs.
func argsReferenceFiles(args []string) bool {
	for _, arg := range args {
		if strings.Contains(arg, "{files}") || strings.Contains(arg, "{file}") {
			return true
		}
	}
	return false
}

// executeBatchChunk executes a single chunk of files in batch mode
func (e *Executor) executeBatchChunk(ctx context.Context, task Task, cmdInfo *binmanager.CommandInfo, workingDir string, files []string, startTime time.Time) ExecutionResult {
	result := ExecutionResult{
		ToolName:    task.ToolName,
		Success:     true,
		WorkingDir:  workingDir,
		RelativeDir: e.getRelativeDir(workingDir),
		ExitCode:    0,
		Scope:       task.OpConfig.Scope,
		Batch:       true,
	}

	args := e.replacePlaceholders(task.OpConfig.Args, "", files, task.ProjectPath, task.ToolName)
	cmdString := e.formatCommandString(cmdInfo, args)
	result.Command = cmdString

	if e.dryRun {
		log.Debug("dry-run mode", zap.Strings("args", args))
		result.Output = "[DRY-RUN] " + cmdString
		result.recordTiming(startTime)
		return result
	}

	log.Debug("executing batch command", zap.Strings("args", args), zap.String("workingDir", workingDir))
	opEnv := e.replaceEnvPlaceholders(task.OpConfig.Env, task.ProjectPath, task.ToolName)
	cmd := e.buildCommand(ctx, cmdInfo, args, workingDir, opEnv)

	// A batch tool with an outputParser (eslint --format=json, …) emits machine
	// output on stdout while wrappers and the runtime write noise to stderr, so the
	// parser must see the streams apart — a combined capture would hand it a JSON
	// document with prose glued in front of it. formatMode is stdout-as-file-content
	// and must never reach the parser (validation keeps it off batch lint, but a
	// tool could still pair output:stdout with a tool-level parser).
	formatMode := task.OpConfig.Output == config.ToolOutputStdout
	parseMode := e.parser != nil && task.Tool.OutputParser != nil && !formatMode
	stdoutBytes, stderrBytes, err := e.runCommandIO(cmd, nil, parseMode)

	output := stdoutBytes
	if parseMode {
		// Keep both streams in the textual fallback shown when parsing yields nothing.
		output = joinStreams(stdoutBytes, stderrBytes)
	}
	result.Output = string(output)

	// Output command output in debug mode
	if len(output) > 0 {
		log.Debug("command output",
			zap.String("tool", task.ToolName),
			zap.String("output", string(output)))
	}

	exitCode := getExitCode(err)
	result.ExitCode = exitCode

	// Batch mode lints many files per invocation, so diagnostics carry their own
	// paths from the parser; nothing to stamp.
	if parseMode {
		e.parseFileDiagnostics(ctx, &result, task, "", stdoutBytes, stderrBytes, exitCode)
	}

	if err != nil {
		log.Debug("batch execution failed", zap.Int("exitCode", exitCode), zap.Error(err))
		result.Success = false
		result.Error = fmt.Errorf("failed to execute (exit code %d): %w", exitCode, err)
		if ctx.Err() != nil {
			result.Cancelled = true
			result.FailureReason = FailureReasonCancelled
		}
	} else {
		log.Debug("batch execution succeeded")
	}

	result.recordTiming(startTime)

	return result
}

// executeBatchChunksParallel executes multiple chunks in parallel with worker pool limiting
func (e *Executor) executeBatchChunksParallel(ctx context.Context, task Task, cmdInfo *binmanager.CommandInfo, workingDir string, chunks [][]string, startTime time.Time) ExecutionResult {
	maxWorkers := env.GetMaxParallelWorkers()
	log.Debug("executeBatchChunksParallel start",
		zap.Int("chunkCount", len(chunks)),
		zap.Int("maxWorkers", maxWorkers))

	result := ExecutionResult{
		ToolName:    task.ToolName,
		Success:     true,
		Scope:       task.OpConfig.Scope,
		Batch:       true,
		WorkingDir:  workingDir,
		RelativeDir: e.getRelativeDir(workingDir),
	}

	// Execute chunks in parallel with worker pool
	chunkResults := make([]ExecutionResult, len(chunks))
	var wg sync.WaitGroup
	var mu sync.Mutex // Protect access to result.Success

	// Create a semaphore to limit concurrent workers
	semaphore := make(chan struct{}, maxWorkers)

	for i, chunk := range chunks {
		wg.Add(1)
		log.Debug("spawning chunk execution", zap.Int("chunkIndex", i), zap.Int("fileCount", len(chunk)))
		go func(idx int, files []string) {
			defer wg.Done()

			// Check if context is cancelled before acquiring semaphore
			select {
			case <-ctx.Done():
				log.Debug("chunk execution skipped due to cancellation", zap.Int("chunkIndex", idx))
				chunkResults[idx] = ExecutionResult{
					ToolName:      task.ToolName,
					Success:       false,
					Error:         errCancelled,
					Cancelled:     true,
					FailureReason: FailureReasonCancelled,
				}
				mu.Lock()
				result.Success = false
				mu.Unlock()
				return
			case semaphore <- struct{}{}:
				// Acquired semaphore slot
			}
			defer func() { <-semaphore }() // Release slot when done

			// Check again after acquiring semaphore
			if ctx.Err() != nil {
				log.Debug("chunk execution skipped after semaphore due to cancellation", zap.Int("chunkIndex", idx))
				chunkResults[idx] = ExecutionResult{
					ToolName:      task.ToolName,
					Success:       false,
					Error:         errCancelled,
					Cancelled:     true,
					FailureReason: FailureReasonCancelled,
				}
				mu.Lock()
				result.Success = false
				mu.Unlock()
				return
			}

			chunkStart := time.Now()
			chunkResult := e.executeBatchChunk(ctx, task, cmdInfo, workingDir, files, chunkStart)
			chunkResults[idx] = chunkResult

			// Update overall result
			mu.Lock()
			if !chunkResult.Success {
				result.Success = false
			}
			mu.Unlock()

			log.Debug("chunk execution completed",
				zap.Int("chunkIndex", idx),
				zap.Bool("success", chunkResult.Success),
				zap.Int64("durationMs", chunkResult.Duration))
		}(i, chunk)
	}

	wg.Wait()

	// Combine outputs from all chunks and propagate cancellation status
	var outputs []string
	var errors []error
	allCancelled := !result.Success
	for i, chunkResult := range chunkResults {
		if chunkResult.Output != "" {
			outputs = append(outputs, fmt.Sprintf("=== Chunk %d/%d ===\n%s", i+1, len(chunks), chunkResult.Output))
		}
		if chunkResult.Error != nil {
			errors = append(errors, fmt.Errorf("chunk %d: %w", i+1, chunkResult.Error))
		}
		result.Diagnostics = append(result.Diagnostics, chunkResult.Diagnostics...)
		if !chunkResult.Success && chunkResult.FailureReason != FailureReasonCancelled {
			allCancelled = false
		}
	}

	result.Output = strings.Join(outputs, "\n")
	if len(errors) > 0 {
		result.Error = fmt.Errorf("batch execution had %d failures: %v", len(errors), errors)
	}

	if !result.Success && allCancelled {
		result.Cancelled = true
		result.FailureReason = FailureReasonCancelled
	}

	result.recordTiming(startTime)
	log.Debug("executeBatchChunksParallel completed",
		zap.String("toolName", task.ToolName),
		zap.Bool("success", result.Success),
		zap.Int("chunkCount", len(chunks)),
		zap.Int64("durationMs", result.Duration))

	return result
}

// replacePlaceholders replaces placeholders in arguments
// Special handling for {file} and {files}: they expand into separate arguments when used standalone
// {cwd} resolves to projectPath (the task's per-project working directory)
// {root} resolves to e.rootPath (the git repository root)
// {toolCache} resolves to the project-specific cache directory (computed per call)
func (e *Executor) replacePlaceholders(args []string, file string, files []string, projectPath string, toolName string) []string {
	log.Debug("replacePlaceholders",
		zap.Strings("inputArgs", args),
		zap.String("file", file),
		zap.Int("filesCount", len(files)))

	var result []string

	for _, arg := range args {
		// Handle {files} placeholder
		if strings.Contains(arg, "{files}") {
			// If {files} is the entire argument, expand it to multiple args
			if arg == "{files}" {
				result = append(result, files...)
				continue
			}
			// If {files} is part of a larger string, join them (legacy behavior)
			arg = strings.ReplaceAll(arg, "{files}", strings.Join(files, " "))
		}

		// Handle {file} placeholder
		if strings.Contains(arg, "{file}") && file != "" {
			// If {file} is the entire argument, use it as-is
			if arg == "{file}" {
				result = append(result, file)
				continue
			}
			// If {file} is part of a larger string, replace it
			arg = strings.ReplaceAll(arg, "{file}", file)
		}

		// {root}, {cwd} and {toolCache} are shared with environment-value expansion.
		arg = e.expandPathPlaceholders(arg, projectPath, toolName)

		result = append(result, arg)
	}

	log.Debug("replacePlaceholders result", zap.Strings("outputArgs", result))
	return result
}

// expandPathPlaceholders replaces the path placeholders shared by argument and
// environment-value expansion: {root} (git root), {cwd} (per-project working
// directory) and {toolCache} (per-project tool cache directory, always absolute).
func (e *Executor) expandPathPlaceholders(s string, projectPath, toolName string) string {
	s = strings.ReplaceAll(s, "{root}", e.rootPath)
	cwdValue := projectPath
	if cwdValue == "" {
		cwdValue = e.rootPath
	}
	s = strings.ReplaceAll(s, "{cwd}", cwdValue)
	if strings.Contains(s, "{toolCache}") {
		relativeProjectPath := ""
		if projectPath != "" {
			rel, err := filepath.Rel(e.rootPath, projectPath)
			if err != nil {
				log.Warn("failed to compute relative project path",
					zap.String("projectPath", projectPath),
					zap.String("rootPath", e.rootPath),
					zap.Error(err))
			} else if rel != "." {
				relativeProjectPath = rel
			}
		}
		cachePath, err := env.GetProjectCachePath(e.rootPath, relativeProjectPath, toolName)
		if err != nil {
			log.Warn("failed to compute project cache path", zap.Error(err))
		} else {
			s = strings.ReplaceAll(s, "{toolCache}", cachePath)
		}
	}
	return s
}

// replaceEnvPlaceholders expands path placeholders ({root}, {cwd}, {toolCache}) in
// tool-operation environment-variable values, mirroring argument expansion so that
// e.g. GOLANGCI_LINT_CACHE: "{toolCache}" resolves to an absolute path rather than
// reaching the tool as the literal "{toolCache}". Returns the input unchanged when
// there are no env vars.
func (e *Executor) replaceEnvPlaceholders(envMap map[string]string, projectPath, toolName string) map[string]string {
	if len(envMap) == 0 {
		return envMap
	}
	expanded := make(map[string]string, len(envMap))
	for key, value := range envMap {
		expanded[key] = e.expandPathPlaceholders(value, projectPath, toolName)
	}
	return expanded
}

// getWorkingDir determines the working directory for execution
// The working directory is now determined by the Planner and stored in task.ProjectPath
func (e *Executor) getWorkingDir(task Task) string {
	// If task has a specific ProjectPath set by planner, use it
	if task.ProjectPath != "" {
		return task.ProjectPath
	}

	// Fallback to root path
	return e.rootPath
}

// makeRelativePaths converts absolute file paths to paths relative to the base directory
func (e *Executor) makeRelativePaths(files []string, baseDir string) []string {
	if len(files) == 0 {
		return files
	}

	result := make([]string, len(files))
	for i, file := range files {
		relPath, err := filepath.Rel(baseDir, file)
		if err != nil {
			// If we can't make it relative, use the original path
			log.Debug("failed to make path relative",
				zap.String("file", file),
				zap.String("baseDir", baseDir),
				zap.Error(err))
			result[i] = file
		} else {
			result[i] = relPath
		}
	}

	log.Debug("makeRelativePaths",
		zap.String("baseDir", baseDir),
		zap.Int("fileCount", len(files)),
		zap.Strings("relativePaths", result))

	return result
}

// chunkFilesByCommandLength splits files into chunks that fit within command line length limits
func (e *Executor) chunkFilesByCommandLength(files []string, baseArgs []string, cmdInfo *binmanager.CommandInfo) [][]string {
	if len(files) == 0 {
		return nil
	}

	// Get max command line length from environment
	maxCommandLineLength := env.GetMaxCommandLength()

	// Calculate base command length (command + args without {files} placeholder)
	baseCmd := e.formatCommandString(cmdInfo, baseArgs)
	// Remove the {files} placeholder to get base length
	baseCmd = strings.ReplaceAll(baseCmd, "{files}", "")
	baseLength := len(baseCmd)

	// Account for spaces between files
	const spaceOverhead = 1

	var chunks [][]string
	var currentChunk []string
	currentLength := baseLength

	for _, file := range files {
		fileLength := len(file) + spaceOverhead

		// If adding this file would exceed the limit, start a new chunk
		if currentLength+fileLength > maxCommandLineLength && len(currentChunk) > 0 {
			chunks = append(chunks, currentChunk)
			currentChunk = []string{file}
			currentLength = baseLength + fileLength
		} else {
			currentChunk = append(currentChunk, file)
			currentLength += fileLength
		}
	}

	// Add the last chunk if it's not empty
	if len(currentChunk) > 0 {
		chunks = append(chunks, currentChunk)
	}

	log.Debug("chunkFilesByCommandLength",
		zap.Int("totalFiles", len(files)),
		zap.Int("chunkCount", len(chunks)),
		zap.Int("baseLength", baseLength),
		zap.Int("maxLength", maxCommandLineLength))

	return chunks
}

// getExitCode extracts the exit code from a command error
func getExitCode(err error) int {
	if err == nil {
		return 0
	}

	exitErr := &exec.ExitError{}
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			return status.ExitStatus()
		}
	}

	// If we can't determine the exit code, return -1
	return -1
}

// runCommandIO runs cmd with optional stdin content and either combined or
// separated stdout/stderr capture. When separate is false (the default for
// every existing tool) stdout and stderr are interleaved into a single buffer,
// returned as stdoutBytes with stderrBytes nil — byte-for-byte the historical
// behavior. When separate is true the streams are captured independently so the
// formatting path can treat stdout as the candidate file content while keeping
// diagnostics (stderr) apart. A nil stdinContent leaves stdin untouched.
func (e *Executor) runCommandIO(cmd *exec.Cmd, stdinContent []byte, separate bool) (stdoutBytes, stderrBytes []byte, err error) {
	var outBuf, errBuf, combined bytes.Buffer
	if separate {
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
	} else {
		cmd.Stdout = &combined
		cmd.Stderr = &combined
	}
	if stdinContent != nil {
		cmd.Stdin = bytes.NewReader(stdinContent)
	}

	setupProcessGroupCleanup(cmd)

	if startErr := cmd.Start(); startErr != nil {
		return nil, nil, fmt.Errorf("start command: %w", startErr)
	}

	err = cmd.Wait()
	if separate {
		return outBuf.Bytes(), errBuf.Bytes(), err
	}
	return combined.Bytes(), nil, err
}

// applyStdoutFormat treats candidate (the tool's separately-captured stdout) as
// the full new text for file, computes the minimal line-based diff against the
// original content (Task 8's textdiff) and applies it. It returns the edits that
// were applied, or nil when the candidate equals the original — in which case the
// file is left untouched so its mtime is preserved ("no change → no edits"). The
// existing file mode is preserved across the rewrite.
func applyStdoutFormat(file string, original, candidate []byte) ([]textdiff.Edit, error) {
	edits := textdiff.ComputeEdits(string(original), string(candidate))
	if len(edits) == 0 {
		return nil, nil
	}

	// Preserve the file's existing mode. The file was just read above, so a stat
	// failure here is unexpected; treat it as fatal rather than silently writing
	// back with a guessed 0o644 that could strip the executable (or other) bits.
	info, statErr := os.Stat(file)
	if statErr != nil {
		return nil, fmt.Errorf("stat %s before formatting: %w", file, statErr)
	}

	// Write the formatter's actual stdout (candidate), not a reconstruction from
	// the edits: candidate is the ground truth, and round-tripping through
	// textdiff.Apply could diverge on a diff edge case and silently corrupt the
	// file. The edits are returned only for FormatEdits (LSP/undo reuse later).
	if err := writeFileAtomic(file, candidate, info.Mode().Perm()); err != nil {
		return nil, fmt.Errorf("write formatted content to %s: %w", file, err)
	}
	return edits, nil
}

// writeFileAtomic writes data to a temp file in path's directory then renames it
// into place, so an interrupted or failed write never leaves the target — a
// user's source file — truncated or half-written. The rename is atomic within a
// single filesystem, which the same-directory temp guarantees. Like gofmt's
// in-place write, a symlinked target is replaced by a regular file rather than
// written through (an accepted tradeoff for atomicity; formatting targets are
// real source files in practice).
func writeFileAtomic(path string, data []byte, mode os.FileMode) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".dm-fmt-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName) // best-effort cleanup on any failure before the rename
		}
	}()

	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err = tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

// stdinForOperation returns the bytes to feed to the tool's stdin for this
// operation, or nil when the operation does not use stdin input. In stdin mode
// it reads the target file's content (per-file scope feeds one file at a time).
func stdinForOperation(op config.ToolOperation, file string) ([]byte, error) {
	if op.Input != config.ToolInputStdin || file == "" {
		return nil, nil
	}
	content, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read stdin content for %s: %w", file, err)
	}
	return content, nil
}
