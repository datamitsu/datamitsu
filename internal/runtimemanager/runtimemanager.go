// Package runtimemanager resolves, installs and caches managed language
// runtimes (uv, node, jvm, go) and the apps that run on top of them.
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
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/syslist"
	"github.com/datamitsu/datamitsu/internal/target"

	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

var log = logger.Logger.With(zap.Namespace("runtimemanager"))

// RuntimeManager resolves, installs and caches managed language runtimes
// (uv/node/jvm/go) and the apps that run on top of them.
type RuntimeManager struct {
	mapOfRuntimes config.MapOfRuntimes
	hostTarget    target.Target
	lookPathFunc  func(string) (string, error)
	// removeAllFunc is an injectable seam for os.RemoveAll so tests can force a
	// removal failure deterministically (a read-only path is unreliable when the
	// test runs as root). A nil value falls back to os.RemoveAll.
	removeAllFunc func(string) error
	// singleflight groups deduplicate concurrent installs by key: only one call
	// per key runs at a time and all waiters share its result. Unlike the prior
	// sync.Once + sync.Map + CompareAndDelete pattern, a failed call does not
	// orphan in-flight readers, and the next Do after completion starts fresh
	// (so retry-after-error works naturally).
	runtimeInstall singleflight.Group // key: runtimeName
	appInstall     singleflight.Group // key: "kind/appName"
	pnpmInstall    singleflight.Group // key: "pnpmVersion\x00pnpmHash"
}

// New creates a RuntimeManager for the given runtime configuration map.
func New(mapOfRuntimes config.MapOfRuntimes) *RuntimeManager {
	return &RuntimeManager{
		mapOfRuntimes: mapOfRuntimes,
		hostTarget:    target.HostTarget(),
		lookPathFunc:  exec.LookPath,
		removeAllFunc: os.RemoveAll,
	}
}

// systemCommandForKind returns the system binary command name for a runtime kind.
// Used when automatically falling back to system mode on musl. The mapping lives
// in the kind registry (config.LookupRuntimeKind) so it stays in lock-step with
// the hash-fold fields and validation rules; an unknown kind yields "".
func systemCommandForKind(kind config.RuntimeKind) string {
	if info, ok := config.LookupRuntimeKind(kind); ok {
		return info.SystemCommand
	}
	return ""
}

// GetRuntimePath returns the path to a runtime binary, downloading it if needed.
// For managed runtimes, this downloads and caches the binary using BinManager patterns.
// For system runtimes, this returns the system command path as-is.
// Safe for concurrent use from multiple goroutines.
//
// This is the timeout-agnostic entry point used by the command-info paths (which
// only resolve already-installed runtimes). The app-install paths call
// getRuntimePath with their per-app install-timeout context so a stalled runtime
// acquisition is bounded by the same deadline as the install itself.
func (rm *RuntimeManager) GetRuntimePath(runtimeName string) (string, error) {
	return rm.getRuntimePath(context.Background(), runtimeName)
}

// resolveLibcKey tries an exact libc match first, then falls back to "glibc"
// if the requested libc is not found (e.g., musl host using a glibc-only runtime).
// Returns nil if no usable entry exists.
func resolveLibcKey(libcMap map[string]binmanager.BinaryOsArchInfo, libc string) (*binmanager.BinaryOsArchInfo, string) {
	if info, ok := libcMap[libc]; ok {
		return &info, libc
	}
	if libc != "glibc" {
		if info, ok := libcMap["glibc"]; ok {
			return &info, "glibc"
		}
	}
	return nil, ""
}

// ResolveRuntime resolves which runtime to use for an app.
// Priority: app-level runtime override -> global default by kind -> error.
func (rm *RuntimeManager) ResolveRuntime(appRuntimeRef string, kind config.RuntimeKind) (string, config.RuntimeConfig, error) {
	if appRuntimeRef != "" {
		rc, ok := rm.mapOfRuntimes[appRuntimeRef]
		if !ok {
			return "", config.RuntimeConfig{}, fmt.Errorf("runtime %q referenced by app not found", appRuntimeRef)
		}
		if rc.Kind != kind {
			return "", config.RuntimeConfig{}, fmt.Errorf("runtime %q is kind %q, expected %q", appRuntimeRef, rc.Kind, kind)
		}
		rc = rm.resolveEffectiveRuntimeConfig(appRuntimeRef, rc)
		return appRuntimeRef, rc, nil
	}

	names := make([]string, 0, len(rm.mapOfRuntimes))
	for name := range rm.mapOfRuntimes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		rc := rm.mapOfRuntimes[name]
		if rc.Kind == kind {
			rc = rm.resolveEffectiveRuntimeConfig(name, rc)
			return name, rc, nil
		}
	}

	return "", config.RuntimeConfig{}, fmt.Errorf("no runtime of kind %q found", kind)
}

