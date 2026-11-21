package lsp

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	zerovalue_sync "sync"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

// shellApps resolves every app to the host `sh`: the cheapest real command the
// executor can run, so these tests exercise the executor itself.
type shellApps struct{}

func (shellApps) GetBinaryPath(context.Context, string) (string, error) { return "sh", nil }

func (shellApps) GetCommandInfo(context.Context, string) (*binmanager.CommandInfo, error) {
	return &binmanager.CommandInfo{Args: nil, Env: nil, Artifact: "", ToolCacheDir: "", ToolCacheSlots: nil, RequiredPaths: nil, Type: "shell", Command: "sh"}, nil
}

func (shellApps) ResolveBindingPath(_ context.Context, _ string, alias string) (string, error) {
	return "sh", nil
}

// recordingPlan has one group per name; each group's task appends its name to
// ran.log in root, so the log is the order groups actually ran in.
func recordingPlan(root string, names ...string) *tooling.ExecutionPlan {
	plan := &tooling.ExecutionPlan{ConfigName: "", FailOn: "", Groups: nil, Skipped: nil}
	for i, name := range names {
		plan.Groups = append(plan.Groups, tooling.TaskGroup{Priority: i, Tasks: []tooling.Task{{
			ID:          "",
			Tool:        config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil},
			Operation:   "",
			Files:       nil,
			UnitDir:     "",
			UnitMembers: nil,
			UnitGuards:  nil,
			Coverage:    "",
			ToolName:    name,
			ProjectPath: root,
			OpConfig: config.ToolOperation{
				Arity:             "",
				Granularity:       "",
				Globs:             nil,
				ExcludeGlobs:      nil,
				Priority:          0,
				Cache:             nil,
				InvalidateOn:      nil,
				Env:               nil,
				InheritEnv:        nil,
				FailOn:            "",
				Input:             "",
				LSP:               nil,
				Output:            "",
				ManagedConfigRefs: nil,
				App:               "sh",
				Scope:             config.ToolScopeRepository,
				Args:              []string{"-c", "echo " + name + " >> ran.log"},
			},
		}}})
	}
	return plan
}

// steppingClock returns start on its first call and start+step on every later
// one: the first group reads the start time, each check sees step elapsed.
func steppingClock(step time.Duration) func() time.Time {
	start := time.Unix(1_700_000_000, 0)
	calls := 0
	return func() time.Time {
		calls++
		if calls == 1 {
			return start
		}
		return start.Add(step)
	}
}

// The watchdog stops at a group boundary: the first group always runs, no
// further group starts once the limit has elapsed, and a running tool is never
// cancelled. Zero disables it.
func TestExecuteWithWatchdog(t *testing.T) {
	tests := []struct {
		name    string
		limit   time.Duration
		elapsed time.Duration
		wantRan []string
	}{
		{"within the limit every group runs", 3 * time.Second, time.Second, []string{"g1", "g2", "g3"}},
		{"the first group runs even past the limit", 3 * time.Second, time.Hour, []string{"g1"}},
		{"reaching the limit exactly stops", 3 * time.Second, 3 * time.Second, []string{"g1"}},
		{"zero disables the watchdog", 0, time.Hour, []string{"g1", "g2", "g3"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			s := &Server{
				conn:              nil,
				loader:            nil,
				launchDir:         "",
				supersededKeys:    nil,
				watch:             nil,
				loadedInputs:      "",
				failedInputs:      "",
				noSession:         "",
				noSessionReported: "",
				refused:           nil,
				options:           nil,
				optionsInvalid:    false,
				policy:            formatPolicy{WidenTo: "", TimeoutMs: 0, Tools: nil},
				docs:              nil,
				initialized:       false,
				shutdownReceived:  false,
				exitCode:          0,
				active:            nil,
				runMu:             zerovalue_sync.Mutex{},
				stopping:          false,
				transport:         nil,
				root:              root,
				loaded:            &session{planner: nil, binMgr: nil, cache: nil, parsers: nil, fixWidenTo: "", managedConfigs: nil, tools: nil, foreignCacheReported: false, executor: tooling.NewExecutor(root, false, false, shellApps{}, nil)},
				now:               steppingClock(tt.elapsed),
			}
			plan := recordingPlan(root, "g1", "g2", "g3")

			results, ran, _, err := s.executeGroups(context.Background(), plan, tt.limit, nil)
			if err != nil {
				t.Fatalf("executeGroups: %v", err)
			}
			if ran != len(tt.wantRan) || len(results) != len(tt.wantRan) {
				t.Errorf("ran = %d with %d results, want %d", ran, len(results), len(tt.wantRan))
			}
			if got := ranLog(t, root); !slices.Equal(got, tt.wantRan) {
				t.Errorf("groups that ran = %v, want %v", got, tt.wantRan)
			}
		})
	}
}

