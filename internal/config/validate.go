package config

import (
	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/target"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ValidateApps validates app configurations including mandatory lockfile checks.
func ValidateApps(apps binmanager.MapOfApps, runtimes MapOfRuntimes) ([]string, error) {
	return doValidateApps(apps, runtimes, false)
}

// ValidateAppsSkipLockfile is like ValidateApps but skips the lockfile check.
// Used by config lockfile to allow generating lockfiles for apps that don't have one yet.
func ValidateAppsSkipLockfile(apps binmanager.MapOfApps, runtimes MapOfRuntimes) ([]string, error) {
	return doValidateApps(apps, runtimes, true)
}

func doValidateApps(apps binmanager.MapOfApps, runtimes MapOfRuntimes, skipLockfileCheck bool) ([]string, error) {
	var errs []string
	var warnings []string

	linkOwners := make(map[string]string)

	appNames := make([]string, 0, len(apps))
	for name := range apps {
		appNames = append(appNames, name)
	}
	sort.Strings(appNames)

	for _, appName := range appNames {
		app := apps[appName]

		if (app.Binary != nil || app.Shell != nil || app.Jvm != nil) && (len(app.Files) > 0 || len(app.Links) > 0 || len(app.Archives) > 0) {
			errs = append(errs, fmt.Sprintf("app %q: files/links/archives are only supported on uv and fnm apps", appName))
			continue
		}

		if app.Binary != nil {
			for osType, archMap := range app.Binary.Binaries {
				for archType, libcMap := range archMap {
					for libc, info := range libcMap {
						platform := fmt.Sprintf("%s/%s/%s", osType, archType, libc)
						if !isValidLibcKey(libc) {
							errs = append(errs, fmt.Sprintf("app %q (%s): libc key %q is not valid; must be one of: glibc, musl, unknown", appName, platform, libc))
						}
						if info.URL == "" {
							errs = append(errs, fmt.Sprintf("app %q (%s): url is required", appName, platform))
						}
						if info.Hash == "" {
							errs = append(errs, fmt.Sprintf("app %q (%s): hash is required", appName, platform))
						} else if !isValidSHA256Hex(info.Hash) {
							errs = append(errs, fmt.Sprintf("app %q (%s): hash must be a valid SHA-256 hex string (64 lowercase hex characters)", appName, platform))
						}
						if info.HashType != nil && !binmanager.IsAllowedDownloadHashType(*info.HashType) {
							errs = append(errs, fmt.Sprintf("app %q (%s): hash type %q is not allowed for downloads; use sha256", appName, platform, *info.HashType))
						}
						if info.BinaryPath != nil {
							if err := validateSafeRelativePath(*info.BinaryPath, "binaryPath"); err != nil {
								errs = append(errs, fmt.Sprintf("app %q (%s): %v", appName, platform, err))
							}
						}
					}
				}
			}
		}

		if app.Jvm != nil {
			if app.Jvm.JarURL == "" {
				errs = append(errs, fmt.Sprintf("app %q: jvm.jarUrl is required", appName))
			}
			if app.Jvm.JarHash == "" {
				errs = append(errs, fmt.Sprintf("app %q: jvm.jarHash is required", appName))
			} else if !isValidSHA256Hex(app.Jvm.JarHash) {
				errs = append(errs, fmt.Sprintf("app %q: jvm.jarHash must be a valid SHA-256 hex string (64 lowercase hex characters)", appName))
			}
			if app.Jvm.Version == "" {
				errs = append(errs, fmt.Sprintf("app %q: jvm.version is required", appName))
			}
		}

		if app.Fnm != nil {
			if app.Fnm.BinPath == "" {
				errs = append(errs, fmt.Sprintf("app %q: fnm.binPath is required", appName))
			} else if err := validateSafeRelativePath(app.Fnm.BinPath, "binPath"); err != nil {
				errs = append(errs, fmt.Sprintf("app %q: %v", appName, err))
			}
		}

		if !skipLockfileCheck && app.Uv != nil && app.Uv.LockFile == "" {
			errs = append(errs, fmt.Sprintf("app %q: lockFile is required (run: datamitsu config lockfile %s)", appName, appName))
		}
		if !skipLockfileCheck && app.Fnm != nil && app.Fnm.LockFile == "" {
			errs = append(errs, fmt.Sprintf("app %q: lockFile is required (run: datamitsu config lockfile %s)", appName, appName))
		}

		if runtimes != nil {
			if app.Uv != nil {
				if refErr := validateAppRuntimeRef(app.Uv.Runtime, RuntimeKindUV, appName, runtimes); refErr != nil {
					errs = append(errs, refErr.Error())
				}
			}
			if app.Fnm != nil {
				if refErr := validateAppRuntimeRef(app.Fnm.Runtime, RuntimeKindFNM, appName, runtimes); refErr != nil {
					errs = append(errs, refErr.Error())
				}
			}
			if app.Jvm != nil {
				if refErr := validateAppRuntimeRef(app.Jvm.Runtime, RuntimeKindJVM, appName, runtimes); refErr != nil {
					errs = append(errs, refErr.Error())
				}
			}
		}

		for fileKey := range app.Files {
			if fileKey == "" {
				errs = append(errs, fmt.Sprintf("app %q: file key must not be empty", appName))
			} else if filepath.IsAbs(fileKey) || strings.Contains(fileKey, "..") {
				errs = append(errs, fmt.Sprintf("app %q: file key %q contains unsafe path components", appName, fileKey))
			}
		}

		for archiveName, archiveSpec := range app.Archives {
			if archiveName == "" {
				errs = append(errs, fmt.Sprintf("app %q: archive name must not be empty", appName))
				continue
			}
			if filepath.IsAbs(archiveName) || strings.Contains(archiveName, "..") {
				errs = append(errs, fmt.Sprintf("app %q: archive name %q contains unsafe path components", appName, archiveName))
				continue
			}

			if archiveSpec == nil {
				errs = append(errs, fmt.Sprintf("app %q: archive %q is nil", appName, archiveName))
				continue
			}

			isInline := archiveSpec.Inline != ""
			isExternal := archiveSpec.URL != ""

			if !isInline && !isExternal {
				errs = append(errs, fmt.Sprintf("app %q: archive %q must have either inline or url field set", appName, archiveName))
				continue
			}

			if isInline && isExternal {
				errs = append(errs, fmt.Sprintf("app %q: archive %q cannot have both inline and url fields set", appName, archiveName))
				continue
			}

			if isInline {
				if !strings.HasPrefix(archiveSpec.Inline, "tar.br:") {
					errs = append(errs, fmt.Sprintf("app %q: archive %q inline field must have 'tar.br:' prefix", appName, archiveName))
				} else if _, err := binmanager.DecompressArchive(archiveSpec.Inline); err != nil {
					errs = append(errs, fmt.Sprintf("app %q: archive %q inline content is invalid: %v", appName, archiveName, err))
				}
			}

			if isExternal {
				if archiveSpec.Hash == "" {
					errs = append(errs, fmt.Sprintf("app %q: archive %q hash is required for external archives (SHA-256)", appName, archiveName))
				} else if !isValidSHA256Hex(archiveSpec.Hash) {
					errs = append(errs, fmt.Sprintf("app %q: archive %q hash must be a valid SHA-256 hex string (64 lowercase hex characters)", appName, archiveName))
				}
				if archiveSpec.Format == "" {
					errs = append(errs, fmt.Sprintf("app %q: archive %q format is required for external archives", appName, archiveName))
				} else {
					validFormats := map[binmanager.BinContentType]bool{
						binmanager.BinContentTypeTar:    true,
						binmanager.BinContentTypeTarGz:  true,
						binmanager.BinContentTypeTarXz:  true,
						binmanager.BinContentTypeTarBz2: true,
						binmanager.BinContentTypeTarZst: true,
					}
					if !validFormats[archiveSpec.Format] {
						errs = append(errs, fmt.Sprintf("app %q: archive %q format must be one of: tar, tar.gz, tar.xz, tar.bz2, tar.zst", appName, archiveName))
					}
				}
			}
		}

		for linkName, linkPath := range app.Links {
			if linkName == "" {
				errs = append(errs, fmt.Sprintf("app %q: link name must not be empty", appName))
				continue
			}
			cleanedLinkName := filepath.Clean(linkName)
			if cleanedLinkName == ".gitignore" || cleanedLinkName == "datamitsu.config.d.ts" {
				errs = append(errs, fmt.Sprintf("app %q: link name %q is reserved for internal use", appName, linkName))
				continue
			}
			if filepath.IsAbs(linkName) || strings.Contains(linkName, "..") {
				errs = append(errs, fmt.Sprintf("app %q: link name %q contains unsafe path components", appName, linkName))
				continue
			}
			if linkPath == "" {
				errs = append(errs, fmt.Sprintf("app %q: links[%q] path must not be empty", appName, linkName))
				continue
			}
			if err := validateSafeRelativePath(linkPath, fmt.Sprintf("links[%q]", linkName)); err != nil {
				errs = append(errs, fmt.Sprintf("app %q: %v", appName, err))
			}
		}

		for linkName := range app.Links {
			normalizedLink := filepath.Clean(linkName)
			if existingApp, ok := linkOwners[normalizedLink]; ok {
				errs = append(errs, fmt.Sprintf("link name %q defined in both %q and %q", linkName, existingApp, appName))
			} else {
				linkOwners[normalizedLink] = appName
			}
		}
	}

	if runtimes != nil {
		runtimeNames := make([]string, 0, len(runtimes))
		for name := range runtimes {
			runtimeNames = append(runtimeNames, name)
		}
		sort.Strings(runtimeNames)
		for _, name := range runtimeNames {
			rc := runtimes[name]
			if rc.Kind == RuntimeKindUV && rc.Mode == RuntimeModeSystem && (rc.UV == nil || rc.UV.PythonVersion == "") {
				warnings = append(warnings, fmt.Sprintf("runtime %q: UV system mode without pythonVersion set; system Python version changes won't invalidate cache. Consider setting system.systemVersion for manual cache invalidation", name))
			}
		}
	}

	if len(errs) > 0 {
		return warnings, fmt.Errorf("config validation failed:\n  %s", strings.Join(errs, "\n  "))
	}

	return warnings, nil
}

func validateAppRuntimeRef(ref string, expectedKind RuntimeKind, appName string, runtimes MapOfRuntimes) error {
	if ref == "" {
		return nil
	}
	rc, ok := runtimes[ref]
	if !ok {
		return fmt.Errorf("app %q: references unknown runtime %q", appName, ref)
	}
	if rc.Kind != expectedKind {
		return fmt.Errorf("app %q: runtime %q is kind %q, expected %q", appName, ref, rc.Kind, expectedKind)
	}
	return nil
}

var safeVersionPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._+-]*$`)

func isValidVersionString(s string) bool {
	if s == "" {
		return false
	}
	if strings.Contains(s, "..") || strings.Contains(s, "/") || strings.Contains(s, "\\") {
		return false
	}
	return safeVersionPattern.MatchString(s)
}

func validateSafeRelativePath(p string, fieldName string) error {
	if p == "" {
		return fmt.Errorf("%s must not be empty", fieldName)
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("%s %q must be a relative path", fieldName, p)
	}
	cleaned := filepath.Clean(p)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s %q escapes parent directory", fieldName, p)
	}
	return nil
}

func isValidLibcKey(key string) bool {
	switch key {
	case string(target.LibcGlibc), string(target.LibcMusl), string(target.LibcUnknown):
		return true
	default:
		return false
	}
}

func isValidSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	if s != strings.ToLower(s) {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// ValidateInit validates init configuration entries.
func ValidateInit(initConfigs MapOfConfigInit) error {
	var errs []string

	names := make([]string, 0, len(initConfigs))
	for name := range initConfigs {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		cfg := initConfigs[name]
		if cfg.Scope != "" && cfg.Scope != ScopeProject && cfg.Scope != ScopeGitRoot {
			errs = append(errs, fmt.Sprintf("init %q: scope must be %q, %q, or empty, got %q", name, ScopeProject, ScopeGitRoot, cfg.Scope))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  %s", strings.Join(errs, "\n  "))
	}

	return nil
}

// ValidateBundles validates bundle configurations.
// It checks that each bundle has content (files or archives), link paths are safe,
// and link names are unique across both apps and bundles.
func ValidateBundles(bundles binmanager.MapOfBundles, apps binmanager.MapOfApps) error {
	if len(bundles) == 0 {
		return nil
	}

	var errs []string

	// Collect link names from apps first for cross-type uniqueness check.
	// Normalize with filepath.Clean to match how CreateDatamitsuLinks resolves them.
	linkOwners := make(map[string]string)
	appNames := make([]string, 0, len(apps))
	for name := range apps {
		appNames = append(appNames, name)
	}
	sort.Strings(appNames)
	for _, appName := range appNames {
		for linkName := range apps[appName].Links {
			linkOwners[filepath.Clean(linkName)] = "app:" + appName
		}
	}

	bundleNames := make([]string, 0, len(bundles))
	for name := range bundles {
		bundleNames = append(bundleNames, name)
	}
	sort.Strings(bundleNames)

	for _, name := range bundleNames {
		if name == "" {
			errs = append(errs, "bundle name must not be empty")
			continue
		}
		cleanedName := filepath.Clean(name)
		if filepath.IsAbs(cleanedName) || cleanedName == "." || cleanedName == ".." || strings.HasPrefix(cleanedName, ".."+string(filepath.Separator)) || strings.Contains(name, "/") || strings.Contains(name, "\\") {
			errs = append(errs, fmt.Sprintf("bundle name %q contains unsafe path components", name))
			continue
		}

		bundle := bundles[name]
		if bundle == nil {
			errs = append(errs, fmt.Sprintf("bundle %q: is nil", name))
			continue
		}

		if len(bundle.Files) == 0 && len(bundle.Archives) == 0 {
			errs = append(errs, fmt.Sprintf("bundle %q: must have at least files or archives", name))
		}

		for fileKey := range bundle.Files {
			if fileKey == "" {
				errs = append(errs, fmt.Sprintf("bundle %q: file key must not be empty", name))
			} else if filepath.IsAbs(fileKey) || strings.Contains(fileKey, "..") {
				errs = append(errs, fmt.Sprintf("bundle %q: file key %q contains unsafe path components", name, fileKey))
			}
		}

		for archiveName, archiveSpec := range bundle.Archives {
			if archiveName == "" {
				errs = append(errs, fmt.Sprintf("bundle %q: archive name must not be empty", name))
				continue
			}
			if filepath.IsAbs(archiveName) || strings.Contains(archiveName, "..") {
				errs = append(errs, fmt.Sprintf("bundle %q: archive name %q contains unsafe path components", name, archiveName))
				continue
			}

			if archiveSpec == nil {
				errs = append(errs, fmt.Sprintf("bundle %q: archive %q is nil", name, archiveName))
				continue
			}

			isInline := archiveSpec.Inline != ""
			isExternal := archiveSpec.URL != ""

			if !isInline && !isExternal {
				errs = append(errs, fmt.Sprintf("bundle %q: archive %q must have either inline or url field set", name, archiveName))
				continue
			}

			if isInline && isExternal {
				errs = append(errs, fmt.Sprintf("bundle %q: archive %q cannot have both inline and url fields set", name, archiveName))
				continue
			}

			if isInline {
				if !strings.HasPrefix(archiveSpec.Inline, "tar.br:") {
					errs = append(errs, fmt.Sprintf("bundle %q: archive %q inline field must have 'tar.br:' prefix", name, archiveName))
				} else if _, err := binmanager.DecompressArchive(archiveSpec.Inline); err != nil {
					errs = append(errs, fmt.Sprintf("bundle %q: archive %q inline content is invalid: %v", name, archiveName, err))
				}
			}

			if isExternal {
				if archiveSpec.Hash == "" {
					errs = append(errs, fmt.Sprintf("bundle %q: archive %q hash is required for external archives (SHA-256)", name, archiveName))
				} else if !isValidSHA256Hex(archiveSpec.Hash) {
					errs = append(errs, fmt.Sprintf("bundle %q: archive %q hash must be a valid SHA-256 hex string (64 lowercase hex characters)", name, archiveName))
				}
				if archiveSpec.Format == "" {
					errs = append(errs, fmt.Sprintf("bundle %q: archive %q format is required for external archives", name, archiveName))
				} else {
					validFormats := map[binmanager.BinContentType]bool{
						binmanager.BinContentTypeTar:    true,
						binmanager.BinContentTypeTarGz:  true,
						binmanager.BinContentTypeTarXz:  true,
						binmanager.BinContentTypeTarBz2: true,
						binmanager.BinContentTypeTarZst: true,
					}
					if !validFormats[archiveSpec.Format] {
						errs = append(errs, fmt.Sprintf("bundle %q: archive %q format must be one of: tar, tar.gz, tar.xz, tar.bz2, tar.zst", name, archiveName))
					}
				}
			}
		}

		for linkName, linkPath := range bundle.Links {
			if linkName == "" {
				errs = append(errs, fmt.Sprintf("bundle %q: link name must not be empty", name))
				continue
			}
			cleanedLinkName := filepath.Clean(linkName)
			if cleanedLinkName == ".gitignore" || cleanedLinkName == "datamitsu.config.d.ts" {
				errs = append(errs, fmt.Sprintf("bundle %q: link name %q is reserved for internal use", name, linkName))
				continue
			}
			if filepath.IsAbs(linkName) || strings.Contains(linkName, "..") {
				errs = append(errs, fmt.Sprintf("bundle %q: link name %q contains unsafe path components", name, linkName))
				continue
			}
			if linkPath == "" {
				errs = append(errs, fmt.Sprintf("bundle %q: links[%q] path must not be empty", name, linkName))
				continue
			}
			if err := validateSafeRelativePath(linkPath, fmt.Sprintf("links[%q]", linkName)); err != nil {
				errs = append(errs, fmt.Sprintf("bundle %q: %v", name, err))
			}
		}

		for linkName := range bundle.Links {
			normalizedLink := filepath.Clean(linkName)
			if existingOwner, ok := linkOwners[normalizedLink]; ok {
				errs = append(errs, fmt.Sprintf("link name %q defined in both %q and bundle %q", linkName, existingOwner, name))
			} else {
				linkOwners[normalizedLink] = "bundle:" + name
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  %s", strings.Join(errs, "\n  "))
	}

	return nil
}

func ValidateRuntimes(runtimes MapOfRuntimes) error {
	var errs []string

	names := make([]string, 0, len(runtimes))
	for name := range runtimes {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		rc := runtimes[name]
		if rc.Kind == RuntimeKindFNM {
			if rc.FNM == nil {
				errs = append(errs, fmt.Sprintf("runtime %q: FNM runtime requires fnm config with nodeVersion, pnpmVersion, and pnpmHash", name))
			} else {
				if rc.FNM.NodeVersion == "" {
					errs = append(errs, fmt.Sprintf("runtime %q: fnm.nodeVersion is required", name))
				} else if !isValidVersionString(rc.FNM.NodeVersion) {
					errs = append(errs, fmt.Sprintf("runtime %q: fnm.nodeVersion %q contains invalid characters (must be alphanumeric, dots, hyphens, underscores, or plus signs)", name, rc.FNM.NodeVersion))
				}
				if rc.FNM.PNPMVersion == "" {
					errs = append(errs, fmt.Sprintf("runtime %q: fnm.pnpmVersion is required", name))
				} else if !isValidVersionString(rc.FNM.PNPMVersion) {
					errs = append(errs, fmt.Sprintf("runtime %q: fnm.pnpmVersion %q contains invalid characters (must be alphanumeric, dots, hyphens, underscores, or plus signs)", name, rc.FNM.PNPMVersion))
				}
				if rc.FNM.PNPMHash == "" {
					errs = append(errs, fmt.Sprintf("runtime %q: fnm.pnpmHash is required (SHA-256 hash of PNPM tarball)", name))
				} else if !isValidSHA256Hex(rc.FNM.PNPMHash) {
					errs = append(errs, fmt.Sprintf("runtime %q: fnm.pnpmHash must be a valid SHA-256 hex string (64 lowercase hex characters)", name))
				}
			}
		}
		if rc.Kind == RuntimeKindJVM {
			if rc.JVM == nil {
				errs = append(errs, fmt.Sprintf("runtime %q: JVM runtime requires jvm config with javaVersion", name))
			} else {
				if rc.JVM.JavaVersion == "" {
					errs = append(errs, fmt.Sprintf("runtime %q: jvm.javaVersion is required", name))
				} else if !isValidVersionString(rc.JVM.JavaVersion) {
					errs = append(errs, fmt.Sprintf("runtime %q: jvm.javaVersion %q contains invalid characters (must be alphanumeric, dots, hyphens, underscores, or plus signs)", name, rc.JVM.JavaVersion))
				}
			}
		}
		if rc.Mode == RuntimeModeManaged {
			if rc.Managed == nil {
				errs = append(errs, fmt.Sprintf("runtime %q: managed mode requires managed config with binaries", name))
			} else {
				for osType, archMap := range rc.Managed.Binaries {
					for archType, libcMap := range archMap {
						for libc, info := range libcMap {
							platform := fmt.Sprintf("%s/%s/%s", osType, archType, libc)
							if !isValidLibcKey(libc) {
								errs = append(errs, fmt.Sprintf("runtime %q (%s): libc key %q is not valid; must be one of: glibc, musl, unknown", name, platform, libc))
							}
							if info.URL == "" {
								errs = append(errs, fmt.Sprintf("runtime %q (%s): url is required", name, platform))
							}
							if info.Hash == "" {
								errs = append(errs, fmt.Sprintf("runtime %q (%s): hash is required", name, platform))
							} else if !isValidSHA256Hex(info.Hash) {
								errs = append(errs, fmt.Sprintf("runtime %q (%s): hash must be a valid SHA-256 hex string (64 lowercase hex characters)", name, platform))
							}
							if info.HashType != nil && !binmanager.IsAllowedDownloadHashType(*info.HashType) {
								errs = append(errs, fmt.Sprintf("runtime %q (%s): hash type %q is not allowed for downloads; use sha256", name, platform, *info.HashType))
							}
							if info.BinaryPath != nil {
								if err := validateSafeRelativePath(*info.BinaryPath, "binaryPath"); err != nil {
									errs = append(errs, fmt.Sprintf("runtime %q (%s): %v", name, platform, err))
								}
							}
						}
					}
				}
			}
		}
		if rc.Mode == RuntimeModeSystem && rc.System == nil {
			errs = append(errs, fmt.Sprintf("runtime %q: system mode requires system config with command", name))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  %s", strings.Join(errs, "\n  "))
	}

	return nil
}
