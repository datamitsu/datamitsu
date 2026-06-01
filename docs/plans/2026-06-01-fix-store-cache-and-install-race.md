# Fix CI Flakiness: Self-Contained uv Store + Pre-Install Plan Tools

## Overview

CI for `datamitsu-config` flakes from **two independent defects in datamitsu**, both
surfacing only under the GitHub Actions store cache and parallel tool execution:

1. **uv apps reference a Python interpreter outside the cached store.** `uv sync`
   installs a managed CPython into uv's default data dir (`~/.local/share/uv/python`),
   while `getUVEnvVars` only redirects `UV_CACHE_DIR` into the app dir. CI caches only
   `~/.cache/datamitsu/store`, so the venv's `.venv/bin/python` symlink dangles after a
   cache restore onto a fresh runner. The "already installed" gate is `os.Stat(binPath)`
   on the wrapper script, which exists → datamitsu trusts the broken venv forever →
   `fork/exec .../.venv/bin/yamllint: no such file or directory` (ENOENT on the missing
   shebang interpreter).

2. **Github-binary installs race under parallelism.** `GetBinaryPath` does lazy
   check-then-download with **no single-flight**, and `moveFile` does `os.Remove(dst)`
   then `os.Rename(src, dst)` (a non-atomic replace that briefly removes `dst`). The lint
   runner executes per-file tasks in parallel, so multiple tasks install the same binary
   (e.g. `yq`) concurrently and one exec lands in the remove/rename window →
   `fork/exec .../.bin/yq/<hash>: no such file or directory`.

**Fixes:**

- Bug #1: redirect uv's managed Python into the store (`UV_PYTHON_INSTALL_DIR`), mirroring
  the Go runtime's `GOPATH`/`GOMODCACHE`/`GOBIN` isolation, + validate the venv interpreter
  resolves at install time only (self-heal a dangling venv instead of trusting it).
- Bug #2: install every tool the plan needs **once, before** parallel execution
  (`EnsureTools(plan.GetToolNames())` between `Planner.Plan` and `Executor.Execute`),
  plus defense-in-depth: single-flight in `GetBinaryPath` and an atomic `moveFile`/`moveDir`.

**Benefits:** the store cache becomes self-contained (portable across runners), datamitsu
self-heals a dangling venv, and binary installs are race-free regardless of how they are
triggered.

## Context (from discovery)

Files/components involved:

- `internal/runtimemanager/uv.go` — `getUVEnvVars` (L16-20), `installUVAppOnce` install gate (L53), `GetUVCommandInfo` env (L207-213)
- `internal/runtimemanager/go.go` — reference pattern: `GOPATH`/`GOMODCACHE`/`GOBIN` under appEnvPath (L85-87)
- `internal/binmanager/binmanager.go` — `GetBinaryPath` lazy install (L423-444), `GetCommandInfo` per-kind dispatch (L449-481), `InstallWithConcurrency` dedup worker-pool pattern to reuse (L331-414)
- `internal/binmanager/download.go` — `moveFile` non-atomic replace (L153-179), `moveDir` (L181-202)
- `internal/runner/runner.go` — `planner.Plan` (L187), `executor.Execute` (L411) — insertion point for the pre-install phase
- `internal/tooling/types.go` — `ExecutionPlan.GetToolNames()` (L66) — the deduped tool set
- `internal/tooling/executor.go` — parallel per-file execution (L211), `BinaryManager` iface (L44-45)
- `internal/runtimemanager/runtimemanager.go` — `GetCommandInfo` installs uv/node/jvm/go via single-flight `InstallXApp` (L432)
- `internal/env/env.go` — `env.GetStorePath()` returns the cached store root

Related patterns found:

- Runtime installs (uv/go/jvm/node) are already coalesced via `rm.appInstall.Do(key,...)` (golang.org/x/sync/singleflight). Only the binary path lacks this guard — replicate it.
- `runtimeManager.GetCommandInfo` already installs runtime apps; only `GetBinaryPath` is lazy+racy. So `EnsureTools` can call `bm.GetCommandInfo(name)` once per distinct tool to install every kind uniformly.
- Binary cache paths are content-addressed (`name/<configHash>`), so "if dst exists, identical content → skip move" is safe.

Dependencies identified:

- `golang.org/x/sync/singleflight` (already used by runtimemanager)
- No new third-party deps required.

## Development Approach

- **Testing approach: TDD (tests first).** For each defect, write a failing test that
  reproduces it (dangling-interpreter venv treated as installed; concurrent binary install
  ENOENT window; missing `UV_PYTHON_INSTALL_DIR`), then implement the fix until green.
- Complete each task fully before moving to the next.
- Make small, focused changes.
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  (success + error/concurrency scenarios), listed as separate checklist items.
- **CRITICAL: all tests must pass before starting the next task.**
- **CRITICAL: update this plan file when scope changes during implementation.**
- Run `go test ./...` after each change; run `golangci-lint run` before finishing.
- Maintain backward compatibility (store layout change is additive; old broken caches simply rebuild).

