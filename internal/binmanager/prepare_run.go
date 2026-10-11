package binmanager

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/prepare"
	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
)

// prepareTimeout reads the effective install timeout: the step shares the
// install budget's knob (visible in `config runtime`), separate from the
// installation of the app itself.
func prepareTimeout() time.Duration {
	eff, err := runtimeconfig.Get()
	if err != nil {
		eff = runtimeconfig.Compute()
	}
	return time.Duration(eff.InstallTimeoutSeconds) * time.Second
}

// EnsurePrepared runs the prepare steps of the app's closure (dependencies
// first) after installation.
func (bm *BinManager) EnsurePrepared(ctx context.Context, appName string) error {
	names, err := AppDependencyClosure(bm.mapOfApps, []string{appName})
	if err != nil {
		return err
	}
	// The closure is dependencies-first; steps run in that order.
	for _, name := range names {
		app, ok := bm.mapOfApps[name]
		if !ok || app.Prepare == nil {
			continue
		}
		if err := bm.runPrepareStep(ctx, name); err != nil {
			if name == appName {
				return err
			}
			return fmt.Errorf("prepare dependency %q: %w", name, err)
		}
	}
	// Sweep abandoned layouts opportunistically: every readiness pass is a
	// good moment, and the sweep is cheap when nothing is stale.
	prepare.SweepStale(env.GetStorePath())
	return nil
}

// PreparedStateReady reports whether the app's prepared state is present
// (marker file included) without running anything. An app without a
// declared step is always ready.
func (bm *BinManager) PreparedStateReady(appName string) (bool, error) {
	app, ok := bm.mapOfApps[appName]
	if !ok {
		return false, &AppNotFoundError{AppName: appName}
	}
	if app.Prepare == nil {
		return true, nil
	}
	key, err := bm.ComputePreparedKey(appName)
	if err != nil {
		return false, err
	}
	return prepare.Ready(prepare.StateDir(PreparePath(), appName, key)), nil
}

// ResolveBindingPath resolves a {bin:<alias>} placeholder of the operation's
// app: the alias must name a direct dependsOn edge, and its target must be a
// binary, uv or go app (the kinds with a single executable entry point).
// Read-only: no install, no prepare step.
func (bm *BinManager) ResolveBindingPath(ctx context.Context, appName, alias string) (string, error) {
	app, ok := bm.mapOfApps[appName]
	if !ok {
		return "", &AppNotFoundError{AppName: appName}
	}
	target, ok := aliasToApp(app.DependsOn, alias)
	if !ok {
		return "", fmt.Errorf("app %q has no dependency with alias %q", appName, alias)
	}
	return bm.resolveBindingPath(target)
}

// runPrepareStep executes one app's declared step. The command is the app's
// own executable (or binPath inside its install directory), the environment
// is inherited + app.env + prepare.env + runtime-owned keys + isolation, and
// PATH follows the declared mode.
func (bm *BinManager) runPrepareStep(ctx context.Context, appName string) error {
	app, ok := bm.mapOfApps[appName]
	if !ok {
		return fmt.Errorf("app %q not found in registry", appName)
	}
	if app.Prepare == nil {
		return fmt.Errorf("app %q declares no prepare step", appName)
	}
	key, err := bm.ComputePreparedKey(appName)
	if err != nil {
		return fmt.Errorf("compute prepared key: %w", err)
	}

	// The step's own environment composition (stepPATH) resolves dependency
	// bindings read-only; materialize the app's dependency directory here so
	// a step that shells out to a dependency (PATH lookup, ${APP_BIN:<alias>})
	// finds it whatever install path led here (init, exec, the runner).
	if closure, cerr := AppDependencyClosure(bm.mapOfApps, []string{appName}); cerr == nil {
		if merr := bm.materializeDependencyPaths(closure, nil); merr != nil {
			return merr
		}
	} else {
		return fmt.Errorf("resolve dependency closure: %w", cerr)
	}

	argv, err := bm.stepCommand(appName, app)
	if err != nil {
		return err
	}

	envList, err := bm.stepEnvironment(appName, app)
	if err != nil {
		return err
	}

	network := app.Prepare.Network != nil && *app.Prepare.Network
	// A relative DATAMITSU_CACHE_DIR keeps store paths relative, but the
	// engine runs the step with the staging workdir as cwd: every path the
	// engine touches must be absolute or it resolves against the wrong
	// directory.
	store, err := filepath.Abs(env.GetStorePath())
	if err != nil {
		return fmt.Errorf("absolutize store path: %w", err)
	}
	preparedRoot, err := filepath.Abs(PreparePath())
	if err != nil {
		return fmt.Errorf("absolutize prepared root: %w", err)
	}
	return prepare.Run(ctx, prepare.Request{
		App:          appName,
		Key:          key,
		Network:      network,
		PreparedRoot: preparedRoot,
		Protocol:     PrepareProtocolVersion,
		Store:        store,
		Command:      argv,
		Env:          envList,
		Stderr:       os.Stderr,
		Timeout:      prepareTimeout(),
	})
}

