package tooling

import (
	"context"
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/project"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type mockAppManager struct {
	binaries map[string]string
	err      error
	commands map[string]*binmanager.CommandInfo // flexible per-app command info
}

func (m *mockAppManager) GetBinaryPath(appName string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	if path, ok := m.binaries[appName]; ok {
		return path, nil
	}
	return "", fmt.Errorf("binary not found: %s", appName)
}

func (m *mockAppManager) GetCommandInfo(appName string) (*binmanager.CommandInfo, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.commands != nil {
		if info, ok := m.commands[appName]; ok {
			return info, nil
		}
	}
	if path, ok := m.binaries[appName]; ok {
		return &binmanager.CommandInfo{
			Type:    "binary",
			Command: path,
		}, nil
	}
	return nil, fmt.Errorf("app not found: %s", appName)
}

func TestNewExecutor(t *testing.T) {
	rootPath := "/root"
	dryRun := true
	failFast := true
	appManager := &mockAppManager{}

	executor := NewExecutor(rootPath, dryRun, failFast, appManager, nil)

	if executor == nil {
		t.Fatal("NewExecutor() returned nil")
	}

	if executor.rootPath != rootPath {
		t.Errorf("rootPath = %q, want %q", executor.rootPath, rootPath)
	}

	if !executor.dryRun {
		t.Error("dryRun should be true")
	}

	if !executor.failFast {
		t.Error("failFast should be true")
	}

}

func TestSetResultCallback(t *testing.T) {
	executor := NewExecutor("/root", false, false, &mockAppManager{}, nil)

	called := false
	callback := func(result ExecutionResult) {
		called = true
	}

	executor.SetResultCallback(callback)

	if executor.resultCallback == nil {
		t.Error("resultCallback should be set")
	}

	executor.resultCallback(ExecutionResult{})

	if !called {
		t.Error("callback should have been called")
	}
}

func TestReplacePlaceholders(t *testing.T) {
	executor := NewExecutor("/root", false, false, &mockAppManager{}, nil)

	rootCachePath, err := env.GetProjectCachePath("/root", "", "mytool")
	if err != nil {
		t.Fatalf("failed to compute project cache path: %v", err)
	}

	// Cache path for per-project task (services/api with mytool)
	projectCachePath, err := env.GetProjectCachePath("/root", "services/api", "mytool")
	if err != nil {
		t.Fatalf("failed to compute project cache path: %v", err)
	}

	tests := []struct {
		name        string
		args        []string
		file        string
		files       []string
		projectPath string
		toolName    string
		expected    []string
	}{
		{
			name:        "cwd resolves to projectPath in per-project scope",
			args:        []string{"{root}/file", "{cwd}/file"},
			file:        "",
			files:       nil,
			projectPath: "/root/services/api",
			toolName:    "",
			expected:    []string{"/root/file", "/root/services/api/file"},
		},
		{
			name:        "cwd falls back to rootPath when projectPath is empty",
			args:        []string{"{cwd}/file"},
			file:        "",
			files:       nil,
			projectPath: "",
			toolName:    "",
			expected:    []string{"/root/file"},
		},
		{
			name:        "root always resolves to git root regardless of projectPath",
			args:        []string{"{root}"},
			file:        "",
			files:       nil,
			projectPath: "/root/services/api",
			toolName:    "",
			expected:    []string{"/root"},
		},
		{
			name:        "replace file",
			args:        []string{"--file={file}"},
			file:        "test.js",
			files:       nil,
			projectPath: "",
			toolName:    "",
			expected:    []string{"--file=test.js"},
		},
		{
			name:        "replace files",
			args:        []string{"{files}"},
			file:        "",
			files:       []string{"file1.js", "file2.js"},
			projectPath: "",
			toolName:    "",
			expected:    []string{"file1.js", "file2.js"},
		},
		{
			name:        "no placeholders",
			args:        []string{"--fix", "test.js"},
			file:        "",
			files:       nil,
			projectPath: "",
			toolName:    "",
			expected:    []string{"--fix", "test.js"},
		},
		{
			name:        "toolCache resolves to project cache path with tool isolation",
			args:        []string{"{toolCache}/foo"},
			file:        "",
			files:       nil,
			projectPath: "",
			toolName:    "mytool",
			expected:    []string{rootCachePath + "/foo"},
		},
		{
			name:        "toolCache multiple occurrences in same arg",
			args:        []string{"--input={toolCache}/in", "--output={toolCache}/out"},
			file:        "",
			files:       nil,
			projectPath: "",
			toolName:    "mytool",
			expected:    []string{"--input=" + rootCachePath + "/in", "--output=" + rootCachePath + "/out"},
		},
		{
			name:        "toolCache combined with root placeholder and project path",
			args:        []string{"--cache={toolCache}", "--root={root}"},
			file:        "",
			files:       nil,
			projectPath: "/root/services/api",
			toolName:    "mytool",
			expected:    []string{"--cache=" + projectCachePath, "--root=/root"},
		},
		{
			name:        "toolCache standalone with root-level project",
			args:        []string{"{toolCache}"},
			file:        "",
			files:       nil,
			projectPath: "",
			toolName:    "mytool",
			expected:    []string{rootCachePath},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := executor.replacePlaceholders(tt.args, tt.file, tt.files, tt.projectPath, tt.toolName)

			if len(result) != len(tt.expected) {
				t.Errorf("len(result) = %d, want %d", len(result), len(tt.expected))
				return
			}

			for i, r := range result {
				if r != tt.expected[i] {
					t.Errorf("result[%d] = %q, want %q", i, r, tt.expected[i])
				}
			}
		})
	}
}

func TestReplacePlaceholders_ToolCacheComputedPerCall(t *testing.T) {
	executor := NewExecutor("/root", false, false, &mockAppManager{}, nil)

	t.Run("basic per-call computation", func(t *testing.T) {
		cachePath, err := env.GetProjectCachePath("/root", "", "tsc")
		if err != nil {
			t.Fatalf("failed to compute project cache path: %v", err)
		}

		result := executor.replacePlaceholders([]string{"{toolCache}/foo"}, "", nil, "", "tsc")
		if len(result) != 1 {
			t.Fatalf("len(result) = %d, want 1", len(result))
		}
		expected := cachePath + "/foo"
		if result[0] != expected {
			t.Errorf("result[0] = %q, want %q", result[0], expected)
		}
	})

	t.Run("different tools get different cache paths", func(t *testing.T) {
		result1 := executor.replacePlaceholders([]string{"{toolCache}"}, "", nil, "", "tsc")
		result2 := executor.replacePlaceholders([]string{"{toolCache}"}, "", nil, "", "eslint")

		if len(result1) != 1 || len(result2) != 1 {
			t.Fatalf("unexpected result lengths: %d, %d", len(result1), len(result2))
		}
		if result1[0] == result2[0] {
			t.Errorf("different tools should get different cache paths, both got: %q", result1[0])
		}
	})

	t.Run("different projects get different cache paths", func(t *testing.T) {
		result1 := executor.replacePlaceholders([]string{"{toolCache}"}, "", nil, "/root/packages/frontend", "tsc")
		result2 := executor.replacePlaceholders([]string{"{toolCache}"}, "", nil, "/root/packages/backend", "tsc")

		if len(result1) != 1 || len(result2) != 1 {
			t.Fatalf("unexpected result lengths: %d, %d", len(result1), len(result2))
		}
		if result1[0] == result2[0] {
			t.Errorf("different projects should get different cache paths, both got: %q", result1[0])
		}
	})

	t.Run("per-project task includes project path in cache", func(t *testing.T) {
		cachePath, err := env.GetProjectCachePath("/root", "packages/frontend", "tsc")
		if err != nil {
			t.Fatalf("failed to compute project cache path: %v", err)
		}

		result := executor.replacePlaceholders([]string{"{toolCache}"}, "", nil, "/root/packages/frontend", "tsc")
		if len(result) != 1 {
			t.Fatalf("len(result) = %d, want 1", len(result))
		}
		if result[0] != cachePath {
			t.Errorf("result[0] = %q, want %q", result[0], cachePath)
		}

		wantSuffix := filepath.Join("cache", "packages", "frontend", "tsc")
		if !strings.HasSuffix(result[0], wantSuffix) {
			t.Errorf("cache path should end with %q, got %q", wantSuffix, result[0])
		}
	})

	t.Run("root-level task omits project path from cache", func(t *testing.T) {
		cachePath, err := env.GetProjectCachePath("/root", "", "golangci-lint")
		if err != nil {
			t.Fatalf("failed to compute project cache path: %v", err)
		}

		result := executor.replacePlaceholders([]string{"{toolCache}"}, "", nil, "", "golangci-lint")
		if len(result) != 1 {
			t.Fatalf("len(result) = %d, want 1", len(result))
		}
		if result[0] != cachePath {
			t.Errorf("result[0] = %q, want %q", result[0], cachePath)
		}

		wantSuffix := filepath.Join("cache", "golangci-lint")
		if !strings.HasSuffix(result[0], wantSuffix) {
			t.Errorf("cache path should end with %q, got %q", wantSuffix, result[0])
		}
	})

	t.Run("projectPath equal to rootPath treated as root-level", func(t *testing.T) {
		rootResult := executor.replacePlaceholders([]string{"{toolCache}"}, "", nil, "", "tsc")
		sameResult := executor.replacePlaceholders([]string{"{toolCache}"}, "", nil, "/root", "tsc")

		if len(rootResult) != 1 || len(sameResult) != 1 {
			t.Fatalf("unexpected result lengths: %d, %d", len(rootResult), len(sameResult))
		}
		if rootResult[0] != sameResult[0] {
			t.Errorf("projectPath == rootPath should produce same cache as empty projectPath\ngot:  %q\nwant: %q", sameResult[0], rootResult[0])
		}
	})

	t.Run("empty toolName still produces valid cache path", func(t *testing.T) {
		result := executor.replacePlaceholders([]string{"{toolCache}/output"}, "", nil, "", "")
		if len(result) != 1 {
			t.Fatalf("len(result) = %d, want 1", len(result))
		}
		if strings.Contains(result[0], "{toolCache}") {
			t.Errorf("toolCache should be expanded even with empty toolName, got: %q", result[0])
		}
	})
}

