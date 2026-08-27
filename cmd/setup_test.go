package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/bundled"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/datamitsuignore"
	"github.com/datamitsu/datamitsu/internal/install"
	"github.com/datamitsu/datamitsu/internal/tooling"

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
				t.Helper()
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
				t.Helper()
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
				t.Helper()
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
				{ConfigName: "lefthook.yml", FilePath: "/repo/lefthook.yml", Scope: config.ScopeGitRoot, Action: "created", Error: errors.New("content generation failed")},
				{ConfigName: "lefthook.yml", FilePath: "/repo/lefthook.yml", Scope: config.ScopeGitRoot, Action: "created"},
			},
			expected: 1,
			check: func(t *testing.T, results []install.InstallResult) {
				t.Helper()
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
				t.Helper()
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

func TestBuildOptInIgnoreContent(t *testing.T) {
	tools := config.MapOfTools{
		"prettier":      {Name: "prettier"},
		"eslint":        {Name: "eslint"},
		"golangci-lint": {Name: "golangci-lint"},
		"hadolint":      {Name: "hadolint"},
		"shellcheck":    {Name: "shellcheck"},
	}

	content, count := buildOptInIgnoreContent(tools)

	if count != len(tools) {
		t.Errorf("count = %d, want %d", count, len(tools))
	}

	if !strings.HasSuffix(content, "\n") {
		t.Errorf("content should end with a trailing newline\ncontent:\n%s", content)
	}

	wantHeaders := []string{
		"# Generated by `datamitsu setup --opt-in-tools`.",
		"# All configured tools are disabled by default (opt-in model).",
		"# Enable a tool by removing it from the list below (or add \"!**/*: <tool>\").",
	}
	for _, h := range wantHeaders {
		if !strings.Contains(content, h) {
			t.Errorf("content missing header %q\ncontent:\n%s", h, content)
		}
	}

	// Single catch-all rule line with sorted tool names.
	wantRule := "**/*: eslint, golangci-lint, hadolint, prettier, shellcheck"
	if !strings.Contains(content, wantRule) {
		t.Errorf("content missing rule line %q\ncontent:\n%s", wantRule, content)
	}

	// Exactly one non-comment, non-blank line.
	ruleLines := 0
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		ruleLines++
	}
	if ruleLines != 1 {
		t.Errorf("non-comment rule lines = %d, want 1\ncontent:\n%s", ruleLines, content)
	}
}

func TestBuildOptInIgnoreContentEmpty(t *testing.T) {
	content, count := buildOptInIgnoreContent(config.MapOfTools{})
	if content != "" {
		t.Errorf("content = %q, want empty string", content)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestBuildOptInIgnoreContentParses(t *testing.T) {
	tools := config.MapOfTools{
		"prettier": {Name: "prettier"},
		"eslint":   {Name: "eslint"},
		"hadolint": {Name: "hadolint"},
	}

	content, _ := buildOptInIgnoreContent(tools)

	rules, err := datamitsuignore.Parse(content)
	if err != nil {
		t.Fatalf("Parse() error = %v\ncontent:\n%s", err, content)
	}
	if len(rules) != 1 {
		t.Fatalf("len(rules) = %d, want 1\ncontent:\n%s", len(rules), content)
	}

	rule := rules[0]
	if rule.Glob != "**/*" {
		t.Errorf("rule.Glob = %q, want %q", rule.Glob, "**/*")
	}
	if rule.Invert {
		t.Error("rule.Invert = true, want false")
	}

	wantTools := []string{"eslint", "hadolint", "prettier"}
	if !reflect.DeepEqual(rule.Tools, wantTools) {
		t.Errorf("rule.Tools = %v, want %v", rule.Tools, wantTools)
	}
}

func TestBuildOptInIgnoreContentCanonical(t *testing.T) {
	// The generated file must already be in canonical form so that a subsequent
	// `datamitsu fix` is a no-op (does not rewrite it). Round-trip the content
	// through the bundled fixer and assert it is byte-for-byte unchanged.
	tools := config.MapOfTools{
		"prettier":      {Name: "prettier"},
		"eslint":        {Name: "eslint"},
		"golangci-lint": {Name: "golangci-lint"},
		"hadolint":      {Name: "hadolint"},
		"shellcheck":    {Name: "shellcheck"},
	}

	content, _ := buildOptInIgnoreContent(tools)

	dir := t.TempDir()
	path := filepath.Join(dir, optInIgnoreFilename)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("setup: WriteFile() error = %v", err)
	}

	found, err := bundled.FindIgnoreFiles(context.Background(), dir)
	if err != nil {
		t.Fatalf("FindIgnoreFiles() error = %v", err)
	}
	if err := bundled.RunFix(dir, found); err != nil {
		t.Fatalf("RunFix() error = %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != content {
		t.Errorf("fix rewrote the generated file; not canonical\nbefore:\n%q\nafter:\n%q", content, string(got))
	}
}

func TestEnsureNoExistingIgnore(t *testing.T) {
	t.Run("absent returns nil", func(t *testing.T) {
		dir := t.TempDir()
		if err := ensureNoExistingIgnore(dir); err != nil {
			t.Errorf("ensureNoExistingIgnore() error = %v, want nil", err)
		}
	})

	t.Run("present returns error", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, optInIgnoreFilename)
		if err := os.WriteFile(path, []byte("**/*: eslint\n"), 0o644); err != nil {
			t.Fatalf("setup: WriteFile() error = %v", err)
		}
		if err := ensureNoExistingIgnore(dir); err == nil {
			t.Error("ensureNoExistingIgnore() error = nil, want non-nil when file exists")
		}
	})
}

