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
	"github.com/datamitsu/datamitsu/internal/exitcode"
	"github.com/datamitsu/datamitsu/internal/facts"
	"github.com/datamitsu/datamitsu/internal/gitenv"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/managedconfig"
	"github.com/datamitsu/datamitsu/internal/ocibundle"
	"github.com/datamitsu/datamitsu/internal/parsermanager"
	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/report/render"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"
	"github.com/datamitsu/datamitsu/internal/term"
	"github.com/datamitsu/datamitsu/internal/timing"
	"github.com/datamitsu/datamitsu/internal/tooling"
	"github.com/datamitsu/datamitsu/internal/trace"
	"github.com/datamitsu/datamitsu/internal/traverser"
	"github.com/datamitsu/datamitsu/internal/ui"
	"github.com/datamitsu/datamitsu/internal/uievent"

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
	currentTask *ui.Task          // shared file-processing task for the active operation
	activeTasks map[string]string // running tasks: task ID -> directory relative to the git root
)

// toolPlanner is the planning surface used by runSingleOperation (satisfied by *tooling.Planner).
type toolPlanner interface {
	Plan(ctx context.Context, operation config.OperationType, sel tooling.Selection, selectedTools []string) (*tooling.ExecutionPlan, error)
	SeedFiles(files []string)
	GetDetectedProjectTypes() []string
	GetTimings() *timing.Timings
}

// planExecutor is the execution surface used by runSingleOperation (satisfied by *tooling.Executor).
type planExecutor interface {
	SetResultCallback(cb tooling.ResultCallback)
	SetTaskStartCallback(cb tooling.TaskStartCallback)
	SetFileProgressCallback(cb tooling.FileProgressCallback)
	SetParser(parser tooling.DiagnosticParser)
	SetParserModules(parsers config.MapOfParsers)
	SetGate(gate tooling.Gate)
	Execute(ctx context.Context, plan *tooling.ExecutionPlan) ([]tooling.GroupExecutionResult, error)
	TaskDir(task tooling.Task) string
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
	selection     tooling.Selection
	opts          Options
	files         []string
	selectedTools []string
	explainLevel  string
	planner       toolPlanner
	projectCache  *cache.Cache
	executor      planExecutor
	binMgr        toolEnsurer
	timings       *timing.Timings
	// parserMgr owns the WASM output-parser runtime (compile-once, instantiate
	// per parse). nil when no parsers are declared; Closed in shutdown to
	// release the shared runtime.
	parserMgr *parsermanager.Manager
	// parseProblems collects what the parsers could not parse, reported once
	// per run; nil when no parser is wired.
	parseProblems *parseProblems
	// ignoredFailOn collects the tools whose threshold was not the default but
	// whose output a module that predates the severity contract parsed, for
	// one warning per run; nil when no parser is wired.
	ignoredFailOn *toolSet
	// nameWidth is the widest configured tool name, computed once so every
	// operation's result block (fix, lint, …) aligns on the same columns.
	nameWidth int
	// failOnSkip makes the run exit non-zero when a tool was skipped because its
	// binary is unavailable for this host (intentional config skips never fail).
	failOnSkip bool
	// platformSkipped collects the names of tools skipped for an unsupported
	// platform across all operations (deduped), to drive failOnSkip after the run.
	platformSkipped map[string]struct{}
	// narrowed collects why the run did not answer completely: tools dropped for
	// narrowing, and tasks that covered only part of their unit.
	narrowed map[string]struct{}
	// failFast stops the run at the first failing tool; without it every group,
	// task, file and operation runs to the end.
	failFast bool
	// afterFailedFix marks the lint operation of a check that runs although its
	// fix failed, which only keep-going allows.
	afterFailedFix bool
	// summaries holds what each operation that ran reported, for check's
	// closing line and the run-level done event.
	summaries []opSummary
	// fileScoped records --file-scoped, which the report states.
	fileScoped bool
	// report records the run for its reports; nil when it writes none.
	report *report.Accumulator
	// secrets are the values reports and diagnostic events mask, collected
	// once (secretValues).
	secrets     []string
	secretsOnce sync.Once
	// annotator anchors each parsed process's findings for the report, in the
	// gate hook; nil when nothing reads them.
	annotator *report.Annotator
	// startedAt stamps the report: SOURCE_DATE_EPOCH or the clock at the
	// start of the run.
	startedAt time.Time
}

