package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/cache"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/managedconfig"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"
	"github.com/datamitsu/datamitsu/internal/textdiff"
	"github.com/datamitsu/datamitsu/internal/tooling"
	"github.com/datamitsu/datamitsu/internal/ui"
	"github.com/datamitsu/datamitsu/internal/uievent"
)

// Server is a minimal, formatting-only LSP server. It is single-threaded: the
// read loop handles one message to completion (including any tool download) before
// reading the next, so the document store and lifecycle flags need no locking.
// This matches Step 2's "minimal residency" — no incremental project model.
type Server struct {
	conn     *conn
	root     string
	planner  *tooling.Planner
	binMgr   *binmanager.BinManager
	executor *tooling.Executor
	cache    *cache.Cache // nil when the cache could not be built (formatting still works)

	// fixWidenTo is the project's execution.widenTo for fix. The editor policy is
	// clamped to it: a session default must not out-scope what the repository
	// asked for.
	fixWidenTo config.WidenTo

	// managedConfigs is what the preflight check compares the files a fix reads
	// against, from the config the session was started with.
	managedConfigs config.MapOfManagedConfigs

	// tools is the configured tool set, which initializationOptions.format.tools
	// is checked against.
	tools config.MapOfTools

	// policy is the editor's session policy: the environment until initialize,
	// then initializationOptions over it, fixed for the rest of the session.
	policy formatPolicy

	// foreignCacheReported keeps the "cache belongs to another configuration"
	// notice to one per session: it holds until the server restarts.
	foreignCacheReported bool

	// now is the watchdog's clock; nil means time.Now.
	now func() time.Time

	docs map[string][]byte // open documents: uri -> current full text

	initialized      bool
	shutdownReceived bool
	exitCode         int
}

// NewServer builds a formatting-only server over r/w using cfg. It assembles its
// OWN lightweight planner+binManager+executor (no parser, no UI) so a format
// request never parses diagnostics or prints to stdout.
//
// The planner's cwd is set to the git root, NOT the process launch directory: an
// editor selects which file to format from anywhere in the workspace, so the
// CLI's CWD-subtree restriction (which would silently drop files outside the
// launch dir) is wrong here — any file under the repo root is formattable.
//
// It shares the SAME execution cache the CLI uses (keyed on the git root, with
// matching invalidation key), so formatting a file in the editor warms the
// per-file fix cache and a later `datamitsu fix`/`check` can skip that unchanged
// file. Only per-file/globbed tools benefit — whole-project tools (e.g.
// golangci-lint fmt) the CLI always runs in bulk and never looks up per file.
func NewServer(r io.Reader, w io.Writer, cfg *config.Config, root string) *Server {
	rm := runtimemanager.New(cfg.Runtimes)
	binMgr := binmanager.New(cfg.Apps, cfg.Bundles, rm)

	planner := tooling.NewPlanner(root, root, nil, cfg.Tools, cfg.ProjectTypes, cfg.IgnoreRules)
	planner.SetPlatformChecker(binMgr)
	// Planned at unit, the widest level a save can reach; editorDecision applies
	// the project's own fix policy, because a task the planner drops reaches no
	// left-out notice and no skipped count.
	planner.SetWidenPolicy(cfg.Execution, config.WidenToUnit)

	// Build the same cache the CLI runner does so keys/paths align. selectedTools
	// is nil — the LSP, like the lefthook `check`, runs the full tool set, so the
	// invalidation keys match and entries are shared. A build failure is non-fatal:
	// formatting just runs without caching.
	projectCache, err := cache.NewCache(env.GetCachePath(), root, *cfg, nil, logger.Logger)
	if err != nil {
		emitLog(uievent.NextOpID("lsp"), uievent.LevelWarn, "cache unavailable, formatting without it: "+err.Error())
		projectCache = nil
	} else {
		// The session keeps the config it started with; once the CLI writes a
		// newer key, overwriting it would reset both caches in a loop.
		projectCache.SetYieldToForeignKey(true)
	}

	return &Server{
		conn:     newConn(r, w),
		root:     root,
		planner:  planner,
		binMgr:   binMgr,
		executor: tooling.NewExecutor(root, false, false, binMgr, projectCache),
		cache:    projectCache,
		docs:     make(map[string][]byte),

		fixWidenTo: cfg.Execution.ResolveWidenTo(config.OpFix, ""),

		managedConfigs: cfg.ManagedConfigs,
		tools:          cfg.Tools,
		policy:         envFormatPolicy(),
		now:            time.Now,
	}
}

