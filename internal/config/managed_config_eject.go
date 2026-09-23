package config

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// DatamitsuDirName is the git-root directory datamitsu provisions: links into
// the store, type definitions, and the internal configs below.
const DatamitsuDirName = ".datamitsu"

// InternalConfigsDir is the DatamitsuDirName subdirectory holding the rendered
// content of ejectable managed configs that the project did not eject.
const InternalConfigsDir = "configs"

// ManagedConfigPlacement says where a managed config file lives.
type ManagedConfigPlacement string

// Placements: written into the repository by reconciliation, or rendered into
// .datamitsu/configs/ by the loader and written there by init.
const (
	PlacementRepo     ManagedConfigPlacement = "repo"
	PlacementInternal ManagedConfigPlacement = "internal"
)

// InternalConfigRelPath returns the git-root-relative, slash-separated path an
// internal-placement entry is written to.
func InternalConfigRelPath(key string) string {
	return path.Join(DatamitsuDirName, InternalConfigsDir, key)
}

// ManagedConfigRef is one managed config a planned task reads.
type ManagedConfigRef struct {
	Tool string
	Key  string
}

// ejectedToolSet returns the tools named in EjectConfigs.
func ejectedToolSet(cfg *Config) map[string]bool {
	set := make(map[string]bool, len(cfg.EjectConfigs))
	for _, name := range cfg.EjectConfigs {
		set[name] = true
	}
	return set
}

// placementFor decides one entry's placement. Any listed tool being ejected
// ejects the file: a file two tools read cannot be in two places.
func placementFor(mc ManagedConfig, ejected map[string]bool) ManagedConfigPlacement {
	if !mc.Ejectable {
		return PlacementRepo
	}
	for _, tool := range mc.Tools {
		if ejected[tool] {
			return PlacementRepo
		}
	}
	return PlacementInternal
}

// ApplyManagedConfigPlacements derives every entry's Placement from its
// Ejectable flag and the chain's final EjectConfigs.
func ApplyManagedConfigPlacements(cfg *Config) {
	ejected := ejectedToolSet(cfg)
	for key, mc := range cfg.ManagedConfigs {
		mc.Placement = placementFor(mc, ejected)
		cfg.ManagedConfigs[key] = mc
	}
}

// validRelocatableKey reports whether key can be mirrored under
// .datamitsu/configs/ without escaping it or colliding with datamitsu's own files.
func validRelocatableKey(key string) bool {
	if key == "" || strings.Contains(key, "\\") || path.IsAbs(key) || strings.Contains(key, ":") {
		return false
	}
	if path.Clean(key) != key || key == "." || key == ".." || strings.HasPrefix(key, "../") {
		return false
	}
	return key != DatamitsuDirName && !strings.HasPrefix(key, DatamitsuDirName+"/")
}

