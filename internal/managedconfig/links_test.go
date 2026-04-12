package managedconfig

import (
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateLinkPath_ValidPaths(t *testing.T) {
	installRoot := t.TempDir()

	// Create files/dirs to resolve against
	_ = os.MkdirAll(filepath.Join(installRoot, "dist", "nested"), 0755)
	_ = os.WriteFile(filepath.Join(installRoot, "config.js"), []byte("x"), 0644)
	_ = os.WriteFile(filepath.Join(installRoot, "dist", "eslint.config.js"), []byte("x"), 0644)
	_ = os.WriteFile(filepath.Join(installRoot, "dist", "nested", "deep.js"), []byte("x"), 0644)

	validPaths := []string{
		"config.js",
		"dist/eslint.config.js",
		"dist/nested/deep.js",
		"./config.js",
		"./dist/eslint.config.js",
		"dist",
	}

	for _, p := range validPaths {
		t.Run(p, func(t *testing.T) {
			if err := validateLinkPath(p, installRoot); err != nil {
				t.Errorf("validateLinkPath(%q) returned unexpected error: %v", p, err)
			}
		})
	}
}

func TestValidateLinkPath_BlocksAbsolutePaths(t *testing.T) {
	installRoot := t.TempDir()

	absPaths := []string{
		"/etc/passwd",
		"/tmp/evil",
	}

	for _, p := range absPaths {
		t.Run(p, func(t *testing.T) {
			err := validateLinkPath(p, installRoot)
			if err == nil {
				t.Fatalf("validateLinkPath(%q) should have returned error for absolute path", p)
			}
			if !strings.Contains(err.Error(), "must be relative") {
				t.Errorf("error should mention 'must be relative', got: %v", err)
			}
		})
	}
}

func TestValidateLinkPath_BlocksParentTraversal(t *testing.T) {
	installRoot := t.TempDir()

	traversalPaths := []string{
		"../../../etc/passwd",
		"..",
		"dist/../../etc/passwd",
		"../sibling",
	}

	for _, p := range traversalPaths {
		t.Run(p, func(t *testing.T) {
			err := validateLinkPath(p, installRoot)
			if err == nil {
				t.Fatalf("validateLinkPath(%q) should have returned error for parent traversal", p)
			}
			if !strings.Contains(err.Error(), "escapes install directory") {
				t.Errorf("error should mention 'escapes install directory', got: %v", err)
			}
		})
	}
}

func TestValidateLinkPath_BlocksSymlinkEscape(t *testing.T) {
	installRoot := t.TempDir()
	outsideDir := t.TempDir()

	// Create a symlink inside installRoot that points outside
	_ = os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("secret"), 0644)
	_ = os.Symlink(outsideDir, filepath.Join(installRoot, "escape-link"))

	err := validateLinkPath("escape-link/secret.txt", installRoot)
	if err == nil {
		t.Error("validateLinkPath should block symlink that escapes install directory")
	}
	if err != nil && !strings.Contains(err.Error(), "escapes install directory") {
		t.Errorf("error should mention 'escapes install directory', got: %v", err)
	}
}

func TestValidateLinkPath_EdgeCases(t *testing.T) {
	installRoot := t.TempDir()

	t.Run("empty string", func(t *testing.T) {
		err := validateLinkPath("", installRoot)
		if err == nil {
			t.Error("validateLinkPath should reject empty string")
		}
	})

	t.Run("dot path resolves to install root", func(t *testing.T) {
		// "." refers to installRoot itself, which is valid
		if err := validateLinkPath(".", installRoot); err != nil {
			t.Errorf("validateLinkPath(\".\") should be valid, got: %v", err)
		}
	})
}

func TestCreateDatamitsuLinks_BlocksPathTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	installDir := filepath.Join(tmpDir, "install", "app", "hash")

	_ = os.MkdirAll(installDir, 0755)
	_ = os.WriteFile(filepath.Join(installDir, "config.js"), []byte("ok"), 0644)

	apps := binmanager.MapOfApps{
		"evil-app": {
			Links: map[string]string{
				"config": "../../../etc/passwd",
			},
		},
	}

	resolver := &mockResolver{
		paths: map[string]string{
			"evil-app": installDir,
		},
	}

	_, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, false)
	if err == nil {
		t.Fatal("expected error for path traversal attempt, got nil")
	}
	if !strings.Contains(err.Error(), "escapes install directory") {
		t.Errorf("error should mention path traversal, got: %v", err)
	}
}

func TestCreateDatamitsuLinks_BlocksAbsolutePathInLinks(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	installDir := filepath.Join(tmpDir, "install", "app", "hash")

	_ = os.MkdirAll(installDir, 0755)

	apps := binmanager.MapOfApps{
		"evil-app": {
			Links: map[string]string{
				"config": "/etc/passwd",
			},
		},
	}

	resolver := &mockResolver{
		paths: map[string]string{
			"evil-app": installDir,
		},
	}

	_, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, false)
	if err == nil {
		t.Fatal("expected error for absolute path, got nil")
	}
	if !strings.Contains(err.Error(), "must be relative") {
		t.Errorf("error should mention relative path requirement, got: %v", err)
	}
}

func TestCreateDatamitsuLinks_BlocksPathTraversalInLinkName(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	installDir := filepath.Join(tmpDir, "install", "app", "hash")

	_ = os.MkdirAll(installDir, 0755)
	_ = os.WriteFile(filepath.Join(installDir, "config.js"), []byte("ok"), 0644)

	tests := []struct {
		name     string
		linkName string
	}{
		{"parent traversal", "../../etc/shadow"},
		{"dotdot only", ".."},
		{"absolute path", "/etc/passwd"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			apps := binmanager.MapOfApps{
				"app": {
					Links: map[string]string{
						tc.linkName: "config.js",
					},
				},
			}

			resolver := &mockResolver{
				paths: map[string]string{
					"app": installDir,
				},
			}

			_, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, false)
			if err == nil {
				t.Fatalf("expected error for malicious link name %q, got nil", tc.linkName)
			}
			if !strings.Contains(err.Error(), "path traversal") {
				t.Errorf("error should mention 'path traversal', got: %v", err)
			}
		})
	}
}

