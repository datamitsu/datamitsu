package tooling

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

// TestApplyStdoutFormat exercises the diff-in-core apply helper directly: a
// single changed line in a multi-line file produces a minimal edit set (not a
// whole-file replacement), the file is rewritten to the candidate content, and
// an unchanged candidate produces nil edits and leaves the file untouched.
func TestApplyStdoutFormat(t *testing.T) {
	t.Run("single line change is minimal", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "f.txt")
		original := []byte("alpha\nbeta\ngamma\ndelta\n")
		if err := os.WriteFile(file, original, 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		candidate := []byte("alpha\nBETA\ngamma\ndelta\n")

		edits, err := applyStdoutFormat(file, original, candidate)
		if err != nil {
			t.Fatalf("applyStdoutFormat: %v", err)
		}
		// A 4-line file with one changed line must not be rewritten whole: the
		// edit set touches only the changed region (here a delete + insert pair,
		// each ≤1 line), never all 4 lines.
		if len(edits) == 0 {
			t.Fatalf("expected edits for a changed file, got none")
		}
		for _, e := range edits {
			span := e.Range.End.Line - e.Range.Start.Line
			if span > 1 {
				t.Errorf("edit spans %d lines, expected a minimal (≤1 line) edit: %+v", span, e)
			}
		}
		got, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if string(got) != string(candidate) {
			t.Errorf("file content = %q, want %q", got, candidate)
		}
	})

	t.Run("no change yields nil edits and untouched file", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "f.txt")
		original := []byte("unchanged\ncontent\n")
		if err := os.WriteFile(file, original, 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		info, err := os.Stat(file)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		before := info.ModTime()

		// Force a measurable mtime gap so an accidental rewrite is detectable.
		time.Sleep(10 * time.Millisecond)

		edits, err := applyStdoutFormat(file, original, original)
		if err != nil {
			t.Fatalf("applyStdoutFormat: %v", err)
		}
		if edits != nil {
			t.Errorf("expected nil edits for identical content, got %+v", edits)
		}
		info2, err := os.Stat(file)
		if err != nil {
			t.Fatalf("stat after: %v", err)
		}
		if !info2.ModTime().Equal(before) {
			t.Errorf("file mtime changed on no-op format: %v -> %v", before, info2.ModTime())
		}
	})
}

