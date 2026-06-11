package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/timing"
	"github.com/datamitsu/datamitsu/internal/tooling"
)

func TestIsCI(t *testing.T) {
	tests := []struct {
		name     string
		ciValue  string
		expected bool
	}{
		{
			name:     "CI=true",
			ciValue:  "true",
			expected: true,
		},
		{
			name:     "CI=false",
			ciValue:  "false",
			expected: true,
		},
		{
			name:     "CI not set",
			ciValue:  "",
			expected: false,
		},
		{
			name:     "CI=1",
			ciValue:  "1",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalCI := os.Getenv("CI")
			defer func() { _ = os.Setenv("CI", originalCI) }() //nolint:usetesting // explicit unset required

			if tt.ciValue == "" {
				_ = os.Unsetenv("CI")
			} else {
				_ = os.Setenv("CI", tt.ciValue) //nolint:usetesting // explicit unset required
			}

			result := env.IsCI()
			if result != tt.expected {
				t.Errorf("env.IsCI() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestFormatToolWithDir(t *testing.T) {
	tests := []struct {
		name        string
		toolName    string
		relativeDir string
		expected    string
	}{
		{
			name:        "tool without directory",
			toolName:    "eslint",
			relativeDir: "",
			expected:    "⏳ eslint",
		},
		{
			name:        "tool with directory",
			toolName:    "eslint",
			relativeDir: "packages/web",
			expected:    "⏳ eslint (packages/web)",
		},
		{
			name:        "tool with nested directory",
			toolName:    "golangci-lint",
			relativeDir: "services/api/internal",
			expected:    "⏳ golangci-lint (services/api/internal)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatToolWithDir(tt.toolName, tt.relativeDir)
			if result != tt.expected {
				t.Errorf("formatToolWithDir(%q, %q) = %q, expected %q", tt.toolName, tt.relativeDir, result, tt.expected)
			}
		})
	}
}

func TestNonCIProgressDescriptionWithDir(t *testing.T) {
	t.Run("file progress description includes directory", func(t *testing.T) {
		savedActiveTools := activeTools
		defer func() { activeTools = savedActiveTools }()

		activeTools = map[string]map[string]bool{
			"eslint": {"packages/web": true},
		}

		result := formatToolWithDir("eslint", "packages/web")
		if !strings.Contains(result, "packages/web") {
			t.Errorf("non-CI progress description should include directory, got: %q", result)
		}
		if !strings.Contains(result, "eslint") {
			t.Errorf("non-CI progress description should include tool name, got: %q", result)
		}
	})
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		ms       int64
		expected string
	}{
		{
			name:     "less than 100ms",
			ms:       50,
			expected: "50ms",
		},
		{
			name:     "exactly 100ms",
			ms:       100,
			expected: "0.10s (100ms)",
		},
		{
			name:     "less than 1 second",
			ms:       500,
			expected: "0.50s (500ms)",
		},
		{
			name:     "exactly 1 second",
			ms:       1000,
			expected: "1.00s (1000ms)",
		},
		{
			name:     "less than 1 minute",
			ms:       45000,
			expected: "45.00s (45000ms)",
		},
		{
			name:     "exactly 1 minute",
			ms:       60000,
			expected: "1m0.00s (60000ms)",
		},
		{
			name:     "more than 1 minute",
			ms:       125000,
			expected: "2m5.00s (125000ms)",
		},
		{
			name:     "multiple minutes with decimals",
			ms:       185500,
			expected: "3m5.50s (185500ms)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDuration(tt.ms)
			if result != tt.expected {
				t.Errorf("formatDuration(%d) = %q, expected %q", tt.ms, result, tt.expected)
			}
		})
	}
}

func TestGroupResultsByTool(t *testing.T) {
	t.Run("single tool single result", func(t *testing.T) {
		groupResults := []tooling.GroupExecutionResult{
			{
				Results: []tooling.ExecutionResult{
					{
						ToolName:    "eslint",
						Success:     true,
						Duration:    1000,
						RelativeDir: ".",
					},
				},
			},
		}

		groups := groupResultsByTool(groupResults)

		if len(groups) != 1 {
			t.Fatalf("expected 1 group, got %d", len(groups))
		}

		group := groups[0]
		if group.toolName != "eslint" {
			t.Errorf("expected tool name 'eslint', got %q", group.toolName)
		}
		if group.totalRuns != 1 {
			t.Errorf("expected totalRuns 1, got %d", group.totalRuns)
		}
		if group.succeededRuns != 1 {
			t.Errorf("expected succeededRuns 1, got %d", group.succeededRuns)
		}
		if group.failedRuns != 0 {
			t.Errorf("expected failedRuns 0, got %d", group.failedRuns)
		}
		if group.totalTime != 1000 {
			t.Errorf("expected totalTime 1000, got %d", group.totalTime)
		}
		if group.minTime != 1000 {
			t.Errorf("expected minTime 1000, got %d", group.minTime)
		}
		if group.maxTime != 1000 {
			t.Errorf("expected maxTime 1000, got %d", group.maxTime)
		}
	})

	t.Run("single tool multiple results", func(t *testing.T) {
		groupResults := []tooling.GroupExecutionResult{
			{
				Results: []tooling.ExecutionResult{
					{
						ToolName:    "eslint",
						Success:     true,
						Duration:    1000,
						RelativeDir: "dir1",
					},
					{
						ToolName:    "eslint",
						Success:     false,
						Duration:    500,
						RelativeDir: "dir2",
					},
					{
						ToolName:    "eslint",
						Success:     true,
						Duration:    1500,
						RelativeDir: "dir3",
					},
				},
			},
		}

		groups := groupResultsByTool(groupResults)

		if len(groups) != 1 {
			t.Fatalf("expected 1 group, got %d", len(groups))
		}

		group := groups[0]
		if group.totalRuns != 3 {
			t.Errorf("expected totalRuns 3, got %d", group.totalRuns)
		}
		if group.succeededRuns != 2 {
			t.Errorf("expected succeededRuns 2, got %d", group.succeededRuns)
		}
		if group.failedRuns != 1 {
			t.Errorf("expected failedRuns 1, got %d", group.failedRuns)
		}
		if group.totalTime != 3000 {
			t.Errorf("expected totalTime 3000, got %d", group.totalTime)
		}
		if group.minTime != 500 {
			t.Errorf("expected minTime 500, got %d", group.minTime)
		}
		if group.maxTime != 1500 {
			t.Errorf("expected maxTime 1500, got %d", group.maxTime)
		}
		if group.minDir != "dir2" {
			t.Errorf("expected minDir 'dir2', got %q", group.minDir)
		}
		if group.maxDir != "dir3" {
			t.Errorf("expected maxDir 'dir3', got %q", group.maxDir)
		}
	})

	t.Run("multiple tools", func(t *testing.T) {
		groupResults := []tooling.GroupExecutionResult{
			{
				Results: []tooling.ExecutionResult{
					{
						ToolName: "eslint",
						Success:  true,
						Duration: 1000,
					},
					{
						ToolName: "prettier",
						Success:  true,
						Duration: 500,
					},
					{
						ToolName: "eslint",
						Success:  false,
						Duration: 1200,
					},
				},
			},
		}

		groups := groupResultsByTool(groupResults)

		if len(groups) != 2 {
			t.Fatalf("expected 2 groups, got %d", len(groups))
		}

		eslintGroup := groups[0]
		if eslintGroup.toolName != "eslint" {
			t.Errorf("expected first tool 'eslint', got %q", eslintGroup.toolName)
		}
		if eslintGroup.totalRuns != 2 {
			t.Errorf("expected eslint totalRuns 2, got %d", eslintGroup.totalRuns)
		}

		prettierGroup := groups[1]
		if prettierGroup.toolName != "prettier" {
			t.Errorf("expected second tool 'prettier', got %q", prettierGroup.toolName)
		}
		if prettierGroup.totalRuns != 1 {
			t.Errorf("expected prettier totalRuns 1, got %d", prettierGroup.totalRuns)
		}
	})

	t.Run("empty results", func(t *testing.T) {
		groupResults := []tooling.GroupExecutionResult{}
		groups := groupResultsByTool(groupResults)

		if len(groups) != 0 {
			t.Errorf("expected 0 groups, got %d", len(groups))
		}
	})
}

func TestFormatExecutionPlan(t *testing.T) {
	plan := &tooling.ExecutionPlan{
		Groups: []tooling.TaskGroup{
			{
				Tasks: []tooling.Task{
					{
						ToolName: "eslint",
						OpConfig: config.ToolOperation{
							Scope: config.ToolScopePerFile,
						},
						Files: []string{"file1.js", "file2.js"},
					},
				},
			},
		},
	}

	tests := []struct {
		name         string
		explainLevel string
		expectType   string
	}{
		{
			name:         "summary format",
			explainLevel: "summary",
			expectType:   "summary",
		},
		{
			name:         "detailed format",
			explainLevel: "detailed",
			expectType:   "detailed",
		},
		{
			name:         "json format",
			explainLevel: "json",
			expectType:   "json",
		},
		{
			name:         "default format",
			explainLevel: "",
			expectType:   "summary",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatExecutionPlan(plan, "/root", "/root/subdir", config.OpFix, tt.explainLevel)
			if result == "" {
				t.Error("formatExecutionPlan returned empty string")
			}

			if tt.explainLevel == "json" && !strings.HasPrefix(result, "{") {
				t.Errorf("expected JSON format to start with '{', got %q", result[:1])
			}
		})
	}
}

