package tooling

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/cache"
	"github.com/datamitsu/datamitsu/internal/config"

	"go.uber.org/zap"
)

// newVerdictExecutor builds an Executor backed by a real cache in a temp dir.
func newVerdictExecutor(t *testing.T) (*Executor, string) {
	t.Helper()
	root := t.TempDir()
	c, err := cache.NewCache(t.TempDir(), root, config.Config{Tools: config.MapOfTools{}}, nil, zap.NewNop())
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	return &Executor{rootPath: root, cache: c}, root
}

// unitTask is a task the verdict cache applies to: unit granularity, one member.
// Tool is populated because the planner always populates it, and sibling
// invalidation reads the tool's other operations out of it.
func unitTask(root string) Task {
	op := config.ToolOperation{
		App: "tsc", Args: []string{"--noEmit"},
		Scope: config.ToolScopePerProject,
	}
	return Task{
		ToolName:  "tsc",
		Tool:      config.Tool{Operations: map[config.OperationType]config.ToolOperation{config.OpLint: op}},
		Operation: config.OpLint,
		OpConfig:  op,

		UnitDir:     "pkg",
		UnitMembers: []string{filepath.Join(root, "pkg", "a.ts")},
		Coverage:    CoverageComplete,
	}
}

func TestVerdictKeysAppliesOnlyWhereItIsSound(t *testing.T) {
	e, root := newVerdictExecutor(t)

	tests := []struct {
		name   string
		mutate func(*Task)
		cache  *cache.Cache
		want   bool
	}{
		{"unit granularity with members", func(*Task) {}, e.cache, true},
		{"no cache configured", func(*Task) {}, nil, false},
		{
			// Per-file verdicts are the existing per-file cache's job.
			"file granularity",
			func(task *Task) {
				task.OpConfig.Scope = config.ToolScopePerFile
				task.OpConfig.Args = []string{"{file}"}
			},
			e.cache, false,
		},
		{
			"cache disabled on the operation",
			func(task *Task) { task.OpConfig.Cache = new(false) },
			e.cache, false,
		},
		{
			// repo granularity is opt-in: the input vector degenerates to every
			// tracked file, which costs more than the tool and hits almost never.
			"repo granularity without opt-in",
			func(task *Task) { task.OpConfig.Scope = config.ToolScopeRepository },
			e.cache, false,
		},
		{
			"repo granularity with cache: true",
			func(task *Task) {
				task.OpConfig.Scope = config.ToolScopeRepository
				task.OpConfig.Cache = new(true)
			},
			e.cache, true,
		},
		{
			// Constant input vector means the verdict could never mismatch.
			"no members",
			func(task *Task) { task.UnitMembers = nil },
			e.cache, false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := unitTask(root)
			tt.mutate(&task)
			exec := &Executor{rootPath: root, cache: tt.cache}

			key, inputs, ok := exec.verdictKeys(task)
			if ok != tt.want {
				t.Fatalf("verdictKeys ok = %v, want %v", ok, tt.want)
			}
			if !ok {
				if key != "" || inputs != "" {
					t.Errorf("an inapplicable task returned key=%q inputs=%q, want both empty", key, inputs)
				}
				return
			}
			if key == "" || inputs == "" {
				t.Errorf("applicable task returned key=%q inputs=%q, want both set", key, inputs)
			}
		})
	}
}

// The key is identity: anything that changes the question must change it.
func TestVerdictIdentitySeparatesDistinctQuestions(t *testing.T) {
	root := t.TempDir()
	base := unitTask(root)
	baseKey := verdictIdentity(base, base.UnitDir)

	tests := []struct {
		name   string
		mutate func(*Task)
	}{
		{"tool", func(task *Task) { task.ToolName = "eslint" }},
		{"operation", func(task *Task) { task.Operation = config.OpFix }},
		{"args", func(task *Task) { task.OpConfig.Args = []string{"--noEmit", "--strict"} }},
		{"declared env", func(task *Task) { task.OpConfig.Env = map[string]string{"TS_NODE": "1"} }},
		{"granularity", func(task *Task) { task.OpConfig.Granularity = config.GranularityRepo }},
		{"arity", func(task *Task) { task.OpConfig.Args = []string{"--noEmit", "{files}"} }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := unitTask(root)
			tt.mutate(&task)
			if got := verdictIdentity(task, task.UnitDir); got == baseKey {
				t.Errorf("changing the %s left the identity unchanged; the cache would answer a different question", tt.name)
			}
		})
	}

	t.Run("unit dir", func(t *testing.T) {
		if verdictIdentity(base, "other") == baseKey {
			t.Error("two units share one identity; a pass in one would satisfy the other")
		}
	})

	t.Run("stable across calls", func(t *testing.T) {
		if verdictIdentity(unitTask(root), "pkg") != baseKey {
			t.Error("identity is unstable; nothing would ever hit")
		}
	})
}