// ValidateEject checks the ejectable declarations and the project's
// ejectConfigs against the final chain.
func ValidateEject(cfg *Config) error {
	var errs []string

	keys := make([]string, 0, len(cfg.ManagedConfigs))
	for key := range cfg.ManagedConfigs {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	owners := make(map[string]bool)
	for _, key := range keys {
		mc := cfg.ManagedConfigs[key]
		if !mc.Ejectable {
			continue
		}
		prefix := fmt.Sprintf("managed config %q: ejectable", key)
		switch {
		case mc.LinkTarget != "":
			errs = append(errs, prefix+" cannot be combined with linkTarget")
		case mc.DeleteOnly:
			errs = append(errs, prefix+" cannot be combined with deleteOnly")
		case len(mc.Tools) == 0:
			errs = append(errs, prefix+" requires tools: ejectConfigs selects files by the tools that read them")
		case mc.Scope != ScopeGitRoot:
			errs = append(errs, fmt.Sprintf("%s requires scope %q", prefix, ScopeGitRoot))
		case !validRelocatableKey(key):
			errs = append(errs, fmt.Sprintf("%s requires a clean relative path outside %s/", prefix, DatamitsuDirName))
		}
		for _, tool := range mc.Tools {
			owners[tool] = true
		}
	}

	errs = append(errs, ejectableKeyCollisions(cfg.ManagedConfigs, keys)...)

	seen := make(map[string]bool, len(cfg.EjectConfigs))
	for _, name := range cfg.EjectConfigs {
		switch {
		case name == "":
			errs = append(errs, "ejectConfigs: tool name must not be empty")
		case seen[name]:
			errs = append(errs, fmt.Sprintf("ejectConfigs: %q is listed more than once", name))
		case !hasTool(cfg.Tools, name):
			errs = append(errs, fmt.Sprintf("ejectConfigs: %q is not a configured tool", name))
		case !owners[name]:
			errs = append(errs, fmt.Sprintf("ejectConfigs: tool %q has no ejectable managed config", name))
		}
		seen[name] = true
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}

// ejectableKeyCollisions reports ejectable keys that .datamitsu/configs/
// cannot hold together: one naming a directory of the other, or two differing
// only in case, which a case-insensitive filesystem stores as one file. Caught
// at load, because the writer failing on them would come after reconciliation
// has already removed repository copies. keys must be sorted.
func ejectableKeyCollisions(configs MapOfManagedConfigs, keys []string) []string {
	var ejectable []string
	for _, key := range keys {
		if configs[key].Ejectable {
			ejectable = append(ejectable, key)
		}
	}
	var errs []string
	for i, a := range ejectable {
		for _, b := range ejectable[i+1:] {
			la, lb := strings.ToLower(a), strings.ToLower(b)
			switch {
			case la == lb:
				errs = append(errs, fmt.Sprintf("managed configs %q and %q: ejectable keys that differ only in case collide in %s/%s/", a, b, DatamitsuDirName, InternalConfigsDir))
			case strings.HasPrefix(lb, la+"/") || strings.HasPrefix(la, lb+"/"):
				errs = append(errs, fmt.Sprintf("managed configs %q and %q: one ejectable key is a directory of the other", a, b))
			}
		}
	}
	return errs
}

func hasTool(tools MapOfTools, name string) bool {
	_, ok := tools[name]
	return ok
}

const managedConfigPlaceholderOpen = "{managedConfig:"

var managedConfigPlaceholder = regexp.MustCompile(`\{managedConfig:([^{}\s]+)\}`)

// managedConfigPathTemplate is the path an entry resolves to, still carrying
// {root}/{cwd} so the executor expands it and nothing hashes an absolute path.
func managedConfigPathTemplate(key string, mc ManagedConfig) string {
	if mc.Placement == PlacementInternal {
		return "{root}/" + InternalConfigRelPath(key)
	}
	if mc.Scope == ScopeGitRoot {
		return "{root}/" + key
	}
	return "{cwd}/" + key
}

// ResolveManagedConfigPlaceholders rewrites every {managedConfig:<key>} in tool
// operation args and env values into the entry's path for its placement, and
// records the referenced keys on the operation. It runs after the chain,
// because a base layer builds its tools before a later layer ejects anything.
// Placements must already be applied.
func ResolveManagedConfigPlaceholders(cfg *Config) error {
	var errs []string

	toolNames := make([]string, 0, len(cfg.Tools))
	for name := range cfg.Tools {
		toolNames = append(toolNames, name)
	}
	sort.Strings(toolNames)

	for _, toolName := range toolNames {
		tool := cfg.Tools[toolName]
		opTypes := make([]string, 0, len(tool.Operations))
		for opType := range tool.Operations {
			opTypes = append(opTypes, string(opType))
		}
		sort.Strings(opTypes)

		for _, opType := range opTypes {
			op := tool.Operations[OperationType(opType)]
			refs := map[string]string{}
			where := fmt.Sprintf("tool %q operation %q", toolName, opType)

			resolve := func(value, field string) string {
				out, used, problems := resolveManagedConfigValue(value, toolName, cfg.ManagedConfigs)
				for _, p := range problems {
					errs = append(errs, fmt.Sprintf("%s: %s in %s", where, p, field))
				}
				for _, key := range used {
					refs[key] = managedConfigPathTemplate(key, cfg.ManagedConfigs[key])
				}
				return out
			}

			if len(op.Args) > 0 {
				args := make([]string, len(op.Args))
				for i, arg := range op.Args {
					args[i] = resolve(arg, "args")
				}
				op.Args = args
			}
			if len(op.Env) > 0 {
				envMap := make(map[string]string, len(op.Env))
				for key, value := range op.Env {
					envMap[key] = resolve(value, fmt.Sprintf("env %q", key))
				}
				op.Env = envMap
			}

			op.ManagedConfigRefs = nil
			if len(refs) > 0 {
				op.ManagedConfigRefs = make([]ManagedConfigOpRef, 0, len(refs))
				for key, pathTemplate := range refs {
					op.ManagedConfigRefs = append(op.ManagedConfigRefs, ManagedConfigOpRef{Key: key, Path: pathTemplate})
				}
				slices.SortFunc(op.ManagedConfigRefs, func(a, b ManagedConfigOpRef) int { return strings.Compare(a.Key, b.Key) })
			}
			tool.Operations[OperationType(opType)] = op
		}
		cfg.Tools[toolName] = tool
	}

	if len(errs) > 0 {
		sort.Strings(errs)
		errs = slices.Compact(errs)
		return fmt.Errorf("config validation failed:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}

// resolveManagedConfigValue expands the placeholders in one string, returning
// the result, the keys it named and a description of every invalid reference.
func resolveManagedConfigValue(value, toolName string, configs MapOfManagedConfigs) (string, []string, []string) {
	if !strings.Contains(value, managedConfigPlaceholderOpen) {
		return value, nil, nil
	}

	var used, problems []string
	out := managedConfigPlaceholder.ReplaceAllStringFunc(value, func(match string) string {
		key := managedConfigPlaceholder.FindStringSubmatch(match)[1]
		mc, ok := configs[key]
		if !ok {
			problems = append(problems, match+" names no managed config")
			return match
		}
		if !slices.Contains(mc.Tools, toolName) {
			problems = append(problems, fmt.Sprintf(
				"%s belongs to tools %v; add %q to its tools so ejectConfigs moves the file this tool reads",
				match, mc.Tools, toolName))
			return match
		}
		used = append(used, key)
		return managedConfigPathTemplate(key, mc)
	})

	if len(problems) == 0 && strings.Contains(out, managedConfigPlaceholderOpen) {
		problems = append(problems, fmt.Sprintf("malformed %s…} placeholder", managedConfigPlaceholderOpen))
	}
	return out, used, problems
}