func TestPrintFailedExecutionContainsAllFields(t *testing.T) {
	exec := executionInstance{
		result: tooling.ExecutionResult{
			ToolName:    "golangci-lint",
			Success:     false,
			Duration:    2500,
			ExitCode:    2,
			Command:     "golangci-lint run ./...",
			Output:      "internal/foo.go:10:5: unused variable\ninternal/bar.go:20:1: missing return",
			WorkingDir:  "/home/user/monorepo/services/api",
			RelativeDir: "services/api",
			Scope:       config.ToolScopePerProject,
		},
		relativeDir: "services/api",
	}

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printFailedExecution(1, exec)

	_ = w.Close()
	os.Stdout = oldStdout

	var buf [16384]byte
	n, _ := r.Read(buf[:])
	captured := string(buf[:n])

	requiredFields := []struct {
		name  string
		value string
	}{
		{"tool name", "golangci-lint"},
		{"scope", "per-project"},
		{"relative dir", "services/api"},
		{"absolute cwd", "/home/user/monorepo/services/api"},
		{"exit code", "Exit code: 2"},
		{"output line 1", "internal/foo.go:10:5: unused variable"},
		{"output line 2", "internal/bar.go:20:1: missing return"},
		{"border top", "┌"},
		{"border bottom", "└"},
		{"border side", "│"},
	}

	for _, field := range requiredFields {
		if !strings.Contains(captured, field.value) {
			t.Errorf("printFailedExecution output missing %s (%q) in:\n%s", field.name, field.value, captured)
		}
	}
	if !strings.Contains(captured, "Command:") || !strings.Contains(captured, "golangci-lint run ./...") {
		t.Errorf("printFailedExecution output should contain command, got:\n%s", captured)
	}

	outputCount := strings.Count(captured, "internal/foo.go:10:5: unused variable")
	if outputCount != 1 {
		t.Errorf("expected tool output to appear exactly once, appeared %d times", outputCount)
	}
}

func TestPrintFailedExecutionMinimalFields(t *testing.T) {
	exec := executionInstance{
		result: tooling.ExecutionResult{
			ToolName: "custom-tool",
			Success:  false,
			Duration: 100,
			ExitCode: 1,
		},
		relativeDir: "",
	}

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printFailedExecution(1, exec)

	_ = w.Close()
	os.Stdout = oldStdout

	var buf [8192]byte
	n, _ := r.Read(buf[:])
	captured := string(buf[:n])

	if !strings.Contains(captured, "custom-tool") {
		t.Error("output should contain tool name even with minimal fields")
	}
	if !strings.Contains(captured, "Exit code: 1") {
		t.Error("output should always contain exit code")
	}
	if strings.Contains(captured, "Dir:") {
		t.Error("output should not contain Dir when relativeDir is empty")
	}
	if strings.Contains(captured, "Cwd:") {
		t.Error("output should not contain Cwd when WorkingDir is empty")
	}
	if strings.Contains(captured, "Command:") {
		t.Error("output should not contain Command when command is empty")
	}
}

