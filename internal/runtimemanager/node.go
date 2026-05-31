package runtimemanager

import (
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

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

// getNodeEnvVars returns the per-app npm/pnpm environment for a node app: node
// apps are pnpm-installed npm packages, so this points pnpm at the shared store
// and the per-app virtual store / global dirs.
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
func (rm *RuntimeManager) installNode(runtimeName string) (string, error) {
	nodeBinPath, err := rm.GetRuntimePath(runtimeName)
	if err != nil {
		return "", fmt.Errorf("failed to acquire node runtime %q: %w", runtimeName, err)
	}
	return nodeBinPath, nil
}

func (rm *RuntimeManager) resolveNodeAppEnvPath(appName string, appConfig *binmanager.AppConfigNode, files map[string]string, archives map[string]*binmanager.ArchiveSpec) (appEnvPath string, runtimeName string, rc config.RuntimeConfig, err error) {
	runtimeName, rc, err = rm.ResolveRuntime(appConfig.Runtime, config.RuntimeKindNode)
	if err != nil {
		return "", "", config.RuntimeConfig{}, fmt.Errorf("failed to resolve node runtime for %q: %w", appName, err)
	}

	// Hash the merged pnpm-workspace.yaml content (defaults + user override) so
	// the cache key invalidates when the secure defaults are tightened.
	filesForHash, err := filesWithMergedWorkspaceYAML(files)
	if err != nil {
		return "", "", config.RuntimeConfig{}, fmt.Errorf("failed to compute pnpm-workspace.yaml for %q: %w", appName, err)
	}

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
func (rm *RuntimeManager) InstallNodeApp(appName string, appConfig *binmanager.AppConfigNode, files map[string]string, archives map[string]*binmanager.ArchiveSpec) error {
	key := "node/" + appName
	_, err, _ := rm.appInstall.Do(key, func() (any, error) {
		return nil, rm.installNodeAppOnce(appName, appConfig, files, archives)
	})
	return err
}

func (rm *RuntimeManager) installNodeAppOnce(appName string, appConfig *binmanager.AppConfigNode, files map[string]string, archives map[string]*binmanager.ArchiveSpec) error {
	appEnvPath, runtimeName, rc, err := rm.resolveNodeAppEnvPath(appName, appConfig, files, archives)
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

	// Acquire the node binary directly from the pinned archive (jvm/go-style).
	nodeBinPath, err := rm.installNode(runtimeName)
	if err != nil {
		return err
	}

	storeRoot := env.GetStorePath()

	pnpmDir := filepath.Join(storeRoot, ".runtimes", "pnpm", pnpmVersion, pnpmHash)
	if err := rm.installPNPM(pnpmVersion, pnpmDir, pnpmHash); err != nil {
		return fmt.Errorf("failed to download PNPM: %w", err)
	}

	if err := os.MkdirAll(appEnvPath, 0755); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}

	cleanupOnError := true
	defer func() {
		if cleanupOnError {
			_ = os.RemoveAll(appEnvPath)
		}
	}()

	mergedWorkspaceYAML, filesToWrite, err := preparePNPMWorkspaceForApp(files)
	if err != nil {
		return fmt.Errorf("failed to set up pnpm-workspace.yaml for %q: %w", appName, err)
	}

	if len(filesToWrite) > 0 || len(archives) > 0 {
		if err := binmanager.WriteAppFiles(appEnvPath, filesToWrite, archives); err != nil {
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
	if err := os.WriteFile(packageJSONPath, packageJSON, 0644); err != nil {
		return fmt.Errorf("failed to write package.json: %w", err)
	}

	if appConfig.LockFile != "" {
		lockContent, decErr := DecompressLockFile(appConfig.LockFile)
		if decErr != nil {
			return fmt.Errorf("failed to decompress lock file for %q: %w", appName, decErr)
		}
		lockFilePath := filepath.Join(appEnvPath, "pnpm-lock.yaml")
		if err := os.WriteFile(lockFilePath, []byte(lockContent), 0644); err != nil {
			return fmt.Errorf("failed to write pnpm-lock.yaml for %q: %w", appName, err)
		}
	}

	envVars := getNodeEnvVars(appEnvPath)

	for _, dir := range []string{envVars["npm_config_store_dir"], envVars["npm_config_global_dir"]} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %q: %w", dir, err)
		}
	}

	pnpmCjsPath := env.GetPNPMPath(storeRoot, pnpmVersion, pnpmHash)

	args := buildPNPMInstallArgs(pnpmCjsPath, appConfig.LockFile != "")

	nodeBinDir := filepath.Dir(nodeBinPath)
	envVars["PATH"] = nodeBinDir + string(os.PathListSeparator) + os.Getenv("PATH")
	cmdEnv := buildEnvWithOverrides(os.Environ(), envVars)

	cmd := exec.Command(nodeBinPath, args...)
	cmd.Dir = appEnvPath
	cmd.Env = cmdEnv
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	log.Debug("installing node app",
		zap.String("app", appName),
		zap.String("package", appConfig.PackageName),
		zap.String("node", rc.Node.NodeVersion),
		zap.String("pnpm", pnpmVersion),
	)

	fmt.Fprintf(os.Stderr, "Installing %s...\n", appName)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to install node app %q: %w", appName, err)
	}

	fmt.Fprintf(os.Stderr, "Installed %s\n", appName)

	cleanupOnError = false
	return nil
}

// GetNodeCommandInfo returns the command info for running a node app. The bin
// shim is executed directly with the node binary's directory prepended to PATH
// so the shim's `#!/usr/bin/env node` (or node's own resolution) finds the
// managed node.
func (rm *RuntimeManager) GetNodeCommandInfo(appName string, appConfig *binmanager.AppConfigNode, files map[string]string, archives map[string]*binmanager.ArchiveSpec) (*binmanager.CommandInfo, error) {
	appEnvPath, runtimeName, rc, err := rm.resolveNodeAppEnvPath(appName, appConfig, files, archives)
	if err != nil {
		return nil, err
	}

	if rc.Node == nil {
		return nil, fmt.Errorf("runtime for %q has no node config (nodeVersion/pnpmVersion)", appName)
	}

	if err := validateRelativePath(appConfig.BinPath); err != nil {
		return nil, fmt.Errorf("app %q: unsafe binPath: %w", appName, err)
	}

	nodeBinPath, err := rm.installNode(runtimeName)
	if err != nil {
		return nil, err
	}
	nodeBinDir := filepath.Dir(nodeBinPath)
	appBinPath := filepath.Join(appEnvPath, appConfig.BinPath)

	envVars := getNodeEnvVars(appEnvPath)
	envVars["PATH"] = nodeBinDir + string(os.PathListSeparator) + os.Getenv("PATH")

	return &binmanager.CommandInfo{
		Type:    "node",
		Command: appBinPath,
		Args:    nil,
		Env:     envVars,
	}, nil
}
