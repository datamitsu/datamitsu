package tooling

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/cache"
	"github.com/datamitsu/datamitsu/internal/config"

	"go.uber.org/zap"
)

const probeVar = "TOOLENV_PROBE"

func inheritingTask(root string, files ...string) Task {
	return Task{
		ToolName:  "probe",
		Operation: config.OpLint,
		OpConfig: config.ToolOperation{
			App: "probe", Scope: config.ToolScopePerFile, Args: []string{"{file}"},
			InheritEnv: []string{probeVar},
		},
		Files:       files,
		ProjectPath: root,
	}
}

// The inherited value is what the tool sees, so it is part of both identities:
// a pass under one value must never answer for another.
func TestInheritedValuesArePartOfTheIdentities(t *testing.T) {
	root := t.TempDir()
	e := &Executor{rootPath: root}
	withValue := func(pairs ...string) Task {
		task := inheritingTask(root)
		task.inherited = pairs
		return task
	}
	base := withValue(probeVar + "=a")

	tests := []struct {
		name string
		task Task
	}{
		{"another value", withValue(probeVar + "=b")},
		{"present but empty", withValue(probeVar + "=")},
		{"absent", withValue()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if verdictIdentity(tt.task, "", "") == verdictIdentity(base, "", "") {
				t.Error("the verdict identity did not change")
			}
			if e.perFileCacheTool(tt.task) == e.perFileCacheTool(base) {
				t.Error("the per-file cache identity did not change")
			}
		})
	}

	t.Run("an inherited pair is not a declared env", func(t *testing.T) {
		declared := inheritingTask(root)
		declared.OpConfig.Env = map[string]string{probeVar: "a"}
		if verdictIdentity(declared, "", "") == verdictIdentity(base, "", "") {
			t.Error("env and inheritEnv share one identity")
		}
	})

	t.Run("an absent variable adds nothing", func(t *testing.T) {
		plain := inheritingTask(root)
		plain.OpConfig.InheritEnv = nil
		if verdictIdentity(withValue(), "", "") != verdictIdentity(plain, "", "") {
			t.Error("naming an absent variable changed the verdict identity")
		}
		if got := e.perFileCacheTool(withValue()); got != "probe" {
			t.Errorf("per-file cache identity = %q, want the bare tool name", got)
		}
	})
}

// The per-file cache is keyed by configuration, which a host value is not: a
// warm cache must still run the tool again when only the inherited value moved,
// and the process must see the value the identity was taken from.
func TestWarmPerFileCacheRerunsWhenAnInheritedValueChanges(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the tool is an sh script")
	}
	root := t.TempDir()
	file := filepath.Join(root, "a.txt")
	if err := os.WriteFile(file, []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	seen := filepath.Join(t.TempDir(), "seen")
	c, err := cache.NewCache(t.TempDir(), root, config.Config{}, nil, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(root, false, false, &mockAppManager{commands: map[string]*binmanager.CommandInfo{
		"probe": shellApp(`printf '%s\n' "${` + probeVar + `-absent}" >> "$SEEN"`),
	}}, c)

	run := func() {
		t.Helper()
		task := inheritingTask(root, file)
		task.OpConfig.Env = map[string]string{"SEEN": seen}
		if result := e.executeTask(context.Background(), task); !result.Success {
			t.Fatalf("the probe failed: %v", result.Error)
		}
	}
	runs := func() []string {
		t.Helper()
		data, err := os.ReadFile(seen)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Fields(string(data))
	}

	t.Setenv(probeVar, "a")
	run()
	run()
	if got := runs(); strings.Join(got, ",") != "a" {
		t.Fatalf("runs = %v, want one run that saw a and a cache hit", got)
	}

	t.Setenv(probeVar, "b")
	run()
	run()
	if got := runs(); strings.Join(got, ",") != "a,b" {
		t.Fatalf("runs = %v, want the changed value to run the tool once more", got)
	}

	if err := os.Unsetenv(probeVar); err != nil {
		t.Fatal(err)
	}
	run()
	if got := runs(); strings.Join(got, ",") != "a,b,absent" {
		t.Fatalf("runs = %v, want an unset variable to run the tool without it", got)
	}
}
