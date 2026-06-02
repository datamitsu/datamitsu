# Global Runtime Config, Minimum Release Age & Install Timeout

## Overview

- Extract `minimumReleaseAge` from pnpm-only to a global default, apply age filtering to all `pull-*` commands (GitHub, npm, PyPI)
- Add per-app install timeout (600s default) with `DATAMITSU_INSTALL_TIMEOUT` env var override
- Expose the full effective runtime config snapshot via `datamitsu config runtime` CLI (introspection/debug only, not injected into JS VM)
- Expose a minimal allowlisted config-evaluation input surface to JS VM as `datamitsuConfigInputs` with only `minimumReleaseAgeMinutes` for now
- Users and security engineers can mechanically verify that env overrides are applied: `datamitsu config runtime | jq .minimumReleaseAgeMinutes`
- Document the Runtime Config Policy in CLAUDE.md

## Context (from discovery)

- Files/components involved:
  - `internal/pnpmdefaults/pnpmdefaults.go` — current home of `minimumReleaseAge: 10080`; must import from new `internal/runtimeconfig`
  - `internal/env/e.go`, `internal/env/env.go` — env var definitions and getters; add 2 new vars
  - `internal/github/client.go` — `Release` struct needs `PublishedAt`/`Prerelease`/`Draft`; add `ListReleases`, `GetLatestReleaseWithMinAge`
  - `internal/registry/npm.go` — add `GetNPMPackageInfoWithMinAge` (two-step: `/latest` first, full metadata only if fresh)
  - `internal/registry/pypi.go` — add `GetPyPIPackageInfoWithMinAge` with yanked/pre-release filtering
  - `cmd/devtools.go`, `cmd/devtools_fnm.go`, `cmd/devtools_uv.go`, `cmd/devtools_pull_runtimes.go` — integrate `--min-age` flag
  - `internal/binmanager/binmanager.go`, `internal/binmanager/download.go` — thread `context.WithTimeout` for install timeout
  - `internal/runtimemanager/runtimemanager.go`, `fnm.go`, `uv.go`, `jvm.go` — thread context for runtime install timeout
  - `internal/engine/configinputs.go` — inject minimal `datamitsuConfigInputs` frozen JS global (allowlisted subset of runtime config)
  - `cmd/config.go` — add `config runtime` subcommand
  - `config/config.d.ts`, `internal/config/config.d.ts` — TypeScript declarations
  - `CLAUDE.md` — Runtime Config Policy section
- Related patterns found:
  - `internal/engine/pnpm.go` — existing pattern for Go-to-JS injection (sorted keys, `Object.freeze`)
  - `internal/env/e.go` — `envVar` struct pattern for env vars
  - `cmd/config.go` — existing `config show` and `config types` subcommands
- Dependencies: `github.com/dop251/goja`, `github.com/spf13/cobra`, `golang.org/x/mod/semver`

## Development Approach

- **Testing approach**: Regular (code first, then tests in each task)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**
- Use `Compute()` for env-sensitive unit tests. Use `resetForTesting()` before `Init`/`Get` lifecycle tests that depend on env changes

## Testing Strategy

- **No key count guards** — test "required keys exist" and "JSON serialization stable", not total count
- **Struct field assertions** — test `Effective` struct fields directly (compile-time checked), not map key strings
- **Unit tests**: required for every task
  - `internal/runtimeconfig/runtimeconfig_test.go` — pin constants, struct JSON round-trip stability, `Compute()` default values and env overrides, `Get()` before-Init error path
  - `internal/env/env_test.go` — new getter defaults, env var overrides, invalid values
  - `internal/github/client_test.go` — `PublishedAt` parsing, draft/zero-time handling, `ListReleases`, `GetLatestReleaseWithMinAge` scenarios
  - `internal/registry/npm_test.go` — `GetNPMPackageInfoWithMinAge` two-step, pre-release skip (hyphen + semver tags), build metadata not skipped (`1.2.3+build.1`), scoped packages, missing time, fast path
  - `internal/registry/pypi_test.go` — `GetPyPIPackageInfoWithMinAge`, PEP 440 pre-release (`(?i)(a|b|rc)\d+|\.dev\d+`), yanked multi-file, `upload_time_iso_8601`
  - `internal/engine/configinputs_test.go` — injects only allowlisted config inputs, frozen, sorted keys, plain object, does NOT expose `installTimeoutSeconds`/`logLevel`/`timings`/`concurrency`/`maxCmdLength`/`maxErrorCmdDisplay`/`maxParallelWorkers`
