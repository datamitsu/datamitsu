package tooling

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

func shellApp(script string) *binmanager.CommandInfo {
	return &binmanager.CommandInfo{Type: "shell", Command: "/bin/sh", Args: []string{"-c", script}}
}

func lintTask(t *testing.T, tool string, scope config.ToolScope, projectPath string, args ...string) Task {
	t.Helper()
	if projectPath != "" {
		if err := os.MkdirAll(projectPath, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return Task{
		ToolName:    tool,
		Operation:   config.OpLint,
		ProjectPath: projectPath,
		OpConfig:    config.ToolOperation{App: tool, Scope: scope, Args: args},
	}
}

func resultsByTool(groups []GroupExecutionResult) map[string]ExecutionResult {
	out := map[string]ExecutionResult{}
	for _, g := range groups {
		for _, r := range g.Results {
			out[r.ToolName] = r
		}
	}
	return out
}

// Without fail-fast every level runs to the end: later priority groups, later
// sequential sub-groups and parallel siblings all run after a failure, and the
// run still reports it.
func TestKeepGoingRunsEveryLevel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the tools are sh scripts")
	}
	root := t.TempDir()
	appManager := &mockAppManager{commands: map[string]*binmanager.CommandInfo{
		"fails":   shellApp("exit 1"),
		"slow":    shellApp("sleep 0.3"),
		"later":   shellApp("exit 0"),
		"overlap": shellApp("exit 0"),
	}}
	plan := &ExecutionPlan{Groups: []TaskGroup{
		{Priority: 10, Tasks: []Task{
			lintTask(t, "fails", config.ToolScopePerProject, filepath.Join(root, "a")),
			lintTask(t, "slow", config.ToolScopePerProject, filepath.Join(root, "b")),
			// Repository scope overlaps everything, so it forms a later sub-group.
			lintTask(t, "overlap", config.ToolScopeRepository, ""),
		}},
		{Priority: 20, Tasks: []Task{lintTask(t, "later", config.ToolScopeRepository, "")}},
	}}

	tests := []struct {
		failFast bool
		want     map[string]string
	}{
		{failFast: false, want: map[string]string{"fails": "failed", "slow": "passed", "overlap": "passed", "later": "passed"}},
		// fails may fail before or after slow finishes; slow is either killed or done,
		// never left out, and nothing after the group runs.
		{failFast: true, want: map[string]string{"fails": "failed", "overlap": "absent", "later": "absent"}},
	}
	for _, tt := range tests {
		t.Run(map[bool]string{true: "fail-fast", false: "keep-going"}[tt.failFast], func(t *testing.T) {
			e := NewExecutor(root, false, tt.failFast, appManager, nil)
			groups, err := e.Execute(context.Background(), plan)
			if tt.failFast != (err != nil) {
				t.Errorf("Execute() error = %v, want an error only under fail-fast", err)
			}
			if !tt.failFast && len(groups) != 2 {
				t.Fatalf("got %d group results, want 2", len(groups))
			}
			byTool := resultsByTool(groups)
			for tool, want := range tt.want {
				r, ok := byTool[tool]
				got := "absent"
				switch {
				case !ok:
				case r.Success:
					got = "passed"
				case r.IsCancelled():
					got = "cancelled"
				default:
					got = "failed"
				}
				if got != want {
					t.Errorf("%s: %s, want %s", tool, got, want)
				}
			}
			for _, g := range groups {
				if g.Priority == 10 && g.Success {
					t.Error("the group holding the failed task reports success")
				}
			}
		})
	}
}

// A running sibling is killed only by fail-fast. Without it the sibling
// finishes, and its result is not a cancellation.
func TestKeepGoingDoesNotKillSiblings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the tools are sh scripts")
	}
	root := t.TempDir()
	done := filepath.Join(root, "done")
	appManager := &mockAppManager{commands: map[string]*binmanager.CommandInfo{
		"fails": shellApp("sleep 0.1; exit 1"),
		"slow":  shellApp("sleep 0.5; touch " + done),
	}}
	t.Setenv("DATAMITSU_MAX_PARALLEL_WORKERS", "2")
	e := NewExecutor(root, false, false, appManager, nil)
	results := e.executeTasksParallel(context.Background(), []Task{
		lintTask(t, "fails", config.ToolScopePerProject, filepath.Join(root, "a")),
		lintTask(t, "slow", config.ToolScopePerProject, filepath.Join(root, "b")),
	}, func() { t.Error("keep-going triggered fail-fast") })

	byTool := map[string]ExecutionResult{}
	for _, r := range results {
		byTool[r.ToolName] = r
	}
	if r := byTool["slow"]; !r.Success || r.IsCancelled() {
		t.Errorf("slow = %+v, want it to finish", r)
	}
	if _, err := os.Stat(done); err != nil {
		t.Errorf("slow never finished: %v", err)
	}
}

