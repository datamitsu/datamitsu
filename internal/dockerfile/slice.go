package dockerfile

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
)

// sliceDir is the in-image directory the config-split stage writes per-stage
// config slices into, and that every builder stage COPYs its own slice from.
// The generator (COPY lines) and `devtools split-config` (file names) must agree
// on it, so it is the single source of truth for both.
const sliceDir = "/slices"

// Slice is one stage's minimal config: just the app (and its runtime) that stage
// installs, including its dependency closure and their runtimes. Each stage
// loads only its slice, so editing an app busts its build cache and those of
// its dependents, not the whole image.
type Slice struct {
	StageName string         // e.g. "rt-node", "app-prettier"
	FileName  string         // SliceFileName(StageName)
	Config    *config.Config // minimal config containing this stage's target and dependencies
}

// SliceFileName is the slice file name for a stage. Shared by the generator's
// COPY lines and split-config's output so they always line up.
func SliceFileName(stage string) string { return stage + ".js" }

// BuildSlices returns one minimal config slice per stage in the plan: a runtime
// stage gets its runtime plus, for Node and Bun, the pnpm runtime it references
// (which that stage installs for the app stages built FROM it); each app stage
// gets its dependency closure plus the runtimes and pnpm references needed to
// install it. A parser stage gets only its `parsers` entry. Order mirrors the
// plan for deterministic output.
func BuildSlices(plan Plan, apps binmanager.MapOfApps, runtimes config.MapOfRuntimes, parsers config.MapOfParsers) ([]Slice, error) {
	if err := plan.ValidateDependencies(apps); err != nil {
		return nil, err
	}

	slices := make([]Slice, 0, len(plan.RuntimeStages)+len(plan.RuntimeAppStages)+len(plan.BinaryStages)+len(plan.ParserStages))

	for _, rt := range plan.RuntimeStages {
		stage := stageName("rt-", rt.Name)
		sliceRuntimes := config.MapOfRuntimes{rt.Name: runtimes[rt.Name]}
		// Every config load validates a Node or Bun runtime's pnpmRuntime
		// reference, so without the pnpm definition the stage could not even load
		// its own slice — it installs pnpm here for the app stages below.
		if rt.PNPMRuntime != "" {
			sliceRuntimes[rt.PNPMRuntime] = runtimes[rt.PNPMRuntime]
		}
		slices = append(slices, Slice{
			StageName: stage,
			FileName:  SliceFileName(stage),
			Config:    &config.Config{Runtimes: sliceRuntimes},
		})
	}

	appNames := make([]string, 0, len(plan.RuntimeAppStages)+len(plan.BinaryStages))
	for _, ts := range plan.RuntimeAppStages {
		appNames = append(appNames, ts.App)
	}
	for _, bs := range plan.BinaryStages {
		appNames = append(appNames, bs.App)
	}
	for _, name := range appNames {
		cfg, err := appSlice(name, apps, runtimes)
		if err != nil {
			return nil, fmt.Errorf("slice for app %q: %w", name, err)
		}
		stage := stageName("app-", name)
		slices = append(slices, Slice{StageName: stage, FileName: SliceFileName(stage), Config: cfg})
	}

	for _, ps := range plan.ParserStages {
		stage := stageName("parser-", ps.Module)
		slices = append(slices, Slice{
			StageName: stage,
			FileName:  SliceFileName(stage),
			Config:    &config.Config{Parsers: config.MapOfParsers{ps.Module: parsers[ps.Module]}},
		})
	}

	return slices, nil
}

func appSlice(root string, apps binmanager.MapOfApps, runtimes config.MapOfRuntimes) (*config.Config, error) {
	names, err := binmanager.AppDependencyClosure(apps, []string{root})
	if err != nil {
		return nil, err
	}
	runtimeNames := make([]string, 0, len(runtimes))
	for name := range runtimes {
		runtimeNames = append(runtimeNames, name)
	}
	sort.Strings(runtimeNames)
	cfg := &config.Config{Apps: make(binmanager.MapOfApps), Runtimes: make(config.MapOfRuntimes)}
	for _, name := range names {
		app := apps[name]
		cfg.Apps[name] = app
		if kind, ref, ok := classifyApp(app); ok {
			runtimeName := resolveRuntimeName(kind, ref, runtimes, runtimeNames)
			if runtimeName == "" {
				return nil, fmt.Errorf("app %q has no resolved %s runtime", name, kind)
			}
			rc := runtimes[runtimeName]
			cfg.Runtimes[runtimeName] = rc
			if pnpmName, ok := runtimes.PNPMRuntimeName(rc); ok {
				cfg.Runtimes[pnpmName] = runtimes[pnpmName]
			}
		}
	}
	return cfg, nil
}

// RenderSlice serializes a minimal config into a self-contained JS config module
// that `datamitsu install --config` can load. getMinVersion returns "0.0.0" so a
// slice never trips the wrapper's version gate, and getConfig ignores its input
// and returns exactly this stage's app/runtime and dependency closure.
//
// The output is a projection of the Go struct, not a round-trip of the author's
// config: json.Marshal writes the fields this binary knows about, with this
// binary's defaults applied. That is fine for the app/runtime slices it is built
// for — install needs no tool definitions — but it means a slice must never be
// used to carry tools. A tool-bearing config passed through here would be
// re-emitted under the current schema, silently dropping any field the running
// binary does not have and freezing inferred values as if the author had
// written them. Keep slices app/runtime-only.
func RenderSlice(cfg *config.Config) (string, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal slice config: %w", err)
	}

	var b strings.Builder
	b.WriteString("// GENERATED by `datamitsu devtools split-config` — minimal per-stage config. DO NOT EDIT.\n")
	b.WriteString("function getMinVersion() { return \"0.0.0\"; }\n")
	b.WriteString("function getConfig() { return ")
	b.Write(data)
	b.WriteString("; }\n")
	// Assign to globalThis so the loader finds the exports even if StripTypes ever
	// compiles this into a module scope (where top-level decls are not global).
	b.WriteString("globalThis.getMinVersion = getMinVersion;\n")
	b.WriteString("globalThis.getConfig = getConfig;\n")
	return b.String(), nil
}