- **Integration**: `go test ./...` after each task
- **Race detection**: `go test -race ./internal/runtimeconfig/... ./internal/engine/... ./cmd/...` in final verification

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with + prefix
- Document issues/blockers with ! prefix
- Update plan if implementation deviates from original scope

## Key Design Decisions

1. `datamitsuConfigInputs` separate from `pnpmWorkspaceDefaults` — orthogonal concerns
2. Key names with explicit unit suffixes (`minimumReleaseAgeMinutes`, `installTimeoutSeconds`)
3. `--min-age` flag: `-1` = global default, `0` = disable, positive = custom
4. Install timeout: env var override, not CLI flag — applies uniformly to init, exec, etc.
5. "No release old enough": existing app = warn + keep current; new app = **hard error** (use `--min-age 0`)
6. endoflife.date excluded from age filtering — major version line selections
7. Effective values, not compile-time — CLI shows what the program runs with right now
8. No circular dependency: `runtimeconfig` -> `env` (one-way); `env` uses literal fallbacks
9. npm two-step fetching: lightweight `/latest` first, full metadata only if too fresh
10. **Typed struct, not `map[string]any`** — `runtimeconfig.Effective` struct with `json` tags provides compile-time guarantees, prevents accidental key name breakage. No `ToMap()` — map conversion for goja is internal to engine layer
11. **No key count guards in tests** — test "required keys exist" + "stable JSON serialization", not total count. Adding a new default should NOT require updating count assertions
12. **Introspectable by design** — all runtime parameters must be programmatically queryable via `datamitsu config runtime`. Documented as policy in CLAUDE.md
13. **Effective is immutable-by-convention** — `Get()` returns a struct copy; caller mutation is safe and does not affect internal state. Internal state is immutable after `Init()`. Go value semantics = free immutability
14. **Package name `runtimeconfig`, not `defaults`** — this layer is effective runtime configuration (env-resolved values, execution limits, runtime policy surface, introspection API), not just compile-time defaults
15. **Idempotent `Init()`** — repeated calls are no-ops, not errors. Cobra tests often execute root command multiple times in one process; embedded/daemon/watch workflows may call `Init()` again. `resetForTesting()` is the escape hatch for tests that need fresh state
16. **Simple sync: `sync.RWMutex` + `bool`** — `Get()` is not a hot path (no millions of calls/sec). Simplicity of reasoning > lock-free performance. `RWMutex` allows concurrent reads, mutex protects writes in `Init()`
17. **Full runtime config is CLI-only, JS VM gets minimal allowlisted inputs** — `datamitsu config runtime` exposes the full `Effective` struct for introspection/debug. JS VM receives only `datamitsuConfigInputs` — a tiny allowlisted surface with only fields that config JS is explicitly allowed to branch on. Currently: `minimumReleaseAgeMinutes`. Full `Effective` is NOT injected into JS VM. This prevents hidden config inputs that break fingerprinting/cache/explain/provenance. Policy "don't use this for branching" is unenforceable if the full object is available; minimal allowlist enforces it structurally
18. **`--min-age` resolves through `runtimeconfig.Effective`** — `resolveMinAge()` reads from `runtimeconfig.Get()`, not from `env.MinimumReleaseAgeMinutes()` directly. Single source of truth for effective runtime values
19. **Install timeout: `0` = disabled** — `DATAMITSU_INSTALL_TIMEOUT=0` means no timeout (no deadline on context), not `context.WithTimeout(ctx, 0)`. Always call `cancel()`. Use `http.NewRequestWithContext`, `exec.CommandContext`. Clean up partial downloads / temp dirs on timeout. Distinguish `context.DeadlineExceeded` from other install errors. Note: `exec.CommandContext` kills the direct process but may not kill grandchildren; consider Unix process-group cleanup later if tools spawn subprocess trees
20. **`runtimeconfig` is the foundation for future runtime policy** — this is the base introspection layer for future OCI materialization policy, offline/network policy, source mode activation policy, and structured event protocol metadata. Not implementing those now, but the architecture must not preclude them
21. **`datamitsuConfigInputs` is a bounded config evaluation input surface** — unlike introspection-only globals, any field in `datamitsuConfigInputs` IS a config evaluation input. Adding a new field requires updating: config fingerprinting, cache invalidation, explain/debug metadata, future provenance metadata, CLAUDE.md policy, TS declarations, and tests
22. **Config evaluation is not cached today** — JS config is re-evaluated fresh every invocation (`cmd/config_loader.go` → goja VM). Therefore branching on `datamitsuConfigInputs.minimumReleaseAgeMinutes` is safe now — no stale cache risk. When config evaluation caching is implemented, `datamitsuConfigInputs` values MUST be included in the cache fingerprint. This is a forward contract documented in `initConfigInputs()` code comments

