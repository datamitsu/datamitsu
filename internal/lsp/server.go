package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/datamitsu/datamitsu/internal/cache"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/managedconfig"
	"github.com/datamitsu/datamitsu/internal/textdiff"
	"github.com/datamitsu/datamitsu/internal/tooling"
	"github.com/datamitsu/datamitsu/internal/ui"
	"github.com/datamitsu/datamitsu/internal/uievent"
)

// errRequestCancelled ends a format that stopped at a checkpoint because its
// request was cancelled or the session is ending.
var errRequestCancelled = errors.New("cancelled")

// Server is a minimal, formatting-only LSP server for one repository: the
// datamitsu root of the workspace initialize names. Messages that touch the
// session run on one worker goroutine (see Run), so the fields below need no
// locking; the reader goroutine owns only docs.
type Server struct {
	conn *conn

	// loader resolves the root and loads its configuration; nil for a server
	// built around a fixed session, which never reloads.
	loader Loader
	// launchDir is where the server was started: the workspace when initialize
	// names none.
	launchDir string

	// root is the served repository, canonical; empty until initialize resolved
	// one.
	root string
	// loaded is the session built from the configuration; nil while none loads.
	loaded *session
	// supersededKeys are the execution-cache keys of the sessions reloads
	// replaced.
	supersededKeys []string
	// watch are the files the configuration was loaded from; loadedInputs and
	// failedInputs fingerprint their content at the last successful and the
	// last failed load.
	watch        []string
	loadedInputs string
	failedInputs string
	// noSession says why nothing is formatted; noSessionReported is the cause a
	// format request last repeated.
	noSession         string
	noSessionReported string
	// refused are the documents outside root already reported.
	refused map[string]struct{}

	// options are initialize's initializationOptions, kept to resolve the policy
	// again when the configuration reloads; optionsInvalid when initialize's
	// params were not an object.
	options        json.RawMessage
	optionsInvalid bool
	// policy is the editor's session policy: the environment until initialize,
	// then initializationOptions over it.
	policy formatPolicy

	// now is the watchdog's clock; nil means time.Now.
	now func() time.Time

	docs map[string][]byte // open documents: uri -> current full text

	initialized      bool
	shutdownReceived bool
	exitCode         int

	// active is the request the worker is running, nil outside Run.
	active *request

	runMu     sync.Mutex
	stopping  bool
	transport *transport
}

// New builds a server that takes its root from initialize and loads the
// configuration through loader. launchDir is the workspace when initialize
// names none.
func New(r io.Reader, w io.Writer, loader Loader, launchDir string) *Server {
	return &Server{
		conn:      newConn(r, w),
		loader:    loader,
		launchDir: launchDir,
		docs:      make(map[string][]byte),
		policy:    envFormatPolicy(),
		now:       time.Now,
	}
}

// ExitCode is the process exit code the caller should honor after Run returns:
// 0 on clean shutdown or stdin EOF, 1 if `exit` arrived before `shutdown` (per
// the LSP spec).
func (s *Server) ExitCode() int { return s.exitCode }

