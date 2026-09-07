package runtimemanager

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/httpx"
	"github.com/datamitsu/datamitsu/internal/trace"
	"github.com/datamitsu/datamitsu/internal/ui"

	"go.uber.org/zap"
)

// node.go implements the archive-based Node.js runtime (kind "node"). Node is
// acquired as a direct, SHA-256-pinned archive, exactly like the jvm/go runtimes:
// the registry holds a per os/arch/libc {url, hash} entry that the generic
// managed-runtime path (rm.GetRuntimePath -> binmanager) downloads, verifies, and
// extracts (extractDir) using the shared 5-minute download client. The git-pinned
// hash is the single source of trust.
//
// Everything after node is on PATH reuses the pnpm flow (pnpm is downloaded
// directly from the npm registry with a pinned SHA-256 + the registry's SHA-512
// integrity, and npm tools are installed with `node <pnpm.cjs> install`). The
// shared pnpm/npm helpers live in pnpm.go.

// getNodeEnvVars returns the per-app npm/pnpm environment for a node app.
//
// NOTE: pnpm 11 does NOT read store-dir / virtual-store-dir from these
// npm_config_* env vars (nor from .npmrc) — it only honors the workspace
// storeDir key, which buildPNPMWorkspaceForApp pins to GetPNPMStorePath(). The
// store_dir entry here is retained only so the installer can pre-create that
// directory; virtual_store_dir already matches pnpm's default (node_modules/
// .pnpm under the cwd, which is appEnvPath).
func getNodeEnvVars(appEnvPath string) map[string]string {
	storePath := env.GetPNPMStorePath()
	return map[string]string{
		"npm_config_store_dir":         storePath,
		"npm_config_virtual_store_dir": filepath.Join(appEnvPath, "node_modules", ".pnpm"),
		"npm_config_global_dir":        filepath.Join(appEnvPath, "global"),
	}
}

// installNode ensures the node runtime archive for runtimeName is downloaded,
// SHA-256-verified, and extracted, returning the absolute path to the extracted
// `node` binary (`node.exe` on Windows). It delegates to the generic
// managed-runtime path (GetRuntimePath), which selects binaries[os][arch][libc]
// with glibc fallback via resolveLibcKey, streams + verifies the pinned hash,
// extracts the full directory tree (extractDir), and resolves the binary via the
// entry's binaryPath. Concurrent callers are deduplicated by GetRuntimePath's
// singleflight, and an already-installed runtime is a cache hit with no refetch.
//
// Unlike jvm/uv/go — which wrap GetRuntimePath inline at their single call site —
// node acquires its binary from two paths (installNodeAppOnce and
// GetNodeCommandInfo), so the error wrapping is centralized in this named helper
// rather than duplicated. It is intentionally retained, not inlined.
func (rm *RuntimeManager) installNode(ctx context.Context, runtimeName string) (string, error) {
	nodeBinPath, err := rm.getRuntimePath(ctx, runtimeName)
	if err != nil {
		return "", fmt.Errorf("failed to acquire node runtime %q: %w", runtimeName, err)
	}
	return nodeBinPath, nil
}

// resolveNodeBinPath is the install-free half of installNode: it returns where
// the node binary lives (or would live) without downloading anything. The path
// is returned whether or not the file exists — the side-effect-free resolution
// path (ResolveCommandInfo) needs the location to report, and decides
// installed-ness with its own stat.
func (rm *RuntimeManager) resolveNodeBinPath(runtimeName string) (string, error) {
	nodeBinPath, err := rm.ResolveRuntimePath(runtimeName)
	if err != nil {
		return "", fmt.Errorf("failed to resolve node runtime %q: %w", runtimeName, err)
	}
	return nodeBinPath, nil
}

// resolveNodeAppEnvPath resolves the node runtime and the app's cache path.
func (rm *RuntimeManager) resolveNodeAppEnvPath(appName string, appConfig *binmanager.AppConfigNode, files map[string]string, archives map[string]*binmanager.ArchiveSpec) (string, error) {
	appEnvPath, _, _, err := rm.resolveNodeAppEnvPathWith(appName, appConfig, files, archives)
	return appEnvPath, err
}