## Testing Strategy

- **Unit tests**: required for every task.
  - uv: `getUVEnvVars` includes `UV_PYTHON_INSTALL_DIR` under the store; interpreter
    validation rebuilds a dangling venv and skips a healthy one.
  - download: `moveFile`/`moveDir` never expose a missing `dst`; concurrent moves to the
    same dst all succeed; skip-if-exists short-circuits.
  - binmanager: concurrent `GetBinaryPath` for one uninstalled binary triggers exactly one
    download (single-flight); `EnsureTools` installs all distinct tools once (mixed kinds).
  - runner: planning is followed by `EnsureTools(plan.GetToolNames())` before `Execute`.
- **Concurrency tests**: use `go test -race` for the binary install / moveFile tests.
- **E2E**: datamitsu has no UI e2e suite; rely on unit + race tests. Real CI re-run is a
  Post-Completion manual verification.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.
- Keep this plan in sync with actual work.

## What Goes Where

- **Implementation Steps** (`[ ]`): code + tests inside the datamitsu repo.
- **Post-Completion** (no checkboxes): CI re-run, cache verification, and the downstream
  `@datamitsu/datamitsu` bump in `datamitsu-config`.

## Implementation Steps

### Task 1: Isolate uv-managed Python inside the store (`UV_PYTHON_INSTALL_DIR`)

- [x] write test in `internal/runtimemanager/uv_test.go`: `getUVEnvVars` returns `UV_PYTHON_INSTALL_DIR` pointing under `env.GetStorePath()` (shared, not per-app) and still sets `UV_CACHE_DIR` under appEnvPath
- [x] implement in `internal/runtimemanager/uv.go`: add `UV_PYTHON_INSTALL_DIR` = `filepath.Join(env.GetStorePath(), ".uv", "python")` to `getUVEnvVars` (one CPython per version, shared across uv apps, inside the cached store)
- [x] confirm the env is applied at install time (`cmd.Env` in `installUVAppOnce`, L111) and is harmless in `GetUVCommandInfo` env (exec uses the absolute shebang, no env needed)
- [x] write test: install-time env override list includes the new var (table-driven via `buildEnvWithOverrides`)
- [x] run `go test ./internal/runtimemanager/...` — must pass before Task 2

### Task 2: Validate venv interpreter at install time (self-heal dangling Python)

- [x] write test: `installUVAppOnce` treats a venv whose `.venv/bin/python` does not resolve (dangling symlink) as NOT installed and rebuilds it; a healthy venv is skipped (no `uv sync`)
- [x] implement in `internal/runtimemanager/uv.go`: extend the `os.Stat(binPath)` gate (L53) to also resolve the interpreter (`.venv/bin/python` via `os.Stat`/`filepath.EvalSymlinks`); if it dangles, fall through to the existing cleanup + `uv sync` reinstall path — **install path only**, not on every exec
- [x] write test: healthy venv path returns early without invoking uv (use a fake/stubbed uv or assert no rebuild side effects)
- [x] write test: error case — interpreter present but `binPath` missing still triggers reinstall
- [x] run `go test ./internal/runtimemanager/...` — must pass before Task 3

### Task 3: Make `moveFile`/`moveDir` atomic (close the ENOENT window)

- [x] write test in `internal/binmanager/download_test.go`: concurrent `moveFile` of distinct temp sources to the same `dst` (with a reader stat-looping) never observes a missing `dst`; run under `-race`
- [x] write test: `moveFile` is a no-op (returns nil, leaves dst intact) when `dst` already exists (content-addressed path)
- [x] implement in `internal/binmanager/download.go`: in `moveFile` (L153-179) drop the pre-`os.Remove(dst)`; rely on atomic `os.Rename` to replace; add early `if Stat(dst)==nil { return nil }`
- [x] implement: in `moveDir` (L181-202) replace `RemoveAll(dst)`-then-`Rename` with skip-if-exists OR rename-aside→rename-new→remove-old (never leave dst absent)
- [x] write test for `moveDir` skip-if-exists and atomic-swap behavior
- [x] run `go test -race ./internal/binmanager/...` — must pass before Task 4

### Task 4: Single-flight `GetBinaryPath`

- [ ] write test: N concurrent `GetBinaryPath` calls for the same uninstalled binary result in exactly one `download` (count via injected/stubbed downloader); run under `-race`
- [ ] implement in `internal/binmanager/binmanager.go`: add a `singleflight.Group` field to `BinManager`; in `GetBinaryPath` (L423) wrap the `download(name)` call in `group.Do(name, ...)`, re-checking `os.Stat(binPath)` inside the critical section
- [ ] write test: already-installed binary returns immediately without entering the group
- [ ] run `go test -race ./internal/binmanager/...` — must pass before Task 5