// ExitCode is the process exit code the caller should honor after Run returns:
// 0 on clean shutdown or stdin EOF, 1 if `exit` arrived before `shutdown` (per
// the LSP spec).
func (s *Server) ExitCode() int { return s.exitCode }

// Run reads and dispatches messages until `exit`, stdin EOF, or a fatal transport
// error. A frame whose body is read in full but is not valid JSON is recoverable:
// per JSON-RPC 2.0 the server replies Parse Error and keeps serving (one corrupt
// message must not tear down the language server). It never writes anything but
// framed JSON-RPC to its writer.
func (s *Server) Run(ctx context.Context) error {
	// Flush + stop the cache's debounce goroutine when the session ends.
	if s.cache != nil {
		defer s.cache.Shutdown()
	}
	for {
		body, err := s.conn.readFrame()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil // client closed the connection
			}
			return fmt.Errorf("lsp read: %w", err) // fatal: stream out of sync
		}

		var m message
		if err := json.Unmarshal(body, &m); err != nil {
			// The frame was fully consumed, so the stream stays framed; only this
			// message is bad. id is unknown for invalid JSON, so reply id:null.
			_ = s.conn.replyError(nil, codeParseError, "parse error: "+err.Error())
			continue
		}

		if s.handle(ctx, &m) {
			return nil // `exit` received; caller honors ExitCode()
		}
	}
}

// FormatFile formats one file by running the project's configured fix on the
// REAL file, on disk, in its real location, then reflecting the result back to
// the editor. This is the only way every tool's own path/project detection
// (nearest package.json/tsconfig, monorepo workspace, parser inference, ignore
// files, ...) matches plain `datamitsu fix`: a tool fed a temp copy or stdin
// would resolve against the wrong location and behave differently or skip the
// file. datamitsu resolves each tool's working directory from the file's project
// itself (the planner's cwd is the git root, so no subtree is dropped and
// repository-scope tools still run), so the caller only passes the path.
//
// In-place tools must see the file on disk, so the result is delivered one of two
// ways depending on whether the editor buffer already matches disk:
//   - buffer == disk (saved / unedited): fix the file as-is and return NO edits.
//     The editor reloads the changed file into its clean buffer, so formatting
//     neither dirties the buffer nor forces a second save.
//   - buffer != disk (unsaved edits, including the format-on-save path): persist
//     the buffer first, then return the diff buffer->fixed so the editor applies
//     it before writing its own save.
//
// Returns an empty (non-nil) slice when no tool applies or nothing changed.
//
// Every request is one `format` phase on the JSON-L stream: tool_run and error
// events for its tasks, notices for what the policy or the watchdog left out,
// and a closing done event on every path, success or error.
func (s *Server) FormatFile(ctx context.Context, absPath string, content []byte) (edits []TextEdit, err error) {
	opID := uievent.NextOpID("fmt")
	started := time.Now()
	ui.Emit(uievent.Event{Type: uievent.TypePhase, OpID: opID, Status: uievent.StatusStart, Op: formatOp})
	var tally formatTally
	defer func() { emitFormatDone(opID, started, tally, err) }()

	display := s.displayPath(absPath)

	// One planner serves the whole session, and its file list is walked once. A
	// file created after the server started would otherwise stay invisible to
	// unit member lists, leaving a unit's verdict inputs unchanged and taking a
	// cached pass the new file invalidates — a formatter silently not running.
	// Correctness over the saved walk: an editor formats one file per request.
	s.planner.Invalidate()

	plan, err := s.planner.Plan(ctx, config.OpFix, tooling.Selection{Mode: tooling.SelectionPaths, Paths: []string{absPath}}, nil)
	if err != nil {
		return nil, fmt.Errorf("plan fix for %s: %w", absPath, err)
	}
	left := filterPlanForEditor(plan, absPath, s.fixWidenTo, s.policy)
	tally.skipped += len(left)
	if msg := leftOutNotice(display, left); msg != "" {
		emitLog(opID, uievent.LevelInfo, msg)
	}

	apps := planApps(plan)
	if len(apps) == 0 {
		return []TextEdit{}, nil // no fix tool applies to this file
	}

	if err := managedconfig.CheckConfigFiles(s.root, s.managedConfigs, plan.ManagedConfigRefs()); err != nil {
		return nil, err
	}

	// Auto-install/verify the tools the plan needs (download progress streams to
	// stderr as JSON-L for the status bar).
	if err := s.binMgr.EnsureTools(ctx, apps); err != nil {
		return nil, fmt.Errorf("ensure tools installed: %w", err)
	}

	// Defense in depth: never touch a path outside the workspace root, whatever
	// URI the editor sent (the planner would also yield no tasks for it).
	rel, relErr := filepath.Rel(s.root, absPath)
	if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("refusing to format %s: outside workspace root", absPath)
	}

	// clean = the editor's buffer already matches the file on disk; then the editor
	// will reload our fix and stay non-dirty, so we return no edits below.
	diskBefore, _ := os.ReadFile(absPath) // missing/unreadable -> treat as dirty
	clean := bytes.Equal(content, diskBefore)

	if !clean {
		// Persist the unsaved buffer (preserving mode) so in-place tools operate on
		// the live content rather than the stale on-disk version.
		perm := os.FileMode(0o644)
		if info, statErr := os.Stat(absPath); statErr == nil {
			perm = info.Mode().Perm()
		}
		//nolint:gosec // absPath is the editor's document URI, validated just above to be inside the workspace root
		if err := os.WriteFile(absPath, content, perm); err != nil {
			return nil, fmt.Errorf("write buffer to %s: %w", absPath, err)
		}
	}

	// failFast is off, so each group runs to completion and returns a nil error
	// even when individual tools fail; we surface whatever ended up on disk.
	s.wireToolEvents(opID)
	results, ran, err := s.executeWithWatchdog(ctx, plan, time.Duration(s.policy.TimeoutMs)*time.Millisecond)
	tally.addResults(results)
	if skipped := plan.Groups[ran:]; len(skipped) > 0 {
		tally.skipped += countTasks(skipped)
		emitLog(opID, uievent.LevelWarn, watchdogNotice(display, ran, len(plan.Groups), s.policy.TimeoutMs, skipped))
	}
	if err != nil {
		return nil, fmt.Errorf("fix %s: %w", absPath, err)
	}

	// Flush the cache synchronously: a format is a discrete, low-frequency event,
	// so don't rely on the 100ms debounce (the server may exit before it fires).
	// Best-effort — a failed save only costs a redundant re-run later.
	s.saveCache(opID)

	if clean {
		// The editor reloads the fixed file into its clean buffer; returning edits
		// here would re-dirty it and force a redundant save.
		return []TextEdit{}, nil
	}

	fixed, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read fixed %s: %w", absPath, err)
	}
	return toTextEdits(textdiff.ComputeEdits(string(content), string(fixed)))
}