// FormatFile formats one file by running the project's configured fix on the
// REAL file, on disk, in its real location, then reflecting the result back to
// the editor. This is the only way every tool's own path/project detection
// (nearest package.json/tsconfig, monorepo workspace, parser inference, ignore
// files, ...) matches plain `datamitsu fix`: a tool fed a temp copy or stdin
// would resolve against the wrong location and behave differently or skip the
// file. datamitsu resolves each tool's working directory from the file's project
// itself (the planner's cwd is the git root, so no subtree is dropped and
// repository-scope tools still run), so the caller only passes the path, which
// must be canonical (see canonicalPath).
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
// A cancelled request stops at the next checkpoint and returns
// errRequestCancelled, as does one cancelled while its last group ran. A running
// tool is never interrupted: killing an in-place formatter can truncate the
// user's file.
//
// Every request is one `format` phase on the JSON-L stream: tool_run and error
// events for its tasks, notices for what the policy, the watchdog or a cancel
// left out, and a closing done event on every path, success or error.
func (s *Server) FormatFile(ctx context.Context, absPath string, content []byte) (edits []TextEdit, err error) {
	opID := uievent.NextOpID("fmt")
	started := time.Now()
	ui.Emit(uievent.Event{Type: uievent.TypePhase, OpID: opID, Status: uievent.StatusStart, Op: formatOp})
	var tally formatTally
	defer func() { emitFormatDone(opID, started, tally, err) }()

	ss := s.loaded
	if ss == nil {
		return nil, errors.New("no configuration is loaded")
	}
	// Defense in depth: the handler refuses such a document before calling here.
	if !within(s.root, absPath) {
		return nil, fmt.Errorf("refusing to format %s: outside workspace root", absPath)
	}
	display := s.displayPath(absPath)

	var plan *tooling.ExecutionPlan
	checkpoint := func() error {
		if !s.stopRequested() {
			return nil
		}
		var notRun []tooling.TaskGroup
		if plan != nil {
			notRun = plan.Groups
		}
		tally.skipped += countTasks(notRun)
		emitLog(opID, uievent.LevelInfo, cancelNotice(display, 0, len(notRun), notRun))
		return errRequestCancelled
	}
	if err := checkpoint(); err != nil {
		return nil, err
	}

	// One planner serves the whole session, and its file list is walked once. A
	// file created after the server started would otherwise stay invisible to
	// unit member lists, leaving a unit's verdict inputs unchanged and taking a
	// cached pass the new file invalidates — a formatter silently not running.
	// Correctness over the saved walk: an editor formats one file per request.
	ss.planner.Invalidate()

	plan, err = ss.planner.Plan(ctx, config.OpFix, tooling.Selection{Mode: tooling.SelectionPaths, Paths: []string{absPath}}, nil)
	if err != nil {
		return nil, fmt.Errorf("plan fix for %s: %w", absPath, err)
	}
	left := filterPlanForEditor(plan, absPath, ss.fixWidenTo, s.policy)
	tally.skipped += len(left)
	if msg := leftOutNotice(display, left); msg != "" {
		emitLog(opID, uievent.LevelInfo, msg)
	}
	if err := checkpoint(); err != nil {
		return nil, err
	}

	apps := planApps(plan)
	if len(apps) == 0 { // no fix tool applies to this file
		if !s.settle() {
			return nil, checkpoint() // stopped since the last one
		}
		return []TextEdit{}, nil
	}

	if err := managedconfig.CheckConfigFiles(s.root, ss.managedConfigs, plan.ManagedConfigRefs()); err != nil {
		return nil, err
	}
	if err := checkpoint(); err != nil {
		return nil, err
	}

	// Auto-install/verify the tools the plan needs (download progress streams to
	// stderr as JSON-L for the status bar). A cancel does not interrupt it: the
	// next save needs the same tools.
	if err := ss.binMgr.EnsureTools(ctx, apps); err != nil {
		return nil, fmt.Errorf("ensure tools installed: %w", err)
	}
	if err := checkpoint(); err != nil {
		return nil, err
	}

	// clean = the editor's buffer already matches the file on disk; then the editor
	// will reload our fix and stay non-dirty, so we return no edits below.
	diskBefore, _ := os.ReadFile(absPath) // missing/unreadable -> treat as dirty
	clean := bytes.Equal(content, diskBefore)

	if !clean {
		if err := checkpoint(); err != nil {
			return nil, err
		}
		// Persist the unsaved buffer so in-place tools operate on the live content
		// rather than the stale on-disk version.
		if err := persistBuffer(absPath, content); err != nil {
			return nil, err
		}
	}

	// failFast is off, so each group runs to completion and returns a nil error
	// even when individual tools fail; we surface whatever ended up on disk.
	s.wireToolEvents(opID)
	results, ran, stop, err := s.executeGroups(ctx, plan, time.Duration(s.policy.TimeoutMs)*time.Millisecond, s.stopRequested)
	tally.addResults(results)
	notRun := plan.Groups[ran:]
	tally.skipped += countTasks(notRun)
	if err != nil {
		return nil, fmt.Errorf("fix %s: %w", absPath, err)
	}

	// Flush the cache synchronously: a format is a discrete, low-frequency event,
	// so don't rely on the 100ms debounce (the server may exit before it fires).
	// Best-effort — a failed save only costs a redundant re-run later.
	s.saveCache(opID)

	// A cancel that arrived while the last group ran found no checkpoint after
	// it, but the request is still answered as cancelled, so the stream says so.
	if stop == stoppedByCancel || !s.settle() {
		emitLog(opID, uievent.LevelInfo, cancelNotice(display, ran, len(plan.Groups), notRun))
		return nil, errRequestCancelled
	}
	if stop == stoppedByWatchdog {
		emitLog(opID, uievent.LevelWarn, watchdogNotice(display, ran, len(plan.Groups), s.policy.TimeoutMs, notRun))
	}

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

// groupsStop is why executeGroups started no further group.
type groupsStop int

const (
	ranAll groupsStop = iota
	stoppedByWatchdog
	stoppedByCancel
)

// executeGroups runs the plan one priority group at a time. Before each group,
// the first included, it stops when cancelled reports true; before each later
// group, once limit has elapsed. A running tool is never cancelled: a chain cut
// mid-way leaves a file with one formatter's edits and not the next one's, so
// the same save would produce different bytes depending on machine load, and
// killing an in-place formatter can truncate the file. limit <= 0 disables the
// watchdog. ran is how many groups started.
func (s *Server) executeGroups(
	ctx context.Context, plan *tooling.ExecutionPlan, limit time.Duration, cancelled func() bool,
) (results []tooling.GroupExecutionResult, ran int, stop groupsStop, err error) {
	now := s.now
	if now == nil {
		now = time.Now
	}
	start := now()
	for i, group := range plan.Groups {
		if cancelled != nil && cancelled() {
			return results, ran, stoppedByCancel, nil
		}
		if i > 0 && limit > 0 && now().Sub(start) >= limit {
			return results, ran, stoppedByWatchdog, nil
		}
		groupResults, execErr := s.loaded.executor.Execute(ctx, &tooling.ExecutionPlan{
			ConfigName: plan.ConfigName,
			Groups:     []tooling.TaskGroup{group},
		})
		results = append(results, groupResults...)
		ran++
		if execErr != nil {
			return results, ran, ranAll, fmt.Errorf("execute priority group %d: %w", group.Priority, execErr)
		}
	}
	return results, ran, ranAll, nil
}

// wireToolEvents points the executor's callbacks at this request's op id. The
// server handles one request at a time, so rewiring per request is safe.
func (s *Server) wireToolEvents(opID string) {
	s.loaded.executor.SetTaskStartCallback(func(tool, dir string) {
		ui.Emit(uievent.Event{
			Type:   uievent.TypeToolRun,
			OpID:   toolRunOpID(opID, tool, dir),
			Status: uievent.StatusStart,
			Tool:   tool,
			Dir:    dir,
		})
	})
	s.loaded.executor.SetResultCallback(func(result tooling.ExecutionResult) {
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
// is left alone and reported once per loaded configuration; formatting is
// unaffected.
func (s *Server) saveCache(opID string) {
	ss := s.loaded
	if ss.cache == nil {
		return
	}
	err := ss.cache.Save()
	switch {
	case err == nil:
	case errors.Is(err, cache.ErrForeignKey):
		if ss.foreignCacheReported {
			return
		}
		ss.foreignCacheReported = true
		emitForeignCache(opID, err)
	default:
		emitLog(opID, uievent.LevelWarn, "cache save failed: "+err.Error())
	}
}

// emitForeignCache explains a cache file this session does not write. A file
// written by another datamitsu is an info: an editor running a different binary
// than the CLI is a normal setup, and a warning would pop up every session with
// advice that cannot help.
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
		"the execution cache on disk was written for a different configuration (a run used --tools, or "+
			"the CLI loaded a configuration this session has not yet reloaded). Formatting still works but "+
			"does not warm the cache until the CLI writes it with this session's configuration; the language "+
			"server reloads a changed configuration on the next format")
}

// displayPath names a file for a notice: relative to the workspace root when it
// is inside it.
func (s *Server) displayPath(absPath string) string {
	if s.root == "" || !within(s.root, absPath) {
		return absPath
	}
	rel, err := filepath.Rel(s.root, absPath)
	if err != nil {
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

// persistBuffer writes the editor's unsaved text to path atomically: a temp
// file in the same directory, given the file's mode, renamed over it. A process
// killed mid-write leaves the old file, never a truncated one, and because path
// is canonical a symlinked document keeps its link.
func persistBuffer(path string, content []byte) error {
	fail := func(err error) error { return fmt.Errorf("write buffer to %s: %w", path, err) }
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// Created in place: there is no old content for a crash to lose, and the
		// umask applies as it does to any file the user creates.
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return fail(err)
		}
		_, err = f.Write(content)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return fail(err)
		}
		return nil
	case err != nil:
		return fail(err)
	case !info.Mode().IsRegular():
		// Renaming over a symlink, a FIFO or a device would replace the entry
		// itself, not write to what it stands for.
		return fail(errors.New("not a regular file"))
	}
	// A rename needs only the directory to be writable: without this, a
	// read-only file, or one this user may not write, would be replaced.
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return fail(err)
	}
	_ = f.Close()

	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".dm-lsp-*")
	if err != nil {
		return fail(err)
	}
	_, err = tmp.Write(content)
	if err == nil {
		err = tmp.Chmod(info.Mode().Perm())
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = renameOver(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return fail(err)
	}
	return nil
}