// NodeAppPathExtra holds npm-app-specific parameters for app hash calculation.
type NodeAppPathExtra struct {
	PackageName string
	BinPath     string
}

// GetAppPath returns the cache path for an installed app environment.
// For node apps, pass NodeAppPathExtra to include package-specific fields in the hash.
func (rm *RuntimeManager) GetAppPath(appName string, kind config.RuntimeKind, version string, deps map[string]string, lockHash string, files map[string]string, archives map[string]*binmanager.ArchiveSpec, runtimeName string, nodeExtra ...NodeAppPathExtra) (string, error) {
	rc, ok := rm.mapOfRuntimes[runtimeName]
	if !ok {
		return "", fmt.Errorf("runtime %q not found", runtimeName)
	}

	rc = rm.resolveEffectiveRuntimeConfig(runtimeName, rc)

	var runtimeHash string
	if rc.Mode == config.RuntimeModeManaged {
		osType, err := syslist.GetOsTypeFromString(runtime.GOOS)
		if err != nil {
			return "", fmt.Errorf("failed to detect OS type: %w", err)
		}
		archType, err := syslist.GetArchTypeFromString(runtime.GOARCH)
		if err != nil {
			return "", fmt.Errorf("failed to detect architecture type: %w", err)
		}
		libc := string(rm.hostTarget.Libc)
		runtimeHash, err = calculateRuntimeHash(rc, osType, archType, libc)
		if err != nil {
			return "", fmt.Errorf("failed to calculate runtime hash: %w", err)
		}
	} else {
		runtimeHash = calculateSystemRuntimeHash(rc)
	}

	var appHash string
	if kind == config.RuntimeKindNode && len(nodeExtra) > 0 {
		extra := nodeExtra[0]
		if extra.PackageName == "" {
			return "", errors.New("NodeAppPathExtra.PackageName is required for npm-based apps")
		}
		if extra.BinPath == "" {
			return "", errors.New("NodeAppPathExtra.BinPath is required for npm-based apps")
		}
		filesHash := binmanager.HashFilesAndArchives(files, archives)
		appHash = calculateNodeAppHash(appName, extra.PackageName, version, extra.BinPath, deps, runtimeHash, lockHash, filesHash)
	} else {
		appHash = calculateAppHash(appName, version, deps, runtimeHash, lockHash, binmanager.HashFilesAndArchives(files, archives))
	}

	return env.GetAppEnvPath(string(kind), appName, appHash), nil
}

// runtimeAppRef reports a runtime-managed app's kind and its explicit runtime
// reference (the app's `runtime` field, empty when it relies on the default
// runtime of its kind). It is the single place that encodes the App.* sub-config
// precedence (uv → node → jvm → go) shared by all three dispatch chains:
// CollectRequiredRuntimes routes through it directly, and the typed dispatchers
// (ComputeAppPath/GetCommandInfo) mirror the same ordering — each keeps its own
// switch only because the kind's install/path method takes a differently-typed
// AppConfig*. ok is false for non-runtime apps (binary/shell/empty).
func runtimeAppRef(app binmanager.App) (kind config.RuntimeKind, runtimeRef string, ok bool) {
	switch {
	case app.Uv != nil:
		return config.RuntimeKindUV, app.Uv.Runtime, true
	case app.Node != nil:
		return config.RuntimeKindNode, app.Node.Runtime, true
	case app.Jvm != nil:
		return config.RuntimeKindJVM, app.Jvm.Runtime, true
	case app.Go != nil:
		return config.RuntimeKindGo, app.Go.Runtime, true
	default:
		return "", "", false
	}
}

