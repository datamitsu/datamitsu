package binmanager

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// AppDependency is one edge of an app's dependsOn list: the target app name
// and the alias under which this app refers to it in ${APP_BIN:<alias>}
// bindings. In config JSON an edge is either a plain app name (alias defaults
// to the app name) or an object { app, as }. In Go it is always normalized:
// DependencyEdges never returns an edge with an empty app or alias.
type AppDependency struct {
	// App is the dependency's name in apps.
	App string `json:"app"`

	// Alias is the role name used in bindings. Equal to App when the edge was
	// declared as a plain name.
	Alias string `json:"as,omitempty"`
}

// appDependencyJSON accepts both config forms of an edge.
type appDependencyJSON struct {
	App string `json:"app"`
	As  string `json:"as"`
}

// UnmarshalJSON normalizes the string-or-object edge form into AppDependency.
// A plain string is the app name with alias = name; an object must carry a
// non-empty app and may override the alias.
func (d *AppDependency) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err == nil {
		if name == "" {
			return errors.New("dependsOn: empty dependency name")
		}
		d.App = name
		d.Alias = name
		return nil
	}
	var raw appDependencyJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("dependsOn entry must be an app name or {app, as}: %w", err)
	}
	if raw.App == "" {
		return errors.New("dependsOn entry: app is required")
	}
	d.App = raw.App
	d.Alias = raw.App
	if raw.As != "" {
		d.Alias = raw.As
	}
	return nil
}

// MarshalJSON emits the canonical object form, so Dockerfile slices and every
// serialized App round-trip edges without depending on how they were spelled.
func (d AppDependency) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(appDependencyJSON{App: d.App, As: d.Alias})
	if err != nil {
		return nil, fmt.Errorf("marshal dependsOn edge: %w", err)
	}
	return data, nil
}

// DependencyEdges returns the app's direct dependency edges in declared order.
func (a App) DependencyEdges() []AppDependency {
	return a.DependsOn
}

// DependencyNames returns the app's direct dependency names in declared order.
// The historical consumers of the string list (closure, validation, PATH
// assembly, installer warnings) read names; aliases only matter to bindings.
func (a App) DependencyNames() []string {
	names := make([]string, 0, len(a.DependsOn))
	for _, edge := range a.DependsOn {
		names = append(names, edge.App)
	}
	return names
}

// dependencyAlias returns the alias a direct edge declares for app, and
// whether app is a direct dependency at all.
func dependencyAlias(edges []AppDependency, app string) (string, bool) {
	for _, edge := range edges {
		if edge.App == app {
			return edge.Alias, true
		}
	}
	return "", false
}

// aliasToApp resolves a binding alias to the direct dependency's app name:
// the reverse of dependencyAlias, used where a ${APP_BIN:<alias>} placeholder
// names a role and the resolution needs the app behind it.
func aliasToApp(edges []AppDependency, alias string) (string, bool) {
	for _, edge := range edges {
		if edge.Alias == alias {
			return edge.App, true
		}
	}
	return "", false
}

// cloneEdges returns a copy of the edge list safe for sorting in place.
func cloneEdges(edges []AppDependency) []AppDependency {
	return slices.Clone(edges)
}
