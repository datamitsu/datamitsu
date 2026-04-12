package remotecfg

import (
	"github.com/datamitsu/datamitsu/internal/logger"
	"fmt"

	"go.uber.org/zap"
)

var log = logger.Logger.With(zap.Namespace("remotecfg"))

// Resolve fetches a remote config, using a hash-based disk cache.
// If a cached file exists and its hash matches expectedHash, it returns the
// cached content without a network request. Otherwise it fetches, verifies
// the hash, and saves to cache.
func Resolve(url, expectedHash, cacheDir string) (string, error) {
	if expectedHash == "" {
		return "", fmt.Errorf("remote config %s: hash is required", url)
	}

	if err := validateHashFormat(expectedHash); err != nil {
		return "", fmt.Errorf("remote config %s: %w", url, err)
	}

	cachePath := CachedConfigPath(cacheDir, url)

	cached, cacheErr := LoadCached(cachePath)
	if cacheErr == nil {
		if err := verifyHash([]byte(cached), expectedHash, url); err == nil {
			log.Debug("using cached remote config", zap.String("url", url))
			return cached, nil
		}
		log.Debug("cached remote config hash mismatch, fetching fresh", zap.String("url", url))
	}

	content, fetchErr := FetchRemoteConfig(url, expectedHash)
	if fetchErr != nil {
		return "", fmt.Errorf("failed to fetch remote config %s: %w", url, fetchErr)
	}

	if saveErr := SaveCached(cachePath, content); saveErr != nil {
		log.Warn("failed to cache remote config", zap.String("url", url), zap.Error(saveErr))
	}

	return content, nil
}
