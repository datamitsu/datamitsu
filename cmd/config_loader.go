package cmd

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/configcache"
	"github.com/datamitsu/datamitsu/internal/datamitsuignore"
	"github.com/datamitsu/datamitsu/internal/engine"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/facts"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/project"
	"github.com/datamitsu/datamitsu/internal/remotecfg"
	"github.com/datamitsu/datamitsu/internal/sourcefarm"
	"github.com/datamitsu/datamitsu/internal/timing"
	"github.com/datamitsu/datamitsu/internal/trace"
	"github.com/datamitsu/datamitsu/internal/traverser"
	"github.com/datamitsu/datamitsu/internal/version"

	"github.com/dop251/goja"
	"go.uber.org/zap"
)

// SkipRemoteConfig skips resolution of remote configs declared via getRemoteConfigs()
var SkipRemoteConfig bool

// resolvedRemoteURLs collects remote config URLs resolved during the last loadConfig call.
// Protected by resolvedRemoteURLsMu for safe concurrent access.
var (
	resolvedRemoteURLs   []string
	resolvedRemoteURLsMu sync.Mutex
)

// configChainFiles collects the on-disk files that fed the last config load, in
// chain order: the --before-config paths (or the before-configs the auto config
// declared), the auto-discovered git-root config, and every --config path.
//
// Source mode records them in the farm manifest's watch set, so a branch that
// changes any of them — including one that deletes the config outright — makes
// the farm stale on the next tool invocation. It is captured here rather than
// re-derived by the caller because the declared before-configs are only known
// after evaluating the auto config, which is exactly the work the watch set
// exists to avoid repeating.
var (
	configChainFiles   []string
	configChainFilesMu sync.Mutex
)

// ConfigChainFiles returns the file paths that fed the last config load.
func ConfigChainFiles() []string {
	configChainFilesMu.Lock()
	defer configChainFilesMu.Unlock()
	return append([]string(nil), configChainFiles...)
}

// setConfigChainFiles records the chain's on-disk files, every path made
// absolute.
//
// The paths that arrived from --config / --before-config are verbatim flag
// values and may be relative. Source mode stats them from whatever directory the
// shim happened to be invoked in, which is rarely the one that baked the farm: a
// relative entry in the watch set records "exists" at bake time and "missing" on
// every later stat, so the farm reads as permanently stale and every tool
// invocation pays a full rebake. (A same-named file in the invoking directory is
// the rarer inverse — it reads as fresh against a file that is not the config.)
// configChainArgs makes the same paths absolute for the same reason.
func setConfigChainFiles(sources []configSource) {
	paths := make([]string, 0, len(sources))
	for _, s := range sources {
		if s.path != "" {
			paths = append(paths, absOrSelf(s.path))
		}
	}
	configChainFilesMu.Lock()
	configChainFiles = paths
	configChainFilesMu.Unlock()
}

// configEvalNonDeterminism names the clock or entropy source read while the
// last config chain was evaluated, or "" if none was. Protected by its mutex
// for the same reason configChainFiles is.
var (
	configEvalNonDeterminism   string
	configEvalNonDeterminismMu sync.Mutex
)

// configEvalCacheable reports whether the config produced by the last load may
// be written to the config-eval cache (internal/configcache).
//
// A config that read the clock or Math.random is not a function of the cache
// key, so storing its result would serve one moment's answer forever — with no
// error, no stale marker and no external symptom, which makes it the one
// failure mode of this cache that a user could never diagnose. Refusing to
// store it costs nothing but the evaluation such a config was always going to
// pay.
func configEvalCacheable() bool {
	configEvalNonDeterminismMu.Lock()
	defer configEvalNonDeterminismMu.Unlock()
	return configEvalNonDeterminism == ""
}

// chainObservations accumulates what only evaluating the chain can know. Today
// that is the first non-deterministic source any layer read; the chain-level
// answer is the OR over its engines, since one impure layer makes the merged
// result impure.
type chainObservations struct {
	nonDeterminism string
}

func (o *chainObservations) record(e *engine.Engine) {
	if o == nil || e == nil {
		return
	}
	if o.nonDeterminism == "" && e.ObservedNonDeterminism() {
		o.nonDeterminism = e.NonDeterminismSource()
	}
}

// markIncomplete parks the global verdict at "not cacheable" for the duration
// of a load, so a chain that fails half-way cannot leave an earlier chain's
// verdict in place for a later reader.
func (o *chainObservations) markIncomplete() {
	configEvalNonDeterminismMu.Lock()
	configEvalNonDeterminism = "config evaluation did not complete"
	configEvalNonDeterminismMu.Unlock()
}

// publish records the chain-level verdict for configEvalCacheable and names the
// refusal at debug level. Debug, not warn: a config that reads the clock is
// unusual but legitimate, and the only consequence is that it evaluates every
// time — which is exactly what happens today for every config.
func (o *chainObservations) publish() {
	configEvalNonDeterminismMu.Lock()
	configEvalNonDeterminism = o.nonDeterminism
	configEvalNonDeterminismMu.Unlock()

	if o.nonDeterminism != "" {
		logger.Logger.Debug("config evaluation read a non-deterministic source; its result will not be cached",
			zap.String("source", o.nonDeterminism))
	}
}

