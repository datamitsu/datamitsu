package config

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"golang.org/x/mod/semver"
)

// prepareIsolationKeys are the environment keys datamitsu itself owns during a
// prepare step (it points TMPDIR/TEMP/TMP and the XDG cache/data/state roots
// at throwaway directories inside the step layout). A config may not set
// them. XDG_CONFIG_HOME and HOME stay available to the step: tools read
// registry credentials through them.
var prepareIsolationKeys = []string{"TMPDIR", "TEMP", "TMP", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"}

// validateAppPrepare enforces the shape of apps.<name>.prepare. The step's
// contract is project-independence: the prepared state is shared by every
// project of the user and ships in OCI bundles, so project placeholders are
// rejected outright, and everything the step needs must be spelled out in
// args/env (the inherited environment is an acknowledged hole, not a feature).
func validateAppPrepare(apps binmanager.MapOfApps, names []string) []string {
	var errs []string
	for _, name := range names {
		app := apps[name]
		if app.Prepare == nil {
			continue
		}
		errs = append(errs, validateAppPrepareOne(apps, name, app)...)
	}
	return errs
}

func validateAppPrepareOne(apps binmanager.MapOfApps, name string, app binmanager.App) []string {
	var errs []string
	prefix := fmt.Sprintf("apps.%s.prepare", name)

	// Rule 1: a shell app has no managed installation to prepare.
	if app.Shell != nil {
		return []string{prefix + ": prepare is not supported on shell apps"}
	}

	// Rule 2: network is required. The declaration is what a config review
	// sees and what DATAMITSU_OFFLINE refusal keys on; there is no default
	// truth datamitsu could verify.
	if app.Prepare.Network == nil {
		errs = append(errs, prefix+".network: required (true when the step downloads anything)")
	}

	// Rule 3: args are required and may be empty — an explicit empty argument
	// list is a decision, a missing one is an accident.
	if app.Prepare.Args == nil {
		errs = append(errs, prefix+".args: required (may be empty)")
	}

	// Rule 4: binPath stays inside the app's own install directory and is
	// only meaningful where another executable exists.
	if binPath := app.Prepare.BinPath; binPath != "" {
		if err := validatePrepareBinPath(app, binPath); err != nil {
			errs = append(errs, prefix+".binPath: "+err.Error())
		}
	}

	// Rule 8: PATH mode.
	switch app.Prepare.Path {
	case "", binmanager.PreparePathDeclared, binmanager.PreparePathInherit:
	default:
		errs = append(errs, fmt.Sprintf("%s.path: must be %q or %q", prefix, binmanager.PreparePathDeclared, binmanager.PreparePathInherit))
	}

	// Placeholder checks need the app's edges to resolve ${APP_BIN:<alias>}.
	resolveAlias := func(alias string) error {
		targetName, ok := dependencyAliasOf(app, alias)
		if !ok {
			return fmt.Errorf("APP_BIN target %q must be listed in direct dependsOn", alias)
		}
		if target, exists := apps[targetName]; exists {
			return validateAppBINTargetKind(target, targetName)
		}
		return nil // a missing target is already reported by dependency validation
	}
	for i, arg := range app.Prepare.Args {
		if err := validatePrepareValue(arg, resolveAlias); err != nil {
			errs = append(errs, fmt.Sprintf("%s.args[%d]: %v", prefix, i, err))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(app.Prepare.Env)) {
		if strings.EqualFold(key, "PATH") {
			errs = append(errs, fmt.Sprintf("%s.env.%s: PATH cannot be set; datamitsu owns the step's PATH (see prepare.path)", prefix, key))
			continue
		}
		if slices.ContainsFunc(prepareIsolationKeys, func(k string) bool { return strings.EqualFold(k, key) }) {
			errs = append(errs, fmt.Sprintf("%s.env.%s: owned by datamitsu during prepare (the step's temporary directories)", prefix, key))
			continue
		}
		if err := validatePrepareValue(app.Prepare.Env[key], resolveAlias); err != nil {
			errs = append(errs, fmt.Sprintf("%s.env.%s: %v", prefix, key, err))
		}
	}
	return errs
}

// validatePrepareValue checks one arg or env value of a prepare step. Only
// the exact tokens ${APP_PREPARED}, ${APP_DIR} and ${APP_BIN:<alias>} may
// appear — a prefix match would let ${APP_PREPARED_TYPO} through — and every
// ${APP_BIN:<alias>} must resolve against the app's direct edges with a
// supported target kind. Tool placeholders are rejected in the literal
// segments too, including text before the first ${…} and between them.
func validatePrepareValue(value string, resolveAlias func(string) error) error {
	// A NUL cannot cross into a process argument or an environment value
	// intact, and the prepared key joins its parts with NUL separators: two
	// values that differ only by an embedded NUL would collide. Reject it
	// outright rather than length-prefix around it.
	if strings.ContainsRune(value, 0) {
		return errors.New("NUL bytes are not allowed in prepare values")
	}
	// Literal segments: everything outside ${…} placeholders. Checked in
	// order so a tool placeholder cannot hide before or between the forms
	// the loop consumes.
	for {
		start := strings.Index(value, "${")
		if start < 0 {
			break
		}
		if hasToolPlaceholder(value[:start]) {
			return errors.New("tool placeholders ({file}, {root}, …) are project-bound and not allowed in prepare; the prepared state is shared by every project")
		}
		rest := value[start:]
		end := strings.IndexByte(rest, '}')
		if end < 0 {
			return fmt.Errorf("unterminated placeholder %q", value[start:])
		}
		token := rest[2:end]
		switch {
		case strings.Contains(token, "${"):
			return fmt.Errorf("malformed nested placeholder %q", rest[:end+1])
		case token == "APP_PREPARED", token == "APP_DIR":
			// Exact matches only.
		case strings.HasPrefix(token, "APP_BIN:"):
			alias := strings.TrimPrefix(token, "APP_BIN:")
			if alias == "" || strings.ContainsAny(alias, "{}:$ \t\r\n") {
				return fmt.Errorf("malformed APP_BIN placeholder %q", rest[:end+1])
			}
			if err := resolveAlias(alias); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown placeholder ${%s} in prepare; allowed: ${APP_PREPARED}, ${APP_DIR}, ${APP_BIN:<alias>}", token)
		}
		value = rest[end+1:]
	}
	if hasToolPlaceholder(value) {
		return errors.New("tool placeholders ({file}, {root}, …) are project-bound and not allowed in prepare; the prepared state is shared by every project")
	}
	return nil
}

// hasToolPlaceholder reports whether the value contains any tool placeholder
// form, bare ({root}) or colon ({managedConfig:…}, {bin:…}).
func hasToolPlaceholder(value string) bool {
	if toolPlaceholderPattern.MatchString(value) {
		return true
	}
	return toolPlaceholderColonPattern.MatchString(value)
}

// validatePrepareBinPath checks that binPath is a relative, clean path that
// stays inside the install directory, declared on a kind that has another
// executable to run (node, bun, uv, or a binary app with extractDir).
func validatePrepareBinPath(app binmanager.App, binPath string) error {
	if binPath == "" {
		return nil
	}
	// A NUL cannot appear in a file path, and the prepared key joins its
	// parts with NUL separators: a binPath carrying one could collide with a
	// different binPath-plus-args pair.
	if strings.ContainsRune(binPath, 0) {
		return errors.New("NUL bytes are not allowed in binPath")
	}
	if strings.HasPrefix(binPath, "/") || strings.HasPrefix(binPath, `\`) || filepath.IsAbs(binPath) {
		return errors.New("must be relative to the app's install directory")
	}
	if cleaned := filepath.Clean(binPath); cleaned != binPath || cleaned == "." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || cleaned == ".." {
		return errors.New("must stay inside the install directory")
	}
	switch {
	case app.Node != nil, app.Bun != nil, app.Uv != nil:
		return nil
	case app.Binary != nil:
		// extractDir is a per-platform property of the binary entry. Every
		// declared platform must extract: a single-file entry has no
		// directory for binPath to live in, whatever the other entries do.
		var offenders []string
		for osType, archMap := range app.Binary.Binaries {
			for archType, libcMap := range archMap {
				for libc, info := range libcMap {
					if !info.ExtractDir {
						offenders = append(offenders, fmt.Sprintf("%s/%s/%s", osType, archType, libc))
					}
				}
			}
		}
		if len(offenders) > 0 {
			sort.Strings(offenders)
			return fmt.Errorf("a single-file binary entry has no other executable; extractDir is required on every platform (missing on %s)", strings.Join(offenders, ", "))
		}
		return nil
	default:
		return errors.New("not supported for this app kind")
	}
}

// PrepareFeatureMinVersion is the first datamitsu version that understands
// apps.<name>.prepare, object dependsOn edges and ${APP_PREPARED}. A layer
// using any of them below this version is silently degraded on an old core
// (the field is dropped, the placeholder reaches the app as a literal), so a
// newer core warns instead.
const PrepareFeatureMinVersion = "0.5.0"

// PrepareFeatureWarnings reports layers whose config uses prepare-era
// constructions while declaring a lower getMinVersion. The caller passes the
// layer's declared minimum (empty when the layer declares none, which is
// itself the problem): a layer already requiring >= PrepareFeatureMinVersion
// needs no warning. It runs on each layer's own result, so the construction
// is attributed to the layer that introduced it. Callers fold the result into
// the same warning channel that the config cache replays, so a cache hit
// prints what the miss printed.
func PrepareFeatureWarnings(label, declaredMinVersion string, apps binmanager.MapOfApps) []string {
	if declaredMinVersion != "" {
		if ok, err := versionMeets(declaredMinVersion, PrepareFeatureMinVersion); err == nil && ok {
			return nil
		}
	}
	var uses []string
	for _, name := range sortedAppNames(apps) {
		app := apps[name]
		if app.Prepare != nil {
			uses = append(uses, "apps."+name+".prepare")
			continue
		}
		// A renamed edge binds a dependency under an alias, which only a
		// prepare-era core understands. An edge whose alias equals the app
		// name is the plain-name form and works everywhere.
		for _, edge := range app.DependsOn {
			if edge.Alias != edge.App {
				uses = append(uses, "apps."+name+".dependsOn (renamed edge)")
				break
			}
		}
		for _, key := range slices.Sorted(maps.Keys(app.RuntimeEnv)) {
			if strings.Contains(app.RuntimeEnv[key], "${APP_PREPARED") {
				uses = append(uses, "apps."+name+".runtimeEnv (${APP_PREPARED})")
				break
			}
		}
	}
	if len(uses) == 0 {
		return nil
	}
	return []string{fmt.Sprintf(
		"config %s: uses %s (datamitsu >= %s); raise getMinVersion() so an older core refuses the config instead of silently ignoring the prepare step",
		label, uses[0], PrepareFeatureMinVersion)}
}

// versionMeets reports whether declared >= required under semver, without
// importing the version package (which compares against the running build);
// the config cache encodes warnings, so this stays a pure function of the
// two strings.
func versionMeets(declared, required string) (bool, error) {
	d, r := semver.Canonical("v"+strings.TrimPrefix(declared, "v")), semver.Canonical("v"+strings.TrimPrefix(required, "v"))
	if !semver.IsValid(d) || !semver.IsValid(r) {
		return false, fmt.Errorf("invalid version %q or %q", declared, required)
	}
	return semver.Compare(d, r) >= 0, nil
}

func sortedAppNames(apps binmanager.MapOfApps) []string {
	names := make([]string, 0, len(apps))
	for name := range apps {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
