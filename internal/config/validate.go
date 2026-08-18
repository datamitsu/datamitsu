package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/configcontract"
	"github.com/datamitsu/datamitsu/internal/ociref"
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

// ValidateOCI validates the top-level oci bundle declaration. A nil ref is
// valid (no bundle configured).
func ValidateOCI(ref *OCIRef) error {
	if ref == nil {
		return nil
	}

	var errs []string

	// The grammar lives in internal/ociref, which the seeder and the CLI parse
	// with too. The messages stay here: each surface frames a bad reference in
	// its own words, so ociref reports which rule broke and the caller words it.
	if ref.Ref == "" {
		errs = append(errs, "oci: ref is required (full reference including registry host, e.g. ghcr.io/owner/repo)")
	} else if _, _, err := ociref.Parse(ref.Ref); err != nil {
		switch {
		case errors.Is(err, ociref.ErrRefNoHost):
			errs = append(errs, fmt.Sprintf("oci: ref %q %s", ref.Ref, ociref.ErrRefNoHost))
		default:
			errs = append(errs, fmt.Sprintf("oci: ref %q is %s", ref.Ref, ociref.ErrRefSyntax))
		}
	}

	const digestPrefix = "sha256:"
	switch {
	case ref.Digest == "":
		errs = append(errs, "oci: digest is required (sha256:<64 hex>)")
	case !strings.HasPrefix(ref.Digest, digestPrefix) || !isValidSHA256Hex(strings.TrimPrefix(ref.Digest, digestPrefix)):
		errs = append(errs, fmt.Sprintf("oci: digest %q must be \"sha256:\" followed by 64 lowercase hex characters", ref.Digest))
	}

	// Rejected at load, not accepted-and-ignored. This build verifies no
	// signatures at all (there is no sigstore dependency), so a config that
	// pins a signer is asserting a guarantee the binary does not deliver — a
	// loud error is strictly better than silent non-verification. The seeder
	// rejects it too, but only once the network is already in play.
	if ref.Signer != nil {
		errs = append(errs, signerRejected("oci: signer", "the pinned digest still verifies the bundle bytes"))
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}

// signerRejected words the "this build cannot verify signatures" rejection the
// bundle and the parser surfaces share, so the two never drift apart. field is
// the key being rejected and stillVerified names what does guarantee the bytes,
// because the answer to "then what protects me?" belongs in the error itself.
func signerRejected(field, stillVerified string) string {
	return fmt.Sprintf("%s is set but signature verification is not implemented in this build; remove signer (%s)", field, stillVerified)
}

// ValidateParsers validates the parsers map: each entry must declare exactly
// one source (url or oci) and a non-empty, well-formed SHA-256 hash (64
// lowercase hex). The hash is mandatory for EVERY source — an empty hash is a
// hard error per the security policy (mirroring the bundle/archive
// hash-mandatory rule), never a download in "hash-less" mode.
//
// The two sources are mutually exclusive rather than a fallback chain: an
// air-gapped organization must be able to prove there is no github.com egress
// left, which a "try the registry, then the URL" declaration would quietly
// undo. Overriding a source is what config layers are for.
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
		switch {
		case p.URL == "" && p.OCI == nil:
			errs = append(errs, fmt.Sprintf("parser %q: exactly one of url or oci is required", name))
		case p.URL != "" && p.OCI != nil:
			errs = append(errs, fmt.Sprintf("parser %q: url and oci are mutually exclusive (declare one source; use a config layer to override)", name))
		}
		if p.Hash == "" {
			errs = append(errs, fmt.Sprintf("parser %q: hash is required (SHA-256)", name))
		} else if !isValidSHA256Hex(p.Hash) {
			errs = append(errs, fmt.Sprintf("parser %q: hash must be a valid SHA-256 hex string (64 lowercase hex characters)", name))
		}
		errs = append(errs, validateParserOCI(name, p.OCI)...)
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}

// validateParserOCI checks a parser's registry source. It mirrors ValidateOCI
// rule for rule — same grammar, same digest form, same signer rejection — so
// there is one reference vocabulary to learn regardless of which entity carries
// the pin; only the message prefix differs.
func validateParserOCI(name string, oci *ParserOCI) []string {
	if oci == nil {
		return nil
	}

	var errs []string
	if oci.Ref == "" {
		errs = append(errs, fmt.Sprintf("parser %q: oci.ref is required (full reference including registry host, e.g. ghcr.io/datamitsu/datamitsu-parsers)", name))
	} else if _, _, err := ociref.Parse(oci.Ref); err != nil {
		switch {
		case errors.Is(err, ociref.ErrRefNoHost):
			errs = append(errs, fmt.Sprintf("parser %q: oci.ref %q %s", name, oci.Ref, ociref.ErrRefNoHost))
		default:
			errs = append(errs, fmt.Sprintf("parser %q: oci.ref %q is %s", name, oci.Ref, ociref.ErrRefSyntax))
		}
	}

	const digestPrefix = "sha256:"
	switch {
	case oci.Digest == "":
		errs = append(errs, fmt.Sprintf("parser %q: oci.digest is required (sha256:<64 hex>)", name))
	case !strings.HasPrefix(oci.Digest, digestPrefix) || !isValidSHA256Hex(strings.TrimPrefix(oci.Digest, digestPrefix)):
		errs = append(errs, fmt.Sprintf("parser %q: oci.digest %q must be \"sha256:\" followed by 64 lowercase hex characters", name, oci.Digest))
	}

	if oci.Signer != nil {
		errs = append(errs, fmt.Sprintf("parser %q: %s", name,
			signerRejected("oci.signer", "the mandatory hash still verifies the module bytes")))
	}
	return errs
}

