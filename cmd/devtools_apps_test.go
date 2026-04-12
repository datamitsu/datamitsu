package cmd

import (
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestBuildFileTree(t *testing.T) {
	dir := t.TempDir()

	// Create test file structure
	_ = os.MkdirAll(filepath.Join(dir, "dist"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "dist", "index.js"), []byte(""), 0644)
	_ = os.WriteFile(filepath.Join(dir, "dist", "index.d.ts"), []byte(""), 0644)

	lines := buildFileTree(dir)

	if len(lines) == 0 {
		t.Fatal("expected non-empty file tree")
	}

	// Check that expected entries exist (with tree prefixes)
	hasPackageJSON := false
	hasDist := false
	for _, line := range lines {
		if strings.Contains(line, "package.json") {
			hasPackageJSON = true
		}
		if strings.Contains(line, "dist") {
			hasDist = true
		}
	}
	if !hasPackageJSON {
		t.Error("file tree missing package.json")
	}
	if !hasDist {
		t.Error("file tree missing dist/")
	}
}

func TestBuildFileTreeEmpty(t *testing.T) {
	dir := t.TempDir()
	lines := buildFileTree(dir)
	if len(lines) != 0 {
		t.Errorf("expected empty file tree for empty dir, got %d lines", len(lines))
	}
}

func TestBuildFileTreeNodeModulesCollapsed(t *testing.T) {
	dir := t.TempDir()

	// Create node_modules with many files
	nmDir := filepath.Join(dir, "node_modules")
	_ = os.MkdirAll(filepath.Join(nmDir, "pkg-a"), 0755)
	_ = os.MkdirAll(filepath.Join(nmDir, "pkg-b"), 0755)
	_ = os.WriteFile(filepath.Join(nmDir, "pkg-a", "index.js"), []byte(""), 0644)
	_ = os.WriteFile(filepath.Join(nmDir, "pkg-b", "index.js"), []byte(""), 0644)
	_ = os.WriteFile(filepath.Join(dir, "index.js"), []byte(""), 0644)

	lines := buildFileTree(dir)

	// node_modules should be collapsed to a single line with file count
	hasCollapsedNodeModules := false
	for _, line := range lines {
		if strings.Contains(line, "node_modules/") && strings.Contains(line, "files") {
			hasCollapsedNodeModules = true
		}
	}
	if !hasCollapsedNodeModules {
		t.Errorf("expected node_modules to be collapsed, got lines: %v", lines)
	}
}

func TestBuildFileTreeVenvCollapsed(t *testing.T) {
	dir := t.TempDir()

	// Create .venv with many files
	venvDir := filepath.Join(dir, ".venv")
	_ = os.MkdirAll(filepath.Join(venvDir, "lib"), 0755)
	_ = os.WriteFile(filepath.Join(venvDir, "lib", "site.py"), []byte(""), 0644)
	_ = os.WriteFile(filepath.Join(dir, "main.py"), []byte(""), 0644)

	lines := buildFileTree(dir)

	hasCollapsedVenv := false
	for _, line := range lines {
		if strings.Contains(line, ".venv/") && strings.Contains(line, "files") {
			hasCollapsedVenv = true
		}
	}
	if !hasCollapsedVenv {
		t.Errorf("expected .venv to be collapsed, got lines: %v", lines)
	}
}

func TestAppsInspectErrorNotInstalled(t *testing.T) {
	bm := binmanager.New(binmanager.MapOfApps{
		"test-app": {Shell: &binmanager.AppConfigShell{Name: "bash"}},
	}, nil, nil)

	_, err := bm.GetInstallRoot("test-app")
	if err == nil {
		t.Error("expected error for shell app install root")
	}
}

func TestCountFiles(t *testing.T) {
	dir := t.TempDir()

	_ = os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte(""), 0644)
	_ = os.WriteFile(filepath.Join(dir, "b.txt"), []byte(""), 0644)
	_ = os.WriteFile(filepath.Join(dir, "sub", "c.txt"), []byte(""), 0644)

	count := countFiles(dir)
	if count != 3 {
		t.Errorf("expected 3 files, got %d", count)
	}
}

func TestCountFilesEmpty(t *testing.T) {
	dir := t.TempDir()
	count := countFiles(dir)
	if count != 0 {
		t.Errorf("expected 0 files, got %d", count)
	}
}

func TestAppsInspect_ShowsDescription(t *testing.T) {
	app := binmanager.App{
		Description: "A spell checker for code",
		Fnm:         &binmanager.AppConfigFNM{PackageName: "cspell", Version: "9.8.0"},
	}

	output := formatInspectHeader("/path/to/app", &app)

	if !strings.Contains(output, "Install path: /path/to/app") {
		t.Errorf("expected install path in output, got: %s", output)
	}
	if !strings.Contains(output, "Description: A spell checker for code") {
		t.Errorf("expected description in output, got: %s", output)
	}
}

func TestAppsInspect_NoDescriptionWhenEmpty(t *testing.T) {
	app := binmanager.App{
		Description: "",
		Shell:       &binmanager.AppConfigShell{Name: "echo"},
	}

	output := formatInspectHeader("/path/to/app", &app)

	if strings.Contains(output, "Description:") {
		t.Errorf("expected no description line for empty description, got: %s", output)
	}
	if !strings.Contains(output, "Install path: /path/to/app") {
		t.Errorf("expected install path in output, got: %s", output)
	}
}

func TestAppsListFormatWithVersionAndDescription(t *testing.T) {
	apps := binmanager.MapOfApps{
		"yamllint": {
			Description: "A linter for YAML files",
			Uv:          &binmanager.AppConfigUV{PackageName: "yamllint", Version: "1.33"},
		},
		"echo": {
			Shell: &binmanager.AppConfigShell{Name: "echo"},
		},
		"golangci-lint": {
			Description: "Go linter",
			Binary:      &binmanager.AppConfigBinary{Version: "v2.7.2"},
		},
	}

	bm := binmanager.New(apps, nil, nil)
	appInfos := bm.GetAppsList()
	sort.Slice(appInfos, func(i, j int) bool {
		return appInfos[i].Name < appInfos[j].Name
	})

	if len(appInfos) != 3 {
		t.Fatalf("expected 3 apps, got %d", len(appInfos))
	}

	// echo (shell) - no version, no description
	echoInfo := appInfos[0]
	if echoInfo.Name != "echo" {
		t.Errorf("expected first app 'echo', got %q", echoInfo.Name)
	}
	if echoInfo.Version != "" {
		t.Errorf("expected empty version for shell app, got %q", echoInfo.Version)
	}
	if echoInfo.Description != "" {
		t.Errorf("expected empty description for echo, got %q", echoInfo.Description)
	}

	// golangci-lint (binary) - has version and description
	glInfo := appInfos[1]
	if glInfo.Version != "v2.7.2" {
		t.Errorf("expected version 'v2.7.2', got %q", glInfo.Version)
	}
	if glInfo.Description != "Go linter" {
		t.Errorf("expected description 'Go linter', got %q", glInfo.Description)
	}

	// yamllint (uv) - has version and description
	ylInfo := appInfos[2]
	if ylInfo.Version != "1.33" {
		t.Errorf("expected version '1.33', got %q", ylInfo.Version)
	}
	if ylInfo.Description != "A linter for YAML files" {
		t.Errorf("expected description 'A linter for YAML files', got %q", ylInfo.Description)
	}
}