### Task 5: Add `BinManager.EnsureTools(ctx, names []string)`

- [ ] write test: `EnsureTools` installs every distinct tool once across mixed kinds (binary + uv/runtime), dedups repeated names, and aggregates errors without aborting the whole set on a single non-fatal failure (match house behavior in `InstallWithConcurrency`)
- [ ] implement in `internal/binmanager/binmanager.go`: `EnsureTools` resolves each distinct name and triggers install once via `GetCommandInfo(name)` (covers binary through `GetBinaryPath`, and uv/node/go/jvm through `runtimeManager.GetCommandInfo` → `InstallXApp`); optional bounded worker pool over **distinct** names (safe — no same-tool concurrency), reusing the `InstallWithConcurrency` pattern
- [ ] write test: unknown tool name surfaces a clear error; empty list is a no-op
- [ ] add `EnsureTools` to the executor's `BinaryManager` interface if the runner accesses it through that abstraction (`internal/tooling/executor.go` L44-45) — keep the interface minimal
- [ ] run `go test -race ./internal/binmanager/...` — must pass before Task 6

### Task 6: Wire the pre-install phase into the runner (before `Execute`)

- [ ] write test in `internal/runner/`: after `planner.Plan(...)`, `EnsureTools(plan.GetToolNames())` is invoked before `executor.Execute(...)` (use a fake binmanager/executor recording call order)
- [ ] implement in `internal/runner/runner.go`: immediately before `sc.executor.Execute(ctx, plan)` (L411), call `sc.binMgr.EnsureTools(ctx, plan.GetToolNames())` and return its error early (respect dry-run: skip actual installs when planning/dry-run)
- [ ] write test: dry-run does NOT install; a failing `EnsureTools` aborts before execution with a clear message
- [ ] run `go test -race ./internal/runner/...` — must pass before Task 7

### Task 7: Verify acceptance criteria

- [ ] verify both Overview defects are covered by reproducing tests (dangling-venv rebuild; concurrent binary install no ENOENT)
- [ ] run full suite `go test ./...` and `go test -race ./...`
- [ ] run `golangci-lint run` — all issues fixed
- [ ] verify coverage on touched packages meets project standard (80%+)
- [ ] confirm store layout change is additive and old broken caches simply rebuild (no migration code needed)

### Task 8: Update documentation

- [ ] update any runtime/store-layout doc (e.g. supply-chain / runtimes guide) to note uv Python now lives under `<store>/.uv/python` so the store cache is self-contained
- [ ] note the new install-phase invariant ("all plan tools are installed before parallel execution") wherever the run lifecycle is documented
- [ ] if a CHANGELOG exists, add entries for both fixes

_Note: ralphex automatically moves completed plans to `docs/plans/completed/`._

## Technical Details

- **uv Python location:** `UV_PYTHON_INSTALL_DIR = <env.GetStorePath()>/.uv/python` (shared
  per-version, not per-app, to avoid duplicating ~50 MB CPython into every uv app dir).
  Cached automatically because it lives under `~/.cache/datamitsu/store`.
- **Interpreter validation:** install-time only — resolve `.venv/bin/python`; dangling →
  reinstall via the existing `cleanupOnError` + `uv sync` path. Exec path unchanged (absolute shebang).
- **Atomic move:** `os.Rename` is atomic on POSIX and replaces an existing dst in place;
  the pre-`os.Remove` is the only thing opening the ENOENT window. Content-addressed paths
  make skip-if-exists correct.
- **Single-flight key:** binary `name` (config hash already encoded in the path).
- **EnsureTools:** iterate `plan.GetToolNames()` (already sorted+deduped), install each once;
  distinct-name concurrency is safe, same-name concurrency cannot occur.

## Post-Completion

_Items requiring manual intervention or external systems — informational only._

**Manual verification:**

- Build the datamitsu binary/image from this branch; in `datamitsu-config` clear the
  `datamitsu-store-*` Actions cache once, run CI to populate a fresh (now self-contained)
  store, then re-run to confirm a **cache-hit** run passes yamllint (interpreter resolves
  from the cached store) and yq runs clean under parallel per-file linting.
- Optionally inspect the cached store contains `.uv/python/<cpython>` so venvs resolve
  without `~/.local/share/uv`.

**External system updates:**

- After a datamitsu release including these fixes, bump `@datamitsu/datamitsu` in
  `datamitsu-config` (`package.json`, `docker/Dockerfile`, `docker/Dockerfile.alpine`).
- Unrelated, tracked separately: `datamitsu-config` still has the un-renamed
  `scripts/sync-fnm-versions-to-package-json.ts` referenced as `sync-node-...` in
  `Taskfile.yaml` (`pull:node` tasks) — fix with a `git mv` in that repo, not part of this plan.