func TestPrintFailedExecutionWithScope(t *testing.T) {
	scopes := []config.ToolScope{
		config.ToolScopeRepository,
		config.ToolScopePerProject,
		config.ToolScopePerFile,
	}

	for _, scope := range scopes {
		t.Run(string(scope), func(t *testing.T) {
			exec := executionInstance{
				result: tooling.ExecutionResult{
					ToolName: "test-tool",
					Success:  false,
					Duration: 100,
					ExitCode: 1,
					Scope:    scope,
				},
			}

			oldStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			printFailedExecution(1, exec)

			_ = w.Close()
			os.Stdout = oldStdout

			var buf [8192]byte
			n, _ := r.Read(buf[:])
			captured := string(buf[:n])

			if !strings.Contains(captured, string(scope)) {
				t.Errorf("output should contain scope %q, got:\n%s", scope, captured)
			}
		})
	}
}

func TestPrintGroupedResultsShowsScope(t *testing.T) {
	toolGroups := []toolExecutionGroup{
		{
			toolName:      "eslint",
			scope:         config.ToolScopePerProject,
			totalRuns:     1,
			succeededRuns: 0,
			failedRuns:    1,
			totalTime:     500,
			minTime:       500,
			maxTime:       500,
			executions: []executionInstance{
				{
					result: tooling.ExecutionResult{
						ToolName:    "eslint",
						Success:     false,
						Duration:    500,
						ExitCode:    1,
						Output:      "test error",
						WorkingDir:  "/home/user/project/packages/web",
						RelativeDir: "packages/web",
						Scope:       config.ToolScopePerProject,
					},
					relativeDir: "packages/web",
				},
			},
		},
	}

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printGroupedResults(toolGroups, 20, false)

	_ = w.Close()
	os.Stdout = oldStdout

	var buf [16384]byte
	n, _ := r.Read(buf[:])
	captured := string(buf[:n])

	if !strings.Contains(captured, "[per-project]") {
		t.Errorf("grouped results summary should contain scope, got:\n%s", captured)
	}
	if !strings.Contains(captured, "eslint") {
		t.Errorf("grouped results should contain tool name, got:\n%s", captured)
	}
	if !strings.Contains(captured, "packages/web") {
		t.Errorf("grouped results should contain relative dir in failure details, got:\n%s", captured)
	}
	if !strings.Contains(captured, "/home/user/project/packages/web") {
		t.Errorf("grouped results should contain absolute cwd in failure details, got:\n%s", captured)
	}
}

func TestGroupResultsByToolWallClock(t *testing.T) {
	// Three overlapping parallel runs of one tool. The serial sum is 7500ms, but
	// the real wall-clock span (earliest start → latest end) is 4000ms.
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	mk := func(start, end, dur int64) tooling.ExecutionResult {
		return tooling.ExecutionResult{
			ToolName:  "eslint",
			Success:   true,
			Duration:  dur,
			StartedAt: t0.Add(time.Duration(start) * time.Millisecond),
			EndedAt:   t0.Add(time.Duration(end) * time.Millisecond),
		}
	}
	groupResults := []tooling.GroupExecutionResult{
		{Results: []tooling.ExecutionResult{
			mk(0, 3000, 3000),
			mk(500, 4000, 3500),
			mk(1000, 2000, 1000),
		}},
	}

	groups := groupResultsByTool(groupResults)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	g := groups[0]
	if g.totalTime != 7500 {
		t.Errorf("totalTime (serial sum) = %d, want 7500", g.totalTime)
	}
	if g.wallTime != 4000 {
		t.Errorf("wallTime (span) = %d, want 4000", g.wallTime)
	}
}

func TestGroupResultsByToolWallClockFallsBackToSum(t *testing.T) {
	// Without timestamps (e.g. dry-run), wallTime falls back to the summed total.
	groupResults := []tooling.GroupExecutionResult{
		{Results: []tooling.ExecutionResult{
			{ToolName: "prettier", Success: true, Duration: 1200},
			{ToolName: "prettier", Success: true, Duration: 800},
		}},
	}
	groups := groupResultsByTool(groupResults)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].wallTime != 2000 {
		t.Errorf("wallTime fallback = %d, want 2000 (sum)", groups[0].wallTime)
	}
}

func TestGroupResultsByToolHidesCancelledResults(t *testing.T) {
	t.Run("FailureReasonCancelled results are excluded", func(t *testing.T) {
		groupResults := []tooling.GroupExecutionResult{
			{
				Results: []tooling.ExecutionResult{
					{
						ToolName:      "eslint",
						Success:       false,
						Duration:      1000,
						FailureReason: tooling.FailureReasonIndependent,
					},
					{
						ToolName:      "prettier",
						Success:       false,
						Duration:      200,
						Cancelled:     true,
						FailureReason: tooling.FailureReasonCancelled,
					},
					{
						ToolName:      "tsc",
						Success:       false,
						Duration:      100,
						Cancelled:     true,
						FailureReason: tooling.FailureReasonCancelled,
					},
				},
			},
		}

		groups := groupResultsByTool(groupResults)

		if len(groups) != 1 {
			t.Fatalf("expected 1 group (only independent failure), got %d", len(groups))
		}
		if groups[0].toolName != "eslint" {
			t.Errorf("expected only 'eslint' group, got %q", groups[0].toolName)
		}
		if groups[0].failedRuns != 1 {
			t.Errorf("expected 1 failed run, got %d", groups[0].failedRuns)
		}
	})

	t.Run("FailureReasonCancelled without Cancelled flag still excluded", func(t *testing.T) {
		groupResults := []tooling.GroupExecutionResult{
			{
				Results: []tooling.ExecutionResult{
					{
						ToolName:      "eslint",
						Success:       false,
						Duration:      500,
						FailureReason: tooling.FailureReasonIndependent,
					},
					{
						ToolName:      "prettier",
						Success:       false,
						Duration:      100,
						Cancelled:     false,
						FailureReason: tooling.FailureReasonCancelled,
					},
				},
			},
		}

		groups := groupResultsByTool(groupResults)

		if len(groups) != 1 {
			t.Fatalf("expected 1 group, got %d", len(groups))
		}
		if groups[0].toolName != "eslint" {
			t.Errorf("expected only 'eslint' group, got %q", groups[0].toolName)
		}
	})

	t.Run("Cancelled flag without FailureReasonCancelled still excluded", func(t *testing.T) {
		groupResults := []tooling.GroupExecutionResult{
			{
				Results: []tooling.ExecutionResult{
					{
						ToolName:      "eslint",
						Success:       true,
						Duration:      1000,
						FailureReason: tooling.FailureReasonNone,
					},
					{
						ToolName:  "tsc",
						Success:   false,
						Duration:  50,
						Cancelled: true,
					},
				},
			},
		}

		groups := groupResultsByTool(groupResults)

		if len(groups) != 1 {
			t.Fatalf("expected 1 group, got %d", len(groups))
		}
		if groups[0].toolName != "eslint" {
			t.Errorf("expected only 'eslint' group, got %q", groups[0].toolName)
		}
	})
}

