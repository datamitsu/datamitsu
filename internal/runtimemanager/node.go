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

// resolveNodeAppEnvPath resolves the node runtime and the app's cache path,
// computing the merged pnpm-workspace.yaml once for the call. Hot paths that
// already merged once per exec should call resolveNodeAppEnvPathWith with the
// shared merged content instead.
func (rm *RuntimeManager) resolveNodeAppEnvPath(appName string, appConfig *binmanager.AppConfigNode, files map[string]string, archives map[string]*binmanager.ArchiveSpec) (string, error) {
	mergedWorkspaceYAML, err := buildPNPMWorkspace(files)
	if err != nil {
		return "", fmt.Errorf("failed to compute pnpm-workspace.yaml for %q: %w", appName, err)
	}
	appEnvPath, _, _, err := rm.resolveNodeAppEnvPathWith(appName, appConfig, files, archives, mergedWorkspaceYAML)
	return appEnvPath, err
}

// resolveNodeAppEnvPathWith is resolveNodeAppEnvPath with the merged
// pnpm-workspace.yaml supplied by the caller (computed once per exec). The merged
// content is folded into the cache key so the path invalidates when the secure
// defaults are tightened — exactly as recomputing it here would, but without the
// repeated merge/marshal across the install and command-info passes.
func (rm *RuntimeManager) resolveNodeAppEnvPathWith(appName string, appConfig *binmanager.AppConfigNode, files map[string]string, archives map[string]*binmanager.ArchiveSpec, mergedWorkspaceYAML string) (appEnvPath string, runtimeName string, rc config.RuntimeConfig, err error) {
	runtimeName, rc, err = rm.ResolveRuntime(appConfig.Runtime, config.RuntimeKindNode)
	if err != nil {
		return "", "", config.RuntimeConfig{}, fmt.Errorf("failed to resolve node runtime for %q: %w", appName, err)
	}

	filesForHash := filesWithWorkspaceYAML(files, mergedWorkspaceYAML)

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
	appEnvPath, runtimeName, rc, err := rm.resolveNodeAppEnvPathWith(appName, appConfig, files, archives, mergedWorkspaceYAML)
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
	mergedWorkspaceYAML, err := buildPNPMWorkspace(files)
	if err != nil {
		return nil, fmt.Errorf("failed to compute pnpm-workspace.yaml for %q: %w", appName, err)
	}
	return rm.getNodeCommandInfo(ctx, appName, appConfig, files, archives, mergedWorkspaceYAML)
}

// getNodeCommandInfo is GetNodeCommandInfo with the merged pnpm-workspace.yaml
// supplied by the caller (computed once per exec).
func (rm *RuntimeManager) getNodeCommandInfo(ctx context.Context, appName string, appConfig *binmanager.AppConfigNode, files map[string]string, archives map[string]*binmanager.ArchiveSpec, mergedWorkspaceYAML string) (*binmanager.CommandInfo, error) {
	appEnvPath, runtimeName, rc, err := rm.resolveNodeAppEnvPathWith(appName, appConfig, files, archives, mergedWorkspaceYAML)
	if err != nil {
		return nil, err
	}

	if rc.Node == nil {
		return nil, fmt.Errorf("runtime for %q has no node config (nodeVersion/pnpmVersion)", appName)
	}

	if err := validateRelativePath(appConfig.BinPath); err != nil {
		return nil, fmt.Errorf("app %q: unsafe binPath: %w", appName, err)
	}

	// Command-info resolution only ever touches an already-installed runtime
	// (the install pass ran first), so there is no fresh download to bound; the
	// caller's context is still propagated for cancellation.
	nodeBinPath, err := rm.installNode(ctx, runtimeName)
	if err != nil {
		return nil, err
	}
	nodeBinDir := filepath.Dir(nodeBinPath)
	appBinPath := filepath.Join(appEnvPath, appConfig.BinPath)

	envVars := getNodeEnvVars(appEnvPath)
	envVars["PATH"] = nodeBinDir + string(os.PathListSeparator) + os.Getenv("PATH") //nolint:forbidigo // standard PATH for child process env, not a datamitsu env var

	return &binmanager.CommandInfo{
		Type:    "node",
		Command: appBinPath,
		Args:    nil,
		Env:     envVars,
	}, nil
}