## Implementation Steps

### Task 1: Create `internal/runtimeconfig` package with compile-time constants and `Effective` struct

- [x] create `internal/runtimeconfig/runtimeconfig.go` with `MinimumReleaseAgeMinutes = 10080` and `InstallTimeoutSeconds = 600` constants
- [x] define `Effective` struct with typed fields and `json` tags: `Concurrency int`, `InstallTimeoutSeconds int`, `LogLevel string`, `MaxCmdLength int`, `MaxErrorCmdDisplay int`, `MaxParallelWorkers int`, `MinimumReleaseAgeMinutes int`, `Timings bool`
- [x] NO `ToMap()` method — the struct is the public API. `map[string]any` conversion is internal to engine via `json.Marshal`/`json.Unmarshal`
- [x] write tests: pin constant values (10080, 600), `Effective` struct JSON round-trip stability (required keys present in serialized output)
- [x] run `go test ./internal/runtimeconfig/...` — must pass before next task

### Task 2: Add env var definitions and getters for new defaults

- [x] add `installTimeout` and `minimumReleaseAge` envVar definitions in `internal/env/e.go` with literal defaults `"600"` and `"10080"`
- [x] add `InstallTimeoutSeconds() int` getter in `internal/env/env.go` (0 = disable, negative = fallback to default)
- [x] add `MinimumReleaseAgeMinutes() int` getter in `internal/env/env.go` (0 = disable, negative = fallback to default)
- [x] write tests for both getters: default value, env override, invalid value handling, zero = valid (means disabled)
- [x] run `go test ./internal/env/...` — must pass before next task

### Task 3: Add `Compute()` / `Init()` / `Get()` and wire pnpmdefaults

Three-layer API: pure computation + idempotent lifecycle + cached getter. Thread-safe via `sync.RWMutex`.

- [x] add `Compute() Effective` — pure function, reads `env` getters, returns struct. No global state, no side effects. Tests use this directly
- [x] add package-level `var effective Effective`, `var initialized bool`, `var mu sync.RWMutex`
- [x] add `Init() error` — idempotent: if already initialized, return nil (no-op). Lock with `mu.Lock()`, compute, set `initialized = true`, unlock. Safe for repeated Cobra command execution and embedded usage
- [x] add `Get() (Effective, error)` — `mu.RLock()`, returns error if `!initialized` (zero-value struct has `MinimumReleaseAgeMinutes=0` = security filtering disabled); returns cached copy
- [x] add `resetForTesting()` unexported helper — resets `initialized` for testing `Get` before-Init error path
- [x] add `cobra.OnInitialize` callback in `cmd/root.go` that calls `runtimeconfig.Init()` and exits on error
- [x] modify `internal/pnpmdefaults/pnpmdefaults.go`: import `internal/runtimeconfig`, replace hardcoded `10080` with `runtimeconfig.MinimumReleaseAgeMinutes`
- [x] write tests via `Compute()`: returns expected default values; set env → override reflected in struct fields
- [x] write test: `Get()` without `Init()` returns error
- [x] write test: double `Init()` returns nil (idempotent, not error)
- [x] write test: `env.InstallTimeoutSeconds()` == `runtimeconfig.InstallTimeoutSeconds` (no env override)
- [x] run `go test ./internal/runtimeconfig/... ./internal/pnpmdefaults/... ./internal/engine/...` — must pass before next task

### Task 4: Add `PublishedAt`/`Prerelease`/`Draft` to GitHub Release and `ListReleases`