type mockResolver struct {
	paths map[string]string
}

func (m *mockResolver) GetInstallRoot(appName string) (string, error) {
	p, ok := m.paths[appName]
	if !ok {
		return "", fmt.Errorf("app %q is not installed", appName)
	}
	return p, nil
}

func TestCreateDatamitsuLinks_CreatesSymlinks(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	installDir := filepath.Join(tmpDir, "install", "eslint", "abc123")

	if err := os.MkdirAll(installDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write the file that the symlink will point to
	configContent := "module.exports = {};"
	if err := os.WriteFile(filepath.Join(installDir, "eslint-base.js"), []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	apps := binmanager.MapOfApps{
		"eslint": {
			Files: map[string]string{
				"eslint-base.js": configContent,
			},
			Links: map[string]string{
				"eslint-base.js": "eslint-base.js",
			},
		},
	}

	resolver := &mockResolver{
		paths: map[string]string{
			"eslint": installDir,
		},
	}

	if _, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, false); err != nil {
		t.Fatalf("CreateDatamitsuLinks failed: %v", err)
	}

	// Verify .datamitsu directory was created
	datamitsuDir := filepath.Join(gitRoot, ".datamitsu")
	info, err := os.Lstat(datamitsuDir)
	if err != nil {
		t.Fatalf(".datamitsu directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal(".datamitsu is not a directory")
	}

	// Verify symlink was created
	linkPath := filepath.Join(datamitsuDir, "eslint-base.js")
	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("symlink not created at %s: %v", linkPath, err)
	}

	// Target should be a relative path from .datamitsu/ to the install dir
	expectedTarget, err := filepath.Rel(filepath.Join(gitRoot, ".datamitsu"), filepath.Join(installDir, "eslint-base.js"))
	if err != nil {
		t.Fatalf("failed to compute expected relative target: %v", err)
	}
	if target != expectedTarget {
		t.Errorf("symlink target = %q, want %q", target, expectedTarget)
	}

	// Verify symlink resolves to correct content
	content, err := os.ReadFile(linkPath)
	if err != nil {
		t.Fatalf("failed to read through symlink: %v", err)
	}
	if string(content) != configContent {
		t.Errorf("content through symlink = %q, want %q", string(content), configContent)
	}
}

func TestCreateDatamitsuLinks_NestedRelativePath(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	installDir := filepath.Join(tmpDir, "install", "eslint-config", "abc123")

	if err := os.MkdirAll(filepath.Join(installDir, "dist"), 0755); err != nil {
		t.Fatal(err)
	}
	configContent := "module.exports = {};"
	if err := os.WriteFile(filepath.Join(installDir, "dist", "eslint.config.js"), []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	apps := binmanager.MapOfApps{
		"eslint-config": {
			Links: map[string]string{
				"eslint-config": "dist/eslint.config.js",
			},
		},
	}

	resolver := &mockResolver{
		paths: map[string]string{
			"eslint-config": installDir,
		},
	}

	if _, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, false); err != nil {
		t.Fatalf("CreateDatamitsuLinks failed: %v", err)
	}

	linkPath := filepath.Join(gitRoot, ".datamitsu", "eslint-config")
	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("symlink not created: %v", err)
	}

	expectedTarget, err := filepath.Rel(filepath.Join(gitRoot, ".datamitsu"), filepath.Join(installDir, "dist", "eslint.config.js"))
	if err != nil {
		t.Fatalf("failed to compute expected relative target: %v", err)
	}
	if target != expectedTarget {
		t.Errorf("symlink target = %q, want %q", target, expectedTarget)
	}

	content, err := os.ReadFile(linkPath)
	if err != nil {
		t.Fatalf("failed to read through symlink: %v", err)
	}
	if string(content) != configContent {
		t.Errorf("content through symlink = %q, want %q", string(content), configContent)
	}
}

func TestCreateDatamitsuLinks_ErrorWhenTargetNotExist(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	installDir := filepath.Join(tmpDir, "install", "app", "hash")

	if err := os.MkdirAll(installDir, 0755); err != nil {
		t.Fatal(err)
	}

	apps := binmanager.MapOfApps{
		"app": {
			Links: map[string]string{
				"config": "dist/nonexistent.js",
			},
		},
	}

	resolver := &mockResolver{
		paths: map[string]string{
			"app": installDir,
		},
	}

	_, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, false)
	if err == nil {
		t.Fatal("expected error when target file does not exist, got nil")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error should mention target not existing, got: %v", err)
	}
}

func TestCreateDatamitsuLinks_MultipleApps(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	eslintDir := filepath.Join(tmpDir, "install", "eslint", "abc")
	prettierDir := filepath.Join(tmpDir, "install", "prettier", "def")

	for _, dir := range []string{eslintDir, prettierDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.WriteFile(filepath.Join(eslintDir, "eslint.js"), []byte("eslint"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prettierDir, "prettier.js"), []byte("prettier"), 0644); err != nil {
		t.Fatal(err)
	}

	apps := binmanager.MapOfApps{
		"eslint": {
			Files: map[string]string{"eslint.js": "eslint"},
			Links: map[string]string{"eslint.js": "eslint.js"},
		},
		"prettier": {
			Files: map[string]string{"prettier.js": "prettier"},
			Links: map[string]string{"prettier.js": "prettier.js"},
		},
	}

	resolver := &mockResolver{
		paths: map[string]string{
			"eslint":   eslintDir,
			"prettier": prettierDir,
		},
	}

	if _, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, false); err != nil {
		t.Fatalf("CreateDatamitsuLinks failed: %v", err)
	}

	// Verify both symlinks exist
	for _, linkName := range []string{"eslint.js", "prettier.js"} {
		linkPath := filepath.Join(gitRoot, ".datamitsu", linkName)
		if _, err := os.Readlink(linkPath); err != nil {
			t.Errorf("symlink %q not created: %v", linkName, err)
		}
	}
}

