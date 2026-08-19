// Package cache stores per-file lint/fix results so unchanged files can skip re-running tools.
package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/hashutil"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/shamaton/msgpack/v2"
	"go.uber.org/zap"
)

// Operation represents the type of operation (lint or fix)
type Operation string

// OperationLint and OperationFix identify whether a cached tool result came from
// a lint run or a fix run.
const (
	OperationLint Operation = "lint"
	OperationFix  Operation = "fix"

	cacheFileName = "toolstate.msgpack"
)

// FileEntry represents cache data for a single file
type FileEntry struct {
	ContentHash string   // xxh3-128 hash of file contents
	Lint        []string // tools that successfully passed lint
	Fix         []string // tools that successfully passed fix
}

// File represents the entire cache for a project
type File struct {
	InvalidationKey string               // hash(datamitsuVersion + fullConfigHash + selectedTools)
	ProjectPath     string               // for debugging
	LastPruned      time.Time            // when we last cleaned up deleted files
	Entries         map[string]FileEntry // relativePath -> entry
	// Verdicts is nil in files written before unit-level caching existed; every
	// lookup then misses, which is the safe direction.
	Verdicts map[string]VerdictEntry // opaque identity key -> entry
}

// Stats holds statistics about cache hits and misses
type Stats struct {
	Hits   int64
	Misses int64
}

// Cache manages caching for a single project
type Cache struct {
	path            string
	projectPath     string
	invalidationKey string
	data            *File
	hits            atomic.Int64 // Cache hits counter
	misses          atomic.Int64 // Cache misses counter
	logger          *zap.Logger
	mu              sync.RWMutex // Protects data map from concurrent access
	// Tombstones for this process's deletions. Without them mergeFromDisk, which
	// can only add, re-adds everything Prune or a delete removed — so pruning was
	// a no-op and an invalidated verdict came back on the next save.
	deletedEntries  map[string]struct{}
	deletedVerdicts map[string]struct{}
	saveMu          sync.Mutex // Protects Save() from concurrent calls

	// Async save support
	dirty        atomic.Bool
	saveTimer    *time.Timer
	saveTimerMu  sync.Mutex
	shutdownCh   chan struct{}
	shutdownOnce sync.Once
}

// NewCache creates a new cache for a project
// cacheDir is the base cache directory (e.g., ~/.cache/datamitsu)
// projectPath is the absolute path to the project
// cfg is the full configuration
// selectedTools is the list of tools selected via --tools flag (for cache key)
func NewCache(
	cacheDir string,
	projectPath string,
	cfg config.Config,
	selectedTools []string,
	logger *zap.Logger,
) (*Cache, error) {
	// Create projects subdirectory
	projectsDir := filepath.Join(cacheDir, "projects")
	if err := os.MkdirAll(projectsDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create projects cache directory: %w", err)
	}

	// Hash the project path to get cache directory name
	projectHash := env.HashProjectPath(projectPath)
	projectDir := filepath.Join(projectsDir, projectHash)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create project cache directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(projectDir, "cache"), 0o755); err != nil {
		return nil, fmt.Errorf("failed to create project tool cache directory: %w", err)
	}
	cachePath := filepath.Join(projectDir, cacheFileName)

	// Calculate invalidation key
	invalidationKey, err := calculateInvalidationKey(cfg, selectedTools)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate invalidation key: %w", err)
	}

	c := &Cache{
		path:            cachePath,
		projectPath:     projectPath,
		invalidationKey: invalidationKey,
		logger:          logger.With(zap.String("component", "cache")),
		shutdownCh:      make(chan struct{}),
	}

	// Load existing cache or create new one
	if err := c.Load(); err != nil {
		c.log().Warn("failed to load cache, creating new", zap.Error(err))
		c.data = &File{
			InvalidationKey: invalidationKey,
			ProjectPath:     projectPath,
			LastPruned:      time.Now(),
			Entries:         make(map[string]FileEntry),
			Verdicts:        make(map[string]VerdictEntry),
		}
	}

	return c, nil
}

