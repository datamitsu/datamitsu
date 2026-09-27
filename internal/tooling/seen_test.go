package tooling

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/cache"
	"github.com/datamitsu/datamitsu/internal/config"

	"go.uber.org/zap"
)

func TestUnchangedSince(t *testing.T) {
	cases := []struct {
		name          string
		old           bool // the file was last written long before it was observed
		rewrite       string
		want          bool
		wantRehash    int64
		needsIdentity bool
	}{
		// A stat proves an old file unchanged; nothing is read again.
		{name: "an old, untouched file", old: true, want: true, wantRehash: 0, needsIdentity: true},
		// A file written inside the mtime tick could have been rewritten at the
		// same length without a visible stat change, so it is read again.
		{name: "a file written just now", want: true, wantRehash: 1},
		{name: "a file rewritten during the run", old: true, rewrite: "b\n", want: false, wantRehash: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.needsIdentity {
				requireIdent(t)
			}
			traceOn(t)
			file := filepath.Join(t.TempDir(), "f.txt")
			if err := os.WriteFile(file, []byte("a\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if c.old {
				backdate(t, file)
			}
			seen := observe(file)
			if seen.Hash == "" {
				t.Fatal("observe could not hash the file")
			}
			if c.rewrite != "" {
				if err := os.WriteFile(file, []byte(c.rewrite), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before := counterValue(t, "cache.file_probe_rehashes")
			if got := unchangedSince(file, seen); got != c.want {
				t.Errorf("unchangedSince = %v, want %v", got, c.want)
			}
			if got := counterValue(t, "cache.file_probe_rehashes") - before; got != c.wantRehash {
				t.Errorf("the probe read the file %d time(s), want %d", got, c.wantRehash)
			}
		})
	}
}

func TestUnchangedSinceWithoutAHash(t *testing.T) {
	file := filepath.Join(t.TempDir(), "gone.txt")
	seen := observe(file)
	if seen.Hash != "" {
		t.Fatalf("a missing file hashed to %q", seen.Hash)
	}
	if err := os.WriteFile(file, []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if unchangedSince(file, seen) {
		t.Error("bytes that were never read cannot be vouched for")
	}
}

// TestPerFileCacheRecordsAgainstTheBytesTheToolSaw is the markPassed
// reproduction through the executor: A passes on X, B passes on Y, the file
// goes back to X, and B must run — it never saw X.
func TestPerFileCacheRecordsAgainstTheBytesTheToolSaw(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "f.txt")
	marker := filepath.Join(root, "runs.log")
	c, err := cache.NewCache(t.TempDir(), root, config.Config{}, nil, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	shell := func(name string) *binmanager.CommandInfo {
		return &binmanager.CommandInfo{Type: "shell", Command: "/bin/sh", Args: []string{"-c", `echo "$0" >> "$MARKER"`, name}}
	}
	e := NewExecutor(root, false, false, &mockAppManager{commands: map[string]*binmanager.CommandInfo{
		"A": shell("A"), "B": shell("B"),
	}}, c)
	run := func(tool string) {
		t.Helper()
		result := e.executeTask(context.Background(), Task{
			ToolName:  tool,
			Operation: config.OpLint,
			OpConfig: config.ToolOperation{
				App: tool, Scope: config.ToolScopePerFile, Args: []string{"{file}"},
				Env: map[string]string{"MARKER": marker},
			},
			Files:       []string{file},
			ProjectPath: root,
		})
		if !result.Success {
			t.Fatalf("%s failed: %v", tool, result.Error)
		}
	}
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("X\n")
	run("A")
	write("Y\n")
	run("B")
	write("X\n")
	run("B")

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(string(data)); strings.Join(got, " ") != "A B B" {
		t.Errorf("runs = %v, want A B B: B was skipped on bytes it never saw", got)
	}
}

// TestLintPassIsNotRecordedForAFileThatChangedDuringTheRun: the tool saw the
// bytes before the write, so a pass recorded afterwards would vouch for the
// new bytes it never read.
func TestLintPassIsNotRecordedForAFileThatChangedDuringTheRun(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "f.txt")
	if err := os.WriteFile(file, []byte("X\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := cache.NewCache(t.TempDir(), root, config.Config{}, nil, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(root, false, false, &mockAppManager{commands: map[string]*binmanager.CommandInfo{
		"edits": {Type: "shell", Command: "/bin/sh", Args: []string{"-c", `echo edited >> "$1"`, "edits"}},
	}}, c)
	task := Task{
		ToolName:    "edits",
		Operation:   config.OpLint,
		OpConfig:    config.ToolOperation{App: "edits", Scope: config.ToolScopePerFile, Args: []string{"{file}"}},
		Files:       []string{file},
		ProjectPath: root,
	}
	if result := e.executeTask(context.Background(), task); !result.Success {
		t.Fatalf("the tool failed: %v", result.Error)
	}
	if !c.Check(file, "edits", cache.OperationLint, observe(file), true) {
		t.Error("a pass was recorded for bytes written while the tool ran")
	}
}