- [x] add `PublishedAt time.Time`, `Prerelease bool`, and `Draft bool` fields to `Release` struct in `internal/github/client.go`
- [x] add `doJSONRequest(url string, target any) error` generic helper, refactor `doRequest` to use it
- [x] add `ListReleases(owner, repo string, perPage int) ([]Release, error)` with retry logic
- [x] write test: `PublishedAt` parsing from ISO 8601 JSON
- [x] write test: `PublishedAt` zero value (missing `published_at` in JSON) is handled gracefully
- [x] write test: `Draft` field is parsed correctly
- [x] write test: `ListReleases` URL construction and result decoding
- [x] run `go test ./internal/github/...` — must pass before next task

### Task 5: Add `GetLatestReleaseWithMinAge` to GitHub client

- [x] add `GetLatestReleaseWithMinAge(owner, repo string, minAgeMinutes int) (*Release, error)` — fetches 30 releases, filters by age, prerelease, and draft; skips releases with zero `PublishedAt`; returns `(nil, nil)` if none qualifies
- [x] write test: selects older release when latest is too fresh
- [x] write test: skips prerelease even if old enough
- [x] write test: skips draft releases
- [x] write test: skips releases with zero `PublishedAt`
- [x] write test: `minAgeMinutes=0` falls through to `GetLatestRelease`
- [x] write test: returns `(nil, nil)` when no release is old enough
- [x] run `go test ./internal/github/...` — must pass before next task

### Task 6: Add `GetNPMPackageInfoWithMinAge` (two-step strategy)

- [x] add `npmFullResponse` type with `DistTags` and `Time` maps in `internal/registry/npm.go`
- [x] add `GetNPMPackageInfoWithMinAge(packageName string, minAgeMinutes int)` — step 1: fetch `/latest`, check age; step 2: if too fresh, fetch full `/{package}` metadata, sort versions by time, return latest old enough
- [x] handle version string normalization: npm versions don't have `v` prefix, use string-based pre-release detection (contains `-`), not `golang.org/x/mod/semver` which requires `v` prefix. Build metadata (`+`) is NOT pre-release per SemVer spec
- [x] skip non-version keys in `Time` map (`created`, `modified`)
- [x] handle scoped package URL encoding (`@scope/name` → `@scope%2fname`)
- [x] write test: latest is old enough — fast path, no full metadata fetch
- [x] write test: latest is too fresh — falls back to older version from full metadata
- [x] write test: skips pre-release versions (`1.2.3-beta.1`, `2.0.0-rc.0`)
- [x] write test: `1.2.3+build.1` is NOT skipped (build metadata is not pre-release)
- [x] write test: handles missing `time[version]` gracefully (skip version)
- [x] write test: handles latest dist-tag pointing to pre-release
- [x] write test: `minAgeMinutes=0` returns latest (no filtering)
- [x] write test: returns `(nil, nil)` when no version is old enough
- [x] run `go test ./internal/registry/...` — must pass before next task

### Task 7: Add `GetPyPIPackageInfoWithMinAge`

- [ ] add `pypiFullResponse` and `pypiReleaseFile` types in `internal/registry/pypi.go`
- [ ] add `GetPyPIPackageInfoWithMinAge(packageName string, minAgeMinutes int)` — parse `releases` with `upload_time_iso_8601` (preferred) or `upload_time`, skip yanked and PEP 440 pre-release versions
- [ ] implement PEP 440 pre-release detection via conservative regex `(?i)(a|b|rc)\d+|\.dev\d+` — matches `1.0.0a1`, `1.0.0b2`, `1.0.0rc1`, `1.0.0.dev3`
- [ ] for versions with multiple files: version is yanked only if ALL files are yanked; use earliest `upload_time` among non-yanked files
- [ ] write test: selects older version when latest is too fresh
- [ ] write test: skips PEP 440 pre-release versions (`rc`, `a`, `b`, `dev`)
- [ ] write test: skips fully yanked releases
- [ ] write test: version with mixed yanked/non-yanked files is NOT skipped
- [ ] write test: `minAgeMinutes=0` returns latest
- [ ] write test: returns `(nil, nil)` when no version is old enough
- [ ] run `go test ./internal/registry/...` — must pass before next task

### Task 8: Create shared `--min-age` flag infrastructure

- [ ] create `cmd/devtools_min_age.go` with `addMinAgeFlag(cmd) *int`, `resolveMinAge(flagValue int, eff runtimeconfig.Effective) int` (reads from `eff.MinimumReleaseAgeMinutes`, not `env` directly), and `minAgeDescription() string`
- [ ] `resolveMinAge`: `-1` = use `eff.MinimumReleaseAgeMinutes`, `0+` = as-is
- [ ] run `go build` — must compile before next task