func TestGroupResultsByToolShowsIndependentFailures(t *testing.T) {
	t.Run("independent failures are included in results", func(t *testing.T) {
		groupResults := []tooling.GroupExecutionResult{
			{
				Results: []tooling.ExecutionResult{
					{
						ToolName:      "eslint",
						Success:       false,
						Duration:      1000,
						ExitCode:      1,
						Output:        "lint error in file.js",
						FailureReason: tooling.FailureReasonIndependent,
					},
					{
						ToolName:      "golangci-lint",
						Success:       false,
						Duration:      2000,
						ExitCode:      2,
						Output:        "unused variable",
						FailureReason: tooling.FailureReasonIndependent,
					},
				},
			},
		}

		groups := groupResultsByTool(groupResults)

		if len(groups) != 2 {
			t.Fatalf("expected 2 groups (both independent failures), got %d", len(groups))
		}

		for _, group := range groups {
			if group.failedRuns != 1 {
				t.Errorf("tool %q: expected 1 failed run, got %d", group.toolName, group.failedRuns)
			}
			if len(group.executions) != 1 {
				t.Errorf("tool %q: expected 1 execution, got %d", group.toolName, len(group.executions))
			}
		}
	})

	t.Run("mix of independent failures and successful results", func(t *testing.T) {
		groupResults := []tooling.GroupExecutionResult{
			{
				Results: []tooling.ExecutionResult{
					{
						ToolName:      "eslint",
						Success:       true,
						Duration:      1000,
						FailureReason: tooling.FailureReasonNone,
					},
					{
						ToolName:      "eslint",
						Success:       false,
						Duration:      500,
						ExitCode:      1,
						FailureReason: tooling.FailureReasonIndependent,
						RelativeDir:   "packages/web",
					},
					{
						ToolName:      "prettier",
						Success:       false,
						Duration:      100,
						Cancelled:     true,
						FailureReason: tooling.FailureReasonCancelled,
					},
				},
			},
		}

		groups := groupResultsByTool(groupResults)

		if len(groups) != 1 {
			t.Fatalf("expected 1 group (eslint only, prettier cancelled), got %d", len(groups))
		}
		if groups[0].toolName != "eslint" {
			t.Errorf("expected 'eslint' group, got %q", groups[0].toolName)
		}
		if groups[0].totalRuns != 2 {
			t.Errorf("expected 2 total runs for eslint, got %d", groups[0].totalRuns)
		}
		if groups[0].succeededRuns != 1 {
			t.Errorf("expected 1 succeeded run, got %d", groups[0].succeededRuns)
		}
		if groups[0].failedRuns != 1 {
			t.Errorf("expected 1 failed run, got %d", groups[0].failedRuns)
		}
	})

	t.Run("all cancelled results produce empty groups", func(t *testing.T) {
		groupResults := []tooling.GroupExecutionResult{
			{
				Results: []tooling.ExecutionResult{
					{
						ToolName:      "eslint",
						Success:       false,
						Cancelled:     true,
						FailureReason: tooling.FailureReasonCancelled,
					},
					{
						ToolName:      "prettier",
						Success:       false,
						Cancelled:     true,
						FailureReason: tooling.FailureReasonCancelled,
					},
				},
			},
		}

		groups := groupResultsByTool(groupResults)

		if len(groups) != 0 {
			t.Errorf("expected 0 groups when all results are cancelled, got %d", len(groups))
		}
	})
}

func TestGroupResultsByToolCapturesScope(t *testing.T) {
	groupResults := []tooling.GroupExecutionResult{
		{
			Results: []tooling.ExecutionResult{
				{
					ToolName: "eslint",
					Success:  true,
					Duration: 1000,
					Scope:    config.ToolScopePerProject,
				},
			},
		},
	}

	groups := groupResultsByTool(groupResults)

	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].scope != config.ToolScopePerProject {
		t.Errorf("expected scope %q, got %q", config.ToolScopePerProject, groups[0].scope)
	}
}

func TestPrintOverallSummary(t *testing.T) {
	toolGroups := []toolExecutionGroup{
		{
			toolName:      "eslint",
			totalRuns:     2,
			succeededRuns: 1,
			failedRuns:    1,
		},
		{
			toolName:      "prettier",
			totalRuns:     1,
			succeededRuns: 1,
			failedRuns:    0,
		},
	}

	r, w, _ := os.Pipe()
	oldStdout := os.Stdout
	os.Stdout = w

	printOperationFooter(toolGroups, 5000, 0, 0, 0)

	_ = w.Close()
	os.Stdout = oldStdout

	outBytes, _ := io.ReadAll(r)
	output := string(outBytes)

	if !strings.Contains(output, "2 tools") {
		t.Errorf("expected output to contain '2 tools', got: %s", output)
	}
	if !strings.Contains(output, "3 runs") {
		t.Errorf("expected output to contain '3 runs', got: %s", output)
	}
	if !strings.Contains(output, "1 failed") {
		t.Errorf("expected output to contain '1 failed', got: %s", output)
	}
}

