package remotecfg

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/datamitsu/datamitsu/internal/hashutil"
	"github.com/datamitsu/datamitsu/internal/utils"
)

const remoteConfigDir = ".remote-configs"

// CachedConfigPath returns the cache file path for a remote config URL.
func CachedConfigPath(cacheDir, url string) string {
	h := hashutil.XXH3Hex([]byte(url))
	return filepath.Join(cacheDir, remoteConfigDir, h+".ts")
}

// LoadCached reads a cached config file. Returns os.ErrNotExist if missing.
func LoadCached(path string) (content string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

// SaveCached writes content to the cache path atomically (temp file + rename).
func SaveCached(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create remote config cache directory: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, ".remote-cfg-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.WriteString(content); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := utils.RenameReplace(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}
