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
	}
	return errs
}
