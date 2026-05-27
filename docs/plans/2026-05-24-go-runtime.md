# Add Go Runtime Kind to datamitsu

## Overview

- Add `RuntimeKindGo` to datamitsu, enabling Go tools (govulncheck, staticcheck, etc.) to be built from source with full supply chain verification
- Solves the gap where `go install pkg@latest` is insecure: no pinned SDK, no locked dependencies, no forced checksum DB verification
- Three-layer security: pinned Go SDK with SHA-256, lockfile storing go.mod+go.sum (JSON wrapper compressed with brotli), and `go build -trimpath -mod=readonly` that fails on any go.sum mismatch
- Include govulncheck as a default example Go app in embedded config.js

## Context (from discovery)

- Files/components involved:
  - `internal/config/config.go` — RuntimeKind, RuntimeConfig structs
  - `internal/config/validate.go` — app and runtime validation
  - `internal/config/config.d.ts` — TypeScript declarations
  - `internal/binmanager/binmanager.go` — App struct, AppConfig types, dispatch
  - `internal/runtimemanager/runtimemanager.go` — RuntimeManager dispatch, ComputeAppPath, GetCommandInfo, CollectRequiredRuntimes
  - `internal/runtimemanager/uv.go` — UV implementation (primary template)
  - `internal/runtimemanager/hash.go` — hash computation for runtimes and apps
  - `cmd/config_lockfile.go` — lockfile generation command
  - `internal/config/config.js` — embedded default config
- Related patterns: UV runtime follows the exact pattern needed (package manager + lockfile + isolated env)
- Lockfile format: JSON wrapper `{"mod": "<go.mod>", "sum": "<go.sum>"}` compressed with brotli+base64 (`br:...`)

## Development Approach

- **Testing approach**: TDD (tests first)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional - they are a required part of the checklist
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- Run tests after each change
- Maintain backward compatibility

## Testing Strategy

- **Unit tests**: required for every task (see Development Approach above)
- Test commands: `go test ./internal/config/...`, `go test ./internal/runtimemanager/...`, `go test ./internal/binmanager/...`, `go test ./cmd/...`
- Full suite: `go test ./...`

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with + prefix
- Document issues/blockers with ! prefix
- Update plan if implementation deviates from original scope
- Keep plan in sync with actual work done

## Implementation Steps

### Task 1: Add Go config types and constants

- [x] write tests for RuntimeKindGo constant and RuntimeConfigGo struct serialization
- [x] write tests for AppConfigGo struct serialization (JSON round-trip)
- [x] add `RuntimeKindGo RuntimeKind = "go"` to `internal/config/config.go`
- [x] add `RuntimeConfigGo` struct with `GoVersion string` field to `internal/config/config.go`
- [x] add `Go *RuntimeConfigGo` field to `RuntimeConfig` struct in `internal/config/config.go`
- [x] add `AppConfigGo` struct to `internal/binmanager/binmanager.go` with fields: PackageName, Version, Runtime, LockFile
- [x] add `Go *AppConfigGo` field to `App` struct in `internal/binmanager/binmanager.go`
- [x] run tests - must pass before next task

### Task 2: Add Go validation rules

- [x] write tests for Go app lockfile mandatory check (skip when flag set)
- [x] write tests for Go app runtime ref validation
- [x] write tests for Go runtime config validation (goVersion required, format valid)
- [x] write tests for files/links/archives allowed on Go apps
- [x] update `doValidateApps` in `internal/config/validate.go`: allow files/links/archives on Go apps (line 40-43 condition)
- [x] add mandatory lockFile check for Go apps (after line 101)
- [x] add runtime ref validation for Go apps (after line 118)
- [x] update `ValidateRuntimes` to validate Go runtime (goVersion required, valid format)
- [x] run tests - must pass before next task

### Task 3: Add Go hash computation

- [x] write tests for `calculateRuntimeHash` with Go runtime config
- [x] write tests for `calculateSystemRuntimeHash` with Go runtime config
- [x] write tests for `calculateAppHash` with Go app parameters
- [x] add Go case to `calculateRuntimeHash` in `internal/runtimemanager/hash.go` (append GoVersion to parts)
- [x] add Go case to `calculateSystemRuntimeHash` in `internal/runtimemanager/hash.go`
- [x] run tests - must pass before next task

### Task 4: Implement Go lockfile JSON wrapper

- [x] write tests for `parseGoLockFile`: valid JSON -> go.mod + go.sum extraction
- [x] write tests for `parseGoLockFile`: invalid JSON, missing fields, malformed input
- [x] write tests for `buildGoLockFileJSON`: go.mod + go.sum -> JSON string
- [x] write tests for roundtrip: build -> compress -> decompress -> parse
- [x] implement `parseGoLockFile(lockFile string) (goMod, goSum string, err error)` in new file `internal/runtimemanager/go.go`
- [x] implement `buildGoLockFileJSON(goMod, goSum string) (string, error)` in `internal/runtimemanager/go.go`
- [x] run tests - must pass before next task

