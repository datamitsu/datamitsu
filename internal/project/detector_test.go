package project

import (
	"context"
	"github.com/datamitsu/datamitsu/internal/config"
	"os"
	"path/filepath"
	"testing"
)

func TestNewDetector(t *testing.T) {
	tmpDir := t.TempDir()
	types := config.MapOfProjectTypes{
		"test": config.ProjectType{
			Markers: []string{"test.txt"},
		},
	}

	detector := NewDetector(tmpDir, types)

	if detector == nil {
		t.Fatal("NewDetector() returned nil")
	}

	if detector.rootPath != tmpDir {
		t.Errorf("rootPath = %q, want %q", detector.rootPath, tmpDir)
	}

	if len(detector.types) != 1 {
		t.Errorf("len(types) = %d, want 1", len(detector.types))
	}
}

func TestDetectAll(t *testing.T) {
	tmpDir := t.TempDir()

	_ = os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte("{}"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(""), 0644)

	types := config.MapOfProjectTypes{
		"node": config.ProjectType{
			Markers: []string{"package.json"},
		},
		"go": config.ProjectType{
			Markers: []string{"go.mod"},
		},
		"rust": config.ProjectType{
			Markers: []string{"Cargo.toml"},
		},
	}

	detector := NewDetector(tmpDir, types)
	ctx := context.Background()

	detected, err := detector.DetectAll(ctx)
	if err != nil {
		t.Fatalf("DetectAll() error = %v", err)
	}

	if len(detected) != 2 {
		t.Errorf("len(detected) = %d, want 2", len(detected))
	}

	hasNode := false
	hasGo := false
	for _, name := range detected {
		if name == "node" {
			hasNode = true
		}
		if name == "go" {
			hasGo = true
		}
	}

	if !hasNode {
		t.Error("node project type not detected")
	}
	if !hasGo {
		t.Error("go project type not detected")
	}
}

func TestDetectAllNoMatches(t *testing.T) {
	tmpDir := t.TempDir()

	types := config.MapOfProjectTypes{
		"node": config.ProjectType{
			Markers: []string{"package.json"},
		},
	}

	detector := NewDetector(tmpDir, types)
	ctx := context.Background()

	detected, err := detector.DetectAll(ctx)
	if err != nil {
		t.Fatalf("DetectAll() error = %v", err)
	}

	if len(detected) != 0 {
		t.Errorf("len(detected) = %d, want 0", len(detected))
	}
}

func TestIsType(t *testing.T) {
	tmpDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte("{}"), 0644)

	types := config.MapOfProjectTypes{
		"node": config.ProjectType{
			Markers: []string{"package.json"},
		},
		"rust": config.ProjectType{
			Markers: []string{"Cargo.toml"},
		},
	}

	detector := NewDetector(tmpDir, types)
	ctx := context.Background()

	t.Run("type exists and matches", func(t *testing.T) {
		isNode, err := detector.IsType(ctx, "node")
		if err != nil {
			t.Fatalf("IsType() error = %v", err)
		}
		if !isNode {
			t.Error("IsType(node) = false, want true")
		}
	})

	t.Run("type exists but doesn't match", func(t *testing.T) {
		isRust, err := detector.IsType(ctx, "rust")
		if err != nil {
			t.Fatalf("IsType() error = %v", err)
		}
		if isRust {
			t.Error("IsType(rust) = true, want false")
		}
	})

	t.Run("type doesn't exist", func(t *testing.T) {
		isUnknown, err := detector.IsType(ctx, "unknown")
		if err != nil {
			t.Fatalf("IsType() error = %v", err)
		}
		if isUnknown {
			t.Error("IsType(unknown) = true, want false")
		}
	})
}

