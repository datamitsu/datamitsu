package binmanager

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/datamitsu/datamitsu/internal/digest"
	"github.com/datamitsu/datamitsu/internal/env"
)

// PrepareProtocolVersion names the prepared-state key protocol. Changing the
// semantics of how a prepare step runs (what it may see, what feeds the key)
// changes every key, which is the intended coarse invalidation: a state
// prepared under another protocol is a different state.
const PrepareProtocolVersion = "datamitsu-prepare/v1"

// AppPrepare is the declared one-time step that fills an app's prepared state
// ({store}/.prepared/<app>/<key>/out) after the app and its whole dependsOn
// closure are installed. The step is the app's own executable (or binPath from
// its install directory) run directly, without a shell.
type AppPrepare struct {
	// Args are the step's arguments, evaluated symbolically for the key:
	// placeholders stay unresolved so the key never depends on the store root.
	// Supports ${APP_PREPARED}, ${APP_DIR} and ${APP_BIN:<alias>}.
	Args []string `json:"args"`

	// Env is environment for the step only; same placeholders as Args. Runtime
	// keys, the isolation keys and PATH are owned by datamitsu during the step
	// and rejected here.
	Env map[string]string `json:"env,omitempty"`

	// BinPath names another executable of this app's own installation to run
	// instead of the app's main command, relative to the install directory.
	// Supported for node, bun and uv apps, and binary apps with extractDir.
	BinPath string `json:"binPath,omitempty"`

	// Network declares whether the step needs the network. It is required:
	// under DATAMITSU_OFFLINE an unprepared step that needs it is refused before
	// the process starts, and the declaration is what a config review sees.
	// A pointer distinguishes "not set" (a validation error) from false.
	Network *bool `json:"network"`

	// Path selects what the step finds on PATH: "declared" (default) — the
	// runtime's own directories and the binaries of the dependsOn closure only;
	// "inherit" — additionally the inherited host PATH.
	Path string `json:"path,omitempty"`
}

// PreparedPathMode values for AppPrepare.Path.
const (
	PreparePathDeclared = "declared"
	PreparePathInherit  = "inherit"
)

// PreparePath returns the store root holding prepared app state:
// {store}/.prepared. Each app's state lives under .prepared/<app>/<key>/out
// and is exposed to the app as ${APP_PREPARED} (run time) — a separate root
// from the install trees, because a single-file binary has no install
// directory, the install hash does not cover the step, and OCI layers copy
// whole subtrees.
func PreparePath() string {
	return filepath.Join(env.GetStorePath(), ".prepared")
}