// resolveNodeAppEnvPathWith resolves the app env path plus the runtime it
// runs under. The cache key folds the storeDir-FREE hash form of the merged
// workspace (buildPNPMWorkspaceHashForm): the merged file installs write out
// pins an absolute storeDir, and hashing that would make node app store paths
// root-dependent — breaking the relocatability the OCI bundle demand matching
// relies on. The hash form still invalidates the path whenever the secure
// defaults or the user workspace override change.
func (rm *RuntimeManager) resolveNodeAppEnvPathWith(appName string, appConfig *binmanager.AppConfigNode, files map[string]string, archives map[string]*binmanager.ArchiveSpec) (appEnvPath string, runtimeName string, rc config.RuntimeConfig, err error) {
	runtimeName, rc, err = rm.ResolveRuntime(appConfig.Runtime, config.RuntimeKindNode)
	if err != nil {
		return "", "", config.RuntimeConfig{}, fmt.Errorf("failed to resolve node runtime for %q: %w", appName, err)
	}

	hashFormYAML, err := buildPNPMWorkspaceHashForm(files)
	if err != nil {
		return "", "", config.RuntimeConfig{}, fmt.Errorf("failed to compute pnpm-workspace.yaml hash form for %q: %w", appName, err)
	}
	filesForHash := filesWithWorkspaceYAML(files, hashFormYAML)

	appEnvPath, err = rm.GetAppPath(appName, config.RuntimeKindNode, appConfig.Version, appConfig.Dependencies, lockFileHash(appConfig.LockFile), filesForHash, archives, runtimeName, NodeAppPathExtra{
		PackageName: appConfig.PackageName,
		BinPath:     appConfig.BinPath,
	})
	if err != nil {
		return "", "", config.RuntimeConfig{}, err
	}

	return appEnvPath, runtimeName, rc, nil
}

// InstallNodeApp installs a node-managed npm app if not already cached.
// If files is non-empty, writes them to the app directory before running pnpm.
// Safe for concurrent use from multiple goroutines.
func (rm *RuntimeManager) InstallNodeApp(ctx context.Context, appName string, appConfig *binmanager.AppConfigNode, customEnv map[string]string, files map[string]string, archives map[string]*binmanager.ArchiveSpec) error {
	mergedWorkspaceYAML, err := buildPNPMWorkspace(files)
	if err != nil {
		return fmt.Errorf("failed to compute pnpm-workspace.yaml for %q: %w", appName, err)
	}
	return rm.installNodeApp(ctx, appName, appConfig, customEnv, files, archives, mergedWorkspaceYAML)
}

// installNodeApp installs a node app using a pre-merged pnpm-workspace.yaml so the
// merge is computed once per exec and shared with the command-info pass.
func (rm *RuntimeManager) installNodeApp(ctx context.Context, appName string, appConfig *binmanager.AppConfigNode, customEnv map[string]string, files map[string]string, archives map[string]*binmanager.ArchiveSpec, mergedWorkspaceYAML string) error {
	ctx, cancel, timeoutSec := newInstallContext(ctx)
	defer cancel()
	key := "node/" + appName
	_, err, _ := rm.appInstall.Do(key, func() (any, error) {
		return nil, rm.installNodeAppOnce(ctx, appName, appConfig, customEnv, files, archives, mergedWorkspaceYAML)
	})
	return wrapInstallTimeout(err, timeoutSec)
}

