package config

import (
	"fmt"
	"sort"
)

// RuntimeKindInfo captures the per-kind facts that were previously duplicated
// across the runtime manager (system-command lookup, cache-hash folding) and the
// config validator. Centralizing them here is what keeps managed-mode and
// system-mode runtime hashes in lock-step: both fold the exact same version
// fields returned by HashFields, so adding a field to a kind can no longer drift
// between the two hash functions. Adding a new runtime kind becomes a single entry in
// runtimeKinds instead of edits fanned out across systemCommandForKind, the two
// hash functions, and ValidateRuntimes.
type RuntimeKindInfo struct {
	// Name is the canonical kind string ("bun" | "uv" | "node" | "jvm" | "go" | "pnpm").
	Name string
	// SystemCommand is the system binary name used when falling back to system
	// mode (e.g. on a musl host without a musl archive). Empty means the kind has
	// no system fallback.
	SystemCommand string
	// HashFields returns the cache-affecting version field(s) for this kind in a
	// fixed order. It returns nil when the kind's typed sub-config is absent,
	// matching the historical "append only when the sub-config is non-nil"
	// behavior. The SAME slice feeds both the managed and system runtime hash
	// functions, so the two can never disagree about which fields invalidate the
	// cache.
	HashFields func(rc RuntimeConfig) []string
	// Validate returns kind-specific validation error strings for the runtime
	// named name (mode-aware via rc.Mode). It validates only the kind's typed
	// sub-config; the kind-agnostic mode/managed/system checks are handled by the
	// caller. A nil Validate means the kind has no required sub-config fields
	// (e.g. uv, whose pythonVersion is optional).
	Validate func(name string, rc RuntimeConfig) []string
}

var runtimeKinds = map[RuntimeKind]RuntimeKindInfo{
	RuntimeKindBun: {
		Name:          string(RuntimeKindBun),
		SystemCommand: "bun",
		HashFields: func(rc RuntimeConfig) []string {
			if rc.Bun == nil {
				return nil
			}
			return []string{rc.Bun.BunVersion}
		},
		Validate: validateBunRuntimeKind,
	},
	RuntimeKindUV: {
		Name:          string(RuntimeKindUV),
		SystemCommand: "uv",
		HashFields: func(rc RuntimeConfig) []string {
			if rc.UV == nil {
				return nil
			}
			return []string{rc.UV.PythonVersion}
		},
		// pythonVersion is optional and only feeds cache invalidation, so there
		// are no required uv sub-config fields to validate.
		Validate: nil,
	},
	RuntimeKindNode: {
		Name:          string(RuntimeKindNode),
		SystemCommand: "node",
		HashFields: func(rc RuntimeConfig) []string {
			if rc.Node == nil {
				return nil
			}
			return []string{rc.Node.NodeVersion}
		},
		Validate: validateNodeRuntimeKind,
	},
	RuntimeKindJVM: {
		Name:          string(RuntimeKindJVM),
		SystemCommand: "java",
		HashFields: func(rc RuntimeConfig) []string {
			if rc.JVM == nil {
				return nil
			}
			return []string{rc.JVM.JavaVersion}
		},
		Validate: validateJVMRuntimeKind,
	},
	RuntimeKindGo: {
		Name:          string(RuntimeKindGo),
		SystemCommand: "go",
		HashFields: func(rc RuntimeConfig) []string {
			if rc.Go == nil {
				return nil
			}
			return []string{rc.Go.GoVersion}
		},
		Validate: validateGoRuntimeKind,
	},
	RuntimeKindPNPM: {
		Name:          string(RuntimeKindPNPM),
		SystemCommand: "pnpm",
		HashFields: func(rc RuntimeConfig) []string {
			if rc.PNPM == nil {
				return nil
			}
			return []string{rc.PNPM.PNPMVersion}
		},
		Validate: validatePNPMRuntimeKind,
	},
}

func validateBunRuntimeKind(name string, rc RuntimeConfig) []string {
	if rc.Bun == nil {
		return []string{fmt.Sprintf("runtime %q: Bun runtime requires bun config with bunVersion and pnpmRuntime", name)}
	}
	var errs []string
	if rc.Bun.BunVersion == "" {
		errs = append(errs, fmt.Sprintf("runtime %q: bun.bunVersion is required", name))
	} else if !isValidVersionString(rc.Bun.BunVersion) {
		errs = append(errs, fmt.Sprintf("runtime %q: bun.bunVersion %q contains invalid characters (must be alphanumeric, dots, hyphens, underscores, or plus signs)", name, rc.Bun.BunVersion))
	}
	if rc.Bun.PNPMRuntime == "" {
		errs = append(errs, fmt.Sprintf("runtime %q: bun.pnpmRuntime is required (the name of the runtime of kind \"pnpm\" that installs Bun apps)", name))
	}
	return errs
}

// LookupRuntimeKind returns the registry entry for kind. The boolean is false for
// kinds not in the registry (e.g. a removed/legacy kind left over in an old
// config), letting callers skip kind-specific handling without erroring.
func LookupRuntimeKind(kind RuntimeKind) (RuntimeKindInfo, bool) {
	info, ok := runtimeKinds[kind]
	return info, ok
}

// AllRuntimeKinds returns the registered runtime kinds. Order is unspecified;
// callers that need determinism should sort the result.
func AllRuntimeKinds() []RuntimeKind {
	kinds := make([]RuntimeKind, 0, len(runtimeKinds))
	for k := range runtimeKinds {
		kinds = append(kinds, k)
	}
	return kinds
}