func TestNormalizeFilePaths(t *testing.T) {
	tests := []struct {
		name        string
		cwdPath     string
		inputFiles  []string
		wantFiles   []string
		description string
	}{
		{
			name:        "all absolute paths remain unchanged",
			cwdPath:     "/Users/test/project",
			inputFiles:  []string{"/Users/test/project/file1.go", "/Users/test/project/file2.go"},
			wantFiles:   []string{"/Users/test/project/file1.go", "/Users/test/project/file2.go"},
			description: "Absolute paths should not be modified",
		},
		{
			name:        "relative paths converted to absolute",
			cwdPath:     "/Users/test/project",
			inputFiles:  []string{"file1.go", "src/file2.go"},
			wantFiles:   []string{"/Users/test/project/file1.go", "/Users/test/project/src/file2.go"},
			description: "Relative paths should be converted to absolute using cwdPath",
		},
		{
			name:        "mixed absolute and relative paths",
			cwdPath:     "/Users/test/project",
			inputFiles:  []string{"/Users/test/project/file1.go", "src/file2.go", "/abs/path/file3.go"},
			wantFiles:   []string{"/Users/test/project/file1.go", "/Users/test/project/src/file2.go", "/abs/path/file3.go"},
			description: "Mixed paths should be normalized correctly",
		},
		{
			name:        "nested relative paths",
			cwdPath:     "/Users/test/project",
			inputFiles:  []string{"packaging/npm/datamitsu/package.json", "internal/runner/runner.go"},
			wantFiles:   []string{"/Users/test/project/packaging/npm/datamitsu/package.json", "/Users/test/project/internal/runner/runner.go"},
			description: "Deep relative paths should be converted correctly",
		},
		{
			name:        "empty file list",
			cwdPath:     "/Users/test/project",
			inputFiles:  []string{},
			wantFiles:   []string{},
			description: "Empty list should remain empty",
		},
		{
			name:        "dot relative path",
			cwdPath:     "/Users/test/project",
			inputFiles:  []string{"./file.go", "../other/file.go"},
			wantFiles:   []string{"/Users/test/project/file.go", "/Users/test/other/file.go"},
			description: "Dot-relative paths should be cleaned and resolved by filepath.Join",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a copy of input to avoid modifying test data
			files := make([]string, len(tt.inputFiles))
			copy(files, tt.inputFiles)

			files = normalizeFilePaths(files, tt.cwdPath)

			// Verify results
			if len(files) != len(tt.wantFiles) {
				t.Errorf("got %d files, want %d files", len(files), len(tt.wantFiles))
				return
			}

			for i, got := range files {
				want := tt.wantFiles[i]
				if got != want {
					t.Errorf("file[%d] = %q, want %q", i, got, want)
				}
			}
		})
	}
}

func TestFilePathNormalizationPreventsRelError(t *testing.T) {
	projectPath := "/Users/test/project"
	cwdPath := "/Users/test/project"
	relativeFile := "packaging/npm/datamitsu/package.json"

	t.Run("filepath.Rel fails with relative path", func(t *testing.T) {
		// This demonstrates the original bug
		_, err := filepath.Rel(projectPath, relativeFile)
		if err == nil {
			t.Error("expected filepath.Rel to fail with relative path, but it succeeded")
		}
		expectedErr := "Rel: can't make packaging/npm/datamitsu/package.json relative to /Users/test/project"
		if err != nil && !strings.Contains(err.Error(), "Rel: can't make") {
			t.Errorf("unexpected error: %v, want error containing %q", err, expectedErr)
		}
	})

	t.Run("filepath.Rel succeeds after normalization", func(t *testing.T) {
		file := relativeFile
		// Apply normalization (same as in runner.go lines 134-140)
		if !filepath.IsAbs(file) {
			file = filepath.Join(cwdPath, file)
		}

		expected := "/Users/test/project/packaging/npm/datamitsu/package.json"
		if file != expected {
			t.Errorf("normalized path = %q, want %q", file, expected)
		}

		if !filepath.IsAbs(file) {
			t.Errorf("normalized path should be absolute, got %q", file)
		}

		// Now filepath.Rel should work
		relPath, err := filepath.Rel(projectPath, file)
		if err != nil {
			t.Errorf("filepath.Rel failed after normalization: %v", err)
		}

		wantRelPath := "packaging/npm/datamitsu/package.json"
		if relPath != wantRelPath {
			t.Errorf("filepath.Rel returned %q, want %q", relPath, wantRelPath)
		}
	})
}

type mockProgressTracker struct {
	taskStartCount      int
	fileProgressCount   int
	resultCallbackCount int
	fileProgressCalls   []fileProgressCall
}

type fileProgressCall struct {
	toolName   string
	fileIndex  int
	totalFiles int
	success    bool
}

func (m *mockProgressTracker) onTaskStart(toolName string) {
	progressMu.Lock()
	m.taskStartCount++
	progressMu.Unlock()
}

func (m *mockProgressTracker) onFileProgress(toolName string, fileIndex, totalFiles int, success bool) {
	progressMu.Lock()
	m.fileProgressCount++
	m.fileProgressCalls = append(m.fileProgressCalls, fileProgressCall{
		toolName:   toolName,
		fileIndex:  fileIndex,
		totalFiles: totalFiles,
		success:    success,
	})
	progressMu.Unlock()
}

func (m *mockProgressTracker) onResult(result tooling.ExecutionResult) {
	progressMu.Lock()
	m.resultCallbackCount++
	progressMu.Unlock()
}

