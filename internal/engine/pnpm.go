package engine

import (
	"github.com/datamitsu/datamitsu/internal/pnpmdefaults"
)

// initPNPMWorkspaceDefaults injects pnpmWorkspaceDefaults as a JS global so
// the bundled config.js (and downstream user configs) can read the recommended
// pnpm 11 workspace security defaults from Go without redefining them.
func (e *Engine) initPNPMWorkspaceDefaults() {
	_ = e.vm.Set("pnpmWorkspaceDefaults", pnpmdefaults.Defaults())
}
