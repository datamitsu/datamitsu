// Package parsermanager downloads, SHA-256 verifies and content-addresses the
// signed WASM output-parser modules declared in the `parsers` config entity. It
// reuses the hardened download/verify path from internal/binmanager so the
// parser store inherits the same retry, offline-guard and hash-mandatory policy
// as every other downloaded artifact; only the store layout and the
// XXH3-keyed cache directory live here.
package parsermanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/hashutil"
	"github.com/datamitsu/datamitsu/internal/logger"

	"github.com/tetratelabs/wazero"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

var log = logger.Logger.With(zap.Namespace("parsermanager"))

// wasmFileName is the fixed name of the module inside its content-addressed dir.
const wasmFileName = "module.wasm"

// Manager resolves parser names to verified, on-disk WASM modules and serves
// ready-to-use instances. It compiles each module once (the expensive step) into
// a shared, sandboxed wazero runtime and instantiates a fresh isolated instance
// per Acquire, so repeated parsing never recompiles or re-reads the module.
// Construct it with the merged `parsers` map from the loaded config; Close it
// (e.g. on shutdown) to release the runtime. The Manager is safe for concurrent
// use; instances it returns are not (one linear memory each).
type Manager struct {
	parsers config.MapOfParsers

	// downloadGroup coalesces concurrent LoadWASMBytes calls for the same parser
	// so a module referenced by N tools is fetched exactly once.
	downloadGroup singleflight.Group

	// mu guards runtime and compiled. runtime is created lazily on first compile;
	// compiled caches each module's CompiledModule by content key so a module is
	// compiled exactly once. compileGroup coalesces concurrent compiles of the
	// same module without holding mu across the (slow) compile.
	mu           sync.Mutex
	runtime      wazero.Runtime
	compiled     map[string]wazero.CompiledModule
	compileGroup singleflight.Group
	closed       bool
}

// errClosed is returned by Acquire/compiledFor when the Manager has been Closed,
// rather than touching the released runtime (a nil interface call would panic).
var errClosed = errors.New("parser manager is closed")

// New returns a Manager over the given parser declarations. A nil map is valid
// (yields not-found for every name).
func New(parsers config.MapOfParsers) *Manager {
	return &Manager{parsers: parsers, compiled: map[string]wazero.CompiledModule{}}
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

// ParseOutput resolves the named parser (downloading+verifying on first use),
// instantiates it in a fresh wazero runtime, and invokes its dispatcher over the
// tool's raw stdout/stderr and exit code. It is the end-to-end seam: declare →
// download → verify → load → invoke. The returned diagnostics are nullable per
// the RawDiagnostic contract (the Go core fills defaults in a later phase).
// ParseOutput loads the `parsers` entry named module (downloading+verifying on
// first use) and runs its parser dispatch key over the tool's raw output. The two
// are distinct: module selects the WASM artifact (so versions are separate
// entries), parser is the dispatch key inside it (a name from describe). Both come
// from a tool's `outputParser`.
func (m *Manager) ParseOutput(
	ctx context.Context,
	module, parser string,
	stdout, stderr []byte,
	exitCode int32,
) ([]RawDiagnostic, error) {
	rt, err := m.Acquire(ctx, module)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rt.Close(ctx) }()
	return rt.Parse(ctx, parser, stdout, stderr, exitCode)
}

// Acquire returns a ready-to-use parser instance for module: it downloads and
// SHA-256 verifies the module on first use, compiles it once into the shared
// runtime (cached), and instantiates a fresh, isolated instance. Each instance
// has its own linear memory, so concurrent Acquire results never interfere; the
// caller owns Close. This is the "give me a parser" seam — callers do not care
// how the instance is produced.
func (m *Manager) Acquire(ctx context.Context, module string) (*ParserRuntime, error) {
	p, ok := m.parsers[module]
	if !ok {
		return nil, fmt.Errorf("parser %q is not declared", module)
	}
	compiled, err := m.compiledFor(ctx, module, p)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	rt := m.runtime
	closed := m.closed
	m.mu.Unlock()
	if closed || rt == nil {
		return nil, errClosed
	}
	// Anonymous name (WithName("")) so many instances of one CompiledModule can
	// coexist — there is no module-name collision in the runtime's namespace.
	mod, err := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(""))
	if err != nil {
		return nil, fmt.Errorf("instantiate parser module %q: %w", module, err)
	}
	return newInstance(ctx, mod, nil)
}

