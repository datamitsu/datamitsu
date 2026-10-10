package config

import (
	"fmt"
	"slices"
	"strings"

	"github.com/datamitsu/datamitsu/internal/binmanager"
)

func validateAppDependencies(apps binmanager.MapOfApps, names []string) []string {
	var errs []string
	for _, name := range names {
		app := apps[name]
		deps := make([]string, 0, len(app.DependsOn))
		for _, edge := range app.DependsOn {
			deps = append(deps, edge.App)
		}
		slices.Sort(deps)
		for i, dep := range deps {
			if i > 0 && dep == deps[i-1] {
				errs = append(errs, fmt.Sprintf("app %q: duplicate dependency %q", name, dep))
				continue
			}
			target, ok := apps[dep]
			switch {
			case dep == name:
				errs = append(errs, fmt.Sprintf("app %q: cannot depend on itself", name))
			case !ok:
				errs = append(errs, fmt.Sprintf("app %q: dependency %q not found in apps", name, dep))
			case target.Shell != nil:
				errs = append(errs, fmt.Sprintf("app %q: dependency %q is a shell app and cannot be provisioned", name, dep))
			case target.Binary == nil && target.Bun == nil && target.Node == nil && target.Uv == nil && target.Jvm == nil && target.Go == nil:
				errs = append(errs, fmt.Sprintf("app %q: dependency %q has no installable configuration", name, dep))
			}
		}
		errs = append(errs, validateDependencyEdges(app.DependsOn, name)...)
	}
	if len(errs) == 0 {
		if _, err := binmanager.AppDependencyClosure(apps, names); err != nil {
			errs = append(errs, err.Error())
		}
	}
	return errs
}

// validateDependencyEdges checks the edge form itself: an alias must be a
// valid app-style name, unique among the app's direct edges (case-insensitive
// — the store is case-preserving on some filesystems and app names are
// case-fold checked), must not collide with another edge's app name, and one
// edge per target app (a second edge to the same app would make the alias
// ambiguous for no benefit).
func validateDependencyEdges(edges []binmanager.AppDependency, name string) []string {
	var errs []string
	seenAliases := make(map[string]string)
	seenTargets := make(map[string]string)
	for i, edge := range edges {
		if err := validateAppName(edge.Alias); err != nil {
			errs = append(errs, fmt.Sprintf("app %q: dependsOn[%d].as %q: %v", name, i, edge.Alias, err))
			continue
		}
		if prev, ok := seenAliases[strings.ToLower(edge.Alias)]; ok {
			errs = append(errs, fmt.Sprintf("app %q: dependsOn[%d].as %q: alias %q is already used by dependency %q", name, i, edge.Alias, edge.Alias, prev))
		}
		seenAliases[strings.ToLower(edge.Alias)] = edge.App
		if prev, ok := seenTargets[strings.ToLower(edge.App)]; ok && prev != edge.Alias {
			errs = append(errs, fmt.Sprintf("app %q: dependency %q is listed more than once (as %q and %q)", name, edge.App, prev, edge.Alias))
		}
		seenTargets[strings.ToLower(edge.App)] = edge.Alias
	}
	return errs
}