### Task 9: Integrate `--min-age` into `pull-github`

- [ ] register `--min-age` flag on `pullGithubCmd` in `cmd/devtools.go`
- [ ] get `runtimeconfig.Get()` at command start, pass `eff` to `resolveMinAge`
- [ ] modify `runPullGithub`: use `GetLatestReleaseWithMinAge` when resolved min age > 0
- [ ] handle nil return: existing app = warn + keep tag; new app = hard error
- [ ] print min age in status banner
- [ ] run `go test ./cmd/...` — must pass before next task

### Task 10: Integrate `--min-age` into `pull-fnm`

- [ ] register `--min-age` flag on `pullFNMCmd` in `cmd/devtools_fnm.go`
- [ ] get `runtimeconfig.Get()` at command start, pass `eff` to `resolveMinAge`
- [ ] modify `runPullFNM`: use `GetNPMPackageInfoWithMinAge` with resolved min age
- [ ] handle nil return: skip package with warning
- [ ] print min age in status banner
- [ ] run `go test ./cmd/...` — must pass before next task

### Task 11: Integrate `--min-age` into `pull-uv`

- [ ] register `--min-age` flag on `pullUVCmd` in `cmd/devtools_uv.go`
- [ ] get `runtimeconfig.Get()` at command start, pass `eff` to `resolveMinAge`
- [ ] modify `runPullUV`: use `GetPyPIPackageInfoWithMinAge` with resolved min age
- [ ] handle nil return: skip package with warning
- [ ] print min age in status banner
- [ ] run `go test ./cmd/...` — must pass before next task

### Task 12: Integrate `--min-age` into `pull-runtimes`

- [ ] register `--min-age` flag on `pullRuntimesCmd` in `cmd/devtools_pull_runtimes.go`
- [ ] get `runtimeconfig.Get()` at command start, pass `eff` to `resolveMinAge`
- [ ] change `pullFNMRuntime`, `pullUVRuntime`, `pullJVMRuntime` signatures to accept `minAge int`
- [ ] apply age-aware calls inside each function (GitHub + npm), NOT to endoflife.date calls
- [ ] handle nil returns with clear error messages
- [ ] update call sites in `runPullRuntimes` to pass resolved min age
- [ ] run `go test ./cmd/...` — must pass before next task

### Task 13: Thread install timeout through binmanager

- [ ] modify `InstallWithConcurrency` in `internal/binmanager/binmanager.go`: wrap each per-app goroutine with `context.WithTimeout` using `env.InstallTimeoutSeconds()`. If timeout == 0, use parent context without deadline (disabled)
- [ ] always call `cancel()` via `defer cancel()` after `context.WithTimeout`
- [ ] add `context.Context` parameter to `installApp()` (or equivalent internal download function)
- [ ] modify `downloadFile()` in `internal/binmanager/download.go` to create HTTP requests via `http.NewRequestWithContext(ctx, ...)`
- [ ] on timeout: clean up partial downloads and temp dirs before returning error
- [ ] produce clear timeout error message: distinguish `context.DeadlineExceeded` ("installation timed out after Xs") from other install errors
- [ ] write test: verify timeout=0 means no deadline (context has no deadline set)
- [ ] write test: timeout path exits correctly, cleans up partial files, and does not leak resources
- [ ] run `go test ./internal/binmanager/...` — must pass before next task

### Task 14: Thread install timeout through runtimemanager

- [ ] modify runtime install methods in `internal/runtimemanager/runtimemanager.go` to accept `context.Context`
- [ ] thread context through `internal/runtimemanager/fnm.go`: `installFNMAppOnce` uses `exec.CommandContext` for subprocess calls. Note: `exec.CommandContext` kills the direct process but may not kill grandchildren; consider Unix process-group cleanup later if tools spawn subprocess trees
- [ ] thread context through `internal/runtimemanager/uv.go`: UV app installation uses context for downloads and subprocesses
- [ ] thread context through `internal/runtimemanager/jvm.go`: JVM installation uses context for downloads
- [ ] on timeout: clean up partial extractions and temp dirs
- [ ] write test: verify context is propagated to download calls
- [ ] write test: subprocess uses `exec.CommandContext` (timeout kills child process)
- [ ] run `go test ./internal/runtimemanager/...` — must pass before next task