func (rm *RuntimeManager) installNodeAppOnce(ctx context.Context, appName string, appConfig *binmanager.AppConfigNode, customEnv map[string]string, files map[string]string, archives map[string]*binmanager.ArchiveSpec, mergedWorkspaceYAML string) error {
	defer trace.Start(trace.CatInstall, "node.installApp").EndWith(trace.A("app", appName))

	appEnvPath, runtimeName, rc, err := rm.resolveNodeAppEnvPathWith(appName, appConfig, files, archives)
	if err != nil {
		return err
	}

	if rc.Node == nil {
		return fmt.Errorf("runtime for %q has no node config (nodeVersion/pnpmVersion)", appName)
	}
	pnpmVersion := rc.Node.PNPMVersion
	pnpmHash := rc.Node.PNPMHash

	if err := validateRelativePath(appConfig.BinPath); err != nil {
		return fmt.Errorf("app %q: unsafe binPath: %w", appName, err)
	}
	appBinPath := filepath.Join(appEnvPath, appConfig.BinPath)
	appModulePkg := filepath.Join(appEnvPath, "node_modules", appConfig.PackageName, "package.json")
	if _, err := os.Stat(appBinPath); err == nil {
		if _, err := os.Stat(appModulePkg); err == nil {
			log.Debug("node app already installed",
				zap.String("app", appName),
				zap.String("path", appBinPath),
			)
			return nil
		}
		log.Warn("node app bin shim exists but module is missing, reinstalling",
			zap.String("app", appName),
			zap.String("module", appModulePkg),
		)
		if err := rm.removeAll(appEnvPath); err != nil {
			return fmt.Errorf("app %q: failed to remove stale install at %q before reinstall: %w", appName, appEnvPath, err)
		}
	}

	// pnpm install spawns a child process whose network cannot be cut;
	// refuse before starting the install.
	if err := httpx.GuardOffline("node app install of " + appName); err != nil {
		return err
	}

	// Acquire the node binary directly from the pinned archive (jvm/go-style).
	nodeBinPath, err := rm.installNode(ctx, runtimeName)
	if err != nil {
		return err
	}

	storeRoot := env.GetStorePath()

	pnpmDir := filepath.Join(storeRoot, ".runtimes", "pnpm", pnpmVersion, pnpmHash)
	if err := rm.installPNPM(ctx, pnpmVersion, pnpmDir, pnpmHash); err != nil {
		return fmt.Errorf("failed to download PNPM: %w", err)
	}

	// appEnvPath is created by WriteAppFiles (when there are files/archives) and
	// always by writeAppWorkspaceFile below, both before package.json is written,
	// so no explicit MkdirAll is needed here.
	cleanupOnError := true
	defer func() {
		if cleanupOnError {
			_ = os.RemoveAll(appEnvPath)
		}
	}()

	filesToWrite := filesWithoutWorkspaceYAML(files)

	if len(filesToWrite) > 0 || len(archives) > 0 {
		if err := binmanager.WriteAppFiles(ctx, appEnvPath, filesToWrite, archives); err != nil {
			return fmt.Errorf("failed to write app files/archives for %q: %w", appName, err)
		}
	}

	// Write pnpm-workspace.yaml AFTER WriteAppFiles so the secure defaults always
	// win over anything an archive might place at this path.
	if err := writeAppWorkspaceFile(appEnvPath, mergedWorkspaceYAML); err != nil {
		return fmt.Errorf("failed to write pnpm-workspace.yaml for %q: %w", appName, err)
	}

	packageJSON, err := buildPackageJSON(appConfig.PackageName, appConfig.Version, appConfig.Dependencies)
	if err != nil {
		return fmt.Errorf("failed to build package.json: %w", err)
	}

	packageJSONPath := filepath.Join(appEnvPath, "package.json")
	if err := os.WriteFile(packageJSONPath, packageJSON, 0o644); err != nil {
		return fmt.Errorf("failed to write package.json: %w", err)
	}

	if appConfig.LockFile != "" {
		lockContent, decErr := DecompressLockFile(appConfig.LockFile)
		if decErr != nil {
			return fmt.Errorf("failed to decompress lock file for %q: %w", appName, decErr)
		}
		lockFilePath := filepath.Join(appEnvPath, "pnpm-lock.yaml")
		if err := os.WriteFile(lockFilePath, []byte(lockContent), 0o644); err != nil {
			return fmt.Errorf("failed to write pnpm-lock.yaml for %q: %w", appName, err)
		}
	}

	envVars := getNodeEnvVars(appEnvPath)

	for _, dir := range []string{envVars["npm_config_store_dir"], envVars["npm_config_global_dir"]} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create directory %q: %w", dir, err)
		}
	}

	pnpmCjsPath := env.GetPNPMPath(storeRoot, pnpmVersion, pnpmHash)

	args := buildPNPMInstallArgs(pnpmCjsPath, appConfig.LockFile != "")

	nodeBinDir := filepath.Dir(nodeBinPath)
	envVars["PATH"] = nodeBinDir + string(os.PathListSeparator) + os.Getenv("PATH") //nolint:forbidigo // standard PATH for child process env, not a datamitsu env var
	envVars = mergeInstallEnv(envVars, customEnv, appEnvPath)
	cmdEnv := buildEnvWithOverrides(os.Environ(), envVars)

	cmd := exec.CommandContext(ctx, nodeBinPath, args...) //nolint:gosec // G204: nodeBinPath comes from the trusted managed runtime store and args are built from validated config
	cmd.Dir = appEnvPath
	cmd.Env = cmdEnv

	log.Debug("installing node app",
		zap.String("app", appName),
		zap.String("package", appConfig.PackageName),
		zap.String("node", rc.Node.NodeVersion),
		zap.String("pnpm", pnpmVersion),
	)

	// Stream pnpm's ndjson stdout into a live spinner (resolved/downloaded/added
	// counters); pnpm reports errors as ndjson too, so they are extracted by the
	// reporter and surfaced only on failure.
	sp := ui.Current().Spinner("Installing " + appName)
	rep := newPNPMReporter(sp)
	stderr, err := runInstallCmdStreaming(ctx, cmd, rep.line)
	if err != nil {
		sp.Fail()
		ui.Current().Errorln(rep.errorOutput(stderr))
		return fmt.Errorf("failed to install node app %q: %w", appName, err)
	}

	sp.Done("Installed " + appName)

	cleanupOnError = false
	return nil
}

