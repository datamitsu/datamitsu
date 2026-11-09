// Package runtimemanager resolves, installs and caches managed language
// runtimes (bun, uv, node, jvm, go) and the apps that run on top of them.
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
	"sync"
	"sync/atomic"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/installstate"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/provision"
	"github.com/datamitsu/datamitsu/internal/storelock"
	"github.com/datamitsu/datamitsu/internal/syslist"
	"github.com/datamitsu/datamitsu/internal/target"
	"github.com/datamitsu/datamitsu/internal/trace"
	"go.uber.org/zap"
)

var log = logger.Logger.With(zap.Namespace("runtimemanager"))

// downloaderConstructions counts every point at which this package builds
// something that fetches from the network — the throwaway binmanager used to
// acquire a runtime archive (pnpm included) and the JAR fetch. It is
// the runtimemanager half of the guard described on binmanager.NetworkDownloads:
// ResolveCommandInfo must leave both counters untouched, so a future refactor
// cannot silently reintroduce a network call on the resolution path.
var downloaderConstructions atomic.Int64

// DownloaderConstructions reports how many times this package has constructed
// something that fetches from the network in this process. Test-visible guard,
// not part of the runtime contract.
func DownloaderConstructions() int64 { return downloaderConstructions.Load() }

// RuntimeManager resolves, installs and caches managed language runtimes
// (bun/uv/node/jvm/go) and the apps that run on top of them.
type RuntimeManager struct {
	mapOfRuntimes config.MapOfRuntimes
	hostTarget    target.Target
	lookPathFunc  func(string) (string, error)
	// removeAllFunc is an injectable seam for os.RemoveAll so tests can force a
	// removal failure deterministically (a read-only path is unreliable when the
	// test runs as root). A nil value falls back to os.RemoveAll.
	removeAllFunc func(string) error
}

