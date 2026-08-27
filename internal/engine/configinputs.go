package engine

import (
	"encoding/json"
	"fmt"
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

// ConfigInputKeys returns the names of the globals injected as
// datamitsuConfigInputs, sorted. It is derived from the same JSON round trip
// initConfigInputs uses, so a new field appears here automatically — which is
// what lets configcache pin the agreement rather than trust a comment.
func ConfigInputKeys() []string {
	m, err := configInputsMap(configInputs{})
	if err != nil {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// configInputsMap round-trips inputs through JSON so the injected keys honor
// the json tags (camelCase) and stay in lockstep with the struct definition
// rather than being hand-duplicated.
func configInputsMap(inputs configInputs) (map[string]any, error) {
	data, err := json.Marshal(inputs)
	if err != nil {
		return nil, fmt.Errorf("marshal config inputs: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("unmarshal config inputs: %w", err)
	}
	return m, nil
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
// Forward contract, now enforced: config JS evaluation IS cached
// (internal/configcache), so every field exposed here MUST be folded into the
// cache key, or stale config could be served after an env override changes the
// value. The mirror lives in configcache.ConfigInputs and the two key sets are
// pinned against each other by TestConfigInputsMatchEngine — a new field here
// fails that test until configcache carries it too.
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

	m, err := configInputsMap(inputs)
	if err != nil {
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