// handle dispatches one message synchronously and reports whether the session
// should end. Run does the same through its reader and worker.
func (s *Server) handle(ctx context.Context, m *message) (stop bool) {
	return s.dispatch(ctx, m, nil)
}

// dispatch runs one message. snap is the document text a formatting request
// was read with; nil reads the document store, which only a synchronous caller
// owns.
func (s *Server) dispatch(ctx context.Context, m *message, snap *docSnapshot) (stop bool) {
	// Per spec, requests other than initialize/shutdown before initialization get
	// ServerNotInitialized.
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
		s.onInitialize(ctx, m)
	case "initialized":
		// notification — nothing to do
	case "textDocument/didOpen":
		s.onDidOpen(m)
	case "textDocument/didChange":
		s.onDidChange(m)
	case "textDocument/didClose":
		s.onDidClose(m)
	case "textDocument/formatting":
		s.onFormatting(ctx, m, snap)
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

func (s *Server) onInitialize(ctx context.Context, m *message) {
	s.initialized = true

	params, ok := decodeInitializeParams(m.Params)
	s.options, s.optionsInvalid = params.InitializationOptions, !ok
	if s.loader != nil {
		s.openSession(ctx, params)
	}
	s.resolvePolicy()

	_ = s.conn.reply(m.ID, initializeResult{
		Capabilities: serverCapabilities{
			PositionEncoding: "utf-16",
			TextDocumentSync: textDocumentSyncOptions{
				OpenClose: true,
				Change:    textDocumentSyncFull,
			},
			DocumentFormattingProvider: true,
			Experimental: experimentalCapabilities{Datamitsu: datamitsuCapabilities{
				Format: formatPolicyEcho{
					WidenTo:   string(s.policy.WidenTo),
					TimeoutMs: s.policy.TimeoutMs,
					Tools:     s.policy.Tools,
				},
				Root: s.root,
			}},
		},
		ServerInfo: serverInfo{Name: ldflags.PackageName + "-lsp", Version: ldflags.Version},
	})
}

// decodeInitializeParams reads initialize's params. A field of the wrong type
// is skipped and the rest still decode: one malformed location must not discard
// initializationOptions. ok is false only when params is not an object.
func decodeInitializeParams(raw json.RawMessage) (params initializeParams, ok bool) {
	if isNull(raw) {
		return params, true
	}
	if trimmed := bytes.TrimSpace(raw); trimmed[0] != '{' {
		return params, false
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		var typeErr *json.UnmarshalTypeError
		return params, errors.As(err, &typeErr)
	}
	return params, true
}

// resolvePolicy applies the initializationOptions kept from initialize over the
// environment, and says what it resolved to. It runs again after a reload: a
// tool the new configuration adds may now be known.
func (s *Server) resolvePolicy() {
	var res policyResolution
	if s.optionsInvalid {
		res = policyResolution{Policy: envFormatPolicy()}
		res.warn("initialize params are not an object; initializationOptions ignored")
	} else {
		res = resolveFormatPolicy(s.options, envFormatPolicy(), s.isTool)
	}
	s.policy = res.Policy
	emitLog(uievent.NextOpID("lsp"), uievent.LevelInfo, res.summary())
	for _, w := range res.Warnings {
		emitLog(uievent.NextOpID("lsp"), uievent.LevelWarn, w)
	}
}

// isTool reports whether name is a configured tool. With no configuration
// loaded every name is kept as given; the policy is checked again once one
// loads.
func (s *Server) isTool(name string) bool {
	if s.loaded == nil {
		return true
	}
	_, ok := s.loaded.tools[name]
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

// onFormatting answers a formatting request. What this server does not format —
// a document outside its root, or anything while no configuration is loaded —
// gets no edits rather than an error, so a save does not pop one up every time.
func (s *Server) onFormatting(ctx context.Context, m *message, snap *docSnapshot) {
	var p formattingParams
	if err := json.Unmarshal(m.Params, &p); err != nil {
		s.replyFormat(m.ID, nil, &requestError{codeInvalidParams, "invalid params: " + err.Error()})
		return
	}
	path, err := uriToPath(p.TextDocument.URI)
	if err != nil {
		s.replyFormat(m.ID, nil, &requestError{codeInvalidParams, err.Error()})
		return
	}
	absPath := canonicalPath(path)

	// Refused before planning: a plan for a foreign file could download tools
	// only to throw the result away.
	if s.root != "" && !within(s.root, absPath) {
		s.reportOutsideRoot(absPath)
		s.replyFormat(m.ID, []TextEdit{}, nil)
		return
	}
	s.refreshSession(ctx)
	if s.loaded == nil {
		s.reportNoSession()
		s.replyFormat(m.ID, []TextEdit{}, nil)
		return
	}

	// Prefer the live (possibly unsaved) buffer; fall back to disk if the client
	// formats a document it never opened.
	if snap == nil {
		text, open := s.docs[p.TextDocument.URI]
		snap = &docSnapshot{text: text, open: open}
	}
	content := snap.text
	if !snap.open {
		b, readErr := os.ReadFile(absPath)
		if readErr != nil {
			s.replyFormat(m.ID, nil, &requestError{codeInvalidParams, "document not open and unreadable: " + readErr.Error()})
			return
		}
		content = b
	}

	edits, err := s.FormatFile(ctx, absPath, content)
	if err != nil {
		s.replyFormat(m.ID, nil, &requestError{codeRequestFailed, err.Error()})
		return
	}
	s.replyFormat(m.ID, edits, nil)
}

type requestError struct {
	code int
	msg  string
}

// replyFormat answers a formatting request. A cancelled one is answered
// RequestCancelled, whatever it ended with: vscode-languageclient drops that
// silently, and pops up an error for any other code. A format that settled is
// no longer cancellable, so its result stands.
func (s *Server) replyFormat(id json.RawMessage, edits []TextEdit, failure *requestError) {
	switch {
	case s.stopRequested():
		_ = s.conn.replyError(id, codeRequestCancelled, "request cancelled")
	case failure != nil:
		_ = s.conn.replyError(id, failure.code, failure.msg)
	default:
		_ = s.conn.reply(id, edits)
	}
}