func TestBatchModeProgressTracking(t *testing.T) {
	tracker := &mockProgressTracker{}

	batchTrue := true
	task := tooling.Task{
		ToolName: "test-tool",
		OpConfig: config.ToolOperation{
			Batch: &batchTrue,
			Scope: config.ToolScopePerProject,
		},
		Files: []string{"file1.go", "file2.go", "file3.go"},
	}

	tracker.onTaskStart(task.ToolName)
	tracker.onFileProgress(task.ToolName, 1, 1, true)

	result := tooling.ExecutionResult{
		ToolName: task.ToolName,
		Success:  true,
		Batch:    true,
	}
	tracker.onResult(result)

	if tracker.fileProgressCount != 1 {
		t.Errorf("batch mode: expected 1 FileProgressCallback call, got %d", tracker.fileProgressCount)
	}

	if tracker.resultCallbackCount != 1 {
		t.Errorf("batch mode: expected 1 ResultCallback call, got %d", tracker.resultCallbackCount)
	}

	if tracker.taskStartCount != 1 {
		t.Errorf("batch mode: expected 1 TaskStartCallback call, got %d", tracker.taskStartCount)
	}
}

func TestPerFileModeProgressTracking(t *testing.T) {
	tracker := &mockProgressTracker{}

	batchFalse := false
	task := tooling.Task{
		ToolName: "test-tool",
		OpConfig: config.ToolOperation{
			Batch: &batchFalse,
			Scope: config.ToolScopePerFile,
		},
		Files: []string{"file1.go", "file2.go", "file3.go", "file4.go", "file5.go"},
	}

	tracker.onTaskStart(task.ToolName)

	for i := range task.Files {
		tracker.onFileProgress(task.ToolName, i+1, len(task.Files), true)
	}

	result := tooling.ExecutionResult{
		ToolName: task.ToolName,
		Success:  true,
		Batch:    false,
	}
	tracker.onResult(result)

	if tracker.fileProgressCount != 5 {
		t.Errorf("per-file mode: expected 5 FileProgressCallback calls (one per file), got %d", tracker.fileProgressCount)
	}

	if tracker.resultCallbackCount != 1 {
		t.Errorf("per-file mode: expected 1 ResultCallback call, got %d", tracker.resultCallbackCount)
	}

	if tracker.taskStartCount != 1 {
		t.Errorf("per-file mode: expected 1 TaskStartCallback call, got %d", tracker.taskStartCount)
	}

	for i, call := range tracker.fileProgressCalls {
		expectedIndex := i + 1
		if call.fileIndex != expectedIndex {
			t.Errorf("call %d: expected fileIndex %d, got %d", i, expectedIndex, call.fileIndex)
		}
		if call.totalFiles != 5 {
			t.Errorf("call %d: expected totalFiles 5, got %d", i, call.totalFiles)
		}
	}
}

func TestProgressTrackingWithCachedFiles(t *testing.T) {
	tracker := &mockProgressTracker{}

	batchTrue := true
	task := tooling.Task{
		ToolName: "test-tool",
		OpConfig: config.ToolOperation{
			Batch: &batchTrue,
			Scope: config.ToolScopePerProject,
		},
		Files: []string{"file1.go", "file2.go"},
	}

	tracker.onTaskStart(task.ToolName)
	tracker.onFileProgress(task.ToolName, 1, 1, true)

	result := tooling.ExecutionResult{
		ToolName: task.ToolName,
		Success:  true,
		Batch:    true,
	}
	tracker.onResult(result)

	if tracker.fileProgressCount != 1 {
		t.Errorf("cached files: expected 1 FileProgressCallback call, got %d", tracker.fileProgressCount)
	}

	if tracker.taskStartCount != 1 {
		t.Errorf("cached files: expected 1 TaskStartCallback call, got %d", tracker.taskStartCount)
	}
}

func TestProgressTrackingWithGetCommandInfoError(t *testing.T) {
	tracker := &mockProgressTracker{}

	batchTrue := true
	task := tooling.Task{
		ToolName: "failing-tool",
		OpConfig: config.ToolOperation{
			Batch: &batchTrue,
			Scope: config.ToolScopePerProject,
		},
		Files: []string{"file1.go"},
	}

	tracker.onTaskStart(task.ToolName)
	tracker.onFileProgress(task.ToolName, 1, 1, false)

	result := tooling.ExecutionResult{
		ToolName: task.ToolName,
		Success:  false,
		Batch:    true,
	}
	tracker.onResult(result)

	if tracker.fileProgressCount != 1 {
		t.Errorf("GetCommandInfo error: expected FileProgressCallback to be called despite error, got %d calls", tracker.fileProgressCount)
	}

	if tracker.taskStartCount != 1 {
		t.Errorf("GetCommandInfo error: expected 1 TaskStartCallback call, got %d", tracker.taskStartCount)
	}

	if tracker.resultCallbackCount != 1 {
		t.Errorf("GetCommandInfo error: expected 1 ResultCallback call, got %d", tracker.resultCallbackCount)
	}
}

func TestMixedBatchAndPerFileProgress(t *testing.T) {
	tracker := &mockProgressTracker{}

	batchTrue := true
	batchFalse := false

	tasks := []tooling.Task{
		{
			ToolName: "batch-tool-1",
			OpConfig: config.ToolOperation{
				Batch: &batchTrue,
				Scope: config.ToolScopePerProject,
			},
			Files: []string{"file1.go"},
		},
		{
			ToolName: "batch-tool-2",
			OpConfig: config.ToolOperation{
				Batch: &batchTrue,
				Scope: config.ToolScopePerProject,
			},
			Files: []string{"file2.go"},
		},
		{
			ToolName: "per-file-tool-1",
			OpConfig: config.ToolOperation{
				Batch: &batchFalse,
				Scope: config.ToolScopePerFile,
			},
			Files: []string{"file3.go", "file4.go", "file5.go"},
		},
		{
			ToolName: "per-file-tool-2",
			OpConfig: config.ToolOperation{
				Batch: &batchFalse,
				Scope: config.ToolScopePerFile,
			},
			Files: []string{"file6.go", "file7.go", "file8.go"},
		},
	}

	for _, task := range tasks {
		tracker.onTaskStart(task.ToolName)

		if task.OpConfig.Batch != nil && *task.OpConfig.Batch {
			tracker.onFileProgress(task.ToolName, 1, 1, true)
			result := tooling.ExecutionResult{
				ToolName: task.ToolName,
				Success:  true,
				Batch:    true,
			}
			tracker.onResult(result)
		} else {
			for i := range task.Files {
				tracker.onFileProgress(task.ToolName, i+1, len(task.Files), true)
			}
			result := tooling.ExecutionResult{
				ToolName: task.ToolName,
				Success:  true,
				Batch:    false,
			}
			tracker.onResult(result)
		}
	}

	expectedProgress := 2 + 6
	if tracker.fileProgressCount != expectedProgress {
		t.Errorf("mixed mode: expected %d total FileProgressCallback calls (2 batch + 6 per-file), got %d",
			expectedProgress, tracker.fileProgressCount)
	}

	if tracker.taskStartCount != 4 {
		t.Errorf("mixed mode: expected 4 TaskStartCallback calls, got %d", tracker.taskStartCount)
	}

	if tracker.resultCallbackCount != 4 {
		t.Errorf("mixed mode: expected 4 ResultCallback calls, got %d", tracker.resultCallbackCount)
	}
}

