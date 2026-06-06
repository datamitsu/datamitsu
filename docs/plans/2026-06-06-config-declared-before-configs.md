# Config-declared local before-configs (`getBeforeConfigs()`)

## Overview

Add a JS-declared equivalent of the `--before-config` CLI flag, exposed as a
`getBeforeConfigs()` function in the project's git-root `datamitsu.config.*`.

**Problem it solves.** Today the npm wrapper
`@shibanet0/datamitsu-config/bin/datamitsu.js` injects
`--before-config <shared config path>` when datamitsu is run via pnpm. A
**globally installed** datamitsu run inside the same repo never receives that
flag, so it does not load the shared config — behaviour diverges, the user must
hand-edit the command, and debugging is harder. `getBeforeConfigs()` lets the
git-root config itself declare the shared config as an under-layer, so a global
datamitsu reproduces the wrapper's behaviour automatically.

**Key benefits.**

- `datamitsu` works identically whether invoked through the pnpm wrapper or a
  global install, from inside the repo.
- Easier debugging: no command rewriting, can link a config repo and run the
  global binary directly.

**How it integrates.** Mirrors the existing `getRemoteConfigs()` surface, but for
local files. Resolution is implemented as a **pre-pass** (Approach B): the
auto-discovered config is evaluated once to read `getBeforeConfigs()`, and the
declared paths are inserted into the config source list **before** the auto
source — so the rest of the loader processes them uniformly and they get exact
parity with `--before-config` (init-layer merging, `getMinVersion` checks,
`currentConfig` flow).

### Decided semantics (locked)

- **API shape:** `getBeforeConfigs()` returning `[{ path: string }]` (mirror of
  `getRemoteConfigs()`).
- **Scope of honouring:** only the auto-discovered git-root config. Declared in
  any other layer (default / `--before-config` / remote / `--config`) → ignored
  (by construction — the pre-pass only reads it from the auto config).
- **Precedence:** if `--before-config` is passed on the CLI, `getBeforeConfigs()`
  is **not evaluated at all** (the flag wins; avoids double-loading the shared
  config when the wrapper is used).
- **Path resolution:** relative to the directory of the git-root config file;
  absolute paths used as-is.
- **No hash required:** local files are in the same trust domain as the root
  config (the mandatory-hash policy in `CLAUDE.md` applies to network downloads;
  a local path is not a download).

### Out of scope (this plan)

- Declarative parity for `--binary-command` / `DATAMITSU_PACKAGE_NAME` that the
  wrapper also injects. Tracked as a possible follow-up (see Post-Completion).

## Context (from discovery)

- Files/components involved:
  - [cmd/config_loader.go](../../cmd/config_loader.go) — `loadConfigImpl`
    (source assembly, `cmd/config_loader.go:77-117`), `processConfigSource`
    (remote-config pre-pass it mirrors, `cmd/config_loader.go:265-310`),
    `discoverAutoConfig`, `loadConfigFile`.
  - [cmd/config_loader_test.go](../../cmd/config_loader_test.go) — test harness:
    `loadConfigWithPaths(...)`, isolated `discoverAutoConfig(dir)` tests,
    `TestBeforeConfigOrdering` temp-dir pattern, `TestProcessConfigSourceRemote*`.
  - `config/config.d.ts` and embedded copy `internal/config/config.d.ts` — TS
    ambient declarations for config authors (kept in sync per `CLAUDE.md`).
- Related patterns found:
  - `getRemoteConfigs()` resolution in `processConfigSource`
    (`cmd/config_loader.go:265-310`) — the structural template (eval JS fn →
    `ExportTo` entries → validate → resolve → chain). The new feature reuses the
    _shape_ but runs as a pre-pass, not inline.
  - `configSource` struct (`cmd/config_loader.go:38-44`) and the sequential
    outer loop (`cmd/config_loader.go:127-140`) where init layers are merged via
    `MergeInitLayers` — the reason Approach B (top-level sources) is needed for
    init parity.
- Dependencies identified: `engine` (fresh VM for the pre-pass), `goja`
  (`AssertFunction`, `ExportTo`), `os`/`filepath` (path resolve + stat).

## Development Approach