### Task 15: Add `datamitsu config runtime` CLI command

- [ ] add `configRuntimeCmd` in `cmd/config.go` — calls `runtimeconfig.Get()`, handles error, then `json.MarshalIndent(eff, "", "  ")`
- [ ] register in `init()` alongside existing `config show` and `config types`
- [ ] write test: command produces valid JSON, required keys present (`concurrency`, `installTimeoutSeconds`, etc.)
- [ ] write test: env override is reflected in output
- [ ] run `go test ./cmd/...` — must pass before next task

### Task 16: Inject minimal `datamitsuConfigInputs` into JS VM

- [ ] create `internal/engine/configinputs.go` with `initConfigInputs()` method
- [ ] define engine-internal struct for the allowlisted input surface:
  ```go
  type configInputs struct {
      MinimumReleaseAgeMinutes int `json:"minimumReleaseAgeMinutes"`
  }
  ```
- [ ] compute from `runtimeconfig.Get()` — extract only allowlisted fields
- [ ] inject as frozen JS global named `datamitsuConfigInputs` with sorted keys, `Object.freeze()`
- [ ] do NOT inject full `runtimeconfig.Effective` into JS VM
- [ ] add `e.initConfigInputs()` call in `engine.New()` after `initPNPMWorkspaceDefaults()`
- [ ] write test: initialize runtimeconfig via `resetForTesting()` + `Init()`, verify injected `minimumReleaseAgeMinutes` matches `runtimeconfig.Get().MinimumReleaseAgeMinutes`
- [ ] write test: object is plain (not function/array)
- [ ] write test: object is frozen (mutation attempts blocked)
- [ ] write test: keys are in sorted order
- [ ] write test: only `minimumReleaseAgeMinutes` is exposed — `installTimeoutSeconds`, `logLevel`, `timings`, `concurrency`, `maxCmdLength`, `maxErrorCmdDisplay`, `maxParallelWorkers` are NOT present
- [ ] document fingerprinting status: config JS evaluation is NOT cached (re-evaluated fresh every invocation via `cmd/config_loader.go`), so branching on `datamitsuConfigInputs` is safe today — no stale cache risk. When config evaluation caching is implemented, `datamitsuConfigInputs` values MUST be included in the cache fingerprint key. Add a code comment in `initConfigInputs()` noting this forward contract
- [ ] run `go test ./internal/engine/...` — must pass before next task

### Task 17: Add TypeScript declarations for `datamitsuConfigInputs`

- [ ] add `datamitsuConfigInputs` declaration in `config/config.d.ts`:
  ```typescript
  /**
   * Bounded config-evaluation input surface.
   * Only fields explicitly allowlisted here may be used in config JS decisions.
   * Adding a field here requires updating fingerprinting, cache invalidation,
   * explain/debug output, and documentation.
   */
  declare const datamitsuConfigInputs: Readonly<{
    minimumReleaseAgeMinutes: number;
  }>;
  ```
- [ ] add identical declaration in `internal/config/config.d.ts` (both files kept identical)
- [ ] run `go test ./internal/config/...` — must pass before next task

### Task 18: Update CLAUDE.md with Runtime Config Policy and Introspectable by Design

- [ ] add "Runtime Config Policy" section after "JS<>Go Shared Constants Policy" in `CLAUDE.md`
- [ ] document: `internal/runtimeconfig` as single source of truth, typed `Effective` struct as contract, `Get()` for CLI, env override pattern, checklist for adding new defaults
- [ ] add "Runtime Config vs Config Inputs Policy":
  - `runtimeconfig.Effective` is the full effective runtime snapshot, exposed via `datamitsu config runtime`
  - It must NOT be injected wholesale into config JS VM
  - JS config VM receives only explicit allowlisted config inputs via `datamitsuConfigInputs`
  - Current allowlist: `minimumReleaseAgeMinutes`
  - Adding a new config input requires updating: fingerprinting, cache invalidation, explain/debug output, tests, TS declarations, documentation
  - Current status: config JS evaluation is not cached (re-evaluated fresh every invocation). Branching on `datamitsuConfigInputs` is safe today. When config evaluation caching is implemented, values MUST be in the cache fingerprint
- [ ] add "Introspectable by Design" policy: all runtime parameters must be programmatically queryable via CLI; use typed structs for public API surfaces, not `map[string]any`

