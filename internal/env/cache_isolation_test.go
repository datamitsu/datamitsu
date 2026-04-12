package env

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolCacheIsolation_DifferentToolsGetIsolatedDirs(t *testing.T) {
	originalEnv, wasSet := os.LookupEnv(cacheDir.Name)
	defer func() {
		if wasSet {
			if err := os.Setenv(cacheDir.Name, originalEnv); err != nil {
				t.Errorf("failed to restore env: %v", err)
			}
		} else {
			if err := os.Unsetenv(cacheDir.Name); err != nil {
				t.Errorf("failed to unset env: %v", err)
			}
		}
	}()

	tmpDir, err := os.MkdirTemp("", "toolcache-isolation-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	if err := os.Setenv(cacheDir.Name, tmpDir); err != nil {
		t.Fatalf("failed to set env: %v", err)
	}

	gitRoot := "/home/user/myproject"

	tscPath, err := GetProjectCachePath(gitRoot, "packages/frontend", "tsc")
	if err != nil {
		t.Fatalf("GetProjectCachePath(tsc) error: %v", err)
	}

	eslintPath, err := GetProjectCachePath(gitRoot, "packages/frontend", "eslint")
	if err != nil {
		t.Fatalf("GetProjectCachePath(eslint) error: %v", err)
	}

	if tscPath == eslintPath {
		t.Fatalf("two tools got same cache path: %q", tscPath)
	}

	// Create directories and write files to verify no conflicts
	if err := os.MkdirAll(tscPath, 0755); err != nil {
		t.Fatalf("failed to create tsc cache dir: %v", err)
	}
	if err := os.MkdirAll(eslintPath, 0755); err != nil {
		t.Fatalf("failed to create eslint cache dir: %v", err)
	}

	// Write identically-named files in each tool's cache
	tscFile := filepath.Join(tscPath, "cache.json")
	eslintFile := filepath.Join(eslintPath, "cache.json")

	if err := os.WriteFile(tscFile, []byte(`{"tool":"tsc"}`), 0644); err != nil {
		t.Fatalf("failed to write tsc cache file: %v", err)
	}
	if err := os.WriteFile(eslintFile, []byte(`{"tool":"eslint"}`), 0644); err != nil {
		t.Fatalf("failed to write eslint cache file: %v", err)
	}

	// Verify files are independent
	tscContent, err := os.ReadFile(tscFile)
	if err != nil {
		t.Fatalf("failed to read tsc cache file: %v", err)
	}
	eslintContent, err := os.ReadFile(eslintFile)
	if err != nil {
		t.Fatalf("failed to read eslint cache file: %v", err)
	}

	if string(tscContent) != `{"tool":"tsc"}` {
		t.Errorf("tsc cache file corrupted: got %q", string(tscContent))
	}
	if string(eslintContent) != `{"tool":"eslint"}` {
		t.Errorf("eslint cache file corrupted: got %q", string(eslintContent))
	}

	// Verify path structure contains tool name
	if !strings.HasSuffix(tscPath, filepath.Join("packages", "frontend", "tsc")) {
		t.Errorf("tsc cache path should end with packages/frontend/tsc, got %q", tscPath)
	}
	if !strings.HasSuffix(eslintPath, filepath.Join("packages", "frontend", "eslint")) {
		t.Errorf("eslint cache path should end with packages/frontend/eslint, got %q", eslintPath)
	}
}

