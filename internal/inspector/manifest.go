// Package inspector exports a portable view of a resolved configuration.
package inspector

import (
	"cmp"
	"slices"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

// Manifest is the versioned display contract consumed by the embedded browser app.
type Manifest struct {
	SchemaVersion int `json:"schemaVersion"`
	// Name is the configuration's own name, already defaulted, so every consumer
	// of a snapshot shows the same label without repeating the fallback.
	Name           string          `json:"name"`
	Version        string          `json:"version"`
	Apps           []App           `json:"apps"`
	Tools          []Tool          `json:"tools"`
	ProjectTypes   []ProjectType   `json:"projectTypes"`
	ManagedConfigs []ManagedConfig `json:"managedConfigs"`
}

// App identifies an executable without its installation payload or environment.
type App struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Runtime     string   `json:"runtime"`
	Version     string   `json:"version"`
	DependsOn   []string `json:"dependsOn"`
	// OfficialURL is where a reader learns about this app, and OfficialURLDerived
	// says whether the configuration chose that page or the core worked it out
	// from a download URL or a package name. A surface shows the two differently:
	// one is the maintainer's answer, the other is an address.
	OfficialURL        string `json:"officialUrl,omitempty"`
	OfficialURLDerived bool   `json:"officialUrlDerived,omitempty"`
}

// Operation exposes matching and scheduling metadata without executable arguments.
type Operation struct {
	Kind         config.OperationType `json:"kind"`
	App          string               `json:"app"`
	Scope        config.ToolScope     `json:"scope"`
	Priority     int                  `json:"priority"`
	Globs        []string             `json:"globs"`
	ExcludeGlobs []string             `json:"excludeGlobs"`
}

// Tool retains skipped definitions so an export explains omissions from execution.
type Tool struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	ProjectTypes []string    `json:"projectTypes"`
	Skipped      bool        `json:"skipped"`
	SkipReason   string      `json:"skipReason"`
	Operations   []Operation `json:"operations"`
}

// ProjectType preserves detection markers without discovering files on the host.
type ProjectType struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Markers     []string `json:"markers"`
}

// ManagedConfig records ownership and deletion intent without evaluating content.
type ManagedConfig struct {
	Name         string   `json:"name"`
	Tools        []string `json:"tools"`
	ProjectTypes []string `json:"projectTypes"`
	Scope        string   `json:"scope"`
	DeleteOnly   bool     `json:"deleteOnly"`
}

// Snapshot omits executable content, environment values, arguments and download
// credentials so the artifact contains display metadata rather than a runnable config.
func Snapshot(cfg *config.Config, version string) Manifest {
	m := Manifest{SchemaVersion: protocol.SchemaVersion, Name: cfg.DisplayName(), Version: version, Apps: []App{}, Tools: []Tool{}, ProjectTypes: []ProjectType{}, ManagedConfigs: []ManagedConfig{}}
	for name, app := range cfg.Apps {
		kind, version := appKind(app)
		m.Apps = append(m.Apps, App{Name: name, Description: app.Description, Runtime: kind, Version: version, DependsOn: list(app.DependsOn), OfficialURL: app.OfficialURL, OfficialURLDerived: app.OfficialURLDerived})
	}
	for id, tool := range cfg.Tools {
		t := Tool{ID: id, Name: tool.Name, ProjectTypes: list(tool.ProjectTypes), Skipped: tool.Skip, SkipReason: tool.SkipReason, Operations: []Operation{}}
		for _, kind := range []config.OperationType{config.OpFix, config.OpLint} {
			if op, ok := tool.Operations[kind]; ok {
				scope := op.Scope
				if scope == "" {
					scope = config.ToolScopePerProject
				}
				t.Operations = append(t.Operations, Operation{Kind: kind, App: op.App, Scope: scope, Priority: op.Priority, Globs: list(op.Globs), ExcludeGlobs: list(op.ExcludeGlobs)})
			}
		}
		m.Tools = append(m.Tools, t)
	}
	for id, p := range cfg.ProjectTypes {
		m.ProjectTypes = append(m.ProjectTypes, ProjectType{ID: id, Description: p.Description, Markers: list(p.Markers)})
	}
	for name, c := range cfg.ManagedConfigs {
		m.ManagedConfigs = append(m.ManagedConfigs, ManagedConfig{Name: name, Tools: list(c.Tools), ProjectTypes: list(c.ProjectTypes), Scope: c.Scope, DeleteOnly: c.DeleteOnly})
	}
	slices.SortFunc(m.Apps, func(a, b App) int { return cmp.Compare(a.Name, b.Name) })
	slices.SortFunc(m.Tools, func(a, b Tool) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(m.ProjectTypes, func(a, b ProjectType) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(m.ManagedConfigs, func(a, b ManagedConfig) int { return cmp.Compare(a.Name, b.Name) })
	return m
}

func list(values []string) []string { return append([]string{}, values...) }
func appKind(a binmanager.App) (string, string) {
	switch {
	case a.Binary != nil:
		return "binary", a.Binary.Version
	case a.Bun != nil:
		return "bun", a.Bun.Version
	case a.Node != nil:
		return "node", a.Node.Version
	case a.Uv != nil:
		return "python", a.Uv.Version
	case a.Go != nil:
		return "go", a.Go.Version
	case a.Jvm != nil:
		return "jvm", a.Jvm.Version
	case a.Shell != nil:
		return "shell", ""
	default:
		return "unknown", ""
	}
}