type configSource struct {
	name      string
	path      string // file path (mutually exclusive with content)
	content   string // raw TS/JS content (for remote configs)
	isDefault bool   // true for the embedded default config
	isRemote  bool   // true for configs loaded via getRemoteConfigs()
}

type remoteConfigEntry struct {
	URL  string `json:"url"`
	Hash string `json:"hash"`
}

type beforeConfigEntry struct {
	Path string `json:"path"`
}

// loadConfig loads and parses the JavaScript configuration. It is the
// context-free entry point used by command handlers that do not thread a
// context; callers that hold one should use loadConfigWithPaths.
func loadConfig() (*config.Config, *config.SetupLayerMap, *goja.Runtime, error) {
	return loadConfigWithPaths(context.Background(), BeforeConfigPaths, NoAutoConfig, ConfigPaths)
}

// loadConfigForSetup loads config for the setup command. Unlike loadConfig it
// runs project detection (one git-root file walk) so setup content() functions
// receive context.projectTypes / context.projectLocations and can build
// per-ecosystem output (e.g. dependabot). Detection is gated to setup so other
// commands keep their detection-free, walk-free config load.
func loadConfigForSetup(ctx context.Context) (*config.Config, *config.SetupLayerMap, *goja.Runtime, error) {
	return loadConfigImpl(ctx, BeforeConfigPaths, NoAutoConfig, ConfigPaths,
		loadConfigOptions{detectProjectLocations: true, evaluateSetupContent: true, requireVM: true})
}

// loadConfigForChainHash loads config with setup content evaluated, which is
// what a chain hash is computed over. It is the only non-setup command that
// needs the layer map.
func loadConfigForChainHash() (*config.Config, *config.SetupLayerMap, *goja.Runtime, error) {
	return loadConfigImpl(context.Background(), BeforeConfigPaths, NoAutoConfig, ConfigPaths,
		loadConfigOptions{evaluateSetupContent: true})
}

// loadConfigForLockfileGen loads config without enforcing lockfile constraints.
// Used by config lockfile to allow bootstrapping lockfiles for apps that don't have one yet.
func loadConfigForLockfileGen() (*config.Config, *config.SetupLayerMap, *goja.Runtime, error) {
	return loadConfigImpl(context.Background(), BeforeConfigPaths, NoAutoConfig, ConfigPaths, loadConfigOptions{skipLockfileValidation: true})
}

// loadConfigForStore loads the config for store-level commands (seed/status/
// import). Unlike project commands they operate on the GLOBAL store, so a
// broken git context (no git binary, dubious-ownership errors inside
// containers) must not be fatal — the auto-discovered project config is
// simply skipped with a warning.
func loadConfigForStore(ctx context.Context) (*config.Config, error) {
	cfg, _, _, err := loadConfigImpl(ctx, BeforeConfigPaths, NoAutoConfig, ConfigPaths, loadConfigOptions{tolerateGitRootFailure: true})
	return cfg, err
}

// loadConfigWithPaths loads the default config and then sequentially loads
// additional configuration files, merging them together.
// Each config file is loaded in a separate VM and receives the previous config as input.
// Remote configs declared via getRemoteConfigs() are resolved depth-first.
func loadConfigWithPaths(ctx context.Context, beforeConfigPaths []string, noAutoConfig bool, configPaths []string) (cfg *config.Config, layerMap *config.SetupLayerMap, vm *goja.Runtime, err error) {
	return loadConfigImpl(ctx, beforeConfigPaths, noAutoConfig, configPaths, loadConfigOptions{})
}

// loadConfigOptions tweaks config loading for special-purpose commands.
type loadConfigOptions struct {
	skipLockfileValidation bool
	// tolerateGitRootFailure downgrades a failed git-root determination to a
	// warning (the auto config is skipped) instead of aborting. Store-level
	// commands set it; project commands keep the hard error so a broken git
	// context can't silently drop the project's config.
	tolerateGitRootFailure bool
	// detectProjectLocations runs project detection (one file walk) during the
	// eager content-evaluation pass and exposes the result to setup content()
	// functions as context.projectTypes / context.projectLocations. Off by
	// default so non-setup loads stay walk-free.
	detectProjectLocations bool
	// evaluateSetupContent renders every setup entry's content() into the layer
	// map. Off by default: it reads each entry's target file from disk and calls
	// into the VM once per entry per config layer — for the shared config, 57
	// reads and 100 calls — and only `setup`, `init` and `config chain-hash`
	// consume the result. Every other command discarded it, having paid for it.
	//
	// A load with this off returns an EMPTY layer map, not a partial one, so a
	// caller that needs the map must ask for it rather than find it thin.
	evaluateSetupContent bool
	// requireVM declares that the caller uses the returned *goja.Runtime.
	// loadConfigForSetup is the only such path (cmd/setup.go). A load that sets
	// it never serves from the config-evaluation cache: a hit has an evaluated
	// config and no VM, and a VM cannot be reconstructed from one. Gating on the
	// caller rather than on the artifact means the wrong shape is never produced
	// instead of being produced and hopefully not used.
	requireVM bool
}