// Load loads the cache from disk
func (c *Cache) Load() error {
	f, err := os.Open(c.path)
	if err != nil {
		if os.IsNotExist(err) {
			// Cache doesn't exist yet, create new
			c.data = &File{
				InvalidationKey: c.invalidationKey,
				ProjectPath:     c.projectPath,
				LastPruned:      time.Now(),
				Entries:         make(map[string]FileEntry),
				Verdicts:        make(map[string]VerdictEntry),
			}
			return nil
		}
		return fmt.Errorf("failed to open cache file: %w", err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			c.log().Warn("failed to close cache file", zap.Error(closeErr))
		}
	}()

	// Read all data from file
	fileData, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("failed to read cache file: %w", err)
	}

	var data File
	if err := msgpack.Unmarshal(fileData, &data); err != nil {
		return fmt.Errorf("failed to decode cache: %w", err)
	}

	// Check invalidation key
	if data.InvalidationKey != c.invalidationKey {
		c.log().Info("invalidation key mismatch, resetting cache",
			zap.String("old", data.InvalidationKey),
			zap.String("new", c.invalidationKey))
		c.data = &File{
			InvalidationKey: c.invalidationKey,
			ProjectPath:     c.projectPath,
			LastPruned:      time.Now(),
			Entries:         make(map[string]FileEntry),
			Verdicts:        make(map[string]VerdictEntry),
		}
		return nil
	}

	c.data = &data

	// Prune deleted files if it's been more than 24 hours
	if time.Since(c.data.LastPruned) > 24*time.Hour {
		c.Prune()
		c.data.LastPruned = time.Now()
		// Save after pruning
		if err := c.Save(); err != nil {
			c.log().Warn("failed to save cache after pruning", zap.Error(err))
		}
	}

	return nil
}

// Save saves the cache to disk atomically
func (c *Cache) Save() error {
	// Prevent concurrent saves
	c.saveMu.Lock()
	defer c.saveMu.Unlock()

	// Fold in anything another process wrote since we loaded. Without this a
	// long-lived LSP session would overwrite the CLI's writes wholesale — and for
	// verdicts that is not just a lost warm cache but a resurrected verdict the
	// CLI had already invalidated.
	if err := c.mergeFromDisk(); err != nil {
		return err
	}

	c.mu.RLock()
	if c.data == nil {
		c.mu.RUnlock()
		return errors.New("cache data is nil")
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		c.mu.RUnlock()
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Write to temporary file first
	tmpPath := c.path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		c.mu.RUnlock()
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	// Encode cache data
	encodedData, err := msgpack.Marshal(c.data)
	c.mu.RUnlock()

	if err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to encode cache: %w", err)
	}

	// Write encoded data to file
	if _, err := f.Write(encodedData); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to write cache: %w", err)
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, c.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to rename cache file: %w", err)
	}

	return nil
}

// Prune removes entries for files that no longer exist
func (c *Cache) Prune() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.data == nil {
		return
	}

	removed := 0
	for relPath := range c.data.Entries {
		absPath := filepath.Join(c.projectPath, relPath)
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			delete(c.data.Entries, relPath)
			if c.deletedEntries == nil {
				c.deletedEntries = make(map[string]struct{})
			}
			c.deletedEntries[relPath] = struct{}{}
			removed++
		}
	}

	// Verdicts are keyed on an opaque identity, so a unit that no longer exists
	// leaves an entry nothing will ever match. Age them out on the same pass.
	verdicts := c.pruneVerdicts(verdictPruneTTL)

	if removed > 0 || verdicts > 0 {
		c.log().Debug("pruned cache entries",
			zap.Int("files", removed),
			zap.Int("verdicts", verdicts))
	}
}

