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

	if rc.Kind == config.RuntimeKindFNM && rc.FNM != nil {
		parts = append(parts, []byte(rc.FNM.NodeVersion), []byte(rc.FNM.PNPMVersion), []byte(rc.FNM.PNPMHash))
	}
	if rc.Kind == config.RuntimeKindUV && rc.UV != nil {
		parts = append(parts, []byte(rc.UV.PythonVersion))
	}
	if rc.Kind == config.RuntimeKindJVM && rc.JVM != nil {
		parts = append(parts, []byte(rc.JVM.JavaVersion))
	}

	return hashutil.XXH3Multi(parts...), nil
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

	if rc.Kind == config.RuntimeKindFNM && rc.FNM != nil {
		parts = append(parts, []byte(rc.FNM.NodeVersion), []byte(rc.FNM.PNPMVersion), []byte(rc.FNM.PNPMHash))
	}
	if rc.Kind == config.RuntimeKindUV && rc.UV != nil {
		parts = append(parts, []byte(rc.UV.PythonVersion))
	}
	if rc.Kind == config.RuntimeKindJVM && rc.JVM != nil {
		parts = append(parts, []byte(rc.JVM.JavaVersion))
	}

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

func calculateFNMAppHash(appName string, packageName string, pkgVersion string, binPath string, deps map[string]string, runtimeHash string, lockHash string, filesHash string) string {
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