func TestCreateDatamitsuLinks_DryRunDoesNotTouchFilesystem(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	installDir := filepath.Join(tmpDir, "install", "eslint", "hash")

	if err := os.MkdirAll(installDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "eslint.js"), []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	apps := binmanager.MapOfApps{
		"eslint": {
			Files: map[string]string{"eslint.js": "content"},
			Links: map[string]string{"eslint.js": "eslint.js"},
		},
	}

	resolver := &mockResolver{
		paths: map[string]string{
			"eslint": installDir,
		},
	}

	if _, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, true); err != nil {
		t.Fatalf("dry run failed: %v", err)
	}

	// .datamitsu directory should NOT exist
	datamitsuDir := filepath.Join(gitRoot, ".datamitsu")
	if _, err := os.Stat(datamitsuDir); !os.IsNotExist(err) {
		t.Errorf(".datamitsu directory should not exist in dry-run mode, but got err: %v", err)
	}
}

func TestCreateDatamitsuLinks_DryRunDetectsUninstalledApp(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")

	apps := binmanager.MapOfApps{
		"eslint": {
			Links: map[string]string{"eslint.js": "eslint.js"},
		},
	}

	resolver := &mockResolver{
		paths: map[string]string{},
	}

	_, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, true)
	if err == nil {
		t.Fatal("expected error for uninstalled app in dry-run mode, got nil")
	}
	if !strings.Contains(err.Error(), "not installed") {
		t.Errorf("error should mention 'not installed', got: %v", err)
	}
}

func TestCreateDatamitsuLinks_ErrorsOnUninstalledApp(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")

	apps := binmanager.MapOfApps{
		"eslint": {
			Links: map[string]string{"eslint.js": "eslint.js"},
		},
	}

	// Resolver returns error for "eslint" (not installed)
	resolver := &mockResolver{
		paths: map[string]string{},
	}

	_, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, false)
	if err == nil {
		t.Fatal("expected error for uninstalled app, got nil")
	}
	if !strings.Contains(err.Error(), "eslint") {
		t.Errorf("error should contain app name 'eslint', got: %v", err)
	}
	if !strings.Contains(err.Error(), "not installed") {
		t.Errorf("error should mention 'not installed', got: %v", err)
	}
}

func TestCreateDatamitsuLinks_ErrorsOnPartiallyInstalledApps(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	eslintDir := filepath.Join(tmpDir, "install", "eslint", "abc")

	if err := os.MkdirAll(eslintDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(eslintDir, "eslint.js"), []byte("eslint"), 0644); err != nil {
		t.Fatal(err)
	}

	apps := binmanager.MapOfApps{
		"eslint": {
			Files: map[string]string{"eslint.js": "eslint"},
			Links: map[string]string{"eslint.js": "eslint.js"},
		},
		"prettier": {
			Links: map[string]string{"prettier.js": "prettier.js"},
		},
	}

	// Only eslint is installed, prettier is not
	resolver := &mockResolver{
		paths: map[string]string{
			"eslint": eslintDir,
		},
	}

	_, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, false)
	if err == nil {
		t.Fatal("expected error when some apps are not installed, got nil")
	}
	if !strings.Contains(err.Error(), "prettier") {
		t.Errorf("error should contain uninstalled app name 'prettier', got: %v", err)
	}
	if !strings.Contains(err.Error(), "not installed") {
		t.Errorf("error should mention 'not installed', got: %v", err)
	}
}

func TestCreateDatamitsuLinks_RemovesExistingDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	installDir := filepath.Join(tmpDir, "install", "app1", "hash")

	if err := os.MkdirAll(installDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "config.js"), []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create pre-existing .datamitsu with stale content
	datamitsuDir := filepath.Join(gitRoot, ".datamitsu")
	if err := os.MkdirAll(datamitsuDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(datamitsuDir, "stale-file.txt"), []byte("stale"), 0644); err != nil {
		t.Fatal(err)
	}

	apps := binmanager.MapOfApps{
		"app1": {
			Files: map[string]string{"config.js": "new"},
			Links: map[string]string{"config.js": "config.js"},
		},
	}

	resolver := &mockResolver{
		paths: map[string]string{
			"app1": installDir,
		},
	}

	if _, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, false); err != nil {
		t.Fatalf("CreateDatamitsuLinks failed: %v", err)
	}

	// Stale file should be gone
	if _, err := os.Stat(filepath.Join(datamitsuDir, "stale-file.txt")); !os.IsNotExist(err) {
		t.Error("stale file should have been removed")
	}

	// New symlink should exist
	if _, err := os.Readlink(filepath.Join(datamitsuDir, "config.js")); err != nil {
		t.Errorf("new symlink not created: %v", err)
	}

	// .gitignore should survive the atomic swap
	content, err := os.ReadFile(filepath.Join(datamitsuDir, ".gitignore"))
	if err != nil {
		t.Fatalf(".gitignore not present after atomic swap: %v", err)
	}
	if string(content) != "*\n" {
		t.Errorf(".gitignore content = %q, want %q", string(content), "*\n")
	}
}

func TestCreateDatamitsuLinks_NoLinksIsNoop(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")

	apps := binmanager.MapOfApps{
		"eslint": {
			Files: map[string]string{"eslint.js": "content"},
			// No Links
		},
		"prettier": {
			// No Files or Links
		},
	}

	resolver := &mockResolver{}

	if _, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// .datamitsu directory should NOT exist (nothing to do)
	datamitsuDir := filepath.Join(gitRoot, ".datamitsu")
	if _, err := os.Stat(datamitsuDir); !os.IsNotExist(err) {
		t.Errorf(".datamitsu directory should not exist when no apps have links")
	}
}

