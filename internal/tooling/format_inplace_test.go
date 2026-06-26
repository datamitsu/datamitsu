package tooling

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

// inPlaceTask builds a per-file fix task whose tool rewrites the file it is given
// (the {file} placeholder), i.e. an in-place formatter (no stdout/stdin contract).
func inPlaceTask(appName, projectPath string) Task {
	return Task{
		ToolName:  appName,
		Operation: config.OpFix,
		OpConfig: config.ToolOperation{
			App:   appName,
			Scope: config.ToolScopePerFile,
			Batch: &batchFalse,
			Args:  []string{"{file}"},
		},
		ProjectPath: projectPath,
	}
}

func TestFormatFileInPlace_TransformWritesAndReadsBack(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir()) // keep scratch dirs under the test's temp
	dir := t.TempDir()
	appMgr := &mockAppManager{commands: map[string]*binmanager.CommandInfo{
		// In-place upper-casing tool: reads $1, writes the result back to $1.
		"upper": {Type: "shell", Command: "/bin/sh", Args: []string{
			"-c", `tr a-z A-Z < "$1" > "$1.up" && mv "$1.up" "$1"`, "sh",
		}},
	}}
	e := NewExecutor(dir, false, false, appMgr, nil)

	content := []byte("hello\nworld\n")
	got, err := e.FormatFileInPlace(context.Background(), inPlaceTask("upper", dir), filepath.Join(dir, "f.txt"), content)
	if err != nil {
		t.Fatalf("FormatFileInPlace: %v", err)
	}
	if string(got) != "HELLO\nWORLD\n" {
		t.Errorf("got %q, want %q", got, "HELLO\nWORLD\n")
	}
	// The real file must be untouched (we only ever write the temp copy).
	if _, statErr := os.Stat(filepath.Join(dir, "f.txt")); !os.IsNotExist(statErr) {
		t.Errorf("real file should not have been created/written, stat err = %v", statErr)
	}
	// No scratch dir should linger after a successful run.
	if entries, _ := os.ReadDir(formatTempBase()); len(entries) != 0 {
		t.Errorf("scratch dirs leaked: %d remain", len(entries))
	}
}

func TestFormatFileInPlace_NonZeroExitStillReadsBack(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	dir := t.TempDir()
	appMgr := &mockAppManager{commands: map[string]*binmanager.CommandInfo{
		// A fixer that applies its change but exits non-zero (unfixable issues
		// remain) must NOT discard the partial fix.
		"fixer": {Type: "shell", Command: "/bin/sh", Args: []string{
			"-c", `tr a-z A-Z < "$1" > "$1.x" && mv "$1.x" "$1"; exit 1`, "sh",
		}},
	}}
	e := NewExecutor(dir, false, false, appMgr, nil)

	got, err := e.FormatFileInPlace(context.Background(), inPlaceTask("fixer", dir), filepath.Join(dir, "f.txt"), []byte("abc\n"))
	if err != nil {
		t.Fatalf("FormatFileInPlace: %v", err)
	}
	if string(got) != "ABC\n" {
		t.Errorf("got %q, want %q (non-zero exit must keep the written change)", got, "ABC\n")
	}
}

func TestFormatFileInPlace_EmptiedNonEmptyFileIsError(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	dir := t.TempDir()
	appMgr := &mockAppManager{commands: map[string]*binmanager.CommandInfo{
		// Truncates the file → would wipe the buffer; must be rejected.
		"sink": {Type: "shell", Command: "/bin/sh", Args: []string{"-c", `: > "$1"`, "sh"}},
	}}
	e := NewExecutor(dir, false, false, appMgr, nil)

	if _, err := e.FormatFileInPlace(context.Background(), inPlaceTask("sink", dir), filepath.Join(dir, "f.txt"), []byte("data\n")); err == nil {
		t.Error("expected error when formatter empties a non-empty file")
	}
}

func TestCleanStaleFormatTempDirs(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	base := formatTempBase()
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}

	stale := filepath.Join(base, "fmt-old")
	fresh := filepath.Join(base, "fmt-new")
	for _, d := range []string{stale, fresh} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// Backdate the stale dir well past the TTL.
	old := time.Now().Add(-2 * formatTempTTL)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	if err := CleanStaleFormatTempDirs(); err != nil {
		t.Fatalf("CleanStaleFormatTempDirs: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale dir should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh dir should be kept, stat err = %v", err)
	}
}

func TestCleanStaleFormatTempDirs_MissingBaseIsNoError(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir()) // base does not exist under this fresh temp
	if err := CleanStaleFormatTempDirs(); err != nil {
		t.Errorf("missing base must not error, got %v", err)
	}
}
