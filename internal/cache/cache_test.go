package cache

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/logger"
)

func TestCacheConcurrentAccess(t *testing.T) {
	// Create temporary directory for cache
	tmpDir, err := os.MkdirTemp("", "cache-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	// Create a test project path
	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}

	// Create some test files
	testFiles := []string{"file1.go", "file2.go", "file3.go"}
	for _, f := range testFiles {
		filePath := filepath.Join(projectPath, f)
		if err := os.WriteFile(filePath, []byte("package main\n"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}
	}

	// Create cache
	cfg := config.Config{}
	invalidateOn := map[string][]string{}
	cache, err := NewCache(tmpDir, projectPath, cfg, invalidateOn, nil, logger.Logger)
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	// Run concurrent operations
	const numGoroutines = 10
	const numOperations = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 3) // 3 types of operations

	// Concurrent ShouldRun checks
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				file := filepath.Join(projectPath, testFiles[j%len(testFiles)])
				cache.ShouldRun(file, "test-tool", OperationLint, true)
			}
		}(i)
	}

	// Concurrent AfterLint updates
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				file := filepath.Join(projectPath, testFiles[j%len(testFiles)])
				_ = cache.AfterLint(file, "test-tool", true)
			}
		}(i)
	}

	// Concurrent AfterFix updates
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				file := filepath.Join(projectPath, testFiles[j%len(testFiles)])
				_ = cache.AfterFix(file, "test-tool", true)
			}
		}(i)
	}

	wg.Wait()

	// Save cache
	if err := cache.Save(); err != nil {
		t.Fatalf("failed to save cache: %v", err)
	}

	// Verify stats
	stats := cache.GetStats()
	if stats.Hits < 0 || stats.Misses < 0 {
		t.Errorf("invalid stats: hits=%d, misses=%d", stats.Hits, stats.Misses)
	}
}

func TestCacheConcurrentSave(t *testing.T) {
	// Create temporary directory for cache
	tmpDir, err := os.MkdirTemp("", "cache-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	// Create a test project path
	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}

	// Create test file
	testFile := filepath.Join(projectPath, "test.go")
	if err := os.WriteFile(testFile, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Create cache
	cfg := config.Config{}
	invalidateOn := map[string][]string{}
	cache, err := NewCache(tmpDir, projectPath, cfg, invalidateOn, nil, logger.Logger)
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	// Simulate concurrent saves (like in the error you had)
	const numGoroutines = 20
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	saveErrors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			// Update cache
			_ = cache.AfterLint(testFile, fmt.Sprintf("tool-%d", id), true)

			// Try to save (this was causing the race condition)
			if err := cache.Save(); err != nil {
				saveErrors <- err
			}
		}(i)
	}

	wg.Wait()
	close(saveErrors)

	// Check for errors
	for err := range saveErrors {
		t.Errorf("concurrent save failed: %v", err)
	}

	// Verify cache file exists and is valid
	if _, err := os.Stat(cache.path); os.IsNotExist(err) {
		t.Errorf("cache file does not exist after concurrent saves")
	}

	// Try to load cache to verify it's not corrupted
	newCache, err := NewCache(tmpDir, projectPath, cfg, invalidateOn, nil, logger.Logger)
	if err != nil {
		t.Fatalf("failed to load cache after concurrent saves: %v", err)
	}

	if newCache.data == nil || newCache.data.Entries == nil {
		t.Errorf("cache data is corrupted after concurrent saves")
	}
}

func TestInvalidationKeyFormat(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cache-invalidation-key-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	projectPath := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectPath, 0755); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}

	cfg := config.Config{}
	key, err := calculateInvalidationKey(cfg, nil, projectPath, nil)
	if err != nil {
		t.Fatalf("calculateInvalidationKey() error = %v", err)
	}

	// XXH3-128 produces 32 hex chars
	if len(key) != 32 {
		t.Errorf("invalidation key length = %d, want 32 (XXH3-128), got %q", len(key), key)
	}

	// Must be valid hex
	if _, err := hex.DecodeString(key); err != nil {
		t.Errorf("invalidation key is not valid hex: %s", key)
	}

	// Deterministic
	key2, err := calculateInvalidationKey(cfg, nil, projectPath, nil)
	if err != nil {
		t.Fatalf("calculateInvalidationKey() error = %v", err)
	}
	if key != key2 {
		t.Errorf("invalidation key not deterministic: %s != %s", key, key2)
	}

	// Different tools produce different keys
	keyWithTools, err := calculateInvalidationKey(cfg, nil, projectPath, []string{"eslint"})
	if err != nil {
		t.Fatalf("calculateInvalidationKey() error = %v", err)
	}
	if key == keyWithTools {
		t.Errorf("different tools should produce different key")
	}
}

func TestContentHashFormat(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cache-hash-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello world\n"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	hash, err := hashFile(testFile)
	if err != nil {
		t.Fatalf("hashFile() error = %v", err)
	}

	// XXH3-128 produces 32 hex chars
	if len(hash) != 32 {
		t.Errorf("ContentHash length = %d, want 32 (XXH3-128), got %q", len(hash), hash)
	}

	// Must be valid hex
	if _, err := hex.DecodeString(hash); err != nil {
		t.Errorf("ContentHash is not valid hex: %s", hash)
	}

	// Deterministic
	hash2, err := hashFile(testFile)
	if err != nil {
		t.Fatalf("hashFile() error = %v", err)
	}
	if hash != hash2 {
		t.Errorf("hashFile not deterministic: %s != %s", hash, hash2)
	}

	// Different content produces different hash
	testFile2 := filepath.Join(tmpDir, "test2.txt")
	if err := os.WriteFile(testFile2, []byte("different content\n"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
	hash3, err := hashFile(testFile2)
	if err != nil {
		t.Fatalf("hashFile() error = %v", err)
	}
	if hash == hash3 {
		t.Errorf("different file content should produce different hash")
	}
}