func loadConfigImpl(ctx context.Context, beforeConfigPaths []string, noAutoConfig bool, configPaths []string, opts loadConfigOptions) (cfg *config.Config, lm *config.SetupLayerMap, vm *goja.Runtime, err error) {
	// Registered before the phase timer so it runs after it (defers are LIFO):
	// the report then includes the load's own total. This is the only call
	// site — every startup phase is recorded within this call, and commands
	// that os.Exit (exec) would never reach a process-exit one anyway.
	// PrintStartup prints at most once per process.
	defer timing.PrintStartup(os.Stderr)
	defer timing.StartStartupPhase(timing.PhaseLoadConfig)()
	defer trace.Start(trace.CatConfig, "loadConfig").End()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("config loading panic: %v", r)
		}
	}()

	// Determine rootPath and cwdPath for eager content evaluation
	cwdPath, cwdErr := os.Getwd()
	if cwdErr != nil {
		return nil, nil, nil, fmt.Errorf("failed to determine working directory: %w", cwdErr)
	}
	rootPath := cwdPath
	gitRootPath := ""

	// Discover the auto-loaded config from git root (unless --no-auto-config).
	var autoConfigPath string
	if !noAutoConfig {
		endGitRoot := timing.StartStartupPhase(timing.PhaseGitRoot)
		gitRoot, gitErr := facts.GetGitRoot(ctx)
		endGitRoot()
		if gitErr != nil {
			if traverser.HasGitDir(cwdPath) {
				if !opts.tolerateGitRootFailure {
					return nil, nil, nil, fmt.Errorf("failed to determine git root: %w", gitErr)
				}
				logger.Logger.Warn("cannot determine the git root; proceeding without the project config",
					zap.Error(gitErr))
			}
		}
		if gitErr == nil && gitRoot != "" {
			rootPath = gitRoot
			gitRootPath = gitRoot
			discovered, autoErr := discoverAutoConfig(gitRoot)
			if autoErr != nil {
				return nil, nil, nil, autoErr
			}
			autoConfigPath = discovered
		}
	}

	// Snapshot the auto config BEFORE resolving the chain, because resolving it
	// reads that file (for its declared before-configs). A snapshot taken
	// afterwards would record the state a concurrent edit left behind rather
	// than the state that was read, and would stamp a key fresh for bytes nobody
	// ran. It is the only file read this early — every other chain file is first
	// read during evaluation, which the post-evaluation re-check covers — so
	// hashing more here would only re-read megabytes to learn nothing.
	var priorChain map[string]configcache.ChainFile
	if autoConfigPath != "" && configCacheUsable(opts) {
		priorChain = hashConfigPaths([]string{autoConfigPath})
	}

	sources, srcErr := buildConfigSources(ctx, beforeConfigPaths, autoConfigPath, configPaths)
	if srcErr != nil {
		return nil, nil, nil, srcErr
	}
	setConfigChainFiles(sources)

	cache := newConfigCache(ctx, configCacheParams{
		sources:       sources,
		explicitChain: append(append([]string(nil), beforeConfigPaths...), configPaths...),
		noAutoConfig:  noAutoConfig,
		gitRoot:       gitRootPath,
		cwd:           cwdPath,
		prior:         priorChain,
		opts:          opts,
	})
	if entry, hit := cache.load(); hit {
		// Nothing was evaluated, so there is nothing new to store — and the
		// verdict of whatever chain ran last must not be left standing for a
		// later reader.
		markConfigServedFromCache()
		publishConfigWarnings(entry.Warnings)
		setResolvedRemoteURLs(entry.RemoteURLs)
		// An EMPTY layer map, never a partial one: ConfigSetup.Content is a live
		// goja value a hit cannot reconstruct, and a caller that needs it asked
		// for evaluateSetupContent, which never reaches this branch.
		emptyLayerMap := make(config.SetupLayerMap)
		return entry.Config, &emptyLayerMap, nil, nil
	}

	// Process all sources sequentially with eager content evaluation.
	// resolved: collects all remote URLs processed (for display/reporting).
	// stack: tracks URLs in the current recursion path (for cycle detection).
	var currentConfig *config.Config
	var lastVM *goja.Runtime
	obs := &chainObservations{}
	// Until the chain has finished evaluating, the verdict is "not cacheable":
	// an early failure must never leave the previous load's verdict standing.
	obs.markIncomplete()
	layerMap := make(config.SetupLayerMap)
	resolved := make(map[string]bool)
	stack := make(map[string]bool)

	// detectProjects runs project detection lazily and at most once (the file
	// walk is shared across config layers), returning the git-root-relative
	// {type, path} locations and their unique types for the eager content pass.
	// It is a no-op unless opts.detectProjectLocations is set.
	var (
		projFiles      []string
		projFilesReady bool
	)
	detectProjects := func(types config.MapOfProjectTypes) ([]string, []config.ProjectLocation) {
		if !opts.detectProjectLocations || rootPath == "" || len(types) == 0 {
			return nil, nil
		}
		if !projFilesReady {
			projFilesReady = true
			if files, walkErr := traverser.FindFiles(ctx, rootPath); walkErr == nil {
				projFiles = files
			} else {
				logger.Logger.Debug("project detection: file walk failed; setup project context will be empty",
					zap.Error(walkErr))
			}
		}
		locs := project.NewDetector(rootPath, types).DetectAllWithLocationsFromFiles(projFiles)
		return projectLocationsToConfig(rootPath, locs)
	}

	for _, source := range sources {
		result, resultEngine, processErr := processConfigSource(ctx, currentConfig, source, resolved, stack, opts, obs)
		if processErr != nil {
			return nil, nil, nil, processErr
		}
		resultVM := resultEngine.VM()

		if result.Setup != nil && opts.evaluateSetupContent {
			evalSpan := trace.Start(trace.CatConfig, "evaluateSetupContent")
			pTypes, pLocs := detectProjects(result.ProjectTypes)
			evaluatedContent := config.EvaluateInitContentWithProjects(result, resultVM, rootPath, cwdPath, layerMap, pTypes, pLocs)
			config.MergeSetupLayers(layerMap, source.name, evaluatedContent, result.Setup)
			evalSpan.EndWith(trace.A("source", source.name), trace.A("entries", len(result.Setup)))
			// content() runs arbitrary config JS, so it is observed too.
			obs.record(resultEngine)
		}

		currentConfig = result
		lastVM = resultVM
	}
	obs.publish()

	// Collect resolved remote URLs from resolved map
	remoteURLs := make([]string, 0, len(resolved))
	for url := range resolved {
		remoteURLs = append(remoteURLs, url)
	}
	sort.Strings(remoteURLs)
	setResolvedRemoteURLs(remoteURLs)

	validateSpan := trace.Start(trace.CatConfig, "validateConfig")
	defer validateSpan.EndWith(
		trace.A("apps", len(currentConfig.Apps)),
		trace.A("tools", len(currentConfig.Tools)),
	)

	// Collected as well as logged: a hit must reproduce the warnings its miss
	// printed, or a command's stderr would depend on whether a cache happened to
	// be warm.
	var configWarnings []string
	var warnings []string
	if opts.skipLockfileValidation {
		warnings, err = config.ValidateAppsSkipLockfile(currentConfig.Apps, currentConfig.Runtimes)
	} else {
		warnings, err = config.ValidateApps(currentConfig.Apps, currentConfig.Runtimes)
	}
	configWarnings = append(configWarnings, warnings...)
	publishConfigWarnings(warnings)
	if err != nil {
		return nil, nil, nil, err
	}

	if err := config.ValidateBundles(currentConfig.Bundles, currentConfig.Apps); err != nil {
		return nil, nil, nil, err
	}

	if err := config.ValidateRuntimes(currentConfig.Runtimes); err != nil {
		return nil, nil, nil, err
	}

	if err := config.ValidateSetup(currentConfig.Setup); err != nil {
		return nil, nil, nil, err
	}
	setupToolWarnings := config.ValidateSetupToolRefs(currentConfig.Setup, currentConfig.Tools)
	configWarnings = append(configWarnings, setupToolWarnings...)
	publishConfigWarnings(setupToolWarnings)

	if err := config.ValidateTools(currentConfig.Tools, currentConfig.Parsers); err != nil {
		return nil, nil, nil, err
	}

	if err := config.ValidateExecution(currentConfig.Execution); err != nil {
		return nil, nil, nil, err
	}

	if err := config.ValidateToolFacets(currentConfig.Tools); err != nil {
		return nil, nil, nil, err
	}

	if err := config.ValidateOCI(currentConfig.OCI); err != nil {
		return nil, nil, nil, err
	}

	if err := config.ValidateParsers(currentConfig.Parsers); err != nil {
		return nil, nil, nil, err
	}

	if err := config.ValidateLsp(currentConfig.Lsp, currentConfig.Tools); err != nil {
		return nil, nil, nil, err
	}

	if len(currentConfig.IgnoreRules) > 0 {
		if _, parseErr := datamitsuignore.ParseRules(currentConfig.IgnoreRules); parseErr != nil {
			return nil, nil, nil, fmt.Errorf("invalid ignoreRules in config: %w", parseErr)
		}
	}

	// The stored config is the post-validation one, which is sound only because
	// ldflags.Version is in the key: a binary that validates differently cannot
	// read this entry.
	cache.save(&configcache.Entry{
		Config:     currentConfig,
		Warnings:   configWarnings,
		RemoteURLs: remoteURLs,
	})

	return currentConfig, &layerMap, lastVM, nil
}

