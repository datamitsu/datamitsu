package lsp

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/cache"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/ui"
	"github.com/datamitsu/datamitsu/internal/uievent"
	"github.com/shamaton/msgpack/v2"
)

// recordingSink captures the typed events the server emits. The sink is
// process-global, so tests that install one must not run in parallel.
type recordingSink struct {
	mu     sync.Mutex
	events []uievent.Event
}

func (r *recordingSink) Emit(e uievent.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recordingSink) all() []uievent.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]uievent.Event(nil), r.events...)
}

func (r *recordingSink) ofType(typ uievent.Type) []uievent.Event {
	var out []uievent.Event
	for _, e := range r.all() {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func captureEvents(t *testing.T) *recordingSink {
	t.Helper()
	sink := &recordingSink{}
	ui.SetEventSink(sink, true)
	t.Cleanup(func() { ui.SetEventSink(nil, false) })
	return sink
}

func shellApp(script string) binmanager.App {
	return binmanager.App{Shell: &binmanager.AppConfigShell{Name: "sh", Args: []string{"-c", script, "sh"}}}
}

func fixOp(app string, priority int) map[config.OperationType]config.ToolOperation {
	return map[config.OperationType]config.ToolOperation{config.OpFix: {
		App:      app,
		Scope:    config.ToolScopePerFile,
		Args:     []string{"{file}"},
		Globs:    []string{"**/*.txt"},
		Priority: priority,
	}}
}

// formatConfig has three fix groups on *.txt: first appends "1", then a group
// with a failing tool and a vetoed one, then last appends "3".
func formatConfig() *config.Config {
	vetoed := fixOp("append-2", 1)
	op := vetoed[config.OpFix]
	op.LSP = new(false)
	vetoed[config.OpFix] = op

	return &config.Config{
		Apps: binmanager.MapOfApps{
			"append-1": shellApp(`printf 1 >> "$1"`),
			"append-2": shellApp(`printf 2 >> "$1"`),
			"append-3": shellApp(`printf 3 >> "$1"`),
			"boom":     shellApp(`echo "cannot format $1" >&2; exit 3`),
		},
		Tools: config.MapOfTools{
			"first":   {Name: "first", Operations: fixOp("append-1", 0)},
			"failing": {Name: "failing", Operations: fixOp("boom", 1)},
			"vetoed":  {Name: "vetoed", Operations: vetoed},
			"last":    {Name: "last", Operations: fixOp("append-3", 2)},
		},
	}
}

func newFormatServer(t *testing.T) (s *Server, root, file string) {
	t.Helper()
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	file = filepath.Join(root, "note.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s = NewServer(strings.NewReader(""), io.Discard, formatConfig(), root)
	t.Cleanup(func() {
		if s.cache != nil {
			s.cache.Shutdown()
		}
	})
	return s, root, file
}

// One save is one format phase: a tool_run pair per task, an error event for
// the failing one, a notice naming what the policy left out, and a done event
// that adds it all up.
func TestFormatFileEvents(t *testing.T) {
	sink := captureEvents(t)
	s, _, file := newFormatServer(t)

	edits, err := s.FormatFile(context.Background(), file, []byte("x"))
	if err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	if len(edits) != 0 {
		t.Errorf("clean buffer returned %d edits, want none", len(edits))
	}
	if got, _ := os.ReadFile(file); string(got) != "x13" {
		t.Errorf("file = %q, want %q: first and last ran, the vetoed tool did not", got, "x13")
	}

	phases := sink.ofType(uievent.TypePhase)
	if len(phases) != 1 || phases[0].Op != formatOp || phases[0].Status != uievent.StatusStart ||
		!strings.HasPrefix(phases[0].OpID, "fmt-") {
		t.Fatalf("phase events = %+v, want one format start with an fmt- op id", phases)
	}
	opID := phases[0].OpID

	var started, finished []string
	for _, e := range sink.ofType(uievent.TypeToolRun) {
		if !strings.HasPrefix(e.OpID, opID+":"+e.Tool+":") {
			t.Errorf("tool_run op id %q, want %s:<tool>:<dir>", e.OpID, opID)
		}
		switch e.Status {
		case uievent.StatusStart:
			started = append(started, e.Tool)
		case uievent.StatusDone, uievent.StatusFail:
			if e.Success == nil || *e.Success != (e.Status == uievent.StatusDone) {
				t.Errorf("tool_run %s: success %v does not match status %q", e.Tool, e.Success, e.Status)
			}
			finished = append(finished, e.Tool)
		}
	}
	if strings.Join(started, ",") != "first,failing,last" || strings.Join(finished, ",") != "first,failing,last" {
		t.Errorf("tool_run starts %v, terminals %v; want first, failing, last in order", started, finished)
	}

	errs := sink.ofType(uievent.TypeError)
	if len(errs) != 1 || errs[0].Tool != "failing" || !strings.Contains(errs[0].Msg, "cannot format") {
		t.Errorf("error events = %+v, want one for failing carrying its output", errs)
	}

	var sawVeto bool
	for _, e := range sink.ofType(uievent.TypeLog) {
		if e.Level == uievent.LevelInfo && strings.Contains(e.Msg, "vetoed (lsp: false in config)") {
			sawVeto = true
			if e.OpID != opID {
				t.Errorf("left-out notice op id %q, want the request's %q", e.OpID, opID)
			}
		}
	}
	if !sawVeto {
		t.Errorf("no notice named the vetoed tool; logs = %+v", sink.ofType(uievent.TypeLog))
	}

	done := sink.ofType(uievent.TypeDone)
	if len(done) != 1 {
		t.Fatalf("done events = %+v, want exactly one", done)
	}
	d := done[0]
	if d.OpID != opID || d.Op != formatOp || d.Status != uievent.StatusFail || d.Success == nil || *d.Success {
		t.Errorf("done = %+v, want a failed format done on %s", d, opID)
	}
	if d.Tools != 3 || d.Runs != 3 || d.Failed != 1 || d.Skipped != 1 {
		t.Errorf("done tools/runs/failed/skipped = %d/%d/%d/%d, want 3/3/1/1", d.Tools, d.Runs, d.Failed, d.Skipped)
	}
}

// The watchdog stops at a group boundary and says so; the response still
// reflects the file on disk and the done event counts the tasks it skipped.
func TestFormatFileWatchdog(t *testing.T) {
	sink := captureEvents(t)
	s, _, file := newFormatServer(t)
	s.policy.TimeoutMs = 50
	s.now = steppingClock(time.Hour)

	// A dirty buffer: persisted, then the diff buffer -> disk comes back.
	edits, err := s.FormatFile(context.Background(), file, []byte("y\n"))
	if err != nil {
		t.Fatalf("FormatFile: %v", err)
	}
	if got, _ := os.ReadFile(file); string(got) != "y\n1" {
		t.Errorf("file = %q, want only the first group applied", got)
	}
	if len(edits) == 0 {
		t.Error("a dirty buffer must get the diff to what is on disk")
	}

	var notice string
	for _, e := range sink.ofType(uievent.TypeLog) {
		if e.Level == uievent.LevelWarn {
			notice = e.Msg
		}
	}
	for _, want := range []string{"note.txt", "1 of 3", "50ms", "did not run: failing, last"} {
		if !strings.Contains(notice, want) {
			t.Errorf("watchdog notice %q does not mention %q", notice, want)
		}
	}

	done := sink.ofType(uievent.TypeDone)
	if len(done) != 1 || done[0].Runs != 1 || done[0].Skipped != 3 || done[0].Success == nil || !*done[0].Success {
		t.Errorf("done = %+v, want 1 run, 3 skipped (vetoed + 2 cut by the watchdog), success", done)
	}
}

// unitFixConfig has one project-wide fix that takes no file argument, in a
// project marked by marker.mod, under the project's own fix policy.
func unitFixConfig(project config.WidenTo, globs []string) *config.Config {
	return &config.Config{
		Apps:         binmanager.MapOfApps{"append-u": shellApp(`printf U >> a.txt`)},
		ProjectTypes: config.MapOfProjectTypes{"marked": {Markers: []string{"**/marker.mod"}}},
		Tools: config.MapOfTools{"unit-fmt": {Name: "unit-fmt", Operations: map[config.OperationType]config.ToolOperation{
			config.OpFix: {App: "append-u", Scope: config.ToolScopePerProject, Globs: globs},
		}}},
		Execution: &config.Execution{WidenTo: map[config.OperationType]config.WidenTo{config.OpFix: project}},
	}
}

// What the project's execution.widenTo.fix keeps out of a save is reported like
// anything else the policy leaves out, through the real planner: a tool missing
// from both the notice and the count leaves the editor with nothing to explain
// why no fixer ran.
func TestFormatFileReportsWhatTheProjectPolicyLeavesOut(t *testing.T) {
	txtGlobs := []string{"**/*.txt"}
	tests := []struct {
		name       string
		project    config.WidenTo
		globs      []string
		tools      map[string]bool
		wantFile   string
		wantRuns   int
		wantReason string
	}{
		{name: "project target", project: config.WidenToTarget, globs: txtGlobs, wantFile: "x", wantReason: reasonProjectTarget},
		{
			name: "project target beats the user's opt-in", project: config.WidenToTarget, globs: txtGlobs,
			tools: map[string]bool{"unit-fmt": true}, wantFile: "x", wantReason: reasonProjectTarget,
		},
		{name: "no globs under project target", project: config.WidenToTarget, wantFile: "x", wantReason: reasonNoGlobs},
		{name: "project unit runs it", project: config.WidenToUnit, globs: txtGlobs, wantFile: "xU", wantRuns: 1},
		{name: "project repo still stops at the unit", project: config.WidenToRepo, globs: txtGlobs, wantFile: "xU", wantRuns: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink := captureEvents(t)
			t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			pkg := filepath.Join(root, "pkg")
			file := filepath.Join(pkg, "a.txt")
			if err := os.MkdirAll(pkg, 0o755); err != nil {
				t.Fatal(err)
			}
			for path, content := range map[string]string{filepath.Join(pkg, "marker.mod"): "", file: "x"} {
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			s := NewServer(strings.NewReader(""), io.Discard, unitFixConfig(tt.project, tt.globs), root)
			t.Cleanup(func() {
				if s.cache != nil {
					s.cache.Shutdown()
				}
			})
			s.policy = formatPolicy{WidenTo: config.WidenToUnit, Tools: tt.tools}

			if _, err := s.FormatFile(context.Background(), file, []byte("x")); err != nil {
				t.Fatalf("FormatFile: %v", err)
			}
			if got, _ := os.ReadFile(file); string(got) != tt.wantFile {
				t.Errorf("a.txt = %q, want %q", got, tt.wantFile)
			}

			logs := sink.ofType(uievent.TypeLog)
			notices := make([]string, 0, len(logs))
			for _, e := range logs {
				notices = append(notices, e.Msg)
			}
			wantNotice := "unit-fmt (" + tt.wantReason + ")"
			if found := slices.ContainsFunc(notices, func(m string) bool { return strings.Contains(m, wantNotice) }); found != (tt.wantReason != "") {
				t.Errorf("notices %q: mention of %q = %v, want %v", notices, wantNotice, found, tt.wantReason != "")
			}

			wantSkipped := 0
			if tt.wantReason != "" {
				wantSkipped = 1
			}
			done := sink.ofType(uievent.TypeDone)
			if len(done) != 1 || done[0].Runs != tt.wantRuns || done[0].Skipped != wantSkipped {
				t.Errorf("done = %+v, want runs %d, skipped %d", done, tt.wantRuns, wantSkipped)
			}
		})
	}
}

// A request that fails still closes its phase.
func TestFormatFileClosesThePhaseOnError(t *testing.T) {
	sink := captureEvents(t)
	s, root, _ := newFormatServer(t)
	// Remove the root so planning cannot walk it.
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}

	if _, err := s.FormatFile(context.Background(), filepath.Join(root, "note.txt"), []byte("x")); err == nil {
		t.Fatal("FormatFile succeeded on a vanished workspace")
	}
	done := sink.ofType(uievent.TypeDone)
	if len(done) != 1 || done[0].Status != uievent.StatusFail || done[0].Success == nil || *done[0].Success {
		t.Errorf("done = %+v, want one failed done closing the phase", done)
	}
}

// After a config edit the CLI owns the cache file under a new key. The editor
// session must neither overwrite it nor stay silent — once.
func TestFormatFileYieldsToAForeignCache(t *testing.T) {
	sink := captureEvents(t)
	s, root, file := newFormatServer(t)

	other := *formatConfig()
	other.IgnoreRules = []string{"changed"}
	cli, err := cache.NewCache(env.GetCachePath(), root, other, nil, logger.Logger)
	if err != nil {
		t.Fatal(err)
	}
	if err := cli.Save(); err != nil {
		t.Fatal(err)
	}
	cacheFile := filepath.Join(env.GetCachePath(), "projects", env.HashProjectPath(root), "toolstate.msgpack")
	before, err := os.ReadFile(cacheFile)
	if err != nil {
		t.Fatalf("read the CLI's cache file: %v", err)
	}

	for range 2 {
		if _, err := s.FormatFile(context.Background(), file, []byte("x")); err != nil {
			t.Fatalf("FormatFile: %v", err)
		}
	}

	after, _ := os.ReadFile(cacheFile)
	if string(after) != string(before) {
		t.Error("the editor session overwrote a cache file keyed by another configuration")
	}
	var notices int
	for _, e := range sink.ofType(uievent.TypeLog) {
		if e.Level == uievent.LevelWarn && strings.Contains(e.Msg, "different configuration") {
			notices++
		}
	}
	if notices != 1 {
		t.Errorf("foreign-cache notices = %d, want exactly one per session", notices)
	}
}

// An editor running another datamitsu than the CLI is a normal setup: say so at
// info, without the restart advice a warning would pop up every session.
func TestFormatFileNamesAForeignCacheVersion(t *testing.T) {
	sink := captureEvents(t)
	s, root, file := newFormatServer(t)

	cli, err := cache.NewCache(env.GetCachePath(), root, *formatConfig(), nil, logger.Logger)
	if err != nil {
		t.Fatal(err)
	}
	if err := cli.Save(); err != nil {
		t.Fatal(err)
	}
	cli.Shutdown()
	cacheFile := filepath.Join(env.GetCachePath(), "projects", env.HashProjectPath(root), "toolstate.msgpack")
	raw, err := os.ReadFile(cacheFile)
	if err != nil {
		t.Fatal(err)
	}
	var onDisk cache.File
	if err := msgpack.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	onDisk.InvalidationKey = "written-by-another-binary"
	onDisk.Version = "0.0.1-other"
	if raw, err = msgpack.Marshal(onDisk); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cacheFile, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := s.FormatFile(context.Background(), file, []byte("x")); err != nil {
		t.Fatalf("FormatFile: %v", err)
	}

	var info, warn int
	for _, e := range sink.ofType(uievent.TypeLog) {
		switch {
		case e.Level == uievent.LevelInfo && strings.Contains(e.Msg, "written by datamitsu 0.0.1-other"):
			info++
		case e.Level == uievent.LevelWarn && strings.Contains(e.Msg, "execution cache"):
			warn++
		}
	}
	if info != 1 || warn != 0 {
		t.Errorf("version notices: info = %d, warn = %d; want one info and no warning", info, warn)
	}
}
