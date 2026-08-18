// Package configcontract is the single source of truth for the parts of the
// config schema that config JavaScript is allowed to observe while it is being
// evaluated: the {placeholder} vocabulary and the set of core capabilities.
//
// Both values are consumed from two directions — the config validator rejects
// tokens outside the placeholder vocabulary, and facts() publishes both sets to
// the JS VM — so they live in a leaf package that either side can import
// without a cycle. Per the JS<->Go Shared Constants Policy in AGENTS.md, Go is
// the source and there is no JS copy to keep in sync.
package configcontract

import "slices"

// ArgPlaceholders and EnvPlaceholders are the substitution placeholders
// datamitsu expands in tool-operation arguments and environment-variable values
// respectively (the expansion itself lives in internal/tooling's executor).
// config.ValidateTools rejects any other token instead of passing it through
// unsubstituted, so adding a name here without implementing its expansion turns
// a loud config error into a silent literal.
// "target" is absent from EnvPlaceholders on purpose: env values expand on a
// path with no task, so it would resolve to a wrong directory instead of erroring.
var (
	ArgPlaceholders = []string{"file", "files", "root", "cwd", "toolCache", "target"}
	EnvPlaceholders = []string{"root", "cwd", "toolCache"}
)

// Capability names a behaviour a core build supports, so config JavaScript can
// branch on what it is running under instead of on a version number.
//
// Version comparison is not usable for this: ldflags.Version is "dev" for local
// builds and "0.0.0-unstable.*" for the prerelease channel this project ships
// from, and both sort below every real release — the same defect that makes
// getMinVersion() inert for those builds.
//
// The vocabulary is closed and append-only. A capability is published only once
// the behaviour it names is complete in that build; a name is never removed and
// never changes meaning, because configs in the wild branch on it.
type Capability string

const (
	// CapArity reports that arity dispatch is live: {target} is a valid argument
	// placeholder, argv shape is derived from arity rather than from batch, and
	// a batch field is ignored for dispatch.
	CapArity Capability = "arity"

	// CapGranularity reports that the granularity model is live: per-operation
	// granularity, run coverage, execution.widenTo, the verdict cache and the
	// LSP granularity policy.
	CapGranularity Capability = "granularity"
)

// supported lists the capabilities this build actually implements, sorted.
//
// A config detects the probe with `facts().capabilities !== undefined` and a
// behaviour with `.includes("arity")`; a core built before the probe existed
// omits the key, which is the correct negative answer.
var supported = []Capability{CapArity}

// Capabilities returns the capability names this build supports, sorted, as
// plain strings for the JS VM. The result is a fresh slice: it is published
// through facts() into a goja runtime and must not alias package state.
func Capabilities() []string {
	out := make([]string, 0, len(supported))
	for _, c := range supported {
		out = append(out, string(c))
	}
	slices.Sort(out)
	return out
}

// Supports reports whether this build publishes the given capability. It exists
// so Go-side callers ask the same question, from the same list, that config JS
// asks through facts().capabilities.
func Supports(c Capability) bool {
	return slices.Contains(supported, c)
}