// ComputeAppPath returns the install directory path for a runtime-managed app without installing it.
// Satisfies binmanager.RuntimeAppManager interface.
func (rm *RuntimeManager) ComputeAppPath(appName string, app binmanager.App) (string, error) {
	switch {
	case app.Uv != nil:
		runtimeName, _, err := rm.ResolveRuntime(app.Uv.Runtime, config.RuntimeKindUV)
		if err != nil {
			return "", fmt.Errorf("failed to resolve UV runtime for %q: %w", appName, err)
		}
		return rm.GetAppPath(appName, config.RuntimeKindUV, uvVersionForHash(app.Uv.Version, app.Uv.RequiresPython), nil, lockFileHash(app.Uv.LockFile), app.Files, app.Archives, runtimeName)
	case app.Node != nil:
		appEnvPath, err := rm.resolveNodeAppEnvPath(appName, app.Node, app.Files, app.Archives)
		if err != nil {
			return "", err
		}
		return appEnvPath, nil
	case app.Jvm != nil:
		runtimeName, _, err := rm.ResolveRuntime(app.Jvm.Runtime, config.RuntimeKindJVM)
		if err != nil {
			return "", fmt.Errorf("failed to resolve JVM runtime for %q: %w", appName, err)
		}
		return rm.GetJVMAppPath(appName, app.Jvm, app.Files, app.Archives, runtimeName)
	case app.Go != nil:
		runtimeName, _, err := rm.ResolveRuntime(app.Go.Runtime, config.RuntimeKindGo)
		if err != nil {
			return "", fmt.Errorf("failed to resolve Go runtime for %q: %w", appName, err)
		}
		return rm.GetGoAppPath(appName, app.Go, app.Files, app.Archives, runtimeName)
	default:
		return "", fmt.Errorf("app %q is not a runtime-managed app", appName)
	}
}

// GetCommandInfo installs (if needed) and returns command info for runtime-managed apps.
// Satisfies binmanager.RuntimeAppManager interface.
func (rm *RuntimeManager) GetCommandInfo(ctx context.Context, appName string, app binmanager.App) (*binmanager.CommandInfo, error) {
	switch {
	case app.Uv != nil:
		if err := rm.InstallUVApp(ctx, appName, app.Uv, app.Env, app.Files, app.Archives); err != nil {
			return nil, err
		}
		return rm.GetUVCommandInfo(appName, app.Uv, app.Files, app.Archives)
	case app.Node != nil:
		// Compute the merged pnpm-workspace.yaml once and thread it into both the
		// install and command-info passes instead of recomputing it in each.
		mergedWorkspaceYAML, err := buildPNPMWorkspace(app.Files)
		if err != nil {
			return nil, fmt.Errorf("failed to compute pnpm-workspace.yaml for %q: %w", appName, err)
		}
		if err := rm.installNodeApp(ctx, appName, app.Node, app.Env, app.Files, app.Archives, mergedWorkspaceYAML); err != nil {
			return nil, err
		}
		return rm.getNodeCommandInfo(ctx, appName, app.Node, app.Files, app.Archives, mergedWorkspaceYAML)
	case app.Jvm != nil:
		if err := rm.InstallJVMApp(ctx, appName, app.Jvm, app.Files, app.Archives); err != nil {
			return nil, err
		}
		return rm.GetJVMCommandInfo(ctx, appName, app.Jvm, app.Files, app.Archives)
	case app.Go != nil:
		if err := rm.InstallGoApp(ctx, appName, app.Go, app.Env, app.Files, app.Archives); err != nil {
			return nil, err
		}
		return rm.GetGoCommandInfo(appName, app.Go, app.Files, app.Archives)
	default:
		return nil, fmt.Errorf("app %q is not a runtime-managed app", appName)
	}
}

// RuntimeInstallResult reports the outcome of installing a single runtime.
type RuntimeInstallResult struct {
	Name  string
	Error error
}

// RuntimeInstallStats summarizes a batch runtime installation: which runtimes
// were downloaded, already cached, skipped, or failed.
type RuntimeInstallStats struct {
	Downloaded    []string
	AlreadyCached []string
	Skipped       []string
	Failed        []RuntimeInstallResult
}