// Prewarm compiles the given modules ahead of use so the one-time compilation
// happens here — typically at planning time, off the per-file execution path —
// rather than lazily on the first parse. It is best-effort: a failure is
// returned for the caller to log, and lazy compilation on first Acquire remains
// the fallback. Modules already compiled are skipped.
func (m *Manager) Prewarm(ctx context.Context, modules []string) error {
	seen := make(map[string]bool, len(modules))
	for _, module := range modules {
		if seen[module] {
			continue
		}
		seen[module] = true
		p, ok := m.parsers[module]
		if !ok {
			continue // an undeclared reference is a config-validation concern, not ours
		}
		if _, err := m.compiledFor(ctx, module, p); err != nil {
			return fmt.Errorf("prewarm parser %q: %w", module, err)
		}
	}
	return nil
}

// Close releases the shared runtime and every module compiled into it. Safe to
// call when nothing was ever compiled (the runtime is created lazily). After
// Close the Manager must not be used again.
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	rt := m.runtime
	m.runtime = nil
	m.compiled = nil
	m.mu.Unlock()
	if rt == nil {
		return nil
	}
	// Closing the runtime closes all compiled modules and outstanding instances.
	if err := rt.Close(ctx); err != nil {
		return fmt.Errorf("close parser runtime: %w", err)
	}
	return nil
}

// compiledFor returns module's CompiledModule, compiling it exactly once. The
// compile (download+verify+read+CompileModule) runs under a singleflight keyed by
// the content key, so concurrent callers for the same module share one compile;
// the short mu critical sections only touch the cache and lazily-created runtime.
func (m *Manager) compiledFor(ctx context.Context, module string, p config.Parser) (wazero.CompiledModule, error) {
	key := cacheKey(p)

	m.mu.Lock()
	if cm := m.compiled[key]; cm != nil {
		m.mu.Unlock()
		return cm, nil
	}
	m.mu.Unlock()

	v, err, _ := m.compileGroup.Do(key, func() (any, error) {
		// Re-check: a racer may have compiled while we waited for the slot.
		m.mu.Lock()
		if cm := m.compiled[key]; cm != nil {
			m.mu.Unlock()
			return cm, nil
		}
		m.mu.Unlock()

		wasm, err := m.LoadWASMBytes(ctx, module) // download+verify (singleflight) + read
		if err != nil {
			return nil, err
		}

		m.mu.Lock()
		if m.closed {
			// Don't resurrect a runtime in a Closed Manager — it would leak (Close
			// already ran and won't close anything created after it).
			m.mu.Unlock()
			return nil, errClosed
		}
		if m.runtime == nil {
			m.runtime = wazero.NewRuntime(ctx)
		}
		rt := m.runtime
		m.mu.Unlock()

		cm, err := rt.CompileModule(ctx, wasm)
		if err != nil {
			return nil, fmt.Errorf("compile parser module %q: %w", module, err)
		}

		m.mu.Lock()
		if m.closed {
			// Manager was Closed concurrently; don't cache into a dead Manager.
			m.mu.Unlock()
			_ = cm.Close(ctx)
			return nil, errClosed
		}
		m.compiled[key] = cm
		m.mu.Unlock()
		return cm, nil
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // err is already wrapped by LoadWASMBytes / the compile step
	}
	cm, ok := v.(wazero.CompiledModule)
	if !ok {
		return nil, fmt.Errorf("parser %q: compiled-module cache returned %T", module, v)
	}
	return cm, nil
}

// ParseLocal runs the named tool's parser, inside an already-loaded WASM module,
// over the given raw output — like ParseOutput but for a local module (no config
// or download). It backs `devtools parsers run --wasm` and offline tests.
func ParseLocal(ctx context.Context, wasm []byte, toolName string, stdout, stderr []byte, exitCode int32) ([]RawDiagnostic, error) {
	rt, err := NewRuntime(ctx, wasm)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rt.Close(ctx) }()
	return rt.Parse(ctx, toolName, stdout, stderr, exitCode)
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
// url+hash fully identify the artifact (hash is content-addressed); the module's
// own version lives in its `describe` output, not the config, so it is not a key.
func cacheKey(p config.Parser) string {
	return hashutil.XXH3Multi([]byte(p.URL), []byte(p.Hash))
}

// moduleDir returns the content-addressed directory for a parser:
// {parsersPath}/{name}/{xxh3(url,hash,version)}.
func moduleDir(name string, p config.Parser) string {
	return filepath.Join(env.GetParsersPath(), name, cacheKey(p))
}