// ShouldRun checks if a tool should run for a file
// Returns true if the tool should run, false if it can be skipped (cache hit)
// toolCacheEnabled controls whether caching is enabled for this specific tool
func (c *Cache) ShouldRun(file, tool string, op Operation, toolCacheEnabled bool) bool {
	// Cache disabled for this tool - always run
	if !toolCacheEnabled {
		return true
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.data == nil {
		c.misses.Add(1)
		return true
	}

	// Convert to relative path
	relPath, err := filepath.Rel(c.projectPath, file)
	if err != nil {
		c.log().Debug("failed to get relative path",
			zap.String("file", file),
			zap.Error(err))
		c.misses.Add(1)
		return true
	}

	// Check if entry exists
	entry, ok := c.data.Entries[relPath]
	if !ok {
		c.misses.Add(1)
		return true
	}

	// Check if file content has changed
	currentHash, err := hashFile(file)
	if err != nil {
		c.log().Debug("failed to hash file",
			zap.String("file", file),
			zap.Error(err))
		c.misses.Add(1)
		return true
	}

	if entry.ContentHash != currentHash {
		c.misses.Add(1)
		return true
	}

	// Check if tool has already passed
	passed := entry.Lint
	if op == OperationFix {
		passed = entry.Fix
	}

	if slices.Contains(passed, tool) {
		c.hits.Add(1)
		return false // Cache hit - skip execution
	}

	c.misses.Add(1)
	return true
}

// AfterLint marks a tool as having successfully passed lint for a file
// toolCacheEnabled controls whether to save the result in cache
func (c *Cache) AfterLint(file, tool string, toolCacheEnabled bool) error {
	if !toolCacheEnabled {
		return nil // Don't save to cache
	}
	return c.markPassed(file, tool, OperationLint)
}

// AfterFix marks a tool as having successfully passed fix for a file
// If the file was modified, resets lint cache and updates content hash
// toolCacheEnabled controls whether to save the result in cache
func (c *Cache) AfterFix(file, tool string, toolCacheEnabled bool) error {
	if !toolCacheEnabled {
		return nil // Don't save to cache
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.data == nil {
		return errors.New("cache data is nil")
	}

	relPath, err := filepath.Rel(c.projectPath, file)
	if err != nil {
		return fmt.Errorf("failed to get relative path: %w", err)
	}

	// Hash file to check if it changed
	newHash, err := hashFile(file)
	if err != nil {
		return fmt.Errorf("failed to hash file: %w", err)
	}

	entry, exists := c.data.Entries[relPath]

	// If file changed after fix, reset lint cache
	if exists && entry.ContentHash != newHash {
		c.log().Debug("file modified by fix, resetting lint cache",
			zap.String("file", relPath),
			zap.String("tool", tool))
		c.data.Entries[relPath] = FileEntry{
			ContentHash: newHash,
			Lint:        []string{},
			Fix:         []string{tool},
		}
	} else {
		// File unchanged or doesn't exist, just add to fix list
		if !exists {
			entry = FileEntry{
				ContentHash: newHash,
				Lint:        []string{},
				Fix:         []string{},
			}
		}
		if !slices.Contains(entry.Fix, tool) {
			entry.Fix = append(entry.Fix, tool)
		}
		c.data.Entries[relPath] = entry
	}

	return nil
}

// GetStats returns cache statistics
func (c *Cache) GetStats() Stats {
	return Stats{
		Hits:   c.hits.Load(),
		Misses: c.misses.Load(),
	}
}

// Clear removes all cache entries.
//
// The file is deleted before the save rather than overwritten by it. Save folds
// in whatever is on disk, and an emptied map carries no tombstones, so the merge
// used to copy every entry Clear had just dropped straight back and rewrite it —
// `datamitsu cache clear` reported success and cleared nothing. Removing the file
// leaves the merge with nothing to find.
func (c *Cache) Clear() error {
	c.mu.Lock()
	c.data = &File{
		InvalidationKey: c.invalidationKey,
		ProjectPath:     c.projectPath,
		LastPruned:      time.Now(),
		Entries:         make(map[string]FileEntry),
		Verdicts:        make(map[string]VerdictEntry),
	}
	// Tombstones describe deletions against a file that no longer exists.
	c.deletedEntries = nil
	c.deletedVerdicts = nil
	c.mu.Unlock()

	if err := os.Remove(c.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove cache file: %w", err)
	}
	return c.Save()
}

// calculateInvalidationKey calculates an XXH3-128 hash from the datamitsu
// version, the full config JSON and the selected tools.
//
// invalidateOn used to be folded in here and no longer is. It was broken —
// paths resolved against the git root, so packages/*/tsconfig.json read as
// missing and never changed the key — and even repaired it was the wrong shape:
// a global key means editing one nested config resets the entire cache file for
// every tool. It is now a per-unit guard in the verdict cache, where a changed
// input invalidates the unit it belongs to and nothing else.
func calculateInvalidationKey(
	cfg config.Config,
	selectedTools []string,
) (string, error) {
	// Build a single byte slice with all components separated by \0
	var parts [][]byte

	// Add version
	parts = append(parts, []byte(ldflags.Version))

	// Add config hash (serialize entire config)
	configBytes, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("failed to marshal config: %w", err)
	}
	parts = append(parts, configBytes)

	// Add selected tools (sorted for deterministic key)
	if len(selectedTools) > 0 {
		sortedTools := make([]string, len(selectedTools))
		copy(sortedTools, selectedTools)
		slices.Sort(sortedTools)
		for _, tool := range sortedTools {
			parts = append(parts, []byte(tool))
		}
	}

	return hashutil.XXH3Multi(parts...), nil
}

// hashFile calculates XXH3-128 hash of a file's contents
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open file for hashing: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	return hashutil.XXH3Reader(f)
}