// CollectRequiredRuntimes returns the list of runtime names needed for installation.
// When includeAll is true, all runtimes from the config are returned.
// When false, only runtimes referenced by required apps are returned.
func CollectRequiredRuntimes(apps binmanager.MapOfApps, runtimes config.MapOfRuntimes, includeAll bool) []string {
	if includeAll {
		names := make([]string, 0, len(runtimes))
		for name := range runtimes {
			names = append(names, name)
		}
		sort.Strings(names)
		return names
	}

	sortedRuntimeNames := make([]string, 0, len(runtimes))
	for name := range runtimes {
		sortedRuntimeNames = append(sortedRuntimeNames, name)
	}
	sort.Strings(sortedRuntimeNames)

	needed := make(map[string]bool)
	for _, app := range apps {
		if !app.Required {
			continue
		}

		kind, ref, ok := runtimeAppRef(app)
		if !ok {
			continue
		}

		// An explicit runtime reference is honored only when it exists in the
		// config; a dangling ref contributes nothing (and never falls back to the
		// default of its kind), matching the prior per-kind blocks.
		if ref != "" {
			if _, exists := runtimes[ref]; exists {
				needed[ref] = true
			}
			continue
		}

		for _, name := range sortedRuntimeNames {
			if runtimes[name].Kind == kind {
				needed[name] = true
				break
			}
		}
	}

	result := make([]string, 0, len(needed))
	for name := range needed {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

// InstallRuntimes downloads and caches managed runtimes with progress bars.
// System runtimes are reported as already cached. Returns installation statistics.
func (rm *RuntimeManager) InstallRuntimes(ctx context.Context, names []string, concurrency int) (RuntimeInstallStats, error) {
	stats := RuntimeInstallStats{
		Downloaded:    []string{},
		AlreadyCached: []string{},
		Skipped:       []string{},
		Failed:        []RuntimeInstallResult{},
	}
	if len(names) == 0 {
		return stats, nil
	}

	osType, err := syslist.GetOsTypeFromString(runtime.GOOS)
	if err != nil {
		return stats, fmt.Errorf("failed to detect OS type: %w", err)
	}
	archType, err := syslist.GetArchTypeFromString(runtime.GOARCH)
	if err != nil {
		return stats, fmt.Errorf("failed to detect architecture type: %w", err)
	}

	type runtimeMeta struct {
		configHash string
		binaryPath *string
	}

	libc := string(rm.hostTarget.Libc)
	toDownload := binmanager.MapOfApps{}
	metaMap := map[string]runtimeMeta{}

	for _, name := range names {
		rc, ok := rm.mapOfRuntimes[name]
		if !ok {
			stats.Skipped = append(stats.Skipped, name)
			continue
		}

		rc = rm.resolveEffectiveRuntimeConfig(name, rc)

		if rc.Mode == config.RuntimeModeSystem {
			stats.AlreadyCached = append(stats.AlreadyCached, name)
			continue
		}

		if rc.Managed == nil {
			stats.Skipped = append(stats.Skipped, name)
			continue
		}

		archMap, ok := rc.Managed.Binaries[osType]
		if !ok {
			stats.Skipped = append(stats.Skipped, name)
			continue
		}

		libcMap, ok := archMap[archType]
		if !ok {
			stats.Skipped = append(stats.Skipped, name)
			continue
		}

		info, resolvedLibc := resolveLibcKey(libcMap, libc)
		if info == nil {
			stats.Skipped = append(stats.Skipped, name)
			continue
		}

		configHash, hashErr := calculateRuntimeHash(rc, osType, archType, resolvedLibc)
		if hashErr != nil {
			stats.Failed = append(stats.Failed, RuntimeInstallResult{Name: name, Error: hashErr})
			continue
		}

		checkPath := env.GetRuntimeBinaryPath(name, configHash)
		if info.BinaryPath != nil {
			checkPath = filepath.Join(checkPath, *info.BinaryPath)
		}

		if _, statErr := os.Stat(checkPath); statErr == nil {
			stats.AlreadyCached = append(stats.AlreadyCached, name)
			continue
		}

		metaMap[name] = runtimeMeta{
			configHash: configHash,
			binaryPath: info.BinaryPath,
		}
		toDownload[name] = binmanager.App{
			Required: true,
			Binary: &binmanager.AppConfigBinary{
				Binaries: rc.Managed.Binaries,
			},
		}
	}

	if len(toDownload) == 0 {
		return stats, nil
	}

	tmpBm := binmanager.New(toDownload, nil, nil)
	dlStats, dlErr := tmpBm.InstallWithConcurrency(ctx, true, concurrency, false)
	if dlErr != nil {
		return stats, dlErr
	}

	for _, name := range dlStats.Downloaded {
		meta := metaMap[name]

		binCachePath, pathErr := tmpBm.GetBinaryPath(ctx, name)
		if pathErr != nil {
			stats.Failed = append(stats.Failed, RuntimeInstallResult{Name: name, Error: pathErr})
			continue
		}

		runtimeCachePath := env.GetRuntimeBinaryPath(name, meta.configHash)
		if mvErr := moveRuntimeFiles(binCachePath, runtimeCachePath, meta.binaryPath); mvErr != nil {
			stats.Failed = append(stats.Failed, RuntimeInstallResult{Name: name, Error: mvErr})
			continue
		}

		stats.Downloaded = append(stats.Downloaded, name)
	}

	// Items cached in the BinManager but not yet in the runtime cache path
	// still need to be moved to the runtime cache.
	for _, name := range dlStats.AlreadyCached {
		meta := metaMap[name]

		binCachePath, pathErr := tmpBm.GetBinaryPath(ctx, name)
		if pathErr != nil {
			stats.Failed = append(stats.Failed, RuntimeInstallResult{Name: name, Error: pathErr})
			continue
		}

		runtimeCachePath := env.GetRuntimeBinaryPath(name, meta.configHash)
		if mvErr := moveRuntimeFiles(binCachePath, runtimeCachePath, meta.binaryPath); mvErr != nil {
			stats.Failed = append(stats.Failed, RuntimeInstallResult{Name: name, Error: mvErr})
			continue
		}

		stats.AlreadyCached = append(stats.AlreadyCached, name)
	}

	for _, result := range dlStats.Failed {
		stats.Failed = append(stats.Failed, RuntimeInstallResult{Name: result.Name, Error: result.Error})
	}

	return stats, nil
}

func moveRuntimeFiles(binCachePath, runtimeCachePath string, binaryPath *string) error {
	if binaryPath != nil {
		if err := validateRelativePath(*binaryPath); err != nil {
			return fmt.Errorf("unsafe binaryPath: %w", err)
		}
	}

	info, err := os.Stat(binCachePath)
	if err != nil {
		return fmt.Errorf("failed to stat source path %q: %w", binCachePath, err)
	}

	if !info.IsDir() {
		if binaryPath != nil {
			dst := filepath.Join(runtimeCachePath, *binaryPath)
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return fmt.Errorf("failed to create runtime cache dir %q: %w", filepath.Dir(dst), err)
			}
			return moveFile(binCachePath, dst)
		}
		if err := os.MkdirAll(filepath.Dir(runtimeCachePath), 0o755); err != nil {
			return fmt.Errorf("failed to create runtime cache dir %q: %w", filepath.Dir(runtimeCachePath), err)
		}
		return moveFile(binCachePath, runtimeCachePath)
	}

	entries, err := os.ReadDir(binCachePath)
	if err != nil {
		return fmt.Errorf("failed to read directory %q: %w", binCachePath, err)
	}

	if len(entries) == 0 {
		_ = os.Remove(binCachePath)
		return fmt.Errorf("source directory %q is empty, removing stale cache entry", binCachePath)
	}

	if err := os.RemoveAll(runtimeCachePath); err != nil {
		return fmt.Errorf("failed to clean stale runtime cache %q: %w", runtimeCachePath, err)
	}
	if err := os.MkdirAll(runtimeCachePath, 0o755); err != nil {
		return fmt.Errorf("failed to create runtime cache dir %q: %w", runtimeCachePath, err)
	}
	for _, entry := range entries {
		src := filepath.Join(binCachePath, entry.Name())
		dst := filepath.Join(runtimeCachePath, entry.Name())
		if err := moveFile(src, dst); err != nil {
			return fmt.Errorf("failed to move runtime file %q: %w", entry.Name(), err)
		}
	}
	_ = os.Remove(binCachePath)
	return nil
}

func validateRelativePath(p string) error {
	if filepath.IsAbs(p) {
		return fmt.Errorf("path %q must be relative", p)
	}
	cleaned := filepath.Clean(p)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path %q escapes parent directory", p)
	}
	return nil
}