// stepCommand resolves the step's argv: the executable and any argument
// prefix the app's own command carries (bun's launcher and script, the JVM's
// -jar and options) — or prepare.binPath inside the install directory —
// followed by the declared args with every placeholder expanded. The layout
// tokens stay symbolic: the engine binds them to the staging paths.
func (bm *BinManager) stepCommand(appName string, app App) ([]string, error) {
	installDir, err := bm.ComputeInstallPath(appName)
	if err != nil {
		return nil, fmt.Errorf("resolve install directory: %w", err)
	}
	// A relative DATAMITSU_CACHE_DIR keeps the install dir relative, but the
	// step's cwd is the staging workdir: every path handed to the step must
	// be absolute or it resolves against the wrong directory. This is the
	// ${APP_DIR} value as well, so absolutize it once, up front.
	installDir, err = filepath.Abs(installDir)
	if err != nil {
		return nil, fmt.Errorf("absolutize install directory: %w", err)
	}
	// The install path of a single-file binary is the file itself; the
	// directory is its parent (binPath is only valid on extractDir apps, so
	// this branch never sees a single file).
	dir := installDir
	if app.Binary != nil {
		if filepath.Ext(installDir) != "" || strings.HasSuffix(installDir, ".exe") {
			dir = filepath.Dir(installDir)
		}
	}

	var argv []string
	if app.Prepare.BinPath != "" {
		exe := filepath.Join(dir, filepath.FromSlash(app.Prepare.BinPath))
		argv = []string{exe}
	} else {
		info, _, err := bm.resolveCommandInfo(appName)
		if err != nil {
			return nil, err
		}
		command, err := absoluteCommand(info.Command)
		if err != nil {
			return nil, err
		}
		argv = []string{command}
		// A launcher's operands can be paths too (bun's script, the JVM's
		// -jar): absolutize operands that look like relative paths against
		// the caller's cwd; flags and bare words stay untouched.
		for _, arg := range info.Args {
			if looksLikeRelativePath(arg) {
				abs, err := filepath.Abs(arg)
				if err != nil {
					return nil, fmt.Errorf("absolutize launcher argument %q: %w", arg, err)
				}
				arg = abs
			}
			argv = append(argv, arg)
		}
	}

	// Expand the declared args: ${APP_DIR} and ${APP_BIN:<alias>} here,
	// ${APP_PREPARED} symbolically (the engine binds it to the staging out).
	for _, arg := range app.Prepare.Args {
		expanded, err := bm.expandStepBindings(app, arg)
		if err != nil {
			return nil, fmt.Errorf("apps.%s.prepare.args: %w", appName, err)
		}
		expanded = strings.ReplaceAll(expanded, "${APP_DIR}", installDir)
		argv = append(argv, expanded)
	}
	return argv, nil
}

