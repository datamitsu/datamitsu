// Package dockerfile builds the stage graph for, and renders, an optimized
// multi-stage Dockerfile from a datamitsu config's app/runtime list.
//
// The layout is hierarchical (one builder stage per binary, per runtime+version,
// and per runtime-managed app inheriting its runtime stage) so that changing a
// single app invalidates and re-pulls only that app's layer. The planning here
// is pure (no I/O, no network) so it is trivially testable; rendering and digest
// resolution live in sibling files/packages.
package dockerfile

import (
	"sort"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/syslist"
)

// PlanOptions tunes plan construction. The zero value applies no libc filtering
// (every binary app is included), preserving the original BuildPlan behavior.
type PlanOptions struct {
	// TargetLibc, when non-empty ("glibc"|"musl"), drops binary apps that have no
	// linux binary for it on every arch they declare — a glibc-only binary can't
	// run on a musl image and vice versa. Runtime-managed apps are unaffected
	// (their runtime carries the libc). Empty disables the filter.
	TargetLibc string
	// ForceInclude names binary apps to keep despite lacking a TargetLibc binary
	// (e.g. statically-linked tools the registry under-declares as glibc-only).
	ForceInclude map[string]bool
}

// RuntimeStage installs one managed runtime (e.g. node, uv). Runtime-managed
// app stages inherit FROM it so the runtime is installed once and shared.
type RuntimeStage struct {
	Name string             // runtime name as keyed in config.Runtimes
	Kind config.RuntimeKind // uv | node | jvm | go
}

// RuntimeAppStage installs one runtime-managed app. It is built FROM the runtime
// stage named Runtime, so only the app's own files become a new layer.
type RuntimeAppStage struct {
	App     string
	Kind    config.RuntimeKind
	Runtime string // RuntimeStage.Name this stage inherits from
}

// BinaryStage installs one downloaded-binary app. It is built FROM the shared
// base stage (binaries need no runtime).
type BinaryStage struct {
	App string
}

// Plan is the resolved stage graph for a config. Stage slices are sorted for
// deterministic, byte-stable rendering.
type Plan struct {
	RuntimeStages    []RuntimeStage
	RuntimeAppStages []RuntimeAppStage
	BinaryStages     []BinaryStage
	// Skipped lists app names omitted from the plan (shell apps, or
	// runtime-managed apps whose runtime reference does not resolve).
	Skipped []string
	// LibcExcluded lists binary apps omitted because they have no binary for
	// PlanOptions.TargetLibc (and were not force-included). Empty when no libc
	// filter is applied. Sorted.
	LibcExcluded []string
}

// classifyApp mirrors runtimemanager.runtimeAppRef's precedence (uv → node → jvm
// → go) without importing the unexported helper. ok is false for binary/shell/
// empty apps; callers then inspect app.Binary to split binary from shell.
func classifyApp(app binmanager.App) (kind config.RuntimeKind, runtimeRef string, ok bool) {
	switch {
	case app.Uv != nil:
		return config.RuntimeKindUV, app.Uv.Runtime, true
	case app.Node != nil:
		return config.RuntimeKindNode, app.Node.Runtime, true
	case app.Jvm != nil:
		return config.RuntimeKindJVM, app.Jvm.Runtime, true
	case app.Go != nil:
		return config.RuntimeKindGo, app.Go.Runtime, true
	default:
		return "", "", false
	}
}

// resolveRuntimeName picks the runtime a managed app installs under, mirroring
// CollectRequiredRuntimes: an explicit, existing ref wins; otherwise the first
// (sorted) runtime of the matching kind is used. Returns "" when nothing
// resolves (a dangling ref or no runtime of the kind).
func resolveRuntimeName(kind config.RuntimeKind, ref string, runtimes config.MapOfRuntimes, sortedRuntimeNames []string) string {
	if ref != "" {
		if _, exists := runtimes[ref]; exists {
			return ref
		}
		return ""
	}
	for _, name := range sortedRuntimeNames {
		if runtimes[name].Kind == kind {
			return name
		}
	}
	return ""
}

// binaryAppSupportsLibc reports whether b provides a linux binary for libc on
// every linux arch it declares. If any declared arch lacks the target libc the
// app can't install on that arch (a glibc binary won't exec on musl, etc.), so
// it is not supported. Apps with no linux binaries are unsupported too.
func binaryAppSupportsLibc(b *binmanager.AppConfigBinary, libc string) bool {
	linux := b.Binaries[syslist.OsTypeLinux]
	if len(linux) == 0 {
		return false
	}
	for _, libcs := range linux {
		if _, ok := libcs[libc]; !ok {
			return false
		}
	}
	return true
}

// BuildPlan resolves the full stage graph for apps + runtimes. It includes every
// app (required and optional), matching the `init --all` semantics the generated
// Dockerfile replaces. The optional PlanOptions filters binary apps by target
// libc (see PlanOptions); the zero value applies no filtering.
func BuildPlan(apps binmanager.MapOfApps, runtimes config.MapOfRuntimes, opts ...PlanOptions) Plan {
	var o PlanOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	appNames := make([]string, 0, len(apps))
	for name := range apps {
		appNames = append(appNames, name)
	}
	sort.Strings(appNames)

	sortedRuntimeNames := make([]string, 0, len(runtimes))
	for name := range runtimes {
		sortedRuntimeNames = append(sortedRuntimeNames, name)
	}
	sort.Strings(sortedRuntimeNames)

	var plan Plan
	neededRuntimes := make(map[string]config.RuntimeKind)

	for _, name := range appNames {
		app := apps[name]
		kind, ref, ok := classifyApp(app)
		if !ok {
			switch {
			case app.Binary == nil:
				plan.Skipped = append(plan.Skipped, name) // shell or empty app
			case o.TargetLibc != "" && !o.ForceInclude[name] && !binaryAppSupportsLibc(app.Binary, o.TargetLibc):
				plan.LibcExcluded = append(plan.LibcExcluded, name) // no binary for the target libc
			default:
				plan.BinaryStages = append(plan.BinaryStages, BinaryStage{App: name})
			}
			continue
		}

		runtimeName := resolveRuntimeName(kind, ref, runtimes, sortedRuntimeNames)
		if runtimeName == "" {
			plan.Skipped = append(plan.Skipped, name) // unresolved runtime reference
			continue
		}
		neededRuntimes[runtimeName] = runtimes[runtimeName].Kind
		plan.RuntimeAppStages = append(plan.RuntimeAppStages, RuntimeAppStage{App: name, Kind: kind, Runtime: runtimeName})
	}

	runtimeNames := make([]string, 0, len(neededRuntimes))
	for name := range neededRuntimes {
		runtimeNames = append(runtimeNames, name)
	}
	sort.Strings(runtimeNames)
	for _, name := range runtimeNames {
		plan.RuntimeStages = append(plan.RuntimeStages, RuntimeStage{Name: name, Kind: neededRuntimes[name]})
	}

	return plan
}