func TestMatchesTypeWithGlob(t *testing.T) {
	tmpDir := t.TempDir()

	_ = os.WriteFile(filepath.Join(tmpDir, "test1.txt"), []byte(""), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "test2.txt"), []byte(""), 0644)

	types := config.MapOfProjectTypes{
		"test": config.ProjectType{
			Markers: []string{"*.txt"},
		},
	}

	detector := NewDetector(tmpDir, types)
	ctx := context.Background()

	detected, err := detector.DetectAll(ctx)
	if err != nil {
		t.Fatalf("DetectAll() error = %v", err)
	}

	if len(detected) != 1 || detected[0] != "test" {
		t.Error("glob pattern did not match")
	}
}

func TestMatchesTypeMultipleMarkers(t *testing.T) {
	tmpDir := t.TempDir()

	_ = os.WriteFile(filepath.Join(tmpDir, "marker2.txt"), []byte(""), 0644)

	types := config.MapOfProjectTypes{
		"test": config.ProjectType{
			Markers: []string{"marker1.txt", "marker2.txt", "marker3.txt"},
		},
	}

	detector := NewDetector(tmpDir, types)
	ctx := context.Background()

	isTest, err := detector.IsType(ctx, "test")
	if err != nil {
		t.Fatalf("IsType() error = %v", err)
	}

	if !isTest {
		t.Error("should match when at least one marker exists")
	}
}

func TestMatchesTypeWithDoublestar(t *testing.T) {
	tmpDir := t.TempDir()

	// Create nested directory structure
	subDir := filepath.Join(tmpDir, "subdir", "nested")
	_ = os.MkdirAll(subDir, 0755)

	// Create package.json in nested directory
	_ = os.WriteFile(filepath.Join(subDir, "package.json"), []byte("{}"), 0644)

	types := config.MapOfProjectTypes{
		"npm-package": config.ProjectType{
			Description: "npm package",
			Markers:     []string{"**/package.json"},
		},
	}

	detector := NewDetector(tmpDir, types)
	ctx := context.Background()

	detected, err := detector.DetectAll(ctx)
	if err != nil {
		t.Fatalf("DetectAll() error = %v", err)
	}

	if len(detected) != 1 || detected[0] != "npm-package" {
		t.Errorf("doublestar pattern did not match nested file, detected: %v", detected)
	}
}

func TestMatchesTypeWithDoublestarMultipleLevels(t *testing.T) {
	tmpDir := t.TempDir()

	// Create files at different levels
	_ = os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(""), 0644)

	subDir1 := filepath.Join(tmpDir, "pkg1")
	_ = os.MkdirAll(subDir1, 0755)
	_ = os.WriteFile(filepath.Join(subDir1, "go.mod"), []byte(""), 0644)

	subDir2 := filepath.Join(tmpDir, "pkg2", "nested")
	_ = os.MkdirAll(subDir2, 0755)
	_ = os.WriteFile(filepath.Join(subDir2, "go.mod"), []byte(""), 0644)

	types := config.MapOfProjectTypes{
		"golang-package": config.ProjectType{
			Description: "Go module",
			Markers:     []string{"**/go.mod"},
		},
	}

	detector := NewDetector(tmpDir, types)
	ctx := context.Background()

	isGo, err := detector.IsType(ctx, "golang-package")
	if err != nil {
		t.Fatalf("IsType() error = %v", err)
	}

	if !isGo {
		t.Error("should match go.mod files at any depth with ** pattern")
	}
}