func TestVerifySymlink_CorrectTarget(t *testing.T) {
	tmpDir := t.TempDir()

	targetFile := filepath.Join(tmpDir, "target.js")
	if err := os.WriteFile(targetFile, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	linkPath := filepath.Join(tmpDir, "link.js")
	relTarget, err := filepath.Rel(filepath.Dir(linkPath), targetFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relTarget, linkPath); err != nil {
		t.Fatal(err)
	}

	if err := verifySymlink(linkPath, relTarget); err != nil {
		t.Errorf("verifySymlink should succeed for correct symlink, got: %v", err)
	}
}

func TestVerifySymlink_DoesNotExist(t *testing.T) {
	tmpDir := t.TempDir()
	linkPath := filepath.Join(tmpDir, "nonexistent-link")

	err := verifySymlink(linkPath, "some-target")
	if err == nil {
		t.Fatal("expected error for non-existent symlink, got nil")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error should mention 'does not exist', got: %v", err)
	}
}

func TestVerifySymlink_WrongTarget(t *testing.T) {
	tmpDir := t.TempDir()

	targetFile := filepath.Join(tmpDir, "target.js")
	if err := os.WriteFile(targetFile, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	linkPath := filepath.Join(tmpDir, "link.js")
	if err := os.Symlink("target.js", linkPath); err != nil {
		t.Fatal(err)
	}

	err := verifySymlink(linkPath, "wrong-target.js")
	if err == nil {
		t.Fatal("expected error for wrong symlink target, got nil")
	}
	if !strings.Contains(err.Error(), "wrong target") {
		t.Errorf("error should mention 'wrong target', got: %v", err)
	}
}

func TestVerifySymlink_BrokenLink(t *testing.T) {
	tmpDir := t.TempDir()

	linkPath := filepath.Join(tmpDir, "link.js")
	if err := os.Symlink("deleted-target.js", linkPath); err != nil {
		t.Fatal(err)
	}

	err := verifySymlink(linkPath, "deleted-target.js")
	if err == nil {
		t.Fatal("expected error for broken symlink, got nil")
	}
	if !strings.Contains(err.Error(), "target does not exist") {
		t.Errorf("error should mention 'target does not exist', got: %v", err)
	}
}

func TestVerifySymlink_NotASymlink(t *testing.T) {
	tmpDir := t.TempDir()

	regularFile := filepath.Join(tmpDir, "regular.js")
	if err := os.WriteFile(regularFile, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	err := verifySymlink(regularFile, "some-target")
	if err == nil {
		t.Fatal("expected error for regular file (not symlink), got nil")
	}
	if !strings.Contains(err.Error(), "not a symlink") {
		t.Errorf("error should mention 'not a symlink', got: %v", err)
	}
}

func TestCreateDatamitsuLinks_VerifiesAllLinks(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	eslintDir := filepath.Join(tmpDir, "install", "eslint", "abc")
	prettierDir := filepath.Join(tmpDir, "install", "prettier", "def")

	for _, dir := range []string{eslintDir, prettierDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.WriteFile(filepath.Join(eslintDir, "eslint.js"), []byte("eslint"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prettierDir, "prettier.js"), []byte("prettier"), 0644); err != nil {
		t.Fatal(err)
	}

	apps := binmanager.MapOfApps{
		"eslint":   {Links: map[string]string{"eslint.js": "eslint.js"}},
		"prettier": {Links: map[string]string{"prettier.js": "prettier.js"}},
	}

	resolver := &mockResolver{paths: map[string]string{
		"eslint":   eslintDir,
		"prettier": prettierDir,
	}}

	links, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(links) != 2 {
		t.Fatalf("expected 2 links, got %d", len(links))
	}

	datamitsuDir := filepath.Join(gitRoot, ".datamitsu")
	for _, linkName := range links {
		linkPath := filepath.Join(datamitsuDir, linkName)
		target, err := os.Readlink(linkPath)
		if err != nil {
			t.Fatalf("link %q: readlink failed: %v", linkName, err)
		}
		if err := verifySymlink(linkPath, target); err != nil {
			t.Errorf("link %q: verification failed after creation: %v", linkName, err)
		}
	}

	// Corrupt one symlink by changing its target
	corruptedLinkPath := filepath.Join(datamitsuDir, links[0])
	if err := os.Remove(corruptedLinkPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("corrupted-nonexistent-target", corruptedLinkPath); err != nil {
		t.Fatal(err)
	}

	// verifySymlink should detect the corrupted link
	err = verifySymlink(corruptedLinkPath, "corrupted-nonexistent-target")
	if err == nil {
		t.Error("expected verifySymlink to detect broken target for corrupted link")
	}
}

func TestCreateDatamitsuLinks_BrokenTargetAfterCreation(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	appDir := filepath.Join(tmpDir, "install", "myapp", "hash")

	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}

	targetFile := filepath.Join(appDir, "config.js")
	if err := os.WriteFile(targetFile, []byte("config"), 0644); err != nil {
		t.Fatal(err)
	}

	apps := binmanager.MapOfApps{
		"myapp": {Links: map[string]string{"config.js": "config.js"}},
	}

	resolver := &mockResolver{paths: map[string]string{
		"myapp": appDir,
	}}

	links, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Delete the target file after links were created (simulates external deletion)
	if err := os.Remove(targetFile); err != nil {
		t.Fatal(err)
	}

	// The symlink should now be broken
	linkPath := filepath.Join(gitRoot, ".datamitsu", links[0])
	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("readlink failed: %v", err)
	}

	err = verifySymlink(linkPath, target)
	if err == nil {
		t.Error("expected verifySymlink to detect broken symlink (target deleted)")
	}
	if err != nil && !strings.Contains(err.Error(), "target does not exist") {
		t.Errorf("expected 'target does not exist' error, got: %v", err)
	}
}

func TestCreateDatamitsuLinks_DetectsStaleBrokenLinks(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	appDir := filepath.Join(tmpDir, "install", "newapp", "hash")

	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "config.js"), []byte("new config"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create pre-existing .datamitsu/ with a broken symlink from a previous run
	datamitsuDir := filepath.Join(gitRoot, ".datamitsu")
	if err := os.MkdirAll(datamitsuDir, 0755); err != nil {
		t.Fatal(err)
	}
	brokenLink := filepath.Join(datamitsuDir, "old-stale-link.js")
	if err := os.Symlink("../../nonexistent/old-app/hash/old.js", brokenLink); err != nil {
		t.Fatal(err)
	}

	// Verify the broken symlink exists before calling CreateDatamitsuLinks
	if _, err := os.Lstat(brokenLink); err != nil {
		t.Fatalf("broken symlink should exist before test: %v", err)
	}

	// Call CreateDatamitsuLinks with new config (old link not referenced)
	apps := binmanager.MapOfApps{
		"newapp": {Links: map[string]string{"config.js": "config.js"}},
	}

	resolver := &mockResolver{paths: map[string]string{
		"newapp": appDir,
	}}

	links, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, false)
	if err != nil {
		t.Fatalf("expected no error (stale links should be cleaned up), got: %v", err)
	}

	// Old broken symlink should be gone (removed with .datamitsu/ recreation)
	if _, err := os.Lstat(brokenLink); !os.IsNotExist(err) {
		t.Error("stale broken symlink should have been removed during atomic recreation")
	}

	// New valid symlink should exist
	if len(links) != 1 || links[0] != "config.js" {
		t.Errorf("expected [config.js], got %v", links)
	}

	newLink := filepath.Join(datamitsuDir, "config.js")
	if _, err := os.Readlink(newLink); err != nil {
		t.Errorf("new symlink should exist: %v", err)
	}
}

func TestCreateDatamitsuLinks_EmptyAppsIsNoop(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")

	if _, err := CreateDatamitsuLinks(gitRoot, binmanager.MapOfApps{}, &mockResolver{}, nil, nil, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	datamitsuDir := filepath.Join(gitRoot, ".datamitsu")
	if _, err := os.Stat(datamitsuDir); !os.IsNotExist(err) {
		t.Error(".datamitsu should not exist for empty apps")
	}
}

func TestVerifySymlink_DirectoryTarget(t *testing.T) {
	tmpDir := t.TempDir()

	targetDir := filepath.Join(tmpDir, "target-dir")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatal(err)
	}

	linkPath := filepath.Join(tmpDir, "link-to-dir")
	relTarget, err := filepath.Rel(filepath.Dir(linkPath), targetDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relTarget, linkPath); err != nil {
		t.Fatal(err)
	}

	if err := verifySymlink(linkPath, relTarget); err != nil {
		t.Errorf("verifySymlink should accept directory targets, got: %v", err)
	}
}

func TestCreateDatamitsuLinks_AcceptsDirectoryTarget(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	installDir := filepath.Join(tmpDir, "install", "app", "hash")

	if err := os.MkdirAll(filepath.Join(installDir, "dist"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "dist", "file.js"), []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	apps := binmanager.MapOfApps{
		"app": {
			Links: map[string]string{
				"my-dir": "dist",
			},
		},
	}

	resolver := &mockResolver{
		paths: map[string]string{
			"app": installDir,
		},
	}

	links, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, false)
	if err != nil {
		t.Fatalf("expected no error for directory target, got: %v", err)
	}
	if len(links) != 1 || links[0] != "my-dir" {
		t.Errorf("expected [my-dir], got %v", links)
	}

	linkPath := filepath.Join(gitRoot, ".datamitsu", "my-dir")
	info, err := os.Stat(linkPath)
	if err != nil {
		t.Fatalf("symlink target stat failed: %v", err)
	}
	if !info.IsDir() {
		t.Error("symlink should resolve to a directory")
	}
}

func TestCreateDatamitsuLinks_DryRunValidatesLinkPaths(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	installDir := filepath.Join(tmpDir, "install", "app", "hash")

	if err := os.MkdirAll(installDir, 0755); err != nil {
		t.Fatal(err)
	}

	apps := binmanager.MapOfApps{
		"app": {
			Links: map[string]string{
				"config": "../../../etc/passwd",
			},
		},
	}

	resolver := &mockResolver{
		paths: map[string]string{
			"app": installDir,
		},
	}

	_, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, true)
	if err == nil {
		t.Fatal("expected error for path traversal in dry-run, got nil")
	}
	if !strings.Contains(err.Error(), "escapes install directory") {
		t.Errorf("error should mention path escape, got: %v", err)
	}
}

func TestCreateDatamitsuLinks_DryRunValidatesLinkNames(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	installDir := filepath.Join(tmpDir, "install", "app", "hash")

	if err := os.MkdirAll(installDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "config.js"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}

	apps := binmanager.MapOfApps{
		"app": {
			Links: map[string]string{
				"../../etc/shadow": "config.js",
			},
		},
	}

	resolver := &mockResolver{
		paths: map[string]string{
			"app": installDir,
		},
	}

	_, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, true)
	if err == nil {
		t.Fatal("expected error for path traversal in link name during dry-run, got nil")
	}
	if !strings.Contains(err.Error(), "path traversal") {
		t.Errorf("error should mention 'path traversal', got: %v", err)
	}
}