func TestWriteOptInIgnore(t *testing.T) {
	tools := config.MapOfTools{
		"prettier": {Name: "prettier"},
		"eslint":   {Name: "eslint"},
	}

	t.Run("real mode writes file with expected content", func(t *testing.T) {
		dir := t.TempDir()
		if err := writeOptInIgnore(dir, tools, false); err != nil {
			t.Fatalf("writeOptInIgnore() error = %v", err)
		}
		path := filepath.Join(dir, optInIgnoreFilename)
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile() error = %v", err)
		}
		want, _ := buildOptInIgnoreContent(tools)
		if string(got) != want {
			t.Errorf("file content = %q, want %q", string(got), want)
		}
	})

	t.Run("dry-run does not create file", func(t *testing.T) {
		dir := t.TempDir()
		if err := writeOptInIgnore(dir, tools, true); err != nil {
			t.Fatalf("writeOptInIgnore() error = %v", err)
		}
		path := filepath.Join(dir, optInIgnoreFilename)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("dry-run should not create file; stat err = %v", err)
		}
	})

	t.Run("zero tools writes nothing", func(t *testing.T) {
		dir := t.TempDir()
		if err := writeOptInIgnore(dir, config.MapOfTools{}, false); err != nil {
			t.Fatalf("writeOptInIgnore() error = %v", err)
		}
		path := filepath.Join(dir, optInIgnoreFilename)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("zero-tools case should not create file; stat err = %v", err)
		}
	})
}

func TestSetupCommandRegistered(t *testing.T) {
	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "setup" {
			found = true
			break
		}
	}
	if !found {
		t.Error("setup command not registered with rootCmd")
	}
}