// ValidateLsp performs minimal structural validation of the reserved lsp
// declarations (no runtime behavior in this release):
//   - type must be "proxy" or "derived";
//   - a proxy requires app + a non-empty projectTypes;
//   - a derived entry requires a tool that exists in tools.
//
// A dangling derived.tool reference is a load-time config error (follows the
// runtime-ref validation style).
func ValidateLsp(lsp MapOfLsp, tools MapOfTools) error {
	if len(lsp) == 0 {
		return nil
	}

	names := make([]string, 0, len(lsp))
	for name := range lsp {
		names = append(names, name)
	}
	sort.Strings(names)

	var errs []string
	for _, name := range names {
		if name == "" {
			errs = append(errs, "lsp entry name must not be empty")
			continue
		}
		entry := lsp[name]
		switch entry.Type {
		case LspTypeProxy:
			if entry.App == "" {
				errs = append(errs, fmt.Sprintf("lsp %q: proxy requires app", name))
			}
			if len(entry.ProjectTypes) == 0 {
				errs = append(errs, fmt.Sprintf("lsp %q: proxy requires a non-empty projectTypes", name))
			}
		case LspTypeDerived:
			if entry.Tool == "" {
				errs = append(errs, fmt.Sprintf("lsp %q: derived requires tool", name))
			} else if _, ok := tools[entry.Tool]; !ok {
				errs = append(errs, fmt.Sprintf("lsp %q: derived references unknown tool %q", name, entry.Tool))
			}
		case "":
			errs = append(errs, fmt.Sprintf("lsp %q: type is required (must be %q or %q)", name, LspTypeProxy, LspTypeDerived))
		default:
			errs = append(errs, fmt.Sprintf("lsp %q: type %q is invalid (must be %q or %q)", name, entry.Type, LspTypeProxy, LspTypeDerived))
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

// ValidateToolDeprecations returns one warning per tool operation still setting
// a deprecated field. It warns rather than erroring so that every config that
// loads today keeps loading: the rejection lands a full release later, once the
// replacement has shipped and configs have had a release in which to migrate.
//
// `batch` is deprecated in favour of `arity`. The two answer different
// questions — `batch` is derived from `scope` ("where does the process start"),
// while argv shape is a property of the tool's command-line contract — and one
// boolean cannot express the four shapes that exist (a list of paths, exactly
// one path, one directory, no paths at all). Until the `arity` capability is
// published, `batch` is still honoured; after it, the field is ignored for
// dispatch and eventually rejected.
//
// Warnings are emitted in a deterministic order so callers can print them
// without reordering.
func ValidateToolDeprecations(tools MapOfTools) []string {
	toolNames := make([]string, 0, len(tools))
	for name := range tools {
		toolNames = append(toolNames, name)
	}
	sort.Strings(toolNames)

	var warnings []string
	for _, toolName := range toolNames {
		tool := tools[toolName]

		opTypes := make([]string, 0, len(tool.Operations))
		for opType := range tool.Operations {
			opTypes = append(opTypes, string(opType))
		}
		sort.Strings(opTypes)

		for _, opType := range opTypes {
			op := tool.Operations[OperationType(opType)]
			if op.Batch == nil {
				continue
			}
			warnings = append(warnings, fmt.Sprintf(
				"tool %q operation %q: %q is deprecated and will be rejected in a future release; "+
					"argv shape comes from %q once this build reports the %q capability. "+
					"Remove the field — see docs/plans/2026-08-18-tool-invocation-granularity.md",
				toolName, opType, "batch", "arity", configcontract.CapArity,
			))
		}
	}

	return warnings
}

// ToolArgPlaceholders and ToolEnvPlaceholders are the substitution placeholders
// datamitsu expands in tool-operation arguments and environment-variable values
// respectively (the actual expansion lives in internal/tooling's executor). They
// are the single source of truth for which {placeholder} tokens are valid;
// ValidateTools rejects any other token instead of passing it through unsubstituted.
//
// The values live in internal/configcontract because facts() publishes them to
// config JavaScript, and a leaf package lets both sides read one definition
// rather than keeping two in sync. These names stay as the in-package spelling
// every validator already uses.
var (
	ToolArgPlaceholders = configcontract.ArgPlaceholders
	ToolEnvPlaceholders = configcontract.EnvPlaceholders
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
//
// It also validates the outputParser reference: when a tool sets outputParser, it
// must name an existing entry in parsers — a dangling reference is a load-time
// config error (there is no existing app-ref check to mirror; this follows the
// runtime-ref validation style).
func ValidateTools(tools MapOfTools, parsers MapOfParsers) error {
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

		if tool.OutputParser != nil {
			// Validate the module reference (which `parsers` entry to load); the
			// dispatch key inside the module can only be checked at runtime.
			if _, ok := parsers[tool.OutputParser.Module]; !ok {
				errs = append(errs, fmt.Sprintf(
					"tool %q: outputParser references unknown parsers module %q",
					toolName, tool.OutputParser.Module,
				))
			}
		}

		opTypes := make([]string, 0, len(tool.Operations))
		for opType := range tool.Operations {
			opTypes = append(opTypes, string(opType))
		}
		sort.Strings(opTypes)

		for _, opType := range opTypes {
			op := tool.Operations[OperationType(opType)]

			switch op.Input {
			case "", ToolInputFile, ToolInputStdin:
			default:
				errs = append(errs, fmt.Sprintf(
					"tool %q operation %q: invalid input mode %q (supported: %q, %q)",
					toolName, opType, op.Input, ToolInputFile, ToolInputStdin,
				))
			}

			switch op.Output {
			case "", ToolOutputInplace, ToolOutputStdout:
			default:
				errs = append(errs, fmt.Sprintf(
					"tool %q operation %q: invalid output mode %q (supported: %q, %q)",
					toolName, opType, op.Output, ToolOutputInplace, ToolOutputStdout,
				))
			}

			// The stdin/stdout formatter contract is only honored by the per-file
			// execution path. Under any batched scope the executor combines output
			// and feeds no stdin, so these modes would silently no-op. Require
			// scope:per-file (and reject an explicit batch:true) so a misconfigured
			// formatter fails fast instead of doing nothing.
			if op.Input == ToolInputStdin || op.Output == ToolOutputStdout {
				if op.Scope != ToolScopePerFile {
					errs = append(errs, fmt.Sprintf(
						"tool %q operation %q: input %q / output %q require scope %q (got %q)",
						toolName, opType, ToolInputStdin, ToolOutputStdout, ToolScopePerFile, op.Scope,
					))
				} else if arity := EffectiveArity(op); arity != ArityOne && arity != ArityNone {
					errs = append(errs, fmt.Sprintf(
						"tool %q operation %q: input %q / output %q need one file per process, but args infer arity %q",
						toolName, opType, ToolInputStdin, ToolOutputStdout, arity,
					))
				}
			}

			if op.Arity != "" {
				if inferred := EffectiveArity(op); op.Arity != inferred {
					errs = append(errs, fmt.Sprintf(
						"tool %q operation %q: declared arity %q but args infer %q; arity asserts the argv shape, it cannot override it",
						toolName, opType, op.Arity, inferred,
					))
				}
			}

			if ArgsReferenceTarget(op.Args) && ArgsReferenceFiles(op.Args) {
				errs = append(errs, fmt.Sprintf(
					"tool %q operation %q: args carry both %s and a file placeholder; a directory target and a file list are mutually exclusive",
					toolName, opType, placeholderTarget,
				))
			}

			// output:stdout drives the diff-in-core formatting path, which rewrites
			// the file on disk. Restrict it to the fix operation so a read-only
			// command (lint) can never silently mutate files via a misplaced mode.
			if op.Output == ToolOutputStdout && OperationType(opType) != OpFix {
				errs = append(errs, fmt.Sprintf(
					"tool %q operation %q: output %q rewrites files and is only valid on the %q operation",
					toolName, opType, ToolOutputStdout, OpFix,
				))
			}

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

// ValidateToolFacets fails when a tool declares no usable facet. After all
// config overlays are merged, every tool must expose at least one fix or lint
// operation (a future lsp binding will also count, but lsp is not yet wired in
// this release). A tool with no operations is silently skipped by the planner
// (collectTasks drops tools missing the requested op), so an empty tool is
// almost always an authoring mistake — surface it at load time instead.
//
// This is a brand-new check: there is no existing empty-operations validation to
// mirror.
func ValidateToolFacets(tools MapOfTools) error {
	toolNames := make([]string, 0, len(tools))
	for name := range tools {
		toolNames = append(toolNames, name)
	}
	sort.Strings(toolNames)

	var errs []string
	for _, toolName := range toolNames {
		tool := tools[toolName]
		_, hasFix := tool.Operations[OpFix]
		_, hasLint := tool.Operations[OpLint]
		if !hasFix && !hasLint {
			errs = append(errs, fmt.Sprintf(
				"tool %q: must declare at least one fix or lint operation",
				toolName,
			))
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
