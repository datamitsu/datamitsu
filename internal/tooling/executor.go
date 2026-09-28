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
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/cache"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/hashutil"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
	"github.com/datamitsu/datamitsu/internal/textdiff"
	"github.com/datamitsu/datamitsu/internal/toolenv"
	"github.com/datamitsu/datamitsu/internal/trace"

	"go.uber.org/zap"
)

var log = logger.Logger.With(zap.Namespace("cmd"))

// Execution counters. The spawn count is the one number that separates
// datamitsu's own overhead from the tools' work: a run whose wall time is
// dominated by N processes is bounded by those processes, not by the planner.
var (
	cntSpawn        = trace.NewCounter("exec.processes_spawned")
	cntVerdictHit   = trace.NewCounter("exec.verdict_cache_hits")
	cntCacheSkipped = trace.NewCounter("exec.files_skipped_by_cache")
	cntParse        = trace.NewCounter("exec.parser_invocations")
)

// errCancelled is a sentinel error used when tasks are cancelled due to fail-fast context cancellation.
var errCancelled = errors.New("cancelled")

// errFailFast is the cause the executor cancels a run with when a task fails
// under fail-fast. Any other cause — a signal the caller turned into a
// cancellation, an editor withdrawing a request — is an interruption, and the
// two are reported differently: a fail-fast cancel is a consequence of the
// failure shown beside it, an interruption is not.
var errFailFast = errors.New("fail-fast")

// errStart marks a command that could not be started at all.
var errStart = errors.New("start command")

// errStopped marks a process the executor's context stopped while it ran. A
// process that ended on its own keeps its own error even when the context is
// cancelled a moment later — while its output is being parsed, say — so the
// failure it reported is not taken for a cancellation.
var errStopped = errors.New("stopped by cancellation")

func stoppedByCancellation(err error) bool {
	return errors.Is(err, errStopped) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func cancelReason(ctx context.Context) FailureReason {
	if errors.Is(context.Cause(ctx), errFailFast) {
		return FailureReasonCancelled
	}
	return FailureReasonInterrupted
}

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
	parserModules        config.MapOfParsers  // the declared parser modules, for the verdict identity
	gate                 Gate                 // Optional: fails a process on its parsed findings
	envObserver          func(environ []string)
	stepCallback         StepCallback
	steps                int
	capturePatches       bool
	limits               ParseLimits

	// cmdInfos memoizes command resolution for the lifetime of one Execute; it is
	// nil outside one (FormatContent), which resolves directly.
	cmdInfos atomic.Pointer[commandInfoMemo]
}

// AppManager interface for getting application command information
type AppManager interface {
	GetBinaryPath(ctx context.Context, appName string) (string, error)
	GetCommandInfo(ctx context.Context, appName string) (*binmanager.CommandInfo, error)
}

// DiagnosticParser turns a tool's raw output into resolved diagnostics. It is
// injected via SetParser; when nil (the default) the executor never parses.
// The concrete implementation (in the runner) loads the WASM modules and
// applies the defaults-in-core resolution, and reports what could not be
// parsed once per run.
type DiagnosticParser interface {
	// Parse loads the WASM module named by `module` (a parsers config entry),
	// dispatches its `parser` (the key inside the module), and labels the resulting
	// diagnostics with `toolName` as their source.
	Parse(ctx context.Context, module, parser, toolName string, stdout, stderr []byte, exitCode int32) (ParseAnswer, error)
	// Fallback runs the sniffer of the fallback module the binary embeds.
	Fallback(ctx context.Context, toolName string, stdout, stderr []byte, exitCode int32) (ParseAnswer, error)
	// FellBack is told that the fallback read, as format, the output of a
	// tool whose declared parser did not recognize it.
	FellBack(toolName string, declared config.OutputParser, format string)
	// Unrecognized is told that neither the declared parser nor the fallback
	// recognized a tool's output.
	Unrecognized(toolName string, declared config.OutputParser)
}

// ParseAnswer is a parser's answer to one output, resolved.
type ParseAnswer struct {
	Diagnostics []diagnostic.Diagnostic
	// Recognized is false when the parser found nothing of its format in the
	// output, which is not the same answer as finding nothing in it.
	Recognized bool
	// Format is the key the parser read: a format key, or a tool name.
	Format string
	// FormatParser reports that the key read a standard format any tool may
	// print, not one tool's own output.
	FormatParser bool
	// Partial is true when the output holds a document the parser could not
	// read whole, so the findings may not be all there were.
	Partial bool
}

// ResultCallback is called when a task completes
type ResultCallback func(result ExecutionResult)

// TaskStartCallback is called when a task starts executing. taskID is the
// task's ID, which its progress callbacks and its result carry too.
type TaskStartCallback func(taskID, toolName, relativeDir string)

// FileProgressCallback is called after each file, or each whole-task run, of
// the task taskID is processed.
type FileProgressCallback func(taskID, toolName string, fileIndex, totalFiles int, success bool)

// StepCallback is called after each step of an execution: the tasks of one
// parallel group, which ran together — alone, or at once over disjoint file
// sets — and after every earlier step had ended. step counts the steps of one
// Execute from 1. It is called after the step's result callbacks, also when a
// task of the step failed or was cancelled; a step the run never reached is
// not reported.
type StepCallback func(step int, tasks []Task)

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

// SetStepCallback sets the callback called after each step of an execution.
func (e *Executor) SetStepCallback(callback StepCallback) {
	e.stepCallback = callback
}