func TestToolCacheIsolation_MonorepoSameToolDifferentProjects(t *testing.T) {
	originalEnv, wasSet := os.LookupEnv(cacheDir.Name)
	defer func() {
		if wasSet {
			if err := os.Setenv(cacheDir.Name, originalEnv); err != nil {
				t.Errorf("failed to restore env: %v", err)
			}
		} else {
			if err := os.Unsetenv(cacheDir.Name); err != nil {
				t.Errorf("failed to unset env: %v", err)
			}
		}
	}()

	tmpDir, err := os.MkdirTemp("", "toolcache-monorepo-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	if err := os.Setenv(cacheDir.Name, tmpDir); err != nil {
		t.Fatalf("failed to set env: %v", err)
	}

	gitRoot := "/home/user/monorepo"

	// Same tool (tsc) in two different projects
	frontendPath, err := GetProjectCachePath(gitRoot, "packages/frontend", "tsc")
	if err != nil {
		t.Fatalf("GetProjectCachePath(frontend/tsc) error: %v", err)
	}

	backendPath, err := GetProjectCachePath(gitRoot, "packages/backend", "tsc")
	if err != nil {
		t.Fatalf("GetProjectCachePath(backend/tsc) error: %v", err)
	}

	// Root-level tool (no project subpath)
	rootToolPath, err := GetProjectCachePath(gitRoot, "", "golangci-lint")
	if err != nil {
		t.Fatalf("GetProjectCachePath(root/golangci-lint) error: %v", err)
	}

	// All paths must be unique
	paths := map[string]string{
		"frontend/tsc":      frontendPath,
		"backend/tsc":       backendPath,
		"root/golangci-lint": rootToolPath,
	}
	seen := make(map[string]string)
	for label, p := range paths {
		if prev, ok := seen[p]; ok {
			t.Fatalf("path collision: %q and %q both resolved to %q", prev, label, p)
		}
		seen[p] = label
	}

	// Create directories and write files
	for label, p := range paths {
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatalf("failed to create dir for %s: %v", label, err)
		}
		cacheFile := filepath.Join(p, "tsbuildinfo")
		if err := os.WriteFile(cacheFile, []byte(label), 0644); err != nil {
			t.Fatalf("failed to write cache file for %s: %v", label, err)
		}
	}

	// Verify each file has correct content (no cross-project contamination)
	for label, p := range paths {
		cacheFile := filepath.Join(p, "tsbuildinfo")
		content, err := os.ReadFile(cacheFile)
		if err != nil {
			t.Fatalf("failed to read cache file for %s: %v", label, err)
		}
		if string(content) != label {
			t.Errorf("cache file for %s has wrong content: got %q, want %q", label, string(content), label)
		}
	}

	// Verify path structure
	if !strings.HasSuffix(frontendPath, filepath.Join("cache", "packages", "frontend", "tsc")) {
		t.Errorf("frontend path should end with cache/packages/frontend/tsc, got %q", frontendPath)
	}
	if !strings.HasSuffix(backendPath, filepath.Join("cache", "packages", "backend", "tsc")) {
		t.Errorf("backend path should end with cache/packages/backend/tsc, got %q", backendPath)
	}
	if !strings.HasSuffix(rootToolPath, filepath.Join("cache", "golangci-lint")) {
		t.Errorf("root tool path should end with cache/golangci-lint, got %q", rootToolPath)
	}
}