// The per-file loop runs every file without fail-fast. The task fails, its exit
// code is the last failing file's, and the output of every file is kept.
func TestKeepGoingPerFileRunsEveryFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the tools are sh scripts")
	}
	root := t.TempDir()
	names := []string{"bad1.txt", "ok.txt", "bad2.txt"}
	files := make([]string, 0, len(names))
	for _, name := range names {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, path)
	}
	marker := filepath.Join(root, "ran")
	// sh -c hands the first argument after the script to it as $0.
	script := `echo "$0" >> ` + marker + `; case "$0" in *bad1*) echo "bad1 failed"; exit 3;; *bad2*) echo "bad2 failed"; exit 4;; esac`
	appManager := &mockAppManager{commands: map[string]*binmanager.CommandInfo{"alpha": shellApp(script)}}

	tests := []struct {
		failFast bool
		wantRuns int
		wantExit int
		wantFile string
		wantOut  []string
	}{
		{failFast: false, wantRuns: 3, wantExit: 4, wantFile: "bad2.txt", wantOut: []string{"bad1 failed", "bad2 failed"}},
		{failFast: true, wantRuns: 1, wantExit: 3, wantFile: "bad1.txt", wantOut: []string{"bad1 failed"}},
	}
	for _, tt := range tests {
		t.Run(map[bool]string{true: "fail-fast", false: "keep-going"}[tt.failFast], func(t *testing.T) {
			_ = os.Remove(marker)
			task := lintTask(t, "alpha", config.ToolScopePerProject, root, "{file}")
			task.Files = files
			result := NewExecutor(root, false, tt.failFast, appManager, nil).executeTask(context.Background(), task)
			if result.Success || result.IsCancelled() {
				t.Fatalf("result = %+v, want a failure of its own", result)
			}
			if result.ExitCode != tt.wantExit {
				t.Errorf("ExitCode = %d, want %d (the last failing file's)", result.ExitCode, tt.wantExit)
			}
			if !strings.HasSuffix(result.Command, tt.wantFile) {
				t.Errorf("Command = %q, want the last failing file's, %s", result.Command, tt.wantFile)
			}
			ran, _ := os.ReadFile(marker)
			if n := strings.Count(string(ran), "\n"); n != tt.wantRuns {
				t.Errorf("the tool ran %d time(s), want %d:\n%s", n, tt.wantRuns, ran)
			}
			for _, want := range tt.wantOut {
				if !strings.Contains(result.Output, want) {
					t.Errorf("Output lacks %q:\n%s", want, result.Output)
				}
			}
		})
	}
}

// A sibling killed because another task failed is a fail-fast cancellation, and
// it had started: the two facts a caller needs to report it as cancelled rather
// than as never started.
func TestFailFastKilledSiblingIsStartedAndCancelled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the tools are sh scripts")
	}
	root := t.TempDir()
	appManager := &mockAppManager{commands: map[string]*binmanager.CommandInfo{
		"fails": shellApp("sleep 0.2; exit 1"),
		"slow":  shellApp("sleep 5"),
	}}
	t.Setenv("DATAMITSU_MAX_PARALLEL_WORKERS", "2")
	e := NewExecutor(root, false, true, appManager, nil)
	start := time.Now()
	groups, err := e.Execute(context.Background(), &ExecutionPlan{Groups: []TaskGroup{{Priority: 1, Tasks: []Task{
		lintTask(t, "fails", config.ToolScopePerProject, filepath.Join(root, "a")),
		lintTask(t, "slow", config.ToolScopePerProject, filepath.Join(root, "b")),
	}}}})
	if err == nil {
		t.Fatal("Execute() = nil, want the fail-fast error")
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Errorf("the run took %s; the sibling was not killed", elapsed)
	}
	slow := resultsByTool(groups)["slow"]
	if !slow.IsCancelled() || slow.FailureReason != FailureReasonCancelled || !slow.Started() {
		t.Errorf("slow = %+v, want a started task cancelled by fail-fast", slow)
	}
	if slow.RelativeDir != "b" {
		t.Errorf("slow.RelativeDir = %q, want b", slow.RelativeDir)
	}
}