// executeWithWatchdog runs the plan one priority group at a time and starts no
// further group once limit has elapsed. The first group always runs, and a
// running tool is never cancelled: a chain cut mid-way leaves a file with one
// formatter's edits and not the next one's, so the same save would produce
// different bytes depending on machine load. limit <= 0 disables the watchdog.
// ran is how many groups started.
func (s *Server) executeWithWatchdog(
	ctx context.Context, plan *tooling.ExecutionPlan, limit time.Duration,
) (results []tooling.GroupExecutionResult, ran int, err error) {
	now := s.now
	if now == nil {
		now = time.Now
	}
	start := now()
	for i, group := range plan.Groups {
		if i > 0 && limit > 0 && now().Sub(start) >= limit {
			break
		}
		groupResults, execErr := s.executor.Execute(ctx, &tooling.ExecutionPlan{
			ConfigName: plan.ConfigName,
			Groups:     []tooling.TaskGroup{group},
		})
		results = append(results, groupResults...)
		ran++
		if execErr != nil {
			return results, ran, fmt.Errorf("execute priority group %d: %w", group.Priority, execErr)
		}
	}
	return results, ran, nil
}

// wireToolEvents points the executor's callbacks at this request's op id. The
// server handles one request at a time, so rewiring per request is safe.
func (s *Server) wireToolEvents(opID string) {
	s.executor.SetTaskStartCallback(func(tool, dir string) {
		ui.Emit(uievent.Event{
			Type:   uievent.TypeToolRun,
			OpID:   toolRunOpID(opID, tool, dir),
			Status: uievent.StatusStart,
			Tool:   tool,
			Dir:    dir,
		})
	})
	s.executor.SetResultCallback(func(result tooling.ExecutionResult) {
		if cancelled(result) {
			return
		}
		runID := toolRunOpID(opID, result.ToolName, result.RelativeDir)
		ui.Emit(uievent.Event{
			Type:       uievent.TypeToolRun,
			OpID:       runID,
			Status:     terminalStatus(result.Success),
			Tool:       result.ToolName,
			Dir:        result.RelativeDir,
			Success:    new(result.Success),
			DurationMs: result.Duration,
		})
		if !result.Success {
			// Without this a failing formatter is invisible: the request still
			// succeeds with whatever the other tools left on disk.
			ui.Emit(uievent.Event{
				Type: uievent.TypeError,
				OpID: runID,
				Tool: result.ToolName,
				Dir:  result.RelativeDir,
				Msg:  resultErrorMessage(result),
			})
		}
	})
}

