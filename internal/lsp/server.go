package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"go.uber.org/zap"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/runtimemanager"
	"github.com/datamitsu/datamitsu/internal/textdiff"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

var log = logger.Logger.With(zap.Namespace("lsp"))

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

	docs map[string][]byte // open documents: uri -> current full text

	initialized      bool
	shutdownReceived bool
	exitCode         int
}

// NewServer builds a formatting-only server over r/w using cfg. It assembles its
// OWN lightweight planner+binManager+executor (no cache, no parser, no UI) so a
// format request never writes files, parses diagnostics, or prints to stdout.
//
// The planner's cwd is set to the git root, NOT the process launch directory: an
// editor selects which file to format from anywhere in the workspace, so the
// CLI's CWD-subtree restriction (which would silently drop files outside the
// launch dir) is wrong here — any file under the repo root is formattable.
func NewServer(r io.Reader, w io.Writer, cfg *config.Config, root string) *Server {
	rm := runtimemanager.New(cfg.Runtimes)
	binMgr := binmanager.New(cfg.Apps, cfg.Bundles, rm)

	planner := tooling.NewPlanner(root, root, nil, cfg.Tools, cfg.ProjectTypes, cfg.IgnoreRules)
	planner.SetPlatformChecker(binMgr)

	return &Server{
		conn:     newConn(r, w),
		root:     root,
		planner:  planner,
		binMgr:   binMgr,
		executor: tooling.NewExecutor(root, false, false, binMgr, nil),
		docs:     make(map[string][]byte),
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

// FormatFile turns one file's content into LSP TextEdits by composing every
// applicable fix formatter for the file (the output of tool N feeds tool N+1),
// then diffing original->final in core. It NEVER reads or writes the real file.
//
// Two formatter shapes are supported, both fed the in-memory buffer (so an
// unsaved editor buffer formats correctly):
//   - stdin->stdout tools run fully in memory (FormatContent), no disk touch;
//   - in-place tools (e.g. `--write {file}`) run against a private temp copy
//     OUTSIDE the repo (FormatFileInPlace); the {file}/{files} placeholder is
//     redirected to that copy while the working dir stays at the real project so
//     config discovery is unaffected.
//
// A tool that is neither output:stdout nor references {file}/{files} cannot be
// redirected off the real tree (it would format the whole project / real files),
// so it is skipped. A single tool failing is logged and skipped rather than
// aborting the whole format. Returns an empty (non-nil) slice when nothing
// applies or the content is already formatted.
func (s *Server) FormatFile(ctx context.Context, absPath string, content []byte) ([]TextEdit, error) {
	plan, err := s.planner.Plan(ctx, config.OpFix, []string{absPath}, nil)
	if err != nil {
		return nil, fmt.Errorf("plan fix for %s: %w", absPath, err)
	}

	// Collect formattable fix tasks in plan order (priority groups, then task
	// order). inPlace records how each runs so the compose loop need not re-derive
	// it; apps is the distinct set to install.
	type formatTask struct {
		task    tooling.Task
		inPlace bool
	}
	var tasks []formatTask
	seen := make(map[string]struct{})
	var apps []string
	for _, group := range plan.Groups {
		for _, task := range group.Tasks {
			stdoutMode := task.OpConfig.Output == config.ToolOutputStdout && task.OpConfig.Input == config.ToolInputStdin
			inPlace := !stdoutMode && hasFilePlaceholder(task.OpConfig.Args)
			if !stdoutMode && !inPlace {
				log.Debug("lsp format: skipping non-redirectable tool", zap.String("tool", task.ToolName))
				continue
			}
			tasks = append(tasks, formatTask{task: task, inPlace: inPlace})
			if _, dup := seen[task.OpConfig.App]; !dup {
				seen[task.OpConfig.App] = struct{}{}
				apps = append(apps, task.OpConfig.App)
			}
		}
	}
	if len(tasks) == 0 {
		return []TextEdit{}, nil
	}

	// Auto-install/verify the formatters (download progress streams to stderr
	// JSON-L). Only the apps we will actually run, not the whole fix plan.
	if err := s.binMgr.EnsureTools(ctx, apps); err != nil {
		return nil, fmt.Errorf("ensure formatters installed: %w", err)
	}

	current := content
	for _, ft := range tasks {
		var next []byte
		var fmtErr error
		if ft.inPlace {
			next, fmtErr = s.executor.FormatFileInPlace(ctx, ft.task, absPath, current)
		} else {
			next, _, fmtErr = s.executor.FormatContent(ctx, ft.task, absPath, current)
		}
		if fmtErr != nil {
			// One formatter failing must not abort the whole format: skip it and
			// keep composing from the last good content.
			log.Warn("lsp format: formatter failed, skipping", zap.String("tool", ft.task.ToolName), zap.Error(fmtErr))
			continue
		}
		current = next
	}

	return toTextEdits(textdiff.ComputeEdits(string(content), string(current)))
}

// hasFilePlaceholder reports whether any arg references the per-file placeholder
// ({file} or {files}) — i.e. the tool can be redirected from the real file to a
// temp copy for in-place formatting.
func hasFilePlaceholder(args []string) bool {
	for _, a := range args {
		if strings.Contains(a, "{file}") || strings.Contains(a, "{files}") {
			return true
		}
	}
	return false
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
	_ = s.conn.reply(m.ID, initializeResult{
		Capabilities: serverCapabilities{
			PositionEncoding: "utf-16",
			TextDocumentSync: textDocumentSyncOptions{
				OpenClose: true,
				Change:    textDocumentSyncFull,
			},
			DocumentFormattingProvider: true,
		},
		ServerInfo: serverInfo{Name: ldflags.PackageName + "-lsp", Version: ldflags.Version},
	})
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