// publishConfigWarnings emits config validation warnings. It is the single
// place they are written, so a hit replaying stored warnings and a miss
// producing them are indistinguishable.
func publishConfigWarnings(warnings []string) {
	for _, w := range warnings {
		logger.Logger.Warn(w, zap.String("source", "config"))
	}
}

// setResolvedRemoteURLs records the remote configs the chain resolved, for
// `devtools verify-all`. A hit restores the stored list rather than leaving the
// previous load's.
func setResolvedRemoteURLs(urls []string) {
	resolvedRemoteURLsMu.Lock()
	resolvedRemoteURLs = append([]string(nil), urls...)
	resolvedRemoteURLsMu.Unlock()
}

// markConfigServedFromCache parks the cacheability verdict after a hit. No
// chain was evaluated, so there is nothing to store and no observation to
// report — and, crucially, no earlier chain's verdict may be left standing.
func markConfigServedFromCache() {
	configEvalNonDeterminismMu.Lock()
	configEvalNonDeterminism = "config was served from the evaluation cache"
	configEvalNonDeterminismMu.Unlock()
}

// projectLocationsToConfig converts absolute detector locations into the
// git-root-relative {type, path} shape exposed to setup content() functions
// (POSIX slashes, "." for the root), deduped and deterministically sorted. It
// also returns the unique sorted list of detected types. Shared by the eager
// config-load detection and the setup install path so both expose identical data.
func projectLocationsToConfig(rootPath string, locs []project.ProjectLocation) ([]string, []config.ProjectLocation) {
	out := make([]config.ProjectLocation, 0, len(locs))
	seen := make(map[string]bool)
	typeSet := make(map[string]bool)
	for _, l := range locs {
		rel, err := filepath.Rel(rootPath, l.Path)
		if err != nil {
			rel = l.Path
		}
		rel = filepath.ToSlash(rel)
		if rel == "" {
			rel = "."
		}
		key := l.Type + "\x00" + rel
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, config.ProjectLocation{Type: l.Type, Path: rel})
		typeSet[l.Type] = true
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Type != out[b].Type {
			return out[a].Type < out[b].Type
		}
		return out[a].Path < out[b].Path
	})

	types := make([]string, 0, len(typeSet))
	for t := range typeSet {
		types = append(types, t)
	}
	sort.Strings(types)
	return types, out
}