func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err != nil {
		info, statErr := os.Stat(src)
		if statErr != nil {
			return fmt.Errorf("rename failed: %w, and stat failed: %w", err, statErr)
		}
		if info.IsDir() {
			if copyErr := copyDir(src, dst); copyErr != nil {
				return fmt.Errorf("rename failed: %w, and dir copy fallback failed: %w", err, copyErr)
			}
		} else {
			if copyErr := copyFile(src, dst); copyErr != nil {
				return fmt.Errorf("rename failed: %w, and copy fallback failed: %w", err, copyErr)
			}
		}
		_ = os.RemoveAll(src)
	}
	if info, err := os.Stat(dst); err == nil && !info.IsDir() {
		if err := os.Chmod(dst, 0o755); err != nil {
			return fmt.Errorf("failed to set executable permissions: %w", err)
		}
	}
	return nil
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("failed to create dir %q: %w", dst, err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("failed to read dir %q: %w", src, err)
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		switch {
		case entry.Type()&os.ModeSymlink != 0:
			target, err := os.Readlink(srcPath)
			if err != nil {
				return fmt.Errorf("failed to read symlink %q: %w", srcPath, err)
			}
			if err := os.Symlink(target, dstPath); err != nil {
				return fmt.Errorf("failed to create symlink %q: %w", dstPath, err)
			}
		case entry.IsDir():
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		default:
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFile(src, dst string) (retErr error) {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("failed to stat %q: %w", src, err)
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open %q: %w", src, err)
	}
	defer func() {
		if cErr := srcFile.Close(); cErr != nil && retErr == nil {
			retErr = cErr
		}
	}()

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return fmt.Errorf("failed to open %q for writing: %w", dst, err)
	}
	defer func() {
		if cErr := dstFile.Close(); cErr != nil && retErr == nil {
			retErr = cErr
		}
	}()

	if _, err = io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("failed to copy %q to %q: %w", src, dst, err)
	}
	return nil
}