func initSharedContext(
	args []string,
	explainMode string,
	fileScoped bool,
	selectedToolsFlag string,
	failOnSkip bool,
	opts Options,
	loadConfigFunc func() (*config.Config, string, error),
) (*sharedContext, error) {
	ctx := context.Background()
	sc := &sharedContext{
		timings:         timing.New(),
		opts:            opts,
		failOnSkip:      failOnSkip,
		platformSkipped: make(map[string]struct{}),
		narrowed:        make(map[string]struct{}),
		failFast:        resolveFailFast(opts.FailFast),
		fileScoped:      fileScoped,
		startedAt:       now(),
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

	// Caller mistakes are refused before anything is loaded: they exit 2 whether
	// or not the directory is a repository and the configuration loads.
	if err := opts.validate(); err != nil {
		return nil, err
	}
	// Rejected before anything runs: --tools drops the skip entries of unselected
	// tools before they can be observed, so the assertion would be trivially true
	// over a debug subset. Failing after the run would waste it.
	if opts.RequireCoverage != "" && len(sc.selectedTools) > 0 {
		return nil, errRequireCoverageWithTools
	}
	if explainMode != "" {
		switch strings.ToLower(explainMode) {
		case "summary", "s":
			sc.explainLevel = "summary"
		case "detailed", "detail", "d":
			sc.explainLevel = "detailed"
		case "json", "j":
			sc.explainLevel = "json"
		default:
			return nil, exitcode.UsageErrorf("invalid --explain value: %s (must be summary, detailed, or json)", explainMode)
		}
		if len(opts.Reports) > 0 {
			return nil, errReportWithExplain
		}
	}

	// Get cwd
	var err error
	sc.cwdPath, err = os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get cwd: %w", err)
	}

	// Get root path.
	//
	// facts.GetGitRoot, not traverser.GetGitRoot: both answer the same question
	// — the topmost repository in a submodule hierarchy — but facts memoizes the
	// answer per cwd for the process and resolves it from the filesystem,
	// forking git only for layouts its walk refuses to decide. traverser's
	// resolver always forks, two processes per hierarchy level, and the config
	// load a few lines below has already computed the identical value.
	//
	// It reads os.Getwd() itself rather than taking a cwd; sc.cwdPath came from
	// the same call above and nothing changes directory in between.
	rootSpan := trace.Start(trace.CatCLI, "runner.gitRoot")
	sc.rootPath, err = facts.GetGitRoot(ctx)
	rootSpan.EndWith(trace.A("root", sc.rootPath))
	if err != nil {
		return nil, fmt.Errorf("failed to get git root: %w", err)
	}
	if err := refuseNarrowedReports(opts, tooling.NewSelection(sc.rootPath, sc.cwdPath, args, fileScoped), fileScoped, sc.selectedTools); err != nil {
		return nil, err
	}

	// Load configuration
	func() {
		defer sc.timings.Start("Load configuration")()
		defer trace.Start(trace.CatConfig, "runner.loadConfig").End()
		sc.cfg, _, err = loadConfigFunc()
	}()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
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

	sc.selection = tooling.NewSelection(sc.rootPath, sc.cwdPath, sc.files, fileScoped)

	log.Debug("selection",
		zap.String("mode", sc.selection.String()),
		zap.Strings("files", sc.files))

	// Create planner
	planner := tooling.NewPlanner(sc.rootPath, sc.cwdPath, nil, sc.cfg.Tools, sc.cfg.ProjectTypes, sc.cfg.IgnoreRules)
	planner.SetWidenPolicy(sc.cfg.Execution, config.WidenTo(sc.opts.WidenTo))
	sc.planner = planner

	// Create cache
	cacheDir := env.GetCachePath()
	cacheSpan := trace.Start(trace.CatCache, "createCache")
	projectCache, err := createCache(cacheDir, sc.rootPath, *sc.cfg, sc.selectedTools)
	cacheSpan.End()
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
	sc.executor = tooling.NewExecutor(sc.rootPath, false, sc.failFast, binMgr, sc.projectCache)
	sc.executor.SetParserModules(sc.cfg.Parsers)
	// Wire output-parsing whenever parsers are declared. --no-parse only changes
	// what a failure frame shows: what the run records must not depend on it.
	if len(sc.cfg.Parsers) > 0 {
		sc.parserMgr = parsermanager.New(sc.cfg.Parsers)
		sc.parseProblems = newParseProblems()
		sc.executor.SetParser(newDiagnosticParser(sc.parserMgr, sc.parseProblems))
		sc.ignoredFailOn = &toolSet{}
	}
	sc.startReport()
	if sc.parserMgr != nil {
		sc.executor.SetGate(sc.gate())
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

func resolveFailFast(flag *bool) bool {
	if flag != nil {
		return *flag
	}
	eff, err := runtimeconfig.Get()
	if err != nil {
		eff = runtimeconfig.Compute()
	}
	return eff.FailFast
}

func (sc *sharedContext) shutdown() {
	if sc.projectCache != nil {
		sc.projectCache.Shutdown()
	}
	if sc.parserMgr != nil {
		// Release the shared WASM runtime and its compiled modules. Use a fresh
		// context: shutdown may run after the operation's ctx is cancelled.
		_ = sc.parserMgr.Close(context.Background())
	}
}

// plannedParserModules returns the distinct WASM parser modules referenced by the
// tools in plan (via their outputParser), for prewarming their one-time
// compilation before execution.
func plannedParserModules(plan *tooling.ExecutionPlan) []string {
	seen := make(map[string]bool)
	var mods []string
	for _, group := range plan.Groups {
		for _, task := range group.Tasks {
			op := task.Tool.OutputParser
			if op == nil || op.Module == "" || seen[op.Module] {
				continue
			}
			seen[op.Module] = true
			mods = append(mods, op.Module)
		}
	}
	return mods
}

// runSingleOperation executes one operation (fix, lint, etc.) using a pre-initialized shared context
func runSingleOperation(ctx context.Context, sc *sharedContext, operation config.OperationType) (retErr error) {
	defer trace.Start(trace.CatCLI, "runSingleOperation").EndWith(trace.A("operation", string(operation)))

	// Create execution plan
	plan, err := sc.planner.Plan(ctx, operation, sc.selection, sc.selectedTools)
	if err != nil {
		return fmt.Errorf("failed to create execution plan: %w", err)
	}

	// Get detected project types from planner cache
	if plan != nil {
		plan.ConfigName = sc.cfg.DisplayName()
		plan.FailOn = config.Severity(sc.opts.FailOn)
	}
	projectTypes := sc.planner.GetDetectedProjectTypes()

	// Explain mode (json/summary/detailed): print the formatted plan, which now
	// lists skipped tools too, and stop.
	if sc.explainLevel != "" {
		// Coverage is decided entirely at plan time, so --require-coverage can be
		// asserted without running or downloading anything — a cheap CI gate.
		// --fail-on-skip deliberately stays inert here: it is about a host lacking
		// a binary, which only matters if something was going to run.
		sc.recordNarrowingSkips(plan.Skipped)
		sc.recordCoverage(plan)

		output := formatExecutionPlan(plan, sc.rootPath, sc.cwdPath, operation, sc.explainLevel)
		fmt.Println(output)
		return nil
	}

	opRecord := sc.beginReportOperation(operation, plan)
	opDuration := int64(0)
	defer func() { opRecord.End(retErr == nil, opDuration) }()

	// Nothing to run (no project types, or no applicable tasks). Still surface
	// any explicit skips — recording unsupported-platform ones for --fail-on-skip
	// — instead of leaving them invisible.
	if len(projectTypes) == 0 || len(plan.Groups) == 0 {
		sc.recordOp(opSummary{op: operation, skipped: len(plan.Skipped)})
		if len(plan.Skipped) > 0 {
			renderSkipOnlyBlock(string(operation), sc.targetLine(), plan.Skipped, sc.nameWidth, sc.footerNote(operation))
			sc.recordSkips(plan.Skipped)
			sc.recordCoverage(plan)
			return nil
		}
		if !ui.Quiet() {
			msg := "ℹ️  No applicable tools found"
			if len(projectTypes) == 0 {
				msg = "⚠️  No project types detected"
			}
			if note := sc.footerNote(operation); note != "" {
				msg += " · " + note
			}
			fmt.Println(msg)
		}
		return nil
	}

	if refs := plan.ManagedConfigRefs(); len(refs) > 0 && sc.cfg != nil {
		if err := managedconfig.CheckConfigFiles(sc.rootPath, sc.cfg.ManagedConfigs, refs); err != nil {
			return err
		}
	}

	// Seed the store from the declared OCI bundle before anything reads it
	// (demand-driven: only the layers of the planned tools), so both the parser
	// prewarm below and EnsureTools later hit seeded content instead of the
	// network. Seeding after the prewarm meant a bundled parser module was
	// fetched from its URL on every cold store — and under DATAMITSU_OFFLINE
	// that fetch is refused, so the airgapped run silently lost its parsers and
	// fell back to raw tool output.
	if sc.binMgr != nil {
		seedSpan := trace.Start(trace.CatInstall, "ocibundle.AutoSeed")
		err := ocibundle.AutoSeed(ctx, sc.cfg, plan.GetAppNames(), nil)
		seedSpan.EndWith(trace.A("apps", len(plan.GetAppNames())))
		if err != nil {
			return fmt.Errorf("failed to seed store from oci bundle: %w", err)
		}
	}

	// Compile the WASM parser modules this plan will use now, once, off the
	// per-file execution path. Best-effort: a failure falls back to lazy
	// compile-on-first-parse inside the executor.
	//
	// Started in the background rather than awaited. Compiling the module takes
	// ~80 ms, and nothing can parse output before a tool has produced any, so
	// blocking here put that entirely on the critical path — every run paid it
	// before the first tool started. Manager.compiledFor collapses concurrent callers onto one compilation, so a
	// parse that arrives before this finishes joins the in-flight compilation
	// instead of starting a second one, and one that arrives after finds it
	// cached; either way the prewarm remains what it always was, an
	// optimisation with a lazy fallback.
	if sc.parserMgr != nil {
		if mods := plannedParserModules(plan); len(mods) > 0 {
			prewarmDone := make(chan struct{})
			defer func() { <-prewarmDone }()
			go func() {
				defer close(prewarmDone)
				if err := sc.parserMgr.Prewarm(ctx, mods); err != nil {
					log.Debug("parser prewarm failed; compiling lazily", zap.Error(err))
				}
			}()
		}
	}

	// Correlation id for every typed event of THIS operation (phase start/done,
	// and the parent of each tool_run/chunk op id). Generated once here, captured
	// by the executor callbacks below.
	runOpID := uievent.NextOpID("run")
	ui.Emit(uievent.Event{
		Type:   uievent.TypePhase,
		OpID:   runOpID,
		Status: uievent.StatusStart,
		Op:     string(operation),
	})

	// Open the operation block: bold bracket header + dimmed project types. The
	// matched-tool list is omitted — the per-tool results below cover it.
	shortTypes := make([]string, len(projectTypes))
	for i, pt := range projectTypes {
		shortTypes[i] = shortProjectType(pt)
	}
	if !ui.Quiet() {
		fmt.Println()
		fmt.Println(phaseTop(string(operation)))
		if line := sc.targetLine(); line != "" {
			fmt.Println(line)
		}
		fmt.Println(clr.Faint("┃ " + strings.Join(shortTypes, " · ")))
	}

	// Calculate total file processing count for progress bar
	totalFileProcessing := 0
	for _, group := range plan.Groups {
		for _, task := range group.Tasks {
			if config.RunsPerFile(task.OpConfig, len(task.Files)) {
				// One process per file: count each file
				totalFileProcessing += len(task.Files)
			} else {
				// One process for the whole task: count as 1 unit
				totalFileProcessing++
			}
		}
	}

	// Track progress
	progressTracker := make(map[string]*toolExecutionGroup)
	activeTasks = make(map[string]string)

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
	sc.executor.SetTaskStartCallback(func(taskID, toolName, relativeDir string) {
		ui.Emit(uievent.Event{
			Type:   uievent.TypeToolRun,
			OpID:   toolOpID(runOpID, taskID),
			Status: uievent.StatusStart,
			Tool:   toolName,
			Dir:    relativeDir,
		})

		progressMu.Lock()
		activeTasks[taskID] = relativeDir
		t := ensureTask()
		progressMu.Unlock()

		t.SetLabel(formatToolWithDir(toolName, relativeDir))
	})

	// Set up file progress callback
	sc.executor.SetFileProgressCallback(func(taskID, toolName string, fileIndex, totalFiles int, success bool) {
		status := "✓"
		if !success {
			status = "✗"
		}

		progressMu.Lock()
		t := ensureTask()
		dir := activeTasks[taskID]
		progressMu.Unlock()

		ui.Emit(uievent.Event{
			Type:    uievent.TypeChunk,
			OpID:    toolOpID(runOpID, taskID),
			Status:  chunkStatus(fileIndex, totalFiles),
			Tool:    toolName,
			Dir:     dir,
			Index:   fileIndex,
			Total:   totalFiles,
			Success: new(success),
		})

		if dir != "" {
			t.SetLabel(fmt.Sprintf("%s %s (%s) [%d/%d]", status, toolName, dir, fileIndex, totalFiles))
		} else {
			t.SetLabel(fmt.Sprintf("%s %s [%d/%d]", status, toolName, fileIndex, totalFiles))
		}
		t.Increment()
	})

	// Tasks the run stopped before they finished: cancelled after they started,
	// never started, or never reached at all.
	var stopped []stoppedTask

	// Set up progress tracking callback
	sc.executor.SetResultCallback(func(result tooling.ExecutionResult) {
		// A cancelled task is not a failure: it ends its chain with a skip event
		// and is listed apart from the results, never as a failed run.
		finished := opRecord.AddTask(result)
		if result.IsCancelled() {
			task := stoppedFromResult(result)
			stopped = append(stopped, task)
			emitStopped(runOpID, task)
			opRecord.Stopped(task.cancel())
		} else {
			opID := toolOpID(runOpID, result.TaskID)
			var levels levelCounts
			for _, d := range result.Diagnostics {
				levels.add(d.Severity)
			}
			ui.Emit(uievent.Event{
				Type:            uievent.TypeToolRun,
				OpID:            opID,
				Status:          doneStatus(result.Success),
				Tool:            result.ToolName,
				Dir:             result.RelativeDir,
				Success:         new(result.Success),
				DurationMs:      result.Duration,
				FindingsError:   new(levels[0]),
				FindingsWarning: new(levels[1]),
				FindingsInfo:    new(levels[2]),
				FindingsHint:    new(levels[3]),
				Cached:          new(result.Cached),
			})
			if !result.Success {
				ui.Emit(uievent.Event{
					Type: uievent.TypeError,
					OpID: opID,
					Tool: result.ToolName,
					Dir:  result.RelativeDir,
					Msg:  resultErrorMessage(result),
				})
			}
		}

		if group, exists := progressTracker[result.ToolName]; exists {
			group.totalRuns++
			if result.Success {
				group.succeededRuns++
			} else {
				group.failedRuns++
			}

			progressMu.Lock()
			delete(activeTasks, result.TaskID)
			progressMu.Unlock()
		}
		sc.emitDiagnostics(runOpID, finished)
	})

	// Pre-install every tool the plan needs once, before parallel per-file
	// execution. This closes the check-then-download install race (multiple
	// per-file tasks installing the same binary concurrently). Explain/dry-run
	// modes return earlier and never reach this point, so no installs happen
	// during planning-only runs.
	if sc.binMgr != nil {
		// The store was already seeded from the bundle before the parser
		// prewarm above, so these stat checks hit seeded content.
		ensureSpan := trace.Start(trace.CatInstall, "EnsureTools")
		err := sc.binMgr.EnsureTools(ctx, plan.GetAppNames())
		ensureSpan.EndWith(trace.A("apps", len(plan.GetAppNames())))
		if err != nil {
			finalizeProgress()
			if !ui.Quiet() {
				fmt.Println(ui.RuleLine("┗", "setup failed", clr.Red("setup failed")))
			}
			ui.Emit(uievent.Event{
				Type:   uievent.TypeDone,
				OpID:   runOpID,
				Status: uievent.StatusFail,
				Op:     string(operation),
			})
			return fmt.Errorf("failed to pre-install tools: %w", err)
		}
	}

	execSpan := trace.Start(trace.CatExec, "executePlan")
	results, execErr := sc.executor.Execute(ctx, plan)
	execSpan.EndWith(trace.A("groups", len(plan.Groups)))
	// Finalize progress before printing any summaries/errors to avoid interleaved output.
	finalizeProgress()

	cause := stopFailFast
	if interruption(ctx) != nil {
		cause = stopInterrupted
	}
	for _, task := range unreachedTasks(plan, results, sc.executor.TaskDir, cause) {
		stopped = append(stopped, task)
		emitStopped(runOpID, task)
		opRecord.Stopped(task.cancel())
	}
	sc.emitDiagnostics(runOpID, opRecord.Flush())
	// An interruption can stop a run between two groups that passed, with no
	// failed result to show for it; the tasks it stopped still fail the
	// operation.
	incomplete := len(stopped) > 0

	// Cache hit/miss feeds the footer.
	cacheHits, cacheMisses := 0, 0
	if sc.projectCache != nil {
		stats := sc.projectCache.GetStats()
		cacheHits, cacheMisses = int(stats.Hits), int(stats.Misses)
	}

	// Calculate total wall-clock time and failure state.
	hasFailures := execErr != nil || incomplete
	var totalWallClockTime int64
	for _, groupResult := range results {
		totalWallClockTime += groupResult.WallClockDuration
		if !groupResult.Success {
			hasFailures = true
		}
	}

	// Close the operation block: per-tool body lines, skipped-tool lines, then the
	// summary footer (the footer doubles as the "complete" marker, so no separate
	// line is printed). The print helpers self-suppress in JSON-L mode.
	toolGroups := groupResultsByTool(results)
	if len(toolGroups) > 0 || len(plan.Skipped) > 0 || len(stopped) > 0 {
		printGroupedResults(toolGroups, sc.nameWidth, env.IsTimingsEnabled())
		printStoppedTasks(stopped, sc.nameWidth)
		printUnrunFiles(results, sc.rootPath, sc.nameWidth, cause)
		printSkippedTools(plan.Skipped, sc.nameWidth)
		printOperationFooter(toolGroups, totalWallClockTime, cacheHits, cacheMisses, len(plan.Skipped), len(stopped), sc.footerNote(operation))
	}

	// Typed completion event for this operation (the JSON-L twin of the footer).
	totalRuns := 0
	failedTools := 0
	for _, group := range toolGroups {
		totalRuns += group.totalRuns
		if group.failedRuns > 0 {
			failedTools++
		}
	}
	ui.Emit(uievent.Event{
		Type:       uievent.TypeDone,
		OpID:       runOpID,
		Status:     doneStatus(!hasFailures),
		Op:         string(operation),
		Tools:      len(toolGroups),
		Runs:       totalRuns,
		Failed:     failedTools,
		Skipped:    len(plan.Skipped),
		Cancelled:  nonZero(len(stopped)),
		DurationMs: totalWallClockTime,
	})
	opDuration = totalWallClockTime
	sc.recordOp(opSummary{
		op:         operation,
		tools:      len(toolGroups),
		runs:       totalRuns,
		failed:     failedTools,
		skipped:    len(plan.Skipped),
		cancelled:  len(stopped),
		partial:    partialTasks(results),
		durationMs: totalWallClockTime,
	})

	sc.recordSkips(plan.Skipped)
	sc.recordCoverage(plan)

	if hasFailures {
		return errors.New("operation failed")
	}

	return nil
}

// reportParseProblems warns about what the parsers could not parse: each module
// that did not load, each parser key its module does not list, and each tool
// whose output failed to parse, once per run however many invocations and
// operations hit it; and, in one line, the tools whose failOn threshold a
// module that predates the severity contract left unenforced. It runs once
// every operation has, so a warning names every tool the problem reached.
func (sc *sharedContext) reportParseProblems() {
	if sc.parseProblems == nil {
		return
	}
	for _, msg := range sc.parseProblems.pending() {
		logger.Logger.Warn(msg)
	}
	if tools := sc.ignoredFailOn.names(); len(tools) > 0 {
		logger.Logger.Warn("failOn ignored for " + strings.Join(tools, ", ") +
			": parser module predates the severity contract")
	}
}

// severityContract reports whether module's levels come only from what its
// tools printed; a module that cannot be described does not, and its failure
// is reported with the parse problems.
func (sc *sharedContext) severityContract(module string) bool {
	ok, err := sc.parserMgr.SeverityContract(context.Background(), module)
	return err == nil && ok
}

// recordSkips accumulates unsupported-platform skips (deduped by tool name) so
// --fail-on-skip can fail the run afterwards. Intentional config skips are not
// recorded — they never fail the run.
func (sc *sharedContext) recordSkips(skipped []tooling.SkippedTool) {
	if sc.narrowed == nil {
		sc.narrowed = make(map[string]struct{})
	}
	for _, s := range skipped {
		switch s.Reason {
		case tooling.SkipReasonUnsupportedPlatform:
			sc.platformSkipped[s.ToolName] = struct{}{}
			sc.narrowed[s.ToolName] = struct{}{}
		case tooling.SkipReasonNotNarrowable:
			sc.narrowed[s.ToolName] = struct{}{}
		case tooling.SkipReasonConfig:
			// Declared, documented, and permanent: a repository that opts a tool
			// out has not given an incomplete answer, it has given its answer.
		}
	}
}

// recordNarrowingSkips records what bears on coverage, leaving --fail-on-skip
// untouched. Explain mode uses it: --fail-on-skip is about a host lacking a
// binary, which only matters if something was going to run, and
// TestExplainFlagMatrix pins it as inert there.
//
// Coverage is a different question, so this records the same reasons the
// executing path does. A tool with no binary for this host will not answer,
// whether or not the plan is executed; recording only not-narrowable let
// `--explain --require-coverage=unit` pass where the real run failed, and two
// answers to one assertion make it worthless as a CI gate.
func (sc *sharedContext) recordNarrowingSkips(skipped []tooling.SkippedTool) {
	if sc.narrowed == nil {
		sc.narrowed = make(map[string]struct{})
	}
	for _, s := range skipped {
		switch s.Reason {
		case tooling.SkipReasonNotNarrowable, tooling.SkipReasonUnsupportedPlatform:
			sc.narrowed[s.ToolName] = struct{}{}
		case tooling.SkipReasonConfig:
			// Declared, documented and permanent: a repository that opts a tool
			// out has not given an incomplete answer, it has given its answer.
		}
	}
}

// recordCoverage notes tasks that ran over only part of their unit.
func (sc *sharedContext) recordCoverage(plan *tooling.ExecutionPlan) {
	for _, group := range plan.Groups {
		for _, task := range group.Tasks {
			if task.Coverage == tooling.CoveragePartial {
				sc.narrowed[task.ToolName] = struct{}{}
			}
		}
	}
}

// coverageFailure enforces --require-coverage.
//
// "unit" asserts every unit the run touched was fully analysed; "repo" adds that
// the run targeted the repository at all. The second clause is not a formality:
// per-task coverage is stamped on the units that ran, so without it
// `check --require-coverage=repo pkg/x.ts` would pass having checked one unit of
// nine — there is no denominator anywhere else.
func (sc *sharedContext) coverageFailure() error {
	level := config.WidenTo(sc.opts.RequireCoverage)
	if level == "" {
		return nil
	}

	var reasons []string
	if len(sc.narrowed) > 0 {
		names := make([]string, 0, len(sc.narrowed))
		for n := range sc.narrowed {
			names = append(names, n)
		}
		sort.Strings(names)
		reasons = append(reasons, fmt.Sprintf("%d tool(s) did not answer completely: %s",
			len(names), strings.Join(names, ", ")))
	}
	if level == config.WidenToRepo && sc.selection.Mode != tooling.SelectionAll {
		reasons = append(reasons, fmt.Sprintf("the run targeted %s, not the whole repository", sc.selection))
	}

	if len(reasons) == 0 {
		return nil
	}
	return exitcode.CoverageErrorf("--require-coverage=%s: %s", level, strings.Join(reasons, "; "))
}

// errReportWithExplain rejects a report of a run that runs nothing: --explain
// prints the plan and stops, so the report asked for could never be written.
var errReportWithExplain = exitcode.UsageError{Err: errors.New(
	"--report cannot be combined with --explain: a plan runs nothing to report")}

// errRequireCoverageWithTools rejects a combination that cannot mean anything:
// --tools drops the skip entries of unselected tools before anything can observe
// them, so the assertion would be trivially true over a debug subset.
var errRequireCoverageWithTools = exitcode.UsageError{Err: errors.New(
	"--require-coverage cannot be combined with --tools: the assertion would only cover the selected subset")}

// targetLine announces that the run covers less than the repository, so a green
// result is not mistaken for a full one. Empty for a whole-repository run, which
// is the common case and needs no annotation.
func (sc *sharedContext) targetLine() string {
	switch sc.selection.Mode {
	case tooling.SelectionAll:
		return ""
	case tooling.SelectionEmpty:
		return clr.Faint("┃ ◑ target: nothing staged · narrowed run")
	case tooling.SelectionPaths:
		noun := "files"
		if len(sc.selection.Paths) == 1 {
			noun = "file"
		}
		names := relativeToRoot(sc.selection.Paths, sc.rootPath)
		shown := strings.Join(names, " ")
		if len(names) > 3 {
			shown = strings.Join(names[:3], " ") + fmt.Sprintf(" +%d more", len(names)-3)
		}
		return clr.Faint(fmt.Sprintf("┃ ◑ target: %d %s · %s · narrowed run",
			len(names), noun, shown))
	case tooling.SelectionSubtree:
		dir := sc.selection.Dir
		if rel, err := filepath.Rel(sc.rootPath, dir); err == nil {
			dir = rel
		}
		return clr.Faint(fmt.Sprintf("┃ ◑ target: %s · narrowed run", dir))
	}
	return ""
}

// relativeToRoot renders paths relative to the git root so the line stays
// readable and stable regardless of where the repository lives.
func relativeToRoot(paths []string, root string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if rel, err := filepath.Rel(root, p); err == nil {
			out = append(out, rel)
			continue
		}
		out = append(out, p)
	}
	return out
}

// printSkippedTools renders faint "┃ ⊘ name   skipped (reason)" body lines,
// aligned to nameWidth like the per-tool result rows. No-op for an empty list.
func printSkippedTools(skipped []tooling.SkippedTool, nameWidth int) {
	if ui.Quiet() {
		return
	}
	for _, s := range skipped {
		pad := max(nameWidth-utf8.RuneCountInString(s.ToolName), 0) + 2
		line := clr.Faint("┃ ⊘ ") + clr.Faint(s.ToolName) +
			strings.Repeat(" ", pad) + clr.Faint("skipped ("+s.ReasonText()+")")
		fmt.Println(line)
	}
}

// renderSkipOnlyBlock prints a minimal operation block containing only skipped
// tools, used when planning produced skips but nothing runnable.
func renderSkipOnlyBlock(operation, targetLine string, skipped []tooling.SkippedTool, nameWidth int, note string) {
	if ui.Quiet() {
		return
	}
	fmt.Println()
	fmt.Println(phaseTop(operation))
	if targetLine != "" {
		fmt.Println(targetLine)
	}
	fmt.Println(clr.Faint("┃"))
	printSkippedTools(skipped, nameWidth)
	printOperationFooter(nil, 0, 0, 0, len(skipped), 0, note)
}

func (sc *sharedContext) footerNote(operation config.OperationType) string {
	if operation == config.OpLint && sc.afterFailedFix {
		return "lint ran after a failed fix"
	}
	return ""
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
// (config, git root, file listing, planner, cache, executor). Under fail-fast
// an operation that fails stops the ones after it; otherwise every operation
// runs and the first failure is returned.
func RunSequential(
	operations []config.OperationType,
	args []string,
	explainMode string,
	fileScoped bool,
	selectedToolsFlag string,
	failOnSkip bool,
	opts Options,
	loadConfigFunc func() (*config.Config, string, error),
) error {
	return runSequential(operations, args, explainMode, fileScoped, selectedToolsFlag, loadConfigFunc, commandName(operations), failOnSkip, opts)
}

// RunContinuation runs a single operation as a continuation of another command's
// output (e.g. config reconcile's post-fix). It reuses the banner already shown by the
// caller instead of printing a second one.
func RunContinuation(
	operation config.OperationType,
	args []string,
	explainMode string,
	fileScoped bool,
	selectedToolsFlag string,
	loadConfigFunc func() (*config.Config, string, error),
) error {
	// Continuations (e.g. config reconcile's post-fix) never harden on skips,
	// and belong to their command: no banner, no run-level report.
	return runSequential([]config.OperationType{operation}, args, explainMode, fileScoped, selectedToolsFlag, loadConfigFunc, "", false, Options{})
}

func runSequential(
	operations []config.OperationType,
	args []string,
	explainMode string,
	fileScoped bool,
	selectedToolsFlag string,
	loadConfigFunc func() (*config.Config, string, error),
	command string,
	failOnSkip bool,
	opts Options,
) error {
	showBanner := command != ""
	started := time.Now()
	sc, err := initSharedContext(args, explainMode, fileScoped, selectedToolsFlag, failOnSkip, opts, loadConfigFunc)
	if err != nil {
		// A run that could not start — no git root, a config that does not load
		// — still ends its event stream with a failed, incomplete summary. A
		// usage error is not a run: it is refused before one begins.
		if _, usage := errors.AsType[exitcode.UsageError](err); command != "" && explainMode == "" && !usage {
			elapsedMs := time.Since(started).Milliseconds()
			if len(operations) > 1 {
				(&sharedContext{}).printRunClosing(command, operations, elapsedMs)
			}
			omitReports(opts.Reports, err)
			(&sharedContext{}).emitRunDone(command, operations, elapsedMs, false)
		}
		return err
	}
	defer func() {
		// Timing reports are human output (bare fmt). Suppress in JSON-L mode so
		// DATAMITSU_TIMINGS doesn't leak a non-JSON block onto the clean streams.
		if !ui.Quiet() {
			sc.timings.Print()
			sc.planner.GetTimings().Print()
		}
		sc.shutdown()
	}()

	// Branded banner once at the top (skipped in explain/json so that output
	// stays clean/machine-readable, and when running as a continuation).
	if showBanner && sc.explainLevel == "" {
		ui.Current().Banner(ldflags.PackageName, ldflags.Version)
	}

	ctx, stopInterrupt := notifyInterrupt(context.Background())
	defer stopInterrupt()

	opErr := sc.runOperations(ctx, operations)
	sc.reportParseProblems()

	if sc.explainLevel != "" {
		return sc.outcome(ctx, opErr)
	}
	elapsedMs := sc.timings.Elapsed().Milliseconds()
	if len(operations) > 1 {
		sc.printRunClosing(command, operations, elapsedMs)
	}
	err = sc.finishReports(operations, sc.outcome(ctx, opErr))
	if command != "" {
		sc.emitRunDone(command, operations, elapsedMs, err == nil)
	}
	return streamOutcome(err)
}

// runOperations walks the repository once, runs the bundled checks, then the
// operations, and returns the first error. A failed walk or bundled check stops
// the run in both modes: it is a setup error, not a tool failure. The caller
// reports the run whatever this returns.
func (sc *sharedContext) runOperations(ctx context.Context, operations []config.OperationType) error {
	hasFix := slices.Contains(operations, config.OpFix)

	// One walk, three consumers: bundled fix, bundled lint, and the planner.
	//
	// All three want the same gitignore-aware list of the same tree, and the
	// planner used to walk for it again a few milliseconds later. Their agreement
	// is not just a saving — buildIgnoreMatcher validates the .datamitsuignore
	// files this list yields and the planner applies their rules, so a divergence
	// would mean linting one set of rules and enforcing another.
	allFiles, err := traverser.FindFilesFromPath(ctx, sc.rootPath, sc.rootPath)
	if err != nil {
		return fmt.Errorf("scanning %s: %w", sc.rootPath, err)
	}
	ignoreFiles := bundled.IgnoreFilesIn(allFiles)
	sc.planner.SeedFiles(allFiles)

	if hasFix && sc.explainLevel == "" {
		if err := bundled.RunFix(sc.rootPath, ignoreFiles); err != nil {
			return err
		}
	}
	if lintErr := bundled.RunLint(sc.rootPath, sc.cfg.Tools, ignoreFiles); lintErr != nil {
		if slices.Contains(operations, config.OpLint) {
			return lintErr
		}
		log.Warn("bundled lint error (non-lint mode, continuing)", zap.Error(lintErr))
	}

	// Under keep-going a later operation can fail for a different reason than
	// an earlier one — lint's install after fix's tool failure — and that
	// reason must not be lost behind the first.
	var errs []error
	for _, op := range operations {
		if ctx.Err() != nil || (len(errs) > 0 && sc.failFast) {
			break
		}
		sc.afterFailedFix = op == config.OpLint && len(errs) > 0
		err := runSingleOperation(ctx, sc, op)
		if err != nil && !slices.ContainsFunc(errs, func(e error) bool { return e.Error() == err.Error() }) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// outcome picks the error a run returns. An interruption wins, because the run
// did not finish; then a tool failure (exit 1); then what the run did not cover
// (exit 4): --fail-on-skip and --require-coverage, both reported, in that order,
// when both fail. Only unsupported-platform skips count for --fail-on-skip;
// intentional config skips (skip: true) never fail the run.
func (sc *sharedContext) outcome(ctx context.Context, opErr error) error {
	if err := interruption(ctx); err != nil {
		return err
	}
	var incomplete []error
	if err := sc.skipFailure(); err != nil {
		incomplete = append(incomplete, err)
	}
	if err := sc.coverageFailure(); err != nil {
		incomplete = append(incomplete, err)
	}
	switch {
	case opErr != nil && len(incomplete) > 0:
		// The tool failure decides the exit code; the assertions that also
		// failed are still reported, but not wrapped, so their code cannot win.
		msgs := make([]string, 0, len(incomplete))
		for _, err := range incomplete {
			msgs = append(msgs, err.Error())
		}
		return fmt.Errorf("%w\n%s", opErr, strings.Join(msgs, "\n"))
	case opErr != nil:
		return opErr
	case len(incomplete) > 1:
		return exitcode.CoverageError{Err: errors.Join(incomplete...)}
	case len(incomplete) == 1:
		return incomplete[0]
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
	return exitcode.CoverageErrorf("--fail-on-skip: %d tool(s) have no binary for this host: %s",
		len(names), strings.Join(names, ", "))
}

// Options carries the run-shaping flags. It exists because the entry points had
// grown to seven positional parameters and the coverage work adds two more.
type Options struct {
	// WidenTo overrides how far the core may widen beyond the selection, in
	// either direction. Empty means "no override".
	WidenTo string
	// RequireCoverage asserts the run answered completely: "unit" for every unit
	// it touched, "repo" for the repository. Empty disables the assertion.
	RequireCoverage string
	// FailFast overrides whether the first failing tool stops the run; nil defers
	// to DATAMITSU_FAIL_FAST and the default.
	FailFast *bool
	// FailOn raises every operation's failOn to this level (a config.Severity
	// name); empty raises nothing. It never lowers an operation's own.
	FailOn string
	// Reports are written once the last operation has ended, whether or not
	// its tools failed.
	Reports []render.Spec
	// AllowPartial writes a report that lists findings for a narrowed run
	// instead of refusing the run.
	AllowPartial bool
	// AllDiagnostics makes the JSON-L stream carry every finding as a
	// diagnostic event, not only those at or above failOn.
	AllDiagnostics bool
}

// validate rejects unknown flag values. Rank() reads an unvalidated string
// through a map, so anything unrecognised ranks 0 — the same as "target", the
// strictest level. Left unchecked, --widen-to=Repo would ask for the widest
// policy and silently get the narrowest, and --require-coverage=Repo would arm
// the assertion while never matching the level that carries the selection
// clause.
func (o Options) validate() error {
	if o.FailOn != "" && !config.Severity(o.FailOn).Valid() {
		return exitcode.UsageErrorf("invalid --fail-on value: %q (must be %s)", o.FailOn, config.SeverityChoices())
	}
	if o.WidenTo != "" && !config.ValidWidenTo(config.WidenTo(o.WidenTo)) {
		return exitcode.UsageErrorf("invalid --widen-to value: %s (must be target, unit or repo)", o.WidenTo)
	}
	switch config.WidenTo(o.RequireCoverage) {
	case "", config.WidenToUnit, config.WidenToRepo:
		return nil
	case config.WidenToTarget:
		return exitcode.UsageErrorf("invalid --require-coverage value: %s (must be unit or repo; "+
			"target asserts only what was named, which is always true)", o.RequireCoverage)
	default:
		// "target" is excluded on purpose: it asserts only what was named, which
		// is always true and so never fails.
		return exitcode.UsageErrorf("invalid --require-coverage value: %s (must be unit or repo)", o.RequireCoverage)
	}
}

// Run executes a single tool operation (fix, lint, etc.)
func Run(
	operation config.OperationType,
	args []string,
	explainMode string,
	fileScoped bool,
	selectedToolsFlag string,
	failOnSkip bool,
	opts Options,
	loadConfigFunc func() (*config.Config, string, error),
) error {
	return RunSequential(
		[]config.OperationType{operation},
		args, explainMode, fileScoped, selectedToolsFlag, failOnSkip, opts, loadConfigFunc,
	)
}

// formatToolWithDir formats a tool name with optional directory context
func formatToolWithDir(toolName, relativeDir string) string {
	if relativeDir != "" {
		return fmt.Sprintf("⏳ %s (%s)", toolName, relativeDir)
	}
	return "⏳ " + toolName
}

// toolOpID is the correlation id of one task's events: the operation's run id
// and the task's ID, which the executor makes unique within the operation. A
// task's start, its progress chunks and its terminal event share it, and no
// other task's do.
func toolOpID(runOpID, taskID string) string {
	return runOpID + ":" + taskID
}

// chunkStatus reports a chunk event's status: done once a tool's last file unit
// is reached, progress otherwise (progress updates are throttled by the sink).
func chunkStatus(index, total int) string {
	if total > 0 && index >= total {
		return uievent.StatusDone
	}
	return uievent.StatusProgress
}

// doneStatus maps a success flag to a terminal event status.
func doneStatus(success bool) string {
	if success {
		return uievent.StatusDone
	}
	return uievent.StatusFail
}

// resultErrorMessage builds a concise message for a failed tool_run's error
// event: the underlying error when present, otherwise a non-zero exit summary.
func resultErrorMessage(result tooling.ExecutionResult) string {
	if result.Error != nil {
		return result.Error.Error()
	}
	return fmt.Sprintf("exited with code %d", result.ExitCode)
}

// groupResultsByTool groups execution results by tool name
func groupResultsByTool(groupResults []tooling.GroupExecutionResult) []toolExecutionGroup {
	toolMap := make(map[string]*toolExecutionGroup)
	var toolOrder int

	for _, groupResult := range groupResults {
		for _, result := range groupResult.Results {
			// Cancelled tasks are listed apart (printStoppedTasks), never as runs.
			if result.IsCancelled() {
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
	if ui.Quiet() {
		return
	}
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
		views, hidden := groupViews(group)
		durStr := ui.FormatDurationShort(group.wallTime)
		if group.totalRuns > 1 || group.failedRuns > 0 || detailed || hidden.total() > 0 {
			durStr = fmt.Sprintf("%-*s", durationColWidth, durStr)
		}
		line := clr.Faint("┃ ") + status + " " + nameDisplay + strings.Repeat(" ", pad) + heatDuration(group.wallTime, maxMs, durStr)
		if group.totalRuns > 1 {
			line += " " + clr.Faint(fmt.Sprintf("×%d", group.totalRuns))
		}
		if group.failedRuns > 0 {
			line += "  " + clr.Red(fmt.Sprintf("(%d failed)", group.failedRuns))
		}
		if hidden.total() > 0 {
			line += "  " + clr.Faint("· "+hidden.String())
		}
		if detailed {
			line += "  " + clr.Faint(toolDetail(group))
		}
		fmt.Println(line)

		runNum := 0
		for i, exec := range group.executions {
			switch {
			case !exec.result.Success:
				runNum++
				printFailedExecution(runNum, exec)
			case views[i].unenforced:
				printUnenforcedExecution(exec, views[i])
			}
		}
	}
}

// groupViews is viewOf for every execution of a tool, and the findings they
// leave out between them.
func groupViews(group toolExecutionGroup) ([]taskView, levelCounts) {
	views := make([]taskView, len(group.executions))
	var hidden levelCounts
	for i, exec := range group.executions {
		views[i] = viewOf(exec.result)
		hidden.merge(views[i].hidden)
	}
	return views, hidden
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
	return formatDiagnosticRelativeTo(d, "")
}

// formatDiagnosticRelativeTo is formatDiagnostic with paths shortened against
// baseDir. Every path the executor hands over is absolute, which in a monorepo
// pushes the useful part of the line off-screen; the box already prints the Cwd
// these are relative to.
func formatDiagnosticRelativeTo(d diagnostic.Diagnostic, baseDir string) string {
	loc := fmt.Sprintf("%d:%d", d.Row, d.Col)
	if d.File != "" {
		loc = relativeToBase(d.File, baseDir) + ":" + loc
	}
	line := fmt.Sprintf("%s %s %s", clr.Faint(loc), severityColor(d.Severity)(d.Severity.String()), d.Message)
	if d.Code != "" {
		line += " " + clr.Faint("["+d.Code+"]")
	}
	return line
}

// relativeToBase shortens an absolute diagnostic path against baseDir, keeping
// the original whenever that is not possible or would escape upwards ("../..")
// — a wrong-looking short path is worse than a long correct one.
func relativeToBase(file, baseDir string) string {
	if baseDir == "" || !filepath.IsAbs(file) {
		return file
	}
	rel, err := filepath.Rel(baseDir, file)
	if err != nil || strings.HasPrefix(rel, "..") {
		return file
	}
	return rel
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

// usableDiagnostics reports whether the parsed diagnostics are worth showing in
// place of the tool's raw output. --no-parse asks for the raw output.
//
// One batch invocation covers many files, so a diagnostic without a file is
// unattributable — and the raw output the parsed view replaces almost always did
// name the file. Rather than silently degrade whenever a batch tool's parser
// does not report paths, fall back to the raw text. Per-file runs, and batches
// of one file, are unaffected: the executor stamps the one file the process was
// given, and even unstamped they are read in the context of a single file.
func usableDiagnostics(result tooling.ExecutionResult) bool {
	if len(result.Diagnostics) == 0 || parsingDisabled() {
		return false
	}
	if !result.Batch {
		return true
	}
	for _, d := range result.Diagnostics {
		if d.File != "" {
			return true
		}
	}
	return false
}

// printFailedExecution prints details of a failed execution in a bordered format
// showing all context needed to interpret error output in monorepo setups
func printFailedExecution(runNum int, exec executionInstance) {
	result := exec.result
	view := viewOf(result)

	// Build header with tool name and scope
	header := "─ " + clr.Red(result.ToolName)
	if result.Scope != "" {
		header += " " + clr.Faint("["+string(result.Scope)+"]")
	}
	header += fmt.Sprintf(" (run #%d) ", runNum)

	border := clr.Red
	label := clr.Faint

	fmt.Printf("  %s%s%s\n", border("┌"), header, border(strings.Repeat("─", 20)))
	printFrameContext(exec, border)

	// Command details
	if result.Command != "" {
		fmt.Printf("  %s  %s %s\n", border("│"), label("Command:  "), result.Command)
	}

	// Exit info
	fmt.Printf("  %s  %s %s\n", border("│"), label("Exit code:"), clr.Red(strconv.Itoa(result.ExitCode)))
	if gating := gatingFindings(result); gating > 0 {
		noun := "findings"
		if gating == 1 {
			noun = "finding"
		}
		fmt.Printf("  %s  %s %s\n", border("│"), label("Failed on:"),
			clr.Red(fmt.Sprintf("%d %s at or above failOn=%s", gating, noun, failOnOf(result))))
	}
	fmt.Printf("  %s  %s %s\n", border("│"), label("Duration: "), formatDuration(result.Duration))

	switch {
	// Parsed diagnostics, when the tool has an outputParser, are clearer than the
	// raw output (often JSON) and take its place.
	case !view.raw:
		printFindings(view, result, border)
		for _, failure := range result.UnparsedFailures {
			fmt.Printf("  %s\n", border("│"))
			for line := range strings.SplitSeq(failure, "\n") {
				fmt.Printf("  %s  %s\n", border("│"), line)
			}
		}
		printHiddenLine(view, result, border)
	case strings.TrimSpace(result.Output) != "":
		fmt.Printf("  %s\n", border("│"))
		lines := strings.SplitSeq(strings.TrimRight(result.Output, "\n"), "\n")
		for line := range lines {
			if strings.TrimSpace(line) == "" {
				fmt.Printf("  %s\n", border("│"))
				continue
			}
			fmt.Printf("  %s  %s\n", border("│"), line)
		}
	case result.Error != nil:
		fmt.Printf("  %s\n", border("│"))
		fmt.Printf("  %s  %s\n", border("│"), result.Error.Error())
	}

	fmt.Printf("  %s%s\n", border("└"), border(strings.Repeat("─", 57)))
	fmt.Println()
}

// printUnenforcedExecution prints the findings of a passed task at or above a
// threshold nobody enforced, in a yellow frame: the parser module predates the
// severity contract, so the exit code decided, and a user who asked for the
// threshold sees what it would have caught.
func printUnenforcedExecution(exec executionInstance, view taskView) {
	result := exec.result
	border := clr.Yellow
	header := "─ " + clr.Yellow(result.ToolName)
	if result.Scope != "" {
		header += " " + clr.Faint("["+string(result.Scope)+"]")
	}
	header += fmt.Sprintf(" (failOn=%s not enforced) ", failOnOf(result))

	fmt.Printf("  %s%s%s\n", border("┌"), header, border(strings.Repeat("─", 20)))
	printFrameContext(exec, border)
	fmt.Printf("  %s  %s\n", border("│"), clr.Faint("its parser module predates the severity contract; the exit code decided"))
	if view.raw {
		fmt.Printf("  %s\n", border("│"))
		for line := range strings.SplitSeq(strings.TrimRight(result.Output, "\n"), "\n") {
			fmt.Printf("  %s  %s\n", border("│"), line)
		}
	} else {
		printFindings(view, result, border)
		printHiddenLine(view, result, border)
	}
	fmt.Printf("  %s%s\n", border("└"), border(strings.Repeat("─", 57)))
	fmt.Println()
}

// printFrameContext prints the directories a frame's paths are relative to.
func printFrameContext(exec executionInstance, border func(a ...any) string) {
	if exec.relativeDir != "" {
		fmt.Printf("  %s  %s %s\n", border("│"), clr.Faint("Dir:      "), exec.relativeDir)
	}
	if exec.result.WorkingDir != "" {
		fmt.Printf("  %s  %s %s\n", border("│"), clr.Faint("Cwd:      "), exec.result.WorkingDir)
	}
}

func printFindings(view taskView, result tooling.ExecutionResult, border func(a ...any) string) {
	if len(view.shown) == 0 {
		return
	}
	fmt.Printf("  %s\n", border("│"))
	for _, d := range view.shown {
		fmt.Printf("  %s  %s\n", border("│"), formatDiagnosticRelativeTo(d, result.WorkingDir))
	}
}

// printHiddenLine closes a frame with what it left out: the findings below the
// threshold, counted rather than dropped.
func printHiddenLine(view taskView, result tooling.ExecutionResult, border func(a ...any) string) {
	if view.hidden.total() == 0 {
		return
	}
	fmt.Printf("  %s  %s\n", border("│"),
		clr.Faint(fmt.Sprintf("+ %s hidden (failOn=%s)", view.hidden.String(), failOnOf(result))))
}

// gatingFindings counts the findings that failed a process that exited 0.
func gatingFindings(result tooling.ExecutionResult) int {
	n := 0
	for _, proc := range result.Processes {
		if !proc.ThresholdFailed {
			continue
		}
		for _, d := range proc.Diagnostics {
			if d.Gates {
				n++
			}
		}
	}
	return n
}

// durationColWidth reserves a fixed slot for the per-tool duration (covers
// values like "11.35s"/"120ms"/"1m05s") so the run count after it never floats.
const durationColWidth = 7

// phaseTop renders the opening bracket rule for an operation.
func phaseTop(operation string) string {
	return ui.RuleLine("┏", operation, clr.Bold(operation))
}

// printOperationFooter renders the closing bracket rule that summarizes the
// operation (tool/run counts, wall-clock time, failures, cancelled tasks, skips,
// cache hit rate and an optional note).
func printOperationFooter(toolGroups []toolExecutionGroup, wallClockTime int64, cacheHits, cacheMisses, skipped, cancelled int, note string) {
	if ui.Quiet() {
		return
	}
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
	if cancelled > 0 {
		cancelText := fmt.Sprintf(" · %d cancelled", cancelled)
		plain += cancelText
		colored += clr.Faint(cancelText)
	}
	var hidden levelCounts
	for _, group := range toolGroups {
		_, groupHidden := groupViews(group)
		hidden.merge(groupHidden)
	}
	if hidden.total() > 0 {
		hiddenText := fmt.Sprintf(" · %s hidden", hidden.String())
		plain += hiddenText
		colored += clr.Faint(hiddenText)
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
	if note != "" {
		plain += " · " + note
		colored += " · " + clr.Yellow(note)
	}

	fmt.Println(ui.RuleLine("┗", plain, colored))
}

func partialTasks(results []tooling.GroupExecutionResult) int {
	n := 0
	for _, group := range results {
		for _, r := range group.Results {
			if r.FilesNotRun > 0 {
				n++
			}
		}
	}
	return n
}

func nonZero(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
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
	cmd.Env = gitenv.Environ()

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
	return cache.NewCache(cacheDir, projectPath, cfg, selectedTools, logger.Logger)
}