// TestFormattingPipelineEndToEnd drives the full per-file formatting path: a fake
// stdin→stdout formatter changes a single line; the core captures stdout, diffs
// against the original, applies the minimal edit set to the file, and records the
// edits on the result. The final file content must equal a direct run of the same
// tool over the original input.
func TestFormattingPipelineEndToEnd(t *testing.T) {
	tmpDir := t.TempDir()
	file := filepath.Join(tmpDir, "src.txt")
	original := "alpha\nfoo\ngamma\ndelta\n"
	if err := os.WriteFile(file, []byte(original), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// Formatter: replace "foo" with "bar" on its line, read from stdin, write to
	// stdout. A diagnostic on stderr proves stdout stays clean.
	const script = "sed 's/foo/bar/'; echo formatted >&2"

	appManager := &mockAppManager{
		commands: map[string]*binmanager.CommandInfo{
			"fmt-tool": {
				Type:    "shell",
				Command: "/bin/sh",
				Args:    []string{"-c", script},
			},
		},
	}
	executor := NewExecutor(tmpDir, false, false, appManager, nil)

	task := Task{
		ToolName:  "fmt-tool",
		Operation: config.OpFix,
		OpConfig: config.ToolOperation{
			App:    "fmt-tool",
			Scope:  config.ToolScopePerFile,
			Batch:  &batchFalse,
			Input:  config.ToolInputStdin,
			Output: config.ToolOutputStdout,
		},
		Files:       []string{file},
		ProjectPath: tmpDir,
	}

	result := executor.executeTask(context.Background(), task)
	if !result.Success {
		t.Fatalf("executeTask failed: %v (output=%q)", result.Error, result.Output)
	}

	// Minimal diff: only the single changed line region, never the whole 4 lines.
	if len(result.FormatEdits) == 0 {
		t.Fatalf("expected format edits, got none")
	}
	for _, e := range result.FormatEdits {
		if span := e.Range.End.Line - e.Range.Start.Line; span > 1 {
			t.Errorf("non-minimal edit spanning %d lines: %+v", span, e)
		}
	}

	// The file on disk must equal a direct run of the same tool on the original.
	want := directRun(t, script, original)
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != want {
		t.Errorf("formatted file = %q, want (direct tool run) %q", got, want)
	}
	if string(got) != "alpha\nbar\ngamma\ndelta\n" {
		t.Errorf("unexpected formatted content: %q", got)
	}
}

// TestFormattingPipelineNoChange asserts the no-op path: a formatter that returns
// its input verbatim yields zero edits and leaves the file byte-identical.
func TestFormattingPipelineNoChange(t *testing.T) {
	tmpDir := t.TempDir()
	file := filepath.Join(tmpDir, "src.txt")
	original := "already\nformatted\n"
	if err := os.WriteFile(file, []byte(original), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	appManager := &mockAppManager{
		commands: map[string]*binmanager.CommandInfo{
			// `cat` echoes stdin verbatim → identical candidate → no edits.
			"noop-fmt": {
				Type:    "shell",
				Command: "/bin/sh",
				Args:    []string{"-c", "cat"},
			},
		},
	}
	executor := NewExecutor(tmpDir, false, false, appManager, nil)

	task := Task{
		ToolName:  "noop-fmt",
		Operation: config.OpFix,
		OpConfig: config.ToolOperation{
			App:    "noop-fmt",
			Scope:  config.ToolScopePerFile,
			Batch:  &batchFalse,
			Input:  config.ToolInputStdin,
			Output: config.ToolOutputStdout,
		},
		Files:       []string{file},
		ProjectPath: tmpDir,
	}

	result := executor.executeTask(context.Background(), task)
	if !result.Success {
		t.Fatalf("executeTask failed: %v", result.Error)
	}
	if result.FormatEdits != nil {
		t.Errorf("expected nil edits for no-op formatter, got %+v", result.FormatEdits)
	}
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != original {
		t.Errorf("file changed on no-op format: %q, want %q", got, original)
	}
}

// TestFormattingPipelineEmptyStdoutDoesNotTruncate guards the data-loss footgun:
// a stdout-mode formatter that exits 0 but writes nothing to stdout (e.g. it only
// emits to stderr) must NOT have its empty output diffed against the file — that
// would delete every line. The op must fail and leave the file byte-identical.
func TestFormattingPipelineEmptyStdoutDoesNotTruncate(t *testing.T) {
	tmpDir := t.TempDir()
	file := filepath.Join(tmpDir, "src.txt")
	original := "keep\nthis\ncontent\n"
	if err := os.WriteFile(file, []byte(original), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	appManager := &mockAppManager{
		commands: map[string]*binmanager.CommandInfo{
			// Writes only to stderr, nothing to stdout, exits 0.
			"silent-fmt": {
				Type:    "shell",
				Command: "/bin/sh",
				Args:    []string{"-c", "echo noise >&2"},
			},
		},
	}
	executor := NewExecutor(tmpDir, false, false, appManager, nil)

	task := Task{
		ToolName:  "silent-fmt",
		Operation: config.OpFix,
		OpConfig: config.ToolOperation{
			App:    "silent-fmt",
			Scope:  config.ToolScopePerFile,
			Batch:  &batchFalse,
			Input:  config.ToolInputStdin,
			Output: config.ToolOutputStdout,
		},
		Files:       []string{file},
		ProjectPath: tmpDir,
	}

	result := executor.executeTask(context.Background(), task)
	if result.Success {
		t.Errorf("expected failure for empty-stdout formatter, got success")
	}
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != original {
		t.Errorf("file was modified by empty-stdout formatter: %q, want %q", got, original)
	}
}

// directRun runs `sh -c <script>` with input piped to stdin and returns stdout,
// giving the reference content a direct tool invocation would produce.
func directRun(t *testing.T, script, input string) string {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("direct run failed: %v", err)
	}
	return string(out)
}
