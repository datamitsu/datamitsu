// Package pnpmdefaults is the single source of truth for the recommended
// pnpm 11 workspace security defaults applied by datamitsu. Both the node app
// installer (internal/runtimemanager) and the JS config engine
// (internal/engine, which injects the map as a JS global so config.js can
// publish it via sharedStorage) read from this package.
//
// Changing values here propagates to both the Go-side merge into per-app
// pnpm-workspace.yaml and the JS-side sharedStorage["pnpm-workspace-defaults"]
// without any further synchronization.
package pnpmdefaults

import "github.com/datamitsu/datamitsu/internal/runtimeconfig"

// Defaults returns a fresh copy of the recommended pnpm 11 workspace security
// defaults. A new map is returned on each call so callers can mutate it
// (e.g. merge user overrides on top) without affecting other callers.
func Defaults() map[string]any {
	return map[string]any{
		"strictDepBuilds":           true,
		"blockExoticSubdeps":        true,
		"enablePrePostScripts":      false,
		"dangerouslyAllowAllBuilds": false,
		"minimumReleaseAge":         runtimeconfig.MinimumReleaseAgeMinutes,
		"trustPolicy":               "no-downgrade",
		"lockfile":                  true,
		"preferFrozenLockfile":      true,
	}
}
