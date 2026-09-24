package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/cache"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/uievent"
	"github.com/shamaton/msgpack/v2"
)

// fakeLoader serves every directory as its own root, unless rootErr is set,
// and loads the configuration named by the first word of cfgFile: a letter
// gives a config whose one fix appends that letter to *.txt, "broken" fails.
type fakeLoader struct {
	cfgFile  string
	rootErr  error
	rootDirs []string
	loads    int
	// duringLoad runs once, after the next load has read cfgFile.
	duringLoad func()
}

func (f *fakeLoader) Watch(string) []string { return []string{f.cfgFile} }

func (f *fakeLoader) Root(_ context.Context, dir string) (string, error) {
	f.rootDirs = append(f.rootDirs, dir)
	if f.rootErr != nil {
		return "", f.rootErr
	}
	return dir, nil
}

func (f *fakeLoader) Load(_ context.Context, _ string) (*config.Config, []string, error) {
	f.loads++
	watch := []string{f.cfgFile}
	raw, err := os.ReadFile(f.cfgFile)
	if during := f.duringLoad; during != nil {
		f.duringLoad = nil
		during()
	}
	if err != nil {
		return nil, watch, err
	}
	letter := strings.TrimSpace(string(raw))
	if letter == "broken" {
		return nil, watch, errors.New("SyntaxError: Unexpected end of input")
	}
	return appendConfig(letter), watch, nil
}

func appendConfig(letter string) *config.Config {
	return &config.Config{
		Apps:  binmanager.MapOfApps{"append": shellApp(`printf ` + letter + ` >> "$1"`)},
		Tools: config.MapOfTools{"append-" + letter: {Name: "append-" + letter, Operations: fixOp("append", 0)}},
	}
}