func TestDetectAllWithLocations(t *testing.T) {
	tmpDir := t.TempDir()

	// Create monorepo structure
	_ = os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte("{}"), 0644)

	pkg1 := filepath.Join(tmpDir, "packages", "frontend")
	_ = os.MkdirAll(pkg1, 0755)
	_ = os.WriteFile(filepath.Join(pkg1, "package.json"), []byte("{}"), 0644)

	pkg2 := filepath.Join(tmpDir, "packages", "backend")
	_ = os.MkdirAll(pkg2, 0755)
	_ = os.WriteFile(filepath.Join(pkg2, "package.json"), []byte("{}"), 0644)
	_ = os.WriteFile(filepath.Join(pkg2, "go.mod"), []byte(""), 0644)

	types := config.MapOfProjectTypes{
		"npm-package": config.ProjectType{
			Description: "npm package",
			Markers:     []string{"**/package.json"},
		},
		"golang-package": config.ProjectType{
			Description: "Go module",
			Markers:     []string{"**/go.mod"},
		},
	}

	detector := NewDetector(tmpDir, types)
	ctx := context.Background()

	locations, err := detector.DetectAllWithLocations(ctx)
	if err != nil {
		t.Fatalf("DetectAllWithLocations() error = %v", err)
	}

	// Should find 3 npm-package locations and 1 golang-package location
	npmCount := 0
	goCount := 0
	foundPaths := make(map[string]bool)

	for _, loc := range locations {
		foundPaths[loc.Path] = true
		if loc.Type == "npm-package" {
			npmCount++
		}
		if loc.Type == "golang-package" {
			goCount++
		}
	}

	if npmCount != 3 {
		t.Errorf("found %d npm-package locations, want 3", npmCount)
	}

	if goCount != 1 {
		t.Errorf("found %d golang-package locations, want 1", goCount)
	}

	// Verify specific paths were found
	expectedPaths := []string{
		tmpDir,
		pkg1,
		pkg2,
	}

	for _, expectedPath := range expectedPaths {
		if !foundPaths[expectedPath] {
			t.Errorf("expected to find path %s", expectedPath)
		}
	}
}

func TestDetectAllWithLocationsRespectsGitignore(t *testing.T) {
	tmpDir := t.TempDir()

	// Create .gitignore
	gitignoreContent := `node_modules/
dist/
`
	_ = os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(gitignoreContent), 0644)

	// Create package.json files in various locations
	_ = os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte("{}"), 0644)

	// Valid location (not gitignored)
	validPkg := filepath.Join(tmpDir, "packages", "frontend")
	_ = os.MkdirAll(validPkg, 0755)
	_ = os.WriteFile(filepath.Join(validPkg, "package.json"), []byte("{}"), 0644)

	// Ignored locations
	nodeModules := filepath.Join(tmpDir, "node_modules", "some-package")
	_ = os.MkdirAll(nodeModules, 0755)
	_ = os.WriteFile(filepath.Join(nodeModules, "package.json"), []byte("{}"), 0644)

	distDir := filepath.Join(tmpDir, "dist", "build")
	_ = os.MkdirAll(distDir, 0755)
	_ = os.WriteFile(filepath.Join(distDir, "package.json"), []byte("{}"), 0644)

	types := config.MapOfProjectTypes{
		"npm-package": config.ProjectType{
			Description: "npm package",
			Markers:     []string{"**/package.json"},
		},
	}

	detector := NewDetector(tmpDir, types)
	ctx := context.Background()

	locations, err := detector.DetectAllWithLocations(ctx)
	if err != nil {
		t.Fatalf("DetectAllWithLocations() error = %v", err)
	}

	// Should find only 2 locations (root and packages/frontend)
	// Should NOT find node_modules/some-package and dist/build
	if len(locations) != 2 {
		t.Errorf("found %d locations, want 2", len(locations))
		for _, loc := range locations {
			t.Logf("  found: %s (%s)", loc.Path, loc.Type)
		}
	}

	// Verify correct paths were found
	foundPaths := make(map[string]bool)
	for _, loc := range locations {
		foundPaths[loc.Path] = true
	}

	expectedPaths := []string{tmpDir, validPkg}
	for _, expectedPath := range expectedPaths {
		if !foundPaths[expectedPath] {
			t.Errorf("expected to find path %s", expectedPath)
		}
	}

	// Verify ignored paths were NOT found
	ignoredPaths := []string{nodeModules, distDir}
	for _, ignoredPath := range ignoredPaths {
		if foundPaths[ignoredPath] {
			t.Errorf("should not find gitignored path %s", ignoredPath)
		}
	}
}
