package engine

import (
	"sort"

	"github.com/datamitsu/datamitsu/internal/pnpmdefaults"
)

// initPNPMWorkspaceDefaults injects pnpmWorkspaceDefaults as a JS global so
// the bundled config.js (and downstream user configs) can read the recommended
// pnpm 11 workspace security defaults from Go without redefining them.
//
// The injected object is built from a fresh map with sorted keys (so
// YAML.stringify produces deterministic output across runs) and deep-frozen
// (so JS code cannot silently downgrade defaults consumed downstream — e.g.
// `pnpmWorkspaceDefaults.strictDepBuilds = false`). Without the freeze, goja
// exposes the underlying Go map by reference and JS mutations write through
// to it, defeating the whole supply-chain-security point of the feature.
func (e *Engine) initPNPMWorkspaceDefaults() {
	defaults := pnpmdefaults.Defaults()
	keys := make([]string, 0, len(defaults))
	for k := range defaults {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	obj := e.vm.NewObject()
	for _, k := range keys {
		_ = obj.Set(k, defaults[k])
	}
	_ = e.vm.Set("pnpmWorkspaceDefaults", obj)
	_, _ = e.vm.RunString(`Object.freeze(pnpmWorkspaceDefaults)`)
}
