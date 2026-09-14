package binmanager

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// AppDependencyClosure orders dependencies before dependents, sorting roots and
// edges lexically. On invalid graphs it returns the reachable names and errors.
func AppDependencyClosure(apps MapOfApps, roots []string) ([]string, error) {
	state := make(map[string]uint8)
	var chain, names []string
	var errs []error
	var visit func(string)
	visit = func(name string) {
		if state[name] == 2 {
			return
		}
		if state[name] == 1 {
			start := slices.Index(chain, name)
			cycle := append(slices.Clone(chain[start:]), name)
			errs = append(errs, fmt.Errorf("app dependency cycle: %s", strings.Join(cycle, " -> ")))
			return
		}
		app, ok := apps[name]
		if !ok {
			state[name] = 2
			errs = append(errs, fmt.Errorf("app '%s' not found in registry", name))
			return
		}
		state[name] = 1
		chain = append(chain, name)
		deps := slices.Clone(app.DependsOn)
		slices.Sort(deps)
		for _, dep := range deps {
			visit(dep)
		}
		chain = chain[:len(chain)-1]
		state[name] = 2
		names = append(names, name)
	}
	orderedRoots := slices.Clone(roots)
	slices.Sort(orderedRoots)
	for _, name := range orderedRoots {
		visit(name)
	}
	return names, errors.Join(errs...)
}

func (bm *BinManager) binaryInstallNames(includeOptional bool) ([]string, error) {
	var roots []string
	for name, app := range bm.mapOfApps {
		if app.Binary != nil && (includeOptional || app.Required) {
			roots = append(roots, name)
		}
	}
	return AppDependencyClosure(bm.mapOfApps, roots)
}
