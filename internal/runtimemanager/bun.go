package runtimemanager

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"

	"go.uber.org/zap"
)

const bunRuntimeBinDir = ".datamitsu-runtime-bin"

func bunNodeAliasPath(appEnvPath string) string {
	name := "node"
	if runtime.GOOS == "windows" {
		name += ".cmd"
	}
	return filepath.Join(appEnvPath, bunRuntimeBinDir, name)
}

// writeBunNodeAlias supplies the `node` command expected by npm lifecycle
// scripts without acquiring Node. It is a tiny launcher instead of a symlink so
// system-mode commands such as bare `bun` work on every filesystem.
func writeBunNodeAlias(appEnvPath, bunBinPath string) error {
	aliasPath := bunNodeAliasPath(appEnvPath)
	if err := os.MkdirAll(filepath.Dir(aliasPath), 0o755); err != nil {
		return fmt.Errorf("create Bun runtime alias directory: %w", err)
	}
	if runtime.GOOS == "windows" {
		// A Windows path cannot contain a double quote. Runtime commands are
		// trusted configuration; quoting preserves spaces and doubled percent
		// signs prevent cmd.exe from expanding path fragments as variables.
		batchCommand := strings.ReplaceAll(bunBinPath, "%", "%%")
		content := "@echo off\r\n\"" + batchCommand + "\" %*\r\n"
		if err := os.WriteFile(aliasPath, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write Windows Bun node alias: %w", err)
		}
		return nil
	}
	quoted := "'" + strings.ReplaceAll(bunBinPath, "'", "'\"'\"'") + "'"
	content := "#!/bin/sh\nexec " + quoted + " \"$@\"\n"
	if err := os.WriteFile(aliasPath, []byte(content), 0o755); err != nil {
		return fmt.Errorf("write Bun node alias: %w", err)
	}
	return nil
}

func (rm *RuntimeManager) resolveBunAppEnvPath(appName string, appConfig *binmanager.AppConfigBun, files map[string]string, archives map[string]*binmanager.ArchiveSpec) (string, error) {
	appEnvPath, _, _, err := rm.resolveBunAppEnvPathWith(appName, appConfig, files, archives)
	return appEnvPath, err
}

func (rm *RuntimeManager) resolveBunAppEnvPathWith(appName string, appConfig *binmanager.AppConfigBun, files map[string]string, archives map[string]*binmanager.ArchiveSpec) (appEnvPath string, runtimeName string, rc config.RuntimeConfig, err error) {
	runtimeName, rc, err = rm.ResolveRuntime(appConfig.Runtime, config.RuntimeKindBun)
	if err != nil {
		return "", "", config.RuntimeConfig{}, fmt.Errorf("failed to resolve Bun runtime for %q: %w", appName, err)
	}

	hashFormYAML, err := buildPNPMWorkspaceHashForm(files)
	if err != nil {
		return "", "", config.RuntimeConfig{}, fmt.Errorf("failed to compute pnpm-workspace.yaml hash form for %q: %w", appName, err)
	}
	filesForHash := filesWithWorkspaceYAML(files, hashFormYAML)

	appEnvPath, err = rm.GetAppPath(appName, config.RuntimeKindBun, appConfig.Version, appConfig.Dependencies, lockFileHash(appConfig.LockFile), filesForHash, archives, runtimeName, PackageAppPathExtra{
		PackageName: appConfig.PackageName,
		BinPath:     appConfig.BinPath,
	})
	if err != nil {
		return "", "", config.RuntimeConfig{}, err
	}

	return appEnvPath, runtimeName, rc, nil
}

// InstallBunApp installs a Bun-managed npm app if it is not already cached.
func (rm *RuntimeManager) InstallBunApp(ctx context.Context, appName string, appConfig *binmanager.AppConfigBun, customEnv map[string]string, files map[string]string, archives map[string]*binmanager.ArchiveSpec) error {
	mergedWorkspaceYAML, err := buildPNPMWorkspace(files)
	if err != nil {
		return fmt.Errorf("failed to compute pnpm-workspace.yaml for %q: %w", appName, err)
	}
	return rm.installBunApp(ctx, appName, appConfig, customEnv, files, archives, mergedWorkspaceYAML)
}

func (rm *RuntimeManager) installBunApp(ctx context.Context, appName string, appConfig *binmanager.AppConfigBun, customEnv map[string]string, files map[string]string, archives map[string]*binmanager.ArchiveSpec, mergedWorkspaceYAML string) error {
	ctx, cancel, timeoutSec := newInstallContext(ctx)
	defer cancel()
	key := "bun/" + appName
	_, err, _ := rm.appInstall.Do(key, func() (any, error) {
		return nil, rm.installBunAppOnce(ctx, appName, appConfig, customEnv, files, archives, mergedWorkspaceYAML)
	})
	return wrapInstallTimeout(err, timeoutSec)
}

