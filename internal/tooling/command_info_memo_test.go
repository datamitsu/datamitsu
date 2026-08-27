package tooling

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

// countingAppManager records every GetCommandInfo call per app and hands back one
// shared CommandInfo per app — exactly what BinManager does not do, so a caller
// that mutates the result is visible here as a change in the source object.
type countingAppManager struct {
	mu       sync.Mutex
	calls    map[string]int
	commands map[string]*binmanager.CommandInfo
}

func (m *countingAppManager) GetBinaryPath(_ context.Context, appName string) (string, error) {
	if info, ok := m.commands[appName]; ok {
		return info.Command, nil
	}
	return "", fmt.Errorf("binary not found: %s", appName)
}

func (m *countingAppManager) GetCommandInfo(_ context.Context, appName string) (*binmanager.CommandInfo, error) {
	m.mu.Lock()
	m.calls[appName]++
	m.mu.Unlock()
	if info, ok := m.commands[appName]; ok {
		return info, nil
	}
	return nil, fmt.Errorf("app not found: %s", appName)
}

func (m *countingAppManager) callsFor(app string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[app]
}

// memoPlanFixture builds a plan of n tasks over two apps, both pointing at a
// command that exists (true), so every task actually reaches the resolve.
func memoPlanFixture(t *testing.T, n int) (*Executor, *countingAppManager, *ExecutionPlan) {
	t.Helper()
	root := t.TempDir()
	file := filepath.Join(root, "a.txt")
	if err := os.WriteFile(file, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := &countingAppManager{
		calls: map[string]int{},
		commands: map[string]*binmanager.CommandInfo{
			"alpha": {Type: "binary", Command: "true", Env: map[string]string{"K": "v"}, Args: []string{"-x"}},
			"beta":  {Type: "binary", Command: "true"},
		},
	}
	e := NewExecutor(root, true /* dryRun: no process is spawned */, false, mgr, nil)

	tasks := make([]Task, 0, n)
	for i := range n {
		app := "alpha"
		if i%2 == 1 {
			app = "beta"
		}
		tasks = append(tasks, Task{
			ToolName:  fmt.Sprintf("tool%d", i),
			Operation: config.OpLint,
			OpConfig: config.ToolOperation{
				App:   app,
				Scope: config.ToolScopeRepository,
			},
			ProjectPath: root,
			Files:       []string{file},
		})
	}
	return e, mgr, &ExecutionPlan{Groups: []TaskGroup{{Priority: 1, Tasks: tasks}}}
}

// The plan's tools are already installed by EnsureTools before Execute runs, so
// resolving a command per task re-walks the store for an answer that cannot have
// changed. One resolve per distinct app is the contract.
func TestExecuteResolvesEachAppOnce(t *testing.T) {
	e, mgr, plan := memoPlanFixture(t, 8)

	if _, err := e.Execute(context.Background(), plan); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got := mgr.callsFor("alpha"); got != 1 {
		t.Errorf("GetCommandInfo(alpha) called %d times across 4 tasks, want 1", got)
	}
	if got := mgr.callsFor("beta"); got != 1 {
		t.Errorf("GetCommandInfo(beta) called %d times across 4 tasks, want 1", got)
	}
}

// The memo lives for one Execute: a tool installed between two runs must be seen
// by the second one.
func TestMemoDoesNotSurviveExecute(t *testing.T) {
	e, mgr, plan := memoPlanFixture(t, 2)

	for range 2 {
		if _, err := e.Execute(context.Background(), plan); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
	}

	if got := mgr.callsFor("alpha"); got != 2 {
		t.Errorf("GetCommandInfo(alpha) called %d times across two Executes, want 2", got)
	}
}

// Handing out the shared pointer would let one task's env or args edit rewrite
// another task's command line — the memo hands out copies.
func TestMemoHandsOutIsolatedCopies(t *testing.T) {
	source := &binmanager.CommandInfo{
		Type:          "shell",
		Command:       "sh",
		Args:          []string{"-c"},
		Env:           map[string]string{"K": "v"},
		RequiredPaths: []string{"/opt/a"},
	}
	mgr := &countingAppManager{
		calls:    map[string]int{},
		commands: map[string]*binmanager.CommandInfo{"alpha": source},
	}
	memo := newCommandInfoMemo()
	ctx := context.Background()

	first, err := memo.get(ctx, "alpha", mgr.GetCommandInfo)
	if err != nil {
		t.Fatalf("get() error = %v", err)
	}
	first.Env["K"] = "poisoned"
	first.Env["ADDED"] = "1"
	first.Args[0] = "-poisoned"
	first.RequiredPaths[0] = "/poisoned"
	first.Command = "poisoned"

	second, err := memo.get(ctx, "alpha", mgr.GetCommandInfo)
	if err != nil {
		t.Fatalf("second get() error = %v", err)
	}
	if second.Env["K"] != "v" || len(second.Env) != 1 {
		t.Errorf("second Env = %v, want the resolved {K:v}", second.Env)
	}
	if second.Args[0] != "-c" {
		t.Errorf("second Args = %v, want [-c]", second.Args)
	}
	if second.RequiredPaths[0] != "/opt/a" {
		t.Errorf("second RequiredPaths = %v, want [/opt/a]", second.RequiredPaths)
	}
	if second.Command != "sh" {
		t.Errorf("second Command = %q, want %q", second.Command, "sh")
	}
	// The resolved object itself must be untouched too.
	if source.Env["K"] != "v" || source.Args[0] != "-c" {
		t.Errorf("the memoized CommandInfo was mutated through a caller's copy: %+v", source)
	}
}

// The executor's worker pool starts many tasks for one app at once; the resolve
// must still happen exactly once, and every caller must get a usable answer.
func TestMemoIsSingleFlightedUnderConcurrency(t *testing.T) {
	mgr := &countingAppManager{
		calls: map[string]int{},
		commands: map[string]*binmanager.CommandInfo{
			"alpha": {Type: "binary", Command: "true", Env: map[string]string{"K": "v"}},
		},
	}
	memo := newCommandInfoMemo()

	var wg sync.WaitGroup
	var bad atomic.Int64
	for range 16 {
		wg.Go(func() {
			info, err := memo.get(context.Background(), "alpha", mgr.GetCommandInfo)
			if err != nil || info == nil || info.Command != "true" {
				bad.Add(1)
			}
		})
	}
	wg.Wait()

	if n := bad.Load(); n != 0 {
		t.Errorf("%d concurrent get() calls returned a wrong answer", n)
	}
	if got := mgr.callsFor("alpha"); got != 1 {
		t.Errorf("GetCommandInfo(alpha) called %d times from 16 goroutines, want 1", got)
	}
}

// A resolve failure is memoized as well: within one Execute an unresolvable tool
// cannot become resolvable, and retrying per task turns one error into N
// installs. The error must still reach every caller.
func TestMemoRemembersFailure(t *testing.T) {
	mgr := &countingAppManager{calls: map[string]int{}}
	memo := newCommandInfoMemo()

	for range 3 {
		if _, err := memo.get(context.Background(), "missing", mgr.GetCommandInfo); err == nil {
			t.Fatal("get() error = nil, want the resolve failure")
		}
	}
	if got := mgr.callsFor("missing"); got != 1 {
		t.Errorf("GetCommandInfo(missing) called %d times, want 1", got)
	}
}
