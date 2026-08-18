package tooling

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

// shCmd builds a `sh -c <script>` command for the IO tests. These tests rely on
// a POSIX shell, which is available on the Linux/macOS CI runners.
func shCmd(ctx context.Context, script string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", script)
}

func TestRunCommandIO_CombinedMode(t *testing.T) {
	e := newTestExecutor(t.TempDir())
	ctx := context.Background()

	// Default (combined) mode interleaves stdout+stderr into one buffer and
	// returns nil for the separated stderr slice — the historical behavior.
	cmd := shCmd(ctx, "echo out; echo err >&2")
	combined, stderr, err := e.runCommandIO(cmd, nil, false)
	if err != nil {
		t.Fatalf("runCommandIO returned error: %v", err)
	}
	if stderr != nil {
		t.Errorf("combined mode should return nil stderr, got %q", stderr)
	}
	got := string(combined)
	if !strings.Contains(got, "out") || !strings.Contains(got, "err") {
		t.Errorf("combined output missing a stream: %q", got)
	}
}

func TestRunCommandIO_SeparatedMode(t *testing.T) {
	e := newTestExecutor(t.TempDir())
	ctx := context.Background()

	// Separated mode keeps stdout (candidate content) apart from stderr
	// (diagnostics) with no cross-contamination.
	cmd := shCmd(ctx, "echo formatted-content; echo diagnostic >&2")
	stdout, stderr, err := e.runCommandIO(cmd, nil, true)
	if err != nil {
		t.Fatalf("runCommandIO returned error: %v", err)
	}
	if got := strings.TrimSpace(string(stdout)); got != "formatted-content" {
		t.Errorf("stdout = %q, want %q", got, "formatted-content")
	}
	if got := strings.TrimSpace(string(stderr)); got != "diagnostic" {
		t.Errorf("stderr = %q, want %q", got, "diagnostic")
	}
	if strings.Contains(string(stdout), "diagnostic") {
		t.Errorf("stdout contaminated with stderr: %q", stdout)
	}
}

func TestRunCommandIO_StdinFeeding(t *testing.T) {
	e := newTestExecutor(t.TempDir())
	ctx := context.Background()

	// `cat` echoes stdin to stdout — proves the content was delivered.
	input := []byte("line one\nline two\n")
	cmd := shCmd(ctx, "cat")
	stdout, _, err := e.runCommandIO(cmd, input, true)
	if err != nil {
		t.Fatalf("runCommandIO returned error: %v", err)
	}
	if string(stdout) != string(input) {
		t.Errorf("stdin not delivered: stdout = %q, want %q", stdout, input)
	}
}

func TestRunCommandIO_NilStdinLeavesStdinUntouched(t *testing.T) {
	e := newTestExecutor(t.TempDir())
	ctx := context.Background()

	// With nil stdinContent the tool reads EOF immediately; `cat` yields empty.
	cmd := shCmd(ctx, "cat")
	stdout, _, err := e.runCommandIO(cmd, nil, true)
	if err != nil {
		t.Fatalf("runCommandIO returned error: %v", err)
	}
	if len(stdout) != 0 {
		t.Errorf("expected empty stdout with nil stdin, got %q", stdout)
	}
}

// TestExecuteTaskStdinStdoutMode drives the full per-file path: a fake formatter
// that reads stdin and writes the (transformed) content to stdout plus a
// diagnostic to stderr. It asserts the file content is delivered on stdin, the
// formatted content lands in CapturedStdout, and the reported output is the
// stderr diagnostic — not the formatted content.
func TestExecuteTaskStdinStdoutMode(t *testing.T) {
	tmpDir := t.TempDir()
	file := filepath.Join(tmpDir, "src.txt")
	if err := os.WriteFile(file, []byte("hello world\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	appManager := &mockAppManager{
		commands: map[string]*binmanager.CommandInfo{
			// `tr` upper-cases stdin → stdout; the diagnostic goes to stderr.
			"fmt-tool": {
				Type:    "shell",
				Command: "/bin/sh",
				Args:    []string{"-c", "tr a-z A-Z; echo formatting-note >&2"},
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
	if result.CapturedStdout != "HELLO WORLD\n" {
		t.Errorf("CapturedStdout = %q, want %q", result.CapturedStdout, "HELLO WORLD\n")
	}
	if !strings.Contains(result.Output, "formatting-note") {
		t.Errorf("reported output should carry stderr diagnostic, got %q", result.Output)
	}
	if strings.Contains(result.Output, "HELLO WORLD") {
		t.Errorf("reported output leaked formatted content: %q", result.Output)
	}
}

// TestExecuteTaskDefaultModeUnchanged regression-guards the historical combined
// capture: with no input/output modes set, stdout+stderr are merged into Output
// and CapturedStdout stays empty.
func TestExecuteTaskDefaultModeUnchanged(t *testing.T) {
	tmpDir := t.TempDir()
	file := filepath.Join(tmpDir, "src.txt")
	if err := os.WriteFile(file, []byte("data\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	appManager := &mockAppManager{
		commands: map[string]*binmanager.CommandInfo{
			"plain-tool": {
				Type:    "shell",
				Command: "/bin/sh",
				Args:    []string{"-c", "echo out-line; echo err-line >&2"},
			},
		},
	}
	executor := NewExecutor(tmpDir, false, false, appManager, nil)

	task := Task{
		ToolName:  "plain-tool",
		Operation: config.OpLint,
		OpConfig: config.ToolOperation{
			App:   "plain-tool",
			Scope: config.ToolScopePerFile,
		},
		Files:       []string{file},
		ProjectPath: tmpDir,
	}

	result := executor.executeTask(context.Background(), task)
	if !result.Success {
		t.Fatalf("executeTask failed: %v", result.Error)
	}
	if result.CapturedStdout != "" {
		t.Errorf("CapturedStdout should be empty in default mode, got %q", result.CapturedStdout)
	}
	if !strings.Contains(result.Output, "out-line") || !strings.Contains(result.Output, "err-line") {
		t.Errorf("combined output missing a stream: %q", result.Output)
	}
}

func TestStdinForOperation(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "input.txt")
	content := []byte("file content here\n")
	if err := os.WriteFile(file, content, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	tests := []struct {
		name    string
		op      config.ToolOperation
		file    string
		want    []byte
		wantErr bool
	}{
		{
			name: "file mode returns nil (default)",
			op:   config.ToolOperation{Input: config.ToolInputFile},
			file: file,
			want: nil,
		},
		{
			name: "unset mode returns nil",
			op:   config.ToolOperation{},
			file: file,
			want: nil,
		},
		{
			name: "stdin mode reads file content",
			op:   config.ToolOperation{Input: config.ToolInputStdin},
			file: file,
			want: content,
		},
		{
			name: "stdin mode with empty file path returns nil",
			op:   config.ToolOperation{Input: config.ToolInputStdin},
			file: "",
			want: nil,
		},
		{
			name:    "stdin mode with missing file errors",
			op:      config.ToolOperation{Input: config.ToolInputStdin},
			file:    filepath.Join(dir, "does-not-exist.txt"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stdinForOperation(tt.op, tt.file)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != string(tt.want) {
				t.Errorf("stdinForOperation = %q, want %q", got, tt.want)
			}
		})
	}
}