// absoluteCommand turns a resolved command into the absolute path the step
// needs. A bare name (a system runtime like java or bun looked up through
// PATH) must go through exec.LookPath — filepath.Abs would merely pin it
// under the current directory, which does not exist for the step's cwd.
func absoluteCommand(command string) (string, error) {
	if filepath.IsAbs(command) {
		return command, nil
	}
	if base := filepath.Base(command); base == command {
		found, err := exec.LookPath(command)
		if err != nil {
			return "", fmt.Errorf("resolve step command %q through PATH: %w", command, err)
		}
		return found, nil
	}
	abs, err := filepath.Abs(command)
	if err != nil {
		return "", fmt.Errorf("absolutize step command: %w", err)
	}
	return abs, nil
}

// looksLikeRelativePath reports whether a launcher operand is a relative
// path worth absolutizing: it names an existing file and carries a path
// separator or extension. Flags (-jar, --flag) and plain words never match.
func looksLikeRelativePath(arg string) bool {
	if arg == "" || strings.HasPrefix(arg, "-") || filepath.IsAbs(arg) {
		return false
	}
	if filepath.Base(arg) == arg && filepath.Ext(arg) == "" {
		return false
	}
	_, err := os.Stat(arg)
	return err == nil
}

// stepEnvironment assembles the step's environment:
//
//	inherited process environment (proxies, credentials)
//	  < app.env (${STORE}, ${APP_DIR} expanded)
//	  < prepare.env (bindings expanded, ${APP_PREPARED} left symbolic)
//	  < runtime-owned keys from the app's resolved CommandInfo
//	  < PATH by prepare.path mode
//	  < isolation keys and DATAMITSU_CACHE_DIR (datamitsu-owned, always win)
//
// Runtime-owned keys win over app.env and prepare.env, matching the run-time
// precedence (mergeAppEnv): a user config can never relocate the pnpm store
// or the uv cache. runtimeEnv is deliberately absent: it is run-time policy,
// never step identity, and a step must not see a flag like an offline switch
// meant for the tool's checks.
func (bm *BinManager) stepEnvironment(appName string, app App) ([]string, error) {
	info, err := bm.resolveRawCommandInfo(appName)
	if err != nil {
		return nil, err
	}
	// Base: the inherited environment, so proxies, custom CAs and registry
	// credentials reach the step exactly as they reach installers. On
	// Windows environment keys are case-insensitive: an inherited "Path"
	// must not survive beside the "PATH" this function sets (the process
	// environment would then hold both, and the case-insensitive lookup
	// takes the last one — the host's). Fold the base keys so a datamitsu
	// value always wins.
	combined := make(map[string]string, len(app.Env)+len(app.Prepare.Env)+len(info.Env)+8)
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			combined[foldEnvKey(k)] = v
		}
	}

	// The ${APP_DIR} and ${STORE} the config expands against must be
	// absolute: the step's cwd is the staging workdir, not the project.
	appDir, err := bm.ComputeInstallPath(appName)
	if err != nil {
		return nil, err
	}
	appDir, err = filepath.Abs(appDir)
	if err != nil {
		return nil, fmt.Errorf("absolutize install directory: %w", err)
	}
	storeDir, err := filepath.Abs(env.GetStorePath())
	if err != nil {
		return nil, fmt.Errorf("absolutize store: %w", err)
	}
	replacer := strings.NewReplacer("${STORE}", storeDir, "${APP_DIR}", appDir)

	// app.env, then prepare.env (stronger than app.env, weaker than the
	// runtime-owned keys applied afterwards).
	for k, v := range app.Env {
		combined[foldEnvKey(k)] = replacer.Replace(v)
	}
	for k, v := range app.Prepare.Env {
		v = replacer.Replace(v)
		v, err := bm.expandStepBindings(app, v)
		if err != nil {
			return nil, fmt.Errorf("apps.%s.prepare.env.%s: %w", appName, k, err)
		}
		combined[foldEnvKey(k)] = v
	}

	// Runtime-owned keys win over everything the config declared. Their
	// path-like values are made absolute first: with a relative cache base
	// the runtime's directories (UV_CACHE_DIR, npm_config_store_dir) would
	// otherwise resolve against the staging workdir.
	runtimeEnv := maps.Clone(info.Env)
	absolutizeRuntimeEnvValues(runtimeEnv)
	for k, v := range runtimeEnv {
		combined[foldEnvKey(k)] = v
	}

	// PATH by mode. "declared": the runtime prefix plus the dependency
	// directory, no inherited tail. "inherit": like a run, inherited tail
	// included.
	pathValue, err := bm.stepPATH(appName, app, info)
	if err != nil {
		return nil, err
	}
	combined[foldEnvKey("PATH")] = pathValue

	// Isolation keys and the cache base are datamitsu-owned: they win over
	// everything, including runtime-owned keys. The cache base is the real
	// {base} (the parent of cache/ and store/): a nested datamitsu launched
	// by the step would otherwise compute {base}/cache/store as its store —
	// absolute, because the step's cwd is not the caller's.
	cacheBase, err := filepath.Abs(env.GetCacheBaseDir())
	if err != nil {
		return nil, fmt.Errorf("absolutize cache base: %w", err)
	}
	combined[foldEnvKey("DATAMITSU_CACHE_DIR")] = cacheBase
	keys := make([]string, 0, len(combined))
	for k := range combined {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys)+6)
	for _, k := range keys {
		out = append(out, unfoldEnvKey(k)+"="+combined[k])
	}
	// TMPDIR/TEMP/TMP and XDG roots are symbolic: the engine binds them to
	// the throwaway layout.
	out = append(out, "TMPDIR=${STEP_TMPDIR}", "TEMP=${STEP_TMPDIR}", "TMP=${STEP_TMPDIR}")
	out = append(out, "XDG_CACHE_HOME=${STEP_TMPDIR}/cache", "XDG_DATA_HOME=${STEP_TMPDIR}/data", "XDG_STATE_HOME=${STEP_TMPDIR}/state")
	return out, nil
}