func TestCreateDatamitsuLinks_DryRunAcceptsDirectoryTarget(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	installDir := filepath.Join(tmpDir, "install", "app", "hash")

	if err := os.MkdirAll(filepath.Join(installDir, "dist"), 0755); err != nil {
		t.Fatal(err)
	}

	apps := binmanager.MapOfApps{
		"app": {
			Links: map[string]string{
				"my-dir": "dist",
			},
		},
	}

	resolver := &mockResolver{
		paths: map[string]string{
			"app": installDir,
		},
	}

	_, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, true)
	if err != nil {
		t.Fatalf("expected no error for directory target in dry-run, got: %v", err)
	}
}

func TestCreateDatamitsuLinks_DirectorySubdirEndToEnd(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	installDir := filepath.Join(tmpDir, "install", "skills-bundle", "hash123")

	skillsDir := filepath.Join(installDir, "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "search.md"), []byte("# Search"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "code.md"), []byte("# Code"), 0644); err != nil {
		t.Fatal(err)
	}

	apps := binmanager.MapOfApps{
		"skills-bundle": {
			Links: map[string]string{
				"skills": "skills",
			},
		},
	}

	resolver := &mockResolver{
		paths: map[string]string{
			"skills-bundle": installDir,
		},
	}

	links, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, false)
	if err != nil {
		t.Fatalf("CreateDatamitsuLinks failed: %v", err)
	}
	if len(links) != 1 || links[0] != "skills" {
		t.Errorf("expected [skills], got %v", links)
	}

	linkedDir := filepath.Join(gitRoot, ".datamitsu", "skills")
	info, err := os.Stat(linkedDir)
	if err != nil {
		t.Fatalf("failed to stat linked directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("linked path should be a directory")
	}

	content, err := os.ReadFile(filepath.Join(linkedDir, "search.md"))
	if err != nil {
		t.Fatalf("failed to read file through directory symlink: %v", err)
	}
	if string(content) != "# Search" {
		t.Errorf("content = %q, want %q", string(content), "# Search")
	}

	content, err = os.ReadFile(filepath.Join(linkedDir, "code.md"))
	if err != nil {
		t.Fatalf("failed to read second file through directory symlink: %v", err)
	}
	if string(content) != "# Code" {
		t.Errorf("content = %q, want %q", string(content), "# Code")
	}
}

