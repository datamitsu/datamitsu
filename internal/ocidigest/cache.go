package ocidigest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/datamitsu/datamitsu/internal/hashutil"
	"github.com/datamitsu/datamitsu/internal/utils"
)

// digestCacheVersion is the on-disk schema version for cached entries.
const digestCacheVersion = 1

// digestCacheEntry is the persisted record mapping a registry/repo/tag to its
// resolved content digest. Only immutable tags are ever resolved-and-cached, so
// entries never expire (no TTL needed).
type digestCacheEntry struct {
	Version  int    `json:"version"`
	Registry string `json:"registry"`
	Repo     string `json:"repo"`
	Tag      string `json:"tag"`
	Digest   string `json:"digest"`
}

// CacheDirName is the cache-relative directory holding resolved-digest entries.
// Exported so `store clear` can remove it: the cache root is a sibling of the
// store, so clearing the store alone would leave these entries — which never
// expire — behind.
const CacheDirName = ".oci-digests"

// digestCachePath returns the cache file path for a registry/repo/tag triple.
// The filename is an XXH3 key (an internal cache key — not a security boundary,
// per the hashing policy); the cached value is the external SHA-256 digest.
func digestCachePath(cacheDir, registry, repo, tag string) string {
	key := hashutil.XXH3Multi([]byte(registry), []byte(repo), []byte(tag))
	return filepath.Join(cacheDir, CacheDirName, key+".json")
}

// loadCachedDigest returns the cached digest and true when a readable, valid
// entry exists. Any read/parse error (missing file, corruption) is reported as a
// miss so the caller transparently re-resolves.
func loadCachedDigest(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var entry digestCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return "", false
	}
	if entry.Digest == "" {
		return "", false
	}
	return entry.Digest, true
}

// saveCachedDigest atomically writes a digest entry via a temp file and rename.
func saveCachedDigest(path, registry, repo, tag, digest string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create digest cache directory: %w", err)
	}

	data, err := json.MarshalIndent(digestCacheEntry{
		Version:  digestCacheVersion,
		Registry: registry,
		Repo:     repo,
		Tag:      tag,
		Digest:   digest,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal digest entry: %w", err)
	}
	data = append(data, '\n')

	tmpFile, err := os.CreateTemp(dir, ".oci-digest-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := utils.RenameReplace(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}
