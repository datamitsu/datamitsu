package verifycache

import (
	"fmt"
	"strconv"

	"github.com/datamitsu/datamitsu/internal/hashutil"
)

func fingerprintFields(fields ...string) string {
	parts := make([][]byte, len(fields))
	for i, f := range fields {
		parts[i] = []byte(f)
	}
	return hashutil.XXH3Multi(parts...)
}

// FingerprintBinary returns the verification fingerprint for a managed binary.
func FingerprintBinary(url, hash, hashType, contentType, binaryPath string, extractDir bool, os, arch, libc string) string {
	return fingerprintFields("binary", url, hash, hashType, contentType, binaryPath, strconv.FormatBool(extractDir), os, arch, libc)
}

// FingerprintRuntime returns the verification fingerprint for a managed runtime.
func FingerprintRuntime(url, hash, hashType, contentType, binaryPath string, extractDir bool, os, arch, libc string) string {
	return fingerprintFields("runtime", url, hash, hashType, contentType, binaryPath, strconv.FormatBool(extractDir), os, arch, libc)
}

// FingerprintRuntimeApp returns the verification fingerprint for a runtime app.
func FingerprintRuntimeApp(appConfigJSON, runtimeConfigJSON, filesJSON, archivesJSON, os, arch string) string {
	return fingerprintFields("runtime-app", appConfigJSON, runtimeConfigJSON, filesJSON, archivesJSON, os, arch)
}

// FingerprintVersionCheck returns the verification fingerprint for a version check.
func FingerprintVersionCheck(appVersion, versionCheckArgs, os, arch, libc string) string {
	return fingerprintFields("version-check", appVersion, versionCheckArgs, os, arch, libc)
}

// BinaryEntryKey returns the state map key for a managed binary.
func BinaryEntryKey(appName, os, arch, libc string) string {
	return fmt.Sprintf("binary:%s:%s:%s:%s", appName, os, arch, libc)
}

// RuntimeEntryKey returns the state map key for a managed runtime.
func RuntimeEntryKey(runtimeName, os, arch, libc string) string {
	return fmt.Sprintf("runtime:%s:%s:%s:%s", runtimeName, os, arch, libc)
}

// RuntimeAppEntryKey returns the state map key for a runtime app.
func RuntimeAppEntryKey(appName, os, arch string) string {
	return fmt.Sprintf("runtime-app:%s:%s:%s", appName, os, arch)
}

// VersionCheckEntryKey returns the state map key for a version check.
func VersionCheckEntryKey(appName, os, arch string) string {
	return fmt.Sprintf("version-check:%s:%s:%s", appName, os, arch)
}

// FingerprintBundle returns the verification fingerprint for a bundle.
func FingerprintBundle(version, filesJSON, archivesJSON string) string {
	return fingerprintFields("bundle", version, filesJSON, archivesJSON)
}

// BundleEntryKey returns the state map key for a bundle.
func BundleEntryKey(bundleName string) string {
	return "bundle:" + bundleName
}