// foldEnvKey folds an environment key for map storage: unchanged on Unix,
// lowercased on Windows where environment keys are case-insensitive (an
// inherited "Path" and datamitsu's "PATH" must collide in the map, or the
// process environment would carry both and the case-insensitive lookup
// would take the host's).
func foldEnvKey(k string) string {
	if runtime.GOOS != "windows" {
		return k
	}
	return strings.ToLower(k)
}

// unfoldEnvKey restores the conventional spelling of a folded key for
// output. Windows accepts any case; the canonical upper-case forms keep
// the environment readable and match what the process environment would
// show. On Unix keys were never folded and pass through unchanged.
func unfoldEnvKey(k string) string {
	if runtime.GOOS != "windows" || k != "path" {
		return k
	}
	return "PATH"
}

// expandStepBindings resolves ${APP_BIN:<alias>} in one prepare value against
// the app's direct edges.
func (bm *BinManager) expandStepBindings(app App, value string) (string, error) {
	return expandAppBindings(value, func(alias string) (string, error) {
		target, ok := aliasToApp(app.DependsOn, alias)
		if !ok {
			return "", fmt.Errorf("APP_BIN target %q must be listed in direct dependsOn", alias)
		}
		path, err := bm.resolveBindingPath(target)
		if err != nil {
			return "", err
		}
		return filepath.Abs(path)
	}, nil)
}

// inheritedPath is the process PATH. A standard variable, read through the
// env package so the policy's one access point holds.
func inheritedPath() string {
	return os.Getenv("PATH") //nolint:forbidigo // standard PATH, not a datamitsu env
}

