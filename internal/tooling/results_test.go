package tooling

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/cache"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/diagnostic"

	"go.uber.org/zap"
)

// resultsProject writes files into a fresh repository and returns an executor
// whose app "tool" runs script with the file(s) it was given as "$@".
func resultsProject(t *testing.T, failFast bool, script string, names ...string) (*Executor, *cache.Cache, string, []string) {
	t.Helper()
	root := t.TempDir()
	files := make([]string, 0, len(names))
	for _, name := range names {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		files = append(files, path)
	}
	c, err := cache.NewCache(t.TempDir(), root, config.Config{}, nil, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(root, false, failFast, &mockAppManager{commands: map[string]*binmanager.CommandInfo{
		"tool": {Type: "shell", Command: "/bin/sh", Args: []string{"-c", script, "tool"}},
	}}, c)
	return e, c, root, files
}

func loopTask(root string, files []string) Task {
	return Task{
		ToolName:    "tool",
		Operation:   config.OpLint,
		OpConfig:    config.ToolOperation{App: "tool", Scope: config.ToolScopePerFile, Args: []string{"{file}"}},
		Files:       files,
		ProjectPath: root,
	}
}

func fileStates(result ExecutionResult) []FileState {
	out := make([]FileState, 0, len(result.FileResults))
	for _, fr := range result.FileResults {
		out = append(out, fr.State)
	}
	return out
}

func TestPerFileProcessesCarryTheirOwnOutcome(t *testing.T) {
	e, _, root, files := resultsProject(t, false, `case "$1" in *bad*) echo finding; exit 1;; esac`, "good.txt", "bad.txt")
	e.SetParser(fileParser{findings: func(stdout string) []diagnostic.Diagnostic {
		if stdout == "finding" {
			return []diagnostic.Diagnostic{{Message: "m"}}
		}
		return nil
	}})
	task := loopTask(root, files)
	task.Tool.OutputParser = &config.OutputParser{Module: "core", Parser: "tool"}

	result := e.executeTask(context.Background(), task)
	if len(result.Processes) != 2 {
		t.Fatalf("Processes = %+v, want one per file", result.Processes)
	}
	good, bad := result.Processes[0], result.Processes[1]
	if good.ID != "#1" || bad.ID != "#2" {
		t.Errorf("IDs = %q, %q", good.ID, bad.ID)
	}
	if good.Files[0] != files[0] || bad.Files[0] != files[1] {
		t.Errorf("files = %v, %v", good.Files, bad.Files)
	}
	if good.State != ProcessRan || !good.Success || *good.ExitCode != 0 || good.Extraction != ExtractionParsedClean {
		t.Errorf("clean process = %+v", good)
	}
	if bad.State != ProcessRan || bad.Success || *bad.ExitCode != 1 || bad.Extraction != ExtractionParsedFindings {
		t.Errorf("failing process = %+v", bad)
	}
	if string(bad.OutputTail) != "finding\n" {
		t.Errorf("OutputTail = %q", bad.OutputTail)
	}
	if got := fileStates(result); len(got) != 2 || got[0] != FileRan || got[1] != FileRan {
		t.Fatalf("file states = %v", got)
	}
	if fr := result.FileResults[1]; fr.ProcessID != "#2" || fr.Success || *fr.ExitCode != 1 {
		t.Errorf("failing file = %+v", fr)
	}
}

// TestFileResultsUnderFailFast: a file the cache answered, one that failed and
// stopped the loop, and one the loop never reached.
func TestFileResultsUnderFailFast(t *testing.T) {
	e, c, root, files := resultsProject(t, true, `case "$1" in *bad*) exit 1;; esac`, "cached.txt", "bad.txt", "later.txt")
	task := loopTask(root, files)
	if err := c.AfterLint(files[0], "tool", observe(files[0]), true, true); err != nil {
		t.Fatal(err)
	}

	result := e.executeTask(context.Background(), task)
	want := []FileState{FileCached, FileRan, FileNotStarted}
	if got := fileStates(result); !equalStates(got, want) {
		t.Errorf("file states = %v, want %v", got, want)
	}
	if len(result.Processes) != 2 || result.Processes[1].State != ProcessNotStarted {
		t.Errorf("Processes = %+v, want the failing file's and a not-started one", result.Processes)
	}
	if fr := result.FileResults[2]; fr.ProcessID != "" || fr.ExitCode != nil {
		t.Errorf("a file that never ran names a process: %+v", fr)
	}
	if result.Cached {
		t.Error("a task that ran a process reports Cached")
	}
}

func TestFileResultsOfACancelledFile(t *testing.T) {
	e, _, root, files := resultsProject(t, false, `sleep 5`, "slow.txt", "next.txt")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	result := e.executeTask(ctx, loopTask(root, files))
	want := []FileState{FileCancelled, FileNotStarted}
	if got := fileStates(result); !equalStates(got, want) {
		t.Errorf("file states = %v, want %v", got, want)
	}
	if proc := result.Processes[0]; proc.State != ProcessCancelled || proc.ExitCode != nil {
		t.Errorf("stopped process = %+v", proc)
	}
}

func TestFileResultsOfAProcessThatCouldNotStart(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "a.txt")
	if err := os.WriteFile(file, []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(root, false, false, &mockAppManager{commands: map[string]*binmanager.CommandInfo{
		"tool": {Type: "shell", Command: filepath.Join(root, "no-such-command")},
	}}, nil)
	stdinMissing := loopTask(root, []string{filepath.Join(root, "missing.txt")})
	stdinMissing.OpConfig.Input = config.ToolInputStdin

	for name, task := range map[string]Task{
		"the command does not start": loopTask(root, []string{file}),
		"its input cannot be read":   stdinMissing,
	} {
		t.Run(name, func(t *testing.T) {
			result := e.executeTask(context.Background(), task)
			if got := fileStates(result); !equalStates(got, []FileState{FileSetupFailed}) {
				t.Errorf("file states = %v", got)
			}
			if proc := result.Processes[0]; proc.State != ProcessSetupFailed || proc.ExitCode != nil || proc.Success {
				t.Errorf("process = %+v", proc)
			}
		})
	}

	t.Run("the command cannot be resolved", func(t *testing.T) {
		broken := NewExecutor(root, false, false, &mockAppManager{err: os.ErrNotExist}, nil)
		result := broken.executeTask(context.Background(), loopTask(root, []string{file}))
		if got := fileStates(result); !equalStates(got, []FileState{FileSetupFailed}) {
			t.Errorf("file states = %v", got)
		}
		if len(result.Processes) != 0 {
			t.Errorf("Processes = %+v, want none", result.Processes)
		}
	})
}

func TestBatchProcessPerChunk(t *testing.T) {
	t.Setenv("DATAMITSU_MAX_CMD_LENGTH", "1")
	e, _, root, files := resultsProject(t, false, `exit 0`, "a.txt", "b.txt")
	task := loopTask(root, files)
	task.OpConfig.Scope = config.ToolScopeRepository
	task.OpConfig.Args = []string{"{files}"}

	result := e.executeTask(context.Background(), task)
	if len(result.Processes) != 2 {
		t.Fatalf("Processes = %+v, want one per chunk", result.Processes)
	}
	for i, proc := range result.Processes {
		if len(proc.Files) != 1 || proc.Files[0] != files[i] || proc.State != ProcessRan {
			t.Errorf("chunk %d = %+v", i, proc)
		}
		if fr := result.FileResults[i]; fr.ProcessID != proc.ID {
			t.Errorf("file %d names process %q, want %q", i, fr.ProcessID, proc.ID)
		}
	}
}

// TestWholeUnitResult: a tool whose argv names no file answers for its unit,
// so its result lists the unit's members, each checked by the one process.
func TestWholeUnitResult(t *testing.T) {
	e, _, root, files := resultsProject(t, false, `exit 0`, "a.ts", "b.ts", "README.md")
	task := loopTask(root, files[:2])
	task.OpConfig.Scope = config.ToolScopePerProject
	task.OpConfig.Args = []string{"--noEmit"}
	task.UnitDir = "pkg"
	task.UnitMembers = files
	task.Coverage = CoverageComplete

	result := e.executeTask(context.Background(), task)
	if !result.WholeUnit || result.UnitDir != "pkg" {
		t.Errorf("WholeUnit = %v, UnitDir = %q", result.WholeUnit, result.UnitDir)
	}
	if len(result.Files) != 3 {
		t.Fatalf("Files = %v, want the unit's members", result.Files)
	}
	for _, fr := range result.FileResults {
		if fr.State != FileRan || fr.ProcessID != "#1" {
			t.Errorf("member %+v", fr)
		}
	}

	again := e.executeTask(context.Background(), task)
	if !again.Cached || len(again.Processes) != 0 {
		t.Errorf("the verdict hit: Cached = %v, Processes = %+v", again.Cached, again.Processes)
	}
	if got := fileStates(again); !equalStates(got, []FileState{FileVerdictHit, FileVerdictHit, FileVerdictHit}) {
		t.Errorf("file states on a verdict hit = %v", got)
	}
}

func TestAllCachedTaskIsCached(t *testing.T) {
	e, c, root, files := resultsProject(t, false, `exit 0`, "a.txt")
	if err := c.AfterLint(files[0], "tool", observe(files[0]), true, true); err != nil {
		t.Fatal(err)
	}
	result := e.executeTask(context.Background(), loopTask(root, files))
	if !result.Cached || !equalStates(fileStates(result), []FileState{FileCached}) {
		t.Errorf("Cached = %v, file states = %v", result.Cached, fileStates(result))
	}
}

// TestFormatEditsArePerFile: the edits of one file are its own, not the last
// formatted file's.
func TestFormatEditsArePerFile(t *testing.T) {
	e, _, root, files := resultsProject(t, false, `case "$1" in *change*) echo changed;; *) cat "$1";; esac`, "change.txt", "keep.txt")
	task := loopTask(root, files)
	task.Operation = config.OpFix
	task.OpConfig.Output = config.ToolOutputStdout

	result := e.executeTask(context.Background(), task)
	if !result.Success {
		t.Fatalf("the formatter failed: %v", result.Error)
	}
	if len(result.FileResults[0].Edits) == 0 {
		t.Error("the rewritten file has no edits")
	}
	if result.FileResults[1].Edits != nil {
		t.Errorf("the untouched file has edits: %+v", result.FileResults[1].Edits)
	}
}

func equalStates(got, want []FileState) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestTaskIdentity: Execute names every task before it runs, two tasks of one
// tool in one directory apart, and every callback and result of a task carries
// its name.
func TestTaskIdentity(t *testing.T) {
	e, _, root, files := resultsProject(t, false, `exit 0`, "a.txt", "b.txt")
	pkg := filepath.Join(root, "pkg")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	plan := &ExecutionPlan{Groups: []TaskGroup{
		{Priority: 1, Tasks: []Task{loopTask(root, files[:1])}},
		{Priority: 2, Tasks: []Task{loopTask(root, files[1:]), loopTask(pkg, files[:1])}},
	}}

	var mu sync.Mutex
	started := map[string]string{}
	progress := map[string]int{}
	e.SetTaskStartCallback(func(taskID, _, dir string) {
		mu.Lock()
		started[taskID] = dir
		mu.Unlock()
	})
	e.SetFileProgressCallback(func(taskID, _ string, _, _ int, _ bool) {
		mu.Lock()
		progress[taskID]++
		mu.Unlock()
	})
	results, err := e.Execute(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"tool::1", "tool::2", "tool:pkg:3"}
	var got []string
	for _, g := range plan.Groups {
		for _, task := range g.Tasks {
			got = append(got, task.ID)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("task IDs = %v, want %v", got, want)
	}
	for _, id := range want {
		if _, ok := started[id]; !ok || progress[id] != 1 {
			t.Errorf("task %s: started = %v, progress calls = %d", id, ok, progress[id])
		}
	}
	for _, g := range results {
		for _, r := range g.Results {
			if !slices.Contains(want, r.TaskID) {
				t.Errorf("result for an unknown task %q", r.TaskID)
			}
			for i, proc := range r.Processes {
				if wantID := fmt.Sprintf("%s#%d", r.TaskID, i+1); proc.ID != wantID {
					t.Errorf("process ID = %q, want %q", proc.ID, wantID)
				}
			}
		}
	}
}