// saveCache flushes the execution cache. A cache owned by another configuration
// is left alone and reported once per session; formatting is unaffected.
func (s *Server) saveCache(opID string) {
	if s.cache == nil {
		return
	}
	err := s.cache.Save()
	switch {
	case err == nil:
	case errors.Is(err, cache.ErrForeignKey):
		if s.foreignCacheReported {
			return
		}
		s.foreignCacheReported = true
		emitForeignCache(opID, err)
	default:
		emitLog(opID, uievent.LevelWarn, "cache save failed: "+err.Error())
	}
}

// emitForeignCache explains a cache file this session does not write. A file
// written by another datamitsu is an info: an editor running a different binary
// than the CLI is a normal setup, and a warning would pop up every session with
// advice (restart) that cannot help.
func emitForeignCache(opID string, err error) {
	var fk *cache.ForeignKeyError
	if errors.As(err, &fk) && fk.DiskVersion != "" && fk.DiskVersion != ldflags.Version {
		emitLog(opID, uievent.LevelInfo, fmt.Sprintf(
			"the execution cache on disk was written by datamitsu %s and this language server runs %s, "+
				"so they do not share cache entries. Formatting still works; run the language server with "+
				"the same datamitsu as the CLI to share them", fk.DiskVersion, ldflags.Version))
		return
	}
	emitLog(opID, uievent.LevelWarn,
		"the execution cache on disk was written for a different configuration (the config changed, "+
			"or a run used --tools). Formatting still works but does not warm the cache until the CLI "+
			"writes it with this session's configuration; if the config changed after this session "+
			"started, restart the language server")
}

// displayPath names a file for a notice: relative to the workspace root when it
// is inside it.
func (s *Server) displayPath(absPath string) string {
	rel, err := filepath.Rel(s.root, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return absPath
	}
	return filepath.ToSlash(rel)
}

// filterPlanForEditor drops the tasks the editor policy keeps out of a save and
// returns the ones worth reporting, with their reasons. Groups left empty are
// removed, so the watchdog counts only groups that run something.
//
// The planner already targets the file: Plan is called with a Paths selection,
// so a file-granularity task carries exactly this file and nothing else. What
// remains is a latency choice, not a correctness one — running a unit task here
// is exactly what `datamitsu fix` does, and correctness lives in the cache's
// coverage gate.
//
// This replaces scopeTasksToFile, which pinned every task's file list and
// appended the path to argv for any task without a placeholder. For the
// operations that take no positional path that produced knowingly wrong
// commands: `tsc --noEmit … a.ts` exits 1 with TS5112 and zero diagnostics,
// `syncpack lint <file>` rejects the argument outright. It also let a
// single-file editor run write cache entries as if it had covered a whole unit.
func filterPlanForEditor(
	plan *tooling.ExecutionPlan, absPath string, projectPolicy config.WidenTo, policy formatPolicy,
) []leftOut {
	var left []leftOut
	groups := plan.Groups[:0]
	for _, group := range plan.Groups {
		kept := group.Tasks[:0]
		for _, task := range group.Tasks {
			run, reason := editorDecision(task, absPath, projectPolicy, policy)
			if run {
				kept = append(kept, task)
				continue
			}
			if reason != "" {
				left = append(left, leftOut{Tool: task.ToolName, Reason: reason})
			}
		}
		if len(kept) > 0 {
			group.Tasks = kept
			groups = append(groups, group)
		}
	}
	plan.Groups = groups
	return left
}

type leftOut struct {
	Tool   string
	Reason string
}

const (
	reasonLSPFalse      = "lsp: false in config"
	reasonToolsFalse    = "disabled by format.tools"
	reasonRepo          = "repository-wide, never on save"
	reasonSessionTarget = "project-wide, editor policy is target"
	reasonProjectTarget = "project policy is target"
	reasonNoGlobs       = "declares no globs, so it cannot tell which files it formats"
)