// buildConfigSources assembles the ordered list of config sources to process.
// Order: default → --before-config flag paths → declared before-configs → auto
// → --config paths. Declared before-configs come from the auto config's
// getBeforeConfigs() (read via the discoverBeforeConfigs pre-pass) and are
// honoured only when no --before-config flag was passed — the flag wins, which
// avoids double-loading the shared config when the pnpm wrapper is used.
// autoConfigPath is empty when there is no git-root config or --no-auto-config
// was given.
func buildConfigSources(ctx context.Context, beforeConfigPaths []string, autoConfigPath string, configPaths []string) ([]configSource, error) {
	var sources []configSource
	sources = append(sources, configSource{name: "default", isDefault: true})

	// --before-config flag paths (for wrappers/libraries, before auto-discovery).
	for _, p := range beforeConfigPaths {
		sources = append(sources, configSource{name: p, path: p})
	}

	// Record the substitution rather than performing it silently: when the flag
	// is present the auto config's own getBeforeConfigs() is never consulted, so
	// the config layer a user believes they pinned may not be the one that ran
	// (the docker images always pass --before-config from their ENTRYPOINT).
	// Debug, not info: this is the normal path for every pnpm-wrapper
	// invocation, and naming what was skipped would mean evaluating the auto
	// config in a second engine just to log it (discoverBeforeConfigs) — real
	// cost on the hot path to describe an intended override.
	if autoConfigPath != "" && len(beforeConfigPaths) > 0 {
		logger.Logger.Debug(
			"--before-config given; declared getBeforeConfigs() in the auto config are not consulted",
			zap.String("autoConfig", autoConfigPath),
			zap.Strings("beforeConfig", beforeConfigPaths),
		)
	}

	// Declared before-configs from the auto config — only when no flag overrides.
	if autoConfigPath != "" && len(beforeConfigPaths) == 0 {
		declared, err := discoverBeforeConfigs(ctx, autoConfigPath)
		if err != nil {
			return nil, err
		}
		for _, p := range declared {
			sources = append(sources, configSource{name: p, path: p})
		}
	}

	// Auto-discovered git-root config.
	if autoConfigPath != "" {
		sources = append(sources, configSource{name: "auto", path: autoConfigPath})
	}

	// Explicit --config paths.
	for _, p := range configPaths {
		sources = append(sources, configSource{name: p, path: p})
	}

	return sources, nil
}