### Task 19: Verify acceptance criteria

- [ ] verify all goals from Overview are implemented
- [ ] verify edge cases: no release old enough (existing app = warn, new app = error), `--min-age 0` bypass, `DATAMITSU_INSTALL_TIMEOUT=0` disables timeout (no deadline), timeout cleanup removes partial files
- [ ] run full test suite (`go test ./...`)
- [ ] run `go test -race ./internal/runtimeconfig/... ./internal/engine/... ./cmd/...`
- [ ] run linter — all issues must be fixed
- [ ] manual verification: `go build && ./datamitsu config runtime` outputs expected keys
- [ ] manual verification: `DATAMITSU_INSTALL_TIMEOUT=1200 ./datamitsu config runtime | jq .installTimeoutSeconds` returns 1200
- [ ] manual verification: JS VM has `datamitsuConfigInputs.minimumReleaseAgeMinutes` but NOT `datamitsuConfigInputs.logLevel`

### Task 20: [Final] Update documentation

- [ ] update `website/docs/guides/supply-chain-security.md` — document global minimum release age, `--min-age` flag
- [ ] update `website/docs/how-to/maintain-wrapper.md` — mention `--min-age`, `datamitsu config runtime`, `datamitsuConfigInputs` JS global

## Technical Details

**`Effective` struct (full runtime config — CLI-only, not injected into JS VM):**

```go
type Effective struct {
    Concurrency              int    `json:"concurrency"`
    InstallTimeoutSeconds    int    `json:"installTimeoutSeconds"`
    LogLevel                 string `json:"logLevel"`
    MaxCmdLength             int    `json:"maxCmdLength"`
    MaxErrorCmdDisplay       int    `json:"maxErrorCmdDisplay"`
    MaxParallelWorkers       int    `json:"maxParallelWorkers"`
    MinimumReleaseAgeMinutes int    `json:"minimumReleaseAgeMinutes"`
    Timings                  bool   `json:"timings"`
}

// Compute reads env getters and returns a fresh Effective.
// Pure function — no global state, no side effects. Tests use this directly.
func Compute() Effective {
    return Effective{
        Concurrency:              env.GetConcurrency(),
        InstallTimeoutSeconds:    env.InstallTimeoutSeconds(),
        LogLevel:                 env.GetLogLevel().String(),
        MaxCmdLength:             env.GetMaxCommandLength(),
        MaxErrorCmdDisplay:       env.GetMaxErrorCommandDisplay(),
        MaxParallelWorkers:       env.GetMaxParallelWorkers(),
        MinimumReleaseAgeMinutes: env.MinimumReleaseAgeMinutes(),
        Timings:                  env.IsTimingsEnabled(),
    }
}

var (
    mu          sync.RWMutex
    effective   Effective
    initialized bool
)

// Init caches Compute() result. Idempotent — repeated calls are no-ops.
func Init() error {
    mu.Lock()
    defer mu.Unlock()
    if initialized {
        return nil
    }
    effective = Compute()
    initialized = true
    return nil
}

// Get returns a copy of the cached value. Returns error if Init() was not called.
// Caller mutation is safe — returned struct is a copy, internal state is immutable after Init.
func Get() (Effective, error) {
    mu.RLock()
    defer mu.RUnlock()
    if !initialized {
        return Effective{}, fmt.Errorf("runtimeconfig: Get called before Init")
    }
    return effective, nil
}
```

**Config inputs struct (engine-internal, injected into JS VM):**

```go
// in internal/engine/configinputs.go
type configInputs struct {
    MinimumReleaseAgeMinutes int `json:"minimumReleaseAgeMinutes"`
}
```

**Initialization:** `cobra.OnInitialize(func() { if err := runtimeconfig.Init(); err != nil { ... os.Exit(1) } })` in `cmd/root.go` — runs once after flag parsing, before any command handler. Idempotent — safe for repeated Cobra command execution in tests.

**JSON output from `datamitsu config runtime` (no env overrides):**

```json
{
  "concurrency": 3,
  "installTimeoutSeconds": 600,
  "logLevel": "info",
  "maxCmdLength": 32000,
  "maxErrorCmdDisplay": 120,
  "maxParallelWorkers": 12,
  "minimumReleaseAgeMinutes": 10080,
  "timings": false
}
```

**API usage:**

