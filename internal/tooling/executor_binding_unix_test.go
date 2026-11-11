//go:build !windows

package tooling

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/config"
)

func TestOperationExecutesItsBoundDependency(t *testing.T) {
	for _, available := range []bool{true, false} {
		name := "available dependency"
		if !available {
			name = "unavailable dependency"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("DATAMITSU_CACHE_DIR", filepath.Join(root, "cache"))
			input := filepath.Join(root, "input.txt")
			if err := os.WriteFile(input, []byte("input"), 0o600); err != nil {
				t.Fatal(err)
			}
			dependencyDir := filepath.Join(root, "dependency with spaces")
			if err := os.MkdirAll(dependencyDir, 0o755); err != nil {
				t.Fatal(err)
			}
			dependency := filepath.Join(dependencyDir, "validator")
			if err := os.WriteFile(dependency, []byte("#!/bin/sh\n[ \"$1\" = --accepted ] || exit 23\nprintf 'bound dependency executed' > \"$2\"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			wrapper := filepath.Join(root, "operation")
			if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec \"$1\" --accepted \"$2\"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			paths := map[string]string{"operation": wrapper}
			if available {
				paths["validator"] = dependency
			}
			executor := NewExecutor(root, false, false, &mockAppManager{binaries: paths}, nil)
			marker := filepath.Join(root, "binding result")
			task := Task{
				ID: "binding-wrapper::1", ToolName: "binding-wrapper", Operation: config.OpLint,
				OpConfig: config.ToolOperation{App: "operation", Args: []string{"{bin:validator}", marker}, Scope: config.ToolScopePerFile},
				Files:    []string{input}, ProjectPath: root,
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			result := executor.executeTask(ctx, task)
			data, readErr := os.ReadFile(marker)
			if available {
				if !result.Success || readErr != nil || string(data) != "bound dependency executed" {
					t.Fatalf("bound dependency did not execute: result=%+v marker=%q err=%v", result, data, readErr)
				}
			} else if result.Success || !errors.Is(readErr, os.ErrNotExist) {
				t.Fatalf("unavailable dependency produced a successful validation: result=%+v marker=%q err=%v", result, data, readErr)
			}
		})
	}
}