// removeAll deletes path and any children via the injectable removeAllFunc seam.
// A nil seam (e.g. a RuntimeManager built directly in a test) falls back to
// os.RemoveAll.
func (rm *RuntimeManager) removeAll(path string) error {
	if rm.removeAllFunc != nil {
		return rm.removeAllFunc(path)
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("failed to remove %q: %w", path, err)
	}
	return nil
}

// resolveEffectiveRuntimeConfig automatically overrides managed mode to system mode
// when running on musl and the managed config only has glibc binaries.
// This prevents downloading incompatible glibc binaries on Alpine Linux.
func (rm *RuntimeManager) resolveEffectiveRuntimeConfig(runtimeName string, rc config.RuntimeConfig) config.RuntimeConfig {
	if rc.Mode != config.RuntimeModeManaged {
		return rc
	}

	if rm.hostTarget.Libc != target.LibcMusl {
		return rc
	}

	if rc.Managed == nil || rc.Managed.Binaries == nil {
		return rc
	}

	osType := syslist.OsType(rm.hostTarget.OS)
	archType := syslist.ArchType(rm.hostTarget.Arch)

	archMap, ok := rc.Managed.Binaries[osType]
	if !ok {
		return rc
	}

	libcMap, ok := archMap[archType]
	if !ok {
		return rc
	}

	if _, hasMusl := libcMap["musl"]; hasMusl {
		return rc
	}

	systemCmd := systemCommandForKind(rc.Kind)
	if systemCmd == "" {
		return rc
	}

	systemPath, err := rm.lookPathFunc(systemCmd)
	if err != nil {
		log.Warn("musl binary unavailable and system binary not found, falling back to glibc",
			zap.String("runtime", runtimeName),
			zap.String("system_command", systemCmd),
		)
		return rc
	}

	log.Info("automatic fallback to system mode",
		zap.String("runtime", runtimeName),
		zap.String("reason", "musl binary unavailable"),
		zap.String("system_command", systemPath),
	)

	rc.Mode = config.RuntimeModeSystem
	systemConfig := &config.RuntimeConfigSystem{
		Command: systemPath,
	}
	if rc.System != nil {
		systemConfig.SystemVersion = rc.System.SystemVersion
	}
	rc.System = systemConfig

	return rc
}