func TestConcurrentProgressUpdates(t *testing.T) {
	tracker := &mockProgressTracker{}

	numTasks := 10
	done := make(chan bool, numTasks)

	for i := range numTasks {
		go func(n int) {
			toolName := fmt.Sprintf("tool-%d", n)
			tracker.onTaskStart(toolName)
			tracker.onFileProgress(toolName, 1, 1, true)
			result := tooling.ExecutionResult{
				ToolName: toolName,
				Success:  true,
				Batch:    true,
			}
			tracker.onResult(result)
			done <- true
		}(i)
	}

	for range numTasks {
		<-done
	}

	if tracker.fileProgressCount != numTasks {
		t.Errorf("concurrent: expected %d FileProgressCallback calls, got %d", numTasks, tracker.fileProgressCount)
	}

	if tracker.taskStartCount != numTasks {
		t.Errorf("concurrent: expected %d TaskStartCallback calls, got %d", numTasks, tracker.taskStartCount)
	}

	if tracker.resultCallbackCount != numTasks {
		t.Errorf("concurrent: expected %d ResultCallback calls, got %d", numTasks, tracker.resultCallbackCount)
	}
}

func TestPrintGroupedResultsOutputOnce(t *testing.T) {
	toolOutput := "specific-error-output-line-12345"
	toolGroups := []toolExecutionGroup{
		{
			toolName:      "failing-tool",
			scope:         config.ToolScopeRepository,
			totalRuns:     1,
			succeededRuns: 0,
			failedRuns:    1,
			totalTime:     500,
			minTime:       500,
			maxTime:       500,
			executions: []executionInstance{
				{
					result: tooling.ExecutionResult{
						ToolName: "failing-tool",
						Success:  false,
						Duration: 500,
						ExitCode: 1,
						Output:   toolOutput,
						Command:  "failing-tool --check",
						Scope:    config.ToolScopeRepository,
					},
				},
			},
		},
	}

	// Capture stdout to count how many times the output appears
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printGroupedResults(toolGroups, 20, false)

	_ = w.Close()
	os.Stdout = oldStdout

	var buf [8192]byte
	n, _ := r.Read(buf[:])
	captured := string(buf[:n])

	count := strings.Count(captured, toolOutput)
	if count != 1 {
		t.Errorf("expected tool output to appear exactly once in printGroupedResults, appeared %d times in:\n%s", count, captured)
	}
}

func TestRunSequentialConfigLoadError(t *testing.T) {
	err := RunSequential(
		[]config.OperationType{config.OpFix},
		nil, "", false, "", false,
		func() (*config.Config, string, error) {
			return nil, "", errors.New("config load failed")
		},
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to load config") {
		t.Errorf("expected error to contain 'failed to load config', got %q", err.Error())
	}
}

func TestRunSequentialInvalidExplainMode(t *testing.T) {
	err := RunSequential(
		[]config.OperationType{config.OpFix},
		nil, "invalid_mode", false, "", false,
		func() (*config.Config, string, error) {
			return &config.Config{}, "", nil
		},
	)
	if err == nil {
		t.Fatal("expected error for invalid explain mode")
	}
	if !strings.Contains(err.Error(), "invalid --explain value") {
		t.Errorf("expected error to contain 'invalid --explain value', got %q", err.Error())
	}
}