// processConfigSource loads a single config source, resolves any remote configs
// declared via getRemoteConfigs() depth-first, then calls getConfig with the
// accumulated input. The resolved map collects all processed URLs for reporting.
// The stack map tracks URLs in the current recursion path for cycle detection;
// URLs are added before recursing and removed after, so shared (diamond)
// dependencies are allowed while true cycles are still caught.
func processConfigSource(ctx context.Context, input *config.Config, source configSource, resolved map[string]bool, stack map[string]bool, opts loadConfigOptions, obs *chainObservations) (*config.Config, *engine.Engine, error) {
	sourceSpan := trace.Start(trace.CatConfig, "configSource")
	defer sourceSpan.EndWith(trace.A("name", source.name))

	e, err := engine.NewWithOptions(ctx, BinaryCommandOverride,
		engine.Options{TolerateGitRootFailure: opts.tolerateGitRootFailure})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create engine: %w", err)
	}
	vm := e.VM()

	// Load JS into VM
	switch {
	case source.isDefault:
		configString, defaultErr := config.GetDefaultConfig()
		if defaultErr != nil {
			return nil, nil, fmt.Errorf("failed to get default config: %w", defaultErr)
		}
		if _, runErr := e.RunWithTimeout(configString, 10*time.Second); runErr != nil {
			return nil, nil, fmt.Errorf("failed to run default config: %w", runErr)
		}
	case source.content != "":
		if loadErr := loadConfigString(e, source.content, source.name); loadErr != nil {
			return nil, nil, loadErr
		}
	default:
		if loadErr := loadConfigFile(e, source.path); loadErr != nil {
			return nil, nil, fmt.Errorf("failed to load config from %s: %w", source.path, loadErr)
		}
	}

	// Validate getMinVersion() for non-default, non-remote config sources.
	// Default config is embedded and always matches the current version.
	// Remote configs (loaded via getRemoteConfigs) are library configs that
	// inherit the version requirement from their parent.
	if !source.isDefault && !source.isRemote {
		sourceLabel := source.name
		if source.path != "" {
			sourceLabel = source.path
		}

		getMinVersionVal := vm.Get("getMinVersion")
		if getMinVersionVal == nil || goja.IsUndefined(getMinVersionVal) || goja.IsNull(getMinVersionVal) {
			return nil, nil, fmt.Errorf("config %s: must export getMinVersion() function returning semver string", sourceLabel)
		}

		minVersionFunc, ok := goja.AssertFunction(getMinVersionVal)
		if !ok {
			return nil, nil, fmt.Errorf("config %s: getMinVersion must be a function", sourceLabel)
		}

		minVersionResult, callErr := e.CallWithTimeout(minVersionFunc, 10*time.Second)
		if callErr != nil {
			return nil, nil, fmt.Errorf("config %s: getMinVersion() failed: %w", sourceLabel, callErr)
		}

		minVersionStr, ok := minVersionResult.Export().(string)
		if !ok {
			return nil, nil, fmt.Errorf("config %s: getMinVersion() must return a string", sourceLabel)
		}
		if minVersionStr == "" {
			return nil, nil, fmt.Errorf("config %s: getMinVersion() must return non-empty string", sourceLabel)
		}

		skipped, err := version.CompareVersions(ldflags.Version, minVersionStr)
		if err != nil {
			return nil, nil, fmt.Errorf("config %s: %w", sourceLabel, err)
		}
		if skipped {
			logger.Logger.Warn(
				"version check skipped: current build is unstable — proceeding at your own risk",
				zap.String("source", sourceLabel),
				zap.String("current", ldflags.Version),
				zap.String("required", minVersionStr),
			)
		}
	}

	// Resolve remote configs depth-first (unless SkipRemoteConfig is set)
	chainedInput := input
	if !SkipRemoteConfig {
		if fn, ok := goja.AssertFunction(vm.Get("getRemoteConfigs")); ok {
			result, callErr := e.CallWithTimeout(fn, 10*time.Second)
			if callErr != nil {
				return nil, nil, fmt.Errorf("failed to call getRemoteConfigs in %s: %w", source.name, callErr)
			}

			var entries []remoteConfigEntry
			if exportErr := vm.ExportTo(result, &entries); exportErr != nil {
				return nil, nil, fmt.Errorf("failed to parse getRemoteConfigs result in %s: %w", source.name, exportErr)
			}

			for _, entry := range entries {
				if entry.URL == "" {
					return nil, nil, fmt.Errorf("remote config entry in %s: url is required", source.name)
				}
				if entry.Hash == "" {
					return nil, nil, fmt.Errorf("remote config %s: hash is required", entry.URL)
				}
				if stack[entry.URL] {
					return nil, nil, fmt.Errorf("circular remote config dependency: %s", entry.URL)
				}
				stack[entry.URL] = true
				resolved[entry.URL] = true

				content, resolveErr := remotecfg.Resolve(ctx, entry.URL, entry.Hash, env.GetStorePath())
				if resolveErr != nil {
					delete(stack, entry.URL)
					return nil, nil, resolveErr
				}

				remoteResult, _, remoteErr := processConfigSource(ctx, chainedInput, configSource{
					name:     entry.URL,
					content:  content,
					isRemote: true,
				}, resolved, stack, opts, obs)
				delete(stack, entry.URL)
				if remoteErr != nil {
					return nil, nil, remoteErr
				}
				chainedInput = remoteResult
			}
		}
	}

	// Call getConfig with accumulated input
	getConfigFunc, ok := goja.AssertFunction(vm.Get("getConfig"))
	if !ok {
		return nil, nil, fmt.Errorf("getConfig is not a function in %s", source.name)
	}

	var inputVal goja.Value
	if chainedInput == nil {
		inputVal = vm.NewObject()
	} else {
		// Strip IgnoreRules from input passed to JS so that only Go handles
		// the append-merge. Without this, JS spread syntax ({...input}) would
		// include old rules, and the Go merge below would duplicate them.
		inputCopy := *chainedInput
		inputCopy.IgnoreRules = nil
		inputVal = vm.ToValue(&inputCopy)
	}

	endGetConfig := timing.StartStartupPhase(timing.PhaseGetConfig)
	getConfigSpan := trace.Start(trace.CatConfig, "callGetConfig")
	resultVal, callErr := e.CallWithTimeout(getConfigFunc, 10*time.Second, inputVal)
	getConfigSpan.EndWith(trace.A("source", source.name))
	endGetConfig()
	if callErr != nil {
		return nil, nil, fmt.Errorf("failed to call getConfig in %s: %w", source.name, callErr)
	}

	exportSpan := trace.Start(trace.CatConfig, "exportConfigResult")
	parsedConfig, parseErr := parseConfigResult(vm, resultVal)
	exportSpan.EndWith(trace.A("source", source.name))
	if parseErr != nil {
		return nil, nil, fmt.Errorf("failed to parse config from %s: %w", source.name, parseErr)
	}

	// IgnoreRules use append semantics: previous rules are prepended to new ones
	if chainedInput != nil && len(chainedInput.IgnoreRules) > 0 {
		parsedConfig.IgnoreRules = append(chainedInput.IgnoreRules, parsedConfig.IgnoreRules...)
	}

	// oci chains as a scalar through {...input} spreads; a layer that reshapes
	// its output without spreading silently drops it (there is no re-inject).
	// Surface that silent drop at debug level.
	if chainedInput != nil && chainedInput.OCI != nil && parsedConfig.OCI == nil {
		logger.Logger.Debug("config layer dropped inherited oci declaration (missing {...input} spread?)",
			zap.String("source", source.name))
	}

	// Recorded after every call into this layer's VM — the top-level run,
	// getRemoteConfigs and getConfig — so any clock or entropy read anywhere in
	// the layer reaches the chain-level verdict.
	obs.record(e)

	return parsedConfig, e, nil
}