// A cancel is checked before every group, the first included — unlike the
// watchdog, which always lets the first run — and wins over the watchdog when
// both apply. The group that is running is never interrupted.
func TestExecuteGroupsStopsWhenCancelled(t *testing.T) {
	tests := []struct {
		name     string
		after    int // groups that have run when the cancel arrives
		limit    time.Duration
		wantRan  []string
		wantStop groupsStop
	}{
		{limit: 0, wantRan: nil, name: "before the first group", after: 0, wantStop: stoppedByCancel},
		{limit: 0, name: "after the first group", after: 1, wantRan: []string{"g1"}, wantStop: stoppedByCancel},
		{name: "over the watchdog", after: 1, limit: time.Nanosecond, wantRan: []string{"g1"}, wantStop: stoppedByCancel},
		{limit: 0, name: "never", after: 99, wantRan: []string{"g1", "g2", "g3"}, wantStop: ranAll},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			s := &Server{
				conn:              nil,
				loader:            nil,
				launchDir:         "",
				supersededKeys:    nil,
				watch:             nil,
				loadedInputs:      "",
				failedInputs:      "",
				noSession:         "",
				noSessionReported: "",
				refused:           nil,
				options:           nil,
				optionsInvalid:    false,
				policy:            formatPolicy{WidenTo: "", TimeoutMs: 0, Tools: nil},
				docs:              nil,
				initialized:       false,
				shutdownReceived:  false,
				exitCode:          0,
				active:            nil,
				runMu:             zerovalue_sync.Mutex{},
				stopping:          false,
				transport:         nil,
				root:              root,
				loaded:            &session{planner: nil, binMgr: nil, cache: nil, parsers: nil, fixWidenTo: "", managedConfigs: nil, tools: nil, foreignCacheReported: false, executor: tooling.NewExecutor(root, false, false, shellApps{}, nil)},
				now:               steppingClock(time.Hour),
			}
			cancelled := func() bool { return len(ranLog(t, root)) >= tt.after }

			_, ran, stop, err := s.executeGroups(context.Background(), recordingPlan(root, "g1", "g2", "g3"), tt.limit, cancelled)
			if err != nil {
				t.Fatalf("executeGroups: %v", err)
			}
			if got := ranLog(t, root); !slices.Equal(got, tt.wantRan) || ran != len(tt.wantRan) {
				t.Errorf("groups that ran = %v (ran %d), want %v", got, ran, tt.wantRan)
			}
			if stop != tt.wantStop {
				t.Errorf("stop = %v, want %v", stop, tt.wantStop)
			}
		})
	}
}

func TestWatchdogNotice(t *testing.T) {
	notRun := []tooling.TaskGroup{
		{Priority: 0, Tasks: []tooling.Task{{ID: "", Tool: config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil}, Operation: "", OpConfig: config.ToolOperation{App: "", Args: nil, Scope: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil}, Files: nil, ProjectPath: "", UnitDir: "", UnitMembers: nil, UnitGuards: nil, Coverage: "", ToolName: "prettier"}, {ID: "", Tool: config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil}, Operation: "", OpConfig: config.ToolOperation{App: "", Args: nil, Scope: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil}, Files: nil, ProjectPath: "", UnitDir: "", UnitMembers: nil, UnitGuards: nil, Coverage: "", ToolName: "eslint"}}},
		{Priority: 0, Tasks: []tooling.Task{{ID: "", Tool: config.Tool{Name: "", ProjectTypes: nil, Skip: false, SkipReason: "", Operations: nil, OutputParser: nil}, Operation: "", OpConfig: config.ToolOperation{App: "", Args: nil, Scope: "", Arity: "", Granularity: "", Globs: nil, ExcludeGlobs: nil, Priority: 0, Cache: nil, InvalidateOn: nil, Env: nil, InheritEnv: nil, FailOn: "", Input: "", LSP: nil, Output: "", ManagedConfigRefs: nil}, Files: nil, ProjectPath: "", UnitDir: "", UnitMembers: nil, UnitGuards: nil, Coverage: "", ToolName: "prettier"}}},
	}
	got := watchdogNotice("src/a.ts", 1, 3, 3000, notRun)
	for _, want := range []string{
		"src/a.ts", "1 of 3", "3000ms", "did not run: prettier, eslint",
		"datamitsu.format.timeoutMs", "DATAMITSU_LSP_FORMAT_TIMEOUT_MS", "format.widenTo",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("notice %q does not mention %q", got, want)
		}
	}
}

func ranLog(t *testing.T, root string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "ran.log"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(raw))
}
