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
	"sort"
	"sync"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/hashutil"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/ociartifact"
	"github.com/datamitsu/datamitsu/internal/parsermanager/embedded"
	"github.com/datamitsu/datamitsu/internal/trace"

	"github.com/tetratelabs/wazero"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

var log = logger.Logger.With(zap.Namespace("parsermanager"))

// Parser counters. Instantiation happens once per parsed tool invocation, so a
// count equal to the parse count says an instance pool would pay for itself.
var (
	cntInstantiate = trace.NewCounter("parser.module_instantiations")
	cntCompile     = trace.NewCounter("parser.module_compilations")
	cntPoolHit     = trace.NewCounter("parser.instance_pool_hits")
	// cntNoReuse counts instances closed after a successful parse because their
	// state could not be cleared — a module without a `reset` export parses at the
	// pre-pooling cost, and this is how that shows up.
	cntNoReuse = trace.NewCounter("parser.unresettable_discards")
	// cntDescribe counts the describe calls HasParser makes: one per module.
	cntDescribe = trace.NewCounter("parser.key_describes")
)

// wasmFileName is the fixed name of the module inside its content-addressed dir.
const wasmFileName = "module.wasm"

// EmbeddedModule is the name a Manager serves the fallback module built into
// datamitsu under, beside the declared ones. No `parsers` entry may take it.
const EmbeddedModule = config.ReservedParserModule

// FallbackParser is the key of the sniffer, which the core runs only on the
// embedded module: the fallback is versioned with the binary, whichever module
// a configuration pins.
const FallbackParser = "fallback"

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

	// idle holds instances ParseOutput has finished with and reset, keyed by content
	// key, so a run that parses N tool invocations of one module instantiates once
	// per concurrent parse rather than once per parse. Guarded by mu.
	idle map[string][]*ParserRuntime

	// described holds, per content key, what a module's describe said, asked
	// once per Manager. Guarded by mu; describeGroup coalesces the first
	// callers.
	described     map[string]moduleFacts
	describeGroup singleflight.Group
}

type moduleFacts struct {
	parsers  map[string]bool
	contract bool
	caps     Capabilities
}

// ParserFacts is what a module described about itself and one of its parser
// keys.
type ParserFacts struct {
	Version string
	Schema  int
	// Contract reports the severity contract (Capabilities.SeverityContract).
	Contract bool
	// Tool is the parser key's entry; the zero value when the module does not
	// list the key.
	Tool ToolCapability
}

// DescribedParser returns what module's describe said about parser, and
// whether this Manager has described the module at all. It never loads a
// module: a caller that only reports on a run asks after the fact, and must
// not fetch or compile anything to do so.
func (m *Manager) DescribedParser(module, parser string) (ParserFacts, bool) {
	key, ok := m.instanceKey(module)
	if !ok {
		return ParserFacts{}, false
	}
	m.mu.Lock()
	facts, described := m.described[key]
	m.mu.Unlock()
	if !described {
		return ParserFacts{}, false
	}
	out := ParserFacts{Version: facts.caps.Version, Schema: facts.caps.SchemaVersion, Contract: facts.contract}
	for _, t := range facts.caps.Tools {
		if t.Name == parser {
			out.Tool = t
			break
		}
	}
	return out, true
}

// ErrModuleUnavailable marks a parse that never reached the module: it could
// not be fetched, verified, compiled or instantiated. The tool's output was not
// parsed at all, which a caller reports differently from a module that parsed
// and failed.
var ErrModuleUnavailable = errors.New("parser module unavailable")

// moduleUnavailableError marks err as ErrModuleUnavailable without prefixing its
// message: the cause already says what went wrong, and a caller that reports it
// names the module itself.
type moduleUnavailableError struct{ err error }

func (e moduleUnavailableError) Error() string        { return e.err.Error() }
func (e moduleUnavailableError) Unwrap() error        { return e.err }
func (e moduleUnavailableError) Is(target error) bool { return target == ErrModuleUnavailable }

// maxIdleInstances bounds how many instances of one module are kept per Manager.
// Instances are only ever pooled after a parse returned, so this caps the pool at
// roughly the peak parse concurrency; anything beyond it is closed rather than
// held (a wasm instance owns a linear memory).
const maxIdleInstances = 8