func pathEqualFold(a, b string) bool {
	return strings.EqualFold(a, b)
}

func validateCacheDir(cacheDir string) error {
	cleaned := filepath.Clean(cacheDir)
	if cleaned == "" || cleaned == "." || !filepath.IsAbs(cleaned) {
		return fmt.Errorf("refusing to clear: invalid cache directory %q", cacheDir)
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		home = filepath.Clean(home)
	}
	volume := filepath.VolumeName(cleaned)
	sep := string(filepath.Separator)
	if cleaned == "/" || pathEqualFold(cleaned, home) ||
		pathEqualFold(cleaned, volume+sep) ||
		(volume != "" && pathEqualFold(cleaned, volume)) ||
		(home != "" && strings.HasPrefix(strings.ToLower(home), strings.ToLower(cleaned+sep))) {
		return fmt.Errorf("refusing to clear dangerous path: %s", cleaned)
	}
	return nil
}

// ClearAll removes all project caches from the cache directory
func ClearAll(cacheDir string) error {
	if err := validateCacheDir(cacheDir); err != nil {
		return err
	}
	projectsDir := filepath.Join(cacheDir, "projects")
	if err := os.RemoveAll(projectsDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove projects cache directory: %w", err)
	}
	return nil
}

// ClearProject removes the entire project cache directory (lint/fix + tool caches)
func ClearProject(cacheDir string, projectPath string) error {
	if err := validateCacheDir(cacheDir); err != nil {
		return err
	}
	projectHash := env.HashProjectPath(projectPath)
	projectDir := filepath.Join(cacheDir, "projects", projectHash)
	if err := os.RemoveAll(projectDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove project directory: %w", err)
	}
	return nil
}

// MarkDirty marks cache as needing save (non-blocking)
func (c *Cache) MarkDirty() {
	c.dirty.Store(true)
	c.debounceSave()
}

// Shutdown ensures final save before exit
func (c *Cache) Shutdown() {
	c.shutdownOnce.Do(func() {
		// A Cache built as a bare struct literal (tests) has no shutdown channel;
		// closing a nil channel panics, and Shutdown must stay callable so the
		// debounce timer can always be stopped before its owner goes away.
		if c.shutdownCh != nil {
			close(c.shutdownCh)
		}

		c.saveTimerMu.Lock()
		if c.saveTimer != nil {
			c.saveTimer.Stop()
		}
		c.saveTimerMu.Unlock()

		if c.dirty.Swap(false) {
			if err := c.Save(); err != nil {
				c.log().Warn("final save failed", zap.Error(err))
			}
		}
	})
}