- **Testing approach: TDD (tests first).** For each task, write failing tests
  first, then implement until green.
- Complete each task fully before moving to the next.
- Make small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** for code changes in
  that task — success and error scenarios both.
- **CRITICAL: all tests must pass before starting the next task.**
- Run tests after each change: `go test ./cmd/...` (full sweep `go test ./...`).
- Maintain backward compatibility: existing config-loading behaviour (default /
  `--before-config` / auto / `--config` / remote) must be unchanged when no
  `getBeforeConfigs()` is present.

## Testing Strategy

- **Unit tests** (required every task):
  - `discoverBeforeConfigs` in isolation (no git root needed — pass a temp
    config path directly, mirroring how `discoverAutoConfig` is tested).
  - `buildConfigSources` source-ordering in isolation (no git root needed).
- **Integration tests**: end-to-end through `loadConfigWithPaths` for parity and
  precedence (reuse the `TestBeforeConfigOrdering` temp-dir harness).
- No UI / e2e in this project — N/A.
- Build note: `go install` does NOT work here (JS embed step); use `go build`.
  Go tests run against the checked-in embedded `internal/config/config.js`.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Update this plan if implementation deviates from scope.

## What Goes Where

- **Implementation Steps** (`[ ]`): Go code, tests, TS declarations, docs in this
  repo.
- **Post-Completion** (no checkboxes): updating the consuming
  `@shibanet0/datamitsu-config` template, manual global-vs-wrapper verification,
  the `--binary-command` follow-up.

## Implementation Steps

### Task 1: `discoverBeforeConfigs` helper + `beforeConfigEntry` type

Pre-pass that loads the auto config in an isolated VM, reads `getBeforeConfigs()`,
and returns resolved absolute paths (declared order, deduped).

- [x] write tests in [cmd/config_loader_test.go](../../cmd/config_loader_test.go)
      for `discoverBeforeConfigs`: returns nil when `getBeforeConfigs` is absent;
      returns nil/empty for `[]`; resolves a relative `path` against the config
      file's dir; uses an absolute `path` as-is; preserves declared order
- [x] write error/edge tests: empty `path` field → error; non-existent file →
      error; duplicate paths deduped to one
- [x] add `beforeConfigEntry struct { Path string `json:"path"` }` in
      [cmd/config_loader.go](../../cmd/config_loader.go)
- [x] implement `discoverBeforeConfigs(autoConfigPath string) ([]string, error)`:
      `engine.New(BinaryCommandOverride)` → `loadConfigFile` → if
      `getBeforeConfigs` is a function, `CallWithTimeout` + `vm.ExportTo` into
      `[]beforeConfigEntry`; resolve each path
      (`filepath.IsAbs` ? as-is : `filepath.Join(filepath.Dir(autoConfigPath), p)`,
      then `filepath.Clean`), `os.Stat` to validate existence, dedup via a `seen`
      set; absent function returns `(nil, nil)`
- [x] run `go test ./cmd/...` — must pass before Task 2

### Task 2: Extract `buildConfigSources` and wire in the pre-pass

Move source-list assembly out of `loadConfigImpl` into a unit-testable function,
and have it insert declared before-configs before the auto source (only when no
`--before-config` flag is set).

- [ ] write tests for `buildConfigSources`: default-only; with `beforeConfigPaths`
      (appear after default, before auto); with `autoConfigPath` whose config
      declares before-configs → declared sources appear **before** the `auto`
      source, in declared order; `configPaths` appended after `auto`
- [ ] write precedence test: when `beforeConfigPaths` is non-empty,
      `getBeforeConfigs()` is **not** consulted (declared before-configs absent
      from the result)
- [ ] implement
      `buildConfigSources(beforeConfigPaths []string, autoConfigPath string, configPaths []string) ([]configSource, error)`
      replicating current ordering (default → before-config flag → [declared
      before-configs, only if `len(beforeConfigPaths)==0`] → auto → config)
- [ ] refactor `loadConfigImpl` to call `buildConfigSources` after git-root /
      `autoConfigPath` discovery (git-root logic stays in `loadConfigImpl`; it
      passes `autoConfigPath=""` when none or `--no-auto-config`)