// errClosed is returned by Acquire/compiledFor when the Manager has been Closed,
// rather than touching the released runtime (a nil interface call would panic).
var errClosed = errors.New("parser manager is closed")

// New returns a Manager over the given parser declarations. A nil map is valid
// (yields not-found for every name).
func New(parsers config.MapOfParsers) *Manager {
	return &Manager{
		parsers:   parsers,
		compiled:  map[string]wazero.CompiledModule{},
		idle:      map[string][]*ParserRuntime{},
		described: map[string]moduleFacts{},
	}
}

// HasParser reports whether module's describe lists parser. A module answers
// an unknown key with an empty result, which reads as a clean run, so a caller
// that must not take "nothing parsed" for "nothing found" asks first. The
// answer is described once per module and Manager. An error wraps
// ErrModuleUnavailable when the module could not be loaded.
func (m *Manager) HasParser(ctx context.Context, module, parser string) (bool, error) {
	facts, err := m.describeOnce(ctx, module)
	if err != nil {
		return false, err
	}
	return facts.parsers[parser], nil
}

// SeverityContract reports whether module's levels come only from what its
// tools printed (descriptor schema 2 or later), which is what a failOn
// threshold needs to be trusted. It shares HasParser's single describe per
// module and Manager, and its error.
func (m *Manager) SeverityContract(ctx context.Context, module string) (bool, error) {
	facts, err := m.describeOnce(ctx, module)
	if err != nil {
		return false, err
	}
	return facts.contract, nil
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

// ParseOutput loads the `parsers` entry named module (downloading+verifying on
// first use) and runs its parser dispatch key over the tool's raw stdout/stderr
// and exit code. It is the end-to-end seam: declare → download → verify → load →
// invoke. The two names are distinct: module selects the WASM artifact (so
// versions are separate entries), parser is the dispatch key inside it (a name
// from describe). Both come from a tool's `outputParser`. The answer's
// diagnostics are nullable per the RawDiagnostic contract (the Go core fills
// defaults in a later phase).
//
// Instances of a module that exports `reset` are pooled: after a successful parse
// the instance is reset and goes back into the Manager rather than being closed,
// so a run that parses many tool invocations of one module pays for one
// instantiation per concurrent parse instead of one per parse. Three rules keep
// that invisible to the caller:
//
//  1. An instance is pooled only after `reset` returned it to its
//     post-instantiation state. The ABI does not make a module stateless, and a
//     module with mutable globals or retained linear memory would let parse N+1
//     observe parse N — one tool's output shaping another's diagnostics. A module
//     that does not export `reset` makes no such guarantee and is never reused:
//     its instances are closed after every parse, as they were before pooling.
//  2. An instance whose parse returned an error is closed, never pooled. A trap
//     mid-ABI can leave the module's allocator in a state the next parse would
//     inherit, and no state may leak from one tool's output into another's.
//  3. A failure on a *reused* instance is retried once on a fresh one. The
//     failure may belong to the instance rather than to the input — it may have
//     been closed underneath the pool — and pooling must never turn a parse that
//     works into one that fails. A fresh instance is not retried: its failure is
//     the module's answer to this input.
func (m *Manager) ParseOutput(
	ctx context.Context,
	module, parser string,
	stdout, stderr []byte,
	exitCode int32,
) (Response, error) {
	inst, reused, err := m.acquirePooled(ctx, module)
	if err != nil {
		return Response{}, moduleUnavailableError{err}
	}

	resp, parseErr := inst.Parse(ctx, parser, stdout, stderr, exitCode)
	if parseErr == nil {
		m.releaseReset(ctx, module, inst)
		return resp, nil
	}
	_ = inst.Close(ctx)
	if !reused {
		return Response{}, parseErr
	}

	fresh, err := m.Acquire(ctx, module)
	if err != nil {
		return Response{}, parseErr // report the parse failure, not the re-instantiate one
	}
	resp, parseErr = fresh.Parse(ctx, parser, stdout, stderr, exitCode)
	if parseErr != nil {
		_ = fresh.Close(ctx)
		return Response{}, parseErr
	}
	m.releaseReset(ctx, module, fresh)
	return resp, nil
}

// Fallback runs the sniffer of the embedded module over a tool's output: the
// answer of the first standard format that recognized it. It shares the
// Manager's compile-once runtime and instance pool with the declared modules.
func (m *Manager) Fallback(ctx context.Context, stdout, stderr []byte, exitCode int32) (Response, error) {
	return m.ParseOutput(ctx, EmbeddedModule, FallbackParser, stdout, stderr, exitCode)
}

// DescribeEmbedded describes the embedded fallback module; it needs no
// configuration and no network.
func DescribeEmbedded(ctx context.Context) (Capabilities, error) {
	return DescribeLocal(ctx, embedded.Module())
}

// ParseEmbedded runs the parser key of the embedded fallback module over a
// tool's output, as ParseLocal does for a module file.
func ParseEmbedded(ctx context.Context, key string, stdout, stderr []byte, exitCode int32) (Response, error) {
	return ParseLocal(ctx, embedded.Module(), key, stdout, stderr, exitCode)
}

// Acquire returns a ready-to-use parser instance for module: it downloads and
// SHA-256 verifies the module on first use, compiles it once into the shared
// runtime (cached), and instantiates a fresh, isolated instance. Each instance
// has its own linear memory, so concurrent Acquire results never interfere; the
// caller owns Close. This is the "give me a parser" seam — callers do not care
// how the instance is produced.
func (m *Manager) Acquire(ctx context.Context, module string) (*ParserRuntime, error) {
	src, ok := m.source(module)
	if !ok {
		return nil, fmt.Errorf("parser %q is not declared", module)
	}
	compiled, err := m.compiledFor(ctx, module, src)
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
	cntInstantiate.Add(1)
	instSpan := trace.Start(trace.CatParse, "parser.instantiate")
	mod, err := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(""))
	instSpan.EndWith(trace.A("module", module))
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
	defer trace.Start(trace.CatParse, "parser.prewarm").EndWith(trace.A("modules", len(modules)))

	seen := make(map[string]bool, len(modules))
	for _, module := range modules {
		if seen[module] {
			continue
		}
		seen[module] = true
		src, ok := m.source(module)
		if !ok {
			continue // an undeclared reference is a config-validation concern, not ours
		}
		if _, err := m.compiledFor(ctx, module, src); err != nil {
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
	// Closing the runtime below closes every instance it owns, pooled or not; the
	// map is dropped so a post-Close release cannot resurrect one.
	m.idle = nil
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

// Prefetch downloads and SHA-256 verifies the named parser modules into the
// store WITHOUT compiling them. It exists so an OCI-bundle Docker build can
// materialize each module as its own store subtree (then COPYed into a layer),
// and so callers can warm the cache ahead of an airgapped run. Empty names means
// every declared parser; names are visited in sorted order for stable logs. A
// module already on disk whose bytes still match the declared SHA-256 is a
// no-op. Unlike Prewarm, it never touches the wazero runtime — fetch only.
func (m *Manager) Prefetch(ctx context.Context, names []string) error {
	if len(names) == 0 {
		names = make([]string, 0, len(m.parsers))
		for name := range m.parsers {
			names = append(names, name)
		}
		sort.Strings(names)
	}
	for _, name := range names {
		if _, err := m.ensureModule(ctx, name); err != nil {
			return fmt.Errorf("prefetch parser %q: %w", name, err)
		}
	}
	return nil
}

// describeOnce is what module's describe said, asked once per content key.
func (m *Manager) describeOnce(ctx context.Context, module string) (moduleFacts, error) {
	key, ok := m.instanceKey(module)
	if !ok {
		return moduleFacts{}, moduleUnavailableError{fmt.Errorf("parser %q is not declared", module)}
	}
	m.mu.Lock()
	facts, described := m.described[key]
	m.mu.Unlock()
	if described {
		return facts, nil
	}
	v, err, _ := m.describeGroup.Do(key, func() (any, error) {
		// A caller that missed the map may arrive after another one's group
		// has finished and stored the answer.
		m.mu.Lock()
		facts, done := m.described[key]
		m.mu.Unlock()
		if done {
			return facts, nil
		}
		cntDescribe.Add(1)
		inst, _, err := m.acquirePooled(ctx, module)
		if err != nil {
			return nil, moduleUnavailableError{err}
		}
		caps, err := inst.Describe(ctx)
		if err != nil {
			_ = inst.Close(ctx)
			return nil, moduleUnavailableError{err}
		}
		m.releaseReset(ctx, module, inst)
		if described := moduleOf(module, caps); described.Outdated() {
			log.Debug(described.OutdatedNote())
		}
		facts = moduleFacts{parsers: make(map[string]bool, len(caps.Tools)), contract: caps.SeverityContract(), caps: caps}
		for _, t := range caps.Tools {
			facts.parsers[t.Name] = true
		}
		m.mu.Lock()
		if m.described != nil {
			m.described[key] = facts
		}
		m.mu.Unlock()
		return facts, nil
	})
	if err != nil {
		return moduleFacts{}, err //nolint:wrapcheck // marked moduleUnavailableError inside the group
	}
	facts, _ = v.(moduleFacts)
	return facts, nil
}

// releaseReset pools inst only after its state has been cleared. A module that
// cannot reset (no such export) or whose reset failed is closed instead: reuse is
// an optimization, and there is no version of it worth a parse observing the
// previous one's leftovers.
func (m *Manager) releaseReset(ctx context.Context, module string, inst *ParserRuntime) {
	if err := inst.Reset(ctx); err != nil {
		cntNoReuse.Add(1)
		if !errors.Is(err, errNotResettable) {
			log.Debug("parser instance not reused", zap.String("module", module), zap.Error(err))
		}
		_ = inst.Close(ctx)
		return
	}
	m.release(ctx, module, inst)
}

// acquirePooled returns an idle instance of module when one is available, and
// instantiates a fresh one otherwise. reused says which, because the caller
// treats a failure on the two differently.
func (m *Manager) acquirePooled(ctx context.Context, module string) (inst *ParserRuntime, reused bool, err error) {
	if key, ok := m.instanceKey(module); ok {
		m.mu.Lock()
		if !m.closed {
			if n := len(m.idle[key]); n > 0 {
				inst = m.idle[key][n-1]
				m.idle[key] = m.idle[key][:n-1]
			}
		}
		m.mu.Unlock()
		if inst != nil {
			cntPoolHit.Add(1)
			return inst, true, nil
		}
	}
	inst, err = m.Acquire(ctx, module)
	if err != nil {
		return nil, false, err
	}
	return inst, false, nil
}

// release returns a finished instance to the pool, or closes it when the pool is
// full or the Manager has been closed under it.
func (m *Manager) release(ctx context.Context, module string, inst *ParserRuntime) {
	key, ok := m.instanceKey(module)
	if !ok {
		_ = inst.Close(ctx)
		return
	}
	m.mu.Lock()
	if m.closed || len(m.idle[key]) >= maxIdleInstances {
		m.mu.Unlock()
		_ = inst.Close(ctx)
		return
	}
	m.idle[key] = append(m.idle[key], inst)
	m.mu.Unlock()
}

// instanceKey is the pool key for a module: its content key, so a re-pinned
// module never draws an instance compiled from the bytes it replaced.
func (m *Manager) instanceKey(module string) (string, bool) {
	src, ok := m.source(module)
	return src.key, ok
}

// moduleSource is where a module's bytes come from, and the content key they
// are compiled and pooled under.
type moduleSource struct {
	key  string
	load func(ctx context.Context) ([]byte, error)
}

// source resolves module: a declared `parsers` entry, fetched and verified,
// or the embedded fallback, whose bytes are part of the binary.
func (m *Manager) source(module string) (moduleSource, bool) {
	if module == EmbeddedModule {
		return moduleSource{
			key:  "embedded-" + embedded.ContentKey(),
			load: func(context.Context) ([]byte, error) { return embedded.Module(), nil },
		}, true
	}
	p, ok := m.parsers[module]
	if !ok {
		return moduleSource{}, false
	}
	return moduleSource{
		key:  cacheKey(p),
		load: func(ctx context.Context) ([]byte, error) { return m.LoadWASMBytes(ctx, module) },
	}, true
}

// compiledFor returns module's CompiledModule, compiling it exactly once. The
// compile (download+verify+read+CompileModule) runs under a singleflight keyed by
// the content key, so concurrent callers for the same module share one compile;
// the short mu critical sections only touch the cache and lazily-created runtime.
func (m *Manager) compiledFor(ctx context.Context, module string, src moduleSource) (wazero.CompiledModule, error) {
	key := src.key

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

		wasm, err := src.load(ctx) // download+verify (singleflight) + read
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

		cntCompile.Add(1)
		compileSpan := trace.Start(trace.CatParse, "parser.compileModule")
		cm, err := rt.CompileModule(ctx, wasm)
		compileSpan.EndWith(trace.A("module", module), trace.A("bytes", len(wasm)))
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
func ParseLocal(ctx context.Context, wasm []byte, toolName string, stdout, stderr []byte, exitCode int32) (Response, error) {
	rt, err := NewRuntime(ctx, wasm)
	if err != nil {
		return Response{}, err
	}
	defer func() { _ = rt.Close(ctx) }()
	return rt.Parse(ctx, toolName, stdout, stderr, exitCode)
}

// ensureModule downloads-and-verifies the parser if not already cached and
// returns the path to the verified .wasm. Concurrent calls for the same parser
// collapse to one download via singleflight; a module already on disk whose
// bytes still verify against the declared SHA-256 skips the network entirely.
func (m *Manager) ensureModule(ctx context.Context, name string) (string, error) {
	p, ok := m.parsers[name]
	if !ok {
		return "", fmt.Errorf("parser %q is not declared", name)
	}
	switch {
	case p.URL == "" && p.OCI == nil:
		return "", fmt.Errorf("parser %q has no source (declare exactly one of url or oci)", name)
	case p.URL != "" && p.OCI != nil:
		return "", fmt.Errorf("parser %q declares both url and oci (they are mutually exclusive)", name)
	}
	if p.Hash == "" {
		// Mirror the bundle/archive hash-mandatory rule: an empty hash is a
		// configuration error, never a silent hash-less download.
		return "", fmt.Errorf("parser %q has no hash (SHA-256 is mandatory)", name)
	}

	dir := moduleDir(name, p)
	wasmPath := filepath.Join(dir, wasmFileName)
	if storedModuleIsValid(wasmPath, p) {
		return wasmPath, nil
	}

	// Key the singleflight on the content-addressed dir so two tools that share a
	// parser name (same module) coalesce, while a re-pinned version is fetched
	// fresh.
	_, err, _ := m.downloadGroup.Do(dir, func() (any, error) {
		// Re-check inside the critical section: a racing caller may have finished
		// the download between our Stat and acquiring the singleflight slot.
		if storedModuleIsValid(wasmPath, p) {
			return struct{}{}, nil
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create store dir: %w", err)
		}
		tmpPath, err := fetchModule(ctx, name, p, dir)
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

// fetchOCIModule pulls a registry-sourced module. It is a variable so tests can
// exercise the dispatch, the post-fetch verification and the store publish
// without a registry; production always calls straight through.
var fetchOCIModule = ociartifact.FetchParserModule

// fetchModule materializes the declared module into a temp file under dir and
// returns its path; the caller publishes it with an atomic rename. Exactly one
// source is declared (ensureModule has already checked), so this is a dispatch,
// never a fallback chain: an air-gapped organization has to be able to prove
// that no path here reaches github.com.
//
// Both branches leave the mandatory SHA-256 in charge of the content. The
// registry branch adds the digest chain on top of it — it never substitutes
// for it.
func fetchModule(ctx context.Context, name string, p config.Parser, dir string) (string, error) {
	if p.OCI == nil {
		// allowLocalFile: a parser module is the one artifact developers rebuild
		// constantly, so a config may point at a locally built .wasm via file://.
		// The mandatory SHA-256 is unchanged — only the transport differs.
		path, err := binmanager.DownloadAndVerifySHA256(ctx, p.URL, p.Hash, dir, name, true)
		if err != nil {
			return "", fmt.Errorf("download parser module: %w", err)
		}
		return path, nil
	}

	path, err := fetchOCIModule(ctx, p.OCI.Ref, p.OCI.Digest, p.Hash, dir, name)
	if err != nil {
		return "", fmt.Errorf("pull parser module: %w", err)
	}
	// Re-check the materialized file. Logically redundant — the manifest pivot
	// compared the layer digest to this same hash, and PullBlob hashed the
	// stream — and kept deliberately: it is the only check that names p.Hash
	// AFTER the bytes exist on disk, so "the config hash is verified on every
	// transport" stays true by grep rather than by reasoning. ~1 ms for 377 KiB,
	// once per module.
	if err := binmanager.VerifyFileHashPublic(path, p.Hash, binmanager.BinHashTypeSHA256); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("hash verification failed: %w", err)
	}
	return path, nil
}

// storedModuleIsValid reports whether the module already on disk is the one the
// config declares, and discards it when it is not.
//
// A bare os.Stat used to be enough on the assumption that only a verified
// download can create this file. It is not: a store is also filled by OCI
// bundle seeding, by a restored CI cache, by an image layer, or by hand — none
// of which the config's mandatory SHA-256 has necessarily ever been applied to.
// Re-hashing here makes the store self-healing regardless of how the bytes
// arrived, and makes the hash mean what the policy says it means: nothing is
// loaded that was not checked against it.
//
// The cost is one hash of a ~400 KiB file per Prewarm or first Acquire — the
// compiled module is cached by key, so this is nowhere near the per-parse path.
func storedModuleIsValid(wasmPath string, p config.Parser) bool {
	if _, err := os.Stat(wasmPath); err != nil {
		return false
	}
	if err := binmanager.VerifyFileHashPublic(wasmPath, p.Hash, binmanager.BinHashTypeSHA256); err != nil {
		log.Warn("stored parser module does not match its declared SHA-256; discarding it and fetching again",
			zap.String("path", wasmPath),
			zap.Error(err),
		)
		if rmErr := os.RemoveAll(filepath.Dir(wasmPath)); rmErr != nil {
			log.Warn("failed to remove the mismatched parser module",
				zap.String("path", wasmPath), zap.Error(rmErr))
		}
		return false
	}
	return true
}

// cacheKey is the XXH3-128 content-addressed key for a parser declaration.
//
// The key is derived from the module's SHA-256 and nothing else, so the same
// module lands in the same directory however it was obtained: a release URL, a
// registry mirror, a locally built file, or a layer of an OCI bundle. That is
// what lets a bundle producer and a consumer who fetches from their own mirror
// agree on a store path without republishing anything — keying on the source as
// well would move the directory the moment a consumer re-pointed the URL, and
// the bundle layer built against the old spelling would silently never match.
//
// XXH3, not a crypto hash: this is an internal key, never compared against a
// value from outside. The SHA-256 it is derived from is what actually gates the
// content. The "parser-v2" prefix is domain separation, and names this layout
// so the next migration costs one character. The module's own version lives in
// its `describe` output rather than the config, so it is not part of the key.
func cacheKey(p config.Parser) string {
	return hashutil.XXH3Multi([]byte("parser-v2"), []byte(p.Hash))
}

// moduleDir returns the content-addressed directory for a parser:
// {parsersPath}/{name}/{xxh3(url,hash,version)}.
func moduleDir(name string, p config.Parser) string {
	return filepath.Join(env.GetParsersPath(), name, cacheKey(p))
}

// ModuleStorePath is the single source of truth for a parser module's
// content-addressed store directory ({store}/.parsers/{name}/{xxh3(url,hash)}).
// The OCI-bundle generator (dockerfile subtree, seed expected-subtree, re-verify)
// and the runtime resolver must agree on this path, so external callers compute
// it through here rather than re-deriving the xxh3 key. The directory holds the
// single verified module.wasm (see WASMFileName).
func ModuleStorePath(name string, p config.Parser) string {
	return moduleDir(name, p)
}

// WASMFileName is the fixed module filename inside a parser's content-addressed
// store directory, exported so bundle re-verification can target it.
const WASMFileName = wasmFileName