func TestSortedEnv(t *testing.T) {
	if got := sortedEnv(nil); got != nil {
		t.Errorf("sortedEnv(nil) = %v, want nil", got)
	}
	if got := sortedEnv(map[string]string{}); got != nil {
		t.Errorf("sortedEnv(empty) = %v, want nil", got)
	}

	// Map order is random, so the hash needs a deterministic ordering.
	got := sortedEnv(map[string]string{"Z": "1", "A": "2", "M": "3"})
	want := []string{"A=2", "M=3", "Z=1"}
	if len(got) != len(want) {
		t.Fatalf("sortedEnv = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sortedEnv = %v, want %v", got, want)
		}
	}
}

func TestInheritedEnvFiltersToTheAllowlist(t *testing.T) {
	t.Setenv("GOFLAGS", "-tags=integration")
	t.Setenv("DATAMITSU_UNRELATED_VAR", "x")

	got := inheritedEnv()

	var sawAllowed, sawUnrelated bool
	for _, kv := range got {
		switch kv {
		case "GOFLAGS=-tags=integration":
			sawAllowed = true
		case "DATAMITSU_UNRELATED_VAR=x":
			sawUnrelated = true
		}
	}
	if !sawAllowed {
		t.Error("GOFLAGS is missing; it changes the package graph without changing the key")
	}
	if sawUnrelated {
		t.Error("an unrelated variable leaked in; session-scoped values prevent every hit")
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("inheritedEnv is unsorted at %d: %v", i, got)
		}
	}
}