func TestCreateDatamitsuLinks_BundleLinks(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	bundleDir := filepath.Join(tmpDir, "bundles", "my-bundle", "hash")

	if err := os.MkdirAll(bundleDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "agents.md"), []byte("# Agents"), 0644); err != nil {
		t.Fatal(err)
	}

	bundles := binmanager.MapOfBundles{
		"my-bundle": {
			Version: "1.0",
			Files:   map[string]string{"agents.md": "# Agents"},
			Links:   map[string]string{"agents": "agents.md"},
		},
	}

	bundleResolver := &mockResolver{
		paths: map[string]string{"my-bundle": bundleDir},
	}

	links, err := CreateDatamitsuLinks(gitRoot, binmanager.MapOfApps{}, nil, bundles, bundleResolver, false)
	if err != nil {
		t.Fatalf("CreateDatamitsuLinks failed: %v", err)
	}

	if len(links) != 1 || links[0] != "agents" {
		t.Errorf("expected [agents], got %v", links)
	}

	linkPath := filepath.Join(gitRoot, ".datamitsu", "agents")
	content, err := os.ReadFile(linkPath)
	if err != nil {
		t.Fatalf("failed to read through symlink: %v", err)
	}
	if string(content) != "# Agents" {
		t.Errorf("content = %q, want %q", string(content), "# Agents")
	}
}

func TestCreateDatamitsuLinks_MixedAppAndBundleLinks(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	appDir := filepath.Join(tmpDir, "install", "eslint", "abc")
	bundleDir := filepath.Join(tmpDir, "bundles", "skills", "def")

	for _, dir := range []string{appDir, bundleDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(appDir, "eslint.js"), []byte("eslint"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "skills.md"), []byte("# Skills"), 0644); err != nil {
		t.Fatal(err)
	}

	apps := binmanager.MapOfApps{
		"eslint": {
			Links: map[string]string{"eslint-config": "eslint.js"},
		},
	}
	bundles := binmanager.MapOfBundles{
		"skills": {
			Version: "1.0",
			Files:   map[string]string{"skills.md": "# Skills"},
			Links:   map[string]string{"skills-md": "skills.md"},
		},
	}

	appResolver := &mockResolver{paths: map[string]string{"eslint": appDir}}
	bundleResolver := &mockResolver{paths: map[string]string{"skills": bundleDir}}

	links, err := CreateDatamitsuLinks(gitRoot, apps, appResolver, bundles, bundleResolver, false)
	if err != nil {
		t.Fatalf("CreateDatamitsuLinks failed: %v", err)
	}

	if len(links) != 2 {
		t.Fatalf("expected 2 links, got %d", len(links))
	}

	for _, name := range []string{"eslint-config", "skills-md"} {
		linkPath := filepath.Join(gitRoot, ".datamitsu", name)
		if _, err := os.Readlink(linkPath); err != nil {
			t.Errorf("symlink %q not created: %v", name, err)
		}
	}
}

