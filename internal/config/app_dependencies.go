package config

import (
	"fmt"
	"slices"

	"github.com/datamitsu/datamitsu/internal/binmanager"
)

func validateAppDependencies(apps binmanager.MapOfApps, names []string) []string {
	var errs []string
	for _, name := range names {
		deps := slices.Clone(apps[name].DependsOn)
		slices.Sort(deps)
		for i, dep := range deps {
			if i > 0 && dep == deps[i-1] {
				errs = append(errs, fmt.Sprintf("app %q: duplicate dependency %q", name, dep))
				continue
			}
			app, ok := apps[dep]
			switch {
			case dep == name:
				errs = append(errs, fmt.Sprintf("app %q: cannot depend on itself", name))
			case !ok:
				errs = append(errs, fmt.Sprintf("app %q: dependency %q not found in apps", name, dep))
			case app.Shell != nil:
				errs = append(errs, fmt.Sprintf("app %q: dependency %q is a shell app and cannot be provisioned", name, dep))
			case app.Binary == nil && app.Bun == nil && app.Node == nil && app.Uv == nil && app.Jvm == nil && app.Go == nil:
				errs = append(errs, fmt.Sprintf("app %q: dependency %q has no installable configuration", name, dep))
			}
		}
	}
	if len(errs) == 0 {
		if _, err := binmanager.AppDependencyClosure(apps, names); err != nil {
			errs = append(errs, err.Error())
		}
	}
	return errs
}