// parseConfigResult converts getConfig result to config.Config struct
func parseConfigResult(vm *goja.Runtime, resultVal goja.Value) (*config.Config, error) {
	cfg := &config.Config{}

	if err := vm.ExportTo(resultVal, cfg); err != nil {
		return nil, fmt.Errorf("failed to export config: %w", err)
	}

	// Initialize empty maps if they are nil
	if cfg.ProjectTypes == nil {
		cfg.ProjectTypes = make(config.MapOfProjectTypes)
	}
	if cfg.Tools == nil {
		cfg.Tools = make(config.MapOfTools)
	}

	// Handle setup configs specially to preserve content functions
	resultObj := resultVal.ToObject(vm)
	if setupVal := resultObj.Get("setup"); setupVal != nil && setupVal != goja.Undefined() {
		setupObj := setupVal.ToObject(vm)
		cfg.Setup = make(config.MapOfConfigSetup)

		for _, key := range setupObj.Keys() {
			cfgSetupVal := setupObj.Get(key)
			cfgSetupObj := cfgSetupVal.ToObject(vm)

			var cfgSetup config.ConfigSetup

			if err := vm.ExportTo(cfgSetupVal, &cfgSetup); err != nil {
				return nil, fmt.Errorf("failed to export setup config %s: %w", key, err)
			}

			if contentVal := cfgSetupObj.Get("content"); contentVal != nil && contentVal != goja.Undefined() {
				cfgSetup.Content = contentVal
			}

			if linkTargetVal := cfgSetupObj.Get("linkTarget"); linkTargetVal != nil && linkTargetVal != goja.Undefined() {
				cfgSetup.LinkTarget = linkTargetVal.String()
			}

			cfg.Setup[key] = cfgSetup
		}
	}

	return cfg, nil
}

// discoverAutoConfig searches for datamitsu.config.js, datamitsu.config.mjs and datamitsu.config.ts at the git root.
// Returns the path if exactly one exists, empty string if none exists,
// or an error if more than one exist.
func discoverAutoConfig(gitRoot string) (string, error) {
	// The same list source-mode's watch set uses, so a farm cannot be reported
	// fresh for a tree that has since gained a second candidate and stopped
	// loading here.
	candidates := make([]string, 0, len(sourcefarm.AutoConfigNames))
	for _, name := range sourcefarm.AutoConfigNames {
		candidates = append(candidates, filepath.Join(gitRoot, name))
	}

	var found []string
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			found = append(found, filepath.Base(p))
		}
	}

	if len(found) > 1 {
		return "", fmt.Errorf("multiple config files found at %s (%s): remove all but one", gitRoot, strings.Join(found, ", "))
	}
	if len(found) == 1 {
		return filepath.Join(gitRoot, found[0]), nil
	}
	return "", nil
}

// discoverBeforeConfigs evaluates the auto-discovered git-root config in an
// isolated VM and reads its getBeforeConfigs() declaration, returning the
// resolved absolute paths in declared order (deduped). Relative paths are
// resolved against the config file's directory; absolute paths are used as-is.
// Each declared path must exist. An absent getBeforeConfigs function returns
// (nil, nil) — only the auto config is consulted, so nested declarations in
// other layers are never read (scope is enforced structurally).
func discoverBeforeConfigs(ctx context.Context, autoConfigPath string) ([]string, error) {
	defer timing.StartStartupPhase(timing.PhaseDiscoverBeforeConfigs)()
	defer trace.Start(trace.CatConfig, "discoverBeforeConfigs").End()

	e, err := engine.New(ctx, BinaryCommandOverride)
	if err != nil {
		return nil, fmt.Errorf("failed to create engine: %w", err)
	}
	if loadErr := loadConfigFile(e, autoConfigPath); loadErr != nil {
		return nil, fmt.Errorf("failed to load config from %s: %w", autoConfigPath, loadErr)
	}
	vm := e.VM()

	fn, ok := goja.AssertFunction(vm.Get("getBeforeConfigs"))
	if !ok {
		return nil, nil
	}

	result, callErr := e.CallWithTimeout(fn, 10*time.Second)
	if callErr != nil {
		return nil, fmt.Errorf("failed to call getBeforeConfigs in %s: %w", autoConfigPath, callErr)
	}

	var entries []beforeConfigEntry
	if exportErr := vm.ExportTo(result, &entries); exportErr != nil {
		return nil, fmt.Errorf("failed to parse getBeforeConfigs result in %s: %w", autoConfigPath, exportErr)
	}

	baseDir := filepath.Dir(autoConfigPath)
	seen := make(map[string]bool)
	var paths []string
	for _, entry := range entries {
		if entry.Path == "" {
			return nil, fmt.Errorf("before config entry in %s: path is required", autoConfigPath)
		}
		resolved := entry.Path
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(baseDir, resolved)
		}
		resolved = filepath.Clean(resolved)
		if seen[resolved] {
			continue
		}
		if _, statErr := os.Stat(resolved); statErr != nil {
			return nil, fmt.Errorf("before config %s: %w", resolved, statErr)
		}
		seen[resolved] = true
		paths = append(paths, resolved)
	}
	return paths, nil
}