func TestCreateDatamitsuLinks_BundleDirectoryLink(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	bundleDir := filepath.Join(tmpDir, "bundles", "agent-skills", "hash")

	skillsDir := filepath.Join(bundleDir, "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "search.md"), []byte("# Search"), 0644); err != nil {
		t.Fatal(err)
	}

	bundles := binmanager.MapOfBundles{
		"agent-skills": {
			Version: "1.0",
			Links:   map[string]string{"agent-skills": "skills"},
		},
	}

	bundleResolver := &mockResolver{
		paths: map[string]string{"agent-skills": bundleDir},
	}

	links, err := CreateDatamitsuLinks(gitRoot, binmanager.MapOfApps{}, nil, bundles, bundleResolver, false)
	if err != nil {
		t.Fatalf("CreateDatamitsuLinks failed: %v", err)
	}

	if len(links) != 1 || links[0] != "agent-skills" {
		t.Errorf("expected [agent-skills], got %v", links)
	}

	linkedDir := filepath.Join(gitRoot, ".datamitsu", "agent-skills")
	info, err := os.Stat(linkedDir)
	if err != nil {
		t.Fatalf("failed to stat linked directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("symlink should resolve to a directory")
	}

	content, err := os.ReadFile(filepath.Join(linkedDir, "search.md"))
	if err != nil {
		t.Fatalf("failed to read file through directory symlink: %v", err)
	}
	if string(content) != "# Search" {
		t.Errorf("content = %q, want %q", string(content), "# Search")
	}
}

func TestCreateDatamitsuLinks_GitignoreCreated(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	installDir := filepath.Join(tmpDir, "install", "eslint", "abc123")

	if err := os.MkdirAll(installDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "eslint.js"), []byte("module.exports = {};"), 0644); err != nil {
		t.Fatal(err)
	}

	apps := binmanager.MapOfApps{
		"eslint": {
			Links: map[string]string{
				"eslint.js": "eslint.js",
			},
		},
	}

	resolver := &mockResolver{
		paths: map[string]string{
			"eslint": installDir,
		},
	}

	if _, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, false); err != nil {
		t.Fatalf("CreateDatamitsuLinks failed: %v", err)
	}

	gitignorePath := filepath.Join(gitRoot, ".datamitsu", ".gitignore")
	content, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf(".datamitsu/.gitignore not created: %v", err)
	}
	if string(content) != "*\n" {
		t.Errorf(".gitignore content = %q, want %q", string(content), "*\n")
	}
}

func TestCreateDatamitsuLinks_DryRunNoGitignore(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	installDir := filepath.Join(tmpDir, "install", "eslint", "hash")

	if err := os.MkdirAll(installDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "eslint.js"), []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	apps := binmanager.MapOfApps{
		"eslint": {
			Links: map[string]string{"eslint.js": "eslint.js"},
		},
	}

	resolver := &mockResolver{
		paths: map[string]string{
			"eslint": installDir,
		},
	}

	if _, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, true); err != nil {
		t.Fatalf("dry run failed: %v", err)
	}

	datamitsuDir := filepath.Join(gitRoot, ".datamitsu")
	if _, err := os.Stat(datamitsuDir); !os.IsNotExist(err) {
		t.Errorf(".datamitsu directory should not exist in dry-run mode")
	}

	gitignorePath := filepath.Join(datamitsuDir, ".gitignore")
	if _, err := os.Stat(gitignorePath); !os.IsNotExist(err) {
		t.Errorf(".datamitsu/.gitignore should not exist in dry-run mode")
	}
}

func TestCreateDatamitsuGitignore_Success(t *testing.T) {
	dir := t.TempDir()

	if err := createDatamitsuGitignore(dir); err != nil {
		t.Fatalf("createDatamitsuGitignore failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("failed to read .gitignore: %v", err)
	}
	if string(content) != "*\n" {
		t.Errorf(".gitignore content = %q, want %q", string(content), "*\n")
	}
}

func TestCreateDatamitsuLinks_RejectsGitignoreLinkName(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	installDir := filepath.Join(tmpDir, "install", "app", "hash")

	_ = os.MkdirAll(installDir, 0755)
	_ = os.WriteFile(filepath.Join(installDir, "config.js"), []byte("content"), 0644)

	apps := binmanager.MapOfApps{
		"myapp": {
			Links: map[string]string{
				".gitignore": "config.js",
			},
		},
	}

	resolver := &mockResolver{
		paths: map[string]string{
			"myapp": installDir,
		},
	}

	_, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, false)
	if err == nil {
		t.Fatal("expected error for reserved .gitignore link name, got nil")
	}
	if !strings.Contains(err.Error(), "reserved for internal use") {
		t.Errorf("error should mention reserved, got: %v", err)
	}
}

func TestCreateDatamitsuLinks_RejectsGitignoreLinkNameDryRun(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	installDir := filepath.Join(tmpDir, "install", "app", "hash")

	_ = os.MkdirAll(installDir, 0755)
	_ = os.WriteFile(filepath.Join(installDir, "config.js"), []byte("content"), 0644)

	apps := binmanager.MapOfApps{
		"myapp": {
			Links: map[string]string{
				".gitignore": "config.js",
			},
		},
	}

	resolver := &mockResolver{
		paths: map[string]string{
			"myapp": installDir,
		},
	}

	_, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, true)
	if err == nil {
		t.Fatal("expected error for reserved .gitignore link name in dry-run, got nil")
	}
	if !strings.Contains(err.Error(), "reserved for internal use") {
		t.Errorf("error should mention reserved, got: %v", err)
	}
}

func TestCreateDatamitsuGitignore_ErrorOnInvalidDir(t *testing.T) {
	err := createDatamitsuGitignore("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("expected error when writing to nonexistent directory, got nil")
	}
}