// markPassed marks a tool as having passed for a file
func (c *Cache) markPassed(file, tool string, op Operation) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.data == nil {
		return errors.New("cache data is nil")
	}

	relPath, err := filepath.Rel(c.projectPath, file)
	if err != nil {
		return fmt.Errorf("failed to get relative path: %w", err)
	}

	// Get or create entry
	entry, exists := c.data.Entries[relPath]
	if !exists {
		contentHash, err := hashFile(file)
		if err != nil {
			return fmt.Errorf("failed to hash file: %w", err)
		}
		entry = FileEntry{
			ContentHash: contentHash,
			Lint:        []string{},
			Fix:         []string{},
		}
	}

	// Add tool to appropriate list
	if op == OperationLint {
		if !slices.Contains(entry.Lint, tool) {
			entry.Lint = append(entry.Lint, tool)
		}
	} else {
		if !slices.Contains(entry.Fix, tool) {
			entry.Fix = append(entry.Fix, tool)
		}
	}

	c.data.Entries[relPath] = entry
	return nil
}

// log returns the cache logger, falling back to a no-op logger when the Cache
// was built without one. debounceSave logs from a timer goroutine that outlives
// the call that scheduled it, and a nil deref there kills the process rather
// than failing a single operation — so logging must never assume a logger.
func (c *Cache) log() *zap.Logger {
	if c.logger == nil {
		return zap.NewNop()
	}
	return c.logger
}

// debounceSave triggers delayed save (coalesces rapid changes)
func (c *Cache) debounceSave() {
	c.saveTimerMu.Lock()
	defer c.saveTimerMu.Unlock()

	if c.saveTimer != nil {
		c.saveTimer.Stop()
	}

	c.saveTimer = time.AfterFunc(100*time.Millisecond, func() {
		if c.dirty.Swap(false) {
			if err := c.Save(); err != nil {
				c.log().Warn("async save failed", zap.Error(err))
			}
		}
	})
}

// mergeFromDisk reconciles in-memory state with whatever is on disk now.
// Read-modify-write is not atomic across processes, so interleaving can still
// lose a write; correctness rests on the mismatch rules below, not on mutual
// exclusion.
func (c *Cache) mergeFromDisk() error {
	f, err := os.Open(c.path)
	if err != nil {
		return nil // no file yet: nothing to merge
	}
	defer func() { _ = f.Close() }()

	onDisk, err := io.ReadAll(f)
	if err != nil {
		return nil // unreadable is not fatal; we still write ours
	}
	var disk File
	if err := msgpack.Unmarshal(onDisk, &disk); err != nil {
		return nil // corrupt on disk: ours replaces it
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// A different key means the file belongs to another config. Load already
	// discarded it in memory, so this process owns the file now: skip the merge
	// and let our data replace it. Refusing instead would wedge every later save,
	// because nothing ever rewrites the on-disk key — one version bump or config
	// edit and the cache never warms again.
	if disk.InvalidationKey != "" && disk.InvalidationKey != c.invalidationKey {
		return nil
	}

	for path, diskEntry := range disk.Entries {
		if _, gone := c.deletedEntries[path]; gone {
			continue
		}
		ours, ok := c.data.Entries[path]
		if !ok {
			c.data.Entries[path] = diskEntry
			continue
		}
		if ours.ContentHash != diskEntry.ContentHash {
			// Two views of one file's content cannot be reconciled: FileEntry
			// carries no timestamp, so "newer" is undecidable, and keeping either
			// list would attribute passing tools to content they never saw.
			delete(c.data.Entries, path)
			continue
		}
		ours.Lint = unionStrings(ours.Lint, diskEntry.Lint)
		ours.Fix = unionStrings(ours.Fix, diskEntry.Fix)
		c.data.Entries[path] = ours
	}

	if c.data.Verdicts == nil {
		c.data.Verdicts = make(map[string]VerdictEntry)
	}
	now := time.Now()
	for key, diskEntry := range disk.Verdicts {
		if _, gone := c.deletedVerdicts[key]; gone {
			continue
		}
		// Clamp a future timestamp: a backwards clock jump would otherwise let a
		// stale entry win every comparison and never expire.
		if diskEntry.ValidatedAt.After(now) {
			diskEntry.ValidatedAt = now
		}
		ours, ok := c.data.Verdicts[key]
		if !ok || diskEntry.ValidatedAt.After(ours.ValidatedAt) {
			c.data.Verdicts[key] = diskEntry
		}
	}

	return nil
}

func unionStrings(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, list := range [][]string{a, b} {
		for _, v := range list {
			if _, dup := seen[v]; dup {
				continue
			}
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}