### Task 5: Implement Go app installation

- [x] write tests for `getGoEnvVars`: verify GOPATH, GOMODCACHE, GONOSUMCHECK, GONOSUMDB, GOFLAGS
- [x] write tests for `getGoBinaryPath`: Linux vs Windows, path construction
- [x] write tests for `InstallGoApp` concurrency wrapper: sync.Once behavior, error cleanup
- [x] implement `getGoEnvVars(appEnvPath string) map[string]string` in `internal/runtimemanager/go.go`
- [x] implement `getGoBinaryPath(appEnvPath, packageName string) string` in `internal/runtimemanager/go.go`
- [x] implement `InstallGoApp` concurrency wrapper (following UV pattern from uv.go:32-44)
- [x] implement `installGoAppOnce`: resolve runtime, compute app path, check cache, create env, write go.mod+go.sum from lockfile, run `go build -trimpath -mod=readonly`, cleanup on error
- [x] implement `GetGoCommandInfo`: return CommandInfo with binary path
- [x] run tests - must pass before next task

### Task 6: Wire Go into RuntimeManager dispatch

- [x] write tests for `systemCommandForKind` returning "go" for RuntimeKindGo
- [x] write tests for `ComputeAppPath` with Go app
- [x] write tests for `GetCommandInfo` dispatch with Go app
- [x] write tests for `CollectRequiredRuntimes` collecting Go app runtime refs
- [x] add `RuntimeKindGo` case to `systemCommandForKind` in `internal/runtimemanager/runtimemanager.go`
- [x] add `app.Go != nil` branch to `ComputeAppPath` in `internal/runtimemanager/runtimemanager.go`
- [x] add `app.Go != nil` branch to `GetCommandInfo` in `internal/runtimemanager/runtimemanager.go`
- [x] add `app.Go != nil` collection to `CollectRequiredRuntimes` in `internal/runtimemanager/runtimemanager.go`
- [x] run tests - must pass before next task

### Task 7: Wire Go into BinManager dispatch

- [x] write tests for `GetCommandInfo` delegating Go apps to runtime manager
- [x] write tests for `ComputeInstallPath` with Go app
- [x] write tests for `GetAppsList` including Go apps
- [x] update `GetCommandInfo` in `internal/binmanager/binmanager.go` (line 463): add `app.Go` to runtime manager delegation
- [x] update `ComputeInstallPath` in `internal/binmanager/binmanager.go` (line 484): add `app.Go`
- [x] update `GetAppsList` in `internal/binmanager/binmanager.go` (line 694): add Go case
- [x] run tests - must pass before next task

### Task 8: Add Go support to config lockfile command

- [x] write tests for `clearAppLockFile` with Go app
- [x] write tests for `readLockFile` with Go app (reads go.mod + go.sum, produces JSON)
- [x] write tests for `listLockfileApps` including Go apps
- [x] write tests for `printAppInfo` with Go app
- [x] update command description in `cmd/config_lockfile.go` to mention Go
- [x] update `runConfigLockfile` (line 56-67): allow Go apps, add `app.Go != nil` check
- [x] update `clearAppLockFile` (line 125): add Go case to clear LockFile
- [x] update `printAppInfo` (line 148): add Go case
- [x] update `listLockfileApps` (line 183): add Go apps group
- [x] update `readLockFile` (line 221): add Go case -- read go.mod + go.sum, call `BuildGoLockFileJSON`
- [x] run tests - must pass before next task
- - scope addition: exported `buildGoLockFileJSON` -> `BuildGoLockFileJSON` so the `cmd` package can call it (was unexported); updated go_test.go refs
- - scope addition: added `RuntimeManager.GenerateGoLockFiles` (+ `getGoGenEnvVars`) in `internal/runtimemanager/go.go`. Reinstall cannot regenerate a Go lockfile because the build refuses without one (mandatory-lockfile); generation runs `go mod init` + `go get pkg@version` with `-mod=readonly` omitted (so deps can be written) while still force-clearing GONOSUMCHECK/GONOSUMDB. `runConfigLockfile` branches to this for Go apps instead of the reinstall path. Unit tests cover input/runtime validation; the network-dependent resolution is exercised by Task 10 / manual verification.

### Task 9: Update TypeScript declarations