// editorDecision reports whether a save runs task, and if not, why. An empty
// reason means the task is dropped without a mention.
//
// Any false wins and a true needs permission from both sides: the author's
// lsp: false beats the user's format.tools opt-in, because some operations
// mutate lock files and some run network scanners. The session policy only
// narrows the project's execution.widenTo, never widens it.
func editorDecision(task tooling.Task, absPath string, projectPolicy config.WidenTo, policy formatPolicy) (run bool, reason string) {
	granularity := config.InferGranularity(task.OpConfig)

	// An operation with no globs is planned once per project regardless of what
	// was saved, so without this a save would run it in every module — rewriting
	// files the editor never opened, since these tools fix in place and only the
	// target is read back. Other modules are not this save's business, so they
	// are not reported either; every rule below would drop the task as well.
	if granularity == config.GranularityUnit && !taskCoversPath(task, absPath) {
		return false, ""
	}

	if task.OpConfig.LSP != nil && !*task.OpConfig.LSP {
		return false, reasonLSPFalse
	}
	optIn, set := policy.Tools[task.ToolName]
	if set && !optIn {
		return false, reasonToolsFalse
	}

	switch granularity {
	case config.GranularityFile:
		return true, ""
	case config.GranularityRepo:
		// A whole-repository fix on every keystroke is never acceptable, so no
		// setting reaches it.
		return false, reasonRepo
	case config.GranularityUnit:
		// The planner plans a glob-less operation for every file in its unit, so
		// saving a TypeScript file ran `golangci-lint fmt` over a whole Go module:
		// 152 s, against 0.31 s for the one file. Only the user can vouch for it.
		if len(task.OpConfig.Globs) == 0 && !optIn {
			return false, reasonNoGlobs
		}
		if projectPolicy == "" {
			projectPolicy = config.DefaultWidenTo
		}
		// The planner plans the editor at unit, so this is what holds a project
		// that declared fix: "target" to it: otherwise saving one file runs an
		// in-place formatter over the whole project, the blast radius that
		// setting exists to prevent.
		if projectPolicy.Rank() < config.WidenToUnit.Rank() {
			return false, reasonProjectTarget
		}
		// The opt-in bypasses the session class, never the project.
		if optIn {
			return true, ""
		}
		if policy.WidenTo.Rank() < config.WidenToUnit.Rank() {
			return false, reasonSessionTarget
		}
		return true, ""
	}
	return false, ""
}

