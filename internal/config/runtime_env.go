package config

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/datamitsu/datamitsu/internal/binmanager"
)

func validateAppRuntimeEnv(apps binmanager.MapOfApps, names []string) []string {
	var errs []string
	for _, name := range names {
		app := apps[name]
		for _, key := range slices.Sorted(maps.Keys(app.RuntimeEnv)) {
			field := fmt.Sprintf("apps.%s.runtimeEnv.%s", name, key)
			if _, exists := app.Env[key]; exists {
				errs = append(errs, field+": key is also defined in env")
			}
			_, err := binmanager.ExpandAppBinaryBindings(app.RuntimeEnv[key], func(dep string) (string, error) {
				if !slices.Contains(app.DependsOn, dep) {
					return "", fmt.Errorf("APP_BIN target %q must be listed in direct dependsOn", dep)
				}
				target, exists := apps[dep]
				if !exists {
					return "", fmt.Errorf("APP_BIN target %q not found in apps", dep)
				}
				if target.Binary == nil {
					return "", fmt.Errorf("APP_BIN target %q: only binary targets are supported", dep)
				}
				return dep, nil
			})
			if err != nil {
				errs = append(errs, field+": "+err.Error())
			}
		}
		for _, key := range slices.Sorted(maps.Keys(app.Env)) {
			if strings.Contains(app.Env[key], "${APP_BIN") {
				errs = append(errs, fmt.Sprintf("apps.%s.env.%s: APP_BIN placeholders are allowed only in runtimeEnv", name, key))
			}
		}
		errs = append(errs, pathKeyErrors(name, "env", app.Env)...)
		errs = append(errs, pathKeyErrors(name, "runtimeEnv", app.RuntimeEnv)...)
	}
	return errs
}

// pathKeyErrors rejects PATH in an app environment. The value is not expanded, so setting it
// replaces the inherited PATH wholesale, and a tool that looks anything up by name then fails
// far from the cause. The supported way to put a binary on PATH is to list it in dependsOn.
// Windows spells the variable in any case, so the comparison ignores it.
func pathKeyErrors(app, field string, values map[string]string) []string {
	var errs []string
	for _, key := range slices.Sorted(maps.Keys(values)) {
		if strings.EqualFold(key, "PATH") {
			errs = append(errs, fmt.Sprintf(
				"apps.%s.%s.%s: PATH cannot be set, it would replace the inherited PATH; list the binary in dependsOn to put it on PATH",
				app, field, key,
			))
		}
	}
	return errs
}