func TestGetWorkingDir(t *testing.T) {
	rootPath := "/root"
	executor := NewExecutor(rootPath, false, false, &mockAppManager{}, nil)

	tests := []struct {
		name        string
		projectPath string
		expected    string
	}{
		{
			name:        "with project path",
			projectPath: "/root/project/subdir",
			expected:    "/root/project/subdir",
		},
		{
			name:        "without project path",
			projectPath: "",
			expected:    rootPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := Task{
				ProjectPath: tt.projectPath,
			}
			result := executor.getWorkingDir(task)
			if result != tt.expected {
				t.Errorf("getWorkingDir() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestExecuteDryRun(t *testing.T) {
	appManager := &mockAppManager{
		binaries: map[string]string{
			"eslint": "/bin/eslint",
		},
	}

	executor := NewExecutor("/root", true, false, appManager, nil)

	plan := &ExecutionPlan{
		Groups: []TaskGroup{
			{
				Priority: 10,
				Tasks: []Task{
					{
						ToolName:  "eslint",
						Operation: config.OpFix,
						OpConfig: config.ToolOperation{
							App: "eslint",
							Args:    []string{"--fix", "{file}"},
							Scope:   config.ToolScopePerFile,
						},
						Files: []string{"test.js"},
					},
				},
			},
		},
	}

	results, err := executor.Execute(context.Background(), plan)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(results) != 1 {
		t.Errorf("len(results) = %d, want 1", len(results))
	}

	if !results[0].Success {
		t.Error("result should be successful in dry-run")
	}

	if len(results[0].Results) != 1 {
		t.Errorf("len(Results) = %d, want 1", len(results[0].Results))
	}

	output := results[0].Results[0].Output
	if output == "" {
		t.Error("output should not be empty in dry-run")
	}
}

func TestExecuteBinaryNotFound(t *testing.T) {
	appManager := &mockAppManager{
		err: fmt.Errorf("binary not found"),
	}

	executor := NewExecutor("/root", false, false, appManager, nil)

	plan := &ExecutionPlan{
		Groups: []TaskGroup{
			{
				Priority: 10,
				Tasks: []Task{
					{
						ToolName:  "missing",
						Operation: config.OpFix,
						OpConfig: config.ToolOperation{
							App: "missing",
							Scope:   config.ToolScopeRepository,
						},
					},
				},
			},
		},
	}

	results, err := executor.Execute(context.Background(), plan)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(results) != 1 {
		t.Errorf("len(results) = %d, want 1", len(results))
	}

	if results[0].Success {
		t.Error("result should fail when binary not found")
	}

	if results[0].Results[0].Error == nil {
		t.Error("result should have error")
	}
}

func TestDetectParallelGroups(t *testing.T) {
	executor := NewExecutor("/root", false, false, &mockAppManager{}, nil)

	tasks := []Task{
		{
			ToolName: "tool1",
			OpConfig: config.ToolOperation{
				Scope: config.ToolScopePerFile,
				Globs: []string{"*.js"},
			},
		},
		{
			ToolName: "tool2",
			OpConfig: config.ToolOperation{
				Scope: config.ToolScopePerFile,
				Globs: []string{"*.ts"},
			},
		},
		{
			ToolName: "tool3",
			OpConfig: config.ToolOperation{
				Scope: config.ToolScopePerFile,
				Globs: []string{"*.js"},
			},
		},
	}

	groups := executor.detectParallelGroups(tasks)

	if len(groups) < 1 {
		t.Error("should have at least one parallel group")
	}
}

func TestHasOverlap(t *testing.T) {
	tests := []struct {
		name     string
		task1    Task
		task2    Task
		expected bool
	}{
		{
			name: "whole-project overlaps with everything",
			task1: Task{
				OpConfig: config.ToolOperation{Scope: config.ToolScopeRepository},
			},
			task2: Task{
				OpConfig: config.ToolOperation{Scope: config.ToolScopePerFile, Globs: []string{"*.js"}},
			},
			expected: true,
		},
		{
			name: "same globs overlap",
			task1: Task{
				OpConfig: config.ToolOperation{Scope: config.ToolScopePerFile, Globs: []string{"*.js"}},
			},
			task2: Task{
				OpConfig: config.ToolOperation{Scope: config.ToolScopePerFile, Globs: []string{"*.js"}},
			},
			expected: true,
		},
		{
			name: "different globs no overlap",
			task1: Task{
				OpConfig: config.ToolOperation{Scope: config.ToolScopePerFile, Globs: []string{"*.js"}},
			},
			task2: Task{
				OpConfig: config.ToolOperation{Scope: config.ToolScopePerFile, Globs: []string{"*.ts"}},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := HasOverlap(tt.task1, tt.task2)
			if result != tt.expected {
				t.Errorf("HasOverlap() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestNewPlanner(t *testing.T) {
	rootPath := "/root"
	cwdPath := "/root/project"
	projectTypes := []string{"node", "go"}
	tools := config.MapOfTools{}
	projectTypesConfig := config.MapOfProjectTypes{}

	planner := NewPlanner(rootPath, cwdPath, projectTypes, tools, projectTypesConfig, nil)

	if planner == nil {
		t.Fatal("NewPlanner() returned nil")
	}

	if planner.rootPath != rootPath {
		t.Errorf("rootPath = %q, want %q", planner.rootPath, rootPath)
	}

	if planner.cwdPath != cwdPath {
		t.Errorf("cwdPath = %q, want %q", planner.cwdPath, cwdPath)
	}

	if len(planner.detectedTypes) != 2 {
		t.Errorf("len(detectedTypes) = %d, want 2", len(planner.detectedTypes))
	}
}

func TestIsApplicableTool(t *testing.T) {
	planner := &Planner{
		detectedTypes: []string{"node", "go"},
	}

	tests := []struct {
		name     string
		tool     config.Tool
		expected bool
	}{
		{
			name:     "no project types",
			tool:     config.Tool{},
			expected: true,
		},
		{
			name: "matching project type",
			tool: config.Tool{
				ProjectTypes: []string{"node"},
			},
			expected: true,
		},
		{
			name: "non-matching project type",
			tool: config.Tool{
				ProjectTypes: []string{"rust"},
			},
			expected: false,
		},
		{
			name: "one matching type",
			tool: config.Tool{
				ProjectTypes: []string{"node", "rust"},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := planner.isApplicableTool(tt.tool)
			if result != tt.expected {
				t.Errorf("isApplicableTool() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestFilterFilesByGlobs(t *testing.T) {
	tmpDir := t.TempDir()

	planner := &Planner{
		rootPath: tmpDir,
	}

	file1 := filepath.Join(tmpDir, "test.js")
	file2 := filepath.Join(tmpDir, "test.ts")
	file3 := filepath.Join(tmpDir, "test.go")

	files := []string{file1, file2, file3}

	tests := []struct {
		name     string
		globs    []string
		expected int
	}{
		{
			name:     "match js files",
			globs:    []string{"*.js"},
			expected: 1,
		},
		{
			name:     "match js and ts files",
			globs:    []string{"*.js", "*.ts"},
			expected: 2,
		},
		{
			name:     "match all files",
			globs:    []string{"*"},
			expected: 3,
		},
		{
			name:     "no match",
			globs:    []string{"*.py"},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := planner.filterFilesByGlobs(files, tt.globs)
			if len(result) != tt.expected {
				t.Errorf("len(result) = %d, want %d", len(result), tt.expected)
			}
		})
	}
}

func TestPlan(t *testing.T) {
	tmpDir := t.TempDir()

	tools := config.MapOfTools{
		"tool1": {
			Name: "tool1",
			Operations: map[config.OperationType]config.ToolOperation{
				config.OpLint: {
					App:  "tool1",
					Scope:    config.ToolScopeRepository,
					Priority: 10,
				},
			},
		},
		"tool2": {
			Name:         "tool2",
			ProjectTypes: []string{"rust"},
			Operations: map[config.OperationType]config.ToolOperation{
				config.OpLint: {
					App:  "tool2",
					Scope:    config.ToolScopeRepository,
					Priority: 20,
				},
			},
		},
	}

	planner := NewPlanner(tmpDir, tmpDir, []string{"node"}, tools, config.MapOfProjectTypes{}, nil)

	plan, err := planner.Plan(context.Background(), config.OpLint, nil, nil)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	if plan == nil {
		t.Fatal("Plan() returned nil")
	}

	if len(plan.Groups) != 1 {
		t.Errorf("len(Groups) = %d, want 1", len(plan.Groups))
	}

	if plan.Groups[0].Priority != 10 {
		t.Errorf("Priority = %d, want 10", plan.Groups[0].Priority)
	}
}

func TestCollectTasks(t *testing.T) {
	tmpDir := t.TempDir()

	tools := config.MapOfTools{
		"tool1": {
			Name: "tool1",
			Operations: map[config.OperationType]config.ToolOperation{
				config.OpLint: {
					App: "tool1",
					Scope:   config.ToolScopeRepository,
				},
			},
		},
	}

	planner := NewPlanner(tmpDir, tmpDir, []string{}, tools, config.MapOfProjectTypes{}, nil)

	tasks := planner.collectTasks(config.OpLint, nil)

	if len(tasks) != 1 {
		t.Errorf("len(tasks) = %d, want 1", len(tasks))
	}

	if tasks[0].ToolName != "tool1" {
		t.Errorf("ToolName = %q, want %q", tasks[0].ToolName, "tool1")
	}
}

func TestCollectTasksRepositoryScope(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "subdir")
	_ = os.MkdirAll(subDir, 0755)

	tools := config.MapOfTools{
		"tool1": {
			Name: "tool1",
			Operations: map[config.OperationType]config.ToolOperation{
				config.OpLint: {
					App: "tool1",
					Scope:   config.ToolScopeRepository,
				},
			},
		},
	}

	t.Run("root path", func(t *testing.T) {
		planner := NewPlanner(tmpDir, tmpDir, []string{}, tools, config.MapOfProjectTypes{}, nil)
		tasks := planner.collectTasks(config.OpLint, nil)

		if len(tasks) != 1 {
			t.Errorf("len(tasks) = %d, want 1", len(tasks))
		}
	})

	t.Run("subdirectory path", func(t *testing.T) {
		// Repository-scoped tools are skipped when cwd is not the git root
		planner := NewPlanner(tmpDir, subDir, []string{}, tools, config.MapOfProjectTypes{}, nil)
		tasks := planner.collectTasks(config.OpLint, nil)

		if len(tasks) != 0 {
			t.Errorf("len(tasks) = %d, want 0 (repository scope skipped when cwd != root)", len(tasks))
		}
	})
}

func TestGroupByPriority(t *testing.T) {
	planner := &Planner{}

	tasks := []Task{
		{
			ToolName: "tool1",
			OpConfig: config.ToolOperation{Priority: 20},
		},
		{
			ToolName: "tool2",
			OpConfig: config.ToolOperation{Priority: 10},
		},
		{
			ToolName: "tool3",
			OpConfig: config.ToolOperation{Priority: 10},
		},
	}

	groups := planner.groupByPriority(tasks)

	if len(groups) != 2 {
		t.Errorf("len(groups) = %d, want 2", len(groups))
	}

	if groups[0].Priority != 10 {
		t.Errorf("groups[0].Priority = %d, want 10", groups[0].Priority)
	}

	if groups[1].Priority != 20 {
		t.Errorf("groups[1].Priority = %d, want 20", groups[1].Priority)
	}

	if len(groups[0].Tasks) != 2 {
		t.Errorf("len(groups[0].Tasks) = %d, want 2", len(groups[0].Tasks))
	}
}

func TestMergeEnvLayers(t *testing.T) {
	t.Run("empty layers", func(t *testing.T) {
		base := []string{"PATH=/usr/bin", "HOME=/root"}
		result := mergeEnvLayers(base)
		if len(result) != 2 {
			t.Errorf("expected 2 entries, got %d", len(result))
		}
	})

	t.Run("app env overrides OS env", func(t *testing.T) {
		base := []string{"FOO=os_value", "PATH=/usr/bin"}
		appEnv := map[string]string{"FOO": "app_value"}
		result := mergeEnvLayers(base, appEnv)
		found := false
		for _, e := range result {
			if e == "FOO=app_value" {
				found = true
			}
			if e == "FOO=os_value" {
				t.Error("OS FOO should have been overridden by app env")
			}
		}
		if !found {
			t.Errorf("expected FOO=app_value in result, got %v", result)
		}
	})

	t.Run("tool op env overrides app env", func(t *testing.T) {
		base := []string{"FOO=os_value", "PATH=/usr/bin"}
		appEnv := map[string]string{"FOO": "app_value", "BAR": "app_bar"}
		toolOpEnv := map[string]string{"FOO": "tool_value"}
		result := mergeEnvLayers(base, appEnv, toolOpEnv)
		found := false
		for _, e := range result {
			if e == "FOO=tool_value" {
				found = true
			}
			if e == "FOO=app_value" || e == "FOO=os_value" {
				t.Errorf("FOO should be tool_value, found %s", e)
			}
		}
		if !found {
			t.Errorf("expected FOO=tool_value in result, got %v", result)
		}
		// BAR from app env should still be present
		barFound := false
		for _, e := range result {
			if e == "BAR=app_bar" {
				barFound = true
			}
		}
		if !barFound {
			t.Errorf("expected BAR=app_bar from app env in result, got %v", result)
		}
	})

	t.Run("full merge priority: OS < app < tool op", func(t *testing.T) {
		base := []string{"A=os", "B=os", "C=os"}
		appEnv := map[string]string{"B": "app", "C": "app"}
		toolOpEnv := map[string]string{"C": "tool"}
		result := mergeEnvLayers(base, appEnv, toolOpEnv)
		expect := map[string]string{"A": "os", "B": "app", "C": "tool"}
		for _, e := range result {
			parts := strings.SplitN(e, "=", 2)
			if len(parts) != 2 {
				continue
			}
			if expected, ok := expect[parts[0]]; ok {
				if parts[1] != expected {
					t.Errorf("expected %s=%s, got %s=%s", parts[0], expected, parts[0], parts[1])
				}
			}
		}
	})

	t.Run("new keys are added", func(t *testing.T) {
		base := []string{"PATH=/usr/bin"}
		appEnv := map[string]string{"NEW_APP": "val1"}
		toolOpEnv := map[string]string{"NEW_TOOL": "val2"}
		result := mergeEnvLayers(base, appEnv, toolOpEnv)
		foundApp := false
		foundTool := false
		for _, e := range result {
			if e == "NEW_APP=val1" {
				foundApp = true
			}
			if e == "NEW_TOOL=val2" {
				foundTool = true
			}
		}
		if !foundApp {
			t.Errorf("expected NEW_APP=val1, got %v", result)
		}
		if !foundTool {
			t.Errorf("expected NEW_TOOL=val2, got %v", result)
		}
	})

	t.Run("base is not mutated", func(t *testing.T) {
		base := []string{"PATH=/usr/bin", "HOME=/root"}
		original := make([]string, len(base))
		copy(original, base)
		mergeEnvLayers(base, map[string]string{"NEW": "val"})
		for i, v := range base {
			if v != original[i] {
				t.Errorf("base was mutated at index %d: got %q, want %q", i, v, original[i])
			}
		}
	})

	t.Run("CI is not forced", func(t *testing.T) {
		base := []string{"PATH=/usr/bin"}
		result := mergeEnvLayers(base, nil, nil)
		for _, e := range result {
			if e == "CI=true" {
				t.Errorf("CI=true should not be forced, got %v", result)
			}
		}
	})
}

func TestBuildCommandEnvMerge(t *testing.T) {
	tmpDir := t.TempDir()
	appManager := &mockAppManager{
		binaries: map[string]string{"testcmd": "/bin/testcmd"},
	}
	executor := NewExecutor(tmpDir, false, false, appManager, nil)

	t.Run("no extra env - inherits OS env", func(t *testing.T) {
		cmdInfo := &binmanager.CommandInfo{
			Type:    "binary",
			Command: "/bin/echo",
		}
		cmd := executor.buildCommand(context.Background(), cmdInfo, []string{"hello"}, tmpDir, nil)
		// When no env layers provided, cmd.Env should be nil (inherits OS env)
		if cmd.Env != nil {
			t.Errorf("expected nil cmd.Env when no extra env, got %v", cmd.Env)
		}
	})

	t.Run("app env applied", func(t *testing.T) {
		cmdInfo := &binmanager.CommandInfo{
			Type:    "shell",
			Command: "/bin/echo",
			Env:     map[string]string{"APP_VAR": "app_value"},
		}
		cmd := executor.buildCommand(context.Background(), cmdInfo, nil, tmpDir, nil)
		found := false
		for _, e := range cmd.Env {
			if e == "APP_VAR=app_value" {
				found = true
			}
		}
		if !found {
			t.Error("expected APP_VAR=app_value in cmd.Env")
		}
	})

	t.Run("tool op env overrides app env", func(t *testing.T) {
		cmdInfo := &binmanager.CommandInfo{
			Type:    "shell",
			Command: "/bin/echo",
			Env:     map[string]string{"SHARED": "app"},
		}
		toolOpEnv := map[string]string{"SHARED": "tool"}
		cmd := executor.buildCommand(context.Background(), cmdInfo, nil, tmpDir, toolOpEnv)
		for _, e := range cmd.Env {
			if e == "SHARED=app" {
				t.Error("app env SHARED should be overridden by tool op env")
			}
		}
		found := false
		for _, e := range cmd.Env {
			if e == "SHARED=tool" {
				found = true
			}
		}
		if !found {
			t.Error("expected SHARED=tool in cmd.Env")
		}
	})
}

func TestFailFastStopsNewTasks(t *testing.T) {
	tmpDir := t.TempDir()

	appManager := &mockAppManager{
		commands: map[string]*binmanager.CommandInfo{
			"failing-tool": {
				Type:    "shell",
				Command: "/bin/sh",
				Args:    []string{"-c", "exit 1"},
			},
			"should-not-run": {
				Type:    "shell",
				Command: "/bin/sh",
				Args:    []string{"-c", "echo ran"},
			},
		},
	}

	executor := NewExecutor(tmpDir, false, true, appManager, nil)

	var startedTools []string
	var mu sync.Mutex
	executor.SetTaskStartCallback(func(toolName string, relativeDir string) {
		mu.Lock()
		startedTools = append(startedTools, toolName)
		mu.Unlock()
	})

	// Two groups with different priorities: group 1 (priority 10) fails,
	// group 2 (priority 20) should never start
	plan := &ExecutionPlan{
		Groups: []TaskGroup{
			{
				Priority: 10,
				Tasks: []Task{
					{
						ToolName:  "failing-tool",
						Operation: config.OpLint,
						OpConfig: config.ToolOperation{
							App:  "failing-tool",
							Scope:    config.ToolScopeRepository,
							Priority: 10,
						},
					},
				},
			},
			{
				Priority: 20,
				Tasks: []Task{
					{
						ToolName:  "should-not-run",
						Operation: config.OpLint,
						OpConfig: config.ToolOperation{
							App:  "should-not-run",
							Scope:    config.ToolScopeRepository,
							Priority: 20,
						},
					},
				},
			},
		},
	}

	results, err := executor.Execute(context.Background(), plan)
	if err == nil {
		t.Fatal("expected error from fail-fast, got nil")
	}

	// Only one group should have results
	if len(results) != 1 {
		t.Errorf("expected 1 group result, got %d", len(results))
	}

	// Only the failing tool should have started
	mu.Lock()
	defer mu.Unlock()
	for _, tool := range startedTools {
		if tool == "should-not-run" {
			t.Error("should-not-run tool started despite fail-fast")
		}
	}
}

func TestFailFastPerFileSkipsRemainingFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test files
	files := []string{
		filepath.Join(tmpDir, "file1.txt"),
		filepath.Join(tmpDir, "file2.txt"),
		filepath.Join(tmpDir, "file3.txt"),
	}
	for _, f := range files {
		if err := os.WriteFile(f, []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// The tool fails on the first file; remaining files should be skipped
	appManager := &mockAppManager{
		commands: map[string]*binmanager.CommandInfo{
			"per-file-tool": {
				Type:    "shell",
				Command: "/bin/sh",
				Args:    []string{"-c", "exit 1"},
			},
		},
	}

	executor := NewExecutor(tmpDir, false, true, appManager, nil)

	var fileProgressCalls []int
	var mu sync.Mutex
	executor.SetFileProgressCallback(func(toolName string, fileIndex, totalFiles int, success bool) {
		mu.Lock()
		fileProgressCalls = append(fileProgressCalls, fileIndex)
		mu.Unlock()
	})

	batchFalse := false
	plan := &ExecutionPlan{
		Groups: []TaskGroup{
			{
				Priority: 10,
				Tasks: []Task{
					{
						ToolName:  "per-file-tool",
						Operation: config.OpLint,
						OpConfig: config.ToolOperation{
							App: "per-file-tool",
							Args:    []string{"{file}"},
							Scope:   config.ToolScopePerFile,
							Batch:   &batchFalse,
						},
						Files: files,
					},
				},
			},
		},
	}

	results, err := executor.Execute(context.Background(), plan)
	if err == nil {
		t.Fatal("expected error from fail-fast, got nil")
	}

	// Should have results for the first group
	if len(results) != 1 {
		t.Fatalf("expected 1 group result, got %d", len(results))
	}

	// The first file should have been processed and failed
	taskResult := results[0].Results[0]
	if taskResult.Success {
		t.Error("expected task to fail")
	}

	// Should only have 1 file progress call (the failed one), not 3
	mu.Lock()
	callCount := len(fileProgressCalls)
	mu.Unlock()
	if callCount > 1 {
		t.Errorf("expected at most 1 file progress call (fail-fast), got %d", callCount)
	}

	_ = err
}

func TestFailFastCancellationPropagatesParallelTasks(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a tool that fails immediately and a slow tool
	appManager := &mockAppManager{
		commands: map[string]*binmanager.CommandInfo{
			"fast-fail": {
				Type:    "shell",
				Command: "/bin/sh",
				Args:    []string{"-c", "exit 1"},
			},
			"slow-tool": {
				Type:    "shell",
				Command: "/bin/sh",
				Args:    []string{"-c", "sleep 10"},
			},
		},
	}

	executor := NewExecutor(tmpDir, false, true, appManager, nil)

	// Two sequential groups: first group has the fast-fail, second has slow-tool
	plan := &ExecutionPlan{
		Groups: []TaskGroup{
			{
				Priority: 10,
				Tasks: []Task{
					{
						ToolName:  "fast-fail",
						Operation: config.OpLint,
						OpConfig: config.ToolOperation{
							App:  "fast-fail",
							Scope:    config.ToolScopeRepository,
							Priority: 10,
						},
					},
				},
			},
			{
				Priority: 20,
				Tasks: []Task{
					{
						ToolName:  "slow-tool",
						Operation: config.OpLint,
						OpConfig: config.ToolOperation{
							App:  "slow-tool",
							Scope:    config.ToolScopeRepository,
							Priority: 20,
						},
					},
				},
			},
		},
	}

	// Execute should complete quickly (not wait for slow-tool's 10s sleep)
	start := time.Now()
	_, err := executor.Execute(context.Background(), plan)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from fail-fast, got nil")
	}

	// Should complete in well under 10 seconds (the slow tool should never start)
	if elapsed > 5*time.Second {
		t.Errorf("execution took %v, expected much less than 10s (slow tool should not run)", elapsed)
	}
}

func TestFailFastContextCancelsRunningPerFileLoop(t *testing.T) {
	tmpDir := t.TempDir()

	// Create many test files
	var files []string
	for i := 0; i < 20; i++ {
		f := filepath.Join(tmpDir, fmt.Sprintf("file%d.txt", i))
		if err := os.WriteFile(f, []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}

	// Tool fails on every file
	appManager := &mockAppManager{
		commands: map[string]*binmanager.CommandInfo{
			"always-fail": {
				Type:    "shell",
				Command: "/bin/sh",
				Args:    []string{"-c", "exit 1"},
			},
		},
	}

	executor := NewExecutor(tmpDir, false, true, appManager, nil)

	var processedCount int
	var mu sync.Mutex
	executor.SetFileProgressCallback(func(toolName string, fileIndex, totalFiles int, success bool) {
		mu.Lock()
		processedCount++
		mu.Unlock()
	})

	batchFalse := false
	plan := &ExecutionPlan{
		Groups: []TaskGroup{
			{
				Priority: 10,
				Tasks: []Task{
					{
						ToolName:  "always-fail",
						Operation: config.OpLint,
						OpConfig: config.ToolOperation{
							App: "always-fail",
							Args:    []string{"{file}"},
							Scope:   config.ToolScopePerFile,
							Batch:   &batchFalse,
						},
						Files: files,
					},
				},
			},
		},
	}

	_, err := executor.Execute(context.Background(), plan)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// With fail-fast, only 1 file should be processed before stopping
	mu.Lock()
	count := processedCount
	mu.Unlock()
	if count > 1 {
		t.Errorf("with fail-fast, expected at most 1 file processed, got %d", count)
	}
}

func TestExecutorDoesNotPrintOutputDirectly(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("per-file failure output captured not printed", func(t *testing.T) {
		appManager := &mockAppManager{
			commands: map[string]*binmanager.CommandInfo{
				"failing-tool": {
					Type:    "shell",
					Command: "/bin/sh",
					Args:    []string{"-c", "echo 'error output from tool' && exit 1"},
				},
			},
		}

		executor := NewExecutor(tmpDir, false, true, appManager, nil)

		file := filepath.Join(tmpDir, "test.txt")
		if err := os.WriteFile(file, []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}

		batchFalse := false
		plan := &ExecutionPlan{
			Groups: []TaskGroup{
				{
					Priority: 10,
					Tasks: []Task{
						{
							ToolName:  "failing-tool",
							Operation: config.OpLint,
							OpConfig: config.ToolOperation{
								App: "failing-tool",
								Args:    []string{"{file}"},
								Scope:   config.ToolScopePerFile,
								Batch:   &batchFalse,
							},
							Files: []string{file},
						},
					},
				},
			},
		}

		// Capture stdout to verify executor does not print directly
		oldStdout := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		_, _ = executor.Execute(context.Background(), plan)

		_ = w.Close()
		os.Stdout = oldStdout

		var buf [4096]byte
		n, _ := r.Read(buf[:])
		captured := string(buf[:n])

		if strings.Contains(captured, "error output from tool") {
			t.Errorf("executor should not print tool output directly to stdout, but captured: %q", captured)
		}
	})

	t.Run("batch failure output captured not printed", func(t *testing.T) {
		appManager := &mockAppManager{
			commands: map[string]*binmanager.CommandInfo{
				"batch-fail": {
					Type:    "shell",
					Command: "/bin/sh",
					Args:    []string{"-c", "echo 'batch error output' && exit 1"},
				},
			},
		}

		executor := NewExecutor(tmpDir, false, true, appManager, nil)

		plan := &ExecutionPlan{
			Groups: []TaskGroup{
				{
					Priority: 10,
					Tasks: []Task{
						{
							ToolName:  "batch-fail",
							Operation: config.OpLint,
							OpConfig: config.ToolOperation{
								App: "batch-fail",
								Scope:   config.ToolScopeRepository,
							},
						},
					},
				},
			},
		}

		oldStdout := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		results, _ := executor.Execute(context.Background(), plan)

		_ = w.Close()
		os.Stdout = oldStdout

		var buf [4096]byte
		n, _ := r.Read(buf[:])
		captured := string(buf[:n])

		if strings.Contains(captured, "batch error output") {
			t.Errorf("executor should not print tool output directly to stdout, but captured: %q", captured)
		}

		// Verify the output IS captured in the result
		if len(results) > 0 && len(results[0].Results) > 0 {
			if !strings.Contains(results[0].Results[0].Output, "batch error output") {
				t.Errorf("expected tool output to be captured in ExecutionResult.Output, got %q", results[0].Results[0].Output)
			}
		}
	})
}

func TestIsUnderCwd(t *testing.T) {
	tests := []struct {
		name     string
		root     string
		cwd      string
		path     string
		expected bool
	}{
		{
			name:     "cwd equals root - always true",
			root:     "/repo",
			cwd:      "/repo",
			path:     "/outside/file.go",
			expected: true,
		},
		{
			name:     "file inside cwd",
			root:     "/repo",
			cwd:      "/repo/services/api",
			path:     "/repo/services/api/main.go",
			expected: true,
		},
		{
			name:     "file in subdirectory of cwd",
			root:     "/repo",
			cwd:      "/repo/services/api",
			path:     "/repo/services/api/internal/handler.go",
			expected: true,
		},
		{
			name:     "file at cwd boundary (cwd itself)",
			root:     "/repo",
			cwd:      "/repo/services/api",
			path:     "/repo/services/api",
			expected: true,
		},
		{
			name:     "file outside cwd - sibling directory",
			root:     "/repo",
			cwd:      "/repo/services/api",
			path:     "/repo/services/web/index.js",
			expected: false,
		},
		{
			name:     "file outside cwd - parent directory",
			root:     "/repo",
			cwd:      "/repo/services/api",
			path:     "/repo/go.mod",
			expected: false,
		},
		{
			name:     "non-normalized path with .. segments",
			root:     "/repo",
			cwd:      "/repo/services/api",
			path:     "/repo/services/api/../api/main.go",
			expected: true,
		},
		{
			name:     "non-normalized path leading outside cwd",
			root:     "/repo",
			cwd:      "/repo/services/api",
			path:     "/repo/services/api/../../lib/utils.go",
			expected: false,
		},
		{
			name:     "path with trailing slash",
			root:     "/repo",
			cwd:      "/repo/services/api",
			path:     "/repo/services/api/pkg/",
			expected: true,
		},
		{
			name:     "sibling directory with matching prefix",
			root:     "/repo",
			cwd:      "/repo/services/api",
			path:     "/repo/services/api-v2/main.go",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			planner := &Planner{
				rootPath: tt.root,
				cwdPath:  tt.cwd,
			}
			result := planner.isUnderCwd(tt.path)
			if result != tt.expected {
				t.Errorf("isUnderCwd(%q) = %v, want %v (root=%q, cwd=%q)",
					tt.path, result, tt.expected, tt.root, tt.cwd)
			}
		})
	}
}

func TestFilterFilesToCwd(t *testing.T) {
	t.Run("no-op when cwd equals root", func(t *testing.T) {
		planner := &Planner{
			rootPath: "/repo",
			cwdPath:  "/repo",
		}
		files := []string{"/repo/a.go", "/repo/pkg/b.go", "/outside/c.go"}
		result := planner.filterFilesToCwd(files)
		if len(result) != len(files) {
			t.Errorf("expected %d files (no-op), got %d", len(files), len(result))
		}
	})

	t.Run("filters to cwd subtree", func(t *testing.T) {
		planner := &Planner{
			rootPath: "/repo",
			cwdPath:  "/repo/services/api",
		}
		files := []string{
			"/repo/services/api/main.go",
			"/repo/services/api/internal/handler.go",
			"/repo/services/web/index.js",
			"/repo/go.mod",
		}
		result := planner.filterFilesToCwd(files)
		if len(result) != 2 {
			t.Errorf("expected 2 files under cwd, got %d: %v", len(result), result)
		}
	})

	t.Run("empty input returns nil", func(t *testing.T) {
		planner := &Planner{
			rootPath: "/repo",
			cwdPath:  "/repo/services/api",
		}
		result := planner.filterFilesToCwd(nil)
		if result != nil {
			t.Errorf("expected nil for nil input, got %v", result)
		}
	})

	t.Run("all files outside cwd returns nil", func(t *testing.T) {
		planner := &Planner{
			rootPath: "/repo",
			cwdPath:  "/repo/services/api",
		}
		files := []string{"/repo/services/web/index.js", "/repo/go.mod"}
		result := planner.filterFilesToCwd(files)
		if len(result) != 0 {
			t.Errorf("expected 0 files, got %d: %v", len(result), result)
		}
	})
}

func TestFilterProjectLocationsToCwd(t *testing.T) {
	t.Run("no-op when cwd equals root", func(t *testing.T) {
		planner := &Planner{
			rootPath: "/repo",
			cwdPath:  "/repo",
		}
		locs := []project.ProjectLocation{
			{Type: "go", Path: "/repo/services/api"},
			{Type: "node", Path: "/repo/services/web"},
		}
		result := planner.filterProjectLocationsToCwd(locs)
		if len(result) != 2 {
			t.Errorf("expected 2 locations (no-op), got %d", len(result))
		}
	})

	t.Run("filters to cwd subtree", func(t *testing.T) {
		planner := &Planner{
			rootPath: "/repo",
			cwdPath:  "/repo/services/api",
		}
		locs := []project.ProjectLocation{
			{Type: "go", Path: "/repo/services/api"},
			{Type: "go", Path: "/repo/services/api/internal"},
			{Type: "node", Path: "/repo/services/web"},
			{Type: "go", Path: "/repo"},
		}
		result := planner.filterProjectLocationsToCwd(locs)
		if len(result) != 2 {
			t.Errorf("expected 2 locations under cwd, got %d: %v", len(result), result)
		}
	})

	t.Run("excludes sibling with matching prefix", func(t *testing.T) {
		planner := &Planner{
			rootPath: "/repo",
			cwdPath:  "/repo/services/api",
		}
		locs := []project.ProjectLocation{
			{Type: "go", Path: "/repo/services/api"},
			{Type: "go", Path: "/repo/services/api-admin"},
		}
		result := planner.filterProjectLocationsToCwd(locs)
		if len(result) != 1 {
			t.Errorf("expected 1 location (api-admin excluded), got %d: %v", len(result), result)
		}
		if len(result) == 1 && result[0].Path != "/repo/services/api" {
			t.Errorf("expected /repo/services/api, got %q", result[0].Path)
		}
	})

	t.Run("empty input returns nil", func(t *testing.T) {
		planner := &Planner{
			rootPath: "/repo",
			cwdPath:  "/repo/services/api",
		}
		result := planner.filterProjectLocationsToCwd(nil)
		if result != nil {
			t.Errorf("expected nil for nil input, got %v", result)
		}
	})
}

func TestCollectTasksPerProjectFromSubdirectory(t *testing.T) {
	root := "/repo"
	cwd := "/repo/services/api"

	tools := config.MapOfTools{
		"go-lint": {
			Name:         "go-lint",
			ProjectTypes: []string{"go"},
			Operations: map[config.OperationType]config.ToolOperation{
				config.OpLint: {
					App:  "golangci-lint",
					Scope:    config.ToolScopePerProject,
					Priority: 10,
					Globs:    []string{"**/*.go"},
				},
			},
		},
	}

	planner := &Planner{
		rootPath:      root,
		cwdPath:       cwd,
		detectedTypes: []string{"go"},
		tools:         tools,
		cachedFiles: []string{
			"/repo/services/api/main.go",
			"/repo/services/api/internal/handler.go",
			"/repo/services/web/server.go",
			"/repo/lib/utils.go",
		},
		cachedProjects: []project.ProjectLocation{
			{Type: "go", Path: "/repo/services/api"},
			{Type: "go", Path: "/repo/services/web"},
			{Type: "go", Path: "/repo"},
		},
		cacheInitialized: true,
	}

	tasks := planner.collectTasks(config.OpLint, nil)

	// Should only create tasks for /repo/services/api (inside cwd), not web or root
	if len(tasks) != 1 {
		t.Fatalf("expected exactly 1 task, got %d", len(tasks))
	}
	if tasks[0].ProjectPath != "/repo/services/api" {
		t.Errorf("expected ProjectPath=/repo/services/api, got %q", tasks[0].ProjectPath)
	}
	if len(tasks[0].Files) != 2 {
		t.Errorf("expected 2 files under cwd, got %d: %v", len(tasks[0].Files), tasks[0].Files)
	}
	for _, f := range tasks[0].Files {
		if !strings.HasPrefix(f, "/repo/services/api/") {
			t.Errorf("expected file under /repo/services/api/, got %q", f)
		}
	}
}

func TestCollectTasksPerProjectExplicitFilesFromSubdirectory(t *testing.T) {
	root := "/repo"
	cwd := "/repo/services"

	tools := config.MapOfTools{
		"go-lint": {
			Name:         "go-lint",
			ProjectTypes: []string{"go"},
			Operations: map[config.OperationType]config.ToolOperation{
				config.OpLint: {
					App:  "golangci-lint",
					Scope:    config.ToolScopePerProject,
					Priority: 10,
					Globs:    []string{"**/*.go"},
				},
			},
		},
	}

	planner := &Planner{
		rootPath:      root,
		cwdPath:       cwd,
		detectedTypes: []string{"go"},
		tools:         tools,
		cachedFiles: []string{
			"/repo/services/api/main.go",
			"/repo/services/api/handler.go",
			"/repo/services/web/server.go",
			"/repo/services/shared/util.go",
			"/repo/lib/helper.go",
		},
		cachedProjects: []project.ProjectLocation{
			{Type: "go", Path: "/repo/services/api"},
			{Type: "go", Path: "/repo/services/web"},
			{Type: "go", Path: "/repo"},
		},
		cacheInitialized: true,
	}

	// Pass explicit files that span cwd and outside cwd
	explicitFiles := []string{
		"/repo/services/api/main.go",
		"/repo/services/web/server.go",
		"/repo/services/shared/util.go",
		"/repo/lib/helper.go",
	}
	tasks := planner.collectTasks(config.OpLint, explicitFiles)

	// Files outside cwd (/repo/lib/helper.go) should be excluded.
	// Files inside cwd but not matching a cwd-subtree project (/repo/services/shared/util.go)
	// should NOT create a task at rootPath.
	for _, task := range tasks {
		if !strings.HasPrefix(task.ProjectPath, "/repo/services") {
			t.Errorf("task ProjectPath %q is outside cwd subtree %q", task.ProjectPath, cwd)
		}
		for _, f := range task.Files {
			if !strings.HasPrefix(f, "/repo/services/") {
				t.Errorf("task file %q is outside cwd subtree %q", f, cwd)
			}
		}
	}

	// Should have tasks for api and web projects (shared/util.go may be grouped into a
	// parent project or dropped since its nearest project is /repo which is outside cwd)
	if len(tasks) < 2 {
		t.Errorf("expected at least 2 tasks (api + web), got %d", len(tasks))
		for i, task := range tasks {
			t.Logf("  task[%d]: ProjectPath=%q Files=%v", i, task.ProjectPath, task.Files)
		}
	}
}

func TestCollectTasksPerProjectWholeProjectModeFromSubdirectory(t *testing.T) {
	root := "/repo"
	cwd := "/repo/services/api"

	tools := config.MapOfTools{
		"go-vet": {
			Name:         "go-vet",
			ProjectTypes: []string{"go"},
			Operations: map[config.OperationType]config.ToolOperation{
				config.OpLint: {
					App:  "go",
					Args:     []string{"vet", "./..."},
					Scope:    config.ToolScopePerProject,
					Priority: 10,
					// No Globs -> whole-project mode
				},
			},
		},
	}

	planner := &Planner{
		rootPath:      root,
		cwdPath:       cwd,
		detectedTypes: []string{"go"},
		tools:         tools,
		cachedFiles:   []string{},
		cachedProjects: []project.ProjectLocation{
			{Type: "go", Path: "/repo/services/api"},
			{Type: "go", Path: "/repo/services/web"},
			{Type: "go", Path: "/repo"},
		},
		cacheInitialized: true,
	}

	tasks := planner.collectTasks(config.OpLint, nil)

	// Should only create task for api project (inside cwd)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].ProjectPath != "/repo/services/api" {
		t.Errorf("expected ProjectPath=/repo/services/api, got %q", tasks[0].ProjectPath)
	}
}

func TestCollectTasksPerProjectFromRootRegression(t *testing.T) {
	root := "/repo"
	cwd := "/repo" // running from root

	tools := config.MapOfTools{
		"go-lint": {
			Name:         "go-lint",
			ProjectTypes: []string{"go"},
			Operations: map[config.OperationType]config.ToolOperation{
				config.OpLint: {
					App:  "golangci-lint",
					Scope:    config.ToolScopePerProject,
					Priority: 10,
					Globs:    []string{"**/*.go"},
				},
			},
		},
	}

	planner := &Planner{
		rootPath:      root,
		cwdPath:       cwd,
		detectedTypes: []string{"go"},
		tools:         tools,
		cachedFiles: []string{
			"/repo/services/api/main.go",
			"/repo/services/web/server.go",
		},
		cachedProjects: []project.ProjectLocation{
			{Type: "go", Path: "/repo/services/api"},
			{Type: "go", Path: "/repo/services/web"},
		},
		cacheInitialized: true,
	}

	tasks := planner.collectTasks(config.OpLint, nil)

	// From root, should create tasks for both projects
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks (one per project), got %d", len(tasks))
		for i, task := range tasks {
			t.Logf("  task[%d]: ProjectPath=%q Files=%v", i, task.ProjectPath, task.Files)
		}
	}

	projectPaths := make(map[string]bool)
	for _, task := range tasks {
		projectPaths[task.ProjectPath] = true
	}
	if !projectPaths["/repo/services/api"] {
		t.Error("expected task for /repo/services/api")
	}
	if !projectPaths["/repo/services/web"] {
		t.Error("expected task for /repo/services/web")
	}
}

func TestCollectTasksPerFileFromSubdirectory(t *testing.T) {
	root := "/repo"
	cwd := "/repo/services/api"

	tools := config.MapOfTools{
		"prettier": {
			Name: "prettier",
			Operations: map[config.OperationType]config.ToolOperation{
				config.OpFix: {
					App:  "prettier",
					Args:     []string{"--write", "{file}"},
					Scope:    config.ToolScopePerFile,
					Priority: 10,
					Globs:    []string{"**/*.js", "**/*.ts"},
				},
			},
		},
	}

	planner := &Planner{
		rootPath:      root,
		cwdPath:       cwd,
		detectedTypes: []string{},
		tools:         tools,
		cachedFiles: []string{
			"/repo/services/api/handler.ts",
			"/repo/services/api/utils.js",
			"/repo/services/web/index.js",
			"/repo/lib/helper.ts",
		},
		cachedProjects:   []project.ProjectLocation{},
		cacheInitialized: true,
	}

	tasks := planner.collectTasks(config.OpFix, nil)

	// Should only create per-file tasks for files inside cwd
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks (files inside cwd), got %d", len(tasks))
	}
	for _, task := range tasks {
		if len(task.Files) != 1 {
			t.Fatalf("expected 1 file per task, got %d", len(task.Files))
		}
		file := task.Files[0]
		if !strings.HasPrefix(file, "/repo/services/api/") {
			t.Errorf("expected file under /repo/services/api/, got %q", file)
		}
	}
}

func TestCollectTasksPerFileFromRootRegression(t *testing.T) {
	root := "/repo"
	cwd := "/repo" // running from root

	tools := config.MapOfTools{
		"prettier": {
			Name: "prettier",
			Operations: map[config.OperationType]config.ToolOperation{
				config.OpFix: {
					App:  "prettier",
					Args:     []string{"--write", "{file}"},
					Scope:    config.ToolScopePerFile,
					Priority: 10,
					Globs:    []string{"**/*.js", "**/*.ts"},
				},
			},
		},
	}

	planner := &Planner{
		rootPath:      root,
		cwdPath:       cwd,
		detectedTypes: []string{},
		tools:         tools,
		cachedFiles: []string{
			"/repo/services/api/handler.ts",
			"/repo/services/api/utils.js",
			"/repo/services/web/index.js",
			"/repo/lib/helper.ts",
		},
		cachedProjects:   []project.ProjectLocation{},
		cacheInitialized: true,
	}

	tasks := planner.collectTasks(config.OpFix, nil)

	// From root, should create tasks for all 4 matching files
	if len(tasks) != 4 {
		t.Errorf("expected 4 tasks (all matching files from root), got %d", len(tasks))
		for i, task := range tasks {
			t.Logf("  task[%d]: Files=%v", i, task.Files)
		}
	}
}

func TestGlobsOverlap(t *testing.T) {
	tests := []struct {
		name     string
		globs1   []string
		globs2   []string
		expected bool
	}{
		{
			name:     "identical globs",
			globs1:   []string{"*.js"},
			globs2:   []string{"*.js"},
			expected: true,
		},
		{
			name:     "different globs",
			globs1:   []string{"*.js"},
			globs2:   []string{"*.ts"},
			expected: false,
		},
		{
			name:     "one matching",
			globs1:   []string{"*.js", "*.ts"},
			globs2:   []string{"*.ts", "*.go"},
			expected: true,
		},
		{
			name:     "no match",
			globs1:   []string{"*.js"},
			globs2:   []string{"*.go"},
			expected: false,
		},
		{
			name:     "first globs empty means all files",
			globs1:   nil,
			globs2:   []string{"*.js"},
			expected: true,
		},
		{
			name:     "second globs empty means all files",
			globs1:   []string{"*.js"},
			globs2:   nil,
			expected: true,
		},
		{
			name:     "both globs empty means all files",
			globs1:   nil,
			globs2:   nil,
			expected: true,
		},
		{
			name:     "suffix overlap ts and d.ts",
			globs1:   []string{"**/*.ts"},
			globs2:   []string{"**/*.d.ts"},
			expected: true,
		},
		{
			name:     "suffix overlap js and min.js",
			globs1:   []string{"**/*.js"},
			globs2:   []string{"**/*.min.js"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := globsOverlap(tt.globs1, tt.globs2)
			if result != tt.expected {
				t.Errorf("globsOverlap() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestDatamitsuignorePerFileScope(t *testing.T) {
	tmpDir := t.TempDir()

	// Create files
	_ = os.MkdirAll(filepath.Join(tmpDir, "src"), 0755)
	_ = os.MkdirAll(filepath.Join(tmpDir, "docs"), 0755)
	_ = os.WriteFile(filepath.Join(tmpDir, "src", "main.js"), []byte("code"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "src", "utils.js"), []byte("code"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "docs", "readme.md"), []byte("doc"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "docs", "guide.md"), []byte("doc"), 0644)

	// Create .datamitsuignore: disable eslint for all markdown files
	_ = os.WriteFile(filepath.Join(tmpDir, ".datamitsuignore"), []byte("**/*.md: eslint\n"), 0644)

	tools := config.MapOfTools{
		"eslint": {
			Name: "eslint",
			Operations: map[config.OperationType]config.ToolOperation{
				config.OpLint: {
					App:  "eslint",
					Scope:    config.ToolScopePerFile,
					Globs:    []string{"**/*.js", "**/*.md"},
					Priority: 10,
				},
			},
		},
	}

	planner := NewPlanner(tmpDir, tmpDir, []string{}, tools, config.MapOfProjectTypes{}, nil)
	plan, err := planner.Plan(context.Background(), config.OpLint, nil, nil)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	// Should only have tasks for .js files, not .md files
	var taskFiles []string
	for _, g := range plan.Groups {
		for _, task := range g.Tasks {
			taskFiles = append(taskFiles, task.Files...)
		}
	}

	for _, f := range taskFiles {
		if strings.HasSuffix(f, ".md") {
			t.Errorf("unexpected task for disabled file: %s", f)
		}
	}

	// Should have exactly 2 tasks (one per .js file)
	if len(taskFiles) != 2 {
		t.Errorf("expected 2 tasks (js files only), got %d: %v", len(taskFiles), taskFiles)
	}
}

func TestDatamitsuignoreInversion(t *testing.T) {
	tmpDir := t.TempDir()

	_ = os.MkdirAll(filepath.Join(tmpDir, "src"), 0755)
	_ = os.MkdirAll(filepath.Join(tmpDir, "docs"), 0755)
	_ = os.WriteFile(filepath.Join(tmpDir, "src", "notes.md"), []byte("notes"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "docs", "guide.md"), []byte("guide"), 0644)

	// Root disables prettier for all .md
	_ = os.WriteFile(filepath.Join(tmpDir, ".datamitsuignore"), []byte("**/*.md: prettier\n"), 0644)
	// docs/ re-enables prettier
	_ = os.WriteFile(filepath.Join(tmpDir, "docs", ".datamitsuignore"), []byte("!**/*.md: prettier\n"), 0644)

	tools := config.MapOfTools{
		"prettier": {
			Name: "prettier",
			Operations: map[config.OperationType]config.ToolOperation{
				config.OpFix: {
					App:  "prettier",
					Scope:    config.ToolScopePerFile,
					Globs:    []string{"**/*.md"},
					Priority: 10,
				},
			},
		},
	}

	planner := NewPlanner(tmpDir, tmpDir, []string{}, tools, config.MapOfProjectTypes{}, nil)
	plan, err := planner.Plan(context.Background(), config.OpFix, nil, nil)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	var taskFiles []string
	for _, g := range plan.Groups {
		for _, task := range g.Tasks {
			taskFiles = append(taskFiles, task.Files...)
		}
	}

	// docs/guide.md should be included (re-enabled), src/notes.md should be excluded
	foundGuide := false
	for _, f := range taskFiles {
		if strings.HasSuffix(f, "notes.md") {
			t.Errorf("src/notes.md should be disabled, but found task for it")
		}
		if strings.HasSuffix(f, "guide.md") {
			foundGuide = true
		}
	}
	if !foundGuide {
		t.Error("docs/guide.md should be re-enabled by docs/.datamitsuignore inversion")
	}
}

func TestDatamitsuignoreNoEffect(t *testing.T) {
	tmpDir := t.TempDir()

	_ = os.WriteFile(filepath.Join(tmpDir, "main.js"), []byte("code"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "test.js"), []byte("test"), 0644)

	// Disable golangci-lint for .go files - should not affect eslint on .js
	_ = os.WriteFile(filepath.Join(tmpDir, ".datamitsuignore"), []byte("**/*.go: golangci-lint\n"), 0644)

	tools := config.MapOfTools{
		"eslint": {
			Name: "eslint",
			Operations: map[config.OperationType]config.ToolOperation{
				config.OpLint: {
					App:  "eslint",
					Scope:    config.ToolScopePerFile,
					Globs:    []string{"**/*.js"},
					Priority: 10,
				},
			},
		},
	}

	planner := NewPlanner(tmpDir, tmpDir, []string{}, tools, config.MapOfProjectTypes{}, nil)
	plan, err := planner.Plan(context.Background(), config.OpLint, nil, nil)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	var taskCount int
	for _, g := range plan.Groups {
		taskCount += len(g.Tasks)
	}

	if taskCount != 2 {
		t.Errorf("expected 2 tasks (no files should be disabled), got %d", taskCount)
	}
}

func TestConfigDefinedIgnoreRules(t *testing.T) {
	tmpDir := t.TempDir()

	_ = os.MkdirAll(filepath.Join(tmpDir, "src"), 0755)
	_ = os.MkdirAll(filepath.Join(tmpDir, "docs"), 0755)
	_ = os.WriteFile(filepath.Join(tmpDir, "src", "main.js"), []byte("code"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "src", "utils.js"), []byte("code"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "docs", "readme.md"), []byte("doc"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "docs", "guide.md"), []byte("doc"), 0644)

	tools := config.MapOfTools{
		"eslint": {
			Name: "eslint",
			Operations: map[config.OperationType]config.ToolOperation{
				config.OpLint: {
					App:  "eslint",
					Scope:    config.ToolScopePerFile,
					Globs:    []string{"**/*.js", "**/*.md"},
					Priority: 10,
				},
			},
		},
	}

	// Config-defined ignore rules (same effect as .datamitsuignore file)
	ignoreRules := []string{"**/*.md: eslint"}

	planner := NewPlanner(tmpDir, tmpDir, []string{}, tools, config.MapOfProjectTypes{}, ignoreRules)
	plan, err := planner.Plan(context.Background(), config.OpLint, nil, nil)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	var taskFiles []string
	for _, g := range plan.Groups {
		for _, task := range g.Tasks {
			taskFiles = append(taskFiles, task.Files...)
		}
	}

	for _, f := range taskFiles {
		if strings.HasSuffix(f, ".md") {
			t.Errorf("config-defined ignore rule should have disabled eslint for: %s", f)
		}
	}

	if len(taskFiles) != 2 {
		t.Errorf("expected 2 tasks (js files only), got %d: %v", len(taskFiles), taskFiles)
	}
}

func TestConfigDefinedIgnoreRulesWithWildcard(t *testing.T) {
	tmpDir := t.TempDir()

	_ = os.MkdirAll(filepath.Join(tmpDir, "src"), 0755)
	_ = os.WriteFile(filepath.Join(tmpDir, "src", "main.js"), []byte("code"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "src", "test.ts"), []byte("code"), 0644)

	tools := config.MapOfTools{
		"eslint": {
			Name: "eslint",
			Operations: map[config.OperationType]config.ToolOperation{
				config.OpLint: {
					App:  "eslint",
					Scope:    config.ToolScopePerFile,
					Globs:    []string{"**/*.js", "**/*.ts"},
					Priority: 10,
				},
			},
		},
	}

	// Disable all tools for .ts files
	ignoreRules := []string{"**/*.ts: *"}

	planner := NewPlanner(tmpDir, tmpDir, []string{}, tools, config.MapOfProjectTypes{}, ignoreRules)
	plan, err := planner.Plan(context.Background(), config.OpLint, nil, nil)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	var taskFiles []string
	for _, g := range plan.Groups {
		for _, task := range g.Tasks {
			taskFiles = append(taskFiles, task.Files...)
		}
	}

	if len(taskFiles) != 1 {
		t.Errorf("expected 1 task (only .js), got %d: %v", len(taskFiles), taskFiles)
	}
	for _, f := range taskFiles {
		if strings.HasSuffix(f, ".ts") {
			t.Errorf("wildcard ignore rule should have disabled all tools for: %s", f)
		}
	}
}

func TestIsToolDisabledForFile(t *testing.T) {
	p := &Planner{
		rootPath: "/repo",
	}

	t.Run("nil matcher returns false", func(t *testing.T) {
		if p.isToolDisabledForFile("eslint", "/repo/src/main.js") {
			t.Error("nil matcher should not disable anything")
		}
	})
}

func TestToolCachePlaceholderIntegration(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("tool receives expanded toolCache path with tool isolation", func(t *testing.T) {
		cachePath, err := env.GetProjectCachePath(tmpDir, "", "echo-tool")
		if err != nil {
			t.Fatalf("failed to compute project cache path: %v", err)
		}

		appManager := &mockAppManager{
			commands: map[string]*binmanager.CommandInfo{
				"echo-tool": {
					Type:    "shell",
					Command: "/bin/sh",
					Args:    []string{"-c"},
				},
			},
		}

		executor := NewExecutor(tmpDir, false, false, appManager, nil)

		plan := &ExecutionPlan{
			Groups: []TaskGroup{
				{
					Priority: 10,
					Tasks: []Task{
						{
							ToolName:  "echo-tool",
							Operation: config.OpLint,
							OpConfig: config.ToolOperation{
								App: "echo-tool",
								Args:    []string{"echo CACHE={toolCache}"},
								Scope:   config.ToolScopeRepository,
							},
						},
					},
				},
			},
		}

		results, err := executor.Execute(context.Background(), plan)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		if len(results) != 1 || len(results[0].Results) != 1 {
			t.Fatalf("unexpected results count: groups=%d", len(results))
		}

		r := results[0].Results[0]
		if !r.Success {
			t.Fatalf("expected success, got error: %v", r.Error)
		}

		expected := "CACHE=" + cachePath
		if !strings.Contains(r.Output, expected) {
			t.Errorf("tool output should contain expanded toolCache path\ngot:  %q\nwant: contains %q", r.Output, expected)
		}
	})

	t.Run("tool can write to toolCache directory", func(t *testing.T) {
		cachePath, err := env.GetProjectCachePath(tmpDir, "", "write-tool")
		if err != nil {
			t.Fatalf("failed to compute project cache path: %v", err)
		}

		appManager := &mockAppManager{
			commands: map[string]*binmanager.CommandInfo{
				"write-tool": {
					Type:    "shell",
					Command: "/bin/sh",
					Args:    []string{"-c"},
				},
			},
		}

		executor := NewExecutor(tmpDir, false, false, appManager, nil)

		plan := &ExecutionPlan{
			Groups: []TaskGroup{
				{
					Priority: 10,
					Tasks: []Task{
						{
							ToolName:  "write-tool",
							Operation: config.OpFix,
							OpConfig: config.ToolOperation{
								App: "write-tool",
								Args:    []string{"mkdir -p {toolCache} && echo ok > {toolCache}/testfile.txt && cat {toolCache}/testfile.txt"},
								Scope:   config.ToolScopeRepository,
							},
						},
					},
				},
			},
		}

		results, err := executor.Execute(context.Background(), plan)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		r := results[0].Results[0]
		if !r.Success {
			t.Fatalf("expected success, got error: %v\noutput: %s", r.Error, r.Output)
		}

		if !strings.Contains(r.Output, "ok") {
			t.Errorf("tool should have written and read from cache dir, got output: %q", r.Output)
		}

		testFilePath := filepath.Join(cachePath, "testfile.txt")
		content, err := os.ReadFile(testFilePath)
		if err != nil {
			t.Fatalf("failed to read cache file: %v", err)
		}
		if !strings.Contains(string(content), "ok") {
			t.Errorf("cache file content = %q, want 'ok'", string(content))
		}
	})

	t.Run("toolCache combined with cwd placeholder uses project isolation", func(t *testing.T) {
		projectDir := filepath.Join(tmpDir, "services", "api")
		if err := os.MkdirAll(projectDir, 0755); err != nil {
			t.Fatal(err)
		}

		cachePath, err := env.GetProjectCachePath(tmpDir, "services/api", "combo-tool")
		if err != nil {
			t.Fatalf("failed to compute project cache path: %v", err)
		}

		appManager := &mockAppManager{
			commands: map[string]*binmanager.CommandInfo{
				"combo-tool": {
					Type:    "shell",
					Command: "/bin/sh",
					Args:    []string{"-c"},
				},
			},
		}

		executor := NewExecutor(tmpDir, false, false, appManager, nil)

		plan := &ExecutionPlan{
			Groups: []TaskGroup{
				{
					Priority: 10,
					Tasks: []Task{
						{
							ToolName:  "combo-tool",
							Operation: config.OpLint,
							OpConfig: config.ToolOperation{
								App: "combo-tool",
								Args:    []string{"echo CACHE={toolCache} CWD={cwd} ROOT={root}"},
								Scope:   config.ToolScopePerProject,
							},
							ProjectPath: projectDir,
						},
					},
				},
			},
		}

		results, err := executor.Execute(context.Background(), plan)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		r := results[0].Results[0]
		if !r.Success {
			t.Fatalf("expected success, got error: %v", r.Error)
		}

		output := r.Output
		if !strings.Contains(output, "CACHE="+cachePath) {
			t.Errorf("output should contain expanded toolCache path with project isolation, got: %q\nwant contains: CACHE=%s", output, cachePath)
		}
		if !strings.Contains(output, "CWD="+projectDir) {
			t.Errorf("output should contain expanded cwd path, got: %q", output)
		}
		if !strings.Contains(output, "ROOT="+tmpDir) {
			t.Errorf("output should contain expanded root path, got: %q", output)
		}
	})

	t.Run("toolCache in per-file mode with file placeholder", func(t *testing.T) {
		testFile := filepath.Join(tmpDir, "test.ts")
		if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}

		cachePath, err := env.GetProjectCachePath(tmpDir, "", "file-cache-tool")
		if err != nil {
			t.Fatalf("failed to compute project cache path: %v", err)
		}

		appManager := &mockAppManager{
			commands: map[string]*binmanager.CommandInfo{
				"file-cache-tool": {
					Type:    "shell",
					Command: "/bin/sh",
					Args:    []string{"-c"},
				},
			},
		}

		executor := NewExecutor(tmpDir, false, false, appManager, nil)

		batchFalse := false
		plan := &ExecutionPlan{
			Groups: []TaskGroup{
				{
					Priority: 10,
					Tasks: []Task{
						{
							ToolName:  "file-cache-tool",
							Operation: config.OpLint,
							OpConfig: config.ToolOperation{
								App: "file-cache-tool",
								Args:    []string{"echo --cache-dir={toolCache} --file={file}"},
								Scope:   config.ToolScopePerFile,
								Batch:   &batchFalse,
							},
							Files: []string{testFile},
						},
					},
				},
			},
		}

		results, err := executor.Execute(context.Background(), plan)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		r := results[0].Results[0]
		if !r.Success {
			t.Fatalf("expected success, got error: %v", r.Error)
		}

		output := r.Output
		expectedCache := "--cache-dir=" + cachePath
		expectedFile := "--file=" + testFile
		if !strings.Contains(output, expectedCache) {
			t.Errorf("output should contain expanded toolCache, got: %q\nwant contains: %q", output, expectedCache)
		}
		if !strings.Contains(output, expectedFile) {
			t.Errorf("output should contain expanded file, got: %q\nwant contains: %q", output, expectedFile)
		}
	})

	t.Run("nested toolCache path scenarios", func(t *testing.T) {
		cachePath, err := env.GetProjectCachePath(tmpDir, "", "nested-tool")
		if err != nil {
			t.Fatalf("failed to compute project cache path: %v", err)
		}

		appManager := &mockAppManager{
			commands: map[string]*binmanager.CommandInfo{
				"nested-tool": {
					Type:    "shell",
					Command: "/bin/sh",
					Args:    []string{"-c"},
				},
			},
		}

		executor := NewExecutor(tmpDir, false, false, appManager, nil)

		plan := &ExecutionPlan{
			Groups: []TaskGroup{
				{
					Priority: 10,
					Tasks: []Task{
						{
							ToolName:  "nested-tool",
							Operation: config.OpLint,
							OpConfig: config.ToolOperation{
								App: "nested-tool",
								Args:    []string{"echo {toolCache}/tsbuildinfo {toolCache}/eslint-cache {toolCache}/stylelint-cache"},
								Scope:   config.ToolScopeRepository,
							},
						},
					},
				},
			},
		}

		results, err := executor.Execute(context.Background(), plan)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		r := results[0].Results[0]
		if !r.Success {
			t.Fatalf("expected success, got error: %v", r.Error)
		}

		output := strings.TrimSpace(r.Output)
		expected := cachePath + "/tsbuildinfo " + cachePath + "/eslint-cache " + cachePath + "/stylelint-cache"
		if output != expected {
			t.Errorf("output mismatch\ngot:  %q\nwant: %q", output, expected)
		}
	})

	t.Run("different tools in same project get isolated cache directories", func(t *testing.T) {
		tscCache, err := env.GetProjectCachePath(tmpDir, "", "tsc")
		if err != nil {
			t.Fatalf("failed to compute tsc cache path: %v", err)
		}
		eslintCache, err := env.GetProjectCachePath(tmpDir, "", "eslint")
		if err != nil {
			t.Fatalf("failed to compute eslint cache path: %v", err)
		}

		appManager := &mockAppManager{
			commands: map[string]*binmanager.CommandInfo{
				"tsc": {
					Type:    "shell",
					Command: "/bin/sh",
					Args:    []string{"-c"},
				},
				"eslint": {
					Type:    "shell",
					Command: "/bin/sh",
					Args:    []string{"-c"},
				},
			},
		}

		executor := NewExecutor(tmpDir, false, false, appManager, nil)

		plan := &ExecutionPlan{
			Groups: []TaskGroup{
				{
					Priority: 10,
					Tasks: []Task{
						{
							ToolName:  "tsc",
							Operation: config.OpLint,
							OpConfig: config.ToolOperation{
								App: "tsc",
								Args:    []string{"echo {toolCache}"},
								Scope:   config.ToolScopeRepository,
							},
						},
						{
							ToolName:  "eslint",
							Operation: config.OpLint,
							OpConfig: config.ToolOperation{
								App: "eslint",
								Args:    []string{"echo {toolCache}"},
								Scope:   config.ToolScopeRepository,
							},
						},
					},
				},
			},
		}

		results, err := executor.Execute(context.Background(), plan)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		if len(results[0].Results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results[0].Results))
		}

		tscOutput := strings.TrimSpace(results[0].Results[0].Output)
		eslintOutput := strings.TrimSpace(results[0].Results[1].Output)

		if tscOutput == eslintOutput {
			t.Errorf("different tools should have different cache paths, both got: %q", tscOutput)
		}
		if tscOutput != tscCache {
			t.Errorf("tsc cache path mismatch\ngot:  %q\nwant: %q", tscOutput, tscCache)
		}
		if eslintOutput != eslintCache {
			t.Errorf("eslint cache path mismatch\ngot:  %q\nwant: %q", eslintOutput, eslintCache)
		}
	})
}