// stepPATH builds the step's PATH on the app's own runtime prefix.
// "declared" (the default) keeps the runtime-owned prefix and the dependency
// directory and drops the inherited host tail; "inherit" appends the host
// tail, exactly like a run.
//
// Under a relative DATAMITSU_CACHE_DIR the resolve path DROPS the runtime's
// relative PATH components (its answer is persisted into the farm manifest,
// where a relative entry would be worse than none). The resolved info's PATH
// may therefore hold only the dependency directory — nonempty, so an
// emptiness check would never notice the loss — or a partial prefix. For a
// Node or Bun app whose runtime manager implements the seam, the complete
// absolutized prefix is taken from the seam unconditionally; other kinds
// keep the resolved (absolutized) prefix.
func (bm *BinManager) stepPATH(appName string, app App, info *CommandInfo) (string, error) {
	runtimePrefix := absolutizePathList(info.Env["PATH"])
	if pp, ok := bm.runtimeManager.(PreparePathPrefixer); ok && (app.Node != nil || app.Bun != nil) {
		runtimePrefix = pp.ResolvePreparePathPrefix(appName, app)
	}
	strict := &CommandInfo{Env: map[string]string{}}
	if runtimePrefix != "" {
		strict.Env["PATH"] = runtimePrefix
	}
	installDep := func(dep string) (string, error) { return bm.resolveBindingPath(dep) }
	if _, err := bm.addDependencyPath(appName, strict, installDep, false); err != nil {
		return "", err
	}
	strictPath := strict.Env["PATH"]
	if app.Prepare.Path == PreparePathInherit {
		if strictPath == "" {
			return inheritedPath(), nil
		}
		// A run's PATH composition: the strict prefix, then the inherited
		// tail (composeDependencyPath already ordered runtime before
		// dependencies; the host tail comes last).
		if strings.HasSuffix(strictPath, string(os.PathListSeparator)) {
			return strictPath + inheritedPath(), nil
		}
		return strictPath + string(os.PathListSeparator) + inheritedPath(), nil
	}
	// Declared mode: the strict prefix is the whole PATH. A binary app with
	// no runtime and no dependencies gets an empty PATH — the declared-only
	// contract means the host tail never leaks in.
	return strictPath, nil
}

// absolutizePathList makes every component of a PATH-style value absolute
// against the caller's cwd. Empty components stay empty (they mean "cwd"
// and the step's cwd differs, but dropping them changes semantics the least).
func absolutizePathList(list string) string {
	if list == "" {
		return ""
	}
	parts := strings.Split(list, string(os.PathListSeparator))
	for i, part := range parts {
		if part == "" || filepath.IsAbs(part) {
			continue
		}
		if abs, err := filepath.Abs(part); err == nil {
			parts[i] = abs
		}
	}
	return strings.Join(parts, string(os.PathListSeparator))
}

// absolutizeRuntimeEnvValues makes path-like runtime-owned env values
// absolute: with a relative DATAMITSU_CACHE_DIR the runtime's cache and
// store directories (UV_CACHE_DIR, npm_config_store_dir, GOPATH-style keys)
// are relative, and the step runs with the staging workdir as cwd. Only
// values that name an existing path are rewritten — flags and plain words
// stay untouched.
func absolutizeRuntimeEnvValues(m map[string]string) {
	for k, v := range m {
		if v == "" || filepath.IsAbs(v) || strings.Contains(v, string(os.PathListSeparator)) {
			continue
		}
		if !pathLikeEnvKey(k) {
			continue
		}
		if _, err := os.Stat(v); err != nil {
			continue
		}
		if abs, err := filepath.Abs(v); err == nil {
			m[k] = abs
		}
	}
}

// pathLikeEnvKey reports whether an environment key plausibly holds a
// directory path (…DIR, …_DIR, …PATH names except PATH itself, which the
// caller handles as a list).
func pathLikeEnvKey(key string) bool {
	up := strings.ToUpper(key)
	return key != "PATH" && (strings.HasSuffix(up, "DIR") || strings.HasSuffix(up, "_DIR") || strings.HasSuffix(up, "PATH") || strings.HasSuffix(up, "HOME"))
}
