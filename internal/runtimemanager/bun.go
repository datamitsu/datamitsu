package runtimemanager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"

	"go.uber.org/zap"
)

const bunRuntimeBinDir = ".datamitsu-runtime-bin"

// bunGuardOptions keeps a target repository's ambient Bun inputs out of the
// processes datamitsu starts. Command-line flags reach only the process
// datamitsu spawns itself, while BUN_OPTIONS is inherited, so the `node`
// children of lifecycle scripts and tools carry the same guards.
const bunGuardOptions = "--config=" + os.DevNull + " --no-env-file --no-install"

func bunNodeAliasPath(appEnvPath string) string {
	name := "node"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(appEnvPath, bunRuntimeBinDir, name)
}

// writeBunNodeAlias supplies the `node` command expected by npm lifecycle
// scripts without acquiring Node. Bun emulates the node CLI only when argv[0]
// names it `node`, so the alias must preserve that name: behind a wrapper
// script Bun sees its own name instead and reads `node build` as its bundler,
// `node test` as its test runner and `node install` as its package manager. On
// POSIX the alias is an absolute symlink, the shape the OCI bundle extractor
// relocates to the consumer store root. Windows has no argv[0]-preserving
// indirection, so the alias is a hard link to the executable there, and a copy
// when the link cannot cross volumes.
func writeBunNodeAlias(appEnvPath, bunBinPath string) error {
	target, err := absoluteBunPath(bunBinPath)
	if err != nil {
		return err
	}
	aliasPath := bunNodeAliasPath(appEnvPath)
	if err := os.MkdirAll(filepath.Dir(aliasPath), 0o755); err != nil {
		return fmt.Errorf("create Bun runtime alias directory: %w", err)
	}
	if err := os.Remove(aliasPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("replace Bun node alias: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(target, aliasPath); err != nil {
			return fmt.Errorf("link Bun node alias: %w", err)
		}
		return nil
	}
	if err := os.Link(target, aliasPath); err == nil {
		return nil
	}
	return copyBunExecutable(target, aliasPath)
}

// absoluteBunPath resolves a system-mode command name through PATH: the alias
// is a link, so a bare `bun` would resolve against the alias directory instead.
func absoluteBunPath(bunBinPath string) (string, error) {
	if filepath.IsAbs(bunBinPath) {
		return bunBinPath, nil
	}
	resolved, err := exec.LookPath(bunBinPath)
	if err != nil {
		return "", fmt.Errorf("resolve Bun command %q: %w", bunBinPath, err)
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve Bun command %q: %w", bunBinPath, err)
	}
	return abs, nil
}

func copyBunExecutable(target, aliasPath string) error {
	src, err := os.Open(target)
	if err != nil {
		return fmt.Errorf("open Bun executable for the node alias: %w", err)
	}
	defer func() { _ = src.Close() }()

	dst, err := os.OpenFile(aliasPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return fmt.Errorf("create Bun node alias: %w", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return fmt.Errorf("copy Bun executable into the node alias: %w", err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("close Bun node alias: %w", err)
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
		return fmt.Errorf("runtime for %q has no bun config (bunVersion/pnpmVersion)", appName)
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
		pnpmVersion:    rc.Bun.PNPMVersion,
		pnpmHash:       rc.Bun.PNPMHash,
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
		return nil, "", fmt.Errorf("runtime for %q has no bun config (bunVersion/pnpmVersion)", appName)
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
	envVars["BUN_OPTIONS"] = bunGuardOptions
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
		// execution boundary it gets under Node; BUN_OPTIONS carries the same
		// guards into the `node` processes the tool starts itself. --bun also
		// makes subprocesses that invoke `node` resolve back to Bun.
		Args:          []string{"--config=" + os.DevNull, "--no-env-file", "run", "--bun", "--no-install", appBinPath},
		Env:           envVars,
		Artifact:      appBinPath,
		RequiredPaths: requiredPaths,
	}, pathPrefix, nil
}
