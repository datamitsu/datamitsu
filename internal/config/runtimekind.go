package config

import "fmt"

// RuntimeKindInfo captures the per-kind facts that were previously duplicated
// across the runtime manager (system-command lookup, cache-hash folding) and the
// config validator. Centralizing them here is what keeps managed-mode and
// system-mode runtime hashes in lock-step: both fold the exact same version
// fields returned by HashFields, so adding a field to a kind can no longer drift
// between the two hash functions. Adding a new runtime kind becomes a single entry in
// runtimeKinds instead of edits fanned out across systemCommandForKind, the two
// hash functions, and ValidateRuntimes.
type RuntimeKindInfo struct {
	// Name is the canonical kind string ("uv" | "node" | "jvm" | "go").
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
			return []string{rc.Node.NodeVersion, rc.Node.PNPMVersion, rc.Node.PNPMHash}
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
		return []string{fmt.Sprintf("runtime %q: Node runtime requires node config with nodeVersion, pnpmVersion, and pnpmHash", name)}
	}
	var errs []string
	if rc.Node.NodeVersion == "" {
		errs = append(errs, fmt.Sprintf("runtime %q: node.nodeVersion is required", name))
	} else if !isValidVersionString(rc.Node.NodeVersion) {
		errs = append(errs, fmt.Sprintf("runtime %q: node.nodeVersion %q contains invalid characters (must be alphanumeric, dots, hyphens, underscores, or plus signs)", name, rc.Node.NodeVersion))
	}
	if rc.Node.PNPMVersion == "" {
		errs = append(errs, fmt.Sprintf("runtime %q: node.pnpmVersion is required", name))
	} else if !isValidVersionString(rc.Node.PNPMVersion) {
		errs = append(errs, fmt.Sprintf("runtime %q: node.pnpmVersion %q contains invalid characters (must be alphanumeric, dots, hyphens, underscores, or plus signs)", name, rc.Node.PNPMVersion))
	}
	if rc.Node.PNPMHash == "" {
		errs = append(errs, fmt.Sprintf("runtime %q: node.pnpmHash is required (SHA-256 hash of PNPM tarball)", name))
	} else if !isValidSHA256Hex(rc.Node.PNPMHash) {
		errs = append(errs, fmt.Sprintf("runtime %q: node.pnpmHash must be a valid SHA-256 hex string (64 lowercase hex characters)", name))
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
