package verifycache

import (
	"fmt"

	"github.com/datamitsu/datamitsu/internal/hashutil"
)

func fingerprintFields(fields ...string) string {
	parts := make([][]byte, len(fields))
	for i, f := range fields {
		parts[i] = []byte(f)
	}
	return hashutil.XXH3Multi(parts...)
}

func FingerprintBinary(url, hash, hashType, contentType, binaryPath string, extractDir bool, os, arch, libc string) string {
	return fingerprintFields("binary", url, hash, hashType, contentType, binaryPath, fmt.Sprintf("%t", extractDir), os, arch, libc)
}

func FingerprintRuntime(url, hash, hashType, contentType, binaryPath string, extractDir bool, os, arch, libc string) string {
	return fingerprintFields("runtime", url, hash, hashType, contentType, binaryPath, fmt.Sprintf("%t", extractDir), os, arch, libc)
}

func FingerprintRuntimeApp(appConfigJSON, runtimeConfigJSON, filesJSON, archivesJSON, os, arch string) string {
	return fingerprintFields("runtime-app", appConfigJSON, runtimeConfigJSON, filesJSON, archivesJSON, os, arch)
}

func FingerprintVersionCheck(appVersion, versionCheckArgs, os, arch, libc string) string {
	return fingerprintFields("version-check", appVersion, versionCheckArgs, os, arch, libc)
}

func BinaryEntryKey(appName, os, arch, libc string) string {
	return fmt.Sprintf("binary:%s:%s:%s:%s", appName, os, arch, libc)
}

func RuntimeEntryKey(runtimeName, os, arch, libc string) string {
	return fmt.Sprintf("runtime:%s:%s:%s:%s", runtimeName, os, arch, libc)
}

func RuntimeAppEntryKey(appName, os, arch string) string {
	return fmt.Sprintf("runtime-app:%s:%s:%s", appName, os, arch)
}

func VersionCheckEntryKey(appName, os, arch string) string {
	return fmt.Sprintf("version-check:%s:%s:%s", appName, os, arch)
}

func FingerprintBundle(version, filesJSON, archivesJSON string) string {
	return fingerprintFields("bundle", version, filesJSON, archivesJSON)
}

func BundleEntryKey(bundleName string) string {
	return fmt.Sprintf("bundle:%s", bundleName)
}
