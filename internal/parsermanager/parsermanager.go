// Package parsermanager downloads, SHA-256 verifies and content-addresses the
// signed WASM output-parser modules declared in the `parsers` config entity. It
// reuses the hardened download/verify path from internal/binmanager so the
// parser store inherits the same retry, offline-guard and hash-mandatory policy
// as every other downloaded artifact; only the store layout and the
// XXH3-keyed cache directory live here.
package parsermanager

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/hashutil"
	"github.com/datamitsu/datamitsu/internal/logger"

	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

var log = logger.Logger.With(zap.Namespace("parsermanager"))

// wasmFileName is the fixed name of the module inside its content-addressed dir.
const wasmFileName = "module.wasm"

// Manager resolves parser names to verified, on-disk WASM modules. Construct it
// with the merged `parsers` map from the loaded config.
type Manager struct {
	parsers config.MapOfParsers

	// downloadGroup coalesces concurrent LoadWASMBytes calls for the same parser
	// so a module referenced by N tools is fetched exactly once.
	downloadGroup singleflight.Group
}

// New returns a Manager over the given parser declarations. A nil map is valid
// (yields not-found for every name).
func New(parsers config.MapOfParsers) *Manager {
	return &Manager{parsers: parsers}
}

// LoadWASMBytes returns the verified bytes of the named parser's WASM module,
// downloading and caching it on first use. Repeated calls for a parser already
// in the store read straight from disk with no network access.
func (m *Manager) LoadWASMBytes(ctx context.Context, name string) ([]byte, error) {
	wasmPath, err := m.ensureModule(ctx, name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(wasmPath)
	if err != nil {
		return nil, fmt.Errorf("parser %q: read module: %w", name, err)
	}
	return data, nil
}

// ensureModule downloads-and-verifies the parser if not already cached and
// returns the path to the verified .wasm. Concurrent calls for the same parser
// collapse to one download via singleflight; a redeclared url+hash that is
// already on disk skips the network entirely.
func (m *Manager) ensureModule(ctx context.Context, name string) (string, error) {
	p, ok := m.parsers[name]
	if !ok {
		return "", fmt.Errorf("parser %q is not declared", name)
	}
	if p.URL == "" {
		return "", fmt.Errorf("parser %q has no url", name)
	}
	if p.Hash == "" {
		// Mirror the bundle/archive hash-mandatory rule: an empty hash is a
		// configuration error, never a silent hash-less download.
		return "", fmt.Errorf("parser %q has no hash (SHA-256 is mandatory)", name)
	}

	dir := moduleDir(name, p)
	wasmPath := filepath.Join(dir, wasmFileName)
	if _, err := os.Stat(wasmPath); err == nil {
		return wasmPath, nil
	}

	// Key the singleflight on the content-addressed dir so two tools that share a
	// parser name (same url+hash) coalesce, while a re-pinned version downloads
	// fresh.
	_, err, _ := m.downloadGroup.Do(dir, func() (any, error) {
		// Re-check inside the critical section: a racing caller may have finished
		// the download between our Stat and acquiring the singleflight slot.
		if _, statErr := os.Stat(wasmPath); statErr == nil {
			return struct{}{}, nil
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create store dir: %w", err)
		}
		tmpPath, err := binmanager.DownloadAndVerifySHA256(ctx, p.URL, p.Hash, dir, name)
		if err != nil {
			return nil, fmt.Errorf("download+verify: %w", err)
		}
		// Atomic publish: rename the verified temp file (same dir, so same
		// filesystem) onto its content-addressed name.
		if err := os.Rename(tmpPath, wasmPath); err != nil {
			_ = os.Remove(tmpPath)
			return nil, fmt.Errorf("publish module: %w", err)
		}
		log.Debug("parser module downloaded",
			zap.String("name", name),
			zap.String("path", wasmPath),
		)
		return struct{}{}, nil
	})
	if err != nil {
		return "", fmt.Errorf("parser %q: %w", name, err)
	}
	return wasmPath, nil
}

// cacheKey is the XXH3-128 content-addressed key for a parser declaration. It is
// an internal cache key (never compared with an external value), so XXH3 — not a
// crypto hash — is correct here; the SHA-256 in p.Hash still gates the download.
func cacheKey(p config.Parser) string {
	return hashutil.XXH3Multi([]byte(p.URL), []byte(p.Hash), []byte(p.Version))
}

// moduleDir returns the content-addressed directory for a parser:
// {parsersPath}/{name}/{xxh3(url,hash,version)}.
func moduleDir(name string, p config.Parser) string {
	return filepath.Join(env.GetParsersPath(), name, cacheKey(p))
}