// GetNodeCommandInfo returns the command info for running a node app. The bin
// shim is executed directly with the node binary's directory prepended to PATH
// so the shim's `#!/usr/bin/env node` (or node's own resolution) finds the
// managed node.
func (rm *RuntimeManager) GetNodeCommandInfo(ctx context.Context, appName string, appConfig *binmanager.AppConfigNode, files map[string]string, archives map[string]*binmanager.ArchiveSpec) (*binmanager.CommandInfo, error) {
	return rm.getNodeCommandInfo(ctx, appName, appConfig, files, archives)
}

// getNodeCommandInfo is GetNodeCommandInfo; path resolution hashes the
// storeDir-free workspace form internally, so no merged YAML is threaded in.
func (rm *RuntimeManager) getNodeCommandInfo(ctx context.Context, appName string, appConfig *binmanager.AppConfigNode, files map[string]string, archives map[string]*binmanager.ArchiveSpec) (*binmanager.CommandInfo, error) {
	// Command-info resolution only ever touches an already-installed runtime
	// (the install pass ran first), so there is no fresh download to bound; the
	// caller's context is still propagated for cancellation.
	info, _, err := rm.nodeCommandInfo(appName, appConfig, files, archives, func(runtimeName string) (string, error) {
		return rm.installNode(ctx, runtimeName)
	})
	return info, err
}

// resolveNodeCommandInfo is getNodeCommandInfo without the install side effect:
// the same Command/Args/Env, resolved through resolveNodeBinPath instead of
// installNode. Both go through nodeCommandInfo so the two can never drift.
//
// The one deliberate difference is PATH. The exec path composes it and uses it
// immediately, so folding in the current process's PATH is right there. This
// path's answer is persisted in the source-mode farm manifest and replayed by
// every later shell, where a captured PATH is not merely stale but actively
// wrong: it pins the baking shell's environment, which for a per-shell version
// manager names a directory that is gone once that shell exits, and it writes
// the user's whole PATH into an on-disk artifact that `source status --json`
// prints. Only the runtime-owned prefix is recorded; the shim prepends it to
// whatever PATH the caller actually has (see shim.mergeEnv). The prefix is
// returned by nodeCommandInfo rather than parsed back out of the composed value,
// so a runtime that contributes no directory at all records no PATH entry.
func (rm *RuntimeManager) resolveNodeCommandInfo(appName string, appConfig *binmanager.AppConfigNode, files map[string]string, archives map[string]*binmanager.ArchiveSpec) (*binmanager.CommandInfo, error) {
	info, pathPrefix, err := rm.nodeCommandInfo(appName, appConfig, files, archives, rm.resolveNodeBinPath)
	if err != nil {
		return nil, err
	}
	if info.Env != nil {
		if pathPrefix == "" {
			delete(info.Env, "PATH")
		} else {
			info.Env["PATH"] = pathPrefix
		}
	}
	return info, nil
}