- [x] add `AppConfigGo` interface to `internal/config/config.d.ts` with lockFile, packageName, runtime?, version
- [x] add `go?: AppConfigGo` to `interface App` in `internal/config/config.d.ts`
- [x] add `RuntimeConfigGo` interface with goVersion field
- [x] add `go?: RuntimeConfigGo` to `interface RuntimeConfig`
- [x] update `type RuntimeKind` to `"fnm" | "go" | "jvm" | "uv"`
- [x] run `go build` to verify embedded TS compiles correctly
- - scope note: `config/config.d.ts` is the canonical git-tracked source; `internal/config/config.d.ts` is a generated copy (`Taskfile.yaml`: `cp ./config/config.d.ts ./internal/config/config.d.ts`) embedded via go:embed. Applied identical edits to BOTH so the build copy isn't reverted. Verified declarations are valid TS via local `tsc --noEmit --strict` (exit 0).

### Task 10: Add govulncheck as default example Go app

- [x] add Go runtime definition to `internal/config/config.js` with managed binaries for Go SDK (linux/darwin, amd64/arm64)
- [x] add govulncheck app entry to `mapOfApps` in `internal/config/config.js` with packageName, version, and placeholder lockFile
- [x] generate lockfile for govulncheck using `datamitsu config lockfile govulncheck` and update config
- [x] run `go build` to verify embedded config is valid
- [x] run tests - must pass before next task
- - scope note: `internal/config/config.js` is a _generated_ artifact (`Taskfile.yaml` build:lib → tsdown compiles `config/src/*` then `sed` into `config.js`). Edited the canonical sources `config/src/runtimes.json` (added `go` runtime: kind/mode managed, `goVersion` 1.26.3, managed binaries for darwin+linux amd64/arm64 and windows/amd64, each with mandatory SHA-256 from go.dev's `?mode=json`, `extractDir: true`, `binaryPath` `go/bin/go`[.exe]) and `config/src/apps.ts` (added `govulncheck` → packageName `golang.org/x/vuln/cmd/govulncheck`, version `v1.3.0`), then regenerated `config.js` via `task build:lib`.
- - scope note: lockfile generated by running the real `./datamitsu config lockfile govulncheck` (downloaded managed go1.26.3 SDK with hash verification, ran `go mod init` + `go get`, emitted the brotli `br:` wrapper) and pasted into `config/src/apps.ts`. This also end-to-end validates Task 8's Go branch and the SDK download path (covers parts of Task 11).
- - scope addition: `cmd/exec.go` `typeOrder` was missing `"go"`, so Go apps were invisible in `datamitsu exec`'s listing. Added `"go"` (after `"jvm"`) so the example app is discoverable.
- - scope addition: added end-to-end tests in `internal/config/config_test.go` (`TestDefaultConfigGoRuntime`, `TestDefaultConfigGovulncheckApp`) that evaluate the embedded config and assert the go runtime (kind/mode/goVersion, mandatory hash+url+binaryPath on every managed binary, required platforms) and the govulncheck app (packageName/version, non-empty `br:` lockFile that decompresses to a JSON wrapper whose go.mod/go.sum reference golang.org/x/vuln).

### Task 11: Verify acceptance criteria

- [x] verify all Go runtime requirements implemented: managed/system mode, lockfile, hash verification
- [x] verify `config lockfile` generates valid JSON wrapper lockfile for Go apps
- [x] verify `go build -mod=readonly` fails when go.sum doesn't match (supply chain protection)
- [x] verify `GONOSUMCHECK` and `GONOSUMDB` are force-cleared in build env
- [x] run full test suite (`go test ./...`)
- [x] run linter - all issues must be fixed
- [x] verify test coverage meets project standard
- - managed/system mode: both supported via the shared runtime machinery — `systemCommandForKind` returns "go" (runtimemanager.go:58), `resolveEffectiveRuntimeConfig` handles musl→system fallback, and `GetRuntimePath` serves both system (PATH) and managed (download) modes. Hash verification: managed Go SDK downloads reuse `BinManager.Install` (mandatory SHA-256), and config validation rejects missing hashes. Lockfile: mandatory in both `validate.go` and `installGoAppOnce` (rejects empty before any build).
- - `config lockfile` JSON wrapper: covered by `cmd/config_lockfile_test.go` (`TestReadLockFile_Go`, `TestReadLockFile_GoMissingGoMod`, `TestPrintAppInfo_Go`, `TestListLockfileApps`) and exercised end-to-end in Task 10 by running the real `./datamitsu config lockfile govulncheck` (emitted the `br:` wrapper now embedded in config).
- - supply-chain protection: added automated hermetic test `TestGoBuildReadonlyFailsOnGoSumMismatch` in `internal/runtimemanager/go_test.go` — drives a real `go build` with datamitsu's own flags (`buildGoBuildArgs`) and env (`getGoEnvVars`) against a module with a missing/tampered go.sum and `GOPROXY=off`; the build fails with a `go.sum` error instead of silently rewriting. Mechanism also unit-tested (`TestBuildGoBuildArgs`).
- - `GONOSUMCHECK`/`GONOSUMDB` force-clear: `TestGetGoEnvVars` (build env) and `TestGetGoGenEnvVars` (generation env) assert both are set to "".
- - full suite `go test ./...`: ALL PASS. linter `golangci-lint run ./...`: 0 issues.
- - coverage: pure helpers in `go.go` are 75–100% (`parseGoLockFile`, `BuildGoLockFileJSON`, `getGoEnvVars`, `buildGoBuildArgs`, `getGoBinaryPath`, `GetGoAppPath`, `InstallGoApp`, `GetGoCommandInfo`). The lower-coverage `installGoAppOnce`/`GenerateGoLockFiles` are the network+SDK build/resolve paths (validated end-to-end in Task 10); package coverage (52.8%) is consistent with sibling runtimes whose download paths are likewise network-bound.

### Task 12: [Final] Update documentation

- [x] update README.md if needed
- [x] update project knowledge docs if new patterns discovered
- - README.md needs no change: it is intentionally minimal (states so on line 1), delegates all detail to `website/docs/`, and does not enumerate runtime kinds (fnm/uv/jvm), so adding Go there would be inconsistent with its scope.
- - Updated `docs/architecture.md` (the project knowledge doc) to add Go across: App Types (now 6 types — `go`), Runtime Manager intro + RuntimeConfig structs + mandatory-lockfile line + musl-fallback system binaries, two new bullets documenting the new patterns (the Go runtime build-from-source flow with GONOSUMCHECK/GONOSUMDB force-clear and `go build -trimpath -mod=readonly`; the `{"mod","sum"}` JSON lockfile wrapper distinct from FNM/UV single-artifact lockfiles), key files (`go.go`), `config lockfile` command (FNM/UV/Go + the separate `go mod init`+`go get` generation path), Key Data Flow (go dispatch branch), and Configuration Structure (`mapOfRuntimes` now UV/FNM/JVM/Go).
- - website docs (`supply-chain-security.md`, architecture diagrams) are explicitly deferred to a separate PR per Post-Completion — out of scope for this task.

## Technical Details

### AppConfigGo struct

```go
type AppConfigGo struct {
    PackageName string `json:"packageName"`       // e.g., "golang.org/x/vuln/cmd/govulncheck"
    Version     string `json:"version"`           // e.g., "v1.1.4"
    Runtime     string `json:"runtime,omitempty"`
    LockFile    string `json:"lockFile,omitempty"` // compressed JSON: {"mod":"...","sum":"..."}
}
```

### Lockfile JSON wrapper format

```json
{ "mod": "<full go.mod content>", "sum": "<full go.sum content>" }
```

Compressed with brotli level 11, base64-encoded, prefixed with `br:`.

### Go build environment variables

```go
map[string]string{
    "GOPATH":       filepath.Join(appEnvPath, "gopath"),
    "GOMODCACHE":   filepath.Join(appEnvPath, "gomodcache"),
    "GOBIN":        filepath.Join(appEnvPath, "bin"),
    "GONOSUMCHECK": "",  // force checksum DB verification
    "GONOSUMDB":    "",  // force checksum DB verification
    "GOFLAGS":      "-mod=readonly -trimpath",
}
```

### Installation flow

```
1. Resolve Go runtime -> download SDK if managed (SHA-256 verified)
2. Compute app path: .apps/go/{appName}/{xxh3-hash}/
3. Check cache -> skip if binary exists
4. Create app env dir + write files/archives
5. Decompress lockFile -> parse JSON -> write go.mod + go.sum
6. go build -trimpath -mod=readonly -o bin/{binaryName} {packageName}
   (-mod=readonly fails immediately if go.sum mismatch)
7. Binary available at .apps/go/{appName}/{hash}/bin/{binaryName}
```

### Config lockfile flow

```
1. go mod init datamitsu-{appName}
2. go get {packageName}@{version}  -> resolves transitive deps
3. Read go.mod + go.sum
4. JSON marshal -> {"mod":"...","sum":"..."}
5. Brotli compress -> base64 -> "br:..." -> output for config
```

## Post-Completion

**Manual verification:**

- Test `datamitsu config lockfile govulncheck` generates valid lockfile
- Test `datamitsu exec govulncheck ./...` runs successfully
- Test supply chain detection: tamper go.sum in lockfile and verify build fails
- Test system mode: verify `go` from PATH works when mode is "system"

**Documentation updates (separate PR):**

- Update `website/docs/guides/supply-chain-security.md` with Go runtime section
- Update `website/docs/guides/architecture/` docs with Go runtime diagrams