func TestWriteTypeDefinitions_Success(t *testing.T) {
	dir := t.TempDir()
	if err := writeTypeDefinitions(dir); err != nil {
		t.Fatalf("writeTypeDefinitions() returned unexpected error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "datamitsu.config.d.ts"))
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}

	expected := config.GetDefaultConfigDTS()
	if string(content) != expected {
		t.Errorf("written content does not match embedded type definitions\ngot length: %d\nexpected length: %d", len(content), len(expected))
	}
}

func TestWriteTypeDefinitions_ErrorOnInvalidDir(t *testing.T) {
	err := writeTypeDefinitions("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("expected error when writing to nonexistent directory, got nil")
	}
}

func TestWriteTypeDefinitions_FilePermissions(t *testing.T) {
	dir := t.TempDir()
	if err := writeTypeDefinitions(dir); err != nil {
		t.Fatalf("writeTypeDefinitions() returned unexpected error: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "datamitsu.config.d.ts"))
	if err != nil {
		t.Fatalf("failed to stat file: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0644 {
		t.Errorf("file permissions = %o, want 0644", perm)
	}
}

func TestCreateDatamitsuLinks_WritesTypeDefinitions(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(gitRoot, 0755); err != nil {
		t.Fatal(err)
	}

	appDir := filepath.Join(tmpDir, "apps", "eslint")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "config.js"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	apps := binmanager.MapOfApps{
		"eslint": binmanager.App{
			Links: map[string]string{"eslint-config": "config.js"},
		},
	}
	resolver := &mockResolver{paths: map[string]string{"eslint": appDir}}

	_, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, false)
	if err != nil {
		t.Fatalf("CreateDatamitsuLinks() returned unexpected error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(gitRoot, ".datamitsu", "datamitsu.config.d.ts"))
	if err != nil {
		t.Fatalf("datamitsu.config.d.ts not found in .datamitsu: %v", err)
	}

	expected := config.GetDefaultConfigDTS()
	if string(content) != expected {
		t.Error("datamitsu.config.d.ts content does not match embedded type definitions")
	}
}

func TestCreateDatamitsuLinks_RejectsReservedTypeDefinitionsName(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(gitRoot, 0755); err != nil {
		t.Fatal(err)
	}

	appDir := filepath.Join(tmpDir, "apps", "myapp")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "file.js"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	apps := binmanager.MapOfApps{
		"myapp": binmanager.App{
			Links: map[string]string{"datamitsu.config.d.ts": "file.js"},
		},
	}
	resolver := &mockResolver{paths: map[string]string{"myapp": appDir}}

	_, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, false)
	if err == nil {
		t.Fatal("expected error for reserved datamitsu.config.d.ts link name, got nil")
	}
	if !strings.Contains(err.Error(), "reserved for internal use") {
		t.Errorf("error should mention reserved, got: %v", err)
	}
}

func TestCreateDatamitsuLinks_RejectsReservedTypeDefinitionsNameDryRun(t *testing.T) {
	tmpDir := t.TempDir()
	gitRoot := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(gitRoot, 0755); err != nil {
		t.Fatal(err)
	}

	appDir := filepath.Join(tmpDir, "apps", "myapp")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "file.js"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	apps := binmanager.MapOfApps{
		"myapp": binmanager.App{
			Links: map[string]string{"datamitsu.config.d.ts": "file.js"},
		},
	}
	resolver := &mockResolver{paths: map[string]string{"myapp": appDir}}

	_, err := CreateDatamitsuLinks(gitRoot, apps, resolver, nil, nil, true)
	if err == nil {
		t.Fatal("expected error for reserved datamitsu.config.d.ts link name in dry-run, got nil")
	}
	if !strings.Contains(err.Error(), "reserved for internal use") {
		t.Errorf("error should mention reserved, got: %v", err)
	}
}

func TestCreateDatamitsuTypeDefinitions(t *testing.T) {
	gitRoot := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(gitRoot, 0755); err != nil {
		t.Fatal(err)
	}

	err := CreateDatamitsuTypeDefinitions(gitRoot, false)
	if err != nil {
		t.Fatalf("CreateDatamitsuTypeDefinitions() returned unexpected error: %v", err)
	}

	// Verify .gitignore exists
	gitignoreContent, err := os.ReadFile(filepath.Join(gitRoot, ".datamitsu", ".gitignore"))
	if err != nil {
		t.Fatalf(".gitignore not found: %v", err)
	}
	if string(gitignoreContent) != "*\n" {
		t.Errorf(".gitignore content = %q, want %q", string(gitignoreContent), "*\n")
	}

	// Verify type definitions file exists with correct content
	dtsContent, err := os.ReadFile(filepath.Join(gitRoot, ".datamitsu", "datamitsu.config.d.ts"))
	if err != nil {
		t.Fatalf("datamitsu.config.d.ts not found: %v", err)
	}
	expected := config.GetDefaultConfigDTS()
	if string(dtsContent) != expected {
		t.Error("datamitsu.config.d.ts content does not match embedded type definitions")
	}
}

func TestCreateDatamitsuTypeDefinitions_ReplacesExisting(t *testing.T) {
	gitRoot := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(gitRoot, 0755); err != nil {
		t.Fatal(err)
	}

	// Create an existing .datamitsu with stale content
	datamitsuDir := filepath.Join(gitRoot, ".datamitsu")
	if err := os.MkdirAll(datamitsuDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(datamitsuDir, "datamitsu.config.d.ts"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(datamitsuDir, "stale-symlink"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	err := CreateDatamitsuTypeDefinitions(gitRoot, false)
	if err != nil {
		t.Fatalf("CreateDatamitsuTypeDefinitions() returned unexpected error: %v", err)
	}

	// Stale files should be removed
	if _, err := os.Stat(filepath.Join(datamitsuDir, "stale-symlink")); !os.IsNotExist(err) {
		t.Error("stale-symlink should have been removed")
	}

	// Type definitions should be fresh
	content, err := os.ReadFile(filepath.Join(datamitsuDir, "datamitsu.config.d.ts"))
	if err != nil {
		t.Fatalf("datamitsu.config.d.ts not found: %v", err)
	}
	if string(content) == "old" {
		t.Error("datamitsu.config.d.ts was not overwritten")
	}
}

func TestCreateDatamitsuTypeDefinitions_DryRun(t *testing.T) {
	gitRoot := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(gitRoot, 0755); err != nil {
		t.Fatal(err)
	}

	err := CreateDatamitsuTypeDefinitions(gitRoot, true)
	if err != nil {
		t.Fatalf("CreateDatamitsuTypeDefinitions() dry-run returned unexpected error: %v", err)
	}

	// .datamitsu should not exist in dry-run
	if _, err := os.Stat(filepath.Join(gitRoot, ".datamitsu")); !os.IsNotExist(err) {
		t.Error(".datamitsu directory should not be created in dry-run mode")
	}
}