// nodeCommandInfo builds a node app's CommandInfo, obtaining the node binary
// through the injected nodeBin func — installNode on the exec path,
// resolveNodeBinPath on the side-effect-free path. The second return value is
// the runtime-owned PATH prefix, empty when the runtime contributes none.
func (rm *RuntimeManager) nodeCommandInfo(appName string, appConfig *binmanager.AppConfigNode, files map[string]string, archives map[string]*binmanager.ArchiveSpec, nodeBin func(string) (string, error)) (*binmanager.CommandInfo, string, error) {
	appEnvPath, runtimeName, rc, err := rm.resolveNodeAppEnvPathWith(appName, appConfig, files, archives)
	if err != nil {
		return nil, "", err
	}

	if rc.Node == nil {
		return nil, "", fmt.Errorf("runtime for %q has no node config (nodeVersion/pnpmVersion)", appName)
	}

	if err := validateRelativePath(appConfig.BinPath); err != nil {
		return nil, "", fmt.Errorf("app %q: unsafe binPath: %w", appName, err)
	}

	nodeBinPath, err := nodeBin(runtimeName)
	if err != nil {
		return nil, "", err
	}
	appBinPath := filepath.Join(appEnvPath, appConfig.BinPath)

	// A system-mode runtime may name its interpreter by bare word ("node"), and
	// filepath.Dir of that is ".". Prepending "." to PATH would put the current
	// working directory in front of every lookup the app makes — and on the
	// resolve path it is persisted into the farm manifest, so every later shell
	// replays it. A bare word is found through PATH by the exec itself, so there
	// is nothing for the runtime to contribute.
	pathPrefix := filepath.Dir(nodeBinPath)
	if !filepath.IsAbs(pathPrefix) {
		pathPrefix = ""
	}

	// The app's dependency bin directory goes in front of that, the way `npm run`
	// and `pnpm exec` put `node_modules/.bin` on PATH: an app's dependencies can
	// ship executables the app itself expects to find there, and nothing else puts
	// them within reach. oxlint locates its type-aware engine (`tsgolint`, declared
	// as an optional peer and installed right beside it) exactly this way —
	// installing the package next to oxlint is not enough, because the lookup goes
	// through PATH.
	//
	// Spelled out rather than derived from BinPath: BinPath is free-form config and
	// only conventionally lives in node_modules/.bin, so filepath.Dir of it would
	// name the dependency bin dir by coincidence and something else the moment an
	// app points BinPath elsewhere.
	//
	// The directory is immutable for a given app configuration — the hash covers
	// version, dependencies and lockfile — so it is as safe to persist into the
	// source-mode farm manifest as the runtime's own prefix. It is prepended rather
	// than appended so an app resolves its own pinned dependency ahead of whatever
	// the ambient PATH happens to carry, matching npm/pnpm.
	appDepBinDir := filepath.Join(appEnvPath, "node_modules", ".bin")
	if filepath.IsAbs(appDepBinDir) {
		if pathPrefix == "" {
			pathPrefix = appDepBinDir
		} else {
			pathPrefix = appDepBinDir + string(os.PathListSeparator) + pathPrefix
		}
	} else {
		// Only reachable with a relative DATAMITSU_CACHE_DIR, which makes the whole
		// app path relative. Recording a relative directory would be worse than
		// recording none — the manifest is replayed from other working directories —
		// but dropping it silently turns "type-aware linting does nothing" into an
		// unexplainable bug, so say so once.
		log.Warn("app dependency bin directory is not absolute; executables shipped by the app's own dependencies will not be found",
			zap.String("app", appName),
			zap.String("dir", appDepBinDir))
	}

	envVars := getNodeEnvVars(appEnvPath)
	//nolint:forbidigo // standard PATH for child process env, not a datamitsu env var
	inheritedPath := os.Getenv("PATH")
	if pathPrefix == "" {
		envVars["PATH"] = inheritedPath
	} else {
		envVars["PATH"] = pathPrefix + string(os.PathListSeparator) + inheritedPath
	}

	return &binmanager.CommandInfo{
		Type:          "node",
		Command:       appBinPath,
		Args:          nil,
		Env:           envVars,
		RequiredPaths: nodeRequiredPaths(appEnvPath, appConfig.PackageName, nodeBinPath, rc),
	}, pathPrefix, nil
}

// nodeRequiredPaths lists what must exist besides the .bin shim for a node app
// to be healthy: the installed package itself, and — for a managed runtime — the
// node binary the shim's `#!/usr/bin/env node` line resolves through.
//
// Both mirror rules that already exist. installNodeAppOnce treats a shim without
// its module as stale and reinstalls; nothing was checking the same thing on the
// resolve path, so source mode reported such an app installed and ran a shim
// whose require() fails. The runtime binary is the sharper one: the shim finds
// node through PATH, and with the managed node gone the runtime-owned prefix
// names a directory that is not there — the lookup falls through to whatever
// node the system has, silently running the app on an unpinned interpreter.
//
// A system-mode runtime contributes nothing here. Its interpreter is the user's
// to provide, and reinstalling the app would not conjure it.
func nodeRequiredPaths(appEnvPath, packageName, nodeBinPath string, rc config.RuntimeConfig) []string {
	paths := []string{filepath.Join(appEnvPath, "node_modules", packageName, "package.json")}
	if rc.Mode != config.RuntimeModeSystem && filepath.IsAbs(nodeBinPath) {
		paths = append(paths, nodeBinPath)
	}
	return paths
}