func TestToolCacheIsolation_CacheClearingWithNewStructure(t *testing.T) {
	originalEnv, wasSet := os.LookupEnv(cacheDir.Name)
	defer func() {
		if wasSet {
			if err := os.Setenv(cacheDir.Name, originalEnv); err != nil {
				t.Errorf("failed to restore env: %v", err)
			}
		} else {
			if err := os.Unsetenv(cacheDir.Name); err != nil {
				t.Errorf("failed to unset env: %v", err)
			}
		}
	}()

	tmpDir, err := os.MkdirTemp("", "toolcache-clear-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	if err := os.Setenv(cacheDir.Name, tmpDir); err != nil {
		t.Fatalf("failed to set env: %v", err)
	}

	gitRoot := "/home/user/monorepo"

	// Create cache structure for multiple projects and tools
	type cacheEntry struct {
		project  string
		tool     string
		filename string
		content  string
	}

	entries := []cacheEntry{
		{"packages/frontend", "tsc", "tsbuildinfo", "frontend-tsc"},
		{"packages/frontend", "eslint", ".eslintcache", "frontend-eslint"},
		{"packages/backend", "tsc", "tsbuildinfo", "backend-tsc"},
		{"packages/backend", "eslint", ".eslintcache", "backend-eslint"},
		{"", "golangci-lint", "cache.json", "root-golangci"},
	}

	// Create all cache files
	for _, e := range entries {
		cachePath, err := GetProjectCachePath(gitRoot, e.project, e.tool)
		if err != nil {
			t.Fatalf("GetProjectCachePath(%s, %s) error: %v", e.project, e.tool, err)
		}
		if err := os.MkdirAll(cachePath, 0755); err != nil {
			t.Fatalf("failed to create cache dir: %v", err)
		}
		filePath := filepath.Join(cachePath, e.filename)
		if err := os.WriteFile(filePath, []byte(e.content), 0644); err != nil {
			t.Fatalf("failed to write cache file: %v", err)
		}
	}

	// Verify all files exist
	for _, e := range entries {
		cachePath, _ := GetProjectCachePath(gitRoot, e.project, e.tool)
		filePath := filepath.Join(cachePath, e.filename)
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			t.Fatalf("cache file should exist: %s", filePath)
		}
	}

	// Test per-project clearing: remove packages/frontend cache
	frontendCachePath, _ := GetProjectCachePath(gitRoot, "packages/frontend", "tsc")
	frontendProjectDir := filepath.Dir(frontendCachePath) // up one level from tsc to frontend
	if err := os.RemoveAll(frontendProjectDir); err != nil {
		t.Fatalf("failed to clear frontend cache: %v", err)
	}

	// Frontend caches should be gone
	for _, e := range entries {
		cachePath, _ := GetProjectCachePath(gitRoot, e.project, e.tool)
		filePath := filepath.Join(cachePath, e.filename)
		_, err := os.Stat(filePath)

		if e.project == "packages/frontend" {
			if !os.IsNotExist(err) {
				t.Errorf("frontend cache file should be gone: %s", filePath)
			}
		} else {
			if os.IsNotExist(err) {
				t.Errorf("non-frontend cache file should still exist: %s", filePath)
			}
		}
	}

	// Test per-tool clearing: find and remove all "eslint" dirs
	projectHash := HashProjectPath(gitRoot)
	cacheBase := filepath.Join(tmpDir, "cache", "projects", projectHash, "cache")

	err = filepath.Walk(cacheBase, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && info.Name() == "eslint" {
			if removeErr := os.RemoveAll(path); removeErr != nil {
				return removeErr
			}
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		t.Fatalf("failed to walk cache for eslint clearing: %v", err)
	}

	// Backend eslint should be gone, backend tsc should remain
	backendEslintPath, _ := GetProjectCachePath(gitRoot, "packages/backend", "eslint")
	if _, err := os.Stat(filepath.Join(backendEslintPath, ".eslintcache")); !os.IsNotExist(err) {
		t.Errorf("backend eslint cache should be gone after per-tool clear")
	}

	backendTscPath, _ := GetProjectCachePath(gitRoot, "packages/backend", "tsc")
	if _, err := os.Stat(filepath.Join(backendTscPath, "tsbuildinfo")); os.IsNotExist(err) {
		t.Errorf("backend tsc cache should still exist after eslint clear")
	}

	// Root golangci-lint should still exist
	rootPath, _ := GetProjectCachePath(gitRoot, "", "golangci-lint")
	if _, err := os.Stat(filepath.Join(rootPath, "cache.json")); os.IsNotExist(err) {
		t.Errorf("root golangci-lint cache should still exist")
	}

	// Test full project clearing: remove entire cache dir
	if err := os.RemoveAll(cacheBase); err != nil {
		t.Fatalf("failed to clear entire cache: %v", err)
	}

	// All should be gone
	for _, e := range entries {
		cachePath, _ := GetProjectCachePath(gitRoot, e.project, e.tool)
		filePath := filepath.Join(cachePath, e.filename)
		if _, err := os.Stat(filePath); !os.IsNotExist(err) {
			t.Errorf("all cache files should be gone after full clear: %s", filePath)
		}
	}
}