// taskCoversPath reports whether a task's unit contains the saved file.
func taskCoversPath(task tooling.Task, absPath string) bool {
	if task.ProjectPath == "" {
		return true
	}
	rel, err := filepath.Rel(task.ProjectPath, absPath)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// planApps returns the distinct apps a plan needs, in first-seen order.
func planApps(plan *tooling.ExecutionPlan) []string {
	seen := make(map[string]struct{})
	var apps []string
	for _, group := range plan.Groups {
		for _, task := range group.Tasks {
			if _, dup := seen[task.OpConfig.App]; !dup {
				seen[task.OpConfig.App] = struct{}{}
				apps = append(apps, task.OpConfig.App)
			}
		}
	}
	return apps
}

// handle dispatches one message and reports whether the loop should stop.
func (s *Server) handle(ctx context.Context, m *message) (stop bool) {
	// Per spec, requests other than initialize/shutdown before initialization get
	// ServerNotInitialized; pre-init notifications are dropped.
	if !s.initialized && m.isRequest() && m.Method != "initialize" {
		_ = s.conn.replyError(m.ID, codeServerNotReady, "server not initialized")
		return false
	}

	// After shutdown, every request except exit must be rejected with
	// InvalidRequest (the `exit` notification is handled below). This prevents a
	// post-shutdown formatting request from spawning tools.
	if s.shutdownReceived && m.isRequest() {
		_ = s.conn.replyError(m.ID, codeInvalidRequest, "server is shutting down")
		return false
	}

	switch m.Method {
	case "initialize":
		s.onInitialize(m)
	case "initialized":
		// notification — nothing to do
	case "textDocument/didOpen":
		s.onDidOpen(m)
	case "textDocument/didChange":
		s.onDidChange(m)
	case "textDocument/didClose":
		s.onDidClose(m)
	case "textDocument/formatting":
		s.onFormatting(ctx, m)
	case "shutdown":
		s.shutdownReceived = true
		_ = s.conn.reply(m.ID, nil) // result: null
	case "exit":
		if s.shutdownReceived {
			s.exitCode = 0
		} else {
			s.exitCode = 1
		}
		return true
	default:
		if m.isRequest() {
			_ = s.conn.replyError(m.ID, codeMethodNotFound, "method not found: "+m.Method)
		}
		// Unknown notifications (e.g. $/setTrace) are ignored.
	}
	return false
}

func (s *Server) onInitialize(m *message) {
	s.initialized = true

	// initializationOptions are read once: the policy is fixed for the session,
	// so a client that changes them restarts the server.
	var params initializeParams
	var res policyResolution
	if isNull(m.Params) || json.Unmarshal(m.Params, &params) == nil {
		res = resolveFormatPolicy(params.InitializationOptions, envFormatPolicy(), s.isTool)
	} else {
		res = policyResolution{Policy: envFormatPolicy()}
		res.warn("initialize params are not an object; initializationOptions ignored")
	}
	s.policy = res.Policy
	emitLog(uievent.NextOpID("lsp"), uievent.LevelInfo, res.summary())
	for _, w := range res.Warnings {
		emitLog(uievent.NextOpID("lsp"), uievent.LevelWarn, w)
	}

	_ = s.conn.reply(m.ID, initializeResult{
		Capabilities: serverCapabilities{
			PositionEncoding: "utf-16",
			TextDocumentSync: textDocumentSyncOptions{
				OpenClose: true,
				Change:    textDocumentSyncFull,
			},
			DocumentFormattingProvider: true,
			Experimental: experimentalCapabilities{Datamitsu: datamitsuCapabilities{Format: formatPolicyEcho{
				WidenTo:   string(s.policy.WidenTo),
				TimeoutMs: s.policy.TimeoutMs,
				Tools:     s.policy.Tools,
			}}},
		},
		ServerInfo: serverInfo{Name: ldflags.PackageName + "-lsp", Version: ldflags.Version},
	})
}

func (s *Server) isTool(name string) bool {
	_, ok := s.tools[name]
	return ok
}

func (s *Server) onDidOpen(m *message) {
	var p didOpenParams
	if err := json.Unmarshal(m.Params, &p); err != nil {
		return
	}
	s.docs[p.TextDocument.URI] = []byte(p.TextDocument.Text)
}

func (s *Server) onDidChange(m *message) {
	var p didChangeParams
	if err := json.Unmarshal(m.Params, &p); err != nil {
		return
	}
	// Full sync: the last change carries the entire new document text.
	if n := len(p.ContentChanges); n > 0 {
		s.docs[p.TextDocument.URI] = []byte(p.ContentChanges[n-1].Text)
	}
}

func (s *Server) onDidClose(m *message) {
	var p didCloseParams
	if err := json.Unmarshal(m.Params, &p); err != nil {
		return
	}
	delete(s.docs, p.TextDocument.URI)
}

func (s *Server) onFormatting(ctx context.Context, m *message) {
	var p formattingParams
	if err := json.Unmarshal(m.Params, &p); err != nil {
		_ = s.conn.replyError(m.ID, codeInvalidParams, "invalid params: "+err.Error())
		return
	}
	absPath, err := uriToPath(p.TextDocument.URI)
	if err != nil {
		_ = s.conn.replyError(m.ID, codeInvalidParams, err.Error())
		return
	}

	// Prefer the live (possibly unsaved) buffer; fall back to disk if the client
	// formats a document it never opened.
	content, ok := s.docs[p.TextDocument.URI]
	if !ok {
		b, readErr := os.ReadFile(absPath)
		if readErr != nil {
			_ = s.conn.replyError(m.ID, codeInvalidParams, "document not open and unreadable: "+readErr.Error())
			return
		}
		content = b
	}

	edits, err := s.FormatFile(ctx, absPath, content)
	if err != nil {
		_ = s.conn.replyError(m.ID, codeRequestFailed, err.Error())
		return
	}
	_ = s.conn.reply(m.ID, edits)
}

// uriToPath converts a file:// LSP document URI to an absolute filesystem path.
// url.Parse already percent-decodes the path (e.g. %20 -> space).
func uriToPath(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("parse uri %q: %w", uri, err)
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("unsupported uri scheme %q (only file:// is supported)", u.Scheme)
	}
	if u.Path == "" {
		return "", fmt.Errorf("uri %q has no path", uri)
	}
	return u.Path, nil
}