// loaderServer is a server whose loader reads cfg, which starts as content.
func loaderServer(t *testing.T, content string) (s *Server, loader *fakeLoader, buf *bytes.Buffer) {
	t.Helper()
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	loader = &fakeLoader{cfgFile: filepath.Join(t.TempDir(), "config")}
	writeFile(t, loader.cfgFile, content)
	buf = &bytes.Buffer{}
	s = New(strings.NewReader(""), buf, loader, "")
	t.Cleanup(s.closeSession)
	return s, loader, buf
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func tempRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// lastResponse decodes the last response written to buf.
func lastResponse(t *testing.T, buf *bytes.Buffer) map[string]json.RawMessage {
	t.Helper()
	frames := allFrames(t, buf.Bytes())
	if len(frames) == 0 {
		t.Fatal("no response written")
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(frames[len(frames)-1], &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func logsAt(sink *recordingSink, level string) []string {
	var msgs []string
	for _, e := range sink.ofType(uievent.TypeLog) {
		if e.Level == level {
			msgs = append(msgs, e.Msg)
		}
	}
	return msgs
}

func countContaining(msgs []string, part string) int {
	n := 0
	for _, m := range msgs {
		if strings.Contains(m, part) {
			n++
		}
	}
	return n
}

func initializeWith(t *testing.T, s *Server, params any) {
	t.Helper()
	s.handle(context.Background(), msg(t, "1", "initialize", params))
}

func formatOnce(t *testing.T, s *Server, buf *bytes.Buffer, path string) map[string]json.RawMessage {
	t.Helper()
	s.handle(context.Background(), msg(t, "9", "textDocument/formatting", formattingParams{
		TextDocument: textDocumentIdentifier{URI: "file://" + path},
	}))
	return lastResponse(t, buf)
}

// The workspace comes from initialize — the first workspace folder, then
// rootUri, then rootPath — and the launch directory only when it names none.
// The root the server settles on is echoed back.
func TestInitializeTakesTheRootFromTheClient(t *testing.T) {
	folder, rootURI, rootPath, launch := tempRoot(t), tempRoot(t), tempRoot(t), tempRoot(t)
	tests := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{
			name: "workspace folder first",
			params: map[string]any{
				"workspaceFolders": []map[string]string{{"uri": "file://" + folder, "name": "f"}},
				"rootUri":          "file://" + rootURI, "rootPath": rootPath,
			},
			want: folder,
		},
		{name: "then rootUri", params: map[string]any{"workspaceFolders": nil, "rootUri": "file://" + rootURI, "rootPath": rootPath}, want: rootURI},
		{name: "then rootPath", params: map[string]any{"rootUri": nil, "rootPath": rootPath}, want: rootPath},
		{name: "then the launch directory", params: map[string]any{}, want: launch},
		{name: "a malformed location is skipped", params: map[string]any{"rootUri": 5, "rootPath": rootPath}, want: rootPath},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			captureEvents(t)
			s, loader, buf := loaderServer(t, "a")
			s.launchDir = launch
			initializeWith(t, s, tt.params)

			if len(loader.rootDirs) != 1 || loader.rootDirs[0] != tt.want {
				t.Errorf("root resolved from %v, want [%s]", loader.rootDirs, tt.want)
			}
			if got := initializeRoot(t, buf); got != tt.want {
				t.Errorf("experimental.datamitsu.root = %q, want %q", got, tt.want)
			}
		})
	}
}

func initializeRoot(t *testing.T, buf *bytes.Buffer) string {
	t.Helper()
	frame := readFrame(t, bufio.NewReader(bytes.NewReader(buf.Bytes())))
	var res initializeResult
	if err := json.Unmarshal(frame["result"], &res); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	return res.Capabilities.Experimental.Datamitsu.Root
}

// Nothing to serve never fails initialize or kills the server: the client
// hears why once at initialize and once more on the first save, and every
// format gets no edits rather than an error that would pop up on each save.
func TestInitializeWithoutAConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(loader *fakeLoader)
		params  func(root string) any
		wantMsg string
	}{
		{
			name:    "not in a repository",
			setup:   func(l *fakeLoader) { l.rootErr = errors.New("determine git root: exit status 128") },
			params:  func(root string) any { return map[string]any{"rootUri": "file://" + root} },
			wantMsg: "exit status 128",
		},
		{
			name:    "not a file uri",
			params:  func(string) any { return map[string]any{"rootUri": "vscode-remote://host/x"} },
			wantMsg: `unsupported uri scheme "vscode-remote"`,
		},
		{
			name:    "the configuration fails to load",
			setup:   func(l *fakeLoader) { writeFile(t, l.cfgFile, "broken") },
			params:  func(root string) any { return map[string]any{"rootUri": "file://" + root} },
			wantMsg: "the configuration failed to load: SyntaxError",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink := captureEvents(t)
			s, loader, buf := loaderServer(t, "a")
			if tt.setup != nil {
				tt.setup(loader)
			}
			root := tempRoot(t)
			file := filepath.Join(root, "note.txt")
			writeFile(t, file, "x")

			initializeWith(t, s, tt.params(root))
			if _, ok := lastResponse(t, buf)["result"]; !ok {
				t.Fatal("initialize failed")
			}
			for range 3 {
				if got := string(formatOnce(t, s, buf, file)["result"]); got != "[]" {
					t.Errorf("format result = %s, want []", got)
				}
			}

			if errs := logsAt(sink, uievent.LevelError); len(errs) != 2 || countContaining(errs, tt.wantMsg) != 2 {
				t.Errorf("error notices = %q, want two naming %q: at initialize and on the first save", errs, tt.wantMsg)
			}
			if got := readFile(t, file); got != "x" {
				t.Errorf("note.txt = %q, want untouched", got)
			}
		})
	}
}

