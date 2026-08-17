package parsermanager

import (
	"context"
	"fmt"
	"sort"
)

// Capabilities is a parser module's self-description, returned by its WASM
// `describe` export (parsers/datamitsu-parsers/src/capabilities.rs). The module
// is the single source of truth for its version and the tools it can parse —
// none of this is declared in datamitsu config, which carries only a source
// and a hash.
type Capabilities struct {
	SchemaVersion int              `json:"schemaVersion"`
	Module        string           `json:"module"`
	Version       string           `json:"version"`
	Tools         []ToolCapability `json:"tools"`
}

// ToolCapability describes one tool a module can parse: what it is, where it
// lives upstream, and how to invoke it per operation mode.
type ToolCapability struct {
	Name        string                     `json:"name"`
	Description string                     `json:"description"`
	URL         string                     `json:"url"`
	Operations  map[string]OperationRecipe `json:"operations"`
}

// OperationRecipe is the recommended invocation of a tool in one mode (e.g.
// "lint"): the args to pass and whether the file content is fed on stdin.
type OperationRecipe struct {
	Args  []string `json:"args"`
	Stdin bool     `json:"stdin"`
}

// ParserCatalog is the deduplicated, cross-module view of every tool the
// configured parsers can parse — the data behind `datamitsu devtools parsers
// list`. Tools are flattened and deduplicated by name; Conflicts records tools
// claimed by two distinct modules with diverging metadata.
type ParserCatalog struct {
	Tools     []CatalogTool `json:"tools"`
	Conflicts []string      `json:"conflicts,omitempty"`
}

// CatalogTool is one entry of a ParserCatalog: a tool plus which configured
// parser/module provides it and at what version.
type CatalogTool struct {
	ToolCapability

	// Parser is the `parsers` config entry name that resolves to the providing
	// module ("(local)" when listed from a --wasm file).
	Parser string `json:"parser"`
	// Module and Version are the providing module's self-reported identity.
	Module  string `json:"module"`
	Version string `json:"version"`
}

// DescribeParser resolves the named parser (downloading+verifying on first use),
// instantiates it from the shared compile-once runtime, and returns its
// self-described capabilities.
func (m *Manager) DescribeParser(ctx context.Context, name string) (Capabilities, error) {
	rt, err := m.Acquire(ctx, name)
	if err != nil {
		return Capabilities{}, err
	}
	defer func() { _ = rt.Close(ctx) }()
	return rt.Describe(ctx)
}

// DescribeLocal instantiates an already-loaded WASM module and returns its
// capabilities. It needs no config or network, so it serves both the
// `--wasm <path>` inspection flag and the release-time manifest cache.
func DescribeLocal(ctx context.Context, wasm []byte) (Capabilities, error) {
	rt, err := NewRuntime(ctx, wasm)
	if err != nil {
		return Capabilities{}, err
	}
	defer func() { _ = rt.Close(ctx) }()
	return rt.Describe(ctx)
}

// CatalogFromCapabilities flattens a single module's capabilities into a catalog,
// attributing every tool to the given parser name. Used by the --wasm path.
func CatalogFromCapabilities(parserName string, caps Capabilities) *ParserCatalog {
	cat := &ParserCatalog{Tools: make([]CatalogTool, 0, len(caps.Tools))}
	for _, t := range caps.Tools {
		cat.Tools = append(cat.Tools, CatalogTool{
			ToolCapability: t,
			Parser:         parserName,
			Module:         caps.Module,
			Version:        caps.Version,
		})
	}
	sortCatalog(cat)
	return cat
}

// ListCapabilities builds the deduplicated catalog across every configured
// parser. Distinct modules (by content key) are described exactly once — N config
// entries sharing one module call `describe` a single time — and tools are
// deduplicated by name across modules, flagging any tool two different modules
// claim with diverging identity.
func (m *Manager) ListCapabilities(ctx context.Context) (*ParserCatalog, error) {
	names := make([]string, 0, len(m.parsers))
	for n := range m.parsers {
		names = append(names, n)
	}
	sort.Strings(names)

	capsByKey := make(map[string]Capabilities) // content key -> describe result (once)
	seen := make(map[string]CatalogTool)       // tool name -> winning entry
	var conflicts []string

	for _, name := range names {
		p := m.parsers[name]
		key := cacheKey(p)
		caps, ok := capsByKey[key]
		if !ok {
			c, err := m.DescribeParser(ctx, name)
			if err != nil {
				return nil, fmt.Errorf("describe parser %q: %w", name, err)
			}
			caps = c
			capsByKey[key] = c
		}
		for _, t := range caps.Tools {
			entry := CatalogTool{
				ToolCapability: t,
				Parser:         name,
				Module:         caps.Module,
				Version:        caps.Version,
			}
			if prev, dup := seen[t.Name]; dup {
				// Same tool from two distinct modules with diverging identity is a
				// configuration smell worth surfacing; first-seen (alphabetical
				// parser name) wins the listing.
				if prev.Module != entry.Module || prev.Version != entry.Version || prev.URL != entry.URL {
					conflicts = append(conflicts, fmt.Sprintf(
						"tool %q is claimed by parser %q (%s %s) and %q (%s %s)",
						t.Name, prev.Parser, prev.Module, prev.Version, name, entry.Module, entry.Version,
					))
				}
				continue
			}
			seen[t.Name] = entry
		}
	}

	cat := &ParserCatalog{Tools: make([]CatalogTool, 0, len(seen)), Conflicts: conflicts}
	for _, e := range seen {
		cat.Tools = append(cat.Tools, e)
	}
	sortCatalog(cat)
	return cat, nil
}

// sortCatalog orders tools by name for a stable, byte-reproducible listing.
func sortCatalog(cat *ParserCatalog) {
	sort.Slice(cat.Tools, func(i, j int) bool {
		return cat.Tools[i].Name < cat.Tools[j].Name
	})
}

// Modes returns the sorted operation modes a tool advertises (e.g. ["lint"]),
// for rendering.
func (t ToolCapability) Modes() []string {
	modes := make([]string, 0, len(t.Operations))
	for m := range t.Operations {
		modes = append(modes, m)
	}
	sort.Strings(modes)
	return modes
}