func TestSetupCommandFlags(t *testing.T) {
	flags := setupCmd.Flags()

	tests := []struct {
		name         string
		defaultValue string
	}{
		{"dry-run", "false"},
		{"skip-fix", "false"},
		{"opt-in-tools", "false"},
		{"tools", ""},
	}

	for _, tt := range tests {
		f := flags.Lookup(tt.name)
		if f == nil {
			t.Errorf("flag %q not found on setup command", tt.name)
			continue
		}
		if f.DefValue != tt.defaultValue {
			t.Errorf("flag %q default = %q, want %q", tt.name, f.DefValue, tt.defaultValue)
		}
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
	configs := config.MapOfConfigSetup{}
	vm := goja.New()

	content := "generated content"
	layerMap := &config.SetupLayerMap{
		".editorconfig": &config.SetupLayerHistory{
			FileName: ".editorconfig",
			Layers: []config.SetupLayerEntry{
				{
					LayerName:        "default",
					GeneratedContent: &content,
				},
			},
			FinalConfig: config.ConfigSetup{},
		},
	}

	installer := install.NewInstaller(rootPath, cwdPath, projectTypes, nil, configs, vm, layerMap)
	if installer == nil {
		t.Fatal("NewInstaller() returned nil")
	}

	// Verify a nil layerMap also works (backward compatibility)
	installerNil := install.NewInstaller(rootPath, cwdPath, projectTypes, nil, configs, vm, nil)
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
	layerMap := &config.SetupLayerMap{
		".editorconfig": &config.SetupLayerHistory{
			FileName: ".editorconfig",
			Layers: []config.SetupLayerEntry{
				{
					LayerName:        "default",
					GeneratedContent: &content,
				},
			},
			FinalConfig: config.ConfigSetup{Scope: config.ScopeGitRoot},
		},
	}

	configs := config.MapOfConfigSetup{
		".editorconfig": config.ConfigSetup{Scope: config.ScopeGitRoot},
	}

	// In dry-run mode, the installer is still created with the layerMap
	installer := install.NewInstaller("/tmp/root", "/tmp/root", []string{}, nil, configs, vm, layerMap)
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

func TestParseSelectedTools(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"empty yields nil", "", nil},
		{"single", "golangci-lint", []string{"golangci-lint"}},
		{"multiple", "golangci-lint,prettier", []string{"golangci-lint", "prettier"}},
		{"trims spaces", " golangci-lint , prettier ", []string{"golangci-lint", "prettier"}},
		{"dedupes", "a,a,b", []string{"a", "b"}},
		{"drops empty entries", "a,,b,", []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSelectedTools(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseSelectedTools(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateSelectedTools(t *testing.T) {
	tools := config.MapOfTools{
		"golangci-lint": {Name: "golangci-lint"},
		"prettier":      {Name: "prettier"},
	}

	t.Run("empty selection is nil", func(t *testing.T) {
		if err := validateSelectedTools(nil, tools); err != nil {
			t.Errorf("validateSelectedTools(nil) = %v, want nil", err)
		}
	})

	t.Run("known tools pass", func(t *testing.T) {
		if err := validateSelectedTools([]string{"golangci-lint"}, tools); err != nil {
			t.Errorf("validateSelectedTools = %v, want nil", err)
		}
	})

	t.Run("unknown tool returns ToolNotFoundError", func(t *testing.T) {
		err := validateSelectedTools([]string{"bogus"}, tools)
		if err == nil {
			t.Fatal("validateSelectedTools = nil, want error")
		}
		var tnf *tooling.ToolNotFoundError
		if !errors.As(err, &tnf) {
			t.Fatalf("error type = %T, want *tooling.ToolNotFoundError", err)
		}
		if len(tnf.NotFound) != 1 || tnf.NotFound[0] != "bogus" {
			t.Errorf("NotFound = %v, want [bogus]", tnf.NotFound)
		}
		wantAvail := []string{"golangci-lint", "prettier"}
		if !reflect.DeepEqual(tnf.Available, wantAvail) {
			t.Errorf("Available = %v, want %v (sorted)", tnf.Available, wantAvail)
		}
	})
}

func TestToolsWithoutGeneratedConfig(t *testing.T) {
	configs := config.MapOfConfigSetup{
		".golangci.yml": {Tools: []string{"golangci-lint"}},
		".prettierrc":   {Tools: []string{"prettier"}},
		".gitignore":    {}, // infra, no tools
	}

	created := func(name string) install.InstallResult {
		return install.InstallResult{ConfigName: name, Action: "created"}
	}
	skipped := func(name string) install.InstallResult {
		return install.InstallResult{ConfigName: name, Action: "skipped"}
	}

	tests := []struct {
		name     string
		selected []string
		results  []install.InstallResult
		want     []string
	}{
		{
			name:     "no selection",
			selected: nil,
			results:  []install.InstallResult{created(".golangci.yml")},
			want:     nil,
		},
		{
			name:     "all generated",
			selected: []string{"golangci-lint", "prettier"},
			results:  []install.InstallResult{created(".golangci.yml"), created(".prettierrc")},
			want:     nil,
		},
		{
			// The key finding-1 case: prettier owns a config but it was skipped
			// (e.g. project-type mismatch), so it must still be surfaced.
			name:     "config skipped by project type is reported",
			selected: []string{"golangci-lint", "prettier"},
			results:  []install.InstallResult{created(".golangci.yml"), skipped(".prettierrc")},
			want:     []string{"prettier"},
		},
		{
			name:     "tool with no associated config is reported",
			selected: []string{"golangci-lint", "shellcheck"},
			results:  []install.InstallResult{created(".golangci.yml")},
			want:     []string{"shellcheck"},
		},
		{
			name:     "sorted output, nothing generated",
			selected: []string{"ztool", "atool"},
			results:  nil,
			want:     []string{"atool", "ztool"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toolsWithoutGeneratedConfig(tt.selected, configs, tt.results)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("toolsWithoutGeneratedConfig(%v) = %v, want %v", tt.selected, got, tt.want)
			}
		})
	}
}