func TestRunSequentialConfigLoadedOnce(t *testing.T) {
	loadCount := 0
	err := RunSequential(
		[]config.OperationType{config.OpFix, config.OpLint},
		nil, "", false, "", false,
		func() (*config.Config, string, error) {
			loadCount++
			return &config.Config{
				Tools:        config.MapOfTools{},
				ProjectTypes: config.MapOfProjectTypes{},
			}, "", nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loadCount != 1 {
		t.Errorf("expected config loaded exactly once for multiple operations, got %d", loadCount)
	}
}

func TestRunDelegatesToRunSequential(t *testing.T) {
	err := Run(
		config.OpFix,
		nil, "", false, "", false,
		func() (*config.Config, string, error) {
			return nil, "", errors.New("test error from Run")
		},
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "test error from Run") {
		t.Errorf("expected error to contain 'test error from Run', got %q", err.Error())
	}
}

// fakePlanner returns a fixed plan and detected project types.
type fakePlanner struct {
	plan *tooling.ExecutionPlan
}

func (f *fakePlanner) Plan(_ context.Context, _ config.OperationType, _ []string, _ []string) (*tooling.ExecutionPlan, error) {
	return f.plan, nil
}
func (f *fakePlanner) GetDetectedProjectTypes() []string { return []string{"go"} }
func (f *fakePlanner) GetTimings() *timing.Timings       { return timing.New() }

// fakeExecutor records when Execute is called (relative to EnsureTools).
type fakeExecutor struct {
	order   *[]string
	called  bool
	results []tooling.GroupExecutionResult
}

func (f *fakeExecutor) SetResultCallback(tooling.ResultCallback)             {}
func (f *fakeExecutor) SetTaskStartCallback(tooling.TaskStartCallback)       {}
func (f *fakeExecutor) SetFileProgressCallback(tooling.FileProgressCallback) {}
func (f *fakeExecutor) Execute(_ context.Context, _ *tooling.ExecutionPlan) ([]tooling.GroupExecutionResult, error) {
	f.called = true
	*f.order = append(*f.order, "execute")
	return f.results, nil
}

// fakeEnsurer records the names passed and optionally returns an error.
type fakeEnsurer struct {
	order  *[]string
	names  []string
	called bool
	err    error
}

func (f *fakeEnsurer) EnsureTools(_ context.Context, names []string) error {
	f.called = true
	f.names = names
	*f.order = append(*f.order, "ensure")
	return f.err
}

func planWithTools(tools ...string) *tooling.ExecutionPlan {
	tasks := make([]tooling.Task, 0, len(tools))
	for _, name := range tools {
		tasks = append(tasks, tooling.Task{
			ToolName: name,
			OpConfig: config.ToolOperation{App: name, Scope: config.ToolScopeRepository},
		})
	}
	return &tooling.ExecutionPlan{
		Groups: []tooling.TaskGroup{{Tasks: tasks}},
	}
}

func TestRunSingleOperationEnsuresToolsBeforeExecute(t *testing.T) {
	t.Setenv("CI", "true")

	var order []string
	ensurer := &fakeEnsurer{order: &order}
	executor := &fakeExecutor{order: &order}
	sc := &sharedContext{
		planner:  &fakePlanner{plan: planWithTools("yq", "lefthook")},
		executor: executor,
		binMgr:   ensurer,
		timings:  timing.New(),
	}

	if err := runSingleOperation(context.Background(), sc, config.OpLint); err != nil {
		t.Fatalf("runSingleOperation error: %v", err)
	}

	if !ensurer.called {
		t.Fatal("expected EnsureTools to be called")
	}
	if !executor.called {
		t.Fatal("expected Execute to be called")
	}
	if len(order) != 2 || order[0] != "ensure" || order[1] != "execute" {
		t.Fatalf("expected EnsureTools before Execute, got order %v", order)
	}
	want := []string{"lefthook", "yq"} // sorted+deduped app names from GetAppNames
	if strings.Join(ensurer.names, ",") != strings.Join(want, ",") {
		t.Errorf("EnsureTools names = %v, want %v", ensurer.names, want)
	}
}

func TestRunSingleOperationEnsuresAppNamesNotToolNames(t *testing.T) {
	t.Setenv("CI", "true")

	var order []string
	ensurer := &fakeEnsurer{order: &order}
	executor := &fakeExecutor{order: &order}
	// Tool names differ from the apps they execute. Pre-install must resolve the
	// apps (what BinManager installs), not the tool registry keys.
	plan := &tooling.ExecutionPlan{
		Groups: []tooling.TaskGroup{{Tasks: []tooling.Task{
			{ToolName: "lint-yaml", OpConfig: config.ToolOperation{App: "yq", Scope: config.ToolScopeRepository}},
			{ToolName: "format", OpConfig: config.ToolOperation{App: "prettier", Scope: config.ToolScopeRepository}},
		}}},
	}
	sc := &sharedContext{
		planner:  &fakePlanner{plan: plan},
		executor: executor,
		binMgr:   ensurer,
		timings:  timing.New(),
	}

	if err := runSingleOperation(context.Background(), sc, config.OpLint); err != nil {
		t.Fatalf("runSingleOperation error: %v", err)
	}

	want := []string{"prettier", "yq"} // app names, sorted
	if strings.Join(ensurer.names, ",") != strings.Join(want, ",") {
		t.Errorf("EnsureTools names = %v, want app names %v", ensurer.names, want)
	}
}

func TestRunSingleOperationEnsureToolsFailureAbortsExecute(t *testing.T) {
	t.Setenv("CI", "true")

	var order []string
	ensurer := &fakeEnsurer{order: &order, err: errors.New("download boom")}
	executor := &fakeExecutor{order: &order}
	sc := &sharedContext{
		planner:  &fakePlanner{plan: planWithTools("yq")},
		executor: executor,
		binMgr:   ensurer,
		timings:  timing.New(),
	}

	err := runSingleOperation(context.Background(), sc, config.OpLint)
	if err == nil {
		t.Fatal("expected error when EnsureTools fails")
	}
	if !strings.Contains(err.Error(), "pre-install tools") {
		t.Errorf("expected error to mention pre-install tools, got %q", err.Error())
	}
	if executor.called {
		t.Error("Execute must not run when EnsureTools fails")
	}
}

func TestRunSingleOperationExplainModeSkipsEnsureTools(t *testing.T) {
	var order []string
	ensurer := &fakeEnsurer{order: &order}
	executor := &fakeExecutor{order: &order}
	sc := &sharedContext{
		planner:      &fakePlanner{plan: planWithTools("yq")},
		executor:     executor,
		binMgr:       ensurer,
		explainLevel: "summary",
		timings:      timing.New(),
	}

	if err := runSingleOperation(context.Background(), sc, config.OpLint); err != nil {
		t.Fatalf("runSingleOperation error: %v", err)
	}
	if ensurer.called {
		t.Error("explain/dry-run mode must not pre-install tools")
	}
	if executor.called {
		t.Error("explain mode must not execute")
	}
}

func TestRunSequentialFixThenLintOrdering(t *testing.T) {
	err := RunSequential(
		[]config.OperationType{config.OpFix, config.OpLint},
		nil, "summary", false, "", false,
		func() (*config.Config, string, error) {
			return &config.Config{
				Tools: config.MapOfTools{
					"test-tool": config.Tool{
						Name: "test-tool",
						Operations: map[config.OperationType]config.ToolOperation{
							config.OpFix: {
								App:   "echo",
								Args:  []string{"fix"},
								Scope: config.ToolScopeRepository,
							},
							config.OpLint: {
								App:   "echo",
								Args:  []string{"lint"},
								Scope: config.ToolScopeRepository,
							},
						},
					},
				},
				ProjectTypes: config.MapOfProjectTypes{
					"go": {Markers: []string{"go.mod"}},
				},
			}, "", nil
		},
	)
	if err != nil {
		t.Fatalf("RunSequential in explain mode should not error, got: %v", err)
	}
}