- [ ] run `go test ./cmd/...` — existing loader tests must stay green; must pass
      before Task 3

### Task 3: Integration — parity with `--before-config` and root-only scope

Validate that a declared before-config produces the same effective config as the
equivalent `--before-config` invocation, and that nesting is ignored.

- [ ] write integration test (temp-dir harness like `TestBeforeConfigOrdering`):
      auto config declares a before-config that defines apps + `init`; assert the
      merged result equals an equivalent `loadConfigWithPaths([before], true, nil)`
      run (apps overridable by auto, init layered identically)
- [ ] write root-only test: a declared before-config that itself exports
      `getBeforeConfigs()` → the nested declaration is ignored (no chaining)
- [ ] write precedence integration test: same auto config run with an explicit
      `--before-config` path → declared before-config is skipped (no double-load)
- [ ] fix any parity gaps surfaced by the above (e.g. init-layer merge, ordering)
- [ ] run `go test ./cmd/...` — must pass before Task 4

### Task 4: TypeScript ambient declarations

- [ ] add `getBeforeConfigs(): { path: string }[]` declaration to
      `config/config.d.ts` (alongside `getRemoteConfigs`)
- [ ] mirror the change into the embedded copy `internal/config/config.d.ts`
      (keep both in sync per `CLAUDE.md`)
- [ ] if a sync test for the embedded `.d.ts` exists, run it; otherwise
      `go build` to confirm embed still compiles
- [ ] run `go test ./...`

### Task 5: Verify acceptance criteria

- [ ] verify all Overview requirements implemented (global == wrapper for config
      loading; root-only; flag overrides; relative+absolute; no hash)
- [ ] verify edge cases: missing file errors clearly; empty/absent
      `getBeforeConfigs`; multiple declared paths; dedup
- [ ] run full test suite `go test ./...`
- [ ] run linter (`golangci-lint run` per repo config) — fix all issues
- [ ] verify coverage of new code meets project standard

### Task 6: Documentation

- [ ] document `getBeforeConfigs()` in the config-loading / architecture docs
      under `website/docs/guides/architecture/` (loading order: default →
      before-config → declared before-config → auto → config → remote)
- [ ] add a short how-to note (wrapper maintenance / global-install usage)
      explaining when to declare `getBeforeConfigs()` vs rely on the wrapper
- [ ] note the precedence rule (CLI `--before-config` overrides the declaration)

## Technical Details

- **New type:** `beforeConfigEntry { Path string `json:"path"` }`.
- **New funcs (in `cmd/config_loader.go`):**
  - `discoverBeforeConfigs(autoConfigPath string) ([]string, error)` — pre-pass
    reader; absent function → `(nil, nil)`.
  - `buildConfigSources(beforeConfigPaths []string, autoConfigPath string, configPaths []string) ([]configSource, error)`
    — single source of truth for source ordering.
- **Loading order (effective):**
  `default → [--before-config flag paths] → [declared before-configs, iff no flag] → auto → [--config paths]`,
  with each source's own `getRemoteConfigs()` resolved under it as today.
- **Path resolution:** absolute → as-is; relative → joined with
  `filepath.Dir(autoConfigPath)` (== git root), then `filepath.Clean`.
- **Validation:** empty `path` → error; missing file → error; duplicates deduped
  (declared order preserved).
- **No `isAuto` flag needed:** scope is enforced structurally — `getBeforeConfigs`
  is read only by the pre-pass, which runs only on `autoConfigPath`.
- **Version check:** declared before-configs are ordinary path sources, so they
  pass through the existing `getMinVersion()` check in `processConfigSource`
  exactly like `--before-config` paths (the shared config already exports it).

## Post-Completion

_Items requiring manual intervention or external systems — informational only._

**External system updates:**

- Update the `@shibanet0/datamitsu-config` root-config template so generated
  project configs emit `getBeforeConfigs()` pointing at
  `./node_modules/@shibanet0/datamitsu-config/datamitsu.config.js`. (Per user
  policy: do not edit datamitsu-config autonomously — propose first.)

**Manual verification:**

- In a real consuming repo, run the **global** `datamitsu` and confirm the
  effective config / generated files match the pnpm-wrapper invocation.