// ComputePreparedKey computes the XXH3-128 identity of an app's prepared state.
// It is a pure function of the config and the resolved platform: no network,
// no store access, no absolute paths — so the key is stable across machines
// (a bundle builder and a consumer compute the same key) and computable by
// read-only resolution (source mode, auto-seed, store status).
//
// Inputs, in order: the protocol version, the app name, the app's
// store-relative install path, every dependency of the closure (name,
// store-relative install path, and its own prepared key when it declares a
// step), the alias map of direct edges, binPath, the symbolic (unexpanded)
// args, prepare.env and app.env (symbolic), and the PATH mode.
//
// Never hashed: the store root and any absolute path, expanded binding values
// (the prepared path contains the key itself), HOME and the inherited
// environment, time, the datamitsu version, runtimeEnv, the network flag and
// timeouts — those are policy or run-time concerns, not state identity.
func (bm *BinManager) ComputePreparedKey(appName string) (string, error) {
	app, ok := bm.mapOfApps[appName]
	if !ok {
		return "", &AppNotFoundError{AppName: appName}
	}
	if app.Prepare == nil {
		return "", &PrepareNotDeclaredError{AppName: appName}
	}

	parts := [][]byte{
		[]byte(PrepareProtocolVersion),
		[]byte(appName),
	}

	installRel, err := bm.storeRelInstallPath(appName)
	if err != nil {
		return "", err
	}
	parts = append(parts, []byte(installRel))

	closure, err := AppDependencyClosure(bm.mapOfApps, []string{appName})
	if err != nil {
		return "", err
	}
	// The closure includes the app itself last; the app's own inputs are
	// already hashed above, so hash every other member.
	for _, dep := range closure {
		if dep == appName {
			continue
		}
		depRel, err := bm.storeRelInstallPath(dep)
		if err != nil {
			return "", err
		}
		parts = append(parts, []byte(dep), []byte(depRel))
		if bm.mapOfApps[dep].Prepare != nil {
			depKey, err := bm.ComputePreparedKey(dep)
			if err != nil {
				return "", err
			}
			parts = append(parts, []byte(depKey))
		} else {
			parts = append(parts, nil)
		}
	}

	// Alias map of direct edges: sorted by alias, so an edge's role is part of
	// the identity regardless of list order. A clone, not the stored slice —
	// the key is a pure function and must not mutate (or race on) the config.
	edges := cloneEdges(app.DependsOn)
	sort.Slice(edges, func(i, j int) bool { return edges[i].Alias < edges[j].Alias })
	parts = append(parts, []byte(fmt.Sprintf("edges:%d", len(edges))))
	for _, edge := range edges {
		parts = append(parts, []byte(edge.Alias), []byte(edge.App))
	}

	parts = append(parts, []byte("binPath:"+app.Prepare.BinPath))
	// Length-prefixed counts as decimal strings: a single-byte count wraps at
	// 256, and unframed sequences let one collection's tail pose as another's
	// head (256 args == 0 args + 128 env pairs). Each collection is framed
	// with its own count so boundaries are unambiguous.
	parts = append(parts, []byte(fmt.Sprintf("args:%d", len(app.Prepare.Args))))
	for _, arg := range app.Prepare.Args {
		parts = append(parts, []byte(arg))
	}
	parts = append(parts, []byte("prepareEnv"))
	parts = append(parts, sortedEnvParts(app.Prepare.Env)...)
	parts = append(parts, []byte("appEnv"))
	parts = append(parts, sortedEnvParts(app.Env)...)
	// The effective mode: an omitted path and an explicit "declared" behave
	// identically, so they hash identically.
	pathMode := app.Prepare.Path
	if pathMode == "" {
		pathMode = PreparePathDeclared
	}
	parts = append(parts, []byte("path:"+pathMode))

	return digest.XXH3Multi(parts...).Hex(), nil
}

// sortedEnvParts frames a sorted key/value map with its pair count, so two
// different maps cannot collide by sharing a serialization prefix.
func sortedEnvParts(m map[string]string) [][]byte {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([][]byte, 0, 2*len(keys)+1)
	parts = append(parts, []byte(fmt.Sprintf("env:%d", len(keys))))
	for _, k := range keys {
		parts = append(parts, []byte(k), []byte(m[k]))
	}
	return parts
}

// storeRelInstallPath returns the app's install path relative to the store
// root (e.g. ".bin/schepherd/<hash>" or ".apps/node/slidev/<hash>"). Only the
// relative form enters the prepared key: an absolute path would make the key
// depend on the store root, and bundle builders seed at a different root
// (/dm/store) than consumers.
func (bm *BinManager) storeRelInstallPath(appName string) (string, error) {
	installPath, err := bm.ComputeInstallPath(appName)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(env.GetStorePath(), installPath)
	if err != nil {
		return "", fmt.Errorf("relativize install path of %q against the store: %w", appName, err)
	}
	return filepath.ToSlash(rel), nil
}

// AppNotFoundError reports a lookup against an app missing from the registry.
type AppNotFoundError struct {
	AppName string
}

func (e *AppNotFoundError) Error() string {
	return "app " + strconv.Quote(e.AppName) + " not found in registry"
}

// PrepareNotDeclaredError reports a prepared-state query for an app that
// declares no prepare step.
type PrepareNotDeclaredError struct {
	AppName string
}

func (e *PrepareNotDeclaredError) Error() string {
	return "app " + strconv.Quote(e.AppName) + " declares no prepare step"
}
