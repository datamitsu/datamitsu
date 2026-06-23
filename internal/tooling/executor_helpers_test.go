package tooling

import (
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

func newTestExecutor(rootPath string) *Executor {
	return NewExecutor(rootPath, false, false, nil, nil)
}

func TestExecutorMakeRelativePaths(t *testing.T) {
	e := newTestExecutor("/repo")

	t.Run("empty input returns input", func(t *testing.T) {
		if got := e.makeRelativePaths(nil, "/repo"); got != nil {
			t.Errorf("makeRelativePaths(nil) = %v, want nil", got)
		}
	})

	t.Run("converts to relative, keeps unrelatable", func(t *testing.T) {
		files := []string{"/repo/pkg/a.go", "/repo/b.go", "relative/c.go"}
		got := e.makeRelativePaths(files, "/repo")
		want := []string{"pkg/a.go", "b.go", "relative/c.go"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("makeRelativePaths() = %v, want %v", got, want)
		}
	})
}

func TestExecutorGetRelativeDir(t *testing.T) {
	e := newTestExecutor("/repo")

	tests := []struct {
		name       string
		workingDir string
		want       string
	}{
		{"root itself yields empty", "/repo", ""},
		{"subdir", "/repo/pkg/sub", "pkg/sub"},
		{"unrelatable yields empty", "relative", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := e.getRelativeDir(tt.workingDir); got != tt.want {
				t.Errorf("getRelativeDir(%q) = %q, want %q", tt.workingDir, got, tt.want)
			}
		})
	}
}

func TestExecutorFormatCommandString(t *testing.T) {
	e := newTestExecutor("/repo")

	tests := []struct {
		name    string
		cmdInfo *binmanager.CommandInfo
		args    []string
		want    string
	}{
		{
			name:    "binary type ignores cmdInfo.Args",
			cmdInfo: &binmanager.CommandInfo{Type: "binary", Command: "gofmt", Args: []string{"-l"}},
			args:    []string{"-w", "."},
			want:    "gofmt -w .",
		},
		{
			name:    "shell type prepends cmdInfo.Args",
			cmdInfo: &binmanager.CommandInfo{Type: "shell", Command: "sh", Args: []string{"-c", "echo"}},
			args:    []string{"hi"},
			want:    "sh -c echo hi",
		},
		{
			name:    "node type prepends cmdInfo.Args",
			cmdInfo: &binmanager.CommandInfo{Type: "node", Command: "node", Args: []string{"cli.js"}},
			args:    []string{"--fix"},
			want:    "node cli.js --fix",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := e.formatCommandString(tt.cmdInfo, tt.args); got != tt.want {
				t.Errorf("formatCommandString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExecutorChunkFilesByCommandLength(t *testing.T) {
	e := newTestExecutor("/repo")
	cmdInfo := &binmanager.CommandInfo{Type: "binary", Command: "tool"}

	t.Run("empty files yields nil", func(t *testing.T) {
		if got := e.chunkFilesByCommandLength(nil, []string{"--check"}, cmdInfo); got != nil {
			t.Errorf("chunkFilesByCommandLength(nil) = %v, want nil", got)
		}
	})

	t.Run("all files fit in one chunk", func(t *testing.T) {
		files := []string{"a.go", "b.go", "c.go"}
		got := e.chunkFilesByCommandLength(files, []string{"--check"}, cmdInfo)
		if len(got) != 1 {
			t.Fatalf("expected 1 chunk, got %d: %v", len(got), got)
		}
		if !reflect.DeepEqual(got[0], files) {
			t.Errorf("chunk = %v, want %v", got[0], files)
		}
	})

	t.Run("low max length forces multiple chunks", func(t *testing.T) {
		// A tiny limit forces one file per chunk.
		t.Setenv("DATAMITSU_MAX_CMD_LENGTH", "20")
		files := []string{"long-file-name-1.go", "long-file-name-2.go", "long-file-name-3.go"}
		got := e.chunkFilesByCommandLength(files, []string{"--check"}, cmdInfo)
		if len(got) < 2 {
			t.Fatalf("expected multiple chunks under tight limit, got %d: %v", len(got), got)
		}
		// Every file must appear exactly once across all chunks.
		var flat []string
		for _, c := range got {
			flat = append(flat, c...)
		}
		if !reflect.DeepEqual(flat, files) {
			t.Errorf("flattened chunks = %v, want %v", flat, files)
		}
	})
}

func TestExecutorFilterFilesByCacheNilCache(t *testing.T) {
	e := newTestExecutor("/repo")
	task := Task{
		ToolName: "gofmt",
		Files:    []string{"/repo/a.go", "/repo/b.go"},
		OpConfig: config.ToolOperation{Scope: config.ToolScopePerFile},
	}
	// With no cache, every file passes through unchanged.
	got := e.filterFilesByCache(task)
	if !reflect.DeepEqual(got, task.Files) {
		t.Errorf("filterFilesByCache(nil cache) = %v, want %v", got, task.Files)
	}
	// updateCacheAfterSuccess must be a no-op (not panic) with a nil cache.
	e.updateCacheAfterSuccess(task, task.Files)
}

func TestGetExitCode(t *testing.T) {
	t.Run("nil error is zero", func(t *testing.T) {
		if got := getExitCode(nil); got != 0 {
			t.Errorf("getExitCode(nil) = %d, want 0", got)
		}
	})

	t.Run("non-exit error is minus one", func(t *testing.T) {
		if got := getExitCode(errors.New("boom")); got != -1 {
			t.Errorf("getExitCode(plain) = %d, want -1", got)
		}
	})

	t.Run("real exit error reports its code", func(t *testing.T) {
		// `false` exits 1 on every POSIX system; skip where unavailable.
		if _, err := exec.LookPath("false"); err != nil {
			t.Skip("/usr/bin/false not available")
		}
		err := exec.Command("false").Run()
		if got := getExitCode(err); got != 1 {
			t.Errorf("getExitCode(false-run) = %d, want 1", got)
		}
	})
}

func TestExecutorFormatCommandStringUnknownTypeFallback(t *testing.T) {
	e := newTestExecutor("/repo")
	// An empty/unknown type hits the default branch (args only, no cmdInfo.Args).
	cmdInfo := &binmanager.CommandInfo{Command: "x", Args: []string{"ignored"}}
	got := e.formatCommandString(cmdInfo, []string{"-a", "-b"})
	if !strings.Contains(got, "x -a -b") || strings.Contains(got, "ignored") {
		t.Errorf("default-branch format = %q, want 'x -a -b' without cmdInfo.Args", got)
	}
}