// New creates a RuntimeManager for the given runtime configuration map.
func New(mapOfRuntimes config.MapOfRuntimes) *RuntimeManager {
	return &RuntimeManager{
		mapOfRuntimes: mapOfRuntimes,
		hostTarget:    target.HostTarget(),
		lookPathFunc:  exec.LookPath,
		removeAllFunc: nil,
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

func validRuntimePathName(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name && !strings.ContainsAny(name, `/\\`)
}

// ErrUnknownRuntime indicates that the requested name is not configured.
var ErrUnknownRuntime = errors.New("unknown runtime")

// RuntimeStatus reports whether a configured runtime can currently be executed.
type RuntimeStatus string

// RuntimeStatus values describe whether the configured target is usable now.
const (
	RuntimeStatusInstalled   RuntimeStatus = "installed"
	RuntimeStatusMissing     RuntimeStatus = "missing"
	RuntimeStatusUnavailable RuntimeStatus = "unavailable"
)

// RuntimeExecution is the fully resolved direct-interpreter execution contract.
type RuntimeExecution struct {
	Executable string             `json:"executable"`
	Env        map[string]string  `json:"env"`
	Kind       config.RuntimeKind `json:"kind"`
	Mode       config.RuntimeMode `json:"mode"`
	Identity   string             `json:"identity"`
	DataRoot   string             `json:"dataRoot"`
	Status     RuntimeStatus      `json:"status"`
	Reason     string             `json:"reason,omitempty"`
}

// RuntimeInventoryEntry describes one configured runtime without materializing it.
type RuntimeInventoryEntry struct {
	Name              string             `json:"name"`
	Kind              config.RuntimeKind `json:"kind"`
	ConfiguredMode    config.RuntimeMode `json:"configuredMode"`
	EffectiveMode     config.RuntimeMode `json:"effectiveMode"`
	Version           string             `json:"version"`
	Runnable          bool               `json:"runnable"`
	Status            RuntimeStatus      `json:"status"`
	Executable        string             `json:"executable,omitempty"`
	Reason            string             `json:"reason,omitempty"`
	NotRunnableReason string             `json:"notRunnableReason,omitempty"`
}

// EnsureExecutionRequest is sent to the provisioning worker before runtime mutation.
type EnsureExecutionRequest struct {
	StoreRoot string               `json:"storeRoot"`
	CacheRoot string               `json:"cacheRoot"`
	Runtimes  config.MapOfRuntimes `json:"runtimes"`
	Name      string               `json:"name"`
}

// GetRuntimePath returns an installed runtime binary, acquiring it if necessary.
func (rm *RuntimeManager) GetRuntimePath(ctx context.Context, runtimeName string) (string, error) {
	resolved, err := rm.computeRuntimeBinPath(runtimeName)
	if err != nil {
		return "", err
	}
	handled, _, err := rm.dispatchInstallRuntimes(ctx, []string{runtimeName}, 1)
	if err != nil {
		return "", err
	}
	if handled {
		return resolved.binPath, nil
	}
	return rm.getRuntimePath(ctx, runtimeName)
}

// ResolveExecution computes the selected interpreter and runtime-owned environment without mutation.
func (rm *RuntimeManager) ResolveExecution(runtimeName string) (RuntimeExecution, error) {
	rc, ok := rm.mapOfRuntimes[runtimeName]
	if !ok {
		return RuntimeExecution{}, fmt.Errorf("%w: %q", ErrUnknownRuntime, runtimeName)
	}
	if reason := runtimeNotRunnableReason(rc.Kind); reason != "" {
		return RuntimeExecution{}, fmt.Errorf("runtime %q: %s", runtimeName, reason)
	}
	if !validRuntimePathName(runtimeName) {
		return RuntimeExecution{}, fmt.Errorf("runtime %q is not a safe store path component", runtimeName)
	}
	resolved, err := rm.computeRuntimeBinPath(runtimeName)
	if err != nil {
		return RuntimeExecution{}, err
	}
	return rm.resolveExecutionFor(runtimeName, rc, resolved)
}

// EnsureExecution provisions the interpreter, then returns an unlocked execution description.
func (rm *RuntimeManager) EnsureExecution(ctx context.Context, runtimeName string) (RuntimeExecution, error) {
	if err := env.RejectInstallChild("runtime execution provisioning"); err != nil {
		return RuntimeExecution{}, err
	}
	configured, ok := rm.mapOfRuntimes[runtimeName]
	if !ok {
		return RuntimeExecution{}, fmt.Errorf("%w: %q", ErrUnknownRuntime, runtimeName)
	}
	if reason := runtimeNotRunnableReason(configured.Kind); reason != "" {
		return RuntimeExecution{}, fmt.Errorf("runtime %q: %s", runtimeName, reason)
	}
	if !validRuntimePathName(runtimeName) {
		return RuntimeExecution{}, fmt.Errorf("runtime %q is not a safe store path component", runtimeName)
	}
	request := EnsureExecutionRequest{StoreRoot: env.GetStorePath(), CacheRoot: env.GetCachePath(), Runtimes: rm.mapOfRuntimes, Name: runtimeName}
	var result RuntimeExecution
	handled, err := provision.Call(ctx, "ensure-execution", request, &result)
	if handled || err != nil {
		return result, err
	}
	resolved, err := rm.ResolveExecution(runtimeName)
	if err != nil {
		return RuntimeExecution{}, err
	}
	if resolved.Status == RuntimeStatusInstalled {
		return resolved, nil
	}
	if resolved.Kind != config.RuntimeKindUV && resolved.Mode != config.RuntimeModeManaged {
		if resolved.Reason != "" {
			return RuntimeExecution{}, fmt.Errorf("runtime %q: %s", runtimeName, resolved.Reason)
		}
		return RuntimeExecution{}, fmt.Errorf("runtime %q is not installed and system runtimes are not provisioned", runtimeName)
	}
	installCtx, cancel, timeout := newInstallContext(ctx)
	defer cancel()
	var executable string
	switch resolved.Kind {
	case config.RuntimeKindUV:
		executable, err = rm.EnsurePython(installCtx, runtimeName)
	case config.RuntimeKindNode, config.RuntimeKindBun, config.RuntimeKindGo, config.RuntimeKindJVM:
		executable, err = rm.GetRuntimePath(installCtx, runtimeName)
	case config.RuntimeKindPNPM:
		return RuntimeExecution{}, fmt.Errorf("runtime %q cannot be run directly: %s", runtimeName, runtimeNotRunnableReason(resolved.Kind))
	default:
		return RuntimeExecution{}, fmt.Errorf("runtime %q has unsupported kind %q", runtimeName, resolved.Kind)
	}
	if err != nil {
		return RuntimeExecution{}, wrapInstallTimeout(err, timeout)
	}
	resolved.Executable, resolved.Status, resolved.Reason = executable, RuntimeStatusInstalled, ""
	return resolved, nil
}

func runtimeNotRunnableReason(kind config.RuntimeKind) string {
	if kind == config.RuntimeKindPNPM {
		return "pnpm is a package manager, not a directly runnable interpreter; configure an app and use datamitsu exec to run pnpm-managed tools"
	}
	if runnableRuntimeKind(kind) {
		return ""
	}
	return fmt.Sprintf("runtime kind %q is not directly runnable", kind)
}

func runnableRuntimeKind(kind config.RuntimeKind) bool {
	switch kind {
	case config.RuntimeKindNode, config.RuntimeKindBun, config.RuntimeKindUV, config.RuntimeKindGo, config.RuntimeKindJVM:
		return true
	case config.RuntimeKindPNPM:
		return false
	default:
		return false
	}
}

// RuntimeInventory resolves every configured entry without provisioning or creating directories.
func (rm *RuntimeManager) RuntimeInventory() ([]RuntimeInventoryEntry, error) {
	names := make([]string, 0, len(rm.mapOfRuntimes))
	for name := range rm.mapOfRuntimes {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]RuntimeInventoryEntry, 0, len(names))
	for _, name := range names {
		configured := rm.mapOfRuntimes[name]
		entry := RuntimeInventoryEntry{Name: name, Kind: configured.Kind, ConfiguredMode: configured.Mode, EffectiveMode: configured.Mode, Runnable: runnableRuntimeKind(configured.Kind), Status: RuntimeStatusUnavailable}
		entry.NotRunnableReason = runtimeNotRunnableReason(configured.Kind)
		entry.Version = runtimeConfiguredVersion(configured)
		if !validRuntimePathName(name) {
			entry.Reason = "runtime name is not a safe store path component"
			entries = append(entries, entry)
			continue
		}
		resolved, err := rm.computeRuntimeBinPath(name)
		if err != nil {
			entry.Reason = err.Error()
			entries = append(entries, entry)
			continue
		}
		entry.EffectiveMode = resolved.rc.Mode
		if configured.Kind == config.RuntimeKindUV && configured.UV != nil {
			entry.Version = configured.UV.PythonVersion
		}
		execPath := resolved.binPath
		if configured.Kind == config.RuntimeKindUV {
			execPath, err = rm.ResolvePython(name)
		} else if resolved.rc.Mode == config.RuntimeModeSystem {
			execPath, err = rm.lookPathFunc(resolved.rc.System.Command)
		} else if info, statErr := os.Stat(execPath); statErr != nil || info.IsDir() {
			err = errors.New("runtime executable is not installed")
		}
		if err != nil {
			entry.Status, entry.Reason = RuntimeStatusMissing, err.Error()
			if resolved.rc.Mode == config.RuntimeModeManaged && configured.Kind != config.RuntimeKindUV {
				entry.Executable = execPath
			}
		} else {
			entry.Status, entry.Executable = RuntimeStatusInstalled, execPath
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func runtimeConfiguredVersion(rc config.RuntimeConfig) string {
	if rc.System != nil && rc.System.SystemVersion != "" {
		return rc.System.SystemVersion
	}
	switch rc.Kind {
	case config.RuntimeKindNode:
		if rc.Node != nil {
			return rc.Node.NodeVersion
		}
	case config.RuntimeKindBun:
		if rc.Bun != nil {
			return rc.Bun.BunVersion
		}
	case config.RuntimeKindUV:
		if rc.UV != nil {
			return rc.UV.PythonVersion
		}
	case config.RuntimeKindGo:
		if rc.Go != nil {
			return rc.Go.GoVersion
		}
	case config.RuntimeKindJVM:
		if rc.JVM != nil {
			return rc.JVM.JavaVersion
		}
	case config.RuntimeKindPNPM:
		if rc.PNPM != nil {
			return rc.PNPM.PNPMVersion
		}
	}
	return ""
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

// ComputeRuntimeStorePath returns the store directory (.runtimes/<name>/<hash>)
// of a managed runtime without installing it, mirroring the hash math of
// getRuntimePath (including the glibc fallback when the host libc has no
// entry). ok is false for system-mode runtimes, which have no store directory.
func (rm *RuntimeManager) ComputeRuntimeStorePath(runtimeName string) (path string, ok bool, err error) {
	rc, found := rm.mapOfRuntimes[runtimeName]
	if !found {
		return "", false, fmt.Errorf("runtime %q not found in registry", runtimeName)
	}

	rc = rm.resolveEffectiveRuntimeConfig(runtimeName, rc)
	if rc.Mode == config.RuntimeModeSystem {
		return "", false, nil
	}
	if rc.Managed == nil {
		return "", false, fmt.Errorf("runtime %q is managed mode but has no managed config", runtimeName)
	}

	osType, err := syslist.GetOsTypeFromString(runtime.GOOS)
	if err != nil {
		return "", false, fmt.Errorf("failed to detect OS type: %w", err)
	}
	archType, err := syslist.GetArchTypeFromString(runtime.GOARCH)
	if err != nil {
		return "", false, fmt.Errorf("failed to detect architecture type: %w", err)
	}
	libc := string(rm.hostTarget.Libc)

	archMap, found := rc.Managed.Binaries[osType]
	if !found {
		return "", false, fmt.Errorf("runtime %q not available for OS %q", runtimeName, osType)
	}
	libcMap, found := archMap[archType]
	if !found {
		return "", false, fmt.Errorf("runtime %q not available for arch %q on OS %q", runtimeName, archType, osType)
	}
	info, resolvedLibc := resolveLibcKey(libcMap, libc)
	if info == nil {
		return "", false, fmt.Errorf("runtime %q not available for libc %q on %q/%q", runtimeName, libc, osType, archType)
	}

	configHash, err := calculateRuntimeHash(rc, osType, archType, resolvedLibc)
	if err != nil {
		return "", false, fmt.Errorf("failed to calculate runtime hash: %w", err)
	}
	return env.GetRuntimeBinaryPath(runtimeName, configHash), true, nil
}

// ResolveRuntimePath returns where a runtime's binary lives without installing
// it. The path is returned whether or not the file exists; callers that care
// stat it themselves.
func (rm *RuntimeManager) ResolveRuntimePath(runtimeName string) (string, error) {
	resolved, err := rm.computeRuntimeBinPath(runtimeName)
	if err != nil {
		return "", err
	}
	return resolved.binPath, nil
}

// PackageAppPathExtra holds package-app-specific parameters for app hash calculation.
type PackageAppPathExtra struct {
	PackageName string
	BinPath     string
}

// GetAppPath returns the cache path for an installed app environment.
// For Node and Bun apps, pass PackageAppPathExtra to include package-specific fields in the hash.
func (rm *RuntimeManager) GetAppPath(appName string, kind config.RuntimeKind, version string, deps map[string]string, lockHash string, files map[string]string, archives map[string]*binmanager.ArchiveSpec, runtimeName string, packageExtra ...PackageAppPathExtra) (string, error) {
	rc, ok := rm.mapOfRuntimes[runtimeName]
	if !ok {
		return "", fmt.Errorf("runtime %q not found", runtimeName)
	}

	rc = rm.resolveEffectiveRuntimeConfig(runtimeName, rc)

	osType, archType, libc, err := rm.hostRuntimePlatform()
	if err != nil {
		return "", err
	}
	runtimeHash, err := rm.runtimeIdentityHash(rc, osType, archType, libc)
	if err != nil {
		return "", err
	}

	var appHash string
	if (kind == config.RuntimeKindNode || kind == config.RuntimeKindBun) && len(packageExtra) > 0 {
		extra := packageExtra[0]
		if extra.PackageName == "" {
			return "", errors.New("PackageAppPathExtra.PackageName is required for package-based apps")
		}
		if extra.BinPath == "" {
			return "", errors.New("PackageAppPathExtra.BinPath is required for package-based apps")
		}
		pnpmName, pnpmHash, err := rm.pnpmIdentityHash(rc, osType, archType, libc)
		if err != nil {
			return "", err
		}
		filesHash := binmanager.HashFilesAndArchives(files, archives)
		appHash = calculatePackageAppHash(appName, extra.PackageName, version, extra.BinPath, deps, runtimeHash, pnpmHash, lockHash, filesHash, runtimeName, pnpmName)
	} else {
		appHash = calculateAppHash(appName, version, deps, runtimeHash, lockHash, binmanager.HashFilesAndArchives(files, archives), runtimeName)
	}

	return env.GetAppEnvPath(string(kind), appName, appHash), nil
}

// runtimeAppRef reports a runtime-managed app's kind and its explicit runtime
// reference (the app's `runtime` field, empty when it relies on the default
// runtime of its kind). It is the single place that encodes the App.* sub-config
// precedence (bun → uv → node → jvm → go) shared by all three dispatch chains:
// CollectRequiredRuntimes routes through it directly, and the typed dispatchers
// (ComputeAppPath/GetCommandInfo) mirror the same ordering — each keeps its own
// switch only because the kind's install/path method takes a differently-typed
// AppConfig*. ok is false for non-runtime apps (binary/shell/empty).
func runtimeAppRef(app binmanager.App) (kind config.RuntimeKind, runtimeRef string, ok bool) {
	switch {
	case app.Bun != nil:
		return config.RuntimeKindBun, app.Bun.Runtime, true
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
	case app.Bun != nil:
		return rm.resolveBunAppEnvPath(appName, app.Bun, app.Files, app.Archives)
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

// Installed checks the resolved runtime executable without provisioning it.
func (rm *RuntimeManager) Installed(runtimeName string) bool {
	resolved, err := rm.computeRuntimeBinPath(runtimeName)
	if err != nil {
		return false
	}
	info, err := os.Stat(resolved.binPath)
	return err == nil && !info.IsDir()
}

// GetCommandInfo installs (if needed) and returns command info for runtime-managed apps.
// Satisfies binmanager.RuntimeAppManager interface.
func (rm *RuntimeManager) GetCommandInfo(ctx context.Context, appName string, app binmanager.App) (*binmanager.CommandInfo, error) {
	switch {
	case app.Bun != nil:
		mergedWorkspaceYAML, err := rm.pnpmWorkspace(app.Bun.Runtime, config.RuntimeKindBun, app.Files)
		if err != nil {
			return nil, fmt.Errorf("failed to compute pnpm-workspace.yaml for %q: %w", appName, err)
		}
		if err := rm.installBunApp(ctx, appName, app.Bun, app.Env, app.Files, app.Archives, mergedWorkspaceYAML); err != nil {
			return nil, err
		}
		return rm.GetBunCommandInfo(ctx, appName, app.Bun, app.Files, app.Archives)
	case app.Uv != nil:
		if err := rm.InstallUVApp(ctx, appName, app.Uv, app.Env, app.Files, app.Archives); err != nil {
			return nil, err
		}
		return rm.GetUVCommandInfo(appName, app.Uv, app.Files, app.Archives)
	case app.Node != nil:
		// Compute the merged pnpm-workspace.yaml once and thread it into both the
		// install and command-info passes instead of recomputing it in each.
		mergedWorkspaceYAML, err := rm.pnpmWorkspace(app.Node.Runtime, config.RuntimeKindNode, app.Files)
		if err != nil {
			return nil, fmt.Errorf("failed to compute pnpm-workspace.yaml for %q: %w", appName, err)
		}
		if err := rm.installNodeApp(ctx, appName, app.Node, app.Env, app.Files, app.Archives, mergedWorkspaceYAML); err != nil {
			return nil, err
		}
		return rm.getNodeCommandInfo(ctx, appName, app.Node, app.Files, app.Archives)
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

// ResolveCommandInfo returns the same CommandInfo GetCommandInfo would return
// for a runtime-managed app, without installing anything: no download, no
// subprocess, no filesystem mutation. It composes the install-free halves of
// the five per-kind resolvers, so the Command/Args/Env it reports are the ones
// the exec path will actually use once the app is installed.
//
// The app need not be installed — the returned Command is the path it will
// occupy. Callers decide installed-ness by stating that path themselves
// (binmanager.ResolveCommandInfo does).
//
// Satisfies binmanager.RuntimeAppManager interface.
func (rm *RuntimeManager) ResolveCommandInfo(appName string, app binmanager.App) (*binmanager.CommandInfo, error) {
	switch {
	case app.Bun != nil:
		return rm.resolveBunCommandInfo(appName, app.Bun, app.Files, app.Archives)
	case app.Uv != nil:
		return rm.GetUVCommandInfo(appName, app.Uv, app.Files, app.Archives)
	case app.Node != nil:
		return rm.resolveNodeCommandInfo(appName, app.Node, app.Files, app.Archives)
	case app.Jvm != nil:
		return rm.resolveJVMCommandInfo(appName, app.Jvm, app.Files, app.Archives)
	case app.Go != nil:
		return rm.GetGoCommandInfo(appName, app.Go, app.Files, app.Archives)
	default:
		return nil, fmt.Errorf("app %q is not a runtime-managed app", appName)
	}
}

// ResolvePreparePathPrefix returns the runtime-owned PATH prefix for a
// prepare step: the same components the resolve path records, but
// absolutized instead of dropped. The resolve path's contract drops
// relative directories because its answer is persisted into the source-mode
// farm manifest and replayed from other working directories — a relative
// entry would be worse than none there. A prepare step runs once, from a
// staging workdir, with the app fully installed: the components are valid
// for this run and must reach the step even under a relative
// DATAMITSU_CACHE_DIR. Non-runtime apps (shell, binary) answer empty.
func (rm *RuntimeManager) ResolvePreparePathPrefix(appName string, app binmanager.App) string {
	switch {
	case app.Bun != nil:
		return rm.preparePathPrefixBun(appName, app.Bun, app.Files, app.Archives)
	case app.Node != nil:
		return rm.preparePathPrefixNode(appName, app.Node, app.Files, app.Archives)
	default:
		return ""
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
// References from the required apps' dependsOn closure are included too.
func CollectRequiredRuntimes(apps binmanager.MapOfApps, runtimes config.MapOfRuntimes, includeAll bool) ([]string, error) {
	if includeAll {
		names := make([]string, 0, len(runtimes))
		for name := range runtimes {
			names = append(names, name)
		}
		sort.Strings(names)
		return names, nil
	}

	var roots []string
	for name, app := range apps {
		if app.Required {
			roots = append(roots, name)
		}
	}
	return CollectAppRuntimes(apps, runtimes, roots)
}

// CollectAppRuntimes includes the runtimes and pnpm references of the app closure.
func CollectAppRuntimes(apps binmanager.MapOfApps, runtimes config.MapOfRuntimes, roots []string) ([]string, error) {
	names, err := binmanager.AppDependencyClosure(apps, roots)
	if err != nil {
		return nil, err
	}

	sortedRuntimeNames := make([]string, 0, len(runtimes))
	for name := range runtimes {
		sortedRuntimeNames = append(sortedRuntimeNames, name)
	}
	sort.Strings(sortedRuntimeNames)

	needed := make(map[string]bool)
	for _, name := range names {
		app := apps[name]

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

	// A needed Node or Bun runtime installs its apps with a pnpm runtime.
	for _, name := range sortedRuntimeNames {
		if !needed[name] {
			continue
		}
		if pnpmName, ok := runtimes.PNPMRuntimeName(runtimes[name]); ok {
			needed[pnpmName] = true
		}
	}

	result := make([]string, 0, len(needed))
	for name := range needed {
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}

// InstallRuntimes downloads and caches managed runtimes with progress bars.
// System runtimes are reported as already cached. Returns installation statistics.
func (rm *RuntimeManager) InstallRuntimes(ctx context.Context, names []string, concurrency int) (RuntimeInstallStats, error) {
	if handled, stats, err := rm.dispatchInstallRuntimes(ctx, names, concurrency); handled || err != nil {
		return stats, err
	}
	defer trace.Start(trace.CatInstall, "runtime.installAll").EndWith(trace.A("runtimes", len(names)))
	stats := RuntimeInstallStats{Downloaded: []string{}, AlreadyCached: []string{}, Skipped: []string{}, Failed: []RuntimeInstallResult{}}
	type outcome struct {
		name       string
		downloaded bool
		err        error
	}
	results := make([]outcome, len(names))
	jobs := make(chan int)
	var wg sync.WaitGroup
	workers := max(1, min(max(1, concurrency), len(names)))
	for range workers {
		wg.Go(func() {
			for index := range jobs {
				name := names[index]
				path, err := rm.getRuntimePath(ctx, name)
				if err == nil {
					if _, statErr := os.Stat(path); statErr != nil {
						err = fmt.Errorf("runtime binary not found at %q after acquisition", path)
					}
				}
				results[index] = outcome{name: name, downloaded: true, err: err}
			}
		})
	}
	for i, name := range names {
		resolved, err := rm.computeRuntimeBinPath(name)
		if err != nil {
			stats.Skipped = append(stats.Skipped, name)
			continue
		}
		if !resolved.managed {
			stats.AlreadyCached = append(stats.AlreadyCached, name)
			continue
		}
		if _, err := os.Stat(resolved.binPath); err == nil {
			stats.AlreadyCached = append(stats.AlreadyCached, name)
			continue
		}
		results[i].name = name
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	for _, result := range results {
		if result.name == "" {
			continue
		}
		if result.err != nil {
			stats.Failed = append(stats.Failed, RuntimeInstallResult{Name: result.name, Error: result.err})
		} else {
			stats.Downloaded = append(stats.Downloaded, result.name)
		}
	}
	return stats, nil
}

func publishRuntimeCopy(source, destination string, binaryPath *string) error {
	if binaryPath != nil {
		if err := validateRelativePath(*binaryPath); err != nil {
			return fmt.Errorf("unsafe binaryPath: %w", err)
		}
	}
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("stat runtime source %q: %w", source, err)
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create runtime destination parent: %w", err)
	}
	staging, err := os.MkdirTemp(parent, ".runtime-stage-")
	if err != nil {
		return fmt.Errorf("create runtime staging directory: %w", err)
	}
	defer func() { _ = ForceRemoveAll(staging) }()
	payload := filepath.Join(staging, "payload")
	var publishPath string
	switch {
	case info.IsDir():
		if err := copyDir(source, payload); err != nil {
			return fmt.Errorf("copy runtime tree: %w", err)
		}
		publishPath = payload
	case binaryPath != nil:
		publishPath = payload
		filePath := filepath.Join(payload, *binaryPath)
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			return fmt.Errorf("create runtime staging path: %w", err)
		}
		if err := copyFile(source, filePath); err != nil {
			return fmt.Errorf("copy runtime binary: %w", err)
		}
	default:
		if err := copyFile(source, payload); err != nil {
			return fmt.Errorf("copy runtime binary: %w", err)
		}
		publishPath = payload
	}
	if _, err := os.Stat(destination); err == nil {
		if err := ForceRemoveAll(destination); err != nil {
			return fmt.Errorf("remove stale runtime destination: %w", err)
		}
	}
	if err := os.Rename(publishPath, destination); err != nil {
		return fmt.Errorf("publish runtime destination: %w", err)
	}
	return nil
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

func (rm *RuntimeManager) resolveExecutionFor(name string, configured config.RuntimeConfig, resolved resolvedRuntime) (RuntimeExecution, error) {
	rc := resolved.rc
	identity, err := rm.runtimeDataIdentity(name, rc)
	if err != nil {
		return RuntimeExecution{}, err
	}
	var dataRoot string
	executable := resolved.binPath
	available := true
	var reason string
	switch {
	case rc.Kind == config.RuntimeKindUV:
		executable, err = rm.ResolvePython(name)
		if err != nil {
			reason = err.Error()
			executable = ""
		}
	case rc.Mode == config.RuntimeModeSystem:
		executable, err = rm.lookPathFunc(rc.System.Command)
		if err != nil {
			reason = err.Error()
			executable = ""
		}
	default:
		info, statErr := os.Stat(executable)
		if statErr != nil || info.IsDir() {
			available = false
			reason = "runtime executable is not installed"
		}
	}
	if executable == "" {
		available = false
	}
	status := RuntimeStatusInstalled
	if !available {
		status = RuntimeStatusMissing
	}
	dataRoot, envVars, err := rm.RuntimeExecutionEnvironment(name, rc)
	if err != nil {
		return RuntimeExecution{}, err
	}
	return RuntimeExecution{Executable: executable, Env: envVars, Kind: configured.Kind, Mode: rc.Mode, Identity: identity, DataRoot: dataRoot, Status: status, Reason: reason}, nil
}

// removeAll deletes path and its children through the injectable test seam.
func (rm *RuntimeManager) removeAll(path string) error {
	if rm.removeAllFunc != nil {
		return rm.removeAllFunc(path)
	}
	if err := ForceRemoveAll(path); err != nil {
		return fmt.Errorf("failed to remove %q: %w", path, err)
	}
	return nil
}

// runtimeIdentityHash is a runtime's cache identity for an explicit platform.
// It never contains a store path, so app hashes stay relocatable.
func (rm *RuntimeManager) runtimeIdentityHash(rc config.RuntimeConfig, osType syslist.OsType, archType syslist.ArchType, libc string) (string, error) {
	if rc.Mode != config.RuntimeModeManaged {
		return calculateSystemRuntimeHash(rc, osType, archType, libc), nil
	}
	hash, err := calculateRuntimeHash(rc, osType, archType, libc)
	if err != nil {
		return "", fmt.Errorf("failed to calculate runtime hash: %w", err)
	}
	return hash, nil
}

func (rm *RuntimeManager) hostRuntimePlatform() (syslist.OsType, syslist.ArchType, string, error) {
	osType, err := syslist.GetOsTypeFromString(runtime.GOOS)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to detect OS type: %w", err)
	}
	archType, err := syslist.GetArchTypeFromString(runtime.GOARCH)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to detect architecture type: %w", err)
	}
	return osType, archType, string(rm.hostTarget.Libc), nil
}

func (rm *RuntimeManager) pnpmIdentityHash(rc config.RuntimeConfig, osType syslist.OsType, archType syslist.ArchType, libc string) (string, string, error) {
	name, pnpmRC, err := rm.ResolveRuntime(rc.PNPMRuntimeRef(), config.RuntimeKindPNPM)
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve pnpm runtime: %w", err)
	}
	hash, err := rm.runtimeIdentityHash(pnpmRC, osType, archType, libc)
	if err != nil {
		return "", "", fmt.Errorf("pnpm runtime %q: %w", name, err)
	}
	return name, hash, nil
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

// resolvedRuntime is the outcome of computing where a runtime's binary lives
// without touching the network. System-mode runtimes carry only BinPath (the
// system command) and have managed == false.
type resolvedRuntime struct {
	binPath    string
	rc         config.RuntimeConfig
	configHash string
	binaryPath *string
	managed    bool
}

// computeRuntimeBinPath resolves the absolute path a runtime's binary occupies
// (or would occupy) in the store, performing no download and no filesystem
// mutation. It is the install-free half of getRuntimePath: both share this
// selection logic, including the glibc fallback and its warning, so a resolve
// and an install can never disagree about which file they mean.
func (rm *RuntimeManager) computeRuntimeBinPath(runtimeName string) (resolvedRuntime, error) {
	rc, ok := rm.mapOfRuntimes[runtimeName]
	if !ok {
		return resolvedRuntime{}, fmt.Errorf("runtime %q not found in registry", runtimeName)
	}

	rc = rm.resolveEffectiveRuntimeConfig(runtimeName, rc)

	if rc.Mode == config.RuntimeModeSystem {
		if rc.System == nil {
			return resolvedRuntime{}, fmt.Errorf("runtime %q is system mode but has no system config", runtimeName)
		}
		return resolvedRuntime{binPath: rc.System.Command, rc: rc}, nil
	}

	if rc.Managed == nil {
		return resolvedRuntime{}, fmt.Errorf("runtime %q is managed mode but has no managed config", runtimeName)
	}

	osType, err := syslist.GetOsTypeFromString(runtime.GOOS)
	if err != nil {
		return resolvedRuntime{}, fmt.Errorf("failed to detect OS type: %w", err)
	}

	archType, err := syslist.GetArchTypeFromString(runtime.GOARCH)
	if err != nil {
		return resolvedRuntime{}, fmt.Errorf("failed to detect architecture type: %w", err)
	}

	libc := string(rm.hostTarget.Libc)

	configHash, err := calculateRuntimeHash(rc, osType, archType, libc)
	if err != nil {
		return resolvedRuntime{}, fmt.Errorf("failed to calculate runtime hash: %w", err)
	}

	binPath := env.GetRuntimeBinaryPath(runtimeName, configHash)

	archMap, ok := rc.Managed.Binaries[osType]
	if !ok {
		return resolvedRuntime{}, fmt.Errorf("runtime %q not available for OS %q", runtimeName, osType)
	}

	libcMap, ok := archMap[archType]
	if !ok {
		return resolvedRuntime{}, fmt.Errorf("runtime %q not available for arch %q on OS %q", runtimeName, archType, osType)
	}

	info, resolvedLibc := resolveLibcKey(libcMap, libc)
	if info == nil {
		return resolvedRuntime{}, fmt.Errorf("runtime %q not available for libc %q on %q/%q", runtimeName, libc, osType, archType)
	}

	if resolvedLibc != libc {
		log.Warn("runtime libc fallback, using incompatible binary (install system binary to enable auto-fallback)",
			zap.String("runtime", runtimeName),
			zap.String("requested", libc),
			zap.String("resolved", resolvedLibc),
		)
		configHash, err = calculateRuntimeHash(rc, osType, archType, resolvedLibc)
		if err != nil {
			return resolvedRuntime{}, fmt.Errorf("failed to calculate runtime hash with fallback libc: %w", err)
		}
		binPath = env.GetRuntimeBinaryPath(runtimeName, configHash)
	}

	if info.BinaryPath != nil {
		if err := validateRelativePath(*info.BinaryPath); err != nil {
			return resolvedRuntime{}, fmt.Errorf("runtime %q: unsafe binaryPath: %w", runtimeName, err)
		}
		binPath = filepath.Join(binPath, *info.BinaryPath)
	}

	return resolvedRuntime{
		binPath:    binPath,
		rc:         rc,
		configHash: configHash,
		binaryPath: info.BinaryPath,
		managed:    true,
	}, nil
}

func (rm *RuntimeManager) getRuntimePath(ctx context.Context, runtimeName string) (string, error) {
	if err := env.RejectInstallChild("runtime installation"); err != nil {
		return "", err
	}
	resolved, err := rm.computeRuntimeBinPath(runtimeName)
	if err != nil {
		return "", err
	}
	if !resolved.managed {
		return resolved.binPath, nil
	}
	rc, configHash, binPath := resolved.rc, resolved.configHash, resolved.binPath
	storeRoot := env.GetStorePath()
	runtimeRoot := env.GetRuntimeBinaryPath(runtimeName, configHash)
	leaseCtx, releaseLease, err := storelock.WithStore(ctx, storeRoot)
	if err != nil {
		return "", fmt.Errorf("acquire store lease: %w", err)
	}
	defer releaseLease()
	storeTarget, err := filepath.Rel(storeRoot, runtimeRoot)
	if err == nil {
		_, err = storelock.ValidateTarget(storeRoot, storeTarget)
	}
	if err != nil {
		return "", fmt.Errorf("resolve runtime store target: %w", err)
	}
	leaseCtx, targetRelease, err := installstate.Acquire(leaseCtx, storeRoot, storeTarget, "runtime "+runtimeName)
	if err != nil {
		return "", fmt.Errorf("lock runtime store target: %w", err)
	}
	defer targetRelease()
	if _, err := os.Stat(binPath); err == nil {
		log.Debug("runtime found in cache", zap.String("name", runtimeName), zap.String("path", binPath))
		return binPath, nil
	}
	if _, err := os.Stat(runtimeRoot); err == nil {
		if err := ForceRemoveAll(runtimeRoot); err != nil {
			return "", fmt.Errorf("remove incomplete runtime target %q: %w", runtimeRoot, err)
		}
	}
	if err := rm.downloadRuntime(leaseCtx, runtimeName, rc, configHash, resolved.binaryPath); err != nil {
		return "", fmt.Errorf("failed to install runtime %q: %w", runtimeName, err)
	}
	if _, err := os.Stat(binPath); err != nil {
		return "", fmt.Errorf("runtime binary not found at %q after download", binPath)
	}
	return binPath, nil
}

func (rm *RuntimeManager) downloadRuntime(ctx context.Context, runtimeName string, rc config.RuntimeConfig, configHash string, binaryPath *string) error {
	defer trace.Start(trace.CatInstall, "runtime.install").EndWith(trace.A("runtime", runtimeName))
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("runtime install context canceled before downloading %q: %w", runtimeName, err)
	}
	log.Debug("runtime not found in cache, downloading", zap.String("name", runtimeName))
	runtimeApp := binmanager.App{Required: true, Binary: &binmanager.AppConfigBinary{Binaries: rc.Managed.Binaries}}
	downloaderConstructions.Add(1)
	tmpBinManager := binmanager.New(binmanager.MapOfApps{runtimeName: runtimeApp}, nil, nil)
	if err := tmpBinManager.Install(ctx); err != nil {
		return fmt.Errorf("failed to download runtime %q: %w", runtimeName, err)
	}
	runtimeCachePath := env.GetRuntimeBinaryPath(runtimeName, configHash)
	return tmpBinManager.WithInstalledBinarySource(ctx, runtimeName, func(source string) error {
		return publishRuntimeCopy(source, runtimeCachePath, binaryPath)
	})
}