func (rm *RuntimeManager) installBunAppOnce(ctx context.Context, appName string, appConfig *binmanager.AppConfigBun, customEnv map[string]string, files map[string]string, archives map[string]*binmanager.ArchiveSpec, mergedWorkspaceYAML string) error {
	appEnvPath, runtimeName, rc, err := rm.resolveBunAppEnvPathWith(appName, appConfig, files, archives)
	if err != nil {
		return err
	}
	if rc.Bun == nil {
		return fmt.Errorf("runtime for %q has no bun config (bunVersion/pnpmRuntime)", appName)
	}

	return rm.installPNPMAppOnce(ctx, pnpmAppInstallSpec{
		appName:        appName,
		packageName:    appConfig.PackageName,
		version:        appConfig.Version,
		binPath:        appConfig.BinPath,
		lockFile:       appConfig.LockFile,
		dependencies:   appConfig.Dependencies,
		runtimeKind:    "bun",
		runtimeName:    runtimeName,
		runtimeVersion: rc.Bun.BunVersion,
		pnpmRuntime:    rc.Bun.PNPMRuntime,
		appEnvPath:     appEnvPath,
	}, customEnv, files, archives, mergedWorkspaceYAML)
}

// GetBunCommandInfo returns command information for running a Bun app.
func (rm *RuntimeManager) GetBunCommandInfo(ctx context.Context, appName string, appConfig *binmanager.AppConfigBun, files map[string]string, archives map[string]*binmanager.ArchiveSpec) (*binmanager.CommandInfo, error) {
	info, _, err := rm.bunCommandInfo(appName, appConfig, files, archives, func(runtimeName string) (string, error) {
		return rm.getRuntimePath(ctx, runtimeName)
	})
	return info, err
}

func (rm *RuntimeManager) resolveBunCommandInfo(appName string, appConfig *binmanager.AppConfigBun, files map[string]string, archives map[string]*binmanager.ArchiveSpec) (*binmanager.CommandInfo, error) {
	info, pathPrefix, err := rm.bunCommandInfo(appName, appConfig, files, archives, rm.ResolveRuntimePath)
	if err != nil {
		return nil, err
	}
	// ResolveCommandInfo is persisted in the source-mode farm manifest. Record
	// only stable store-owned prefixes; the shim prepends them to the caller's
	// live PATH when it runs, so capturing this process's PATH would leak stale
	// per-shell paths into every future invocation.
	if info.Env != nil {
		if pathPrefix == "" {
			delete(info.Env, "PATH")
		} else {
			info.Env["PATH"] = pathPrefix
		}
	}
	return info, nil
}

// bunCommandInfo builds the common exec/resolve representation. pathPrefix is
// returned separately so the resolve path can persist only runtime-owned paths
// while the immediate exec path still inherits the caller's PATH.
func (rm *RuntimeManager) bunCommandInfo(appName string, appConfig *binmanager.AppConfigBun, files map[string]string, archives map[string]*binmanager.ArchiveSpec, bunBin func(string) (string, error)) (*binmanager.CommandInfo, string, error) {
	appEnvPath, runtimeName, rc, err := rm.resolveBunAppEnvPathWith(appName, appConfig, files, archives)
	if err != nil {
		return nil, "", err
	}
	if rc.Bun == nil {
		return nil, "", fmt.Errorf("runtime for %q has no bun config (bunVersion/pnpmRuntime)", appName)
	}
	if err := validateRelativePath(appConfig.BinPath); err != nil {
		return nil, "", fmt.Errorf("app %q: unsafe binPath: %w", appName, err)
	}

	bunBinPath, err := bunBin(runtimeName)
	if err != nil {
		return nil, "", fmt.Errorf("failed to resolve Bun runtime %q: %w", runtimeName, err)
	}
	appBinPath := filepath.Join(appEnvPath, appConfig.BinPath)

	pathParts := make([]string, 0, 3)
	aliasDir := filepath.Dir(bunNodeAliasPath(appEnvPath))
	if filepath.IsAbs(aliasDir) {
		pathParts = append(pathParts, aliasDir)
	}
	appDepBinDir := filepath.Join(appEnvPath, "node_modules", ".bin")
	if filepath.IsAbs(appDepBinDir) {
		pathParts = append(pathParts, appDepBinDir)
	} else {
		log.Warn("app dependency bin directory is not absolute; executables shipped by the app's own dependencies will not be found", zap.String("app", appName), zap.String("dir", appDepBinDir))
	}
	if runtimeBinDir := filepath.Dir(bunBinPath); filepath.IsAbs(runtimeBinDir) {
		pathParts = append(pathParts, runtimeBinDir)
	}
	pathPrefix := strings.Join(pathParts, string(os.PathListSeparator))

	envVars := getPNPMEnvVars(appEnvPath)
	inheritedPath := os.Getenv("PATH") //nolint:forbidigo // standard PATH for child process env, not a datamitsu env var
	if pathPrefix == "" {
		envVars["PATH"] = inheritedPath
	} else {
		envVars["PATH"] = pathPrefix + string(os.PathListSeparator) + inheritedPath
	}

	requiredPaths := []string{
		filepath.Join(appEnvPath, "node_modules", appConfig.PackageName, "package.json"),
		bunNodeAliasPath(appEnvPath),
	}
	if rc.Mode != config.RuntimeModeSystem && filepath.IsAbs(bunBinPath) {
		requiredPaths = append(requiredPaths, bunBinPath)
	}

	return &binmanager.CommandInfo{
		Type:    "bun",
		Command: bunBinPath,
		// Bun normally reads bunfig.toml and .env files from the tool's target
		// repository. Disable both ambient inputs so a pinned tool has the same
		// execution boundary it gets under Node. --bun also makes subprocesses
		// that invoke `node` resolve back to Bun.
		Args:          []string{"--config=" + os.DevNull, "--no-env-file", "run", "--bun", "--no-install", appBinPath},
		Env:           envVars,
		Artifact:      appBinPath,
		RequiredPaths: requiredPaths,
	}, pathPrefix, nil
}