// The configuration is read again before a format when one of its files
// changed. Unchanged files cost no load; a failed load keeps the previous
// configuration formatting and is reported once, not retried on every save
// until its bytes change; and a server that started with no configuration picks
// one up as soon as it loads.
func TestFormatReloadsAChangedConfiguration(t *testing.T) {
	type step struct {
		config    string // written before the format; "" leaves it as is
		wantLoads int
		wantAdded string // what the format appended
		wantInfo  int    // "configuration reloaded" notices so far
		wantWarn  int    // failed-reload warnings so far
	}
	tests := []struct {
		name    string
		initial string
		steps   []step
	}{
		{
			name:    "changed, unchanged, failed, failed again, fixed",
			initial: "a",
			steps: []step{
				{wantLoads: 1, wantAdded: "a"},
				{config: "b", wantLoads: 2, wantAdded: "b", wantInfo: 1},
				{wantLoads: 2, wantAdded: "b", wantInfo: 1},
				{config: "broken", wantLoads: 3, wantAdded: "b", wantInfo: 1, wantWarn: 1},
				{wantLoads: 3, wantAdded: "b", wantInfo: 1, wantWarn: 1},
				{config: "c", wantLoads: 4, wantAdded: "c", wantInfo: 2, wantWarn: 1},
			},
		},
		{
			name:    "no configuration until it is fixed",
			initial: "broken",
			steps: []step{
				{wantLoads: 1},
				{config: "a", wantLoads: 2, wantAdded: "a", wantInfo: 1},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink := captureEvents(t)
			s, loader, buf := loaderServer(t, tt.initial)
			root := tempRoot(t)
			file := filepath.Join(root, "note.txt")
			initializeWith(t, s, map[string]any{"rootUri": "file://" + root})

			for i, st := range tt.steps {
				if st.config != "" {
					writeFile(t, loader.cfgFile, st.config)
				}
				// New content each time, so the execution cache never skips the tool.
				before := strings.Repeat("x", i+1)
				writeFile(t, file, before)
				formatOnce(t, s, buf, file)

				if loader.loads != st.wantLoads {
					t.Errorf("step %d: %d loads, want %d", i, loader.loads, st.wantLoads)
				}
				if got := readFile(t, file); got != before+st.wantAdded {
					t.Errorf("step %d: note.txt = %q, want %q", i, got, before+st.wantAdded)
				}
				if got := countContaining(logsAt(sink, uievent.LevelInfo), "configuration reloaded"); got != st.wantInfo {
					t.Errorf("step %d: %d reload notices, want %d", i, got, st.wantInfo)
				}
				if got := countContaining(logsAt(sink, uievent.LevelWarn), "failed to load"); got != st.wantWarn {
					t.Errorf("step %d: %d failed-reload warnings, want %d", i, got, st.wantWarn)
				}
			}
		})
	}
}

// A configuration edited while a load evaluates it — a checkout at startup, an
// install during a reload — is loaded again by the next format: the session
// never takes bytes it did not read as the ones it was built from.
func TestAnEditDuringALoadIsLoadedAgain(t *testing.T) {
	for _, reload := range []bool{false, true} {
		name := "first load"
		if reload {
			name = "reload"
		}
		t.Run(name, func(t *testing.T) {
			captureEvents(t)
			s, loader, buf := loaderServer(t, "a")
			root := tempRoot(t)
			file := filepath.Join(root, "note.txt")
			edit := func() { writeFile(t, loader.cfgFile, "z") }
			if !reload {
				loader.duringLoad = edit
			}
			initializeWith(t, s, map[string]any{"rootUri": "file://" + root})
			if reload {
				loader.duringLoad = edit
				writeFile(t, loader.cfgFile, "b")
				writeFile(t, file, "x")
				formatOnce(t, s, buf, file)
			}

			writeFile(t, file, "y")
			formatOnce(t, s, buf, file)
			if got := readFile(t, file); got != "yz" {
				t.Errorf("note.txt = %q, want the configuration written during the load applied", got)
			}
		})
	}
}

