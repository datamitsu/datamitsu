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
			_, err := binmanager.ExpandAppBinaryBindings(app.RuntimeEnv[key], func(alias string) (string, error) {
				targetName, ok := dependencyAliasOf(app, alias)
				if !ok {
					return "", fmt.Errorf("APP_BIN target %q must be listed in direct dependsOn", alias)
				}
				target, exists := apps[targetName]
				if !exists {
					return "", fmt.Errorf("APP_BIN target %q not found in apps", targetName)
				}
				if err := validateAppBINTargetKind(target, targetName); err != nil {
					return "", err
				}
				return targetName, nil
			})
			if err != nil {
				errs = append(errs, field+": "+err.Error())
			}
		}
		for _, key := range slices.Sorted(maps.Keys(app.Env)) {
			value := app.Env[key]
			if strings.Contains(value, "${APP_BIN") {
				errs = append(errs, fmt.Sprintf("apps.%s.env.%s: APP_BIN placeholders are allowed only in runtimeEnv", name, key))
			}
			if strings.Contains(value, "${APP_PREPARED") {
				errs = append(errs, fmt.Sprintf("apps.%s.env.%s: ${APP_PREPARED} is not allowed in env (env reaches installers, and the prepared state does not exist yet); use runtimeEnv", name, key))
			}
		}
		for _, key := range slices.Sorted(maps.Keys(app.RuntimeEnv)) {
			if strings.Contains(app.RuntimeEnv[key], "${APP_PREPARED") && app.Prepare == nil {
				errs = append(errs, fmt.Sprintf("apps.%s.runtimeEnv.%s: ${APP_PREPARED} requires apps.%s.prepare", name, key, name))
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

// dependencyAliasOf resolves a binding alias to the direct dependency's app
// name. It mirrors binmanager's edge lookup for config validation, which
// works on the same normalized edge list.
func dependencyAliasOf(app binmanager.App, alias string) (string, bool) {
	for _, edge := range app.DependsOn {
		if edge.Alias == alias {
			return edge.App, true
		}
	}
	return "", false
}

// validateAppBINTargetKind admits the app kinds with a single executable
// entry point that binding resolution can hand out today: native binaries,
// uv and go apps. Node, bun and jvm apps need a launcher that does not exist
// yet, so they are a load-time error with a message that says so.
func validateAppBINTargetKind(target binmanager.App, name string) error {
	switch {
	case target.Binary != nil:
		return nil
	case target.Uv != nil, target.Go != nil:
		return nil
	default:
		return fmt.Errorf("APP_BIN target %q: %s apps need a launcher (not supported yet)", name, appKindName(target))
	}
}

// appKindName names an app's kind for error messages.
func appKindName(app binmanager.App) string {
	switch {
	case app.Binary != nil:
		return "binary"
	case app.Bun != nil:
		return "bun"
	case app.Uv != nil:
		return "uv"
	case app.Node != nil:
		return "node"
	case app.Jvm != nil:
		return "jvm"
	case app.Go != nil:
		return "go"
	case app.Shell != nil:
		return "shell"
	default:
		return "unknown"
	}
}
