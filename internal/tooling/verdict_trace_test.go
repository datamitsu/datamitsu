package tooling

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/cache"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/trace"

	"go.uber.org/zap"
)

// tracedVerdictFixture builds a unit of n members plus a guard, and an executor
// whose configured command does not exist — every case here asserts on what was
// recorded before the tool would have run.
func tracedVerdictFixture(t *testing.T, n int) (*Executor, Task) {
	t.Helper()
	root := t.TempDir()
	unitDir := filepath.Join(root, "pkg")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	members := make([]string, 0, n)
	for i := range n {
		p := filepath.Join(unitDir, fmt.Sprintf("a%d.ts", i))
		if err := os.WriteFile(p, []byte("export const a = 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		members = append(members, p)
	}
	guard := filepath.Join(unitDir, "tsconfig.json")
	if err := os.WriteFile(guard, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := cache.NewCache(t.TempDir(), root, config.Config{Tools: config.MapOfTools{}}, nil, zap.NewNop())
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	appMgr := &mockAppManager{commands: map[string]*binmanager.CommandInfo{
		"tsc": {Command: filepath.Join(root, "does-not-exist")},
	}}
	e := NewExecutor(root, false, false, appMgr, c)

	task := Task{
		ToolName:  "tsc",
		Operation: config.OpLint,
		OpConfig: config.ToolOperation{
			App: "tsc", Args: []string{"--noEmit"},
			Scope: config.ToolScopePerProject,
		},
		ProjectPath: unitDir,
		UnitDir:     "pkg",
		UnitMembers: members,
		UnitGuards:  []string{guard},
		Coverage:    CoverageComplete,
	}
	return e, task
}

// traceOn turns recording on for one case and restores the previous state, so a
// case asserting the disabled path cannot be poisoned by an earlier one.
func traceOn(t *testing.T) {
	t.Helper()
	prev := trace.Enabled()
	trace.Reset()
	trace.SetEnabled(true)
	t.Cleanup(func() {
		trace.SetEnabled(prev)
		trace.Reset()
	})
}

func verdictSpan(t *testing.T) trace.RecordedSpan {
	t.Helper()
	for _, s := range trace.Spans() {
		if s.Name == "verdictKeys" {
			return s
		}
	}
	t.Fatal("no verdictKeys span was recorded")
	return trace.RecordedSpan{}
}

func counterValue(t *testing.T, name string) int64 {
	t.Helper()
	for _, c := range trace.Counters() {
		if c.Name() == name {
			return c.Value()
		}
	}
	t.Fatalf("counter %q is not registered", name)
	return 0
}

func spanAttr(t *testing.T, s trace.RecordedSpan, key string) any {
	t.Helper()
	v, ok := s.Attr(key)
	if !ok {
		t.Fatalf("span %s has no %q attribute", s.Name, key)
	}
	return v
}

// The point of the instrumentation is to make the trade visible per tool: how
// many members and bytes a task hashed to decide whether to run, and whether
// that decision was a hit.
func TestVerdictSpanRecordsTheCostOfTheDecision(t *testing.T) {
	t.Run("miss", func(t *testing.T) {
		e, task := tracedVerdictFixture(t, 3)
		traceOn(t)

		e.executeTask(context.Background(), task)

		s := verdictSpan(t)
		if got := spanAttr(t, s, "applies"); got != true {
			t.Errorf("applies = %v, want true", got)
		}
		if got := spanAttr(t, s, "hit"); got != false {
			t.Errorf("hit = %v, want false on an empty cache", got)
		}
		if got := spanAttr(t, s, "members"); got != 3 {
			t.Errorf("members = %v, want 3", got)
		}
		if got := spanAttr(t, s, "guards"); got != 1 {
			t.Errorf("guards = %v, want 1", got)
		}
		// 3 members of 19 bytes plus a 2-byte guard.
		wantBytes := int64(3*len("export const a = 1\n") + len("{}"))
		if got := spanAttr(t, s, "bytes"); got != wantBytes {
			t.Errorf("bytes = %v, want %d", got, wantBytes)
		}
		if got := counterValue(t, "cache.verdict_bytes_hashed"); got < wantBytes {
			t.Errorf("cache.verdict_bytes_hashed = %d, want at least %d", got, wantBytes)
		}
	})

	t.Run("hit", func(t *testing.T) {
		e, task := tracedVerdictFixture(t, 2)
		key, inputs, ok := e.verdictKeys(task)
		if !ok {
			t.Fatal("precondition: the verdict cache should apply to this task")
		}
		e.recordVerdict(task, key, inputs, ok)

		traceOn(t)
		result := e.executeTask(context.Background(), task)
		if !result.Success {
			t.Fatalf("a cached verdict did not short-circuit the run: %v", result.Error)
		}
		if got := spanAttr(t, verdictSpan(t), "hit"); got != true {
			t.Errorf("hit = %v, want true for a stored verdict", got)
		}
	})
}

// Tracing off must cost nothing and record nothing — the bytes counter rides on
// the hottest loop in the executor, so a leak here is a leak everywhere.
func TestVerdictTracingRecordsNothingWhenDisabled(t *testing.T) {
	e, task := tracedVerdictFixture(t, 3)

	trace.Reset()
	trace.SetEnabled(false)
	t.Cleanup(trace.Reset)

	e.executeTask(context.Background(), task)

	for _, s := range trace.Spans() {
		t.Errorf("recorded span %s/%s while tracing was off", s.Cat, s.Name)
	}
	if got := counterValue(t, "cache.verdict_bytes_hashed"); got != 0 {
		t.Errorf("cache.verdict_bytes_hashed = %d while tracing was off, want 0", got)
	}
}

// BenchmarkVerdictInputs is the target for the later tasks in this plan: the
// per-task cost of hashing a unit's members, at a size a real monorepo package
// reaches.
func BenchmarkVerdictInputs(b *testing.B) {
	root := b.TempDir()
	unitDir := filepath.Join(root, "pkg")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		b.Fatal(err)
	}
	const members = 2000
	// ~2 KB per file, the order of a typical source file.
	content := make([]byte, 2048)
	for i := range content {
		content[i] = byte('a' + i%26)
	}
	paths := make([]string, 0, members)
	for i := range members {
		p := filepath.Join(unitDir, fmt.Sprintf("f%04d.ts", i))
		if err := os.WriteFile(p, content, 0o644); err != nil {
			b.Fatal(err)
		}
		paths = append(paths, p)
	}
	guard := filepath.Join(unitDir, "tsconfig.json")
	if err := os.WriteFile(guard, []byte("{}"), 0o644); err != nil {
		b.Fatal(err)
	}
	guards := []string{guard}

	b.SetBytes(int64(members * len(content)))
	b.ResetTimer()
	for range b.N {
		if hash, _ := verdictInputs(paths, guards, root); hash == "" {
			b.Fatal("empty input hash")
		}
	}
}
