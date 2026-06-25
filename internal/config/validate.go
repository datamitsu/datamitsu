package config

import (
	"encoding/hex"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/target"
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

		if (app.Binary != nil || app.Shell != nil || app.Jvm != nil || app.Go != nil) && (len(app.Files) > 0 || len(app.Links) > 0 || len(app.Archives) > 0) {
			errs = append(errs, fmt.Sprintf("app %q: files/links/archives are only supported on uv and node apps", appName))
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

		if app.Node != nil {
			if app.Node.BinPath == "" {
				errs = append(errs, fmt.Sprintf("app %q: node.binPath is required", appName))
			} else if err := validateSafeRelativePath(app.Node.BinPath, "binPath"); err != nil {
				errs = append(errs, fmt.Sprintf("app %q: %v", appName, err))
			}
		}

		if app.Go != nil {
			// packageName goes verbatim into `go get pkg@version`, so reject
			// path traversal and any character outside the safe set.
			switch {
			case app.Go.PackageName == "":
				errs = append(errs, fmt.Sprintf("app %q: go.packageName is required", appName))
			case strings.Contains(app.Go.PackageName, ".."):
				errs = append(errs, fmt.Sprintf("app %q: go.packageName %q must not contain %q", appName, app.Go.PackageName, ".."))
			case !safeGoPackagePattern.MatchString(app.Go.PackageName):
				errs = append(errs, fmt.Sprintf("app %q: go.packageName %q contains invalid characters (must be alphanumeric, dots, slashes, hyphens, or underscores)", appName, app.Go.PackageName))
			default:
				if base := path.Base(app.Go.PackageName); base == "." || base == ".." {
					errs = append(errs, fmt.Sprintf("app %q: go.packageName %q must end in a valid path element", appName, app.Go.PackageName))
				}
			}
			// version is the pinned `go get` query; a floating "latest" defeats
			// reproducible lockfile generation, so require a concrete version.
			switch {
			case app.Go.Version == "":
				errs = append(errs, fmt.Sprintf("app %q: go.version is required", appName))
			case strings.EqualFold(app.Go.Version, "latest"):
				errs = append(errs, fmt.Sprintf("app %q: go.version must be a pinned version, not %q", appName, app.Go.Version))
			case !isValidVersionString(app.Go.Version):
				errs = append(errs, fmt.Sprintf("app %q: go.version %q contains invalid characters (must be alphanumeric, dots, hyphens, underscores, or plus signs)", appName, app.Go.Version))
			}
		}

		if !skipLockfileCheck && app.Uv != nil && app.Uv.LockFile == "" {
			errs = append(errs, fmt.Sprintf("app %q: lockFile is required (run: datamitsu config lockfile %s)", appName, appName))
		}
		if !skipLockfileCheck && app.Node != nil && app.Node.LockFile == "" {
			errs = append(errs, fmt.Sprintf("app %q: lockFile is required (run: datamitsu config lockfile %s)", appName, appName))
		}
		if !skipLockfileCheck && app.Go != nil && app.Go.LockFile == "" {
			errs = append(errs, fmt.Sprintf("app %q: lockFile is required (run: datamitsu config lockfile %s)", appName, appName))
		}

		if runtimes != nil {
			if app.Uv != nil {
				if refErr := validateAppRuntimeRef(app.Uv.Runtime, RuntimeKindUV, appName, runtimes); refErr != nil {
					errs = append(errs, refErr.Error())
				}
			}
			if app.Node != nil {
				if refErr := validateAppRuntimeRef(app.Node.Runtime, RuntimeKindNode, appName, runtimes); refErr != nil {
					errs = append(errs, refErr.Error())
				}
			}
			if app.Jvm != nil {
				if refErr := validateAppRuntimeRef(app.Jvm.Runtime, RuntimeKindJVM, appName, runtimes); refErr != nil {
					errs = append(errs, refErr.Error())
				}
			}
			if app.Go != nil {
				if refErr := validateAppRuntimeRef(app.Go.Runtime, RuntimeKindGo, appName, runtimes); refErr != nil {
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
			if rc.Kind == RuntimeKindGo && rc.Mode == RuntimeModeSystem && (rc.Go == nil || rc.Go.GoVersion == "") {
				warnings = append(warnings, fmt.Sprintf("runtime %q: Go system mode without goVersion set; system Go version changes won't invalidate cache. Consider setting system.systemVersion for manual cache invalidation", name))
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

// safeGoPackagePattern matches a Go import path: it must start with an
// alphanumeric character and may contain dots, slashes, hyphens, and
// underscores. It deliberately excludes characters that could be used for
// shell or argument injection when the package path is passed to `go get`.
var safeGoPackagePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9./_-]*$`)

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

// ociRefPattern matches a repository reference: a registry host (optionally
// with a port) followed by at least one path component. The character set
// excludes ":" outside the port position and "@" entirely, so a ref carrying
// a ":tag" or "@digest" suffix cannot match — content is pinned exclusively
// by OCIRef.Digest.
var ociRefPattern = regexp.MustCompile(`^[a-z0-9.-]+(:[0-9]+)?(/[a-z0-9._-]+)+$`)

// ValidateOCI validates the top-level oci bundle declaration. A nil ref is
// valid (no bundle configured).
func ValidateOCI(ref *OCIRef) error {
	if ref == nil {
		return nil
	}

	var errs []string

	switch {
	case ref.Ref == "":
		errs = append(errs, "oci: ref is required (full reference including registry host, e.g. ghcr.io/owner/repo)")
	case !ociRefPattern.MatchString(ref.Ref):
		errs = append(errs, fmt.Sprintf("oci: ref %q is not a valid repository reference (expected host[:port]/path, lowercase, no tag and no digest)", ref.Ref))
	default:
		// Require an explicit registry host: the first segment must look like a
		// hostname (contain a dot or a port) or be "localhost". There is no
		// default host and no docker.io magic.
		host := ref.Ref[:strings.IndexByte(ref.Ref, '/')]
		if host != "localhost" && !strings.Contains(host, ".") && !strings.Contains(host, ":") {
			errs = append(errs, fmt.Sprintf("oci: ref %q must include the registry host as its first segment (e.g. ghcr.io/owner/repo)", ref.Ref))
		}
	}

	const digestPrefix = "sha256:"
	switch {
	case ref.Digest == "":
		errs = append(errs, "oci: digest is required (sha256:<64 hex>)")
	case !strings.HasPrefix(ref.Digest, digestPrefix) || !isValidSHA256Hex(strings.TrimPrefix(ref.Digest, digestPrefix)):
		errs = append(errs, fmt.Sprintf("oci: digest %q must be \"sha256:\" followed by 64 lowercase hex characters", ref.Digest))
	}

	if ref.Signer != nil {
		if ref.Signer.Identity == "" {
			errs = append(errs, "oci: signer.identity is required when signer is set")
		}
		if ref.Signer.Issuer == "" {
			errs = append(errs, "oci: signer.issuer is required when signer is set")
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}

// ValidateParsers validates the parsers map: each entry must have a non-empty
// url and a non-empty, well-formed SHA-256 hash (64 lowercase hex). An empty
// hash is a hard error per the security policy (mirrors the bundle/archive
// hash-mandatory rule) — never a download in "hash-less" mode.
func ValidateParsers(parsers MapOfParsers) error {
	if len(parsers) == 0 {
		return nil
	}

	names := make([]string, 0, len(parsers))
	for name := range parsers {
		names = append(names, name)
	}
	sort.Strings(names)

	var errs []string
	for _, name := range names {
		if name == "" {
			errs = append(errs, "parser name must not be empty")
			continue
		}
		p := parsers[name]
		if p.URL == "" {
			errs = append(errs, fmt.Sprintf("parser %q: url is required", name))
		}
		if p.Hash == "" {
			errs = append(errs, fmt.Sprintf("parser %q: hash is required (SHA-256)", name))
		} else if !isValidSHA256Hex(p.Hash) {
			errs = append(errs, fmt.Sprintf("parser %q: hash must be a valid SHA-256 hex string (64 lowercase hex characters)", name))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
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

// ValidateSetup validates init configuration entries.
func ValidateSetup(initConfigs MapOfConfigSetup) error {
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

// ValidateSetupToolRefs returns warnings for any ConfigSetup.Tools entry that does
// not reference a configured tool. Such a config can never be selected via
// `setup --tools` (the name won't intersect any selection), so it would be
// silently excluded — almost always an authoring typo. This mirrors the
// unknown-tool warning emitted for .datamitsuignore rules. It warns rather than
// errors so a config that conditionally omits a tool in some environment still
// loads.
func ValidateSetupToolRefs(initConfigs MapOfConfigSetup, tools MapOfTools) []string {
	var warnings []string

	names := make([]string, 0, len(initConfigs))
	for name := range initConfigs {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		for _, toolName := range initConfigs[name].Tools {
			if _, ok := tools[toolName]; !ok {
				warnings = append(warnings, fmt.Sprintf(
					"init %q: tools references unknown tool %q (it will never match `setup --tools %s`)",
					name, toolName, toolName,
				))
			}
		}
	}

	return warnings
}

// ToolArgPlaceholders and ToolEnvPlaceholders are the substitution placeholders
// datamitsu expands in tool-operation arguments and environment-variable values
// respectively (the actual expansion lives in internal/tooling's executor). They
// are the single source of truth for which {placeholder} tokens are valid;
// ValidateTools rejects any other token instead of passing it through unsubstituted.
var (
	ToolArgPlaceholders = []string{"file", "files", "root", "cwd", "toolCache"}
	ToolEnvPlaceholders = []string{"root", "cwd", "toolCache"}
)

// toolPlaceholderPattern matches a datamitsu-style placeholder: a brace-wrapped
// identifier. Brace groups containing commas/dots/spaces (shell globs like
// {js,ts}, Go templates like {{.X}}) don't match, and doubled braces are skipped
// by findUnknownPlaceholders, so only genuine placeholder-shaped tokens validate.
var toolPlaceholderPattern = regexp.MustCompile(`\{[A-Za-z][A-Za-z0-9_]*\}`)

func findUnknownPlaceholders(value string, allowed map[string]bool) []string {
	var unknown []string
	for _, loc := range toolPlaceholderPattern.FindAllStringIndex(value, -1) {
		start, end := loc[0], loc[1]
		// Skip doubled braces ({{...}}) — Go templates / shell brace groups.
		if start > 0 && value[start-1] == '{' {
			continue
		}
		if end < len(value) && value[end] == '}' {
			continue
		}
		name := value[start+1 : end-1]
		if !allowed[name] {
			unknown = append(unknown, "{"+name+"}")
		}
	}
	return unknown
}

func placeholderSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

func placeholderList(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = "{" + n + "}"
	}
	return strings.Join(quoted, ", ")
}

// ValidateTools fails fast when a tool argument or environment value uses a
// {placeholder} datamitsu does not substitute. Passing an unknown placeholder
// through unchanged silently breaks the tool (e.g. GOLANGCI_LINT_CACHE={toolcache}
// reaching golangci-lint as a literal non-absolute path), so it is a config error.
func ValidateTools(tools MapOfTools) error {
	argAllowed := placeholderSet(ToolArgPlaceholders)
	envAllowed := placeholderSet(ToolEnvPlaceholders)

	toolNames := make([]string, 0, len(tools))
	for name := range tools {
		toolNames = append(toolNames, name)
	}
	sort.Strings(toolNames)

	var errs []string
	for _, toolName := range toolNames {
		tool := tools[toolName]

		opTypes := make([]string, 0, len(tool.Operations))
		for opType := range tool.Operations {
			opTypes = append(opTypes, string(opType))
		}
		sort.Strings(opTypes)

		for _, opType := range opTypes {
			op := tool.Operations[OperationType(opType)]

			for _, arg := range op.Args {
				for _, ph := range findUnknownPlaceholders(arg, argAllowed) {
					errs = append(errs, fmt.Sprintf(
						"tool %q operation %q: unsupported placeholder %s in args (supported: %s)",
						toolName, opType, ph, placeholderList(ToolArgPlaceholders),
					))
				}
			}

			envKeys := make([]string, 0, len(op.Env))
			for key := range op.Env {
				envKeys = append(envKeys, key)
			}
			sort.Strings(envKeys)
			for _, key := range envKeys {
				for _, ph := range findUnknownPlaceholders(op.Env[key], envAllowed) {
					errs = append(errs, fmt.Sprintf(
						"tool %q operation %q: unsupported placeholder %s in env %q (supported: %s)",
						toolName, opType, ph, key, placeholderList(ToolEnvPlaceholders),
					))
				}
			}
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

// ValidateRuntimes checks each runtime entry's kind-specific and mode-specific
// configuration, returning a combined error describing all problems found.
func ValidateRuntimes(runtimes MapOfRuntimes) error {
	var errs []string

	names := make([]string, 0, len(runtimes))
	for name := range runtimes {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		rc := runtimes[name]
		// Kind-specific sub-config validation is registry-driven so the rules live
		// next to the kind they describe (see runtimekind.go). Unknown/legacy kinds
		// have no registry entry and are skipped here (mode checks below still run).
		if info, ok := LookupRuntimeKind(rc.Kind); ok && info.Validate != nil {
			errs = append(errs, info.Validate(name, rc)...)
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