func (rm *RuntimeManager) getRuntimePath(ctx context.Context, runtimeName string) (string, error) {
	rc, ok := rm.mapOfRuntimes[runtimeName]
	if !ok {
		return "", fmt.Errorf("runtime %q not found in registry", runtimeName)
	}

	rc = rm.resolveEffectiveRuntimeConfig(runtimeName, rc)

	if rc.Mode == config.RuntimeModeSystem {
		if rc.System == nil {
			return "", fmt.Errorf("runtime %q is system mode but has no system config", runtimeName)
		}
		return rc.System.Command, nil
	}

	if rc.Managed == nil {
		return "", fmt.Errorf("runtime %q is managed mode but has no managed config", runtimeName)
	}

	osType, err := syslist.GetOsTypeFromString(runtime.GOOS)
	if err != nil {
		return "", fmt.Errorf("failed to detect OS type: %w", err)
	}

	archType, err := syslist.GetArchTypeFromString(runtime.GOARCH)
	if err != nil {
		return "", fmt.Errorf("failed to detect architecture type: %w", err)
	}

	libc := string(rm.hostTarget.Libc)

	configHash, err := calculateRuntimeHash(rc, osType, archType, libc)
	if err != nil {
		return "", fmt.Errorf("failed to calculate runtime hash: %w", err)
	}

	binPath := env.GetRuntimeBinaryPath(runtimeName, configHash)

	archMap, ok := rc.Managed.Binaries[osType]
	if !ok {
		return "", fmt.Errorf("runtime %q not available for OS %q", runtimeName, osType)
	}

	libcMap, ok := archMap[archType]
	if !ok {
		return "", fmt.Errorf("runtime %q not available for arch %q on OS %q", runtimeName, archType, osType)
	}

	info, resolvedLibc := resolveLibcKey(libcMap, libc)
	if info == nil {
		return "", fmt.Errorf("runtime %q not available for libc %q on %q/%q", runtimeName, libc, osType, archType)
	}

	if resolvedLibc != libc {
		log.Warn("runtime libc fallback, using incompatible binary (install system binary to enable auto-fallback)",
			zap.String("runtime", runtimeName),
			zap.String("requested", libc),
			zap.String("resolved", resolvedLibc),
		)
		configHash, err = calculateRuntimeHash(rc, osType, archType, resolvedLibc)
		if err != nil {
			return "", fmt.Errorf("failed to calculate runtime hash with fallback libc: %w", err)
		}
		binPath = env.GetRuntimeBinaryPath(runtimeName, configHash)
	}

	if info.BinaryPath != nil {
		if err := validateRelativePath(*info.BinaryPath); err != nil {
			return "", fmt.Errorf("runtime %q: unsafe binaryPath: %w", runtimeName, err)
		}
		binPath = filepath.Join(binPath, *info.BinaryPath)
	}

	if _, err := os.Stat(binPath); err == nil {
		log.Debug("runtime found in cache",
			zap.String("name", runtimeName),
			zap.String("path", binPath),
		)
		return binPath, nil
	}

	_, err, _ = rm.runtimeInstall.Do(runtimeName, func() (any, error) {
		return nil, rm.downloadRuntime(ctx, runtimeName, rc, configHash, info.BinaryPath)
	})
	if err != nil {
		return "", fmt.Errorf("failed to install runtime %q: %w", runtimeName, err)
	}

	if _, err := os.Stat(binPath); err != nil {
		return "", fmt.Errorf("runtime binary not found at %q after download", binPath)
	}

	return binPath, nil
}

func (rm *RuntimeManager) downloadRuntime(ctx context.Context, runtimeName string, rc config.RuntimeConfig, configHash string, binaryPath *string) error {
	// Bail out early if the per-app install deadline already elapsed before we
	// even started acquiring the runtime. The binary download itself runs through
	// binmanager, which applies its own per-app install timeout, so the heavy
	// fetch stays bounded regardless of this context.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("runtime install context canceled before downloading %q: %w", runtimeName, err)
	}

	log.Debug("runtime not found in cache, downloading",
		zap.String("name", runtimeName),
	)

	fmt.Fprintf(os.Stderr, "Downloading runtime %s...\n", runtimeName)

	runtimeApp := binmanager.App{
		Required: true,
		Binary: &binmanager.AppConfigBinary{
			Binaries: rc.Managed.Binaries,
		},
	}

	tmpBinManager := binmanager.New(binmanager.MapOfApps{
		runtimeName: runtimeApp,
	}, nil, nil)

	if err := tmpBinManager.Install(ctx); err != nil {
		return fmt.Errorf("failed to download runtime %q: %w", runtimeName, err)
	}

	runtimeCachePath := env.GetRuntimeBinaryPath(runtimeName, configHash)

	binCachePath, err := tmpBinManager.GetBinaryPath(ctx, runtimeName)
	if err != nil {
		return fmt.Errorf("failed to get binary path for runtime %q: %w", runtimeName, err)
	}

	if err := moveRuntimeFiles(binCachePath, runtimeCachePath, binaryPath); err != nil {
		return fmt.Errorf("failed to move runtime files for %q: %w", runtimeName, err)
	}

	fmt.Fprintf(os.Stderr, "Downloaded runtime %s\n", runtimeName)

	return nil
}