// SetCapturePatches makes a formatter that writes its result on stdout keep a
// unified diff of each file it changed (FileResult.Patch). It costs a diff per
// changed file and holds source text, so only a run that writes the patches
// asks for it.
func (e *Executor) SetCapturePatches(capture bool) {
	e.capturePatches = capture
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

// ParseLimits bound what one tool process's output costs to parse: the bytes of
// each stream a parser reads and the findings kept. Output beyond either is
// dropped and the process's extraction is truncated.
type ParseLimits struct {
	InputBytes int
	Findings   int
}

// EffectiveParseLimits are the parse caps of the effective runtime
// configuration.
func EffectiveParseLimits() ParseLimits {
	eff, err := runtimeconfig.Get()
	if err != nil {
		eff = runtimeconfig.Compute()
	}
	return ParseLimits{InputBytes: eff.MaxParseInputBytes, Findings: eff.MaxFindingsPerProcess}
}

// SetParseLimits replaces the executor's parse limits, which are otherwise
// those of the effective runtime configuration; a limit that is not positive
// keeps that value.
func (e *Executor) SetParseLimits(limits ParseLimits) {
	e.limits = limits
}

// orDefaults is l with each limit that is not positive at its effective value.
func (l ParseLimits) orDefaults() ParseLimits {
	defaults := EffectiveParseLimits()
	if l.InputBytes <= 0 {
		l.InputBytes = defaults.InputBytes
	}
	if l.Findings <= 0 {
		l.Findings = defaults.Findings
	}
	return l
}

// SetParserModules tells the executor which parser modules the configuration
// declares. A unit verdict names the module its tool's output was parsed with,
// so every executor sharing a cache has to be told the same modules.
func (e *Executor) SetParserModules(parsers config.MapOfParsers) {
	e.parserModules = parsers
}

// Execute runs an execution plan
func (e *Executor) Execute(ctx context.Context, plan *ExecutionPlan) ([]GroupExecutionResult, error) {
	log.Debug("starting execution plan", zap.Int("groupCount", len(plan.Groups)))
	var results []GroupExecutionResult

	// A fresh memo per Execute: an app installed between two runs must be
	// re-resolved, and only within one run is the answer constant.
	e.cmdInfos.Store(newCommandInfoMemo())
	defer e.cmdInfos.Store(nil)
	e.steps = 0

	e.assignTaskIDs(plan)

	execCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	failFast := func() { cancel(errFailFast) }

	for _, group := range plan.Groups {
		// Check if already cancelled before starting next group
		if execCtx.Err() != nil {
			log.Debug("skipping group due to cancellation", zap.Int("priority", group.Priority))
			break
		}

		log.Debug("executing group", zap.Int("priority", group.Priority), zap.Int("taskCount", len(group.Tasks)))
		groupResult := e.executeGroup(execCtx, group, failFast)
		results = append(results, groupResult)

		log.Debug("group execution completed",
			zap.Int("priority", group.Priority),
			zap.Bool("success", groupResult.Success),
			zap.Int("resultCount", len(groupResult.Results)))

		// Fail-fast: stop on first group failure
		if e.failFast && !groupResult.Success {
			log.Debug("fail-fast triggered", zap.Int("priority", group.Priority))
			failFast()
			// Collect failed tool errors, skip cancelled tasks (noise from fail-fast)
			var errorMessages []string
			for _, r := range groupResult.Results {
				if !r.Success && !errors.Is(r.Error, errCancelled) && !r.IsCancelled() {
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

// SetEnvObserver wires a function handed the environment of every tool process
// the executor builds, as the process gets it — placeholders expanded, app and
// operation env layered. It is called concurrently.
func (e *Executor) SetEnvObserver(observe func(environ []string)) {
	e.envObserver = observe
}

// AssignTaskIDs names the tasks of plan as Execute does, so a caller can refer
// to them before execution starts — or when it never does. Execute names them
// again, identically.
func (e *Executor) AssignTaskIDs(plan *ExecutionPlan) {
	e.assignTaskIDs(plan)
}

// TaskDir is the directory a task runs in, relative to the git root, as its
// results report it in RelativeDir ("" is the root).
func (e *Executor) TaskDir(task Task) string {
	return e.getRelativeDir(e.getWorkingDir(task))
}

// endStep reports that the tasks of one parallel group have ended.
func (e *Executor) endStep(tasks []Task) {
	e.steps++
	if e.stepCallback != nil {
		e.stepCallback(e.steps, tasks)
	}
}

// assignTaskIDs names every task of plan "<tool>:<dir>:<seq>", seq counting the
// plan's tasks from 1 in plan order. The names are written into plan itself,
// so a caller can tell which planned task a result, an event or a stopped task
// stands for: tasks of one tool in one directory are otherwise
// indistinguishable.
func (e *Executor) assignTaskIDs(plan *ExecutionPlan) {
	seq := 0
	for g := range plan.Groups {
		for t := range plan.Groups[g].Tasks {
			seq++
			task := &plan.Groups[g].Tasks[t]
			task.ID = fmt.Sprintf("%s:%s:%d", task.ToolName, e.TaskDir(*task), seq)
		}
	}
}

// executeGroup executes a task group. failFast cancels the whole run; it is
// called only when the executor runs with fail-fast.
func (e *Executor) executeGroup(ctx context.Context, group TaskGroup, failFast func()) (result GroupExecutionResult) {
	startTime := time.Now()
	log.Debug("executeGroup start", zap.Int("priority", group.Priority), zap.Int("tasks", len(group.Tasks)))
	result = GroupExecutionResult{
		Priority: group.Priority,
		Success:  true,
	}

	// Recorded in a defer, not at the bottom of the function: the two fail-fast
	// branches below return early, and they used to skip the assignment
	// entirely. A group that failed fast therefore reported a wall-clock time of
	// zero, and the run footer showed "done in 0ms" for a run that had just spent
	// seconds spawning processes — with several groups, it silently reported the
	// sum of only the groups that did not fail.
	defer func() {
		result.WallClockDuration = time.Since(startTime).Milliseconds()
	}()

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
			e.endStep(parallelTasks)

			if !taskResult.Success {
				result.Success = false
				if e.failFast {
					failFast()
					return result
				}
			}
		} else {
			// Multiple non-overlapping tasks - execute in parallel
			log.Debug("executing tasks in parallel", zap.Int("parallelTaskCount", len(parallelTasks)))
			taskResults := e.executeTasksParallel(ctx, parallelTasks, failFast)
			result.Results = append(result.Results, taskResults...)
			log.Debug("parallel execution completed", zap.Int("resultCount", len(taskResults)))

			// Call callback for each result if set
			if e.resultCallback != nil {
				for _, tr := range taskResults {
					e.resultCallback(tr)
				}
			}
			e.endStep(parallelTasks)

			for _, tr := range taskResults {
				if !tr.Success {
					result.Success = false
					if e.failFast {
						failFast()
						return result
					}
				}
			}
		}
	}

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
// failFast is called on first failure when fail-fast is enabled, so that sibling tasks
// waiting for the semaphore (or running via exec.CommandContext) are stopped promptly.
func (e *Executor) executeTasksParallel(ctx context.Context, tasks []Task, failFast func()) []ExecutionResult {
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
				results[idx] = e.unstartedResult(ctx, t)
				return
			case semaphore <- struct{}{}:
				// Acquired semaphore slot
			}
			defer func() { <-semaphore }() // Release slot when done

			// Check again after acquiring semaphore in case context was cancelled while waiting
			if ctx.Err() != nil {
				log.Debug("parallel task skipped after semaphore due to cancellation",
					zap.Int("index", idx), zap.String("toolName", t.ToolName))
				results[idx] = e.unstartedResult(ctx, t)
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
				failFast()
			}
		}(i, task)
	}

	wg.Wait()
	log.Debug("executeTasksParallel completed", zap.Int("taskCount", len(tasks)))
	return results
}

// unstartedResult is the result of a task cancelled while it waited for a
// worker. It carries the task's directory like any other result, so a caller can
// tell which planned task it stands for; it has no timing because nothing ran.
func (e *Executor) unstartedResult(ctx context.Context, task Task) ExecutionResult {
	workingDir := e.getWorkingDir(task)
	result := ExecutionResult{
		ToolName:      task.ToolName,
		TaskID:        task.ID,
		Success:       false,
		Error:         errCancelled,
		WorkingDir:    workingDir,
		RelativeDir:   e.getRelativeDir(workingDir),
		Scope:         task.OpConfig.Scope,
		Cancelled:     true,
		FailureReason: cancelReason(ctx),
	}
	describeFiles(task, &result, FileNotStarted)
	return result
}

// executeTask executes a single task
func (e *Executor) executeTask(ctx context.Context, task Task) ExecutionResult {
	startTime := time.Now()
	taskSpan := trace.Start(trace.CatExec, "executeTask")
	defer func() {
		taskSpan.EndWith(
			trace.A("tool", task.ToolName),
			trace.A("app", task.OpConfig.App),
			trace.A("files", len(task.Files)),
		)
	}()
	log.Debug("executeTask start",
		zap.String("toolName", task.ToolName),
		zap.String("app", task.OpConfig.App),
		zap.Int("fileCount", len(task.Files)))

	task.inherited = toolenv.Capture(os.Environ(), task.OpConfig.InheritEnv)
	// Taken before the tool runs: a config saved while it runs must not have
	// this run's result recorded under its digest.
	task.perFileCache = e.perFileCacheTool(task)

	// Determine working directory early for callback and result population
	workingDir := e.getWorkingDir(task)
	relativeDir := e.getRelativeDir(workingDir)

	// Call task start callback if set
	if e.taskStartCallback != nil {
		e.taskStartCallback(task.ID, task.ToolName, relativeDir)
	}

	result := ExecutionResult{
		ToolName: task.ToolName,
		TaskID:   task.ID,
		Success:  true,
	}

	// Get command info
	cmdSpan := trace.Start(trace.CatExec, "getCommandInfo")
	cmdInfo, err := e.commandInfo(ctx, task.OpConfig.App)
	cmdSpan.EndWith(trace.A("app", task.OpConfig.App))
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
		fileState := FileSetupFailed
		if ctx.Err() != nil && stoppedByCancellation(err) {
			result.Cancelled = true
			result.FailureReason = cancelReason(ctx)
			fileState = FileNotStarted
		}
		describeFiles(task, &result, fileState)

		// Call file progress callback even on error to maintain progress tracking
		if e.fileProgressCallback != nil {
			if !config.RunsPerFile(task.OpConfig, len(task.Files)) {
				// One process for the whole task: count as 1 unit
				e.fileProgressCallback(task.ID, task.ToolName, 1, 1, false)
			} else {
				// Per-file mode: count each file
				for i := range task.Files {
					e.fileProgressCallback(task.ID, task.ToolName, i+1, len(task.Files), false)
				}
			}
		}
		return result
	}

	log.Debug("working directory determined",
		zap.String("workingDir", workingDir),
		zap.String("relativeDir", relativeDir),
		zap.String("scope", string(task.OpConfig.Scope)))

	// A stored verdict means every member and guard is byte-identical to the run
	// that passed, which is as sound for a narrowed invocation as for a full one
	// — so the read is not gated on coverage. Only the write is.
	verdictSpan := trace.Start(trace.CatCache, "verdictKeys")
	verdictKey, verdictSnap, verdictBytes, verdictApplies := e.verdictKeys(task)
	// The lookup is a map read under a read lock, so folding it into this span
	// keeps the recorded duration comparable while letting the hit/miss ride along
	// with the member count and byte volume that produced it — the three numbers
	// are only meaningful together.
	verdictHit := verdictApplies && !e.cache.ShouldRunVerdict(verdictKey, verdictSnap.hash(), e.verdictTTL())
	verdictSpan.EndWith(
		trace.A("tool", task.ToolName),
		trace.A("applies", verdictApplies),
		trace.A("hit", verdictHit),
		trace.A("members", len(task.UnitMembers)),
		trace.A("guards", len(task.UnitGuards)),
		trace.A("bytes", verdictBytes),
	)
	// verdictHit already implies verdictApplies.
	if verdictHit {
		cntVerdictHit.Add(1)
		log.Debug("verdict cache hit", zap.String("tool", task.ToolName), zap.String("unit", task.UnitDir))
		// Same shape as a real run: consumers key JSON-L on RelativeDir and
		// print the scope badge only when Scope is set, so a hit that omitted
		// them would emit a differently-shaped event for the same task.
		result.Success = true
		result.WorkingDir = workingDir
		result.RelativeDir = relativeDir
		result.Scope = task.OpConfig.Scope
		result.recordTiming(startTime)
		describeVerdictHit(task, &result)
		if e.fileProgressCallback != nil {
			e.fileProgressCallback(task.ID, task.ToolName, 1, 1, true)
		}
		return result
	}

	// Dispatch on argv shape, not scope: only {file} takes one path, so only it
	// needs one process per file.
	if config.RunsPerFile(task.OpConfig, len(task.Files)) {
		result = e.executePerFile(ctx, task, cmdInfo, workingDir, startTime)
	} else {
		result = e.executeBatch(ctx, task, cmdInfo, workingDir, startTime)
	}

	// One write point per task, after every process it spawned has succeeded.
	// The three per-process updateCacheAfterSuccess calls would otherwise let the
	// first success of an N-process task record a verdict a later failure refutes.
	if result.Success && verdictEligible(result) {
		e.recordVerdict(task, verdictKey, verdictSnap, verdictApplies)
	}

	// Classify unclassified failures as independent (tool failed on its own)
	if !result.Success && result.FailureReason == FailureReasonNone {
		result.FailureReason = FailureReasonIndependent
	}
	result.TaskID = task.ID
	describeFiles(task, &result, FileNotStarted)

	log.Debug("executeTask completed",
		zap.String("toolName", task.ToolName),
		zap.Bool("success", result.Success),
		zap.Int64("durationMs", result.Duration))

	return result
}

// buildCommand creates an exec.Cmd from CommandInfo and arguments. The tool's
// environment is toolenv.Apply's: the process environment without the stripped
// variables, then the inherited host pairs, the app env (cmdInfo.Env) and the
// operation env (ToolOperation.Env), and NO_COLOR=1 last.
func (e *Executor) buildCommand(ctx context.Context, cmdInfo *binmanager.CommandInfo, args []string, workingDir string, toolOpEnv map[string]string, inherited toolenv.Inherited) *exec.Cmd {
	var cmd *exec.Cmd

	switch cmdInfo.Type {
	case "shell", "bun", "uv", "node", "jvm":
		allArgs := make([]string, 0, len(cmdInfo.Args)+len(args))
		allArgs = append(allArgs, cmdInfo.Args...)
		allArgs = append(allArgs, args...)
		cmd = exec.CommandContext(ctx, cmdInfo.Command, allArgs...) //nolint:gosec // G204: command path comes from the trusted managed store (binmanager.CommandInfo) and args come from validated config
	default:
		cmd = exec.CommandContext(ctx, cmdInfo.Command, args...) //nolint:gosec // G204: command path comes from the trusted managed store (binmanager.CommandInfo) and args come from validated config
	}

	cmd.Dir = workingDir
	cmd.Env = toolenv.Apply(cmd.Environ(), inherited, cmdInfo.Env, toolOpEnv)
	if e.envObserver != nil {
		e.envObserver(cmd.Env)
	}

	log.Debug("buildCommand", zap.Int("countOfArgs", len(cmd.Args)), zap.String("dir", cmd.Dir), zap.String("path", cmd.Path), zap.Strings("args", cmd.Args))

	return cmd
}

// formatCommandString formats a command for display (dry-run, logging)
func (e *Executor) formatCommandString(cmdInfo *binmanager.CommandInfo, args []string) string {
	switch cmdInfo.Type {
	case "shell", "bun", "uv", "node", "jvm":
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

// filterFilesByCache returns the files the tool must run on, and for each of
// them the bytes it is about to be handed. A pass is recorded against those
// bytes (C2), so they are hashed before the run, through the process-wide
// content memo, for a file the cache has never seen as much as for one it has.
// seen is nil when no per-file pass can be recorded for the task.
func (e *Executor) filterFilesByCache(task Task) (filesToProcess []string, seen map[string]cache.Seen) {
	if e.cache == nil {
		return task.Files, nil
	}

	// Per-file entries record "this file passed", which is only a verdict when a
	// file's result stands alone. For unit and repo granularity it is not: tsc's
	// answer depends on tsconfig.json, which no .ts glob matches, so an edit to
	// it left every content hash unchanged and the whole task was skipped with a
	// tick. Those granularities use the verdict cache instead.
	if config.InferGranularity(task.OpConfig) != config.GranularityFile {
		return task.Files, nil
	}

	if task.OpConfig.Cache != nil && !*task.OpConfig.Cache {
		return task.Files, nil
	}

	cacheOp := cache.OperationFix
	if task.Operation == config.OpLint {
		cacheOp = cache.OperationLint
	}

	cacheTool := task.perFileCache
	if cacheTool == "" {
		cacheTool = e.perFileCacheTool(task)
	}
	filterSpan := trace.Start(trace.CatCache, "filterFilesByCache")
	seen = make(map[string]cache.Seen, len(task.Files))
	for _, file := range task.Files {
		observed := observe(file)
		if e.cache.Check(file, cacheTool, cacheOp, observed, true) {
			filesToProcess = append(filesToProcess, file)
			seen[filepath.Clean(file)] = observed
		}
	}
	cntCacheSkipped.Add(int64(len(task.Files) - len(filesToProcess)))
	filterSpan.EndWith(
		trace.A("tool", task.ToolName),
		trace.A("in", len(task.Files)),
		trace.A("out", len(filesToProcess)),
	)

	return filesToProcess, seen
}

// perFileCacheTool is the name a per-file cache entry records a pass under.
// For an operation that reads managed configs it carries a digest of their
// content, so editing .yamlfmt.yaml — or init rewriting its internal copy —
// re-runs that tool on every file without discarding any other tool's entries,
// which folding the digest into the cache-wide invalidation key would. For an
// operation that inherits host variables it carries a digest of the values it
// was handed, for the same reason: the invalidation key hashes configuration,
// and a host value is not configuration.
func (e *Executor) perFileCacheTool(task Task) string {
	name := task.ToolName
	if refs := task.OpConfig.ManagedConfigRefs; len(refs) > 0 {
		parts := make([][]byte, 0, 2*len(refs))
		for _, ref := range refs {
			path := e.expandPathPlaceholders(ref.Path, task.ProjectPath, task.ToolName)
			data, err := os.ReadFile(path)
			if err != nil {
				data = []byte("\x00missing")
			}
			parts = append(parts, []byte(ref.Key), data)
		}
		name += "@" + hashutil.XXH3Multi(parts...)
	}
	if pairs := task.inherited.Pairs(); len(pairs) > 0 {
		parts := make([][]byte, 0, len(pairs))
		for _, kv := range pairs {
			parts = append(parts, []byte(kv))
		}
		name += "+env:" + hashutil.XXH3Multi(parts...)
	}
	return name
}

// updateCacheAfterSuccess records the passes of files a tool succeeded on. A
// lint pass is recorded against the bytes filterFilesByCache saw before the
// run, and only when the file still holds them: a file edited while the tool
// ran would otherwise be marked as passing content the tool never read.
func (e *Executor) updateCacheAfterSuccess(task Task, files []string, seen map[string]cache.Seen) {
	if e.cache == nil || seen == nil {
		return
	}

	cacheTool := e.perFileCacheTool(task)
	if task.perFileCache != "" && task.perFileCache != cacheTool {
		log.Debug("managed config changed while the tool ran; not caching its result",
			zap.String("tool", task.ToolName))
		return
	}
	for _, file := range files {
		var err error
		if task.Operation == config.OpLint {
			observed, ok := seen[filepath.Clean(file)]
			unchanged := ok && unchangedSince(file, observed)
			if ok && !unchanged {
				log.Debug("file changed while the tool ran; not recording its pass",
					zap.String("file", file), zap.String("tool", task.ToolName))
			}
			err = e.cache.AfterLint(file, cacheTool, observed, unchanged, true)
		} else {
			err = e.cache.AfterFix(file, cacheTool, true)
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

// parseFileDiagnostics extracts the findings of one process's captured output
// and records on proc what it yielded: its diagnostics, its extraction outcome
// and what read them. A path the tool reported is made absolute against
// workingDir, the directory the process ran in. When proc was given exactly one
// file, a diagnostic the parser left without a file is about it; a process
// given several files, or none, leaves such a diagnostic file-less. A parse
// failure is not fatal — the tool's own pass/fail is unaffected — and is
// reported once per run by the parser, so it is logged here at debug only.
//
// Three parsers may read the output, in turn (extract): the declared one when
// declared is set, then, when it did not recognize the output, the sniffer of
// the fallback module the binary embeds.
//
// The parsers read the streams without their ANSI sequences: a tool that
// colours its output even into a pipe would otherwise hide a position or a
// level from a line parser behind an escape. The caller keeps the raw streams
// for the frame. They read at most the executor's ParseLimits of either stream
// and keep at most its limit of findings; a process that exceeds either is
// truncated, with the findings that were kept.
func (e *Executor) parseFileDiagnostics(ctx context.Context, proc *ProcessResult, task Task, workingDir string, stdout, stderr []byte, exitCode int, declared bool) {
	limits := e.limits.orDefaults()
	stdout, stderr = StripCSI(stdout), StripCSI(stderr)
	stdout, cutOut := limits.cut(stdout)
	stderr, cutErr := limits.cut(stderr)
	cntParse.Add(1)
	parseSpan := trace.Start(trace.CatParse, "parseDiagnostics")
	x := e.extract(ctx, task, proc.Files, workingDir, stdout, stderr, exitCode, declared)
	parseSpan.EndWith(
		trace.A("tool", task.ToolName),
		trace.A("provenance", x.provenance),
		trace.A("bytes", len(stdout)+len(stderr)),
		trace.A("diagnostics", len(x.diags)),
	)
	proc.Extraction, proc.ParseError = x.outcome, x.parseError
	proc.Provenance, proc.ParserModule = x.provenance, x.module
	diags := x.diags
	dropped := len(diags) > limits.Findings
	if dropped {
		diags = diags[:limits.Findings]
	}
	proc.Diagnostics = diags
	if x.outcome == ExtractionParsedClean && len(diags) > 0 {
		proc.Extraction = ExtractionParsedFindings
	}
	if (cutOut || cutErr || dropped || x.partial) && x.outcome != ExtractionParserUnavailable {
		proc.Extraction = ExtractionTruncated
		log.Debug("tool output exceeded a parse limit or held a document cut off",
			zap.String("tool", task.ToolName),
			zap.Bool("stdoutCut", cutOut), zap.Bool("stderrCut", cutErr), zap.Bool("findingsDropped", dropped),
			zap.Bool("partialDocument", x.partial))
	}
}

// extracted is what the parsers made of one output.
type extracted struct {
	diags      []diagnostic.Diagnostic
	outcome    Extraction
	parseError string
	provenance string
	module     string
	// partial: the output held a document the parser could not read whole.
	partial bool
}

// Provenances of findings.
const (
	ProvenanceParser         = "parser"
	ProvenanceFormat         = "format"
	ProvenanceFallbackPrefix = "fallback:"
)

// EmbeddedParserModule is the name the fallback module the binary embeds is
// served under (config.ReservedParserModule).
const EmbeddedParserModule = config.ReservedParserModule

// lineFormats are the formats the fallback recognizes by a matching line, not
// by an envelope, in the order the sniffer tries them: prose can match them,
// so a line whose path is not a file on disk does not count.
var lineFormats = []string{"github-annotations", "azure-logissue", "msvc", "gcc"}

// extract runs, in turn, the parsers that may read one output. A declared
// parser that recognized it decides. Otherwise — no parser declared, one that
// failed, did not recognize the output, or answered with an empty ABI 1 list
// under a non-zero exit — the embedded fallback may read it: what it
// recognizes is kept with the fallback:<format> provenance; a declared parser
// that did not recognize the output is then parse-failed, and one the run
// could not use stays parser-unavailable, its findings kept all the same.
// Without a declaration, output the fallback does not recognize has no
// extraction (none), and the exit status decides as it always did.
func (e *Executor) extract(ctx context.Context, task Task, files []string, workingDir string, stdout, stderr []byte, exitCode int, declared bool) extracted {
	x := extracted{outcome: ExtractionNone}
	op := task.Tool.OutputParser
	//nolint:gosec // G115: a process exit code is small; the int32 cast is intentional.
	code := int32(exitCode)
	// A parse that failed with an error is reported as such; one that
	// answered without recognizing the output only once the fallback, too,
	// recognized nothing.
	unrecognized := false
	if declared {
		answer, err := e.parser.Parse(ctx, op.Module, op.Parser, task.ToolName, stdout, stderr, code)
		switch {
		case err != nil:
			x.outcome, x.parseError = ExtractionParseFailed, err.Error()
			if _, unavailable := errors.AsType[*ParserUnavailableError](err); unavailable {
				x.outcome = ExtractionParserUnavailable
			}
			log.Debug("output parser failed",
				zap.String("tool", task.ToolName),
				zap.String("module", op.Module),
				zap.String("parser", op.Parser),
				zap.String("extraction", string(x.outcome)),
				zap.Strings("files", files),
				zap.Error(err))
		case answer.Recognized:
			provenance := ProvenanceParser
			if answer.FormatParser {
				provenance = ProvenanceFormat
			}
			return extracted{
				diags:      located(answer.Diagnostics, files, workingDir),
				outcome:    ExtractionParsedClean,
				provenance: provenance,
				module:     op.Module,
				partial:    answer.Partial,
			}
		default:
			unrecognized = true
			x.outcome = ExtractionParseFailed
			x.parseError = fmt.Sprintf("parser %q of module %q did not recognize the output", op.Parser, op.Module)
		}
	}
	fallback, ok, err := e.fallback(ctx, task, files, workingDir, stdout, stderr, code)
	switch {
	case ok && declared:
		e.parser.FellBack(task.ToolName, *op, fallback.format)
	case !ok && unrecognized:
		e.parser.Unrecognized(task.ToolName, *op)
	}
	if !ok {
		// Without a declaration, nothing recognized keeps the exit-status rule
		// only when the fallback could read the output: one that failed, or
		// found a document cut off, has not shown the output holds nothing.
		switch {
		case declared:
		case err != nil:
			x.outcome, x.parseError = ExtractionParseFailed, err.Error()
		case fallback.x.partial:
			x.outcome, x.partial = ExtractionTruncated, true
			x.parseError = "the output holds a document cut off or malformed, which no parser read"
		}
		return x
	}
	fallback.x.outcome = ExtractionParsedClean
	if x.outcome == ExtractionParserUnavailable {
		fallback.x.outcome, fallback.x.parseError = ExtractionParserUnavailable, x.parseError
	}
	return fallback.x
}

// sniffed is what the fallback recognized in one output.
type sniffed struct {
	x      extracted
	format string
}

// fallback runs the embedded sniffer; ok is false when it recognized nothing,
// with the error when it could not run and x.partial when it found a document
// cut off. Of a line format it keeps only the findings on files that exist.
func (e *Executor) fallback(ctx context.Context, task Task, files []string, workingDir string, stdout, stderr []byte, code int32) (sniffed, bool, error) {
	if len(bytes.TrimSpace(stdout)) == 0 && len(bytes.TrimSpace(stderr)) == 0 {
		return sniffed{}, false, nil
	}
	answer, err := e.parser.Fallback(ctx, task.ToolName, stdout, stderr, code)
	if err != nil {
		log.Debug("the fallback parser failed", zap.String("tool", task.ToolName), zap.Error(err))
		return sniffed{}, false, err
	}
	partial := sniffed{x: extracted{partial: answer.Partial}}
	if !answer.Recognized {
		return partial, false, nil
	}
	diags := located(answer.Diagnostics, files, workingDir)
	if at := slices.Index(lineFormats, answer.Format); at >= 0 {
		diags = onDisk(diags)
		// A format whose every line named no file matched nothing: the line
		// formats the sniffer would have tried after it get their turn.
		for _, format := range lineFormats[at+1:] {
			if len(diags) > 0 {
				break
			}
			later, err := e.parser.Parse(ctx, EmbeddedParserModule, format, task.ToolName, stdout, stderr, code)
			if err != nil {
				log.Debug("the fallback parser failed", zap.String("tool", task.ToolName), zap.String("format", format), zap.Error(err))
				return sniffed{}, false, err
			}
			diags = onDisk(located(later.Diagnostics, files, workingDir))
			answer.Format = format
		}
		if len(diags) == 0 {
			return partial, false, nil
		}
	}
	return sniffed{
		x: extracted{
			diags:      diags,
			provenance: ProvenanceFallbackPrefix + answer.Format,
			module:     EmbeddedParserModule,
			partial:    answer.Partial,
		},
		format: answer.Format,
	}, true, nil
}

// located stamps a process's one file on a finding that names none and makes
// every path absolute against the process's working directory.
func located(diags []diagnostic.Diagnostic, files []string, workingDir string) []diagnostic.Diagnostic {
	stamp := ""
	if len(files) == 1 {
		stamp = files[0]
	}
	for i := range diags {
		if diags[i].File == "" {
			diags[i].File = stamp
		}
		diags[i].File = diagnostic.AbsPath(diags[i].File, workingDir)
	}
	return diags
}

// onDisk keeps the findings that name no file or a file that exists: a
// sniffed line naming a path that is not there is prose that looks like one.
func onDisk(diags []diagnostic.Diagnostic) []diagnostic.Diagnostic {
	kept := diags[:0]
	for _, d := range diags {
		if d.File == "" {
			kept = append(kept, d)
			continue
		}
		if info, err := os.Stat(d.File); err == nil && !info.IsDir() {
			kept = append(kept, d)
		}
	}
	return kept
}

// cut returns the part of stream a parser reads, and whether any was left out.
func (l ParseLimits) cut(stream []byte) ([]byte, bool) {
	if len(stream) <= l.InputBytes {
		return stream, false
	}
	return stream[:l.InputBytes], true
}

func (e *Executor) executePerFile(ctx context.Context, task Task, cmdInfo *binmanager.CommandInfo, workingDir string, startTime time.Time) ExecutionResult {
	log.Debug("executePerFile start", zap.String("toolName", task.ToolName), zap.Int("fileCount", len(task.Files)))

	filesToProcess, seen := e.filterFilesByCache(task)
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
		cached:      cachedOf(task.Files, filesToProcess),
	}

	// If all files are cached, return success immediately
	if len(filesToProcess) == 0 {
		result.recordTiming(startTime)
		// Even when all cached, call progress callback for each file
		for i := range task.Files {
			if e.fileProgressCallback != nil {
				e.fileProgressCallback(task.ID, task.ToolName, i+1, len(task.Files), true)
			}
		}
		return result
	}

	// Report progress for cached files using total file count for consistent display
	totalFiles := len(task.Files)
	if e.fileProgressCallback != nil && cachedCount > 0 {
		for i := range cachedCount {
			e.fileProgressCallback(task.ID, task.ToolName, i+1, totalFiles, true)
		}
	}

	var outputs []fileOutput
	var failures []error
	var lastExitCode int
	var passed []string
	// The failure a frame shows is the last failing file's: its exit code and
	// its command, not those of a later file that passed or was cancelled.
	var failedCommand string
	var failedExit int
	// A task that already failed on its own stays a failure when the run is
	// then cancelled: the cancellation only leaves the rest of its files
	// unchecked, and hiding the failure would hide what the run found.
	failedOnOwn := false
	// gatedOnly stays true while every failure is a threshold failure.
	gatedOnly := true

	formatMode := task.OpConfig.Output == config.ToolOutputStdout
	// A formatter's stdout is the formatted file content, not diagnostics, so it
	// must never be fed to a parser. Validation already limits output:stdout to
	// the fix op, but a tool could still pair it with a tool-level outputParser;
	// !formatMode keeps the declared parser off the formatted text in that
	// case, and only its stderr reaches the fallback.
	declared := e.parser != nil && task.Tool.OutputParser != nil && !formatMode
	parseMode := e.parser != nil
	// Formatting and every parser need stdout and stderr kept apart: a line of
	// stderr landing inside a JSON document on stdout would break it.
	separate := formatMode || parseMode

	for i, file := range filesToProcess {
		// Check if context is cancelled before processing next file
		if ctx.Err() != nil {
			log.Debug("per-file execution cancelled, skipping remaining files",
				zap.String("toolName", task.ToolName),
				zap.Int("remainingFiles", len(filesToProcess)-i))
			result.addNotStarted(filesToProcess[i:])
			if failedOnOwn {
				result.FilesNotRun = len(filesToProcess) - i
				break
			}
			result.Success = false
			result.Error = fmt.Errorf("%w: %d files remaining", errCancelled, len(filesToProcess)-i)
			result.Cancelled = true
			result.FailureReason = cancelReason(ctx)
			break
		}

		args := e.replacePlaceholders(task.OpConfig.Args, file, task.Files, task.ProjectPath, task.ToolName)
		cmdString := e.formatCommandString(cmdInfo, args)

		if e.dryRun {
			log.Debug("dry-run mode", zap.String("file", file), zap.Strings("args", args))
			outputs = append(outputs, fileOutput{text: "[DRY-RUN] " + cmdString})
			result.addProcess(ProcessResult{
				Files: []string{filepath.Clean(file)}, State: ProcessNotStarted, Extraction: ExtractionNone,
			})

			// Call progress callback for dry-run files (offset by cached count)
			if e.fileProgressCallback != nil {
				e.fileProgressCallback(task.ID, task.ToolName, cachedCount+i+1, totalFiles, true)
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
		cmd := e.buildCommand(ctx, cmdInfo, args, workingDir, opEnv, task.inherited)

		stdinContent, stdinErr := stdinForOperation(task.OpConfig, file)
		if stdinErr != nil {
			failedOnOwn = true
			failedCommand, failedExit = cmdString, -1
			result.Success = false
			result.ExitCode = -1
			stdinFailure := fmt.Errorf("failed to prepare stdin for file %s: %w", file, stdinErr)
			failures = append(failures, stdinFailure)
			gatedOnly = false
			// The frame shows the joined output, not Error, once any file wrote
			// some: without this line a later file's output would stand in for
			// a failure no process reported.
			outputs = append(outputs, fileOutput{text: stdinFailure.Error(), failed: true})
			if parseMode {
				result.UnparsedFailures = append(result.UnparsedFailures, stdinFailure.Error())
			}
			result.addProcess(ProcessResult{
				Files: []string{filepath.Clean(file)}, State: ProcessSetupFailed, Extraction: ExtractionNone,
				OutputTail: outputTail([]byte(stdinFailure.Error())),
			})
			if e.fileProgressCallback != nil {
				e.fileProgressCallback(task.ID, task.ToolName, cachedCount+i+1, totalFiles, false)
			}
			if e.failFast {
				result.FilesNotRun = len(filesToProcess) - i - 1
				result.addNotStarted(filesToProcess[i+1:])
				break
			}
			continue
		}

		procStart := time.Now()
		stdoutBytes, stderrBytes, err := e.runCommandIO(cmd, stdinContent, separate)
		procDuration := time.Since(procStart).Milliseconds()

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
		outputs = append(outputs, fileOutput{text: string(output)})

		exitCode := getExitCode(err)
		lastExitCode = exitCode

		proc := ProcessResult{
			Files: []string{filepath.Clean(file)}, OutputTail: outputTail(output), DurationMs: procDuration,
		}
		proc.Extraction, proc.ParseError = e.unparsed(task, formatMode)
		if parseMode {
			parseOut := stdoutBytes
			if formatMode {
				parseOut = nil
			}
			e.parseFileDiagnostics(ctx, &proc, task, workingDir, parseOut, stderrBytes, exitCode, declared)
			if gateErr := e.applyGate(task, &proc, err == nil); gateErr != nil {
				err = gateErr
			}
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
					proc.edits = edits
					if e.capturePatches && len(edits) > 0 {
						proc.patch = textdiff.Unified(e.patchPath(file), string(original), string(stdoutBytes))
					}
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

		proc.State, proc.Success = processState(err), err == nil
		if proc.State == ProcessRan {
			proc.ExitCode = new(exitCode)
		}
		result.addProcess(proc)

		fileSuccess := err == nil
		if err != nil {
			log.Debug("per-file execution failed",
				zap.String("file", file),
				zap.Int("exitCode", exitCode),
				zap.Error(err))
			if stoppedByCancellation(err) {
				if failedOnOwn {
					result.FilesNotRun = len(filesToProcess) - i
				} else {
					result.Success = false
					result.ExitCode = exitCode
					result.Error = fmt.Errorf("failed to execute for file %s (exit code %d): %w", file, exitCode, err)
					result.Cancelled = true
					result.FailureReason = cancelReason(ctx)
				}
				if e.fileProgressCallback != nil {
					e.fileProgressCallback(task.ID, task.ToolName, cachedCount+i+1, totalFiles, fileSuccess)
				}
				result.addNotStarted(filesToProcess[i+1:])
				break
			}
			failedOnOwn = true
			result.Success = false
			result.ExitCode = exitCode
			fileFailure := fmt.Errorf("failed to execute for file %s (exit code %d): %w", file, exitCode, err)
			if proc.ThresholdFailed {
				fileFailure = fmt.Errorf("file %s: %w", file, err)
			} else {
				gatedOnly = false
			}
			failures = append(failures, fileFailure)
			label, explained := failureLabel(workingDir, file, exitCode, err)
			outputs[len(outputs)-1].failed = true
			outputs[len(outputs)-1].label = label
			outputs[len(outputs)-1].explained = explained
			if parseMode && len(proc.Diagnostics) == 0 {
				unparsed := label
				if text := strings.TrimSpace(string(output)); text != "" {
					unparsed += "\n" + text
				}
				result.UnparsedFailures = append(result.UnparsedFailures, unparsed)
			}
			failedCommand, failedExit = cmdString, exitCode
			if e.failFast {
				log.Debug("fail-fast triggered in per-file execution")
				// Call progress callback before breaking (offset by cached count)
				if e.fileProgressCallback != nil {
					e.fileProgressCallback(task.ID, task.ToolName, cachedCount+i+1, totalFiles, fileSuccess)
				}
				result.FilesNotRun = len(filesToProcess) - i - 1
				result.addNotStarted(filesToProcess[i+1:])
				break
			}
		} else {
			log.Debug("per-file execution succeeded", zap.String("file", file))
			passed = append(passed, passesOf(proc, proc.Files)...)
		}

		// Call progress callback after processing each file (offset by cached count)
		if e.fileProgressCallback != nil {
			e.fileProgressCallback(task.ID, task.ToolName, cachedCount+i+1, totalFiles, fileSuccess)
		}
	}

	if len(passed) > 0 {
		e.updateCacheAfterSuccess(task, passed, seen)
	}

	// A threshold failure exits 0, so a zero exit code says nothing about
	// whether a failure recorded one.
	switch {
	case result.Success:
		result.ExitCode = 0
	case failedCommand != "":
		result.Command, result.ExitCode = failedCommand, failedExit
	case result.ExitCode == 0:
		result.ExitCode = lastExitCode
	}

	switch len(failures) {
	case 0:
	case 1:
		result.Error = failures[0]
	default:
		result.Error = errors.Join(failures...)
	}
	if len(failures) > 0 && gatedOnly && !result.Cancelled {
		result.FailureReason = FailureReasonThreshold
	}
	result.Output = joinFileOutputs(outputs)
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

	filesToProcess, seen := e.filterFilesByCache(task)
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
		cached:      cachedOf(task.Files, filesToProcess),
	}

	// If files were specified but all are cached, return success immediately.
	// Do not skip when task.Files is nil (whole-project mode with no globs).
	if len(task.Files) > 0 && len(filesToProcess) == 0 {
		result.recordTiming(startTime)
		// Call file progress callback for batch mode (counts as 1 unit of work)
		if e.fileProgressCallback != nil {
			e.fileProgressCallback(task.ID, task.ToolName, 1, 1, true)
		}
		return result
	}

	// Convert files to relative paths relative to workingDir
	relativeFiles := e.makeRelativePaths(filesToProcess, workingDir)

	cached := result.cached

	// Whole-project mode: no files to chunk, execute once with no file args
	if len(relativeFiles) == 0 {
		chunkResult := e.executeBatchChunk(ctx, task, cmdInfo, workingDir, nil, startTime)
		chunkResult.cached = cached
		if e.fileProgressCallback != nil {
			e.fileProgressCallback(task.ID, task.ToolName, 1, 1, chunkResult.Success)
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
		chunkResult.cached = cached
		if e.fileProgressCallback != nil {
			e.fileProgressCallback(task.ID, task.ToolName, 1, 1, chunkResult.Success)
		}
		e.updateCacheAfterSuccess(task, batchPasses(chunkResult.Processes, filesToProcess), seen)
		return chunkResult
	}

	// Split files into chunks based on command line length limits
	chunks := e.chunkFilesByCommandLength(relativeFiles, task.OpConfig.Args, cmdInfo)
	log.Debug("files split into chunks", zap.Int("chunkCount", len(chunks)))

	// If only one chunk, execute sequentially (no need for parallelization)
	if len(chunks) == 1 {
		chunkResult := e.executeBatchChunk(ctx, task, cmdInfo, workingDir, chunks[0], startTime)
		chunkResult.cached = cached
		if e.fileProgressCallback != nil {
			e.fileProgressCallback(task.ID, task.ToolName, 1, 1, chunkResult.Success)
		}
		e.updateCacheAfterSuccess(task, batchPasses(chunkResult.Processes, nil), seen)
		return chunkResult
	}

	// Multiple chunks - execute in parallel
	result = e.executeBatchChunksParallel(ctx, task, cmdInfo, workingDir, chunks, startTime)
	result.cached = cached
	if e.fileProgressCallback != nil {
		e.fileProgressCallback(task.ID, task.ToolName, 1, 1, result.Success)
	}
	e.updateCacheAfterSuccess(task, batchPasses(result.Processes, nil), seen)
	return result
}

// batchPasses returns the files the successful processes of a batch task let a
// pass be recorded for. Each process answers for the files it was given, as a
// per-file process does, so a chunk that passed keeps its passes when another
// failed; a process given none (argv-less, one run for the whole list) answers
// for covered.
func batchPasses(processes []ProcessResult, covered []string) []string {
	passes := make([]string, 0, len(covered))
	for _, proc := range processes {
		if proc.State != ProcessRan || !proc.Success {
			continue
		}
		files := proc.Files
		if len(files) == 0 {
			files = make([]string, len(covered))
			for i, file := range covered {
				files[i] = filepath.Clean(file)
			}
		}
		passes = append(passes, passesOf(proc, files)...)
	}
	return passes
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
		result.addProcess(ProcessResult{
			Files: absolutePaths(files, workingDir), State: ProcessNotStarted, Extraction: ExtractionNone,
		})
		result.recordTiming(startTime)
		return result
	}

	log.Debug("executing batch command", zap.Strings("args", args), zap.String("workingDir", workingDir))
	opEnv := e.replaceEnvPlaceholders(task.OpConfig.Env, task.ProjectPath, task.ToolName)
	cmd := e.buildCommand(ctx, cmdInfo, args, workingDir, opEnv, task.inherited)

	// A batch tool with an outputParser (eslint --format=json, …) emits machine
	// output on stdout while wrappers and the runtime write noise to stderr, so the
	// parser must see the streams apart — a combined capture would hand it a JSON
	// document with prose glued in front of it. formatMode is stdout-as-file-content
	// and must never reach the parser (validation keeps it off batch lint, but a
	// tool could still pair output:stdout with a tool-level parser).
	formatMode := task.OpConfig.Output == config.ToolOutputStdout
	declared := e.parser != nil && task.Tool.OutputParser != nil && !formatMode
	parseMode := e.parser != nil && !formatMode
	procStart := time.Now()
	stdoutBytes, stderrBytes, err := e.runCommandIO(cmd, nil, parseMode)
	procDuration := time.Since(procStart).Milliseconds()

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

	proc := ProcessResult{
		Files: absolutePaths(files, workingDir), State: processState(err), Success: err == nil,
		OutputTail: outputTail(output), DurationMs: procDuration,
	}
	proc.Extraction, proc.ParseError = e.unparsed(task, formatMode)
	if proc.State == ProcessRan {
		proc.ExitCode = new(exitCode)
	}
	if parseMode {
		e.parseFileDiagnostics(ctx, &proc, task, workingDir, stdoutBytes, stderrBytes, exitCode, declared)
		if gateErr := e.applyGate(task, &proc, err == nil); gateErr != nil {
			err = gateErr
			proc.Success = false
		}
		if err != nil && !stoppedByCancellation(err) && len(proc.Diagnostics) == 0 {
			result.UnparsedFailures = append(result.UnparsedFailures, unparsedFailure(exitCode, err, output))
		}
	}
	result.addProcess(proc)

	if err != nil {
		log.Debug("batch execution failed", zap.Int("exitCode", exitCode), zap.Error(err))
		result.Success = false
		result.Error = fmt.Errorf("failed to execute (exit code %d): %w", exitCode, err)
		switch {
		case stoppedByCancellation(err):
			result.Cancelled = true
			result.FailureReason = cancelReason(ctx)
		case proc.ThresholdFailed:
			result.Error = err
			result.FailureReason = FailureReasonThreshold
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
				chunkResults[idx] = unstartedChunk(ctx, task, workingDir, files)
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
				chunkResults[idx] = unstartedChunk(ctx, task, workingDir, files)
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

	mergeChunkResults(ctx, &result, chunks, chunkResults)

	result.recordTiming(startTime)
	log.Debug("executeBatchChunksParallel completed",
		zap.String("toolName", task.ToolName),
		zap.Bool("success", result.Success),
		zap.Int("chunkCount", len(chunks)),
		zap.Int64("durationMs", result.Duration))

	return result
}

// unstartedChunk is the result of a chunk cancelled while it waited for a
// worker: a process that was never spawned, over the files it would have been
// given.
func unstartedChunk(ctx context.Context, task Task, workingDir string, files []string) ExecutionResult {
	result := ExecutionResult{
		ToolName:      task.ToolName,
		Success:       false,
		Error:         errCancelled,
		Cancelled:     true,
		FailureReason: cancelReason(ctx),
	}
	result.addProcess(ProcessResult{Files: absolutePaths(files, workingDir), State: ProcessNotStarted, Extraction: ExtractionNone})
	return result
}

// unparsed is a spawned process's extraction before any parse. A formatter's
// stdout is file content, which no parser reads, and a tool without an
// outputParser has nothing to extract: none, the exit-status rule. A tool that
// declares a parser this executor was not given is another matter — its
// findings were never extracted, so its output proves nothing.
func (e *Executor) unparsed(task Task, formatMode bool) (Extraction, string) {
	if task.Tool.OutputParser == nil || formatMode || e.parser != nil {
		return ExtractionNone, ""
	}
	return ExtractionParserUnavailable, "no output parser is wired"
}

// processState classifies how a process ended from the error its run
// returned. A start refused because the run was already cancelled is a process
// that never started, not one that failed to.
func processState(err error) ProcessState {
	switch {
	case err == nil:
		return ProcessRan
	case errors.Is(err, errStart) && stoppedByCancellation(err):
		return ProcessNotStarted
	case errors.Is(err, errStart):
		return ProcessSetupFailed
	case stoppedByCancellation(err):
		return ProcessCancelled
	default:
		return ProcessRan
	}
}

// mergeChunkResults folds the chunks of one batch task into its result. A task
// whose every failed chunk was cancelled is itself cancelled. One with a chunk
// that failed on its own is a failure, and the files of its cancelled chunks
// count in FilesNotRun: it did not check everything it was given.
func mergeChunkResults(ctx context.Context, result *ExecutionResult, chunks [][]string, chunkResults []ExecutionResult) {
	var outputs []string
	var errs []error
	allCancelled := !result.Success
	notRun := 0
	// The frame names one command and one exit code: the last chunk that failed
	// on its own, as a per-file task names its last failing file.
	failedCommand, failedExit := "", 0
	gatedOnly := true
	for i, chunkResult := range chunkResults {
		if chunkResult.Command != "" {
			result.Command = chunkResult.Command
		}
		if !chunkResult.Success && !chunkResult.IsCancelled() {
			failedCommand, failedExit = chunkResult.Command, chunkResult.ExitCode
			gatedOnly = gatedOnly && chunkResult.FailureReason == FailureReasonThreshold
		}
		if chunkResult.Output != "" {
			outputs = append(outputs, fmt.Sprintf("=== Chunk %d/%d ===\n%s", i+1, len(chunks), chunkResult.Output))
		}
		if chunkResult.Error != nil {
			errs = append(errs, fmt.Errorf("chunk %d: %w", i+1, chunkResult.Error))
		}
		for _, proc := range chunkResult.Processes {
			result.addProcess(proc)
		}
		for _, failure := range chunkResult.UnparsedFailures {
			result.UnparsedFailures = append(result.UnparsedFailures, fmt.Sprintf("chunk %d/%d: %s", i+1, len(chunks), failure))
		}
		if chunkResult.IsCancelled() {
			notRun += len(chunks[i])
		} else if !chunkResult.Success {
			allCancelled = false
		}
	}

	result.Output = strings.Join(outputs, "\n")
	if len(errs) > 0 {
		result.Error = fmt.Errorf("batch execution had %d failures: %v", len(errs), errs)
	}
	if failedCommand != "" {
		result.Command, result.ExitCode = failedCommand, failedExit
	}

	switch {
	case result.Success:
	case allCancelled:
		result.Cancelled = true
		result.FailureReason = cancelReason(ctx)
	default:
		result.FilesNotRun = notRun
		if gatedOnly {
			result.FailureReason = FailureReasonThreshold
		}
	}
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

		// Expanded here, never in expandPathPlaceholders: that helper is shared
		// with env-value expansion, which has no task and so no target. {target}
		// carries the same directory as {cwd} today but differs in intent — it
		// marks what the tool scans, which is what makes arity inferable.
		if strings.Contains(arg, "{target}") {
			if arg == "{target}" {
				result = append(result, projectPath)
				continue
			}
			arg = strings.ReplaceAll(arg, "{target}", projectPath)
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

// absolutePaths undoes makeRelativePaths: the paths a batch process was handed,
// as the absolute, cleaned paths the rest of the core names files by.
func absolutePaths(files []string, baseDir string) []string {
	if len(files) == 0 {
		return nil
	}
	out := make([]string, len(files))
	for i, file := range files {
		out[i] = diagnostic.AbsPath(file, baseDir)
	}
	return out
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
	stop := trackStop(cmd)

	cntSpawn.Add(1)
	spawnSpan := trace.Start(trace.CatExec, "spawn")
	if startErr := cmd.Start(); startErr != nil {
		spawnSpan.EndWith(trace.A("error", startErr.Error()))
		return nil, nil, fmt.Errorf("%w: %w", errStart, startErr)
	}

	err = cmd.Wait()
	spawnSpan.EndWith(
		trace.A("argv0", cmd.Path),
		trace.A("exit", getExitCode(err)),
	)
	if at := stop.requested.Load(); at != 0 {
		killGroupAfterGrace(cmd.Process.Pid, time.Unix(0, at))
	}
	if err != nil && stop.stopped.Load() {
		err = fmt.Errorf("%w: %w", errStopped, err)
	}
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
	// file. The edits are returned for the file's FileResult.
	if err := writeFileAtomic(file, candidate, info.Mode().Perm()); err != nil {
		return nil, fmt.Errorf("write formatted content to %s: %w", file, err)
	}
	return edits, nil
}

// patchPath is the name a patch gives file: relative to the repository root
// with "/", or absolute outside it.
func (e *Executor) patchPath(file string) string {
	rel, err := filepath.Rel(e.rootPath, file)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(file)
	}
	return filepath.ToSlash(rel)
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

// unparsedFailure describes a failed batch process whose output held no
// finding: once another process's findings take the frame's place, this is all
// that is left of it.
func unparsedFailure(exitCode int, err error, output []byte) string {
	label := fmt.Sprintf("exit code %d", exitCode)
	if _, ok := errors.AsType[*exec.ExitError](err); !ok {
		label = err.Error()
	}
	if text := strings.TrimSpace(string(output)); text != "" {
		label += "\n" + text
	}
	return label
}

// fileOutput is what one file of a per-file task printed. label names a file
// whose run failed on its own, and explained marks a label that gives a reason
// no exit code does; a file whose input could not be prepared is failed with
// its error as the text.
type fileOutput struct {
	text      string
	label     string
	failed    bool
	explained bool
}

// joinFileOutputs joins what the files of a per-file task printed. A frame
// names one failing command and exit code, so a failing file's output is
// headed by its label when more than one file failed, or when the failure is
// not an exit code; a file that failed silently is then still listed.
func joinFileOutputs(outputs []fileOutput) string {
	failures := 0
	for _, o := range outputs {
		if o.failed {
			failures++
		}
	}
	parts := make([]string, 0, len(outputs))
	for _, o := range outputs {
		if o.label != "" && (failures > 1 || o.explained) {
			parts = append(parts, o.label+"\n"+o.text)
			continue
		}
		parts = append(parts, o.text)
	}
	return strings.Join(parts, "\n")
}

// failureLabel names a failed file with its exit code, or with the error when
// the failure is not the tool's exit — a formatter's empty output, a command
// that could not start — which reports itself as explained.
func failureLabel(workingDir, file string, exitCode int, err error) (string, bool) {
	name := file
	if rel, relErr := filepath.Rel(workingDir, file); relErr == nil && !strings.HasPrefix(rel, "..") {
		name = rel
	}
	if _, ok := errors.AsType[*exec.ExitError](err); ok {
		return fmt.Sprintf("%s: exit code %d", name, exitCode), false
	}
	return fmt.Sprintf("%s: %v", name, err), true
}