// A reload takes over the cache file the session it replaced wrote: that key is
// the server's own earlier one, not a newer configuration to yield to, so no
// warning appears and the cache keeps warming.
func TestReloadTakesOverItsOwnCache(t *testing.T) {
	sink := captureEvents(t)
	s, loader, buf := loaderServer(t, "a")
	root := tempRoot(t)
	file := filepath.Join(root, "note.txt")
	initializeWith(t, s, map[string]any{"rootUri": "file://" + root})

	for i, next := range []string{"", "b", "c"} {
		if next != "" {
			writeFile(t, loader.cfgFile, next)
		}
		writeFile(t, file, strings.Repeat("x", i+1))
		formatOnce(t, s, buf, file)
	}

	if warns := logsAt(sink, uievent.LevelWarn); len(warns) != 0 {
		t.Errorf("warnings = %q, want none", warns)
	}
	raw, err := os.ReadFile(filepath.Join(env.GetCachePath(), "projects", env.HashProjectPath(root), "toolstate.msgpack"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk cache.File
	if err := msgpack.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	if want := s.loaded.cache.InvalidationKey(); onDisk.InvalidationKey != want {
		t.Errorf("cache file key = %q, want the reloaded session's %q", onDisk.InvalidationKey, want)
	}
	if len(onDisk.Entries) == 0 {
		t.Error("the reloaded session wrote no cache entries")
	}
}

// A reload resolves the format policy again: an opt-in for a tool the new
// configuration adds is honored, and one it removed is warned about.
func TestReloadResolvesThePolicyAgain(t *testing.T) {
	sink := captureEvents(t)
	s, loader, buf := loaderServer(t, "a")
	root := tempRoot(t)
	file := filepath.Join(root, "note.txt")
	writeFile(t, file, "x")
	initializeWith(t, s, map[string]any{
		"rootUri":               "file://" + root,
		"initializationOptions": map[string]any{"format": map[string]any{"tools": map[string]bool{"append-b": true}}},
	})
	if got := countContaining(logsAt(sink, uievent.LevelWarn), "format.tools.append-b: unknown tool"); got != 1 {
		t.Fatalf("%d unknown-tool warnings at initialize, want 1", got)
	}

	writeFile(t, loader.cfgFile, "b")
	formatOnce(t, s, buf, file)
	if !s.policy.Tools["append-b"] {
		t.Errorf("policy tools after the reload = %v, want append-b opted in", s.policy.Tools)
	}
	infos := logsAt(sink, uievent.LevelInfo)
	if len(infos) < 2 || infos[len(infos)-2] != "configuration reloaded" ||
		!strings.HasPrefix(infos[len(infos)-1], "format policy:") {
		t.Errorf("info notices = %q, want the reload followed by the policy line", infos)
	}
}

// A document outside the served root is refused before anything is planned or
// installed for it, with no edits and one notice per document.
func TestFormatRefusesADocumentOutsideTheRoot(t *testing.T) {
	sink := captureEvents(t)
	s, _, buf := loaderServer(t, "a")
	root := tempRoot(t)
	outside := filepath.Join(tempRoot(t), "note.txt")
	writeFile(t, outside, "x")
	initializeWith(t, s, map[string]any{"rootUri": "file://" + root})

	for range 2 {
		if got := string(formatOnce(t, s, buf, outside)["result"]); got != "[]" {
			t.Errorf("format result = %s, want []", got)
		}
	}
	if phases := sink.ofType(uievent.TypePhase); len(phases) != 0 {
		t.Errorf("phase events = %+v: the refused document reached the formatter", phases)
	}
	if got := countContaining(logsAt(sink, uievent.LevelInfo), "format "+outside+": outside "+root); got != 1 {
		t.Errorf("%d outside-root notices, want 1", got)
	}
	if got := readFile(t, outside); got != "x" {
		t.Errorf("outside file = %q, want untouched", got)
	}
}

// A workspace opened through a symlink names its documents through the link,
// while the root resolves to the real directory: both are compared resolved.
// A symlinked document keeps its link when an unsaved buffer is persisted.
func TestFormatThroughASymlinkedWorkspace(t *testing.T) {
	captureEvents(t)
	s, _, buf := loaderServer(t, "a")
	realDir := tempRoot(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	writeFile(t, filepath.Join(realDir, "note.txt"), "x")
	if err := os.Symlink("note.txt", filepath.Join(realDir, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	initializeWith(t, s, map[string]any{"rootUri": "file://" + link})
	if got := initializeRoot(t, buf); got != realDir {
		t.Fatalf("served root = %q, want the resolved %q", got, realDir)
	}

	s.handle(context.Background(), msg(t, "", "textDocument/didOpen", didOpenParams{
		TextDocument: textDocumentItem{URI: "file://" + filepath.Join(link, "alias.txt"), Text: "y"},
	}))
	res := formatOnce(t, s, buf, filepath.Join(link, "alias.txt"))
	if _, ok := res["error"]; ok {
		t.Fatalf("format through the link failed: %s", res["error"])
	}
	if got := readFile(t, filepath.Join(realDir, "note.txt")); got != "ya" {
		t.Errorf("note.txt = %q, want the persisted buffer, formatted", got)
	}
	if info, err := os.Lstat(filepath.Join(realDir, "alias.txt")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("alias.txt is no longer a symlink (%v, %v)", info, err)
	}
}

// A document that is a dangling symlink is judged, and written, by the file it
// names: one inside the root is created and the link kept, one outside it is
// refused before anything runs.
func TestFormatThroughADanglingSymlink(t *testing.T) {
	sink := captureEvents(t)
	s, _, buf := loaderServer(t, "a")
	root := tempRoot(t)
	outside := tempRoot(t)
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	inLink, outLink := filepath.Join(root, "alias.txt"), filepath.Join(root, "escape.txt")
	if err := os.Symlink(filepath.Join("sub", "note.txt"), inLink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "note.txt"), outLink); err != nil {
		t.Fatal(err)
	}
	initializeWith(t, s, map[string]any{"rootUri": "file://" + root})

	for _, link := range []string{inLink, outLink} {
		s.handle(context.Background(), msg(t, "", "textDocument/didOpen", didOpenParams{
			TextDocument: textDocumentItem{URI: "file://" + link, Text: "y"},
		}))
		if res := formatOnce(t, s, buf, link); res["error"] != nil {
			t.Fatalf("format %s failed: %s", link, res["error"])
		}
		if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s is no longer a symlink (%v, %v)", link, info, err)
		}
	}
	if got := readFile(t, filepath.Join(root, "sub", "note.txt")); got != "ya" {
		t.Errorf("sub/note.txt = %q, want the persisted buffer, formatted", got)
	}
	if _, err := os.Stat(filepath.Join(outside, "note.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the target outside the root was written (%v)", err)
	}
	if got := countContaining(logsAt(sink, uievent.LevelInfo), "format "+filepath.Join(outside, "note.txt")+": outside "+root); got != 1 {
		t.Errorf("%d outside-root notices, want 1", got)
	}
}

// What a rename would silently replace is refused, and left as it was: a file
// this user may not write, and anything that is not a regular file.
func TestPersistBufferRefuses(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, path string)
	}{
		{name: "read-only file", setup: func(t *testing.T, path string) {
			t.Helper()
			if os.Geteuid() == 0 {
				t.Skip("root writes a read-only file")
			}
			writeFile(t, path, "old")
			if err := os.Chmod(path, 0o444); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlink", setup: func(t *testing.T, path string) {
			t.Helper()
			if err := os.Symlink(path, path); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "a.ts")
			tt.setup(t, path)
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}

			if err := persistBuffer(path, []byte("new")); err == nil {
				t.Fatal("persistBuffer succeeded")
			}
			after, err := os.Lstat(path)
			if err != nil || after.Mode() != before.Mode() || !after.ModTime().Equal(before.ModTime()) {
				t.Errorf("a.ts changed: %v -> %v (%v)", before.Mode(), after.Mode(), err)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 1 {
				t.Errorf("directory holds %d entries, want only a.ts", len(entries))
			}
		})
	}
}

// The editor's buffer replaces the file in one rename: the file keeps its
// mode, and no temporary file is left behind.
func TestPersistBuffer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "script.sh")
	writeFile(t, path, "old")
	if err := os.Chmod(path, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := persistBuffer(path, []byte("new")); err != nil {
		t.Fatalf("persistBuffer: %v", err)
	}
	if got := readFile(t, path); got != "new" {
		t.Errorf("content = %q, want new", got)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o750 {
		t.Errorf("mode = %v (%v), want 0750", info.Mode().Perm(), err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, want only the file", len(entries))
	}
}
