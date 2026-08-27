package tooling

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
)

// TestPlanOrderIsDeterministic pins that two plans over the same tree come out
// in the same order.
//
// The per-project task list used to be built by ranging over a map, so Go's
// randomized map iteration reordered the plan on every run: `--explain=json`
// could not be diffed between two invocations of the same binary on an unchanged
// repository, and the execution order was arbitrary. A single run cannot catch
// that — the test has to compare two.
func TestPlanOrderIsDeterministic(t *testing.T) {
	root := t.TempDir()
	// Enough sibling packages that a random iteration order is essentially
	// certain to differ between two runs.
	for _, pkg := range []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel"} {
		dir := filepath.Join(root, "packages", pkg)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dir, "package.json"), `{"name":"`+pkg+`"}`)
		write(t, filepath.Join(dir, "index.ts"), "export const x = 1\n")
		write(t, filepath.Join(dir, "other.ts"), "export const y = 2\n")
	}

	tools := config.MapOfTools{
		"tsc": {
			Name: "tsc",
			Operations: map[config.OperationType]config.ToolOperation{
				config.OpLint: {App: "tsc", Scope: config.ToolScopePerProject, Globs: []string{"**/*.ts"}},
			},
		},
	}
	types := config.MapOfProjectTypes{
		"npm-package": {Markers: []string{"**/package.json"}},
	}

	plans := make([]string, 2)
	for i := range plans {
		p := NewPlanner(root, root, nil, tools, types, nil)
		plan, err := p.Plan(context.Background(), config.OpLint, Selection{Mode: SelectionAll}, nil)
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		plans[i] = planFingerprint(plan)
	}

	if plans[0] != plans[1] {
		t.Errorf("plan order differs between runs:\n run 1: %s\n run 2: %s", plans[0], plans[1])
	}
	if plans[0] == "" {
		t.Fatal("plan produced no tasks; the fixture is not exercising the ordering")
	}
}

// planFingerprint renders a plan's task order as a comparable string.
func planFingerprint(plan *ExecutionPlan) string {
	var b strings.Builder
	for _, g := range plan.Groups {
		for _, task := range g.Tasks {
			b.WriteString(task.ToolName)
			b.WriteString("@")
			b.WriteString(task.ProjectPath)
			b.WriteString(";")
			for _, f := range task.Files {
				b.WriteString(f)
				b.WriteString(",")
			}
			b.WriteString("|")
		}
	}
	return b.String()
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