func validateNodeRuntimeKind(name string, rc RuntimeConfig) []string {
	if rc.Node == nil {
		return []string{fmt.Sprintf("runtime %q: Node runtime requires node config with nodeVersion and pnpmRuntime", name)}
	}
	var errs []string
	if rc.Node.NodeVersion == "" {
		errs = append(errs, fmt.Sprintf("runtime %q: node.nodeVersion is required", name))
	} else if !isValidVersionString(rc.Node.NodeVersion) {
		errs = append(errs, fmt.Sprintf("runtime %q: node.nodeVersion %q contains invalid characters (must be alphanumeric, dots, hyphens, underscores, or plus signs)", name, rc.Node.NodeVersion))
	}
	if rc.Node.PNPMRuntime == "" {
		errs = append(errs, fmt.Sprintf("runtime %q: node.pnpmRuntime is required (the name of the runtime of kind \"pnpm\" that installs Node apps)", name))
	}
	return errs
}

func validateJVMRuntimeKind(name string, rc RuntimeConfig) []string {
	if rc.JVM == nil {
		return []string{fmt.Sprintf("runtime %q: JVM runtime requires jvm config with javaVersion", name)}
	}
	var errs []string
	if rc.JVM.JavaVersion == "" {
		errs = append(errs, fmt.Sprintf("runtime %q: jvm.javaVersion is required", name))
	} else if !isValidVersionString(rc.JVM.JavaVersion) {
		errs = append(errs, fmt.Sprintf("runtime %q: jvm.javaVersion %q contains invalid characters (must be alphanumeric, dots, hyphens, underscores, or plus signs)", name, rc.JVM.JavaVersion))
	}
	return errs
}

func validateGoRuntimeKind(name string, rc RuntimeConfig) []string {
	switch {
	case rc.Mode == RuntimeModeSystem:
		// goVersion is optional in system mode; it only feeds cache invalidation
		// (mirrors UV's pythonVersion). Validate it only when explicitly set. A
		// missing-version warning is emitted in doValidateApps, matching UV.
		if rc.Go != nil && rc.Go.GoVersion != "" && !isValidVersionString(rc.Go.GoVersion) {
			return []string{fmt.Sprintf("runtime %q: go.goVersion %q contains invalid characters (must be alphanumeric, dots, hyphens, underscores, or plus signs)", name, rc.Go.GoVersion)}
		}
	case rc.Go == nil:
		return []string{fmt.Sprintf("runtime %q: Go runtime requires go config with goVersion", name)}
	case rc.Go.GoVersion == "":
		return []string{fmt.Sprintf("runtime %q: go.goVersion is required", name)}
	case !isValidVersionString(rc.Go.GoVersion):
		return []string{fmt.Sprintf("runtime %q: go.goVersion %q contains invalid characters (must be alphanumeric, dots, hyphens, underscores, or plus signs)", name, rc.Go.GoVersion)}
	}
	return nil
}

func validatePNPMRuntimeKind(name string, rc RuntimeConfig) []string {
	switch {
	case rc.Mode == RuntimeModeSystem:
		// A system pnpm is whatever the host provides; pnpmVersion then only
		// feeds cache invalidation, like goVersion, so it is optional.
		if rc.PNPM != nil && rc.PNPM.PNPMVersion != "" && !isValidVersionString(rc.PNPM.PNPMVersion) {
			return []string{fmt.Sprintf("runtime %q: pnpm.pnpmVersion %q contains invalid characters (must be alphanumeric, dots, hyphens, underscores, or plus signs)", name, rc.PNPM.PNPMVersion)}
		}
	case rc.PNPM == nil:
		return []string{fmt.Sprintf("runtime %q: pnpm runtime requires pnpm config with pnpmVersion", name)}
	case rc.PNPM.PNPMVersion == "":
		return []string{fmt.Sprintf("runtime %q: pnpm.pnpmVersion is required", name)}
	case !isValidVersionString(rc.PNPM.PNPMVersion):
		return []string{fmt.Sprintf("runtime %q: pnpm.pnpmVersion %q contains invalid characters (must be alphanumeric, dots, hyphens, underscores, or plus signs)", name, rc.PNPM.PNPMVersion)}
	}
	return nil
}

// validatePNPMRuntimeRef checks that the pnpmRuntime of a Node or Bun runtime
// names a runtime of kind pnpm. An empty reference is reported by the kind's
// own validation.
func validatePNPMRuntimeRef(name string, rc RuntimeConfig, runtimes MapOfRuntimes) []string {
	ref := rc.PNPMRuntimeRef()
	if ref == "" {
		return nil
	}
	field := string(rc.Kind) + ".pnpmRuntime"
	target, ok := runtimes[ref]
	if !ok {
		return []string{fmt.Sprintf("runtime %q: %s %q not found", name, field, ref)}
	}
	if target.Kind != RuntimeKindPNPM {
		return []string{fmt.Sprintf("runtime %q: %s %q is kind %q, expected %q", name, field, ref, target.Kind, RuntimeKindPNPM)}
	}
	return nil
}

// PNPMRuntimeName returns the pnpm runtime that installs the apps of rc, a Node
// or Bun runtime: its pnpmRuntime when that names a runtime of kind pnpm, or,
// when pnpmRuntime is empty, the first pnpm runtime by name — the default the
// runtime manager applies. ok is false when nothing resolves.
func (m MapOfRuntimes) PNPMRuntimeName(rc RuntimeConfig) (string, bool) {
	if rc.Kind != RuntimeKindNode && rc.Kind != RuntimeKindBun {
		return "", false
	}
	if ref := rc.PNPMRuntimeRef(); ref != "" {
		if target, ok := m[ref]; ok && target.Kind == RuntimeKindPNPM {
			return ref, true
		}
		return "", false
	}
	names := make([]string, 0, len(m))
	for name, candidate := range m {
		if candidate.Kind == RuntimeKindPNPM {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "", false
	}
	sort.Strings(names)
	return names[0], true
}
