package engine

import (
	"encoding/json"
	"sort"

	"github.com/datamitsu/datamitsu/internal/runtimeconfig"
)

// configInputs is the bounded, allowlisted subset of the effective runtime
// config that config JS is permitted to branch on. Unlike pnpmWorkspaceDefaults
// (a published recommendation) and unlike the full runtimeconfig.Effective
// snapshot (CLI introspection only), every field here is a genuine config
// evaluation input.
//
// Adding a field requires updating config fingerprinting, cache invalidation,
// explain/debug metadata, future provenance metadata, the TS declarations in
// config/config.d.ts, the Runtime Config Policy in CLAUDE.md, and the engine
// tests that pin the exposed key set.
type configInputs struct {
	MinimumReleaseAgeMinutes int `json:"minimumReleaseAgeMinutes"`
}

// initConfigInputs injects datamitsuConfigInputs as a frozen JS global holding
// only the allowlisted config-evaluation inputs. The full runtimeconfig.Effective
// struct is intentionally NOT injected — exposing every runtime parameter would
// create hidden config inputs that silently affect fingerprinting/cache/explain
// once those exist. Keeping the surface minimal enforces the allowlist
// structurally rather than by policy alone.
//
// The object is built from a fresh map with sorted keys (deterministic
// enumeration) and frozen, mirroring initPNPMWorkspaceDefaults — without the
// freeze, goja exposes the underlying values such that JS mutation could write
// through to them.
//
// Forward contract: config JS evaluation is NOT cached today
// (cmd/config_loader.go re-evaluates the VM fresh every invocation), so
// branching on these inputs is safe — there is no stale-cache risk. When config
// evaluation caching is implemented, every field exposed here MUST be folded
// into the cache fingerprint key, or stale config could be served after an env
// override changes the value.
func (e *Engine) initConfigInputs() {
	eff, err := runtimeconfig.Get()
	if err != nil {
		// Not initialized (e.g. unit tests constructing an Engine directly).
		// Compute fresh from env so the VM still sees correct effective values.
		eff = runtimeconfig.Compute()
	}

	inputs := configInputs{
		MinimumReleaseAgeMinutes: eff.MinimumReleaseAgeMinutes,
	}

	// Round-trip through JSON so the injected keys honor the json tags
	// (camelCase) and stay in lockstep with the struct definition rather than
	// being hand-duplicated.
	data, err := json.Marshal(inputs)
	if err != nil {
		return
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return
	}

	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	obj := e.vm.NewObject()
	for _, k := range keys {
		_ = obj.Set(k, m[k])
	}
	_ = e.vm.Set("datamitsuConfigInputs", obj)
	_, _ = e.vm.RunString(`Object.freeze(datamitsuConfigInputs)`)
}