// A missing file must hash to something, or deleting a file would look like no
// change at all and the stale pass would survive it.
func TestHashedPathsMarksMissingFiles(t *testing.T) {
	root := t.TempDir()
	present := filepath.Join(root, "a.ts")
	if err := os.WriteFile(present, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(root, "gone.ts")

	got, _ := hashedPaths([]string{present, missing}, root)
	if len(got) != 2 {
		t.Fatalf("hashedPaths = %v, want 2 entries", got)
	}
	// Relative, so moving the repository does not orphan every entry.
	for _, entry := range got {
		if filepath.IsAbs(entry[:len(entry)-len(filepath.Ext(entry))]) {
			t.Errorf("entry %q is absolute", entry)
		}
	}
	if got[0] != "a.ts\x00"+hashOf(t, "x") {
		t.Errorf("present file entry = %q", got[0])
	}
	if got[1] != "gone.ts\x00(missing)" {
		t.Errorf("missing file entry = %q, want the sentinel", got[1])
	}
}

func hashOf(t *testing.T, s string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, _ := hashedPaths([]string{p}, dir)
	return entries[0][len("f\x00"):]
}

// Without Init() the executor must still get a usable TTL, or every embedded or
// test use silently loses the cache.
func TestVerdictTTLFallsBackToTheCompileTimeDefault(t *testing.T) {
	e, _ := newVerdictExecutor(t)
	if got := e.verdictTTL(); got <= 0 {
		t.Errorf("verdictTTL = %v, want a positive duration", got)
	}
}

func TestRecordVerdict(t *testing.T) {
	t.Run("records a complete run", func(t *testing.T) {
		e, root := newVerdictExecutor(t)
		task := unitTask(root)
		writeMembers(t, task)

		key, inputs, ok := e.verdictKeys(task)
		e.recordVerdict(task, key, inputs, ok)

		if e.cache.ShouldRunVerdict(key, inputs, time.Hour) {
			t.Error("the verdict was not stored; the next identical run repeats the work")
		}
	})

	t.Run("refuses a narrowed run", func(t *testing.T) {
		e, root := newVerdictExecutor(t)
		task := unitTask(root)
		task.Coverage = CoveragePartial
		writeMembers(t, task)

		key, inputs, ok := e.verdictKeys(task)
		e.recordVerdict(task, key, inputs, ok)

		if !e.cache.ShouldRunVerdict(key, inputs, time.Hour) {
			t.Error("a partial run stamped a whole-unit pass")
		}
	})

	t.Run("refuses when the cache does not apply", func(t *testing.T) {
		e, root := newVerdictExecutor(t)
		task := unitTask(root)
		writeMembers(t, task)
		key, inputs, _ := e.verdictKeys(task)

		e.recordVerdict(task, key, inputs, false)

		if !e.cache.ShouldRunVerdict(key, inputs, time.Hour) {
			t.Error("recorded a verdict for a task the cache does not apply to")
		}
	})

	// A lint that ran while its inputs moved proves nothing about the state on
	// disk now, so it must not be recorded at all.
	t.Run("drops a torn read for a read-only operation", func(t *testing.T) {
		e, root := newVerdictExecutor(t)
		task := unitTask(root)
		writeMembers(t, task)
		key, inputs, ok := e.verdictKeys(task)

		if err := os.WriteFile(task.UnitMembers[0], []byte("edited mid-run"), 0o644); err != nil {
			t.Fatal(err)
		}
		e.recordVerdict(task, key, inputs, ok)

		after, _ := verdictInputs(task.UnitMembers, task.UnitGuards, root)
		if !e.cache.ShouldRunVerdict(key, after, time.Hour) {
			t.Error("a lint recorded a pass for a state that no longer exists")
		}
		if !e.cache.ShouldRunVerdict(key, inputs, time.Hour) {
			t.Error("a lint recorded a pass against the pre-run state")
		}
	})

	// A fix is supposed to change its inputs, so it records the state it produced.
	t.Run("records the produced state for a fix", func(t *testing.T) {
		e, root := newVerdictExecutor(t)
		task := unitTask(root)
		task.Operation = config.OpFix
		writeMembers(t, task)
		key, inputs, ok := e.verdictKeys(task)

		if err := os.WriteFile(task.UnitMembers[0], []byte("formatted"), 0o644); err != nil {
			t.Fatal(err)
		}
		e.recordVerdict(task, key, inputs, ok)

		after, _ := verdictInputs(task.UnitMembers, task.UnitGuards, root)
		if e.cache.ShouldRunVerdict(key, after, time.Hour) {
			t.Error("a fix did not record the state it produced, so it can never hit")
		}
	})

	// The sibling has to be the real lint operation, not the fix task relabelled.
	// verdictIdentity hashes args, env, granularity and arity, so a fix carrying
	// --write and a lint carrying --check hash differently: relabelling deleted a
	// key no lint ever used and left the actual lint verdict standing. The case
	// below is the norm — a fix and a lint sharing byte-identical args is the
	// exception, and it is what made this invisible.
	t.Run("invalidates the lint identity even when the args differ", func(t *testing.T) {
		e, root := newVerdictExecutor(t)

		lintOp := config.ToolOperation{
			App: "prettier", Args: []string{"--check"},
			Scope: config.ToolScopePerProject,
		}
		fixOp := config.ToolOperation{
			App: "prettier", Args: []string{"--write"},
			Scope: config.ToolScopePerProject,
		}
		tool := config.Tool{Operations: map[config.OperationType]config.ToolOperation{
			config.OpLint: lintOp,
			config.OpFix:  fixOp,
		}}

		lint := unitTask(root)
		lint.Tool, lint.OpConfig, lint.Operation = tool, lintOp, config.OpLint
		writeMembers(t, lint)
		lintKey, lintInputs, lintOK := e.verdictKeys(lint)
		e.recordVerdict(lint, lintKey, lintInputs, lintOK)
		if e.cache.ShouldRunVerdict(lintKey, lintInputs, time.Hour) {
			t.Fatal("precondition: the lint verdict should be stored")
		}

		fix := lint
		fix.OpConfig, fix.Operation = fixOp, config.OpFix
		fixKey, fixInputs, fixOK := e.verdictKeys(fix)
		e.recordVerdict(fix, fixKey, fixInputs, fixOK)

		if !e.cache.ShouldRunVerdict(lintKey, lintInputs, time.Hour) {
			t.Error("the fix left the real lint verdict standing; the next lint reuses a " +
				"pass for content the fix rewrote")
		}
		if e.cache.ShouldRunVerdict(fixKey, fixInputs, time.Hour) {
			t.Error("the fix deleted its own verdict")
		}
	})

	// A fix that rewrote files invalidates the matching lint — but only that
	// sibling, or it would delete the verdict it just wrote.
	t.Run("invalidates the sibling lint but keeps its own verdict", func(t *testing.T) {
		e, root := newVerdictExecutor(t)

		lint := unitTask(root)
		writeMembers(t, lint)
		lintKey, lintInputs, lintOK := e.verdictKeys(lint)
		e.recordVerdict(lint, lintKey, lintInputs, lintOK)
		if e.cache.ShouldRunVerdict(lintKey, lintInputs, time.Hour) {
			t.Fatal("precondition: the lint verdict should be stored")
		}

		fix := lint
		fix.Operation = config.OpFix
		fixKey, fixInputs, fixOK := e.verdictKeys(fix)
		e.recordVerdict(fix, fixKey, fixInputs, fixOK)

		if !e.cache.ShouldRunVerdict(lintKey, lintInputs, time.Hour) {
			t.Error("the fix left a stale lint verdict behind")
		}
		if e.cache.ShouldRunVerdict(fixKey, fixInputs, time.Hour) {
			t.Error("the fix deleted its own verdict; no fix could ever hit the cache")
		}
	})
}

func writeMembers(t *testing.T, task Task) {
	t.Helper()
	for _, m := range task.UnitMembers {
		if err := os.MkdirAll(filepath.Dir(m), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(m, []byte("original"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
