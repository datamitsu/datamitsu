package runtimemanager

import (
	"fmt"
	"sort"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/hashutil"
	"github.com/datamitsu/datamitsu/internal/syslist"
)

func lockFileHash(lockFile string) string {
	if lockFile == "" {
		return ""
	}
	return hashutil.XXH3Hex([]byte(lockFile))
}

// goAppLockHash binds the built package path into a Go app's lock hash. A Go
// app's build identity is (packageName, go.mod, go.sum): two apps that share a
// lockfile but build different packages from the same module must not collide
// on the same cache directory. The NUL separator keeps the two fields
// unambiguous.
func goAppLockHash(packageName, lockFile string) string {
	return hashutil.XXH3Hex([]byte(packageName + "\x00" + lockFile))
}

func calculateRuntimeHash(rc config.RuntimeConfig, osType syslist.OsType, archType syslist.ArchType, libc string) (string, error) {
	if rc.Mode != config.RuntimeModeManaged || rc.Managed == nil {
		return "", fmt.Errorf("cannot calculate hash for non-managed runtime")
	}

	archMap, ok := rc.Managed.Binaries[osType]
	if !ok {
		return "", fmt.Errorf("runtime not available for OS %q", osType)
	}

	libcMap, ok := archMap[archType]
	if !ok {
		return "", fmt.Errorf("runtime not available for arch %q on OS %q", archType, osType)
	}

	info, resolvedLibc := resolveLibcKey(libcMap, libc)
	if info == nil {
		return "", fmt.Errorf("runtime not available for libc %q on %q/%q", libc, osType, archType)
	}
	libc = resolvedLibc

	binaryPath := ""
	if info.BinaryPath != nil {
		binaryPath = *info.BinaryPath
	}
	extractDir := ""
	if info.ExtractDir {
		extractDir = "extractDir"
	}

	parts := [][]byte{
		[]byte(info.URL),
		[]byte(info.Hash),
		[]byte(info.ContentType),
		[]byte(binaryPath),
		[]byte(extractDir),
		[]byte(osType),
		[]byte(archType),
		[]byte(libc),
	}

	parts = appendKindVersionFields(parts, rc)

	return hashutil.XXH3Multi(parts...), nil
}

// appendKindVersionFields folds the cache-affecting version field(s) for rc's
// kind into parts. Both the managed and system hash functions route through this single
// helper (backed by the kind registry) so the two can never fold a different set
// of fields — the lock-step drift the registry was introduced to eliminate.
func appendKindVersionFields(parts [][]byte, rc config.RuntimeConfig) [][]byte {
	info, ok := config.LookupRuntimeKind(rc.Kind)
	if !ok || info.HashFields == nil {
		return parts
	}
	for _, field := range info.HashFields(rc) {
		parts = append(parts, []byte(field))
	}
	return parts
}

func calculateSystemRuntimeHash(rc config.RuntimeConfig) string {
	command := ""
	systemVersion := ""
	if rc.System != nil {
		command = rc.System.Command
		systemVersion = rc.System.SystemVersion
	}

	parts := [][]byte{
		[]byte("system"),
		[]byte(command),
		[]byte(systemVersion),
	}

	parts = appendKindVersionFields(parts, rc)

	return hashutil.XXH3Multi(parts...)
}

func calculateAppHash(appName string, version string, deps map[string]string, runtimeHash string, lockHash string, filesHash string) string {
	parts := [][]byte{
		[]byte(appName),
		[]byte(version),
		[]byte(runtimeHash),
		[]byte(lockHash),
		[]byte(filesHash),
	}

	if len(deps) > 0 {
		keys := make([]string, 0, len(deps))
		for k := range deps {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			parts = append(parts, []byte(k), []byte(deps[k]))
		}
	}

	return hashutil.XXH3Multi(parts...)
}

// calculateNodeAppHash hashes a node-kind npm app's identity. Node apps are
// pnpm-installed npm packages, so the cache key folds in the package name,
// version, binPath, dependencies, lockfile, files, and the runtime hash (which
// already includes the node kind's {url, hash, nodeVersion, ...}). The app-env
// path is additionally prefixed by kind ("node").
func calculateNodeAppHash(appName string, packageName string, pkgVersion string, binPath string, deps map[string]string, runtimeHash string, lockHash string, filesHash string) string {
	parts := [][]byte{
		[]byte(appName),
		[]byte(packageName),
		[]byte(pkgVersion),
		[]byte(binPath),
		[]byte(runtimeHash),
		[]byte(lockHash),
		[]byte(filesHash),
	}

	if len(deps) > 0 {
		keys := make([]string, 0, len(deps))
		for k := range deps {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			parts = append(parts, []byte(k), []byte(deps[k]))
		}
	}

	return hashutil.XXH3Multi(parts...)
}
