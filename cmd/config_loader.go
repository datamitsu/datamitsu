package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/datamitsuignore"
	"github.com/datamitsu/datamitsu/internal/engine"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/facts"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/remotecfg"
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
}

func loadConfigImpl(ctx context.Context, beforeConfigPaths []string, noAutoConfig bool, configPaths []string, opts loadConfigOptions) (cfg *config.Config, lm *config.SetupLayerMap, vm *goja.Runtime, err error) {
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

	// Discover the auto-loaded config from git root (unless --no-auto-config).
	var autoConfigPath string
	if !noAutoConfig {
		gitRoot, gitErr := facts.GetGitRoot(ctx)
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
			discovered, autoErr := discoverAutoConfig(gitRoot)
			if autoErr != nil {
				return nil, nil, nil, autoErr
			}
			autoConfigPath = discovered
		}
	}

	sources, srcErr := buildConfigSources(ctx, beforeConfigPaths, autoConfigPath, configPaths)
	if srcErr != nil {
		return nil, nil, nil, srcErr
	}

	// Process all sources sequentially with eager content evaluation.
	// resolved: collects all remote URLs processed (for display/reporting).
	// stack: tracks URLs in the current recursion path (for cycle detection).
	var currentConfig *config.Config
	var lastVM *goja.Runtime
	layerMap := make(config.SetupLayerMap)
	resolved := make(map[string]bool)
	stack := make(map[string]bool)
	for _, source := range sources {
		result, resultVM, processErr := processConfigSource(ctx, currentConfig, source, resolved, stack, opts)
		if processErr != nil {
			return nil, nil, nil, processErr
		}

		if result.Setup != nil {
			evaluatedContent := config.EvaluateInitContent(result, resultVM, rootPath, cwdPath, layerMap)
			config.MergeSetupLayers(layerMap, source.name, evaluatedContent, result.Setup)
		}

		currentConfig = result
		lastVM = resultVM
	}

	// Collect resolved remote URLs from resolved map
	resolvedRemoteURLsMu.Lock()
	resolvedRemoteURLs = nil
	for url := range resolved {
		resolvedRemoteURLs = append(resolvedRemoteURLs, url)
	}
	sort.Strings(resolvedRemoteURLs)
	resolvedRemoteURLsMu.Unlock()

	var warnings []string
	if opts.skipLockfileValidation {
		warnings, err = config.ValidateAppsSkipLockfile(currentConfig.Apps, currentConfig.Runtimes)
	} else {
		warnings, err = config.ValidateApps(currentConfig.Apps, currentConfig.Runtimes)
	}
	for _, w := range warnings {
		logger.Logger.Warn(w, zap.String("source", "config"))
	}
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
	for _, w := range config.ValidateSetupToolRefs(currentConfig.Setup, currentConfig.Tools) {
		logger.Logger.Warn(w, zap.String("source", "config"))
	}

	if err := config.ValidateTools(currentConfig.Tools); err != nil {
		return nil, nil, nil, err
	}

	if err := config.ValidateOCI(currentConfig.OCI); err != nil {
		return nil, nil, nil, err
	}

	if len(currentConfig.IgnoreRules) > 0 {
		if _, parseErr := datamitsuignore.ParseRules(currentConfig.IgnoreRules); parseErr != nil {
			return nil, nil, nil, fmt.Errorf("invalid ignoreRules in config: %w", parseErr)
		}
	}

	return currentConfig, &layerMap, lastVM, nil
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
func processConfigSource(ctx context.Context, input *config.Config, source configSource, resolved map[string]bool, stack map[string]bool, opts loadConfigOptions) (*config.Config, *goja.Runtime, error) {
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
				}, resolved, stack, opts)
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

	resultVal, callErr := e.CallWithTimeout(getConfigFunc, 10*time.Second, inputVal)
	if callErr != nil {
		return nil, nil, fmt.Errorf("failed to call getConfig in %s: %w", source.name, callErr)
	}

	parsedConfig, parseErr := parseConfigResult(vm, resultVal)
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

	return parsedConfig, vm, nil
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
	candidates := []string{
		filepath.Join(gitRoot, ldflags.PackageName+".config.js"),
		filepath.Join(gitRoot, ldflags.PackageName+".config.mjs"),
		filepath.Join(gitRoot, ldflags.PackageName+".config.ts"),
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

// loadConfigFile loads and executes a single configuration file in the given engine.
func loadConfigFile(e *engine.Engine, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	jsCode, err := config.StripTypes(string(data))
	if err != nil {
		return fmt.Errorf("failed to strip types: %w", err)
	}

	if _, err := e.RunWithTimeout(jsCode, 10*time.Second); err != nil {
		return fmt.Errorf("failed to execute config: %w", err)
	}

	return nil
}

// loadConfigString loads and executes a config from a string content in the given engine.
// The content is treated as TypeScript and types are stripped before execution.
func loadConfigString(e *engine.Engine, content, sourceName string) error {
	jsCode, err := config.StripTypes(content)
	if err != nil {
		return fmt.Errorf("failed to strip types from %s: %w", sourceName, err)
	}

	if _, err := e.RunWithTimeout(jsCode, 10*time.Second); err != nil {
		return fmt.Errorf("failed to execute config %s: %w", sourceName, err)
	}

	return nil
}