// isPlainJavaScriptSource reports whether a config source is already plain
// JavaScript and can therefore skip the esbuild type-stripping pass.
//
// The decision is made from the file extension only, never by sniffing the
// content: a heuristic that guesses wrong hands goja a file still full of
// TypeScript and surfaces as a syntax error far from its cause. Anything
// unrecognised — .ts/.mts/.cts, a path with no extension, an OCI reference —
// keeps going through esbuild, which is a no-op for JavaScript that is already
// valid.
//
// ref is either a filesystem path or, for configs pulled in via
// getRemoteConfigs(), the source URL; the URL's path component carries the same
// extension, so both route through this one check.
func isPlainJavaScriptSource(ref string) bool {
	switch configSourceExt(ref) {
	case ".js", ".mjs":
		return true
	default:
		return false
	}
}

// configSourceExt returns ref's lowercased extension, or "" when it has none.
// For a ref carrying a scheme only the path component counts, so a bare host
// (https://example.js) and an OCI reference (oci://ghcr.io/org/cfg:v1) both
// come back without an extension instead of looking like JavaScript.
func configSourceExt(ref string) string {
	if scheme, rest, isURL := strings.Cut(ref, "://"); isURL && scheme != "" {
		_, urlPath, hasPath := strings.Cut(rest, "/")
		if !hasPath {
			return ""
		}
		urlPath, _, _ = strings.Cut(urlPath, "#")
		urlPath, _, _ = strings.Cut(urlPath, "?")
		return strings.ToLower(path.Ext(urlPath))
	}
	return strings.ToLower(filepath.Ext(ref))
}

// prepareConfigSource returns the JavaScript to execute for a config source,
// running esbuild only when the source may contain TypeScript. Skipping it for
// plain JavaScript is the single largest esbuild cost per invocation: the
// shared oci-ghcr config is a ~2 MB file that never had types to strip.
//
// The PhaseStripTypes timing observation doubles as the seam tests use to check
// which sources actually reach esbuild.
func prepareConfigSource(content, ref string) (string, error) {
	if isPlainJavaScriptSource(ref) {
		return content, nil
	}

	endStrip := timing.StartStartupPhase(timing.PhaseStripTypes)
	defer endStrip()
	return config.StripTypes(content)
}

// loadConfigFile loads and executes a single configuration file in the given engine.
func loadConfigFile(e *engine.Engine, path string) error {
	readSpan := trace.Start(trace.CatConfig, "readConfigFile")
	data, err := os.ReadFile(path)
	readSpan.EndWith(trace.A("path", path), trace.A("bytes", len(data)))
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	jsCode, err := prepareConfigSource(string(data), path)
	if err != nil {
		return fmt.Errorf("failed to strip types: %w", err)
	}

	// The single largest unattributed cost in a cold `datamitsu <anything>`:
	// goja compiles and then executes the whole config source, and the shared
	// wrapper config is a ~2 MB file. Compilation and execution are separated
	// here rather than left inside RunWithTimeout because they answer different
	// questions — parsing scales with source bytes, the top-level run with what
	// the config actually does at load time — and only the split says which one
	// a cache would have to eliminate.
	compileSpan := trace.Start(trace.CatConfig, "compileConfig")
	program, err := goja.Compile(path, jsCode, false)
	compileSpan.EndWith(trace.A("path", path), trace.A("bytes", len(jsCode)))
	if err != nil {
		return fmt.Errorf("failed to compile config: %w", err)
	}

	runSpan := trace.Start(trace.CatConfig, "runConfigTopLevel")
	_, err = e.RunProgramWithTimeout(program, 10*time.Second)
	runSpan.EndWith(trace.A("path", path))
	if err != nil {
		return fmt.Errorf("failed to execute config: %w", err)
	}

	return nil
}

// loadConfigString loads and executes a config from a string content in the given engine.
// Types are stripped first unless sourceName identifies the content as plain
// JavaScript (see isPlainJavaScriptSource).
func loadConfigString(e *engine.Engine, content, sourceName string) error {
	jsCode, err := prepareConfigSource(content, sourceName)
	if err != nil {
		return fmt.Errorf("failed to strip types from %s: %w", sourceName, err)
	}

	runSpan := trace.Start(trace.CatConfig, "vmRunConfig")
	_, err = e.RunWithTimeout(jsCode, 10*time.Second)
	runSpan.EndWith(trace.A("path", sourceName), trace.A("bytes", len(jsCode)))
	if err != nil {
		return fmt.Errorf("failed to execute config %s: %w", sourceName, err)
	}

	return nil
}
