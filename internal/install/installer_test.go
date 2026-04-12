package install

import (
	"context"
	"github.com/datamitsu/datamitsu/internal/config"
	"os"
	"path/filepath"
	"testing"

	"github.com/dop251/goja"
)

func TestNewInstaller(t *testing.T) {
	rootPath := "/tmp/root"
	cwdPath := "/tmp/root/project"
	projectTypes := []string{"node", "go"}
	configs := config.MapOfConfigInit{}
	vm := goja.New()

	installer := NewInstaller(rootPath, cwdPath, projectTypes, configs, vm, nil)

	if installer == nil {
		t.Fatal("NewInstaller() returned nil")
	}

	if installer.rootPath != rootPath {
		t.Errorf("rootPath = %q, want %q", installer.rootPath, rootPath)
	}

	if installer.cwdPath != cwdPath {
		t.Errorf("cwdPath = %q, want %q", installer.cwdPath, cwdPath)
	}

	if len(installer.projectTypes) != 2 {
		t.Errorf("len(projectTypes) = %d, want 2", len(installer.projectTypes))
	}
}

func TestIsApplicable(t *testing.T) {
	installer := &Installer{
		projectTypes: []string{"node", "go"},
	}

	tests := []struct {
		name     string
		cfg      config.ConfigInit
		expected bool
	}{
		{
			name:     "no project types specified",
			cfg:      config.ConfigInit{},
			expected: true,
		},
		{
			name: "matching project type",
			cfg: config.ConfigInit{
				ProjectTypes: []string{"node"},
			},
			expected: true,
		},
		{
			name: "non-matching project type",
			cfg: config.ConfigInit{
				ProjectTypes: []string{"rust"},
			},
			expected: false,
		},
		{
			name: "one matching, one non-matching",
			cfg: config.ConfigInit{
				ProjectTypes: []string{"node", "rust"},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := installer.isApplicable(tt.cfg)
			if result != tt.expected {
				t.Errorf("isApplicable() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestFileExists(t *testing.T) {
	tmpDir := t.TempDir()
	installer := &Installer{}

	t.Run("file exists", func(t *testing.T) {
		filePath := filepath.Join(tmpDir, "test.txt")
		if err := os.WriteFile(filePath, []byte("test"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		if !installer.fileExists(filePath) {
			t.Error("fileExists() = false, want true")
		}
	})

	t.Run("file does not exist", func(t *testing.T) {
		filePath := filepath.Join(tmpDir, "nonexistent.txt")

		if installer.fileExists(filePath) {
			t.Error("fileExists() = true, want false")
		}
	})
}

func TestInstallConfigSkipped(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	installer := NewInstaller(tmpDir, tmpDir, []string{"node"}, config.MapOfConfigInit{}, vm, nil)

	cfg := config.ConfigInit{
		ProjectTypes: []string{"rust"},
	}

	ctx := context.Background()
	result := installer.installConfig(ctx, "test.yml", cfg, false)

	if result.Action != "skipped" {
		t.Errorf("Action = %q, want %q", result.Action, "skipped")
	}
}

func TestInstallConfigDeleteOnly(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	altFile := filepath.Join(tmpDir, "alt.yml")
	if err := os.WriteFile(altFile, []byte("old"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	installer := NewInstaller(tmpDir, tmpDir, []string{}, config.MapOfConfigInit{}, vm, nil)

	cfg := config.ConfigInit{
		OtherFileNameList: []string{"alt.yml"},
		DeleteOnly:        true,
	}

	ctx := context.Background()
	result := installer.installConfig(ctx, "main.yml", cfg, false)

	if result.Action != "deleted" {
		t.Errorf("Action = %q, want %q", result.Action, "deleted")
	}

	if len(result.DeletedFiles) != 1 {
		t.Errorf("len(DeletedFiles) = %d, want 1", len(result.DeletedFiles))
	}

	if installer.fileExists(altFile) {
		t.Error("alt file should be deleted")
	}
}

func TestInstallConfigCreated(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	_ = vm.Set("contentFunc", func(ctx goja.Value) string {
		return "new content"
	})

	contentFunc := vm.Get("contentFunc")

	installer := NewInstaller(tmpDir, tmpDir, []string{}, config.MapOfConfigInit{}, vm, nil)

	cfg := config.ConfigInit{
		Content: contentFunc,
	}

	ctx := context.Background()
	result := installer.installConfig(ctx, "test.yml", cfg, false)

	if result.Error != nil {
		t.Fatalf("installConfig() error = %v", result.Error)
	}

	if result.Action != "created" {
		t.Errorf("Action = %q, want %q", result.Action, "created")
	}

	filePath := filepath.Join(tmpDir, "test.yml")
	if !installer.fileExists(filePath) {
		t.Error("file should be created")
	}

	content, _ := os.ReadFile(filePath)
	if string(content) != "new content" {
		t.Errorf("content = %q, want %q", string(content), "new content")
	}
}

func TestInstallConfigDryRun(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	_ = vm.Set("contentFunc", func(ctx goja.Value) string {
		return "new content"
	})

	contentFunc := vm.Get("contentFunc")

	installer := NewInstaller(tmpDir, tmpDir, []string{}, config.MapOfConfigInit{}, vm, nil)

	cfg := config.ConfigInit{
		Content: contentFunc,
	}

	ctx := context.Background()
	result := installer.installConfig(ctx, "test.yml", cfg, true)

	if result.Error != nil {
		t.Fatalf("installConfig() error = %v", result.Error)
	}

	if result.Action != "created" {
		t.Errorf("Action = %q, want %q", result.Action, "created")
	}

	filePath := filepath.Join(tmpDir, "test.yml")
	if installer.fileExists(filePath) {
		t.Error("file should not be created in dry run mode")
	}
}

func TestInstallConfigScopeGitRootSkipsWhenNotAtRoot(t *testing.T) {
	tmpDir := t.TempDir()
	cwdDir := filepath.Join(tmpDir, "subdir")
	_ = os.MkdirAll(cwdDir, 0755)

	vm := goja.New()

	_ = vm.Set("contentFunc", func(ctx goja.Value) string {
		return "content"
	})

	contentFunc := vm.Get("contentFunc")

	installer := NewInstaller(tmpDir, cwdDir, []string{}, config.MapOfConfigInit{}, vm, nil)

	cfg := config.ConfigInit{
		Scope:   config.ScopeGitRoot,
		Content: contentFunc,
	}

	ctx := context.Background()
	result := installer.installConfig(ctx, "test.yml", cfg, false)

	if result.Action != "skipped" {
		t.Errorf("Action = %q, want %q", result.Action, "skipped")
	}

	rootFilePath := filepath.Join(tmpDir, "test.yml")
	if installer.fileExists(rootFilePath) {
		t.Error("file should not be created when cwdPath != rootPath")
	}
}

func TestInstallConfigScopeGitRootRunsAtRoot(t *testing.T) {
	tmpDir := t.TempDir()

	vm := goja.New()

	_ = vm.Set("contentFunc", func(ctx goja.Value) string {
		return "content"
	})

	contentFunc := vm.Get("contentFunc")

	installer := NewInstaller(tmpDir, tmpDir, []string{}, config.MapOfConfigInit{}, vm, nil)

	cfg := config.ConfigInit{
		Scope:   config.ScopeGitRoot,
		Content: contentFunc,
	}

	ctx := context.Background()
	result := installer.installConfig(ctx, "test.yml", cfg, false)

	if result.Error != nil {
		t.Fatalf("installConfig() error = %v", result.Error)
	}

	if result.Action != "created" {
		t.Errorf("Action = %q, want %q", result.Action, "created")
	}

	rootFilePath := filepath.Join(tmpDir, "test.yml")
	if !installer.fileExists(rootFilePath) {
		t.Error("file should be created at root when cwdPath == rootPath")
	}
}

func TestInstallConfigScopeGitRootWithProjectTypesSkipsWhenTypeNotMatched(t *testing.T) {
	tmpDir := t.TempDir()

	vm := goja.New()

	_ = vm.Set("contentFunc", func(ctx goja.Value) string {
		return "content"
	})

	contentFunc := vm.Get("contentFunc")

	installer := NewInstaller(tmpDir, tmpDir, []string{"node"}, config.MapOfConfigInit{}, vm, nil)

	cfg := config.ConfigInit{
		Scope:        config.ScopeGitRoot,
		ProjectTypes: []string{"rust"},
		Content:      contentFunc,
	}

	ctx := context.Background()
	result := installer.installConfig(ctx, "test.yml", cfg, false)

	if result.Action != "skipped" {
		t.Errorf("Action = %q, want %q", result.Action, "skipped")
	}
}

func TestInstallConfigDeletesAlternatives(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	alt1 := filepath.Join(tmpDir, "alt1.yml")
	alt2 := filepath.Join(tmpDir, "alt2.yml")
	if err := os.WriteFile(alt1, []byte("old"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	if err := os.WriteFile(alt2, []byte("old"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	_ = vm.Set("contentFunc", func(ctx goja.Value) string {
		return "new content"
	})

	contentFunc := vm.Get("contentFunc")

	installer := NewInstaller(tmpDir, tmpDir, []string{}, config.MapOfConfigInit{}, vm, nil)

	cfg := config.ConfigInit{
		OtherFileNameList: []string{"alt1.yml", "alt2.yml"},
		Content:           contentFunc,
	}

	ctx := context.Background()
	result := installer.installConfig(ctx, "main.yml", cfg, false)

	if result.Error != nil {
		t.Fatalf("installConfig() error = %v", result.Error)
	}

	if len(result.DeletedFiles) != 2 {
		t.Errorf("len(DeletedFiles) = %d, want 2", len(result.DeletedFiles))
	}

	if installer.fileExists(alt1) || installer.fileExists(alt2) {
		t.Error("alternative files should be deleted")
	}
}

func TestInstallAll(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	_ = vm.Set("contentFunc", func(ctx goja.Value) string {
		return "content"
	})

	contentFunc := vm.Get("contentFunc")

	configs := config.MapOfConfigInit{
		"config1.yml": {
			Content: contentFunc,
		},
		"config2.yml": {
			Content: contentFunc,
		},
	}

	installer := NewInstaller(tmpDir, tmpDir, []string{}, configs, vm, nil)

	ctx := context.Background()
	results, err := installer.InstallAll(ctx, false)

	if err != nil {
		t.Fatalf("InstallAll() error = %v", err)
	}

	if len(results) != 2 {
		t.Errorf("len(results) = %d, want 2", len(results))
	}

	for _, result := range results {
		if result.Error != nil {
			t.Errorf("result for %s has error: %v", result.ConfigName, result.Error)
		}
	}
}

func TestInstallSymlinkSetsLinkTarget(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	if err := os.WriteFile(filepath.Join(tmpDir, "AGENTS.md"), []byte("target"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	installer := NewInstaller(tmpDir, tmpDir, []string{}, config.MapOfConfigInit{}, vm, nil)

	tests := []struct {
		name           string
		configName     string
		linkTarget     string
		expectedTarget string
		dryRun         bool
	}{
		{
			name:           "same-dir link target",
			configName:     "CLAUDE.md",
			linkTarget:     "AGENTS.md",
			expectedTarget: "AGENTS.md",
		},
		{
			name:           "parent-dir link target for nested config",
			configName:     ".cursor/rules",
			linkTarget:     "../AGENTS.md",
			expectedTarget: "../AGENTS.md",
		},
		{
			name:           "link target set in dry run",
			configName:     "GEMINI.md",
			linkTarget:     "AGENTS.md",
			expectedTarget: "AGENTS.md",
			dryRun:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.ConfigInit{
				Scope: config.ScopeGitRoot,
				LinkTarget: tt.linkTarget,
			}

			ctx := context.Background()
			result := installer.installConfig(ctx, tt.configName, cfg, tt.dryRun)

			if result.Error != nil {
				t.Fatalf("installConfig() error = %v", result.Error)
			}

			if result.Action != "linked" {
				t.Errorf("Action = %q, want %q", result.Action, "linked")
			}

			if result.LinkTarget != tt.expectedTarget {
				t.Errorf("LinkTarget = %q, want %q", result.LinkTarget, tt.expectedTarget)
			}
		})
	}
}

func TestInstallConfigLinkTargetCreatesSymlink(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	// Create the target file that the symlink will point to
	targetPath := filepath.Join(tmpDir, "AGENTS.md")
	if err := os.WriteFile(targetPath, []byte("# Agents"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	installer := NewInstaller(tmpDir, tmpDir, []string{}, config.MapOfConfigInit{}, vm, nil)

	cfg := config.ConfigInit{
		Scope: config.ScopeGitRoot,
		LinkTarget: "AGENTS.md",
	}

	ctx := context.Background()
	result := installer.installConfig(ctx, "CLAUDE.md", cfg, false)

	if result.Error != nil {
		t.Fatalf("installConfig() error = %v", result.Error)
	}

	if result.Action != "linked" {
		t.Errorf("Action = %q, want %q", result.Action, "linked")
	}

	symlinkPath := filepath.Join(tmpDir, "CLAUDE.md")
	linkDest, err := os.Readlink(symlinkPath)
	if err != nil {
		t.Fatalf("Readlink() error = %v", err)
	}

	if linkDest != "AGENTS.md" {
		t.Errorf("symlink target = %q, want %q", linkDest, "AGENTS.md")
	}
}

func TestInstallConfigLinkTargetUpdatesStaleSymlink(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	// Create old target and new target
	if err := os.WriteFile(filepath.Join(tmpDir, "OLD.md"), []byte("old"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "AGENTS.md"), []byte("new"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Create an existing stale symlink pointing to old target
	symlinkPath := filepath.Join(tmpDir, "CLAUDE.md")
	_ = os.Symlink("OLD.md", symlinkPath)

	installer := NewInstaller(tmpDir, tmpDir, []string{}, config.MapOfConfigInit{}, vm, nil)

	cfg := config.ConfigInit{
		Scope: config.ScopeGitRoot,
		LinkTarget: "AGENTS.md",
	}

	ctx := context.Background()
	result := installer.installConfig(ctx, "CLAUDE.md", cfg, false)

	if result.Error != nil {
		t.Fatalf("installConfig() error = %v", result.Error)
	}

	if result.Action != "linked" {
		t.Errorf("Action = %q, want %q", result.Action, "linked")
	}

	linkDest, err := os.Readlink(symlinkPath)
	if err != nil {
		t.Fatalf("Readlink() error = %v", err)
	}

	if linkDest != "AGENTS.md" {
		t.Errorf("symlink target = %q, want %q", linkDest, "AGENTS.md")
	}
}

func TestInstallConfigLinkTargetReplacesRegularFile(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	// Create a regular file that will be replaced by symlink
	symlinkPath := filepath.Join(tmpDir, "CLAUDE.md")
	if err := os.WriteFile(symlinkPath, []byte("regular file"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "AGENTS.md"), []byte("target"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	installer := NewInstaller(tmpDir, tmpDir, []string{}, config.MapOfConfigInit{}, vm, nil)

	cfg := config.ConfigInit{
		Scope: config.ScopeGitRoot,
		LinkTarget: "AGENTS.md",
	}

	ctx := context.Background()
	result := installer.installConfig(ctx, "CLAUDE.md", cfg, false)

	if result.Error != nil {
		t.Fatalf("installConfig() error = %v", result.Error)
	}

	if result.Action != "linked" {
		t.Errorf("Action = %q, want %q", result.Action, "linked")
	}

	info, err := os.Lstat(symlinkPath)
	if err != nil {
		t.Fatalf("Lstat() error = %v", err)
	}

	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("expected symlink, got regular file")
	}
}

func TestInstallConfigLinkTargetIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	if err := os.WriteFile(filepath.Join(tmpDir, "AGENTS.md"), []byte("target"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	installer := NewInstaller(tmpDir, tmpDir, []string{}, config.MapOfConfigInit{}, vm, nil)

	cfg := config.ConfigInit{
		Scope: config.ScopeGitRoot,
		LinkTarget: "AGENTS.md",
	}

	ctx := context.Background()

	// Run twice - should be idempotent
	result1 := installer.installConfig(ctx, "CLAUDE.md", cfg, false)
	if result1.Error != nil {
		t.Fatalf("first installConfig() error = %v", result1.Error)
	}

	result2 := installer.installConfig(ctx, "CLAUDE.md", cfg, false)
	if result2.Error != nil {
		t.Fatalf("second installConfig() error = %v", result2.Error)
	}

	if result2.Action != "linked" {
		t.Errorf("Action = %q, want %q", result2.Action, "linked")
	}

	symlinkPath := filepath.Join(tmpDir, "CLAUDE.md")
	linkDest, err := os.Readlink(symlinkPath)
	if err != nil {
		t.Fatalf("Readlink() error = %v", err)
	}

	if linkDest != "AGENTS.md" {
		t.Errorf("symlink target = %q, want %q", linkDest, "AGENTS.md")
	}
}

func TestInstallConfigLinkTargetDryRun(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	installer := NewInstaller(tmpDir, tmpDir, []string{}, config.MapOfConfigInit{}, vm, nil)

	cfg := config.ConfigInit{
		Scope: config.ScopeGitRoot,
		LinkTarget: "AGENTS.md",
	}

	ctx := context.Background()
	result := installer.installConfig(ctx, "CLAUDE.md", cfg, true)

	if result.Error != nil {
		t.Fatalf("installConfig() error = %v", result.Error)
	}

	if result.Action != "linked" {
		t.Errorf("Action = %q, want %q", result.Action, "linked")
	}

	symlinkPath := filepath.Join(tmpDir, "CLAUDE.md")
	if installer.fileExists(symlinkPath) {
		t.Error("symlink should not be created in dry run mode")
	}
}

func TestInstallConfigLinkTargetNestedPath(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	if err := os.WriteFile(filepath.Join(tmpDir, "AGENTS.md"), []byte("target"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	installer := NewInstaller(tmpDir, tmpDir, []string{}, config.MapOfConfigInit{}, vm, nil)

	// Symlink in a subdirectory pointing to parent
	cfg := config.ConfigInit{
		Scope: config.ScopeGitRoot,
		LinkTarget: "../AGENTS.md",
	}

	ctx := context.Background()
	result := installer.installConfig(ctx, ".cursor/rules", cfg, false)

	if result.Error != nil {
		t.Fatalf("installConfig() error = %v", result.Error)
	}

	if result.Action != "linked" {
		t.Errorf("Action = %q, want %q", result.Action, "linked")
	}

	symlinkPath := filepath.Join(tmpDir, ".cursor", "rules")
	linkDest, err := os.Readlink(symlinkPath)
	if err != nil {
		t.Fatalf("Readlink() error = %v", err)
	}

	if linkDest != "../AGENTS.md" {
		t.Errorf("symlink target = %q, want %q", linkDest, "../AGENTS.md")
	}
}

func TestInstallConfigLinkTargetIgnoresContentAndDeleteOnly(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	if err := os.WriteFile(filepath.Join(tmpDir, "AGENTS.md"), []byte("target"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Set up a content function that should NOT be called
	_ = vm.Set("contentFunc", func(ctx goja.Value) string {
		return "should not be written"
	})
	contentFunc := vm.Get("contentFunc")

	installer := NewInstaller(tmpDir, tmpDir, []string{}, config.MapOfConfigInit{}, vm, nil)

	cfg := config.ConfigInit{
		Scope: config.ScopeGitRoot,
		LinkTarget: "AGENTS.md",
		Content:    contentFunc,
		DeleteOnly: true,
	}

	ctx := context.Background()
	result := installer.installConfig(ctx, "CLAUDE.md", cfg, false)

	if result.Error != nil {
		t.Fatalf("installConfig() error = %v", result.Error)
	}

	if result.Action != "linked" {
		t.Errorf("Action = %q, want %q", result.Action, "linked")
	}

	// Verify it's a symlink, not a regular file with content
	symlinkPath := filepath.Join(tmpDir, "CLAUDE.md")
	info, err := os.Lstat(symlinkPath)
	if err != nil {
		t.Fatalf("Lstat() error = %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("expected symlink, got regular file")
	}
}

func TestInstallAllAgentsMDAndSymlinks(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	// Content function for AGENTS.md: preserve existing or create stub
	_ = vm.Set("agentsContentFunc", func(ctx goja.Value) string {
		obj := ctx.ToObject(vm)
		existing := obj.Get("existingContent")
		if existing != nil && !goja.IsUndefined(existing) && !goja.IsNull(existing) {
			return existing.String()
		}
		return "# Agents\n\nThis file contains instructions for AI coding assistants.\n"
	})
	agentsContentFunc := vm.Get("agentsContentFunc")

	configs := config.MapOfConfigInit{
		"AGENTS.md": {
			Content:  agentsContentFunc,
			Scope: config.ScopeGitRoot,
		},
		"CLAUDE.md": {
			LinkTarget: "AGENTS.md",
			Scope: config.ScopeGitRoot,
		},
		"GEMINI.md": {
			LinkTarget: "AGENTS.md",
			Scope: config.ScopeGitRoot,
		},
		".cursorrules": {
			LinkTarget: "AGENTS.md",
			Scope: config.ScopeGitRoot,
		},
		".cursor/rules": {
			LinkTarget: "../AGENTS.md",
			Scope: config.ScopeGitRoot,
		},
		".windsurfrules": {
			LinkTarget: "AGENTS.md",
			Scope: config.ScopeGitRoot,
		},
		".github/copilot-instructions.md": {
			LinkTarget: "../AGENTS.md",
			Scope: config.ScopeGitRoot,
		},
	}

	installer := NewInstaller(tmpDir, tmpDir, []string{}, configs, vm, nil)

	ctx := context.Background()
	results, err := installer.InstallAll(ctx, false)
	if err != nil {
		t.Fatalf("InstallAll() error = %v", err)
	}

	if len(results) != 7 {
		t.Errorf("len(results) = %d, want 7", len(results))
	}

	for _, result := range results {
		if result.Error != nil {
			t.Errorf("result for %s has error: %v", result.ConfigName, result.Error)
		}
	}

	// Verify AGENTS.md was created with stub content
	agentsPath := filepath.Join(tmpDir, "AGENTS.md")
	content, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatalf("ReadFile(AGENTS.md) error = %v", err)
	}
	expectedContent := "# Agents\n\nThis file contains instructions for AI coding assistants.\n"
	if string(content) != expectedContent {
		t.Errorf("AGENTS.md content = %q, want %q", string(content), expectedContent)
	}

	// Verify root-level symlinks point to AGENTS.md
	rootSymlinks := []string{"CLAUDE.md", "GEMINI.md", ".cursorrules", ".windsurfrules"}
	for _, name := range rootSymlinks {
		symlinkPath := filepath.Join(tmpDir, name)
		linkDest, err := os.Readlink(symlinkPath)
		if err != nil {
			t.Fatalf("Readlink(%s) error = %v", name, err)
		}
		if linkDest != "AGENTS.md" {
			t.Errorf("%s symlink target = %q, want %q", name, linkDest, "AGENTS.md")
		}
	}

	// Verify nested symlinks point to ../AGENTS.md
	nestedSymlinks := map[string]string{
		".cursor/rules":                   "../AGENTS.md",
		".github/copilot-instructions.md": "../AGENTS.md",
	}
	for name, expectedTarget := range nestedSymlinks {
		symlinkPath := filepath.Join(tmpDir, name)
		linkDest, err := os.Readlink(symlinkPath)
		if err != nil {
			t.Fatalf("Readlink(%s) error = %v", name, err)
		}
		if linkDest != expectedTarget {
			t.Errorf("%s symlink target = %q, want %q", name, linkDest, expectedTarget)
		}
	}
}

func TestInstallAgentsMDPreservesExistingContent(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	// Create an existing AGENTS.md with user content
	existingAgentsContent := "# My Custom Agents Config\n\nCustom instructions here.\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "AGENTS.md"), []byte(existingAgentsContent), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	_ = vm.Set("agentsContentFunc", func(ctx goja.Value) string {
		obj := ctx.ToObject(vm)
		existing := obj.Get("existingContent")
		if existing != nil && !goja.IsUndefined(existing) && !goja.IsNull(existing) {
			return existing.String()
		}
		return "# Agents\n\nThis file contains instructions for AI coding assistants.\n"
	})
	agentsContentFunc := vm.Get("agentsContentFunc")

	configs := config.MapOfConfigInit{
		"AGENTS.md": {
			Content:  agentsContentFunc,
			Scope: config.ScopeGitRoot,
		},
	}

	installer := NewInstaller(tmpDir, tmpDir, []string{}, configs, vm, nil)

	ctx := context.Background()
	results, err := installer.InstallAll(ctx, false)
	if err != nil {
		t.Fatalf("InstallAll() error = %v", err)
	}

	// Verify AGENTS.md preserved existing content
	for _, result := range results {
		if result.ConfigName == "AGENTS.md" {
			if result.Action != "patched" {
				t.Errorf("Action = %q, want %q", result.Action, "patched")
			}
		}
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("ReadFile(AGENTS.md) error = %v", err)
	}
	if string(content) != existingAgentsContent {
		t.Errorf("AGENTS.md content = %q, want %q", string(content), existingAgentsContent)
	}
}

func TestInstallConfigSetsScope(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	_ = vm.Set("contentFunc", func(ctx goja.Value) string {
		return "content"
	})
	contentFunc := vm.Get("contentFunc")

	installer := NewInstaller(tmpDir, tmpDir, []string{}, config.MapOfConfigInit{}, vm, nil)
	ctx := context.Background()

	t.Run("scope git-root", func(t *testing.T) {
		cfg := config.ConfigInit{
			Scope:   config.ScopeGitRoot,
			Content: contentFunc,
		}
		result := installer.installConfig(ctx, "root.yml", cfg, true)
		if result.Scope != config.ScopeGitRoot {
			t.Errorf("Scope = %q, want %q", result.Scope, config.ScopeGitRoot)
		}
	})

	t.Run("scope empty (project default)", func(t *testing.T) {
		cfg := config.ConfigInit{
			Content: contentFunc,
		}
		result := installer.installConfig(ctx, "local.yml", cfg, true)
		if result.Scope != "" {
			t.Errorf("Scope = %q, want empty", result.Scope)
		}
	})

	t.Run("scope preserved on skipped config", func(t *testing.T) {
		cfg := config.ConfigInit{
			Scope:        config.ScopeGitRoot,
			ProjectTypes: []string{"rust"},
		}
		result := installer.installConfig(ctx, "skip.yml", cfg, true)
		if result.Action != "skipped" {
			t.Errorf("Action = %q, want %q", result.Action, "skipped")
		}
		if result.Scope != config.ScopeGitRoot {
			t.Errorf("Scope = %q, want %q on skipped config", result.Scope, config.ScopeGitRoot)
		}
	})
}

func TestGenerateContentWithContext(t *testing.T) {
	tmpDir := t.TempDir()
	cwdDir := filepath.Join(tmpDir, "project")
	_ = os.MkdirAll(cwdDir, 0755)

	vm := goja.New()

	_ = vm.Set("contentFunc", func(ctx goja.Value) string {
		obj := ctx.ToObject(vm)
		rootPath := obj.Get("rootPath").String()
		cwdPath := obj.Get("cwdPath").String()
		return rootPath + ":" + cwdPath
	})

	contentFunc := vm.Get("contentFunc")

	installer := NewInstaller(tmpDir, cwdDir, []string{}, config.MapOfConfigInit{}, vm, nil)

	cfg := config.ConfigInit{
		Content: contentFunc,
	}

	existingContent := "old"
	originalContent := "old"
	existingPath := "/tmp/old.yml"

	content, err := installer.generateContent(context.Background(), cfg, &existingContent, &originalContent, &existingPath)
	if err != nil {
		t.Fatalf("generateContent() error = %v", err)
	}

	expected := tmpDir + ":" + cwdDir
	if content != expected {
		t.Errorf("content = %q, want %q", content, expected)
	}
}

func TestInstallConfigLinkTargetRejectsTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	installer := NewInstaller(tmpDir, tmpDir, []string{}, config.MapOfConfigInit{}, vm, nil)

	tests := []struct {
		name       string
		linkTarget string
		configName string
		wantError  bool
	}{
		{
			name:       "absolute path rejected",
			linkTarget: "/etc/passwd",
			configName: "CLAUDE.md",
			wantError:  true,
		},
		{
			name:       "traversal above root rejected",
			linkTarget: "../../etc/passwd",
			configName: "CLAUDE.md",
			wantError:  true,
		},
		{
			name:       "deep traversal above root rejected",
			linkTarget: "../../../etc/passwd",
			configName: ".cursor/rules",
			wantError:  true,
		},
		{
			name:       "valid same-dir target accepted",
			linkTarget: "AGENTS.md",
			configName: "CLAUDE.md",
			wantError:  false,
		},
		{
			name:       "valid parent-dir target for nested config accepted",
			linkTarget: "../AGENTS.md",
			configName: ".cursor/rules",
			wantError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.ConfigInit{
				Scope: config.ScopeGitRoot,
				LinkTarget: tt.linkTarget,
			}

			ctx := context.Background()
			result := installer.installConfig(ctx, tt.configName, cfg, true)

			if tt.wantError && result.Error == nil {
				t.Errorf("expected error for linkTarget %q, got nil", tt.linkTarget)
			}
			if !tt.wantError && result.Error != nil {
				t.Errorf("unexpected error for linkTarget %q: %v", tt.linkTarget, result.Error)
			}
		})
	}
}

func TestInstallConfigLinkTargetRejectsSymlinkEscape(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	// Create a symlink inside the repo that points outside
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("secret"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(tmpDir, "escape")); err != nil {
		t.Fatalf("failed to create test symlink: %v", err)
	}

	installer := NewInstaller(tmpDir, tmpDir, []string{}, config.MapOfConfigInit{}, vm, nil)

	cfg := config.ConfigInit{
		Scope: config.ScopeGitRoot,
		LinkTarget: "escape/secret.txt",
	}

	ctx := context.Background()
	result := installer.installConfig(ctx, "link.md", cfg, false)

	if result.Error == nil {
		t.Error("expected error for linkTarget through symlink escaping repo root, got nil")
	}
}

func TestInstallConfigLinkTargetRejectsSymlinkEscapeNonExistentTarget(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	// Create a symlink inside the repo that points outside
	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(tmpDir, "escape")); err != nil {
		t.Fatalf("failed to create test symlink: %v", err)
	}

	installer := NewInstaller(tmpDir, tmpDir, []string{}, config.MapOfConfigInit{}, vm, nil)

	// Target file does NOT exist in outsideDir — EvalSymlinks on the full path will fail,
	// but the ancestor check should still catch the escape
	cfg := config.ConfigInit{
		Scope: config.ScopeGitRoot,
		LinkTarget: "escape/new.txt",
	}

	ctx := context.Background()
	result := installer.installConfig(ctx, "link.md", cfg, false)

	if result.Error == nil {
		t.Error("expected error for linkTarget through symlink escaping repo root with non-existent target, got nil")
	}
}

func TestInstallConfigLinkTargetRejectsBrokenSymlinkEscape(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	// Create a broken symlink inside the repo pointing to a non-existent outside path.
	// EvalSymlinks will fail for the symlink itself, so the ancestor walk must detect
	// it as an unresolvable symlink rather than skipping to the parent.
	if err := os.Symlink("/outside/missing-dir", filepath.Join(tmpDir, "escape")); err != nil {
		t.Fatalf("failed to create test symlink: %v", err)
	}

	installer := NewInstaller(tmpDir, tmpDir, []string{}, config.MapOfConfigInit{}, vm, nil)

	cfg := config.ConfigInit{
		Scope: config.ScopeGitRoot,
		LinkTarget: "escape/new.txt",
	}

	ctx := context.Background()
	result := installer.installConfig(ctx, "link.md", cfg, false)

	if result.Error == nil {
		t.Error("expected error for linkTarget through broken symlink escaping repo root, got nil")
	}
}

func TestInstallConfigLinkTargetRejectsBrokenLeafSymlink(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	// Create a broken symlink as the direct leaf target (not an intermediate directory).
	// This tests the case where LinkTarget points directly to a broken symlink
	// rather than through one (e.g., "escape" vs "escape/new.txt").
	if err := os.Symlink("/outside/missing", filepath.Join(tmpDir, "escape")); err != nil {
		t.Fatalf("failed to create test symlink: %v", err)
	}

	installer := NewInstaller(tmpDir, tmpDir, []string{}, config.MapOfConfigInit{}, vm, nil)

	cfg := config.ConfigInit{
		Scope: config.ScopeGitRoot,
		LinkTarget: "escape",
	}

	ctx := context.Background()
	result := installer.installConfig(ctx, "link.md", cfg, false)

	if result.Error == nil {
		t.Error("expected error for linkTarget that is a broken leaf symlink escaping repo root, got nil")
	}
}

func TestInstallConfigOtherFileNameListSkipsMainFile(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	// Create the main file
	mainFile := filepath.Join(tmpDir, "main.yml")
	if err := os.WriteFile(mainFile, []byte("original content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	_ = vm.Set("contentFunc", func(ctx goja.Value) string {
		return "new content"
	})
	contentFunc := vm.Get("contentFunc")

	installer := NewInstaller(tmpDir, tmpDir, []string{}, config.MapOfConfigInit{}, vm, nil)

	// OtherFileNameList includes the main filename — should be skipped
	cfg := config.ConfigInit{
		OtherFileNameList: []string{"main.yml", "alt.yml"},
		Content:           contentFunc,
	}

	ctx := context.Background()
	result := installer.installConfig(ctx, "main.yml", cfg, false)

	if result.Error != nil {
		t.Fatalf("installConfig() error = %v", result.Error)
	}

	// Should be "patched" because the main file existed and was read before content generation
	if result.Action != "patched" {
		t.Errorf("Action = %q, want %q (main file should not be deleted by OtherFileNameList)", result.Action, "patched")
	}

	// main.yml should not appear in DeletedFiles
	for _, d := range result.DeletedFiles {
		if d == mainFile {
			t.Error("main file should not be in DeletedFiles")
		}
	}
}

func TestGenerateContentDatamitsuDir(t *testing.T) {
	tests := []struct {
		name        string
		cwdRelPath  string
		expectedDir string
	}{
		{
			name:        "cwd is root",
			cwdRelPath:  "",
			expectedDir: ".datamitsu",
		},
		{
			name:        "cwd is one level deep",
			cwdRelPath:  "project",
			expectedDir: filepath.Join("..", ".datamitsu"),
		},
		{
			name:        "cwd is two levels deep",
			cwdRelPath:  filepath.Join("packages", "frontend"),
			expectedDir: filepath.Join("..", "..", ".datamitsu"),
		},
		{
			name:        "cwd is three levels deep",
			cwdRelPath:  filepath.Join("packages", "frontend", "src"),
			expectedDir: filepath.Join("..", "..", "..", ".datamitsu"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			cwdDir := tmpDir
			if tt.cwdRelPath != "" {
				cwdDir = filepath.Join(tmpDir, tt.cwdRelPath)
				_ = os.MkdirAll(cwdDir, 0755)
			}

			vm := goja.New()

			_ = vm.Set("contentFunc", func(ctx goja.Value) string {
				obj := ctx.ToObject(vm)
				datamitsuDir := obj.Get("datamitsuDir")
				if datamitsuDir == nil || goja.IsUndefined(datamitsuDir) {
					return "UNDEFINED"
				}
				return datamitsuDir.String()
			})
			contentFunc := vm.Get("contentFunc")

			installer := NewInstaller(tmpDir, cwdDir, []string{}, config.MapOfConfigInit{}, vm, nil)
			cfg := config.ConfigInit{
				Content: contentFunc,
			}

			content, err := installer.generateContent(context.Background(), cfg, nil, nil, nil)
			if err != nil {
				t.Fatalf("generateContent() error = %v", err)
			}

			if content != tt.expectedDir {
				t.Errorf("datamitsuDir = %q, want %q", content, tt.expectedDir)
			}
		})
	}
}

func TestNewInstallerWithLayerMap(t *testing.T) {
	rootPath := "/tmp/root"
	cwdPath := "/tmp/root/project"
	projectTypes := []string{"node"}
	configs := config.MapOfConfigInit{}
	vm := goja.New()

	layerMap := &config.InitLayerMap{}

	installer := NewInstaller(rootPath, cwdPath, projectTypes, configs, vm, layerMap)

	if installer == nil {
		t.Fatal("NewInstaller() returned nil")
	}
	if installer.layerMap != layerMap {
		t.Error("layerMap not stored in installer")
	}
}

func TestInstallConfigUsesLayerHistoryWhenPresent(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	// Set up a content function that should NOT be called when layer history is used
	_ = vm.Set("contentFunc", func(ctx goja.Value) string {
		return "should not be called"
	})
	contentFunc := vm.Get("contentFunc")

	layerContent := "content from layer history"
	layerMap := config.InitLayerMap{
		"test.yml": &config.InitLayerHistory{
			FileName: "test.yml",
			Layers: []config.InitLayerEntry{
				{
					LayerName:        "default",
					GeneratedContent: &layerContent,

				},
			},
			FinalConfig: config.ConfigInit{
				Content: contentFunc,
			},
		},
	}

	configs := config.MapOfConfigInit{
		"test.yml": {Content: contentFunc, Scope: config.ScopeGitRoot},
	}

	installer := NewInstaller(tmpDir, tmpDir, []string{}, configs, vm, &layerMap)

	ctx := context.Background()
	result := installer.installConfig(ctx, "test.yml", configs["test.yml"], false)

	if result.Error != nil {
		t.Fatalf("installConfig() error = %v", result.Error)
	}

	if result.Action != "created" {
		t.Errorf("Action = %q, want %q", result.Action, "created")
	}

	filePath := filepath.Join(tmpDir, "test.yml")
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if string(content) != layerContent {
		t.Errorf("content = %q, want %q", string(content), layerContent)
	}
}

func TestInstallConfigProjectScopedSkipsLayerHistory(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	// Content function that WILL be called because project-scoped entries don't use layer history
	_ = vm.Set("contentFunc", func(ctx goja.Value) string {
		return "generated per-project"
	})
	contentFunc := vm.Get("contentFunc")

	layerContent := "content from layer history"
	layerMap := config.InitLayerMap{
		"tsconfig.json": &config.InitLayerHistory{
			FileName: "tsconfig.json",
			Layers: []config.InitLayerEntry{
				{
					LayerName:        "default",
					GeneratedContent: &layerContent,
				},
			},
			FinalConfig: config.ConfigInit{
				Content: contentFunc,
			},
		},
	}

	configs := config.MapOfConfigInit{
		"tsconfig.json": {Content: contentFunc, Scope: config.ScopeProject},
	}

	installer := NewInstaller(tmpDir, tmpDir, []string{}, configs, vm, &layerMap)

	ctx := context.Background()
	result := installer.installConfig(ctx, "tsconfig.json", configs["tsconfig.json"], false)

	if result.Error != nil {
		t.Fatalf("installConfig() error = %v", result.Error)
	}

	filePath := filepath.Join(tmpDir, "tsconfig.json")
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	// Should use generateContent(), not layer history
	if string(content) != "generated per-project" {
		t.Errorf("content = %q, want %q (project-scoped should not use layer history)", string(content), "generated per-project")
	}
}

func TestInstallConfigFallsToDiskWhenNoLayerHistory(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	_ = vm.Set("contentFunc", func(ctx goja.Value) string {
		return "generated by content function"
	})
	contentFunc := vm.Get("contentFunc")

	// layerMap exists but has no entry for "other.yml"
	layerMap := config.InitLayerMap{
		"test.yml": &config.InitLayerHistory{
			FileName: "test.yml",
			Layers:   []config.InitLayerEntry{},
		},
	}

	configs := config.MapOfConfigInit{
		"other.yml": {Content: contentFunc},
	}

	installer := NewInstaller(tmpDir, tmpDir, []string{}, configs, vm, &layerMap)

	ctx := context.Background()
	result := installer.installConfig(ctx, "other.yml", configs["other.yml"], false)

	if result.Error != nil {
		t.Fatalf("installConfig() error = %v", result.Error)
	}

	filePath := filepath.Join(tmpDir, "other.yml")
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if string(content) != "generated by content function" {
		t.Errorf("content = %q, want %q", string(content), "generated by content function")
	}
}

func TestInstallConfigUsesGetLastGeneratedContent(t *testing.T) {
	tmpDir := t.TempDir()
	vm := goja.New()

	_ = vm.Set("contentFunc", func(ctx goja.Value) string {
		return "should not be called"
	})
	contentFunc := vm.Get("contentFunc")

	firstContent := "first layer"
	secondContent := "second layer override"
	layerMap := config.InitLayerMap{
		"test.yml": &config.InitLayerHistory{
			FileName: "test.yml",
			Layers: []config.InitLayerEntry{
				{
					LayerName:        "default",
					GeneratedContent: &firstContent,

				},
				{
					LayerName:        "auto",
					GeneratedContent: &secondContent,

				},
			},
			FinalConfig: config.ConfigInit{
				Content: contentFunc,
			},
		},
	}

	configs := config.MapOfConfigInit{
		"test.yml": {Content: contentFunc, Scope: config.ScopeGitRoot},
	}

	installer := NewInstaller(tmpDir, tmpDir, []string{}, configs, vm, &layerMap)

	ctx := context.Background()
	result := installer.installConfig(ctx, "test.yml", configs["test.yml"], false)

	if result.Error != nil {
		t.Fatalf("installConfig() error = %v", result.Error)
	}

	filePath := filepath.Join(tmpDir, "test.yml")
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if string(content) != secondContent {
		t.Errorf("content = %q, want %q (should use last layer's content)", string(content), secondContent)
	}
}