- CLI: `eff, err := runtimeconfig.Get()` → check err → `json.MarshalIndent(eff, "", "  ")` — full `Effective` struct, direct serialization
- JS VM (engine-internal): extract allowlisted fields into `configInputs` struct → `json.Marshal` → `json.Unmarshal` into `map[string]any` → sorted keys → `Object.freeze()` → inject as `datamitsuConfigInputs`. Only `minimumReleaseAgeMinutes` for now
- `--min-age` resolution: `eff, _ := runtimeconfig.Get()` → `resolveMinAge(flagValue, eff)` — reads from Effective, not from `env` directly
- Tests: assert on struct fields directly (compile-time checked), not map key strings

**Two surfaces:**

```
runtimeconfig.Effective           datamitsuConfigInputs
  full effective snapshot           tiny allowlisted JS VM input
  CLI introspection only            bounded config evaluation surface
  `datamitsu config runtime`        fingerprint/cache/explain relevant
  8 fields (current scope)          1 field: minimumReleaseAgeMinutes
```

**Dependency direction:** `internal/runtimeconfig` -> `internal/env` (one-way, no cycle). `env` uses literal fallback values, does NOT import `runtimeconfig`.

**Install timeout semantics:**

- `DATAMITSU_INSTALL_TIMEOUT=0` → no timeout (parent context without deadline)
- `DATAMITSU_INSTALL_TIMEOUT=600` → 10 minute per-app timeout
- Always `defer cancel()` after `context.WithTimeout`
- HTTP: `http.NewRequestWithContext(ctx, ...)`
- Subprocess: `exec.CommandContext(ctx, ...)` — kills direct process; grandchild cleanup deferred to future if needed
- On `context.DeadlineExceeded`: cleanup partial downloads/temp dirs, report "installation timed out after Xs"

**Age filtering scope (pull-runtimes):**

| Call site                                               | Registry       | Age filter? | Reason             |
| ------------------------------------------------------- | -------------- | ----------- | ------------------ |
| `pullFNMRuntime` -> `GetLatestNodeLTSVersion()`         | endoflife.date | No          | Major version line |
| `pullFNMRuntime` -> `GetNPMPackageInfo("pnpm")`         | npm            | Yes         | Specific version   |
| `pullFNMRuntime` -> `GetLatestRelease("Schniz","fnm")`  | GitHub         | Yes         | Binary release     |
| `pullUVRuntime` -> `GetLatestPythonStableVersion()`     | endoflife.date | No          | Major version line |
| `pullUVRuntime` -> `GetLatestRelease("astral-sh","uv")` | GitHub         | Yes         | Binary release     |
| `pullJVMRuntime` -> `GetLatestTemurinMajorVersion()`    | adoptium API   | No          | Major version      |
| `pullJVMRuntime` -> `GetLatestRelease("adoptium",repo)` | GitHub         | Yes         | Binary release     |

**npm two-step strategy:**

1. Fetch `/{package}/latest` (~1KB) — if version age >= cutoff, return immediately
2. Only if too fresh: fetch full `/{package}` (may be MBs), find latest old enough version

**npm version detection:** Pre-release = contains `-` (e.g., `1.2.3-beta.1`). Build metadata `+` is NOT pre-release (`1.2.3+build.1` is stable). Do NOT use `golang.org/x/mod/semver` — requires `v` prefix.

**PyPI PEP 440 pre-release detection:** Conservative regex `(?i)(a|b|rc)\d+|\.dev\d+` — matches `1.0.0a1`, `1.0.0b2`, `1.0.0rc1`, `1.0.0.dev3`.

## Post-Completion

**Manual verification:**

- Build and run `datamitsu config runtime` — verify expected keys present
- Run with env overrides (`DATAMITSU_INSTALL_TIMEOUT=1200`, `DATAMITSU_MIN_RELEASE_AGE=1440`, `DATAMITSU_CONCURRENCY=8`) — verify reflected in output
- Run `datamitsu devtools pull-github --update --min-age 0` — verify age bypass works
- Run `datamitsu devtools pull-fnm --update` — verify min age banner and filtering
- Test install timeout with `DATAMITSU_INSTALL_TIMEOUT=5 datamitsu init` (should timeout quickly)
- Verify JS VM: `datamitsuConfigInputs.minimumReleaseAgeMinutes` exists, `datamitsuConfigInputs.logLevel` does NOT
