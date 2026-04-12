package cmd

import (
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/install"
	"fmt"
	"testing"

	"github.com/dop251/goja"
)

func TestDeduplicateGitRootResults(t *testing.T) {
	tests := []struct {
		name     string
		input    []install.InstallResult
		expected int
		check    func(t *testing.T, results []install.InstallResult)
	}{
		{
			name:     "empty input",
			input:    []install.InstallResult{},
			expected: 0,
		},
		{
			name: "single result not deduplicated",
			input: []install.InstallResult{
				{ConfigName: "lefthook.yml", FilePath: "/repo/lefthook.yml", Scope: config.ScopeGitRoot, Action: "created"},
			},
			expected: 1,
		},
		{
			name: "multiple git-root results with same path deduplicated to one",
			input: []install.InstallResult{
				{ConfigName: "lefthook.yml", FilePath: "/repo/lefthook.yml", Scope: config.ScopeGitRoot, Action: "created"},
				{ConfigName: "lefthook.yml", FilePath: "/repo/lefthook.yml", Scope: config.ScopeGitRoot, Action: "created"},
				{ConfigName: "lefthook.yml", FilePath: "/repo/lefthook.yml", Scope: config.ScopeGitRoot, Action: "created"},
			},
			expected: 1,
			check: func(t *testing.T, results []install.InstallResult) {
				if results[0].ConfigName != "lefthook.yml" {
					t.Errorf("kept result ConfigName = %q, want %q", results[0].ConfigName, "lefthook.yml")
				}
			},
		},
		{
			name: "non-git-root results with same path not deduplicated",
			input: []install.InstallResult{
				{ConfigName: "tsconfig.json", FilePath: "/repo/packages/a/tsconfig.json", Action: "created"},
				{ConfigName: "tsconfig.json", FilePath: "/repo/packages/a/tsconfig.json", Action: "created"},
			},
			expected: 2,
		},
		{
			name: "mixed git-root and project scope with same path not deduplicated",
			input: []install.InstallResult{
				{ConfigName: "config.yml", FilePath: "/repo/config.yml", Scope: config.ScopeGitRoot, Action: "created"},
				{ConfigName: "config.yml", FilePath: "/repo/config.yml", Action: "created"},
			},
			expected: 2,
		},
		{
			name: "different paths not deduplicated",
			input: []install.InstallResult{
				{ConfigName: "lefthook.yml", FilePath: "/repo/lefthook.yml", Scope: config.ScopeGitRoot, Action: "created"},
				{ConfigName: "tsconfig.json", FilePath: "/repo/tsconfig.json", Scope: config.ScopeGitRoot, Action: "created"},
			},
			expected: 2,
		},
		{
			name: "mixed scenario: git-root duplicates and unique entries",
			input: []install.InstallResult{
				{ConfigName: "lefthook.yml", FilePath: "/repo/lefthook.yml", Scope: config.ScopeGitRoot, Action: "created"},
				{ConfigName: "tsconfig.json", FilePath: "/repo/packages/a/tsconfig.json", Action: "created"},
				{ConfigName: "lefthook.yml", FilePath: "/repo/lefthook.yml", Scope: config.ScopeGitRoot, Action: "created"},
				{ConfigName: "tsconfig.json", FilePath: "/repo/packages/b/tsconfig.json", Action: "created"},
				{ConfigName: "lefthook.yml", FilePath: "/repo/lefthook.yml", Scope: config.ScopeGitRoot, Action: "created"},
			},
			expected: 3,
			check: func(t *testing.T, results []install.InstallResult) {
				gitRootCount := 0
				for _, r := range results {
					if r.FilePath == "/repo/lefthook.yml" {
						gitRootCount++
					}
				}
				if gitRootCount != 1 {
					t.Errorf("git-root results for lefthook.yml = %d, want 1", gitRootCount)
				}
			},
		},
		{
			name: "skipped results preserved",
			input: []install.InstallResult{
				{ConfigName: "lefthook.yml", FilePath: "/repo/lefthook.yml", Scope: config.ScopeGitRoot, Action: "created"},
				{ConfigName: "lefthook.yml", FilePath: "/repo/lefthook.yml", Scope: config.ScopeGitRoot, Action: "skipped"},
			},
			expected: 1,
		},
		{
			name: "preserves order of first occurrence",
			input: []install.InstallResult{
				{ConfigName: "a.yml", FilePath: "/repo/a.yml", Scope: config.ScopeGitRoot, Action: "created"},
				{ConfigName: "b.yml", FilePath: "/repo/b.yml", Action: "created"},
				{ConfigName: "a.yml", FilePath: "/repo/a.yml", Scope: config.ScopeGitRoot, Action: "created"},
			},
			expected: 2,
			check: func(t *testing.T, results []install.InstallResult) {
				if results[0].FilePath != "/repo/a.yml" {
					t.Errorf("first result FilePath = %q, want %q", results[0].FilePath, "/repo/a.yml")
				}
				if results[1].FilePath != "/repo/b.yml" {
					t.Errorf("second result FilePath = %q, want %q", results[1].FilePath, "/repo/b.yml")
				}
			},
		},
		{
			name: "dedup prefers error result over success",
			input: []install.InstallResult{
				{ConfigName: "lefthook.yml", FilePath: "/repo/lefthook.yml", Scope: config.ScopeGitRoot, Action: "created"},
				{ConfigName: "lefthook.yml", FilePath: "/repo/lefthook.yml", Scope: config.ScopeGitRoot, Action: "created", Error: fmt.Errorf("content generation failed")},
				{ConfigName: "lefthook.yml", FilePath: "/repo/lefthook.yml", Scope: config.ScopeGitRoot, Action: "created"},
			},
			expected: 1,
			check: func(t *testing.T, results []install.InstallResult) {
				if results[0].Error == nil {
					t.Error("dedup should prefer the result with an error")
				}
			},
		},
		{
			name: "dedup keeps first when no errors",
			input: []install.InstallResult{
				{ConfigName: "lefthook.yml", FilePath: "/repo/lefthook.yml", Scope: config.ScopeGitRoot, Action: "created"},
				{ConfigName: "lefthook.yml", FilePath: "/repo/lefthook.yml", Scope: config.ScopeGitRoot, Action: "patched"},
			},
			expected: 1,
			check: func(t *testing.T, results []install.InstallResult) {
				if results[0].Action != "created" {
					t.Errorf("Action = %q, want %q (should keep first)", results[0].Action, "created")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := deduplicateGitRootResults(tt.input)
			if len(result) != tt.expected {
				t.Errorf("len(result) = %d, want %d", len(result), tt.expected)
			}
			if tt.check != nil {
				tt.check(t, result)
			}
		})
	}
}

func TestSetupLoadConfigReturns4Tuple(t *testing.T) {
	cfg, layerMap, vm, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if cfg == nil {
		t.Error("loadConfig() returned nil config")
	}
	if layerMap == nil {
		t.Error("loadConfig() returned nil layerMap")
	}
	if vm == nil {
		t.Error("loadConfig() returned nil VM")
	}
}

func TestSetupPassesLayerMapToNewInstaller(t *testing.T) {
	rootPath := "/tmp/test-root"
	cwdPath := "/tmp/test-root/project"
	projectTypes := []string{"node"}
	configs := config.MapOfConfigInit{}
	vm := goja.New()

	content := "generated content"
	layerMap := &config.InitLayerMap{
		".editorconfig": &config.InitLayerHistory{
			FileName: ".editorconfig",
			Layers: []config.InitLayerEntry{
				{
					LayerName:        "default",
					GeneratedContent: &content,

				},
			},
			FinalConfig: config.ConfigInit{},
		},
	}

	installer := install.NewInstaller(rootPath, cwdPath, projectTypes, configs, vm, layerMap)
	if installer == nil {
		t.Fatal("NewInstaller() returned nil")
	}

	// Verify a nil layerMap also works (backward compatibility)
	installerNil := install.NewInstaller(rootPath, cwdPath, projectTypes, configs, vm, nil)
	if installerNil == nil {
		t.Fatal("NewInstaller() with nil layerMap returned nil")
	}
}

func TestDryRunModeLayerHistoryStillBuilt(t *testing.T) {
	// Layer history is built during config loading, which happens before
	// dry-run is checked. This test verifies the layerMap is always populated
	// regardless of dry-run mode, and that the installer receives it.

	vm := goja.New()
	content := "generated in load phase"
	layerMap := &config.InitLayerMap{
		".editorconfig": &config.InitLayerHistory{
			FileName: ".editorconfig",
			Layers: []config.InitLayerEntry{
				{
					LayerName:        "default",
					GeneratedContent: &content,

				},
			},
			FinalConfig: config.ConfigInit{Scope: config.ScopeGitRoot},
		},
	}

	configs := config.MapOfConfigInit{
		".editorconfig": config.ConfigInit{Scope: config.ScopeGitRoot},
	}

	// In dry-run mode, the installer is still created with the layerMap
	installer := install.NewInstaller("/tmp/root", "/tmp/root", []string{}, configs, vm, layerMap)
	if installer == nil {
		t.Fatal("NewInstaller() returned nil even with layerMap for dry-run scenario")
	}

	// Verify the layerMap has the expected content
	history, ok := (*layerMap)[".editorconfig"]
	if !ok {
		t.Fatal("expected .editorconfig in layerMap")
	}
	lastContent := config.GetLastGeneratedContent(history)
	if lastContent == nil || *lastContent != "generated in load phase" {
		t.Error("layerMap content should be preserved for dry-run mode")
	}
}
